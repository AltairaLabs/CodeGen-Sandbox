#!/usr/bin/env bash
#
# End-to-end multi-workspace dispatch smoke (#60). Boots `bin/sandbox`
# with `-workspaces=primary=A,extension=B` and asserts every tool that
# accepts the `workspace` arg dispatches to the correct root, rejects
# the no-hint case with an actionable error, and rejects unknown-name
# hints. The read-tracker invariant — one workspace's Read does NOT
# unlock another's Write — is verified at the end.
#
# This covers the class of regression that unit tests can't catch:
#   - `Read` resolving `file_path` against workspace A when workspace B
#     was requested.
#   - `Bash` cmd.Dir pointing at the wrong workspace.
#   - The read-tracker spilling from one workspace into the other.
#
# Runtime target: ~10 seconds on a warm cache.
# Requires: go 1.25+, bash, curl, jq. Compatible with the `go` +
# `integration` CI jobs' toolchains (no ripgrep / gopls needed here).

set -euo pipefail

REPO_ROOT=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
BIN="$REPO_ROOT/bin/sandbox"
ADDR="127.0.0.1:19878"
PRIMARY=$(mktemp -d -t codegen-mw-primary.XXXXXX)
EXTENSION=$(mktemp -d -t codegen-mw-extension.XXXXXX)
SSE_FILE=$(mktemp -t codegen-mw-sse.XXXXXX)
SANDBOX_PID=""
SSE_PID=""
ID_FILE=""

cleanup() {
  local ec=$?
  [[ -n "$SSE_PID"     ]] && kill "$SSE_PID"     2>/dev/null || true
  [[ -n "$SANDBOX_PID" ]] && kill "$SANDBOX_PID" 2>/dev/null || true
  rm -rf "$PRIMARY" "$EXTENSION" "$SSE_FILE" "${ID_FILE:-}"
  if (( ec == 0 )); then
    echo
    echo "e2e-multi-workspace passed — dispatch + tracker isolation works end-to-end."
  else
    echo
    echo "e2e-multi-workspace FAILED (exit $ec). SSE tail (last 40 lines):"
    tail -40 "$SSE_FILE" 2>/dev/null || true
  fi
  return $ec
}
trap cleanup EXIT

say()  { printf "\n\033[1;34m▶ %s\033[0m\n" "$*" ; return 0 ; }
pass() { printf "   \033[1;32m✓\033[0m %s\n" "$*" ; return 0 ; }
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

say "Seed primary workspace (Go) + extension workspace (Node)"
cat > "$PRIMARY/go.mod" <<'EOF'
module example.com/primary

go 1.21
EOF
cat > "$PRIMARY/probe.go" <<'EOF'
package primary

func Add(a, b int) int { return a + b }
EOF
echo "marker-primary" > "$PRIMARY/MARKER"
# LOCK_TEST is read/written ONLY in the read-tracker isolation test (step 7).
# Keeping it untouched by earlier tests means the tracker hasn't seen either
# absolute path until the test exercises it explicitly.
echo "lock-primary" > "$PRIMARY/LOCK_TEST"

cat > "$EXTENSION/package.json" <<'EOF'
{
  "name": "extension",
  "version": "0.0.0",
  "private": true
}
EOF
echo "marker-extension" > "$EXTENSION/MARKER"
echo "lock-extension" > "$EXTENSION/LOCK_TEST"
pass "primary + extension seeded"

say "Start sandbox at $ADDR with two workspaces"
"$BIN" -addr="$ADDR" -workspaces="primary=$PRIMARY,extension=$EXTENSION" \
  >/tmp/sandbox-e2e-mw.log 2>&1 &
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
curl -sS -N --max-time 120 --output "$SSE_FILE" "http://$ADDR/sse" &
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
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"e2e-mw","version":"0"},"capabilities":{}}}' \
  >/dev/null
curl -sS -X POST "http://$ADDR$SESSION" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' \
  >/dev/null
pass "initialize complete"

# --- MCP helpers ------------------------------------------------------

ID_FILE=$(mktemp -t codegen-mw-id.XXXXXX)
echo 100 > "$ID_FILE"

# mcp_raw TOOL ARGS_JSON — returns the raw JSON-RPC response.
mcp_raw() {
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
      echo "=== mcp_raw timeout: tool=$tool id=$id ==="
      tail -30 "$SSE_FILE"
    } >&2
    return 1
  fi
  printf '%s' "$resp"
}

# mcp_text TOOL ARGS_JSON — returns the tool's text content; fails on isError.
mcp_text() {
  local resp
  resp=$(mcp_raw "$1" "$2") || return 1
  if printf '%s' "$resp" | jq -e '.result.isError == true' >/dev/null 2>&1; then
    local msg
    msg=$(printf '%s' "$resp" | jq -r '.result.content[0].text // ""')
    echo "tool $1 returned error result: $msg" >&2
    return 1
  fi
  printf '%s' "$resp" | jq -r '.result.content[0].text // ""'
}

# mcp_error TOOL ARGS_JSON — returns the error text; fails on success.
mcp_error() {
  local resp
  resp=$(mcp_raw "$1" "$2") || return 1
  if ! printf '%s' "$resp" | jq -e '.result.isError == true' >/dev/null 2>&1; then
    echo "tool $1 unexpectedly succeeded (expected isError=true)" >&2
    printf '%s' "$resp" >&2
    return 1
  fi
  printf '%s' "$resp" | jq -r '.result.content[0].text // ""'
}

# --- Dispatch assertions ---------------------------------------------

say "1. Read dispatches by workspace"
OUT=$(mcp_text "Read" '{"workspace":"primary","file_path":"MARKER"}')
grep -q 'marker-primary' <<<"$OUT" || fail "Read primary MARKER did not return primary content: $OUT"
OUT=$(mcp_text "Read" '{"workspace":"extension","file_path":"MARKER"}')
grep -q 'marker-extension' <<<"$OUT" || fail "Read extension MARKER did not return extension content: $OUT"
pass "Read primary ≠ Read extension"

say "2. Read without workspace in multi-workspace mode is an actionable error"
ERR=$(mcp_error "Read" '{"file_path":"MARKER"}')
grep -q 'multi-workspace sandbox' <<<"$ERR" || fail "expected 'multi-workspace sandbox' in error: $ERR"
grep -q 'pass `workspace`' <<<"$ERR" || fail "expected 'pass \`workspace\`' in error: $ERR"
grep -q 'primary' <<<"$ERR" || fail "expected 'primary' in configured-names list: $ERR"
grep -q 'extension' <<<"$ERR" || fail "expected 'extension' in configured-names list: $ERR"
pass "missing workspace → actionable error"

say "3. Read with unknown workspace name errors cleanly"
ERR=$(mcp_error "Read" '{"workspace":"bogus","file_path":"MARKER"}')
grep -q 'unknown workspace "bogus"' <<<"$ERR" || fail "expected 'unknown workspace \"bogus\"': $ERR"
grep -q 'primary' <<<"$ERR" || fail "configured list missing primary: $ERR"
grep -q 'extension' <<<"$ERR" || fail "configured list missing extension: $ERR"
pass "unknown workspace → 'unknown workspace \"bogus\"'"

say "4. Glob dispatches by workspace"
OUT=$(mcp_text "Glob" '{"workspace":"primary","pattern":"*.go"}')
grep -q 'probe.go' <<<"$OUT" || fail "primary Glob *.go missing probe.go: $OUT"
OUT=$(mcp_text "Glob" '{"workspace":"extension","pattern":"*.go"}')
# Extension has no .go files — successful call with empty-or-"no matches"
# body is acceptable. The critical assertion is that probe.go (which lives
# in the primary workspace only) is NOT in the extension's response.
if grep -q 'probe.go' <<<"$OUT"; then
  fail "extension Glob unexpectedly saw primary's probe.go: $OUT"
fi
pass "Glob primary sees probe.go, extension does not"

say "5. Bash dispatches by workspace (cmd.Dir)"
OUT=$(mcp_text "Bash" '{"workspace":"primary","command":"pwd && cat MARKER","description":"probe primary cwd","timeout":5}')
grep -q 'marker-primary' <<<"$OUT" || fail "primary Bash did not see primary MARKER: $OUT"
# The resolved pwd must be the primary workspace root — some mount setups
# canonicalise via /private/... on macOS, so check the suffix rather than
# an exact string match.
printf '%s' "$OUT" | grep -qE "$(basename "$PRIMARY")\$|$(basename "$PRIMARY")$" || \
  printf '%s' "$OUT" | grep -q "$PRIMARY" || \
  fail "primary Bash pwd did not match primary tempdir: $OUT (want suffix $PRIMARY)"

OUT=$(mcp_text "Bash" '{"workspace":"extension","command":"pwd && cat MARKER","description":"probe extension cwd","timeout":5}')
grep -q 'marker-extension' <<<"$OUT" || fail "extension Bash did not see extension MARKER: $OUT"
pass "Bash cwd differs per workspace"

say "6. Grep dispatches by workspace"
OUT=$(mcp_text "Grep" '{"workspace":"primary","pattern":"marker-"}')
grep -q 'marker-primary' <<<"$OUT" || fail "primary Grep missing marker-primary: $OUT"
if grep -q 'marker-extension' <<<"$OUT"; then
  fail "primary Grep unexpectedly saw extension content: $OUT"
fi
OUT=$(mcp_text "Grep" '{"workspace":"extension","pattern":"marker-"}')
grep -q 'marker-extension' <<<"$OUT" || fail "extension Grep missing marker-extension: $OUT"
if grep -q 'marker-primary' <<<"$OUT"; then
  fail "extension Grep unexpectedly saw primary content: $OUT"
fi
pass "Grep stays inside its workspace"

# --- Read-tracker isolation ------------------------------------------

say "7. Read-tracker isolation: Read(primary, LOCK_TEST) does NOT unlock Write(extension, LOCK_TEST)"
# LOCK_TEST has not been read in either workspace yet. Reading the primary
# copy marks ONLY its absolute path. Because the extension copy lives at a
# different absolute path, a Write to extension/LOCK_TEST must still trip
# the "refusing to overwrite: Read it first" guard. If the tracker
# accidentally keyed on filename or normalised across roots, this call
# would succeed and silently overwrite the extension file.
_=$(mcp_text "Read" '{"workspace":"primary","file_path":"LOCK_TEST"}')
ERR=$(mcp_error "Write" '{"workspace":"extension","file_path":"LOCK_TEST","content":"hijacked"}')
grep -q 'refusing to overwrite' <<<"$ERR" || fail "tracker leaked across workspaces: $ERR"
# Sanity: after a matching Read, the Write does succeed.
_=$(mcp_text "Read" '{"workspace":"extension","file_path":"LOCK_TEST"}')
OUT=$(mcp_text "Write" '{"workspace":"extension","file_path":"LOCK_TEST","content":"updated-extension"}')
grep -qE 'wrote [0-9]+ bytes' <<<"$OUT" || fail "Write after matching Read did not confirm success: $OUT"
# Verify the overwrite took effect and did NOT touch primary/LOCK_TEST.
[[ "$(cat "$EXTENSION/LOCK_TEST")" == "updated-extension" ]] || fail "extension/LOCK_TEST not updated on disk"
[[ "$(cat "$PRIMARY/LOCK_TEST")" == "lock-primary" ]]       || fail "primary/LOCK_TEST corrupted by extension Write"
pass "tracker is per-absolute-path, no cross-workspace leak"

say "8. Unknown-workspace hint is rejected on a write-path tool too"
ERR=$(mcp_error "Write" '{"workspace":"bogus","file_path":"NEWFILE","content":"x"}')
grep -q 'unknown workspace "bogus"' <<<"$ERR" || fail "Write unknown workspace wrong error: $ERR"
pass "Write unknown workspace → 'unknown workspace \"bogus\"'"
