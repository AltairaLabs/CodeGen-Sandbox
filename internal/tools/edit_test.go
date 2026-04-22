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

func callEdit(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleEdit(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func writeAndMarkRead(t *testing.T, deps *tools.Deps, path, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	deps.Tracker.MarkRead(path)
}

func TestEdit_EmptyOldStringRejected(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha\nbeta\n")

	res := callEdit(t, deps, map[string]any{
		"file_path":   path,
		"old_string":  "",
		"new_string":  "X",
		"replace_all": true,
	})
	require.True(t, res.IsError, "empty old_string must be rejected to prevent file corruption")

	data, _ := os.ReadFile(path)
	assert.Equal(t, "alpha\nbeta\n", string(data), "file must not be modified")
}

func TestEdit_ReplacesFirstOccurrence(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha\nbeta\nalpha\n")

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "gamma",
	})
	assert.True(t, res.IsError, "non-unique match without replace_all must fail")
}

func TestEdit_UniqueReplace(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha\nbeta\n")

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "gamma",
	})
	require.False(t, res.IsError, "unexpected error: %v", res.Content)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "gamma\nbeta\n", string(data))
}

func TestEdit_ReplaceAll(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha alpha alpha")

	res := callEdit(t, deps, map[string]any{
		"file_path":   path,
		"old_string":  "alpha",
		"new_string":  "gamma",
		"replace_all": true,
	})
	require.False(t, res.IsError)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "gamma gamma gamma", string(data))
}

func TestEdit_RequiresPriorRead(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	require.NoError(t, os.WriteFile(path, []byte("alpha"), 0o644))
	// intentionally do not mark as read

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "beta",
	})
	assert.True(t, res.IsError)
}

func TestEdit_OldStringNotFound(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha")

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "zulu",
		"new_string": "beta",
	})
	assert.True(t, res.IsError)
}

func TestEdit_MissingFile(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "nope.txt")

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "x",
		"new_string": "y",
	})
	assert.True(t, res.IsError)
}

func TestEdit_PathOutsideWorkspace(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callEdit(t, deps, map[string]any{
		"file_path":  "/etc/passwd",
		"old_string": "root",
		"new_string": "evil",
	})
	assert.True(t, res.IsError)
}

func TestEdit_MissingParams(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha")

	cases := []map[string]any{
		{"old_string": "alpha", "new_string": "beta"},
		{"file_path": path, "new_string": "beta"},
		{"file_path": path, "old_string": "alpha"},
	}
	for _, args := range cases {
		res := callEdit(t, deps, args)
		assert.True(t, res.IsError, "args=%v", args)
	}
}

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
