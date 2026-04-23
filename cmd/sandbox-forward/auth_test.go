package main

import (
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseFlagsForTest(t *testing.T, args []string) *authFlags {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	a := &authFlags{}
	a.register(fs)
	require.NoError(t, fs.Parse(args))
	return a
}

func TestAuthFlags_Bearer(t *testing.T) {
	a := parseFlagsForTest(t, []string{"--bearer", "abc123"})
	h := http.Header{}
	require.NoError(t, a.Apply(h))
	assert.Equal(t, "Bearer abc123", h.Get("Authorization"))
}

func TestAuthFlags_BearerFile_TrimsNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok")
	require.NoError(t, os.WriteFile(path, []byte("tok-xyz\n"), 0o600))

	a := parseFlagsForTest(t, []string{"--bearer-file", path})
	h := http.Header{}
	require.NoError(t, a.Apply(h))
	assert.Equal(t, "Bearer tok-xyz", h.Get("Authorization"))
}

func TestAuthFlags_BearerFile_TrimsCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok")
	require.NoError(t, os.WriteFile(path, []byte("tok-xyz\r\n"), 0o600))

	a := parseFlagsForTest(t, []string{"--bearer-file", path})
	h := http.Header{}
	require.NoError(t, a.Apply(h))
	assert.Equal(t, "Bearer tok-xyz", h.Get("Authorization"))
}

func TestAuthFlags_Cookie_Repeatable(t *testing.T) {
	a := parseFlagsForTest(t, []string{
		"--cookie", "session=abc",
		"--cookie", "csrf=xyz",
	})
	h := http.Header{}
	require.NoError(t, a.Apply(h))
	got := h.Values("Cookie")
	assert.Equal(t, []string{"session=abc", "csrf=xyz"}, got)
}

func TestAuthFlags_Header_Repeatable(t *testing.T) {
	a := parseFlagsForTest(t, []string{
		"--header", "X-Org=acme",
		"--header", "X-Env=prod",
	})
	h := http.Header{}
	require.NoError(t, a.Apply(h))
	assert.Equal(t, "acme", h.Get("X-Org"))
	assert.Equal(t, "prod", h.Get("X-Env"))
}

func TestAuthFlags_Compose_AllFour(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok")
	require.NoError(t, os.WriteFile(path, []byte("from-file\n"), 0o600))

	a := parseFlagsForTest(t, []string{
		"--bearer-file", path,
		"--cookie", "session=s",
		"--header", "X-Trace=42",
	})
	h := http.Header{}
	require.NoError(t, a.Apply(h))

	assert.Equal(t, "Bearer from-file", h.Get("Authorization"))
	assert.Equal(t, "session=s", h.Get("Cookie"))
	assert.Equal(t, "42", h.Get("X-Trace"))
}

func TestAuthFlags_BearerAndBearerFile_Mutex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))

	a := parseFlagsForTest(t, []string{
		"--bearer", "b",
		"--bearer-file", path,
	})
	err := a.Apply(http.Header{})
	require.Error(t, err)
}

func TestAuthFlags_Cookie_Malformed(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	// Silence flag package error output.
	fs.SetOutput(os.Stderr)
	a := &authFlags{}
	a.register(fs)
	err := fs.Parse([]string{"--cookie", "no-equals"})
	require.Error(t, err)
}

func TestAuthFlags_Header_Malformed(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	a := &authFlags{}
	a.register(fs)
	err := fs.Parse([]string{"--header", "no-equals"})
	require.Error(t, err)
}

func TestAuthFlags_Empty_NoHeaders(t *testing.T) {
	a := parseFlagsForTest(t, []string{})
	h := http.Header{}
	require.NoError(t, a.Apply(h))
	assert.Empty(t, h)
}
