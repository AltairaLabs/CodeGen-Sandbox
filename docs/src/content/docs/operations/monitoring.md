---
title: Monitoring — Prometheus + Grafana
description: End-to-end setup for scraping the sandbox's Prometheus surface and importing the two canonical Grafana dashboards.
---

The sandbox exposes a [Prometheus surface](/operations/metrics/) on an optional listener. This page is the operator-facing end-to-end: get a Prometheus scraping the sandbox, import the two Grafana dashboards shipped in the repo, and act on what they show.

## At a glance

- Start the sandbox with `-metrics-addr=:9090` (or any port) to turn on the `/metrics` listener.
- Point a Prometheus scrape config at that port.
- Import `deploy/grafana/agent-health.json` + `deploy/grafana/agent-arena.json` into Grafana; pick your Prometheus datasource.

Full setup below.

## 1. Start the sandbox with metrics enabled

```bash
codegen-sandbox \
  -addr=:8080 \
  -metrics-addr=:9090 \
  -workspace=/workspace
```

See [Metrics](/operations/metrics/) for every knob the metrics listener accepts, and for what each emitted metric family means.

## 2. Scrape from Prometheus

Minimal static-config scrape:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: codegen-sandbox
    scrape_interval: 30s
    static_configs:
      - targets: ["codegen-sandbox:9090"]
```

Kubernetes / `PodMonitor`:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: codegen-sandbox
spec:
  selector:
    matchLabels:
      app: codegen-sandbox
  podMetricsEndpoints:
    - port: metrics  # matches the containerPort named "metrics" on 9090
      interval: 30s
```

The `/metrics` listener is unauthenticated on purpose — restrict access at the network layer (firewall / NetworkPolicy / mesh). See [Metrics § Enabling the endpoint](/operations/metrics/#enabling-the-endpoint) for the rationale.

## 3. Import the dashboards

Two JSON files live under [`deploy/grafana/`](https://github.com/AltairaLabs/CodeGen-Sandbox/tree/main/deploy/grafana):

| File | Audience | Purpose |
|---|---|---|
| `agent-health.json` | Operators / on-call | "Is my agent having a bad day?" |
| `agent-arena.json` | Sales demos, leadership, curiosity | "Look what's happening right now" |

In Grafana: **Dashboards → New → Import**. Upload the JSON (or paste its contents), then pick your Prometheus datasource for the `DS_PROMETHEUS` variable.

Both dashboards target **Grafana 11.x** (schema version 39) and use templated datasource variables so the same JSON imports cleanly across environments.

Full import recipes (including `curl`-based API import and dashboard-as-code via `grafana-cli`) are in the dashboard [README](https://github.com/AltairaLabs/CodeGen-Sandbox/blob/main/deploy/grafana/README.md).

## Agent Health — operator playbook

Each panel maps to a specific on-call action:

| Panel | What it tells you | Suggested action |
|---|---|---|
| **Time since last green** | Seconds since the last clean `run_tests` / `run_lint` / `run_typecheck`. | > 5 min: check the agent transcript. > 30 min: agent is probably stuck in a fix-one-thing-break-two cycle. |
| **Tool error rate** | Errored tool calls / total, rolling window (default 100 calls). | > 5%: check which tool is failing on the per-tool timeseries. > 20%: the agent is flailing — interrupt. |
| **Test-failure streak** | Consecutive `run_tests` whose failure count didn't decrease. | > 3: agent isn't making progress; > 6: probable thrash. Reset via a `snapshot_restore` or agent interrupt. |
| **Tool latency p95 / p99** | Per-tool latency histogram. | p95 > 5s on `run_tests` is normal; p95 > 5s on `Read` / `Write` / `Edit` is a signal (disk / network issue). |
| **Denylist hits (last 1h)** | Bash commands rejected by the denylist, grouped by matched token (`sudo`, `mkfs`, ...). | Any hit is worth a look. Pattern of hits = check the agent prompt; the sandbox is holding the line but the agent is probing. |
| **Scrub hits (last 1h)** | Scrub-middleware matches by pattern. | Pattern spikes = content with that shape is flowing through. Good signal that scrub is earning its keep. |
| **Path-containment violations** | Rejected writes / reads that resolved outside the workspace. | Non-zero = the agent is trying to escape the sandbox. Investigate. |
| **Workspace size** | Bytes in the workspace volume (excluding `.git` / `node_modules`). | Unbounded growth = stuck build loop, leaked artefacts, or agent writing the same file over and over. |
| **Tool-repetition bursts** | (tool, args) tuples seen more than N times in a window. | Any entry = "ping-pong" signal — agent is calling the same thing repeatedly. Check the targeted file. |
| **Bash exit mix** | Bash foreground exit codes bucketed. | Dominant non-zero = builds / tests are failing; a sustained `exit=124` (timeout) row = runaway commands. |

## Agent Arena

Built for demos and curiosity, not alerting. Panels show:

- **Vibe** (stat, huge, green ↔ red) — `(1 − error_rate) × (1 − clamp(time_since_green / 600, 0, 1))`. Vibes-based by design; owns its unseriousness.
- **Sessions online**, **characters/minute vs average human typing speed** (overlaid reference line at ~400 cpm), **words per minute** (CPM ÷ 5).
- **Tools per minute (stacked)** — "busyness" chart showing which tools the agent is leaning on.
- **Read : write ratio** — the "measure twice, cut once" index. Higher = more deliberation per byte changed.
- **Dangerous-command attempts blocked** — lifetime denylist trophy case.
- **Language of the day** — donut chart of `sandbox_tool_calls_total{language}` over the last hour.
- **Scrub leaderboard** — most-caught secret type.

## What the dashboards deliberately don't cover

Some panels from the original [#29](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/29) sketch depend on metrics the sandbox doesn't yet expose:

- **Per-session breakouts** (top 10 sessions by time-since-green, ping-pong by file). Today's agent-health gauges are process-scoped, not session-scoped.
- **Most-grepped string / most-read file / largest write** bounded-label trackers.
- **Agent personality** (avg function length via tree-sitter, comment density).

They're parked for follow-up instrumentation rather than baked in as broken queries. See the dashboard [README](https://github.com/AltairaLabs/CodeGen-Sandbox/blob/main/deploy/grafana/README.md#panels-intentionally-not-included) for the full list.

## Related

- [Metrics](/operations/metrics/) — metric inventory, scrape configuration.
- [Agent health](/operations/agent-health/) — the thinking behind `sandbox_agent_*` gauges.
- [Tracing](/operations/tracing/) — OpenTelemetry complement to the Prometheus surface.
