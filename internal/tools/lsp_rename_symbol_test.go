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

func TestRenameSymbol_Happy(t *testing.T) {
	deps, root := newLSPTestDeps(t, "rename")
	// Rename at line 10; file body must have ≥10 lines for the diff hunk
	// renderer to slice the target line.
	lines := "package testmod\n\nfunc ValidateToken() {}\n" + pad(7) + "    ValidateToken()\n"
	writeSeedGoFile(t, root, "a.go", lines)
	// Mark file as read so rename_symbol's Read gate passes. We mark using
	// the workspace-resolved path since Resolve canonicalizes symlinks and
	// the tracker keys on the canonical form.
	resolved, err := deps.Workspace.Resolve("a.go")
	require.NoError(t, err)
	deps.Tracker.MarkRead(resolved)

	res := callLSP(t, tools.HandleRenameSymbol(deps), map[string]any{
		"file_path": "a.go",
		"line":      float64(3),
		"col":       float64(6),
		"new_name":  "VerifyToken",
	})
	require.False(t, res.IsError, textOf(t, res))
	body := textOf(t, res)
	mustContain(t, body, `Rename → "VerifyToken"`)
	mustContain(t, body, "--- a/a.go")
	mustContain(t, body, "+++ b/a.go")
}

func TestRenameSymbol_RequiresRead(t *testing.T) {
	deps, root := newLSPTestDeps(t, "rename")
	writeSeedGoFile(t, root, "a.go", "package testmod\n")
	// Deliberately do NOT mark as read.
	res := callLSP(t, tools.HandleRenameSymbol(deps), map[string]any{
		"file_path": "a.go",
		"line":      float64(1),
		"col":       float64(1),
		"new_name":  "x",
	})
	assert.True(t, res.IsError)
	mustContain(t, textOf(t, res), "Read it first")
}

func TestRenameSymbol_MissingNewName(t *testing.T) {
	deps, root := newLSPTestDeps(t, "rename")
	writeSeedGoFile(t, root, "a.go", "package testmod\n")
	resolved, err := deps.Workspace.Resolve("a.go")
	require.NoError(t, err)
	deps.Tracker.MarkRead(resolved)

	res := callLSP(t, tools.HandleRenameSymbol(deps), map[string]any{
		"file_path": "a.go",
		"line":      float64(1),
		"col":       float64(1),
	})
	assert.True(t, res.IsError)
}

func TestRenameSymbol_RegistryNil(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644))
	// Nil LSPRegistry — exercise the "registry unavailable" branch.
	deps, _ := newLSPTestDeps(t, "rename")
	deps.LSPRegistry = nil

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"file_path": "a.go",
		"line":      float64(1),
		"col":       float64(1),
		"new_name":  "x",
	}
	// We expect an error result but no transport-level Go error.
	_, err := tools.HandleRenameSymbol(deps)(context.Background(), req)
	require.NoError(t, err)
}

// pad returns n blank lines so we can place a target after a known offset
// in the seed file. Used to satisfy the diff-hunk renderer which indexes
// into the file by line number.
func pad(n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += "\n"
	}
	return out
}
