---
title: Metrics
description: Prometheus /metrics endpoint and emitted metric families.
---

The sandbox exposes an optional Prometheus surface over a dedicated listener. Every code-touching tool call, HTTP API request, and background resource is instrumented; operators can alert on tool-error rates, denylist hits, scrub anomalies, and workspace sprawl without custom log parsing.

## Enabling the endpoint

```bash
codegen-sandbox \
  -addr=:8080 \
  -metrics-addr=:9090 \
  -workspace=/workspace
```

`-metrics-addr` is empty by default, which disables the listener entirely. A non-empty value starts a third `net/http` server exposing only `/metrics` (the MCP server runs on `-addr`; the optional human-facing API runs on `-api-addr`).

There is **no authentication on the listener** — this is a deliberate design choice. Bolting on identity middleware couples scraper deployment to the sandbox's JWT-forwarding path. Instead, restrict scraper access at the network layer (Kubernetes `NetworkPolicy`, security group, mesh policy).

## Example Prometheus scrape config

```yaml
scrape_configs:
  - job_name: codegen-sandbox
    scrape_interval: 30s
    static_configs:
      - targets: ["codegen-sandbox.sandbox.svc.cluster.local:9090"]
```

## Emitted metrics

Everything is prefixed `sandbox_`. Runtime + process metrics (`go_*`, `process_*`) ship for free via the `prometheus/client_golang` default collectors.

### Tool plane

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `sandbox_tool_calls_total` | counter | `tool`, `status`, `language` | `status ∈ {ok,error,denied,timeout}`; `language` is the detected project type (`go`, `node`, `python`, `rust`) or empty |
| `sandbox_tool_duration_seconds` | histogram | `tool` | Buckets tuned for tool latency: `[0.01, 0.05, 0.1, 0.5, 1, 5, 30, 120]` |
| `sandbox_read_bytes_total` | counter | — | Bytes returned by `Read` |
| `sandbox_write_bytes_total` | counter | — | Bytes written by `Write` |
| `sandbox_edit_bytes_total` | counter | — | Replacement-string bytes applied by `Edit` |
| `sandbox_bash_exit_codes_total` | counter | `exit` | Bucketed: `0`, `1-125`, `126-128`, `timeout(124)`, `>=129` |

### HTTP API plane

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `sandbox_api_http_requests_total` | counter | `route`, `status` | `route` is the matched mux pattern; `status ∈ {2xx,3xx,4xx,5xx,101,other}` |
| `sandbox_api_http_duration_seconds` | histogram | `route` | Same buckets as the tool histogram |

### Resource plane

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `sandbox_workspace_bytes` | gauge | — | Updated every 30s; excludes `.git` and `node_modules` |
| `sandbox_workspace_files` | gauge | — | Updated every 30s; same exclusions |
| `sandbox_background_shells` | gauge | — | Currently-registered background bash shells |
| `sandbox_ws_connections` | gauge | `endpoint` | `endpoint ∈ {exec,port-forward}` |
| `sandbox_sse_streams` | gauge | — | Open `/api/events` Server-Sent-Event streams |

### Security plane

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `sandbox_denylist_hits_total` | counter | `token` | Normalised token from the bash denylist regex (`sudo`, `su`, `mkfs`, …) |
| `sandbox_scrub_hits_total` | counter | `pattern` | Pattern name from `internal/scrub/` (bounded ~13 entries) |
| `sandbox_scrub_bytes_redacted_total` | counter | — | Total bytes of matched secret tokens scrubbed |
| `sandbox_path_violations_total` | counter | — | Containment-rejected reads / writes / edits |

## Cardinality discipline

The sandbox label set is **always** drawn from closed, bounded enums:

- `tool` is the fixed MCP tool list.
- `status`, `language`, `endpoint`, `exit` are closed enums.
- `route` is the matched mux pattern, never the raw URL.
- `pattern` / `token` come from the scrub and bash-denylist registries.

Explicitly **excluded** from labels:

- Identity / session IDs (use OTel traces for per-request correlation)
- File paths
- Raw command strings
- User-provided regex, globs, or query parameters

If you add a new tool, denylist token, or scrub pattern, the corresponding label value is fixed by that registry — there is no path by which user-supplied text leaks into a label. This keeps the active series count bounded regardless of workload.

## Workspace-size timer

The workspace gauge is refreshed on a 30s ticker, not as a custom Prometheus collector. A collector runs on every scrape, so a fast scraper could turn the gauge into a CPU hog by repeatedly walking the workspace. The ticker approach guarantees exactly one walk every 30 seconds regardless of scrape cadence. Primed on startup so the first scrape sees a real value.
