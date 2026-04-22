package tools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestBash_TimeoutKillsBackgroundedChildren(t *testing.T) {
	requireBash(t)
	deps, _ := newTestDeps(t)

	// Without a process-group kill, `sleep 10 & wait` ignores the timeout —
	// SIGKILLing only the bash PID leaves the sleep child running, and
	// CombinedOutput blocks on the still-open inherited pipe until sleep
	// completes 10s later. This test locks in the whole-group-kill contract.
	start := time.Now()
	res := callBash(t, deps, map[string]any{
		"command":     "sleep 10 & wait",
		"description": "backgrounded sleep must be killed on timeout",
		"timeout":     float64(1),
	})
	elapsed := time.Since(start)

	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "timed out after 1s")
	assert.Contains(t, body, "exit: 124", "timeout exit code must follow the timeout(1) convention")
	assert.Less(t, elapsed, 5*time.Second, "timeout must kill the process group, not wait for backgrounded children")
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
