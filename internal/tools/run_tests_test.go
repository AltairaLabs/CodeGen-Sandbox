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
// file into root, so `go test ./...` will succeed. When passing=false, the
// test deliberately fails.
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
	// `go test ./...` in non-verbose mode prints "ok  \tprobe\t..." per package
	// rather than "PASS"; check for the package-level ok summary line instead.
	assert.Contains(t, body, "ok")
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
