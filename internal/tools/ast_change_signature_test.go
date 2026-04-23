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

const sigFixture = `package probe

// Greet says hi.
func Greet(name string) string {
	msg := "hello, " + name
	return msg
}

type Server struct{}

// Run does things.
func (s *Server) Run(x int) error {
	if x < 0 {
		return nil
	}
	return nil
}
`

func callChangeFunctionSignature(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleChangeFunctionSignature(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestChangeFunctionSignature_RenamesFunctionAndAddsParam(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	writeAndMarkRead(t, deps, path, sigFixture)

	res := callChangeFunctionSignature(t, deps, map[string]any{
		"file_path":     path,
		"function_name": "Greet",
		"new_signature": "func Greet(name, greeting string) string",
	})
	require.False(t, res.IsError, "unexpected error: %v", textOf(t, res))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, "func Greet(name, greeting string) string {")
	// Body preserved verbatim.
	assert.Contains(t, body, `msg := "hello, " + name`)
	assert.Contains(t, body, "return msg")
	// Doc comment preserved.
	assert.Contains(t, body, "// Greet says hi.")
}

func TestChangeFunctionSignature_BodyLiterallyPreserved(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	writeAndMarkRead(t, deps, path, sigFixture)

	res := callChangeFunctionSignature(t, deps, map[string]any{
		"file_path":     path,
		"function_name": "(*Server).Run",
		"new_signature": "func (s *Server) Run(ctx context.Context, x int) error",
	})
	require.False(t, res.IsError, "unexpected error: %v", textOf(t, res))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(data)
	// The original body text must be present, character for character.
	assert.Contains(t, body, "if x < 0 {\n\t\treturn nil\n\t}\n\treturn nil\n")
}

func TestChangeFunctionSignature_InvalidSignature(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	writeAndMarkRead(t, deps, path, sigFixture)

	res := callChangeFunctionSignature(t, deps, map[string]any{
		"file_path":     path,
		"function_name": "Greet",
		"new_signature": "func Greet(name string", // unbalanced paren
	})
	require.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "replacement did not parse")

	data, _ := os.ReadFile(path)
	assert.Equal(t, sigFixture, string(data), "file must be unchanged on invalid input")
}

func TestChangeFunctionSignature_NotFound(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	writeAndMarkRead(t, deps, path, sigFixture)

	res := callChangeFunctionSignature(t, deps, map[string]any{
		"file_path":     path,
		"function_name": "Missing",
		"new_signature": "func Missing() error",
	})
	require.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "not found")
}

func TestChangeFunctionSignature_RequiresPriorRead(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	require.NoError(t, os.WriteFile(path, []byte(sigFixture), 0o644))

	res := callChangeFunctionSignature(t, deps, map[string]any{
		"file_path":     path,
		"function_name": "Greet",
		"new_signature": "func Greet(name string) string",
	})
	require.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "Read it first")
}

func TestChangeFunctionSignature_PathOutsideWorkspace(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callChangeFunctionSignature(t, deps, map[string]any{
		"file_path":     "/etc/passwd",
		"function_name": "Greet",
		"new_signature": "func Greet() string",
	})
	require.True(t, res.IsError)
}
