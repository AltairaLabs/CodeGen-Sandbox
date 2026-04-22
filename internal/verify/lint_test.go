package verify_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireGolangciLint(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not on PATH; skipping")
	}
}

func TestParseLint_HappyPath(t *testing.T) {
	sample := "bad.go:6:17: Error return value of `os.WriteFile` is not checked (errcheck)\n" +
		"    os.WriteFile(\"x\", []byte(\"y\"), 0o644)\n" +
		"                ^\n" +
		"main.go:6:17: printf: fmt.Printf format %d has arg \"x\" of wrong type string (govet)\n" +
		"    fmt.Printf(\"%d\", \"x\")\n" +
		"                ^\n" +
		"\n" +
		"2 issues:\n" +
		"* errcheck: 1\n" +
		"* govet: 1\n"
	findings := verify.ParseLint(sample)
	require.Len(t, findings, 2)

	assert.Equal(t, "bad.go", findings[0].File)
	assert.Equal(t, 6, findings[0].Line)
	assert.Equal(t, 17, findings[0].Column)
	assert.Equal(t, "errcheck", findings[0].Rule)
	assert.Contains(t, findings[0].Message, "os.WriteFile")

	assert.Equal(t, "main.go", findings[1].File)
	assert.Equal(t, "govet", findings[1].Rule)
}

func TestParseLint_EmptyInput(t *testing.T) {
	assert.Empty(t, verify.ParseLint(""))
}

func TestParseLint_NoFindingsTolerated(t *testing.T) {
	assert.Empty(t, verify.ParseLint("0 issues.\n"))
}

func TestParseLint_MessageWithParentheses(t *testing.T) {
	// A linter message can itself contain parentheses; the trailing "(rule)"
	// is the last parenthesized group on the line.
	sample := "x.go:1:1: something (with nested) stuff (errcheck)\n"
	findings := verify.ParseLint(sample)
	require.Len(t, findings, 1)
	assert.Equal(t, "errcheck", findings[0].Rule)
	assert.Equal(t, "something (with nested) stuff", findings[0].Message)
}

func TestLint_NoDetectorReturnsNilNil(t *testing.T) {
	dir := t.TempDir() // no go.mod → no detector
	findings, err := verify.Lint(context.Background(), dir, 30)
	require.NoError(t, err)
	assert.Nil(t, findings)
}

func TestLint_MissingBinaryReturnsSentinel(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\ngo 1.21\n"), 0o644))
	t.Setenv("PATH", t.TempDir()) // empty PATH — golangci-lint unreachable

	_, err := verify.Lint(context.Background(), dir, 30)
	require.Error(t, err)
	assert.True(t, errors.Is(err, verify.ErrLinterMissing), "expected ErrLinterMissing, got %v", err)
}

func TestLint_LiveFindsRealIssue(t *testing.T) {
	requireGolangciLint(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".golangci.yml"), []byte(
		"version: \"2\"\nlinters:\n  default: none\n  enable:\n    - errcheck\n",
	), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.go"), []byte(
		"package probe\n\nimport \"os\"\n\nfunc writeErr() { os.WriteFile(\"x\", []byte(\"y\"), 0o644) }\n",
	), 0o644))

	findings, err := verify.Lint(context.Background(), dir, 60)
	require.NoError(t, err)
	require.NotEmpty(t, findings, "live linter should have produced at least one errcheck finding")
	assert.Equal(t, "bad.go", findings[0].File)
	assert.Equal(t, "errcheck", findings[0].Rule)
}
