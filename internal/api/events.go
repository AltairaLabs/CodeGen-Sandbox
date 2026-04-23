package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/fsnotify/fsnotify"
)

const eventsKeepAlive = 30 * time.Second

// fsEvent is one entry in the SSE stream.
type fsEvent struct {
	Type string `json:"type"` // create | write | remove | rename
	Path string `json:"path"` // workspace-relative
	TS   string `json:"ts"`   // RFC3339
}

// eventsHandler streams fsnotify events as SSE.
func eventsHandler(ws *workspace.Workspace) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		writeSSEHeaders(w, flusher)

		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			// Headers already sent; best-effort comment, then return.
			_, _ = fmt.Fprintf(w, ": watcher init error: %v\n\n", err)
			flusher.Flush()
			return
		}
		defer func() { _ = watcher.Close() }()

		if err := addWatchRecursive(watcher, ws.Root()); err != nil {
			_, _ = fmt.Fprintf(w, ": watch init error: %v\n\n", err)
			flusher.Flush()
			return
		}

		ping := time.NewTicker(eventsKeepAlive)
		defer ping.Stop()

		runEventsLoop(r, w, flusher, watcher, ws.Root(), ping.C)
	})
}

func writeSSEHeaders(w http.ResponseWriter, flusher http.Flusher) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
}

func runEventsLoop(r *http.Request, w http.ResponseWriter, flusher http.Flusher, watcher *fsnotify.Watcher, root string, pingC <-chan time.Time) {
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !handleFSEvent(w, flusher, watcher, root, ev) {
				return
			}
		case werr, ok := <-watcher.Errors:
			if !ok {
				return
			}
			if werr != nil {
				log.Printf("api events watcher error: %v", werr)
			}
		case <-pingC:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handleFSEvent writes one event (if non-skipped) to the SSE stream.
// Returns false when the client stream is dead and the loop should exit.
func handleFSEvent(w http.ResponseWriter, flusher http.Flusher, watcher *fsnotify.Watcher, root string, ev fsnotify.Event) bool {
	rel, err := filepath.Rel(root, ev.Name)
	if err != nil || strings.HasPrefix(rel, "..") {
		return true
	}
	if skipPath(rel) {
		return true
	}

	if ev.Op&fsnotify.Create != 0 {
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			_ = addWatchRecursive(watcher, ev.Name)
		}
	}

	payload := fsEvent{
		Type: classifyOp(ev.Op),
		Path: filepath.ToSlash(rel),
		TS:   time.Now().UTC().Format(time.RFC3339),
	}
	if payload.Type == "" {
		return true
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return true
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// classifyOp maps an fsnotify Op to the string used in the SSE payload.
// Precedence: remove > rename > create > write. Chmod alone is dropped.
func classifyOp(op fsnotify.Op) string {
	switch {
	case op&fsnotify.Remove != 0:
		return "remove"
	case op&fsnotify.Rename != 0:
		return "rename"
	case op&fsnotify.Create != 0:
		return "create"
	case op&fsnotify.Write != 0:
		return "write"
	default:
		return ""
	}
}

// skipPath filters out watch noise we never want to surface to the UI.
func skipPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	if rel == ".git" || strings.HasPrefix(rel, ".git/") {
		return true
	}
	if rel == "node_modules" || strings.HasPrefix(rel, "node_modules/") {
		return true
	}
	return false
}

// addWatchRecursive walks dir and adds every directory to the watcher.
// Missing directories (e.g. removed mid-walk) are tolerated.
func addWatchRecursive(watcher *fsnotify.Watcher, dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Missing entries are skipped; surface only hard IO errors.
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if base == ".git" || base == "node_modules" {
			return filepath.SkipDir
		}
		_ = watcher.Add(path)
		return nil
	})
}
