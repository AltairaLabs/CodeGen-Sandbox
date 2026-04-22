package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireRg(t *testing.T) {
	t.Helper()
	if err := tools.LookupRipgrep(); err != nil {
		t.Skip("ripgrep (rg) not installed; skipping")
	}
}

func callGlob(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleGlob(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestGlob_MatchesPattern(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.go"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "c.txt"), []byte("x"), 0o644))

	res := callGlob(t, deps, map[string]any{"pattern": "**/*.go"})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "a.go")
	assert.Contains(t, body, "b.go")
	assert.NotContains(t, body, "c.txt")
}

func TestGlob_RespectsGitignore(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.go\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "kept.go"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ignored.go"), []byte("x"), 0o644))

	res := callGlob(t, deps, map[string]any{"pattern": "**/*.go"})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "kept.go")
	assert.NotContains(t, body, "ignored.go")
}

func TestGlob_SortsByMtimeDesc(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	older := filepath.Join(root, "older.go")
	newer := filepath.Join(root, "newer.go")
	require.NoError(t, os.WriteFile(older, []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(newer, []byte("x"), 0o644))

	// Force older file's mtime to be clearly earlier.
	past := time.Now().Add(-1 * time.Hour)
	require.NoError(t, os.Chtimes(older, past, past))

	res := callGlob(t, deps, map[string]any{"pattern": "**/*.go"})
	require.False(t, res.IsError)

	lines := strings.Split(strings.TrimRight(textOf(t, res), "\n"), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	assert.Equal(t, "newer.go", lines[0])
	assert.Equal(t, "older.go", lines[1])
}

func TestGlob_EqualMtimeTiebreaksLexicographic(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)

	b := filepath.Join(root, "b.go")
	a := filepath.Join(root, "a.go")
	require.NoError(t, os.WriteFile(b, []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(a, []byte("x"), 0o644))

	// Force identical mtimes so the sort must fall through to the path tiebreak.
	same := time.Now()
	require.NoError(t, os.Chtimes(a, same, same))
	require.NoError(t, os.Chtimes(b, same, same))

	res := callGlob(t, deps, map[string]any{"pattern": "**/*.go"})
	require.False(t, res.IsError)

	lines := strings.Split(strings.TrimRight(textOf(t, res), "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "a.go", lines[0])
	assert.Equal(t, "b.go", lines[1])
}

func TestGlob_PathArgScopesSearch(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	sub := filepath.Join(root, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "top.go"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "nested.go"), []byte("x"), 0o644))

	res := callGlob(t, deps, map[string]any{"pattern": "**/*.go", "path": "sub"})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "nested.go")
	assert.NotContains(t, body, "top.go")
}

func TestGlob_PathOutsideWorkspace(t *testing.T) {
	requireRg(t)
	deps, _ := newTestDeps(t)
	res := callGlob(t, deps, map[string]any{"pattern": "**/*", "path": "/etc"})
	assert.True(t, res.IsError)
}

func TestGlob_MissingPattern(t *testing.T) {
	requireRg(t)
	deps, _ := newTestDeps(t)
	res := callGlob(t, deps, map[string]any{})
	assert.True(t, res.IsError)
}

func TestGlob_NoMatchesReturnsEmpty(t *testing.T) {
	requireRg(t)
	deps, _ := newTestDeps(t)

	res := callGlob(t, deps, map[string]any{"pattern": "**/*.impossible-extension"})
	require.False(t, res.IsError)
	assert.Empty(t, strings.TrimSpace(textOf(t, res)))
}

func TestGlob_LimitTruncates(t *testing.T) {
	requireRg(t)
	deps, root := newTestDeps(t)
	for i := 0; i < 5; i++ {
		p := filepath.Join(root, strings.Repeat("x", i+1)+".go")
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
	}

	res := callGlob(t, deps, map[string]any{"pattern": "**/*.go", "limit": float64(2)})
	require.False(t, res.IsError)

	lines := strings.Split(strings.TrimRight(textOf(t, res), "\n"), "\n")
	assert.Len(t, lines, 2)
}
