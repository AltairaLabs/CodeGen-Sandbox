package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/ast"
	"github.com/mark3labs/mcp-go/mcp"
)

// RegisterChangeFunctionSignature registers the change_function_signature tool.
func RegisterChangeFunctionSignature(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("change_function_signature",
		mcp.WithDescription("Rewrite a function or method signature while preserving the body verbatim. `new_signature` is the replacement declaration up to (but not including) the opening brace. Requires a prior Read. Go only."),
		mcp.WithString("file_path", mcp.Required(), mcp.Description("Absolute or workspace-relative path to a Go source file.")),
		mcp.WithString("function_name", mcp.Required(), mcp.Description("Either a top-level function name (\"Foo\") or a method (\"(*T).Foo\").")),
		mcp.WithString("new_signature", mcp.Required(), mcp.Description("Replacement declaration up to the body, e.g. `func (f *Foo) Bar(ctx context.Context) error`.")),
	)
	s.AddTool(tool, HandleChangeFunctionSignature(deps))
}

// HandleChangeFunctionSignature returns the change_function_signature handler.
func HandleChangeFunctionSignature(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		filePath, funcName, newSig, errRes := parseChangeSigArgs(args)
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

		if errRes := validateNewSignature(lang, newSig); errRes != nil {
			return errRes, nil
		}

		updated, errRes := spliceSignature(source, match, newSig)
		if errRes != nil {
			return errRes, nil
		}

		if err := atomicWrite(abs, updated); err != nil {
			return ErrorResult("write: %v", err), nil
		}

		rel := relForReport(deps.Workspace.Root(), abs)
		return TextResult(formatChangeSigResult(rel, match, source, updated)), nil
	}
}

func parseChangeSigArgs(args map[string]any) (filePath, funcName, newSig string, errRes *mcp.CallToolResult) {
	filePath, _ = args["file_path"].(string)
	if filePath == "" {
		return "", "", "", ErrorResult("file_path is required")
	}
	funcName, _ = args["function_name"].(string)
	if funcName == "" {
		return "", "", "", ErrorResult("function_name is required")
	}
	newSig, _ = args["new_signature"].(string)
	if newSig == "" {
		return "", "", "", ErrorResult("new_signature is required")
	}
	return filePath, funcName, newSig, nil
}

// validateNewSignature confirms the provided signature pairs with a stub body
// to form a valid function declaration. We append "{}" before handing to the
// language validator — this catches a typo in the params, a missing `func`,
// or a malformed return list before we touch the file.
func validateNewSignature(lang ast.Language, newSig string) *mcp.CallToolResult {
	synthetic := strings.TrimRight(newSig, " \t\n") + " {}"
	if err := lang.ValidateFuncDecl(synthetic); err != nil {
		return ErrorResult(errReplacementParse, err)
	}
	return nil
}

// spliceSignature replaces [SigStart, BodyStart) — i.e. the signature bytes,
// not including the '{' — with `newSig + " "`. The trailing space preserves
// the `func F() { ... }` spacing. If new_signature already ends with a
// whitespace, we skip the added space to avoid double-whitespace.
func spliceSignature(source []byte, match ast.Match, newSig string) ([]byte, *mcp.CallToolResult) {
	replacement := newSig
	if !endsWithWhitespace(newSig) {
		replacement += " "
	}
	out, err := ast.ReplaceRange(source, match.SigStart, match.BodyStart, []byte(replacement))
	if err != nil {
		return nil, ErrorResult("splice: %v", err)
	}
	return out, nil
}

func endsWithWhitespace(s string) bool {
	if s == "" {
		return false
	}
	last := s[len(s)-1]
	return last == ' ' || last == '\t' || last == '\n'
}

func formatChangeSigResult(rel string, match ast.Match, before, after []byte) string {
	beforeRegion := ast.RenderNode(before, match.SigStart, match.BodyStart)
	delta := len(after) - len(before)
	afterRegion := ast.RenderNode(after, match.SigStart, match.BodyStart+delta)
	diff := unifiedDiffRegion(beforeRegion, afterRegion, match.Line)
	return fmt.Sprintf("change_function_signature: modified %s at line %d\n%s", rel, match.Line, diff)
}
