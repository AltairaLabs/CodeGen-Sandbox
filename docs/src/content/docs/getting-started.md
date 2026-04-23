---
title: Getting Started
description: Build, run, and talk to the codegen sandbox locally.
---

This guide gets you from a fresh clone to an agent-reachable sandbox in ~5 minutes.

## Prerequisites

- **Go** 1.25+ (for local builds; the Docker image already has it)
- **Docker** (to run the image)
- **`ripgrep`**, **`golangci-lint` v2**, **`bash`** on `PATH` if you want to run the test suite locally (the Docker image pins all of these)
- A POSIX-like OS (Linux or macOS). Windows is not a supported target — the sandbox relies on `Setpgid` for process-group kills.

## Clone and build (local)

```bash
git clone https://github.com/AltairaLabs/codegen-sandbox.git
cd codegen-sandbox
make build
./bin/sandbox -addr=:8080 -workspace=/tmp/my-workspace
```

You should see:

```
codegen-sandbox listening on :8080 (workspace=/tmp/my-workspace)
```

## Run in Docker (recommended)

```bash
make docker-build
make docker-run
```

`docker-run` mounts `/tmp/codegen-sandbox-workspace` as the agent's workspace and listens on `127.0.0.1:8080`.

## Talk to the sandbox

The sandbox is an MCP server over HTTP+SSE. Your agent (PromptKit, or any MCP client) opens a streaming SSE connection to `/sse`, which returns a per-session `/message?sessionId=…` endpoint. JSON-RPC requests go to that endpoint; replies stream back down the SSE channel.

Smoke test with `curl`:

```bash
# 1. Open the SSE stream in the background so we can capture the session URL.
curl -sS -N --max-time 4 --output /tmp/sse.txt http://127.0.0.1:8080/sse &
sleep 0.3
SESSION=$(grep -o 'data:.*' /tmp/sse.txt | head -1 | sed 's|data: *||' | tr -d '\r\n ')

# 2. Initialize.
curl -sS -X POST "http://127.0.0.1:8080${SESSION}" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"curl","version":"0"},"capabilities":{}}}'

# 3. List tools.
curl -sS -X POST "http://127.0.0.1:8080${SESSION}" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

You'll get back 11 tool names: `Read`, `Write`, `Edit`, `Glob`, `Grep`, `Bash`, `BashOutput`, `KillShell`, `run_tests`, `run_lint`, `run_typecheck`. Web-search / fetch tools come from vendor MCP servers wired alongside — see [Non-sandbox tools](/concepts/non-sandbox-tools/).

## Next

- [Architecture](/architecture/) — the brain/hands split, transport, and layered defence.
- [Tools](/tools/read/) — per-tool reference starting with Read.
- [Docker deployment](/operations/docker/) — ops-level details.
