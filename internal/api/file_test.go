package api

import (
	"bytes"
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

func TestFileHandler_ValidGoPath_TextPlain(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte("package main\n"), 0o600))

	ws, err := workspace.New(dir)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/file?path=foo.go", nil)
	fileHandler(ws).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "text/plain; charset=utf-8", rr.Header().Get("Content-Type"))
	body, _ := io.ReadAll(rr.Body)
	assert.Equal(t, "package main\n", string(body))
}

func TestFileHandler_BinaryFile_DetectsContentType(t *testing.T) {
	dir := t.TempDir()
	// PNG magic bytes so DetectContentType returns image/png.
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "img.bin"), png, 0o600))

	ws, err := workspace.New(dir)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/file?path=img.bin", nil)
	fileHandler(ws).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	ct := rr.Header().Get("Content-Type")
	assert.NotEqual(t, "text/plain; charset=utf-8", ct)
	// DetectContentType on PNG magic bytes → "image/png".
	assert.Contains(t, ct, "image/png")
}

func TestFileHandler_OutsideWorkspace_Returns400(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/file?path=../../etc/passwd", nil)
	fileHandler(ws).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestFileHandler_MissingFile_Returns400(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/file?path=nope.txt", nil)
	fileHandler(ws).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestFileHandler_TooLarge_Returns413(t *testing.T) {
	dir := t.TempDir()
	// 2 MiB + 1 byte
	big := bytes.Repeat([]byte{'a'}, 2*1024*1024+1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.txt"), big, 0o600))

	ws, err := workspace.New(dir)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/file?path=big.txt", nil)
	fileHandler(ws).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
	body, _ := io.ReadAll(rr.Body)
	assert.Contains(t, strings.ToLower(string(body)), "too large")
}

func TestFileHandler_MissingPathParam_Returns400(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/file", nil)
	fileHandler(ws).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
