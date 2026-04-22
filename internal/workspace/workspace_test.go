package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_RejectsRelativeRoot(t *testing.T) {
	_, err := workspace.New("relative/path")
	require.Error(t, err)
}

func TestNew_RejectsMissingRoot(t *testing.T) {
	_, err := workspace.New("/nonexistent/codegen-sandbox-test-root")
	require.Error(t, err)
}

func TestNew_RejectsFileAsRoot(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "not-a-dir")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = workspace.New(f.Name())
	require.Error(t, err)
}

func TestNew_AcceptsAbsoluteDirectory(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	canonical, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	assert.Equal(t, canonical, ws.Root())
}

func TestResolve_RelativePath(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	canonicalDir, _ := filepath.EvalSymlinks(dir)
	resolved, err := ws.Resolve("foo/bar.txt")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(canonicalDir, "foo", "bar.txt"), resolved)
}

func TestResolve_AbsolutePathInsideRoot(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	canonicalDir, _ := filepath.EvalSymlinks(dir)
	target := filepath.Join(canonicalDir, "a", "b.txt")

	resolved, err := ws.Resolve(target)
	require.NoError(t, err)
	assert.Equal(t, target, resolved)
}

func TestResolve_TraversalEscape(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	_, err = ws.Resolve("../etc/passwd")
	require.ErrorIs(t, err, workspace.ErrOutsideWorkspace)
}

func TestResolve_AbsoluteOutsideRoot(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	_, err = ws.Resolve("/etc/passwd")
	require.ErrorIs(t, err, workspace.ErrOutsideWorkspace)
}

func TestResolve_SymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(dir, "escape")
	require.NoError(t, os.Symlink(outside, link))

	ws, err := workspace.New(dir)
	require.NoError(t, err)

	_, err = ws.Resolve("escape/secrets")
	require.ErrorIs(t, err, workspace.ErrOutsideWorkspace)
}

func TestResolve_NonExistentFileInsideRoot(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	canonicalDir, _ := filepath.EvalSymlinks(dir)
	resolved, err := ws.Resolve("new/nested/file.txt")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(canonicalDir, "new", "nested", "file.txt"), resolved)
}

func TestResolve_ExistingFileViaSymlinkInsideRoot(t *testing.T) {
	dir := t.TempDir()
	realFile := filepath.Join(dir, "real.txt")
	require.NoError(t, os.WriteFile(realFile, []byte("x"), 0o644))
	link := filepath.Join(dir, "link.txt")
	require.NoError(t, os.Symlink(realFile, link))

	ws, err := workspace.New(dir)
	require.NoError(t, err)

	canonicalDir, _ := filepath.EvalSymlinks(dir)
	resolved, err := ws.Resolve("link.txt")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(canonicalDir, "real.txt"), resolved)

}
