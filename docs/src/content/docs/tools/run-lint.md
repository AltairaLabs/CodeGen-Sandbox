---
title: run_lint
description: "Run the project's linter with structured file:line:col:rule: message output."
---

Run the detected project's linter and return structured findings. Go today (uses `golangci-lint`); future detectors will plug in eslint / ruff / clippy.

## Schema

| Param | Type | Required | Default |
|---|---|---|---|
| `timeout` | number | no | 120 (clamped to 600) |

## Behaviour

1. Detect project. Nil detector → `no supported project detected`.
2. If the linter binary isn't on PATH → `linter not installed: <binary>` (names the binary so operators can tell if it's a dev-env or Docker-image misconfiguration).
3. Run `detector.LintCmd()` from the workspace root.
4. Parse the linter's stdout with `verify.ParseLint`: a regex that matches `<file>:<line>:<col>: <msg> (<rule>)`, tolerating context lines and the trailing `N issues:` summary block.
5. Return structured output:

```
path/to/file.go:42:10:errcheck: Error return value of `os.WriteFile` is not checked
path/to/file.go:55:3:govet: printf: fmt.Printf format %d has arg "x" of wrong type string
2 findings
```

## Partial findings on error

If the linter times out or exits ≥ 2, the handler forwards any findings it *did* parse plus an `(lint incomplete: ...)` trailer — partial results are more useful than a plain error. This differs from [`Edit`](/tools/edit/)'s post-edit feedback, which suppresses findings on any error because its primary signal is "the replacement succeeded."

## Clean-module output

```
0 findings
```

Always present, even when zero.

## Exit-code semantics

`golangci-lint` exits 1 when findings exist — that's NOT an error, it's data. The handler treats exit 0 and exit 1 identically (findings, nil). Exit ≥ 2 is a genuine linter failure.

## Related

- [Edit](/tools/edit/) — post-edit lint feedback uses the same `verify.Lint` helper, filtered to the edited file.
- [run_typecheck](/tools/run-typecheck/) — for `go vet` and equivalents.
