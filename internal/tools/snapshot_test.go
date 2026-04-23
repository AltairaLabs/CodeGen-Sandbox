package tools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callSnapshotCreate(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleSnapshotCreate(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func callSnapshotList(t *testing.T, deps *tools.Deps) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}
	res, err := tools.HandleSnapshotList(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func callSnapshotRestore(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleSnapshotRestore(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func callSnapshotDiff(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleSnapshotDiff(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

// gitInRoot runs a git command in root and returns trimmed stdout or t.Fatal.
func gitInRoot(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// setupUserGitRepo initialises a git repo at root, configures identity so
// commits work in CI, and creates a committed seed file. Used by tests that
// need to verify snapshot ops don't disturb the user's own git state.
func setupUserGitRepo(t *testing.T, root string) {
	t.Helper()
	gitInRoot(t, root, "init", "-b", "main")
	gitInRoot(t, root, "config", "user.email", "test@example.com")
	gitInRoot(t, root, "config", "user.name", "test")
	require.NoError(t, os.WriteFile(filepath.Join(root, "seed.txt"), []byte("seed\n"), 0o644))
	gitInRoot(t, root, "add", "seed.txt")
	gitInRoot(t, root, "commit", "-m", "seed")
}

func TestSnapshotCreate_AutoInitsGit(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644))

	res := callSnapshotCreate(t, deps, map[string]any{"name": "s1"})
	require.False(t, res.IsError, "create failed: %s", textOf(t, res))

	_, err := os.Stat(filepath.Join(root, ".git"))
	require.NoError(t, err, ".git should exist after first snapshot_create")
}

func TestSnapshotCreate_HappyPath(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("v1"), 0o644))

	res := callSnapshotCreate(t, deps, map[string]any{"name": "s1", "description": "initial"})
	require.False(t, res.IsError, textOf(t, res))
	body := textOf(t, res)
	assert.Contains(t, body, "s1")
	// commit SHA should appear (40 hex chars)
	assert.Regexp(t, `[0-9a-f]{40}`, body)
}

func TestSnapshotCreate_RejectsInvalidName(t *testing.T) {
	deps, _ := newTestDeps(t)

	for _, name := range []string{"", ".secret", "foo/bar", "foo bar", "-leading"} {
		res := callSnapshotCreate(t, deps, map[string]any{"name": name})
		assert.True(t, res.IsError, "expected error for name %q: %s", name, textOf(t, res))
	}
}

func TestSnapshotList_ShowsCreated(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("v1"), 0o644))

	callSnapshotCreate(t, deps, map[string]any{"name": "alpha", "description": "first one"})
	callSnapshotCreate(t, deps, map[string]any{"name": "beta"})

	res := callSnapshotList(t, deps)
	require.False(t, res.IsError, textOf(t, res))
	body := textOf(t, res)
	assert.Contains(t, body, "alpha")
	assert.Contains(t, body, "beta")
	assert.Contains(t, body, "first one")
}

func TestSnapshotList_EmptyReturnsFriendlyMessage(t *testing.T) {
	deps, _ := newTestDeps(t)

	res := callSnapshotList(t, deps)
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, strings.ToLower(body), "no snapshots")
}

func TestSnapshotRestore_RevertsEdit(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	require.NoError(t, os.WriteFile(path, []byte("v1"), 0o644))

	callSnapshotCreate(t, deps, map[string]any{"name": "s1"})

	require.NoError(t, os.WriteFile(path, []byte("v2"), 0o644))

	res := callSnapshotRestore(t, deps, map[string]any{"name": "s1"})
	require.False(t, res.IsError, textOf(t, res))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "v1", string(data))
}

func TestSnapshotRestore_RemovesNewFiles(t *testing.T) {
	deps, root := newTestDeps(t)
	aPath := filepath.Join(root, "a.txt")
	require.NoError(t, os.WriteFile(aPath, []byte("v1"), 0o644))

	callSnapshotCreate(t, deps, map[string]any{"name": "s1"})

	bPath := filepath.Join(root, "b.txt")
	require.NoError(t, os.WriteFile(bPath, []byte("new"), 0o644))

	res := callSnapshotRestore(t, deps, map[string]any{"name": "s1"})
	require.False(t, res.IsError, textOf(t, res))

	_, err := os.Stat(bPath)
	assert.True(t, os.IsNotExist(err), "b.txt created after snapshot should be gone after restore")
}

func TestSnapshotRestore_UnknownName(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("v1"), 0o644))

	// Create at least one snapshot so the repo exists.
	callSnapshotCreate(t, deps, map[string]any{"name": "s1"})

	// Modify workspace.
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("v2"), 0o644))

	res := callSnapshotRestore(t, deps, map[string]any{"name": "does-not-exist"})
	assert.True(t, res.IsError)

	data, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	assert.Equal(t, "v2", string(data), "workspace must be unchanged on unknown-name restore")
}

func TestSnapshotDiff_ShowsDelta(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	require.NoError(t, os.WriteFile(path, []byte("v1\n"), 0o644))

	callSnapshotCreate(t, deps, map[string]any{"name": "s1"})

	require.NoError(t, os.WriteFile(path, []byte("v2\n"), 0o644))

	res := callSnapshotDiff(t, deps, map[string]any{"name": "s1"})
	require.False(t, res.IsError, textOf(t, res))
	body := textOf(t, res)
	assert.Contains(t, body, "-v1")
	assert.Contains(t, body, "+v2")
}

func TestSnapshotDiff_UnknownName(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("v1"), 0o644))
	callSnapshotCreate(t, deps, map[string]any{"name": "s1"})

	res := callSnapshotDiff(t, deps, map[string]any{"name": "nope"})
	assert.True(t, res.IsError)
}

func TestSnapshot_RespectsGitignore(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules/\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "node_modules", "foo.txt"), []byte("ignored"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "real.txt"), []byte("kept"), 0o644))

	callSnapshotCreate(t, deps, map[string]any{"name": "s1"})

	// Delete the ignored file, then restore.
	require.NoError(t, os.Remove(filepath.Join(root, "node_modules", "foo.txt")))

	res := callSnapshotRestore(t, deps, map[string]any{"name": "s1"})
	require.False(t, res.IsError, textOf(t, res))

	_, err := os.Stat(filepath.Join(root, "node_modules", "foo.txt"))
	assert.True(t, os.IsNotExist(err), "ignored file must not be restored because snapshot didn't capture it")
}

func TestSnapshot_DoesNotDisturbUserIndex(t *testing.T) {
	deps, root := newTestDeps(t)
	setupUserGitRepo(t, root)

	// Stage a new file in the user's index.
	require.NoError(t, os.WriteFile(filepath.Join(root, "staged.txt"), []byte("pending"), 0o644))
	gitInRoot(t, root, "add", "staged.txt")

	userHEADBefore := gitInRoot(t, root, "rev-parse", "HEAD")
	userIndexBefore := gitInRoot(t, root, "ls-files", "--stage")

	assertUserStateIntact := func(step string) {
		t.Helper()
		assert.Equal(t, userHEADBefore, gitInRoot(t, root, "rev-parse", "HEAD"), "%s: HEAD moved", step)
		assert.Equal(t, userIndexBefore, gitInRoot(t, root, "ls-files", "--stage"), "%s: index changed", step)
	}

	// create
	res := callSnapshotCreate(t, deps, map[string]any{"name": "s1"})
	require.False(t, res.IsError, textOf(t, res))
	assertUserStateIntact("after create")

	// list
	_ = callSnapshotList(t, deps)
	assertUserStateIntact("after list")

	// diff
	require.NoError(t, os.WriteFile(filepath.Join(root, "seed.txt"), []byte("changed\n"), 0o644))
	_ = callSnapshotDiff(t, deps, map[string]any{"name": "s1"})
	assertUserStateIntact("after diff")

	// restore
	_ = callSnapshotRestore(t, deps, map[string]any{"name": "s1"})
	assertUserStateIntact("after restore")
}

func TestSnapshot_FullRoundTrip(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	require.NoError(t, os.WriteFile(path, []byte("v1\n"), 0o644))

	// create
	create := callSnapshotCreate(t, deps, map[string]any{"name": "checkpoint", "description": "before refactor"})
	require.False(t, create.IsError, textOf(t, create))

	// list shows it
	list := callSnapshotList(t, deps)
	require.False(t, list.IsError)
	assert.Contains(t, textOf(t, list), "checkpoint")
	assert.Contains(t, textOf(t, list), "before refactor")

	// modify
	require.NoError(t, os.WriteFile(path, []byte("v2\n"), 0o644))

	// diff shows the delta
	diff := callSnapshotDiff(t, deps, map[string]any{"name": "checkpoint"})
	require.False(t, diff.IsError)
	assert.Contains(t, textOf(t, diff), "-v1")
	assert.Contains(t, textOf(t, diff), "+v2")

	// restore reverts
	restore := callSnapshotRestore(t, deps, map[string]any{"name": "checkpoint"})
	require.False(t, restore.IsError)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "v1\n", string(data))
}
