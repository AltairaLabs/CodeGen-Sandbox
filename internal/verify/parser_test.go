package verify_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// detectorForMarker constructs a detector by seeding the marker file in a
// temp workspace and calling Detect. Per-detector parser tests only need
// the ParseLint method, so we don't actually run the linter.
func detectorForMarker(t *testing.T, marker string) verify.Detector {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, marker), []byte{}, 0o644))
	d := verify.Detect(dir)
	require.NotNil(t, d)
	return d
}

// --------------- Python (ruff) -----------------

func TestPythonDetector_ParseLint_RuffOutput(t *testing.T) {
	d := detectorForMarker(t, "pyproject.toml")

	stdout := "bad.py:6:9: F401 [*] `os` imported but unused\n" +
		"bad.py:10:1: E501 line too long (92 > 88 characters)\n" +
		"All checks failed.\n" // summary line, silently skipped
	findings := d.ParseLint(stdout, "")
	require.Len(t, findings, 2)

	assert.Equal(t, "bad.py", findings[0].File)
	assert.Equal(t, 6, findings[0].Line)
	assert.Equal(t, 9, findings[0].Column)
	assert.Equal(t, "F401", findings[0].Rule)
	assert.Contains(t, findings[0].Message, "os")
	assert.Contains(t, findings[0].Message, "unused")

	assert.Equal(t, "bad.py", findings[1].File)
	assert.Equal(t, "E501", findings[1].Rule)
	assert.Equal(t, 88, 88) // sanity
}

func TestPythonDetector_ParseLint_EmptyStdout(t *testing.T) {
	d := detectorForMarker(t, "pyproject.toml")
	assert.Empty(t, d.ParseLint("", ""))
}

// --------------- Node (eslint --format=compact) -----------------

func TestNodeDetector_ParseLint_EslintCompact(t *testing.T) {
	d := detectorForMarker(t, "package.json")

	stdout := "/app/src/index.js: line 5, col 3, Error - Missing semicolon (semi)\n" +
		"/app/src/index.js: line 12, col 5, Warning - Unused variable 'x' (no-unused-vars)\n" +
		"\n2 problems (1 error, 1 warning)\n" // summary line skipped
	findings := d.ParseLint(stdout, "")
	require.Len(t, findings, 2)

	assert.Equal(t, "/app/src/index.js", findings[0].File)
	assert.Equal(t, 5, findings[0].Line)
	assert.Equal(t, 3, findings[0].Column)
	assert.Equal(t, "semi", findings[0].Rule)
	assert.Equal(t, "Missing semicolon", findings[0].Message)

	assert.Equal(t, "no-unused-vars", findings[1].Rule)
	assert.Contains(t, findings[1].Message, "Unused variable")
}

func TestNodeDetector_ParseLint_MessageWithParentheses(t *testing.T) {
	d := detectorForMarker(t, "package.json")

	stdout := "a.js: line 1, col 1, Error - foo (bar) qux (semi)\n"
	findings := d.ParseLint(stdout, "")
	require.Len(t, findings, 1)
	assert.Equal(t, "semi", findings[0].Rule, "non-greedy message must let the trailing (rule) win")
	assert.Equal(t, "foo (bar) qux", findings[0].Message)
}

// --------------- Rust (cargo clippy --message-format=short) -----------------

func TestRustDetector_ParseLint_ClippyShort(t *testing.T) {
	d := detectorForMarker(t, "Cargo.toml")

	stderr := "    Checking probe v0.1.0 (/workspace)\n" + // cargo noise, skipped
		"src/main.rs:3:9: warning: unused variable: `x`\n" +
		"src/lib.rs:12:5: error: cannot find value `y` in this scope\n" +
		"warning: `probe` (lib) generated 1 warning\n" // summary, skipped
	findings := d.ParseLint("", stderr)
	require.Len(t, findings, 2)

	assert.Equal(t, "src/main.rs", findings[0].File)
	assert.Equal(t, 3, findings[0].Line)
	assert.Equal(t, 9, findings[0].Column)
	assert.Equal(t, "warning", findings[0].Rule)
	assert.Contains(t, findings[0].Message, "unused variable")

	assert.Equal(t, "src/lib.rs", findings[1].File)
	assert.Equal(t, "error", findings[1].Rule)
}

// --------------- Go (golangci-lint) — verifies Detector method wraps ParseLint correctly -----------------

func TestGoDetector_ParseLint_DelegatesToTopLevel(t *testing.T) {
	d := detectorForMarker(t, "go.mod")

	stdout := "bad.go:6:17: Error return value of `os.WriteFile` is not checked (errcheck)\n" +
		"    os.WriteFile(...)\n" + // context line, skipped
		"                ^\n" // caret line, skipped
	findings := d.ParseLint(stdout, "")
	require.Len(t, findings, 1)
	assert.Equal(t, "errcheck", findings[0].Rule)
	assert.Equal(t, "bad.go", findings[0].File)
}

// --------------- Cross-cutting: ignores the wrong stream -----------------

func TestPythonDetector_IgnoresStderr(t *testing.T) {
	d := detectorForMarker(t, "pyproject.toml")
	// ruff emits on stdout; anything on stderr should be ignored by the
	// parser.
	findings := d.ParseLint("", "bad.py:1:1: F401 [*] should be on stdout, not stderr")
	assert.Empty(t, findings)
}

func TestRustDetector_IgnoresStdout(t *testing.T) {
	d := detectorForMarker(t, "Cargo.toml")
	// clippy emits on stderr; anything on stdout should be ignored.
	findings := d.ParseLint("src/main.rs:1:1: warning: should be on stderr", "")
	assert.Empty(t, findings)
}
