---
title: run_failing_tests
description: Rerun only the tests that failed in the most recent run_tests call.
---

Rerun **only** the tests that failed in the most recent [`run_tests`](/tools/run-tests/) call in this session. Skips anything that passed, so the fix-rerun loop drops from minutes to seconds on large codebases.

The sandbox composes a narrow test-runner invocation from the structured failure records it already captured for [`last_test_failures`](/tools/last-test-failures/). After the rerun completes, the structured-failure store is overwritten with the fresh results — a follow-up `last_test_failures` reflects the new state.

## Schema

| Param | Type | Required | Default |
|---|---|---|---|
| `limit` | number | no | 50 (clamped to 200) — max distinct test names in the rerun filter |
| `timeout` | number | no | 300s (max 1800s) — same semantics as `run_tests` |

## Behaviour

1. If no `run_tests` call has been made yet in this session → text result `no run_tests call yet in this session`.
2. If no supported project is detected in the workspace root → error result.
3. If the detector has no structured-failures parser (v1: every language except Go) → text result `run_failing_tests: <language> detector has no structured failures; rerun run_tests manually`.
4. If the last run had zero failures → text result `last run_tests had no failures — nothing to rerun (<language>)`.
5. Otherwise the sandbox composes a Go `-run` regex from the stored failures and executes it. Output shape matches `run_tests` (stdout, optional stderr section, optional timeout line, trailing `exit: N`).

## Language support

- **Go** — composes `go test -json -count=1 -run '^(TestA|TestB|...)$' <packages>`. Packages are the set of import paths from the failure records; when the set is ≤ 10 distinct packages they're passed positionally, otherwise the invocation falls back to `./...` to keep argv bounded. Subtest failures rerun the parent test (Go's `-run` regex matches the top-level name).
- **Node / Python / Rust** — parser not yet wired. The tool surfaces a `not supported for <language>` notice; rerun `run_tests` directly.

## Examples

After a failing run:

```json
{"tool": "run_tests"}
→ 3 failures: pkg/a:TestAlpha, pkg/a:TestBeta, pkg/b:TestGamma
```

Rerun only those:

```json
{"tool": "run_failing_tests"}
```

The composed invocation is `go test -json -count=1 -run '^(TestAlpha|TestBeta|TestGamma)$' pkg/a pkg/b`.

Limit the filter (useful when dozens of tests failed and you want to focus on the first batch):

```json
{"tool": "run_failing_tests", "arguments": {"limit": 5}}
```

## Store semantics

- **Reads** the same `TestResultStore` slot `last_test_failures` reads from.
- **Writes** the slot on completion, so a follow-up `last_test_failures` surfaces the rerun's results — not the original run's.
- **Session-scoped.** Process-local; no persistence across restarts.

## Related

- [run_tests](/tools/run-tests/) — produces the failure records this tool reruns.
- [last_test_failures](/tools/last-test-failures/) — shares the same storage slot.
- [Language support model](/concepts/language-support/) — the `Detector.ParseTestFailures` contract that gates rerun support.
