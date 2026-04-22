package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// RegisterEdit registers the Edit tool.
func RegisterEdit(s *mcpserver.MCPServer, deps *Deps) {
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

		filePath, _ := args["file_path"].(string)
		if filePath == "" {
			return ErrorResult("file_path is required"), nil
		}
		oldStr, ok := args["old_string"].(string)
		if !ok {
			return ErrorResult("old_string is required"), nil
		}
		if oldStr == "" {
			return ErrorResult("old_string must be non-empty"), nil
		}
		newStr, ok := args["new_string"].(string)
		if !ok {
			return ErrorResult("new_string is required"), nil
		}
		replaceAll, _ := args["replace_all"].(bool)

		abs, err := deps.Workspace.Resolve(filePath)
		if err != nil {
			return ErrorResult("resolve path: %v", err), nil
		}

		info, err := os.Stat(abs)
		if err != nil {
			return ErrorResult("stat: %v", err), nil
		}
		if info.IsDir() {
			return ErrorResult("path is a directory: %s", filePath), nil
		}
		if !deps.Tracker.HasBeenRead(abs) {
			return ErrorResult("refusing to edit %s: Read it first", filePath), nil
		}

		data, err := os.ReadFile(abs) //nolint:gosec // workspace-contained
		if err != nil {
			return ErrorResult("read: %v", err), nil
		}
		body := string(data)

		count := strings.Count(body, oldStr)
		if count == 0 {
			return ErrorResult("old_string not found in %s", filePath), nil
		}
		if count > 1 && !replaceAll {
			return ErrorResult("old_string matched %d times in %s; add context to make it unique or set replace_all=true", count, filePath), nil
		}

		var updated string
		if replaceAll {
			updated = strings.ReplaceAll(body, oldStr, newStr)
		} else {
			updated = strings.Replace(body, oldStr, newStr, 1)
		}

		if err := atomicWrite(abs, []byte(updated)); err != nil {
			return ErrorResult("write: %v", err), nil
		}
		msg := fmt.Sprintf("replaced %d occurrence(s) in %s", count, filePath)
		if feedback := postEditLintFeedback(ctx, deps.Workspace.Root(), abs); feedback != "" {
			msg += "\n\n" + feedback
		}
		return TextResult(msg), nil
	}
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
