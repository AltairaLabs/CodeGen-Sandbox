package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/metrics"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOperatorListener_MountsAllRoutes is the contract test for the public
// /metrics + /healthz + /readyz surface promised by operations/metrics.md. It
// runs the same factory that Run uses (buildMetricsServer), wraps the
// resulting Handler in an httptest server, and asserts every documented route
// answers 200. Any drift between the docs and the wired listener fails here
// — the failure mode is "kubelet probes 404 for weeks before someone
// notices", which is exactly what this test exists to prevent.
func TestOperatorListener_MountsAllRoutes(t *testing.T) {
	m, err := metrics.New()
	require.NoError(t, err)

	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)
	set := workspace.NewSingletonSet(ws)

	srv := buildMetricsServer("127.0.0.1:0", m, set)
	require.NotNil(t, srv, "buildMetricsServer returned nil with non-empty addr + non-nil metrics")
	require.NotNil(t, srv.Handler)

	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)

	for _, route := range []string{"/metrics", "/healthz", "/readyz"} {
		resp, err := http.Get(ts.URL + route)
		require.NoErrorf(t, err, "GET %s", route)
		_ = resp.Body.Close()
		assert.Equalf(t, http.StatusOK, resp.StatusCode, "GET %s should be 200", route)
	}
}

// TestOperatorListener_ReadyzFlipsOnVanishedWorkspace verifies the readiness
// probe escalates to 503 (not 200, not panic) when a workspace root
// disappears under the running process — k8s should take the pod out of
// rotation rather than keep routing requests at it.
func TestOperatorListener_ReadyzFlipsOnVanishedWorkspace(t *testing.T) {
	m, err := metrics.New()
	require.NoError(t, err)

	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)
	set := workspace.NewSingletonSet(ws)

	srv := buildMetricsServer("127.0.0.1:0", m, set)
	require.NotNil(t, srv)

	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/readyz")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "readyz should be 200 with reachable workspace")

	require.NoError(t, os.RemoveAll(dir))

	resp, err = http.Get(ts.URL + "/readyz")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "readyz should flip to 503 once workspace is gone")
	assert.Contains(t, string(body), ws.Name(), "503 body should name the failing workspace")
}

// TestOperatorListener_NilWhenDisabled documents that buildMetricsServer
// returns nil when MetricsAddr is empty — the same condition that disables
// /metrics also disables /healthz and /readyz, since they share the
// listener. Operators wanting probes must enable -metrics-addr.
func TestOperatorListener_NilWhenDisabled(t *testing.T) {
	srv := buildMetricsServer("", nil, nil)
	assert.Nil(t, srv)
}
