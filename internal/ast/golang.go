package ast

import (
	"bytes"
	stdast "go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// goLang implements Language for Go via the stdlib parser. Keeping the token
// fileset inside the Tree handle lets FindFunction / FindMethod translate
// ast.Node positions into byte offsets and line numbers without re-parsing.
type goLang struct{}

// goTree is the parse result for Go source.
type goTree struct {
	fset *token.FileSet
	file *stdast.File
	src  []byte
}

// ID is the language identifier used for registry lookups and docs.
func (goLang) ID() string { return "go" }

// Parse returns a goTree or an error describing a syntactic parse failure.
func (goLang) Parse(source []byte) (Tree, error) {
	fset := token.NewFileSet()
	// ParseComments: we need comments so doc comments (and thus SigStart)
	// can be preserved when editing the body only.
	file, err := parser.ParseFile(fset, "input.go", source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	return &goTree{fset: fset, file: file, src: source}, nil
}

// FindFunction locates top-level functions (methods excluded) by name.
func (goLang) FindFunction(tree Tree, name string) ([]Match, error) {
	gt, ok := tree.(*goTree)
	if !ok {
		return nil, ErrBadTree
	}
	var out []Match
	for _, d := range gt.file.Decls {
		fd, ok := d.(*stdast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Name == nil || fd.Name.Name != name {
			continue
		}
		out = append(out, matchFromFuncDecl(gt, fd, name))
	}
	return out, nil
}

// FindMethod locates methods on the named receiver type. Both `T` and `*T`
// written on the receiver match a search for either form of receiver — the
// function / replacement contract doesn't care about pointer-vs-value receiver.
func (goLang) FindMethod(tree Tree, receiver, name string) ([]Match, error) {
	gt, ok := tree.(*goTree)
	if !ok {
		return nil, ErrBadTree
	}
	want := normalizeReceiverType(receiver)
	var out []Match
	for _, d := range gt.file.Decls {
		fd, ok := d.(*stdast.FuncDecl)
		if !ok || fd.Recv == nil || fd.Name == nil || fd.Name.Name != name {
			continue
		}
		if receiverTypeName(fd) != want {
			continue
		}
		out = append(out, matchFromFuncDecl(gt, fd, fullMethodName(receiver, name)))
	}
	return out, nil
}

// ListMethods returns every method declared on receiver (either pointer or
// value form), sorted by source order. Used to format helpful error messages.
func (goLang) ListMethods(tree Tree, receiver string) ([]Match, error) {
	gt, ok := tree.(*goTree)
	if !ok {
		return nil, ErrBadTree
	}
	want := normalizeReceiverType(receiver)
	var out []Match
	for _, d := range gt.file.Decls {
		fd, ok := d.(*stdast.FuncDecl)
		if !ok || fd.Recv == nil || fd.Name == nil {
			continue
		}
		if receiverTypeName(fd) != want {
			continue
		}
		out = append(out, matchFromFuncDecl(gt, fd, fullMethodName(receiver, fd.Name.Name)))
	}
	return out, nil
}

// ValidateFuncDecl parses text as a complete Go source file wrapped in a
// package decl. The caller must pass text that already contains a function
// declaration (with or without body). This is how we reject mis-typed
// signatures / method bodies before they hit the file.
func (goLang) ValidateFuncDecl(text string) error {
	wrapped := "package _validate\n" + text + "\n"
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "validate.go", wrapped, parser.ParseComments); err != nil {
		return err
	}
	return nil
}

// matchFromFuncDecl computes the three byte offsets the tools operate on:
// start-of-signature (after any doc comment), start of body open-brace,
// and one past the body close-brace.
func matchFromFuncDecl(gt *goTree, fd *stdast.FuncDecl, name string) Match {
	sigStart := gt.fset.Position(fd.Pos()).Offset
	// Body may be nil for external (assembly) declarations — shouldn't
	// happen for valid Go source the agent is editing, but guard anyway.
	var bodyStart, bodyEnd int
	if fd.Body != nil {
		bodyStart = gt.fset.Position(fd.Body.Lbrace).Offset
		// End() returns the position right after the last token, which
		// for a BlockStmt is one past the closing '}'.
		bodyEnd = gt.fset.Position(fd.Body.End()).Offset
	}
	return Match{
		Name:      name,
		Line:      gt.fset.Position(fd.Pos()).Line,
		SigStart:  sigStart,
		BodyStart: bodyStart,
		BodyEnd:   bodyEnd,
	}
}

// receiverTypeName returns the type name as written in a method's receiver
// clause, stripping any leading '*'. Generics (receivers with type-param
// lists like `(t *T[K])`) are collapsed to just "T".
func receiverTypeName(fd *stdast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	return typeExprName(fd.Recv.List[0].Type)
}

func typeExprName(expr stdast.Expr) string {
	switch t := expr.(type) {
	case *stdast.StarExpr:
		return typeExprName(t.X)
	case *stdast.Ident:
		return t.Name
	case *stdast.IndexExpr:
		return typeExprName(t.X)
	case *stdast.IndexListExpr:
		return typeExprName(t.X)
	default:
		return ""
	}
}

// normalizeReceiverType strips a leading '*' / '(' / ')' so lookup is
// pointer-insensitive.
func normalizeReceiverType(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "(")
	s = strings.TrimSuffix(s, ")")
	s = strings.TrimPrefix(s, "*")
	return s
}

// fullMethodName renders the method form used in Match.Name ("(*T).Foo").
func fullMethodName(receiver, name string) string {
	t := normalizeReceiverType(receiver)
	return "(*" + t + ")." + name
}

// RenderNode formats an ast node slice of the source, assuming the offsets
// are valid. Kept in this file so the tools package doesn't have to import
// go/ast directly.
func RenderNode(src []byte, start, end int) []byte {
	if start < 0 || end > len(src) || start > end {
		return nil
	}
	return bytes.Clone(src[start:end])
}

// ErrBadTree signals that a Tree handle passed to a Language method wasn't
// produced by that language's Parse — a programmer error, not a user-input
// failure.
var ErrBadTree = errBadTree{}

type errBadTree struct{}

func (errBadTree) Error() string { return "ast: tree not produced by this language" }

// init registers the Go language at package load so callers can `Lookup("go")`
// without an explicit wire-up step.
func init() { //nolint:gochecknoinits // registry pattern, deliberate
	Register(goLang{})
}
