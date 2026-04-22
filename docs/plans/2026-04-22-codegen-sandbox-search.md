# Codegen Sandbox — Search (Glob + Grep) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `Glob` and `Grep` MCP tools to the codegen sandbox, both backed by ripgrep, both respecting `.gitignore`, both path-contained through `workspace.Resolve`.

**Architecture:** Both tools shell out to the `rg` binary via a shared helper (`runRipgrep`). `Glob` uses `rg --files -g <pattern>` to enumerate matching files and sorts them by mtime descending. `Grep` uses `rg` with flags matched to its `output_mode` parameter (`content` → `-n`, `files_with_matches` → `-l`, `count` → `-c`). Both tools cwd into the workspace root so `rg` emits workspace-relative paths. Path arguments pass through `workspace.Resolve` before being handed to `rg`.

**Tech Stack:** Go 1.25+, `github.com/mark3labs/mcp-go` v0.49.0, ripgrep (`rg`) binary on `PATH`, `github.com/stretchr/testify`.

**Prerequisite (operator):** `rg` must be installed on any machine that runs the test suite or the built binary. macOS: `brew install ripgrep`. Debian/Ubuntu: `apt install ripgrep`. Alpine: `apk add ripgrep`. If `rg` is missing, the helper returns a clear error and tests that depend on it are skipped with a `t.Skip` message.

**Deviation discovered during execution (2026-04-22):** Task 2's original design passed `-g <pattern>` to `rg`. During TDD, `TestGlob_RespectsGitignore` revealed that `rg -g` acts as a whitelist that **bypasses `.gitignore`** (files matching the glob are included even when they're ignored). The fix: omit `-g` from the rg invocation so `.gitignore` is honored, and apply the glob pattern in Go after rg returns the file list. See `internal/tools/glob.go:matchDoublestar`. The matcher supports `*`, `?`, `[...]`, and `**`; brace expansion and negation are not supported.

**Out of scope for this plan:**
- `Bash`, `run_tests`, `run_lint`, `run_typecheck` (Plans 3–4)
- Secret scrubbing of grep output (Plan 5 — this plan returns raw matching lines)
- Web tools (Plan 8)
- JSON output mode for `Grep` (the built-in text modes are structured enough for v1)

---

## File Structure

Files introduced by this plan:

| Path | Responsibility |
|---|---|
| `internal/tools/ripgrep.go` | `runRipgrep(ctx, args, cwd) ([]byte, error)` — thin wrapper around `exec.CommandContext("rg", ...)`; maps rg exit codes (0=matches, 1=no matches, ≥2=error). |
| `internal/tools/ripgrep_test.go` | Helper tests: match-found, no-matches, invalid flag, binary-not-found skip. |
| `internal/tools/glob.go` | `RegisterGlob`, `HandleGlob`. Invokes `rg --files -g <pattern>` inside workspace root; stats results; sorts by mtime desc; caps at `limit`. |
| `internal/tools/glob_test.go` | Handler tests: pattern match, respect .gitignore, mtime sort, path arg, outside-workspace rejection, no matches, limit. |
| `internal/tools/grep.go` | `RegisterGrep`, `HandleGrep`. Builds rg args per `output_mode`; head-limits in Go. |
| `internal/tools/grep_test.go` | Handler tests: content/files/count modes, case-insensitive, glob filter, head_limit, invalid regex, outside-workspace, no matches. |
| `internal/server/server.go` | Two additional registration lines (`RegisterGlob`, `RegisterGrep`). |

Design rule: `ripgrep.go` knows nothing about tool schemas; `glob.go` / `grep.go` know nothing about how to spawn a subprocess. That split keeps each file understandable in isolation and gives exactly one place to change if we swap `rg` for a Go-native searcher later.

---

## Task 1: Ripgrep helper

**Files:**
- Create: `internal/tools/ripgrep.go`
- Test: `internal/tools/ripgrep_test.go`

**Contract of `runRipgrep`:**
- Signature: `func runRipgrep(ctx context.Context, args []string, cwd string) ([]byte, error)`.
- Spawns `rg` with the given args, cwd set to `cwd`.
- Exit code `0` → matches found. Returns stdout, nil.
- Exit code `1` → no matches (rg's convention). Returns empty stdout, nil. Not an error.
- Exit code ≥ `2` → actual error. Returns `(stdout, error)` where `error` wraps rg's stderr so callers can propagate a useful message.
- `rg` not on `PATH` → returns a sentinel error `ErrRipgrepMissing` so callers can distinguish "tool is missing" from "search failed".

- [ ] **Step 1: Write the failing test**

Create `internal/tools/ripgrep_test.go`:
```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/...
```
Expected: compile error — `runRipgrep` undefined, `ErrRipgrepMissing` undefined.

- [ ] **Step 3: Implement `runRipgrep`**

Create `internal/tools/ripgrep.go`:
```go
package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// ErrRipgrepMissing is returned when the `rg` binary cannot be found on PATH.
var ErrRipgrepMissing = errors.New("ripgrep (rg) not found on PATH")

// runRipgrep invokes `rg` with the given args and cwd. Exit code 0 returns
// stdout. Exit code 1 (rg's "no matches" signal) returns empty stdout and nil.
// Exit codes >= 2 return an error wrapping stderr.
func runRipgrep(ctx context.Context, args []string, cwd string) ([]byte, error) {
	path, err := exec.LookPath("rg")
	if err != nil {
		return nil, ErrRipgrepMissing
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil, nil
	}
	return stdout.Bytes(), fmt.Errorf("rg: %w: %s", err, stderr.String())
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run TestRunRipgrep -v
```
Expected: 4 PASS (or 3 PASS + 1 SKIP if rg is not installed — but the `TestRunRipgrep_MissingBinary` test always runs because it manipulates PATH itself).

Note: `TestRunRipgrep_MissingBinary` runs even without `rg` installed because `t.Setenv` is called before `exec.LookPath`. If the preceding tests skipped because of missing `rg`, that test still runs and passes — good indicator the error path works.

- [ ] **Step 5: Lint**

Run:
```bash
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox lint
```
Expected: exit 0.

- [ ] **Step 6: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/tools/ripgrep.go internal/tools/ripgrep_test.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(tools): add runRipgrep helper with exit-code-aware error handling
EOF
```

---

## Task 2: Glob tool

**Files:**
- Create: `internal/tools/glob.go`
- Test: `internal/tools/glob_test.go`
- Modify: `internal/server/server.go` — one additional registration line.

**Tool contract:**
- Name: `Glob`
- Parameters:
  - `pattern` (string, required) — e.g., `"**/*.go"`, `"src/**/*.ts"`. Same syntax as ripgrep's `-g` / `--glob`.
  - `path` (string, optional, default = workspace root) — directory to search within. Must be inside the workspace. Relative paths resolved against workspace root.
  - `limit` (number, optional, default 100) — maximum number of paths to return. Truncates after mtime sort.
- Behavior:
  - If `pattern` is missing → error.
  - If `path` resolves outside the workspace → error.
  - Invokes `rg --files --no-require-git --color=never -g <pattern>` with `cwd` set to `path` (or workspace root if not given). `.gitignore` is always respected (even without a `.git` directory, thanks to `--no-require-git`).
  - Stats each returned file and sorts by mtime descending. Ties broken by path lexicographic order for determinism.
  - Returns a newline-joined list of paths relative to the search-root cwd.
  - Empty result (no matches) returns an empty `TextResult` — not an error.

**Step 1: Write the failing Glob tests**

- [ ] Create `internal/tools/glob_test.go`:
```go
package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireRg(t *testing.T) {
	t.Helper()
	// Delegates to the same check `runRipgrep` does. We expose a tiny
	// helper in the test rather than exporting runRipgrep.
	if _, err := tools.LookupRipgrep(); err != nil {
		t.Skip("ripgrep (rg) not installed; skipping")
	}
}

func callGlob(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleGlob(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestGlob_MatchesPattern(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.go"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "c.txt"), []byte("x"), 0o644))

	res := callGlob(t, deps, map[string]any{"pattern": "**/*.go"})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "a.go")
	assert.Contains(t, body, "b.go")
	assert.NotContains(t, body, "c.txt")
}

func TestGlob_RespectsGitignore(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.go\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "kept.go"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ignored.go"), []byte("x"), 0o644))

	res := callGlob(t, deps, map[string]any{"pattern": "**/*.go"})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "kept.go")
	assert.NotContains(t, body, "ignored.go")
}

func TestGlob_SortsByMtimeDesc(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	older := filepath.Join(root, "older.go")
	newer := filepath.Join(root, "newer.go")
	require.NoError(t, os.WriteFile(older, []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(newer, []byte("x"), 0o644))

	// Force older file's mtime to be clearly earlier.
	past := time.Now().Add(-1 * time.Hour)
	require.NoError(t, os.Chtimes(older, past, past))

	res := callGlob(t, deps, map[string]any{"pattern": "**/*.go"})
	require.False(t, res.IsError)

	lines := strings.Split(strings.TrimRight(textOf(t, res), "\n"), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	assert.Equal(t, "newer.go", lines[0])
	assert.Equal(t, "older.go", lines[1])
}

func TestGlob_PathArgScopesSearch(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	sub := filepath.Join(root, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "top.go"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "nested.go"), []byte("x"), 0o644))

	res := callGlob(t, deps, map[string]any{"pattern": "**/*.go", "path": "sub"})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "nested.go")
	assert.NotContains(t, body, "top.go")
}

func TestGlob_PathOutsideWorkspace(t *testing.T) {
	requireRg(t)
	deps, _ := newTestDeps(t)
	res := callGlob(t, deps, map[string]any{"pattern": "**/*", "path": "/etc"})
	assert.True(t, res.IsError)
}

func TestGlob_MissingPattern(t *testing.T) {
	requireRg(t)
	deps, _ := newTestDeps(t)
	res := callGlob(t, deps, map[string]any{})
	assert.True(t, res.IsError)
}

func TestGlob_NoMatchesReturnsEmpty(t *testing.T) {
	requireRg(t)
	deps, _ := newTestDeps(t)

	res := callGlob(t, deps, map[string]any{"pattern": "**/*.impossible-extension"})
	require.False(t, res.IsError)
	assert.Empty(t, strings.TrimSpace(textOf(t, res)))
}

func TestGlob_LimitTruncates(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	for i := 0; i < 5; i++ {
		p := filepath.Join(root, strings.Repeat("x", i+1)+".go")
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
	}

	res := callGlob(t, deps, map[string]any{"pattern": "**/*.go", "limit": float64(2)})
	require.False(t, res.IsError)

	lines := strings.Split(strings.TrimRight(textOf(t, res), "\n"), "\n")
	assert.Len(t, lines, 2)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run TestGlob
```
Expected: compile error — `tools.HandleGlob` undefined, `tools.LookupRipgrep` undefined.

- [ ] **Step 3: Export a `LookupRipgrep` helper for tests**

The black-box tests need a way to detect "is rg installed?" without importing internal helpers. Add a small exported helper in `internal/tools/ripgrep.go`.

Append to `internal/tools/ripgrep.go`:
```go
// LookupRipgrep returns nil if `rg` is on PATH, or ErrRipgrepMissing otherwise.
// Intended for use by black-box tests that want to skip when rg is unavailable.
func LookupRipgrep() error {
	if _, err := exec.LookPath("rg"); err != nil {
		return ErrRipgrepMissing
	}
	return nil
}
```

- [ ] **Step 4: Implement `Glob`**

Create `internal/tools/glob.go`:
```go
package tools

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const defaultGlobLimit = 100

// RegisterGlob registers the Glob tool on the given MCP server.
func RegisterGlob(s *mcpserver.MCPServer, deps *Deps) {
	tool := mcp.NewTool("Glob",
		mcp.WithDescription("Find files matching a glob pattern. Respects .gitignore. Returns workspace-relative paths sorted by mtime (most recent first)."),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Glob pattern, e.g. '**/*.go' or 'src/**/*.ts'.")),
		mcp.WithString("path", mcp.Description("Directory to search within (workspace-relative or absolute). Defaults to workspace root.")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of paths to return (default 100).")),
	)
	s.AddTool(tool, HandleGlob(deps))
}

// HandleGlob returns the Glob tool handler.
func HandleGlob(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		pattern, _ := args["pattern"].(string)
		if pattern == "" {
			return ErrorResult("pattern is required"), nil
		}

		cwd := deps.Workspace.Root()
		if pathArg, ok := args["path"].(string); ok && pathArg != "" {
			abs, err := deps.Workspace.Resolve(pathArg)
			if err != nil {
				return ErrorResult("resolve path: %v", err), nil
			}
			info, err := os.Stat(abs)
			if err != nil {
				return ErrorResult("stat path: %v", err), nil
			}
			if !info.IsDir() {
				return ErrorResult("path is not a directory: %s", pathArg), nil
			}
			cwd = abs
		}

		limit := defaultGlobLimit
		if v, ok := args["limit"].(float64); ok && int(v) > 0 {
			limit = int(v)
		}

		rgArgs := []string{
			"--files",
			"--no-require-git",
			"--color=never",
			"-g", pattern,
		}
		out, err := runRipgrep(ctx, rgArgs, cwd)
		if err != nil {
			return ErrorResult("glob: %v", err), nil
		}

		paths := splitLines(out)
		sortByMtimeDesc(cwd, paths)
		if len(paths) > limit {
			paths = paths[:limit]
		}
		return TextResult(strings.Join(paths, "\n")), nil
	}
}

func splitLines(b []byte) []string {
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// sortByMtimeDesc sorts `paths` (each a path relative to `cwd`) in-place by
// descending mtime, breaking ties by path lexicographic order. Paths that
// cannot be stat'd drop to the end; their relative order is undefined.
func sortByMtimeDesc(cwd string, paths []string) {
	type entry struct {
		path  string
		mtime int64
		ok    bool
	}
	entries := make([]entry, len(paths))
	for i, p := range paths {
		info, err := os.Stat(filepath.Join(cwd, p))
		if err != nil {
			entries[i] = entry{path: p}
			continue
		}
		entries[i] = entry{path: p, mtime: info.ModTime().UnixNano(), ok: true}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].ok != entries[j].ok {
			return entries[i].ok
		}
		if entries[i].mtime != entries[j].mtime {
			return entries[i].mtime > entries[j].mtime
		}
		return entries[i].path < entries[j].path
	})
	for i, e := range entries {
		paths[i] = e.path
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run TestGlob -v
```
Expected: all 8 Glob tests PASS (or SKIP if rg is missing — but ideally PASS with rg installed).

- [ ] **Step 6: Register the tool in the server**

In `internal/server/server.go`, in `New`, after the existing `tools.RegisterEdit(...)` line, add:
```go
	tools.RegisterGlob(s.mcp, &tools.Deps{Workspace: s.ws, Tracker: s.tracker})
```

- [ ] **Step 7: Full test + lint**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./... -race -count=1
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox lint
```
Both must exit 0.

- [ ] **Step 8: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/tools/glob.go internal/tools/glob_test.go internal/tools/ripgrep.go internal/server/server.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(tools): add Glob tool with mtime-sorted, gitignore-aware discovery
EOF
```

---

## Task 3: Grep tool

**Files:**
- Create: `internal/tools/grep.go`
- Test: `internal/tools/grep_test.go`
- Modify: `internal/server/server.go` — one additional registration line.

**Tool contract:**
- Name: `Grep`
- Parameters:
  - `pattern` (string, required) — a ripgrep-compatible regex (Rust regex syntax).
  - `path` (string, optional) — file or directory to search. Defaults to workspace root.
  - `glob` (string, optional) — file glob filter, e.g. `"*.go"`.
  - `case_insensitive` (boolean, optional, default false) — adds `-i`.
  - `output_mode` (string, optional, default `"content"`) — one of `"content"`, `"files_with_matches"`, `"count"`.
  - `head_limit` (number, optional) — if set, truncates total output lines to that number.
- Behavior:
  - If `pattern` is missing → error.
  - If `path` resolves outside the workspace → error.
  - If `output_mode` is unknown → error.
  - Invokes `rg` with:
    - Always: `--no-require-git --color=never`
    - content: `-n --heading=false`
    - files_with_matches: `-l`
    - count: `-c`
    - Case-insensitive: `-i`
    - Glob filter: `-g <glob>`
    - Pattern separator: `--` then pattern
    - Optional path arg appended last
  - cwd is workspace root so rg emits workspace-relative paths in content and files modes.
  - No matches returns empty `TextResult` (not an error).
  - Invalid regex returns an error result with rg's stderr.
  - `head_limit` truncates the final output by line count.

- [ ] **Step 1: Write the failing Grep tests**

Create `internal/tools/grep_test.go`:
```go
package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callGrep(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleGrep(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestGrep_ContentMode(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("alpha\nbeta\nalpha again\n"), 0o644))

	res := callGrep(t, deps, map[string]any{"pattern": "alpha"})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "a.go:1:alpha")
	assert.Contains(t, body, "a.go:3:alpha again")
	assert.NotContains(t, body, "beta")
}

func TestGrep_FilesWithMatchesMode(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "has.go"), []byte("alpha\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "nope.go"), []byte("beta\n"), 0o644))

	res := callGrep(t, deps, map[string]any{"pattern": "alpha", "output_mode": "files_with_matches"})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "has.go")
	assert.NotContains(t, body, "nope.go")
}

func TestGrep_CountMode(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("alpha\nbeta\nalpha\n"), 0o644))

	res := callGrep(t, deps, map[string]any{"pattern": "alpha", "output_mode": "count"})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "a.go:2")
}

func TestGrep_CaseInsensitive(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("ALPHA\n"), 0o644))

	res := callGrep(t, deps, map[string]any{"pattern": "alpha", "case_insensitive": true})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "a.go:1:ALPHA")
}

func TestGrep_GlobFilter(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("alpha\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644))

	res := callGrep(t, deps, map[string]any{"pattern": "alpha", "glob": "*.go"})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "a.go")
	assert.NotContains(t, body, "a.txt")
}

func TestGrep_HeadLimit(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	var sb strings.Builder
	for i := 0; i < 10; i++ {
		sb.WriteString("alpha\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte(sb.String()), 0o644))

	res := callGrep(t, deps, map[string]any{"pattern": "alpha", "head_limit": float64(3)})
	require.False(t, res.IsError)

	body := textOf(t, res)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	assert.Len(t, lines, 3)
}

func TestGrep_NoMatchesReturnsEmpty(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("hello\n"), 0o644))

	res := callGrep(t, deps, map[string]any{"pattern": "zzzzz-no-such-pattern"})
	require.False(t, res.IsError)
	assert.Empty(t, strings.TrimSpace(textOf(t, res)))
}

func TestGrep_InvalidRegexIsError(t *testing.T) {
	requireRg(t)
	deps, _ := newTestDeps(t)

	res := callGrep(t, deps, map[string]any{"pattern": "[unclosed"})
	assert.True(t, res.IsError)
}

func TestGrep_PathOutsideWorkspace(t *testing.T) {
	requireRg(t)
	deps, _ := newTestDeps(t)

	res := callGrep(t, deps, map[string]any{"pattern": "x", "path": "/etc"})
	assert.True(t, res.IsError)
}

func TestGrep_MissingPattern(t *testing.T) {
	requireRg(t)
	deps, _ := newTestDeps(t)

	res := callGrep(t, deps, map[string]any{})
	assert.True(t, res.IsError)
}

func TestGrep_UnknownOutputMode(t *testing.T) {
	requireRg(t)
	deps, _ := newTestDeps(t)

	res := callGrep(t, deps, map[string]any{"pattern": "x", "output_mode": "nonsense"})
	assert.True(t, res.IsError)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run TestGrep
```
Expected: compile error — `tools.HandleGrep` undefined.

- [ ] **Step 3: Implement `Grep`**

Create `internal/tools/grep.go`:
```go
package tools

import (
	"context"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// RegisterGrep registers the Grep tool on the given MCP server.
func RegisterGrep(s *mcpserver.MCPServer, deps *Deps) {
	tool := mcp.NewTool("Grep",
		mcp.WithDescription("Search file contents with a regex. ripgrep-backed; respects .gitignore. Returns matches in the requested output_mode."),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Regex (Rust regex syntax).")),
		mcp.WithString("path", mcp.Description("File or directory to search. Defaults to workspace root.")),
		mcp.WithString("glob", mcp.Description("Glob filter, e.g. '*.go'.")),
		mcp.WithBoolean("case_insensitive", mcp.Description("Case-insensitive match.")),
		mcp.WithString("output_mode", mcp.Description("One of 'content' (default), 'files_with_matches', 'count'.")),
		mcp.WithNumber("head_limit", mcp.Description("Truncate output to this many lines.")),
	)
	s.AddTool(tool, HandleGrep(deps))
}

// HandleGrep returns the Grep tool handler.
func HandleGrep(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		pattern, _ := args["pattern"].(string)
		if pattern == "" {
			return ErrorResult("pattern is required"), nil
		}

		mode := "content"
		if v, ok := args["output_mode"].(string); ok && v != "" {
			mode = v
		}
		modeArgs, err := grepModeArgs(mode)
		if err != nil {
			return ErrorResult("%v", err), nil
		}

		rgArgs := []string{"--no-require-git", "--color=never"}
		rgArgs = append(rgArgs, modeArgs...)

		if v, _ := args["case_insensitive"].(bool); v {
			rgArgs = append(rgArgs, "-i")
		}
		if glob, ok := args["glob"].(string); ok && glob != "" {
			rgArgs = append(rgArgs, "-g", glob)
		}

		rgArgs = append(rgArgs, "--", pattern)

		cwd := deps.Workspace.Root()
		if pathArg, ok := args["path"].(string); ok && pathArg != "" {
			abs, err := deps.Workspace.Resolve(pathArg)
			if err != nil {
				return ErrorResult("resolve path: %v", err), nil
			}
			if _, err := os.Stat(abs); err != nil {
				return ErrorResult("stat path: %v", err), nil
			}
			rel, err := relToRoot(deps.Workspace.Root(), abs)
			if err != nil {
				return ErrorResult("relative path: %v", err), nil
			}
			if rel != "" {
				rgArgs = append(rgArgs, rel)
			}
		}

		out, err := runRipgrep(ctx, rgArgs, cwd)
		if err != nil {
			return ErrorResult("grep: %v", err), nil
		}

		body := string(out)
		if v, ok := args["head_limit"].(float64); ok && int(v) > 0 {
			body = truncateLines(body, int(v))
		}
		return TextResult(body), nil
	}
}

func grepModeArgs(mode string) ([]string, error) {
	switch mode {
	case "content":
		return []string{"-n", "--heading=false"}, nil
	case "files_with_matches":
		return []string{"-l"}, nil
	case "count":
		return []string{"-c"}, nil
	default:
		return nil, &unknownModeError{mode: mode}
	}
}

type unknownModeError struct{ mode string }

func (e *unknownModeError) Error() string {
	return "unknown output_mode: " + e.mode + " (valid: content, files_with_matches, count)"
}

func relToRoot(root, abs string) (string, error) {
	if abs == root {
		return "", nil
	}
	return filepath.Rel(root, abs)
}

func truncateLines(s string, n int) string {
	lines := strings.SplitAfter(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "")
}
```

Final `grep.go` import block:
```go
import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/... -run TestGrep -v
```
Expected: all 11 Grep tests PASS.

- [ ] **Step 5: Register the tool in the server**

In `internal/server/server.go`, in `New` after the `tools.RegisterGlob(...)` line, add:
```go
	tools.RegisterGrep(s.mcp, &tools.Deps{Workspace: s.ws, Tracker: s.tracker})
```

- [ ] **Step 6: Full test + race + lint + build sweep**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./... -race -count=1
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox lint
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox build ./...
```
All three must exit 0.

- [ ] **Step 7: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/tools/grep.go internal/tools/grep_test.go internal/server/server.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(tools): add Grep tool with ripgrep-backed content/files/count modes
EOF
```

---

## Task 4: Smoke-verify both tools are live

**Files:** none (manual verification)

This mirrors Plan 1's Task 8 but confirms the two new tools are wired. No new code, no commit.

- [ ] **Step 1: Build the binary**

```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox build -o /Users/chaholl/repos/altairalabs/codegen-sandbox/bin/sandbox ./cmd/sandbox
```
Expected: exit 0.

- [ ] **Step 2: Start the server against a temp workspace**

```bash
mkdir -p /tmp/codegen-sandbox-search-smoke
/Users/chaholl/repos/altairalabs/codegen-sandbox/bin/sandbox -addr=127.0.0.1:18081 -workspace=/tmp/codegen-sandbox-search-smoke >/tmp/sandbox-search-smoke.log 2>&1 &
echo $! > /tmp/sandbox-search-smoke.pid
```

- [ ] **Step 3: Initialize the MCP session and list tools**

```bash
curl -sS -N --max-time 4 --output /tmp/sandbox-search-sse.txt http://127.0.0.1:18081/sse 2>/dev/null &
SSEPID=$!
sleep 0.3
SESSION_URL=$(grep -o 'data:.*' /tmp/sandbox-search-sse.txt | head -1 | sed 's|data: *||' | tr -d '\r\n ')
curl -sS -X POST "http://127.0.0.1:18081${SESSION_URL}" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"smoke","version":"0"},"capabilities":{}}}' >/dev/null
curl -sS -X POST "http://127.0.0.1:18081${SESSION_URL}" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' >/dev/null
curl -sS -X POST "http://127.0.0.1:18081${SESSION_URL}" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' >/dev/null
sleep 1
kill $SSEPID 2>/dev/null || true
grep -o '"name":"[^"]*"' /tmp/sandbox-search-sse.txt | sort -u
```
Expected output includes at least:
```
"name":"Edit"
"name":"Glob"
"name":"Grep"
"name":"Read"
"name":"Write"
"name":"codegen-sandbox"
```

- [ ] **Step 4: Stop the server**

```bash
kill "$(cat /tmp/sandbox-search-smoke.pid)" 2>/dev/null || true
```

- [ ] **Step 5: No commit** (no file changes).

---

## Self-Review Notes

Covered from the spec:
- Glob: mtime-sorted, `.gitignore`-respecting, path-contained. Task 2.
- Grep: ripgrep-backed, `.gitignore`-respecting, structured output (`file:line:text`), multiple output modes. Task 3.
- Shared rg invocation: Task 1.
- End-to-end wire verification: Task 4.

Deliberately deferred:
- JSON output mode for Grep → revisit if agents want more structure.
- Multiline / context-before/after for Grep → add when agent workflows ask for it (one rg flag each).
- Binary detection / reject-binary-read → cross-cutting concern better handled in Plan 5's scrubbing layer.
- Streaming large outputs → for v1, read all stdout into memory and let `head_limit` protect the response size.

Placeholder scan: no TBDs, no "implement later", no "add appropriate error handling". The one place the plan mentions a judgment call ("If you find the wrapper overkill, inline `filepath.Rel`") names the two concrete options.

Type consistency: `runRipgrep` signature matches across Tasks 1/2/3. `Deps` matches the foundation's exported type. `Workspace.Resolve` / `Workspace.Root()` match Plan 1's exports. `newTestDeps` / `textOf` are shared helpers from Plan 1's `read_test.go` and are reused, not re-declared.
