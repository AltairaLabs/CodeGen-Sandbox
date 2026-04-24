---
title: Feature tools layers
description: Per-feature scratch images carrying binaries that particular sandbox features need; operators COPY the ones they want into their base image.
---

Feature tools layers are per-feature artifact images carrying the language-specific binaries that individual sandbox features need (LSP servers, linters, formatters, render tools). Operators `COPY --from=` the ones they want into their own base image — each layer is small (tens of MB) so composing several is cheap.

This is the companion to the core [Docker deployment](/operations/docker/) page: that page covers the sandbox tools layer (`codegen-sandbox-tools`, with `/sandbox` + `/rg`); this page covers the optional feature layers. See the [image composition model](/concepts/language-support/#image-composition-model) in language-support for how the pieces fit together.

All feature layer images are published on every `v*` tag to:

- `ghcr.io/altairalabs/codegen-sandbox-tools-<layer>:<tag>`
- `ghcr.io/altairalabs/codegen-sandbox-tools-<layer>:latest`

Multi-arch: `linux/amd64` + `linux/arm64`.

## `codegen-sandbox-tools-go`

**Image**: `ghcr.io/altairalabs/codegen-sandbox-tools-go`

**Size**: ~85 MB (both binaries are statically linked; compressed manifest is smaller)

**Binaries carried**:

| Path | Purpose | Version |
|---|---|---|
| `/gopls` | Go language server — powers LSP navigation ([#9](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/9)) | `v0.21.0` |
| `/golangci-lint` | Go linter — powers `run_lint` + post-edit feedback | `v2.6.0` |

Both binaries are built / fetched as fully static (scratch-safe) artifacts: `gopls` is compiled with `CGO_ENABLED=0 go install`; `golangci-lint` is the official static tarball from the project's GitHub releases. They run on any glibc or musl base.

### Why `go install` for `gopls`

Upstream does not publish prebuilt release tarballs for `gopls` on GitHub. The distribution channel is the Go module proxy (`proxy.golang.org`), and the only reliable way to pin a version of `gopls` to a reproducible binary is `go install golang.org/x/tools/gopls@<ver>` in a builder stage. The resulting binary is copied into the final `scratch` stage exactly like a prebuilt would be.

### Operator composition

```dockerfile
FROM golang:1.25-alpine

# Core sandbox tools.
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools:latest      /sandbox        /usr/local/bin/sandbox
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools:latest      /rg             /usr/local/bin/rg

# Go feature layer.
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools-go:latest   /gopls          /usr/local/bin/gopls
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools-go:latest   /golangci-lint  /usr/local/bin/golangci-lint

WORKDIR /workspace
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/sandbox"]
CMD ["-addr=:8080", "-workspace=/workspace"]
```

You do **not** need both binaries. Copy only the ones the features you enable will use:

- Running `run_lint` against Go projects? `/golangci-lint` is enough.
- Enabling LSP navigation? `/gopls` is enough.
- Want the full Go developer experience? Copy both.

### Verifying locally

```bash
docker buildx build -f Dockerfile.tools-go --load -t codegen-sandbox-tools-go:test .

docker create --name probe --entrypoint /gopls codegen-sandbox-tools-go:test
docker cp probe:/gopls /tmp/gopls && docker cp probe:/golangci-lint /tmp/glci
docker rm probe

# These run inside a linux container of the matching arch (or via docker run).
docker run --rm --entrypoint /gopls         codegen-sandbox-tools-go:test version
docker run --rm --entrypoint /golangci-lint codegen-sandbox-tools-go:test --version
```

The `--entrypoint` override is required because the final image targets `scratch` with no default CMD / ENTRYPOINT (it's an artifact source, not a runtime image).

## `codegen-sandbox-tools-node`

**Image**: `ghcr.io/altairalabs/codegen-sandbox-tools-node`

**Size**: ~60 MB (both binaries are statically linked native executables)

**Binaries carried**:

| Path | Purpose | Version |
|---|---|---|
| `/pnpm` | pnpm package manager — driven by Node package-manager detection ([#25](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/25)) | `v9.15.0` |
| `/bun` | bun runtime + package manager — same | `1.1.38` |

Both are native per-arch binaries shipped by their upstream projects (pnpm as `pnpm-linux-<arch>`; bun as a per-arch zip).

**Glibc required on the consumer base image.** Upstream pnpm and bun release artifacts are dynamically linked against glibc — they will fail with "not found" on an alpine / musl base. Compose this layer onto a glibc base such as `node:22-slim`, `node:22` (Debian), `debian:bookworm-slim`, or `ubuntu:24.04`.

### Not included (and why)

- **yarn** — yarn classic (v1) is a Node.js script, and yarn berry (v2+) ships per-project under `.yarn/releases/yarn-*.cjs`. Neither fits the scratch-image "one static binary" contract. Operators who need yarn should enable [corepack](https://nodejs.org/api/corepack.html) in their own base image — it ships with Node.js 16+ and transparently dispatches to `pnpm` / `yarn` / `npm`.
- **`typescript-language-server`** and **`prettier`** — both are pure Node.js packages that need a runtime at execute time. Single-file bundling via `pkg` / `@vercel/ncc` / `bun build --compile` is a separate lift and is deferred to a follow-up image bump. Until they land, install them at build time with `npm i -g typescript typescript-language-server prettier` on top of a Node base image.

### Operator composition

```dockerfile
# node:22-slim is glibc (Debian). Do NOT use node:22-alpine — pnpm and bun
# are dynamically linked against glibc and will fail on musl.
FROM node:22-slim

# Core sandbox tools.
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools:latest       /sandbox  /usr/local/bin/sandbox
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools:latest       /rg       /usr/local/bin/rg

# Node feature layer.
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools-node:latest  /pnpm     /usr/local/bin/pnpm
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools-node:latest  /bun      /usr/local/bin/bun

WORKDIR /workspace
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/sandbox"]
CMD ["-addr=:8080", "-workspace=/workspace"]
```

Copy only what the features you enable will use — if your agent only ever calls `pnpm install`, `/bun` is dead weight.

### Verifying locally

```bash
docker buildx build -f Dockerfile.tools-node --load -t codegen-sandbox-tools-node:test .

docker create --name probe --entrypoint /pnpm codegen-sandbox-tools-node:test
docker cp probe:/pnpm /tmp/pnpm && docker cp probe:/bun /tmp/bun
docker rm probe

docker run --rm --entrypoint /pnpm codegen-sandbox-tools-node:test --version
docker run --rm --entrypoint /bun  codegen-sandbox-tools-node:test --version
```

The `--entrypoint` override is required because the final image targets `scratch` with no default CMD / ENTRYPOINT.

## `codegen-sandbox-tools-python`

**Image**: `ghcr.io/altairalabs/codegen-sandbox-tools-python`

**Size**: ~30 MB (one native binary)

**Binaries carried**:

| Path | Purpose | Version |
|---|---|---|
| `/ruff` | Python linter + formatter — powers `run_lint` + post-edit feedback for Python projects | `0.8.4` |

`ruff` is the official per-arch tarball from the [astral-sh/ruff](https://github.com/astral-sh/ruff/releases) GitHub releases (the `-gnu` variant).

**Glibc required on the consumer base image.** The upstream `-gnu` tarball is dynamically linked against glibc (libc, libgcc_s, libpthread, etc.) — it will fail with "not found" on an alpine / musl base. Compose this layer onto a glibc base such as `python:3.12-slim`, `debian:bookworm-slim`, or `ubuntu:24.04`. (A `-musl` tarball is also published upstream; swap the URL in `Dockerfile.tools-python` if you need a musl build.)

### Not included (and why)

- **`pyright-langserver`** — published as an npm module and requires a Node.js runtime at execute time. Single-file bundling via `pkg` / `@vercel/ncc` / `bun build --compile` is a separate lift and is deferred to a follow-up image bump (same deferral as `typescript-language-server` and `prettier` on the Node layer). Until it lands, operators who want Python LSP can install it at build time with `npm i -g pyright` on top of a Node-capable base image.

### Operator composition

```dockerfile
# python:3.12-slim is glibc (Debian). Do NOT use `*-alpine` — the upstream
# ruff -gnu tarball is dynamically linked against glibc and will fail on musl.
FROM python:3.12-slim

# Core sandbox tools.
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools:latest         /sandbox  /usr/local/bin/sandbox
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools:latest         /rg       /usr/local/bin/rg

# Python feature layer.
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools-python:latest  /ruff     /usr/local/bin/ruff

WORKDIR /workspace
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/sandbox"]
CMD ["-addr=:8080", "-workspace=/workspace"]
```

### Verifying locally

```bash
docker buildx build -f Dockerfile.tools-python --load -t codegen-sandbox-tools-python:test .

docker create --name probe --entrypoint /ruff codegen-sandbox-tools-python:test
docker cp probe:/ruff /tmp/ruff
docker rm probe

docker run --rm --entrypoint /ruff codegen-sandbox-tools-python:test --version
```

The `--entrypoint` override is required because the final image targets `scratch` with no default CMD / ENTRYPOINT.

## `codegen-sandbox-tools-rust`

**Image**: `ghcr.io/altairalabs/codegen-sandbox-tools-rust`

**Size**: ~46 MB (one native binary)

**Binaries carried**:

| Path | Purpose | Version |
|---|---|---|
| `/rust-analyzer` | Rust language server — powers LSP navigation ([#9](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/9)) for Rust projects | `2025-01-27` |

`rust-analyzer` is the official per-arch gzipped binary from the [rust-lang/rust-analyzer](https://github.com/rust-lang/rust-analyzer/releases) GitHub releases (the `-unknown-linux-gnu` variant).

**Glibc required on the consumer base image.** The upstream `-gnu` binary is dynamically linked against glibc (libc, libgcc_s, libpthread, etc.) — it will fail with "not found" on an alpine / musl base. Compose this layer onto a glibc base such as `rust:1-slim-bookworm`, `debian:bookworm-slim`, or `ubuntu:24.04`.

### Not included (and why)

- **`rustfmt` / `clippy` / `cargo`** — these ship with the Rust toolchain itself via `rustup component add rustfmt clippy` (or are already present by default on `rust:<ver>` base images). Re-shipping them from this layer would duplicate binaries the operator already has, so they intentionally stay on the base image.

### Operator composition

```dockerfile
# rust:1-slim-bookworm is glibc (Debian) and ships rustfmt / clippy / cargo
# out of the box. Do NOT use *-alpine — the upstream rust-analyzer -gnu
# binary is dynamically linked against glibc and will fail on musl.
FROM rust:1-slim-bookworm

# Core sandbox tools.
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools:latest       /sandbox        /usr/local/bin/sandbox
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools:latest       /rg             /usr/local/bin/rg

# Rust feature layer.
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools-rust:latest  /rust-analyzer  /usr/local/bin/rust-analyzer

WORKDIR /workspace
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/sandbox"]
CMD ["-addr=:8080", "-workspace=/workspace"]
```

### Verifying locally

```bash
docker buildx build -f Dockerfile.tools-rust --load -t codegen-sandbox-tools-rust:test .

docker create --name probe --entrypoint /rust-analyzer codegen-sandbox-tools-rust:test
docker cp probe:/rust-analyzer /tmp/rust-analyzer
docker rm probe

docker run --rm --entrypoint /rust-analyzer codegen-sandbox-tools-rust:test --version
```

The `--entrypoint` override is required because the final image targets `scratch` with no default CMD / ENTRYPOINT.

## `codegen-sandbox-tools-render`

**Image**: `ghcr.io/altairalabs/codegen-sandbox-tools-render`

**Size**: ~700 MB (chromium dominates; graphviz + mmdc + Node together add ~80 MB)

**Binaries carried** (on `node:22-bookworm-slim`, **not** scratch):

| Path | Purpose | Version |
|---|---|---|
| `/usr/bin/dot` | Graphviz layout engine — drives the render_dot tool ([#22](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/22)) | Debian bookworm `graphviz` package |
| `/usr/local/bin/mmdc` | Wrapper around the npm-installed `mmdc` that injects `-p /opt/render/puppeteer-config.json` so Chromium runs with `--no-sandbox` under root | `11.4.2` |
| `/usr/local/bin/mmdc-direct` | The unwrapped npm-installed `mmdc`, for non-root invocations that should keep Chromium's default sandbox enabled | `11.4.2` |
| `/usr/bin/chromium` | Headless browser mmdc drives via Puppeteer | Debian bookworm `chromium` package |
| `/opt/render/puppeteer-config.json` | Pre-baked `--no-sandbox` puppeteer config consumed by the `/usr/local/bin/mmdc` wrapper | n/a |

### Why this layer breaks the scratch + `COPY --from` pattern

The other feature tools layers ship one or two static binaries on `scratch` so operators can `COPY --from=...` exactly the bits they want. The render layer can't fit that contract:

- **`mmdc` is a Node.js script** that drives a real Chromium binary via Puppeteer to rasterise SVG. There is no single-binary distribution; bundling mermaid-cli + Node + Puppeteer + Chromium into a self-contained executable is a large lift that adds little value over shipping the runtime directly.
- **`dot` from graphviz is dynamically linked** against ~10 `.so` files (`libgvc`, `libcgraph`, `libcdt`, `libxdot`, `libpathplan`, `libgvpr`, `libgd`, `libfontconfig`, `libexpat`, ...). Transplanting it into a scratch image would require dragging the whole library closure with it.

So this layer is a **runnable image**, not an artifact image. Operators consume it in one of two shapes.

### Operator shape 1 — sibling render container (recommended)

Run the render image alongside the sandbox image, share the workspace via a volume, and have the agent shell out to `mmdc` / `dot` over `docker exec`. Keeps the agent's runtime image small and isolates Chromium from the agent process.

```yaml
# docker-compose.yml
services:
  sandbox:
    image: ghcr.io/altairalabs/codegen-sandbox:latest
    ports: ["8080:8080"]
    volumes:
      - workspace:/workspace
  render:
    image: ghcr.io/altairalabs/codegen-sandbox-tools-render:latest
    # No ports — invoked by the sandbox via docker exec, not over HTTP.
    volumes:
      - workspace:/workspace
    # Keep the container alive without occupying the entrypoint;
    # docker exec is the only thing that runs commands inside it.
    command: ["sleep", "infinity"]
volumes:
  workspace:
```

```bash
# From the sandbox container, render via the sibling render container.
docker exec render mmdc -i /workspace/diagram.mmd -o /workspace/diagram.svg
docker exec render dot  -Tsvg /workspace/graph.dot   -o /workspace/graph.svg
```

### Operator shape 2 — adopt as base

Use the render image as the `FROM` and copy the sandbox binary onto it. Single image, ~700 MB, no orchestration. Good for environments where running multiple containers is awkward.

```dockerfile
FROM ghcr.io/altairalabs/codegen-sandbox-tools-render:latest

COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools:latest /sandbox /usr/local/bin/sandbox
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools:latest /rg      /usr/local/bin/rg

WORKDIR /workspace
EXPOSE 8080
# Override the render image's dumb-init entrypoint with the sandbox.
ENTRYPOINT ["/usr/local/bin/sandbox"]
CMD ["-addr=:8080", "-workspace=/workspace"]
```

### `--no-sandbox` is pre-configured

Chromium refuses to run as root without `--no-sandbox`, and containers run everything as root by default. The image solves this by:

1. Shipping `/opt/render/puppeteer-config.json` with `--no-sandbox` + `--disable-setuid-sandbox` + `--disable-dev-shm-usage`.
2. Replacing the npm-installed `/usr/local/bin/mmdc` shim with a small shell wrapper that always passes `-p /opt/render/puppeteer-config.json` to the real binary.

So `mmdc -i diagram.mmd -o diagram.svg` "just works" in this image — no extra flags, no env vars to set. (Upstream Puppeteer / mermaid-cli have no `PUPPETEER_CONFIG` env var — the wrapper is the only way to bake this in.)

If you adopt this image as a base and switch to a non-root user, drop the wrapper by calling `/usr/local/bin/mmdc-direct` directly, which lets Chromium use its default sandbox.

### Verifying locally

```bash
docker buildx build -f Dockerfile.tools-render --load -t codegen-sandbox-tools-render:test .

# Probe binaries without rendering anything.
docker run --rm --entrypoint dot  codegen-sandbox-tools-render:test -V
docker run --rm --entrypoint mmdc codegen-sandbox-tools-render:test --version

# Real round-trip: render a tiny mermaid + dot graph to SVG.
mkdir -p /tmp/render
cat > /tmp/render/diagram.mmd <<'MMD'
graph LR
  A[client] --> B[sandbox] --> C[(workspace)]
MMD
cat > /tmp/render/graph.dot <<'DOT'
digraph G { rankdir=LR; client -> sandbox -> workspace; }
DOT
docker run --rm -v /tmp/render:/work --entrypoint mmdc \
  codegen-sandbox-tools-render:test -i /work/diagram.mmd -o /work/diagram.svg
docker run --rm -v /tmp/render:/work --entrypoint dot \
  codegen-sandbox-tools-render:test -Tsvg /work/graph.dot -o /work/graph.svg
ls -lh /tmp/render/*.svg
```

### Not included (and why)

- **PlantUML / d2 / structurizr-cli / asciidoctor** — orthogonal diagram dialects with their own runtime requirements (JVM, Go binary, Ruby). Keeping this layer scoped to the two formats motivated by [#22](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/22) avoids a runtime-zoo image. Operators who want one of these can fork the Dockerfile and add it.

## Composing several layers

Layers are independent artifacts; operators freely mix them:

```dockerfile
# Use a glibc Node base — pnpm and bun are dynamically linked against glibc.
FROM node:22-slim

COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools:latest      /sandbox /usr/local/bin/
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools:latest      /rg      /usr/local/bin/

# Hybrid project — Node app that also calls into a Go helper.
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools-node:latest /pnpm    /usr/local/bin/
COPY --from=ghcr.io/altairalabs/codegen-sandbox-tools-go:latest   /gopls   /usr/local/bin/
```

The [image composition model](/concepts/language-support/#image-composition-model) describes the contract in full.
