---
title: Configuration
description: CLI flags, environment variables, and Docker run options.
---

The sandbox is configured via CLI flags and a small set of env vars. No config file.

## CLI flags

```
codegen-sandbox -addr=<host:port> -workspace=<path>
```

| Flag | Default | Notes |
|---|---|---|
| `-addr` | `:8080` | HTTP listen address for the MCP server. Bind to `127.0.0.1:8080` for host-only access. |
| `-api-addr` | `""` | HTTP listen address for the human-facing API (`/api/*`). Empty disables. |
| `-metrics-addr` | `""` | HTTP listen address for the Prometheus `/metrics` endpoint. Empty disables. See [Metrics](/operations/metrics/). |
| `-workspace` | `/workspace` | Absolute path to the agent's workspace root. Must exist and be a directory. |

## Environment variables

The sandbox itself has no required runtime env vars. It inherits the full environment from `docker run`, but none of the shipped tools (`Read` / `Edit` / `Bash` / etc.) consult env vars for behaviour. Web-search / fetch configuration happens on the sibling MCP server you wire in alongside — see [Non-sandbox tools](/concepts/non-sandbox-tools/).

[Secret scrubbing](/concepts/secret-scrubbing/) redacts well-known shapes from tool OUTPUT, but does not redact env vars themselves. Only pass secrets the agent is genuinely expected to consume via `Bash` (e.g. `GITHUB_TOKEN` for `gh` CLI calls).

## Docker run options

```bash
docker run --rm -it \
  -p 8080:8080 \
  -v /host/workspace:/workspace \
  codegen-sandbox:dev
```

Recommended production additions:
- `--read-only` — read-only root filesystem. The sandbox writes only to `/workspace` and tempdirs.
- `--tmpfs /tmp` — if `--read-only`, give the sandbox a writable `/tmp`.
- `--cap-drop ALL --security-opt=no-new-privileges` — drop Linux capabilities.
- `--network ...` — constrain egress (e.g. to a proxy that enforces allowlisting).
- `--memory=1g --cpus=1.0` — resource limits.

## Timeouts

All timeouts are per-tool-call, set by the caller via the tool's `timeout` parameter, and clamped to per-tool maxima:

| Tool | Default | Max |
|---|---|---|
| Bash (foreground) | 120s | 600s |
| run_tests | 300s | 1800s |
| run_lint | 120s | 600s |
| run_typecheck | 120s | 600s |
| Edit (post-edit lint) | 30s (hardcoded) | n/a |

HTTP server-level timeouts:

- `ReadHeaderTimeout`: 10 seconds (slowloris defence).
- `IdleTimeout`: 60 seconds.
- `WriteTimeout`: unset (SSE streams are long-lived).
- Graceful shutdown grace window: 10 seconds.

## Ports

The sandbox listens on HTTP only. TLS termination is the operator's responsibility (reverse proxy, service mesh, etc.). The `LocalDockerProvider` use case maps the port to `127.0.0.1` on the host; the `RemoteDockerProvider` case should front it with a TLS-terminating proxy.
