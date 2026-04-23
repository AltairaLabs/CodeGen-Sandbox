package verify

import "regexp"

// rustDetector implements Detector for Rust projects identified by a
// Cargo.toml at the workspace root.
//
// Commands assume the operator's image has `cargo` + `clippy` on PATH.
// The `rust:latest` Docker image provides both by default; `rust:slim`
// omits clippy and requires `rustup component add clippy`.
type rustDetector struct{ root string }

// Language reports "rust".
func (*rustDetector) Language() string { return "rust" }

// TestCmd returns "cargo test".
func (*rustDetector) TestCmd() []string { return []string{"cargo", "test"} }

// LintCmd returns cargo clippy with --message-format=short so diagnostics
// are one-line-per-finding (path:line:col: level: message) rather than
// cargo's default multi-line layout. --all-targets picks up tests/bench
// code, -D warnings promotes warnings to errors (the typical CI posture).
func (*rustDetector) LintCmd() []string {
	return []string{
		"cargo", "clippy",
		"--all-targets",
		"--message-format=short",
		"--", "-D", "warnings",
	}
}

// TypecheckCmd returns "cargo check" — compiles without producing
// binaries, the fastest way to get type-checking feedback.
func (*rustDetector) TypecheckCmd() []string { return []string{"cargo", "check", "--all-targets"} }

// clippyLineRe matches cargo clippy's --message-format=short output on
// stderr:
//
//	src/main.rs:3:9: warning: unused variable: `x`
//	src/main.rs:5:5: error: type mismatch, expected ...
//
// The rule name isn't present in short-form output (it's only in the
// longer multi-line default). We capture the severity into Rule so
// agents can still distinguish errors from warnings.
var clippyLineRe = regexp.MustCompile(
	`^(?P<file>[^:]+):(?P<line>\d+):(?P<col>\d+):\s+(?P<rule>warning|error):\s+(?P<msg>.+)$`,
)

// ParseLint parses cargo clippy --message-format=short output on stderr.
// Stdout is ignored (cargo writes build progress there).
func (*rustDetector) ParseLint(_, stderr string) []LintFinding {
	return parseLintRegex(stderr, clippyLineRe)
}
