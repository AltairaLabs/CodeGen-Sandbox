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

// fakeBinaryOnPath plants an executable shell script named `name` in a
// fresh tempdir, prepends that dir to PATH for the duration of the test
// (t.Setenv auto-restores), and returns the bin dir. The script lets
// render_* unit tests exercise the runMermaid / runDot subprocess paths
// (tempfile creation, stdin piping, stderr capture, output size, timeout)
// without needing the heavy real mmdc + Chromium runtime installed.
func fakeBinaryOnPath(t *testing.T, name, script string) string {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, name)
	require.NoError(t, os.WriteFile(binPath, []byte(script), 0o755)) //nolint:gosec // test-only fake
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return binDir
}

func TestRender_FakeMmdc_SuccessReturnsBytes(t *testing.T) {
	deps, root := newTestDeps(t)
	// Fake mmdc: copy the -i input file to the -o output, mirroring real
	// mmdc behaviour just enough to verify the runMermaid plumbing
	// (tempfile creation + cleanup + cmd.Run + post-size check).
	fakeBinaryOnPath(t, "mmdc", `#!/bin/sh
set -e
in=""
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -i) in="$2"; shift 2 ;;
    -o) out="$2"; shift 2 ;;
    -p) shift 2 ;;
    *) shift ;;
  esac
done
cp "$in" "$out"
`)
	res := callRender(t, deps, "render_mermaid", map[string]any{
		"source":      "graph LR\nA-->B",
		"output_path": "diagram.svg",
	})
	require.False(t, res.IsError, "render_mermaid surfaced error: %s", textOf(t, res))
	body, err := os.ReadFile(filepath.Join(root, "diagram.svg")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, "graph LR\nA-->B", string(body))
	assert.Contains(t, textOf(t, res), "wrote 14 bytes")
	assert.Contains(t, textOf(t, res), "(svg)")
}

func TestRender_FakeDot_SuccessReadsStdin(t *testing.T) {
	deps, root := newTestDeps(t)
	// Fake dot: copy stdin to the -o output. Verifies that runDot pipes
	// source via stdin (no temp file) and that -T<format> + -o flags are
	// passed through.
	fakeBinaryOnPath(t, "dot", `#!/bin/sh
set -e
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -T*) shift ;;
    *) shift ;;
  esac
done
cat > "$out"
`)
	res := callRender(t, deps, "render_dot", map[string]any{
		"source":      "digraph G { a -> b }",
		"output_path": "graph.png",
	})
	require.False(t, res.IsError, "render_dot surfaced error: %s", textOf(t, res))
	body, err := os.ReadFile(filepath.Join(root, "graph.png")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, "digraph G { a -> b }", string(body))
	assert.Contains(t, textOf(t, res), "(png)")
}

func TestRender_FakeMmdc_NonZeroExitSurfacesStderr(t *testing.T) {
	deps, _ := newTestDeps(t)
	fakeBinaryOnPath(t, "mmdc", `#!/bin/sh
echo "syntax error on line 42" 1>&2
exit 1
`)
	res := callRender(t, deps, "render_mermaid", map[string]any{
		"source":      "this is not mermaid",
		"output_path": "broken.svg",
	})
	assert.True(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "render_mermaid:")
	assert.Contains(t, body, "syntax error on line 42")
}

func TestRender_FakeDot_NonZeroExitSurfacesStderr(t *testing.T) {
	deps, _ := newTestDeps(t)
	fakeBinaryOnPath(t, "dot", `#!/bin/sh
cat >/dev/null
echo "syntax error: stray { on line 1" 1>&2
exit 1
`)
	res := callRender(t, deps, "render_dot", map[string]any{
		"source":      "not a valid dot file",
		"output_path": "broken.svg",
	})
	assert.True(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "render_dot:")
	assert.Contains(t, body, "syntax error: stray {")
}

func TestRender_OutputSizeCapDeletesAndErrors(t *testing.T) {
	deps, root := newTestDeps(t)
	// Fake dot: write a payload larger than maxRenderOutputBytes (10 MiB).
	// Verifies the post-subprocess size cap deletes the runaway artifact
	// and surfaces an actionable error.
	fakeBinaryOnPath(t, "dot", `#!/bin/sh
set -e
cat >/dev/null
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    *) shift ;;
  esac
done
# 11 MiB of zeros — over the 10 MiB cap.
dd if=/dev/zero of="$out" bs=1048576 count=11 status=none
`)
	res := callRender(t, deps, "render_dot", map[string]any{
		"source":      "digraph G {}",
		"output_path": "huge.svg",
	})
	assert.True(t, res.IsError, "expected size-cap error, got: %s", textOf(t, res))
	assert.Contains(t, textOf(t, res), "exceeds cap")
	// Output should have been removed by the size-cap path.
	_, err := os.Stat(filepath.Join(root, "huge.svg"))
	assert.True(t, os.IsNotExist(err), "expected output to be deleted, got err=%v", err)
}

func TestRender_TimeoutKillsSubprocess(t *testing.T) {
	deps, root := newTestDeps(t)
	// Fake dot: sleep 30s. With timeout=1 the subprocess must be killed
	// and the call must return a "timed out after 1s" error.
	fakeBinaryOnPath(t, "dot", `#!/bin/sh
cat >/dev/null
sleep 30
`)
	res := callRender(t, deps, "render_dot", map[string]any{
		"source":      "digraph G {}",
		"output_path": "slow.svg",
		"timeout":     float64(1),
	})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "timed out after 1s")
	// And no partial output remains.
	_, err := os.Stat(filepath.Join(root, "slow.svg"))
	assert.True(t, os.IsNotExist(err), "expected no partial output, got err=%v", err)
}

func TestRender_SuccessMarksOutputRead(t *testing.T) {
	deps, root := newTestDeps(t)
	fakeBinaryOnPath(t, "mmdc", `#!/bin/sh
set -e
out=""
while [ $# -gt 0 ]; do
  case "$1" in -o) out="$2"; shift 2 ;; *) shift ;; esac
done
echo svg > "$out"
`)
	res := callRender(t, deps, "render_mermaid", map[string]any{
		"source":      "graph LR\nA-->B",
		"output_path": "ok.svg",
	})
	require.False(t, res.IsError, "render_mermaid surfaced error: %s", textOf(t, res))
	abs := filepath.Join(root, "ok.svg")
	assert.True(t, deps.Tracker.HasBeenRead(abs), "expected render output to be marked Read so the agent can overwrite without a prior Read")
}
