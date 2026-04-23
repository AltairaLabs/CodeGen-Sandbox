package tools_test

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const insertFixture = `package probe

type Server struct{}

// Run is the main loop.
func (s *Server) Run(x int) error {
	return nil
}

// Close shuts down.
func (s *Server) Close() error {
	return nil
}
`

func callInsertAfterMethod(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleInsertAfterMethod(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func assertParses(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	fset := token.NewFileSet()
	_, err = parser.ParseFile(fset, path, data, parser.AllErrors)
	require.NoError(t, err, "file must parse after edit:\n%s", string(data))
}

func TestInsertAfterMethod_HappyPath(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	writeAndMarkRead(t, deps, path, insertFixture)

	newMethod := "// Start begins execution.\nfunc (s *Server) Start() error {\n\treturn nil\n}"
	res := callInsertAfterMethod(t, deps, map[string]any{
		"file_path":     path,
		"receiver_type": "*Server",
		"method_name":   "Run",
		"new_method":    newMethod,
	})
	require.False(t, res.IsError, "unexpected error: %v", textOf(t, res))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(data)
	// New method present.
	assert.Contains(t, body, "func (s *Server) Start() error {")
	// Anchor method still there.
	assert.Contains(t, body, "func (s *Server) Run(x int) error {")
	// Ordering: Run comes before Start, Start comes before Close.
	runIdx := strings.Index(body, "func (s *Server) Run")
	startIdx := strings.Index(body, "func (s *Server) Start")
	closeIdx := strings.Index(body, "func (s *Server) Close")
	assert.Less(t, runIdx, startIdx, "new method must follow anchor")
	assert.Less(t, startIdx, closeIdx, "new method must precede existing trailing method")

	assertParses(t, path)
}

func TestInsertAfterMethod_ZeroIndentPeerInsertsAtColumnZero(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	writeAndMarkRead(t, deps, path, insertFixture)

	// Anchor is at column 0; tool must not add any leading whitespace.
	newMethod := "func (s *Server) Start() error { return nil }"
	res := callInsertAfterMethod(t, deps, map[string]any{
		"file_path":     path,
		"receiver_type": "*Server",
		"method_name":   "Run",
		"new_method":    newMethod,
	})
	require.False(t, res.IsError, "unexpected error: %v", textOf(t, res))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, "\nfunc (s *Server) Start() error { return nil }\n")
	assertParses(t, path)
}

func TestInsertAfterMethod_FirstMethodAnchor(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	singleMethod := "package probe\n\ntype Server struct{}\n\nfunc (s *Server) Run() {}\n"
	writeAndMarkRead(t, deps, path, singleMethod)

	newMethod := "func (s *Server) Stop() {}"
	res := callInsertAfterMethod(t, deps, map[string]any{
		"file_path":     path,
		"receiver_type": "*Server",
		"method_name":   "Run",
		"new_method":    newMethod,
	})
	require.False(t, res.IsError, "unexpected error: %v", textOf(t, res))

	data, _ := os.ReadFile(path)
	body := string(data)
	assert.Contains(t, body, "func (s *Server) Stop() {}")
	assertParses(t, path)
}

func TestInsertAfterMethod_AnchorNotFound_ListsMethods(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	writeAndMarkRead(t, deps, path, insertFixture)

	res := callInsertAfterMethod(t, deps, map[string]any{
		"file_path":     path,
		"receiver_type": "*Server",
		"method_name":   "Missing",
		"new_method":    "func (s *Server) Stop() {}",
	})
	require.True(t, res.IsError)
	msg := textOf(t, res)
	assert.Contains(t, msg, "not found")
	assert.Contains(t, msg, "available")
	// List includes Run and Close.
	assert.Contains(t, msg, "Run")
	assert.Contains(t, msg, "Close")
}

func TestInsertAfterMethod_NoMethodsOnReceiver(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	writeAndMarkRead(t, deps, path, insertFixture)

	res := callInsertAfterMethod(t, deps, map[string]any{
		"file_path":     path,
		"receiver_type": "*Missing",
		"method_name":   "Run",
		"new_method":    "func (s *Missing) Run() {}",
	})
	require.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "no methods found")
}

func TestInsertAfterMethod_InvalidNewMethod(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	writeAndMarkRead(t, deps, path, insertFixture)

	res := callInsertAfterMethod(t, deps, map[string]any{
		"file_path":     path,
		"receiver_type": "*Server",
		"method_name":   "Run",
		"new_method":    "func (s *Server) Broken(",
	})
	require.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "replacement did not parse")

	data, _ := os.ReadFile(path)
	assert.Equal(t, insertFixture, string(data), "file must be unchanged on invalid input")
}

func TestInsertAfterMethod_RequiresPriorRead(t *testing.T) {
	deps, root := newTestDeps(t)
	path := filepath.Join(root, "probe.go")
	require.NoError(t, os.WriteFile(path, []byte(insertFixture), 0o644))

	res := callInsertAfterMethod(t, deps, map[string]any{
		"file_path":     path,
		"receiver_type": "*Server",
		"method_name":   "Run",
		"new_method":    "func (s *Server) Stop() {}",
	})
	require.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "Read it first")
}

func TestInsertAfterMethod_PathOutsideWorkspace(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callInsertAfterMethod(t, deps, map[string]any{
		"file_path":     "/etc/passwd",
		"receiver_type": "*Server",
		"method_name":   "Run",
		"new_method":    "func (s *Server) Stop() {}",
	})
	require.True(t, res.IsError)
}
