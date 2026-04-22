package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// RegisterEdit registers the Edit tool.
func RegisterEdit(s *mcpserver.MCPServer, deps *Deps) {
	tool := mcp.NewTool("Edit",
		mcp.WithDescription("Exact-string replace within a file. Requires a prior Read."),
		mcp.WithString("file_path", mcp.Required()),
		mcp.WithString("old_string", mcp.Required()),
		mcp.WithString("new_string", mcp.Required()),
		mcp.WithBoolean("replace_all", mcp.Description("If true, replace every occurrence; default false.")),
	)
	s.AddTool(tool, HandleEdit(deps))
}

// HandleEdit returns the Edit tool handler.
func HandleEdit(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		return TextResult(fmt.Sprintf("replaced %d occurrence(s) in %s", count, filePath)), nil
	}
}
