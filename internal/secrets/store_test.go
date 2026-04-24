package secrets_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const envPrefix = "CODEGEN_SANDBOX_SECRET_"

func TestStore_EnvSource_DiscoversAndReturns(t *testing.T) {
	env := []string{
		envPrefix + "GITHUB_TOKEN=ghp-xyz",
		envPrefix + "BRAVE_API_KEY=bsa-abc",
		"UNRELATED=ignored",
	}
	s := secrets.New("", env)

	assert.ElementsMatch(t, []string{"github_token", "brave_api_key"}, s.Names())

	val, ok, src := s.Get("github_token")
	assert.True(t, ok)
	assert.Equal(t, "ghp-xyz", val)
	assert.Equal(t, secrets.SourceEnv, src)

	// Case-insensitive lookup.
	val, ok, src = s.Get("GitHub_Token")
	assert.True(t, ok)
	assert.Equal(t, "ghp-xyz", val)
	assert.Equal(t, secrets.SourceEnv, src)
}

func TestStore_FileSource_DiscoversAndReturns(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "github_token"), []byte("ghp-file\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "brave_api_key"), []byte("bsa-file"), 0o600))

	s := secrets.New(dir, nil)

	names := s.Names()
	sort.Strings(names)
	assert.Equal(t, []string{"brave_api_key", "github_token"}, names)

	val, ok, src := s.Get("github_token")
	assert.True(t, ok)
	assert.Equal(t, "ghp-file", val, "trailing newline trimmed")
	assert.Equal(t, secrets.SourceFile, src)

	val, ok, src = s.Get("brave_api_key")
	assert.True(t, ok)
	assert.Equal(t, "bsa-file", val)
	assert.Equal(t, secrets.SourceFile, src)
}

func TestStore_EnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "demo"), []byte("from-file"), 0o600))
	env := []string{envPrefix + "DEMO=from-env"}

	s := secrets.New(dir, env)

	val, ok, src := s.Get("demo")
	assert.True(t, ok)
	assert.Equal(t, "from-env", val)
	assert.Equal(t, secrets.SourceEnv, src)

	// Names list is the union; "demo" appears exactly once.
	names := s.Names()
	assert.Equal(t, []string{"demo"}, names)
}

func TestStore_UnionOfNames(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file_only"), []byte("f"), 0o600))
	env := []string{envPrefix + "ENV_ONLY=e"}

	s := secrets.New(dir, env)

	names := s.Names()
	sort.Strings(names)
	assert.Equal(t, []string{"env_only", "file_only"}, names)
}

func TestStore_UnknownName(t *testing.T) {
	s := secrets.New("", []string{envPrefix + "KNOWN=v"})
	val, ok, src := s.Get("missing")
	assert.False(t, ok)
	assert.Equal(t, "", val)
	assert.Equal(t, secrets.Source(""), src)
}

func TestStore_FileSource_IgnoresUnsafeBasenames(t *testing.T) {
	dir := t.TempDir()
	// Dotfiles ignored.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0o600))
	// Subdirs ignored (Store only enumerates top-level files).
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "nested"), []byte("y"), 0o600))
	// A valid one so the store isn't empty.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok"), []byte("z"), 0o600))

	s := secrets.New(dir, nil)
	assert.Equal(t, []string{"ok"}, s.Names())
}

func TestStore_NilSafe_EmptySources(t *testing.T) {
	s := secrets.New("", nil)
	assert.Empty(t, s.Names())
	val, ok, src := s.Get("anything")
	assert.False(t, ok)
	assert.Equal(t, "", val)
	assert.Equal(t, secrets.Source(""), src)
}

func TestStore_MissingDir_TreatedAsEmpty(t *testing.T) {
	// A missing secrets dir shouldn't panic or prevent env lookups.
	s := secrets.New("/nonexistent/codegen-sandbox-secrets-dir", []string{envPrefix + "E=v"})
	val, ok, _ := s.Get("e")
	assert.True(t, ok)
	assert.Equal(t, "v", val)
}

func TestStore_DashesAllowed(t *testing.T) {
	// Spec: dashes allowed in names; the env form maps them to '_'. Both
	// canonical forms resolve to the same lowercased name.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "vendor-key"), []byte("fv"), 0o600))
	env := []string{envPrefix + "OTHER_VENDOR_KEY=ev"}

	s := secrets.New(dir, env)
	names := s.Names()
	sort.Strings(names)
	assert.Equal(t, []string{"other_vendor_key", "vendor-key"}, names)

	val, ok, _ := s.Get("vendor-key")
	assert.True(t, ok)
	assert.Equal(t, "fv", val)
}
