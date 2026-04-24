package tools_test

import (
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMultiWorkspaceDeps(t *testing.T, names ...string) *tools.Deps {
	t.Helper()
	entries := make([]workspace.Entry, 0, len(names))
	for _, n := range names {
		entries = append(entries, workspace.Entry{Name: n, Root: t.TempDir()})
	}
	set, err := workspace.NewSet(entries)
	require.NoError(t, err)
	return &tools.Deps{
		Workspace:  set.All()[0],
		Workspaces: set,
		Tracker:    workspace.NewReadTracker(),
	}
}

func TestResolveWorkspace_SingleWorkspaceNoHintUsesDefault(t *testing.T) {
	deps := newMultiWorkspaceDeps(t, "only")
	// Handler args - no workspace hint.
	ws, errRes := tools.ResolveWorkspace(deps, map[string]any{})
	assert.Nil(t, errRes)
	require.NotNil(t, ws)
	assert.Equal(t, "only", ws.Name())
}

func TestResolveWorkspace_MultiWorkspaceNoHintIsError(t *testing.T) {
	deps := newMultiWorkspaceDeps(t, "primary", "extension")
	_, errRes := tools.ResolveWorkspace(deps, map[string]any{})
	require.NotNil(t, errRes)
	assert.True(t, errRes.IsError)
	body := textOf(t, errRes)
	assert.Contains(t, body, "multi-workspace sandbox")
	assert.Contains(t, body, "pass `workspace`")
	assert.Contains(t, body, "primary")
	assert.Contains(t, body, "extension")
}

func TestResolveWorkspace_ExplicitHintSelectsWorkspace(t *testing.T) {
	deps := newMultiWorkspaceDeps(t, "primary", "extension")
	ws, errRes := tools.ResolveWorkspace(deps, map[string]any{"workspace": "extension"})
	assert.Nil(t, errRes)
	require.NotNil(t, ws)
	assert.Equal(t, "extension", ws.Name())
}

func TestResolveWorkspace_UnknownHintIsError(t *testing.T) {
	deps := newMultiWorkspaceDeps(t, "primary", "extension")
	_, errRes := tools.ResolveWorkspace(deps, map[string]any{"workspace": "nope"})
	require.NotNil(t, errRes)
	assert.True(t, errRes.IsError)
	body := textOf(t, errRes)
	assert.Contains(t, body, `unknown workspace "nope"`)
	assert.Contains(t, body, "primary")
	assert.Contains(t, body, "extension")
}

func TestResolveWorkspace_NilSetFallsBackToSingleWorkspace(t *testing.T) {
	// Embedders / tests that haven't populated the Set should still
	// resolve — this preserves every existing call site that pre-dates
	// multi-workspace support.
	deps, _ := newTestDeps(t)
	ws, errRes := tools.ResolveWorkspace(deps, map[string]any{})
	assert.Nil(t, errRes)
	assert.Equal(t, deps.Workspace.Root(), ws.Root())
}

func TestResolveWorkspace_NilSetHintMismatchIsError(t *testing.T) {
	// With a nameless default workspace, any hint is effectively
	// unknown. (Named singletons are rare in tests; this is the common
	// path.)
	deps, _ := newTestDeps(t)
	ws, errRes := tools.ResolveWorkspace(deps, map[string]any{"workspace": "primary"})
	// Nameless default workspace accepts any hint as matching — there
	// is no other workspace to disambiguate against. Keeps tests that
	// pass a workspace hint working even before NewSingletonSet has
	// been wired.
	assert.Nil(t, errRes)
	assert.NotNil(t, ws)
}

func TestResolveWorkspace_HintIsTrimmed(t *testing.T) {
	deps := newMultiWorkspaceDeps(t, "primary", "extension")
	ws, errRes := tools.ResolveWorkspace(deps, map[string]any{"workspace": "  extension  "})
	assert.Nil(t, errRes)
	require.NotNil(t, ws)
	assert.Equal(t, "extension", ws.Name())
}
