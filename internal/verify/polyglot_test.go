package verify_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDetectAll_EmptyWorkspaceReturnsEmptySlice — zero detectors is the
// tool-dispatch "no supported project" signal.
func TestDetectAll_EmptyWorkspaceReturnsEmptySlice(t *testing.T) {
	assert.Empty(t, verify.DetectAll(t.TempDir()))
}

// TestDetectAll_SingleMarkerReturnsOneDetector — single-language workspaces
// get the pre-polyglot behaviour: exactly one detector.
func TestDetectAll_SingleMarkerReturnsOneDetector(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\n"), 0o644))
	d := verify.DetectAll(dir)
	require.Len(t, d, 1)
	assert.Equal(t, "go", d[0].Language())
}

// TestDetectAll_AllFourMarkersReturnsAllDetectorsInStableOrder — the
// polyglot contract: every marker present → every detector returned, in
// Go → Rust → Node → Python order. Locks the ordering so error-message
// enumerations stay deterministic across refactors.
func TestDetectAll_AllFourMarkersReturnsAllDetectorsInStableOrder(t *testing.T) {
	dir := t.TempDir()
	for _, marker := range []string{"go.mod", "Cargo.toml", "package.json", "pyproject.toml"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, marker), []byte("{}"), 0o644))
	}

	d := verify.DetectAll(dir)
	require.Len(t, d, 4)
	assert.Equal(t, []string{"go", "rust", "node", "python"},
		[]string{d[0].Language(), d[1].Language(), d[2].Language(), d[3].Language()})
}

// TestDetectAll_GoPlusNodeMonorepoIsTheCanonicalPolyglotCase — the exact
// scenario #19 calls out (Go service with a frontend package.json). Both
// detectors present means an agent can target either via `language`.
func TestDetectAll_GoPlusNodeMonorepoIsTheCanonicalPolyglotCase(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module svc\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"frontend"}`), 0o644))

	d := verify.DetectAll(dir)
	require.Len(t, d, 2)
	assert.Equal(t, "go", d[0].Language())
	assert.Equal(t, "node", d[1].Language())
}

// TestDetect_FirstMatchSemanticsPreserved — Detect is the first-of-DetectAll
// shim; the polyglot refactor must not change what it returns for
// single-language workspaces OR what order it picks in polyglot ones.
func TestDetect_FirstMatchSemanticsPreserved(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module svc\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"frontend"}`), 0o644))

	d := verify.Detect(dir)
	require.NotNil(t, d)
	assert.Equal(t, "go", d.Language(), "Detect must still return the first detector in DetectAll's order")
}
