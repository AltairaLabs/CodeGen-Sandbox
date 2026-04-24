---
title: LSP navigation
description: Three tools backed by a language server — find_definition, find_references, rename_symbol.
---

Grep returns false positives — identically-named methods in unrelated packages, template strings, comments. When the agent wants to know where `User.ValidateToken` is *really* defined or called, text search isn't enough.

These three tools bind to a per-language language server via LSP and answer those questions with full type information.

## Tools

| Tool | Purpose |
|---|---|
| `find_definition` | Return the defining location(s) of the symbol at a cursor position. |
| `find_references` | Return every call site / reference (including the declaration). |
| `rename_symbol` | Compute a **structured rename diff** for review — does **not** apply the edit. |

All three accept the same positional shape: `file_path` + 1-based `line` + 1-based `col`. `rename_symbol` additionally takes `new_name`.

## Common contract

- `file_path` is resolved through [path containment](/concepts/path-containment/); paths outside the workspace are rejected.
- `line` and `col` are **1-based** (consistent with the rest of the sandbox). They're converted to LSP's 0-based form internally.
- The language server launches lazily on first call per (workspace, language) pair and shuts down after 10 minutes idle.
- `find_definition` and `find_references` do **not** require a prior Read — they're read-only.
- `rename_symbol` **does** require a prior Read of the file being edited (same Read-gate as [Edit](/tools/edit/)).

## `find_definition`

Return the defining location(s) of the symbol at `file_path:line:col`.

### Schema

| Param | Type | Required | Notes |
|---|---|---|---|
| `file_path` | string | yes | Workspace-contained path. |
| `line` | number | yes | 1-based cursor line. |
| `col` | number | yes | 1-based cursor column. |

### Example output

```
Found 1 definition(s) for symbol at internal/auth/user.go:42:6:
  1. internal/auth/user.go:87:1  func (u *User) ValidateToken(token string) error
```

## `find_references`

Return every reference (including the declaration) to the symbol at `file_path:line:col`.

### Schema

Identical to `find_definition`.

### Example output

```
Found 3 reference(s) for symbol at internal/auth/user.go:87:16:
  1. internal/auth/user.go:42:3   // calls u.ValidateToken before ...
  2. internal/auth/handlers.go:108:9  if err := user.ValidateToken(tok); err != nil {
  3. internal/auth/mock.go:23:1  func (m *MockUser) ValidateToken(token string) error
```

## `rename_symbol`

Compute the workspace edit that would rename the symbol at `file_path:line:col` to `new_name`. The sandbox **does not apply** the edit — it returns a diff the agent can inspect before committing via the normal [Edit](/tools/edit/) tool.

### Schema

| Param | Type | Required | Notes |
|---|---|---|---|
| `file_path` | string | yes | Workspace-contained path. Must have been Read. |
| `line` | number | yes | 1-based cursor line. |
| `col` | number | yes | 1-based cursor column. |
| `new_name` | string | yes | The new identifier. Legality is validated by the language server. |

### Why not auto-apply?

Two reasons:

1. **Reviewability.** A multi-file rename is a diff the agent should inspect, not a blind side-effect. Letting the agent see the proposed change and decide keeps control in the model's hands.
2. **Scrubbing & tracker parity.** Every write in the sandbox flows through the Read-gate + scrub middleware + post-edit lint feedback. Routing rename-writes through Edit keeps those guarantees uniform.

### Example output

```
Rename → "VerifyToken" would touch 2 file(s):
  - internal/auth/user.go (2 edit(s))
  - internal/auth/handlers.go (1 edit(s))

Review the diff below; apply via Edit once approved:

--- a/internal/auth/user.go
+++ b/internal/auth/user.go
@@ -42,1 +42,1 @@
-	return u.ValidateToken(token)
+	return u.VerifyToken(token)
@@ -87,1 +87,1 @@
-func (u *User) ValidateToken(token string) error {
+func (u *User) VerifyToken(token string) error {

--- a/internal/auth/handlers.go
+++ b/internal/auth/handlers.go
@@ -108,1 +108,1 @@
-	if err := user.ValidateToken(tok); err != nil {
+	if err := user.VerifyToken(tok); err != nil {
```

## Language support

v1 ships **Go only** via `gopls`. The `Detector` interface exposes `LSPCommand()`; other detectors return `nil` today and will gain bindings alongside their feature-tools image layer:

| Language | Server | Status |
|---|---|---|
| Go | `gopls serve` | v1 (this issue) |
| Python | `pyright-langserver` / `pylsp` | Follow-up |
| Node | `typescript-language-server` | Follow-up |
| Rust | `rust-analyzer` | Follow-up |

### When `gopls` isn't on PATH

The sandbox base image does **not** bundle `gopls`. Operators compose it in via `codegen-sandbox-tools-go` (see [Language support model](/concepts/language-support/)). If `gopls` isn't present when a tool is called, the response is:

```
gopls not found on PATH. See docs/concepts/language-support for the image
composition model (gopls ships in codegen-sandbox-tools-go).
```

No silent no-op; no panic; the agent sees a clear, actionable message.

### When the workspace has no detectable language

```
no language detected in workspace — LSP navigation requires a recognised
project (go.mod, etc.)
```

### When the detector has no LSP binding yet

```
LSP not configured for rust
```

## Lifecycle

- One language-server subprocess per (workspace, language) pair.
- Lazy-started on first call; serves all subsequent calls for that pair.
- Idle-shutdown after 10 minutes without a successful call (configurable in a follow-up).
- On server crash, the next call re-spawns.

## Limitations

- **Language-server only.** No static analysis fallback — if the server is unreachable, the tool errors cleanly rather than returning partial results.
- **No cross-language refactors.** Renaming a Go symbol referenced by a Python script isn't in scope for these tools.
- **No cancellation mid-call.** The tool blocks on the LSP response until context deadline. Set a tool-level timeout if your agent harness expects faster feedback.
- **No document-sync.** The sandbox doesn't send `textDocument/didOpen` — `gopls` reads files from disk via the workspace root. Edits made since the last save are invisible until the file is flushed.

## Related

- [Edit](/tools/edit/) — where `rename_symbol` output lands once the agent approves.
- [AST-safe edit primitives](/tools/ast-edits/) — complementary: AST edits for intraprocedural changes, LSP navigation for "where is this".
- [Language support model](/concepts/language-support/) — how new languages + language servers land.
