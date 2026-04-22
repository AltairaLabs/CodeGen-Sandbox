# Codegen Sandbox — Bash Foreground + Command Denylist Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a foreground-only `Bash` MCP tool that runs shell commands via `bash -c` inside the workspace, with a timeout, an output cap, and a minimal command denylist as defense-in-depth.

**Architecture:** `HandleBash` invokes `bash -c <command>` via `exec.CommandContext` from the workspace root with stdin silenced. stdout+stderr are merged via `cmd.CombinedOutput()`, capped at 100 KiB with a truncation marker, and returned along with the exit code. A small regex-based `denyReason` helper rejects obvious footgun commands (`sudo`, `shutdown`, `mkfs`, etc.) before they run; this is a defense-in-depth layer — the container itself remains the real trust boundary.

**Tech Stack:** Go 1.25+, `github.com/mark3labs/mcp-go` v0.49.0, `bash` binary on `PATH`, `github.com/stretchr/testify`.

**Prerequisite (operator):** `bash` must be on `PATH` on any machine that runs the test suite or the built binary. macOS and Debian-family Linux ship bash by default. Alpine requires `apk add bash` (Plan 6's Dockerfile will pin this). Tests that invoke Bash use a `requireBash(t)` helper that skips if the binary is missing.

**Out of scope for this plan:**
- Background execution, `BashOutput`, `KillShell` (Plan 7)
- URL-scheme filtering / WebFetch / WebSearch (Plan 8)
- Secret scrubbing of Bash output (Plan 5 — this plan returns raw bash output)
- Per-tool `run_tests` / `run_lint` / `run_typecheck` (Plan 4 — those shell out to different tools and have structured parsers)
- Fine-grained sandboxing beyond the container (gVisor, seccomp, capability drops)

**Denylist scope:** Intentionally minimal for v1. The sandbox container is the real trust boundary; the MCP-layer denylist catches accidents and obvious attempts, not determined attackers. Documented limitations:
- The regex matches at plausible command positions only (start-of-string, after `;`, `&`, `|`, `(`). Denied tokens inside double-quoted strings (e.g., `bash -c "sudo ..."`) are caught only if the quote character is in the boundary class — which we do NOT include, to avoid false positives on `echo "don't sudo"`. Determined attackers can obfuscate trivially (e.g. `$(echo su)do`). This is acceptable for defense-in-depth.
- The denylist is the same for every caller; there is no per-session override.

---

## File Structure

Files introduced by this plan:

| Path | Responsibility |
|---|---|
| `internal/tools/bash.go` | `RegisterBash`, `HandleBash`, `truncateOutput`, `denyReason`, `denyPattern`. Owns the exec wiring (cwd, stdin, timeout) and the denylist check. |
| `internal/tools/bash_test.go` | Black-box tests: simple run, non-zero exit, stderr capture, timeout, output cap, missing params, denylist cases. Plus a `requireBash(t)` helper. |
| `internal/server/server.go` | One additional registration line (`tools.RegisterBash`). |

Design rule carried over from earlier plans: `bash.go` contains only handler + helpers scoped to this tool. The denylist data (`denyPattern`) lives next to the code that uses it; if a second tool ever needs it we can extract, but until then DRY isn't worth the cost.

---

## Task 1: Bash foreground execution

**Files:**
- Create: `internal/tools/bash.go`
- Test: `internal/tools/bash_test.go`

**Tool contract:**
- Name: `Bash`
- Parameters:
  - `command` (string, required) — shell command to run. Passed to `bash -c`.
  - `description` (string, required) — 5-10 word description of what the command does. Recorded for agent context; does not affect execution.
  - `timeout` (number, optional, default 120, clamped to max 600) — timeout in seconds.
- Behavior:
  - Missing `command` or `description` → error.
  - Spawn `bash -c <command>` with:
    - `cmd.Dir` = workspace root
    - `cmd.Stdin = nil` (bash sees closed stdin)
    - Environment inherits the server process env — the container runtime controls what's exposed.
    - `context.WithTimeout(ctx, timeout)` wraps the parent ctx.
  - Collect combined stdout+stderr with `cmd.CombinedOutput()`.
  - Cap output at 100 KiB. If truncated, append a marker.
  - Emit a text result of the form:
    - Just the captured output (newline-terminated), if exit 0 and not timed out.
    - Output plus a trailing `exit: <N>` line if non-zero exit.
    - Output plus a trailing `bash: timed out after <N>s` line and `exit: <N>` line if timed out.
- The denylist is NOT part of Task 1. Task 2 adds it.

**Step 1: Write the failing Bash tests**

Create `internal/tools/bash_test.go`:
```go
package tools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found on PATH; skipping")
	}
}

func callBash(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleBash(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestBash_SimpleCommandReturnsStdout(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)

	res := callBash(t, deps, map[string]any{
		"command":     "echo hello-bash",
		"description": "print greeting",
	})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "hello-bash")
	assert.NotContains(t, body, "exit:", "exit line should be absent on exit 0")
}

func TestBash_RunsInWorkspaceRoot(t *testing.T) {
	requireBash(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "marker.txt"), []byte("x"), 0o644))

	// If cwd is the workspace root, `ls marker.txt` succeeds and prints the
	// file name. If cwd is anywhere else, ls returns a no-such-file error on
	// stderr, which would make the test fail — more robust than comparing pwd
	// output, which can diverge between logical and physical paths on macOS.
	res := callBash(t, deps, map[string]any{
		"command":     "ls marker.txt",
		"description": "confirm cwd is workspace root",
	})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "marker.txt")
	assert.NotContains(t, body, "No such file")
}

func TestBash_NonZeroExitReportsCode(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)

	res := callBash(t, deps, map[string]any{
		"command":     "exit 7",
		"description": "fail with code 7",
	})
	require.False(t, res.IsError, "non-zero exit must still be a success result; the exit line conveys the status")
	body := textOf(t, res)
	assert.Contains(t, body, "exit: 7")
}

func TestBash_StderrIsCaptured(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)

	res := callBash(t, deps, map[string]any{
		"command":     "echo oops 1>&2",
		"description": "emit to stderr",
	})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "oops")
}

func TestBash_TimeoutReportsTimedOut(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)

	res := callBash(t, deps, map[string]any{
		"command":     "sleep 5",
		"description": "sleep long enough to time out",
		"timeout":     float64(1),
	})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "timed out after 1s")
}

func TestBash_OutputIsCapped(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)

	// Emit ~200 KiB of output; the cap is 100 KiB.
	res := callBash(t, deps, map[string]any{
		"command":     "head -c 204800 /dev/zero | base64",
		"description": "generate large output",
	})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "output truncated")
	// Allow some overhead for the truncation marker.
	assert.Less(t, len(body), 110*1024, "body should be close to the 100 KiB cap plus marker")
}

func TestBash_MissingCommand(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)
	res := callBash(t, deps, map[string]any{"description": "irrelevant"})
	assert.True(t, res.IsError)
}

func TestBash_MissingDescription(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)
	res := callBash(t, deps, map[string]any{"command": "true"})
	assert.True(t, res.IsError)
}

func TestBash_TimeoutClampedToMax(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)

	// 9999 is clamped to 600; we can't easily assert the clamp directly, but
	// we can at least confirm the command doesn't error on the oversized
	// timeout value.
	res := callBash(t, deps, map[string]any{
		"command":     "true",
		"description": "no-op",
		"timeout":     float64(9999),
	})
	require.False(t, res.IsError)
	assert.NotContains(t, strings.ToLower(textOf(t, res)), "timed out")
}
```

Do NOT redeclare `newTestDeps` / `textOf`; they exist in `read_test.go` in the same `tools_test` package.

**Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run TestBash
```
Expected: compile error — `tools.HandleBash` undefined.

**Step 3: Implement `Bash`**

Create `internal/tools/bash.go`:
```go
package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const (
	defaultBashTimeoutSec = 120
	maxBashTimeoutSec     = 600
	bashOutputCapBytes    = 100 * 1024
)

// RegisterBash registers the Bash tool on the given MCP server.
func RegisterBash(s *mcpserver.MCPServer, deps *Deps) {
	tool := mcp.NewTool("Bash",
		mcp.WithDescription("Run a shell command in the workspace via bash -c. Returns combined stdout+stderr. A trailing 'exit: N' line is emitted for non-zero exits. A 'timed out after Ns' marker is emitted on timeout."),
		mcp.WithString("command", mcp.Required(), mcp.Description("Shell command to run.")),
		mcp.WithString("description", mcp.Required(), mcp.Description("5-10 word description of what this command does. Recorded for agent context; does not affect execution.")),
		mcp.WithNumber("timeout", mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.", defaultBashTimeoutSec, maxBashTimeoutSec))),
	)
	s.AddTool(tool, HandleBash(deps))
}

// HandleBash returns the Bash tool handler.
func HandleBash(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		command, _ := args["command"].(string)
		if command == "" {
			return ErrorResult("command is required"), nil
		}
		// description is required by the schema but has no execution effect;
		// it exists so agent-context inspection of the MCP request log shows
		// human-readable intent for each Bash call.
		if desc, _ := args["description"].(string); desc == "" {
			return ErrorResult("description is required"), nil
		}

		timeoutSec := defaultBashTimeoutSec
		if v, ok := args["timeout"].(float64); ok && int(v) > 0 {
			timeoutSec = int(v)
			if timeoutSec > maxBashTimeoutSec {
				timeoutSec = maxBashTimeoutSec
			}
		}

		execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()

		cmd := exec.CommandContext(execCtx, "bash", "-c", command)
		cmd.Dir = deps.Workspace.Root()
		cmd.Stdin = nil

		out, runErr := cmd.CombinedOutput()

		timedOut := errors.Is(execCtx.Err(), context.DeadlineExceeded)
		exitCode := 0
		if runErr != nil {
			var exitErr *exec.ExitError
			switch {
			case errors.As(runErr, &exitErr):
				exitCode = exitErr.ExitCode()
			case timedOut:
				exitCode = -1
			default:
				return ErrorResult("bash: %v", runErr), nil
			}
		}

		body := truncateOutput(out, bashOutputCapBytes)

		var sb strings.Builder
		sb.Write(body)
		if len(body) > 0 && !bytes.HasSuffix(body, []byte("\n")) {
			sb.WriteByte('\n')
		}
		if timedOut {
			fmt.Fprintf(&sb, "bash: timed out after %ds\n", timeoutSec)
		}
		if exitCode != 0 || timedOut {
			fmt.Fprintf(&sb, "exit: %d\n", exitCode)
		}
		return TextResult(sb.String()), nil
	}
}

// truncateOutput caps b at limit bytes, appending a marker when truncated.
func truncateOutput(b []byte, limit int) []byte {
	if len(b) <= limit {
		return b
	}
	trunc := make([]byte, 0, limit+64)
	trunc = append(trunc, b[:limit]...)
	trunc = append(trunc, fmt.Appendf(nil, "\n... (output truncated, %d bytes elided)", len(b)-limit)...)
	return trunc
}
```

*Note on description:* `description` is required by the schema but has no effect on execution — it's recorded in the MCP request log so agent-context inspection is easier.

**Step 4: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run TestBash -v
```
Expected: all 9 Bash tests PASS.

**Step 5: Lint + full suite**

```bash
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox lint
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./... -race -count=1
```
Both must exit 0.

**Step 6: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/tools/bash.go internal/tools/bash_test.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(tools): add Bash tool with timeout, output cap, and combined stdout+stderr
EOF
```

---

## Task 2: Command denylist

**Files:**
- Modify: `internal/tools/bash.go` — add `denyPattern`, `denyReason`, and call it from `HandleBash`.
- Modify: `internal/tools/bash_test.go` — add denylist tests.

**Denylist scope (verbatim):**
- `sudo`, `su` — privilege escalation (unavailable in Docker image anyway; fail loudly if invoked)
- `shutdown`, `reboot`, `halt`, `poweroff` — host / container shutdown
- `chroot` — sandbox escape (not possible as non-root, but fail loudly)
- `mount`, `umount` — filesystem manipulation
- `mkfs` (and variants like `mkfs.ext4`, `mkfs.xfs`) — filesystem formatting

Matching rules (intentional limits documented in the plan header):
- Match at plausible command positions: start-of-string or after one of `\s`, `;`, `&`, `|`, `(`.
- Terminated by end-of-string or one of `\s`, `;`, `&`, `|`, `)`.
- Quoted subcommands (`bash -c "sudo ..."`) are NOT caught; documented limitation.
- Determined attackers can trivially bypass (`$(echo su)do`). Acceptable — the container is the real boundary.

**Step 1: Write the failing denylist tests**

Append to `internal/tools/bash_test.go`:
```go
func TestBashDeny_SudoRejected(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)
	res := callBash(t, deps, map[string]any{
		"command":     "sudo whoami",
		"description": "try sudo",
	})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "sudo")
}

func TestBashDeny_SudoAfterPipeRejected(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)
	res := callBash(t, deps, map[string]any{
		"command":     "echo x && sudo ls",
		"description": "sudo after &&",
	})
	assert.True(t, res.IsError)
}

func TestBashDeny_MkfsVariantRejected(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)
	res := callBash(t, deps, map[string]any{
		"command":     "mkfs.ext4 /dev/null",
		"description": "format ext4",
	})
	assert.True(t, res.IsError)
}

func TestBashDeny_ShutdownRejected(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)
	res := callBash(t, deps, map[string]any{
		"command":     "shutdown -h now",
		"description": "shutdown the host",
	})
	assert.True(t, res.IsError)
}

func TestBashDeny_FalsePositiveAvoided_FilenameContainsSu(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)
	// /etc/sudoers as a filename must NOT trigger the sudo-at-command-position check.
	res := callBash(t, deps, map[string]any{
		"command":     "ls /etc/sudoers 2>/dev/null; true",
		"description": "list sudoers path (should not trigger the deny regex)",
	})
	require.False(t, res.IsError, "unexpected deny: %s", textOf(t, res))
}

func TestBashDeny_FalsePositiveAvoided_WordPseudo(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)
	res := callBash(t, deps, map[string]any{
		"command":     "echo pseudo-random",
		"description": "echo word containing su",
	})
	require.False(t, res.IsError, "unexpected deny: %s", textOf(t, res))
}

func TestBashDeny_MountRejected(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)
	res := callBash(t, deps, map[string]any{
		"command":     "mount -t tmpfs none /mnt",
		"description": "mount tmpfs",
	})
	assert.True(t, res.IsError)
}

func TestBashDeny_ChrootRejected(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)
	res := callBash(t, deps, map[string]any{
		"command":     "chroot /tmp/jail /bin/sh",
		"description": "chroot attempt",
	})
	assert.True(t, res.IsError)
}
```

**Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run TestBashDeny
```
Expected: `TestBashDeny_*` tests fail — the commands currently run (and some exit non-zero, which the test asserts is ALSO a success result from Task 1).

**Step 3: Implement the denylist**

Append to `internal/tools/bash.go`:
```go
import (
	// ... existing imports ...
	"regexp"
)

// denyPattern matches denylisted command tokens at plausible command positions.
// This is a defense-in-depth layer: the container is the real trust boundary.
// Quoted subcommands (e.g. bash -c "sudo ...") are intentionally NOT caught
// to avoid false positives on echo/printf of the same tokens.
var denyPattern = regexp.MustCompile(
	`(?:^|[\s;&|(])\s*(sudo|su|shutdown|reboot|halt|poweroff|chroot|mount|umount|mkfs(?:\.\w+)?)(?:$|[\s;&|)])`,
)

// denyReason returns a non-empty reason string if command matches the denylist.
func denyReason(command string) string {
	if m := denyPattern.FindStringSubmatch(command); m != nil {
		return fmt.Sprintf("command uses denylisted token %q", m[1])
	}
	return ""
}
```

(Ensure `"regexp"` is added to the existing import block; don't split it out.)

Then insert the denylist check in `HandleBash`, immediately after the `command`/`description` presence checks and BEFORE the timeout computation:
```go
		if reason := denyReason(command); reason != "" {
			return ErrorResult("command rejected: %s", reason), nil
		}
```

**Step 4: Run tests to verify they pass**

```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run TestBash -v
```
Expected: all Bash tests (Task 1's 9 + Task 2's 8) pass. Specifically:
- The 6 denylist-rejection tests (`TestBashDeny_*Rejected`) return `IsError = true`.
- The 2 false-positive tests (`TestBashDeny_FalsePositiveAvoided_*`) still run successfully.

**Step 5: Lint + full suite**

```bash
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox lint
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./... -race -count=1
```
Both must exit 0.

**Step 6: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/tools/bash.go internal/tools/bash_test.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(tools): add Bash command denylist for obvious-footgun tokens
EOF
```

---

## Task 3: Register on the server + manual smoke test

**Files:**
- Modify: `internal/server/server.go` — one additional registration line.

**Step 1: Register the Bash tool**

In `internal/server/server.go`, in `New` after the existing `tools.RegisterGrep(...)` line, add:
```go
	tools.RegisterBash(s.mcp, &tools.Deps{Workspace: s.ws, Tracker: s.tracker})
```

**Step 2: Full test + race + lint + build sweep**

```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./... -race -count=1
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox lint
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox build ./...
```
All three must exit 0.

**Step 3: Commit the registration**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/server/server.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(server): register Bash tool on MCP server
EOF
```

**Step 4: Build the binary**

```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox build -o /Users/chaholl/repos/altairalabs/codegen-sandbox/bin/sandbox ./cmd/sandbox
```

**Step 5: Start the server against a temp workspace**

```bash
mkdir -p /tmp/codegen-sandbox-bash-smoke
/Users/chaholl/repos/altairalabs/codegen-sandbox/bin/sandbox -addr=127.0.0.1:18082 -workspace=/tmp/codegen-sandbox-bash-smoke >/tmp/sandbox-bash-smoke.log 2>&1 &
echo $! > /tmp/sandbox-bash-smoke.pid
```
(Use `run_in_background` if executing via the Bash tool.)

**Step 6: Verify Bash is listed and works**

```bash
curl -sS -N --max-time 4 --output /tmp/sandbox-bash-sse.txt http://127.0.0.1:18082/sse 2>/dev/null &
SSEPID=$!
sleep 0.3
SESSION_URL=$(grep -o 'data:.*' /tmp/sandbox-bash-sse.txt | head -1 | sed 's|data: *||' | tr -d '\r\n ')
curl -sS -X POST "http://127.0.0.1:18082${SESSION_URL}" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"smoke","version":"0"},"capabilities":{}}}' >/dev/null
curl -sS -X POST "http://127.0.0.1:18082${SESSION_URL}" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' >/dev/null
curl -sS -X POST "http://127.0.0.1:18082${SESSION_URL}" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' >/dev/null
curl -sS -X POST "http://127.0.0.1:18082${SESSION_URL}" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"Bash","arguments":{"command":"echo hello-from-bash","description":"smoke test"}}}' >/dev/null
sleep 1
kill $SSEPID 2>/dev/null || true
echo '--- tool names ---'
grep -o '"name":"[^"]*"' /tmp/sandbox-bash-sse.txt | sort -u
echo '--- Bash call result ---'
grep 'hello-from-bash' /tmp/sandbox-bash-sse.txt || echo "FAIL: Bash call did not return expected text"
```
Expected: `"name":"Bash"` appears in the tools list (alongside the other 5 names), and the grep finds `hello-from-bash` in the captured SSE stream.

**Step 7: Stop the server**

```bash
kill "$(cat /tmp/sandbox-bash-smoke.pid)" 2>/dev/null || true
```

**Step 8: No commit** (no file changes).

---

## Self-Review Notes

**Spec coverage:**
- Foreground bash execution (cwd, stdin, env inheritance, timeout, output cap, exit code) — Task 1.
- Command denylist (sudo/su/shutdown/reboot/halt/poweroff/chroot/mount/umount/mkfs) — Task 2.
- End-to-end wiring — Task 3.

**Deliberately deferred:**
- Background mode, `BashOutput`, `KillShell` → Plan 7.
- Network-restriction denylist (curl, wget, nc, ssh) → Plan 8 will address via URL filter.
- Secret scrubbing of output → Plan 5.
- Per-call env allowlist → out of scope; container controls env.
- ~~Captures of partial output on timeout (`cmd.CombinedOutput` returns whatever was buffered before the process was killed — which is what we use; no extra logic needed).~~ **Superseded:** this claim was falsified during Task 1. `exec.CommandContext` SIGKILLs only the direct child; commands that fork detached descendants (`sleep 10 & wait`, piped jobs) keep the inherited pipe open, so `CombinedOutput` blocks past the timeout. Fixed in commit `23bb87d` with `SysProcAttr{Setpgid: true}` + a custom `cmd.Cancel` that kills the whole process group, plus `cmd.WaitDelay = 2s` to let buffered output flush. Test: `TestBash_TimeoutKillsBackgroundedChildren`.

**Placeholder scan:** no TBDs, no "implement later", no "add appropriate error handling". Each step has code blocks where code is needed and explicit expected outputs where commands are run.

**Type consistency:**
- `HandleBash` signature matches the Read/Write/Edit/Glob/Grep handler pattern.
- `Deps` unchanged from prior plans.
- `denyReason` returns a string (empty for no-match); consistent internal helper style with `grepModeArgs` from Plan 2.
- Constants `defaultBashTimeoutSec`, `maxBashTimeoutSec`, `bashOutputCapBytes` are all lowercase `const` — same convention as `defaultReadLimit`, `defaultGlobLimit`.

**Known limits (documented above, not plan bugs):**
- Denylist is regex-based; determined attackers can bypass via `$(echo sudo)` or quoted strings. Acceptable — container isolation is the real boundary.
- Output merging via `cmd.CombinedOutput()` interleaves stdout and stderr at buffer granularity, which is OK for agent consumption but not perfectly ordered.
- `1> output.txt` style redirections inside the command are invisible to the tool result (they land on disk inside the workspace — which is fine for workflows that produce files the agent later Reads).
