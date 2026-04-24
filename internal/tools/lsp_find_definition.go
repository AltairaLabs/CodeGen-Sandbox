package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

func registerFindDefinition(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("find_definition",
		mcp.WithDescription("Return the defining location(s) of the symbol at file_path:line:col. Backed by the language server (gopls for Go). Positions are 1-based."),
		mcp.WithString("file_path", mcp.Required(), mcp.Description("Absolute or workspace-relative path to the source file.")),
		mcp.WithNumber("line", mcp.Required(), mcp.Description("1-based line number of the cursor position.")),
		mcp.WithNumber("col", mcp.Required(), mcp.Description("1-based column number of the cursor position.")),
	)
	s.AddTool(tool, HandleFindDefinition(deps))
}

// HandleFindDefinition returns the find_definition handler.
func HandleFindDefinition(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)
		parsed, errRes := parseLSPPosArgs(args)
		if errRes != nil {
			return errRes, nil
		}

		_, rel, errRes := resolveLSPFile(deps, parsed.filePath)
		if errRes != nil {
			return errRes, nil
		}

		client, errRes := acquireLSPClient(ctx, deps)
		if errRes != nil {
			return errRes, nil
		}

		locs, err := client.Definition(ctx, rel, parsed.line, parsed.col)
		if err != nil {
			return ErrorResult("find_definition: %v", err), nil
		}
		if len(locs) == 0 {
			return TextResult(fmt.Sprintf("no definition found at %s:%d:%d", rel, parsed.line, parsed.col)), nil
		}

		header := fmt.Sprintf("Found %d definition(s) for symbol at %s:%d:%d:", len(locs), rel, parsed.line, parsed.col)
		return TextResult(formatLocationList(header, deps.Workspace.Root(), locs)), nil
	}
}
