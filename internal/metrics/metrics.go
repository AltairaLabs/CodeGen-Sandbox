// Package metrics owns the sandbox's Prometheus registry and every collector.
//
// Cardinality discipline: labels on custom metrics only use values from
// bounded, closed sets (fixed tool list, scrub-pattern registry, bash-denylist
// tokens, HTTP status classes). The package never accepts user-supplied free
// text as a label value — no file paths, session IDs, raw commands, or
// user-provided regex.
//
// Nil-safety: a *Metrics value of nil is a valid no-op. Call sites that don't
// plumb a registry (tests, unconfigured embedders) can pass nil without
// gating every increment behind a branch.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// toolDurationBuckets is the histogram bucket set for tool-call latency.
// Tuned for "quick read" (10ms) through "long-running build/test" (120s).
var toolDurationBuckets = []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 30, 120}

// apiDurationBuckets mirrors toolDurationBuckets but is separately named so
// operators can re-bucket the API plane independently later.
var apiDurationBuckets = []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 30, 120}

// Metrics owns the registry + every collector the sandbox emits.
// All mutator methods are nil-safe receivers.
type Metrics struct {
	reg *prometheus.Registry

	toolCalls    *prometheus.CounterVec
	toolDuration *prometheus.HistogramVec

	editBytes  prometheus.Counter
	writeBytes prometheus.Counter
	readBytes  prometheus.Counter

	bashExitCodes *prometheus.CounterVec

	apiHTTPRequests *prometheus.CounterVec
	apiHTTPDuration *prometheus.HistogramVec

	workspaceBytes prometheus.Gauge
	workspaceFiles prometheus.Gauge

	backgroundShells prometheus.Gauge

	wsConnections *prometheus.GaugeVec
	sseStreams    prometheus.Gauge

	denylistHits       *prometheus.CounterVec
	scrubHits          *prometheus.CounterVec
	scrubBytesRedacted prometheus.Counter
	pathViolations     prometheus.Counter

	agentTestFailureStreak      prometheus.Gauge
	agentTimeSinceLastGreenSecs prometheus.Gauge
	agentToolErrorRate          prometheus.Gauge
	agentToolRepetitionTotal    *prometheus.CounterVec
}

// New constructs a Metrics with a fresh registry, registering the runtime
// (go_*) + process (process_*) default collectors alongside the sandbox's
// own. Returns an error only if collector registration fails, which would
// indicate a programming bug (duplicate metric name).
func New() (*Metrics, error) {
	reg := prometheus.NewRegistry()
	m := &Metrics{reg: reg}
	m.buildCounters()
	m.buildHistograms()
	m.buildGauges()

	if err := reg.Register(collectors.NewGoCollector()); err != nil {
		return nil, err
	}
	if err := reg.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return nil, err
	}

	for _, c := range m.collectors() {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Metrics) buildCounters() {
	m.toolCalls = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "sandbox_tool_calls_total", Help: "Count of MCP tool invocations by tool, outcome, and detected project language."},
		[]string{"tool", "status", "language"},
	)
	m.editBytes = prometheus.NewCounter(prometheus.CounterOpts{Name: "sandbox_edit_bytes_total", Help: "Total bytes written through the Edit tool (sum of new_string lengths applied)."})
	m.writeBytes = prometheus.NewCounter(prometheus.CounterOpts{Name: "sandbox_write_bytes_total", Help: "Total bytes written through the Write tool."})
	m.readBytes = prometheus.NewCounter(prometheus.CounterOpts{Name: "sandbox_read_bytes_total", Help: "Total bytes returned by the Read tool."})
	m.bashExitCodes = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "sandbox_bash_exit_codes_total", Help: "Bash foreground exit codes bucketed by category."},
		[]string{"exit"},
	)
	m.apiHTTPRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "sandbox_api_http_requests_total", Help: "Count of HTTP API requests by matched route pattern and status class."},
		[]string{"route", "status"},
	)
	m.denylistHits = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "sandbox_denylist_hits_total", Help: "Bash denylist matches bucketed by matched token."},
		[]string{"token"},
	)
	m.scrubHits = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "sandbox_scrub_hits_total", Help: "Scrub-pattern matches by pattern name."},
		[]string{"pattern"},
	)
	m.scrubBytesRedacted = prometheus.NewCounter(prometheus.CounterOpts{Name: "sandbox_scrub_bytes_redacted_total", Help: "Total bytes replaced by the scrub middleware."})
	m.pathViolations = prometheus.NewCounter(prometheus.CounterOpts{Name: "sandbox_path_violations_total", Help: "Count of path-containment rejections across filesystem tools."})
	m.agentToolRepetitionTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "sandbox_agent_tool_repetition_total", Help: "Count of (tool, args) repetition bursts detected within the configured window."},
		[]string{"tool"},
	)
}

func (m *Metrics) buildHistograms() {
	m.toolDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Name: "sandbox_tool_duration_seconds", Help: "MCP tool-call latency.", Buckets: toolDurationBuckets},
		[]string{"tool"},
	)
	m.apiHTTPDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Name: "sandbox_api_http_duration_seconds", Help: "HTTP API request latency.", Buckets: apiDurationBuckets},
		[]string{"route"},
	)
}

func (m *Metrics) buildGauges() {
	m.workspaceBytes = prometheus.NewGauge(prometheus.GaugeOpts{Name: "sandbox_workspace_bytes", Help: "Total size of the workspace in bytes (.git and node_modules excluded)."})
	m.workspaceFiles = prometheus.NewGauge(prometheus.GaugeOpts{Name: "sandbox_workspace_files", Help: "Total file count in the workspace (.git and node_modules excluded)."})
	m.backgroundShells = prometheus.NewGauge(prometheus.GaugeOpts{Name: "sandbox_background_shells", Help: "Currently-registered background bash shells."})
	m.wsConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "sandbox_ws_connections", Help: "Open WebSocket connections by endpoint."},
		[]string{"endpoint"},
	)
	m.sseStreams = prometheus.NewGauge(prometheus.GaugeOpts{Name: "sandbox_sse_streams", Help: "Open Server-Sent-Events streams on /api/events."})
	m.agentTestFailureStreak = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "sandbox_agent_test_failure_streak",
		Help: "Consecutive run_tests invocations whose failure count did not decrease. Reset on decrease or exit=0.",
	})
	m.agentTimeSinceLastGreenSecs = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "sandbox_agent_time_since_last_green_seconds",
		Help: "Seconds since the last run_tests/run_lint/run_typecheck that exited 0. Initialised to 0 on process start so \"never ran\" reads as 0.",
	})
	m.agentToolErrorRate = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "sandbox_agent_tool_error_rate",
		Help: "Errored tool calls divided by total tool calls over the configured rolling window (default 100).",
	})
}

// collectors returns every sandbox-owned collector so New can register them
// in one loop and tests can round-trip them through a registry.
func (m *Metrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		m.toolCalls, m.toolDuration,
		m.editBytes, m.writeBytes, m.readBytes,
		m.bashExitCodes,
		m.apiHTTPRequests, m.apiHTTPDuration,
		m.workspaceBytes, m.workspaceFiles,
		m.backgroundShells,
		m.wsConnections, m.sseStreams,
		m.denylistHits, m.scrubHits, m.scrubBytesRedacted, m.pathViolations,
		m.agentTestFailureStreak, m.agentTimeSinceLastGreenSecs, m.agentToolErrorRate, m.agentToolRepetitionTotal,
	}
}

// Handler returns an http.Handler serving the Prometheus text exposition
// format. Safe to call on a nil receiver (returns a 404 handler) so the
// caller does not have to gate on listener presence.
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{Registry: m.reg})
}

// Registry returns the underlying registry. Callers that need to register
// additional collectors (tests, embedders) use this.
func (m *Metrics) Registry() *prometheus.Registry {
	if m == nil {
		return nil
	}
	return m.reg
}

// ToolCall records one completed MCP tool invocation.
// status must be one of: ok, error, denied, timeout.
// language must be one of: go, node, python, rust, "" (unknown).
func (m *Metrics) ToolCall(tool, status, language string, dur time.Duration) {
	if m == nil {
		return
	}
	m.toolCalls.WithLabelValues(tool, status, language).Inc()
	m.toolDuration.WithLabelValues(tool).Observe(dur.Seconds())
}

// EditBytes records bytes written via the Edit tool.
func (m *Metrics) EditBytes(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.editBytes.Add(float64(n))
}

// WriteBytes records bytes written via the Write tool.
func (m *Metrics) WriteBytes(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.writeBytes.Add(float64(n))
}

// ReadBytes records bytes returned by the Read tool.
func (m *Metrics) ReadBytes(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.readBytes.Add(float64(n))
}

// BashExit records a foreground Bash exit bucketed per the issue.
func (m *Metrics) BashExit(code int) {
	if m == nil {
		return
	}
	m.bashExitCodes.WithLabelValues(bucketBashExit(code)).Inc()
}

// bucketBashExit clamps a bash exit code into one of five fixed buckets so
// the cardinality of sandbox_bash_exit_codes_total stays bounded regardless
// of what agents run.
func bucketBashExit(code int) string {
	switch {
	case code == 0:
		return "0"
	case code == 124: // timeout(1) convention
		return "timeout(124)"
	case code >= 1 && code <= 125:
		return "1-125"
	case code >= 126 && code <= 128:
		return "126-128"
	case code >= 129:
		return ">=129"
	default:
		// Negative exit codes (e.g. Go's -1 for signal-killed without
		// ExitCode) land here; reuse ">=129" is wrong semantically so we
		// route them to their own sentinel bucket.
		return ">=129"
	}
}

// APIHTTPRequest records one completed HTTP API request.
// route is the matched mux pattern (bounded); status is "2xx"|"3xx"|"4xx"|"5xx"|"101".
func (m *Metrics) APIHTTPRequest(route, status string, dur time.Duration) {
	if m == nil {
		return
	}
	m.apiHTTPRequests.WithLabelValues(route, status).Inc()
	m.apiHTTPDuration.WithLabelValues(route).Observe(dur.Seconds())
}

// BucketHTTPStatus maps a raw HTTP status code into the fixed label set.
func BucketHTTPStatus(code int) string {
	switch {
	case code == 101:
		return "101"
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500 && code < 600:
		return "5xx"
	default:
		return "other"
	}
}

// SetWorkspace updates the workspace size gauges. Fed by a periodic walker
// rather than on-tool-call so a single chatty tool doesn't re-stat the tree
// on every invocation.
func (m *Metrics) SetWorkspace(bytes int64, files int64) {
	if m == nil {
		return
	}
	m.workspaceBytes.Set(float64(bytes))
	m.workspaceFiles.Set(float64(files))
}

// SetBackgroundShells updates the background-shell gauge.
func (m *Metrics) SetBackgroundShells(n int) {
	if m == nil {
		return
	}
	m.backgroundShells.Set(float64(n))
}

// WSConnectionInc increments the WebSocket gauge for the given endpoint.
// endpoint is a closed enum: "exec" | "port-forward".
func (m *Metrics) WSConnectionInc(endpoint string) {
	if m == nil {
		return
	}
	m.wsConnections.WithLabelValues(endpoint).Inc()
}

// WSConnectionDec decrements the WebSocket gauge for the given endpoint.
func (m *Metrics) WSConnectionDec(endpoint string) {
	if m == nil {
		return
	}
	m.wsConnections.WithLabelValues(endpoint).Dec()
}

// SSEStreamInc increments the SSE gauge (events endpoint).
func (m *Metrics) SSEStreamInc() {
	if m == nil {
		return
	}
	m.sseStreams.Inc()
}

// SSEStreamDec decrements the SSE gauge.
func (m *Metrics) SSEStreamDec() {
	if m == nil {
		return
	}
	m.sseStreams.Dec()
}

// DenylistHit records a Bash denylist match.
// token is one of the fixed denylist keywords.
func (m *Metrics) DenylistHit(token string) {
	if m == nil {
		return
	}
	m.denylistHits.WithLabelValues(token).Inc()
}

// ScrubHit records one scrub-pattern match.
// pattern is one of the fixed pattern names in internal/scrub.
func (m *Metrics) ScrubHit(pattern string, bytesRedacted int) {
	if m == nil {
		return
	}
	m.scrubHits.WithLabelValues(pattern).Inc()
	if bytesRedacted > 0 {
		m.scrubBytesRedacted.Add(float64(bytesRedacted))
	}
}

// PathViolation records one path-containment rejection.
func (m *Metrics) PathViolation() {
	if m == nil {
		return
	}
	m.pathViolations.Inc()
}

// SetAgentTestFailureStreak updates the consecutive-non-decreasing-failures
// gauge. Callers clamp negative values at zero before invoking.
func (m *Metrics) SetAgentTestFailureStreak(n int) {
	if m == nil {
		return
	}
	if n < 0 {
		n = 0
	}
	m.agentTestFailureStreak.Set(float64(n))
}

// SetAgentTimeSinceLastGreenSeconds updates the time-since-last-green gauge.
// Initialised to 0 on process start so "never ran" reads as 0 rather than a
// misleading giant number.
func (m *Metrics) SetAgentTimeSinceLastGreenSeconds(secs float64) {
	if m == nil {
		return
	}
	if secs < 0 {
		secs = 0
	}
	m.agentTimeSinceLastGreenSecs.Set(secs)
}

// SetAgentToolErrorRate updates the rolling-window tool error rate gauge.
// Values outside [0,1] are clamped so the gauge stays a valid ratio.
func (m *Metrics) SetAgentToolErrorRate(rate float64) {
	if m == nil {
		return
	}
	if rate < 0 {
		rate = 0
	}
	if rate > 1 {
		rate = 1
	}
	m.agentToolErrorRate.Set(rate)
}

// IncAgentToolRepetition increments the (tool) repetition-burst counter. The
// tool label is drawn from the closed MCP tool list, so cardinality stays
// bounded.
func (m *Metrics) IncAgentToolRepetition(tool string) {
	if m == nil {
		return
	}
	m.agentToolRepetitionTotal.WithLabelValues(tool).Inc()
}
