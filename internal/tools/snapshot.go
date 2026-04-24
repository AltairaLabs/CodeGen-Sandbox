package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// snapshotRefPrefix namespaces snapshot refs so they live outside the user's
// normal ref space (refs/heads, refs/tags). This keeps the user's git log,
// branch listing, and tags untouched.
const snapshotRefPrefix = "refs/sandbox/snapshots/"

// snapshotNamePattern matches names safe for use as a git ref component.
// First char must be alphanumeric or underscore to avoid dotfiles and
// leading-hyphen CLI-flag confusion; remaining chars may also include '.' and '-'.
var snapshotNamePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

const (
	// errSnapshotNotFound is the canonical error-message format for "no
	// snapshot by that name" across the three tools that check it.
	errSnapshotNotFound = "snapshot %q not found"
	// gitIndexEnvKey is the env-var prefix for GIT_INDEX_FILE; extracted
	// to avoid the literal being duplicated (go:S1192).
	gitIndexEnvKey = "GIT_INDEX_FILE="
)

// RegisterSnapshots registers the four snapshot tools (create + restore
// are mutating; list + diff are read-only). Kept as a convenience wrapper
// so existing tests / callers that want the full set don't have to touch
// every call site; the server uses the split halves directly so it can
// gate the mutating pair on read-only mode.
func RegisterSnapshots(s ToolAdder, deps *Deps) {
	RegisterSnapshotsReadOnly(s, deps)
	RegisterSnapshotsMutating(s, deps)
}

// RegisterSnapshotsReadOnly registers the non-mutating snapshot tools
// (snapshot_list + snapshot_diff). Safe to expose in read-only mode —
// listing and diffing do not touch refs/sandbox/snapshots/* or the
// workspace tree.
func RegisterSnapshotsReadOnly(s ToolAdder, deps *Deps) {
	registerSnapshotList(s, deps)
	registerSnapshotDiff(s, deps)
}

// RegisterSnapshotsMutating registers the workspace-mutating snapshot
// tools (snapshot_create + snapshot_restore). Skipped in read-only mode:
// create writes a new snapshot ref, restore resets the workspace tree.
func RegisterSnapshotsMutating(s ToolAdder, deps *Deps) {
	registerSnapshotCreate(s, deps)
	registerSnapshotRestore(s, deps)
}

func registerSnapshotCreate(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("snapshot_create",
		mcp.WithDescription("Record the current workspace state under a name. Returns the commit SHA. The first call in a workspace that isn't already a git repo will 'git init' lazily. Snapshots live on refs/sandbox/snapshots/* and do NOT touch the user's HEAD, branch, or index. Respects .gitignore."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Snapshot name. Must match [A-Za-z0-9_-][A-Za-z0-9_.-]*.")),
		mcp.WithString("description", mcp.Description("Optional human-readable description stored as the snapshot commit message.")),
	)
	s.AddTool(tool, HandleSnapshotCreate(deps))
}

func registerSnapshotList(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("snapshot_list",
		mcp.WithDescription("List all named snapshots with their timestamps, commit SHAs, and descriptions."),
	)
	s.AddTool(tool, HandleSnapshotList(deps))
}

func registerSnapshotRestore(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("snapshot_restore",
		mcp.WithDescription("Reset the workspace to a named snapshot's state. Files present now but absent in the snapshot are removed. The user's HEAD / branch / index are not touched."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Snapshot name to restore.")),
	)
	s.AddTool(tool, HandleSnapshotRestore(deps))
}

func registerSnapshotDiff(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("snapshot_diff",
		mcp.WithDescription("Return a unified diff between the named snapshot and the current workspace."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Snapshot name to diff against.")),
	)
	s.AddTool(tool, HandleSnapshotDiff(deps))
}

// HandleSnapshotCreate returns the snapshot_create handler.
func HandleSnapshotCreate(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		name, errRes := parseSnapshotName(args)
		if errRes != nil {
			return errRes, nil
		}
		description, _ := args["description"].(string)
		if description == "" {
			description = "snapshot: " + name
		}

		root := deps.Workspace.Root()
		if errRes := ensureGitRepo(ctx, root); errRes != nil {
			return errRes, nil
		}

		sha, errRes := createSnapshot(ctx, root, name, description)
		if errRes != nil {
			return errRes, nil
		}
		return TextResult(fmt.Sprintf("snapshot %s created: %s", name, sha)), nil
	}
}

// HandleSnapshotList returns the snapshot_list handler.
func HandleSnapshotList(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		root := deps.Workspace.Root()
		if !gitRepoExists(root) {
			return TextResult("no snapshots"), nil
		}

		entries, err := listSnapshots(ctx, root)
		if err != nil {
			return ErrorResult("snapshot_list: %v", err), nil
		}
		if len(entries) == 0 {
			return TextResult("no snapshots"), nil
		}
		return TextResult(formatSnapshotList(entries)), nil
	}
}

// HandleSnapshotRestore returns the snapshot_restore handler.
func HandleSnapshotRestore(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		name, errRes := parseSnapshotName(args)
		if errRes != nil {
			return errRes, nil
		}

		root := deps.Workspace.Root()
		if !gitRepoExists(root) {
			return ErrorResult(errSnapshotNotFound, name), nil
		}
		if !snapshotExists(ctx, root, name) {
			return ErrorResult(errSnapshotNotFound, name), nil
		}

		changed, errRes := restoreSnapshot(ctx, root, name)
		if errRes != nil {
			return errRes, nil
		}
		return TextResult(formatRestoreResult(name, changed)), nil
	}
}

// HandleSnapshotDiff returns the snapshot_diff handler.
func HandleSnapshotDiff(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		name, errRes := parseSnapshotName(args)
		if errRes != nil {
			return errRes, nil
		}

		root := deps.Workspace.Root()
		if !gitRepoExists(root) {
			return ErrorResult(errSnapshotNotFound, name), nil
		}
		if !snapshotExists(ctx, root, name) {
			return ErrorResult(errSnapshotNotFound, name), nil
		}

		diff, errRes := diffSnapshot(ctx, root, name)
		if errRes != nil {
			return errRes, nil
		}
		if diff == "" {
			return TextResult("no differences"), nil
		}
		return TextResult(diff), nil
	}
}

func parseSnapshotName(args map[string]any) (string, *mcp.CallToolResult) {
	name, _ := args["name"].(string)
	if name == "" {
		return "", ErrorResult("name is required")
	}
	if !snapshotNamePattern.MatchString(name) {
		return "", ErrorResult("invalid snapshot name %q: must match [A-Za-z0-9_-][A-Za-z0-9_.-]*", name)
	}
	return name, nil
}

// --- git plumbing helpers ---

// gitRepoExists reports whether the workspace already hosts a git repo.
func gitRepoExists(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

// ensureGitRepo initialises a git repo at root if one doesn't exist yet.
// Uses -b main so initial snapshots don't depend on git's default-branch config.
func ensureGitRepo(ctx context.Context, root string) *mcp.CallToolResult {
	if gitRepoExists(root) {
		return nil
	}
	if _, err := runGit(ctx, root, nil, "init", "-b", "main"); err != nil {
		return ErrorResult("git init: %v", err)
	}
	return nil
}

// snapshotRef returns the full ref path for a named snapshot.
func snapshotRef(name string) string { return snapshotRefPrefix + name }

// snapshotExists reports whether a snapshot by that name is recorded.
func snapshotExists(ctx context.Context, root, name string) bool {
	_, err := runGit(ctx, root, nil, "rev-parse", "--verify", "--quiet", snapshotRef(name)+"^{commit}")
	return err == nil
}

// tempIndexPath returns a per-operation index path under .git/ so concurrent
// snapshot ops don't collide with each other or with the user's index.
func tempIndexPath(root string) string {
	return filepath.Join(root, ".git", fmt.Sprintf("sandbox-index.%d.%d", os.Getpid(), time.Now().UnixNano()))
}

// createSnapshot captures the current worktree into a detached commit under
// refs/sandbox/snapshots/<name>. Uses a temporary GIT_INDEX_FILE so the
// user's on-disk index is untouched.
func createSnapshot(ctx context.Context, root, name, description string) (string, *mcp.CallToolResult) {
	idx := tempIndexPath(root)
	defer func() { _ = os.Remove(idx) }()
	env := []string{gitIndexEnvKey + idx}

	if _, err := runGit(ctx, root, env, "add", "--all"); err != nil {
		return "", ErrorResult("snapshot add: %v", err)
	}

	treeOut, err := runGit(ctx, root, env, "write-tree")
	if err != nil {
		return "", ErrorResult("write-tree: %v", err)
	}
	tree := strings.TrimSpace(string(treeOut))

	// Commit env: set a stable identity so commit-tree doesn't depend on
	// user.email/user.name in the user's config. We only pass committer
	// vars — author vars inherit, but commit-tree requires committer.
	commitEnv := append([]string{
		"GIT_COMMITTER_NAME=codegen-sandbox",
		"GIT_COMMITTER_EMAIL=sandbox@codegen.local",
		"GIT_AUTHOR_NAME=codegen-sandbox",
		"GIT_AUTHOR_EMAIL=sandbox@codegen.local",
	}, env...)
	shaOut, err := runGit(ctx, root, commitEnv, "commit-tree", tree, "-m", description)
	if err != nil {
		return "", ErrorResult("commit-tree: %v", err)
	}
	sha := strings.TrimSpace(string(shaOut))

	if _, err := runGit(ctx, root, nil, "update-ref", snapshotRef(name), sha); err != nil {
		return "", ErrorResult("update-ref: %v", err)
	}
	return sha, nil
}

// snapshotEntry describes a single snapshot for listing.
type snapshotEntry struct {
	name        string
	sha         string
	timestamp   string
	description string
}

// listSnapshots returns all snapshots under the sandbox ref namespace.
func listSnapshots(ctx context.Context, root string) ([]snapshotEntry, error) {
	// Format: ref<TAB>sha<TAB>iso-8601 date<TAB>subject
	out, err := runGit(ctx, root, nil,
		"for-each-ref",
		"--format=%(refname)%09%(objectname)%09%(committerdate:iso-strict)%09%(subject)",
		snapshotRefPrefix,
	)
	if err != nil {
		return nil, err
	}
	entries := parseSnapshotRefs(string(out))
	sort.Slice(entries, func(i, j int) bool { return entries[i].timestamp > entries[j].timestamp })
	return entries, nil
}

func parseSnapshotRefs(s string) []snapshotEntry {
	var entries []snapshotEntry
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 3 {
			continue
		}
		name := strings.TrimPrefix(parts[0], snapshotRefPrefix)
		desc := ""
		if len(parts) == 4 {
			desc = parts[3]
		}
		entries = append(entries, snapshotEntry{
			name:        name,
			sha:         parts[1],
			timestamp:   parts[2],
			description: desc,
		})
	}
	return entries
}

func formatSnapshotList(entries []snapshotEntry) string {
	var sb strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&sb, "%s\t%s\t%s\t%s\n", e.name, e.sha[:min(len(e.sha), 12)], e.timestamp, e.description)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// restoreSnapshot resets the worktree to the snapshot's tree. Uses a temp
// index so the user's own index stays intact; read-tree populates the temp
// index from the snapshot, then checkout-index materialises every file and
// clean removes anything not in the snapshot.
func restoreSnapshot(ctx context.Context, root, name string) ([]string, *mcp.CallToolResult) {
	before, err := listWorktreePaths(ctx, root)
	if err != nil {
		return nil, ErrorResult("inventory pre-restore: %v", err)
	}

	idx := tempIndexPath(root)
	defer func() { _ = os.Remove(idx) }()
	env := []string{gitIndexEnvKey + idx}

	if _, err := runGit(ctx, root, env, "read-tree", snapshotRef(name)); err != nil {
		return nil, ErrorResult("read-tree: %v", err)
	}

	// Materialise every path from the temp index into the worktree.
	if _, err := runGit(ctx, root, env, "checkout-index", "-a", "-f"); err != nil {
		return nil, ErrorResult("checkout-index: %v", err)
	}

	// Remove any pre-existing tracked-in-before-state file that's absent
	// from the snapshot. We compute this as (before) minus (snapshot tree
	// paths). Also clean any untracked files that weren't in the snapshot.
	snapshotPaths, err := snapshotPathSet(ctx, root, env)
	if err != nil {
		return nil, ErrorResult("snapshot paths: %v", err)
	}
	if err := removeExtrasNotInSnapshot(root, before, snapshotPaths); err != nil {
		return nil, ErrorResult("cleanup: %v", err)
	}

	after, err := listWorktreePaths(ctx, root)
	if err != nil {
		return nil, ErrorResult("inventory post-restore: %v", err)
	}
	return diffPathSets(before, after), nil
}

// listWorktreePaths returns the tracked+untracked paths visible from the
// workspace right now, respecting .gitignore. This is the "what does the
// user see" set — consulted before and after restore to compute a change list.
func listWorktreePaths(ctx context.Context, root string) ([]string, error) {
	out, err := runGit(ctx, root, nil, "ls-files", "--cached", "--others", "--exclude-standard")
	if err != nil {
		// An empty repo with no commits and nothing indexed returns non-zero
		// from ls-files on some git versions; tolerate by reporting empty.
		return nil, nil
	}
	return splitNonEmptyLines(string(out)), nil
}

func snapshotPathSet(ctx context.Context, root string, env []string) (map[string]struct{}, error) {
	out, err := runGit(ctx, root, env, "ls-files")
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{})
	for _, p := range splitNonEmptyLines(string(out)) {
		set[p] = struct{}{}
	}
	return set, nil
}

// removeExtrasNotInSnapshot deletes every path in `before` that isn't in the
// snapshot set. We walk the before-list (not the current worktree) so files
// added after the snapshot AND present pre-restore get cleaned up too.
func removeExtrasNotInSnapshot(root string, before []string, snapshot map[string]struct{}) error {
	for _, p := range before {
		if _, ok := snapshot[p]; ok {
			continue
		}
		abs := filepath.Join(root, p)
		if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// Best-effort prune empty directories up the chain; ignore errors
		// from non-empty dirs.
		pruneEmptyDirs(root, filepath.Dir(abs))
	}
	return nil
}

func pruneEmptyDirs(root, dir string) {
	for dir != root && strings.HasPrefix(dir, root) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func diffPathSets(before, after []string) []string {
	seen := make(map[string]int, len(before)+len(after))
	for _, p := range before {
		seen[p] |= 1
	}
	for _, p := range after {
		seen[p] |= 2
	}
	changed := make([]string, 0, len(seen))
	for p, bits := range seen {
		if bits != 3 {
			changed = append(changed, p)
		}
	}
	sort.Strings(changed)
	return changed
}

func formatRestoreResult(name string, changed []string) string {
	if len(changed) == 0 {
		return fmt.Sprintf("restored snapshot %s (no files changed)", name)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "restored snapshot %s (%d file(s) changed):\n", name, len(changed))
	for _, p := range changed {
		fmt.Fprintf(&sb, "%s\n", p)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// diffSnapshot returns a unified diff from the snapshot's tree to the current
// worktree. We stage the current worktree into a temp index (so the user's
// real index is untouched), write-tree to produce a tree object for "now",
// then diff-tree the two trees.
func diffSnapshot(ctx context.Context, root, name string) (string, *mcp.CallToolResult) {
	idx := tempIndexPath(root)
	defer func() { _ = os.Remove(idx) }()
	env := []string{gitIndexEnvKey + idx}

	if _, err := runGit(ctx, root, env, "add", "--all"); err != nil {
		return "", ErrorResult("diff add: %v", err)
	}
	treeOut, err := runGit(ctx, root, env, "write-tree")
	if err != nil {
		return "", ErrorResult("diff write-tree: %v", err)
	}
	currentTree := strings.TrimSpace(string(treeOut))

	out, err := runGit(ctx, root, env,
		"diff", "--no-color",
		snapshotRef(name), currentTree,
	)
	if err != nil {
		return "", ErrorResult("diff: %v", err)
	}
	return string(out), nil
}

// gitPathCache caches exec.LookPath("git") across runGit calls. Resolving
// git to an absolute path (not relying on $PATH resolution per exec.Cmd
// invocation) closes the "PATH may contain a writable directory" hotspot
// gosecurity:S4036, the same way internal/tools/bash.go pins /bin/bash.
var (
	gitPathOnce sync.Once
	gitPathVal  string
)

func resolvedGitPath() string {
	gitPathOnce.Do(func() {
		if p, err := exec.LookPath("git"); err == nil {
			gitPathVal = p
			return
		}
		// Fall back to the literal: the subsequent exec will surface a
		// clear "git: executable file not found" error at the first call.
		gitPathVal = "git"
	})
	return gitPathVal
}

// runGit invokes git with the given args in root. Extra environment pairs
// (e.g. GIT_INDEX_FILE) are appended to os.Environ. Returns combined
// stdout+stderr on non-zero exit.
func runGit(ctx context.Context, root string, extraEnv []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, resolvedGitPath(), args...)
	cmd.Dir = root
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return out.Bytes(), nil
}

func splitNonEmptyLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
