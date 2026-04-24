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

Coming soon — see [#26](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/26). Will carry `ruff` as a statically-linked binary on scratch. Python LSP is deferred; see the [language-support page](/concepts/language-support/#feature--runtime-binary-matrix).

## `codegen-sandbox-tools-rust`

Coming soon — see [#26](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/26). Will carry `rust-analyzer`. `rustfmt` and `clippy` ship with the Rust toolchain (`rustup component add`) and are expected on the operator's `rust:<ver>` base image, not on this layer.

## `codegen-sandbox-tools-render`

Coming soon — see [#26](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/26). Will carry `mmdc` (mermaid-cli) + `dot` (graphviz) for the [render tools](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/22). Because `mmdc` drags a Node + Chromium runtime closure, the expected shape is for operators to either run this image as a sibling render container or adopt it as their base.

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
