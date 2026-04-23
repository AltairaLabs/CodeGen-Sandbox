package verify

// goDetector implements Detector for Go projects identified by a go.mod at
// the workspace root.
type goDetector struct {
	// root is retained for future root-relative helpers attached to the
	// detector (e.g. project-specific test-command overrides). Currently
	// unused — callers pass root explicitly to verify.Lint and friends.
	root string
}

// Language reports "go".
func (*goDetector) Language() string { return "go" }

// TestCmd returns "go test ./..." — runs every package in the module.
func (*goDetector) TestCmd() []string { return []string{"go", "test", "./..."} }

// LintCmd returns "golangci-lint run ./..." — matches the project's Makefile
// convention and the golangci-lint v2 invocation shape.
func (*goDetector) LintCmd() []string { return []string{"golangci-lint", "run", "./..."} }

// TypecheckCmd returns "go vet ./..." — Go's native "does this type-check
// and pass static checks" command.
func (*goDetector) TypecheckCmd() []string { return []string{"go", "vet", "./..."} }

// ParseLint parses golangci-lint v2's default text output from stdout.
// Stderr is ignored — golangci-lint emits diagnostics on stdout and only
// uses stderr for tool-level errors.
func (*goDetector) ParseLint(stdout, _ string) []LintFinding {
	return ParseLint(stdout)
}
