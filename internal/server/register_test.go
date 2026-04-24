package server

import (
	"sort"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingToolAdder captures registered tool names so tests can assert
// the set the server exposes in each mode without spinning up the SSE
// transport and round-tripping a tools/list JSON-RPC request.
type recordingToolAdder struct {
	names []string
}

func (r *recordingToolAdder) AddTool(tool mcp.Tool, _ mcpserver.ToolHandlerFunc) {
	r.names = append(r.names, tool.Name)
}

func (r *recordingToolAdder) sorted() []string {
	out := append([]string(nil), r.names...)
	sort.Strings(out)
	return out
}

func newRegisterDeps(t *testing.T) *tools.Deps {
	t.Helper()
	ws, err := workspace.New(t.TempDir())
	require.NoError(t, err)
	return &tools.Deps{
		Workspace:     ws,
		Tracker:       workspace.NewReadTracker(),
		Shells:        tools.NewShellRegistry(),
		TestResults:   tools.NewTestResultStore(),
		CoverageIndex: tools.NewCoverageIndex(),
	}
}

// readOnlyToolNames is the contract of which tools must be available in
// read-only mode. Pinned in code so a refactor that accidentally drops
// (or adds) a tool to the read-only surface fails this test rather than
// silently changing what subagents can do.
var readOnlyToolNames = []string{
	"Glob",
	"Grep",
	"Read",
	"find_definition",
	"find_references",
	"last_test_failures",
	"search_code",
	"secret",
	"secrets_available",
	"snapshot_diff",
	"snapshot_list",
	"tests_covering",
}

// mutatingToolNames is the additional set registered when ReadOnly is
// false. Both sets together must equal the full surface — see
// TestRegisterToolsForMode_NoToolLeftBehind below for the exhaustiveness
// check.
var mutatingToolNames = []string{
	"Bash",
	"BashOutput",
	"Edit",
	"KillShell",
	"Write",
	"change_function_signature",
	"edit_function_body",
	"insert_after_method",
	"rename_symbol",
	"render_dot",
	"render_mermaid",
	"run_failing_tests",
	"run_lint",
	"run_script",
	"run_tests",
	"run_typecheck",
	"snapshot_create",
	"snapshot_restore",
}

func TestRegisterToolsForMode_ReadOnlyExposesOnlyReadTools(t *testing.T) {
	rec := &recordingToolAdder{}
	registerToolsForMode(rec, newRegisterDeps(t), true)
	assert.Equal(t, readOnlyToolNames, rec.sorted())
}

func TestRegisterToolsForMode_FullModeExposesEverything(t *testing.T) {
	rec := &recordingToolAdder{}
	registerToolsForMode(rec, newRegisterDeps(t), false)

	want := append([]string(nil), readOnlyToolNames...)
	want = append(want, mutatingToolNames...)
	sort.Strings(want)
	assert.Equal(t, want, rec.sorted())
}

// TestRegisterToolsForMode_ReadOnlyOmitsEveryMutator nails down the
// negative side of the contract: each entry in mutatingToolNames is
// individually absent from the read-only surface. Catches accidental
// inclusion of a write-capable tool in the read-only Register* halves.
func TestRegisterToolsForMode_ReadOnlyOmitsEveryMutator(t *testing.T) {
	rec := &recordingToolAdder{}
	registerToolsForMode(rec, newRegisterDeps(t), true)
	have := map[string]bool{}
	for _, n := range rec.names {
		have[n] = true
	}
	for _, n := range mutatingToolNames {
		assert.False(t, have[n], "read-only mode unexpectedly registered mutating tool %q", n)
	}
}

// TestRegisterToolsForMode_NoToolLeftBehind verifies that read-only +
// mutating sets together cover the full-mode set with no overlap and no
// gap — guards against a future Register* function being added without
// an explicit decision about its read/write classification.
func TestRegisterToolsForMode_NoToolLeftBehind(t *testing.T) {
	rec := &recordingToolAdder{}
	registerToolsForMode(rec, newRegisterDeps(t), false)
	full := map[string]bool{}
	for _, n := range rec.names {
		full[n] = true
	}
	declared := map[string]bool{}
	for _, n := range readOnlyToolNames {
		declared[n] = true
	}
	for _, n := range mutatingToolNames {
		assert.False(t, declared[n], "tool %q listed in both readOnlyToolNames and mutatingToolNames", n)
		declared[n] = true
	}
	for n := range full {
		assert.True(t, declared[n], "tool %q registered by registerToolsForMode but missing from readOnlyToolNames + mutatingToolNames in this test", n)
	}
	for n := range declared {
		assert.True(t, full[n], "tool %q listed in test contract but not registered by registerToolsForMode", n)
	}
}
