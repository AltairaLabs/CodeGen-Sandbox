package search

import (
	"go/ast"
	"go/printer"
	"go/token"
	"strings"
)

// nodeString renders an AST node back to source using go/printer. Newlines
// and consecutive whitespace are collapsed so the caller can embed the
// result on a single line (a symbol signature).
func nodeString(n ast.Node, fset *token.FileSet) string {
	var sb strings.Builder
	if err := printer.Fprint(&sb, fset, n); err != nil {
		return ""
	}
	return strings.Join(strings.Fields(sb.String()), " ")
}
