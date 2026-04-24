# Grafana dashboards for codegen-sandbox

Two pre-built dashboards covering the Prometheus surface the sandbox exposes on `/metrics`:

| File | Audience | Purpose |
|---|---|---|
| [`agent-health.json`](agent-health.json) | Operators / on-call | "Is my agent having a bad day?" — tool error rate, time since last green, test-failure streak, denylist + scrub + path-violation counters, latency p95/p99, workspace size trend. |
| [`agent-arena.json`](agent-arena.json) | Sales demos, leadership updates, curiosity | "Look what's happening right now" — vibe stat, characters per minute (vs human typing speed), tools-per-minute by tool, language pie chart, scrub leaderboard, denylist trophy case. |

## Compatibility

- **Grafana 11.x** (schema version 39). Tested against 11.0+. Older Grafana versions may need a re-import to migrate the schema.
- **Prometheus datasource** (any version that supports the standard `prometheus` data-source plugin).
- The sandbox process must be started with `-metrics-addr=:<port>` so the `/metrics` endpoint is exposed; see [/operations/metrics](../../docs/src/content/docs/operations/metrics.md) for the metric inventory.

## Importing

In Grafana:

1. **Dashboards → New → Import**.
2. Either upload the JSON file or paste its contents into the **Import via panel json** box.
3. On the import screen pick your Prometheus datasource for the `DS_PROMETHEUS` variable. Both dashboards use a templated datasource so you can wire them to whichever Prometheus instance is scraping the sandbox.
4. Save. The dashboard UID (`codegen-sandbox-agent-health` / `codegen-sandbox-agent-arena`) is fixed so re-imports update in place.

Or via `grafana-cli` / API:

```bash
curl -sSf -H "Authorization: Bearer $GRAFANA_TOKEN" \
     -H "Content-Type: application/json" \
     -X POST \
     -d "{\"dashboard\": $(jq . agent-health.json), \"overwrite\": true}" \
     https://grafana.example/api/dashboards/db
```

## Variables

Both dashboards expose:

- **`DS_PROMETHEUS`** — the Prometheus datasource name. Required.

`agent-health.json` additionally exposes:

- **`tool`** — multi-select filter populated from `label_values(sandbox_tool_calls_total, tool)`. Default: All. Affects the per-tool error-rate timeseries and the latency p95/p99 panel.

## Metric inventory the dashboards depend on

The dashboards consume only metrics that the sandbox exposes today (see `internal/metrics/metrics.go` for the full list):

### Counters

| Metric | Labels | Used in |
|---|---|---|
| `sandbox_tool_calls_total` | `tool, status, language` | Both — error rate, language pie, tools/min |
| `sandbox_edit_bytes_total` | — | Arena CPM/WPM, read:write ratio |
| `sandbox_write_bytes_total` | — | Arena CPM/WPM, read:write ratio |
| `sandbox_read_bytes_total` | — | Arena read:write ratio |
| `sandbox_bash_exit_codes_total` | `exit` | Health bash-exit-mix timeseries |
| `sandbox_denylist_hits_total` | `token` | Health denylist-hits stat, Arena trophy case |
| `sandbox_scrub_hits_total` | `pattern` | Health scrub-hits stat, Arena scrub leaderboard |
| `sandbox_path_violations_total` | — | Health path-violations stat |
| `sandbox_agent_tool_repetition_total` | `tool` | Health tool-repetition timeseries |

### Histograms

| Metric | Labels | Used in |
|---|---|---|
| `sandbox_tool_duration_seconds` | `tool` | Health latency p95/p99 |

### Gauges

| Metric | Labels | Used in |
|---|---|---|
| `sandbox_workspace_bytes` | — | Health workspace-size trend |
| `sandbox_background_shells` | — | Health background-shells stat |
| `sandbox_sse_streams` | — | Arena sessions-online stat |
| `sandbox_agent_test_failure_streak` | — | Health test-failure-streak stat |
| `sandbox_agent_time_since_last_green_seconds` | — | Health time-since-last-green stat, Arena vibe |
| `sandbox_agent_tool_error_rate` | — | Health error-rate stat, Arena vibe |

## Panels intentionally NOT included

The original design ([#29](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/29)) sketched a few panels that depend on instrumentation the sandbox doesn't expose yet. They're parked for a follow-up rather than baked in as broken queries:

- **Per-session breakouts** (top 10 sessions by time-since-green, ping-pong by file, diff-size explosion). Need a `session` label on the gauges; today they're process-scoped.
- **Most-grepped string of the day, most-read file, largest write ever.** Need a small bounded label set (or a tracker emitted as a labelled gauge). Cardinality concerns mean these probably want a separate, low-frequency exporter rather than per-call labels.
- **Agent personality** (avg function length, comment density). Depend on tree-sitter integration ([#10](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/10) follow-on).

## Updating

When new metrics ship in `internal/metrics/metrics.go`:

1. Add a panel referencing the metric.
2. Bump `version` at the top of the JSON.
3. Re-import via Grafana UI (the fixed `uid` makes this an in-place update).

Keep the JSON files **valid Grafana export format** — the easiest sanity check is `jq . agent-health.json | diff - agent-health.json` (the file should round-trip through `jq` cleanly).
