---
title: "Language support model"
description: "How the sandbox stays honest about which languages each tool supports, and how operators extend to new languages or strip unused ones."
---

The sandbox is deliberately **polyglot-aware**: every tool that depends on language-specific behaviour must declare it explicitly, not fail silently when pointed at a language it wasn't designed for. This page is the contract.

## The `Detector` interface is the extension point

A `Detector` (`internal/verify/detector.go`) represents a project type that the sandbox can reason about. Today:

| Detector | Marker file | Lint | Test | Typecheck |
|---|---|---|---|---|
| Go | `go.mod` | `golangci-lint run ./...` | `go test ./...` | `go vet ./...` |
| Node | `package.json` | `npx eslint .` (compact format) | `npm test --silent` | `npx tsc --noEmit` |
| Python | `pyproject.toml` / `setup.py` | `ruff check` | `pytest` | *(none)* |
| Rust | `Cargo.toml` | `cargo clippy --message-format=short` | `cargo test` | `cargo check` |

Every language-coupled tool — `run_lint`, `run_tests`, `run_typecheck`, post-edit lint feedback, and everything in the [P0/P1 roadmap](#planned-language-coupled-tools) below — dispatches through a `Detector`. No tool has a hardcoded language assumption.

## When you add a language-coupled tool

Extend the `Detector` interface with a method that captures the per-language behaviour. Examples from the current roadmap:

- **Structured test failures** ([#12](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/12)) — add `Detector.ParseTestFailures(stdout, stderr string) []TestFailure`, one implementation per language (go test `-json`, pytest `--tb`, jest `--json`, cargo test `--format json`).
- **Post-edit format** ([#14](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/14)) — add `Detector.FormatCheckCmd() []string` + `Detector.ParseFormatDiff(...) []FormatFinding`.
- **Coverage** ([#16](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/16)) — add `Detector.ParseCoverageProfile(path string) []CoverageEntry`.
- **LSP navigation** ([#9](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/9)) — language-server launch + teardown lives in `internal/lsp/<language>.go`, the Detector exposes only `LSPCommand() []string`.
- **AST edits** / **semantic search** ([#10](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/10), [#11](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/11)) — tree-sitter grammar registered per language in a shared `internal/ast/` registry keyed by `Detector.Name()`.

**Each new tool ships with at least one Detector implementation (usually Go, since it's our dominant path).** Other-language implementations land in subsequent PRs or stay at "not implemented for this language" until someone wires them.

## When a tool is pointed at an unsupported language

Contract: **emit a clear, actionable error; do not silently no-op**.

```
run_tests: no Detector registered for workspace at /workspace
           (found markers: Cargo.toml)
           — this build ships Go, Node, Python, Rust detectors.
           Open an issue or fork the image to add a new detector.
```

vs. the wrong shape: returning "0 tests passed" or "no findings" for a language we never actually ran against.

## Monorepos with multiple languages

See [#19](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/19) for the full issue. The contract:

1. `Detect(root)` returns `[]Detector` — one per marker file found in the workspace.
2. Every language-coupled tool accepts an optional `language` argument (`"go" | "node" | "python" | "rust"`).
3. When `language` is omitted AND multiple detectors match, the tool returns an error listing the detected set; the agent picks one.
4. When `language` is omitted AND exactly one detector matches, the tool uses it (identical to today's single-language behaviour).
5. A `language: "all"` shortcut runs the tool against every detected language and interleaves output, marked per-language.

This keeps the per-request tool surface simple while refusing to silently guess in ambiguous cases.

## Cross-language, language-agnostic tools

A subset of the roadmap is intentionally language-agnostic and carries **no** per-language extension burden:

- **Snapshot / restore** ([#13](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/13)) — git-based; works the same across any workspace.
- **OTel telemetry** ([#17](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/17)) — tool-call metadata, not content.
- **Secrets interface** ([#18](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/18)) — env + file access.
- **`watch_process`** ([#20](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/20)) — regex-configurable crash detection, not tied to a runtime.
- **`-readonly` mode** ([#21](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/21)) — capability gate.
- **Render tools** ([#22](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/22)) — mermaid / dot are their own mini-languages, orthogonal to source languages.
- **Multi-workspace** ([#23](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/23)) — amplifies the monorepo story but doesn't add language coupling itself.

Prioritise these for PRs that don't need to pull tree-sitter grammars or language-server binaries into the tools-layer image.

## Operator: strip unused languages

The tools-layer image ships with the full Detector set, which means `rustfmt`, `ruff`, `eslint`, `golangci-lint`, and eventually tree-sitter grammars / LSP binaries for all four. For operators who only target one language, that's dead weight.

The tools-layer pattern (see [Docker operations](/operations/docker/)) lets you **fork the Dockerfile and delete the binaries you don't need**. The sandbox degrades gracefully: `Detect` returns a subset based on markers present in the workspace, and only the matching Detectors get used.

## Operator: add a new language

1. Open an issue describing the target (marker file, lint/test/typecheck commands).
2. Implement a `Detector` in `internal/verify/<language>.go`.
3. Extend `verify.Detect` to recognise the new marker.
4. Add the runtime + CLI tooling to `Dockerfile.tools` (or a fork of it).
5. Add per-language test fixtures under `internal/verify/<language>_test.go`.
6. Extend any `go:S3776`-style language-coupled tools you care about (structured failures, coverage, format) — each takes its own `Detector` method.

The sandbox binary does not need to recompile when you add a language via an image fork — the Detector interface is the recompile-free boundary **only if** the language-coupled tools you want are already implemented for whatever Detector shape you're providing. Adding a brand-new language + brand-new language-coupled tool is a two-PR exercise.

## What this contract prevents

- **Silent misbehaviour**: "I ran tests" for a workspace that uses a language the sandbox was never built to test.
- **Implicit language selection**: if a polyglot repo has Go and Node, the sandbox refuses to guess which one you meant.
- **Per-tool language hardcoding**: every language-coupled tool goes through a Detector; future languages don't require N scattered edits.
- **Feature coupling drift**: if someone adds coverage support for Python before Go, it's explicit that Go is missing — no "well, we have some Go stuff and some Python stuff and it's unclear which" state.

## What this contract does NOT solve

- **Cross-language refactors** (e.g. rename a Go symbol that's referenced in a Python service). LSP doesn't cross language boundaries; tree-sitter doesn't either. Out of scope for this repo — belongs to higher-level tooling.
- **Runtime version selection** within a language. The image ships one Go version, one Node version, etc. Operators bump via image tags; we don't surface a `go_version` arg on `run_tests`.
- **Language-specific package-manager semantics** (npm vs pnpm vs yarn, pip vs poetry vs uv). Each Detector picks one and documents it; alternates require a fork.
