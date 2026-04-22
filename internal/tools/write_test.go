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

func callWrite(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleWrite(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestWrite_CreateNewFile(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "sub", "new.txt")

	res := callWrite(t, deps, map[string]any{"file_path": path, "content": "hello"})
	require.False(t, res.IsError, "unexpected error: %v", res.Content)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
	assert.True(t, deps.Tracker.HasBeenRead(path), "Write should mark file as read")
}

func TestWrite_OverwriteRequiresPriorRead(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "existing.txt")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))

	res := callWrite(t, deps, map[string]any{"file_path": path, "content": "new"})
	assert.True(t, res.IsError, "overwrite without prior read must fail")

	data, _ := os.ReadFile(path)
	assert.Equal(t, "old", string(data), "file must not be modified on error")
}

func TestWrite_OverwriteAfterRead(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "existing.txt")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))
	deps.Tracker.MarkRead(path)

	res := callWrite(t, deps, map[string]any{"file_path": path, "content": "new"})
	require.False(t, res.IsError)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "new", string(data))
}

func TestWrite_MissingFilePath(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callWrite(t, deps, map[string]any{"content": "x"})
	assert.True(t, res.IsError)
}

func TestWrite_MissingContent(t *testing.T) {
	deps, root := newTestDeps(t)
	res := callWrite(t, deps, map[string]any{"file_path": filepath.Join(root, "x.txt")})
	assert.True(t, res.IsError)
}

func TestWrite_PathOutsideWorkspace(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callWrite(t, deps, map[string]any{"file_path": "/etc/evil", "content": "x"})
	assert.True(t, res.IsError)
}

func TestWrite_RejectsDirectoryTarget(t *testing.T) {
	deps, root := newTestDeps(t)
	sub := filepath.Join(root, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	deps.Tracker.MarkRead(sub) // even with prior "read", a dir target is invalid

	res := callWrite(t, deps, map[string]any{"file_path": sub, "content": "x"})
	assert.True(t, res.IsError)
}
