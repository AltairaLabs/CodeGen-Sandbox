//go:build integration

// Integration tests that drive the real `mmdc` / `dot` binaries through
// the render_mermaid / render_dot MCP tools. Unlike the unit suite in
// render_test.go, this tier writes actual SVG files to a tempdir and
// grep-asserts the SVG prelude — so regressions in argument wiring or
// subprocess plumbing surface here rather than in a PromptKit session.
//
// Each case skips when its binary isn't on PATH, so the file stays safe
// to run on machines that only have a subset of the render layer installed.

package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireMermaidCLI(t *testing.T) {
	t.Helper()
	if err := tools.LookupMermaidCLI(); err != nil {
		t.Skipf("skipping: %v", err)
	}
}

func requireGraphvizDot(t *testing.T) {
	t.Helper()
	if err := tools.LookupGraphvizDot(); err != nil {
		t.Skipf("skipping: %v", err)
	}
}

func TestRenderMermaid_Integration_SVGRoundTrip(t *testing.T) {
	requireMermaidCLI(t)
	deps, root := newTestDeps(t)

	reg := &fakeToolRegistrar{}
	tools.RegisterRender(reg, deps)
	h := reg.handlers["render_mermaid"]
	require.NotNil(t, h)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"source":      "graph LR\n  A[client] --> B[sandbox] --> C[(workspace)]\n",
		"output_path": "diagram.svg",
	}
	res, err := h(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError, "render_mermaid returned error: %s", integrationText(t, res))

	outPath := filepath.Join(root, "diagram.svg")
	body, err := os.ReadFile(outPath) //nolint:gosec // test path, not attacker-controlled
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(body), "<svg"), "mermaid output missing <svg: %q", string(body)[:minInt(len(body), 120)])
	assert.Greater(t, len(body), 100, "mermaid output suspiciously small: %d bytes", len(body))
}

func TestRenderDot_Integration_SVGRoundTrip(t *testing.T) {
	requireGraphvizDot(t)
	deps, root := newTestDeps(t)

	reg := &fakeToolRegistrar{}
	tools.RegisterRender(reg, deps)
	h := reg.handlers["render_dot"]
	require.NotNil(t, h)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"source":      "digraph G { rankdir=LR; client -> sandbox -> workspace; }\n",
		"output_path": "graph.svg",
	}
	res, err := h(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError, "render_dot returned error: %s", integrationText(t, res))

	outPath := filepath.Join(root, "graph.svg")
	body, err := os.ReadFile(outPath) //nolint:gosec // test path, not attacker-controlled
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(body), "<svg"), "dot output missing <svg: %q", string(body)[:minInt(len(body), 120)])
	assert.Greater(t, len(body), 100, "dot output suspiciously small: %d bytes", len(body))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
