package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
)

// ErrUnknownWorkspace is returned by Set.Get when the requested workspace
// name isn't in the set. Tool handlers surface this as an actionable
// error listing the configured names.
var ErrUnknownWorkspace = errors.New("unknown workspace")

// Entry is one slot in a multi-workspace sandbox. Name is the operator-
// provided or auto-derived short identifier the agent references via the
// per-tool `workspace` argument; Root is the absolute path the Workspace
// will be rooted at.
type Entry struct {
	Name string
	Root string
}

// Set is a named container of one or more Workspace instances. It is
// the sandbox's unit of "how many roots does this process expose?".
// Single-workspace mode is the degenerate case: Len() == 1 and
// Default() returns that sole workspace.
//
// Zero value is not usable — call NewSet to construct.
type Set struct {
	// ordered preserves the order in which workspaces were passed at
	// construction so tool-body error messages list names in a stable,
	// operator-controlled sequence rather than map-iteration order.
	ordered []*Workspace
	byName  map[string]*Workspace
}

// NewSet constructs a Set from the given entries. Entry.Name defaults to
// filepath.Base(Entry.Root) when empty. Duplicate names (after defaults
// are filled in) return an error — operators must disambiguate. Every
// root goes through Workspace.New, so path-containment rules apply per
// entry exactly as they do to single-workspace mode.
//
// At least one entry is required; a zero-entry Set is rejected here
// rather than masked as "no workspace detected" at tool-invocation time.
func NewSet(entries []Entry) (*Set, error) {
	if len(entries) == 0 {
		return nil, errors.New("at least one workspace entry is required")
	}
	set := &Set{
		ordered: make([]*Workspace, 0, len(entries)),
		byName:  make(map[string]*Workspace, len(entries)),
	}
	for i, e := range entries {
		name := e.Name
		if name == "" {
			name = filepath.Base(e.Root)
		}
		if name == "" || name == "." || name == "/" {
			return nil, fmt.Errorf("workspace[%d]: cannot derive a non-empty name from root %q; pass an explicit name", i, e.Root)
		}
		if _, dup := set.byName[name]; dup {
			return nil, fmt.Errorf("workspace[%d]: duplicate name %q", i, name)
		}
		ws, err := New(e.Root)
		if err != nil {
			return nil, fmt.Errorf("workspace[%d] (%s): %w", i, name, err)
		}
		ws.name = name
		set.ordered = append(set.ordered, ws)
		set.byName[name] = ws
	}
	return set, nil
}

// NewSingletonSet is the common-case constructor for the existing
// single-workspace call sites that predate multi-workspace support.
// Equivalent to NewSet([]Entry{{Root: ws.Root()}}) but reuses the
// already-constructed Workspace so its canonicalised root is preserved.
func NewSingletonSet(ws *Workspace) *Set {
	if ws.name == "" {
		ws.name = filepath.Base(ws.root)
	}
	return &Set{
		ordered: []*Workspace{ws},
		byName:  map[string]*Workspace{ws.name: ws},
	}
}

// Len returns the number of workspaces in the set.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.ordered)
}

// Names returns every configured name in the operator-supplied order.
func (s *Set) Names() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.ordered))
	for _, w := range s.ordered {
		out = append(out, w.name)
	}
	return out
}

// SortedNames returns every configured name, alphabetised. Used by tool
// error-message formatters that want deterministic output across
// refactors.
func (s *Set) SortedNames() []string {
	names := s.Names()
	sort.Strings(names)
	return names
}

// Get returns the workspace with the given name, or (nil,
// ErrUnknownWorkspace) when the name isn't in the set.
func (s *Set) Get(name string) (*Workspace, error) {
	if s == nil {
		return nil, ErrUnknownWorkspace
	}
	ws, ok := s.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownWorkspace, name)
	}
	return ws, nil
}

// Default returns the sole workspace when Len() == 1, nil otherwise.
// Tool handlers use this to preserve single-workspace behaviour
// (no `workspace` arg required) while still rejecting ambiguous
// polyglot-style calls on multi-workspace sandboxes.
func (s *Set) Default() *Workspace {
	if s == nil || len(s.ordered) != 1 {
		return nil
	}
	return s.ordered[0]
}

// All returns every workspace in operator-supplied order. Used by
// cross-workspace refresh loops (e.g. the workspace-size metrics
// gauge) that need to walk all roots.
func (s *Set) All() []*Workspace {
	if s == nil {
		return nil
	}
	out := make([]*Workspace, len(s.ordered))
	copy(out, s.ordered)
	return out
}
