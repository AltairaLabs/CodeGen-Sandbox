package verify_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedPythonProject writes a pyproject.toml so Detect returns the Python
// detector; returns the workspace root.
func seedPythonProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname='probe'\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "probe.py"), []byte("x = 1\n"), 0o644))
	return dir
}

// seedGoProject writes a go.mod so Detect returns the Go detector (which has
// no formatter wired — FormatCheckCmd returns nil).
func seedGoProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\ngo 1.21\n"), 0o644))
	return dir
}

// writeStubBinary installs a shell-script stub named `name` in binDir that
// exits with `exitCode` and writes `output` to stdout+stderr. Returns the
// absolute path to the stub directory so callers can prepend it to PATH.
func writeStubBinary(t *testing.T, name string, exitCode int, output string) string {
	t.Helper()
	binDir := t.TempDir()
	script := "#!/bin/sh\n"
	if output != "" {
		// Use printf to preserve exact bytes; avoid echo portability issues.
		script += "printf '%s' \"" + output + "\"\n"
	}
	script += "exit " + itoa(exitCode) + "\n"
	stubPath := filepath.Join(binDir, name)
	require.NoError(t, os.WriteFile(stubPath, []byte(script), 0o755))
	return binDir
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

func TestFormatCheck_NoDetectorReturnsNilNil(t *testing.T) {
	// Empty tempdir → no detector.
	result, err := verify.FormatCheck(context.Background(), t.TempDir(), "probe.py", 10)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestFormatCheck_DetectorWithoutFormatterReturnsNilNil(t *testing.T) {
	// Go detector's FormatCheckCmd returns nil (golangci-lint covers it).
	result, err := verify.FormatCheck(context.Background(), seedGoProject(t), "probe.go", 10)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestFormatCheck_MissingBinaryReturnsSentinel(t *testing.T) {
	dir := seedPythonProject(t)
	// Empty PATH so ruff isn't reachable.
	t.Setenv("PATH", t.TempDir())

	result, err := verify.FormatCheck(context.Background(), dir, "probe.py", 10)
	require.Error(t, err)
	assert.True(t, errors.Is(err, verify.ErrFormatterMissing), "expected ErrFormatterMissing, got %v", err)
	require.NotNil(t, result)
	assert.Equal(t, "ruff", result.Binary)
}

func TestFormatCheck_StubbedFormatterCleanExit(t *testing.T) {
	dir := seedPythonProject(t)
	binDir := writeStubBinary(t, "ruff", 0, "")
	t.Setenv("PATH", binDir)

	result, err := verify.FormatCheck(context.Background(), dir, "probe.py", 10)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.OK)
	assert.Empty(t, result.Output)
	assert.Equal(t, "ruff", result.Binary)
}

func TestFormatCheck_StubbedFormatterReportsDrift(t *testing.T) {
	dir := seedPythonProject(t)
	binDir := writeStubBinary(t, "ruff", 1, "Would reformat: probe.py\n1 file would be reformatted")
	t.Setenv("PATH", binDir)

	result, err := verify.FormatCheck(context.Background(), dir, "probe.py", 10)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.OK)
	assert.Contains(t, result.Output, "Would reformat: probe.py")
}
