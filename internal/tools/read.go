package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
)

const defaultReadLimit = 2000

// RegisterRead registers the Read tool with the given MCP server.
func RegisterRead(s ToolAdder, deps *Deps) {
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

		filePath, offset, limit, errRes := parseReadArgs(args)
		if errRes != nil {
			return errRes, nil
		}

		abs, errRes := resolveReadPath(deps, filePath)
		if errRes != nil {
			return errRes, nil
		}

		body, err := readNumbered(abs, offset, limit)
		if err != nil {
			return ErrorResult("read: %v", err), nil
		}

		deps.Tracker.MarkRead(abs)
		deps.Metrics.ReadBytes(len(body))
		return TextResult(body), nil
	}
}

func parseReadArgs(args map[string]any) (filePath string, offset, limit int, errRes *mcp.CallToolResult) {
	filePath, _ = args["file_path"].(string)
	if filePath == "" {
		return "", 0, 0, ErrorResult("file_path is required")
	}
	offset = 1
	if v, ok := args["offset"].(float64); ok && int(v) > 1 {
		offset = int(v)
	}
	limit = defaultReadLimit
	if v, ok := args["limit"].(float64); ok && int(v) > 0 {
		limit = int(v)
	}
	return filePath, offset, limit, nil
}

func resolveReadPath(deps *Deps, filePath string) (string, *mcp.CallToolResult) {
	abs, err := deps.Workspace.Resolve(filePath)
	if err != nil {
		if errors.Is(err, workspace.ErrOutsideWorkspace) {
			deps.Metrics.PathViolation()
		}
		return "", ErrorResult("resolve path: %v", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", ErrorResult("stat: %v", err)
	}
	if info.IsDir() {
		return "", ErrorResult("path is a directory: %s", filePath)
	}
	return abs, nil
}

func readNumbered(abs string, offset, limit int) (body string, err error) {
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
