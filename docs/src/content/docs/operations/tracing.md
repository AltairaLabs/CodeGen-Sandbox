---
title: Tracing
description: OpenTelemetry spans for MCP tool invocations.
---

The sandbox emits one OpenTelemetry span per MCP tool invocation. Operators already running an OTel collector for the agent runtime (PromptKit, the model provider, retrieval services) can see sandbox tool-call activity inline with the rest of a trace view — no separate log stream to correlate against.

Tracing **adds** machine-readable telemetry; it does not replace the existing `log.Printf` audit lines. Spans are for span stores (Tempo, Jaeger, Honeycomb, Dynatrace, any OTLP-speaking backend); the audit lines stay as a human-readable fallback.

## Enabling the exporter

```bash
codegen-sandbox \
  -addr=:8080 \
  -otlp-endpoint=http://otel-collector:4318 \
  -workspace=/workspace
```

`-otlp-endpoint` defaults to `$OTEL_EXPORTER_OTLP_ENDPOINT` (the standard OTel env var) so most operators can drop the flag entirely and configure the exporter alongside whatever other OTel consumers the pod already runs. An empty value disables tracing — the tracer provider is nil-safe end-to-end, so there is no runtime cost when no endpoint is configured.

Only the OTLP-HTTP transport is supported. gRPC OTLP, stdout, and Jaeger-native transports are deliberately out of scope — the collector sidecar pattern is the expected deployment shape, and every OTel collector speaks OTLP-HTTP.

## Span shape

Every tool invocation produces one span.

- **Name**: `tool.<ToolName>` — e.g. `tool.Edit`, `tool.run_tests`, `tool.Bash`.
- **Span status**: `Ok` when the handler returned successfully; `Error` when either the Go handler returned an error or the tool result had `IsError = true`.

### Attributes

| Attribute | Type | Notes |
|---|---|---|
| `tool.name` | string | The MCP tool name (same value as the `tool` label on the Prometheus metric). |
| `tool.status` | string | `ok` or `error`. Matches the `status` dimension on `sandbox_tool_calls_total`. |
| `tool.duration_ms` | int64 | Wall-clock duration of the full handler pipeline (scrub + metrics + tool). |
| `tool.language` | string | Detected project language (`go`, `node`, `python`, `rust`) or empty. |
| `tool.error` | string | Only present on error spans. Populated from the first `TextContent` of the tool result or the Go error string. Clipped to 512 bytes with an ellipsis suffix so a runaway handler can't blow up span payload size. |

Attributes intentionally **omitted** from v1:

- `bytes_in`, `bytes_out` — per-tool, high-variance, and already captured by the dedicated `sandbox_read_bytes_total` / `sandbox_write_bytes_total` / `sandbox_edit_bytes_total` counters.
- `exit_code` — specific to `Bash` only; sits on the metrics plane instead.
- File paths, raw command strings, session IDs — same cardinality discipline applied to metrics labels applies to span attributes.

If you need per-invocation file paths or command strings, correlate by trace-id against the sandbox's own audit `log.Printf` lines rather than embedding them in span attributes.

## Middleware composition

Tool handlers are wrapped in three layers, innermost to outermost:

1. **scrub** — redact secret-like tokens before the result leaves the sandbox.
2. **metrics** — record latency + status of the scrubbed pipeline into `sandbox_tool_calls_total` / `sandbox_tool_duration_seconds`.
3. **tracing** — open one span covering the whole invocation, including scrub + metrics overhead.

Tracing is outermost on purpose: the span's `tool.duration_ms` matches what the MCP caller actually observed, including the scrub+metrics layers (both are microsecond-scale in practice, but the invariant matters).

## Correlation with metrics

Every span attribute mirrors a metrics label. An alert that fires on `sandbox_tool_calls_total{status="error"}` can be pivoted into span search via the matching `tool.status = "error"` attribute — same tool names, same status values, same language enum. The two surfaces are designed to agree.

There is **no traceparent propagation** from the MCP request today. `mcp-go` does not surface the `traceparent` header on inbound tool calls, so every span is currently a root span at this layer. When an upstream ingress does propagate the header, the span will become a child automatically — no code change needed on the sandbox side — but until the MCP transport grows that hook every sandbox span is detached from the agent runtime's parent trace. This is tracked as follow-up work; metrics correlation plus the `log.Printf` audit lines bridge the gap for v1.

## Shutdown drain

The tracer provider uses a batch span processor, so spans are buffered before export. On `SIGTERM` / `SIGINT` the sandbox drains the buffer inside the same 10-second grace window as the HTTP listeners; no extra configuration needed.
