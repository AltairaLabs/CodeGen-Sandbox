package tools

import (
	"context"
	"fmt"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultRunTestsTimeoutSec = 300
	maxRunTestsTimeoutSec     = 1800
)

// RegisterRunTests registers the run_tests tool on the given MCP server.
func RegisterRunTests(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("run_tests",
		mcp.WithDescription("Run the project's test suite. Project type is detected from the workspace root (currently: Go via go.mod). Returns combined stdout+stderr plus a trailing 'exit: N' line."),
		mcp.WithNumber("timeout", mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.", defaultRunTestsTimeoutSec, maxRunTestsTimeoutSec))),
	)
	s.AddTool(tool, HandleRunTests(deps))
}

// HandleRunTests returns the run_tests tool handler.
func HandleRunTests(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		det := verify.Detect(deps.Workspace.Root())
		if det == nil {
			return ErrorResult("no supported project detected in workspace root"), nil
		}

		args, _ := req.Params.Arguments.(map[string]any)
		timeoutSec := defaultRunTestsTimeoutSec
		if v, ok := args["timeout"].(float64); ok && int(v) > 0 {
			timeoutSec = int(v)
			if timeoutSec > maxRunTestsTimeoutSec {
				timeoutSec = maxRunTestsTimeoutSec
			}
		}

		res, err := runVerifyCmd(ctx, det.TestCmd(), deps.Workspace.Root(), timeoutSec)
		if err != nil {
			return ErrorResult("run_tests: %v", err), nil
		}

		return TextResult(formatVerifyResult(res, timeoutSec)), nil
	}
}
