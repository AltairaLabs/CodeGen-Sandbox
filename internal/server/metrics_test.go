package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/metrics"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScrubbingHandler_RecordsHitAndBytes(t *testing.T) {
	m, err := metrics.New()
	require.NoError(t, err)

	inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("token=AKIAIOSFODNN7EXAMPLE"), nil
	}
	wrapped := scrubbingHandler(inner, m)

	_, err = wrapped(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)

	body := scrapeMetricsBody(t, m)
	assert.Contains(t, body, `sandbox_scrub_hits_total{pattern="aws-access-key"} 1`)
	// Exact byte count depends on the REDACTED replacement length, but a
	// non-zero delta confirms the counter got wired.
	assert.NotContains(t, body, "sandbox_scrub_bytes_redacted_total 0")
}

func TestMetricsMiddleware_ObservesDurationAndStatus(t *testing.T) {
	m, err := metrics.New()
	require.NoError(t, err)

	ok := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}
	fail := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("boom"), nil
	}

	wrappedOK := metricsMiddleware(m, "Read", nil, ok)
	wrappedFail := metricsMiddleware(m, "Read", nil, fail)

	_, _ = wrappedOK(context.Background(), mcp.CallToolRequest{})
	_, _ = wrappedOK(context.Background(), mcp.CallToolRequest{})
	_, _ = wrappedFail(context.Background(), mcp.CallToolRequest{})

	body := scrapeMetricsBody(t, m)
	assert.Contains(t, body, `sandbox_tool_calls_total{language="",status="ok",tool="Read"} 2`)
	assert.Contains(t, body, `sandbox_tool_calls_total{language="",status="error",tool="Read"} 1`)
	assert.Contains(t, body, `sandbox_tool_duration_seconds_count{tool="Read"} 3`)
}

func scrapeMetricsBody(t *testing.T, m *metrics.Metrics) string {
	t.Helper()
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}
