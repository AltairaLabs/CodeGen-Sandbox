---
title: "Integration tests"
description: "Four-tier test strategy: unit, integration (real binaries), end-to-end MCP wire, and Docker-image smoke."
---

The project ships four tiers of tests. Each tier catches a different class of regression — a change is "done" when every tier the change can plausibly break is green.

## Tier 1 — Unit tests (`make test`)

Default `go test ./... -race -count=1`. Runs on every push and every PR via the `go` CI job. Covers pure-Go logic and the mock-backed LSP wire client. No external binary dependencies beyond the Go toolchain itself.

```bash
make test
```

## Tier 2 — Integration tests (`make test-integration`)

Files tagged `//go:build integration`. Drive the real external binaries the sandbox image ships:

| Package | Binary | What it catches |
| --- | --- | --- |
| `internal/lsp/integration_test.go` | `gopls` | Mock-vs-real wire drift (the original implementation returned no rename edits because gopls uses `documentChanges` not `changes`; this tier caught it) |
| `internal/verify/integration_test.go` | `golangci-lint` | Lint-output format changes across linter versions |
| `internal/tools/integration_test.go` | `go` | Structured-failure parsing from live `go test -json` output |

Run locally:

```bash
make test-integration
```

Binaries the tier needs on PATH:

- `go` (always present in a Go dev environment)
- `gopls` — `go install golang.org/x/tools/gopls@latest`
- `golangci-lint` v2 — `brew install golangci-lint` / `apt install golangci-lint`

Any missing binary **skips** the corresponding test with a clear message; it is not a failure. That keeps the target safe to run on a partially-provisioned machine while still being meaningful when all three are present.

CI runs this tier in a dedicated `integration` job that installs each binary fresh.

## Tier 3 — End-to-end MCP wire (`scripts/e2e-p0.sh`)

Out-of-test-tree smoke that chains every P0 tool over the real MCP HTTP+SSE wire against a real `bin/sandbox` binary. This is the only tier that exercises the tool surface through the full transport — MCP initialization, JSON-RPC request / SSE response round-trips, tool dispatch, the scrub + metrics + tracing middleware stack. Mirrors `scripts/e2e-demo.sh` in shape.

```bash
bash scripts/e2e-p0.sh
```

Runtime ~60s on a warm Go cache. LSP steps skip cleanly when `gopls` isn't on PATH; everything else is unconditional. Binaries needed: `go`, `curl`, `jq`, `git`, `ripgrep`, `gopls` (optional).

CI runs this tier as the `e2e-smoke` job: installs `gopls` + `ripgrep`, builds the binary, runs the script. No binary skips in CI — every LSP step is exercised.

## Tier 4 — Docker image smoke (CI only)

The `docker-integration` CI job:

1. Builds both `Dockerfile.tools` and `Dockerfile.tools-go` locally via buildx.
2. Composes them into an operator-style probe image: `golang:1.25-alpine` base, plus `sandbox` / `rg` / `gopls` / `golangci-lint` copied in.
3. Boots a container, opens the SSE stream, initialises an MCP session, and issues `tools/list`.
4. Asserts every P0 tool name is present in the response.

This is the only tier that verifies **the published image actually boots and registers its tool surface**. If this goes red, no other test tier's green matters — the operators can't run the thing.

See [#58](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/58) for the planned extension to every feature layer (`-node` / `-python` / `-rust` / `-render`).

## When to run what

- **Small refactor, no external deps touched**: unit tests cover it.
- **New tool, new flag, anything agent-visible**: add a case to `scripts/e2e-p0.sh` and run it locally before pushing.
- **Touching the LSP client, the lint parser, or the Go test parser**: the integration tier is your regression net; run `make test-integration` locally.
- **Touching the MCP transport, middleware, or any tool handler's wire contract**: `e2e-smoke` catches the full-stack regression.
- **Dockerfile or image composition change**: push the branch and let `docker-integration` gate the merge.
