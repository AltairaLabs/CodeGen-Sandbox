---
title: Glob
description: Find files by pattern, sorted by mtime, respecting .gitignore.
---

Find files in the workspace matching a glob pattern. Results are sorted newest-first by modification time.

## Schema

| Param | Type | Required | Default | Notes |
|---|---|---|---|---|
| `pattern` | string | yes | — | Glob pattern. |
| `path` | string | no | workspace root | Subdirectory to scope the search to. |
| `limit` | number | no | 100 | Max results to return. |

## Pattern syntax

Supports `*`, `?`, `[...]`, and `**`. **Brace expansion and negation are NOT supported** — make multiple calls for multi-extension matches.

- `**/*.go` — all Go files, any depth
- `src/**/*.ts` — TS files under `src/`
- `*.md` — top-level markdown only (basename match when no `/` in pattern)

## Behaviour

- Always runs with cwd=workspace root, so emitted paths are workspace-relative regardless of the `path` argument.
- If `path` is set, the search is scoped to that subdirectory (path is passed as a positional arg to ripgrep).
- Uses `rg --files --no-require-git --color=never` under the hood. The Go-side matcher filters the results (ripgrep's `-g` flag is NOT used because it acts as a whitelist that overrides `.gitignore`).
- `.gitignore` is honored whether or not the workspace is a git repo.
- Results are sorted by mtime descending; ties broken lexicographically.

## Failure modes

| Condition | Result |
|---|---|
| Missing `pattern` | Error result |
| `path` outside workspace | Error result |
| `path` not a directory | Error result |
| No matches | Empty text result (not an error) |

## Related

- [Grep](/tools/grep/) — search file *content*, not just names.
- [Read](/tools/read/) — pair a Glob to find paths with Reads to inspect them.
