---
title: tests_covering
description: Return the tests whose coverage profile touched the given file (and optionally line).
---

Answers the question "which tests exercise `internal/foo/bar.go`?" so the agent can scope [`run_tests`](/tools/run-tests/) or [`run_failing_tests`](/tools/run-failing-tests/) to the minimum packages that matter — rather than rerunning the whole suite after every edit.

The session coverage index is populated by `run_tests` on every Go run and queried by `tests_covering`. No extra test run is needed.

## Schema

| Param | Type | Required | Default |
|---|---|---|---|
| `file` | string | yes | Workspace-relative path to the source file (e.g. `internal/tools/read.go`). |
| `line` | number | no | 1-based line. Omitted = any line in the file. |

## Behaviour

1. If the coverage index isn't configured on the server → error.
2. If no `run_tests` call has produced coverage yet in this session → text result `no coverage data yet — run run_tests first`.
3. If `file` is missing or blank → error.
4. If `file` has no entry in the index → text result `no coverage found for <file>`.
5. If `line` is set but no test covers that line in the file → text result `no coverage found for <file>:<line>`.
6. Otherwise → a text block listing the covering tests, grouped by Go import path, capped at 200 entries with a `... (N more truncated)` footer (same style as `last_test_failures`).

## Attribution model (v1)

Attribution is **package-level**, not per-test.

Go's standard `-coverprofile=<path>` is a per-run aggregate — it doesn't split coverage per individual test. For each profile record that touched a source file, `tests_covering` attributes every test that ran in that file's package during the same run. In practice this is the right unit of work for an agent: reruns happen per-package anyway via `go test -run '^TestX$' <pkg>`.

Splitting coverage per test would require running tests one-at-a-time with `-run ^TestX$ -coverprofile`, which is expensive and defeats the point of targeted reruns.

## Language support

- **Go** — supported. The Go detector's `go test -json -count=1` is augmented with `-coverprofile=<tempfile>` on every `run_tests` invocation; the profile is parsed, ingested into the session index, and the tempfile is removed.
- **Node / Python / Rust** — not yet wired. `run_tests` on these languages leaves the index empty, so `tests_covering` returns `no coverage data yet`.

## Examples

After a passing Go run:

```json
{"tool": "run_tests"}
```

Find the tests covering a file:

```json
{"tool": "tests_covering", "arguments": {"file": "internal/tools/read.go"}}
```

Sample output:

```
3 test(s) cover internal/tools/read.go:

github.com/altairalabs/codegen-sandbox/internal/tools:
  TestRead_ReturnsNumberedLines
  TestRead_OffsetAndLimit
  TestRead_MarksFileAsRead
```

Scope to a specific line:

```json
{"tool": "tests_covering", "arguments": {"file": "internal/tools/read.go", "line": 42}}
```

## Store semantics

- **Session-scoped.** Process-local; no persistence across restarts.
- **Overwritten on every `run_tests` call** (same contract as the structured-failure store). There is no merged history.

## Related

- [run_tests](/tools/run-tests/) — produces the coverage profile this tool indexes.
- [run_failing_tests](/tools/run-failing-tests/) — natural follow-up for reruns targeted at covering tests.
- [last_test_failures](/tools/last-test-failures/) — same session-scoped store pattern.
