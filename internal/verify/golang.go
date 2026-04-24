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

// TestCmd returns "go test -json -count=1 ./..." — `-json` emits test2json
// events on stdout so ParseTestFailures can extract structured failures;
// `-count=1` defeats the build cache so a re-run actually re-runs.
func (*goDetector) TestCmd() []string {
	return []string{"go", "test", "-json", "-count=1", "./..."}
}

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

// ParseTestFailures parses `go test -json` test2json events from stdout.
// Stderr is ignored — test2json emits everything on stdout. Never returns
// an error; malformed lines are skipped.
func (*goDetector) ParseTestFailures(stdout, _ string) []TestFailure {
	return ParseGoTest2JSON(stdout)
}

// LSPCommand returns the gopls launch command. `gopls serve` is the canonical
// stdio invocation. The sandbox image may not include gopls (it ships in
// codegen-sandbox-tools-go per #26); the LSP layer surfaces "gopls not on
// PATH" cleanly when absent.
func (*goDetector) LSPCommand() []string { return []string{"gopls", "serve"} }

// FormatCheckCmd returns nil for Go: the existing post-edit lint path runs
// golangci-lint, whose default configuration includes gofmt / gofumpt
// coverage that subsumes a standalone format-check hook. Running both would
// surface the same formatting drift twice.
func (*goDetector) FormatCheckCmd(_ string) []string { return nil }

// PackageManager returns "" for Go: modules are the native package system,
// and the script-runner contract is Node-only in v1.
func (*goDetector) PackageManager() string { return "" }

// ScriptRunner returns nil for Go: no equivalent of package.json#scripts
// in the Go ecosystem; run_script surfaces "not supported" for Go roots.
func (*goDetector) ScriptRunner() []string { return nil }
