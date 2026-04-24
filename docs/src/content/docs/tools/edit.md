---
title: Edit
description: Exact-string replacement with required prior Read, post-edit lint feedback, and post-edit format check.
---

Replace an exact substring in a file. On Go projects, lint findings for the edited file are appended to the success message. On Python / Node / Rust projects, a single-file format check (`ruff format --check --diff`, `prettier --check`, `rustfmt --check`) is appended under `--- format ---`.

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
7. If the detected language declares a per-file format check (`Detector.FormatCheckCmd`) and its binary is on PATH, run it with a 10s timeout and append any output under a `--- format ---` header. Go's `FormatCheckCmd` is nil (its lint path already covers gofmt), so Go edits never render a format section.

## Post-edit lint feedback

The "single biggest quality win" per the project proposal. On Go projects, after every successful `Edit`:

1. `verify.Lint` runs `golangci-lint run ./...` from the workspace root.
2. Findings are parsed into structured `file:line:col:rule: message` records.
3. Findings whose file matches the edited path are appended to the success body.

Best-effort: if the linter times out, isn't installed, or the project isn't Go, the base success message is unchanged.

See [Post-edit lint feedback](/concepts/post-edit-lint-feedback/).

## Post-edit format check

On non-Go projects, after the lint section (if any), Edit runs the detector's `FormatCheckCmd` against the edited file with a 10s timeout. Per language:

| Language | Command | Notes |
|---|---|---|
| Go | (nil) | Skipped — the lint path already covers gofmt / gofumpt coverage. |
| Python | `ruff format --check --diff <file>` | `--diff` prints the fix ruff would apply; `--check` keeps it non-destructive. |
| Node | `prettier --check <file>` | Assumes prettier is on PATH directly; projects installing it locally shadow via a wrapper script. |
| Rust | `rustfmt --check <file>` | Edition is inherited from the enclosing `Cargo.toml`. |

Output rules:

- Formatter exit 0 (file is formatted) → no section rendered.
- Formatter exit non-zero with output → appended under `--- format ---`, truncated to 500 lines.
- Detector declares a formatter but the binary isn't on PATH → a single line `post-edit format: <binary> not found on PATH` is appended (Edit never fails on format feedback).

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

Edit on a Python file that's not formatted (rendered under `--- format ---`):

```
replaced 1 occurrence(s) in app.py

--- format ---
--- app.py
+++ app.py
@@ -1 +1 @@
-x=1
+x = 1
```

## Related

- [Read](/tools/read/) — prerequisite.
- [Write](/tools/write/) — for whole-file replacement.
- [run_lint](/tools/run-lint/) — explicit whole-project lint run.
