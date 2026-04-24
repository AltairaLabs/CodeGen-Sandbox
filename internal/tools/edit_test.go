package tools_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callEdit(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleEdit(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func writeAndMarkRead(t *testing.T, deps *tools.Deps, path, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	deps.Tracker.MarkRead(path)
}

func TestEdit_EmptyOldStringRejected(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha\nbeta\n")

	res := callEdit(t, deps, map[string]any{
		"file_path":   path,
		"old_string":  "",
		"new_string":  "X",
		"replace_all": true,
	})
	require.True(t, res.IsError, "empty old_string must be rejected to prevent file corruption")

	data, _ := os.ReadFile(path)
	assert.Equal(t, "alpha\nbeta\n", string(data), "file must not be modified")
}

func TestEdit_ReplacesFirstOccurrence(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha\nbeta\nalpha\n")

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "gamma",
	})
	assert.True(t, res.IsError, "non-unique match without replace_all must fail")
}

func TestEdit_UniqueReplace(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha\nbeta\n")

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "gamma",
	})
	require.False(t, res.IsError, "unexpected error: %v", res.Content)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "gamma\nbeta\n", string(data))
}

func TestEdit_ReplaceAll(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha alpha alpha")

	res := callEdit(t, deps, map[string]any{
		"file_path":   path,
		"old_string":  "alpha",
		"new_string":  "gamma",
		"replace_all": true,
	})
	require.False(t, res.IsError)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "gamma gamma gamma", string(data))
}

func TestEdit_RequiresPriorRead(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	require.NoError(t, os.WriteFile(path, []byte("alpha"), 0o644))
	// intentionally do not mark as read

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "beta",
	})
	assert.True(t, res.IsError)
}

func TestEdit_OldStringNotFound(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha")

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "zulu",
		"new_string": "beta",
	})
	assert.True(t, res.IsError)
}

func TestEdit_MissingFile(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "nope.txt")

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "x",
		"new_string": "y",
	})
	assert.True(t, res.IsError)
}

func TestEdit_PathOutsideWorkspace(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callEdit(t, deps, map[string]any{
		"file_path":  "/etc/passwd",
		"old_string": "root",
		"new_string": "evil",
	})
	assert.True(t, res.IsError)
}

func TestEdit_MissingParams(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "a.txt")
	writeAndMarkRead(t, deps, path, "alpha")

	cases := []map[string]any{
		{"old_string": "alpha", "new_string": "beta"},
		{"file_path": path, "new_string": "beta"},
		{"file_path": path, "old_string": "alpha"},
	}
	for _, args := range cases {
		res := callEdit(t, deps, args)
		assert.True(t, res.IsError, "args=%v", args)
	}
}

func TestEdit_PostEditLintFeedback(t *testing.T) {
	requireGolangciLintTool(t)
	deps, root := newTestDeps(t)

	// Set up a Go module with a .golangci.yml that ONLY enables errcheck —
	// so we get deterministic findings regardless of the default lint set.
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".golangci.yml"), []byte(
		"version: \"2\"\nlinters:\n  default: none\n  enable:\n    - errcheck\n",
	), 0o644))

	// Start with a file that lints clean.
	path := filepath.Join(root, "probe.go")
	good := "package probe\n\nimport \"os\"\n\nfunc Write() error { return os.WriteFile(\"x\", []byte(\"y\"), 0o644) }\n"
	writeAndMarkRead(t, deps, path, good)

	// Edit introduces an unchecked error — errcheck should fire.
	bad := "package probe\n\nimport \"os\"\n\nfunc Write() { os.WriteFile(\"x\", []byte(\"y\"), 0o644) }\n"
	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": good,
		"new_string": bad,
	})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "replaced")
	assert.Contains(t, body, "post-edit lint findings")
	assert.Contains(t, body, "probe.go")
	assert.Contains(t, body, "errcheck")
}

func TestEdit_PostEditLintSilentOnNonGoProject(t *testing.T) {
	// No go.mod — Detect returns nil — no feedback block should appear.
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "notes.txt")
	writeAndMarkRead(t, deps, path, "alpha\nbeta\n")

	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "gamma",
	})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "replaced")
	assert.NotContains(t, body, "post-edit lint findings")
	assert.NotContains(t, body, "--- format ---")
}

// stubFormatter writes a shell script at <dir>/<name> that exits with
// exitCode after printing msg, then prepends <dir> to PATH for the life of
// the test. Lets us assert Edit invokes `prettier` / `ruff` / `rustfmt`
// without needing the real binaries on the test host, and without ever
// hitting a real formatter.
func stubFormatter(t *testing.T, name, msg string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub formatter uses a POSIX shell script; skip on Windows")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, name)
	// Capture the arguments the script was invoked with so the test can
	// verify the edited file made it through to the formatter argv.
	argsLogPath := filepath.Join(dir, name+".args")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nprintf %q\nexit %d\n", argsLogPath, msg, exitCode)
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return argsLogPath
}

func TestEdit_PostEditFormatPython(t *testing.T) {
	deps, root := newTestDeps(t)
	// pyproject.toml selects the python detector.
	require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[tool.ruff]\n"), 0o644))
	argsLog := stubFormatter(t, "ruff", "--- hello.py ---\n-x=1\n+x = 1\n", 1)

	path := filepath.Join(root, "hello.py")
	writeAndMarkRead(t, deps, path, "alpha\n")
	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "gamma",
	})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "--- format ---")
	assert.Contains(t, body, "hello.py")

	args, err := os.ReadFile(argsLog)
	require.NoError(t, err)
	// Expected argv: format --check --diff hello.py
	argsText := string(args)
	assert.Contains(t, argsText, "format")
	assert.Contains(t, argsText, "--check")
	assert.Contains(t, argsText, "--diff")
	assert.Contains(t, argsText, "hello.py")
}

func TestEdit_PostEditFormatNode(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte("{}\n"), 0o644))
	argsLog := stubFormatter(t, "prettier", "[warn] src/index.ts\n", 1)

	path := filepath.Join(root, "index.ts")
	writeAndMarkRead(t, deps, path, "let x = 1\n")
	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "let x = 1",
		"new_string": "const x = 1",
	})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "--- format ---")
	assert.Contains(t, body, "index.ts")

	args, err := os.ReadFile(argsLog)
	require.NoError(t, err)
	argsText := string(args)
	assert.Contains(t, argsText, "--check")
	assert.Contains(t, argsText, "index.ts")
}

func TestEdit_PostEditFormatRust(t *testing.T) {
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[package]\nname=\"p\"\n"), 0o644))
	argsLog := stubFormatter(t, "rustfmt", "Diff in src/lib.rs\n", 1)

	path := filepath.Join(root, "lib.rs")
	writeAndMarkRead(t, deps, path, "fn foo(){}\n")
	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "fn foo(){}",
		"new_string": "fn foo() {}",
	})
	require.False(t, res.IsError)

	body := textOf(t, res)
	assert.Contains(t, body, "--- format ---")

	args, err := os.ReadFile(argsLog)
	require.NoError(t, err)
	argsText := string(args)
	assert.Contains(t, argsText, "--check")
	assert.Contains(t, argsText, "lib.rs")
}

func TestEdit_PostEditFormatCleanIsSilent(t *testing.T) {
	// Formatter exits 0 → file is formatted → no format section rendered.
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[tool.ruff]\n"), 0o644))
	_ = stubFormatter(t, "ruff", "", 0)

	path := filepath.Join(root, "hello.py")
	writeAndMarkRead(t, deps, path, "alpha\n")
	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "gamma",
	})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "replaced")
	assert.NotContains(t, body, "--- format ---")
}

func TestEdit_PostEditFormatBinaryMissing(t *testing.T) {
	// Detector advertises a formatter; binary not on PATH. Edit surfaces
	// a one-line advisory (never fails).
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[tool.ruff]\n"), 0o644))
	t.Setenv("PATH", t.TempDir()) // empty PATH — no ruff reachable.

	path := filepath.Join(root, "hello.py")
	writeAndMarkRead(t, deps, path, "alpha\n")
	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "gamma",
	})
	require.False(t, res.IsError, "Edit must not fail when formatter is missing")
	body := textOf(t, res)
	assert.Contains(t, body, "post-edit format: ruff not found on PATH")
}

func TestEdit_PostEditFormatGoReturnsNilDetector(t *testing.T) {
	// Go detector's FormatCheckCmd returns nil: no format section even
	// though the Go lint path is fully wired. Guards the additive design
	// — format is not the Go lint's replacement, just a sibling for
	// languages whose lint doesn't already cover formatting.
	deps, root := newTestDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644))

	path := filepath.Join(root, "probe.go")
	writeAndMarkRead(t, deps, path, "package probe\n\nfunc Add(a, b int) int { return a + b }\n")
	res := callEdit(t, deps, map[string]any{
		"file_path":  path,
		"old_string": "a + b",
		"new_string": "a - b",
	})
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.NotContains(t, body, "--- format ---")
}
