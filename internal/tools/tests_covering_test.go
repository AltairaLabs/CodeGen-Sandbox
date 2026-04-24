package tools_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callTestsCovering(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleTestsCovering(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

// seedCoverageIndex writes a synthetic profile + ingests tests so the
// index has data for the happy-path queries.
func seedCoverageIndex(t *testing.T, deps *tools.Deps) {
	t.Helper()
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "c.out")
	body := "mode: set\n" +
		"m/alpha/foo.go:10.1,20.5 5 1\n" +
		"m/alpha/bar.go:1.1,3.5 1 1\n" +
		"m/beta/qux.go:7.1,9.5 2 1\n"
	require.NoError(t, os.WriteFile(profilePath, []byte(body), 0o644))

	deps.CoverageIndex = tools.NewCoverageIndex()
	deps.CoverageIndex.Ingest(profilePath, map[string][]string{
		"m/alpha": {"TestAlpha1", "TestAlpha2"},
		"m/beta":  {"TestBetaOnly"},
	})
}

func TestTestsCovering_CoverageIndexNotConfigured(t *testing.T) {
	deps, _ := newTestDeps(t) // CoverageIndex left nil
	res := callTestsCovering(t, deps, map[string]any{"file": "alpha/foo.go"})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "coverage index not configured")
}

func TestTestsCovering_EmptyIndexIsFriendlyText(t *testing.T) {
	deps, _ := newTestDeps(t)
	deps.CoverageIndex = tools.NewCoverageIndex()
	res := callTestsCovering(t, deps, map[string]any{"file": "alpha/foo.go"})
	require.False(t, res.IsError)
	assert.Contains(t, textOf(t, res), "no coverage data yet")
}

func TestTestsCovering_MissingFileArg(t *testing.T) {
	deps, _ := newTestDeps(t)
	seedCoverageIndex(t, deps)
	res := callTestsCovering(t, deps, map[string]any{})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "file is required")
}

func TestTestsCovering_BlankFileArg(t *testing.T) {
	deps, _ := newTestDeps(t)
	seedCoverageIndex(t, deps)
	res := callTestsCovering(t, deps, map[string]any{"file": "   "})
	assert.True(t, res.IsError)
}

func TestTestsCovering_FileNotFound(t *testing.T) {
	deps, _ := newTestDeps(t)
	seedCoverageIndex(t, deps)
	res := callTestsCovering(t, deps, map[string]any{"file": "does/not/exist.go"})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "no coverage found for does/not/exist.go")
	assert.NotContains(t, body, ":")
}

func TestTestsCovering_LineScopedNoMatch(t *testing.T) {
	deps, _ := newTestDeps(t)
	seedCoverageIndex(t, deps)
	// alpha/foo.go covers lines 10..20. Line 50 is outside the range.
	res := callTestsCovering(t, deps, map[string]any{
		"file": "alpha/foo.go",
		"line": float64(50),
	})
	require.False(t, res.IsError)
	assert.Contains(t, textOf(t, res), "no coverage found for alpha/foo.go:50")
}

func TestTestsCovering_HappyPath_AnyLine(t *testing.T) {
	deps, _ := newTestDeps(t)
	seedCoverageIndex(t, deps)
	res := callTestsCovering(t, deps, map[string]any{"file": "alpha/foo.go"})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "2 test(s) cover alpha/foo.go")
	assert.Contains(t, body, "m/alpha:")
	assert.Contains(t, body, "TestAlpha1")
	assert.Contains(t, body, "TestAlpha2")
	// beta tests must not appear — they live in a different package.
	assert.NotContains(t, body, "TestBetaOnly")
}

func TestTestsCovering_HappyPath_LineScoped(t *testing.T) {
	deps, _ := newTestDeps(t)
	seedCoverageIndex(t, deps)
	res := callTestsCovering(t, deps, map[string]any{
		"file": "alpha/foo.go",
		"line": float64(15), // inside [10, 20]
	})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "2 test(s) cover alpha/foo.go:15")
	assert.Contains(t, body, "TestAlpha1")
}

func TestTestsCovering_PackageGroupingSortsAlphabetically(t *testing.T) {
	// A single source file covered by tests from TWO packages (simulates
	// test-in-package-a referencing a subroutine whose source happens to
	// live at a shared file path — unusual but indexable).
	deps, _ := newTestDeps(t)
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "c.out")
	body := "mode: set\n" +
		"m/z/shared.go:1.1,10.5 3 1\n" +
		"m/a/shared.go:1.1,10.5 3 1\n"
	require.NoError(t, os.WriteFile(profilePath, []byte(body), 0o644))
	deps.CoverageIndex = tools.NewCoverageIndex()
	deps.CoverageIndex.Ingest(profilePath, map[string][]string{
		"m/z": {"TestZ"},
		"m/a": {"TestA"},
	})

	res := callTestsCovering(t, deps, map[string]any{"file": "shared.go"})
	require.False(t, res.IsError)
	body2 := textOf(t, res)
	// "m/a:" section must appear before "m/z:" section.
	idxA := strings.Index(body2, "m/a:")
	idxZ := strings.Index(body2, "m/z:")
	require.GreaterOrEqual(t, idxA, 0)
	require.GreaterOrEqual(t, idxZ, 0)
	assert.Less(t, idxA, idxZ, "packages should be sorted alphabetically")
}

func TestTestsCovering_TruncatesAt200(t *testing.T) {
	// Build a profile with 1 file + 250 tests in a single package.
	deps, _ := newTestDeps(t)
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "c.out")
	require.NoError(t, os.WriteFile(profilePath,
		[]byte("mode: set\nm/big/main.go:1.1,5.5 1 1\n"), 0o644))
	tests := make([]string, 0, 250)
	for i := 0; i < 250; i++ {
		tests = append(tests, fmt.Sprintf("TestN%03d", i))
	}
	deps.CoverageIndex = tools.NewCoverageIndex()
	deps.CoverageIndex.Ingest(profilePath, map[string][]string{
		"m/big": tests,
	})

	res := callTestsCovering(t, deps, map[string]any{"file": "big/main.go"})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "250 test(s) cover")
	assert.Contains(t, body, "... (50 more truncated)")
}

func TestTestsCovering_ZeroLineMeansAnyLine(t *testing.T) {
	// Agents pass line=0 explicitly sometimes; it should be treated the
	// same as omitting the arg (any line).
	deps, _ := newTestDeps(t)
	seedCoverageIndex(t, deps)
	res := callTestsCovering(t, deps, map[string]any{
		"file": "alpha/foo.go",
		"line": float64(0),
	})
	require.False(t, res.IsError)
	body := textOf(t, res)
	// Header should NOT include ":0" — 0 is treated as "no line filter".
	assert.Contains(t, body, "cover alpha/foo.go:\n")
	assert.NotContains(t, body, "alpha/foo.go:0")
}
