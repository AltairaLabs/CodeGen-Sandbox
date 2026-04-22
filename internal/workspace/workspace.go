// Package workspace enforces path containment for sandbox filesystem tools.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideWorkspace is returned when a path resolves outside the workspace root.
var ErrOutsideWorkspace = errors.New("path is outside workspace root")

// Workspace is a container-scoped filesystem boundary.
type Workspace struct {
	root string // canonical absolute, symlinks resolved
}

// New constructs a Workspace rooted at the given absolute directory.
func New(root string) (*Workspace, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("workspace root must be absolute: %q", root)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return nil, fmt.Errorf("stat workspace root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root is not a directory: %q", canonical)
	}
	return &Workspace{root: canonical}, nil
}

// Root returns the canonical absolute workspace root.
func (w *Workspace) Root() string {
	return w.root
}

// Resolve returns the canonical absolute form of p, guaranteed to live inside
// the workspace root. If p is relative it is joined against the root. Symlinks
// on existing path components are resolved. If the target does not exist, the
// deepest existing ancestor is resolved and the remaining components are
// rejoined, which supports "I want to write a new file" lookups.
func (w *Workspace) Resolve(p string) (string, error) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(w.root, p)
	}
	p = filepath.Clean(p)

	resolved, err := evalSymlinksAllowMissing(p)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	rel, err := filepath.Rel(w.root, resolved)
	if err != nil {
		return "", fmt.Errorf("compute relative path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrOutsideWorkspace
	}
	return resolved, nil
}

// evalSymlinksAllowMissing behaves like filepath.EvalSymlinks but tolerates a
// path whose tail components do not exist yet. It walks up until it finds an
// existing ancestor, resolves that, then rejoins the missing suffix.
func evalSymlinksAllowMissing(p string) (string, error) {
	p = filepath.Clean(p)
	var missing []string
	cur := p
	for {
		if _, err := os.Lstat(cur); err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("no existing ancestor for path: %q", p)
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
	}
}
