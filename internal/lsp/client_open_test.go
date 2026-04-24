package lsp

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLanguageIDForFile_ExtensionMapping(t *testing.T) {
	cases := map[string]string{
		"main.go":      "go",
		"probe.py":     "python",
		"lib.rs":       "rust",
		"app.ts":       "typescript",
		"app.tsx":      "typescriptreact",
		"app.js":       "javascript",
		"app.jsx":      "javascriptreact",
		"README.md":    "",
		"binary":       "",
		"x.unknown":    "",
		"path/to/x.py": "python",
	}
	for file, want := range cases {
		assert.Equal(t, want, languageIDForFile(file), "file=%q", file)
	}
}

func TestEnsureOpen_CachesPerAbsolutePath(t *testing.T) {
	// ensureOpen marks files in the `opened` set so a second call for
	// the same file is a no-op. The cache is keyed by the canonical
	// absolute path (workspace root + relative file). This test verifies
	// the cache shape without spawning a real subprocess — the notify
	// itself can't fire because the client never started, but the cache
	// state is observable.
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.py"), []byte("x = 1\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.py"), []byte("y = 2\n"), 0o644))

	c := NewClient(root, []string{"unused"})

	// First call records the file. Notify silently fails because the
	// subprocess was never started — that's fine; the cache update
	// happens before the notify and is what we're asserting on.
	c.ensureOpen("a.py")
	c.openedMu.Lock()
	_, gotA := c.opened[filepath.Join(root, "a.py")]
	c.openedMu.Unlock()
	assert.True(t, gotA, "ensureOpen should record a.py in the cache")

	// Second call for the same file is a no-op; we observe by checking
	// the set still has exactly one entry.
	c.ensureOpen("a.py")
	c.openedMu.Lock()
	count := len(c.opened)
	c.openedMu.Unlock()
	assert.Equal(t, 1, count, "second ensureOpen for the same file should be cached, set size unchanged")

	// A different file lands as a new entry.
	c.ensureOpen("b.py")
	c.openedMu.Lock()
	count = len(c.opened)
	c.openedMu.Unlock()
	assert.Equal(t, 2, count, "ensureOpen for a new file should grow the set")
}

func TestEnsureOpen_MissingFileIsSilent(t *testing.T) {
	// A file that doesn't exist on disk records in the cache (so we don't
	// retry the read on every query) and silently swallows the read
	// error. The follow-up query against the file may fail in the server,
	// but ensureOpen itself never panics or returns an error.
	root := t.TempDir()
	c := NewClient(root, []string{"unused"})
	assert.NotPanics(t, func() { c.ensureOpen("does-not-exist.py") })
	c.openedMu.Lock()
	_, ok := c.opened[filepath.Join(root, "does-not-exist.py")]
	c.openedMu.Unlock()
	assert.True(t, ok, "ensureOpen should still mark a missing file as 'attempted' to avoid retry on every query")
}

func TestEnsureOpen_ConcurrentSafe(t *testing.T) {
	// The opened-set guard is a sync.Mutex. Concurrent ensureOpen calls
	// for distinct files should all land in the cache without races.
	root := t.TempDir()
	for _, name := range []string{"a.py", "b.py", "c.py", "d.py"} {
		_ = os.WriteFile(filepath.Join(root, name), []byte("x = 1\n"), 0o644)
	}
	c := NewClient(root, []string{"unused"})
	var wg sync.WaitGroup
	for _, name := range []string{"a.py", "b.py", "c.py", "d.py"} {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			c.ensureOpen(n)
		}(name)
	}
	wg.Wait()
	c.openedMu.Lock()
	got := len(c.opened)
	c.openedMu.Unlock()
	assert.Equal(t, 4, got, "all four files should land in the cache exactly once")
}
