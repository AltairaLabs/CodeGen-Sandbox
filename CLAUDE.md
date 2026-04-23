# codegen-sandbox — Claude Code Project Instructions

## Project

Docker-based MCP server that ships safe codegen tools (Read, Edit, Write, Glob, Grep, Bash, run_tests/lint/typecheck) for PromptKit agents. WebFetch/WebSearch intentionally not included — agents connect vendor MCP servers (Brave / Exa / Tavily / official `fetch`) alongside this sandbox; see `docs/concepts/non-sandbox-tools`. The brain (PromptKit agent) and hands (this sandbox) are separated by an MCP wire — PromptKit calls tools over MCP, this server executes them inside the container.

Spec: [docs/PROPOSAL.md](docs/PROPOSAL.md). Read it first.

## Lineage

This repo is a fresh-start replacement for [`../codegen-mcp`](../codegen-mcp/), an early prototype that over-engineered the transport (gRPC coordinator/worker, hand-rolled task queue, custom worker registry). Lessons informed this design; **no code is being ported**. That repo is left untouched as historical reference.

## What ships from this repo

- A single Go binary: the sandbox MCP server.
- A Docker image containing the binary plus pinned toolchains (Go, Node, Python, ripgrep, golangci-lint, git, common build tools).

That's it. The PromptKit-side wiring (sandbox provider abstraction, capability registration, codegen skill) lives in the PromptKit repo.

## Tech stack (committed)

- Go 1.24+
- MCP server library: `github.com/mark3labs/mcp-go` (proven from prior prototype)
- HTTP+SSE transport (works for both local and remote sandboxes)
- Standard Go testing + testify
- golangci-lint
- Docker, multi-stage build

## Conventions

- **Conventional commits** (`feat:`, `fix:`, `chore:`, `ci:`, `docs:`).
- **TDD by default**: failing test → minimal impl → green → commit. Bootstrap/scaffolding tasks are exempt.
- **golangci-lint must pass** before commit.
- **Path containment is non-negotiable**: every filesystem-touching tool resolves and validates paths against the workspace root before any I/O.
- **Structured tool output**: tools return structured fields (e.g. `file:line:rule:message` for lint), not raw subprocess stdout dumps.
- **Same MCP server across all eval methodologies**: do not fork the server for benchmark variants.

## Implementation plans

Plans live in `docs/plans/` (not yet created). The series sketched in the prior PromptKit session:

1. `2026-04-22-codegen-sandbox-foundation.md` — scaffolding, MCP server skeleton, path containment, Read/Write/Edit
2. `codegen-sandbox-search.md` — Glob, Grep
3. `codegen-sandbox-bash.md` — Bash foreground + command denylist
4. `codegen-sandbox-verification.md` — project detection + run_tests/run_lint/run_typecheck + post-edit feedback wired into Edit
5. `codegen-sandbox-scrubbing.md` — secret scrub middleware
6. `codegen-sandbox-docker.md` — Dockerfile + image build + CI publish
7. `codegen-sandbox-bash-background.md` — background mode + BashOutput + KillShell
8. ~~`codegen-sandbox-web.md`~~ — dropped; WebFetch/WebSearch live in vendor MCP servers alongside this sandbox (see `docs/concepts/non-sandbox-tools`).

A separate plan in PromptKit will cover the consumer side (sandbox provider, capability, skill).

## Where the prior session left off

Brainstorming and proposal are done (see `docs/PROPOSAL.md`). Implementation plans are not yet written. The next step is to write Plan 1 (foundation) using the `superpowers:writing-plans` skill, then execute it via `superpowers:subagent-driven-development` or `superpowers:executing-plans`.
