package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/ast"
	"github.com/mark3labs/mcp-go/mcp"
)

// RegisterInsertAfterMethod registers the insert_after_method tool.
func RegisterInsertAfterMethod(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("insert_after_method",
		mcp.WithDescription("Insert a new method declaration immediately after an existing method on the same receiver type. Preserves the anchor method's indentation. Requires a prior Read. Go only."),
		mcp.WithString("file_path", mcp.Required(), mcp.Description("Absolute or workspace-relative path to a Go source file.")),
		mcp.WithString("receiver_type", mcp.Required(), mcp.Description("Name of the receiver type (e.g. \"Server\" or \"*Server\"). Pointer/value form doesn't matter — both forms are treated as methods on the type.")),
		mcp.WithString("method_name", mcp.Required(), mcp.Description("Name of the anchor method the new method should follow.")),
		mcp.WithString("new_method", mcp.Required(), mcp.Description("Complete new method declaration including `func`, receiver, signature, and body.")),
	)
	s.AddTool(tool, HandleInsertAfterMethod(deps))
}

// HandleInsertAfterMethod returns the insert_after_method handler.
func HandleInsertAfterMethod(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		parsed, errRes := parseInsertArgs(args)
		if errRes != nil {
			return errRes, nil
		}

		abs, source, lang, tree, errRes := astLoadTarget(deps, parsed.filePath)
		if errRes != nil {
			return errRes, nil
		}

		match, errRes := resolveAnchorMethod(lang, tree, parsed)
		if errRes != nil {
			return errRes, nil
		}

		if err := lang.ValidateFuncDecl(parsed.newMethod); err != nil {
			return ErrorResult(errReplacementParse, err), nil
		}

		updated, insertLine, errRes := spliceAfterMethod(source, match, parsed.newMethod)
		if errRes != nil {
			return errRes, nil
		}

		if err := atomicWrite(abs, updated); err != nil {
			return ErrorResult("write: %v", err), nil
		}

		rel := relForReport(deps.Workspace.Root(), abs)
		return TextResult(formatInsertResult(rel, insertLine, parsed.newMethod)), nil
	}
}

type insertArgs struct {
	filePath     string
	receiverType string
	methodName   string
	newMethod    string
}

func parseInsertArgs(args map[string]any) (*insertArgs, *mcp.CallToolResult) {
	filePath, _ := args["file_path"].(string)
	if filePath == "" {
		return nil, ErrorResult("file_path is required")
	}
	receiver, _ := args["receiver_type"].(string)
	if receiver == "" {
		return nil, ErrorResult("receiver_type is required")
	}
	method, _ := args["method_name"].(string)
	if method == "" {
		return nil, ErrorResult("method_name is required")
	}
	newMethod, _ := args["new_method"].(string)
	if newMethod == "" {
		return nil, ErrorResult("new_method is required")
	}
	return &insertArgs{filePath: filePath, receiverType: receiver, methodName: method, newMethod: newMethod}, nil
}

// resolveAnchorMethod finds the anchor method or returns an error enumerating
// the methods that DO exist on the receiver — mirrors how Edit reports
// "old_string not found" with extra context so the agent can recover.
func resolveAnchorMethod(lang ast.Language, tree ast.Tree, p *insertArgs) (ast.Match, *mcp.CallToolResult) {
	matches, err := lang.FindMethod(tree, p.receiverType, p.methodName)
	if err != nil {
		return ast.Match{}, ErrorResult("lookup method: %v", err)
	}
	if len(matches) == 0 {
		return ast.Match{}, formatMethodNotFound(lang, tree, p)
	}
	if len(matches) > 1 {
		return ast.Match{}, ErrorResult(errAmbiguous, p.methodName, len(matches), ast.FormatMatches(matches))
	}
	return matches[0], nil
}

func formatMethodNotFound(lang ast.Language, tree ast.Tree, p *insertArgs) *mcp.CallToolResult {
	existing, _ := lang.ListMethods(tree, p.receiverType)
	if len(existing) == 0 {
		return ErrorResult("method %s not found on %s in %s (no methods found on that receiver)", p.methodName, p.receiverType, p.filePath)
	}
	names := make([]string, 0, len(existing))
	for _, m := range existing {
		names = append(names, m.Name)
	}
	return ErrorResult("method %s not found on %s in %s (available: %s)", p.methodName, p.receiverType, p.filePath, strings.Join(names, ", "))
}

// spliceAfterMethod inserts the new method after the anchor method's closing
// brace, preserving indentation and separating the two with a blank line.
func spliceAfterMethod(source []byte, anchor ast.Match, newMethod string) ([]byte, int, *mcp.CallToolResult) {
	indent := lineIndentAtOffset(source, anchor.SigStart)
	reIndented := reindentMethod(newMethod, indent)

	// Insert point is anchor.BodyEnd. We precede the inserted method with
	// "\n\n" + indented method text. The file's trailing newline (if any)
	// stays untouched since BodyEnd is before any post-brace whitespace.
	insertion := "\n\n" + reIndented

	out, err := ast.ReplaceRange(source, anchor.BodyEnd, anchor.BodyEnd, []byte(insertion))
	if err != nil {
		return nil, 0, ErrorResult("splice: %v", err)
	}
	// The first line of the new method lands two lines after BodyEnd's
	// source line (blank separator + method line).
	insertLine := lineOfOffset(source, anchor.BodyEnd) + 2
	return out, insertLine, nil
}

// reindentMethod strips the caller's leading whitespace (if any) and prefixes
// every non-empty line with the anchor's indent. Callers typically pass a
// zero-indent method; re-indenting makes sure the inserted block visually
// matches a same-level peer rather than drifting to column 0.
func reindentMethod(method, indent string) string {
	if indent == "" {
		return method
	}
	lines := strings.Split(method, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		lines[i] = indent + ln
	}
	return strings.Join(lines, "\n")
}

func formatInsertResult(rel string, line int, newMethod string) string {
	return fmt.Sprintf("insert_after_method: inserted %d bytes in %s at line %d\n+%s",
		len(newMethod), rel, line,
		strings.ReplaceAll(strings.TrimRight(newMethod, "\n"), "\n", "\n+"))
}
