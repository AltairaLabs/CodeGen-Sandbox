---
title: "Language support model"
description: "How the sandbox stays honest about which languages each tool supports, and how operators extend to new languages or strip unused ones."
---

The sandbox is deliberately **polyglot-aware**: every tool that depends on language-specific behaviour must declare it explicitly, not fail silently when pointed at a language it wasn't designed for. This page is the contract.

## The `Detector` interface is the extension point

A `Detector` (`internal/verify/detector.go`) represents a project type that the sandbox can reason about. Today:

| Detector | Marker file | Lint | Test | Typecheck |
|---|---|---|---|---|
| Go | `go.mod` | `golangci-lint run ./...` | `go test -json -count=1 ./...` | `go vet ./...` |
| Node | `package.json` | `<pm> run lint` when defined, else `npx eslint .` (compact format) | `<pm> run test` when defined, else `npm test --silent` | `<pm> run typecheck` when defined, else `npx tsc --noEmit` |
| Python | `pyproject.toml` / `setup.py` | `ruff check` | `pytest` | *(none)* |
| Rust | `Cargo.toml` | `cargo clippy --message-format=short` | `cargo test` | `cargo check` |

For Node projects, `<pm>` is the package manager the sandbox picks from lock-file presence: `pnpm-lock.yaml` → `pnpm`, `yarn.lock` → `yarn`, `bun.lockb` → `bun`, `package-lock.json` → `npm`, fallback `npm`. The [`run_script`](/tools/run-script/) tool uses the same mapping to invoke arbitrary entries from `package.json#scripts`.

Every language-coupled tool — `run_lint`, `run_tests`, `run_typecheck`, post-edit lint feedback, and everything in the [P0/P1 roadmap](#planned-language-coupled-tools) below — dispatches through a `Detector`. No tool has a hardcoded language assumption.

## When you add a language-coupled tool

Extend the `Detector` interface with a method that captures the per-language behaviour. Examples from the current roadmap:

- **Structured test failures** ([#12](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/12)) — `Detector.ParseTestFailures(stdout, stderr string) []TestFailure` is wired on every detector; Go ships the first implementation (test2json parser), other languages return nil until someone wires pytest `--tb`, jest `--json`, cargo test `--format json`.
- **Post-edit format** ([#14](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/14)) — add `Detector.FormatCheckCmd() []string` + `Detector.ParseFormatDiff(...) []FormatFinding`.
- **Coverage** ([#16](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/16)) — add `Detector.ParseCoverageProfile(path string) []CoverageEntry`.
- **LSP navigation** ([#9](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/9)) — language-server launch + teardown lives in `internal/lsp/<language>.go`, the Detector exposes only `LSPCommand() []string`.
- **AST edits** / **semantic search** ([#10](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/10), [#11](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/11)) — per-language adapter registered in `internal/ast/`, keyed by file extension. v1 uses stdlib `go/parser` for the Go adapter (every tree-sitter Go binding we tried required CGO, which is incompatible with the current `CGO_ENABLED=0` + `scratch` image); the registry shape is otherwise tree-sitter-ready for the next language to plug in under a build tag.

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

Polyglot workspaces (e.g. a Go service with a frontend `package.json`, or a Python service with a Rust extension crate) are first-class. The contract, implemented in `verify.DetectAll` and `tools.dispatchByLanguage`:

1. **`verify.DetectAll(root)`** returns every `Detector` whose marker is present at `root`, in a stable order: Go → Rust → Node → Python.
2. Language-coupled verify tools (`run_tests`, `run_lint`, `run_typecheck`, `run_failing_tests`, `run_script`) accept an optional `language` argument: `"go"`, `"node"`, `"python"`, or `"rust"`. Case-insensitive; surrounding whitespace trimmed.
3. **When `language` is omitted AND exactly one detector matches**, the tool uses it. Single-language workspaces behave identically to the pre-polyglot version.
4. **When `language` is omitted AND multiple detectors match**, the tool returns an error listing the detected set:

   ```
   polyglot workspace: 2 project types detected (go, node) — pass `language` to pick one
   ```

   The agent picks one and retries. The sandbox never guesses in ambiguous cases.
5. **When `language` is provided**, the matching detector is used. If the hinted language isn't one of the detected set:

   ```
   language "rust" not detected in workspace; detected: go, node
   ```

### Not in this iteration

- **`language: "all"` shortcut** — running every detector and interleaving output is a separate design call (per-language prefixing, per-language coverage handling, etc.). Agents working across several languages in a monorepo should call each tool once per language.
- **Cross-language LSP dispatch** — `find_definition` / `find_references` / `rename_symbol` still use `verify.Detect` (first-match). In a polyglot workspace an agent navigating a `.ts` file while `go.mod` is also present will get the Go LSP, not the Node one. Tracked separately; the dispatch helper is a drop-in target when that lift happens.

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

## Image composition model

**The sandbox never bundles a language runtime.** The operator composes their container image from three layers:

1. **Base image** — their language choice: `golang:1.25-alpine`, `node:22-alpine`, `python:3.11-slim`, `rust:1.83-slim`, etc. Carries the language runtime + its stock package manager (go, npm, pip, cargo).
2. **Sandbox tools layer** (`ghcr.io/altairalabs/codegen-sandbox-tools`) — scratch image with exactly two binaries: `/sandbox` and `/rg`. Always `COPY --from=`'d.
3. **Feature tools layers** (planned — see [#26](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/26)) — per-feature scratch images carrying binaries that particular features need: `gopls` for LSP navigation, `pnpm` / `typescript-language-server` / `prettier` for Node tooling, `ruff` for Python format, `mmdc` / `dot` for render tools, etc. Operator `COPY --from=`'s the ones they want. Each layer is small (MBs, not GBs) so composing several is cheap.

Example — a Next.js project with LSP + formatter + framework support:

```dockerfile
FROM node:22-alpine

COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools:latest /sandbox /usr/local/bin/
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools:latest /rg /usr/local/bin/

# Feature layers — only the ones the Next.js path cares about.
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools-node:latest /pnpm /usr/local/bin/
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools-node:latest /typescript-language-server /usr/local/bin/
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools-node:latest /prettier /usr/local/bin/

WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/sandbox"]
```

### Feature → runtime-binary matrix

Every tool / feature declares its runtime requirement. A feature whose binaries aren't present returns a **clear, actionable error** ("`gopls` not found on PATH") — never a silent no-op.

| Capability | Language | Binaries | Notes |
|---|---|---|---|
| `run_tests` / `run_lint` / `run_typecheck` | Go | `go`, `golangci-lint` | In `codegen-sandbox-tools-go` |
| | Node | `npm` (or `pnpm` / `yarn` / `bun`), `eslint`, `tsc` (project-local via `npx`) | Core PMs in `-node`; eslint/tsc via project deps |
| | Python | `pytest`, `ruff`, `mypy` (project-local) | In `-python` |
| | Rust | `cargo`, `clippy`, `rustfmt` | `-rust` (rustfmt ships with toolchain) |
| Post-edit format ([#14](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/14)) | Python | `ruff` | `-python` |
| | Node | `prettier` | `-node` (or project-local) |
| | Rust | `rustfmt` | `-rust` |
| LSP navigation ([#9](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/9)) | Go | `gopls` | `-go` |
| | Python | `pyright-langserver` or `pylsp` | `-python` |
| | Node | `typescript-language-server` | `-node` |
| | Rust | `rust-analyzer` | `-rust` |
| AST edits ([#10](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/10)) | Go (v1) | (none — stdlib `go/parser` + `go/ast` linked into the sandbox binary) | v1 ships Go only; the `internal/ast` registry leaves a slot for tree-sitter grammars to land under a build tag for Python / TS / Rust in a follow-up issue. See [AST-safe edit primitives](/tools/ast-edits/). |
| Semantic search ([#11](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/11)) | Go (v1) | (none — `go/ast` stdlib) | BM25 over Go symbols + docstrings; extensible per language via `internal/search/` extractor registry. Other languages follow when tree-sitter lands. |
| Render tools ([#22](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/22)) | any | `mmdc` (mermaid-cli), `dot` (graphviz) | `codegen-sandbox-tools-render` |
| Next.js / framework scripts ([#25](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/25)) | Node | `pnpm` / `yarn` / `bun` (as applicable) | `-node` (all three bundled) |

### Operator: strip unused capabilities

- Want only Go + LSP? COPY from `-go` only; skip `-node` / `-python` / `-rust`.
- Want Node without LSP? COPY `/pnpm` but not `/typescript-language-server`. The feature layers are structured so individual binaries are copyable independently.
- Want the bleeding edge of one binary? Don't COPY from our layer — `RUN apk add ...` it yourself.

### Operator: add a new language

1. Open an issue describing the target (marker file, lint/test/typecheck commands, runtime binaries it needs).
2. Implement a `Detector` in `internal/verify/<language>.go`.
3. Extend `verify.Detect` to recognise the new marker.
4. Define the runtime-binary set in the Detector (e.g. `LintCmd() []string{"my-linter", ...}`).
5. Provide (or fork) a feature tools layer image carrying `my-linter` for operators who want it.
6. Add per-language test fixtures under `internal/verify/<language>_test.go`.
7. Extend any language-coupled tools you care about (structured failures, coverage, format) — each takes its own `Detector` method.

The sandbox binary does not need to recompile when you add a language via an image fork — the Detector interface is the recompile-free boundary **only if** the language-coupled tools you want are already implemented for whatever Detector shape you're providing. Adding a brand-new language + brand-new language-coupled tool is a two-PR exercise.

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
