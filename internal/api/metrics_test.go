package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithMetrics_IncrementsRequestCounter(t *testing.T) {
	m, err := metrics.New()
	require.NoError(t, err)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	srv := httptest.NewServer(withMetrics(m, "/api/file", inner))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	body := scrapeMetrics(t, m)
	assert.Contains(t, body, `sandbox_api_http_requests_total{route="/api/file",status="4xx"} 1`)
	assert.Contains(t, body, `sandbox_api_http_duration_seconds_count{route="/api/file"} 1`)
}

func TestWithMetrics_DefaultsTo200OnImplicitWrite(t *testing.T) {
	m, err := metrics.New()
	require.NoError(t, err)

	// net/http defaults the first Write to 200; the recorder must match.
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	srv := httptest.NewServer(withMetrics(m, "/api/tree", inner))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	body := scrapeMetrics(t, m)
	assert.Contains(t, body, `sandbox_api_http_requests_total{route="/api/tree",status="2xx"} 1`)
}

func TestWithMetrics_NilMetricsIsPassthrough(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := withMetrics(nil, "/x", inner)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	assert.Equal(t, http.StatusTeapot, rr.Code)
}

func scrapeMetrics(t *testing.T, m *metrics.Metrics) string {
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

func TestWithMetrics_StatusRecorderForwardsHijack(t *testing.T) {
	// The wrapper must implement http.Hijacker via the underlying writer;
	// otherwise the WebSocket handlers (/api/exec, /api/port-forward) break.
	m, err := metrics.New()
	require.NoError(t, err)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("no hijacker"))
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(err.Error()))
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n\r\n"))
		_ = conn.Close()
	})

	srv := httptest.NewServer(withMetrics(m, "/api/exec", inner))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	body := scrapeMetrics(t, m)
	assert.True(t, strings.Contains(body, `sandbox_api_http_requests_total{route="/api/exec",status="101"} 1`),
		"missing 101 counter; got %q", body)
}
