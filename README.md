# codegen-sandbox

[![CI](https://github.com/AltairaLabs/CodeGen-Sandbox/actions/workflows/ci.yml/badge.svg)](https://github.com/AltairaLabs/CodeGen-Sandbox/actions/workflows/ci.yml)
[![Quality Gate Status](https://sonarcloud.io/api/project_badges/measure?project=AltairaLabs_CodeGen-Sandbox&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=AltairaLabs_CodeGen-Sandbox)
[![Coverage](https://sonarcloud.io/api/project_badges/measure?project=AltairaLabs_CodeGen-Sandbox&metric=coverage)](https://sonarcloud.io/summary/new_code?id=AltairaLabs_CodeGen-Sandbox)
[![Maintainability](https://sonarcloud.io/api/project_badges/measure?project=AltairaLabs_CodeGen-Sandbox&metric=sqale_rating)](https://sonarcloud.io/summary/new_code?id=AltairaLabs_CodeGen-Sandbox)

Docker-based MCP server providing safe codegen tools for PromptKit agents. The brain (PromptKit) and hands (this sandbox) are separated by an MCP wire.

**Spec:** [docs/PROPOSAL.md](docs/PROPOSAL.md)

**Docs:** the Astro site under [`docs/`](docs/) covers architecture, every tool, concepts (trust boundary, path containment, secret scrubbing, URL filter, post-edit lint feedback), operations (Docker deployment, extending), and reference (MCP protocol, Detector interface, configuration).

## Quickstart

```bash
# Build + run the Go convenience image
make docker-build
make docker-run

# Or compose the tools layer into your own base image (see examples/)
docker pull ghcr.io/altairalabs/codegen-sandbox-tools:latest
```

See [docs/operations/docker](docs/src/content/docs/operations/docker.md) for the composition pattern and production hardening.

## Tools shipped

13 MCP tools: `Read`, `Write`, `Edit` (with post-edit lint feedback); `Glob`, `Grep`; `Bash` (foreground + background) with `BashOutput` and `KillShell`; `run_tests`, `run_lint`, `run_typecheck` (Go, Rust, Node, Python detectors); `WebFetch` (SSRF-filtered), `WebSearch` (backend-pluggable).

All tool output is scrubbed for well-known secret shapes before leaving the sandbox.
