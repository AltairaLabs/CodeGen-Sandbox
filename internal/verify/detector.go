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
	// FormatCheckCmd returns the argv for checking the formatting of a
	// single file (typically passed as the last argument). Implementations
	// return nil when no formatter is wired for the language, in which case
	// callers (currently the post-edit format hook in Edit) skip the check
	// silently. A non-nil result whose first element isn't on PATH surfaces
	// a "<binary> not found on PATH" line — formatting feedback is
	// advisory, never an Edit-level failure.
	//
	// The file argument is the workspace-relative path to the edited file,
	// not an absolute path — formatters emit relative paths in their
	// output, so keeping the input consistent avoids cross-path confusion
	// when output is surfaced back to the agent.
	FormatCheckCmd(file string) []string
	// PackageManager returns a short identifier for the detected package
	// manager (e.g. "npm", "pnpm", "yarn", "bun"). Only Node projects
	// return a value today; every other detector returns "". Callers use
	// the empty value as a signal that script-based invocations (e.g.
	// run_script) do not apply to this language.
	PackageManager() string
	// ScriptRunner returns the argv prefix for "run a script" under this
	// detector's package manager. For Node: npm → ["npm", "run"], yarn →
	// ["yarn"] (yarn omits "run"), pnpm → ["pnpm", "run"], bun → ["bun",
	// "run"]. Non-Node detectors return nil. A nil result means the tool
	// surface for scripts (run_script) is not supported for this language.
	ScriptRunner() []string
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
