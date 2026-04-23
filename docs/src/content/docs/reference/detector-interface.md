---
title: Detector interface
description: How project-type detection drives run_tests, run_lint, and run_typecheck.
---

The `verify.Detector` interface decouples the verify tools from specific toolchains. v1 ships a Go implementation; new languages plug in by implementing the interface.

## The interface

```go
type Detector interface {
    Language() string       // "go", "node", "python", ...
    TestCmd() []string      // argv for the test runner
    LintCmd() []string      // argv for the linter
    TypecheckCmd() []string // argv for the type checker
}

func Detect(root string) Detector
```

`Detect` inspects the workspace root for marker files and returns the first matching detector, or `nil` if none are found. Only the immediate root is inspected; markers in subdirectories don't count (the workspace root is the authoritative anchor).

## Go implementation

Marker: `go.mod` in the workspace root.

```go
type goDetector struct { root string }

func (*goDetector) Language() string      { return "go" }
func (*goDetector) TestCmd() []string      { return []string{"go", "test", "./..."} }
func (*goDetector) LintCmd() []string      { return []string{"golangci-lint", "run", "./..."} }
func (*goDetector) TypecheckCmd() []string { return []string{"go", "vet", "./..."} }
```

## Adding a new language

1. **Pick markers.** `package.json` for Node, `pyproject.toml` or `setup.py` for Python, `Cargo.toml` for Rust.
2. **Implement `Detector`.** Example for Node:

   ```go
   type nodeDetector struct{ root string }

   func (*nodeDetector) Language() string      { return "node" }
   func (*nodeDetector) TestCmd() []string      { return []string{"npm", "test", "--silent"} }
   func (*nodeDetector) LintCmd() []string      { return []string{"npx", "eslint", ".", "--format=compact"} }
   func (*nodeDetector) TypecheckCmd() []string { return []string{"npx", "tsc", "--noEmit"} }
   ```

3. **Register in `Detect`.** Add a check for the marker in the if/else chain in `internal/verify/verify.go`.
4. **Docker image.** Add the runtime (e.g. `apk add nodejs npm`) to the Dockerfile.
5. **Parser.** If the new linter uses a different output format than `<file>:<line>:<col>: <msg> (<rule>)`, update `verify.ParseLint` (or add a per-detector `ParseLint` method).

## Parser limits

`verify.ParseLint` is currently golangci-lint-specific. It uses a non-greedy regex to correctly isolate the trailing `(rule)` suffix even when the message contains parentheses. The `sk-ant-` problem from the scrub package doesn't apply here (no two linters share an output shape), but when a second detector lands the parser should be generalised.

Today's exit-code convention assumption: exit 1 means "findings exist" (golangci-lint), exit ≥ 2 means "genuine failure." That's NOT universal — eslint exits 1 for warnings AND errors, ruff exits 1 for any finding. When Node / Python detectors land, the exit-code semantics should move into `Detector` (e.g. `LintErrorThreshold() int`).

## Known detectors (roadmap)

| Language | Marker | Test | Lint | Typecheck |
|---|---|---|---|---|
| Go (shipped) | `go.mod` | `go test ./...` | `golangci-lint run ./...` | `go vet ./...` |
| Node (future) | `package.json` | `npm test` | `npx eslint .` | `npx tsc --noEmit` |
| Python (future) | `pyproject.toml` | `pytest` | `ruff check .` | `mypy .` |
| Rust (future) | `Cargo.toml` | `cargo test` | `cargo clippy` | `cargo check` |

None of these require sandbox-code changes beyond the detector implementation itself.
