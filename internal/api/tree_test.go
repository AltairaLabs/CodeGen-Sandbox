package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTreeHandler_ExcludesGitAndNodeModules(t *testing.T) {
	if err := tools.LookupRipgrep(); err != nil {
		t.Skip("ripgrep not on PATH")
	}

	dir := t.TempDir()
	must := func(err error) { t.Helper(); require.NoError(t, err) }

	must(os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o600))
	must(os.MkdirAll(filepath.Join(dir, "internal"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "internal", "foo.go"), []byte("package x\n"), 0o600))
	must(os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	must(os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600))
	must(os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "node_modules", "bar.js"), []byte("module.exports={}\n"), 0o600))

	ws, err := workspace.New(dir)
	require.NoError(t, err)

	h := treeHandler(ws)
	req := httptest.NewRequest(http.MethodGet, "/api/tree", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var resp treeResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, ws.Root(), resp.Root)

	paths := map[string]string{}
	for _, e := range resp.Entries {
		paths[e.Path] = e.Type
	}
	assert.Equal(t, "file", paths["go.mod"])
	assert.Equal(t, "file", paths["internal/foo.go"])
	assert.Equal(t, "dir", paths["internal"])
	assert.NotContains(t, paths, ".git")
	assert.NotContains(t, paths, ".git/HEAD")
	assert.NotContains(t, paths, "node_modules")
	assert.NotContains(t, paths, "node_modules/bar.js")
}

func TestTreeHandler_LexicographicSort(t *testing.T) {
	if err := tools.LookupRipgrep(); err != nil {
		t.Skip("ripgrep not on PATH")
	}

	dir := t.TempDir()
	for _, p := range []string{"zeta.txt", "alpha.txt", "m/beta.txt", "m/alpha.txt"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, p), []byte("x"), 0o600))
	}

	ws, err := workspace.New(dir)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	treeHandler(ws).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/tree", nil))
	require.Equal(t, http.StatusOK, rr.Code)

	var resp treeResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))

	var got []string
	for _, e := range resp.Entries {
		got = append(got, e.Path)
	}
	// Sorted ascending.
	for i := 1; i < len(got); i++ {
		assert.Less(t, got[i-1], got[i])
	}
}
