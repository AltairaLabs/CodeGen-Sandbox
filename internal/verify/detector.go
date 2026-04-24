package verify

// Detector is the interface every supported project type implements. It tells
// the verify tools what language the project is, what commands to run for
// each verification axis, and how to parse runner output into structured
// records.
type Detector interface {
	// Language returns a short identifier for the detected project type
	// (e.g. "go", "node").
	Language() string
	// TestCmd returns the argv (including the binary name) for running the
	// project's test suite from the workspace root.
	TestCmd() []string
	// LintCmd returns the argv for running the project's linter.
	LintCmd() []string
	// TypecheckCmd returns the argv for running the project's type checker.
	TypecheckCmd() []string
	// ParseLint extracts structured findings from the linter's output.
	// stdout and stderr are passed separately because linters differ in
	// which stream they write to (golangci-lint / ruff / eslint: stdout;
	// clippy / go vet: stderr). Unrecognised lines are silently skipped.
	ParseLint(stdout, stderr string) []LintFinding
	// ParseTestFailures parses the combined stdout+stderr of the detector's
	// TestCmd and returns a structured failure list. Detectors whose TestCmd
	// emits a machine-readable format (e.g. Go's `-json`) parse that; others
	// return nil until a per-language implementation lands.
	//
	// The parser MUST be total — never panic, never return an error, skip
	// lines it doesn't recognise. An empty or nil slice means "no failures
	// detected" or "format not understood"; last_test_failures surfaces the
	// distinction via the detector's language label.
	ParseTestFailures(stdout, stderr string) []TestFailure
	// LSPCommand returns the argv for launching the language server for
	// this detector's language, or nil when no server is wired. Callers
	// surface a "LSP not configured for this language" message for nil
	// results; a non-nil result whose first element isn't on PATH surfaces
	// a "<binary> not found on PATH" message at Start time.
	LSPCommand() []string
}

// TestFailure is one structured test failure extracted from a test runner's
// output. Fields are best-effort: runners that don't emit a particular piece
// of information leave the field zero-valued.
type TestFailure struct {
	// File is the source file the failure was reported from, relative to
	// the workspace root when derivable, empty otherwise.
	File string
	// Line is the 1-based line number; 0 means "unknown".
	Line int
	// TestName is the fully qualified test identifier, e.g.
	// "example.com/pkg/foo:TestValidate/empty_input".
	TestName string
	// Message is the failure message or first stack line, trimmed.
	Message string
	// Diff is the expected-vs-actual diff block when the runner emitted one,
	// otherwise empty.
	Diff string
}
