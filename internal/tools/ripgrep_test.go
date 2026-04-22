package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireRipgrep(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep (rg) not found on PATH; install with `brew install ripgrep` / `apt install ripgrep`")
	}
}

func TestRunRipgrep_ListsFiles(t *testing.T) {
	requireRipgrep(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("y"), 0o644))

	out, err := runRipgrep(context.Background(), []string{"--files", "--no-require-git", "--color=never"}, dir)
	require.NoError(t, err)
	s := string(out)
	assert.Contains(t, s, "a.txt")
	assert.Contains(t, s, "b.txt")
}

func TestRunRipgrep_NoMatchesIsNotError(t *testing.T) {
	requireRipgrep(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644))

	out, err := runRipgrep(context.Background(), []string{"--no-require-git", "--color=never", "--", "zzzzz-no-such-pattern"}, dir)
	require.NoError(t, err, "exit code 1 from rg means 'no matches' and must not surface as error")
	assert.Empty(t, out)
}

func TestRunRipgrep_InvalidFlagIsError(t *testing.T) {
	requireRipgrep(t)

	dir := t.TempDir()
	_, err := runRipgrep(context.Background(), []string{"--definitely-not-a-flag"}, dir)
	require.Error(t, err)
}

func TestRunRipgrep_MissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty PATH — rg cannot be found
	_, err := runRipgrep(context.Background(), []string{"--files"}, t.TempDir())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRipgrepMissing), "expected ErrRipgrepMissing, got %v", err)
}
