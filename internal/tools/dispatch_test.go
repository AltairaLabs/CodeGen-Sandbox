package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedGoMarker / seedNodeMarker / seedPythonMarker / seedRustMarker
// write just enough to satisfy verify.DetectAll without trying to build
// anything — the dispatch tests want to probe Detector selection, not run
// real toolchains.
func seedGoMarker(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644))
}

func seedNodeMarker(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"probe"}`), 0o644))
}

func seedPythonMarker(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\nname='probe'\n"), 0o644))
}

func seedRustMarker(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[package]\nname='probe'\n"), 0o644))
}

// Unit-style dispatch coverage exercised through HandleRunTypecheck —
// the simplest tool that hits dispatchByLanguage (no store, no coverage,
// no rerun logic). Each test case verifies one branch of the polyglot
// policy surfaces the expected MCP result.

// callDispatchProbe calls run_typecheck as a thin probe into
// dispatchByLanguage. A separate name avoids colliding with the
// existing callRunTypecheck helper in run_typecheck_test.go.
func callDispatchProbe(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleRunTypecheck(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestDispatch_NoMarkerIsError(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callDispatchProbe(t, deps, map[string]any{})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "no supported project detected")
}

func TestDispatch_SingleLanguageNoHintUsesSoleDetector(t *testing.T) {
	deps, root := newTestDeps(t)
	seedGoMarker(t, root)
	// typecheck will fail to spawn `go vet` on a dummy module, but the
	// dispatch path either (a) hits the spawn and returns a non-dispatch
	// error or (b) succeeds — either way the response MUST NOT be the
	// polyglot-ambiguity error.
	res := callDispatchProbe(t, deps, map[string]any{})
	assert.NotContains(t, textOf(t, res), "polyglot workspace")
	assert.NotContains(t, textOf(t, res), "not detected")
}

func TestDispatch_PolyglotNoHintErrorsWithLanguageList(t *testing.T) {
	deps, root := newTestDeps(t)
	seedGoMarker(t, root)
	seedNodeMarker(t, root)
	res := callDispatchProbe(t, deps, map[string]any{})
	assert.True(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "polyglot workspace")
	assert.Contains(t, body, "pass `language`")
	assert.Contains(t, body, "go")
	assert.Contains(t, body, "node")
}

func TestDispatch_PolyglotFourLanguagesErrorListsAllFour(t *testing.T) {
	deps, root := newTestDeps(t)
	seedGoMarker(t, root)
	seedNodeMarker(t, root)
	seedPythonMarker(t, root)
	seedRustMarker(t, root)
	res := callDispatchProbe(t, deps, map[string]any{})
	assert.True(t, res.IsError)
	body := textOf(t, res)
	for _, want := range []string{"go", "node", "python", "rust"} {
		assert.Contains(t, body, want, "expected language %q in error: %s", want, body)
	}
}

func TestDispatch_ExplicitHintSelectsCorrectDetector(t *testing.T) {
	deps, root := newTestDeps(t)
	seedGoMarker(t, root)
	seedNodeMarker(t, root)
	// Ask for Node specifically. The call may still fail downstream
	// (running `tsc` / `npm run typecheck` against an empty project)
	// but the dispatch MUST have picked the Node detector, not errored
	// on ambiguity.
	res := callDispatchProbe(t, deps, map[string]any{"language": "node"})
	assert.NotContains(t, textOf(t, res), "polyglot workspace")
	assert.NotContains(t, textOf(t, res), "not detected")
}

func TestDispatch_ExplicitHintCaseInsensitive(t *testing.T) {
	deps, root := newTestDeps(t)
	seedGoMarker(t, root)
	seedNodeMarker(t, root)
	// "GO" / "Go" / "  go  " should all resolve to the go detector.
	for _, hint := range []string{"GO", "Go", "  go  "} {
		t.Run(hint, func(t *testing.T) {
			res := callDispatchProbe(t, deps, map[string]any{"language": hint})
			assert.NotContains(t, textOf(t, res), "polyglot workspace")
			assert.NotContains(t, textOf(t, res), "not detected")
		})
	}
}

func TestDispatch_ExplicitHintNotDetectedIsError(t *testing.T) {
	deps, root := newTestDeps(t)
	seedGoMarker(t, root)
	res := callDispatchProbe(t, deps, map[string]any{"language": "rust"})
	assert.True(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, `language "rust" not detected`)
	assert.Contains(t, body, "detected: go")
}
