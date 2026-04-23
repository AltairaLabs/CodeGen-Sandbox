package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/ast"
	"github.com/mark3labs/mcp-go/mcp"
)

// RegisterEditFunctionBody registers the edit_function_body tool.
func RegisterEditFunctionBody(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("edit_function_body",
		mcp.WithDescription("AST-safe replacement of a function or method body. Leaves the signature and any leading doc comment intact. Prefer this over Edit when you're replacing a whole function body — it can't accidentally eat a trailing comma or brace. Requires a prior Read. Go only."),
		mcp.WithString("file_path", mcp.Required(), mcp.Description("Absolute or workspace-relative path to a Go source file.")),
		mcp.WithString("function_name", mcp.Required(), mcp.Description("Either a top-level function name (\"Foo\") or a method (\"(*T).Foo\").")),
		mcp.WithString("new_body", mcp.Required(), mcp.Description("Replacement body WITHOUT the enclosing braces — the tool writes the braces itself.")),
	)
	s.AddTool(tool, HandleEditFunctionBody(deps))
}

// HandleEditFunctionBody returns the edit_function_body handler.
func HandleEditFunctionBody(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		filePath, funcName, newBody, errRes := parseEditBodyArgs(args)
		if errRes != nil {
			return errRes, nil
		}

		abs, source, lang, tree, errRes := astLoadTarget(deps, filePath)
		if errRes != nil {
			return errRes, nil
		}

		match, errRes := resolveFunctionOrMethod(lang, tree, funcName, filePath)
		if errRes != nil {
			return errRes, nil
		}

		if errRes := validateNewBody(lang, source, match, newBody); errRes != nil {
			return errRes, nil
		}

		updated, errRes := spliceBody(source, match, newBody)
		if errRes != nil {
			return errRes, nil
		}

		if err := atomicWrite(abs, updated); err != nil {
			return ErrorResult("write: %v", err), nil
		}

		rel := relForReport(deps.Workspace.Root(), abs)
		return TextResult(formatEditBodyResult(rel, match, source, updated)), nil
	}
}

func parseEditBodyArgs(args map[string]any) (filePath, funcName, newBody string, errRes *mcp.CallToolResult) {
	filePath, _ = args["file_path"].(string)
	if filePath == "" {
		return "", "", "", ErrorResult("file_path is required")
	}
	funcName, _ = args["function_name"].(string)
	if funcName == "" {
		return "", "", "", ErrorResult("function_name is required")
	}
	newBody, _ = args["new_body"].(string)
	// new_body of "" is legal — it means "empty body" — but it must be a
	// provided argument, not absent. JSON null shows up as nil here.
	if _, ok := args["new_body"]; !ok {
		return "", "", "", ErrorResult("new_body is required")
	}
	return filePath, funcName, newBody, nil
}

// resolveFunctionOrMethod routes on the shape of the name: "(*T).Foo" is a
// method lookup, bare "Foo" is a top-level lookup.
func resolveFunctionOrMethod(lang ast.Language, tree ast.Tree, name, filePath string) (ast.Match, *mcp.CallToolResult) {
	if recv, meth, isMethod := splitMethodName(name); isMethod {
		matches, err := lang.FindMethod(tree, recv, meth)
		if err != nil {
			return ast.Match{}, ErrorResult("lookup method: %v", err)
		}
		return requireSingleMatch(
			matches,
			fmt.Sprintf("method %s not found in %s", name, filePath),
			name,
		)
	}
	matches, err := lang.FindFunction(tree, name)
	if err != nil {
		return ast.Match{}, ErrorResult("lookup function: %v", err)
	}
	return requireSingleMatch(
		matches,
		fmt.Sprintf("function %s not found in %s", name, filePath),
		name,
	)
}

// splitMethodName parses "(*T).Foo" or "(T).Foo" into (receiver, method).
// Returns (_, _, false) for plain names.
func splitMethodName(name string) (receiver, method string, ok bool) {
	if !strings.HasPrefix(name, "(") {
		return "", "", false
	}
	closeIdx := strings.Index(name, ")")
	if closeIdx < 0 || closeIdx+2 > len(name) || name[closeIdx+1] != '.' {
		return "", "", false
	}
	recv := name[1:closeIdx]
	meth := name[closeIdx+2:]
	if recv == "" || meth == "" {
		return "", "", false
	}
	return recv, meth, true
}

// validateNewBody wraps the replacement body back into a synthetic function
// declaration and reparses, so a missing '}' or an unbalanced brace surfaces
// here rather than landing an invalid file on disk.
func validateNewBody(lang ast.Language, source []byte, match ast.Match, newBody string) *mcp.CallToolResult {
	signature := string(source[match.SigStart:match.BodyStart])
	wrapped := signature + "{" + newBody + "}"
	if err := lang.ValidateFuncDecl(wrapped); err != nil {
		return ErrorResult(errReplacementParse, err)
	}
	return nil
}

// spliceBody replaces the entire body including braces with `{<newBody>}`,
// preserving the trailing newline convention of the surrounding file.
func spliceBody(source []byte, match ast.Match, newBody string) ([]byte, *mcp.CallToolResult) {
	replacement := "{" + newBody + "}"
	out, err := ast.ReplaceRange(source, match.BodyStart, match.BodyEnd, []byte(replacement))
	if err != nil {
		return nil, ErrorResult("splice: %v", err)
	}
	return out, nil
}

func formatEditBodyResult(rel string, match ast.Match, before, after []byte) string {
	beforeRegion := ast.RenderNode(before, match.SigStart, match.BodyEnd)
	// Find the corresponding region in `after`: signature start is
	// unchanged, body ends at SigStart + len(signature) + len(new region).
	// Simpler: compute delta in length and use SigStart + len(new body with braces).
	delta := len(after) - len(before)
	afterRegion := ast.RenderNode(after, match.SigStart, match.BodyEnd+delta)
	diff := unifiedDiffRegion(beforeRegion, afterRegion, match.Line)
	return fmt.Sprintf("edit_function_body: modified %s at line %d\n%s", rel, match.Line, diff)
}

// relForReport returns a workspace-relative path for reporting. Absolute
// input paths are abbreviated to their relative form; on error the original
// absolute is returned so the report is never worse than the input.
func relForReport(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return abs
	}
	return rel
}
