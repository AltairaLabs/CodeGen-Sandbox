# Codegen Sandbox — Verification (run_tests / run_lint / run_typecheck + Post-Edit Lint Feedback) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add project-type detection plus three verification tools (`run_tests`, `run_lint`, `run_typecheck`) to the sandbox, and wire the `Edit` tool to surface post-edit lint findings for the file it just modified — what the proposal calls "the single biggest quality win."

**Architecture:** A new `internal/verify` package exposes a `Detector` interface (language name, test/lint/typecheck commands) and a `Detect(root)` constructor. A Go implementation ships in this plan (marker: `go.mod`; commands: `go test ./...`, `golangci-lint run ./...`, `go vet ./...`). A second helper `verify.Lint(ctx, root, timeout)` executes the linter and parses `<file>:<line>:<col>: <msg> (<rule>)` into `LintFinding` values. Three new thin MCP tool handlers in `internal/tools` wrap the detector + a shared `runVerifyCmd` subprocess helper (same process-group-kill pattern as Bash, but with separate stdout/stderr capture). `HandleEdit` calls `verify.Lint` on success and appends findings filtered to the edited file to the text result.

**Tech Stack:** Go 1.25+, `github.com/mark3labs/mcp-go` v0.49.0, `golangci-lint` v2 on `PATH`, `github.com/stretchr/testify`.

**Prerequisite (operator):** Tests that invoke real Go tooling require `go` (always present when running tests) and `golangci-lint` (already installed locally at `/opt/homebrew/bin/golangci-lint` v2.6.0). Tests that need `golangci-lint` use a `requireGolangciLint(t)` helper that skips if it's missing. Plan 6 (Docker) will pin `golangci-lint` v2.x in the image.

**Out of scope for this plan:**
- **Non-Go languages.** The `Detector` interface is explicitly multi-language-ready, but only the Go implementation ships. Node/Python/Rust detectors are follow-up work — each is ~30 lines against the established interface.
- **Custom `make test` / `make lint` hooks.** The detector picks a convention; projects with non-standard scripts can work around using `Bash`.
- **Structured test-result parsing** (per-test PASS/FAIL records). `run_tests` returns raw output with an exit line; agents read it as text for v1. If a future plan wants test-result tables, extend `Detector` with a parser.
- **Structured typecheck parsing.** `go vet` output is `file:line:col: msg` — parseable, but no agent workflow today needs it. Raw output for v1.
- **Fix-on-save / auto-fix.** `Edit` reports lint findings; it does not attempt to fix them.

---

## File Structure

Files introduced or modified by this plan:

| Path | Responsibility |
|---|---|
| `internal/verify/verify.go` | `Detector` interface + `Detect(root) Detector` constructor. |
| `internal/verify/golang.go` | `goDetector` — Go implementation of `Detector`. |
| `internal/verify/verify_test.go` | Tests for `Detect` (Go marker present/absent) and the Go detector's command shapes. |
| `internal/verify/lint.go` | `LintFinding` struct, `ParseLint(text) []LintFinding`, and `Lint(ctx, root, timeoutSec) ([]LintFinding, error)`. |
| `internal/verify/lint_test.go` | Tests for `ParseLint` (including context-line / summary-block tolerance) and a live-linter test for `Lint` when `golangci-lint` is available. |
| `internal/tools/exec.go` | `runVerifyCmd(ctx, cmd, cwd, timeoutSec) (execResult, error)` — shared subprocess plumbing for verify tools. Separate stdout/stderr capture, process-group kill on timeout (same pattern as Bash). |
| `internal/tools/run_tests.go` | `RegisterRunTests`, `HandleRunTests`. |
| `internal/tools/run_tests_test.go` | Handler tests against a temp Go module. |
| `internal/tools/run_lint.go` | `RegisterRunLint`, `HandleRunLint`. |
| `internal/tools/run_lint_test.go` | Handler tests (lint happy path, no-detector, lint failure surfaces structured findings). |
| `internal/tools/run_typecheck.go` | `RegisterRunTypecheck`, `HandleRunTypecheck`. |
| `internal/tools/run_typecheck_test.go` | Handler tests (vet finds a bad printf). |
| `internal/tools/edit.go` | Modified: call `verify.Lint` after successful atomic write; filter to edited file; append findings to text result. |
| `internal/tools/edit_test.go` | Add `TestEdit_PostEditLintFeedback` (integration test against a real Go module). |
| `internal/server/server.go` | Register three new tools. |

Design rule carried forward: `internal/verify` owns project detection and structured output parsing. `internal/tools` owns MCP schemas, subprocess plumbing, and handler flow. Tasks 5 (`run_typecheck`) and 7 (Edit integration) are the only places that cross package lines.

---

## Task 1: Detector interface + Go detector

**Files:**
- Create: `internal/verify/verify.go`
- Create: `internal/verify/golang.go`
- Test: `internal/verify/verify_test.go`

**Contract:**
- `Detector` interface: `Language() string`, `TestCmd() []string`, `LintCmd() []string`, `TypecheckCmd() []string`.
- `Detect(root string) Detector` — returns a non-nil detector when a known marker is found in `root`, `nil` otherwise. Only the immediate workspace root is inspected (no recursive search).
- Go detector: marker is `go.mod` directly in root. Commands: `go test ./...`, `golangci-lint run ./...`, `go vet ./...`.

- [ ] **Step 1: Write the failing tests**

Create `internal/verify/verify_test.go`:
```go
package verify_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetect_NoMarkerReturnsNil(t *testing.T) {
	dir := t.TempDir()
	assert.Nil(t, verify.Detect(dir))
}

func TestDetect_GoModuleReturnsGoDetector(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\n"), 0o644))

	d := verify.Detect(dir)
	require.NotNil(t, d)
	assert.Equal(t, "go", d.Language())
}

func TestDetect_OnlyRootMarkerCounts(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nested")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "go.mod"), []byte("module probe\n"), 0o644))

	assert.Nil(t, verify.Detect(dir), "a go.mod in a subdirectory must not be detected as the workspace project")
}

func TestGoDetector_Commands(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\n"), 0o644))

	d := verify.Detect(dir)
	require.NotNil(t, d)
	assert.Equal(t, []string{"go", "test", "./..."}, d.TestCmd())
	assert.Equal(t, []string{"golangci-lint", "run", "./..."}, d.LintCmd())
	assert.Equal(t, []string{"go", "vet", "./..."}, d.TypecheckCmd())
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/verify/...
```
Expected: compile error — package `verify` does not exist.

- [ ] **Step 3: Implement the `Detector` interface + `Detect`**

Create `internal/verify/verify.go`:
```go
// Package verify implements project-type detection and structured output
// parsing for the codegen sandbox's verification tools (run_tests, run_lint,
// run_typecheck, and the post-edit lint feedback baked into Edit).
package verify

import (
	"os"
	"path/filepath"
)

// Detector is the interface every supported project type implements. It tells
// the verify tools what language the project is and what commands to run for
// each verification axis.
type Detector interface {
	// Language returns a short identifier for the detected project type
	// (e.g. "go", "node").
	Language() string
	// TestCmd returns the argv (including the binary name) for running the
	// project's test suite from the workspace root.
	TestCmd() []string
	// LintCmd returns the argv for running the project's linter.
	LintCmd() []string
	// TypecheckCmd returns the argv for running the project's type checker.
	TypecheckCmd() []string
}

// Detect returns a Detector for the project rooted at root, or nil if no
// known marker is found. Only the immediate root is inspected; markers in
// subdirectories do not count (the workspace root is the authoritative
// anchor per the sandbox's trust-boundary model).
func Detect(root string) Detector {
	if fileExists(filepath.Join(root, "go.mod")) {
		return &goDetector{root: root}
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
```

- [ ] **Step 4: Implement the Go detector**

Create `internal/verify/golang.go`:
```go
package verify

// goDetector implements Detector for Go projects identified by a go.mod at
// the workspace root.
type goDetector struct {
	root string
}

// Language reports "go".
func (*goDetector) Language() string { return "go" }

// TestCmd returns "go test ./..." — runs every package in the module.
func (*goDetector) TestCmd() []string { return []string{"go", "test", "./..."} }

// LintCmd returns "golangci-lint run ./..." — matches the project's Makefile
// convention and the golangci-lint v2 invocation shape.
func (*goDetector) LintCmd() []string { return []string{"golangci-lint", "run", "./..."} }

// TypecheckCmd returns "go vet ./..." — Go's native "does this type-check
// and pass static checks" command.
func (*goDetector) TypecheckCmd() []string { return []string{"go", "vet", "./..."} }
```

- [ ] **Step 5: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/verify/... -v
```
Expected: all 4 tests PASS.

- [ ] **Step 6: Lint**

Run:
```bash
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox lint
```
Expected: 0 issues.

- [ ] **Step 7: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/verify/verify.go internal/verify/golang.go internal/verify/verify_test.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(verify): add Detector interface with Go implementation
EOF
```

---

## Task 2: runVerifyCmd helper + run_tests tool

**Files:**
- Create: `internal/tools/exec.go`
- Create: `internal/tools/run_tests.go`
- Test: `internal/tools/run_tests_test.go`
- Modify: `internal/server/server.go` — one additional registration line.

**Contract of `runVerifyCmd`:**
- Signature: `func runVerifyCmd(ctx context.Context, cmd []string, cwd string, timeoutSec int) (execResult, error)`.
- `execResult` fields: `Stdout []byte`, `Stderr []byte`, `ExitCode int`, `TimedOut bool`.
- Spawns `cmd[0] cmd[1:]...` via `exec.CommandContext`, cwd set. Captures stdout and stderr to separate buffers.
- Per-process-group kill on timeout (same pattern as `Bash`: `SysProcAttr{Setpgid: true}` + custom `Cancel` that `syscall.Kill(-pid, SIGKILL)` + `WaitDelay = 2s`).
- Each stream capped independently at `verifyOutputCapBytes = 500 * 1024` with a truncation marker appended.
- Exit code: from `*exec.ExitError.ExitCode()` on non-zero exit; `124` on timeout (matching Bash's convention); `0` on success; `-1` if the process couldn't be spawned (binary missing) — in this case the returned `error` is non-nil.
- Non-zero exit is NOT a Go error — it's a data point. The caller decides what to do.
- Error return is reserved for transport/spawn faults (e.g. `exec.LookPath` failure).

**Tool contract (`run_tests`):**
- Name: `run_tests`
- Parameters: `timeout` (optional number, default 300s, clamped to max 1800s / 30 min).
- Behavior:
  - Call `verify.Detect(workspace root)`. If nil → error result "no supported project detected".
  - Run `detector.TestCmd()` via `runVerifyCmd` from the workspace root.
  - On spawn failure → error result "binary not found on PATH".
  - Otherwise → text result with combined stdout+stderr, plus a trailing `exit: N` line (always, so agents see the pass/fail status unambiguously).
  - If timed out, also include `timed out after Ns`.

- [ ] **Step 1: Write the failing `run_tests` tests**

Create `internal/tools/run_tests_test.go`:
```go
package tools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireGo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH; skipping")
	}
}

func callRunTests(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleRunTests(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

// seedGoModule writes a minimal go.mod + a trivial source and a passing test
// file into root, so `go test ./...` will succeed.
func seedGoModule(t *testing.T, root string, passing bool) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "probe.go"), []byte("package probe\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644))
	body := "package probe\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1,2) != 3 { t.Fatal(\"bad\") } }\n"
	if !passing {
		body = "package probe\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { t.Fatal(\"intentional failure\") }\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "probe_test.go"), []byte(body), 0o644))
}

func TestRunTests_NoDetectorIsError(t *testing.T) {
	requireGo(t)
	deps, _ := newTestDeps(t) // empty workspace, no go.mod
	res := callRunTests(t, deps, map[string]any{})
	assert.True(t, res.IsError)
}

func TestRunTests_PassingModule(t *testing.T) {
	requireGo(t)
	deps, root := newTestDeps(t)
	seedGoModule(t, root, true)

	res := callRunTests(t, deps, map[string]any{})
	require.False(t, res.IsError, "unexpected error: %s", textOf(t, res))

	body := textOf(t, res)
	assert.Contains(t, body, "PASS")
	assert.Contains(t, body, "exit: 0")
}

func TestRunTests_FailingModuleReturnsNonZeroExit(t *testing.T) {
	requireGo(t)
	deps, root := newTestDeps(t)
	seedGoModule(t, root, false)

	res := callRunTests(t, deps, map[string]any{})
	require.False(t, res.IsError, "test failures are expected results, not MCP errors")

	body := textOf(t, res)
	assert.Contains(t, body, "intentional failure")
	assert.NotContains(t, body, "exit: 0")
}
```

Do NOT redeclare `newTestDeps` / `textOf`; they live in `read_test.go`.

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run TestRunTests
```
Expected: compile error — `tools.HandleRunTests` undefined.

- [ ] **Step 3: Implement the shared `runVerifyCmd` helper**

Create `internal/tools/exec.go`:
```go
package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

const verifyOutputCapBytes = 500 * 1024

// Timeout exit code shared across verify tools, following the timeout(1)
// convention (same as bashTimeoutExitCode).
const verifyTimeoutExitCode = 124

// execResult is the output of a single verify subprocess call.
type execResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	TimedOut bool
}

// runVerifyCmd runs cmd in cwd with a timeout. Captures stdout and stderr
// separately (for structured parsing). Kills the whole process group on
// timeout. Caps each stream independently at verifyOutputCapBytes.
//
// Returns a non-nil error only for transport/spawn faults (e.g. binary not
// found). A non-zero exit code is a successful invocation with a negative
// result — the caller decides how to surface it.
func runVerifyCmd(ctx context.Context, cmd []string, cwd string, timeoutSec int) (execResult, error) {
	if len(cmd) == 0 {
		return execResult{}, errors.New("empty command")
	}

	if _, err := exec.LookPath(cmd[0]); err != nil {
		return execResult{}, fmt.Errorf("%s: not found on PATH", cmd[0])
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	c := exec.CommandContext(execCtx, cmd[0], cmd[1:]...)
	c.Dir = cwd
	c.Stdin = nil

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	// Same process-group-kill pattern as the Bash tool: Setpgid so bash's
	// descendants (and go test's test-binary subprocesses) land in a fresh
	// group, then kill that group on ctx cancel.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	c.WaitDelay = 2 * time.Second

	runErr := c.Run()

	res := execResult{
		Stdout:   truncateOutput(stdout.Bytes(), verifyOutputCapBytes),
		Stderr:   truncateOutput(stderr.Bytes(), verifyOutputCapBytes),
		TimedOut: errors.Is(execCtx.Err(), context.DeadlineExceeded),
	}
	switch {
	case res.TimedOut:
		res.ExitCode = verifyTimeoutExitCode
	case runErr == nil:
		res.ExitCode = 0
	default:
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		} else {
			return res, fmt.Errorf("%s: %w", cmd[0], runErr)
		}
	}
	return res, nil
}
```

`truncateOutput` is the helper already defined in `bash.go` — no need to redeclare.

- [ ] **Step 4: Implement `run_tests`**

Create `internal/tools/run_tests.go`:
```go
package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const (
	defaultRunTestsTimeoutSec = 300
	maxRunTestsTimeoutSec     = 1800
)

// RegisterRunTests registers the run_tests tool on the given MCP server.
func RegisterRunTests(s *mcpserver.MCPServer, deps *Deps) {
	tool := mcp.NewTool("run_tests",
		mcp.WithDescription("Run the project's test suite. Project type is detected from the workspace root (currently: Go via go.mod). Returns combined stdout+stderr plus a trailing 'exit: N' line."),
		mcp.WithNumber("timeout", mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.", defaultRunTestsTimeoutSec, maxRunTestsTimeoutSec))),
	)
	s.AddTool(tool, HandleRunTests(deps))
}

// HandleRunTests returns the run_tests tool handler.
func HandleRunTests(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		det := verify.Detect(deps.Workspace.Root())
		if det == nil {
			return ErrorResult("no supported project detected in workspace root"), nil
		}

		args, _ := req.Params.Arguments.(map[string]any)
		timeoutSec := defaultRunTestsTimeoutSec
		if v, ok := args["timeout"].(float64); ok && int(v) > 0 {
			timeoutSec = int(v)
			if timeoutSec > maxRunTestsTimeoutSec {
				timeoutSec = maxRunTestsTimeoutSec
			}
		}

		res, err := runVerifyCmd(ctx, det.TestCmd(), deps.Workspace.Root(), timeoutSec)
		if err != nil {
			return ErrorResult("run_tests: %v", err), nil
		}

		return TextResult(formatVerifyResult(res, timeoutSec)), nil
	}
}

// formatVerifyResult renders an execResult as agent-facing text:
// interleaved stdout + stderr (stdout first, then stderr on a new section),
// an optional timeout marker, and a trailing "exit: N" line.
func formatVerifyResult(res execResult, timeoutSec int) string {
	var sb strings.Builder
	sb.Write(res.Stdout)
	if len(res.Stdout) > 0 && !strings.HasSuffix(string(res.Stdout), "\n") {
		sb.WriteByte('\n')
	}
	if len(res.Stderr) > 0 {
		sb.WriteString("--- stderr ---\n")
		sb.Write(res.Stderr)
		if !strings.HasSuffix(string(res.Stderr), "\n") {
			sb.WriteByte('\n')
		}
	}
	if res.TimedOut {
		fmt.Fprintf(&sb, "timed out after %ds\n", timeoutSec)
	}
	fmt.Fprintf(&sb, "exit: %d\n", res.ExitCode)
	return sb.String()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run TestRunTests -v
```
Expected: 3 PASS.

- [ ] **Step 6: Register the tool**

In `internal/server/server.go`, in `New` after the existing `tools.RegisterBash(...)` line, add:
```go
	tools.RegisterRunTests(s.mcp, &tools.Deps{Workspace: s.ws, Tracker: s.tracker})
```

- [ ] **Step 7: Full test + lint**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./... -race -count=1
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox lint
```
Both must exit 0.

- [ ] **Step 8: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/tools/exec.go internal/tools/run_tests.go internal/tools/run_tests_test.go internal/server/server.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(tools): add run_tests tool with shared runVerifyCmd helper
EOF
```

---

## Task 3: LintFinding + ParseLint + verify.Lint + run_lint tool

**Files:**
- Create: `internal/verify/lint.go`
- Test: `internal/verify/lint_test.go`
- Create: `internal/tools/run_lint.go`
- Test: `internal/tools/run_lint_test.go`
- Modify: `internal/server/server.go` — one additional registration line.

**Contract of `LintFinding`:**
```go
type LintFinding struct {
	File    string // path as emitted by the linter (typically workspace-relative)
	Line    int
	Column  int
	Rule    string // linter name, e.g. "errcheck"
	Message string
}
```

**Contract of `ParseLint(text string) []LintFinding`:**
- Parses lines of the form `<file>:<line>:<col>: <msg> (<rule>)`.
- Tolerates context lines (the offending source lines shown below each finding) and the trailing `N issues:` summary block — anything that doesn't match the expected shape is silently skipped.
- Deduplication is NOT performed; callers see findings in emission order.

**Contract of `verify.Lint(ctx context.Context, root string, timeoutSec int) ([]LintFinding, error)`:**
- Detects the project via `Detect(root)`.
- If no detector: returns `(nil, nil)` — "no project" is not an error.
- Otherwise runs `detector.LintCmd()` from `root` with the given timeout.
- If the linter binary isn't on PATH: returns `(nil, ErrLinterMissing)` — a named sentinel so callers can distinguish "linter isn't installed" from "linter failed".
- On exit-code mismatch (including exit 1 which means "findings exist"), returns the parsed findings with nil error.
- On timeout or spawn fault: returns `(partial findings, err)` — best effort.

**Tool contract (`run_lint`):**
- Name: `run_lint`
- Parameters: `timeout` (optional, default 120s, max 600s).
- Behavior:
  - Call `verify.Lint`.
  - If no detector → error result.
  - If linter missing → error result `linter not installed: <binary>`.
  - Otherwise → text result with one finding per line in `<file>:<line>:<col>:<rule>: <message>` format, plus a trailing `N findings` summary. Empty result (no findings) returns `0 findings` (not an error, not empty text).

- [ ] **Step 1: Write the failing `lint.go` tests**

Create `internal/verify/lint_test.go`:
```go
package verify_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireGolangciLint(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not on PATH; skipping")
	}
}

func TestParseLint_HappyPath(t *testing.T) {
	sample := "bad.go:6:17: Error return value of `os.WriteFile` is not checked (errcheck)\n" +
		"    os.WriteFile(\"x\", []byte(\"y\"), 0o644)\n" +
		"                ^\n" +
		"main.go:6:17: printf: fmt.Printf format %d has arg \"x\" of wrong type string (govet)\n" +
		"    fmt.Printf(\"%d\", \"x\")\n" +
		"                ^\n" +
		"\n" +
		"2 issues:\n" +
		"* errcheck: 1\n" +
		"* govet: 1\n"
	findings := verify.ParseLint(sample)
	require.Len(t, findings, 2)

	assert.Equal(t, "bad.go", findings[0].File)
	assert.Equal(t, 6, findings[0].Line)
	assert.Equal(t, 17, findings[0].Column)
	assert.Equal(t, "errcheck", findings[0].Rule)
	assert.Contains(t, findings[0].Message, "os.WriteFile")

	assert.Equal(t, "main.go", findings[1].File)
	assert.Equal(t, "govet", findings[1].Rule)
}

func TestParseLint_EmptyInput(t *testing.T) {
	assert.Empty(t, verify.ParseLint(""))
}

func TestParseLint_NoFindingsTolerated(t *testing.T) {
	assert.Empty(t, verify.ParseLint("0 issues.\n"))
}

func TestParseLint_MessageWithParentheses(t *testing.T) {
	// A linter message can itself contain parentheses; the trailing "(rule)"
	// is the last parenthesized group on the line.
	sample := "x.go:1:1: something (with nested) stuff (errcheck)\n"
	findings := verify.ParseLint(sample)
	require.Len(t, findings, 1)
	assert.Equal(t, "errcheck", findings[0].Rule)
	assert.Equal(t, "something (with nested) stuff", findings[0].Message)
}

func TestLint_NoDetectorReturnsNilNil(t *testing.T) {
	dir := t.TempDir() // no go.mod → no detector
	findings, err := verify.Lint(context.Background(), dir, 30)
	require.NoError(t, err)
	assert.Nil(t, findings)
}

func TestLint_MissingBinaryReturnsSentinel(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\ngo 1.21\n"), 0o644))
	t.Setenv("PATH", t.TempDir()) // empty PATH — golangci-lint unreachable

	_, err := verify.Lint(context.Background(), dir, 30)
	require.Error(t, err)
	assert.True(t, errors.Is(err, verify.ErrLinterMissing), "expected ErrLinterMissing, got %v", err)
}

func TestLint_LiveFindsRealIssue(t *testing.T) {
	requireGolangciLint(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".golangci.yml"), []byte(
		"version: \"2\"\nlinters:\n  default: none\n  enable:\n    - errcheck\n",
	), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.go"), []byte(
		"package probe\n\nimport \"os\"\n\nfunc writeErr() { os.WriteFile(\"x\", []byte(\"y\"), 0o644) }\n",
	), 0o644))

	findings, err := verify.Lint(context.Background(), dir, 60)
	require.NoError(t, err)
	require.NotEmpty(t, findings, "live linter should have produced at least one errcheck finding")
	assert.Equal(t, "bad.go", findings[0].File)
	assert.Equal(t, "errcheck", findings[0].Rule)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/verify/... -run 'TestParseLint|TestLint'
```
Expected: compile error — `verify.ParseLint`, `verify.Lint`, `verify.ErrLinterMissing` undefined.

- [ ] **Step 3: Implement `lint.go`**

Create `internal/verify/lint.go`:
```go
package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrLinterMissing is returned by Lint when the detected project's lint
// binary cannot be found on PATH.
var ErrLinterMissing = errors.New("linter binary not found on PATH")

// LintFinding is a single structured diagnostic emitted by the linter.
type LintFinding struct {
	File    string
	Line    int
	Column  int
	Rule    string
	Message string
}

// lintLineRe matches golangci-lint v2's default output format:
//
//	path/to/file.go:LINE:COL: free-form message (rulename)
//
// The rule group is the LAST parenthesized token on the line, so a message
// that itself contains parentheses is preserved intact.
var lintLineRe = regexp.MustCompile(
	`^(?P<file>[^:]+):(?P<line>\d+):(?P<col>\d+):\s+(?P<msg>.+?)\s+\((?P<rule>[A-Za-z][A-Za-z0-9_\-]*)\)\s*$`,
)

// ParseLint extracts structured findings from linter output. Lines that
// don't match the expected format (context lines, summary block, banners)
// are silently ignored.
func ParseLint(text string) []LintFinding {
	var findings []LintFinding
	for _, line := range strings.Split(text, "\n") {
		m := lintLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lineNo, _ := strconv.Atoi(m[lintLineRe.SubexpIndex("line")])
		col, _ := strconv.Atoi(m[lintLineRe.SubexpIndex("col")])
		findings = append(findings, LintFinding{
			File:    m[lintLineRe.SubexpIndex("file")],
			Line:    lineNo,
			Column:  col,
			Rule:    m[lintLineRe.SubexpIndex("rule")],
			Message: m[lintLineRe.SubexpIndex("msg")],
		})
	}
	return findings
}

// Lint runs the project's linter and returns parsed findings. Returns
// (nil, nil) when there is no detected project. Returns (nil,
// ErrLinterMissing) when the linter binary isn't installed. Returns
// (findings, nil) on exit 0 or exit 1 (the latter is the linter's
// "findings exist" convention). Returns (findings, err) on timeout or
// spawn error — callers treat this as best-effort.
func Lint(ctx context.Context, root string, timeoutSec int) ([]LintFinding, error) {
	det := Detect(root)
	if det == nil {
		return nil, nil
	}
	cmd := det.LintCmd()
	if len(cmd) == 0 {
		return nil, nil
	}
	if _, err := exec.LookPath(cmd[0]); err != nil {
		return nil, ErrLinterMissing
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	c := exec.CommandContext(execCtx, cmd[0], cmd[1:]...)
	c.Dir = root
	c.Stdin = nil
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	c.WaitDelay = 2 * time.Second

	runErr := c.Run()

	// Parse whatever we got regardless of exit code — golangci-lint emits
	// findings on stdout, and exits 1 when findings exist.
	findings := ParseLint(stdout.String())

	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		return findings, fmt.Errorf("lint: timed out after %ds", timeoutSec)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return findings, fmt.Errorf("lint: %w", runErr)
		}
		// Exit 1 for golangci-lint == "findings exist". That's expected.
		// Exit ≥ 2 is a genuine linter failure.
		if exitErr.ExitCode() >= 2 {
			return findings, fmt.Errorf("lint: %w (stderr: %s)", runErr, stderr.String())
		}
	}
	return findings, nil
}
```

- [ ] **Step 4: Run verify tests to confirm `lint.go` passes**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/verify/... -v
```
Expected: all tests PASS (including the live `TestLint_LiveFindsRealIssue`).

- [ ] **Step 5: Write the failing `run_lint` handler tests**

Create `internal/tools/run_lint_test.go`:
```go
package tools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireGolangciLintTool(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not on PATH; skipping")
	}
}

func callRunLint(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleRunLint(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

// seedLintableModule writes a go.mod, a .golangci.yml enabling errcheck,
// and a bad.go file with an unchecked error (guaranteed errcheck finding).
func seedLintableModule(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".golangci.yml"), []byte(
		"version: \"2\"\nlinters:\n  default: none\n  enable:\n    - errcheck\n",
	), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bad.go"), []byte(
		"package probe\n\nimport \"os\"\n\nfunc writeErr() { os.WriteFile(\"x\", []byte(\"y\"), 0o644) }\n",
	), 0o644))
}

func TestRunLint_NoDetectorIsError(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callRunLint(t, deps, map[string]any{})
	assert.True(t, res.IsError)
}

func TestRunLint_FindingsPrintedAsStructured(t *testing.T) {
	requireGolangciLintTool(t)
	deps, root := newTestDeps(t)
	seedLintableModule(t, root)

	res := callRunLint(t, deps, map[string]any{})
	require.False(t, res.IsError, "unexpected error: %s", textOf(t, res))

	body := textOf(t, res)
	assert.Contains(t, body, "bad.go:")
	assert.Contains(t, body, "errcheck:")
	// Final summary line.
	assert.Contains(t, body, "findings")
}

func TestRunLint_CleanModuleReturnsZeroFindings(t *testing.T) {
	requireGolangciLintTool(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".golangci.yml"), []byte(
		"version: \"2\"\nlinters:\n  default: none\n  enable:\n    - errcheck\n",
	), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "good.go"), []byte(
		"package probe\n\nfunc Add(a, b int) int { return a + b }\n",
	), 0o644))

	res := callRunLint(t, deps, map[string]any{})
	require.False(t, res.IsError)
	assert.Contains(t, textOf(t, res), "0 findings")
}
```

- [ ] **Step 6: Run handler tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run TestRunLint
```
Expected: compile error — `tools.HandleRunLint` undefined.

- [ ] **Step 7: Implement `run_lint`**

Create `internal/tools/run_lint.go`:
```go
package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const (
	defaultRunLintTimeoutSec = 120
	maxRunLintTimeoutSec     = 600
)

// RegisterRunLint registers the run_lint tool on the given MCP server.
func RegisterRunLint(s *mcpserver.MCPServer, deps *Deps) {
	tool := mcp.NewTool("run_lint",
		mcp.WithDescription("Run the project's linter. Returns structured findings as 'file:line:col:rule: message' followed by 'N findings'. Project type is detected from the workspace root (currently: Go via go.mod, uses golangci-lint)."),
		mcp.WithNumber("timeout", mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.", defaultRunLintTimeoutSec, maxRunLintTimeoutSec))),
	)
	s.AddTool(tool, HandleRunLint(deps))
}

// HandleRunLint returns the run_lint tool handler.
func HandleRunLint(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if verify.Detect(deps.Workspace.Root()) == nil {
			return ErrorResult("no supported project detected in workspace root"), nil
		}

		args, _ := req.Params.Arguments.(map[string]any)
		timeoutSec := defaultRunLintTimeoutSec
		if v, ok := args["timeout"].(float64); ok && int(v) > 0 {
			timeoutSec = int(v)
			if timeoutSec > maxRunLintTimeoutSec {
				timeoutSec = maxRunLintTimeoutSec
			}
		}

		findings, err := verify.Lint(ctx, deps.Workspace.Root(), timeoutSec)
		if err != nil {
			if errors.Is(err, verify.ErrLinterMissing) {
				return ErrorResult("linter not installed on PATH"), nil
			}
			return ErrorResult("run_lint: %v", err), nil
		}

		return TextResult(formatFindings(findings)), nil
	}
}

// formatFindings renders a []LintFinding as agent-facing text with one
// finding per line plus a trailing "N findings" summary.
func formatFindings(findings []verify.LintFinding) string {
	var sb strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&sb, "%s:%d:%d:%s: %s\n", f.File, f.Line, f.Column, f.Rule, f.Message)
	}
	fmt.Fprintf(&sb, "%d findings\n", len(findings))
	return sb.String()
}
```

- [ ] **Step 8: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run TestRunLint -v
```
Expected: 3 tests PASS.

- [ ] **Step 9: Register the tool**

In `internal/server/server.go`, in `New` after the existing `tools.RegisterRunTests(...)` line, add:
```go
	tools.RegisterRunLint(s.mcp, &tools.Deps{Workspace: s.ws, Tracker: s.tracker})
```

- [ ] **Step 10: Full test + lint**

```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./... -race -count=1
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox lint
```
Both must exit 0.

- [ ] **Step 11: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/verify/lint.go internal/verify/lint_test.go internal/tools/run_lint.go internal/tools/run_lint_test.go internal/server/server.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(tools): add run_lint tool with structured findings + verify.Lint helper
EOF
```

---

## Task 4: run_typecheck tool

**Files:**
- Create: `internal/tools/run_typecheck.go`
- Test: `internal/tools/run_typecheck_test.go`
- Modify: `internal/server/server.go` — one additional registration line.

**Tool contract (`run_typecheck`):**
- Name: `run_typecheck`
- Parameters: `timeout` (optional, default 120s, max 600s).
- Behavior:
  - Detect project; no detector → error.
  - Run `detector.TypecheckCmd()` via `runVerifyCmd`.
  - Binary missing → error.
  - Otherwise: text result with combined stdout+stderr, trailing `exit: N` line. `go vet` emits diagnostics on stderr, so the stderr section will typically carry the findings.

- [ ] **Step 1: Write the failing `run_typecheck` tests**

Create `internal/tools/run_typecheck_test.go`:
```go
package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callRunTypecheck(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleRunTypecheck(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestRunTypecheck_NoDetectorIsError(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callRunTypecheck(t, deps, map[string]any{})
	assert.True(t, res.IsError)
}

func TestRunTypecheck_CleanModuleExitsZero(t *testing.T) {
	requireGo(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "good.go"), []byte(
		"package probe\n\nfunc Add(a, b int) int { return a + b }\n",
	), 0o644))

	res := callRunTypecheck(t, deps, map[string]any{})
	require.False(t, res.IsError)
	assert.Contains(t, textOf(t, res), "exit: 0")
}

func TestRunTypecheck_FlagsTypeError(t *testing.T) {
	requireGo(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bad.go"), []byte(
		"package probe\n\nimport \"fmt\"\n\nfunc Bad() { fmt.Printf(\"%d\", \"not-an-int\") }\n",
	), 0o644))

	res := callRunTypecheck(t, deps, map[string]any{})
	require.False(t, res.IsError, "vet failures are expected results, not MCP errors")

	body := textOf(t, res)
	assert.Contains(t, body, "fmt.Printf")
	assert.NotContains(t, body, "exit: 0")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run TestRunTypecheck
```
Expected: compile error — `tools.HandleRunTypecheck` undefined.

- [ ] **Step 3: Implement `run_typecheck`**

Create `internal/tools/run_typecheck.go`:
```go
package tools

import (
	"context"
	"fmt"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const (
	defaultRunTypecheckTimeoutSec = 120
	maxRunTypecheckTimeoutSec     = 600
)

// RegisterRunTypecheck registers the run_typecheck tool.
func RegisterRunTypecheck(s *mcpserver.MCPServer, deps *Deps) {
	tool := mcp.NewTool("run_typecheck",
		mcp.WithDescription("Run the project's type checker. Project type is detected from the workspace root (currently: Go via go.mod, uses `go vet`). Returns combined stdout+stderr plus a trailing 'exit: N' line."),
		mcp.WithNumber("timeout", mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.", defaultRunTypecheckTimeoutSec, maxRunTypecheckTimeoutSec))),
	)
	s.AddTool(tool, HandleRunTypecheck(deps))
}

// HandleRunTypecheck returns the run_typecheck tool handler.
func HandleRunTypecheck(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		det := verify.Detect(deps.Workspace.Root())
		if det == nil {
			return ErrorResult("no supported project detected in workspace root"), nil
		}

		args, _ := req.Params.Arguments.(map[string]any)
		timeoutSec := defaultRunTypecheckTimeoutSec
		if v, ok := args["timeout"].(float64); ok && int(v) > 0 {
			timeoutSec = int(v)
			if timeoutSec > maxRunTypecheckTimeoutSec {
				timeoutSec = maxRunTypecheckTimeoutSec
			}
		}

		res, err := runVerifyCmd(ctx, det.TypecheckCmd(), deps.Workspace.Root(), timeoutSec)
		if err != nil {
			return ErrorResult("run_typecheck: %v", err), nil
		}
		return TextResult(formatVerifyResult(res, timeoutSec)), nil
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run TestRunTypecheck -v
```
Expected: 3 tests PASS.

- [ ] **Step 5: Register the tool**

In `internal/server/server.go`, in `New` after the existing `tools.RegisterRunLint(...)` line, add:
```go
	tools.RegisterRunTypecheck(s.mcp, &tools.Deps{Workspace: s.ws, Tracker: s.tracker})
```

- [ ] **Step 6: Full test + lint**

```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./... -race -count=1
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox lint
```
Both must exit 0.

- [ ] **Step 7: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/tools/run_typecheck.go internal/tools/run_typecheck_test.go internal/server/server.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(tools): add run_typecheck tool (Go: go vet)
EOF
```

---

## Task 5: Post-edit lint feedback wired into Edit

**Files:**
- Modify: `internal/tools/edit.go` — call `verify.Lint` on success; filter to edited file; append findings.
- Modify: `internal/tools/edit_test.go` — add `TestEdit_PostEditLintFeedback` integration test.

**Contract change (Edit tool):**
- On successful atomic write, call `verify.Lint(ctx, workspaceRoot, postEditLintTimeoutSec)` with a short timeout (30s).
- Best-effort: if `Lint` returns an error OR `ErrLinterMissing` OR a timeout partial, silently suppress the feedback block. Edit's success message is unchanged.
- Filter findings to those whose file path equals the edited file's workspace-relative path.
- If any findings match: append a blank line, `post-edit lint findings (N):`, and one line per finding in the same shape `run_lint` emits (`file:line:col:rule: message`).
- If no matching findings: no feedback block — the base success message is unchanged.

**Behavioral note:** Edit continues to succeed even if lint reports findings. The findings are informational. Agents can respond to them by issuing a follow-up Edit.

- [ ] **Step 1: Write the failing post-edit lint test**

Append to `internal/tools/edit_test.go`:
```go
func TestEdit_PostEditLintFeedback(t *testing.T) {
	requireGolangciLintTool(t)
	deps, root := newTestDeps(t)

	// Set up a Go module with a .golangci.yml that ONLY enables errcheck —
	// so we get deterministic findings regardless of the default lint set.
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".golangci.yml"), []byte(
		"version: \"2\"\nlinters:\n  default: none\n  enable:\n    - errcheck\n",
	), 0o644))

	// Start with a file that lints clean.
	path := filepath.Join(root, "probe.go")
	good := "package probe\n\nimport \"os\"\n\nfunc Write() error { return os.WriteFile(\"x\", []byte(\"y\"), 0o644) }\n"
	writeAndMarkRead(t, deps, path, good)

	// Edit introduces an unchecked error — errcheck should fire.
	bad := "package probe\n\nimport \"os\"\n\nfunc Write() { os.WriteFile(\"x\", []byte(\"y\"), 0o644) }\n"
	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": good,
		"new_string": bad,
	})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "replaced")
	assert.Contains(t, body, "post-edit lint findings")
	assert.Contains(t, body, "probe.go")
	assert.Contains(t, body, "errcheck")
}

func TestEdit_PostEditLintSilentOnNonGoProject(t *testing.T) {
	// No go.mod — Detect returns nil — no feedback block should appear.
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "notes.txt")
	writeAndMarkRead(t, deps, path, "alpha\nbeta\n")

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "gamma",
	})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "replaced")
	assert.NotContains(t, body, "post-edit lint findings")
}
```

(Both tests reuse `writeAndMarkRead` and `callEdit` from the existing `edit_test.go`. The first reuses `requireGolangciLintTool` from `run_lint_test.go`. All three are package-local helpers in `tools_test`.)

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run 'TestEdit_PostEditLint' -v
```
Expected: `TestEdit_PostEditLintFeedback` fails — the Edit result doesn't contain the lint findings block yet. `TestEdit_PostEditLintSilentOnNonGoProject` passes incidentally (no lint block expected either way).

- [ ] **Step 3: Wire post-edit lint into `Edit`**

Modify `internal/tools/edit.go`. Find the existing end-of-handler block:
```go
		if err := atomicWrite(abs, []byte(updated)); err != nil {
			return ErrorResult("write: %v", err), nil
		}
		return TextResult(fmt.Sprintf("replaced %d occurrence(s) in %s", count, filePath)), nil
```

Replace it with:
```go
		if err := atomicWrite(abs, []byte(updated)); err != nil {
			return ErrorResult("write: %v", err), nil
		}
		msg := fmt.Sprintf("replaced %d occurrence(s) in %s", count, filePath)
		if feedback := postEditLintFeedback(ctx, deps.Workspace.Root(), abs); feedback != "" {
			msg += "\n\n" + feedback
		}
		return TextResult(msg), nil
```

Then add the helper at the bottom of the file (after `HandleEdit`):
```go
// postEditLintTimeoutSec is deliberately short — the linter run is a
// best-effort annotation on the Edit result, not the primary purpose of the
// call, so we don't want a slow linter to dominate per-Edit latency.
const postEditLintTimeoutSec = 30

// postEditLintFeedback runs the project's linter (best effort) and returns a
// formatted block of findings that apply to the file just edited. Returns
// "" if there are no findings, no detected project, or the linter couldn't
// run for any reason — Edit should proceed normally.
func postEditLintFeedback(ctx context.Context, root, editedAbs string) string {
	findings, err := verify.Lint(ctx, root, postEditLintTimeoutSec)
	if err != nil || len(findings) == 0 {
		return ""
	}
	rel, err := filepath.Rel(root, editedAbs)
	if err != nil {
		return ""
	}

	var matched []verify.LintFinding
	for _, f := range findings {
		cmpFile := f.File
		if filepath.IsAbs(cmpFile) {
			if r, err := filepath.Rel(root, cmpFile); err == nil {
				cmpFile = r
			}
		}
		if cmpFile == rel {
			matched = append(matched, f)
		}
	}
	if len(matched) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "post-edit lint findings (%d):\n", len(matched))
	for _, f := range matched {
		fmt.Fprintf(&sb, "%s:%d:%d:%s: %s\n", f.File, f.Line, f.Column, f.Rule, f.Message)
	}
	return sb.String()
}
```

Also extend the `internal/tools/edit.go` import block to include `"path/filepath"`, `"strings"`, and `"github.com/altairalabs/codegen-sandbox/internal/verify"`. Replace the existing import block:
```go
import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)
```
with:
```go
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run 'TestEdit' -v
```
Expected: all Edit tests PASS, including the two new post-edit-lint tests.

- [ ] **Step 5: Full test + race + lint + build**

```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./... -race -count=1
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox lint
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox build ./...
```
All three must exit 0.

- [ ] **Step 6: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/tools/edit.go internal/tools/edit_test.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(tools): Edit appends post-edit lint findings for the edited file

This closes the loop the proposal calls the "single biggest quality win":
after a successful Edit, the tool runs the project's linter (best effort,
30s timeout) and appends any findings that apply to the file just edited.
On non-Go projects, missing linter binaries, or lint timeouts, the Edit
result is unchanged.
EOF
```

---

## Task 6: Manual smoke test

**Files:** none (manual verification)

Mirrors the smokes from prior plans. No new code, no commit.

- [ ] **Step 1: Build the binary**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox build -o /Users/chaholl/repos/altairalabs/codegen-sandbox/bin/sandbox ./cmd/sandbox
```
Expected: exit 0.

- [ ] **Step 2: Start the server against a fresh Go-module temp workspace**

```bash
mkdir -p /tmp/codegen-sandbox-verify-smoke
cat > /tmp/codegen-sandbox-verify-smoke/go.mod <<'EOF'
module smoke

go 1.21
EOF
cat > /tmp/codegen-sandbox-verify-smoke/probe.go <<'EOF'
package smoke

func Add(a, b int) int { return a + b }
EOF
cat > /tmp/codegen-sandbox-verify-smoke/probe_test.go <<'EOF'
package smoke

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("bad")
	}
}
EOF
/Users/chaholl/repos/altairalabs/codegen-sandbox/bin/sandbox -addr=127.0.0.1:18083 -workspace=/tmp/codegen-sandbox-verify-smoke >/tmp/sandbox-verify-smoke.log 2>&1 &
echo $! > /tmp/sandbox-verify-smoke.pid
```
(Use `run_in_background` if executing via the Bash tool.)

- [ ] **Step 3: Initialize the MCP session and list tools**

```bash
curl -sS -N --max-time 4 --output /tmp/sandbox-verify-sse.txt http://127.0.0.1:18083/sse 2>/dev/null &
SSEPID=$!
sleep 0.3
SESSION_URL=$(grep -o 'data:.*' /tmp/sandbox-verify-sse.txt | head -1 | sed 's|data: *||' | tr -d '\r\n ')
curl -sS -X POST "http://127.0.0.1:18083${SESSION_URL}" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"smoke","version":"0"},"capabilities":{}}}' >/dev/null
curl -sS -X POST "http://127.0.0.1:18083${SESSION_URL}" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' >/dev/null
curl -sS -X POST "http://127.0.0.1:18083${SESSION_URL}" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' >/dev/null
curl -sS -X POST "http://127.0.0.1:18083${SESSION_URL}" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"run_tests","arguments":{}}}' >/dev/null
sleep 2
kill $SSEPID 2>/dev/null || true
echo '--- tool names ---'
grep -o '"name":"[^"]*"' /tmp/sandbox-verify-sse.txt | sort -u
echo '--- run_tests response ---'
grep -c 'PASS' /tmp/sandbox-verify-sse.txt || echo "FAIL: no PASS in run_tests response"
```
Expected output includes `run_lint`, `run_tests`, `run_typecheck`, and at least one `PASS` from the Go test run.

- [ ] **Step 4: Stop the server**

```bash
kill "$(cat /tmp/sandbox-verify-smoke.pid)" 2>/dev/null || true
```

- [ ] **Step 5: No commit** (no file changes).

---

## Self-Review Notes

**Spec coverage:**
- Project-type detection (`go.mod` → Go) — Task 1.
- `run_tests` — Task 2.
- `run_lint` with structured `file:line:rule:message` output — Task 3.
- `run_typecheck` — Task 4.
- Post-edit lint feedback wired into `Edit` — Task 5.
- End-to-end wire verification — Task 6.

**Deliberately deferred:**
- Non-Go detectors (Node/Python/Rust) — follow-up plans; `Detector` interface is designed for trivial extension.
- Structured parsing for `run_tests` and `run_typecheck` — raw output + exit line for v1.
- Background / watch modes — agents invoke each tool per request.
- Caching of recent lint runs — each `Edit` pays a fresh lint cost; acceptable at v1 latencies on small modules.
- Lint-fix / code-action loop — Edit reports, agents act.

**Placeholder scan:** no TBDs, no "implement later", no "handle edge cases" — every step shows the code or exact commands to run.

**Type consistency:**
- `Detector` interface's method names match across `verify.go`, `golang.go`, and each handler's call-sites.
- `execResult` struct fields (`Stdout`, `Stderr`, `ExitCode`, `TimedOut`) match across `runVerifyCmd`, `formatVerifyResult`, and each caller.
- `LintFinding` fields (`File`, `Line`, `Column`, `Rule`, `Message`) match across `lint.go`, `run_lint.go`, and the Edit post-edit helper.
- Constants `defaultRunTestsTimeoutSec`, `maxRunTestsTimeoutSec`, `defaultRunLintTimeoutSec`, `maxRunLintTimeoutSec`, `defaultRunTypecheckTimeoutSec`, `maxRunTypecheckTimeoutSec`, `postEditLintTimeoutSec` — consistent lowercase camelCase, one pair per tool.
- `verifyOutputCapBytes`, `verifyTimeoutExitCode` — shared across verify tools in `exec.go`.

**Cross-cutting:**
- Process-group kill pattern (`Setpgid` + `cmd.Cancel` + `WaitDelay`) reused from `bash.go` and introduced in one place (`exec.go` for the verify tools; `lint.go` for the lint-specific helper).
- `truncateOutput` from `bash.go` is reused in `exec.go` — no redeclaration.
- Live linter tests use a `requireGolangciLint`/`requireGolangciLintTool` skip helper so the suite stays green on machines without the linter installed.
