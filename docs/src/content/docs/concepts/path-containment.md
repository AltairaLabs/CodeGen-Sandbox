---
title: Path containment
description: How Workspace.Resolve guarantees every filesystem tool stays inside the workspace.
---

Path containment is the single choke point every filesystem tool passes through. If it's sound, the sandbox can't be tricked into reading or writing outside the workspace. If it's wrong, every other layer becomes a fig leaf.

## The `Workspace` type

```go
type Workspace struct {
    root string // canonical absolute, symlinks resolved
}

func New(root string) (*Workspace, error)
func (w *Workspace) Root() string
func (w *Workspace) Resolve(p string) (string, error)
```

`New` canonicalises the given root (must be absolute, must exist, must be a directory) via `filepath.EvalSymlinks`. All subsequent operations compare against this canonical path.

## The `Resolve` algorithm

1. If the input is relative, join it with the canonical root.
2. `filepath.Clean` the result.
3. Resolve symlinks on every **existing** path component using `evalSymlinksAllowMissing`:
   - Walk up until an existing ancestor is found.
   - `EvalSymlinks` that ancestor.
   - Re-attach the non-existing suffix verbatim.

   This supports "I want to write a new file" without a sentinel value — the caller passes the target path, and Resolve returns its canonical form even if only the parent directory exists.
4. Compute `filepath.Rel(root, resolved)`.
5. If `rel == ".."` OR `strings.HasPrefix(rel, "../")`, reject with `ErrOutsideWorkspace`.

## Attack vectors it catches

| Input | Outcome |
|---|---|
| `../etc/passwd` (relative traversal) | Cleaned to `<root>/../etc/passwd`; Rel returns `../../etc/passwd`; rejected. |
| `/etc/passwd` (absolute outside) | Rel returns `../../etc/passwd`; rejected. |
| `<root>/link-to-outside` (symlink escape) | `EvalSymlinks` resolves the link to its real target; Rel returns `../<somewhere>`; rejected. |
| `<root>/new/nested/file.txt` (doesn't exist yet) | Deepest-existing-ancestor walk resolves `<root>`, re-attaches `new/nested/file.txt`; Rel returns `new/nested/file.txt`; accepted. |

## Known TOCTOU window

Between `Resolve` returning a safe path and the caller performing I/O, another process (inside the same container) could swap a path component for a symlink to outside. The threat model treats this as acceptable: the only actor inside the sandbox container is the agent itself — if it's malicious enough to race symlinks, container isolation is the real defence.

## `ReadTracker`

A companion structure records paths that have been `Read` in the current session. `Edit` always consults it; `Write` consults it when overwriting. The key is the canonical resolved path, so symlink-vs-real-path can't be used to bypass the check.

```go
type ReadTracker struct { ... }

func (t *ReadTracker) MarkRead(absPath string)
func (t *ReadTracker) HasBeenRead(absPath string) bool
```

Concurrent-safe. Per-sandbox lifetime (one registry per server instance, which matches the proposal's "one sandbox per session" model).

## What `Resolve` does NOT do

- **Rate limiting.** If an agent makes millions of Read calls, Resolve won't slow it down.
- **Permission checks.** If the workspace has a sub-directory the agent shouldn't touch (e.g., a secrets folder), Resolve can't enforce that — only OS-level permissions (non-root user, proper chown) can.
- **Content filtering.** Resolve operates on paths, not bytes. Secret scrubbing covers the bytes on the way out.
