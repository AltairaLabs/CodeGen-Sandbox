package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// RegisterWrite registers the Write tool.
func RegisterWrite(s Registrar, deps *Deps) {
	tool := mcp.NewTool("Write",
		mcp.WithDescription("Write a file. Overwriting an existing file requires a prior Read."),
		mcp.WithString("file_path", mcp.Required()),
		mcp.WithString("content", mcp.Required()),
	)
	s.AddTool(tool, HandleWrite(deps))
}

// HandleWrite returns the Write tool handler.
func HandleWrite(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		filePath, _ := args["file_path"].(string)
		if filePath == "" {
			return ErrorResult("file_path is required"), nil
		}
		content, ok := args["content"].(string)
		if !ok {
			return ErrorResult("content is required"), nil
		}

		abs, err := deps.Workspace.Resolve(filePath)
		if err != nil {
			return ErrorResult("resolve path: %v", err), nil
		}

		if info, statErr := os.Stat(abs); statErr == nil {
			if info.IsDir() {
				return ErrorResult("path is a directory: %s", filePath), nil
			}
			if !deps.Tracker.HasBeenRead(abs) {
				return ErrorResult("refusing to overwrite %s: Read it first", filePath), nil
			}
		}

		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return ErrorResult("mkdir: %v", err), nil
		}

		if err := atomicWrite(abs, []byte(content)); err != nil {
			return ErrorResult("write: %v", err), nil
		}

		deps.Tracker.MarkRead(abs)
		return TextResult(fmt.Sprintf("wrote %d bytes to %s", len(content), filePath)), nil
	}
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
