# syntax=docker/dockerfile:1.7
#
# codegen-sandbox:go — convenience image for Go projects.
#
# This image is a DEMONSTRATION of the tools-layer pattern: an operator
# picks a language base image (`golang:1.25-alpine` here) and COPYs the
# sandbox + ripgrep binaries from `codegen-sandbox-tools` alongside the
# language toolchain already present in the base.
#
# For non-Go projects, compose your own Dockerfile:
#
#   FROM python:3.11-slim
#   RUN pip install --no-cache-dir ruff mypy pytest
#   COPY --from=altairalabs/codegen-sandbox-tools:latest /sandbox /usr/local/bin/sandbox
#   COPY --from=altairalabs/codegen-sandbox-tools:latest /rg /usr/local/bin/rg
#   WORKDIR /workspace
#   ENTRYPOINT ["/usr/local/bin/sandbox"]
#   CMD ["-addr=:8080", "-workspace=/workspace"]
#
# See examples/ for ready-made Dockerfile.python / Dockerfile.node /
# Dockerfile.rust templates.

# -------- Runtime: Go base + sandbox tools + golangci-lint --------
#
# The `COPY --from=codegen-sandbox-tools:dev` lines below reference the
# tools artifact image. Locally, `make docker-build` builds Dockerfile.tools
# first and tags it `codegen-sandbox-tools:dev`. In CI's release workflow,
# buildx's --build-context remaps that name to the freshly-published
# `ghcr.io/altairalabs/codegen-sandbox-tools:<tag>` multi-arch manifest.
FROM golang:1.25-alpine

ARG GOLANGCI_LINT_VERSION=v2.14.0
ARG TARGETARCH

# Shared utilities the sandbox tools depend on + Go-specific linter.
# `apk upgrade` first: the golang base tag trails Alpine's security fixes
# (libcurl, libexpat, openssl), and this image is scanned by consumers'
# publish gates, so it ships with every package at its fixed version.
#
# golangci-lint is fetched from its release directly rather than via the
# upstream install.sh: from v2.14.0 the release also ships per-tarball
# `.sbom.json` files, and install.sh's checksum lookup matches the SBOM's
# line instead of the tarball's and fails. The tarball is verified against
# the release checksums file by exact filename.
RUN apk upgrade --no-cache \
    && apk add --no-cache \
      bash \
      git \
      make \
      ca-certificates \
      curl \
    && V="${GOLANGCI_LINT_VERSION#v}" \
    && T="golangci-lint-${V}-linux-${TARGETARCH:-amd64}.tar.gz" \
    && BASE="https://github.com/golangci/golangci-lint/releases/download/${GOLANGCI_LINT_VERSION}" \
    && cd /tmp \
    && curl -sSfLO "${BASE}/${T}" \
    && curl -sSfL "${BASE}/golangci-lint-${V}-checksums.txt" \
       | awk -v f="${T}" '$2 == f {print; n++} END {exit n != 1}' > "${T}.sha256" \
    && sha256sum -c "${T}.sha256" \
    && tar -xzf "${T}" \
    && install -m 0755 "golangci-lint-${V}-linux-${TARGETARCH:-amd64}/golangci-lint" /usr/local/bin/golangci-lint \
    && rm -rf /tmp/golangci-lint-* \
    && golangci-lint version \
    && apk del curl

# Sandbox layer — the artifact pattern. Operators replace this COPY with a
# published `altairalabs/codegen-sandbox-tools:vX.Y.Z` tag in their own
# Dockerfile.
COPY --from=codegen-sandbox-tools:dev /sandbox /usr/local/bin/sandbox
COPY --from=codegen-sandbox-tools:dev /rg /usr/local/bin/rg

# Unprivileged user owning the workspace mount.
RUN addgroup -S sandbox \
    && adduser -S -G sandbox -h /home/sandbox sandbox \
    && mkdir -p /workspace /home/sandbox/.cache/go-build /home/sandbox/go \
    && chown -R sandbox:sandbox /workspace /home/sandbox

USER sandbox
WORKDIR /workspace

ENV GOPATH=/home/sandbox/go \
    GOCACHE=/home/sandbox/.cache/go-build

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/sandbox"]
CMD ["-addr=:8080", "-workspace=/workspace"]
