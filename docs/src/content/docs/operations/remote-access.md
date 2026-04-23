---
title: "Remote access: HTTP API + SSH"
description: "Operator guide to the human-facing API that lets developers drive a sandbox pod from VS Code, JetBrains, or a raw shell."
---

The sandbox's primary interface is the MCP server on `-addr` (agent-facing). A **second** HTTP listener on `-api-addr` exposes a human-facing surface — file tree, file read, port-forward tunnels, interactive exec, embedded SSH — so a developer's local IDE can attach to a pod without `kubectl port-forward`.

This page is for the operator standing the sandbox up. The [VS Code remote guide](/guides/vscode-remote/) is for the developer consuming it.

## Topology

```
  developer laptop                          k8s cluster
 ┌──────────────┐    https      ┌──────────────────────┐
 │ VS Code      │ ──────────►   │ routing service      │
 │ JetBrains    │  auth at      │ (OIDC / header mint) │
 │ sandbox-     │  the edge     └──────────┬───────────┘
 │  forward     │                          │ HTTP + X-Forwarded-* headers
 └──────────────┘                          ▼
                                ┌──────────────────────┐
                                │ sandbox pod          │
                                │  - :8080 MCP         │ <-- agents
                                │  - :8081 API         │ <-- developers
                                │  - 127.0.0.1 SSHd    │ <-- via port-forward
                                └──────────────────────┘
```

The sandbox trusts identity headers from the routing service and serves unauthenticated otherwise. **Never expose `-api-addr` directly to the internet**; always put an auth-terminating proxy (your platform's existing OIDC gateway, an Envoy filter, an nginx `auth_request`, etc.) in front of it. See [identity headers](#identity-header-contract) for the exact contract.

## Flags

Set on the sandbox binary:

| Flag | Default | Description |
|---|---|---|
| `-api-addr` | `""` | Listen address for the human-facing API. Empty disables the listener entirely. |
| `-enable-api` | `false` | Mounts read-only routes: `/api/tree`, `/api/file`, `/api/events`. |
| `-enable-exec` | `false` | Mounts `/api/exec` (WebSocket PTY) for browser-side terminals. |
| `-enable-port-forward` | `false` | Mounts `/api/port-forward?port=N` (WebSocket raw TCP tunnel). Loopback-only targets. |
| `-enable-ssh` | `false` | Starts the embedded SSH server on `127.0.0.1:0` and mounts `/api/ssh-authorized-keys` + `/api/ssh-port`. |
| `-dev-mode` | `false` | When identity headers are missing, inject a placeholder `dev/dev` identity instead of returning 401. **Do not set in production.** |

Example:

```bash
sandbox \
  -addr=:8080 \
  -api-addr=:8081 \
  -enable-api \
  -enable-exec \
  -enable-port-forward \
  -enable-ssh \
  -workspace=/workspace
```

Each flag is independent — an operator who only wants file tree + raw TCP can skip `-enable-ssh` and `-enable-exec`.

## Discoverability: OpenAPI spec + Scalar UI

Two self-describing endpoints are always mounted when `-api-addr` is set (both are still gated by identity middleware):

| Route | Serves |
|---|---|
| `GET /api/openapi.yaml` | The embedded OpenAPI 3.1 spec (canonical source of truth). |
| `GET /api/docs` | A [Scalar](https://github.com/scalar/scalar) rendering of the spec. Loads the Scalar bundle from `cdn.jsdelivr.net`. |

The spec documents every route — including features gated behind `-enable-*` flags that may be off in a given deployment. Disabled routes return `404`; the spec still lists them so operators can discover the full surface. A drift test (`internal/api/docs_test.go`) keeps the spec and the server's route table in lock-step in CI.

> **Egress note:** the `/api/docs` page is an HTML shell that loads Scalar from `cdn.jsdelivr.net`. If your `NetworkPolicy` blocks outbound traffic from developer browsers (or from the sandbox pod itself, if you front the UI through an internal proxy), the page will render blank. `GET /api/openapi.yaml` is self-contained and has no external dependencies — point a local copy of Scalar, Swagger UI, or Redoc at it, or just consume the YAML directly.

## Identity header contract

The routing service MUST set three headers on every request it forwards:

| Header | Required | Semantics |
|---|---|---|
| `X-Forwarded-Sub` | yes | The caller's stable OIDC subject (opaque string). Used as the audit-log identifier and the SSH key ownership key. |
| `X-Forwarded-User` | no | Human-readable username. Convenience only — surfaced in audit logs. |
| `X-Forwarded-Groups` | no | Comma-separated group list. Currently informational (reserved for future policy). |

Behaviour when headers are absent:

- Without `-dev-mode`: **401 Unauthorized**. The request never reaches the route handler.
- With `-dev-mode`: a placeholder `Sub=dev, User=dev` identity is injected and the request proceeds.

The routing service MUST strip these headers from inbound requests before injecting its own. A client that sets `X-Forwarded-Sub` directly against an unprotected sandbox would impersonate an arbitrary subject.

## k8s deployment

Minimal Deployment + Service + NetworkPolicy snippet. Adjust `image:`, `workspace`, and namespace to your setup.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: sandbox
spec:
  replicas: 1
  selector:
    matchLabels: { app: sandbox }
  template:
    metadata:
      labels: { app: sandbox }
    spec:
      containers:
        - name: sandbox
          image: ghcr.io/altairalabs/codegen-sandbox:v0.1.0
          args:
            - -addr=:8080
            - -api-addr=:8081
            - -enable-api
            - -enable-exec
            - -enable-port-forward
            - -enable-ssh
            - -workspace=/workspace
          ports:
            - { name: mcp, containerPort: 8080 }
            - { name: api, containerPort: 8081 }
          # The embedded SSH listener binds 127.0.0.1 and is intentionally
          # NOT a container port — it is only reachable through /api/port-forward.
          volumeMounts:
            - { name: workspace, mountPath: /workspace }
      volumes:
        - { name: workspace, emptyDir: {} }
---
apiVersion: v1
kind: Service
metadata:
  name: sandbox
spec:
  selector: { app: sandbox }
  ports:
    - { name: mcp, port: 8080, targetPort: mcp }
    - { name: api, port: 8081, targetPort: api }
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: sandbox-api-only-from-routing
spec:
  podSelector:
    matchLabels: { app: sandbox }
  policyTypes: [Ingress]
  ingress:
    # Only the routing service may reach :8081.
    - from:
        - namespaceSelector:
            matchLabels: { role: routing }
      ports:
        - { protocol: TCP, port: 8081 }
    # MCP (:8080) is its own trust boundary — allow from the agent
    # namespace per your org's policy.
    - from:
        - namespaceSelector:
            matchLabels: { role: agents }
      ports:
        - { protocol: TCP, port: 8080 }
```

The NetworkPolicy is the enforcement mechanism for "the routing service is the only path in". The sandbox itself trusts every peer that reaches `-api-addr`.

## Dev mode

`-dev-mode` skips the 401 when identity headers are missing and injects a placeholder subject. Use it for:

- Local loops where `curl -H 'X-Forwarded-Sub: alice' http://127.0.0.1:8081/api/tree` is a pain.
- The e2e demo script (`scripts/e2e-demo.sh`) and similar integration tests.

What it weakens:

- Any client that can reach `-api-addr` gets authenticated as `dev`. In production this is equivalent to "no auth at all".
- SSH pubkeys registered in dev mode are all owned by `dev` — there is no per-user separation.

If you find yourself reaching for `-dev-mode` in production, fix the routing-service identity plumbing instead.

## Audit log format

Each session's close (port-forward, exec, SSH) logs exactly one line to stderr. Fields are `key=value`, space-separated, prefixed with the route family.

```
api port-forward sub=alice@example.com port=2345 in=18432 out=9812 dur=2m13.4s
api exec        sub=alice@example.com duration=1h4m2.1s  exit=0
api ssh         sub=alice@example.com duration=42m3.2s   exit=130
api ssh-authorized-keys sub=alice@example.com type=ssh-ed25519
```

`sub` is always the OIDC subject from `X-Forwarded-Sub` (or `unknown` if somehow missing by the time the session closes). Pipe stderr into your normal log aggregator — these are standard `log.Printf` lines, not structured JSON.

## Security invariants

- **Port-forward target is loopback-only.** `/api/port-forward?port=N` dials `127.0.0.1:N`. There is no host parameter and one will not be added. The endpoint cannot be turned into an SSRF exit node.
- **SSH host key is ephemeral.** A fresh ed25519 host key is generated at sandbox start; no on-disk keys, no host-key reuse across pod restarts. Clients should use `StrictHostKeyChecking no` (the `sandbox-forward ssh-setup` output does this automatically).
- **SSH auth is public-key only.** The embedded server does not accept passwords or keyboard-interactive. Keys are registered via `POST /api/ssh-authorized-keys` and are owned by the identity subject that registered them.
- **SSH port is not a k8s service port.** The listener binds `127.0.0.1:0`. The only ingress path is through the already-authenticated `/api/port-forward` tunnel.
- **The routing service owns identity.** The sandbox never talks to an OIDC provider. If the routing service is compromised, the sandbox is too — scope the trust boundary accordingly.

## See also

- [VS Code Remote-SSH + IDE debugging guide](/guides/vscode-remote/) — the developer-facing counterpart to this page.
