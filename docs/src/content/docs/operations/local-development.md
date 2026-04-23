---
title: Local development
description: How to work on the codegen-sandbox source.
---

## Prerequisites

- Go 1.25+
- `golangci-lint` v2 (`brew install golangci-lint` / `apt install golangci-lint`)
- `ripgrep` (`brew install ripgrep` / `apt install ripgrep`)
- `bash` (preinstalled on macOS and most Linux)
- Docker (for image builds)

The Makefile's `test`, `lint`, `fmt` targets work from a plain clone; `docker-build` additionally needs Docker.

## Common flows

```bash
# Unit + integration tests, race detector on
make test

# Lint
make lint

# Format (gofmt + goimports — goimports must be on PATH)
make fmt

# Build the local binary
make build
./bin/sandbox -workspace=/tmp/ws

# Build + run the Docker image
make docker-build
make docker-run
```

## Project layout

```
cmd/sandbox/           Entry point (main + Run + graceful shutdown)
internal/workspace/    Path containment + read tracker
internal/server/       MCP server + scrubbing middleware
internal/tools/        MCP tool handlers + shared exec helper
internal/verify/       Project detection + lint parser
internal/scrub/        Secret-pattern redaction
docs/                  This Astro docs site
Dockerfile             Multi-stage container image
```

## Conventions

- **Conventional commits** (`feat:`, `fix:`, `chore:`, `docs:`).
- **TDD by default**: failing test → minimal impl → green → commit. Bootstrapping is the documented exception.
- **`golangci-lint` must pass** before commit. Run `make lint` locally.
- **Path containment is non-negotiable**: every filesystem-touching tool resolves paths via `workspace.Resolve` before I/O.
- **Structured tool output**: tools return structured fields (e.g. `file:line:rule:message` for lint), not raw subprocess stdout dumps.
- **Same MCP server across all eval methodologies**: no forks for benchmark variants.

## Implementation plans

The full implementation history is in `docs/plans/` — one document per feature, written before implementation and versioned with the code. Each plan has:

- Spec + architecture + tech stack.
- File structure (what's created/modified).
- Numbered tasks with TDD steps.
- Self-review notes.

Plans are organised by date:

- 2026-04-22 foundation, search, bash, verification, scrubbing
- 2026-04-23 docker, bash-background, web

Plans are executed via the `superpowers:subagent-driven-development` skill, which dispatches a fresh implementer subagent per task with spec-compliance and code-quality review checkpoints.

## Adding a feature

See [Extending](/operations/extending/) for the common extension points (new tool, new detector, new WebSearch backend, new scrub pattern).

For anything bigger, write a plan first and execute it task-by-task. Prior plans under `docs/plans/` are a reference for the expected shape.

## Running the docs locally

```bash
cd docs
npm install
npm run dev
```

Visit `http://localhost:4321/`. Astro hot-reloads on file save.

## Docs hygiene

```bash
cd docs
npm run build         # Catches broken frontmatter and typecheck errors.
npm run check-links   # Catches broken internal + external links.
```
