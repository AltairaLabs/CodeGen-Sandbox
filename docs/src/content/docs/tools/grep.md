---
title: Grep
description: Search file content with a regex. ripgrep-backed, gitignore-aware, structured output.
---

Search file content via ripgrep. Returns matches in the chosen output mode.

## Schema

| Param | Type | Required | Default | Notes |
|---|---|---|---|---|
| `pattern` | string | yes | — | Rust regex syntax (ripgrep's engine). |
| `path` | string | no | workspace root | File or directory to search. |
| `glob` | string | no | — | File filter, e.g. `"*.go"`. |
| `case_insensitive` | boolean | no | false | Adds `-i`. |
| `output_mode` | string | no | `"content"` | One of `"content"`, `"files_with_matches"`, `"count"`. |
| `head_limit` | number | no | unlimited | Truncate output to this many lines. 0 means no limit. |

## Output modes

- `content` — one match per line: `path:line:text`
- `files_with_matches` — one path per line (files that have at least one match)
- `count` — one line per file: `path:count`

## Behaviour

- `.gitignore` is honored (via `rg --no-require-git` so it works outside a git repo too).
- Always runs with cwd = workspace root, so paths in output are workspace-relative.
- Invalid regex returns an error result wrapping ripgrep's stderr.
- No matches returns an empty text result (not an error).
- `head_limit` is applied client-side after ripgrep returns; for `count` and `files_with_matches` it bounds the number of files reported.

## Example

Pattern: `TODO`, output mode: `content`, glob: `*.go`

```
cmd/sandbox/main.go:42:// TODO: handle signal cleanup
internal/tools/bash.go:120:// TODO: consider per-shell max-runtime
```

## Related

- [Glob](/tools/glob/) — search file *paths*.
- [Read](/tools/read/) — after finding a match, read the surrounding lines.
