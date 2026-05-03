package probes

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLivenessHandler_AlwaysReturns200(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	LivenessHandler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok\n", rec.Body.String())
}

func TestLivenessHandler_NoWorkspaceDependency(t *testing.T) {
	// Liveness must not depend on filesystem or any other backing resource;
	// k8s liveness reflects "process is alive", nothing else.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	LivenessHandler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestReadinessHandler_NilSetReturns503(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	ReadinessHandler(nil).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestReadinessHandler_AllRootsReachableReturns200(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	set, err := workspace.NewSet([]workspace.Entry{
		{Name: "primary", Root: dir1},
		{Name: "extension", Root: dir2},
	})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	ReadinessHandler(set).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "ready")
}

func TestReadinessHandler_MissingRootReturns503WithName(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)
	set := workspace.NewSingletonSet(ws)

	require.NoError(t, os.RemoveAll(dir))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	ReadinessHandler(set).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), ws.Name())
}
