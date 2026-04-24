package verify

import (
	"regexp"
	"strconv"
	"strings"
)

// pythonDetector implements Detector for Python projects identified by a
// pyproject.toml or setup.py at the workspace root.
//
// Commands assume the operator's image has `pytest`, `ruff`, and `mypy` on
// PATH. Operators using different tooling (e.g. `pylint` instead of ruff)
// fork the image and override.
type pythonDetector struct{ root string }

// Language reports "python".
func (*pythonDetector) Language() string { return "python" }

// TestCmd returns "pytest" — runs the default test discovery from
// pyproject.toml or pytest.ini.
func (*pythonDetector) TestCmd() []string { return []string{"pytest"} }

// LintCmd returns "ruff check ." — ruff is ~100x faster than pylint and
// its output is structurally similar enough to golangci-lint that future
// ParseLint variants can share infrastructure.
func (*pythonDetector) LintCmd() []string { return []string{"ruff", "check", "."} }

// TypecheckCmd returns "mypy ." — projects without mypy configured see
// missing-binary or "no type hints" output.
func (*pythonDetector) TypecheckCmd() []string { return []string{"mypy", "."} }

// ruffLineRe matches ruff's default output on stdout:
//
//	path/to/file.py:LINE:COL: RULE [*] optional message
//
// where [*] is the optional "autofix available" marker. Rule codes look
// like F401, E501, PLR0913 — alphanumeric with no internal whitespace.
var ruffLineRe = regexp.MustCompile(
	`^(?P<file>[^:]+):(?P<line>\d+):(?P<col>\d+):\s+(?P<rule>[A-Z]+\d+)(?:\s+\[\*\])?\s+(?P<msg>.+)$`,
)

// ParseLint parses ruff's default text output from stdout. Stderr is
// ignored (ruff emits diagnostics on stdout).
func (*pythonDetector) ParseLint(stdout, _ string) []LintFinding {
	return parseLintRegex(stdout, ruffLineRe)
}

// ParseTestFailures is not yet implemented for Python; returns nil so
// last_test_failures surfaces a "not supported for python" result.
func (*pythonDetector) ParseTestFailures(_, _ string) []TestFailure { return nil }

// LSPCommand returns nil: pyright / pylsp land in a follow-up issue
// alongside codegen-sandbox-tools-python bundling.
func (*pythonDetector) LSPCommand() []string { return nil }

// FormatCheckCmd returns `ruff format --check --diff <file>`. `--check`
// makes ruff exit 1 on unformatted code without rewriting, and `--diff`
// prints the fix it would apply — handy feedback for the agent to either
// accept (re-run without `--check`) or correct manually.
func (*pythonDetector) FormatCheckCmd(file string) []string {
	return []string{"ruff", "format", "--check", "--diff", file}
}

// PackageManager returns "" for Python: the script-runner contract is
// Node-only in v1. Python's equivalents (poetry run, pipenv run, hatch
// run) are deferred to a follow-up.
func (*pythonDetector) PackageManager() string { return "" }

// ScriptRunner returns nil for Python: no package.json#scripts equivalent
// today; run_script surfaces "not supported" for Python roots.
func (*pythonDetector) ScriptRunner() []string { return nil }

// parseLintRegex is the shared implementation for regex-based per-line
// finding extraction. Named subexpressions `file`, `line`, `col`, `rule`,
// `msg` are looked up; others are ignored.
func parseLintRegex(text string, re *regexp.Regexp) []LintFinding {
	var findings []LintFinding
	fileIdx := re.SubexpIndex("file")
	lineIdx := re.SubexpIndex("line")
	colIdx := re.SubexpIndex("col")
	ruleIdx := re.SubexpIndex("rule")
	msgIdx := re.SubexpIndex("msg")
	for _, line := range strings.Split(text, "\n") {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lineNo, _ := strconv.Atoi(m[lineIdx])
		col, _ := strconv.Atoi(m[colIdx])
		f := LintFinding{
			File:    m[fileIdx],
			Line:    lineNo,
			Column:  col,
			Message: m[msgIdx],
		}
		if ruleIdx >= 0 {
			f.Rule = m[ruleIdx]
		}
		findings = append(findings, f)
	}
	return findings
}
