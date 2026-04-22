package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDeps(t *testing.T) (*tools.Deps, string) {
	t.Helper()
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)
	return &tools.Deps{Workspace: ws, Tracker: workspace.NewReadTracker()}, ws.Root()
}

func callRead(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleRead(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestRead_ReturnsNumberedLines(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "hello.txt")
	require.NoError(t, os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644))

	res := callRead(t, deps, map[string]any{"file_path": path})
	require.False(t, res.IsError, "unexpected error: %v", res.Content)

	body := textOf(t, res)
	assert.Contains(t, body, "1\talpha")
	assert.Contains(t, body, "2\tbeta")
	assert.Contains(t, body, "3\tgamma")
}

func TestRead_MarksFileAsRead(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "seen.txt")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	_ = callRead(t, deps, map[string]any{"file_path": path})
	assert.True(t, deps.Tracker.HasBeenRead(path))
}

func TestRead_MissingFilePath(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callRead(t, deps, map[string]any{})
	assert.True(t, res.IsError)
}

func TestRead_PathOutsideWorkspace(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callRead(t, deps, map[string]any{"file_path": "/etc/passwd"})
	assert.True(t, res.IsError)
}

func TestRead_FileNotFound(t *testing.T) {
	deps, root := newTestDeps(t)
	res := callRead(t, deps, map[string]any{"file_path": filepath.Join(root, "missing.txt")})
	assert.True(t, res.IsError)
}

func TestRead_DirectoryIsError(t *testing.T) {
	deps, root := newTestDeps(t)
	res := callRead(t, deps, map[string]any{"file_path": root})
	assert.True(t, res.IsError)
}

func TestRead_OffsetAndLimit(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "many.txt")
	require.NoError(t, os.WriteFile(path, []byte("l1\nl2\nl3\nl4\nl5\n"), 0o644))

	res := callRead(t, deps, map[string]any{"file_path": path, "offset": float64(2), "limit": float64(2)})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.NotContains(t, body, "1\tl1")
	assert.Contains(t, body, "2\tl2")
	assert.Contains(t, body, "3\tl3")
	assert.NotContains(t, body, "4\tl4")
}

func textOf(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
