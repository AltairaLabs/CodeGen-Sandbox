package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callRender exercises the render_mermaid / render_dot tools via their
// registration path, so the test covers tool metadata (name, descriptions)
// as well as the handler body.
func callRender(t *testing.T, deps *tools.Deps, toolName string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	reg := &fakeToolRegistrar{}
	tools.RegisterRender(reg, deps)
	handler, ok := reg.handlers[toolName]
	require.True(t, ok, "tool %q not registered", toolName)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := handler(context.Background(), req)
	require.NoError(t, err)
	return res
}

// renderBothTools returns the two tool names the Register call exposes,
// for table-driven tests that should exercise both.
func renderBothTools() []string { return []string{"render_mermaid", "render_dot"} }

func TestRender_MissingSourceIsError(t *testing.T) {
	deps, _ := newTestDeps(t)
	for _, name := range renderBothTools() {
		t.Run(name, func(t *testing.T) {
			res := callRender(t, deps, name, map[string]any{"output_path": "out.svg"})
			assert.True(t, res.IsError)
			assert.Contains(t, textOf(t, res), "source is required")
		})
	}
}

func TestRender_MissingOutputPathIsError(t *testing.T) {
	deps, _ := newTestDeps(t)
	for _, name := range renderBothTools() {
		t.Run(name, func(t *testing.T) {
			res := callRender(t, deps, name, map[string]any{"source": "graph LR\nA-->B"})
			assert.True(t, res.IsError)
			assert.Contains(t, textOf(t, res), "output_path is required")
		})
	}
}

func TestRender_SourceSizeCapIsError(t *testing.T) {
	deps, _ := newTestDeps(t)
	big := strings.Repeat("x", 1024*1024+1)
	for _, name := range renderBothTools() {
		t.Run(name, func(t *testing.T) {
			res := callRender(t, deps, name, map[string]any{"source": big, "output_path": "out.svg"})
			assert.True(t, res.IsError)
			assert.Contains(t, textOf(t, res), "exceeds cap")
		})
	}
}

func TestRender_UnsupportedExtensionIsError(t *testing.T) {
	deps, _ := newTestDeps(t)
	for _, name := range renderBothTools() {
		t.Run(name, func(t *testing.T) {
			res := callRender(t, deps, name, map[string]any{
				"source":      "graph LR\nA-->B",
				"output_path": "out.jpeg",
			})
			assert.True(t, res.IsError)
			body := textOf(t, res)
			assert.Contains(t, body, "unsupported output extension")
			assert.Contains(t, body, ".svg")
		})
	}
}

func TestRender_NoExtensionIsError(t *testing.T) {
	deps, _ := newTestDeps(t)
	for _, name := range renderBothTools() {
		t.Run(name, func(t *testing.T) {
			res := callRender(t, deps, name, map[string]any{
				"source":      "graph LR\nA-->B",
				"output_path": "diagram",
			})
			assert.True(t, res.IsError)
			assert.Contains(t, textOf(t, res), "unsupported output extension")
		})
	}
}

func TestRender_OutputOutsideWorkspaceIsError(t *testing.T) {
	deps, _ := newTestDeps(t)
	for _, name := range renderBothTools() {
		t.Run(name, func(t *testing.T) {
			res := callRender(t, deps, name, map[string]any{
				"source":      "graph LR\nA-->B",
				"output_path": "/etc/hacked.svg",
			})
			assert.True(t, res.IsError)
			assert.Contains(t, textOf(t, res), "resolve path")
		})
	}
}

func TestRender_OutputIsExistingDirectoryIsError(t *testing.T) {
	deps, root := newTestDeps(t)
	dirAsOutput := filepath.Join(root, "artifacts.svg")
	require.NoError(t, os.MkdirAll(dirAsOutput, 0o755))
	for _, name := range renderBothTools() {
		t.Run(name, func(t *testing.T) {
			res := callRender(t, deps, name, map[string]any{
				"source":      "graph LR\nA-->B",
				"output_path": "artifacts.svg",
			})
			assert.True(t, res.IsError)
			assert.Contains(t, textOf(t, res), "is a directory")
		})
	}
}

// TestRender_MissingBinaryNamesIt locks in the actionable "not on PATH"
// error. We empty PATH inside the test (t.Setenv auto-restores) so both
// mmdc and dot fail the lookup without needing a matching-but-broken
// binary in the test environment.
func TestRender_MissingBinaryNamesIt(t *testing.T) {
	deps, _ := newTestDeps(t)
	t.Setenv("PATH", t.TempDir())

	cases := []struct {
		tool string
		want string
	}{
		{"render_mermaid", "mermaid-cli (mmdc) not found on PATH"},
		{"render_dot", "graphviz (dot) not found on PATH"},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			res := callRender(t, deps, tc.tool, map[string]any{
				"source":      "graph LR\nA-->B",
				"output_path": "out.svg",
			})
			assert.True(t, res.IsError, "expected error result, got: %s", textOf(t, res))
			body := textOf(t, res)
			assert.Contains(t, body, tc.want)
			assert.Contains(t, body, "codegen-sandbox-tools-render")
		})
	}
}

func TestLookupRenderBinaries_MissingReturnsSentinels(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	assert.ErrorIs(t, tools.LookupMermaidCLI(), tools.ErrMermaidCLIMissing)
	assert.ErrorIs(t, tools.LookupGraphvizDot(), tools.ErrGraphvizDotMissing)
}

// fakeToolRegistrar captures handlers so tests can invoke them by tool
// name — lets each render_* test dispatch through the same Register
// function the server uses, rather than calling the handler directly
// and skipping tool-registration coverage.
type fakeToolRegistrar struct {
	handlers map[string]mcpserver.ToolHandlerFunc
}

func (f *fakeToolRegistrar) AddTool(tool mcp.Tool, handler mcpserver.ToolHandlerFunc) {
	if f.handlers == nil {
		f.handlers = map[string]mcpserver.ToolHandlerFunc{}
	}
	f.handlers[tool.Name] = handler
}
