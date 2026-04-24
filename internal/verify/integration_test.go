//go:build integration

// Integration tests that run real `golangci-lint` against a seeded
// project and feed its output through the detector's ParseLint.
//
// Unit coverage in this package asserts on canned output strings. This
// tier asserts on what the live binary actually emits, so a linter
// upgrade that changes its formatting is caught at CI time rather than
// in production.

package verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireGolangciLint(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not on PATH; skipping real-linter integration test")
	}
}

// TestRealGolangciLint_ParsesOutput seeds a minimal Go module with one
// guaranteed errcheck finding, runs the real linter against it, and
// confirms that goDetector.ParseLint surfaces at least one finding
// pointing at bad.go. Exact line / message text is not asserted — those
// vary across golangci-lint versions — but file + rule are stable.
func TestRealGolangciLint_ParsesOutput(t *testing.T) {
	requireGolangciLint(t)

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module probe\n\ngo 1.21\n"), 0o644))
	// default: none + enable errcheck gives deterministic output that
	// doesn't depend on the upstream linter's default set.
	require.NoError(t, os.WriteFile(filepath.Join(root, ".golangci.yml"),
		[]byte("version: \"2\"\nlinters:\n  default: none\n  enable:\n    - errcheck\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bad.go"), []byte(
		"package probe\n\nimport \"os\"\n\nfunc writeErr() { os.WriteFile(\"x\", []byte(\"y\"), 0o644) }\n",
	), 0o644))

	// Run the linter directly — the public Lint() helper wraps this but
	// we want to feed the raw output through the detector's parser to
	// verify the parse, not the wrapper.
	cmd := exec.Command("golangci-lint", "run", "--output.text.path=stdout", "./...")
	cmd.Dir = root
	out, runErr := cmd.CombinedOutput()
	// Exit 1 is golangci-lint's "findings exist" convention — not a
	// test failure. Exit >= 2 is real trouble.
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); !ok || ee.ExitCode() >= 2 {
			t.Fatalf("golangci-lint failed: %v\n%s", runErr, string(out))
		}
	}

	det := &goDetector{}
	findings := det.ParseLint(string(out), "")
	require.NotEmpty(t, findings, "golangci-lint emitted no parseable findings — output was:\n%s", out)

	seenBad := false
	for _, f := range findings {
		if filepath.Base(f.File) == "bad.go" && f.Rule == "errcheck" {
			seenBad = true
			assert.Positive(t, f.Line, "expected 1-based line number on bad.go finding")
			break
		}
	}
	assert.True(t, seenBad, "no errcheck finding on bad.go in parsed findings: %+v", findings)
}
