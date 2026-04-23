---
title: Write
description: Write a file atomically. Overwriting requires a prior Read.
---

Write a file to disk. Content is written atomically (tmp file + rename). Parent directories are created if needed.

## Schema

| Param | Type | Required | Notes |
|---|---|---|---|
| `file_path` | string | yes | Absolute or workspace-relative path. |
| `content` | string | yes | File content. Empty string creates a zero-byte file. |

## Behaviour

- Path goes through `Workspace.Resolve` first.
- If the target file **already exists**, the canonical absolute path MUST have been `Read` first in the current session; otherwise the call is rejected with `refusing to overwrite %s: Read it first`.
- Creating a new file: no prior Read required.
- Writing to a path that resolves to a directory returns an error.
- On success, the path is recorded in the [read-tracker](/concepts/read-tracker/) so subsequent `Edit` calls don't need an intervening Read.

## Atomicity

Content is written to `<path>.tmp.<pid>.<nanos>` with `O_EXCL`, then `fsync`'d, then `Rename`'d onto the target. A partial write is never visible. If the rename fails, the temp file is removed.

## Failure modes

| Condition | Result |
|---|---|
| Missing `file_path` or `content` | Error result |
| Path outside workspace | Error result |
| Target is a directory | Error result |
| Target file exists but not Read'd | Error result |
| Parent directory creation fails | Error result |

## Success output

```
wrote 42 bytes to notes.md
```

## Related

- [Read](/tools/read/) — prerequisite for overwrite.
- [Edit](/tools/edit/) — smaller-scoped in-place edits.
