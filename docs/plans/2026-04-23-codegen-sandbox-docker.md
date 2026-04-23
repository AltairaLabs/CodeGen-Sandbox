# Codegen Sandbox — Docker Packaging + Graceful Shutdown Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the codegen sandbox as a pinned Docker image (Alpine base + Go 1.25 + golangci-lint v2 + ripgrep + bash + git) with graceful shutdown and HTTP server timeouts in the binary.

**Architecture:** Refactor `cmd/sandbox/main.go` into a tiny `main` that calls a testable `Run(ctx, addr, workspace)` function in `cmd/sandbox/run.go`. `Run` constructs an `*http.Server` with `ReadHeaderTimeout` (guards against slowloris) and `IdleTimeout` (prevents connection leaks), listens for SIGINT/SIGTERM via `signal.NotifyContext`, and drains inflight requests with `srv.Shutdown(ctx)` when cancelled. A multi-stage Dockerfile builds the binary in a Go builder image and ships it on `alpine:3.20.3` with pinned toolchains. Makefile targets `docker-build` and `docker-run` streamline the local workflow.

**Tech Stack:** Go 1.25+, Alpine Linux 3.20, golangci-lint v2.6.0, ripgrep (from Alpine repo), bash (from Alpine repo), git (from Alpine repo), Docker (operator tool).

**Out of scope for this plan:**
- **CI publishing.** This repo has no remote; publishing to a registry is future work.
- **Multi-arch builds** (linux/amd64 only for v1; `docker buildx` can be added when needed).
- **Node / Python / Rust toolchains.** The proposal lists them, but v1 ships Go-only. Adding more is a follow-up plan and straightforward — one `apk add` per toolchain.
- **Runtime secret injection.** The agent's workspace credentials (e.g. for `git push`) are operator-supplied at `docker run` time; this plan doesn't add magic wiring.
- **gVisor / Firecracker / microVM sandboxing.** Per the proposal, that's the provider's concern (the Docker container itself IS the trust boundary for `LocalDockerProvider`).

---

## File Structure

Files introduced or modified:

| Path | Responsibility |
|---|---|
| `cmd/sandbox/main.go` | Shrinks to ~15 lines: flag parse + call into `Run`. |
| `cmd/sandbox/run.go` | New. `Run(ctx, addr, workspaceRoot)` builds workspace + server + `*http.Server` with timeouts and signal-based graceful shutdown. |
| `cmd/sandbox/run_test.go` | Tests: Run returns cleanly when ctx is cancelled before ListenAndServe. Run reports error when workspace root is invalid. |
| `Dockerfile` | Multi-stage build. Builder: `golang:1.25-alpine`. Runtime: `alpine:3.20.3` + pinned toolchains. Non-root `sandbox` user. |
| `.dockerignore` | Keeps build context small (exclude `bin/`, `.git/`, `/tmp`, editor junk). |
| `Makefile` | Add `docker-build` and `docker-run` targets. |

---

## Task 1: Graceful shutdown + server timeouts

**Files:**
- Modify: `cmd/sandbox/main.go` — shrink to flag parse + call to Run.
- Create: `cmd/sandbox/run.go`
- Test: `cmd/sandbox/run_test.go`

**Contract of `Run(ctx context.Context, addr, workspaceRoot string) error`:**
- Builds the workspace via `workspace.New(workspaceRoot)` — returns the error as-is on failure.
- Builds the server via `server.New(workspace)`.
- Wraps `srv.Handler()` in an `*http.Server` with `ReadHeaderTimeout = 10 * time.Second`, `IdleTimeout = 60 * time.Second`, no `WriteTimeout` (SSE needs streaming).
- Runs `http.Server.ListenAndServe` in a goroutine.
- On `ctx.Done()`, calls `srv.Shutdown(shutdownCtx)` with a 10-second deadline and returns its error (or nil).
- Returns `nil` on clean shutdown; non-nil only on unrecoverable startup or shutdown errors.

**Behavior of `main()`:**
- `flag.Parse()` for `-addr` and `-workspace`.
- `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` — SIGINT (Ctrl-C) and SIGTERM (docker stop) both trigger graceful shutdown.
- Calls `Run(ctx, addr, workspaceRoot)`; on error, `log.Fatal`.

- [ ] **Step 1: Write the failing tests**

Create `cmd/sandbox/run_test.go`:
```go
package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_CancelledContextExitsCleanly(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, "127.0.0.1:0", dir)
	}()

	// Give the server a beat to bind, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err, "Run should exit cleanly on ctx cancel")
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of ctx cancel")
	}
}

func TestRun_InvalidWorkspaceReturnsError(t *testing.T) {
	err := Run(context.Background(), "127.0.0.1:0", "/nonexistent/codegen-sandbox-test-root")
	require.Error(t, err)
}

func TestRun_AddressAlreadyInUseReturnsError(t *testing.T) {
	dir := t.TempDir()

	// Grab a port so the second Run fails to bind.
	ln, err := os.CreateTemp(t.TempDir(), "probe")
	require.NoError(t, err)
	ln.Close()

	// Start a first instance on an ephemeral port.
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	done1 := make(chan error, 1)
	go func() {
		done1 <- Run(ctx1, "127.0.0.1:18085", dir)
	}()
	time.Sleep(100 * time.Millisecond)

	// Second instance should fail to bind.
	err2 := Run(context.Background(), "127.0.0.1:18085", dir)
	assert.Error(t, err2)

	cancel1()
	<-done1
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./cmd/sandbox/...
```
Expected: compile error — `Run` undefined.

- [ ] **Step 3: Implement `run.go`**

Create `cmd/sandbox/run.go`:
```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/server"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
)

const (
	shutdownGraceSeconds = 10
	readHeaderTimeout    = 10 * time.Second
	idleTimeout          = 60 * time.Second
)

// Run starts the sandbox MCP server on addr with workspaceRoot as the
// agent-visible workspace. It listens for ctx cancellation and drains
// inflight HTTP requests within a bounded grace window before returning.
// Returns nil on clean shutdown; non-nil on startup failure or a shutdown
// that exceeds the grace window.
func Run(ctx context.Context, addr, workspaceRoot string) error {
	ws, err := workspace.New(workspaceRoot)
	if err != nil {
		return fmt.Errorf("workspace: %w", err)
	}

	srv, err := server.New(ws)
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}

	// No WriteTimeout — SSE streams are long-lived.
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	log.Printf("codegen-sandbox listening on %s (workspace=%s)", addr, ws.Root())

	listenErr := make(chan error, 1)
	go func() {
		err := httpSrv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		listenErr <- err
	}()

	select {
	case err := <-listenErr:
		// Crash before shutdown signal.
		return err
	case <-ctx.Done():
		log.Printf("shutdown signal received; draining up to %ds", shutdownGraceSeconds)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGraceSeconds*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	// Wait for the listen goroutine to return so we don't leak it.
	return <-listenErr
}
```

- [ ] **Step 4: Simplify `main.go`**

Replace `cmd/sandbox/main.go` with:
```go
// Package main is the entry point for the codegen-sandbox MCP server.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	root := flag.String("workspace", "/workspace", "workspace root (absolute path)")
	flag.Parse()

	// SIGINT for Ctrl-C; SIGTERM for docker stop and most orchestrators.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := Run(ctx, *addr, *root); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 5: Run tests**

```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./cmd/sandbox/... -v -count=1
```
Expected: 3 pass.

- [ ] **Step 6: Full suite + lint**

```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./... -race -count=1
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox lint
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox build ./...
```
All exit 0.

- [ ] **Step 7: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add cmd/sandbox
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(cmd): graceful shutdown + HTTP server timeouts

Extracts main's server setup into a testable Run(ctx, addr, workspace)
function. Adds ReadHeaderTimeout (10s, slowloris defence) and IdleTimeout
(60s, prevents connection leaks). main() now catches SIGINT/SIGTERM via
signal.NotifyContext and propagates cancellation to Run, which drains
inflight requests for up to 10s via http.Server.Shutdown before returning.
WriteTimeout is deliberately unset — SSE streams are long-lived.
EOF
```

---

## Task 2: Dockerfile + .dockerignore

**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`

**Image layout:**
- Stage 1 (`builder`): `golang:1.25-alpine`, runs `go mod download` + `go build` with `CGO_ENABLED=0` for a static binary.
- Stage 2 (runtime): `alpine:3.20.3`. Installs `bash`, `ripgrep`, `git`, `ca-certificates` via `apk`. Installs golangci-lint v2.6.0 from the official installer script. Copies Go 1.25's toolchain from the builder image (so agents can `go build` inside the workspace). Copies the sandbox binary. Creates a non-root `sandbox` user owning `/workspace`. Exposes 8080.

- [ ] **Step 1: Write the Dockerfile**

Create `Dockerfile`:
```dockerfile
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
# compile, run tests, and `go vet`. Keeping this pinned to the builder's Go
# version means run_tests/run_typecheck exercise the same compiler we built
# the sandbox binary with.
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
```

- [ ] **Step 2: Write the `.dockerignore`**

Create `.dockerignore`:
```
.git
.gitignore
.github
bin/
docs/
README.md
*.md
*.out
*.test
.DS_Store
.golangci.yml
.claude
```

(Keeping `.golangci.yml` out of the image because the sandbox itself doesn't lint its own workspace during runtime — agents bring their own config inside the workspace.)

- [ ] **Step 3: No tests, no commit yet** — the build happens in Task 3 after the Makefile targets land. Skipping commit here keeps the Dockerfile + its invocation in the same commit.

---

## Task 3: Makefile targets + smoke test

**Files:**
- Modify: `Makefile` — add `docker-build`, `docker-run`, `docker-clean` targets.

**Contract:**
- `make docker-build` → builds `codegen-sandbox:dev` locally.
- `make docker-run` → runs the image on port 8080 with `/tmp/codegen-sandbox-workspace` mounted.
- `make docker-clean` → removes the image.

- [ ] **Step 1: Add targets to `Makefile`**

Append to `Makefile`:
```makefile
.PHONY: docker-build docker-run docker-clean

IMAGE ?= codegen-sandbox:dev

docker-build:
	docker build -t $(IMAGE) .

docker-run:
	mkdir -p /tmp/codegen-sandbox-workspace
	docker run --rm -it \
		-p 8080:8080 \
		-v /tmp/codegen-sandbox-workspace:/workspace \
		$(IMAGE)

docker-clean:
	docker rmi $(IMAGE) 2>/dev/null || true
```

- [ ] **Step 2: Commit Dockerfile + ignore + Makefile together**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add Dockerfile .dockerignore Makefile
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(docker): multi-stage Dockerfile + make docker-build/run targets

Alpine 3.20.3 runtime pinning Go 1.25, golangci-lint v2.6.0, ripgrep, bash,
git, make. The builder stage compiles a static CGO_ENABLED=0 binary with
-trimpath + -ldflags='-s -w'. Runtime copies the full Go toolchain from
the builder so agents can compile, test, vet, and lint code inside
/workspace using the same Go version the sandbox itself was built with.
Non-root sandbox user owns /workspace.
EOF
```

- [ ] **Step 3: Smoke test — build the image**

Run (requires Docker):
```bash
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox docker-build
```
Expected: exit 0. The final image tag is `codegen-sandbox:dev`.

If `docker` is not installed, skip to step 6.

- [ ] **Step 4: Smoke test — run the image and hit the SSE endpoint**

Run:
```bash
mkdir -p /tmp/codegen-sandbox-docker-smoke
docker run --rm -d --name codegen-sandbox-smoke \
  -p 18086:8080 \
  -v /tmp/codegen-sandbox-docker-smoke:/workspace \
  codegen-sandbox:dev

# Wait for bind.
for i in 1 2 3 4 5; do
  if curl -sS -o /dev/null --max-time 1 http://127.0.0.1:18086/sse 2>/dev/null; then break; fi
  sleep 1
done

# Capture the initial SSE endpoint event.
curl -sS -N --max-time 2 http://127.0.0.1:18086/sse | head -n 2
```
Expected: `event: endpoint` + `data: /message?sessionId=...`.

- [ ] **Step 5: Smoke test — verify graceful shutdown**

Run:
```bash
docker stop -t 15 codegen-sandbox-smoke
docker logs codegen-sandbox-smoke 2>&1 | grep -E '(listening|shutdown|draining)'
```
Expected: the log shows `listening on :8080` then `shutdown signal received; draining up to 10s`. Container exits with code 0.

- [ ] **Step 6: No commit** (this step is verification only).

---

## Self-Review Notes

**Spec coverage:**
- Pinned Docker image with Go, golangci-lint, ripgrep, git, bash — Task 2.
- Graceful shutdown on SIGTERM — Task 1.
- Server timeouts — Task 1 (ReadHeaderTimeout + IdleTimeout; no WriteTimeout because SSE is long-lived).
- Non-root container user — Task 2.
- Make targets for dev workflow — Task 3.

**Deliberately deferred:**
- Node/Python/Rust toolchains — one `apk add` per toolchain in a follow-up.
- Multi-arch builds.
- CI publishing to a registry.
- In-image credentials.

**Placeholder scan:** no TBDs, code blocks complete, commands explicit.

**Type consistency:**
- `Run(ctx context.Context, addr, workspaceRoot string) error` — single signature referenced by both `run.go` and tests.
- `shutdownGraceSeconds = 10`, `readHeaderTimeout = 10 * time.Second`, `idleTimeout = 60 * time.Second` — consistent constant names.
- Docker `sandbox` user + group name is consistent across `adduser`, `addgroup`, and `chown`.
- `GOLANGCI_LINT_VERSION` ARG default matches the project's locally installed version (v2.6.0).

**Known trade-offs documented in the plan:**
- Copying the full Go toolchain from the builder keeps the runtime image larger but ensures agents compile with the same toolchain the sandbox was built with. For a smaller image, a follow-up could ship just `go` binaries without stdlib (unlikely, since agents will want stdlib).
- `apk` packages aren't pinned to specific versions — the Alpine base tag (`3.20.3`) is the reproducibility anchor. Pinning every package is high-maintenance and the base-tag pin gives reasonable stability.
- `ReadHeaderTimeout = 10s` is conservative; real clients always send headers in one packet. Can be tightened to 5s if slowloris becomes a concern in production.
