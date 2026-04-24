package tools_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireBashForWatch(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH; skipping watch_process test")
	}
}

func callWatchProcess(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleWatchProcess(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func callWatchEvents(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleWatchProcessEvents(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

// waitForEventLine polls watch_process_events up to timeout, returning
// when the response body contains `want`. Fails the test with the most
// recent body on timeout.
func waitForEventLine(t *testing.T, deps *tools.Deps, id, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		res := callWatchEvents(t, deps, map[string]any{"shell_id": id})
		require.False(t, res.IsError)
		last = textOf(t, res)
		if contains(last, want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in events; last body:\n%s", want, last)
}

func TestWatchProcess_MissingCommandIsError(t *testing.T) {
	deps, _ := newBGDeps(t)
	res := callWatchProcess(t, deps, map[string]any{"description": "d"})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "command is required")
}

func TestWatchProcess_MissingDescriptionIsError(t *testing.T) {
	deps, _ := newBGDeps(t)
	res := callWatchProcess(t, deps, map[string]any{"command": "echo ok"})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "description is required")
}

func TestWatchProcess_InvalidRegexIsError(t *testing.T) {
	deps, _ := newBGDeps(t)
	res := callWatchProcess(t, deps, map[string]any{
		"command":        "echo ok",
		"description":    "bad regex",
		"error_patterns": []any{"valid", "([unbalanced"},
	})
	assert.True(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "error_patterns[1]")
	assert.Contains(t, body, "failed to compile")
}

func TestWatchProcess_TooManyPatternsIsError(t *testing.T) {
	deps, _ := newBGDeps(t)
	tooMany := make([]any, 33)
	for i := range tooMany {
		tooMany[i] = "x"
	}
	res := callWatchProcess(t, deps, map[string]any{
		"command":        "echo ok",
		"description":    "too many",
		"error_patterns": tooMany,
	})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "too many error_patterns")
}

func TestWatchProcess_DenylistedCommandIsRejected(t *testing.T) {
	deps, _ := newBGDeps(t)
	res := callWatchProcess(t, deps, map[string]any{
		"command":     "sudo rm -rf /",
		"description": "nope",
	})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "command rejected")
}

func TestWatchProcess_ReturnsShellIDAndStartedEvent(t *testing.T) {
	requireBashForWatch(t)
	deps, _ := newBGDeps(t)

	start := callWatchProcess(t, deps, map[string]any{
		"command":     "echo hi",
		"description": "probe",
	})
	require.False(t, start.IsError, "start returned error: %s", textOf(t, start))
	body := textOf(t, start)
	assert.Contains(t, body, "shell_id:")
	assert.Contains(t, body, "started in background")
	// Default patterns are listed so the agent sees them.
	assert.Contains(t, body, "error_patterns:")
	assert.Contains(t, body, "panic:")

	id := extractShellID(t, body)
	waitForEventLine(t, deps, id, "started", 2*time.Second)
	waitForEventLine(t, deps, id, "exited  exit=0", 2*time.Second)
}

func TestWatchProcess_MatchedErrorEmitsStructuredEvent(t *testing.T) {
	requireBashForWatch(t)
	deps, _ := newBGDeps(t)

	start := callWatchProcess(t, deps, map[string]any{
		// Stream a clean line, then a line matching the default panic:
		// pattern, then exit cleanly. We want to see the "error" event
		// for the panic line AND the normal "exited" event for exit 0.
		"command":     `echo "all good" 1>&2; sleep 0.05; echo "panic: runtime error" 1>&2; sleep 0.05`,
		"description": "panic probe",
	})
	require.False(t, start.IsError, "start returned error: %s", textOf(t, start))
	id := extractShellID(t, textOf(t, start))

	waitForEventLine(t, deps, id, "exited  exit=0", 3*time.Second)

	final := callWatchEvents(t, deps, map[string]any{"shell_id": id})
	require.False(t, final.IsError)
	body := textOf(t, final)
	assert.Contains(t, body, "error  (matched \"^panic:\")")
	assert.Contains(t, body, "panic: runtime error")
	// Only one matched-event line (the panic one). The non-matching
	// "all good" line is in the raw stderr buffer but MUST NOT appear
	// as a matched event — so exactly one "matched" token in the body.
	assert.Equal(t, 1, strings.Count(body, "(matched "),
		"expected exactly one matched event, got body:\n%s", body)
}

func TestWatchProcess_CustomPatternOverridesDefaults(t *testing.T) {
	requireBashForWatch(t)
	deps, _ := newBGDeps(t)

	start := callWatchProcess(t, deps, map[string]any{
		"command":        `echo "BANG: custom failure" 1>&2; sleep 0.05`,
		"description":    "custom pattern",
		"error_patterns": []any{"^BANG:"},
	})
	require.False(t, start.IsError)
	id := extractShellID(t, textOf(t, start))

	waitForEventLine(t, deps, id, "exited  exit=0", 3*time.Second)

	final := callWatchEvents(t, deps, map[string]any{"shell_id": id})
	body := textOf(t, final)
	assert.Contains(t, body, `matched "^BANG:"`)
	assert.Contains(t, body, "BANG: custom failure")
}

func TestWatchProcess_EmptyPatternListDisablesMatching(t *testing.T) {
	requireBashForWatch(t)
	deps, _ := newBGDeps(t)

	start := callWatchProcess(t, deps, map[string]any{
		// Would normally match the default "^panic:" pattern.
		"command":        `echo "panic: ignore me" 1>&2; sleep 0.05`,
		"description":    "matching disabled",
		"error_patterns": []any{},
	})
	require.False(t, start.IsError)
	body := textOf(t, start)
	assert.Contains(t, body, "(none — pattern matching disabled)")
	id := extractShellID(t, body)

	waitForEventLine(t, deps, id, "exited  exit=0", 3*time.Second)

	final := callWatchEvents(t, deps, map[string]any{"shell_id": id})
	finalBody := textOf(t, final)
	// Only started + exited — no error event for the panic line. We
	// assert via pattern counts rather than absence of "matched", since
	// the header section of the body can echo the configured pattern
	// (though in this case it's empty) — assert zero matched events.
	assert.Equal(t, 0, strings.Count(finalBody, "(matched "),
		"expected no matched events with empty pattern list, got body:\n%s", finalBody)
	// The events listing is exactly two: started + exited.
	assert.Contains(t, finalBody, "events (2)")
}

func TestWatchProcess_SinceEventIDPaginatesCorrectly(t *testing.T) {
	requireBashForWatch(t)
	deps, _ := newBGDeps(t)

	start := callWatchProcess(t, deps, map[string]any{
		"command":     `echo "panic: one" 1>&2; sleep 0.05; echo "panic: two" 1>&2; sleep 0.05`,
		"description": "paginated panics",
	})
	require.False(t, start.IsError)
	id := extractShellID(t, textOf(t, start))

	waitForEventLine(t, deps, id, "exited  exit=0", 3*time.Second)

	// First poll from scratch: gets every event.
	first := textOf(t, callWatchEvents(t, deps, map[string]any{"shell_id": id}))
	assert.Contains(t, first, "panic: one")
	assert.Contains(t, first, "panic: two")

	// Second poll with since_event_id beyond the last event: event
	// section is empty. The header section of the body still echoes
	// the command string (which contains the panic text), so we assert
	// on the "events (0)" count rather than absence of the panic
	// substring.
	after := textOf(t, callWatchEvents(t, deps, map[string]any{
		"shell_id":       id,
		"since_event_id": float64(9999),
	}))
	assert.Contains(t, after, "events (0)")
}

func TestWatchProcess_IdleTimeoutKillsProcess(t *testing.T) {
	requireBashForWatch(t)
	deps, _ := newBGDeps(t)

	// Emit nothing, then sleep for longer than the watchdog's idle
	// timeout. The watchdog should kill the process group and the
	// lifecycle goroutine should record a non-zero exit.
	start := callWatchProcess(t, deps, map[string]any{
		"command":              "sleep 30",
		"description":          "idle probe",
		"idle_timeout_seconds": float64(1),
	})
	require.False(t, start.IsError)
	body := textOf(t, start)
	assert.Contains(t, body, "idle_timeout: 1s")
	id := extractShellID(t, body)

	// Wait up to 5s for the idle_timeout event + subsequent exit.
	waitForEventLine(t, deps, id, "idle_timeout", 5*time.Second)
	waitForEventLine(t, deps, id, "exited", 5*time.Second)
}

func TestWatchProcessEvents_UnknownShellIDIsError(t *testing.T) {
	deps, _ := newBGDeps(t)
	res := callWatchEvents(t, deps, map[string]any{"shell_id": "not-a-real-id"})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "unknown shell_id")
}

func TestWatchProcessEvents_BashShellIDIsError(t *testing.T) {
	// A shell started via Bash(run_in_background=true) should NOT be
	// visible through watch_process_events — the tool surface is
	// specific to watched shells. BashOutput is the right read for
	// those.
	requireBashForWatch(t)
	deps, _ := newBGDeps(t)

	start := callBash(t, deps, map[string]any{
		"command":           "echo hi",
		"description":       "bg echo",
		"run_in_background": true,
	})
	require.False(t, start.IsError)
	id := extractShellID(t, textOf(t, start))

	res := callWatchEvents(t, deps, map[string]any{"shell_id": id})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "not started by watch_process")
}
