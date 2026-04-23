---
title: Architecture
description: How the brain/hands split is implemented, and where every trust boundary lives.
---

## The brain/hands split

The PromptKit agent (the **brain**) never directly touches a filesystem, runs a process, or makes a network call. All execution happens inside the sandbox container (the **hands**). They communicate over MCP.

The split matters because prompt injection or model error can corrupt the sandbox; it cannot corrupt the agent runtime or the host.

| Responsibility | Lives in |
|---|---|
| Running the model, holding context | Brain (PromptKit) |
| `TodoWrite`, `SubagentDispatch` | Brain |
| Everything else: Read, Write, Edit, Glob, Grep, Bash, run_tests/lint/typecheck, WebFetch/WebSearch | Hands (this sandbox) |

## Transport

MCP runs over **HTTP+SSE**. A client opens `GET /sse`; the server responds with `event: endpoint` carrying a per-session URL like `/message?sessionId=<uuid>`. JSON-RPC requests `POST` to that URL; responses and notifications stream back as `event: message` frames on the SSE connection.

Why HTTP+SSE and not stdio?

- Works identically for local and remote sandboxes.
- Survives `docker exec`-based local invocation if we ever want it.
- One transport across all provider variants keeps the eval matrix honest.

## Layered defence

The container is the real trust boundary — the sandbox binary has no host filesystem access beyond the mounted workspace volume, and the container runtime controls egress. Inside that, the sandbox applies several **defence-in-depth** layers:

### Path containment

Every filesystem-touching tool routes path arguments through [`workspace.Resolve`](/concepts/trust-boundary/) before any I/O. It:

1. Canonicalises the root with `EvalSymlinks`.
2. Resolves the argument (joining with root for relative paths).
3. Walks symlinks through existing components.
4. Rejects anything with `..` escaping the root.

This prevents an agent from reading `/etc/passwd` by asking for `../etc/passwd` or by setting up a symlink that points outside the workspace.

### Command denylist

`Bash` rejects obvious footgun tokens at plausible command positions (`sudo`, `shutdown`, `mkfs`, etc.) before the command runs. It's defence-in-depth, not a security guarantee — quoted subcommands (`bash -c "sudo ..."`) are deliberately not caught to avoid false positives, and determined attackers bypass with `$(echo su)do`. The container remains the real boundary.

See [Bash denylist](/concepts/bash-denylist/).

### Secret scrubbing

Every tool's text output passes through a regex-based scrubber before it leaves the sandbox. Well-known shapes (AWS keys, GitHub PATs, OpenAI/Anthropic keys, JWTs, PEM private keys, basic-auth URLs, `API_KEY=...` assignments) are replaced with `[REDACTED:<type>]`.

See [Secret scrubbing](/concepts/secret-scrubbing/).

### URL filter

`WebFetch` resolves each URL's host and rejects private ranges (RFC1918, link-local including `169.254.169.254`, loopback, IPv6 equivalents) plus known cloud-metadata hostnames. Redirects are re-filtered at each hop.

See [URL filter](/concepts/url-filter/).

### Post-edit lint feedback

After every successful `Edit` on a Go project, the tool runs `golangci-lint` with a short timeout and appends any findings for the edited file to the response. This is the proposal's "single biggest quality win" — the agent sees mistakes immediately, before it calls `run_lint` or `run_tests`.

See [Post-edit lint feedback](/concepts/post-edit-lint-feedback/).

## Process model

```
┌─────────────────────────────────┐
│ cmd/sandbox                     │
│   main.go  — flag parse + SIGINT/SIGTERM → cancel ctx
│   run.go   — http.Server wiring + graceful shutdown
└─────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────┐
│ internal/server                 │
│   server.go     — MCP server, tool registration
│   middleware.go — scrubbingRegistrar wraps handlers with scrub.Scrub
└─────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────┐
│ internal/tools                                          │
│   read / write / edit / glob / grep / bash / run_*      │
│   bash_output / kill_shell / web_fetch / web_search     │
│   shell_registry  — background shells                   │
│   exec.go         — shared runVerifyCmd with pgroup kill│
└─────────────────────────────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────┐
│ internal/{workspace,verify,web,scrub}  — primitives     │
└─────────────────────────────────┘
```

Each package has one clear responsibility. The separation makes it easy to add a tool (drop in an `internal/tools/foo.go` + `RegisterFoo`) or a language detector (implement the `verify.Detector` interface).

## Sandbox lifecycle

- One sandbox per session.
- Sandbox provider (in the PromptKit repo, not this one) creates a container with a fresh ephemeral volume.
- The container's MCP server starts; the agent connects.
- Agent runs to completion.
- Container destroyed; volume reclaimed.

Warm-volume mode (volume reattached across sessions for the same task) is an opt-in for fast iteration cycles; the sandbox code itself doesn't know or care.

## What's NOT in the sandbox

- **The sandbox provider abstraction** (local Docker, remote Docker, e2b, Modal, Daytona adapters). Lives in the PromptKit repo.
- **Credentials for `git push` / GitHub PRs**. Bring-your-own — supplied at `docker run` time as env vars or mounted files; the sandbox scrubs known-secret shapes from output but doesn't manage identity.
- **gVisor / Firecracker / microVM sandboxing**. Third-party providers can provide stronger isolation; `LocalDockerProvider` gets you a container.
