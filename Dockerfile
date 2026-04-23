# syntax=docker/dockerfile:1.7

# -------- Builder --------
FROM golang:1.25-alpine AS builder

WORKDIR /src
RUN apk add --no-cache git

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags='-s -w' \
    -o /out/sandbox ./cmd/sandbox

# -------- Runtime --------
FROM alpine:3.20.3

ARG GOLANGCI_LINT_VERSION=v2.6.0

# Base toolchain the agent will use inside the workspace, plus utilities the
# sandbox tools depend on (ripgrep for Glob/Grep, bash for Bash, git for
# clone-on-start and post-edit verify).
RUN apk add --no-cache \
      bash \
      ripgrep \
      git \
      make \
      ca-certificates \
      curl \
    # golangci-lint pinned via the upstream installer script.
    && curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
        | sh -s -- -b /usr/local/bin "${GOLANGCI_LINT_VERSION}" \
    && apk del curl

# Go toolchain copied from the builder so agents inside the workspace can
# compile, run tests, and `go vet`. Pinning this to the builder's Go version
# means run_tests/run_typecheck exercise the same compiler we built with.
COPY --from=builder /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:/usr/local/bin:${PATH}" \
    GOPATH=/home/sandbox/go \
    GOCACHE=/home/sandbox/.cache/go-build

# The sandbox binary itself.
COPY --from=builder /out/sandbox /usr/local/bin/sandbox

# Unprivileged user. The workspace is owned by sandbox:sandbox so agent file
# writes don't require root.
RUN addgroup -S sandbox \
    && adduser -S -G sandbox -h /home/sandbox sandbox \
    && mkdir -p /workspace /home/sandbox/.cache/go-build /home/sandbox/go \
    && chown -R sandbox:sandbox /workspace /home/sandbox

USER sandbox
WORKDIR /workspace

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/sandbox"]
CMD ["-addr=:8080", "-workspace=/workspace"]
