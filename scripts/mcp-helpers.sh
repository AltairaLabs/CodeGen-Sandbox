# scripts/mcp-helpers.sh — shared MCP-over-SSE plumbing for end-to-end
# scripts. Source-only; not executable. Designed for the e2e + Docker
# integration jobs that exercise a running `bin/sandbox` (or sandbox in a
# container) over the real MCP wire.
#
# Variables the caller must set BEFORE sourcing:
#   ADDR     — host:port the sandbox is listening on (e.g. 127.0.0.1:18091)
#   SSE_FILE — absolute path to write the SSE stream into
#
# Functions exported (all idempotent):
#   mcp_open_session   — opens the SSE stream + initialise MCP session
#   mcp_call           — TOOL ARGS_JSON → echo .result.content[0].text;
#                        fail (return 1) on transport timeout or isError
#   mcp_call_allow_error — same but echoes the text regardless of isError
#   mcp_close_session  — kill background SSE reader (called from trap)
#   say / pass / fail  — coloured progress reporting
#
# The session-id is stashed in $SESSION; the request-id counter is in
# $ID_FILE (created lazily on first mcp_call).

# --- Reporting ------------------------------------------------------

say()  { printf "\n\033[1;34m▶ %s\033[0m\n" "$*" ; return 0 ; }
pass() { printf "   \033[1;32m✓\033[0m %s\n" "$*" ; return 0 ; }
fail() {
  printf "   \033[1;31m✗\033[0m %s\n" "$*" >&2
  exit 1
}

# --- Session lifecycle ---------------------------------------------

# mcp_open_session waits up to ~5s for the sandbox's /sse endpoint to
# accept a connection, opens a long-lived stream into $SSE_FILE, then
# extracts the session-suffixed message endpoint advertised on the first
# `data: /message...` frame. Sets $SESSION + $SSE_PID. Issues the LSP-
# style initialize + initialized handshake. Fails fast (exit 1) on any
# step.
mcp_open_session() {
  : "${ADDR:?mcp_open_session requires ADDR}"
  : "${SSE_FILE:?mcp_open_session requires SSE_FILE}"

  for _ in $(seq 1 20); do
    if curl -sS -o /dev/null --max-time 1 "http://$ADDR/sse" 2>/dev/null; then
      break
    fi
    sleep 0.25
  done

  curl -sS -N --max-time 600 --output "$SSE_FILE" "http://$ADDR/sse" &
  SSE_PID=$!
  for _ in $(seq 1 40); do
    if grep -q '^data: /message' "$SSE_FILE" 2>/dev/null; then break; fi
    sleep 0.2
  done
  SESSION=$(grep -o '^data: /message.*' "$SSE_FILE" | head -1 | sed 's|^data: *||' | tr -d '\r\n ')
  [[ -n "$SESSION" ]] || fail "did not receive endpoint frame"

  curl -sS -X POST "http://$ADDR$SESSION" \
    -H 'Content-Type: application/json' \
    -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"docker-integration","version":"0"},"capabilities":{}}}' \
    >/dev/null
  curl -sS -X POST "http://$ADDR$SESSION" \
    -H 'Content-Type: application/json' \
    -d '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' \
    >/dev/null
}

# mcp_close_session kills the background SSE reader. Safe to call
# multiple times (typical use: from a cleanup trap).
mcp_close_session() {
  if [[ -n "${SSE_PID:-}" ]]; then
    kill "$SSE_PID" 2>/dev/null || true
    SSE_PID=""
  fi
}

# --- Tool calls ----------------------------------------------------

# _mcp_send TOOL ARGS_JSON — fires the JSON-RPC tools/call POST and
# echoes the raw response payload. Internal helper.
_mcp_send() {
  local tool="$1"
  local args_json="$2"
  if [[ -z "${ID_FILE:-}" ]]; then
    ID_FILE=$(mktemp -t codegen-mcp-id.XXXXXX)
    echo 100 > "$ID_FILE"
  fi
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
  for _ in $(seq 1 600); do
    resp=$(grep -o "^data: {\"jsonrpc\":\"2.0\",\"id\":${id},.*" "$SSE_FILE" | tail -1 | sed 's|^data: *||') || true
    if [[ -n "$resp" ]]; then break; fi
    sleep 0.15
  done
  if [[ -z "$resp" ]]; then
    {
      echo "=== mcp_call timeout: tool=$tool id=$id ==="
      tail -30 "$SSE_FILE"
    } >&2
    return 1
  fi
  printf '%s' "$resp"
}

# mcp_call TOOL ARGS_JSON — issues a tools/call and echoes the
# .result.content[0].text. Fails on transport timeout or when the
# result has isError=true.
mcp_call() {
  local resp
  resp=$(_mcp_send "$1" "$2") || return 1
  if printf '%s' "$resp" | jq -e '.result.isError == true' >/dev/null 2>&1; then
    local msg
    msg=$(printf '%s' "$resp" | jq -r '.result.content[0].text // ""')
    echo "tool $1 returned error result: $msg" >&2
    return 1
  fi
  printf '%s' "$resp" | jq -r '.result.content[0].text // ""'
}

# mcp_call_allow_error TOOL ARGS_JSON — same as mcp_call but echoes
# the text regardless of isError. Use for tools whose useful output
# (parsed test failures, exit codes) is on the failure path.
mcp_call_allow_error() {
  local resp
  resp=$(_mcp_send "$1" "$2") || return 1
  printf '%s' "$resp" | jq -r '.result.content[0].text // ""'
}
