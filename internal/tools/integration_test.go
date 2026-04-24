//go:build integration

// Integration tests that exercise the real `go test -json` path through
// HandleRunTests + HandleLastTestFailures. Unlike the package-level unit
// suite this tier runs against a seeded module on disk with the actual
// Go toolchain, so regressions in the test2json parser or the result
// store's propagation surface here rather than in the next engineer's
// agent session.

package tools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireGoIntegration(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH; skipping real-toolchain integration test")
	}
}

// newIntegrationDeps builds a Deps wired to a fresh workspace + a
// TestResultStore (so HandleRunTests actually populates it for the
// follow-up HandleLastTestFailures call).
func newIntegrationDeps(t *testing.T) (*tools.Deps, string) {
	t.Helper()
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)
	return &tools.Deps{
		Workspace:   ws,
		Tracker:     workspace.NewReadTracker(),
		TestResults: tools.NewTestResultStore(),
	}, ws.Root()
}

// seedFailingGoModule writes a minimal Go module with a deliberately
// failing test so the run_tests → last_test_failures chain has
// something structured to parse.
func seedFailingGoModule(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module probe\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "probe.go"),
		[]byte("package probe\n\nfunc Add(a, b int) int { return a - b }\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "probe_test.go"),
		[]byte("package probe\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatalf(\"Add(1,2)=%d want 3\", Add(1,2))\n\t}\n}\n"), 0o644))
}

func callHandler(
	t *testing.T,
	h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error),
	args map[string]any,
) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	require.NoError(t, err)
	return res
}

func integrationText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// TestRealGoTestJSON_PopulatesStore runs the real Go toolchain against a
// seeded failing module and confirms that (a) HandleRunTests writes a
// TestResult into the store and (b) HandleLastTestFailures surfaces a
// structured failure whose TestName mentions TestAdd.
func TestRealGoTestJSON_PopulatesStore(t *testing.T) {
	requireGoIntegration(t)
	deps, root := newIntegrationDeps(t)
	seedFailingGoModule(t, root)

	runRes := callHandler(t, tools.HandleRunTests(deps), map[string]any{"timeout": float64(120)})
	require.False(t, runRes.IsError, "run_tests surfaced MCP error — want structured failure: %s",
		integrationText(t, runRes))

	body := integrationText(t, runRes)
	assert.Contains(t, body, "TestAdd", "run_tests body should mention the failing test")
	assert.NotContains(t, body, "exit: 0", "run_tests should not report exit 0 on a failing module")

	failRes := callHandler(t, tools.HandleLastTestFailures(deps), map[string]any{})
	require.False(t, failRes.IsError)
	failBody := integrationText(t, failRes)
	assert.Contains(t, failBody, "test failure(s)", "last_test_failures missing header: %s", failBody)
	assert.Contains(t, failBody, "TestAdd", "last_test_failures missing TestAdd: %s", failBody)
}
