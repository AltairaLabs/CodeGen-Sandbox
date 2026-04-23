---
title: BashOutput
description: Poll the stdout, stderr, and status of a background Bash shell.
---

Fetch the current state of a background shell started via [`Bash`](/tools/bash/) with `run_in_background: true`.

## Schema

| Param | Type | Required |
|---|---|---|
| `shell_id` | string | yes |

## Behaviour

- Each call returns the **FULL** captured buffer up to the per-stream cap (1 MiB stdout, 1 MiB stderr). There's no per-reader offset — agents should grep client-side.
- Running shells return `status: running`.
- Completed shells return `status: completed (exit N)`.
- Unknown `shell_id` returns an error result.

## Output format

```
command: npm run build
status: completed (exit 0)
started: 2026-04-23T14:30:22+01:00

--- stdout (1234 bytes) ---
webpack compiled successfully
...

--- stderr (0 bytes) ---
```

When either stream hits its cap, the header includes `[TRUNCATED]`:

```
--- stdout (1048576 bytes) [TRUNCATED] ---
```

## Lifecycle

- Shells stay in the registry after they complete. You can `BashOutput` a finished shell indefinitely (until the container exits).
- To actively release registry space, call [`KillShell`](/tools/kill-shell/) — even on an already-exited shell, this removes it from the registry.

## Related

- [Bash](/tools/bash/) — launching background shells.
- [KillShell](/tools/kill-shell/) — terminating them.
