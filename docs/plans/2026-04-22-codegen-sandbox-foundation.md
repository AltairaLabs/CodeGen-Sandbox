# Codegen Sandbox — Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Scaffold a Go MCP server that serves `Read`, `Write`, and `Edit` tools against a workspace root, with strict path containment and in-process read-tracking.

**Architecture:** A single Go binary (`cmd/sandbox`) starts an MCP server over HTTP+SSE using `mark3labs/mcp-go`. Every filesystem-touching tool routes path arguments through a `Workspace` that resolves them to absolute form, follows symlinks where possible, and rejects anything outside the workspace root. A per-process `ReadTracker` records which files have been `Read`; `Edit` always requires a prior `Read`, and `Write` requires a prior `Read` when overwriting an existing file.

**Tech Stack:** Go 1.24+, `github.com/mark3labs/mcp-go`, `github.com/stretchr/testify`, `golangci-lint`, standard library `testing`.

**Out of scope for this plan:** `Glob`, `Grep`, `Bash`, `run_*`, `WebFetch`, `WebSearch`, secret scrubbing, Docker packaging, multi-session support, post-edit lint feedback inside `Edit` (Plan 4 wires that through `run_lint`).

---

## File Structure

Files introduced by this plan:

| Path | Responsibility |
|---|---|
| `go.mod`, `go.sum` | Module declaration and dependency pins |
| `Makefile` | `build`, `test`, `lint`, `fmt` targets |
| `.gitignore` | Exclude build artifacts |
| `.golangci.yml` | Lint config |
| `cmd/sandbox/main.go` | Entry point: parses flags, builds workspace, starts SSE server |
| `internal/workspace/workspace.go` | `Workspace` type: `New`, `Root`, `Resolve`; path-containment logic |
| `internal/workspace/workspace_test.go` | Tests for path resolution (relative, abs, traversal, symlinks, missing files) |
| `internal/workspace/tracker.go` | `ReadTracker` — thread-safe record of which absolute paths have been read |
| `internal/workspace/tracker_test.go` | Tests for tracker |
| `internal/server/server.go` | `Server` wraps `*server.MCPServer` + registers tools; `Handler()` returns SSE http.Handler |
| `internal/server/server_test.go` | Smoke test: tools are registered with the expected names |
| `internal/tools/tools.go` | Shared helpers: argument extraction, error formatting, result builders |
| `internal/tools/read.go` | `Read` tool handler + registration |
| `internal/tools/read_test.go` | Handler tests for `Read` |
| `internal/tools/write.go` | `Write` tool handler + registration |
| `internal/tools/write_test.go` | Handler tests for `Write` |
| `internal/tools/edit.go` | `Edit` tool handler + registration |
| `internal/tools/edit_test.go` | Handler tests for `Edit` |

Design rules followed: one responsibility per file; path validation lives in `workspace`, transport wiring in `server`, tool semantics in `tools`. Tests live next to the code they cover.

---

## Task 1: Project scaffolding (bootstrap — exempt from TDD)

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `Makefile`
- Create: `.golangci.yml`
- Create: `cmd/sandbox/main.go`

- [ ] **Step 1: Initialize Go module**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox mod init github.com/altairalabs/codegen-sandbox
```
Expected: creates `go.mod` with `module github.com/altairalabs/codegen-sandbox` and `go 1.24` (adjust the `go` line to `1.24` if the installed Go prints a different minor).

- [ ] **Step 2: Add `.gitignore`**

Create `.gitignore`:
```
/bin/
*.out
*.test
.DS_Store
coverage.out
```

- [ ] **Step 3: Add `Makefile`**

Create `Makefile`:
```makefile
.PHONY: build test lint fmt tidy

build:
	go build -o bin/sandbox ./cmd/sandbox

test:
	go test ./...

lint:
	golangci-lint run

fmt:
	gofmt -w .
	goimports -w .

tidy:
	go mod tidy
```

- [ ] **Step 4: Add `.golangci.yml`**

Create `.golangci.yml`:
```yaml
run:
  timeout: 5m

linters:
  disable-all: true
  enable:
    - errcheck
    - govet
    - staticcheck
    - ineffassign
    - unused
    - gofmt
    - revive

linters-settings:
  revive:
    rules:
      - name: exported
        severity: warning
```

- [ ] **Step 5: Add placeholder `cmd/sandbox/main.go`**

Create `cmd/sandbox/main.go`:
```go
// Package main is the entry point for the codegen-sandbox MCP server.
package main

import "fmt"

func main() {
	fmt.Println("codegen-sandbox: not yet wired")
}
```

- [ ] **Step 6: Add mcp-go and testify dependencies**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox get github.com/mark3labs/mcp-go@latest
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox get github.com/stretchr/testify@latest
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox mod tidy
```
Expected: `go.mod` lists both modules; `go.sum` is populated.

- [ ] **Step 7: Verify it builds**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox build ./...
```
Expected: exits 0 with no output.

- [ ] **Step 8: Verify golangci-lint runs clean**

Run:
```bash
golangci-lint run --config /Users/chaholl/repos/altairalabs/codegen-sandbox/.golangci.yml /Users/chaholl/repos/altairalabs/codegen-sandbox/...
```
Expected: exits 0 with no findings.

- [ ] **Step 9: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add go.mod go.sum .gitignore Makefile .golangci.yml cmd/sandbox/main.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
chore: scaffold go module, lint config, and placeholder entry point
EOF
```

---

## Task 2: Workspace path containment

**Files:**
- Create: `internal/workspace/workspace.go`
- Test: `internal/workspace/workspace_test.go`

**Contract of `Workspace`:**
- `New(root string) (*Workspace, error)` — `root` must be absolute. The root itself is canonicalized with `EvalSymlinks` so a symlinked root works. Returns error if root doesn't exist, isn't a directory, or isn't absolute.
- `Root() string` — the canonical absolute root.
- `Resolve(p string) (string, error)` — accepts absolute or workspace-relative paths. Returns canonical absolute form, guaranteed to live inside the root. Supports paths to not-yet-existing files (resolves the deepest existing ancestor, then rejoins the remaining components). Rejects traversal, absolute paths outside root, and symlinks pointing outside root.

- [ ] **Step 1: Write the failing test for `New`**

Create `internal/workspace/workspace_test.go`:
```go
package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_RejectsRelativeRoot(t *testing.T) {
	_, err := workspace.New("relative/path")
	require.Error(t, err)
}

func TestNew_RejectsMissingRoot(t *testing.T) {
	_, err := workspace.New("/nonexistent/codegen-sandbox-test-root")
	require.Error(t, err)
}

func TestNew_RejectsFileAsRoot(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "not-a-dir")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = workspace.New(f.Name())
	require.Error(t, err)
}

func TestNew_AcceptsAbsoluteDirectory(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	canonical, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	assert.Equal(t, canonical, ws.Root())
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/workspace/...
```
Expected: compile error — package `workspace` does not exist.

- [ ] **Step 3: Write minimal `New` implementation**

Create `internal/workspace/workspace.go`:
```go
// Package workspace enforces path containment for sandbox filesystem tools.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrOutsideWorkspace is returned when a path resolves outside the workspace root.
var ErrOutsideWorkspace = errors.New("path is outside workspace root")

// Workspace is a container-scoped filesystem boundary.
type Workspace struct {
	root string // canonical absolute, symlinks resolved
}

// New constructs a Workspace rooted at the given absolute directory.
func New(root string) (*Workspace, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("workspace root must be absolute: %q", root)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return nil, fmt.Errorf("stat workspace root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root is not a directory: %q", canonical)
	}
	return &Workspace{root: canonical}, nil
}

// Root returns the canonical absolute workspace root.
func (w *Workspace) Root() string {
	return w.root
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/workspace/...
```
Expected: PASS.

- [ ] **Step 5: Write failing tests for `Resolve`**

Append to `internal/workspace/workspace_test.go`:
```go
func TestResolve_RelativePath(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	canonicalDir, _ := filepath.EvalSymlinks(dir)
	resolved, err := ws.Resolve("foo/bar.txt")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(canonicalDir, "foo", "bar.txt"), resolved)
}

func TestResolve_AbsolutePathInsideRoot(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	canonicalDir, _ := filepath.EvalSymlinks(dir)
	target := filepath.Join(canonicalDir, "a", "b.txt")

	resolved, err := ws.Resolve(target)
	require.NoError(t, err)
	assert.Equal(t, target, resolved)
}

func TestResolve_TraversalEscape(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	_, err = ws.Resolve("../etc/passwd")
	require.ErrorIs(t, err, workspace.ErrOutsideWorkspace)
}

func TestResolve_AbsoluteOutsideRoot(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	_, err = ws.Resolve("/etc/passwd")
	require.ErrorIs(t, err, workspace.ErrOutsideWorkspace)
}

func TestResolve_SymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(dir, "escape")
	require.NoError(t, os.Symlink(outside, link))

	ws, err := workspace.New(dir)
	require.NoError(t, err)

	_, err = ws.Resolve("escape/secrets")
	require.ErrorIs(t, err, workspace.ErrOutsideWorkspace)
}

func TestResolve_NonExistentFileInsideRoot(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	canonicalDir, _ := filepath.EvalSymlinks(dir)
	resolved, err := ws.Resolve("new/nested/file.txt")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(canonicalDir, "new", "nested", "file.txt"), resolved)
}

func TestResolve_ExistingFileViaSymlinkInsideRoot(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	require.NoError(t, os.WriteFile(real, []byte("x"), 0o644))
	link := filepath.Join(dir, "link.txt")
	require.NoError(t, os.Symlink(real, link))

	ws, err := workspace.New(dir)
	require.NoError(t, err)

	canonicalDir, _ := filepath.EvalSymlinks(dir)
	resolved, err := ws.Resolve("link.txt")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(canonicalDir, "real.txt"), resolved)
}
```

- [ ] **Step 6: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/workspace/...
```
Expected: compile error — `ws.Resolve` undefined.

- [ ] **Step 7: Implement `Resolve`**

First, extend the existing `import` block in `internal/workspace/workspace.go` to include `"strings"`. Then append the following to the same file:
```go
// Resolve returns the canonical absolute form of p, guaranteed to live inside
// the workspace root. If p is relative it is joined against the root. Symlinks
// on existing path components are resolved. If the target does not exist, the
// deepest existing ancestor is resolved and the remaining components are
// rejoined, which supports "I want to write a new file" lookups.
func (w *Workspace) Resolve(p string) (string, error) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(w.root, p)
	}
	p = filepath.Clean(p)

	resolved, err := evalSymlinksAllowMissing(p)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	rel, err := filepath.Rel(w.root, resolved)
	if err != nil {
		return "", fmt.Errorf("compute relative path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrOutsideWorkspace
	}
	return resolved, nil
}

// evalSymlinksAllowMissing behaves like filepath.EvalSymlinks but tolerates a
// path whose tail components do not exist yet. It walks up until it finds an
// existing ancestor, resolves that, then rejoins the missing suffix.
func evalSymlinksAllowMissing(p string) (string, error) {
	p = filepath.Clean(p)
	var missing []string
	cur := p
	for {
		if _, err := os.Lstat(cur); err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("no existing ancestor for path: %q", p)
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
	}
}
```

- [ ] **Step 8: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/workspace/...
```
Expected: PASS (all 8 tests).

- [ ] **Step 9: Lint**

Run:
```bash
golangci-lint run --config /Users/chaholl/repos/altairalabs/codegen-sandbox/.golangci.yml /Users/chaholl/repos/altairalabs/codegen-sandbox/internal/workspace/...
```
Expected: exits 0.

- [ ] **Step 10: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/workspace/workspace.go internal/workspace/workspace_test.go go.sum
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(workspace): path containment with symlink and traversal rejection
EOF
```

---

## Task 3: Read tracker

**Files:**
- Create: `internal/workspace/tracker.go`
- Test: `internal/workspace/tracker_test.go`

A `ReadTracker` records which absolute paths have been read during the current session. `Edit` requires a prior read; `Write` requires a prior read only when the target file already exists. Keying is by canonical absolute path (post-`Resolve`), so symlink vs real path can't be exploited to bypass the check.

- [ ] **Step 1: Write the failing tracker test**

Create `internal/workspace/tracker_test.go`:
```go
package workspace_test

import (
	"sync"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/assert"
)

func TestTracker_UnseenPathIsNotRead(t *testing.T) {
	tr := workspace.NewReadTracker()
	assert.False(t, tr.HasBeenRead("/workspace/foo.txt"))
}

func TestTracker_MarkThenQuery(t *testing.T) {
	tr := workspace.NewReadTracker()
	tr.MarkRead("/workspace/foo.txt")
	assert.True(t, tr.HasBeenRead("/workspace/foo.txt"))
	assert.False(t, tr.HasBeenRead("/workspace/other.txt"))
}

func TestTracker_ConcurrentAccess(t *testing.T) {
	tr := workspace.NewReadTracker()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.MarkRead("/workspace/foo.txt")
			_ = tr.HasBeenRead("/workspace/foo.txt")
		}()
	}
	wg.Wait()
	assert.True(t, tr.HasBeenRead("/workspace/foo.txt"))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/workspace/...
```
Expected: compile error — `NewReadTracker` undefined.

- [ ] **Step 3: Implement the tracker**

Create `internal/workspace/tracker.go`:
```go
package workspace

import "sync"

// ReadTracker records which absolute paths have been Read in the current session.
// It is safe for concurrent use.
type ReadTracker struct {
	mu    sync.RWMutex
	paths map[string]struct{}
}

// NewReadTracker constructs an empty tracker.
func NewReadTracker() *ReadTracker {
	return &ReadTracker{paths: make(map[string]struct{})}
}

// MarkRead records that the given absolute path has been read.
func (t *ReadTracker) MarkRead(absPath string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.paths[absPath] = struct{}{}
}

// HasBeenRead reports whether the given absolute path has been read.
func (t *ReadTracker) HasBeenRead(absPath string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.paths[absPath]
	return ok
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/workspace/... -race
```
Expected: PASS (including under `-race`).

- [ ] **Step 5: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/workspace/tracker.go internal/workspace/tracker_test.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(workspace): add concurrent-safe read tracker
EOF
```

---

## Task 4: MCP server skeleton + tool helpers

**Files:**
- Create: `internal/server/server.go`
- Test: `internal/server/server_test.go`
- Create: `internal/tools/tools.go`

The `Server` wires together a `Workspace`, a `ReadTracker`, and a `*server.MCPServer` from `mark3labs/mcp-go`. Tools are registered by the `internal/tools` package so `Server` stays transport-focused. This task produces a server with zero tools registered; Tasks 5–7 add them.

- [ ] **Step 1: Write the failing server test**

Create `internal/server/server_test.go`:
```go
package server_test

import (
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/server"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/require"
)

func TestServer_New(t *testing.T) {
	ws, err := workspace.New(t.TempDir())
	require.NoError(t, err)

	srv, err := server.New(ws)
	require.NoError(t, err)
	require.NotNil(t, srv)
	require.NotNil(t, srv.Handler())
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/server/...
```
Expected: compile error — package `server` does not exist.

- [ ] **Step 3: Write `internal/tools/tools.go` with shared helpers**

Create `internal/tools/tools.go`:
```go
// Package tools hosts MCP tool handlers for the codegen sandbox.
package tools

import (
	"fmt"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
)

// Deps carries the dependencies a tool handler needs.
type Deps struct {
	Workspace *workspace.Workspace
	Tracker   *workspace.ReadTracker
}

// errorResult wraps a user-visible message as an MCP error result.
// Tool handlers should return (errorResult(msg), nil) rather than a Go error
// for user-caused failures; Go errors are reserved for transport-level faults.
func errorResult(format string, args ...any) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf(format, args...))
}

// textResult wraps a plain text body.
func textResult(body string) *mcp.CallToolResult {
	return mcp.NewToolResultText(body)
}
```

- [ ] **Step 4: Write `internal/server/server.go`**

Create `internal/server/server.go`:
```go
// Package server wires the codegen-sandbox MCP server and its HTTP+SSE transport.
package server

import (
	"net/http"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Server is the codegen sandbox MCP server.
type Server struct {
	mcp     *mcpserver.MCPServer
	sse     *mcpserver.SSEServer
	ws      *workspace.Workspace
	tracker *workspace.ReadTracker
}

// New constructs a Server bound to the given workspace.
func New(ws *workspace.Workspace) (*Server, error) {
	mcpSrv := mcpserver.NewMCPServer(
		"codegen-sandbox",
		"0.1.0",
		mcpserver.WithToolCapabilities(true),
	)
	s := &Server{
		mcp:     mcpSrv,
		ws:      ws,
		tracker: workspace.NewReadTracker(),
	}
	s.sse = mcpserver.NewSSEServer(mcpSrv)
	return s, nil
}

// Handler returns the SSE http.Handler for this server.
func (s *Server) Handler() http.Handler { return s.sse }

// MCP exposes the underlying MCP server for tool registration.
func (s *Server) MCP() *mcpserver.MCPServer { return s.mcp }

// Workspace exposes the bound workspace.
func (s *Server) Workspace() *workspace.Workspace { return s.ws }

// Tracker exposes the bound read tracker.
func (s *Server) Tracker() *workspace.ReadTracker { return s.tracker }
```

*Note:* The exact `mcp-go` API may differ slightly across minor versions (option names, SSE constructor shape, whether `SSEServer` itself is an `http.Handler` or exposes one). If a symbol above doesn't resolve, consult the installed version in `$GOPATH/pkg/mod/github.com/mark3labs/mcp-go@*` and adjust to the nearest equivalent. The shape — register tools on an MCP server, wrap with SSE, expose as `http.Handler` — is stable.

- [ ] **Step 5: Run server tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/server/...
```
Expected: PASS.

- [ ] **Step 6: Wire `cmd/sandbox/main.go` to start the server**

Replace `cmd/sandbox/main.go` with:
```go
// Package main is the entry point for the codegen-sandbox MCP server.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/altairalabs/codegen-sandbox/internal/server"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	root := flag.String("workspace", "/workspace", "workspace root (absolute path)")
	flag.Parse()

	ws, err := workspace.New(*root)
	if err != nil {
		log.Fatalf("workspace: %v", err)
	}

	srv, err := server.New(ws)
	if err != nil {
		log.Fatalf("server: %v", err)
	}

	log.Printf("codegen-sandbox listening on %s (workspace=%s)", *addr, ws.Root())
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 7: Smoke-build the binary**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox build ./...
```
Expected: exits 0.

- [ ] **Step 8: Lint**

Run:
```bash
golangci-lint run --config /Users/chaholl/repos/altairalabs/codegen-sandbox/.golangci.yml /Users/chaholl/repos/altairalabs/codegen-sandbox/...
```
Expected: exits 0.

- [ ] **Step 9: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/server internal/tools/tools.go cmd/sandbox/main.go go.sum
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(server): MCP server skeleton with SSE transport and ping tool
EOF
```

---

## Task 5: Read tool

**Files:**
- Create: `internal/tools/read.go`
- Test: `internal/tools/read_test.go`
- Modify: `internal/server/server.go`

**Tool contract:**
- Name: `Read`
- Parameters: `file_path` (string, required), `offset` (number, optional, default 0 — 1-based line to start at; 0 and 1 both mean "start"), `limit` (number, optional, default 2000).
- Returns text: for each line in the selected range, `LINENO\tCONTENT` (tab separator, line number 1-indexed, no trailing whitespace changes). This matches `cat -n` semantics used by other codegen tools.
- Side effect: on success, calls `tracker.MarkRead(absolute-path)`.
- Errors (as MCP error results, not Go errors):
  - missing `file_path`
  - path outside workspace
  - file does not exist
  - target is a directory
  - `offset` > total lines

- [ ] **Step 1: Write the failing Read tests**

Create `internal/tools/read_test.go`:
```go
package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDeps(t *testing.T) (*tools.Deps, string) {
	t.Helper()
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)
	return &tools.Deps{Workspace: ws, Tracker: workspace.NewReadTracker()}, ws.Root()
}

func callRead(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleRead(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestRead_ReturnsNumberedLines(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "hello.txt")
	require.NoError(t, os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644))

	res := callRead(t, deps, map[string]any{"file_path": path})
	require.False(t, res.IsError, "unexpected error: %v", res.Content)

	body := textOf(t, res)
	assert.Contains(t, body, "1\talpha")
	assert.Contains(t, body, "2\tbeta")
	assert.Contains(t, body, "3\tgamma")
}

func TestRead_MarksFileAsRead(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "seen.txt")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	_ = callRead(t, deps, map[string]any{"file_path": path})
	assert.True(t, deps.Tracker.HasBeenRead(path))
}

func TestRead_MissingFilePath(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callRead(t, deps, map[string]any{})
	assert.True(t, res.IsError)
}

func TestRead_PathOutsideWorkspace(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callRead(t, deps, map[string]any{"file_path": "/etc/passwd"})
	assert.True(t, res.IsError)
}

func TestRead_FileNotFound(t *testing.T) {
	deps, root := newTestDeps(t)
	res := callRead(t, deps, map[string]any{"file_path": filepath.Join(root, "missing.txt")})
	assert.True(t, res.IsError)
}

func TestRead_DirectoryIsError(t *testing.T) {
	deps, root := newTestDeps(t)
	res := callRead(t, deps, map[string]any{"file_path": root})
	assert.True(t, res.IsError)
}

func TestRead_OffsetAndLimit(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "many.txt")
	require.NoError(t, os.WriteFile(path, []byte("l1\nl2\nl3\nl4\nl5\n"), 0o644))

	res := callRead(t, deps, map[string]any{"file_path": path, "offset": float64(2), "limit": float64(2)})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.NotContains(t, body, "1\tl1")
	assert.Contains(t, body, "2\tl2")
	assert.Contains(t, body, "3\tl3")
	assert.NotContains(t, body, "4\tl4")
}

func textOf(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
```

*Note:* MCP tool arguments arrive as JSON-unmarshaled values — numbers are `float64`. Tests reflect that.

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/...
```
Expected: compile error — `HandleRead` undefined.

- [ ] **Step 3: Implement `Read`**

Create `internal/tools/read.go`:
```go
package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const defaultReadLimit = 2000

// RegisterRead registers the Read tool with the given MCP server.
func RegisterRead(s *mcpserver.MCPServer, deps *Deps) {
	tool := mcp.NewTool("Read",
		mcp.WithDescription("Read a file from the workspace. Returns cat -n style line-numbered text."),
		mcp.WithString("file_path", mcp.Required(), mcp.Description("Absolute or workspace-relative path.")),
		mcp.WithNumber("offset", mcp.Description("1-based line to start at (default 1).")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of lines to return (default 2000).")),
	)
	s.AddTool(tool, HandleRead(deps))
}

// HandleRead returns the Read tool handler.
func HandleRead(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filePath, ok := req.Params.Arguments["file_path"].(string)
		if !ok || filePath == "" {
			return errorResult("file_path is required"), nil
		}

		abs, err := deps.Workspace.Resolve(filePath)
		if err != nil {
			return errorResult("resolve path: %v", err), nil
		}

		info, err := os.Stat(abs)
		if err != nil {
			return errorResult("stat: %v", err), nil
		}
		if info.IsDir() {
			return errorResult("path is a directory: %s", filePath), nil
		}

		offset := 1
		if v, ok := req.Params.Arguments["offset"].(float64); ok && int(v) > 1 {
			offset = int(v)
		}
		limit := defaultReadLimit
		if v, ok := req.Params.Arguments["limit"].(float64); ok && int(v) > 0 {
			limit = int(v)
		}

		body, err := readNumbered(abs, offset, limit)
		if err != nil {
			return errorResult("read: %v", err), nil
		}

		deps.Tracker.MarkRead(abs)
		return textResult(body), nil
	}
}

func readNumbered(abs string, offset, limit int) (string, error) {
	f, err := os.Open(abs) //nolint:gosec // path already contained by workspace
	if err != nil {
		return "", err
	}
	defer f.Close()

	var out strings.Builder
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	lineNo := 0
	written := 0
	for scanner.Scan() {
		lineNo++
		if lineNo < offset {
			continue
		}
		if written >= limit {
			break
		}
		fmt.Fprintf(&out, "%d\t%s\n", lineNo, scanner.Text())
		written++
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if written == 0 && lineNo > 0 && offset > lineNo {
		return "", fmt.Errorf("offset %d exceeds line count %d", offset, lineNo)
	}
	return out.String(), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/...
```
Expected: PASS (all 7 `TestRead_*` tests).

- [ ] **Step 5: Register the tool in the server**

In `internal/server/server.go`, replace the body of `New` — specifically, after `s.sse = mcpserver.NewSSEServer(mcpSrv)` and before `s.registerPing()`, add:

```go
	tools.RegisterRead(s.mcp, &tools.Deps{Workspace: s.ws, Tracker: s.tracker})
```

Add `"github.com/altairalabs/codegen-sandbox/internal/tools"` to the import block.

You can also drop the `registerPing` call and its helper now that real tools exist — update the test to expect the server is constructible without checking specific tool registration.

- [ ] **Step 6: Re-run all tests and lint**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./... -race
golangci-lint run --config /Users/chaholl/repos/altairalabs/codegen-sandbox/.golangci.yml /Users/chaholl/repos/altairalabs/codegen-sandbox/...
```
Expected: both exit 0.

- [ ] **Step 7: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/tools/read.go internal/tools/read_test.go internal/server/server.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(tools): add Read tool with line-numbered output and offset/limit
EOF
```

---

## Task 6: Write tool

**Files:**
- Create: `internal/tools/write.go`
- Test: `internal/tools/write_test.go`
- Modify: `internal/server/server.go`

**Tool contract:**
- Name: `Write`
- Parameters: `file_path` (string, required), `content` (string, required).
- Behavior: if the target file already exists, the absolute path MUST have been `Read` first; otherwise return an error. Creates parent directories as needed (mode `0o755`). Writes the file atomically (write to `<target>.tmp.<pid>.<nanos>` then `Rename`) with mode `0o644`.
- Side effect: on success, `tracker.MarkRead(absolute-path)` — so subsequent edits don't require a re-read of the file we just wrote.
- Errors: missing params, path outside workspace, overwrite without prior Read, target is a directory.

- [ ] **Step 1: Write the failing Write tests**

Create `internal/tools/write_test.go`:
```go
package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callWrite(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleWrite(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestWrite_CreateNewFile(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "sub", "new.txt")

	res := callWrite(t, deps, map[string]any{"file_path": path, "content": "hello"})
	require.False(t, res.IsError, "unexpected error: %v", res.Content)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
	assert.True(t, deps.Tracker.HasBeenRead(path), "Write should mark file as read")
}

func TestWrite_OverwriteRequiresPriorRead(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "existing.txt")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))

	res := callWrite(t, deps, map[string]any{"file_path": path, "content": "new"})
	assert.True(t, res.IsError, "overwrite without prior read must fail")

	data, _ := os.ReadFile(path)
	assert.Equal(t, "old", string(data), "file must not be modified on error")
}

func TestWrite_OverwriteAfterRead(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "existing.txt")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))
	deps.Tracker.MarkRead(path)

	res := callWrite(t, deps, map[string]any{"file_path": path, "content": "new"})
	require.False(t, res.IsError)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "new", string(data))
}

func TestWrite_MissingFilePath(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callWrite(t, deps, map[string]any{"content": "x"})
	assert.True(t, res.IsError)
}

func TestWrite_MissingContent(t *testing.T) {
	deps, root := newTestDeps(t)
	res := callWrite(t, deps, map[string]any{"file_path": filepath.Join(root, "x.txt")})
	assert.True(t, res.IsError)
}

func TestWrite_PathOutsideWorkspace(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callWrite(t, deps, map[string]any{"file_path": "/etc/evil", "content": "x"})
	assert.True(t, res.IsError)
}

func TestWrite_RejectsDirectoryTarget(t *testing.T) {
	deps, root := newTestDeps(t)
	sub := filepath.Join(root, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	deps.Tracker.MarkRead(sub) // even with prior "read", a dir target is invalid

	res := callWrite(t, deps, map[string]any{"file_path": sub, "content": "x"})
	assert.True(t, res.IsError)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/...
```
Expected: compile error — `HandleWrite` undefined.

- [ ] **Step 3: Implement `Write`**

Create `internal/tools/write.go`:
```go
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// RegisterWrite registers the Write tool.
func RegisterWrite(s *mcpserver.MCPServer, deps *Deps) {
	tool := mcp.NewTool("Write",
		mcp.WithDescription("Write a file. Overwriting an existing file requires a prior Read."),
		mcp.WithString("file_path", mcp.Required()),
		mcp.WithString("content", mcp.Required()),
	)
	s.AddTool(tool, HandleWrite(deps))
}

// HandleWrite returns the Write tool handler.
func HandleWrite(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filePath, ok := req.Params.Arguments["file_path"].(string)
		if !ok || filePath == "" {
			return errorResult("file_path is required"), nil
		}
		content, ok := req.Params.Arguments["content"].(string)
		if !ok {
			return errorResult("content is required"), nil
		}

		abs, err := deps.Workspace.Resolve(filePath)
		if err != nil {
			return errorResult("resolve path: %v", err), nil
		}

		if info, statErr := os.Stat(abs); statErr == nil {
			if info.IsDir() {
				return errorResult("path is a directory: %s", filePath), nil
			}
			if !deps.Tracker.HasBeenRead(abs) {
				return errorResult("refusing to overwrite %s: Read it first", filePath), nil
			}
		}

		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return errorResult("mkdir: %v", err), nil
		}

		if err := atomicWrite(abs, []byte(content)); err != nil {
			return errorResult("write: %v", err), nil
		}

		deps.Tracker.MarkRead(abs)
		return textResult(fmt.Sprintf("wrote %d bytes to %s", len(content), filePath)), nil
	}
}

func atomicWrite(abs string, data []byte) error {
	tmp := fmt.Sprintf("%s.tmp.%d.%d", abs, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/...
```
Expected: PASS (all `TestWrite_*` plus prior `TestRead_*`).

- [ ] **Step 5: Register `Write` on the server**

In `internal/server/server.go`, in `New` after `tools.RegisterRead(...)`, add:
```go
	tools.RegisterWrite(s.mcp, &tools.Deps{Workspace: s.ws, Tracker: s.tracker})
```

- [ ] **Step 6: Re-run full test + lint**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./... -race
golangci-lint run --config /Users/chaholl/repos/altairalabs/codegen-sandbox/.golangci.yml /Users/chaholl/repos/altairalabs/codegen-sandbox/...
```
Expected: both exit 0.

- [ ] **Step 7: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/tools/write.go internal/tools/write_test.go internal/server/server.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(tools): add Write tool with overwrite-requires-read and atomic rename
EOF
```

---

## Task 7: Edit tool

**Files:**
- Create: `internal/tools/edit.go`
- Test: `internal/tools/edit_test.go`
- Modify: `internal/server/server.go`

**Tool contract:**
- Name: `Edit`
- Parameters: `file_path` (string, required), `old_string` (string, required), `new_string` (string, required), `replace_all` (boolean, optional, default false).
- Behavior:
  - File must exist and must have been `Read` in this session.
  - If `replace_all` is false and `old_string` occurs more than once → error (ask caller to add context).
  - If `old_string` does not occur → error.
  - Replace (either first occurrence or all, per flag) and atomic-write the result.
- Side effect: rewrites the file, leaves the read-tracker entry in place (the file is still "seen").
- Errors: missing params, path outside workspace, file not found, file not read, `old_string` not found, multiple matches without `replace_all`.
- Post-edit lint feedback is Plan 4's job; don't add it here.

- [ ] **Step 1: Write the failing Edit tests**

Create `internal/tools/edit_test.go`:
```go
package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callEdit(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleEdit(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func writeAndMarkRead(t *testing.T, deps *tools.Deps, path, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	deps.Tracker.MarkRead(path)
}

func TestEdit_ReplacesFirstOccurrence(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha\nbeta\nalpha\n")

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "gamma",
	})
	assert.True(t, res.IsError, "non-unique match without replace_all must fail")
}

func TestEdit_UniqueReplace(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha\nbeta\n")

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "gamma",
	})
	require.False(t, res.IsError, "unexpected error: %v", res.Content)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "gamma\nbeta\n", string(data))
}

func TestEdit_ReplaceAll(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha alpha alpha")

	res := callEdit(t, deps, map[string]any{
		"file_path":   path,
		"old_string":  "alpha",
		"new_string":  "gamma",
		"replace_all": true,
	})
	require.False(t, res.IsError)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "gamma gamma gamma", string(data))
}

func TestEdit_RequiresPriorRead(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	require.NoError(t, os.WriteFile(path, []byte("alpha"), 0o644))
	// intentionally do not mark as read

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "beta",
	})
	assert.True(t, res.IsError)
}

func TestEdit_OldStringNotFound(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha")

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "zulu",
		"new_string": "beta",
	})
	assert.True(t, res.IsError)
}

func TestEdit_MissingFile(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "nope.txt")

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "x",
		"new_string": "y",
	})
	assert.True(t, res.IsError)
}

func TestEdit_PathOutsideWorkspace(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callEdit(t, deps, map[string]any{
		"file_path":  "/etc/passwd",
		"old_string": "root",
		"new_string": "evil",
	})
	assert.True(t, res.IsError)
}

func TestEdit_MissingParams(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha")

	cases := []map[string]any{
		{"old_string": "alpha", "new_string": "beta"},
		{"file_path": path, "new_string": "beta"},
		{"file_path": path, "old_string": "alpha"},
	}
	for _, args := range cases {
		res := callEdit(t, deps, args)
		assert.True(t, res.IsError, "args=%v", args)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/...
```
Expected: compile error — `HandleEdit` undefined.

- [ ] **Step 3: Implement `Edit`**

Create `internal/tools/edit.go`:
```go
package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// RegisterEdit registers the Edit tool.
func RegisterEdit(s *mcpserver.MCPServer, deps *Deps) {
	tool := mcp.NewTool("Edit",
		mcp.WithDescription("Exact-string replace within a file. Requires a prior Read."),
		mcp.WithString("file_path", mcp.Required()),
		mcp.WithString("old_string", mcp.Required()),
		mcp.WithString("new_string", mcp.Required()),
		mcp.WithBoolean("replace_all", mcp.Description("If true, replace every occurrence; default false.")),
	)
	s.AddTool(tool, HandleEdit(deps))
}

// HandleEdit returns the Edit tool handler.
func HandleEdit(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filePath, ok := req.Params.Arguments["file_path"].(string)
		if !ok || filePath == "" {
			return errorResult("file_path is required"), nil
		}
		oldStr, ok := req.Params.Arguments["old_string"].(string)
		if !ok {
			return errorResult("old_string is required"), nil
		}
		newStr, ok := req.Params.Arguments["new_string"].(string)
		if !ok {
			return errorResult("new_string is required"), nil
		}
		replaceAll, _ := req.Params.Arguments["replace_all"].(bool)

		abs, err := deps.Workspace.Resolve(filePath)
		if err != nil {
			return errorResult("resolve path: %v", err), nil
		}

		info, err := os.Stat(abs)
		if err != nil {
			return errorResult("stat: %v", err), nil
		}
		if info.IsDir() {
			return errorResult("path is a directory: %s", filePath), nil
		}
		if !deps.Tracker.HasBeenRead(abs) {
			return errorResult("refusing to edit %s: Read it first", filePath), nil
		}

		data, err := os.ReadFile(abs) //nolint:gosec // workspace-contained
		if err != nil {
			return errorResult("read: %v", err), nil
		}
		body := string(data)

		count := strings.Count(body, oldStr)
		if count == 0 {
			return errorResult("old_string not found in %s", filePath), nil
		}
		if count > 1 && !replaceAll {
			return errorResult("old_string matched %d times in %s; add context or set replace_all=true", count, filePath), nil
		}

		var updated string
		if replaceAll {
			updated = strings.ReplaceAll(body, oldStr, newStr)
		} else {
			updated = strings.Replace(body, oldStr, newStr, 1)
		}

		if err := atomicWrite(abs, []byte(updated)); err != nil {
			return errorResult("write: %v", err), nil
		}
		return textResult(fmt.Sprintf("replaced %d occurrence(s) in %s", count, filePath)), nil
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/tools/...
```
Expected: PASS (all `TestEdit_*`, `TestWrite_*`, `TestRead_*`).

- [ ] **Step 5: Register `Edit` on the server**

In `internal/server/server.go`, in `New` after `tools.RegisterWrite(...)`, add:
```go
	tools.RegisterEdit(s.mcp, &tools.Deps{Workspace: s.ws, Tracker: s.tracker})
```

- [ ] **Step 6: Full test + race + lint sweep**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./... -race
golangci-lint run --config /Users/chaholl/repos/altairalabs/codegen-sandbox/.golangci.yml /Users/chaholl/repos/altairalabs/codegen-sandbox/...
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox build ./...
```
Expected: all three exit 0.

- [ ] **Step 7: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/tools/edit.go internal/tools/edit_test.go internal/server/server.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(tools): add Edit tool with unique-match enforcement and replace_all
EOF
```

---

## Task 8: Manual end-to-end smoke

**Files:** none (manual verification)

Purpose: prove the wired binary actually serves a working MCP endpoint before we declare Plan 1 done. No new code; just start the server against a temp workspace and confirm the SSE endpoint is alive.

- [ ] **Step 1: Build the binary**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox build -o /Users/chaholl/repos/altairalabs/codegen-sandbox/bin/sandbox ./cmd/sandbox
```
Expected: exits 0, produces `bin/sandbox`.

- [ ] **Step 2: Start the server in the background against a temp workspace**

Run:
```bash
mkdir -p /tmp/codegen-sandbox-smoke
/Users/chaholl/repos/altairalabs/codegen-sandbox/bin/sandbox -addr=127.0.0.1:18080 -workspace=/tmp/codegen-sandbox-smoke >/tmp/sandbox-smoke.log 2>&1 &
echo $! > /tmp/sandbox-smoke.pid
```
Use `run_in_background` if via the Bash tool.

- [ ] **Step 3: Hit the SSE endpoint**

Run:
```bash
curl -sS -N --max-time 2 http://127.0.0.1:18080/sse | head -n 5
```
Expected: SSE `event:` / `data:` frames, or at minimum a non-empty response. Exact path (`/sse` vs `/`) depends on the mcp-go SSE server — check `/tmp/sandbox-smoke.log` for the routing hint and adjust.

- [ ] **Step 4: Stop the server**

Run:
```bash
kill "$(cat /tmp/sandbox-smoke.pid)" 2>/dev/null || true
rm -f /tmp/sandbox-smoke.pid
```

- [ ] **Step 5: No commit required** (no file changes).

---

## Self-Review Notes

Covered from the proposal:
- Brain/hands split: only the hands (sandbox MCP server) is in scope; this plan delivers the server binary.
- Tool surface: `Read`, `Write`, `Edit` implemented. `Glob`, `Grep`, `Bash`, `run_*`, `WebFetch`, `WebSearch` deferred to later plans per CLAUDE.md plan series.
- Trust boundary / path containment: every filesystem-touching tool routes through `Workspace.Resolve` before any I/O — non-negotiable per CLAUDE.md, satisfied.
- Structured tool output: `Read` emits `LINENO\tCONTENT` lines; `Write`/`Edit` return short success strings. Lint-formatted output is Plan 4's concern.
- Transport: HTTP+SSE via `mark3labs/mcp-go` — matches tech stack in CLAUDE.md.
- TDD discipline: Tasks 2–7 each start with failing tests and drive minimal implementation. Task 1 is explicitly bootstrap.
- Commits: one per task, conventional-commits style.

Deliberately deferred:
- Post-edit lint feedback inside `Edit` → Plan 4 (`codegen-sandbox-verification.md`).
- Command denylist and secret scrubbing → Plans 3 and 5.
- Docker packaging → Plan 6.
- Multi-session concurrent read tracking — out of scope because the container is one session per lifetime; the single-process tracker is correct for v1.
