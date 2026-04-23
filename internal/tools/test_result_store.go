package tools

import (
	"sync"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
)

// TestResultStore is a single-slot, goroutine-safe record of the most
// recent run_tests invocation. last_test_failures reads it; run_tests
// writes to it after every call. Reset on server restart — there's no
// persistence.
type TestResultStore struct {
	mu      sync.RWMutex
	present bool
	result  TestResult
}

// TestResult is one captured run_tests outcome.
type TestResult struct {
	// Language is the Detector.Language() label of the project that ran.
	Language string
	// Failures is the parsed structured failure list (nil for passing runs
	// OR when the detector has no ParseTestFailures implementation — the
	// caller distinguishes via TestsPassed).
	Failures []verify.TestFailure
	// TestsPassed records how many tests succeeded. -1 means "unknown"
	// (runner didn't emit a countable signal).
	TestsPassed int
	// At is the wall-clock time the run completed.
	At time.Time
	// Supported is true when the detector implements structured parsing
	// for its language; false means agents should fall back to reading
	// raw run_tests output.
	Supported bool
}

// NewTestResultStore constructs an empty store.
func NewTestResultStore() *TestResultStore {
	return &TestResultStore{}
}

// Set replaces the stored result. Always overwrites the previous slot —
// agents care about the most recent run, not history.
func (s *TestResultStore) Set(r TestResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result = r
	s.present = true
}

// Get returns the stored result. ok is false when Set has never been
// called in this session.
func (s *TestResultStore) Get() (TestResult, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.result, s.present
}
