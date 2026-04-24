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

func callRunScript(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleRunScript(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

// writeScriptStub installs a shell-script stub named `name` on PATH that
// exits 0 and echoes its first argument (so tests can verify the correct
// script name was forwarded). Returns the bin dir to prepend to PATH.
func writeScriptStub(t *testing.T, name string) string {
	t.Helper()
	binDir := t.TempDir()
	script := "#!/bin/sh\necho \"invoked:$@\"\nexit 0\n"
	stubPath := filepath.Join(binDir, name)
	require.NoError(t, os.WriteFile(stubPath, []byte(script), 0o755))
	return binDir
}

func TestRunScript_NoDetectorIsError(t *testing.T) {
	deps, _ := newTestDeps(t) // empty workspace, no markers
	res := callRunScript(t, deps, map[string]any{"name": "build"})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "no supported project")
}

func TestRunScript_NonNodeDetectorRejected(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n"), 0o644))

	res := callRunScript(t, deps, map[string]any{"name": "build"})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "only Node projects support scripts")
	assert.Contains(t, textOf(t, res), "go")
}

func TestRunScript_MissingNameArg(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"build":"next build"}}`), 0o644))

	res := callRunScript(t, deps, map[string]any{})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "name is required")
}

func TestRunScript_EmptyNameRejected(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"build":"next build"}}`), 0o644))

	res := callRunScript(t, deps, map[string]any{"name": "   "})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "name is required")
}

func TestRunScript_UnknownScriptListsAvailable(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{"scripts":{"build":"next build","dev":"next dev","lint":"eslint ."}}`), 0o644))

	res := callRunScript(t, deps, map[string]any{"name": "nonexistent"})
	assert.True(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "no script named")
	assert.Contains(t, body, `"nonexistent"`)
	// Alphabetised list:
	assert.Contains(t, body, "build, dev, lint")
}

func TestRunScript_UnknownScriptEmptyScriptsSection(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{"name":"probe"}`), 0o644))

	res := callRunScript(t, deps, map[string]any{"name": "build"})
	assert.True(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "no script named")
	assert.Contains(t, body, "(none defined)")
}

func TestRunScript_MalformedPackageJSONSurfacesParseError(t *testing.T) {
	deps, root := newTestDeps(t)
	// Just { will trip the JSON parser. Detector tolerates this (no
	// scripts), but run_script reads package.json again and surfaces
	// the parse error explicitly so agents understand why their
	// script lookup failed.
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte("{ not json"), 0o644))

	res := callRunScript(t, deps, map[string]any{"name": "build"})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "parsing package.json")
}

func TestRunScript_HappyPath_NpmStub(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{"scripts":{"build":"next build"}}`), 0o644))
	binDir := writeScriptStub(t, "npm")
	t.Setenv("PATH", binDir)

	res := callRunScript(t, deps, map[string]any{"name": "build"})
	require.False(t, res.IsError, "unexpected error: %s", textOf(t, res))
	body := textOf(t, res)
	assert.Contains(t, body, "exit: 0")
	// Stub echoes "invoked:<args>" — verify the script-name hit.
	assert.Contains(t, body, "invoked:run build")
}

func TestRunScript_HappyPath_PnpmStub(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{"scripts":{"test":"jest"}}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pnpm-lock.yaml"), []byte("lock"), 0o644))
	binDir := writeScriptStub(t, "pnpm")
	t.Setenv("PATH", binDir)

	res := callRunScript(t, deps, map[string]any{"name": "test"})
	require.False(t, res.IsError, "unexpected error: %s", textOf(t, res))
	body := textOf(t, res)
	assert.Contains(t, body, "exit: 0")
	assert.Contains(t, body, "invoked:run test")
}

func TestRunScript_HappyPath_YarnOmitsRun(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{"scripts":{"dev":"next dev"}}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "yarn.lock"), []byte("lock"), 0o644))
	binDir := writeScriptStub(t, "yarn")
	t.Setenv("PATH", binDir)

	res := callRunScript(t, deps, map[string]any{"name": "dev"})
	require.False(t, res.IsError, "unexpected error: %s", textOf(t, res))
	body := textOf(t, res)
	assert.Contains(t, body, "exit: 0")
	// yarn doesn't use "run" — stub invocation should see "dev" as
	// the first positional arg.
	assert.Contains(t, body, "invoked:dev")
}

func TestRunScript_TimeoutDefaultAndClamp(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{"scripts":{"build":"next build"}}`), 0o644))
	binDir := writeScriptStub(t, "npm")
	t.Setenv("PATH", binDir)

	// Huge value gets clamped to maxRunScriptTimeoutSec without error.
	res := callRunScript(t, deps, map[string]any{"name": "build", "timeout": float64(1_000_000)})
	require.False(t, res.IsError, "unexpected error: %s", textOf(t, res))
	assert.Contains(t, textOf(t, res), "exit: 0")

	// Zero / negative values fall back to the default.
	res = callRunScript(t, deps, map[string]any{"name": "build", "timeout": float64(-5)})
	require.False(t, res.IsError)
	assert.Contains(t, textOf(t, res), "exit: 0")
}

func TestRunScript_BinaryNotOnPATH_SurfaceMissing(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{"scripts":{"build":"next build"}}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pnpm-lock.yaml"), []byte("lock"), 0o644))
	// Empty PATH — pnpm can't be found.
	t.Setenv("PATH", t.TempDir())

	res := callRunScript(t, deps, map[string]any{"name": "build"})
	assert.True(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "pnpm")
	assert.Contains(t, body, "not found on PATH")
}
