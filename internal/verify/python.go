package verify

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
