package metrics_test

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetricsContract_DocsMatchScrape is a bidirectional guard against silent
// drift between docs/operations/metrics.md (+ docs/operations/agent-health.md)
// and the actual /metrics scrape output. Either direction failing means the
// inventory the operator can read no longer matches what the process actually
// emits — which is the failure mode that Grafana panels go quiet under
// without anyone noticing until someone asks.
//
// Direction 1 (docs → scrape): every documented family appears in the scrape.
//
// Direction 2 (scrape → docs): every emitted sandbox_* family is documented.
// Standard go_* / process_* runtime collectors are excluded — those come from
// prometheus/client_golang and aren't on the sandbox's documentation surface.
func TestMetricsContract_DocsMatchScrape(t *testing.T) {
	documented := loadDocumentedSandboxMetrics(t)
	require.NotEmpty(t, documented, "no sandbox_* metrics extracted from docs — markdown table format may have drifted")

	emitted := scrapeAllSandboxMetricFamilies(t)
	require.NotEmpty(t, emitted, "scrape returned no sandbox_* metric families — exposition format change?")

	// docs → scrape
	for _, name := range documented {
		_, ok := emitted[name]
		assert.True(t, ok, "metric %q is documented but not emitted by /metrics; either the collector was dropped or the docs are stale", name)
	}

	// scrape → docs
	docSet := make(map[string]struct{}, len(documented))
	for _, n := range documented {
		docSet[n] = struct{}{}
	}
	for name := range emitted {
		if _, ok := docSet[name]; !ok {
			assert.Failf(t, "undocumented metric", "metric %q is emitted by /metrics but not documented in operations/metrics.md or operations/agent-health.md; either remove the collector or document it", name)
		}
	}
}

// exerciseAllSandboxCounters touches every method on the *Metrics surface so
// that all collectors have observed at least one sample. A counter that has
// never been incremented isn't reported via /metrics in the text exposition
// format with the default registry options unless it has labels with at least
// one observed series — so for label-bearing counters we have to call them
// with at least one fixed label value to make them appear.
func exerciseAllSandboxCounters(m *metrics.Metrics) {
	// Tool plane.
	m.ToolCall("Read", "ok", "go", 10*time.Millisecond)
	m.ReadBytes(1)
	m.WriteBytes(1)
	m.EditBytes(1)
	m.BashExit(0)

	// HTTP API plane.
	m.APIHTTPRequest("/api/tree", metrics.BucketHTTPStatus(200), time.Millisecond)

	// Resource plane.
	m.SetWorkspace(1, 1)
	m.SetBackgroundShells(0)
	m.WSConnectionInc("exec")
	m.WSConnectionDec("exec")
	m.SSEStreamInc()
	m.SSEStreamDec()

	// Security plane.
	m.DenylistHit("sudo")
	m.ScrubHit("aws-access-key", 1)
	m.PathViolation()

	// Agent-health plane.
	m.SetAgentTestFailureStreak(0)
	m.SetAgentTimeSinceLastGreenSeconds(0)
	m.SetAgentToolErrorRate(0)
	m.IncAgentToolRepetition("Read")
}

// scrapeAllSandboxMetricFamilies builds a fresh Metrics, exercises every
// method, then parses the scrape body into a name→present map filtered to
// sandbox_* families.
//
// The text exposition format prefixes each family with a `# HELP <name>` line
// (followed by `# TYPE <name> <type>` and the sample lines). Reading off the
// HELP lines gives us a stable, version-independent way to enumerate emitted
// families: the prometheus/common expfmt parser also works but its newer
// versions require setting a name-validation scheme globally and panic
// otherwise, which would couple this contract test to package init order.
func scrapeAllSandboxMetricFamilies(t *testing.T) map[string]struct{} {
	t.Helper()
	m, err := metrics.New()
	require.NoError(t, err)
	exerciseAllSandboxCounters(m)

	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	out := make(map[string]struct{})
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		const helpPrefix = "# HELP "
		if !strings.HasPrefix(line, helpPrefix) {
			continue
		}
		rest := strings.TrimPrefix(line, helpPrefix)
		// rest is "<name> <help text>"; split on first space.
		idx := strings.IndexByte(rest, ' ')
		if idx <= 0 {
			continue
		}
		name := rest[:idx]
		if strings.HasPrefix(name, "sandbox_") {
			out[name] = struct{}{}
		}
	}
	require.NoError(t, scanner.Err())
	return out
}

// docsMetricRow extracts a sandbox_* metric name from the first cell of a
// markdown table row. Table cells in both metrics.md and agent-health.md
// wrap the metric name in backticks; we anchor on `| ` plus a backtick to
// avoid catching prose mentions like "the sandbox_tool_calls_total counter".
//
// One caveat: histogram families are documented under their base name (e.g.
// sandbox_tool_duration_seconds), and the scrape output exposes the same
// base name as the MetricFamily — expfmt rolls _bucket / _sum / _count under
// the family. So no special handling is needed beyond the regex.
var docsMetricRow = regexp.MustCompile("\\|\\s*`(sandbox_[a-z0-9_]+)`")

// loadDocumentedSandboxMetrics walks the two ops markdown files that
// publicly enumerate metric names. The slice is sorted + de-duplicated so
// the contract test reports a stable order on failure.
func loadDocumentedSandboxMetrics(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	paths := []string{
		filepath.Join(root, "docs", "src", "content", "docs", "operations", "metrics.md"),
		filepath.Join(root, "docs", "src", "content", "docs", "operations", "agent-health.md"),
	}
	seen := make(map[string]struct{})
	for _, p := range paths {
		body, err := os.ReadFile(p)
		require.NoErrorf(t, err, "read %s", p)
		for _, m := range docsMetricRow.FindAllStringSubmatch(string(body), -1) {
			seen[m[1]] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// repoRoot resolves the repo root from this test file's location. Going up
// two levels from internal/metrics/ lands on the module root; we double-check
// by looking for go.mod so the test fails cleanly if the package layout
// moves rather than reading a stale directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		require.NoErrorf(t, err, "go.mod not found at expected repo root %s", root)
	}
	return root
}

// TestMetricsContract_DocsRegexCatchesEveryTableRow guards the scraper for
// the docs-side parse: if a future docs edit moves metric names out of
// backticks (or out of a leading `|` cell), this regex will silently miss
// them. The check runs the regex against a known-good fragment so a
// regression in the regex itself fails fast.
func TestMetricsContract_DocsRegexCatchesEveryTableRow(t *testing.T) {
	const sample = "| `sandbox_tool_calls_total` | counter | `tool` |\n" +
		"| `sandbox_tool_duration_seconds` | histogram | `tool` |\n"
	got := docsMetricRow.FindAllStringSubmatch(sample, -1)
	require.Len(t, got, 2)
	assert.Equal(t, "sandbox_tool_calls_total", got[0][1])
	assert.Equal(t, "sandbox_tool_duration_seconds", got[1][1])
}

// TestMetricsContract_NilMetricsHandlerStaysSilent makes sure the contract
// suite plays well with the nil-receiver path on Metrics — a /metrics fetch
// against a nil receiver must 404, not panic, so dashboards probing a
// disabled instance get a clear signal rather than a connection reset.
func TestMetricsContract_NilMetricsHandlerStaysSilent(t *testing.T) {
	var m *metrics.Metrics
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "panic")
}
