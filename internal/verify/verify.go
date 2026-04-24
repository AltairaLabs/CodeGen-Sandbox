// Package verify implements project-type detection and structured output
// parsing for the codegen sandbox's verification tools (run_tests, run_lint,
// run_typecheck, and the post-edit lint feedback baked into Edit).
package verify

import (
	"os"
	"path/filepath"
)

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
		return newNodeDetector(root)
	case fileExists(filepath.Join(root, "pyproject.toml")) || fileExists(filepath.Join(root, "setup.py")):
		return &pythonDetector{root: root}
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// AllDetectors returns one instance of each registered Detector. Order is
// stable and matches Detect()'s detection order (Go, Rust, Node, Python).
// Used by callers that need to enumerate per-language capabilities
// (currently: LSP argv lookup) without having to resolve a workspace first.
func AllDetectors() []Detector {
	return []Detector{
		&goDetector{},
		&rustDetector{},
		// Zero-value nodeDetector is fine for the language-→argv lookups
		// AllDetectors is used for (LSPCommand, FormatCheckCmd with a
		// literal file arg). It has no pm / scripts populated — which is
		// only relevant to per-workspace *Cmd methods that Detect() is
		// used for.
		&nodeDetector{},
		&pythonDetector{},
	}
}
