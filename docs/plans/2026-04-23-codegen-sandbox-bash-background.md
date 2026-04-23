# Codegen Sandbox — Bash Background Mode + BashOutput + KillShell Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Extend the `Bash` tool with `run_in_background`, and add `BashOutput` (fetch stdout/stderr/exit for a background shell) and `KillShell` (terminate a background shell and its process group). Shells live for the lifetime of the sandbox container.

**Architecture:** A new `internal/tools/shell_registry.go` owns an `*ShellRegistry` — a goroutine-safe map of `shell_id` → `*backgroundShell`. Each `backgroundShell` holds the `exec.Cmd`, process-group pid, a capped stdout buffer, a capped stderr buffer, an exit code (nil while running), and timestamps. The registry is constructed in `server.New` and injected via `tools.Deps`. `Bash` in background mode registers a shell, spawns background reader goroutines to fill the buffers, and returns the shell ID immediately. `BashOutput` reads the current state of a shell. `KillShell` sends SIGKILL to the process group and removes the shell from the registry.

**Tech Stack:** Go 1.25+, `github.com/mark3labs/mcp-go` v0.49.0, `github.com/google/uuid` for shell IDs.

**Out of scope:**
- Incremental output (per-reader offset tracking). Each `BashOutput` returns the FULL buffer (up to the cap). Agents can grep what they need.
- Background-shell auto-timeout. Background shells run until they finish or are killed. Future plan can add a max-runtime guard if needed.
- Per-tool output filtering (regex filter on BashOutput). Agents can `grep` client-side.
- Multi-shell `stdin` — shells are started with stdin=/dev/null.

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/tools/shell_registry.go` | `ShellRegistry` (goroutine-safe map), `backgroundShell` (cmd + buffers + exit state), `RegisterShell`/`GetShell`/`RemoveShell`/`NewShellID`. |
| `internal/tools/shell_registry_test.go` | Concurrent register/get/remove, snapshot-while-running. |
| `internal/tools/tools.go` | Extend `Deps` with `Shells *ShellRegistry`. |
| `internal/tools/bash.go` | Add `run_in_background` schema param + handler branch. On true: launch shell, register, return `shell_id: <uuid>\n`. |
| `internal/tools/bash_test.go` | Test for background path (returns shell_id, doesn't block). |
| `internal/tools/bash_output.go` | `RegisterBashOutput`, `HandleBashOutput`. Returns stdout + stderr + exit/status for a given shell_id. |
| `internal/tools/bash_output_test.go` | Tests: unknown id, running shell, completed shell. |
| `internal/tools/kill_shell.go` | `RegisterKillShell`, `HandleKillShell`. SIGKILLs process group, removes from registry. |
| `internal/tools/kill_shell_test.go` | Tests: unknown id, kill while running. |
| `internal/server/server.go` | Construct `ShellRegistry`, put in `Deps`, register the two new tools. |

---

## Task 1: ShellRegistry + backgroundShell

**Files:** `internal/tools/shell_registry.go`, `_test.go`

Contract:
- `NewShellID() string` — returns a fresh UUID.
- `NewShellRegistry() *ShellRegistry`.
- `(*ShellRegistry).Register(shell *backgroundShell)` — insert (shell's ID is pre-generated).
- `(*ShellRegistry).Get(id string) (*backgroundShell, bool)`.
- `(*ShellRegistry).Remove(id string)`.
- `backgroundShell` fields (all unexported; accessed through methods):
  - `id string` / `ID() string`
  - `command string` / `Command() string`
  - `startedAt time.Time` / `StartedAt()`
  - `AppendStdout([]byte)`, `AppendStderr([]byte)` — capped at `shellOutputCapBytes = 1 * 1024 * 1024` per stream. When cap reached, further writes are silently dropped and `StdoutTruncated()/StderrTruncated()` return true.
  - `Snapshot() (stdout, stderr []byte, stdoutTruncated, stderrTruncated bool, exitCode *int, running bool)` — atomic read of current state.
  - `SetExit(code int)` — atomic mark-complete.
  - `Pgid() int` / `SetPgid(int)` — process group for KillShell.

**Step 1: Write tests**

Create `internal/tools/shell_registry_test.go`:
```go
package tools

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShellRegistry_RegisterGetRemove(t *testing.T) {
	r := NewShellRegistry()
	id := NewShellID()
	sh := newBackgroundShell(id, "echo x")
	r.Register(sh)

	got, ok := r.Get(id)
	require.True(t, ok)
	assert.Same(t, sh, got)

	r.Remove(id)
	_, ok = r.Get(id)
	assert.False(t, ok)
}

func TestShellRegistry_ConcurrentAccess(t *testing.T) {
	r := NewShellRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := NewShellID()
			sh := newBackgroundShell(id, "cmd")
			r.Register(sh)
			_, _ = r.Get(id)
			r.Remove(id)
		}()
	}
	wg.Wait()
}

func TestBackgroundShell_AppendAndSnapshot(t *testing.T) {
	sh := newBackgroundShell("id-1", "cmd")
	sh.AppendStdout([]byte("hello "))
	sh.AppendStdout([]byte("world"))
	sh.AppendStderr([]byte("oops"))

	stdout, stderr, stdoutT, stderrT, exit, running := sh.Snapshot()
	assert.Equal(t, "hello world", string(stdout))
	assert.Equal(t, "oops", string(stderr))
	assert.False(t, stdoutT)
	assert.False(t, stderrT)
	assert.Nil(t, exit)
	assert.True(t, running)

	sh.SetExit(42)
	_, _, _, _, exit2, running2 := sh.Snapshot()
	require.NotNil(t, exit2)
	assert.Equal(t, 42, *exit2)
	assert.False(t, running2)
}

func TestBackgroundShell_CapTruncates(t *testing.T) {
	sh := newBackgroundShell("id", "cmd")
	// Write cap+1 bytes across multiple appends.
	chunk := make([]byte, 512*1024)
	sh.AppendStdout(chunk)
	sh.AppendStdout(chunk)
	sh.AppendStdout([]byte("should be truncated"))

	stdout, _, stdoutT, _, _, _ := sh.Snapshot()
	assert.Len(t, stdout, shellOutputCapBytes)
	assert.True(t, stdoutT)
}

func TestBackgroundShell_StartedAt(t *testing.T) {
	before := time.Now()
	sh := newBackgroundShell("id", "cmd")
	assert.WithinDuration(t, before, sh.StartedAt(), time.Second)
}
```

White-box test (`package tools`) — exercises unexported constructors.

**Step 2: Add dep**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox get github.com/google/uuid
```

**Step 3: Implement**

Create `internal/tools/shell_registry.go`:
```go
package tools

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const shellOutputCapBytes = 1 * 1024 * 1024 // 1 MiB per stream

// NewShellID returns a fresh random shell identifier.
func NewShellID() string { return uuid.NewString() }

// ShellRegistry is a goroutine-safe map of shell ID to backgroundShell.
type ShellRegistry struct {
	mu     sync.RWMutex
	shells map[string]*backgroundShell
}

// NewShellRegistry constructs an empty registry.
func NewShellRegistry() *ShellRegistry {
	return &ShellRegistry{shells: make(map[string]*backgroundShell)}
}

// Register adds shell to the registry under shell.ID().
func (r *ShellRegistry) Register(shell *backgroundShell) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shells[shell.ID()] = shell
}

// Get returns the shell for id, or (nil, false) if absent.
func (r *ShellRegistry) Get(id string) (*backgroundShell, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sh, ok := r.shells[id]
	return sh, ok
}

// Remove deletes the shell with the given id (no-op if absent).
func (r *ShellRegistry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.shells, id)
}

// backgroundShell tracks one background-mode Bash invocation.
type backgroundShell struct {
	mu sync.Mutex

	id        string
	command   string
	startedAt time.Time
	pgid      int

	stdout          []byte
	stdoutTruncated bool
	stderr          []byte
	stderrTruncated bool

	exitCode *int
}

func newBackgroundShell(id, command string) *backgroundShell {
	return &backgroundShell{
		id:        id,
		command:   command,
		startedAt: time.Now(),
	}
}

func (s *backgroundShell) ID() string           { return s.id }
func (s *backgroundShell) Command() string      { return s.command }
func (s *backgroundShell) StartedAt() time.Time { return s.startedAt }

// Pgid / SetPgid are used by the Bash handler (to set) and KillShell (to read).
func (s *backgroundShell) Pgid() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pgid
}
func (s *backgroundShell) SetPgid(pgid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pgid = pgid
}

func (s *backgroundShell) AppendStdout(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stdout, s.stdoutTruncated = appendCapped(s.stdout, b, s.stdoutTruncated)
}
func (s *backgroundShell) AppendStderr(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stderr, s.stderrTruncated = appendCapped(s.stderr, b, s.stderrTruncated)
}

func appendCapped(dst, src []byte, alreadyTruncated bool) ([]byte, bool) {
	if alreadyTruncated {
		return dst, true
	}
	remaining := shellOutputCapBytes - len(dst)
	if remaining <= 0 {
		return dst, true
	}
	if len(src) <= remaining {
		return append(dst, src...), false
	}
	return append(dst, src[:remaining]...), true
}

// SetExit marks the shell as completed with the given exit code.
func (s *backgroundShell) SetExit(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exitCode = &code
}

// Snapshot atomically reads the current state. stdout/stderr are returned
// by copy so callers can mutate them safely. exitCode is nil while running.
func (s *backgroundShell) Snapshot() (stdout, stderr []byte, stdoutTruncated, stderrTruncated bool, exitCode *int, running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stdout = append([]byte(nil), s.stdout...)
	stderr = append([]byte(nil), s.stderr...)
	stdoutTruncated = s.stdoutTruncated
	stderrTruncated = s.stderrTruncated
	if s.exitCode != nil {
		ec := *s.exitCode
		exitCode = &ec
	}
	running = s.exitCode == nil
	return
}
```

**Step 4: Run tests**

```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run 'TestShellRegistry|TestBackgroundShell' -v -race
```
Expected: 5 pass.

**Step 5: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/tools/shell_registry.go internal/tools/shell_registry_test.go go.mod go.sum
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(tools): add ShellRegistry + backgroundShell for background Bash
EOF
```

---

## Task 2: Bash background mode

**Files:** modify `internal/tools/tools.go`, `internal/tools/bash.go`, `internal/tools/bash_test.go`, `internal/server/server.go`.

**Deps change:** add `Shells *ShellRegistry` field.

**Bash schema change:** add `run_in_background` (optional bool, default false).

**Handler change:** when `run_in_background` is true:
- Same denylist + description/command validation.
- Spawn the cmd with `Setpgid: true`. Capture its pgid after Start.
- Start two goroutines draining stdout/stderr into the backgroundShell's buffers.
- A third goroutine `Wait()`s and calls `SetExit(exitCode)` when done.
- Immediately return a text result: `shell_id: <uuid>\nstarted in background: <command>\n`.

**Step 1: Append `Shells` to Deps**

In `internal/tools/tools.go`:
```go
type Deps struct {
	Workspace *workspace.Workspace
	Tracker   *workspace.ReadTracker
	Shells    *ShellRegistry
}
```

**Step 2: Wire ShellRegistry in server.New**

In `internal/server/server.go`, where `deps := &tools.Deps{Workspace: s.ws, Tracker: s.tracker}` is constructed, change to:
```go
deps := &tools.Deps{
    Workspace: s.ws,
    Tracker:   s.tracker,
    Shells:    tools.NewShellRegistry(),
}
```

**Step 3: Extend Bash schema and handler**

Modify `internal/tools/bash.go`. In `RegisterBash`, add:
```go
mcp.WithBoolean("run_in_background", mcp.Description("If true, spawn the command in the background and return a shell_id immediately. Use BashOutput to poll and KillShell to terminate.")),
```

In `HandleBash`, after the denylist check but before the timeout computation, branch:
```go
if bg, _ := args["run_in_background"].(bool); bg {
    return handleBashBackground(deps, command)
}
```

Add the helper at the bottom of `bash.go`:
```go
func handleBashBackground(deps *Deps, command string) (*mcp.CallToolResult, error) {
	if deps.Shells == nil {
		return ErrorResult("background shells not configured"), nil
	}
	id := NewShellID()
	sh := newBackgroundShell(id, command)
	deps.Shells.Register(sh)

	cmd := exec.Command("bash", "-c", command)
	cmd.Dir = deps.Workspace.Root()
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		deps.Shells.Remove(id)
		return ErrorResult("bash-bg stdout: %v", err), nil
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		deps.Shells.Remove(id)
		return ErrorResult("bash-bg stderr: %v", err), nil
	}

	if err := cmd.Start(); err != nil {
		deps.Shells.Remove(id)
		return ErrorResult("bash-bg start: %v", err), nil
	}
	sh.SetPgid(cmd.Process.Pid) // leader pid == pgid after Setpgid

	// Drain stdout.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				sh.AppendStdout(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	// Drain stderr.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stderrPipe.Read(buf)
			if n > 0 {
				sh.AppendStderr(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	// Wait for completion.
	go func() {
		err := cmd.Wait()
		code := 0
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				code = exitErr.ExitCode()
			} else {
				code = -1
			}
		}
		sh.SetExit(code)
	}()

	return TextResult(fmt.Sprintf("shell_id: %s\nstarted in background: %s\n", id, command)), nil
}
```

**Step 4: Test**

Add to `internal/tools/bash_test.go`:
```go
func TestBash_BackgroundReturnsShellID(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)
	deps.Shells = tools.NewShellRegistry()

	res := callBash(t, deps, map[string]any{
		"command":           "sleep 0.1; echo done",
		"description":       "background sleep",
		"run_in_background": true,
	})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "shell_id:")
	assert.Contains(t, body, "started in background")
}
```

(`newTestDeps` currently returns a `*Deps` without Shells — either update it to include one, or have the test inject one as above. The inline injection is less invasive.)

**Step 5: Run tests + lint + commit**

```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./... -race -count=1
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox lint

git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/tools/bash.go internal/tools/bash_test.go internal/tools/tools.go internal/server/server.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(tools): Bash run_in_background launches a shell and returns its ID
EOF
```

---

## Task 3: BashOutput tool

**Files:** `internal/tools/bash_output.go`, `_test.go`, modify `internal/server/server.go`.

**Schema:** `shell_id` (required string).

**Response:**
```
command: <cmd>
status: running | completed (exit N)
started: <RFC3339 timestamp>

--- stdout (N bytes)[truncated] ---
<content>

--- stderr (N bytes)[truncated] ---
<content>
```

- [ ] Create handler + tests. Unknown id → ErrorResult.

**Implementation sketch:**
```go
package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func RegisterBashOutput(s Registrar, deps *Deps) {
	tool := mcp.NewTool("BashOutput",
		mcp.WithDescription("Return the current stdout, stderr, and status of a background shell started via Bash with run_in_background=true."),
		mcp.WithString("shell_id", mcp.Required()),
	)
	s.AddTool(tool, HandleBashOutput(deps))
}

func HandleBashOutput(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)
		id, _ := args["shell_id"].(string)
		if id == "" {
			return ErrorResult("shell_id is required"), nil
		}
		sh, ok := deps.Shells.Get(id)
		if !ok {
			return ErrorResult("unknown shell_id: %s", id), nil
		}
		stdout, stderr, stdoutT, stderrT, exit, running := sh.Snapshot()

		var sb strings.Builder
		fmt.Fprintf(&sb, "command: %s\n", sh.Command())
		if running {
			sb.WriteString("status: running\n")
		} else {
			fmt.Fprintf(&sb, "status: completed (exit %d)\n", *exit)
		}
		fmt.Fprintf(&sb, "started: %s\n\n", sh.StartedAt().Format(time.RFC3339))
		fmt.Fprintf(&sb, "--- stdout (%d bytes)%s ---\n%s\n", len(stdout), truncMarker(stdoutT), stdout)
		fmt.Fprintf(&sb, "--- stderr (%d bytes)%s ---\n%s\n", len(stderr), truncMarker(stderrT), stderr)
		return TextResult(sb.String()), nil
	}
}

func truncMarker(t bool) string {
	if t {
		return " [TRUNCATED]"
	}
	return ""
}
```

Tests: start a background shell via the helper, then call BashOutput and verify stdout/status.

Register in `server.New`: add `tools.RegisterBashOutput(reg, deps)` after the existing Bash registration.

Commit: `feat(tools): add BashOutput to poll a background shell`.

---

## Task 4: KillShell tool

**Files:** `internal/tools/kill_shell.go`, `_test.go`, modify `internal/server/server.go`.

**Schema:** `shell_id` (required string).

**Behavior:** Look up the shell; `syscall.Kill(-pgid, SIGKILL)`; remove from registry. Returns `killed: <id>\n`.

Implementation:
```go
package tools

import (
	"context"
	"syscall"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func RegisterKillShell(s Registrar, deps *Deps) {
	tool := mcp.NewTool("KillShell",
		mcp.WithDescription("Kill a background shell started via Bash with run_in_background=true. Sends SIGKILL to the shell's process group."),
		mcp.WithString("shell_id", mcp.Required()),
	)
	s.AddTool(tool, HandleKillShell(deps))
}

func HandleKillShell(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)
		id, _ := args["shell_id"].(string)
		if id == "" {
			return ErrorResult("shell_id is required"), nil
		}
		sh, ok := deps.Shells.Get(id)
		if !ok {
			return ErrorResult("unknown shell_id: %s", id), nil
		}
		if pgid := sh.Pgid(); pgid > 0 {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
		deps.Shells.Remove(id)
		return TextResult("killed: " + id + "\n"), nil
	}
}
```

Tests: unknown id → error; kill-then-BashOutput → unknown id.

Commit: `feat(tools): add KillShell to terminate a background shell`.

---

## Task 5: Smoke

- Build binary.
- Start bg bash: `sleep 2 && echo done`.
- Verify BashOutput shows running, then (after wait) shows exit 0 + "done".
- KillShell on a long-running bg bash (`sleep 60`). Verify exit + removal.

No commit.

---

## Self-Review

**Spec coverage:** background launch (Task 2), BashOutput (Task 3), KillShell (Task 4), shell lifecycle (Task 1 registry).

**Out-of-scope confirmed:** incremental output, auto-timeout, filter. Each documented.

**Known trade-offs:**
- Background shells accumulate in the registry forever. Per-container lifetime is bounded by container lifetime (Plan 6).
- Process-group kill in KillShell uses `syscall.Kill(-pgid, SIGKILL)` — same pattern already used in bash.go foreground, exec.go, lint.go.
- `BashOutput` returns the full buffer every call. For very active shells approaching 1 MiB, this is N*1MB round-trips. Acceptable for v1.
