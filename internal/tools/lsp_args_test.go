package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// argErrorCases covers the argument-validation paths shared across
// find_definition / find_references / rename_symbol. Dispatch is
// centralised in lsp_common.go so hitting them via any of the three
// handlers is sufficient.
func callFindDefinition(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleFindDefinition(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func callFindReferences(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleFindReferences(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func callRenameSymbol(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleRenameSymbol(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func bareLSPDeps(t *testing.T) *tools.Deps {
	t.Helper()
	ws, err := workspace.New(t.TempDir())
	require.NoError(t, err)
	return &tools.Deps{Workspace: ws}
}

func TestLSP_MissingFilePath(t *testing.T) {
	deps := bareLSPDeps(t)
	for name, call := range map[string]func(*testing.T, *tools.Deps, map[string]any) *mcp.CallToolResult{
		"find_definition": callFindDefinition,
		"find_references": callFindReferences,
		"rename_symbol": func(t *testing.T, d *tools.Deps, args map[string]any) *mcp.CallToolResult {
			args["new_name"] = "Whatever"
			return callRenameSymbol(t, d, args)
		},
	} {
		t.Run(name, func(t *testing.T) {
			res := call(t, deps, map[string]any{"line": float64(1), "col": float64(1)})
			assert.True(t, res.IsError)
			assert.Contains(t, textOf(t, res), "file_path")
		})
	}
}

func TestLSP_MissingOrInvalidLine(t *testing.T) {
	deps := bareLSPDeps(t)
	res := callFindDefinition(t, deps, map[string]any{"file_path": "foo.go", "col": float64(1)})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "line")

	res = callFindDefinition(t, deps, map[string]any{"file_path": "foo.go", "line": float64(0), "col": float64(1)})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "line")
}

func TestLSP_MissingOrInvalidCol(t *testing.T) {
	deps := bareLSPDeps(t)
	res := callFindReferences(t, deps, map[string]any{"file_path": "foo.go", "line": float64(1)})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "col")

	res = callFindReferences(t, deps, map[string]any{"file_path": "foo.go", "line": float64(1), "col": float64(-5)})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "col")
}

func TestRenameSymbol_MissingNewNameArg(t *testing.T) {
	deps := bareLSPDeps(t)
	res := callRenameSymbol(t, deps, map[string]any{
		"file_path": "foo.go", "line": float64(1), "col": float64(1),
	})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "new_name")
}

// TestLSP_UnresolvableFilePath exercises the "path outside workspace"
// / stat-missing / is-directory branches of resolveLSPFile. Without a
// recognised project in the workspace the Detector step fails first,
// but we want to hit the file-resolve step — so seed a go.mod.
func TestLSP_UnresolvableFilePath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\ngo 1.25\n"), 0o644))
	ws, err := workspace.New(dir)
	require.NoError(t, err)
	deps := &tools.Deps{Workspace: ws}

	t.Run("outside workspace", func(t *testing.T) {
		res := callFindDefinition(t, deps, map[string]any{
			"file_path": "../../etc/passwd", "line": float64(1), "col": float64(1),
		})
		assert.True(t, res.IsError)
	})

	t.Run("nonexistent file", func(t *testing.T) {
		res := callFindDefinition(t, deps, map[string]any{
			"file_path": "nope.go", "line": float64(1), "col": float64(1),
		})
		assert.True(t, res.IsError)
		assert.Contains(t, textOf(t, res), "stat")
	})

	t.Run("directory not file", func(t *testing.T) {
		sub := filepath.Join(dir, "sub")
		require.NoError(t, os.Mkdir(sub, 0o755))
		res := callFindDefinition(t, deps, map[string]any{
			"file_path": "sub", "line": float64(1), "col": float64(1),
		})
		assert.True(t, res.IsError)
		assert.Contains(t, textOf(t, res), "directory")
	})
}
