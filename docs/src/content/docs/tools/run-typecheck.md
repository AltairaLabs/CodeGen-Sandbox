---
title: run_typecheck
description: Run the detected project's type checker.
---

Run the project's type checker. Go today (uses `go vet`); future detectors add `tsc`, `mypy`, etc.

## Schema

| Param | Type | Required | Default |
|---|---|---|---|
| `timeout` | number | no | 120 (clamped to 600) |

## Behaviour

Near-clone of [`run_tests`](/tools/run-tests/) but invokes `detector.TypecheckCmd()` (for Go: `go vet ./...`). Output format is identical: stdout → optional `--- stderr ---` → optional timeout marker → trailing `exit: N`.

`go vet` emits diagnostics on stderr, so typical output looks like:

```
--- stderr ---
./bad.go:6:17: fmt.Printf format %d has arg "not-an-int" of wrong type string
exit: 1
```

## Failure modes

Same as `run_tests`:

- No detector → error result.
- Binary missing → error result.
- Non-zero exit → text result (not an error).
- Timeout → text result with marker + exit 124.

## Related

- [run_tests](/tools/run-tests/)
- [run_lint](/tools/run-lint/) — static analysis is usually run together with vet for a full verify pass.
