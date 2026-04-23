package tools_test

import (
	"context"
	"testing"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callLastFailures(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleLastTestFailures(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func newDepsWithStore(t *testing.T) (*tools.Deps, *tools.TestResultStore) {
	t.Helper()
	deps, _ := newTestDeps(t)
	store := tools.NewTestResultStore()
	deps.TestResults = store
	return deps, store
}

func TestLastTestFailures_NoPriorCall(t *testing.T) {
	deps, _ := newDepsWithStore(t)
	res := callLastFailures(t, deps, map[string]any{})
	require.False(t, res.IsError)
	assert.Contains(t, textOf(t, res), "no run_tests call yet")
}

func TestLastTestFailures_StoreNotConfigured(t *testing.T) {
	deps, _ := newTestDeps(t) // no TestResults set
	res := callLastFailures(t, deps, map[string]any{})
	assert.True(t, res.IsError)
}

func TestLastTestFailures_LastRunPassed(t *testing.T) {
	deps, store := newDepsWithStore(t)
	store.Set(tools.TestResult{
		Language:    "go",
		Failures:    nil,
		TestsPassed: 7,
		At:          time.Now(),
		Supported:   true,
	})
	res := callLastFailures(t, deps, map[string]any{})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "had no failures")
	assert.Contains(t, body, "7 tests passed")
	assert.Contains(t, body, "go")
}

func TestLastTestFailures_RendersFailures(t *testing.T) {
	deps, store := newDepsWithStore(t)
	store.Set(tools.TestResult{
		Language: "go",
		Failures: []verify.TestFailure{
			{
				TestName: "example.com/pkg/foo:TestValidate/empty_input",
				File:     "internal/foo/foo.go",
				Line:     42,
				Message:  "expected error for empty input, got nil",
			},
			{
				TestName: "example.com/pkg/bar:TestBaz",
				File:     "internal/bar/bar_test.go",
				Line:     87,
				Message:  "mismatch",
				Diff:     "got: 5\nwant: 3",
			},
		},
		At:        time.Now(),
		Supported: true,
	})
	res := callLastFailures(t, deps, map[string]any{})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "2 test failure(s)")
	assert.Contains(t, body, "TestValidate/empty_input")
	assert.Contains(t, body, "internal/foo/foo.go:42")
	assert.Contains(t, body, "expected error for empty input")
	assert.Contains(t, body, "TestBaz")
	assert.Contains(t, body, "--- diff ---")
	assert.Contains(t, body, "got: 5")
	assert.Contains(t, body, "want: 3")
}

func TestLastTestFailures_LimitCapsOutput(t *testing.T) {
	deps, store := newDepsWithStore(t)
	failures := make([]verify.TestFailure, 5)
	for i := range failures {
		failures[i] = verify.TestFailure{
			TestName: "pkg:TestN",
			Message:  "boom",
		}
	}
	store.Set(tools.TestResult{
		Language:  "go",
		Failures:  failures,
		At:        time.Now(),
		Supported: true,
	})

	res := callLastFailures(t, deps, map[string]any{"limit": float64(2)})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "5 test failure(s)")
	assert.Contains(t, body, "3 more entries truncated")
}

func TestLastTestFailures_UnsupportedLanguage(t *testing.T) {
	deps, store := newDepsWithStore(t)
	store.Set(tools.TestResult{
		Language:  "python",
		Failures:  nil,
		At:        time.Now(),
		Supported: false,
	})
	res := callLastFailures(t, deps, map[string]any{})
	require.False(t, res.IsError)
	assert.Contains(t, textOf(t, res), "no structured failures available for python")
}

func TestLastTestFailures_AgeFormatting(t *testing.T) {
	deps, store := newDepsWithStore(t)
	store.Set(tools.TestResult{
		Language:  "go",
		Failures:  nil,
		At:        time.Now().Add(-90 * time.Second),
		Supported: true,
	})
	res := callLastFailures(t, deps, map[string]any{})
	body := textOf(t, res)
	// Should render as "1m30s ago" (±1s tolerance via substring check).
	assert.Contains(t, body, "1m")
	assert.Contains(t, body, "ago")
}

func TestLastTestFailures_UnknownPassCountFallsBack(t *testing.T) {
	deps, store := newDepsWithStore(t)
	store.Set(tools.TestResult{
		Language:    "go",
		Failures:    nil,
		TestsPassed: -1, // detector had no countable signal
		At:          time.Now(),
		Supported:   true,
	})
	res := callLastFailures(t, deps, map[string]any{})
	body := textOf(t, res)
	// Fallback wording: "passed" without an explicit count.
	assert.Contains(t, body, "passed")
	assert.NotContains(t, body, "-1")
}

func TestLastTestFailures_LimitClamped(t *testing.T) {
	deps, store := newDepsWithStore(t)
	// A single failure is enough; we just want to exercise the over-max
	// branch of parseLastFailuresLimit.
	store.Set(tools.TestResult{
		Language:  "go",
		Failures:  []verify.TestFailure{{TestName: "p:T", Message: "boom"}},
		At:        time.Now(),
		Supported: true,
	})
	res := callLastFailures(t, deps, map[string]any{"limit": float64(100000)})
	require.False(t, res.IsError)
	// No "truncated" marker — the clamp saturates but doesn't add truncation
	// because the actual slice is tiny.
	assert.NotContains(t, textOf(t, res), "truncated")
}

func TestLastTestFailures_HourScaleAge(t *testing.T) {
	deps, store := newDepsWithStore(t)
	store.Set(tools.TestResult{
		Language:  "go",
		Failures:  nil,
		At:        time.Now().Add(-2 * time.Hour),
		Supported: true,
	})
	body := textOf(t, callLastFailures(t, deps, map[string]any{}))
	assert.Contains(t, body, "2h")
}

func TestLastTestFailures_JustNowAge(t *testing.T) {
	deps, store := newDepsWithStore(t)
	store.Set(tools.TestResult{
		Language:  "go",
		Failures:  nil,
		At:        time.Now(),
		Supported: true,
	})
	body := textOf(t, callLastFailures(t, deps, map[string]any{}))
	assert.Contains(t, body, "just now")
}

// Integration-ish: run_tests followed by last_test_failures should reflect
// the just-executed result. Skips when `go` is unavailable.
func TestLastTestFailures_AfterRunTests_FailingModule(t *testing.T) {
	requireGo(t)
	deps, root := newTestDeps(t)
	deps.TestResults = tools.NewTestResultStore()
	seedGoModule(t, root, false)

	runRes := callRunTests(t, deps, map[string]any{})
	require.False(t, runRes.IsError)

	res := callLastFailures(t, deps, map[string]any{})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "test failure(s)")
	assert.Contains(t, body, "TestAdd")
}

func TestLastTestFailures_AfterRunTests_PassingModule(t *testing.T) {
	requireGo(t)
	deps, root := newTestDeps(t)
	deps.TestResults = tools.NewTestResultStore()
	seedGoModule(t, root, true)

	runRes := callRunTests(t, deps, map[string]any{})
	require.False(t, runRes.IsError)

	res := callLastFailures(t, deps, map[string]any{})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "no failures")
}
