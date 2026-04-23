package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
)

// RegisterEdit registers the Edit tool.
func RegisterEdit(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("Edit",
		mcp.WithDescription("Exact-string replace within a file. Requires a prior Read. On Go projects, lint findings for the edited file are appended to the success message as 'post-edit lint findings (N):' — best effort, silent on linter failure or absence."),
		mcp.WithString("file_path", mcp.Required()),
		mcp.WithString("old_string", mcp.Required()),
		mcp.WithString("new_string", mcp.Required()),
		mcp.WithBoolean("replace_all", mcp.Description("If true, replace every occurrence; default false.")),
	)
	s.AddTool(tool, HandleEdit(deps))
}

// HandleEdit returns the Edit tool handler.
func HandleEdit(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		parsed, errRes := parseEditArgs(args)
		if errRes != nil {
			return errRes, nil
		}

		abs, errRes := resolveEditTarget(deps, parsed.filePath)
		if errRes != nil {
			return errRes, nil
		}

		data, err := os.ReadFile(abs) //nolint:gosec // workspace-contained
		if err != nil {
			return ErrorResult("read: %v", err), nil
		}

		updated, count, errRes := applyEdit(string(data), parsed)
		if errRes != nil {
			return errRes, nil
		}

		if err := atomicWrite(abs, []byte(updated)); err != nil {
			return ErrorResult("write: %v", err), nil
		}
		msg := fmt.Sprintf("replaced %d occurrence(s) in %s", count, parsed.filePath)
		if feedback := postEditLintFeedback(ctx, deps.Workspace.Root(), abs); feedback != "" {
			msg += "\n\n" + feedback
		}
		return TextResult(msg), nil
	}
}

type editArgs struct {
	filePath   string
	oldStr     string
	newStr     string
	replaceAll bool
}

func parseEditArgs(args map[string]any) (*editArgs, *mcp.CallToolResult) {
	filePath, _ := args["file_path"].(string)
	if filePath == "" {
		return nil, ErrorResult("file_path is required")
	}
	oldStr, ok := args["old_string"].(string)
	if !ok {
		return nil, ErrorResult("old_string is required")
	}
	if oldStr == "" {
		return nil, ErrorResult("old_string must be non-empty")
	}
	newStr, ok := args["new_string"].(string)
	if !ok {
		return nil, ErrorResult("new_string is required")
	}
	replaceAll, _ := args["replace_all"].(bool)
	return &editArgs{filePath: filePath, oldStr: oldStr, newStr: newStr, replaceAll: replaceAll}, nil
}

func resolveEditTarget(deps *Deps, filePath string) (string, *mcp.CallToolResult) {
	abs, err := deps.Workspace.Resolve(filePath)
	if err != nil {
		return "", ErrorResult("resolve path: %v", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", ErrorResult("stat: %v", err)
	}
	if info.IsDir() {
		return "", ErrorResult("path is a directory: %s", filePath)
	}
	if !deps.Tracker.HasBeenRead(abs) {
		return "", ErrorResult("refusing to edit %s: Read it first", filePath)
	}
	return abs, nil
}

func applyEdit(body string, p *editArgs) (string, int, *mcp.CallToolResult) {
	count := strings.Count(body, p.oldStr)
	if count == 0 {
		return "", 0, ErrorResult("old_string not found in %s", p.filePath)
	}
	if count > 1 && !p.replaceAll {
		return "", 0, ErrorResult("old_string matched %d times in %s; add context to make it unique or set replace_all=true", count, p.filePath)
	}
	if p.replaceAll {
		return strings.ReplaceAll(body, p.oldStr, p.newStr), count, nil
	}
	return strings.Replace(body, p.oldStr, p.newStr, 1), count, nil
}

// postEditLintTimeoutSec is deliberately short — the linter run is a
// best-effort annotation on the Edit result, not the primary purpose of the
// call, so we don't want a slow linter to dominate per-Edit latency.
const postEditLintTimeoutSec = 30

// postEditLintFeedback runs the project's linter (best effort) and returns a
// formatted block of findings that apply to the file just edited. Returns
// "" if there are no findings, no detected project, or the linter couldn't
// run for any reason — Edit should proceed normally.
//
// This contract intentionally diverges from run_lint: Edit suppresses
// partial findings on error to keep Edit's success signal crisp ("the
// replacement succeeded"), while run_lint forwards partial findings because
// its sole purpose IS to report lint state.
func postEditLintFeedback(ctx context.Context, root, editedAbs string) string {
	findings, err := verify.Lint(ctx, root, postEditLintTimeoutSec)
	if err != nil || len(findings) == 0 {
		return ""
	}
	rel, err := filepath.Rel(root, editedAbs)
	if err != nil {
		return ""
	}

	var matched []verify.LintFinding
	for _, f := range findings {
		cmpFile := f.File
		if filepath.IsAbs(cmpFile) {
			if r, err := filepath.Rel(root, cmpFile); err == nil {
				cmpFile = r
			}
		}
		if cmpFile == rel {
			matched = append(matched, f)
		}
	}
	if len(matched) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "post-edit lint findings (%d):\n", len(matched))
	for _, f := range matched {
		fmt.Fprintf(&sb, "%s:%d:%d:%s: %s\n", f.File, f.Line, f.Column, f.Rule, f.Message)
	}
	return sb.String()
}
