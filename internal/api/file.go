package api

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
)

// maxFileBytes caps GET /api/file responses. Callers that want larger reads
// should stream via a dedicated endpoint (not yet implemented).
const maxFileBytes = 2 * 1024 * 1024

// textExtensions are extensions we force to text/plain even when
// http.DetectContentType would pick something else (e.g. text/xml for .yml).
var textExtensions = map[string]struct{}{
	".go":   {},
	".md":   {},
	".txt":  {},
	".json": {},
	".yml":  {},
	".yaml": {},
	".toml": {},
	".sh":   {},
}

// fileHandler serves raw bytes of a single workspace-relative path.
func fileHandler(ws *workspace.Workspace) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := r.URL.Query().Get("path")
		if rel == "" {
			http.Error(w, "path query parameter is required", http.StatusBadRequest)
			return
		}

		abs, ok := resolveFileRequest(w, ws, rel)
		if !ok {
			return
		}
		if !validateFile(w, abs) {
			return
		}
		serveFileBytes(w, abs)
	})
}

// resolveFileRequest resolves rel against ws and writes an HTTP error on
// failure. Returns (abs, true) on success or ("", false) if the response has
// already been written.
func resolveFileRequest(w http.ResponseWriter, ws *workspace.Workspace, rel string) (string, bool) {
	abs, err := ws.Resolve(rel)
	if err == nil {
		return abs, true
	}
	if errors.Is(err, workspace.ErrOutsideWorkspace) {
		http.Error(w, "path outside workspace", http.StatusBadRequest)
		return "", false
	}
	http.Error(w, "resolve: "+err.Error(), http.StatusBadRequest)
	return "", false
}

// validateFile stats abs and rejects missing / directory / oversized files.
func validateFile(w http.ResponseWriter, abs string) bool {
	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "file not found", http.StatusNotFound)
			return false
		}
		http.Error(w, "stat: "+err.Error(), http.StatusInternalServerError)
		return false
	}
	if info.IsDir() {
		http.Error(w, "path is a directory", http.StatusBadRequest)
		return false
	}
	if info.Size() > maxFileBytes {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return false
	}
	return true
}

func serveFileBytes(w http.ResponseWriter, abs string) {
	f, err := os.Open(abs) //nolint:gosec // abs already contained by workspace
	if err != nil {
		http.Error(w, "open: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	sniff := make([]byte, 512)
	n, _ := io.ReadFull(f, sniff)
	sniff = sniff[:n]

	w.Header().Set("Content-Type", detectContentType(abs, sniff))

	if _, err := w.Write(sniff); err != nil {
		return
	}
	_, _ = io.Copy(w, f)
}

func detectContentType(abs string, sniff []byte) string {
	ext := strings.ToLower(filepath.Ext(abs))
	if _, ok := textExtensions[ext]; ok {
		return "text/plain; charset=utf-8"
	}
	return http.DetectContentType(sniff)
}
