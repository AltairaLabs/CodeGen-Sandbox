// Package verify implements project-type detection and structured output
// parsing for the codegen sandbox's verification tools (run_tests, run_lint,
// run_typecheck, and the post-edit lint feedback baked into Edit).
package verify

import (
	"os"
	"path/filepath"
)

// Detector is the interface every supported project type implements. It tells
// the verify tools what language the project is and what commands to run for
// each verification axis.
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
}

// Detect returns a Detector for the project rooted at root, or nil if no
// known marker is found. Only the immediate root is inspected; markers in
// subdirectories do not count (the workspace root is the authoritative
// anchor per the sandbox's trust-boundary model).
func Detect(root string) Detector {
	if fileExists(filepath.Join(root, "go.mod")) {
		return &goDetector{root: root}
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
