package workspace_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSet_ZeroEntriesRejected(t *testing.T) {
	_, err := workspace.NewSet(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one workspace")
}

func TestNewSet_NameDefaultsToBasename(t *testing.T) {
	dir := t.TempDir()
	set, err := workspace.NewSet([]workspace.Entry{{Root: dir}})
	require.NoError(t, err)
	require.Equal(t, 1, set.Len())
	assert.Equal(t, filepath.Base(dir), set.Names()[0])
}

func TestNewSet_ExplicitNameOverridesBasename(t *testing.T) {
	dir := t.TempDir()
	set, err := workspace.NewSet([]workspace.Entry{{Name: "primary", Root: dir}})
	require.NoError(t, err)
	assert.Equal(t, []string{"primary"}, set.Names())
}

func TestNewSet_DuplicateNameRejected(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	_, err := workspace.NewSet([]workspace.Entry{
		{Name: "one", Root: a},
		{Name: "one", Root: b},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestNewSet_BadRootSurfacesPathError(t *testing.T) {
	_, err := workspace.NewSet([]workspace.Entry{{Name: "x", Root: "/does-not-exist-codegen-sandbox"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace[0] (x)")
}

func TestSet_Default_SingletonReturnsIt(t *testing.T) {
	set, err := workspace.NewSet([]workspace.Entry{{Name: "only", Root: t.TempDir()}})
	require.NoError(t, err)
	ws := set.Default()
	require.NotNil(t, ws)
	assert.Equal(t, "only", ws.Name())
}

func TestSet_Default_NilWhenMultiple(t *testing.T) {
	set, err := workspace.NewSet([]workspace.Entry{
		{Name: "a", Root: t.TempDir()},
		{Name: "b", Root: t.TempDir()},
	})
	require.NoError(t, err)
	assert.Nil(t, set.Default())
}

func TestSet_GetUnknownReturnsSentinel(t *testing.T) {
	set, err := workspace.NewSet([]workspace.Entry{{Name: "a", Root: t.TempDir()}})
	require.NoError(t, err)
	_, err = set.Get("missing")
	require.Error(t, err)
	assert.True(t, errors.Is(err, workspace.ErrUnknownWorkspace))
}

func TestSet_SortedNamesIsDeterministic(t *testing.T) {
	set, err := workspace.NewSet([]workspace.Entry{
		{Name: "zebra", Root: t.TempDir()},
		{Name: "alpha", Root: t.TempDir()},
		{Name: "mike", Root: t.TempDir()},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "mike", "zebra"}, set.SortedNames())
	// Names() keeps insertion order.
	assert.Equal(t, []string{"zebra", "alpha", "mike"}, set.Names())
}

func TestNewSingletonSet_UsesBasenameWhenEmpty(t *testing.T) {
	ws, err := workspace.New(t.TempDir())
	require.NoError(t, err)
	set := workspace.NewSingletonSet(ws)
	assert.Equal(t, 1, set.Len())
	assert.Equal(t, filepath.Base(ws.Root()), set.Names()[0])
}

// Nil-receiver behaviour is part of the contract — gauge loops and
// other tools-package call sites pass *Set values that may be nil in
// single-workspace embedders that haven't constructed one. Each method
// returns a zero value rather than panicking.
func TestSet_NilReceiverIsZeroValue(t *testing.T) {
	var s *workspace.Set
	assert.Equal(t, 0, s.Len())
	assert.Nil(t, s.Names())
	assert.Nil(t, s.SortedNames())
	assert.Nil(t, s.Default())
	assert.Nil(t, s.All())
	_, err := s.Get("anything")
	require.Error(t, err)
}

func TestSet_AllReturnsCopyInOrder(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	set, err := workspace.NewSet([]workspace.Entry{
		{Name: "zebra", Root: a},
		{Name: "alpha", Root: b},
	})
	require.NoError(t, err)
	all := set.All()
	require.Len(t, all, 2)
	assert.Equal(t, "zebra", all[0].Name())
	assert.Equal(t, "alpha", all[1].Name())
	// Mutating the returned slice must not affect the Set's internals.
	all[0] = nil
	assert.Equal(t, "zebra", set.All()[0].Name())
}

func TestSet_GetExistingReturnsWorkspace(t *testing.T) {
	a := t.TempDir()
	set, err := workspace.NewSet([]workspace.Entry{{Name: "primary", Root: a}})
	require.NoError(t, err)
	ws, err := set.Get("primary")
	require.NoError(t, err)
	assert.Equal(t, "primary", ws.Name())
}
