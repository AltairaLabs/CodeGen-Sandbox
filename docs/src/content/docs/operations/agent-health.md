---
title: Agent Health Metrics
description: State-carrying Prometheus signals that surface whether the agent is making progress.
---

Counter metrics (`sandbox_tool_calls_total`, `sandbox_edit_bytes_total`, ...) answer **what** happened. Agent-health metrics answer **whether the agent is making progress**: a failing-test streak that never shrinks, a rising tool error rate, or the same tool invocation repeating inside a short window each signal the agent is stuck even when individual tool calls look fine.

The agent-health surface is a strict extension of the Prometheus pipeline: when `-metrics-addr` is set, the health tracker is constructed automatically and every tool call flows into it through the existing observability middleware. There is no separate enable flag.

## Emitted metrics

All four live on the existing `/metrics` endpoint, prefixed `sandbox_agent_`.

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `sandbox_agent_test_failure_streak` | gauge | — | Consecutive `run_tests` invocations whose failure count did not decrease. Reset on decrease or `exit=0`. |
| `sandbox_agent_time_since_last_green_seconds` | gauge | — | Seconds since the last `run_tests` / `run_lint` / `run_typecheck` that exited `0`. Reads `0` on process start so "never green" never looks like "green at epoch". |
| `sandbox_agent_tool_error_rate` | gauge | — | Errored tool calls divided by total tool calls over the configured rolling window (default 100 calls). Value is clamped to `[0, 1]`. |
| `sandbox_agent_tool_repetition_total` | counter | `tool` | Increments once per burst when the same `(tool, args-hash)` pair repeats at least `-metrics-tool-repetition-threshold` times inside `-metrics-tool-repetition-window`. |

The `tool` label on the repetition counter is drawn from the closed MCP tool list — no argument value or file path ever becomes a label, so the active series count stays bounded.

## Tunables

Three flags sit next to `-metrics-addr` in `cmd/sandbox/main.go`:

```bash
codegen-sandbox \
  -metrics-addr=:9090 \
  -metrics-tool-repetition-window=10m \
  -metrics-tool-repetition-threshold=3 \
  -metrics-error-rate-window=100
```

- `-metrics-tool-repetition-window` (`time.Duration`, default `10m`) — how far back repeat counts are considered.
- `-metrics-tool-repetition-threshold` (int, default `3`) — minimum repeats within the window before the counter increments.
- `-metrics-error-rate-window` (int, default `100`) — size of the rolling outcome buffer feeding the error-rate gauge; expressed as a count of recent tool calls, not a duration.

The gauges initialise to `0` on process start. The time-since-last-green gauge advances every second via a lightweight ticker so dashboards see a smooth line regardless of Prometheus scrape cadence.

## Spike patterns to watch

**`sandbox_agent_test_failure_streak` climbing steadily**
> The agent keeps running `run_tests` but the failing set never shrinks — classic "thrashing" signature. Correlate with `sandbox_tool_calls_total{tool="run_tests"}` to confirm the streak comes from actual test runs rather than stale state.

**`sandbox_agent_time_since_last_green_seconds` crossing a multi-minute threshold**
> The agent hasn't produced a clean verify run in that interval. Combined with elevated `sandbox_tool_calls_total{tool="Edit"}`, it's a strong signal the agent is editing without compiling — the right time to interrupt and checkpoint.

**`sandbox_agent_tool_error_rate` sustained > 0.3**
> Roughly one in three tool calls is erroring. Often correlates with path violations (`sandbox_path_violations_total`), denylist hits (`sandbox_denylist_hits_total`), or tool-argument mis-understandings. Alert thresholds depend on the agent's typical baseline — in steady state, a well-behaved agent stays below ~0.05.

**`sandbox_agent_tool_repetition_total{tool="Read"}` rising fast**
> The agent is reading the same file with the same arguments repeatedly — usually a prompt-loop symptom. The counter increments once per burst, so a steady rise means many distinct bursts, not one long one.

## Implementation notes

- The tracker lives in `internal/metrics/health` and is wired into `internal/server` through the existing `observabilityRegistrar`. Only one Prometheus registry exists.
- Args hashing is `sha256(tool + "\n" + canonicalJSON(args))[:8]`. Map keys are sorted before encoding so argument order can't create spurious duplicates.
- Every tracker method is safe on a nil receiver, so call sites (server, verify tools) never need a nil-guard.
- The verify tools (`run_tests`, `run_lint`, `run_typecheck`) call `ObserveGreen` on clean exits; `run_tests` additionally calls `ObserveTestResult` with the parsed failure count so the streak gauge can advance per language-specific parser.
