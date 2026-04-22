package tools

import (
	"context"
	"fmt"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const (
	defaultRunTypecheckTimeoutSec = 120
	maxRunTypecheckTimeoutSec     = 600
)

// RegisterRunTypecheck registers the run_typecheck tool.
func RegisterRunTypecheck(s *mcpserver.MCPServer, deps *Deps) {
	tool := mcp.NewTool("run_typecheck",
		mcp.WithDescription("Run the project's type checker. Project type is detected from the workspace root (currently: Go via go.mod, uses `go vet`). Returns combined stdout+stderr plus a trailing 'exit: N' line."),
		mcp.WithNumber("timeout", mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.", defaultRunTypecheckTimeoutSec, maxRunTypecheckTimeoutSec))),
	)
	s.AddTool(tool, HandleRunTypecheck(deps))
}

// HandleRunTypecheck returns the run_typecheck tool handler.
func HandleRunTypecheck(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		det := verify.Detect(deps.Workspace.Root())
		if det == nil {
			return ErrorResult("no supported project detected in workspace root"), nil
		}

		args, _ := req.Params.Arguments.(map[string]any)
		timeoutSec := defaultRunTypecheckTimeoutSec
		if v, ok := args["timeout"].(float64); ok && int(v) > 0 {
			timeoutSec = int(v)
			if timeoutSec > maxRunTypecheckTimeoutSec {
				timeoutSec = maxRunTypecheckTimeoutSec
			}
		}

		res, err := runVerifyCmd(ctx, det.TypecheckCmd(), deps.Workspace.Root(), timeoutSec)
		if err != nil {
			return ErrorResult("run_typecheck: %v", err), nil
		}
		return TextResult(formatVerifyResult(res, timeoutSec)), nil
	}
}
