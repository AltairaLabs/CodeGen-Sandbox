---
title: last_test_failures
description: Return the structured failure list from the most recent run_tests call in this session.
---

Return a structured view of the failures from the most recent [`run_tests`](/tools/run-tests/) call in this session. Saves the agent from re-parsing raw runner output (go test `FAIL: TestFoo`, pytest tracebacks, jest diffs, cargo panic formatting) after every run.

The sandbox parses the runner's machine-readable output once, at `run_tests` time, and stores the result in a process-scoped singleton. `last_test_failures` reads from that slot.

## Schema

| Param | Type | Required | Default |
|---|---|---|---|
| `limit` | number | no | 50 (clamped to 500) |

## Behaviour

1. If no `run_tests` call has been made yet in this session → text result `no run_tests call yet in this session`.
2. If the last `run_tests` call's detector has no structured-failures parser (v1: every language except Go) → `no structured failures available for <language> (last run_tests Ns ago)`.
3. If the last run had zero failures → `last run_tests had no failures (<N> tests passed, <lang>, <duration> ago)`.
4. Otherwise → a block-per-failure listing:

```
2 test failure(s) from last run_tests call (go, 3s ago):

1. example.com/pkg/foo/TestValidate/empty_input
   internal/foo/foo.go:42
   expected error for empty input, got nil

2. example.com/pkg/bar/TestBaz
   internal/bar/bar_test.go:87
   mismatch
   --- diff ---
   got: 5
   want: 3
```

Each block carries the fully qualified `TestName`, the `file:line` where available, the message, and an optional diff when the runner emitted `got:` / `want:` / `Diff:` markers.

## Language support

- **Go** — parses `go test -json` test2json events. `run_tests` automatically uses `-json -count=1 ./...` for Go projects.
- **Node / Python / Rust** — parser not yet wired. The tool surfaces a clear "not supported for <language>" notice; fall back to reading raw `run_tests` output.

The contract is the [language-support model](/concepts/language-support/)'s `Detector.ParseTestFailures` method — each language's implementation lands in a subsequent PR.

## Storage semantics

- **Single slot, session-scoped.** Only the most recent run is retained. Agents generally care about their latest run.
- **Process-local.** Nothing is persisted to disk. A server restart resets the slot.
- **Written on every `run_tests` call**, whether tests passed or failed — so "no failures" is distinguishable from "no call yet".

## Failure modes

- **No previous `run_tests` call** → text result with the `no run_tests call yet` message (NOT an error).
- **Unsupported language** → text result with the `not supported for <language>` message.
- **Store not configured** (only reachable via direct handler construction in tests) → error result.

## Related

- [run_tests](/tools/run-tests/) — produces the input.
- [Language support model](/concepts/language-support/) — the `Detector.ParseTestFailures` contract.
