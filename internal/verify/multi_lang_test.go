package verify_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedMarker(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("marker\n"), 0o644))
	return dir
}

func TestDetect_NodeProject(t *testing.T) {
	d := verify.Detect(seedMarker(t, "package.json"))
	require.NotNil(t, d)
	assert.Equal(t, "node", d.Language())
	assert.Equal(t, []string{"npm", "test", "--silent"}, d.TestCmd())
	assert.Equal(t, []string{"npx", "--no-install", "eslint", ".", "--format=compact"}, d.LintCmd())
	assert.Equal(t, []string{"npx", "--no-install", "tsc", "--noEmit"}, d.TypecheckCmd())
}

func TestDetect_PythonProject_Pyproject(t *testing.T) {
	d := verify.Detect(seedMarker(t, "pyproject.toml"))
	require.NotNil(t, d)
	assert.Equal(t, "python", d.Language())
	assert.Equal(t, []string{"pytest"}, d.TestCmd())
	assert.Equal(t, []string{"ruff", "check", "."}, d.LintCmd())
	assert.Equal(t, []string{"mypy", "."}, d.TypecheckCmd())
}

func TestDetect_PythonProject_SetupPy(t *testing.T) {
	d := verify.Detect(seedMarker(t, "setup.py"))
	require.NotNil(t, d)
	assert.Equal(t, "python", d.Language())
}

func TestDetect_RustProject(t *testing.T) {
	d := verify.Detect(seedMarker(t, "Cargo.toml"))
	require.NotNil(t, d)
	assert.Equal(t, "rust", d.Language())
	assert.Equal(t, []string{"cargo", "test"}, d.TestCmd())
	assert.Equal(t, []string{"cargo", "clippy", "--all-targets", "--", "-D", "warnings"}, d.LintCmd())
	assert.Equal(t, []string{"cargo", "check", "--all-targets"}, d.TypecheckCmd())
}

func TestDetect_GoWinsOverOthers(t *testing.T) {
	dir := t.TempDir()
	// A Go service with a frontend: both markers present.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}\n"), 0o644))

	d := verify.Detect(dir)
	require.NotNil(t, d)
	assert.Equal(t, "go", d.Language(), "Go must win when both markers are present (first in the fixed order)")
}

func TestDetect_UnknownMarkerReturnsNil(t *testing.T) {
	assert.Nil(t, verify.Detect(seedMarker(t, "Makefile")))
}
