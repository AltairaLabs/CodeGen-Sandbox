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
	// name is the short identifier used by multi-workspace sandboxes to
	// pick this workspace via the per-tool `workspace` argument. Empty
	// when the Workspace was constructed outside a Set — callers
	// (workspace.NewSet, workspace.NewSingletonSet) populate it; the
	// single-workspace call sites that predate multi-workspace support
	// leave it empty, which is harmless as they don't consult Name().
	name string
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

// Name returns the short identifier this workspace was registered under in
// its containing Set, or "" when constructed outside a Set. Callers that
// care about multi-workspace semantics read Name to attribute output
// (e.g. "grep findings from workspace <name>").
func (w *Workspace) Name() string {
	return w.name
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

// Size walks the workspace and returns (totalBytes, totalFiles), skipping
// well-known directories that bloat the count without being part of the
// agent's code surface (.git, node_modules). Permission errors and
// mid-walk removals are tolerated — this feeds a metrics gauge, so returning
// a best-effort sample is preferable to erroring out.
func (w *Workspace) Size() (bytes int64, files int64) {
	_ = filepath.WalkDir(w.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Missing entries / permission faults are swallowed; the gauge
			// is a best-effort snapshot, not an inventory audit.
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if path != w.root && (base == ".git" || base == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		bytes += info.Size()
		files++
		return nil
	})
	return bytes, files
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
