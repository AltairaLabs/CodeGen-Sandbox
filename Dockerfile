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

ARG GOLANGCI_LINT_VERSION=v2.6.0

# Shared utilities the sandbox tools depend on + Go-specific linter.
RUN apk add --no-cache \
      bash \
      git \
      make \
      ca-certificates \
      curl \
    && curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
        | sh -s -- -b /usr/local/bin "${GOLANGCI_LINT_VERSION}" \
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
