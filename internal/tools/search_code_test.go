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

func callSearchCode(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleSearchCode(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestSearchCode_QueryRequired(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callSearchCode(t, deps, map[string]any{})
	assert.True(t, res.IsError)
}

func TestSearchCode_EmptyQuery(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callSearchCode(t, deps, map[string]any{"query": "   "})
	assert.True(t, res.IsError)
}

func TestSearchCode_GoOnlyMessageForEmptyWorkspace(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callSearchCode(t, deps, map[string]any{"query": "anything"})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "no Go files found")
}

func TestSearchCode_FindsGoSymbol(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte(`package p

// FileHandler serves raw bytes; caps at 2 MiB. Returns 500 on unexpected errors.
func FileHandler() {}
`), 0o644))

	res := callSearchCode(t, deps, map[string]any{"query": "serves bytes"})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "FileHandler")
	assert.Contains(t, body, "a.go")
	assert.Contains(t, body, "results for")
}

func TestSearchCode_NoResultsMessage(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\nfunc Present() {}\n"), 0o644))

	res := callSearchCode(t, deps, map[string]any{"query": "nonsenseabsolutelynothing"})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "no results for")
}

func TestSearchCode_LimitClamped(t *testing.T) {
	deps, root := newTestDeps(t)
	var sb strings.Builder
	sb.WriteString("package p\n")
	for i := 0; i < 10; i++ {
		sb.WriteString("func Alpha")
		sb.WriteByte(byte('A' + i))
		sb.WriteString("() {}\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte(sb.String()), 0o644))

	res := callSearchCode(t, deps, map[string]any{"query": "alpha", "limit": float64(2)})
	require.False(t, res.IsError)
	body := textOf(t, res)
	// Enumerated results are "1." "2." ... count only these.
	assert.Contains(t, body, "1.")
	assert.Contains(t, body, "2.")
	assert.NotContains(t, body, "3.")
}

func TestSearchCode_FirstCallShowsIndexBuildNote(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\nfunc OnlyOne() {}\n"), 0o644))

	res := callSearchCode(t, deps, map[string]any{"query": "OnlyOne"})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "first call:")
}

func TestSearchCode_SecondCallNoBuildNote(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\nfunc OnlyOne() {}\n"), 0o644))

	_ = callSearchCode(t, deps, map[string]any{"query": "OnlyOne"})
	res := callSearchCode(t, deps, map[string]any{"query": "OnlyOne"})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.NotContains(t, body, "first call:")
}
