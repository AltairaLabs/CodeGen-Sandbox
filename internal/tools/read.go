package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const defaultReadLimit = 2000

// RegisterRead registers the Read tool with the given MCP server.
func RegisterRead(s *mcpserver.MCPServer, deps *Deps) {
	tool := mcp.NewTool("Read",
		mcp.WithDescription("Read a file from the workspace. Returns cat -n style line-numbered text."),
		mcp.WithString("file_path", mcp.Required(), mcp.Description("Absolute or workspace-relative path.")),
		mcp.WithNumber("offset", mcp.Description("1-based line to start at (default 1).")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of lines to return (default 2000).")),
	)
	s.AddTool(tool, HandleRead(deps))
}

// HandleRead returns the Read tool handler.
func HandleRead(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		filePath, _ := args["file_path"].(string)
		if filePath == "" {
			return ErrorResult("file_path is required"), nil
		}

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

		offset := 1
		if v, ok := args["offset"].(float64); ok && int(v) > 1 {
			offset = int(v)
		}
		limit := defaultReadLimit
		if v, ok := args["limit"].(float64); ok && int(v) > 0 {
			limit = int(v)
		}

		body, err := readNumbered(abs, offset, limit)
		if err != nil {
			return ErrorResult("read: %v", err), nil
		}

		deps.Tracker.MarkRead(abs)
		return TextResult(body), nil
	}
}

func readNumbered(abs string, offset, limit int) (string, error) {
	f, err := os.Open(abs) //nolint:gosec // path already contained by workspace
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	var out strings.Builder
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	lineNo := 0
	written := 0
	for scanner.Scan() {
		lineNo++
		if lineNo < offset {
			continue
		}
		if written >= limit {
			break
		}
		fmt.Fprintf(&out, "%d\t%s\n", lineNo, scanner.Text())
		written++
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if written == 0 && lineNo > 0 && offset > lineNo {
		return "", fmt.Errorf("offset %d exceeds line count %d", offset, lineNo)
	}
	return out.String(), nil
}
