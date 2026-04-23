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
2. Runs the detector's `TestCmd()`. For Go: `go test ./...`.
3. Executes via a shared `runVerifyCmd` helper that:
   - `Setpgid` + process-group kill on timeout (same as [Bash](/tools/bash/)).
   - Captures stdout and stderr separately.
   - Caps each stream at 500 KiB with a `[TRUNCATED]` marker.
4. Formats output: stdout first, then `--- stderr ---` section (if non-empty), then optional timeout marker, then `exit: N` (always).

## Example output

```
ok  	example.com/my-module	0.123s
exit: 0
```

On failure:

```
--- FAIL: TestFoo
    foo_test.go:42: want 3 got 2
FAIL
--- stderr ---
--- FAIL
exit: 1
```

## Failure modes

- **No project detected** → error result.
- **Binary missing** (e.g., `go` not installed) → error result.
- **Non-zero exit** → text result (NOT an error — test failures are legitimate results).
- **Timeout** → text result with `timed out after Ns` marker, exit 124.

## Related

- [run_lint](/tools/run-lint/)
- [run_typecheck](/tools/run-typecheck/)
- [Detector interface](/reference/detector-interface/)
