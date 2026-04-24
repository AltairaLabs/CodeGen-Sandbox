---
title: Secret
description: Retrieve operator-configured credentials through an audited interface.
---

Return the value of a named operator-configured secret, in place of exposing credentials through the sandbox's environment. Every access is audit-logged.

## Schema

### `secret`

| Param | Type | Required | Notes |
|---|---|---|---|
| `name` | string | yes | Secret name (case-insensitive). |

### `secrets_available`

No arguments. Returns the sorted list of configured secret names, one per line. Values are never returned.

## Example

Discover what's configured:

```json
{"tool": "secrets_available"}
```

Response body:

```
brave_api_key
github_token
```

Retrieve one:

```json
{"tool": "secret", "arguments": {"name": "github_token"}}
```

The body of a successful response IS the secret value. Pass it straight to the downstream API — never echo or log it.

## Configuration

### `-secrets-dir` flag (file source)

Pass `-secrets-dir=<path>` on the sandbox binary to enable the file source. Each top-level file's basename is the secret name; the file contents (with any trailing `\n` trimmed) is the value. Subdirectories, dotfiles, and names containing path separators are ignored.

The directory should be read-only and owned by the UID the sandbox runs as. A Kubernetes `Secret` mounted as a volume fits this model directly:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: codegen-sandbox
spec:
  containers:
    - name: sandbox
      image: codegen-sandbox:latest
      args: ["-secrets-dir=/run/codegen-secrets"]
      volumeMounts:
        - name: creds
          mountPath: /run/codegen-secrets
          readOnly: true
  volumes:
    - name: creds
      secret:
        secretName: codegen-agent-credentials
        defaultMode: 0400
```

With that `Secret`'s keys (`github_token`, `brave_api_key`, …) each written as one file, the sandbox exposes them by those names.

### `CODEGEN_SANDBOX_SECRET_<NAME>` env vars

Without a mounted volume, operators can expose secrets by setting `CODEGEN_SANDBOX_SECRET_<NAME>=<value>` on the container. `<NAME>` is lowercased internally, so `CODEGEN_SANDBOX_SECRET_GITHUB_TOKEN=...` exposes `github_token`.

### Both sources at once

File and env sources work together. When the same name is configured in both, **env wins** — this lets operators temporarily override a mounted value without remounting the volume. The `secrets_available` list is the union.

## Audit log

Every successful `secret` call emits one line to the sandbox's stderr:

```
api secret sub=mcp name=github_token len=40 source=env
```

Fields:

- `sub` — call origin. Always `mcp` for MCP tool calls (the MCP surface has no per-caller identity; HTTP API uses `sub=<forwarded-identity>` elsewhere).
- `name` — the looked-up name, lowercased.
- `len` — length of the returned value in bytes.
- `source` — `env` or `file`.

The audit line never contains the secret value — under any code path, including error paths. A test enforces this on every commit.

## Path-traversal safety

Names are validated at the tool-call layer before the store ever sees them. Only ASCII letters, digits, `_`, and `-` are allowed. Anything else (`../etc/passwd`, `foo/bar`, dotfiles) returns `invalid secret name: <input>` without touching the file system.

## Secret shapes still get scrubbed

The tool's response passes through the same [scrub middleware](/concepts/secret-scrubbing/) as every other tool result. If a configured value happens to match a known secret shape (e.g. an operator stores `ghp_` PAT literals), the response body gets `[REDACTED:…]` replacements on the way out. The audit line is unaffected.

This is defense in depth. The intended pattern is: the agent reads the secret and immediately feeds it to the credentialed API in the same turn. If that path inadvertently echoes into a Bash stdout or log that the agent returns, the scrubber catches the accidental re-emit.

## Nil-safe behaviour

If the sandbox is started with neither `-secrets-dir` nor any `CODEGEN_SANDBOX_SECRET_*` env var:

- `secret` returns `secrets not configured`.
- `secrets_available` returns an empty string.

## Related

- [Secret scrubbing](/concepts/secret-scrubbing/) — the redactor that covers the value if it leaks via another channel.
- [Configuration](/reference/configuration/) — the `-secrets-dir` flag.
- [Trust boundary](/concepts/trust-boundary/) — why credentials live outside the agent's shell env.
