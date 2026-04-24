package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWorkspacesSpec_PathsOnly(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	entries, err := parseWorkspacesSpec(a + "," + b)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, a, entries[0].Root)
	assert.Empty(t, entries[0].Name, "name defaults to basename at Set-construction time, not here")
	assert.Equal(t, b, entries[1].Root)
}

func TestParseWorkspacesSpec_NamedPaths(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	entries, err := parseWorkspacesSpec("primary=" + a + ",extension=" + b)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "primary", entries[0].Name)
	assert.Equal(t, a, entries[0].Root)
	assert.Equal(t, "extension", entries[1].Name)
	assert.Equal(t, b, entries[1].Root)
}

func TestParseWorkspacesSpec_ToleratesWhitespaceAndTrailingComma(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	entries, err := parseWorkspacesSpec("  primary = " + a + " , " + b + " , ")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "primary", entries[0].Name)
	assert.Equal(t, a, entries[0].Root)
	assert.Equal(t, b, entries[1].Root)
}

func TestParseWorkspacesSpec_EmptyStringIsError(t *testing.T) {
	_, err := parseWorkspacesSpec("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no entries parsed")
}

func TestParseWorkspacesSpec_EmptyPathPartIsError(t *testing.T) {
	_, err := parseWorkspacesSpec("name=")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty path")
}

// TestRun_WithTwoWorkspaces verifies Run accepts a -workspaces spec, builds
// a Set, starts the server, and exits cleanly on ctx cancel. Doesn't
// exercise any tool calls — those live in the tools-package integration
// suite.
func TestRun_WithTwoWorkspaces_CancelledContextExitsCleanly(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Addr:           "127.0.0.1:0",
			WorkspaceRoot:  "/workspace", // default; ignored when WorkspacesSpec is set.
			WorkspacesSpec: "primary=" + a + ",extension=" + b,
		})
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err, "Run with two workspaces should exit cleanly on ctx cancel")
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of ctx cancel")
	}
}

func TestRun_WorkspaceAndWorkspacesMutuallyExclusive(t *testing.T) {
	a := t.TempDir()
	err := Run(context.Background(), Config{
		Addr:           "127.0.0.1:0",
		WorkspaceRoot:  "/tmp/some-other-dir",
		WorkspacesSpec: "name=" + a,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}
