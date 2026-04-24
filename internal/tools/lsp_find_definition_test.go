package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/lsp"
	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindDefinition_Happy(t *testing.T) {
	deps, root := newLSPTestDeps(t, "definition")
	writeSeedGoFile(t, root, "a.go", "package testmod\n\nfunc ValidateToken() {}\n")

	res := callLSP(t, tools.HandleFindDefinition(deps), map[string]any{
		"file_path": "a.go",
		"line":      float64(3),
		"col":       float64(6),
	})
	require.False(t, res.IsError, textOf(t, res))
	body := textOf(t, res)
	mustContain(t, body, "Found 1 definition")
	mustContain(t, body, "a.go:42:3")
}

func TestFindDefinition_MissingArgs(t *testing.T) {
	deps, _ := newLSPTestDeps(t, "definition")
	res := callLSP(t, tools.HandleFindDefinition(deps), map[string]any{})
	assert.True(t, res.IsError)
}

func TestFindDefinition_OutsideWorkspace(t *testing.T) {
	deps, _ := newLSPTestDeps(t, "definition")
	res := callLSP(t, tools.HandleFindDefinition(deps), map[string]any{
		"file_path": "/etc/passwd",
		"line":      float64(1),
		"col":       float64(1),
	})
	assert.True(t, res.IsError)
}

func TestFindDefinition_LSPMissing(t *testing.T) {
	// Construct deps with a registry that returns a non-existent binary so
	// we exercise the "not found on PATH → gopls hint" branch.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644))
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	reg := lsp.NewRegistry(func(lang string) []string {
		if lang == "go" {
			return []string{"this-binary-definitely-doesnt-exist-xyz"}
		}
		return nil
	}, 0)
	t.Cleanup(func() { reg.Shutdown(context.Background()) })

	deps := &tools.Deps{Workspace: ws, Tracker: workspace.NewReadTracker(), LSPRegistry: reg}
	writeSeedGoFile(t, dir, "a.go", "package x\n")

	res := callLSP(t, tools.HandleFindDefinition(deps), map[string]any{
		"file_path": "a.go",
		"line":      float64(1),
		"col":       float64(1),
	})
	assert.True(t, res.IsError)
	mustContain(t, textOf(t, res), "gopls")
}

func TestFindDefinition_NoDetector(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)
	reg := lsp.NewRegistry(func(string) []string { return nil }, 0)
	deps := &tools.Deps{Workspace: ws, Tracker: workspace.NewReadTracker(), LSPRegistry: reg}
	writeSeedGoFile(t, dir, "a.txt", "hello")

	res := callLSP(t, tools.HandleFindDefinition(deps), map[string]any{
		"file_path": "a.txt",
		"line":      float64(1),
		"col":       float64(1),
	})
	assert.True(t, res.IsError)
	mustContain(t, textOf(t, res), "no language detected")
}
