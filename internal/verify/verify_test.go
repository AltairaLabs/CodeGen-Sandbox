package verify_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetect_NoMarkerReturnsNil(t *testing.T) {
	dir := t.TempDir()
	assert.Nil(t, verify.Detect(dir))
}

func TestDetect_GoModuleReturnsGoDetector(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\n"), 0o644))

	d := verify.Detect(dir)
	require.NotNil(t, d)
	assert.Equal(t, "go", d.Language())
}

func TestDetect_OnlyRootMarkerCounts(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nested")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "go.mod"), []byte("module probe\n"), 0o644))

	assert.Nil(t, verify.Detect(dir), "a go.mod in a subdirectory must not be detected as the workspace project")
}

func TestGoDetector_Commands(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\n"), 0o644))

	d := verify.Detect(dir)
	require.NotNil(t, d)
	assert.Equal(t, []string{"go", "test", "./..."}, d.TestCmd())
	assert.Equal(t, []string{"golangci-lint", "run", "./..."}, d.LintCmd())
	assert.Equal(t, []string{"go", "vet", "./..."}, d.TypecheckCmd())
}
