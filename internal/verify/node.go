package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// nodeDetector implements Detector for Node projects identified by a
// package.json at the workspace root.
//
// Package-manager selection is lock-file driven: pnpm-lock.yaml → pnpm,
// yarn.lock → yarn, bun.lockb → bun, package-lock.json → npm, nothing →
// npm (fallback). The detected PM drives TestCmd / LintCmd / TypecheckCmd
// whenever the corresponding script is defined in package.json#scripts
// — that way agents running against a pnpm workspace get `pnpm run test`
// instead of the broken `npm test --silent`.
//
// Commands assume the operator's image has the detected PM's binary on
// PATH. If the binary isn't present, runVerifyCmd surfaces a
// "<binary> not found on PATH" message — no pre-check is wired here so
// operators who bundle pnpm / yarn / bun via feature-tool-image layers
// (issue #26 follow-ons) see the same error path as any other missing
// binary.
type nodeDetector struct {
	root string
	// pm is the detected package manager: "npm" | "pnpm" | "yarn" | "bun".
	// Populated at construction time in Detect().
	pm string
	// scripts mirrors the keys of package.json#scripts. Empty / nil when
	// package.json is missing, unreadable, or malformed — the detector
	// degrades silently back to the hardcoded defaults in that case.
	scripts map[string]bool
}

// newNodeDetector builds a Node detector for root, reading lock-file
// markers and package.json#scripts up front so all subsequent *Cmd
// methods are pure lookups.
func newNodeDetector(root string) *nodeDetector {
	return &nodeDetector{
		root:    root,
		pm:      detectNodePackageManager(root),
		scripts: readPackageJSONScripts(root),
	}
}

// detectNodePackageManager inspects lock-file presence at root and picks
// the canonical PM. Priority (first match wins): pnpm > yarn > bun > npm.
// Fallback is "npm" when no lock file is present, matching the historical
// behaviour of this detector.
func detectNodePackageManager(root string) string {
	switch {
	case fileExists(filepath.Join(root, "pnpm-lock.yaml")):
		return "pnpm"
	case fileExists(filepath.Join(root, "yarn.lock")):
		return "yarn"
	case fileExists(filepath.Join(root, "bun.lockb")):
		return "bun"
	case fileExists(filepath.Join(root, "package-lock.json")):
		return "npm"
	}
	return "npm"
}

// readPackageJSONScripts reads package.json at root and returns a set of
// defined script names. Any failure (missing file, unreadable, malformed
// JSON, non-object "scripts") yields an empty map — callers interpret
// that as "no scripts defined" and fall back to hardcoded defaults.
func readPackageJSONScripts(root string) map[string]bool {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var parsed struct {
		Scripts map[string]json.RawMessage `json:"scripts"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	if len(parsed.Scripts) == 0 {
		return nil
	}
	out := make(map[string]bool, len(parsed.Scripts))
	for name := range parsed.Scripts {
		out[name] = true
	}
	return out
}

// Language reports "node".
func (*nodeDetector) Language() string { return "node" }

// TestCmd prefers the project-defined "test" script via `<pm> run test`
// when present, falling back to the historical `npm test --silent`.
// The fallback keeps the pre-PM-detection behaviour for bare package.json
// projects with no lock file and no scripts section.
func (d *nodeDetector) TestCmd() []string {
	if d.scripts["test"] {
		return d.scriptInvocation("test")
	}
	return []string{"npm", "test", "--silent"}
}

// LintCmd prefers the project-defined "lint" script via `<pm> run lint`
// when present, falling back to `npx eslint . --format=json`. The JSON
// formatter is the most stable of eslint's built-ins — `compact` was
// removed from the core in v9, leaving stylish (human-only), html,
// json, and unix. ParseLint reads stdout as JSON to extract findings.
func (d *nodeDetector) LintCmd() []string {
	if d.scripts["lint"] {
		return d.scriptInvocation("lint")
	}
	return []string{"npx", "--no-install", "eslint", ".", "--format=json"}
}

// TypecheckCmd prefers the project-defined "typecheck" script via
// `<pm> run typecheck` when present, falling back to
// `npx tsc --noEmit`. Next.js agents typically define
// `"typecheck": "tsc --noEmit"` alongside build, which this picks up.
func (d *nodeDetector) TypecheckCmd() []string {
	if d.scripts["typecheck"] {
		return d.scriptInvocation("typecheck")
	}
	return []string{"npx", "--no-install", "tsc", "--noEmit"}
}

// eslintJSONFile is one element of eslint's --format=json output: an
// array of per-file objects each with a `messages` slice. Only the
// fields ParseLint consumes are decoded; eslint emits more (output,
// usedDeprecatedRules, suppressedMessages, etc.) which we discard.
type eslintJSONFile struct {
	FilePath string          `json:"filePath"`
	Messages []eslintJSONMsg `json:"messages"`
}

type eslintJSONMsg struct {
	RuleID   string `json:"ruleId"`
	Severity int    `json:"severity"`
	Message  string `json:"message"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

// ParseLint parses eslint's --format=json output on stdout. The JSON
// formatter is stable across eslint v8 and v9 (the legacy `compact`
// formatter was removed from core in v9), so this is the long-term
// canonical shape.
//
// Severity 1 = warning, 2 = error in eslint's wire format; we surface
// both as findings without a level label (LintFinding has no severity
// field today). Findings without a ruleId (parse errors emitted by
// eslint itself) are kept — agents need to see the parse error.
func (*nodeDetector) ParseLint(stdout, _ string) []LintFinding {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil
	}
	var files []eslintJSONFile
	if err := json.Unmarshal([]byte(stdout), &files); err != nil {
		return nil
	}
	var out []LintFinding
	for _, f := range files {
		for _, m := range f.Messages {
			out = append(out, LintFinding{
				File:    f.FilePath,
				Line:    m.Line,
				Column:  m.Column,
				Rule:    m.RuleID,
				Message: m.Message,
			})
		}
	}
	return out
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

// PackageManager returns the detected PM identifier ("npm" | "pnpm" |
// "yarn" | "bun").
func (d *nodeDetector) PackageManager() string { return d.pm }

// ScriptRunner returns the argv prefix for "run a script" under the
// detected PM. yarn omits "run" (legacy behaviour preserved across v1
// and v2); npm / pnpm / bun all need the explicit subcommand.
func (d *nodeDetector) ScriptRunner() []string {
	switch d.pm {
	case "yarn":
		return []string{"yarn"}
	case "pnpm":
		return []string{"pnpm", "run"}
	case "bun":
		return []string{"bun", "run"}
	default:
		// npm and unset both route through "npm run".
		return []string{"npm", "run"}
	}
}

// scriptInvocation returns the full argv for invoking the named script
// under this detector's PM (e.g. ["pnpm", "run", "test"]).
func (d *nodeDetector) scriptInvocation(name string) []string {
	runner := d.ScriptRunner()
	out := make([]string, 0, len(runner)+1)
	out = append(out, runner...)
	out = append(out, name)
	return out
}
