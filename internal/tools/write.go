package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
)

// RegisterWrite registers the Write tool.
func RegisterWrite(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("Write",
		mcp.WithDescription("Write a file. Overwriting an existing file requires a prior Read. In multi-workspace mode pass `workspace` to pick one."),
		mcp.WithString("file_path", mcp.Required()),
		mcp.WithString("content", mcp.Required()),
		withWorkspaceArg(),
	)
	s.AddTool(tool, HandleWrite(deps))
}

// HandleWrite returns the Write tool handler.
func HandleWrite(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		ws, errRes := ResolveWorkspace(deps, args)
		if errRes != nil {
			return errRes, nil
		}

		filePath, content, errRes := parseWriteArgs(args)
		if errRes != nil {
			return errRes, nil
		}

		abs, err := ws.Resolve(filePath)
		if err != nil {
			if errors.Is(err, workspace.ErrOutsideWorkspace) {
				deps.Metrics.PathViolation()
			}
			return ErrorResult("resolve path: %v", err), nil
		}

		if errRes := checkWriteTarget(deps, abs, filePath); errRes != nil {
			return errRes, nil
		}

		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return ErrorResult("mkdir: %v", err), nil
		}

		if err := atomicWrite(abs, []byte(content)); err != nil {
			return ErrorResult("write: %v", err), nil
		}

		deps.Tracker.MarkRead(abs)
		deps.Metrics.WriteBytes(len(content))
		return TextResult(fmt.Sprintf("wrote %d bytes to %s", len(content), filePath)), nil
	}
}

func parseWriteArgs(args map[string]any) (filePath, content string, errRes *mcp.CallToolResult) {
	filePath, _ = args["file_path"].(string)
	if filePath == "" {
		return "", "", ErrorResult("file_path is required")
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", "", ErrorResult("content is required")
	}
	return filePath, content, nil
}

func checkWriteTarget(deps *Deps, abs, filePath string) *mcp.CallToolResult {
	info, err := os.Stat(abs)
	if err != nil {
		return nil
	}
	if info.IsDir() {
		return ErrorResult("path is a directory: %s", filePath)
	}
	if !deps.Tracker.HasBeenRead(abs) {
		return ErrorResult("refusing to overwrite %s: Read it first", filePath)
	}
	return nil
}

func atomicWrite(abs string, data []byte) (err error) {
	tmp := fmt.Sprintf("%s.tmp.%d.%d", abs, os.Getpid(), time.Now().UnixNano())
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // path already contained by workspace
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()

	if _, werr := f.Write(data); werr != nil {
		return werr
	}
	if serr := f.Sync(); serr != nil {
		return serr
	}

	if rerr := os.Rename(tmp, abs); rerr != nil {
		return rerr
	}
	return nil
}
