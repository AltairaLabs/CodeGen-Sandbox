package api

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
)

// downloadSkipDirs are directory names skipped at any depth when streaming
// a workspace download. They are dominated by machine-regenerable state
// (VCS metadata, dependency caches) that would bloat the zip by orders of
// magnitude with no recovery value.
var downloadSkipDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
}

// downloadHandler streams the workspace as a zip on GET /api/download. The
// archive is generated on the fly — no temp file, no in-memory buffering —
// so very large workspaces complete without blowing the pod's memory.
//
// Directories named ".git" or "node_modules" are skipped at any depth; see
// downloadSkipDirs.
func downloadHandler(ws *workspace.Workspace) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root := ws.Root()
		ts := time.Now().UTC().Format("20060102-150405")
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set(
			"Content-Disposition",
			fmt.Sprintf(`attachment; filename="workspace-%s.zip"`, ts),
		)

		zw := zip.NewWriter(w)
		defer func() { _ = zw.Close() }()

		walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path == root {
				return nil
			}
			// Honour context cancellation mid-walk for large workspaces.
			if rerr := r.Context().Err(); rerr != nil {
				return rerr
			}

			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			base := filepath.Base(rel)
			if _, skip := downloadSkipDirs[base]; skip {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			if d.IsDir() {
				// Zip convention: directory entries have a trailing slash.
				_, err := zw.Create(filepath.ToSlash(rel) + "/")
				return err
			}
			// Use the DirEntry's type so symlinks / sockets / devices are
			// skipped without following them — os.Stat would follow the
			// link and re-zip the target under the link's name.
			if !d.Type().IsRegular() {
				return nil
			}
			return zipRegularFile(zw, path, filepath.ToSlash(rel))
		})
		if walkErr != nil {
			// Headers + partial archive are already on the wire — best
			// effort: log and let the client see a truncated zip.
			log.Printf("api download: walk: %v", walkErr)
		}
	})
}

// zipRegularFile copies one file into the zip stream. Caller is
// responsible for ensuring the entry is a regular file (non-regular
// entries should be filtered by the caller against the WalkDir DirEntry's
// type; os.Stat would follow symlinks which is wrong here).
func zipRegularFile(zw *zip.Writer, abs, rel string) error {
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = rel
	header.Method = zip.Deflate

	dst, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	src, err := os.Open(abs) //nolint:gosec // abs already contained by workspace
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return nil
}

// downloadFilenameFromHeader extracts filename="..." out of a
// Content-Disposition value. Exposed only for tests.
func downloadFilenameFromHeader(h string) string {
	const marker = `filename="`
	i := strings.Index(h, marker)
	if i < 0 {
		return ""
	}
	rest := h[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
