---
title: Read
description: Read a file from the workspace with line-numbered output.
---

Read a file from the workspace and return its content as `cat -n`-style numbered lines.

## Schema

| Param | Type | Required | Default | Notes |
|---|---|---|---|---|
| `file_path` | string | yes | — | Absolute or workspace-relative path. |
| `offset` | number | no | 1 | 1-based line to start reading at. |
| `limit` | number | no | 2000 | Maximum lines to return. |

## Behaviour

- Path goes through `Workspace.Resolve` first. Any path that resolves outside the workspace is rejected.
- Output format: `LINENO\tCONTENT\n` per line — standard `cat -n` shape.
- On success, the path is recorded in the session's [read-tracker](/concepts/read-tracker/). This is the gate that `Edit` and overwriting `Write` check.
- If the target doesn't exist or is a directory, returns an error result.
- If `offset` exceeds total line count, returns an error result.

## Example response

```
1	package main
2	
3	import "fmt"
4	
5	func main() { fmt.Println("hello") }
```

## Limits

- Single lines must be ≤ 1 MiB (bufio.Scanner token cap). Minified JS/CSS with no newlines beyond that limit returns `bufio.Scanner: token too long`.
- Binary files with embedded NULs are not specifically detected — the scanner splits on `\n` and garbage may be emitted. Read is intended for text.
- Offsets < 1 are treated as 1.

## Related

- [Edit](/tools/edit/) — requires a prior Read of the edited file.
- [Write](/tools/write/) — requires a prior Read when overwriting an existing file.
- [Glob](/tools/glob/) — find paths, then Read them.
