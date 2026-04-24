package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callRunFailing(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleRunFailingTests(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestRunFailingTests_StoreNotConfigured(t *testing.T) {
	deps, _ := newTestDeps(t) // no TestResults set
	res := callRunFailing(t, deps, map[string]any{})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "store not configured")
}

func TestRunFailingTests_NoPriorRun(t *testing.T) {
	deps, _ := newTestDeps(t)
	deps.TestResults = tools.NewTestResultStore()
	res := callRunFailing(t, deps, map[string]any{})
	require.False(t, res.IsError)
	assert.Contains(t, textOf(t, res), "no run_tests call yet")
}

func TestRunFailingTests_NoDetector(t *testing.T) {
	// Empty workspace — no go.mod, so Detect returns nil.
	deps, _ := newTestDeps(t)
	deps.TestResults = tools.NewTestResultStore()
	deps.TestResults.Set(tools.TestResult{
		Language:  "go",
		Failures:  []verify.TestFailure{{TestName: "p:TestX", Message: "boom"}},
		At:        time.Now(),
		Supported: true,
	})
	res := callRunFailing(t, deps, map[string]any{})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "no supported project detected")
}

func TestRunFailingTests_NoFailures(t *testing.T) {
	requireGo(t)
	deps, root := newTestDeps(t)
	seedGoModule(t, root, true) // so Detect finds go
	deps.TestResults = tools.NewTestResultStore()
	deps.TestResults.Set(tools.TestResult{
		Language:    "go",
		Failures:    nil,
		TestsPassed: 3,
		At:          time.Now(),
		Supported:   true,
	})
	res := callRunFailing(t, deps, map[string]any{})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "nothing to rerun")
	assert.Contains(t, body, "go")
}

func TestRunFailingTests_RerunsOnlyFailing(t *testing.T) {
	requireGo(t)
	deps, root := newTestDeps(t)
	// Seed a module with TWO tests — one passes, one fails. Ensures that
	// a subsequent rerun filter on only TestFailing executes TestFailing
	// and does NOT run TestPassing.
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "probe.go"), []byte("package probe\n"), 0o644))
	body := `package probe
import "testing"
func TestPassing(t *testing.T) {}
func TestFailing(t *testing.T) { t.Fatal("boom") }
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "probe_test.go"), []byte(body), 0o644))

	// Simulate a prior run_tests that reported one failure.
	deps.TestResults = tools.NewTestResultStore()
	deps.TestResults.Set(tools.TestResult{
		Language: "go",
		Failures: []verify.TestFailure{
			{TestName: "probe:TestFailing", Message: "boom"},
		},
		At:        time.Now(),
		Supported: true,
	})

	res := callRunFailing(t, deps, map[string]any{})
	require.False(t, res.IsError, "unexpected error: %s", textOf(t, res))
	out := textOf(t, res)
	assert.Contains(t, out, "TestFailing")
	assert.NotContains(t, out, "TestPassing", "rerun should not execute passing tests")

	// The store is overwritten with the rerun: last_test_failures now
	// surfaces TestFailing specifically.
	after, ok := deps.TestResults.Get()
	require.True(t, ok)
	require.Len(t, after.Failures, 1)
	assert.Contains(t, after.Failures[0].TestName, "TestFailing")
}

func TestComposeGoRerunArgv_EmptyFailures(t *testing.T) {
	argv := tools.ExportComposeGoRerunArgv(nil, 50)
	assert.Nil(t, argv)
}

func TestComposeGoRerunArgv_SingleFailureSinglePackage(t *testing.T) {
	argv := tools.ExportComposeGoRerunArgv([]verify.TestFailure{
		{TestName: "example.com/pkg/foo:TestAlpha"},
	}, 50)
	// Last arg is the package positional.
	assert.Equal(t, []string{
		"go", "test", "-json", "-count=1", "-run", "^(TestAlpha)$",
		"example.com/pkg/foo",
	}, argv)
}

func TestComposeGoRerunArgv_MultipleFailuresSamePackage(t *testing.T) {
	argv := tools.ExportComposeGoRerunArgv([]verify.TestFailure{
		{TestName: "pkg:TestBeta"},
		{TestName: "pkg:TestAlpha"},
	}, 50)
	// Regex members are sorted for determinism.
	assert.Contains(t, argv, "-run")
	idx := indexOf(argv, "-run")
	require.GreaterOrEqual(t, idx, 0)
	assert.Equal(t, "^(TestAlpha|TestBeta)$", argv[idx+1])
	assert.Equal(t, "pkg", argv[len(argv)-1])
}

func TestComposeGoRerunArgv_MultiplePackages(t *testing.T) {
	argv := tools.ExportComposeGoRerunArgv([]verify.TestFailure{
		{TestName: "pkg/a:TestA"},
		{TestName: "pkg/b:TestB"},
	}, 50)
	// Both packages present as positional args, sorted.
	assert.Equal(t, []string{
		"go", "test", "-json", "-count=1", "-run", "^(TestA|TestB)$",
		"pkg/a", "pkg/b",
	}, argv)
}

func TestComposeGoRerunArgv_SubtestsRerunParent(t *testing.T) {
	argv := tools.ExportComposeGoRerunArgv([]verify.TestFailure{
		{TestName: "pkg:TestParent/sub1"},
		{TestName: "pkg:TestParent/sub2"},
	}, 50)
	// Parent dedupes to a single entry.
	assert.Contains(t, strings.Join(argv, " "), "^(TestParent)$")
}

func TestComposeGoRerunArgv_DuplicatesCollapsed(t *testing.T) {
	argv := tools.ExportComposeGoRerunArgv([]verify.TestFailure{
		{TestName: "pkg:TestFoo"},
		{TestName: "pkg:TestFoo"},
		{TestName: "pkg:TestFoo"},
	}, 50)
	idx := indexOf(argv, "-run")
	require.GreaterOrEqual(t, idx, 0)
	assert.Equal(t, "^(TestFoo)$", argv[idx+1])
}

func TestComposeGoRerunArgv_LimitClamps(t *testing.T) {
	failures := make([]verify.TestFailure, 0, 5)
	for _, n := range []string{"TestA", "TestB", "TestC", "TestD", "TestE"} {
		failures = append(failures, verify.TestFailure{TestName: "pkg:" + n})
	}
	argv := tools.ExportComposeGoRerunArgv(failures, 2)
	idx := indexOf(argv, "-run")
	require.GreaterOrEqual(t, idx, 0)
	// Sorted alphabetically, first two are TestA and TestB.
	assert.Equal(t, "^(TestA|TestB)$", argv[idx+1])
}

func TestComposeGoRerunArgv_OverflowFallsBackToWildcard(t *testing.T) {
	// 11 distinct packages → falls back to ./...
	failures := make([]verify.TestFailure, 0, 11)
	for i := 0; i < 11; i++ {
		failures = append(failures, verify.TestFailure{
			TestName: "pkg" + string(rune('a'+i)) + ":TestIt",
		})
	}
	argv := tools.ExportComposeGoRerunArgv(failures, 50)
	assert.Equal(t, "./...", argv[len(argv)-1])
	// Regex is still present.
	idx := indexOf(argv, "-run")
	require.GreaterOrEqual(t, idx, 0)
	assert.Equal(t, "^(TestIt)$", argv[idx+1])
}

func TestComposeGoRerunArgv_TestNameWithoutPackage(t *testing.T) {
	// A detector that doesn't emit a package prefix — argv still composes,
	// but no positional package arg → falls through to ./...
	argv := tools.ExportComposeGoRerunArgv([]verify.TestFailure{
		{TestName: "TestSolo"},
	}, 50)
	assert.Equal(t, "./...", argv[len(argv)-1])
	idx := indexOf(argv, "-run")
	require.GreaterOrEqual(t, idx, 0)
	assert.Equal(t, "^(TestSolo)$", argv[idx+1])
}

func TestComposeGoRerunArgv_EmptyTestNameSkipped(t *testing.T) {
	argv := tools.ExportComposeGoRerunArgv([]verify.TestFailure{
		{TestName: ""},
		{TestName: "pkg:TestReal"},
	}, 50)
	idx := indexOf(argv, "-run")
	require.GreaterOrEqual(t, idx, 0)
	assert.Equal(t, "^(TestReal)$", argv[idx+1])
}

func TestParseRerunLimit_DefaultsAndClamping(t *testing.T) {
	// Missing / non-positive → default.
	assert.Equal(t, 50, tools.ExportParseRerunLimit(nil))
	assert.Equal(t, 50, tools.ExportParseRerunLimit(map[string]any{"limit": float64(0)}))
	assert.Equal(t, 50, tools.ExportParseRerunLimit(map[string]any{"limit": float64(-5)}))
	// Within range → used as-is.
	assert.Equal(t, 7, tools.ExportParseRerunLimit(map[string]any{"limit": float64(7)}))
	// Over max → clamped.
	assert.Equal(t, 200, tools.ExportParseRerunLimit(map[string]any{"limit": float64(1_000_000)}))
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
