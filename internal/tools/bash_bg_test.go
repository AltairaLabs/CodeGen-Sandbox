package tools_test

import (
	"context"
	"testing"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newBGDeps returns a Deps with a fresh ShellRegistry so background-mode
// tests don't share state.
func newBGDeps(t *testing.T) (*tools.Deps, string) {
	t.Helper()
	deps, root := newTestDeps(t)
	deps.Shells = tools.NewShellRegistry()
	return deps, root
}

func callBashOutput(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleBashOutput(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func callKillShell(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleKillShell(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

// extractShellID pulls the UUID out of a "shell_id: <uuid>\n..." response.
func extractShellID(t *testing.T, body string) string {
	t.Helper()
	const prefix = "shell_id: "
	i := 0
	for i < len(body) && (body[i] == ' ' || body[i] == '\n') {
		i++
	}
	if len(body) < i+len(prefix) || body[i:i+len(prefix)] != prefix {
		t.Fatalf("expected response to start with %q, got: %q", prefix, body)
	}
	rest := body[i+len(prefix):]
	j := 0
	for j < len(rest) && rest[j] != '\n' {
		j++
	}
	return rest[:j]
}

func TestBash_BackgroundReturnsShellID(t *testing.T) {
	requireBash(t)
	deps, _ := newBGDeps(t)

	res := callBash(t, deps, map[string]any{
		"command":           "echo hi",
		"description":       "bg echo",
		"run_in_background": true,
	})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "shell_id:")
	assert.Contains(t, body, "started in background")
}

func TestBashOutput_CompletedShellReportsExit(t *testing.T) {
	requireBash(t)
	deps, _ := newBGDeps(t)

	start := callBash(t, deps, map[string]any{
		"command":           "echo hello-bg",
		"description":       "bg hello",
		"run_in_background": true,
	})
	require.False(t, start.IsError)
	id := extractShellID(t, textOf(t, start))

	// Wait for the shell to finish. Poll up to 2s.
	deadline := time.Now().Add(2 * time.Second)
	var body string
	for time.Now().Before(deadline) {
		res := callBashOutput(t, deps, map[string]any{"shell_id": id})
		require.False(t, res.IsError)
		body = textOf(t, res)
		if contains(body, "completed") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.Contains(t, body, "completed (exit 0)")
	assert.Contains(t, body, "hello-bg")
}

func TestBashOutput_UnknownShellIDIsError(t *testing.T) {
	deps, _ := newBGDeps(t)
	res := callBashOutput(t, deps, map[string]any{"shell_id": "does-not-exist"})
	assert.True(t, res.IsError)
}

func TestBashOutput_MissingShellIDIsError(t *testing.T) {
	deps, _ := newBGDeps(t)
	res := callBashOutput(t, deps, map[string]any{})
	assert.True(t, res.IsError)
}

func TestKillShell_KillsRunningShell(t *testing.T) {
	requireBash(t)
	deps, _ := newBGDeps(t)

	start := callBash(t, deps, map[string]any{
		"command":           "sleep 60",
		"description":       "long running bg",
		"run_in_background": true,
	})
	require.False(t, start.IsError)
	id := extractShellID(t, textOf(t, start))

	// Give the shell a beat to claim its pgid.
	time.Sleep(200 * time.Millisecond)

	kill := callKillShell(t, deps, map[string]any{"shell_id": id})
	require.False(t, kill.IsError)
	assert.Contains(t, textOf(t, kill), "killed:")

	// After killing, the shell is removed from the registry.
	after := callBashOutput(t, deps, map[string]any{"shell_id": id})
	assert.True(t, after.IsError)
}

func TestKillShell_UnknownShellIDIsError(t *testing.T) {
	deps, _ := newBGDeps(t)
	res := callKillShell(t, deps, map[string]any{"shell_id": "nope"})
	assert.True(t, res.IsError)
}

// contains is a tiny strings.Contains to avoid importing strings just for this.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
