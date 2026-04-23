package verify

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// test2jsonEvent is the subset of `go test -json` event fields we consume.
// Matches the shape documented by cmd/internal/test2json in the Go toolchain.
type test2jsonEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

// testKey groups events by (package, test). The zero-value Test means a
// package-level event, which we ignore for failure extraction.
type testKey struct {
	pkg  string
	test string
}

// goFailFrame accumulates per-test state while we scan the event stream.
type goFailFrame struct {
	outputs []string
	failed  bool
}

// goTestFileLineRe matches a `file.go:LINE:` prefix inside a test output
// line. Test helpers emit `    <file>:<line>: <msg>` (8-space indent) and
// panics surface `<file>:<line>: ...`. We capture whichever we see first.
var goTestFileLineRe = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_./\-]+\.go):(\d+):`)

// ParseGoTest2JSON parses a stream of test2json events from `go test -json`
// stdout into a slice of TestFailure records. Never returns an error — lines
// that aren't valid JSON (or aren't events we recognise) are silently
// skipped.
func ParseGoTest2JSON(stdout string) []TestFailure {
	frames := scanGoEvents(stdout)
	return framesToFailures(frames)
}

// CountGoTest2JSONPasses returns the number of `pass` actions for named
// tests in the stream. Package-level pass events (Test == "") don't count.
// Total parser, like ParseGoTest2JSON.
func CountGoTest2JSONPasses(stdout string) int {
	count := 0
	for _, line := range strings.Split(stdout, "\n") {
		ev, ok := decodeEvent(line)
		if !ok {
			continue
		}
		if ev.Action == "pass" && ev.Test != "" {
			count++
		}
	}
	return count
}

// scanGoEvents streams through stdout line by line, applies each event to a
// frame keyed by (package, test), and returns the frames in first-seen order.
func scanGoEvents(stdout string) []*goFailFrame {
	byKey := map[testKey]*goFailFrame{}
	var order []testKey
	for _, line := range strings.Split(stdout, "\n") {
		ev, ok := decodeEvent(line)
		if !ok || ev.Test == "" {
			continue
		}
		key := testKey{pkg: ev.Package, test: ev.Test}
		frame := byKey[key]
		if frame == nil {
			frame = &goFailFrame{}
			byKey[key] = frame
			order = append(order, key)
		}
		applyEvent(frame, ev)
	}
	// Preserve insertion order, which matches failure order in the stream.
	out := make([]*goFailFrame, 0, len(order))
	for _, k := range order {
		frame := byKey[k]
		frame.outputs = append(frame.outputs, keySentinel(k))
		out = append(out, frame)
	}
	return out
}

// keySentinel encodes the test key into the last outputs slot so
// framesToFailures can build TestName without a parallel slice.
func keySentinel(k testKey) string { return "\x00" + k.pkg + "\x00" + k.test }

// decodeEvent parses a single JSON-per-line event. Returns (event, false)
// on any decode error, empty line, or plainly non-JSON input.
func decodeEvent(line string) (test2jsonEvent, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || trimmed[0] != '{' {
		return test2jsonEvent{}, false
	}
	var ev test2jsonEvent
	if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
		return test2jsonEvent{}, false
	}
	return ev, true
}

// applyEvent updates the frame for one event. Only `output` and `fail`
// actions mutate state; pass/skip leave the frame's failed bit clear, so
// framesToFailures drops them.
func applyEvent(frame *goFailFrame, ev test2jsonEvent) {
	switch ev.Action {
	case "output":
		if ev.Output != "" {
			frame.outputs = append(frame.outputs, ev.Output)
		}
	case "fail":
		frame.failed = true
	}
}

// framesToFailures converts accumulated frames into TestFailure records,
// dropping non-failed entries.
func framesToFailures(frames []*goFailFrame) []TestFailure {
	var out []TestFailure
	for _, f := range frames {
		if !f.failed {
			continue
		}
		if tf, ok := frameToFailure(f); ok {
			out = append(out, tf)
		}
	}
	return out
}

// frameToFailure extracts the TestName, File, Line, Message, Diff from a
// single failed frame. The final outputs entry is the key sentinel injected
// by scanGoEvents.
func frameToFailure(f *goFailFrame) (TestFailure, bool) {
	n := len(f.outputs)
	if n == 0 {
		return TestFailure{}, false
	}
	pkg, test := splitKeySentinel(f.outputs[n-1])
	body := f.outputs[:n-1]
	file, line := extractFileLine(body)
	msg := extractMessage(body)
	diff := extractDiff(body)
	return TestFailure{
		File:     file,
		Line:     line,
		TestName: pkg + "/" + test,
		Message:  msg,
		Diff:     diff,
	}, true
}

// splitKeySentinel reverses keySentinel. A malformed sentinel returns ("",
// ""), which falls through to an empty TestName.
func splitKeySentinel(s string) (pkg, test string) {
	if !strings.HasPrefix(s, "\x00") {
		return "", ""
	}
	parts := strings.SplitN(s[1:], "\x00", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// extractFileLine returns the first file:line pair found across the frame's
// outputs. Scans every output line so `--- FAIL` headers (which omit the
// file) don't short-circuit extraction.
func extractFileLine(outputs []string) (string, int) {
	for _, raw := range outputs {
		m := goTestFileLineRe.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		ln, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		return m[1], ln
	}
	return "", 0
}

// extractMessage returns the first non-empty output line after the
// `--- FAIL:` marker. Falls back to the first non-trivial line if no
// marker is present (robust to truncated streams).
func extractMessage(outputs []string) string {
	if msg := messageAfterFailMarker(outputs); msg != "" {
		return msg
	}
	return firstMeaningfulLine(outputs)
}

// messageAfterFailMarker scans for `--- FAIL:` and returns the first
// non-empty line that follows it. `=== NAME` lines are skipped.
func messageAfterFailMarker(outputs []string) string {
	seen := false
	for _, raw := range outputs {
		if !seen {
			if strings.Contains(raw, "--- FAIL:") {
				seen = true
			}
			continue
		}
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// firstMeaningfulLine returns the first output line that isn't a
// === / --- marker, trimmed. Used as a fallback when `--- FAIL:` is absent.
func firstMeaningfulLine(outputs []string) string {
	for _, raw := range outputs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "===") || strings.HasPrefix(trimmed, "---") {
			continue
		}
		// Strip a leading `file.go:42:` prefix — the file:line is already
		// captured separately; the human-readable message is what follows.
		if loc := goTestFileLineRe.FindStringIndex(trimmed); loc != nil && loc[0] == 0 {
			return strings.TrimSpace(trimmed[loc[1]:])
		}
		return trimmed
	}
	return ""
}

// diffMarkers are the literal strings we recognise as the top of a diff
// block inside test output. Hoisted to a const to satisfy Sonar's
// "duplicated literals" rule when more markers are added later.
var diffMarkers = []string{"got:", "want:", "Diff:", "--- Expected", "+++ Actual"}

// extractDiff returns the contiguous block of lines surrounding the first
// diff marker. Returns "" when no marker is found. The window is capped so
// a runaway output doesn't bloat the Failure.
func extractDiff(outputs []string) string {
	idx := findDiffStart(outputs)
	if idx < 0 {
		return ""
	}
	const maxLines = 20
	end := idx + maxLines
	if end > len(outputs) {
		end = len(outputs)
	}
	var sb strings.Builder
	for i := idx; i < end; i++ {
		sb.WriteString(strings.TrimRight(outputs[i], "\n"))
		sb.WriteByte('\n')
	}
	return strings.TrimRight(sb.String(), "\n")
}

// findDiffStart returns the index of the first output line containing any
// diffMarker, or -1 when none is present.
func findDiffStart(outputs []string) int {
	for i, raw := range outputs {
		for _, m := range diffMarkers {
			if strings.Contains(raw, m) {
				return i
			}
		}
	}
	return -1
}
