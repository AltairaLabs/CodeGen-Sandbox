---
title: Trust boundary
description: Where the real trust boundary lives and what the in-process defences are for.
---

## The real boundary

**The Docker container is the trust boundary.** Everything inside the sandbox — the MCP server, the agent-invoked tools, the filesystem the agent edits, any subprocesses Bash spawns — is inside one trust domain. The host OS, other containers, and the PromptKit agent's runtime are outside.

The container:
- Has no host filesystem access beyond the mounted workspace volume.
- Runs as a non-root user (`sandbox:sandbox`).
- Can have egress restricted by network policy (operator's choice — the sandbox binary itself doesn't enforce this).
- Is disposable: destroyed at session end, along with its ephemeral workspace volume.

## In-process defences

The sandbox MCP server adds **defense-in-depth** layers. None is a security guarantee on its own — each is a guard against routine accidents and obvious attempts.

### Path containment

Every path argument goes through `workspace.Resolve`:

1. Canonicalise the workspace root with `EvalSymlinks`.
2. Join relative paths with root.
3. `filepath.Clean` the result.
4. Walk up until an existing ancestor is found; resolve symlinks on it; re-attach missing suffix. This handles "I want to write a new file" without bypassing symlink checks.
5. Compute `filepath.Rel(root, resolved)`; reject if it equals `..` or starts with `../`.

Blocked cases (with tests):
- `../etc/passwd` (traversal)
- `/etc/passwd` (absolute outside root)
- `workspace/symlink-to-outside/foo` (symlink escape — caught at step 4)

### Read-tracker gate

`Edit` always requires a prior `Read` on the edited file. `Write` requires a prior `Read` when overwriting an existing file. This stops an agent from blindly mutating a file it has never observed — a pre-write `Read` forces the agent to actually look at what's there.

### Command denylist

[Bash](/tools/bash/) rejects obvious footgun tokens at plausible command positions. See [the Bash page](/tools/bash/#denylist) for the full list.

### Secret scrubbing

[Scrubbing middleware](/concepts/secret-scrubbing/) redacts well-known secret shapes from every tool's text output.

### URL filter

[WebFetch](/tools/web-fetch/) rejects private / loopback / metadata IPs and hostnames. DNS is resolved at check-time and every resolved IP is checked.

## What the sandbox does NOT defend against

- **Determined adversaries.** A compromised agent that knows exactly how the denylist regex works can trivially bypass it with `$(echo su)do`. The defence-in-depth layers raise the accidental-leak bar; container isolation raises the intentional-compromise bar.
- **Host-level threats.** If the Docker daemon is compromised, the container doesn't help. Operators who need stronger isolation should use gVisor, Firecracker, or a third-party provider (e2b, Modal, Daytona).
- **Secrets in the workspace.** If the operator mounts a volume containing `.env` with real API keys, the agent will see them (and the scrubber will redact them in TOOL OUTPUT, but not in the source file the agent reads). Operators should curate what gets mounted.

## Threat model summary

| Attacker | Defended by |
|---|---|
| Accidental path escape | `workspace.Resolve` |
| Stale overwrite | Read-tracker gate on Edit/Write |
| Accidental secret leak in tool output | Scrub middleware |
| Accidental private-net request | URL filter |
| Accidental `sudo` / `mkfs` | Bash denylist |
| Prompt-injected malicious commands | Container isolation |
| Determined container escape | gVisor / Firecracker / microVM (provider's choice) |
| Host compromise | Out of scope |
