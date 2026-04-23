package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// openAPIDoc is a minimal decode target — enough to assert the fields we
// rely on in the drift tests, but permissive about the rest so the spec
// can grow without this test needing to track every field.
type openAPIDoc struct {
	OpenAPI string         `yaml:"openapi"`
	Info    openAPIInfo    `yaml:"info"`
	Paths   map[string]any `yaml:"paths"`
}

type openAPIInfo struct {
	Title   string `yaml:"title"`
	Version string `yaml:"version"`
}

func decodeSpec(t *testing.T) openAPIDoc {
	t.Helper()
	var doc openAPIDoc
	require.NoError(t, yaml.Unmarshal(OpenAPISpec(), &doc), "openapi.yaml must parse")
	return doc
}

func TestOpenAPISpec_Parses(t *testing.T) {
	doc := decodeSpec(t)
	assert.Truef(t, strings.HasPrefix(doc.OpenAPI, "3."), "openapi version should be 3.x, got %q", doc.OpenAPI)
	assert.NotEmpty(t, doc.Info.Title)
	assert.NotEmpty(t, doc.Info.Version)
	assert.NotEmpty(t, doc.Paths)
}

// TestOpenAPISpec_PathsMatchServer is the drift guard: every path the
// server registers (with all feature flags on) must appear in the spec,
// and every spec path must be registered. Update the spec when you add
// or remove a route — this test will remind you.
func TestOpenAPISpec_PathsMatchServer(t *testing.T) {
	ws := mustWorkspace(t)

	h, closer, err := New(Config{
		Workspace:         ws,
		DevMode:           true,
		EnableAPI:         true,
		EnableExec:        true,
		EnablePortForward: true,
		EnableSSH:         true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	// Probe every candidate path and record which ones the mux recognises
	// (i.e., don't return 404). Identity middleware is active and we're in
	// dev-mode, so any registered route returns something other than 404.
	candidates := []string{
		"/api/tree",
		"/api/file",
		"/api/events",
		"/api/exec",
		"/api/port-forward",
		"/api/ssh-authorized-keys",
		"/api/ssh-port",
		"/api/openapi.yaml",
		"/api/docs",
	}

	registered := map[string]bool{}
	for _, p := range candidates {
		// Give each probe a short context so long-polling handlers
		// (SSE events, WS upgrades) return promptly instead of blocking.
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		req := httptest.NewRequest(http.MethodGet, p, nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		cancel()
		if rr.Code != http.StatusNotFound {
			registered[p] = true
		}
	}

	doc := decodeSpec(t)
	specPaths := map[string]bool{}
	for k := range doc.Paths {
		specPaths[k] = true
	}

	for p := range registered {
		assert.Truef(t, specPaths[p], "route %q is registered by the server but missing from openapi.yaml", p)
	}
	for p := range specPaths {
		assert.Truef(t, registered[p], "path %q is in openapi.yaml but no route is registered for it", p)
	}
}

func TestOpenAPIHandler_ServesYAML(t *testing.T) {
	ws := mustWorkspace(t)
	h, closer, err := New(Config{Workspace: ws, DevMode: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	req := httptest.NewRequest(http.MethodGet, "/api/openapi.yaml", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Type"), "yaml")
	assert.Contains(t, rr.Body.String(), "openapi:")
	assert.Contains(t, rr.Body.String(), "Codegen Sandbox Remote Access API")
}

func TestDocsHandler_ServesHTML(t *testing.T) {
	ws := mustWorkspace(t)
	h, closer, err := New(Config{Workspace: ws, DevMode: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	req := httptest.NewRequest(http.MethodGet, "/api/docs", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, rr.Body.String(), "@scalar/api-reference")
	assert.Contains(t, rr.Body.String(), "/api/openapi.yaml")
}

func TestDocsEndpoints_RequireIdentity(t *testing.T) {
	ws := mustWorkspace(t)
	h, closer, err := New(Config{Workspace: ws, DevMode: false})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	for _, path := range []string{"/api/openapi.yaml", "/api/docs"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		assert.Equalf(t, http.StatusUnauthorized, rr.Code, "%s should require identity when dev-mode is off", path)
	}
}

func mustWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()
	ws, err := workspace.New(t.TempDir())
	require.NoError(t, err)
	return ws
}
