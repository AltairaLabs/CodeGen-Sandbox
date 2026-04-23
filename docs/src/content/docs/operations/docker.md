---
title: Docker deployment
description: Building, running, and hardening the sandbox Docker image.
---

## Image layout

The Dockerfile is multi-stage:

**Builder (`golang:1.25-alpine`)**
- Downloads modules.
- Compiles `cmd/sandbox` with `CGO_ENABLED=0`, `-trimpath`, `-ldflags='-s -w'` for a static, stripped binary.

**Runtime (`alpine:3.20.3`)**
- Installs `bash`, `ripgrep`, `git`, `make`, `ca-certificates` via `apk`.
- Installs `golangci-lint v2.6.0` via the upstream installer script.
- Copies the full Go 1.25 toolchain from the builder (agents inside the workspace need `go` to compile and test code).
- Copies the sandbox binary to `/usr/local/bin/sandbox`.
- Creates a non-root `sandbox` user; `chown`s `/workspace` to it.
- `EXPOSE 8080`, `ENTRYPOINT ["/usr/local/bin/sandbox"]`, default CMD `-addr=:8080 -workspace=/workspace`.

Final image size: ~410 MB.

## Build

```bash
make docker-build      # → codegen-sandbox:dev
# or
docker build -t codegen-sandbox:dev .
```

## Run

```bash
make docker-run
# or
docker run --rm -it \
  -p 8080:8080 \
  -v /tmp/codegen-sandbox-workspace:/workspace \
  codegen-sandbox:dev
```

## Graceful shutdown

The binary traps `SIGINT` and `SIGTERM`. On signal, it stops accepting new HTTP connections and calls `http.Server.Shutdown` with a 10-second grace window to drain inflight requests. `docker stop -t 15` (which sends SIGTERM, waits up to 15s, then SIGKILL) gives the sandbox time to exit cleanly.

SSE streams don't receive an explicit close; clients should expect the connection to drop. Any background Bash shells are orphaned — their process groups die when the container's PID 1 exits.

## Hardening

Recommended `docker run` flags for production:

```bash
docker run --rm \
  -p 8080:8080 \
  -v /host/workspace:/workspace \
  --read-only \
  --tmpfs /tmp \
  --tmpfs /home/sandbox/.cache/go-build \
  --cap-drop ALL \
  --security-opt=no-new-privileges \
  --memory=2g \
  --cpus=2.0 \
  --network=<restricted> \
  codegen-sandbox:dev
```

- `--read-only` + `--tmpfs /tmp` + `--tmpfs /home/sandbox/.cache/go-build` — the sandbox needs a writable `/tmp`, a Go build cache (inside the user's home), and the workspace volume. Everything else can be read-only.
- `--cap-drop ALL --security-opt=no-new-privileges` — the sandbox doesn't need any Linux capabilities and shouldn't inherit any from a host-level compromise.
- `--memory` / `--cpus` — prevent resource exhaustion (e.g. an agent running a fork-bomb).
- `--network` — the biggest lever. Options:
  - `--network=none` — no egress at all. Breaks `go mod download`, WebFetch, WebSearch.
  - A bridge to a filtering proxy that enforces allowlists. Closes the DNS-rebinding gap the [URL filter](/concepts/url-filter/) can't fully cover.
  - A `docker network` with no route to RFC1918 ranges.

## Dev smoke test

```bash
docker run --rm -d --name sandbox-test \
  -p 18086:8080 \
  -v /tmp/sandbox-test:/workspace \
  codegen-sandbox:dev

# SSE handshake
curl -sS -N --max-time 2 http://127.0.0.1:18086/sse | head -n 2
# event: endpoint
# data: /message?sessionId=...

docker stop -t 12 sandbox-test
# Should exit cleanly within the grace window.
```

## Multi-arch

v1 builds `linux/amd64` only. For multi-arch (arm64), use `docker buildx`:

```bash
docker buildx build --platform=linux/amd64,linux/arm64 -t codegen-sandbox:dev --load .
```

`--load` only works for single-platform builds; use `--push` to push a multi-arch manifest to a registry.

## Alpine version pinning

The base is pinned to `alpine:3.20.3` for reproducibility. Package versions within that base (`bash`, `ripgrep`, `git`) are NOT individually pinned — that's high-maintenance for marginal benefit. When Alpine bumps a package, rebuilding the image picks up the update. If strict reproducibility is required, bump the base tag deliberately.

## CI / Publishing

v1 has no CI. When shipping to a registry:

```bash
docker tag codegen-sandbox:dev ghcr.io/yourorg/codegen-sandbox:<tag>
docker push ghcr.io/yourorg/codegen-sandbox:<tag>
```

Consider tagging with the git SHA and a rolling `:latest` for a specific release train.
