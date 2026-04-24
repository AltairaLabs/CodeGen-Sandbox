//go:build integration

// Integration tests that run real `eslint` against a seeded JS project
// and feed its output through nodeDetector.ParseLint.
//
// Unit coverage in this package asserts on canned output strings. This
// tier asserts on what the live binary actually emits, so an eslint
// upgrade that changes its `--format=compact` shape is caught at CI time.

package verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireEslint(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("eslint"); err != nil {
		t.Skip("eslint not on PATH (`npm i -g eslint`); skipping real-linter integration test")
	}
}

// TestRealEslint_ParsesOutput seeds a tiny JS project with one
// guaranteed `no-var` finding, runs eslint with the same `--format=compact`
// output the detector expects in production, and confirms that
// nodeDetector.ParseLint surfaces at least one finding on bad.js.
//
// eslint flat config (`eslint.config.mjs`) is used because eslint v9+
// defaults to flat config and refuses to load without one present.
// The config imports `@eslint/js` recommended rules — but to avoid
// requiring a per-project npm install, we instead inline the one rule
// we need (`no-var`). That keeps the test self-contained (no
// node_modules) while still exercising the real binary.
func TestRealEslint_ParsesOutput(t *testing.T) {
	requireEslint(t)

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{
  "name": "probe",
  "version": "0.0.0",
  "private": true,
  "type": "module"
}
`), 0o644))
	// eslint flat config: one rule, no plugins, no globals. Enough to
	// fire `no-var` on a `var` declaration.
	require.NoError(t, os.WriteFile(filepath.Join(root, "eslint.config.mjs"),
		[]byte(`export default [
  {
    rules: {
      "no-var": "error",
    },
  },
];
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bad.js"),
		[]byte("var x = 1;\n"), 0o644))

	// Mirror the detector's LintCmd flags precisely so the parser sees
	// exactly the output it would in production. We invoke the global
	// eslint directly (rather than the `npx --no-install` wrapper the
	// detector uses) so the test doesn't require a per-project
	// node_modules install — `--format=compact` is the only flag that
	// affects the parsed surface.
	cmd := exec.Command("eslint", ".", "--format=compact")
	cmd.Dir = root
	stdout, runErr := cmd.Output()
	// eslint exits 1 when findings exist — that's the expected path here.
	// exit >= 2 is a real error (config invalid, etc).
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); !ok || ee.ExitCode() >= 2 {
			t.Fatalf("eslint failed: %v", runErr)
		}
	}

	det := &nodeDetector{}
	findings := det.ParseLint(string(stdout), "")
	require.NotEmpty(t, findings, "eslint emitted no parseable findings — output was:\n%s", stdout)

	var seenBad bool
	for _, f := range findings {
		if filepath.Base(f.File) == "bad.js" && f.Rule == "no-var" {
			seenBad = true
			assert.Positive(t, f.Line, "expected 1-based line number on bad.js finding")
			assert.Positive(t, f.Column, "expected 1-based column on bad.js finding")
			break
		}
	}
	assert.True(t, seenBad, "no no-var finding on bad.js in parsed findings: %+v", findings)
}
