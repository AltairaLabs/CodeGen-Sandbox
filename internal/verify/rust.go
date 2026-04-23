package verify

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

// LintCmd returns "cargo clippy --all-targets -- -D warnings" — fails the
// lint on warnings for stricter feedback, mirroring how most Rust CI
// pipelines are configured.
func (*rustDetector) LintCmd() []string {
	return []string{"cargo", "clippy", "--all-targets", "--", "-D", "warnings"}
}

// TypecheckCmd returns "cargo check" — compiles without producing
// binaries, the fastest way to get type-checking feedback.
func (*rustDetector) TypecheckCmd() []string { return []string{"cargo", "check", "--all-targets"} }
