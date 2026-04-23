// Package verify implements project-type detection and structured output
// parsing for the codegen sandbox's verification tools (run_tests, run_lint,
// run_typecheck, and the post-edit lint feedback baked into Edit).
package verify

import (
	"os"
	"path/filepath"
)

// Detector is the interface every supported project type implements. It tells
// the verify tools what language the project is, what commands to run for
// each verification axis, and how to parse the linter's output into
// structured LintFinding records.
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
}

// Detect returns a Detector for the project rooted at root, or nil if no
// known marker is found. Only the immediate root is inspected; markers in
// subdirectories do not count (the workspace root is the authoritative
// anchor per the sandbox's trust-boundary model). root must be an absolute
// path; callers should resolve the workspace root before invoking.
//
// Detection order is fixed: Go, Rust, Node, Python. Where a project has
// multiple markers (e.g. a Go service with a frontend `package.json`), the
// first match wins. Operators whose workspaces contain unusual combinations
// can set up separate workspace roots per language.
func Detect(root string) Detector {
	switch {
	case fileExists(filepath.Join(root, "go.mod")):
		return &goDetector{root: root}
	case fileExists(filepath.Join(root, "Cargo.toml")):
		return &rustDetector{root: root}
	case fileExists(filepath.Join(root, "package.json")):
		return &nodeDetector{root: root}
	case fileExists(filepath.Join(root, "pyproject.toml")) || fileExists(filepath.Join(root, "setup.py")):
		return &pythonDetector{root: root}
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
