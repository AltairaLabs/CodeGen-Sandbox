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
