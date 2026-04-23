package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// Watch starts a background fsnotify watcher over the index's root. Events
// for .go files trigger AddFile / RemoveFile; non-.go events are ignored.
// The watcher stops when ctx is cancelled.
//
// Watch is safe to call once per index; subsequent calls are a no-op if the
// watcher is already running.
func (i *Index) Watch(ctx context.Context) error {
	i.mu.Lock()
	if i.watcherStarted {
		i.mu.Unlock()
		return nil
	}
	i.watcherStarted = true
	i.mu.Unlock()

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := addRecursiveDirs(w, i.root); err != nil {
		_ = w.Close()
		return err
	}
	go i.runWatch(ctx, w)
	return nil
}

// runWatch consumes fsnotify events until ctx is done. It treats new
// directories as add-to-watcher and .go file events as index mutations.
func (i *Index) runWatch(ctx context.Context, w *fsnotify.Watcher) {
	defer func() { _ = w.Close() }()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			i.handleEvent(w, ev)
		case _, ok := <-w.Errors:
			if !ok {
				return
			}
			// Errors are non-fatal for the watcher itself; a transient
			// permissions issue shouldn't kill the index. Drop and continue.
		}
	}
}

// handleEvent dispatches one fsnotify event. Directory creates extend the
// watcher; .go file ops mutate the index; everything else is dropped.
func (i *Index) handleEvent(w *fsnotify.Watcher, ev fsnotify.Event) {
	if skipPath(ev.Name) {
		return
	}
	if ev.Op&fsnotify.Create != 0 {
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			_ = addRecursiveDirs(w, ev.Name)
			return
		}
	}
	if filepath.Ext(ev.Name) != ".go" {
		return
	}
	switch {
	case ev.Op&(fsnotify.Create|fsnotify.Write) != 0:
		_ = i.AddFile(ev.Name)
	case ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
		i.RemoveFile(ev.Name)
	}
}

// addRecursiveDirs adds root and every subdirectory to the watcher, skipping
// .git and node_modules at any depth.
func addRecursiveDirs(w *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Inaccessible paths shouldn't kill the whole walk.
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		name := info.Name()
		if name == ".git" || name == "node_modules" {
			return filepath.SkipDir
		}
		return w.Add(path)
	})
}

// skipPath reports whether a filesystem event should be ignored outright.
// fsnotify doesn't give us the parent directory, so we check for the skipped
// segments as path components instead.
func skipPath(p string) bool {
	norm := filepath.ToSlash(p)
	for _, bad := range []string{"/.git/", "/node_modules/"} {
		if strings.Contains(norm, bad) {
			return true
		}
	}
	return false
}
