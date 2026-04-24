package verify

import "regexp"

// nodeDetector implements Detector for Node projects identified by a
// package.json at the workspace root.
//
// Commands assume the operator's image has `npm` + `npx` on PATH. If the
// project uses a different package manager (pnpm, yarn, bun), run_tests
// and friends will fail with "binary not found on PATH"; operators fork
// the image and adjust.
type nodeDetector struct{ root string }

// Language reports "node".
func (*nodeDetector) Language() string { return "node" }

// TestCmd returns "npm test --silent".
func (*nodeDetector) TestCmd() []string { return []string{"npm", "test", "--silent"} }

// LintCmd returns "npx eslint . --format=compact". The `compact` format
// emits single-line findings that future ParseLint variants can parse.
func (*nodeDetector) LintCmd() []string {
	return []string{"npx", "--no-install", "eslint", ".", "--format=compact"}
}

// TypecheckCmd returns "npx tsc --noEmit". Projects without TypeScript
// will see a missing-binary error; that's a signal they should run lint
// only, not typecheck.
func (*nodeDetector) TypecheckCmd() []string {
	return []string{"npx", "--no-install", "tsc", "--noEmit"}
}

// eslintLineRe matches eslint's --format=compact output on stdout:
//
//	/path/to/file.js: line 5, col 3, Error - Missing semicolon (semi)
//
// Level is Error|Warning. The message may contain any chars; the rule is
// the final parenthesised token on the line (same approach as golangci-lint).
var eslintLineRe = regexp.MustCompile(
	`^(?P<file>[^:]+):\s+line\s+(?P<line>\d+),\s+col\s+(?P<col>\d+),\s+\w+\s+-\s+(?P<msg>.+?)\s+\((?P<rule>[A-Za-z][A-Za-z0-9_\-\/]*)\)\s*$`,
)

// ParseLint parses eslint's --format=compact output on stdout.
func (*nodeDetector) ParseLint(stdout, _ string) []LintFinding {
	return parseLintRegex(stdout, eslintLineRe)
}

// ParseTestFailures is not yet implemented for Node; returns nil so
// last_test_failures surfaces a "not supported for node" result.
func (*nodeDetector) ParseTestFailures(_, _ string) []TestFailure { return nil }

// LSPCommand returns nil: the Node language server (typescript-language-server)
// lands in a follow-up issue alongside codegen-sandbox-tools-node bundling.
// Callers surface a "LSP not configured for node" error.
func (*nodeDetector) LSPCommand() []string { return nil }

// FormatCheckCmd returns `prettier --check <file>`. `--check` reports which
// files would be reformatted without rewriting. Prettier is assumed to be
// on PATH directly (operators who install it project-local can shadow the
// binary via a wrapper script); we deliberately avoid `npx` here to keep
// the per-edit latency bounded — `npx` without `--no-install` can drag in
// a network fetch, and `--no-install` adds ambiguity when the project
// happens to have prettier installed locally vs globally.
func (*nodeDetector) FormatCheckCmd(file string) []string {
	return []string{"prettier", "--check", file}
}
