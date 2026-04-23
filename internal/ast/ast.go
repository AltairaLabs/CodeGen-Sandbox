// Package ast hosts AST-aware helpers used by the AST-safe edit tools.
//
// Design note: issue #10 originally called for tree-sitter grammars, but every
// available tree-sitter Go binding we evaluated (smacker/go-tree-sitter,
// tree-sitter/go-tree-sitter, alexaandru/go-tree-sitter-bare) requires CGO.
// Our runtime image compiles with CGO_ENABLED=0 and ships on `scratch`, so
// pulling in a CGO dependency would either break the Docker image or force a
// rebase onto a glibc base with libc present at runtime — neither is worth it
// for v1 when only Go is in scope.
//
// v1 uses Go's stdlib parser (`go/parser`, `go/ast`, `go/printer`) as the Go
// language adapter. The Language interface below keeps the tree-sitter-style
// registry shape so a subsequent issue can plug in tree-sitter grammars under
// a build tag for Python / TS / Rust without touching the tool handlers.
package ast

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// Match describes a resolved function or method node in source.
//
// Byte offsets are into the original source bytes. SigStart / BodyStart /
// BodyEnd form the three anchors callers care about:
//
//	<whatever preceded>
//	<SigStart>func (r *T) F(...) ReturnType <BodyStart>{...body...<BodyEnd>}
type Match struct {
	Name      string // e.g. "Foo" or "(*T).Foo"
	Line      int    // 1-based line of the declaration
	SigStart  int    // byte offset of the first rune of the signature (excludes doc comment)
	BodyStart int    // byte offset of the opening '{' of the body
	BodyEnd   int    // byte offset ONE PAST the closing '}'
}

// Language is the per-language adapter. A Language parses source and exposes
// lookups scoped to the parsed tree. All returned byte offsets are into the
// original source bytes the caller passed to Parse.
type Language interface {
	// ID is the short language identifier (e.g. "go").
	ID() string
	// Parse returns an opaque tree handle plus any parse error.
	Parse(source []byte) (Tree, error)
	// FindFunction locates a top-level function by name.
	FindFunction(tree Tree, name string) ([]Match, error)
	// FindMethod locates a method on a named receiver type.
	// receiver may be "T" or "*T"; both forms match a declaration written
	// against either pointer or value receiver.
	FindMethod(tree Tree, receiver, name string) ([]Match, error)
	// ListMethods returns every method declared on the given receiver type.
	ListMethods(tree Tree, receiver string) ([]Match, error)
	// ValidateFuncDecl checks that a replacement function declaration
	// (signature optionally plus body) parses cleanly when spliced in.
	ValidateFuncDecl(text string) error
}

// Tree is an opaque per-language parse result.
type Tree any

// registry is the process-wide language-ID → Language map.
var (
	registryMu sync.RWMutex
	registry   = map[string]Language{}
)

// Register installs a Language under its ID. Re-registration replaces the
// prior entry, which keeps tests deterministic when they stub a language.
func Register(lang Language) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[lang.ID()] = lang
}

// Lookup returns the Language registered under id, or (nil, false).
func Lookup(id string) (Language, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	l, ok := registry[id]
	return l, ok
}

// Detect returns the language ID for a path based on file extension, or "" if
// the extension is unknown. This is the single source of truth for extension
// → language mapping.
func Detect(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	default:
		return ""
	}
}

// ReplaceRange returns a new byte slice with source[start:end] replaced by
// replacement. Callers pass byte offsets from a Match to splice a new body
// or signature in place.
func ReplaceRange(source []byte, start, end int, replacement []byte) ([]byte, error) {
	if start < 0 || end < start || end > len(source) {
		return nil, fmt.Errorf("invalid range [%d:%d] over source of length %d", start, end, len(source))
	}
	out := make([]byte, 0, len(source)-(end-start)+len(replacement))
	out = append(out, source[:start]...)
	out = append(out, replacement...)
	out = append(out, source[end:]...)
	return out, nil
}

// FormatMatches renders a slice of Matches as "name@line,name@line,…" for use
// in ambiguous-match error messages.
func FormatMatches(matches []Match) string {
	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		parts = append(parts, fmt.Sprintf("line %d", m.Line))
	}
	return strings.Join(parts, ", ")
}
