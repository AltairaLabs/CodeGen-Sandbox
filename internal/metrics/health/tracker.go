// Package health computes "is the agent making progress" signals and pushes
// them into the sandbox Prometheus surface.
//
// Counters in internal/metrics tell you WHAT happened (tool calls, bytes
// read, denylist hits). These state-carrying signals tell you WHETHER the
// agent is making progress — a failing-test streak that never shrinks, a
// rising tool error rate, or the same (tool, args) invocation repeating
// within a short window all indicate the agent is stuck even when individual
// tool calls look fine.
//
// Tracker is the single state holder: tool middleware calls Observe on every
// tool return; the verify tools (run_tests/run_lint/run_typecheck) call
// ObserveGreen on exit=0; run_tests additionally calls ObserveTestResult
// with the failure count so the streak gauge can advance.
//
// Every method is nil-safe — a nil *Tracker is a valid no-op so call sites
// that don't plumb the tracker (tests, unconfigured embedders) don't need a
// sentinel.
package health

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/metrics"
)

// Config bundles the tunables operators set at process start. The zero
// value is usable but conservative; prefer DefaultConfig for callers that
// haven't parsed flags.
type Config struct {
	// RepetitionWindow is how far back (in wall time) a repeat
	// (tool, args-hash) entry counts toward the burst threshold.
	RepetitionWindow time.Duration
	// RepetitionThreshold is the minimum repeat count within the window
	// before we emit one increment on sandbox_agent_tool_repetition_total.
	RepetitionThreshold int
	// ErrorRateWindow is the size of the rolling outcome buffer feeding
	// sandbox_agent_tool_error_rate. Expressed as a count of recent calls,
	// not a duration.
	ErrorRateWindow int
}

// DefaultConfig matches the v1 flag defaults in cmd/sandbox/main.go.
func DefaultConfig() Config {
	return Config{
		RepetitionWindow:    10 * time.Minute,
		RepetitionThreshold: 3,
		ErrorRateWindow:     100,
	}
}

// repetitionEntry is one row in the per-tool ring buffer used to detect
// (tool, args-hash) bursts.
type repetitionEntry struct {
	hash string
	at   time.Time
}

// Tracker is the single state holder for agent-health signals.
//
// Zero-value Tracker is not useful — construct with New. Nil *Tracker is a
// valid no-op on every method.
type Tracker struct {
	m   *metrics.Metrics
	cfg Config
	now func() time.Time

	mu sync.Mutex

	// Test-failure streak: consecutive run_tests results whose failure count
	// did NOT decrease. Reset on decrease or zero.
	lastFailures int
	haveLast     bool
	streak       int

	// Time-since-last-green: set once on any green verify run; the gauge is
	// driven by a goroutine reading lastGreen every second. Zero-value
	// (lastGreen IsZero) means "never green" — the gauge reports 0 so
	// dashboards don't see a misleading giant number.
	lastGreen time.Time

	// Rolling outcome buffer for the tool error rate gauge.
	errBuf     []bool
	errBufIdx  int
	errBufFull bool
	errCount   int

	// Per-tool ring buffers for repetition detection plus a "bursts already
	// counted" set so a hot streak contributes exactly one increment per
	// burst rather than flapping every call.
	repBuf     map[string][]repetitionEntry
	repBufIdx  map[string]int
	repCounted map[string]map[string]bool
}

// New returns a Tracker bound to m and cfg. Passing a nil *metrics.Metrics
// is valid — the Tracker still computes state but the Set/Inc calls
// become no-ops.
func New(m *metrics.Metrics, cfg Config) *Tracker {
	if cfg.RepetitionWindow <= 0 {
		cfg.RepetitionWindow = DefaultConfig().RepetitionWindow
	}
	if cfg.RepetitionThreshold <= 0 {
		cfg.RepetitionThreshold = DefaultConfig().RepetitionThreshold
	}
	if cfg.ErrorRateWindow <= 0 {
		cfg.ErrorRateWindow = DefaultConfig().ErrorRateWindow
	}
	t := &Tracker{
		m:          m,
		cfg:        cfg,
		now:        time.Now,
		errBuf:     make([]bool, cfg.ErrorRateWindow),
		repBuf:     make(map[string][]repetitionEntry),
		repBufIdx:  make(map[string]int),
		repCounted: make(map[string]map[string]bool),
	}
	// Initialise the "time since last green" gauge to 0 so scrapers see a
	// sane value before the first verify run lands.
	t.m.SetAgentTimeSinceLastGreenSeconds(0)
	t.m.SetAgentToolErrorRate(0)
	t.m.SetAgentTestFailureStreak(0)
	return t
}

// SetNow replaces the wall-clock source for tests. The production path uses
// time.Now; deterministic streak/time tests inject a fake here.
func (t *Tracker) SetNow(now func() time.Time) {
	if t == nil || now == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.now = now
}

// Observe is the generic tool-call hook. The middleware calls it after
// status is derived for every MCP tool invocation.
//
//   - status is the closed enum from deriveStatus ("ok"|"error").
//   - argsHash is a stable short fingerprint of the tool arguments; use
//     HashArgs to compute it.
//
// All four gauges/counters are updated atomically under one mutex so the
// scrape view is internally consistent.
func (t *Tracker) Observe(tool, status, argsHash string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	t.recordErrorOutcome(status == "ok")
	t.recordRepetition(tool, argsHash)
}

// ObserveTestResult is the run_tests-specific hook. The streak increments
// when failureCount did not decrease from the previous run; resets on
// decrease or zero.
func (t *Tracker) ObserveTestResult(failureCount int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	switch {
	case failureCount == 0:
		t.streak = 0
	case t.haveLast && failureCount < t.lastFailures:
		t.streak = 0
	default:
		t.streak++
	}
	t.lastFailures = failureCount
	t.haveLast = true
	t.m.SetAgentTestFailureStreak(t.streak)
}

// ObserveGreen marks "now" as the most recent successful verify run.
// Called from run_tests/run_lint/run_typecheck when exit=0.
func (t *Tracker) ObserveGreen() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastGreen = t.now()
	t.m.SetAgentTimeSinceLastGreenSeconds(0)
}

// UpdateTimeSinceLastGreen pushes the current "seconds since last green"
// value to the gauge. Called from a 1s ticker in run.go; cheap enough to
// run unconditionally.
func (t *Tracker) UpdateTimeSinceLastGreen() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.lastGreen.IsZero() {
		// Never green — report 0 rather than "seconds since epoch".
		t.m.SetAgentTimeSinceLastGreenSeconds(0)
		return
	}
	secs := t.now().Sub(t.lastGreen).Seconds()
	t.m.SetAgentTimeSinceLastGreenSeconds(secs)
}

// recordErrorOutcome pushes one outcome into the rolling buffer and
// republishes the gauge. Must be called with mu held.
func (t *Tracker) recordErrorOutcome(ok bool) {
	if len(t.errBuf) == 0 {
		return
	}
	// Overwrite the slot we're about to replace so the running count stays
	// accurate without scanning the whole buffer.
	if t.errBufFull {
		if !t.errBuf[t.errBufIdx] {
			t.errCount--
		}
	}
	t.errBuf[t.errBufIdx] = ok
	if !ok {
		t.errCount++
	}
	t.errBufIdx = (t.errBufIdx + 1) % len(t.errBuf)
	if !t.errBufFull && t.errBufIdx == 0 {
		t.errBufFull = true
	}

	size := t.errBufIdx
	if t.errBufFull {
		size = len(t.errBuf)
	}
	var rate float64
	if size > 0 {
		rate = float64(t.errCount) / float64(size)
	}
	t.m.SetAgentToolErrorRate(rate)
}

// recordRepetition pushes one (tool, argsHash, now) entry into the tool's
// ring buffer. When the same hash has appeared >= RepetitionThreshold
// times within RepetitionWindow, it increments the repetition counter
// ONCE per burst — the "already counted" set guards against flapping.
// Must be called with mu held.
func (t *Tracker) recordRepetition(tool, argsHash string) {
	if argsHash == "" {
		return
	}
	bufSize := t.cfg.RepetitionThreshold * 4
	buf, ok := t.repBuf[tool]
	if !ok {
		buf = make([]repetitionEntry, bufSize)
		t.repBuf[tool] = buf
		t.repBufIdx[tool] = 0
		t.repCounted[tool] = make(map[string]bool)
	}
	idx := t.repBufIdx[tool]
	buf[idx] = repetitionEntry{hash: argsHash, at: t.now()}
	t.repBuf[tool] = buf
	t.repBufIdx[tool] = (idx + 1) % bufSize

	// Count occurrences of this hash within the active window.
	cutoff := t.now().Add(-t.cfg.RepetitionWindow)
	count := 0
	for _, e := range buf {
		if e.hash == argsHash && !e.at.Before(cutoff) {
			count++
		}
	}

	counted := t.repCounted[tool]
	switch {
	case count >= t.cfg.RepetitionThreshold && !counted[argsHash]:
		counted[argsHash] = true
		t.m.IncAgentToolRepetition(tool)
	case count < t.cfg.RepetitionThreshold && counted[argsHash]:
		// The hash aged out of the window (or got overwritten in the ring);
		// clear the "already counted" flag so a fresh burst counts once more.
		delete(counted, argsHash)
	}
}

// HashArgs produces a short stable fingerprint of arbitrary tool arguments
// for use as the (tool, args-hash) repetition key.
//
// The hash is the first 8 hex chars of sha256 over "tool\n" + the
// JSON-stable encoding of args. Objects are key-sorted via json.Marshal's
// deterministic ordering so {a:1,b:2} and {b:2,a:1} collapse to the same
// key.
func HashArgs(tool string, args any) string {
	data, err := canonicalJSON(args)
	if err != nil {
		// Pathological inputs (non-serialisable types) fall through to an
		// empty hash; Observe skips empty hashes so they don't pollute the
		// ring buffer with unstable fingerprints.
		return ""
	}
	sum := sha256.Sum256([]byte(tool + "\n" + string(data)))
	return hex.EncodeToString(sum[:])[:8]
}

// canonicalJSON returns a key-sorted JSON encoding of v. json.Marshal on
// map[string]any already sorts keys; for other map types we recurse via
// reflection-free sort on a flattened key list.
func canonicalJSON(v any) ([]byte, error) {
	return json.Marshal(canonicalize(v))
}

// canonicalize replaces any map[string]any in v with an
// explicitly-key-sorted []kv so json.Marshal emits a stable encoding.
// Nested maps / slices recurse.
func canonicalize(v any) any {
	switch vv := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(vv))
		for k := range vv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]kv, 0, len(keys))
		for _, k := range keys {
			out = append(out, kv{K: k, V: canonicalize(vv[k])})
		}
		return out
	case []any:
		out := make([]any, len(vv))
		for i, e := range vv {
			out[i] = canonicalize(e)
		}
		return out
	default:
		return v
	}
}

// kv is the stable JSON shape canonicalize produces for map values. Using a
// two-field struct with explicit JSON tags guarantees the marshaller emits
// keys in the order we sorted them.
type kv struct {
	K string `json:"k"`
	V any    `json:"v"`
}
