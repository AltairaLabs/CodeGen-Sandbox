#!/usr/bin/env bash
#
# End-to-end walkthrough that drives the sandbox over its real MCP HTTP+SSE
# transport via curl+jq, exercising a realistic agent coding loop:
#
#   1. Scaffold a Go module via Bash.
#   2. Read probe.go.
#   3. Edit to introduce an errcheck violation. Expect post-edit lint
#      feedback to flag it inline.
#   4. Edit to fix the violation. Expect no post-edit lint block.
#   5. Edit to break the implementation (swap + to -) so TestAdd fails.
#   6. run_tests. Expect FAIL.
#   7. Edit to restore the implementation.
#   8. run_tests. Expect PASS.
#
# Each step asserts the expected content in the response; any mismatch
# exits non-zero so this doubles as a regression smoke.
#
# Requires: go 1.25+, golangci-lint v2, curl, jq, ripgrep on PATH. Builds
# the sandbox binary into ./bin via `make build` if it's not already there.

set -euo pipefail

REPO_ROOT=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
BIN="$REPO_ROOT/bin/sandbox"
ADDR="127.0.0.1:19876"
WORKSPACE=$(mktemp -d -t codegen-sandbox-e2e.XXXXXX)
SSE_FILE=$(mktemp -t codegen-sandbox-sse.XXXXXX)
SANDBOX_PID=""
SSE_PID=""

cleanup() {
  local ec=$?
  [[ -n "$SSE_PID"     ]] && kill "$SSE_PID"     2>/dev/null || true
  [[ -n "$SANDBOX_PID" ]] && kill "$SANDBOX_PID" 2>/dev/null || true
  rm -rf "$WORKSPACE" "$SSE_FILE" "${ID_FILE:-}"
  if (( ec == 0 )); then
    echo
    echo "✅ e2e demo passed — sandbox tool loop works end-to-end."
  else
    echo
    echo "❌ e2e demo failed (exit $ec)."
  fi
  exit $ec
}
trap cleanup EXIT

say()  { printf "\n\033[1;34m▶ %s\033[0m\n" "$*" ; }
pass() { printf "   \033[1;32m✓\033[0m %s\n" "$*" ; }
fail() { printf "   \033[1;31m✗\033[0m %s\n" "$*"; exit 1 ; }

# --- Build + start sandbox -------------------------------------------

say "Build sandbox binary"
if [[ ! -x "$BIN" ]]; then
  (cd "$REPO_ROOT" && make build >/dev/null)
fi
[[ -x "$BIN" ]] || fail "sandbox binary not built at $BIN"
pass "$BIN"

say "Start sandbox at $ADDR (workspace=$WORKSPACE)"
"$BIN" -addr="$ADDR" -workspace="$WORKSPACE" >/tmp/sandbox-e2e.log 2>&1 &
SANDBOX_PID=$!
# Wait for listen.
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
# Wait for the endpoint frame.
for _ in $(seq 1 20); do
  if grep -q '^data: /message' "$SSE_FILE" 2>/dev/null; then break; fi
  sleep 0.2
done
SESSION=$(grep -o '^data: /message.*' "$SSE_FILE" | head -1 | sed 's|^data: *||' | tr -d '\r\n ')
[[ -n "$SESSION" ]] || fail "did not receive endpoint frame"
pass "session=$SESSION"

curl -sS -X POST "http://$ADDR$SESSION" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"e2e-demo","version":"0"},"capabilities":{}}}' \
  >/dev/null
curl -sS -X POST "http://$ADDR$SESSION" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' \
  >/dev/null
pass "initialize complete"

# --- MCP call helper --------------------------------------------------

ID_FILE=$(mktemp -t codegen-sandbox-id.XXXXXX)
echo 10 > "$ID_FILE"
# mcp_call TOOL ARGS_JSON
#   Returns the tool's text content on stdout. Fails if the tool returned
#   an MCP error result (IsError=true). A file-backed counter tracks the
#   JSON-RPC id across subshells (command substitution $() runs in a
#   subshell and would lose an in-memory counter increment).
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

  # Wait for response matching this id on the SSE stream.
  local resp=""
  for _ in $(seq 1 200); do
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

  # If it's an MCP error result (isError: true), print and fail loudly.
  if printf '%s' "$resp" | jq -e '.result.isError == true' >/dev/null 2>&1; then
    local msg
    msg=$(printf '%s' "$resp" | jq -r '.result.content[0].text // ""')
    echo "tool $tool returned error result: $msg" >&2
    return 1
  fi

  # Normal result — print the first text content block.
  printf '%s' "$resp" | jq -r '.result.content[0].text // ""'
}

# --- 1. Scaffold Go module via Bash ----------------------------------

say "1. Scaffold a Go module in the workspace via Bash"
OUT=$(mcp_call "Bash" '{
  "command": "cat > go.mod <<EOF\nmodule example.com/probe\n\ngo 1.21\nEOF\ncat > probe.go <<EOF\npackage probe\n\nimport \"os\"\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Write() error { return os.WriteFile(\"x\", []byte(\"y\"), 0o644) }\nEOF\ncat > probe_test.go <<EOF\npackage probe\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"Add(1,2) != 3\")\n\t}\n}\nEOF\necho scaffolded",
  "description": "scaffold a Go module with a passing test",
  "timeout": 30
}')
printf '%s\n' "$OUT" | sed 's/^/   /'
grep -q 'exit: 0' <<<"$OUT" 2>/dev/null || ! grep -q 'exit:' <<<"$OUT" || fail "scaffold exited non-zero"
grep -q 'scaffolded' <<<"$OUT" || fail "did not see 'scaffolded' in Bash output"
pass "module scaffolded"

# --- 2. Read probe.go (satisfies the read-tracker for subsequent Edits)

say "2. Read probe.go"
OUT=$(mcp_call "Read" '{"file_path": "probe.go"}')
printf '%s\n' "$OUT" | head -5 | sed 's/^/   /'
grep -q 'func Add' <<<"$OUT" || fail "Read output missing func Add"
grep -q 'func Write() error' <<<"$OUT" || fail "Read output missing func Write"
pass "Read returned numbered lines"

# --- 3. Edit to introduce an errcheck violation; expect post-edit lint

say "3. Edit probe.go to drop the error return — expect post-edit lint to flag it"
OUT=$(mcp_call "Edit" '{
  "file_path": "probe.go",
  "old_string": "func Write() error { return os.WriteFile(\"x\", []byte(\"y\"), 0o644) }",
  "new_string": "func Write() { os.WriteFile(\"x\", []byte(\"y\"), 0o644) }"
}')
printf '%s\n' "$OUT" | sed 's/^/   /'
grep -q 'replaced 1 occurrence' <<<"$OUT" || fail "Edit did not report replacement"
grep -q 'post-edit lint findings' <<<"$OUT" || fail "post-edit lint feedback missing — the critical quality feature"
grep -qE 'errcheck|Error return value' <<<"$OUT" || fail "errcheck finding missing from post-edit feedback"
pass "post-edit lint feedback fired as expected"

# --- 4. Edit to restore the error return; expect NO post-edit lint block

say "4. Edit probe.go back to the clean form — expect no lint block"
OUT=$(mcp_call "Edit" '{
  "file_path": "probe.go",
  "old_string": "func Write() { os.WriteFile(\"x\", []byte(\"y\"), 0o644) }",
  "new_string": "func Write() error { return os.WriteFile(\"x\", []byte(\"y\"), 0o644) }"
}')
printf '%s\n' "$OUT" | sed 's/^/   /'
grep -q 'replaced 1 occurrence' <<<"$OUT" || fail "Edit did not report replacement"
if grep -q 'post-edit lint findings' <<<"$OUT"; then
  fail "post-edit lint block should be absent on a clean edit"
fi
pass "no post-edit block on clean edit"

# --- 5. Break the implementation so TestAdd fails ---------------------

say "5. Edit Add to return a - b (breaks TestAdd)"
OUT=$(mcp_call "Edit" '{
  "file_path": "probe.go",
  "old_string": "func Add(a, b int) int { return a + b }",
  "new_string": "func Add(a, b int) int { return a - b }"
}')
grep -q 'replaced 1 occurrence' <<<"$OUT" || fail "Edit did not report replacement"
pass "Add broken on purpose"

# --- 6. run_tests: expect failure -------------------------------------

say "6. run_tests — expect FAIL"
OUT=$(mcp_call "run_tests" '{"timeout": 120}')
printf '%s\n' "$OUT" | head -10 | sed 's/^/   /'
grep -q 'exit: 0' <<<"$OUT" && fail "tests passed but the implementation was broken"
grep -qE 'FAIL|Add\(1,2\)' <<<"$OUT" || fail "expected test failure output missing"
pass "tests failed as expected"

# --- 7. Restore the implementation -----------------------------------

say "7. Edit Add back to a + b"
OUT=$(mcp_call "Edit" '{
  "file_path": "probe.go",
  "old_string": "func Add(a, b int) int { return a - b }",
  "new_string": "func Add(a, b int) int { return a + b }"
}')
grep -q 'replaced 1 occurrence' <<<"$OUT" || fail "Edit did not report replacement"
pass "Add restored"

# --- 8. run_tests: expect pass ----------------------------------------

say "8. run_tests — expect PASS"
OUT=$(mcp_call "run_tests" '{"timeout": 120}')
printf '%s\n' "$OUT" | head -5 | sed 's/^/   /'
grep -q 'exit: 0' <<<"$OUT" || fail "tests did not exit 0 after restore"
grep -qE 'ok\s+example.com/probe' <<<"$OUT" || fail "expected PASS line missing"
pass "tests passed"
