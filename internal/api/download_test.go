package api

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustFile writes content at the given relative path inside the workspace
// root, creating parent directories as needed.
func mustFile(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(content), 0o600))
}

func TestDownloadHandler_StreamsZipWithExpectedEntries(t *testing.T) {
	dir := t.TempDir()
	mustFile(t, dir, "README.md", "top-level")
	mustFile(t, dir, "cmd/sandbox/main.go", "package main")
	mustFile(t, dir, "internal/api/tree.go", "package api")

	ws, err := workspace.New(dir)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/download", nil)
	rr := httptest.NewRecorder()
	downloadHandler(ws).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/zip", rr.Header().Get("Content-Type"))
	cd := rr.Header().Get("Content-Disposition")
	assert.True(t, strings.HasPrefix(cd, `attachment; filename="workspace-`), cd)
	assert.True(t, strings.HasSuffix(downloadFilenameFromHeader(cd), ".zip"), cd)

	zr, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	require.NoError(t, err)

	got := map[string]string{}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue // directory entry
		}
		rc, err := f.Open()
		require.NoError(t, err)
		body, err := io.ReadAll(rc)
		require.NoError(t, err)
		_ = rc.Close()
		got[f.Name] = string(body)
	}
	assert.Equal(t, "top-level", got["README.md"])
	assert.Equal(t, "package main", got["cmd/sandbox/main.go"])
	assert.Equal(t, "package api", got["internal/api/tree.go"])
}

func TestDownloadHandler_SkipsGitAndNodeModules(t *testing.T) {
	dir := t.TempDir()
	mustFile(t, dir, "go.mod", "module x")
	mustFile(t, dir, ".git/HEAD", "ref: refs/heads/main")
	mustFile(t, dir, ".git/objects/abc/def", "binary")
	mustFile(t, dir, "node_modules/foo/package.json", `{"name":"foo"}`)
	mustFile(t, dir, "node_modules/foo/index.js", "// lots of bytes")
	mustFile(t, dir, "src/deep/node_modules/bar/index.js", "// should also be skipped")

	ws, err := workspace.New(dir)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/download", nil)
	rr := httptest.NewRecorder()
	downloadHandler(ws).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	zr, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	require.NoError(t, err)

	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	assert.Contains(t, names, "go.mod")
	for _, n := range names {
		assert.Falsef(t, strings.HasPrefix(n, ".git/"), "should not include .git entry: %s", n)
		assert.Falsef(t, strings.Contains(n, "node_modules/"), "should not include node_modules entry: %s", n)
	}
}

func TestDownloadHandler_EmptyWorkspaceProducesEmptyZip(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/download", nil)
	rr := httptest.NewRecorder()
	downloadHandler(ws).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	zr, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	require.NoError(t, err)
	assert.Empty(t, zr.File)
}

func TestDownloadFilenameFromHeader(t *testing.T) {
	assert.Equal(t, "workspace-20260423-173045.zip",
		downloadFilenameFromHeader(`attachment; filename="workspace-20260423-173045.zip"`))
	assert.Equal(t, "", downloadFilenameFromHeader("attachment"))
	assert.Equal(t, "", downloadFilenameFromHeader(`attachment; filename=unquoted`))
}

func TestDownloadHandler_SkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	mustFile(t, dir, "target.txt", "real file")
	// Create a symlink to the regular file. zipRegularFile should skip it.
	require.NoError(t, os.Symlink(filepath.Join(dir, "target.txt"), filepath.Join(dir, "alias.txt")))

	ws, err := workspace.New(dir)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/download", nil)
	rr := httptest.NewRecorder()
	downloadHandler(ws).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	zr, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	require.NoError(t, err)

	names := make(map[string]bool)
	for _, f := range zr.File {
		names[f.Name] = true
	}
	assert.True(t, names["target.txt"], "regular file should be present")
	assert.False(t, names["alias.txt"], "symlink should be skipped")
}

func TestDownloadHandler_ContextCancellationStopsWalk(t *testing.T) {
	dir := t.TempDir()
	// Seed enough files that cancellation lands mid-walk on most systems.
	for i := range 200 {
		mustFile(t, dir, filepath.Join("bulk", fmt.Sprintf("file-%03d.txt", i)), "payload")
	}

	ws, err := workspace.New(dir)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: walk should short-circuit on the first non-root entry

	req := httptest.NewRequest(http.MethodGet, "/api/download", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	downloadHandler(ws).ServeHTTP(rr, req)

	// Status header was written before walk started, so we still see 200.
	require.Equal(t, http.StatusOK, rr.Code)
	// Body is a best-effort partial zip — verify it's at most a handful of
	// entries, not the full 200.
	zr, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	require.NoError(t, err)
	assert.Less(t, len(zr.File), 200, "walk should have been cut short by ctx cancel, got %d entries", len(zr.File))
}
