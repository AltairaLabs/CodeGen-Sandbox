package health_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/metrics"
	"github.com/altairalabs/codegen-sandbox/internal/metrics/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTracker returns a fresh tracker wired to a real Prometheus surface we
// can scrape, so tests assert against the wire format rather than private
// fields.
func newTracker(t *testing.T, cfg health.Config) (*health.Tracker, *metrics.Metrics) {
	t.Helper()
	m, err := metrics.New()
	require.NoError(t, err)
	return health.New(m, cfg), m
}

func TestTracker_TestFailureStreak_IncrementsOnNonDecrease(t *testing.T) {
	tr, m := newTracker(t, health.DefaultConfig())

	tr.ObserveTestResult(5) // non-zero first call → streak 1
	tr.ObserveTestResult(5) // stable → streak 2
	tr.ObserveTestResult(7) // grew → streak 3

	assert.Contains(t, scrape(t, m), "sandbox_agent_test_failure_streak 3")
}

func TestTracker_TestFailureStreak_ResetsOnDecrease(t *testing.T) {
	tr, m := newTracker(t, health.DefaultConfig())

	tr.ObserveTestResult(5) // streak 1
	tr.ObserveTestResult(5) // streak 2
	tr.ObserveTestResult(3) // decreased → reset to 0

	assert.Contains(t, scrape(t, m), "sandbox_agent_test_failure_streak 0")
}

func TestTracker_TestFailureStreak_ResetsOnZero(t *testing.T) {
	tr, m := newTracker(t, health.DefaultConfig())

	tr.ObserveTestResult(4) // streak 1
	tr.ObserveTestResult(4) // streak 2
	tr.ObserveTestResult(0) // all green → reset

	assert.Contains(t, scrape(t, m), "sandbox_agent_test_failure_streak 0")
}

func TestTracker_TimeSinceLastGreen_ZeroAtStart(t *testing.T) {
	_, m := newTracker(t, health.DefaultConfig())

	// No green run recorded; gauge must read 0.
	assert.Contains(t, scrape(t, m), "sandbox_agent_time_since_last_green_seconds 0")
}

func TestTracker_TimeSinceLastGreen_AdvancesAndResets(t *testing.T) {
	tr, m := newTracker(t, health.DefaultConfig())

	// Pin the clock so we can advance it manually across three samples.
	clock := time.Unix(1_700_000_000, 0)
	tr.SetNow(func() time.Time { return clock })

	tr.ObserveGreen()                   // lastGreen = t0
	clock = clock.Add(30 * time.Second) // advance
	tr.UpdateTimeSinceLastGreen()       // gauge should read 30s
	assert.Contains(t, scrape(t, m), "sandbox_agent_time_since_last_green_seconds 30")

	clock = clock.Add(15 * time.Second)
	tr.ObserveGreen() // lastGreen reset; gauge re-zeros
	tr.UpdateTimeSinceLastGreen()
	assert.Contains(t, scrape(t, m), "sandbox_agent_time_since_last_green_seconds 0")
}

func TestTracker_ErrorRate_EmptyIsZero(t *testing.T) {
	_, m := newTracker(t, health.DefaultConfig())
	// No observations yet; gauge must be 0 so dashboards start sane.
	assert.Contains(t, scrape(t, m), "sandbox_agent_tool_error_rate 0")
}

func TestTracker_ErrorRate_TenOkTenErrWindow20(t *testing.T) {
	tr, m := newTracker(t, health.Config{
		RepetitionWindow:    time.Hour,
		RepetitionThreshold: 100, // intentionally impossible so repetition doesn't fire
		ErrorRateWindow:     20,
	})

	for i := 0; i < 10; i++ {
		tr.Observe("Read", "ok", "")
	}
	for i := 0; i < 10; i++ {
		tr.Observe("Read", "error", "")
	}
	assert.Contains(t, scrape(t, m), "sandbox_agent_tool_error_rate 0.5")
}

func TestTracker_ErrorRate_RollingWindowEvicts(t *testing.T) {
	tr, m := newTracker(t, health.Config{
		RepetitionWindow:    time.Hour,
		RepetitionThreshold: 100,
		ErrorRateWindow:     4,
	})

	// Fill with errors (rate 1.0), then flood with oks; once the window
	// holds 4 oks, rate must drop to 0.
	for i := 0; i < 4; i++ {
		tr.Observe("Read", "error", "")
	}
	assert.Contains(t, scrape(t, m), "sandbox_agent_tool_error_rate 1")

	for i := 0; i < 4; i++ {
		tr.Observe("Read", "ok", "")
	}
	assert.Contains(t, scrape(t, m), "sandbox_agent_tool_error_rate 0")
}

func TestTracker_Repetition_BurstEmitsOnce(t *testing.T) {
	tr, m := newTracker(t, health.Config{
		RepetitionWindow:    time.Minute,
		RepetitionThreshold: 3,
		ErrorRateWindow:     100,
	})

	hash := health.HashArgs("Read", map[string]any{"file_path": "/a"})
	tr.Observe("Read", "ok", hash)
	tr.Observe("Read", "ok", hash)
	// First threshold-satisfying call emits the increment; subsequent
	// matching calls within the same burst must NOT flap the counter.
	tr.Observe("Read", "ok", hash)
	tr.Observe("Read", "ok", hash)
	tr.Observe("Read", "ok", hash)

	assert.Contains(t, scrape(t, m), `sandbox_agent_tool_repetition_total{tool="Read"} 1`)
}

func TestTracker_Repetition_DifferentArgsNoBurst(t *testing.T) {
	tr, m := newTracker(t, health.Config{
		RepetitionWindow:    time.Minute,
		RepetitionThreshold: 3,
		ErrorRateWindow:     100,
	})

	for i := 0; i < 5; i++ {
		// A different args-hash every call → no burst emitted.
		tr.Observe("Read", "ok", health.HashArgs("Read", map[string]any{"i": i}))
	}
	// Counter is a zero-initialised CounterVec; absence of the labelled
	// family in the scrape body confirms no increment fired.
	body := scrape(t, m)
	assert.NotContains(t, body, `sandbox_agent_tool_repetition_total{tool="Read"}`)
}

func TestTracker_Repetition_WindowEviction(t *testing.T) {
	tr, m := newTracker(t, health.Config{
		RepetitionWindow:    100 * time.Millisecond,
		RepetitionThreshold: 3,
		ErrorRateWindow:     100,
	})

	clock := time.Unix(1_700_000_000, 0)
	tr.SetNow(func() time.Time { return clock })

	hash := health.HashArgs("Read", map[string]any{"f": "/x"})
	tr.Observe("Read", "ok", hash)
	tr.Observe("Read", "ok", hash)
	tr.Observe("Read", "ok", hash) // burst #1, emits once

	clock = clock.Add(time.Second) // evict the window
	// A fresh run of three counts the second burst — one more increment.
	tr.Observe("Read", "ok", hash)
	tr.Observe("Read", "ok", hash)
	tr.Observe("Read", "ok", hash)

	assert.Contains(t, scrape(t, m), `sandbox_agent_tool_repetition_total{tool="Read"} 2`)
}

func TestTracker_HashArgs_StableUnderKeyOrder(t *testing.T) {
	a := health.HashArgs("Edit", map[string]any{"a": 1, "b": 2})
	b := health.HashArgs("Edit", map[string]any{"b": 2, "a": 1})
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, health.HashArgs("Edit", map[string]any{"a": 2, "b": 1}))
}

func TestTracker_HashArgs_NestedMapsCanonical(t *testing.T) {
	a := health.HashArgs("Edit", map[string]any{
		"outer": map[string]any{"x": 1, "y": 2},
		"list":  []any{map[string]any{"p": 1, "q": 2}},
	})
	b := health.HashArgs("Edit", map[string]any{
		"list":  []any{map[string]any{"q": 2, "p": 1}},
		"outer": map[string]any{"y": 2, "x": 1},
	})
	assert.Equal(t, a, b)
}

func TestTracker_HashArgs_NonSerialisableReturnsEmpty(t *testing.T) {
	// Channels don't marshal to JSON; the hasher must fail gracefully so
	// repetition detection doesn't pollute the ring with a bogus hash.
	assert.Equal(t, "", health.HashArgs("X", map[string]any{"c": make(chan int)}))
}

func TestTracker_NilReceiverSafe(t *testing.T) {
	var tr *health.Tracker
	assert.NotPanics(t, func() {
		tr.Observe("Read", "ok", "abcd1234")
		tr.ObserveTestResult(5)
		tr.ObserveGreen()
		tr.UpdateTimeSinceLastGreen()
		tr.SetNow(time.Now)
	})
}

func TestDefaultConfig_MatchesDocumentedDefaults(t *testing.T) {
	c := health.DefaultConfig()
	assert.Equal(t, 10*time.Minute, c.RepetitionWindow)
	assert.Equal(t, 3, c.RepetitionThreshold)
	assert.Equal(t, 100, c.ErrorRateWindow)
}

func TestTracker_NewClampsZeroConfigToDefaults(t *testing.T) {
	// Zero-value Config should fall back to DefaultConfig so a mis-wired
	// embedder doesn't end up with a zero-length error buffer.
	tr, m := newTracker(t, health.Config{})
	tr.Observe("Read", "error", "")
	// Zero-length buffer would leave the gauge at its initial 0 forever;
	// with the default 100-slot buffer, one error in one call → rate 1.
	assert.Contains(t, scrape(t, m), "sandbox_agent_tool_error_rate 1")
}

func scrape(t *testing.T, m *metrics.Metrics) string {
	t.Helper()
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}
