package tools_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callWebFetch(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleWebFetch(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func callWebSearch(t *testing.T, deps *tools.Deps, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := tools.HandleWebSearch(deps)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func TestWebFetch_MissingURLIsError(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callWebFetch(t, deps, map[string]any{})
	assert.True(t, res.IsError)
}

func TestWebFetch_RejectsLoopbackURL(t *testing.T) {
	deps, _ := newTestDeps(t)
	// httptest servers bind to 127.0.0.1 — the filter should reject them
	// even when the caller provides a directly-reachable URL.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("should not reach"))
	}))
	defer srv.Close()

	res := callWebFetch(t, deps, map[string]any{"url": srv.URL})
	assert.True(t, res.IsError, "loopback URL must be blocked by the filter")
	assert.Contains(t, textOf(t, res), "loopback")
}

func TestWebFetch_RejectsMetadataHostname(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callWebFetch(t, deps, map[string]any{"url": "http://metadata.google.internal/"})
	assert.True(t, res.IsError)
}

func TestWebFetch_RejectsNonHTTPScheme(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callWebFetch(t, deps, map[string]any{"url": "file:///etc/passwd"})
	assert.True(t, res.IsError)
}

func TestWebSearch_NoBackendReturnsConfigurationError(t *testing.T) {
	deps, _ := newTestDeps(t)
	t.Setenv("CODEGEN_SANDBOX_SEARCH_BACKEND", "")

	res := callWebSearch(t, deps, map[string]any{"query": "anything"})
	assert.True(t, res.IsError)
	body := textOf(t, res)
	assert.Contains(t, strings.ToLower(body), "not configured")
}

func TestWebSearch_MissingQueryIsError(t *testing.T) {
	deps, _ := newTestDeps(t)
	res := callWebSearch(t, deps, map[string]any{})
	assert.True(t, res.IsError)
}

func TestWebSearch_BackendSetButNotImplemented(t *testing.T) {
	deps, _ := newTestDeps(t)
	t.Setenv("CODEGEN_SANDBOX_SEARCH_BACKEND", "brave")

	res := callWebSearch(t, deps, map[string]any{"query": "golang"})
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(t, res), "not yet implemented")
}
