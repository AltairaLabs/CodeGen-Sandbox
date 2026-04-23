---
title: Detector interface
description: How project-type detection drives run_tests, run_lint, and run_typecheck.
---

The `verify.Detector` interface decouples the verify tools from specific toolchains. The sandbox ships four detectors — Go, Rust, Node, Python — that automatically pick the right commands based on which marker file (`go.mod`, `Cargo.toml`, `package.json`, `pyproject.toml` / `setup.py`) is at the workspace root.

## The interface

```go
type Detector interface {
    Language() string       // "go", "node", "python", ...
    TestCmd() []string      // argv for the test runner
    LintCmd() []string      // argv for the linter
    TypecheckCmd() []string // argv for the type checker

    ParseLint(stdout, stderr string) []LintFinding
    ParseTestFailures(stdout, stderr string) []TestFailure
}

func Detect(root string) Detector
```

`ParseTestFailures` powers the [`last_test_failures`](/tools/last-test-failures/) tool. Detectors with no parser (v1: everyone except Go) return `nil` — the tool surfaces a clear "not supported for <language>" notice rather than silently claiming zero failures.

`Detect` inspects the workspace root for marker files and returns the first matching detector, or `nil` if none are found. Only the immediate root is inspected; markers in subdirectories don't count (the workspace root is the authoritative anchor).

## Go implementation

Marker: `go.mod` in the workspace root.

```go
type goDetector struct { root string }

func (*goDetector) Language() string      { return "go" }
func (*goDetector) TestCmd() []string      { return []string{"go", "test", "-json", "-count=1", "./..."} }
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

## Shipped detectors

| Language | Marker | Test | Lint | Typecheck |
|---|---|---|---|---|
| Go | `go.mod` | `go test -json -count=1 ./...` | `golangci-lint run ./...` | `go vet ./...` |
| Rust | `Cargo.toml` | `cargo test` | `cargo clippy --all-targets -- -D warnings` | `cargo check --all-targets` |
| Node | `package.json` | `npm test --silent` | `npx --no-install eslint . --format=compact` | `npx --no-install tsc --noEmit` |
| Python | `pyproject.toml` / `setup.py` | `pytest` | `ruff check .` | `mypy .` |

Detection order (first match wins): Go → Rust → Node → Python. A Go service with a frontend `package.json` detects as Go — operators whose workspaces need the frontend detected should split them into separate workspace roots.

## Binary availability

The sandbox invokes the commands above with `exec.Command`. When a binary isn't on PATH:

- For `run_tests`/`run_typecheck` — the spawn fails and the handler returns `ErrorResult("<tool>: <binary>: not found on PATH")`.
- For `run_lint` — the handler returns `linter not installed: <binary>` (the `ErrLinterMissing` sentinel), naming the missing binary so operators can tell whether it's a dev-env or Docker-image misconfiguration.

This is how the [tools-layer Docker pattern](/operations/docker/) works: the sandbox binary is language-agnostic and just invokes commands; the operator's base image provides the toolchain. A Python image without `pytest` installed returns a clear error instead of silently falling back.

## Lint-output parsing

Each detector implements `ParseLint(stdout, stderr string) []LintFinding` to convert its linter's raw output into structured records. Two strings are passed because linters differ in which stream they use:

| Detector | Linter | Emits findings on | Rule capture |
|---|---|---|---|
| Go | golangci-lint v2 | stdout | rule name, e.g. `errcheck` |
| Python | ruff | stdout | rule code, e.g. `F401` |
| Node | eslint (--format=compact) | stdout | rule name, e.g. `semi` / `no-unused-vars` |
| Rust | cargo clippy (--message-format=short) | stderr | severity (`warning` or `error`) — clippy's short format drops the rule name |

Unrecognised lines (context, summary blocks, cargo's "Checking..." banner) are silently skipped. Each parser is unit-tested against sample output in `parser_test.go`.

## Adding a detector

1. Create `internal/verify/<language>.go` with a struct implementing the five `Detector` methods.
2. Extend `Detect` in `internal/verify/verify.go` to check for the marker file (specific markers first).
3. Write the `ParseLint` implementation. If the linter's output resembles `<file>:<line>:<col>: <msg> (<rule>)` or similar, reuse `parseLintRegex` with a tailored regex. Otherwise write a custom parser.
4. Update the Dockerfile / example image to install the runtime.
5. Add unit tests: marker detection (cheap), parser regex against sample output (cheap), and optionally a live integration test behind a `requireXxx(t)` skip helper (like `requireGolangciLint`) for CI environments that have the tool installed.
