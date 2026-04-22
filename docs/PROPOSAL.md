# PromptKit Codegen Sandbox

**Status:** Proposal. The accompanying methodology eval doc lives in the promptkit repo at `docs/local-backlog/CODEGEN_BUNDLE_PROPOSAL.md`.

**What this is:** A set of tools that let a PromptKit agent generate, edit, and verify code, plus a remote Docker-based sandbox the tools execute inside. Shipped as PromptKit primitives (capability + tools + skill), not a standalone product.

**What this isn't:** A multi-agent platform, a SaaS, a deployment story, a billing system, or anything with a customer dashboard. Just tools + a sandbox.

**Lineage:** A previous attempt, [`codegen-mcp`](../../codegen-mcp/), validated the brain/hands split as the right idea but over-engineered the transport (gRPC coordinator/worker, hand-rolled task queue, custom worker registry, "untestable infrastructure" carve-outs). This proposal keeps the split and replaces the transport with MCP plus Docker. No code is being ported.

---

## 1. Brain and hands

The PromptKit agent (the **brain**) never directly touches a filesystem, runs a process, or makes a network call. All execution happens inside a Docker container (the **hands**). Brain and hands talk over MCP.

This is a trust boundary. Prompt injection or model error can corrupt the sandbox; it cannot corrupt the agent runtime or the host.

```
┌──────────────────┐         ┌────────────────────────────────────┐
│  PromptKit agent │  MCP    │  Sandbox container (docker)        │
│  (brain)         │ ──────► │  (hands)                           │
│                  │         │                                    │
│  - runs model    │         │  - Read / Edit / Write             │
│  - holds context │         │  - Glob / Grep                     │
│  - TodoWrite     │         │  - Bash (incl. background)         │
│  - SubagentDisp. │         │  - run_tests / run_lint / ...      │
│                  │         │  - WebFetch / WebSearch            │
│  no exec privs   │         │  - workspace volume mounted        │
└──────────────────┘         └────────────────────────────────────┘
```

---

## 2. Sandbox container

A Docker image that ships:
- The sandbox MCP server as PID 1 (or via tini).
- Pinned versions of common toolchains (Go, Node, Python, ripgrep, golangci-lint, common build tools, git).
- A standard filesystem layout: `/workspace` (mounted volume, read-write), everything else read-only.

The container exposes one MCP endpoint. Workspace is a Docker volume; nothing on the host is reachable.

---

## 3. Sandbox lifecycle

One sandbox per session.

1. Agent opens a session.
2. Sandbox provider creates a container with a fresh ephemeral volume (and optionally clones a target repo into it).
3. The container's MCP server starts; the agent connects.
4. Agent runs to completion.
5. Container destroyed; volume reclaimed.

Per-session ephemeral by default. Warm-volume mode (volume reattached across sessions for the same task) is opt-in for fast iteration cycles.

---

## 4. Tool surface

The brain holds two purely-local tools; everything else is one MCP round-trip into the sandbox.

| Tool | Location | Notes |
|---|---|---|
| `TodoWrite` | brain | Pure prompt-state. Lives in PromptKit session state. |
| `SubagentDispatch` | brain | PromptKit's A2A. Spawns a child session with its own sandbox. |
| `Read` | sandbox | Line-numbered output. Path containment enforced at MCP entry. |
| `Edit` | sandbox | Requires prior Read in same session. Returns post-edit lint feedback inline (single biggest quality win). |
| `Write` | sandbox | If file exists, requires prior Read. |
| `Glob` | sandbox | mtime-sorted. Respects `.gitignore`. |
| `Grep` | sandbox | ripgrep-backed. Respects `.gitignore`. |
| `Bash` | sandbox | Foreground + background. Background jobs live as long as the container. |
| `run_tests` | sandbox | Project-type-detected. Returns structured output. |
| `run_lint` | sandbox | Same. Used internally by `Edit` for the post-edit feedback contract. |
| `run_typecheck` | sandbox | Same. |
| `WebFetch` | sandbox | URL filter rejects RFC1918, link-local, cloud-metadata endpoints at MCP entry. |
| `WebSearch` | sandbox | Backend-pluggable (Brave / Exa / Tavily). |

The sandbox MCP server owns:
- The trust boundary (path containment, command denylist, URL filter, secret scrub at MCP entry/exit).
- Structured-output formatting (file:line:rule:message for lint, structured test results).
- Project-type detection (`go.mod` → Go, `package.json` → Node, etc.) to drive verification tools.
- The post-edit feedback contract: `Edit` calls the same lint runner that `run_lint` exposes and returns errors inline.

It is the same MCP server across every methodology variant in the eval matrix — that is what makes those comparisons honest.

---

## 5. Sandbox provider abstraction

"Give me a sandbox" is a pluggable interface. The agent doesn't know or care where the container runs.

- `LocalDockerProvider` — talks to the local Docker daemon. Default for development.
- `RemoteDockerProvider` — talks to a remote Docker daemon over TCP+TLS. The "remote sandbox" case.
- Third-party adapters (e2b, Modal, Daytona, etc.) — same interface, their backend.

Each returns a `Sandbox` handle with the MCP endpoint URL, lifecycle methods (Close), and inputs (workspace volume, repo URL/branch, scrubbed env).

This abstraction lives in the PromptKit repo (the consumer side), not in this repo. This repo's deliverable is a Docker image + MCP server only.

MCP transport: HTTP+SSE for genuinely remote sandboxes; stdio over `docker exec` is a viable shortcut for local. Pick one to start (probably HTTP+SSE since that's the only one that works for both).

---

## 6. Workspace and git

If the agent is working on a real repo:
- Repo URL + branch passed to the provider at session open.
- Provider clones into the workspace volume on container start.
- Short-lived git credentials passed in via env or mounted file; scrubbed from tool output.
- Pushing / opening a PR is the agent's job (via `gh` CLI or `git push`); the credentials it has scope what it can do.

Bring-your-own credentials. We don't ship a GitHub App or any identity story — that's the user's concern.

---

## 7. Sandboxing layers

| Layer | Mechanism |
|---|---|
| MCP middleware | path containment, command denylist, URL filter, secret scrub at entry/exit |
| Container | the actual trust boundary — no host filesystem access, network access only as configured |

Tighter sandboxing (gVisor, Firecracker, microVMs) is the provider's concern — `LocalDockerProvider` gets you a container, third-party providers may give you more. We don't try to layer microVMs on top of Docker ourselves.

---

## 8. Open questions

- **MCP transport choice.** HTTP+SSE everywhere, or stdio-over-`docker-exec` for local + HTTP+SSE for remote? Two transports = more code; one transport = a small efficiency loss on local.
- **Provider for v1.** `LocalDockerProvider` is obvious for dev. Which remote provider ships first — our own `RemoteDockerProvider`, or an adapter for an existing service (e2b / Modal / Daytona)?
- **Warm-volume mechanism.** Volume name reuse vs container reuse vs neither. Trade storage cost against cold-start latency once we have measurements.
- **Repo hydration model.** Clone-on-start (simple, slow on large repos) vs prebuilt workspace images (faster, more machinery) vs git's partial clone / sparse checkout (best for monorepos). Probably start with clone-on-start; revisit if it hurts.
- **`SubagentDispatch` and sandboxes.** A subagent gets its own session, which means its own sandbox. Fine for isolation, expensive in startup cost for short-lived subagents. Consider whether subagents can share the parent's sandbox in some constrained mode.
