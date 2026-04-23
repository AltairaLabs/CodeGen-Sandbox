// Package search provides the in-memory BM25 symbol index powering the
// search_code MCP tool. v1 indexes Go sources only; other languages will add
// their own extractors registered via this package's LanguageExtractor
// registry.
package search

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// Unit is a single indexable code atom — one top-level Go declaration (or the
// package comment) with its preceding doc.
type Unit struct {
	// File is workspace-relative and slash-normalised.
	File string
	// Symbol is the identifier (empty for a package-level unit).
	Symbol string
	// Kind is one of: func, method, type, const, var, package.
	Kind string
	// Signature is the compact one-line rendering used in output.
	Signature string
	// Doc is the preceding doc comment, whitespace-normalised, truncated to
	// docMaxChars.
	Doc string
}

const (
	docMaxChars     = 200
	fileDocMaxChars = 200
)

// ExtractGoFile parses one Go source file and returns its Units.
// root is the workspace root; the file path is stored relative to it.
// Non-.go files produce an empty slice with no error.
//
// Follow-up: an embeddings hook (CODEGEN_SANDBOX_EMBEDDINGS_URL) will augment
// Units with dense vectors once #11's follow-up lands.
func ExtractGoFile(path, root string) ([]Unit, error) {
	if filepath.Ext(path) != ".go" {
		return nil, nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)

	var units []Unit
	if f.Doc != nil {
		units = append(units, Unit{
			File:      rel,
			Symbol:    f.Name.Name,
			Kind:      "package",
			Signature: "package " + f.Name.Name,
			Doc:       truncate(normalizeDoc(f.Doc.Text()), fileDocMaxChars),
		})
	}
	for _, decl := range f.Decls {
		units = append(units, extractDecl(decl, rel, fset)...)
	}
	return units, nil
}

// extractDecl dispatches a single top-level decl to its specific extractor.
func extractDecl(decl ast.Decl, file string, fset *token.FileSet) []Unit {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return []Unit{funcUnit(d, file, fset)}
	case *ast.GenDecl:
		return genDeclUnits(d, file, fset)
	}
	return nil
}

// funcUnit renders a function or method as one Unit.
func funcUnit(fn *ast.FuncDecl, file string, fset *token.FileSet) Unit {
	kind := "func"
	if fn.Recv != nil {
		kind = "method"
	}
	return Unit{
		File:      file,
		Symbol:    fn.Name.Name,
		Kind:      kind,
		Signature: renderFuncSig(fn, fset),
		Doc:       truncate(normalizeDoc(fn.Doc.Text()), docMaxChars),
	}
}

// genDeclUnits expands a const / var / type block into one Unit per spec.
// The GenDecl's own doc comment applies to every spec that lacks its own.
func genDeclUnits(d *ast.GenDecl, file string, fset *token.FileSet) []Unit {
	var out []Unit
	groupDoc := normalizeDoc(d.Doc.Text())
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			out = append(out, typeUnit(s, file, fset, groupDoc))
		case *ast.ValueSpec:
			out = append(out, valueUnits(s, file, fset, d.Tok.String(), groupDoc)...)
		}
	}
	return out
}

// typeUnit renders one TypeSpec as a Unit whose Signature is a single-line
// summary of its struct fields when applicable.
func typeUnit(s *ast.TypeSpec, file string, fset *token.FileSet, groupDoc string) Unit {
	doc := normalizeDoc(s.Doc.Text())
	if doc == "" {
		doc = groupDoc
	}
	sig := "type " + s.Name.Name
	if st, ok := s.Type.(*ast.StructType); ok {
		sig += " struct{" + structFieldSummary(st, fset) + "}"
	} else {
		sig += " " + renderNode(s.Type, fset)
	}
	return Unit{
		File:      file,
		Symbol:    s.Name.Name,
		Kind:      "type",
		Signature: sig,
		Doc:       truncate(doc, docMaxChars),
	}
}

// valueUnits expands one ValueSpec (`var a, b = ...` or `const x = 1`) into
// one Unit per name. tok is "var" or "const".
func valueUnits(s *ast.ValueSpec, file string, fset *token.FileSet, tok, groupDoc string) []Unit {
	doc := normalizeDoc(s.Doc.Text())
	if doc == "" {
		doc = groupDoc
	}
	typ := ""
	if s.Type != nil {
		typ = " " + renderNode(s.Type, fset)
	}
	out := make([]Unit, 0, len(s.Names))
	for _, n := range s.Names {
		out = append(out, Unit{
			File:      file,
			Symbol:    n.Name,
			Kind:      tok,
			Signature: tok + " " + n.Name + typ,
			Doc:       truncate(doc, docMaxChars),
		})
	}
	return out
}

// renderFuncSig produces a compact, single-line Go function signature.
func renderFuncSig(fn *ast.FuncDecl, fset *token.FileSet) string {
	var sb strings.Builder
	sb.WriteString("func ")
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		sb.WriteString("(")
		sb.WriteString(renderFieldList(fn.Recv, fset, false))
		sb.WriteString(") ")
	}
	sb.WriteString(fn.Name.Name)
	sb.WriteString("(")
	if fn.Type.Params != nil {
		sb.WriteString(renderFieldList(fn.Type.Params, fset, true))
	}
	sb.WriteString(")")
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		sb.WriteString(" ")
		if needsResultParens(fn.Type.Results) {
			sb.WriteString("(")
			sb.WriteString(renderFieldList(fn.Type.Results, fset, true))
			sb.WriteString(")")
		} else {
			sb.WriteString(renderFieldList(fn.Type.Results, fset, true))
		}
	}
	return sb.String()
}

func needsResultParens(fl *ast.FieldList) bool {
	if len(fl.List) > 1 {
		return true
	}
	return len(fl.List) == 1 && len(fl.List[0].Names) > 0
}

// renderFieldList renders a FieldList as "name type, name type" (or just
// "type, type" when includeNames is false — used for receivers without an
// explicit name).
func renderFieldList(fl *ast.FieldList, fset *token.FileSet, includeNames bool) string {
	parts := make([]string, 0, len(fl.List))
	for _, f := range fl.List {
		typeStr := renderNode(f.Type, fset)
		if includeNames && len(f.Names) > 0 {
			names := make([]string, 0, len(f.Names))
			for _, n := range f.Names {
				names = append(names, n.Name)
			}
			parts = append(parts, strings.Join(names, ", ")+" "+typeStr)
			continue
		}
		parts = append(parts, typeStr)
	}
	return strings.Join(parts, ", ")
}

// structFieldSummary renders exported struct fields as "Name type, Name type".
// Unexported fields are omitted to keep the signature readable.
func structFieldSummary(st *ast.StructType, fset *token.FileSet) string {
	if st.Fields == nil {
		return ""
	}
	var parts []string
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 {
			parts = append(parts, renderNode(f.Type, fset))
			continue
		}
		for _, n := range f.Names {
			if !n.IsExported() {
				continue
			}
			parts = append(parts, n.Name+" "+renderNode(f.Type, fset))
		}
	}
	return strings.Join(parts, ", ")
}

// renderNode prints an AST expression back to source form using go/printer.
// Falls back to the identifier name for bare idents (the common case) to
// avoid pulling go/printer for trivial cases.
func renderNode(n ast.Node, fset *token.FileSet) string {
	if id, ok := n.(*ast.Ident); ok {
		return id.Name
	}
	return nodeString(n, fset)
}

// normalizeDoc collapses a multi-line doc comment into a single whitespace-
// normalised line.
func normalizeDoc(s string) string {
	if s == "" {
		return ""
	}
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// WalkGoFiles yields every .go file under root, skipping .git/ and
// node_modules/ at any depth. It returns paths in deterministic order
// (filepath.Walk's lexicographic traversal).
func WalkGoFiles(root string, fn func(path string) error) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		return fn(path)
	})
}
