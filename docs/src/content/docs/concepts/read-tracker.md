---
title: Read tracker
description: Per-session record of which files have been Read — gates Edit and overwriting Write.
---

A `ReadTracker` is a concurrent-safe set of canonical absolute paths that have been `Read` during the current MCP session. It exists to prevent blind mutation: an agent must look at a file before it modifies it.

## The contract

- **`Read`** — on success, calls `MarkRead(absPath)`.
- **`Write`** (overwrite) — requires `HasBeenRead(absPath)`; otherwise errors with `refusing to overwrite %s: Read it first`.
- **`Write`** (new file) — no prior Read needed.
- **`Edit`** — ALWAYS requires `HasBeenRead(absPath)`; otherwise errors with `refusing to edit %s: Read it first`.

After a successful Write, the path is marked Read automatically — so an agent can `Write` then `Edit` without a manual re-Read.

## Why canonical paths

The tracker keys on the canonical resolved path (post-`Workspace.Resolve`). Symlinks resolve to their target, so `link.txt` and `real.txt` (where `link.txt → real.txt`) share a single tracker entry. An agent can't slip past the gate by reading through one name and writing through the other.

## Lifetime

One tracker per sandbox server instance (constructed in `server.New`). Since the sandbox is one container per agent session, the tracker's lifetime matches the session's lifetime. No cross-session persistence; no eviction; no TTL.

## Concurrency

`sync.RWMutex` protects the underlying map. `HasBeenRead` takes the read lock; `MarkRead` takes the write lock. Concurrent agent calls are safe.

## Interaction with `Bash`

Bash commands that modify files (`echo foo > bar.txt`, `git checkout`, `sed -i`) do NOT update the tracker. This is deliberate: if the agent wants to `Edit` bar.txt after a Bash modification, it should `Read` the new contents first anyway — otherwise the agent's mental model of the file is stale, and Edit may replace text that no longer exists.

## Not security

The tracker is an ergonomic / correctness feature, not a security one. An agent determined to overwrite without reading could delete the file and `Write` a new one (no Read required for new files). The check prevents common mistakes where an agent's mental model of a file is wrong.
