package tools

import (
	"fmt"
	"os"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/ast"
	"github.com/mark3labs/mcp-go/mcp"
)

// RegisterASTEdits registers the three AST-safe edit tools.
func RegisterASTEdits(s ToolAdder, deps *Deps) {
	RegisterEditFunctionBody(s, deps)
	RegisterInsertAfterMethod(s, deps)
	RegisterChangeFunctionSignature(s, deps)
}

// errAmbiguous is the format used by every "multiple matches" failure.
const errAmbiguous = "ambiguous: %s matches %d declarations at %s"

// errReplacementParse is the format emitted when the caller's replacement
// body / method / signature doesn't parse in context.
const errReplacementParse = "replacement did not parse: %v"

// errParse is the format emitted when the existing source fails to parse.
const errParse = "parse error: %v"

// astLoadTarget reads the file, verifies Read-gate + path containment, and
// returns (abs, source, language, tree) ready for tool-specific manipulation.
// The bool return distinguishes the "an error result has already been built"
// path from the happy path (nil result, ok=true).
//
// The AST tools predate multi-workspace support (#23); they resolve
// against deps.Workspace (the default / sole workspace) rather than a
// hint-supplied workspace. Extending them to accept a `workspace` arg
// is a follow-up — the Deps.Workspace field continues to point at the
// default workspace in multi-workspace mode so this call site keeps
// working.
func astLoadTarget(deps *Deps, filePath string) (abs string, source []byte, lang ast.Language, tree ast.Tree, errRes *mcp.CallToolResult) {
	abs, errRes = resolveEditTarget(deps.Workspace, deps, filePath)
	if errRes != nil {
		return "", nil, nil, nil, errRes
	}
	langID := ast.Detect(abs)
	if langID == "" {
		return "", nil, nil, nil, ErrorResult("no AST support for %s (known extensions: .go)", filePath)
	}
	lang, ok := ast.Lookup(langID)
	if !ok {
		return "", nil, nil, nil, ErrorResult("no registered language %q (BUG)", langID)
	}
	data, err := os.ReadFile(abs) //nolint:gosec // workspace-contained
	if err != nil {
		return "", nil, nil, nil, ErrorResult("read: %v", err)
	}
	tree, err = lang.Parse(data)
	if err != nil {
		return "", nil, nil, nil, ErrorResult(errParse, err)
	}
	return abs, data, lang, tree, nil
}

// requireSingleMatch collapses FindFunction / FindMethod results to one
// Match, or turns a 0-match / N-match outcome into an error result.
func requireSingleMatch(matches []ast.Match, notFoundMsg, ambiguousName string) (ast.Match, *mcp.CallToolResult) {
	switch len(matches) {
	case 0:
		return ast.Match{}, ErrorResult("%s", notFoundMsg)
	case 1:
		return matches[0], nil
	default:
		return ast.Match{}, ErrorResult(errAmbiguous, ambiguousName, len(matches), ast.FormatMatches(matches))
	}
}

// unifiedDiffRegion produces a unified-diff-like rendering of the edited
// region only. Keeping this tight (contextual line numbers around the
// touched lines) avoids dumping an entire long file in the tool result.
//
// The output is not strictly a `diff -u` shape — we format it as
//
//	--- before
//	+++ after
//	@@ start .. end @@
//	-old line
//	+new line
//
// which keeps it machine-parseable for agents and readable for humans.
func unifiedDiffRegion(before, after []byte, startLine int) string {
	var sb strings.Builder
	sb.WriteString("--- before\n+++ after\n")
	beforeLines := strings.Split(strings.TrimRight(string(before), "\n"), "\n")
	afterLines := strings.Split(strings.TrimRight(string(after), "\n"), "\n")
	fmt.Fprintf(&sb, "@@ starting line %d @@\n", startLine)
	for _, ln := range beforeLines {
		sb.WriteString("-")
		sb.WriteString(ln)
		sb.WriteString("\n")
	}
	for _, ln := range afterLines {
		sb.WriteString("+")
		sb.WriteString(ln)
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// lineOfOffset returns the 1-based line number at the given byte offset.
// Used to compute the reported "modified at line N" anchor.
func lineOfOffset(src []byte, offset int) int {
	if offset > len(src) {
		offset = len(src)
	}
	line := 1
	for i := 0; i < offset; i++ {
		if src[i] == '\n' {
			line++
		}
	}
	return line
}

// lineIndentAtOffset returns the leading whitespace of the line that contains
// the given byte offset. Used by insert_after_method to match the indentation
// of the anchor method when splicing in a peer.
func lineIndentAtOffset(src []byte, offset int) string {
	lineStart := offset
	for lineStart > 0 && src[lineStart-1] != '\n' {
		lineStart--
	}
	end := lineStart
	for end < len(src) && (src[end] == ' ' || src[end] == '\t') {
		end++
	}
	return string(src[lineStart:end])
}
