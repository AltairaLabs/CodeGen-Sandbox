package tools_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/metrics"
	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newDepsWithMetrics mirrors newTestDeps but plumbs a real *metrics.Metrics
// so we can scrape counters.
func newDepsWithMetrics(t *testing.T) (*tools.Deps, string, *metrics.Metrics) {
	t.Helper()
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)
	m, err := metrics.New()
	require.NoError(t, err)
	return &tools.Deps{
		Workspace: ws,
		Tracker:   workspace.NewReadTracker(),
		Shells:    tools.NewShellRegistry(),
		Metrics:   m,
	}, ws.Root(), m
}

func TestRead_RecordsReadBytes(t *testing.T) {
	deps, root, m := newDepsWithMetrics(t)
	path := filepath.Join(root, "hello.txt")
	require.NoError(t, os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"file_path": path}
	res, err := tools.HandleRead(deps)(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError)

	body := scrape(t, m)
	// Exact bytes = rendered numbered body length; here we just assert the
	// counter has been incremented (non-zero) which implicitly proves the
	// hook fires.
	assert.NotContains(t, body, "sandbox_read_bytes_total 0\n")
}

func TestWrite_RecordsWriteBytes(t *testing.T) {
	deps, root, m := newDepsWithMetrics(t)
	path := filepath.Join(root, "w.txt")

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"file_path": path, "content": "hello-metrics"}
	res, err := tools.HandleWrite(deps)(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError)

	body := scrape(t, m)
	assert.Contains(t, body, "sandbox_write_bytes_total 13")
}

func TestBash_RecordsExitCode(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	deps, _, m := newDepsWithMetrics(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"command": "exit 7", "description": "bash exit"}
	res, err := tools.HandleBash(deps)(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError)

	body := scrape(t, m)
	assert.Contains(t, body, `sandbox_bash_exit_codes_total{exit="1-125"} 1`)
}

func TestBash_DenylistRecordsHit(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	deps, _, m := newDepsWithMetrics(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"command": "sudo whoami", "description": "denied"}
	res, err := tools.HandleBash(deps)(context.Background(), req)
	require.NoError(t, err)
	require.True(t, res.IsError)

	body := scrape(t, m)
	assert.Contains(t, body, `sandbox_denylist_hits_total{token="sudo"} 1`)
}

func TestBash_DenylistMkfsVariantsCollapseToSingleToken(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	deps, _, m := newDepsWithMetrics(t)

	for _, cmd := range []string{"mkfs.ext4 /dev/null", "mkfs.xfs /dev/null", "mkfs /dev/null"} {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{"command": cmd, "description": "denied"}
		_, err := tools.HandleBash(deps)(context.Background(), req)
		require.NoError(t, err)
	}

	body := scrape(t, m)
	// Cardinality invariant: mkfs.ext4 / mkfs.xfs / mkfs all map to "mkfs".
	assert.Contains(t, body, `sandbox_denylist_hits_total{token="mkfs"} 3`)
}

func TestRead_PathViolationRecorded(t *testing.T) {
	deps, _, m := newDepsWithMetrics(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"file_path": "/etc/passwd"}
	res, err := tools.HandleRead(deps)(context.Background(), req)
	require.NoError(t, err)
	require.True(t, res.IsError)

	body := scrape(t, m)
	assert.Contains(t, body, "sandbox_path_violations_total 1")
}

func TestSnapshotCreate_MetricsCompat(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	// Smoke test: snapshot_create with a *metrics.Metrics in Deps must not
	// panic. The snapshot handler is not directly instrumented (only the
	// generic tool-call wrapper is), so this locks in the "nil-safe /
	// real-metrics both work" contract at the handler level.
	deps, _, _ := newDepsWithMetrics(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"name": "metricsSmoke"}
	assert.NotPanics(t, func() {
		_, _ = tools.HandleSnapshotCreate(deps)(context.Background(), req)
	})
}

func scrape(t *testing.T, m *metrics.Metrics) string {
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
