package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goodGoSource is the fixture used by the edit_function_body tests. Has
// doc-comment, a top-level function, and a method — enough to exercise all
// three of the tool's paths with one fixture.
const goodGoSource = `package probe

// Greet returns a friendly message.
func Greet(name string) string {
	return "hello, " + name
}

type Server struct{}

// Run is the main loop.
func (s *Server) Run(x int) error {
	if x < 0 {
		return nil
	}
	return nil
}
`

func callEditFunctionBody(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleEditFunctionBody(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestEditFunctionBody_ReplacesTopLevel(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	writeAndMarkRead(t, deps, path, goodGoSource)

	res := callEditFunctionBody(t, deps, map[string]any{
		"file_path":     path,
		"function_name": "Greet",
		"new_body":      "\n\treturn \"hi, \" + name + \"!\"\n",
	})
	require.False(t, res.IsError, "unexpected error: %v", textOf(t, res))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, `return "hi, " + name + "!"`)
	// Signature and doc comment preserved.
	assert.Contains(t, body, "// Greet returns a friendly message.")
	assert.Contains(t, body, "func Greet(name string) string {")
	// File still parses: the tool's diff text mentions the new function line.
	assert.Contains(t, textOf(t, res), "edit_function_body: modified")
}

func TestEditFunctionBody_ReplacesMethod(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	writeAndMarkRead(t, deps, path, goodGoSource)

	res := callEditFunctionBody(t, deps, map[string]any{
		"file_path":     path,
		"function_name": "(*Server).Run",
		"new_body":      "\n\treturn nil\n",
	})
	require.False(t, res.IsError, "unexpected error: %v", textOf(t, res))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(data)
	// Old body gone.
	assert.NotContains(t, body, "if x < 0 {")
	// Signature preserved.
	assert.Contains(t, body, "func (s *Server) Run(x int) error {")
	// Doc comment preserved.
	assert.Contains(t, body, "// Run is the main loop.")
}

func TestEditFunctionBody_AmbiguousName(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	src := goodGoSource + "\nfunc Greet(name string) string { return name }\n"
	writeAndMarkRead(t, deps, path, src)

	res := callEditFunctionBody(t, deps, map[string]any{
		"file_path":     path,
		"function_name": "Greet",
		"new_body":      "\n\treturn \"x\"\n",
	})
	require.True(t, res.IsError, "ambiguous match must fail")
	body := textOf(t, res)
	assert.Contains(t, body, "ambiguous")
	assert.Contains(t, body, "line ")
}

func TestEditFunctionBody_NotFound(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	writeAndMarkRead(t, deps, path, goodGoSource)

	res := callEditFunctionBody(t, deps, map[string]any{
		"file_path":     path,
		"function_name": "MissingFunction",
		"new_body":      "\n\treturn nil\n",
	})
	require.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "not found")
}

func TestEditFunctionBody_InvalidReplacementNoWrite(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	writeAndMarkRead(t, deps, path, goodGoSource)

	res := callEditFunctionBody(t, deps, map[string]any{
		"file_path":     path,
		"function_name": "Greet",
		"new_body":      "\n\treturn \"hi, \" +\n", // dangling '+'
	})
	require.True(t, res.IsError, "invalid replacement must fail")
	assert.Contains(t, textOf(t, res), "replacement did not parse")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	// File unchanged.
	assert.Equal(t, goodGoSource, string(data))
}

func TestEditFunctionBody_RequiresPriorRead(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	require.NoError(t, os.WriteFile(path, []byte(goodGoSource), 0o644))
	// NOT marked read.

	res := callEditFunctionBody(t, deps, map[string]any{
		"file_path":     path,
		"function_name": "Greet",
		"new_body":      "\n\treturn name\n",
	})
	require.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "Read it first")
}

func TestEditFunctionBody_PathOutsideWorkspace(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callEditFunctionBody(t, deps, map[string]any{
		"file_path":     "/etc/passwd",
		"function_name": "Greet",
		"new_body":      "\n\treturn \"\"\n",
	})
	require.True(t, res.IsError)
}

func TestEditFunctionBody_NonGoFile(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "notes.txt")
	writeAndMarkRead(t, deps, path, "not go\n")

	res := callEditFunctionBody(t, deps, map[string]any{
		"file_path":     path,
		"function_name": "Greet",
		"new_body":      "\n\treturn nil\n",
	})
	require.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "AST support")
}

func TestEditFunctionBody_ParseErrorInSource(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	writeAndMarkRead(t, deps, path, "package probe\nfunc Broken() {\n")

	res := callEditFunctionBody(t, deps, map[string]any{
		"file_path":     path,
		"function_name": "Broken",
		"new_body":      "\n\treturn\n",
	})
	require.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "parse error")
}
