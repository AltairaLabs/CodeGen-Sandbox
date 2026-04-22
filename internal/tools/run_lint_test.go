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

func requireGolangciLintTool(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not on PATH; skipping")
	}
}

func callRunLint(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleRunLint(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

// seedLintableModule writes a go.mod, a .golangci.yml enabling errcheck,
// and a bad.go file with an unchecked error (guaranteed errcheck finding).
func seedLintableModule(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".golangci.yml"), []byte(
		"version: \"2\"\nlinters:\n  default: none\n  enable:\n    - errcheck\n",
	), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bad.go"), []byte(
		"package probe\n\nimport \"os\"\n\nfunc writeErr() { os.WriteFile(\"x\", []byte(\"y\"), 0o644) }\n",
	), 0o644))
}

func TestRunLint_NoDetectorIsError(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callRunLint(t, deps, map[string]any{})
	assert.True(t, res.IsError)
}

func TestRunLint_MissingBinaryNamesIt(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644))
	t.Setenv("PATH", t.TempDir()) // empty PATH — golangci-lint unreachable

	res := callRunLint(t, deps, map[string]any{})
	assert.True(t, res.IsError)
	// Plan-specified wording: "linter not installed: <binary>". The binary
	// name lets an operator tell whether it's a dev-env or Docker-image
	// misconfiguration.
	assert.Contains(t, textOf(t, res), "golangci-lint")
}

func TestRunLint_FindingsPrintedAsStructured(t *testing.T) {
	requireGolangciLintTool(t)
	deps, root := newTestDeps(t)
	seedLintableModule(t, root)

	res := callRunLint(t, deps, map[string]any{})
	require.False(t, res.IsError, "unexpected error: %s", textOf(t, res))

	body := textOf(t, res)
	assert.Contains(t, body, "bad.go:")
	assert.Contains(t, body, "errcheck:")
	// Final summary line.
	assert.Contains(t, body, "findings")
}

func TestRunLint_CleanModuleReturnsZeroFindings(t *testing.T) {
	requireGolangciLintTool(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".golangci.yml"), []byte(
		"version: \"2\"\nlinters:\n  default: none\n  enable:\n    - errcheck\n",
	), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "good.go"), []byte(
		"package probe\n\nfunc Add(a, b int) int { return a + b }\n",
	), 0o644))

	res := callRunLint(t, deps, map[string]any{})
	require.False(t, res.IsError)
	assert.Contains(t, textOf(t, res), "0 findings")
}
