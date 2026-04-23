---
title: Edit
description: Exact-string replacement with required prior Read and post-edit lint feedback.
---

Replace an exact substring in a file. On Go projects, lint findings for the edited file are appended to the success message.

## Schema

| Param | Type | Required | Default | Notes |
|---|---|---|---|---|
| `file_path` | string | yes | — | Absolute or workspace-relative path. |
| `old_string` | string | yes | — | Must be non-empty. |
| `new_string` | string | yes | — | May be empty (deletes the match). |
| `replace_all` | boolean | no | false | If false and `old_string` appears more than once, call is rejected. |

## Behaviour

1. Resolve the path (containment check).
2. Verify the file exists and isn't a directory.
3. Verify the file has been `Read` in the current session — otherwise `refusing to edit %s: Read it first`.
4. Verify `old_string` is non-empty (prevents an `os.WriteFile`-shaped corruption path).
5. Count occurrences of `old_string`:
   - 0 → error `old_string not found in %s`
   - 1 → replace, atomic write.
   - \>1 with `replace_all=false` → error `old_string matched N times in %s; add context to make it unique or set replace_all=true`
   - \>1 with `replace_all=true` → replace all, atomic write.
6. On success, run the project's linter with a 30s timeout. If any findings apply to the edited file, append them to the response.

## Post-edit lint feedback

The "single biggest quality win" per the project proposal. On Go projects, after every successful `Edit`:

1. `verify.Lint` runs `golangci-lint run ./...` from the workspace root.
2. Findings are parsed into structured `file:line:col:rule: message` records.
3. Findings whose file matches the edited path are appended to the success body.

Best-effort: if the linter times out, isn't installed, or the project isn't Go, the base success message is unchanged.

See [Post-edit lint feedback](/concepts/post-edit-lint-feedback/).

## Example output

Clean edit:

```
replaced 1 occurrence(s) in probe.go
```

Edit that introduces a lint violation:

```
replaced 1 occurrence(s) in probe.go

post-edit lint findings (1):
probe.go:5:3:errcheck: Error return value of `os.WriteFile` is not checked
```

## Related

- [Read](/tools/read/) — prerequisite.
- [Write](/tools/write/) — for whole-file replacement.
- [run_lint](/tools/run-lint/) — explicit whole-project lint run.
