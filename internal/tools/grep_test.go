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

func callGrep(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleGrep(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestGrep_ContentMode(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("alpha\nbeta\nalpha again\n"), 0o644))

	res := callGrep(t, deps, map[string]any{"pattern": "alpha"})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "a.go:1:alpha")
	assert.Contains(t, body, "a.go:3:alpha again")
	assert.NotContains(t, body, "beta")
}

func TestGrep_FilesWithMatchesMode(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "has.go"), []byte("alpha\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "nope.go"), []byte("beta\n"), 0o644))

	res := callGrep(t, deps, map[string]any{"pattern": "alpha", "output_mode": "files_with_matches"})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "has.go")
	assert.NotContains(t, body, "nope.go")
}

func TestGrep_CountMode(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("alpha\nbeta\nalpha\n"), 0o644))

	res := callGrep(t, deps, map[string]any{"pattern": "alpha", "output_mode": "count"})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "a.go:2")
}

func TestGrep_RespectsGitignore(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.go\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "kept.go"), []byte("alpha\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ignored.go"), []byte("alpha\n"), 0o644))

	res := callGrep(t, deps, map[string]any{"pattern": "alpha"})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "kept.go:1:alpha")
	assert.NotContains(t, body, "ignored.go")
}

func TestGrep_CaseInsensitive(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("ALPHA\n"), 0o644))

	res := callGrep(t, deps, map[string]any{"pattern": "alpha", "case_insensitive": true})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "a.go:1:ALPHA")
}

func TestGrep_GlobFilter(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("alpha\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644))

	res := callGrep(t, deps, map[string]any{"pattern": "alpha", "glob": "*.go"})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "a.go")
	assert.NotContains(t, body, "a.txt")
}

func TestGrep_HeadLimit(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	var sb strings.Builder
	for i := 0; i < 10; i++ {
		sb.WriteString("alpha\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte(sb.String()), 0o644))

	res := callGrep(t, deps, map[string]any{"pattern": "alpha", "head_limit": float64(3)})
	require.False(t, res.IsError)

	body := textOf(t, res)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	assert.Len(t, lines, 3)
}

func TestGrep_NoMatchesReturnsEmpty(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("hello\n"), 0o644))

	res := callGrep(t, deps, map[string]any{"pattern": "zzzzz-no-such-pattern"})
	require.False(t, res.IsError)
	assert.Empty(t, strings.TrimSpace(textOf(t, res)))
}

func TestGrep_InvalidRegexIsError(t *testing.T) {
	requireRg(t)
	deps, _ := newTestDeps(t)

	res := callGrep(t, deps, map[string]any{"pattern": "[unclosed"})
	assert.True(t, res.IsError)
}

func TestGrep_PathOutsideWorkspace(t *testing.T) {
	requireRg(t)
	deps, _ := newTestDeps(t)

	res := callGrep(t, deps, map[string]any{"pattern": "x", "path": "/etc"})
	assert.True(t, res.IsError)
}

func TestGrep_MissingPattern(t *testing.T) {
	requireRg(t)
	deps, _ := newTestDeps(t)

	res := callGrep(t, deps, map[string]any{})
	assert.True(t, res.IsError)
}

func TestGrep_UnknownOutputMode(t *testing.T) {
	requireRg(t)
	deps, _ := newTestDeps(t)

	res := callGrep(t, deps, map[string]any{"pattern": "x", "output_mode": "nonsense"})
	assert.True(t, res.IsError)
}
