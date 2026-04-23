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

		abs, err := ws.Resolve(rel)
		if err != nil {
			if errors.Is(err, workspace.ErrOutsideWorkspace) {
				http.Error(w, "path outside workspace", http.StatusBadRequest)
				return
			}
			http.Error(w, "resolve: "+err.Error(), http.StatusBadRequest)
			return
		}

		info, err := os.Stat(abs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.Error(w, "file not found", http.StatusNotFound)
				return
			}
			http.Error(w, "stat: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if info.IsDir() {
			http.Error(w, "path is a directory", http.StatusBadRequest)
			return
		}
		if info.Size() > maxFileBytes {
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
			return
		}

		f, err := os.Open(abs) //nolint:gosec // abs already contained by workspace
		if err != nil {
			http.Error(w, "open: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = f.Close() }()

		sniff := make([]byte, 512)
		n, _ := io.ReadFull(f, sniff)
		sniff = sniff[:n]

		ext := strings.ToLower(filepath.Ext(abs))
		var ct string
		if _, ok := textExtensions[ext]; ok {
			ct = "text/plain; charset=utf-8"
		} else {
			ct = http.DetectContentType(sniff)
		}
		w.Header().Set("Content-Type", ct)

		if _, err := w.Write(sniff); err != nil {
			return
		}
		if _, err := io.Copy(w, f); err != nil {
			return
		}
	})
}
