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
| `-addr` | `:8080` | HTTP listen address. Bind to `127.0.0.1:8080` for host-only access. |
| `-workspace` | `/workspace` | Absolute path to the agent's workspace root. Must exist and be a directory. |

## Environment variables

| Variable | Used by | Effect |
|---|---|---|
| `CODEGEN_SANDBOX_SEARCH_BACKEND` | WebSearch | Selects the search backend: `brave`, `exa`, `tavily`. Unset disables WebSearch. |
| `CODEGEN_SANDBOX_BRAVE_API_KEY` (etc.) | WebSearch | Backend-specific API key. |

The sandbox inherits the full environment from `docker run`. [Secret scrubbing](/concepts/secret-scrubbing/) redacts well-known shapes from tool OUTPUT, but does not redact env vars themselves. Operators should only pass secrets that the agent is genuinely expected to use.

## Docker run options

```bash
docker run --rm -it \
  -p 8080:8080 \
  -v /host/workspace:/workspace \
  -e CODEGEN_SANDBOX_SEARCH_BACKEND=brave \
  -e CODEGEN_SANDBOX_BRAVE_API_KEY=... \
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
| WebFetch | 30s | 120s |
| Edit (post-edit lint) | 30s (hardcoded) | n/a |

HTTP server-level timeouts:

- `ReadHeaderTimeout`: 10 seconds (slowloris defence).
- `IdleTimeout`: 60 seconds.
- `WriteTimeout`: unset (SSE streams are long-lived).
- Graceful shutdown grace window: 10 seconds.

## Ports

The sandbox listens on HTTP only. TLS termination is the operator's responsibility (reverse proxy, service mesh, etc.). The `LocalDockerProvider` use case maps the port to `127.0.0.1` on the host; the `RemoteDockerProvider` case should front it with a TLS-terminating proxy.
