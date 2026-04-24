// Package verify implements project-type detection and structured output
// parsing for the codegen sandbox's verification tools (run_tests, run_lint,
// run_typecheck, and the post-edit lint feedback baked into Edit).
package verify

import (
	"os"
	"path/filepath"
)

// Detect returns the first Detector whose marker is present at root, or nil
// if no known marker is found. Equivalent to DetectAll(root)[0] when the
// workspace contains exactly one project type. Kept for callers (and tests)
// that want first-match semantics — polyglot-aware tools should use
// DetectAll instead so the agent can pick a specific language via the
// `language` arg surfaced on each verify tool. See #19 for the polyglot
// contract.
func Detect(root string) Detector {
	all := DetectAll(root)
	if len(all) == 0 {
		return nil
	}
	return all[0]
}

// DetectAll returns every Detector whose marker is present at root, in a
// stable order: Go, Rust, Node, Python. An empty slice means no recognised
// marker. Only the immediate root is inspected; markers in subdirectories
// do not count (the workspace root is the authoritative anchor per the
// sandbox's trust-boundary model). root must be an absolute path; callers
// should resolve the workspace root before invoking.
//
// Polyglot workspaces (e.g. a Go service with a frontend `package.json`,
// or a Python service with a Rust extension crate) return more than one
// detector. Tool callers — see internal/tools.dispatchByLanguage — apply
// the polyglot policy: 0 → error, 1 → use it, N + no language hint →
// error listing the detected languages, N + language hint → look up the
// matching detector or error if not present.
func DetectAll(root string) []Detector {
	var out []Detector
	if fileExists(filepath.Join(root, "go.mod")) {
		out = append(out, &goDetector{root: root})
	}
	if fileExists(filepath.Join(root, "Cargo.toml")) {
		out = append(out, &rustDetector{root: root})
	}
	if fileExists(filepath.Join(root, "package.json")) {
		out = append(out, newNodeDetector(root))
	}
	if fileExists(filepath.Join(root, "pyproject.toml")) || fileExists(filepath.Join(root, "setup.py")) {
		out = append(out, &pythonDetector{root: root})
	}
	return out
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
