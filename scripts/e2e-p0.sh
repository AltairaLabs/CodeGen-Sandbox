#!/usr/bin/env bash
#
# End-to-end P0-tool coverage over the real MCP HTTP+SSE wire. Exercises
# every P0 tool shipped through #9/#10/#11/#12/#13 in a realistic chain:
#
#   1. Seed a Go workspace (go.mod, probe.go with Add, probe_test.go).
#   2. Bash "git init" — snapshots need a git repo with at least one commit.
#   3. snapshot_create "baseline"        — captures a reference state.
#   4. snapshot_list                      — confirms baseline is listed.
#   5. search_code "Add"                  — indexes hit the Add function.
#   6. Read probe.go                      — satisfies the read-tracker.
#   7. find_definition on Add             — LSP (gopls) — SKIPs w/o gopls.
#   8. find_references on Add             — LSP.
#   9. rename_symbol Add -> Sum           — LSP (diff only, not applied).
#  10. edit_function_body Add <- "a-b"    — AST-safe breaking edit.
#  11. run_tests                          — expects FAIL.
#  12. last_test_failures                 — structured TestAdd failure.
#  13. snapshot_diff baseline             — shows +- of the body edit.
#  14. snapshot_restore baseline          — reverts the edit.
#  15. run_tests                          — expects PASS again.
#  16. insert_after_method                — seeds a struct; inserts a peer.
#  17. change_function_signature          — adds a param, body preserved.
#
# LSP steps (7-9) run before any edits so gopls sees pristine file state.
# They SKIP cleanly with a warning banner when gopls isn't on $PATH; the
# overall script still exits 0 in that case.
#
# Runtime target: ~60 seconds on a warm Go cache.
# Requires: go 1.25+, curl, jq, ripgrep, git.

set -euo pipefail

REPO_ROOT=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
BIN="$REPO_ROOT/bin/sandbox"
ADDR="127.0.0.1:19877"
WORKSPACE=$(mktemp -d -t codegen-sandbox-p0.XXXXXX)
SSE_FILE=$(mktemp -t codegen-sandbox-sse-p0.XXXXXX)
SANDBOX_PID=""
SSE_PID=""
ID_FILE=""

cleanup() {
  local ec=$?
  [[ -n "$SSE_PID"     ]] && kill "$SSE_PID"     2>/dev/null || true
  [[ -n "$SANDBOX_PID" ]] && kill "$SANDBOX_PID" 2>/dev/null || true
  rm -rf "$WORKSPACE" "$SSE_FILE" "${ID_FILE:-}"
  if (( ec == 0 )); then
    echo
    echo "e2e-p0 passed — P0 tool chain works end-to-end."
  else
    echo
    echo "e2e-p0 FAILED (exit $ec). SSE tail (last 40 lines):"
    tail -40 "$SSE_FILE" 2>/dev/null || true
  fi
  return $ec
}
trap cleanup EXIT

say()  { printf "\n\033[1;34m▶ %s\033[0m\n" "$*" ; return 0 ; }
pass() { printf "   \033[1;32m✓\033[0m %s\n" "$*" ; return 0 ; }
skip() { printf "   \033[1;33m⚠\033[0m %s\n" "$*" ; return 0 ; }
fail() {
  printf "   \033[1;31m✗\033[0m %s\n" "$*" >&2
  exit 1
}

# --- Build + start sandbox -------------------------------------------

say "Build sandbox binary"
if [[ ! -x "$BIN" ]]; then
  (cd "$REPO_ROOT" && make build >/dev/null)
fi
[[ -x "$BIN" ]] || fail "sandbox binary not built at $BIN"
pass "$BIN"

say "Start sandbox at $ADDR (workspace=$WORKSPACE)"
"$BIN" -addr="$ADDR" -workspace="$WORKSPACE" >/tmp/sandbox-e2e-p0.log 2>&1 &
SANDBOX_PID=$!
for _ in $(seq 1 20); do
  if curl -sS -o /dev/null --max-time 1 "http://$ADDR/sse" 2>/dev/null; then
    break
  fi
  sleep 0.25
done
pass "listening (pid $SANDBOX_PID)"

# --- MCP handshake ----------------------------------------------------

say "Open SSE stream + initialize MCP session"
curl -sS -N --max-time 300 --output "$SSE_FILE" "http://$ADDR/sse" &
SSE_PID=$!
for _ in $(seq 1 20); do
  if grep -q '^data: /message' "$SSE_FILE" 2>/dev/null; then break; fi
  sleep 0.2
done
SESSION=$(grep -o '^data: /message.*' "$SSE_FILE" | head -1 | sed 's|^data: *||' | tr -d '\r\n ')
[[ -n "$SESSION" ]] || fail "did not receive endpoint frame"
pass "session=$SESSION"

curl -sS -X POST "http://$ADDR$SESSION" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"e2e-p0","version":"0"},"capabilities":{}}}' \
  >/dev/null
curl -sS -X POST "http://$ADDR$SESSION" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' \
  >/dev/null
pass "initialize complete"

# --- MCP call helper --------------------------------------------------

ID_FILE=$(mktemp -t codegen-sandbox-id-p0.XXXXXX)
echo 100 > "$ID_FILE"
# mcp_call TOOL ARGS_JSON — returns the tool's text content; fails on isError.
mcp_call() {
  local tool="$1"
  local args_json="$2"
  local id
  id=$(cat "$ID_FILE")
  echo $((id + 1)) > "$ID_FILE"
  local body
  body=$(jq -cn --arg t "$tool" --argjson a "$args_json" --argjson id "$id" '{
    jsonrpc: "2.0", id: $id, method: "tools/call",
    params: {name: $t, arguments: $a}
  }')
  curl -sS -X POST "http://$ADDR$SESSION" -H 'Content-Type: application/json' -d "$body" >/dev/null

  local resp=""
  for _ in $(seq 1 400); do
    resp=$(grep -o "^data: {\"jsonrpc\":\"2.0\",\"id\":${id},.*" "$SSE_FILE" | tail -1 | sed 's|^data: *||') || true
    if [[ -n "$resp" ]]; then break; fi
    sleep 0.15
  done
  if [[ -z "$resp" ]]; then
    {
      echo "=== mcp_call timeout: tool=$tool id=$id ==="
      echo "=== SSE file tail (last 30 lines) ==="
      tail -30 "$SSE_FILE"
      echo "=== end ==="
    } >&2
    return 1
  fi

  if printf '%s' "$resp" | jq -e '.result.isError == true' >/dev/null 2>&1; then
    local msg
    msg=$(printf '%s' "$resp" | jq -r '.result.content[0].text // ""')
    echo "tool $tool returned error result: $msg" >&2
    return 1
  fi

  printf '%s' "$resp" | jq -r '.result.content[0].text // ""'
}

# mcp_call_allow_error returns the tool's text output regardless of
# success / error, so callers can assert on "exit: N" failures without
# bubbling them up as bash errors.
mcp_call_allow_error() {
  local tool="$1"
  local args_json="$2"
  local id
  id=$(cat "$ID_FILE")
  echo $((id + 1)) > "$ID_FILE"
  local body
  body=$(jq -cn --arg t "$tool" --argjson a "$args_json" --argjson id "$id" '{
    jsonrpc: "2.0", id: $id, method: "tools/call",
    params: {name: $t, arguments: $a}
  }')
  curl -sS -X POST "http://$ADDR$SESSION" -H 'Content-Type: application/json' -d "$body" >/dev/null

  local resp=""
  for _ in $(seq 1 400); do
    resp=$(grep -o "^data: {\"jsonrpc\":\"2.0\",\"id\":${id},.*" "$SSE_FILE" | tail -1 | sed 's|^data: *||') || true
    if [[ -n "$resp" ]]; then break; fi
    sleep 0.15
  done
  if [[ -z "$resp" ]]; then
    echo "=== mcp_call_allow_error timeout: tool=$tool id=$id ===" >&2
    tail -30 "$SSE_FILE" >&2
    return 1
  fi
  printf '%s' "$resp" | jq -r '.result.content[0].text // ""'
}

# --- 1. Seed workspace + git repo -----------------------------------

say "1. Seed Go workspace + git init + initial commit"
OUT=$(mcp_call "Bash" '{
  "command": "cat > go.mod <<EOF\nmodule example.com/probe\n\ngo 1.21\nEOF\ncat > probe.go <<EOF\npackage probe\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\nEOF\ncat > probe_test.go <<EOF\npackage probe\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"Add(1,2) != 3\")\n\t}\n}\nEOF\ngit init -b main -q\ngit -c user.email=e2e@example.com -c user.name=e2e add -A\ngit -c user.email=e2e@example.com -c user.name=e2e commit -q -m seed\necho seeded",
  "description": "seed a Go module + git repo",
  "timeout": 30
}')
grep -q 'seeded' <<<"$OUT" || fail "scaffold did not print 'seeded': $OUT"
pass "workspace seeded"

# --- 2. snapshot_create ----------------------------------------------

say "2. snapshot_create baseline"
OUT=$(mcp_call "snapshot_create" '{"name":"baseline"}')
grep -q 'snapshot baseline created' <<<"$OUT" || fail "missing 'snapshot baseline created' in: $OUT"
pass "baseline snapshot created"

# --- 3. snapshot_list -------------------------------------------------

say "3. snapshot_list contains baseline"
OUT=$(mcp_call "snapshot_list" '{}')
grep -q '^baseline\b' <<<"$OUT" || fail "baseline missing from snapshot_list: $OUT"
pass "baseline listed"

# --- 4. search_code ---------------------------------------------------

say "4. search_code query=Add returns probe.go / Add"
OUT=$(mcp_call "search_code" '{"query":"Add"}')
grep -q 'probe.go' <<<"$OUT" || fail "probe.go missing from search_code output: $OUT"
grep -qiE 'Add' <<<"$OUT" || fail "Add missing from search_code output: $OUT"
pass "search_code hit Add"

# --- 5. Read probe.go to satisfy the read-tracker ---------------------

say "5. Read probe.go"
OUT=$(mcp_call "Read" '{"file_path":"probe.go"}')
grep -q 'func Add' <<<"$OUT" || fail "Read output missing func Add"
# The Read-prefixed lines look like: "N\tfunc Add(...)". The number on the
# row is the in-file line, which is what gopls needs (1-based).
ADD_LINE=$(grep -E '^\s*[0-9]+\s+func Add' <<<"$OUT" | head -1 | awk '{print $1}')
[[ -n "$ADD_LINE" ]] || fail "could not extract line of func Add"
# Column of the 'A' in `func Add(` — 1-based (f=1 u=2 n=3 c=4 sp=5 A=6).
ADD_COL=6
pass "Read saw func Add at line $ADD_LINE"

# --- 6-8. LSP steps — find_definition / find_references / rename ---

# Run these BEFORE any edits so gopls sees pristine file content. After
# AST edits + snapshot_restore gopls's in-memory state lags the on-disk
# file until its watcher reparses, which makes the rename endpoint flaky
# in a single-shot script.
HAVE_GOPLS=0
if command -v gopls >/dev/null 2>&1; then
  HAVE_GOPLS=1
fi

if (( HAVE_GOPLS == 0 )); then
  skip "gopls not on PATH — skipping find_definition / find_references / rename_symbol"
else
  say "6. find_definition on Add at probe.go:$ADD_LINE:$ADD_COL"
  OUT=$(mcp_call "find_definition" "$(jq -cn --arg f probe.go --argjson l "$ADD_LINE" --argjson c "$ADD_COL" '{file_path:$f,line:$l,col:$c}')")
  grep -q 'probe.go' <<<"$OUT" || fail "find_definition missing probe.go: $OUT"
  grep -qE 'probe.go:[0-9]+' <<<"$OUT" || fail "find_definition missing file:line shape: $OUT"
  pass "definition at probe.go:<line>"

  say "7. find_references on Add"
  OUT=$(mcp_call "find_references" "$(jq -cn --arg f probe.go --argjson l "$ADD_LINE" --argjson c "$ADD_COL" '{file_path:$f,line:$l,col:$c}')")
  grep -q 'probe_test.go' <<<"$OUT" || fail "find_references missing probe_test.go ref: $OUT"
  pass "references include probe_test.go"

  say "8. rename_symbol Add -> Sum"
  OUT=$(mcp_call "rename_symbol" "$(jq -cn --arg f probe.go --argjson l "$ADD_LINE" --argjson c "$ADD_COL" --arg n Sum '{file_path:$f,line:$l,col:$c,new_name:$n}')")
  grep -q 'Rename' <<<"$OUT" || fail "rename_symbol missing header: $OUT"
  PROBE_HITS=$(grep -c '^--- a/probe.go$' <<<"$OUT" || true)
  TEST_HITS=$(grep -c '^--- a/probe_test.go$' <<<"$OUT" || true)
  (( PROBE_HITS >= 1 )) || fail "rename diff missing probe.go block: $OUT"
  (( TEST_HITS  >= 1 )) || fail "rename diff missing probe_test.go block: $OUT"
  grep -q 'Sum' <<<"$OUT" || fail "rename diff missing the new name 'Sum': $OUT"
  pass "rename_symbol diff touches probe.go + probe_test.go"
fi

# --- 9. edit_function_body — break Add ------------------------------

say "9. edit_function_body Add <- 'return a - b'"
OUT=$(mcp_call "edit_function_body" '{"file_path":"probe.go","function_name":"Add","new_body":"return a - b"}')
grep -q 'edit_function_body: modified' <<<"$OUT" || fail "edit_function_body missing confirmation: $OUT"
pass "body replaced"

# --- 10. run_tests — expect FAIL -------------------------------------

say "10. run_tests (expect FAIL)"
OUT=$(mcp_call_allow_error "run_tests" '{"timeout":120}')
grep -q 'exit: 0' <<<"$OUT" && fail "tests passed but Add was broken"
grep -qE 'FAIL|Add\(1,2\)' <<<"$OUT" || fail "expected failure output missing: $OUT"
pass "tests failed as intended"

# --- 11. last_test_failures ------------------------------------------

say "11. last_test_failures (structured TestAdd)"
OUT=$(mcp_call "last_test_failures" '{}')
grep -q 'test failure(s)' <<<"$OUT" || fail "last_test_failures missing failure header: $OUT"
grep -q 'TestAdd' <<<"$OUT" || fail "TestAdd missing from structured failures: $OUT"
pass "structured failure surfaced TestAdd"

# --- 12. snapshot_diff — shows +- of body change ---------------------

say "12. snapshot_diff baseline"
OUT=$(mcp_call "snapshot_diff" '{"name":"baseline"}')
grep -q 'return a + b' <<<"$OUT" || fail "diff missing original 'return a + b': $OUT"
grep -q 'return a - b' <<<"$OUT" || fail "diff missing new 'return a - b': $OUT"
pass "snapshot_diff shows the +-"

# --- 13. snapshot_restore ------------------------------------------

say "13. snapshot_restore baseline"
OUT=$(mcp_call "snapshot_restore" '{"name":"baseline"}')
grep -q 'restored snapshot baseline' <<<"$OUT" || fail "restore confirmation missing: $OUT"
pass "baseline restored"

# --- 14. run_tests — expect PASS after restore ---------------------

say "14. run_tests after restore (expect PASS)"
OUT=$(mcp_call "run_tests" '{"timeout":120}')
grep -q 'exit: 0' <<<"$OUT" || fail "tests not green after restore: $OUT"
pass "tests green again"

# --- 15. insert_after_method ---------------------------------------

say "15. insert_after_method — seed a struct + one method, insert peer"
OUT=$(mcp_call "Bash" '{
  "command": "cat > server.go <<EOF\npackage probe\n\ntype Server struct{}\n\nfunc (s *Server) Alpha() string { return \"alpha\" }\nEOF\necho seeded-server",
  "description": "seed a struct with one method",
  "timeout": 10
}')
grep -q 'seeded-server' <<<"$OUT" || fail "struct seed did not complete: $OUT"
mcp_call "Read" '{"file_path":"server.go"}' >/dev/null

OUT=$(mcp_call "insert_after_method" '{
  "file_path":"server.go",
  "receiver_type":"Server",
  "method_name":"Alpha",
  "new_method":"func (s *Server) Beta() string { return \"beta\" }"
}')
grep -q 'insert_after_method' <<<"$OUT" || fail "insert_after_method missing confirmation: $OUT"

VERIFY=$(mcp_call "Read" '{"file_path":"server.go"}')
grep -q 'func (s \*Server) Beta' <<<"$VERIFY" || fail "Beta not found in server.go after insert: $VERIFY"
pass "Beta landed after Alpha"

# --- 16. change_function_signature ---------------------------------

say "16. change_function_signature Alpha — add an n int param"
mcp_call "Read" '{"file_path":"server.go"}' >/dev/null
OUT=$(mcp_call "change_function_signature" '{
  "file_path":"server.go",
  "function_name":"(*Server).Alpha",
  "new_signature":"func (s *Server) Alpha(n int) string"
}')
grep -q 'change_function_signature' <<<"$OUT" || fail "change_function_signature missing confirmation: $OUT"

VERIFY=$(mcp_call "Read" '{"file_path":"server.go"}')
grep -q 'func (s \*Server) Alpha(n int) string' <<<"$VERIFY" || fail "new signature not in server.go: $VERIFY"
grep -q 'return "alpha"' <<<"$VERIFY" || fail "body 'return \"alpha\"' not preserved in: $VERIFY"
pass "signature changed, body preserved"
