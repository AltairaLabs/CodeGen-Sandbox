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
	assert.Equal(t, []string{"npx", "--no-install", "eslint", ".", "--format=json"}, d.LintCmd())
	assert.Equal(t, []string{"npx", "--no-install", "tsc", "--noEmit"}, d.TypecheckCmd())
}

func TestDetect_PythonProject_Pyproject(t *testing.T) {
	d := verify.Detect(seedMarker(t, "pyproject.toml"))
	require.NotNil(t, d)
	assert.Equal(t, "python", d.Language())
	assert.Equal(t, []string{"pytest"}, d.TestCmd())
	assert.Equal(t, []string{"ruff", "check", "--output-format=concise", "."}, d.LintCmd())
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
	assert.Equal(t, []string{"cargo", "clippy", "--all-targets", "--message-format=short", "--", "-D", "warnings"}, d.LintCmd())
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

// TestDetect_FormatCheckCmd covers the per-language argv returned by
// FormatCheckCmd. Go returns nil (its lint path already covers formatting
// drift); Python/Node/Rust return a formatter invocation targeting the
// single edited file. No subprocess is spawned here — the check is that
// each detector returns the shape the post-edit format hook expects.
func TestDetect_FormatCheckCmd(t *testing.T) {
	cases := []struct {
		name     string
		marker   string
		file     string
		expected []string
	}{
		{
			name:     "go returns nil (lint path covers formatting)",
			marker:   "go.mod",
			file:     "foo.go",
			expected: nil,
		},
		{
			name:     "python uses ruff format --check --diff",
			marker:   "pyproject.toml",
			file:     "app/main.py",
			expected: []string{"ruff", "format", "--check", "--diff", "app/main.py"},
		},
		{
			name:     "node uses prettier --check",
			marker:   "package.json",
			file:     "src/index.ts",
			expected: []string{"prettier", "--check", "src/index.ts"},
		},
		{
			name:     "rust uses rustfmt --check",
			marker:   "Cargo.toml",
			file:     "src/lib.rs",
			expected: []string{"rustfmt", "--check", "src/lib.rs"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := verify.Detect(seedMarker(t, tc.marker))
			require.NotNil(t, d)
			assert.Equal(t, tc.expected, d.FormatCheckCmd(tc.file))
		})
	}
}
