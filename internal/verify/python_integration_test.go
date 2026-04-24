//go:build integration

// Integration tests that run real `ruff` against a seeded Python
// project and feed its output through pythonDetector.ParseLint.
//
// Unit coverage in this package asserts on canned output strings. This
// tier asserts on what the live binary actually emits, so a ruff
// upgrade that changes its diagnostic format is caught at CI time
// rather than when an operator notices broken `run_lint` output.

package verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireRuff(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ruff"); err != nil {
		t.Skip("ruff not on PATH; skipping real-linter integration test")
	}
}

// TestRealRuff_ParsesOutput seeds a minimal Python project with one
// guaranteed F401 finding (unused import), runs the real linter against
// it, and confirms that pythonDetector.ParseLint surfaces at least one
// finding pointing at bad.py with rule F401. Exact line / column / message
// text isn't asserted — those vary across ruff versions — but file + rule
// are the agent-relevant invariants.
func TestRealRuff_ParsesOutput(t *testing.T) {
	requireRuff(t)

	root := t.TempDir()
	// pyproject.toml with explicit ruff config keeps the rule set stable
	// across ruff defaults changes (the F401 unused-import lint moved
	// between defaults groups historically).
	require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"),
		[]byte(`[project]
name = "probe"
version = "0.0.0"

[tool.ruff.lint]
select = ["F"]
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bad.py"),
		[]byte("import os  # noqa: trailing comment doesn't suppress F401 here\n"), 0o644))

	cmd := exec.Command("ruff", "check", ".")
	cmd.Dir = root
	stdout, runErr := cmd.Output()
	// ruff exits non-zero when findings exist — that's the expected path
	// here, not a test failure. exit >= 2 (and an *exec.ExitError without
	// findings on stdout) is real trouble.
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			t.Fatalf("ruff failed: %v", runErr)
		}
	}

	det := &pythonDetector{}
	findings := det.ParseLint(string(stdout), "")
	require.NotEmpty(t, findings, "ruff emitted no parseable findings — output was:\n%s", stdout)

	var seenBad bool
	for _, f := range findings {
		if filepath.Base(f.File) == "bad.py" && f.Rule == "F401" {
			seenBad = true
			assert.Positive(t, f.Line, "expected 1-based line number on bad.py finding")
			assert.Positive(t, f.Column, "expected 1-based column on bad.py finding")
			break
		}
	}
	assert.True(t, seenBad, "no F401 finding on bad.py in parsed findings: %+v", findings)
}
