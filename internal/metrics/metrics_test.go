package metrics_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_RegistersExpectedFamilies(t *testing.T) {
	m, err := metrics.New()
	require.NoError(t, err)
	require.NotNil(t, m)

	body := scrapeBody(t, m)
	// The Go + process collectors ship a fixed set of well-known names; their
	// presence proves the default collectors were registered alongside
	// sandbox-specific families.
	assert.Contains(t, body, "go_goroutines")
	assert.Contains(t, body, "process_cpu_seconds_total")
}

func TestToolCall_IncrementsCounterAndObservesHistogram(t *testing.T) {
	m, err := metrics.New()
	require.NoError(t, err)

	m.ToolCall("Read", "ok", "go", 150*time.Millisecond)
	m.ToolCall("Read", "ok", "go", 200*time.Millisecond)
	m.ToolCall("Bash", "error", "", 50*time.Millisecond)

	body := scrapeBody(t, m)
	assert.Contains(t, body, `sandbox_tool_calls_total{language="go",status="ok",tool="Read"} 2`)
	assert.Contains(t, body, `sandbox_tool_calls_total{language="",status="error",tool="Bash"} 1`)
	assert.Contains(t, body, `sandbox_tool_duration_seconds_bucket{tool="Read",le="0.5"} 2`)
}

func TestBashExit_Buckets(t *testing.T) {
	m, err := metrics.New()
	require.NoError(t, err)

	m.BashExit(0)
	m.BashExit(1)
	m.BashExit(125)
	m.BashExit(124)
	m.BashExit(127)
	m.BashExit(137)
	m.BashExit(-1)

	body := scrapeBody(t, m)
	assert.Contains(t, body, `sandbox_bash_exit_codes_total{exit="0"} 1`)
	assert.Contains(t, body, `sandbox_bash_exit_codes_total{exit="1-125"} 2`)
	assert.Contains(t, body, `sandbox_bash_exit_codes_total{exit="timeout(124)"} 1`)
	assert.Contains(t, body, `sandbox_bash_exit_codes_total{exit="126-128"} 1`)
	assert.Contains(t, body, `sandbox_bash_exit_codes_total{exit=">=129"} 2`)
}

func TestByteCounters_SumApplied(t *testing.T) {
	m, err := metrics.New()
	require.NoError(t, err)

	m.ReadBytes(100)
	m.ReadBytes(50)
	m.WriteBytes(400)
	m.EditBytes(25)
	// Non-positive values must be a no-op; a negative add to a prom Counter
	// would panic.
	m.ReadBytes(-1)
	m.ReadBytes(0)
	m.WriteBytes(0)
	m.EditBytes(0)

	body := scrapeBody(t, m)
	assert.Contains(t, body, "sandbox_read_bytes_total 150")
	assert.Contains(t, body, "sandbox_write_bytes_total 400")
	assert.Contains(t, body, "sandbox_edit_bytes_total 25")
}

func TestAPIHTTPRequest_BucketsStatus(t *testing.T) {
	m, err := metrics.New()
	require.NoError(t, err)

	m.APIHTTPRequest("/api/tree", metrics.BucketHTTPStatus(200), 10*time.Millisecond)
	m.APIHTTPRequest("/api/tree", metrics.BucketHTTPStatus(200), 15*time.Millisecond)
	m.APIHTTPRequest("/api/file", metrics.BucketHTTPStatus(404), 2*time.Millisecond)
	m.APIHTTPRequest("/api/exec", metrics.BucketHTTPStatus(101), 1*time.Millisecond)

	body := scrapeBody(t, m)
	assert.Contains(t, body, `sandbox_api_http_requests_total{route="/api/tree",status="2xx"} 2`)
	assert.Contains(t, body, `sandbox_api_http_requests_total{route="/api/file",status="4xx"} 1`)
	assert.Contains(t, body, `sandbox_api_http_requests_total{route="/api/exec",status="101"} 1`)
}

func TestBucketHTTPStatus_Exhaustive(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{101, "101"},
		{200, "2xx"},
		{204, "2xx"},
		{301, "3xx"},
		{404, "4xx"},
		{500, "5xx"},
		{599, "5xx"},
		{700, "other"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, metrics.BucketHTTPStatus(tc.code), "code=%d", tc.code)
	}
}

func TestGauges_SetIncDec(t *testing.T) {
	m, err := metrics.New()
	require.NoError(t, err)

	m.SetWorkspace(4096, 42)
	m.SetBackgroundShells(3)
	m.WSConnectionInc("exec")
	m.WSConnectionInc("exec")
	m.WSConnectionInc("port-forward")
	m.WSConnectionDec("exec")
	m.SSEStreamInc()
	m.SSEStreamInc()
	m.SSEStreamDec()

	body := scrapeBody(t, m)
	assert.Contains(t, body, "sandbox_workspace_bytes 4096")
	assert.Contains(t, body, "sandbox_workspace_files 42")
	assert.Contains(t, body, "sandbox_background_shells 3")
	assert.Contains(t, body, `sandbox_ws_connections{endpoint="exec"} 1`)
	assert.Contains(t, body, `sandbox_ws_connections{endpoint="port-forward"} 1`)
	assert.Contains(t, body, "sandbox_sse_streams 1")
}

func TestScrubAndDenylist_Hits(t *testing.T) {
	m, err := metrics.New()
	require.NoError(t, err)

	m.ScrubHit("aws-access-key", 20)
	m.ScrubHit("aws-access-key", 20)
	m.ScrubHit("github-pat", 42)
	m.DenylistHit("sudo")
	m.PathViolation()

	body := scrapeBody(t, m)
	assert.Contains(t, body, `sandbox_scrub_hits_total{pattern="aws-access-key"} 2`)
	assert.Contains(t, body, `sandbox_scrub_hits_total{pattern="github-pat"} 1`)
	assert.Contains(t, body, "sandbox_scrub_bytes_redacted_total 82")
	assert.Contains(t, body, `sandbox_denylist_hits_total{token="sudo"} 1`)
	assert.Contains(t, body, "sandbox_path_violations_total 1")
}

func TestAgentHealthMetrics_Propagate(t *testing.T) {
	m, err := metrics.New()
	require.NoError(t, err)

	m.SetAgentTestFailureStreak(4)
	m.SetAgentTimeSinceLastGreenSeconds(42)
	m.SetAgentToolErrorRate(0.5)
	m.IncAgentToolRepetition("Read")
	m.IncAgentToolRepetition("Read")
	m.IncAgentToolRepetition("Bash")

	body := scrapeBody(t, m)
	assert.Contains(t, body, "sandbox_agent_test_failure_streak 4")
	assert.Contains(t, body, "sandbox_agent_time_since_last_green_seconds 42")
	assert.Contains(t, body, "sandbox_agent_tool_error_rate 0.5")
	assert.Contains(t, body, `sandbox_agent_tool_repetition_total{tool="Read"} 2`)
	assert.Contains(t, body, `sandbox_agent_tool_repetition_total{tool="Bash"} 1`)
}

func TestAgentHealthMetrics_ClampNegativesAndRates(t *testing.T) {
	m, err := metrics.New()
	require.NoError(t, err)

	// Negative streak / time / rate below zero all clamp to zero; rate above
	// one clamps to one. The gauge values are then stable under scrape.
	m.SetAgentTestFailureStreak(-5)
	m.SetAgentTimeSinceLastGreenSeconds(-12)
	m.SetAgentToolErrorRate(1.5)

	body := scrapeBody(t, m)
	assert.Contains(t, body, "sandbox_agent_test_failure_streak 0")
	assert.Contains(t, body, "sandbox_agent_time_since_last_green_seconds 0")
	assert.Contains(t, body, "sandbox_agent_tool_error_rate 1")
}

func TestNilMetrics_EveryMethodIsNoop(t *testing.T) {
	// A nil *Metrics models "metrics disabled" — every method must be safe
	// so callers can embed a plain pointer field without a nil-guard on every
	// call site.
	var m *metrics.Metrics

	assert.NotPanics(t, func() {
		m.ToolCall("Read", "ok", "go", time.Second)
		m.EditBytes(1)
		m.WriteBytes(1)
		m.ReadBytes(1)
		m.BashExit(0)
		m.APIHTTPRequest("/x", "2xx", time.Second)
		m.SetWorkspace(1, 1)
		m.SetBackgroundShells(1)
		m.WSConnectionInc("exec")
		m.WSConnectionDec("exec")
		m.SSEStreamInc()
		m.SSEStreamDec()
		m.DenylistHit("sudo")
		m.ScrubHit("x", 1)
		m.PathViolation()
		m.SetAgentTestFailureStreak(3)
		m.SetAgentTimeSinceLastGreenSeconds(10)
		m.SetAgentToolErrorRate(0.25)
		m.IncAgentToolRepetition("Read")
	})

	// Nil Handler returns a 404 handler so embedders can unconditionally
	// mount it behind `/metrics` even when metrics are disabled.
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusNotFound, rr.Code)

	assert.Nil(t, m.Registry())
}

// scrapeBody hits the Prometheus handler via an httptest.Server and returns
// the exposition body. The indirection through a real server is deliberate —
// it catches content-type + HTTP wiring regressions that ServeHTTP + a
// recorder would let slip through.
func scrapeBody(t *testing.T, m *metrics.Metrics) string {
	t.Helper()
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}
