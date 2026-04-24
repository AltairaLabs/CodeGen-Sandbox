package tools_test

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/secrets"
	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// logMu serialises the tests that swap log.Default()'s writer, so two
// parallel tests don't overwrite each other's buffers.
var logMu sync.Mutex

func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	logMu.Lock()
	defer logMu.Unlock()
	prev := log.Writer()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	fn()
	return buf.String()
}

func callSecret(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleSecret(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func callSecretsAvailable(t *testing.T, deps *tools.Deps) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	res, err := tools.HandleSecretsAvailable(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestSecret_EnvSource_HappyPath(t *testing.T) {
	store := secrets.New("", []string{"CODEGEN_SANDBOX_SECRET_DEMO=hello-world"})
	deps := &tools.Deps{Secrets: store}

	res := callSecret(t, deps, map[string]any{"name": "demo"})
	require.False(t, res.IsError)
	assert.Equal(t, "hello-world", textOf(t, res))
}

func TestSecret_AuditLineDoesNotContainValue(t *testing.T) {
	store := secrets.New("", []string{"CODEGEN_SANDBOX_SECRET_DEMO=super-secret-value"})
	deps := &tools.Deps{Secrets: store}

	out := captureLog(t, func() {
		_ = callSecret(t, deps, map[string]any{"name": "demo"})
	})

	assert.Contains(t, out, "api secret")
	assert.Contains(t, out, "name=demo")
	assert.Contains(t, out, "source=env")
	assert.Contains(t, out, "len=18")
	assert.NotContains(t, out, "super-secret-value", "audit line must never leak the value")
}

func TestSecret_FileSource_AuditHasSourceFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "demo"), []byte("file-val\n"), 0o600))
	store := secrets.New(dir, nil)
	deps := &tools.Deps{Secrets: store}

	var got string
	out := captureLog(t, func() {
		res := callSecret(t, deps, map[string]any{"name": "demo"})
		got = textOf(t, res)
	})

	assert.Equal(t, "file-val", got)
	assert.Contains(t, out, "source=file")
	assert.Contains(t, out, "len=8")
	assert.NotContains(t, out, "file-val")
}

func TestSecret_UnknownName_ListsAvailable(t *testing.T) {
	store := secrets.New("", []string{
		"CODEGEN_SANDBOX_SECRET_ALPHA=a",
		"CODEGEN_SANDBOX_SECRET_BETA=b",
	})
	deps := &tools.Deps{Secrets: store}

	res := callSecret(t, deps, map[string]any{"name": "missing"})
	require.True(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, body, "unknown secret")
	assert.Contains(t, body, "missing")
	assert.Contains(t, body, "alpha")
	assert.Contains(t, body, "beta")
}

func TestSecret_MissingName(t *testing.T) {
	store := secrets.New("", []string{"CODEGEN_SANDBOX_SECRET_X=v"})
	deps := &tools.Deps{Secrets: store}

	res := callSecret(t, deps, map[string]any{})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "name is required")
}

func TestSecret_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok"), []byte("v"), 0o600))
	store := secrets.New(dir, nil)
	deps := &tools.Deps{Secrets: store}

	// Path-traversal names are rejected at the tool-call layer — the store
	// never sees them as a lookup.
	for _, bad := range []string{"../etc/passwd", "foo/bar", "..", ".hidden"} {
		res := callSecret(t, deps, map[string]any{"name": bad})
		require.True(t, res.IsError, "expected error for %q", bad)
		assert.Contains(t, strings.ToLower(textOf(t, res)), "invalid")
	}
}

func TestSecret_DepsNil_ReturnsClearError(t *testing.T) {
	deps := &tools.Deps{Secrets: nil}
	res := callSecret(t, deps, map[string]any{"name": "any"})
	require.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "secrets not configured")
}

func TestSecretsAvailable_ReturnsSortedList(t *testing.T) {
	store := secrets.New("", []string{
		"CODEGEN_SANDBOX_SECRET_GAMMA=g",
		"CODEGEN_SANDBOX_SECRET_ALPHA=a",
		"CODEGEN_SANDBOX_SECRET_BETA=b",
	})
	deps := &tools.Deps{Secrets: store}

	res := callSecretsAvailable(t, deps)
	require.False(t, res.IsError)
	body := textOf(t, res)
	assert.Equal(t, "alpha\nbeta\ngamma", body)
}

func TestSecretsAvailable_DepsNil_EmptyList(t *testing.T) {
	deps := &tools.Deps{Secrets: nil}
	res := callSecretsAvailable(t, deps)
	require.False(t, res.IsError)
	assert.Equal(t, "", textOf(t, res))
}
