//go:build linux || darwin

package search_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("predicate never became true within timeout")
}

func TestWatcher_CreatedFileIsIndexed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "seed.go"), "package p\nfunc Seeded() {}\n")
	idx, err := search.Build(dir)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, idx.Watch(ctx))

	newPath := filepath.Join(dir, "new.go")
	require.NoError(t, os.WriteFile(newPath, []byte("package p\n// Fresh handles fresh requests.\nfunc Fresh() {}\n"), 0o644))

	waitFor(t, func() bool {
		return len(idx.Search("Fresh", 5)) > 0
	})
	assert.Equal(t, "Fresh", idx.Search("Fresh", 5)[0].Unit.Symbol)
}

func TestWatcher_ModifiedFileReindexes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	writeFile(t, path, "package p\nfunc Before() {}\n")
	idx, err := search.Build(dir)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, idx.Watch(ctx))

	require.NoError(t, os.WriteFile(path, []byte("package p\nfunc After() {}\n"), 0o644))

	waitFor(t, func() bool {
		return len(idx.Search("After", 5)) > 0 && len(idx.Search("Before", 5)) == 0
	})
}

func TestWatcher_RemovedFileDrops(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.go")
	writeFile(t, path, "package p\nfunc Gone() {}\n")
	idx, err := search.Build(dir)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, idx.Watch(ctx))

	require.NoError(t, os.Remove(path))

	waitFor(t, func() bool {
		return len(idx.Search("Gone", 5)) == 0
	})
}

func TestWatcher_IgnoresNonGoFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package p\nfunc Existing() {}\n")
	idx, err := search.Build(dir)
	require.NoError(t, err)
	originalUnits := idx.UnitCount()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, idx.Watch(ctx))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# hi"), 0o644))
	// Give the watcher a moment to receive the event even though we expect
	// it to be ignored.
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, originalUnits, idx.UnitCount())
}
