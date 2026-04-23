---
title: run_tests
description: Run the detected project's test suite.
---

Run the test suite for the detected project type. Go today; future plans extend the [Detector interface](/reference/detector-interface/) to Node / Python / Rust.

## Schema

| Param | Type | Required | Default |
|---|---|---|---|
| `timeout` | number | no | 300 (clamped to 1800) |

## Behaviour

1. `verify.Detect(workspace root)` picks the detector. No detector → error result `no supported project detected in workspace root`.
2. Runs the detector's `TestCmd()`. For Go: `go test -json -count=1 ./...` — the `-json` flag emits test2json events so the companion [`last_test_failures`](/tools/last-test-failures/) tool can surface structured failure records without re-parsing raw runner output. `-count=1` defeats the Go build cache so reruns actually rerun.
3. Executes via a shared `runVerifyCmd` helper that:
   - `Setpgid` + process-group kill on timeout (same as [Bash](/tools/bash/)).
   - Captures stdout and stderr separately.
   - Caps each stream at 500 KiB with a `[TRUNCATED]` marker.
4. Formats output: stdout first, then `--- stderr ---` section (if non-empty), then optional timeout marker, then `exit: N` (always).
5. Parses the output through the detector's `ParseTestFailures` method and stores the result in a session-scoped slot. Retrieve it via [`last_test_failures`](/tools/last-test-failures/).

## Example output

With `-json`, each line is a test2json event (Go projects):

```
{"Action":"run","Package":"example.com/my-module","Test":"TestFoo"}
{"Action":"output","Package":"example.com/my-module","Test":"TestFoo","Output":"--- PASS: TestFoo\n"}
{"Action":"pass","Package":"example.com/my-module","Test":"TestFoo","Elapsed":0}
{"Action":"pass","Package":"example.com/my-module","Elapsed":0.123}
exit: 0
```

Agents who want the human-readable summary should call [`last_test_failures`](/tools/last-test-failures/) after `run_tests`; the raw JSON stream is still exposed here for debugging and for cases where an agent wants to replay the event stream itself.

## Failure modes

- **No project detected** → error result.
- **Binary missing** (e.g., `go` not installed) → error result.
- **Non-zero exit** → text result (NOT an error — test failures are legitimate results).
- **Timeout** → text result with `timed out after Ns` marker, exit 124.

## Related

- [last_test_failures](/tools/last-test-failures/) — structured view of the most recent run's failures.
- [run_lint](/tools/run-lint/)
- [run_typecheck](/tools/run-typecheck/)
- [Detector interface](/reference/detector-interface/)
