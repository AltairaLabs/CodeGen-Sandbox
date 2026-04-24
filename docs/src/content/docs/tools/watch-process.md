---
title: watch_process / watch_process_events
description: Spawn a long-running command with regex-based crash surfacing on stderr and structured, pollable events.
---

`watch_process` is the MCP tool for the "tail a dev server and tell me when it crashes" workflow. It complements the existing background-mode [`Bash`](/tools/bash/) + [`BashOutput`](/tools/bash-output/) pair — those give you raw bytes and an exit code; `watch_process` layers regex-based crash surfacing and structured events on top, so the agent can tell a crash from a normal log line without parsing ANSI-coloured stderr client-side.

- **`watch_process(command, error_patterns?, idle_timeout_seconds?, description)`** — spawns the command in background and returns a `shell_id`.
- **`watch_process_events(shell_id, since_event_id?)`** — returns structured events since the last poll.
- **[`KillShell`](/tools/kill-shell/)** — same machinery as background Bash; terminates a watched process by `shell_id`.

The `shell_id` returned by `watch_process` is also valid for [`BashOutput`](/tools/bash-output/) — the raw stdout and stderr are still captured into the same per-shell buffers. Use `watch_process_events` for the filtered view, `BashOutput` for the full byte stream.

## Schema

### `watch_process`

| Param | Type | Required | Notes |
|---|---|---|---|
| `command` | string | yes | Shell command passed to `/bin/bash -c`. Same denylist as `Bash`. |
| `description` | string | yes | 5-10 word description for agent context. Recorded, not executed. |
| `error_patterns` | string[] | no | Go-regex strings matched against each stderr line. Default: `["^Error:", "^Fatal:", "^panic:", "\buncaught\b", "UnhandledPromiseRejection"]`. Cap: 32 patterns. Submit an empty list to disable pattern matching entirely. |
| `idle_timeout_seconds` | number | no | If > 0, the process is killed when no output has been observed on stdout or stderr for this many seconds. Default 0 (disabled); clamped to 3600. |

### `watch_process_events`

| Param | Type | Required | Notes |
|---|---|---|---|
| `shell_id` | string | yes | ID returned by `watch_process`. |
| `since_event_id` | number | no | Only return events with ID > this value. Default 0 (all events so far). |

## Event model

Each event carries:

- `ID` — 1-based monotonic sequence. Pass the max ID you've seen back as `since_event_id` on the next poll.
- `Type` — `started` / `error` / `idle_timeout` / `exited`.
- `At` — RFC 3339 timestamp.
- `Pattern` — for `error`: the regex source string that matched.
- `Line` — for `error`: the full stderr line; for `idle_timeout`: the kill reason.
- `ExitCode` — for `exited`.

Rendered in the tool body as one event per line:

```
[1] 2026-04-24T15:59:59+01:00  started
[2] 2026-04-24T15:59:59+01:00  error  (matched "^panic:")  panic: runtime error
[3] 2026-04-24T15:59:59+01:00  exited  exit=1
```

## Event cap

Each shell's event log is capped at 1024 events. When the cap is reached the oldest ~10% are dropped in one go; subsequent calls to `watch_process_events` include a header note so the agent can tell some events have been missed:

```
NOTE: 102 earlier events dropped (per-shell cap of 1024 reached)
```

A pattern-heavy process that hits the cap is almost always a crash-loop; the cap is meant to bound memory rather than be a normal working limit.

## Behaviour details

- Stderr is read line-by-line via `bufio.Scanner` (64 KiB initial buffer, 1 MiB max). Lines longer than the max are dropped — consistent with how background `Bash` handles pipe overflow.
- Stdout is still captured byte-for-byte into the per-shell 1 MiB buffer but **not** pattern-matched. Dev-server crashes typically surface on stderr; stdout is normal log.
- The idle watchdog polls once per second and kills the whole process group (same semantics as `KillShell`) with `SIGKILL` when the configured timeout has elapsed with no output. A following `exited` event is stamped by the lifecycle goroutine once the child reaps.
- First match wins for any given stderr line — if two patterns overlap, only the earlier one in `error_patterns` fires.

## Failure modes

| Condition | Result |
|---|---|
| Missing `command` or `description` | Error result |
| `command` triggers the Bash denylist | Error result (same as Bash) |
| `error_patterns` contains an invalid regex | Error result naming the offending index + source |
| More than 32 patterns | Error result |
| `watch_process_events` called with an unknown `shell_id` | Error result |
| `watch_process_events` called on a shell started by `Bash` (not `watch_process`) | Error result telling the agent to use `BashOutput` instead |

## Typical flow

```
1. watch_process({command: "pnpm dev", description: "next dev server"})
   → shell_id: abc-123

2. watch_process_events({shell_id: "abc-123"})
   → [1] started
     [2] error  (matched "^Error:")  Error: Module not found ...

3. (agent fixes the import, hits Save — next.js reloads)

4. watch_process_events({shell_id: "abc-123", since_event_id: 2})
   → events (0)   # no new events; server is healthy

5. KillShell({shell_id: "abc-123"})
   → killed: abc-123
```

## Not included

- **`event_log` watchdog for stdout** — intentionally off; dev-server crashes don't go to stdout in any framework we target.
- **Per-event deduplication** — repeated matching lines emit repeated events by design (a crash loop emitting 20 "panic:" lines in 1 second is signal, not noise).
- **Backpressure on very fast crash loops** — the 1024-event cap + drop-oldest policy is the bound; there is no sliding-window rate-limit.

## Related

- [Bash](/tools/bash/) — foreground shell / background shell without filtering.
- [BashOutput](/tools/bash-output/) — raw bytes for a background / watched shell.
- [KillShell](/tools/kill-shell/) — terminate a background or watched shell.
- [Agent-health metrics](/operations/agent-health/) — tool-error-rate / time-since-last-green gauges complement watch_process for "is this agent session productive?" signals.
