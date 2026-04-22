package workspace

import "sync"

// ReadTracker records which absolute paths have been Read in the current session.
// It is safe for concurrent use.
type ReadTracker struct {
	mu    sync.RWMutex
	paths map[string]struct{}
}

// NewReadTracker constructs an empty tracker.
func NewReadTracker() *ReadTracker {
	return &ReadTracker{paths: make(map[string]struct{})}
}

// MarkRead records that the given absolute path has been read.
func (t *ReadTracker) MarkRead(absPath string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.paths[absPath] = struct{}{}
}

// HasBeenRead reports whether the given absolute path has been read.
func (t *ReadTracker) HasBeenRead(absPath string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.paths[absPath]
	return ok
}
