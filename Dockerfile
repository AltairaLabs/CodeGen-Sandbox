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

# -------- golangci-lint, built from source --------
# Its release binaries are built with whatever Go the release used (v2.14.0:
# Go 1.27.0), so they carry that toolchain's stdlib CVEs until the next
# release. Building it here with the base's own Go gives it the same patched
# stdlib as everything else in the image. Cross-compiled on the build
# platform, so a multi-arch build never compiles it under emulation.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS golangci-lint
ARG GOLANGCI_LINT_VERSION=v2.14.0
ARG TARGETOS
ARG TARGETARCH
# A cross-compiled `go install` lands in $GOPATH/bin/<os>_<arch>/, a native
# one in $GOPATH/bin/; take whichever was written.
RUN CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
    go install -trimpath -ldflags='-s -w' \
    "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}" \
    && mkdir -p /out \
    && B="$(go env GOPATH)/bin" \
    && cp "$(ls "$B/${TARGETOS:-linux}_${TARGETARCH:-amd64}/golangci-lint" "$B/golangci-lint" 2>/dev/null | head -1)" /out/golangci-lint

# -------- Runtime: Go base + sandbox tools + golangci-lint --------
#
# The `COPY --from=codegen-sandbox-tools:dev` lines below reference the
# tools artifact image. Locally, `make docker-build` builds Dockerfile.tools
# first and tags it `codegen-sandbox-tools:dev`. In CI's release workflow,
# buildx's --build-context remaps that name to the freshly-published
# `ghcr.io/altairalabs/codegen-sandbox-tools:<tag>` multi-arch manifest.
#
# Go 1.27: the 1.25 line has no release fixing stdlib CVE-2026-78667 and
# CVE-2026-97031, which consumers' publish gates refuse in the toolchain.
FROM golang:1.27-alpine

# Shared utilities the sandbox tools depend on.
# `apk upgrade` first: the golang base tag trails Alpine's security fixes
# (libcurl, libexpat, openssl), and this image is scanned by consumers'
# publish gates, so it ships with every package at its fixed version.
RUN apk upgrade --no-cache \
    && apk add --no-cache \
      bash \
      git \
      make \
      ca-certificates

COPY --from=golangci-lint /out/golangci-lint /usr/local/bin/golangci-lint
RUN golangci-lint version

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
