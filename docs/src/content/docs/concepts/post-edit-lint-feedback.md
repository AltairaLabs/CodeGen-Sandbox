---
title: Post-edit lint feedback
description: The "single biggest quality win" — Edit surfaces lint findings for the file it just modified.
---

Per the project proposal, this is the single biggest quality win of the whole sandbox. After every successful [`Edit`](/tools/edit/) on a Go project, the tool runs the project's linter with a short timeout and appends any findings for the edited file to the success message.

The agent sees mistakes immediately, before it calls `run_lint` or `run_tests`. Feedback is in the same response as the edit, not a separate round-trip.

## How it works

Inside `HandleEdit`, after the atomic write succeeds:

```go
msg := fmt.Sprintf("replaced %d occurrence(s) in %s", count, filePath)
if feedback := postEditLintFeedback(ctx, deps.Workspace.Root(), abs); feedback != "" {
    msg += "\n\n" + feedback
}
return TextResult(msg), nil
```

`postEditLintFeedback`:

1. Calls `verify.Lint(ctx, root, 30)` — 30-second timeout, deliberately short.
2. On any error (linter missing, timeout, exit ≥ 2), returns empty string. Edit proceeds normally.
3. Otherwise, filters findings to the edited file's workspace-relative path. Normalises absolute linter paths via `filepath.Rel`.
4. Formats matching findings as `file:line:col:rule: message`, prefixed with `post-edit lint findings (N):`.

## Contract

- **Best effort.** Lint errors never cause Edit to fail. An agent gets the same success message with or without feedback depending on whether the linter happened to be running.
- **File-scoped.** Findings from other files are ignored. The agent can call `run_lint` explicitly for a full-project scan.
- **30s timeout.** On a large module with a cold cache, this may not be enough. Tradeoff: per-Edit latency vs completeness. Warm-cache runs are typically sub-second.

## Divergence from `run_lint`

Both tools call `verify.Lint`, but they handle the `(findings, err)` return differently:

| Situation | Edit post-lint | run_lint |
|---|---|---|
| No detector | Silent (no block) | Error result |
| Linter missing | Silent (no block) | Error result (naming the binary) |
| Timeout with partial findings | Silent (no block) | Text result with findings + `(lint incomplete: ...)` |
| Exit ≥ 2 with findings | Silent (no block) | Text result with findings + `(lint incomplete: ...)` |

Rationale: Edit's primary signal is "the replacement succeeded"; adding a possibly-misleading partial-findings block would degrade that. `run_lint`'s primary signal IS lint state, so partial data is better than no data.

## Example

Start with a clean Go file:

```go
package probe

import "os"

func Write() error {
    return os.WriteFile("x", []byte("y"), 0o644)
}
```

Edit to drop the error return:

```
old_string: func Write() error { return os.WriteFile(...
new_string: func Write() { os.WriteFile(...
```

Response:

```
replaced 1 occurrence(s) in probe.go

post-edit lint findings (1):
probe.go:5:3:errcheck: Error return value of `os.WriteFile` is not checked
```

The agent can now fix the violation in the next Edit without needing to invoke `run_lint`.

## Sibling: post-edit format check

For languages whose lint path doesn't already cover formatting drift (Python, Node, Rust), Edit runs a separate per-file format check after the lint section. It uses the detector's `FormatCheckCmd(file)` and renders any diff under a `--- format ---` header. Go's `FormatCheckCmd` returns nil — its golangci-lint default set already covers gofmt/gofumpt, so running rustfmt's equivalent a second time would just print the same thing.

Same contract as lint: best-effort, never fails Edit, surfaces `post-edit format: <binary> not found on PATH` when the formatter advertised by the detector isn't installed. See [Edit](/tools/edit/#post-edit-format-check) for the per-language argv.
