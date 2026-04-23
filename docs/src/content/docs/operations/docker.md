---
title: Docker deployment
description: Composing the sandbox tools into your own base image.
---

The sandbox ships as a **tools layer** that you compose into a base image carrying your language toolchain, the same pattern modern CI systems use (GitHub Actions runners, GitLab runner, CircleCI orbs).

## The pattern

```
altairalabs/codegen-sandbox-tools:vX.Y.Z
├── /sandbox    — the MCP server binary (static, ~13 MB)
└── /rg         — ripgrep (static binary, used by Glob/Grep)
```

Your Dockerfile picks a base image with your language toolchain and COPYs the tools layer on top:

```dockerfile
FROM python:3.12-slim

# Operator-chosen language toolchain. The sandbox's Python detector looks
# for pytest / ruff / mypy on PATH.
RUN pip install --no-cache-dir pytest ruff mypy

# Sandbox tools layer.
COPY --from=altairalabs/codegen-sandbox-tools:latest /sandbox /usr/local/bin/sandbox
COPY --from=altairalabs/codegen-sandbox-tools:latest /rg /usr/local/bin/rg

WORKDIR /workspace
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/sandbox"]
CMD ["-addr=:8080", "-workspace=/workspace"]
```

See `examples/Dockerfile.python`, `examples/Dockerfile.node`, `examples/Dockerfile.rust` in the repo for ready-made templates.

## Why this pattern

- **Language support is a binary upgrade, not an image rebuild.** When the sandbox adds a new `Detector` in a release, every operator image picks it up on the next `COPY` with a newer tag.
- **Operators keep full control of their language toolchain.** Pick `python:3.11-slim` or `python:3.12-alpine` or a custom base with your org's pinned packages — the sandbox doesn't care.
- **Zero coupling between tools and image.** The sandbox binary is language-agnostic (`Detector` interface). If a tool isn't on PATH when a run_tests call happens, `verify.Lint` / `runVerifyCmd` returns a clear "binary not found" error — no sandbox code change needed.
- **Small attack surface.** Most images carry only one language's toolchain. A Python sandbox image doesn't need `go` or `rustc`.

## Go convenience image

The repo also ships a Dockerfile for the Go case (it's what's exercised by the test suite):

```bash
make docker-build    # builds codegen-sandbox:dev using the tools pattern
make docker-run      # mounts /tmp/codegen-sandbox-workspace + listens on 8080
```

Sizes:
- `codegen-sandbox-tools:dev` — ~19 MB (sandbox binary + static rg)
- `codegen-sandbox:dev` (Go) — ~420 MB (`golang:1.25-alpine` + golangci-lint + tools)
- Hypothetical `codegen-sandbox-python:dev` — expect ~250 MB (`python:3.12-slim` + pytest/ruff/mypy + tools)

## Graceful shutdown

The binary traps SIGINT and SIGTERM. On signal, it stops accepting new HTTP connections and calls `http.Server.Shutdown` with a 10-second grace window to drain inflight requests. `docker stop -t 15` (SIGTERM, wait 15s, then SIGKILL) gives the sandbox time to exit cleanly.

SSE streams don't receive an explicit close; clients should expect the connection to drop. Any background Bash shells are orphaned — their process groups die when the container's PID 1 exits.

## Production hardening

```bash
docker run --rm \
  -p 8080:8080 \
  -v /host/workspace:/workspace \
  --read-only \
  --tmpfs /tmp \
  --cap-drop ALL \
  --security-opt=no-new-privileges \
  --memory=2g \
  --cpus=2.0 \
  --network=<restricted> \
  my-org/codegen-sandbox-python:v1.0.0
```

- `--read-only` + `--tmpfs /tmp` — the sandbox writes to `/workspace`, `/tmp`, and language caches only. Make those the only writable paths.
- `--cap-drop ALL --security-opt=no-new-privileges` — the sandbox needs no Linux capabilities.
- `--memory` / `--cpus` — prevent resource exhaustion (e.g. an agent running a fork-bomb).
- `--network` — the biggest lever. `--network=none` breaks `pip install` and any outbound HTTP inside `Bash`. A bridge to a filtering proxy gives you allowlist-based egress, which is the right place to control outbound since this sandbox no longer ships its own URL filter — web tools are served by sibling MCP servers (see [Non-sandbox tools](/concepts/non-sandbox-tools/)).

## What the operator must provide

The sandbox expects, at runtime:

- A writable `/workspace` (or whatever `-workspace` resolves to).
- Port `8080` exposed (or whatever `-addr` points at).
- `bash` on PATH (used by the `Bash` tool).
- Language binaries that match the project's [Detector](/reference/detector-interface/). Missing binaries surface as clear `linter not installed: <binary>` errors; they never crash the sandbox.

That's it. No init system, no sidecars, no specific user (though non-root is recommended and the example Dockerfiles do it).

## Dev smoke test

```bash
# 1. Build both images locally.
make docker-build

# 2. Run the Go image.
docker run --rm -d --name sandbox-test \
  -p 18086:8080 \
  -v /tmp/sandbox-test:/workspace \
  codegen-sandbox:dev

# 3. SSE handshake.
curl -sS -N --max-time 2 http://127.0.0.1:18086/sse | head -n 2
# event: endpoint
# data: /message?sessionId=...

# 4. Graceful stop.
docker stop -t 12 sandbox-test
# Exits within the grace window.
```

## Multi-arch

The tools artifact supports `linux/amd64` and `linux/arm64` (ripgrep release has both). `docker buildx` for multi-arch builds:

```bash
docker buildx build --platform=linux/amd64,linux/arm64 \
  -f Dockerfile.tools -t codegen-sandbox-tools:dev --load .
```

`--load` only works for single-platform builds; use `--push` for a multi-arch manifest.

## Alpine version pinning

The convenience Dockerfiles pin the base tag explicitly (e.g. `golang:1.25-alpine`, `python:3.12-slim`). Package versions within those bases (`bash`, `git`, language stdlibs) are not individually pinned — the base tag IS the reproducibility anchor. Rebuilds pick up upstream updates.

## CI / Publishing

Once remote/CI is set up, publish:

```bash
# The tools artifact — the thing operators reference from their Dockerfiles.
docker buildx build --platform=linux/amd64,linux/arm64 \
  -f Dockerfile.tools \
  -t ghcr.io/altairalabs/codegen-sandbox-tools:v0.1.0 \
  -t ghcr.io/altairalabs/codegen-sandbox-tools:latest \
  --push .

# Convenience images referenced in the quickstart.
docker buildx build --platform=linux/amd64,linux/arm64 \
  -t ghcr.io/altairalabs/codegen-sandbox-go:v0.1.0 --push .

# Similar for python, node, rust.
```

Tag on git-tag events (v0.1.0 → pinned); tag `:latest` on pushes to main for rolling convenience.
