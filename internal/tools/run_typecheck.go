package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultRunTypecheckTimeoutSec = 120
	maxRunTypecheckTimeoutSec     = 600
)

// RegisterRunTypecheck registers the run_typecheck tool.
func RegisterRunTypecheck(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("run_typecheck",
		mcp.WithDescription("Run the project's type checker. Project type is detected from the workspace root (Go: `go vet`, Node: project tsc, Rust: cargo check; Python has no first-class typecheck wired). In a polyglot workspace pass `language` to pick one. Returns combined stdout+stderr plus a trailing 'exit: N' line."),
		mcp.WithNumber("timeout", mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.", defaultRunTypecheckTimeoutSec, maxRunTypecheckTimeoutSec))),
		withLanguageArg(),
	)
	s.AddTool(tool, HandleRunTypecheck(deps))
}

// HandleRunTypecheck returns the run_typecheck tool handler.
func HandleRunTypecheck(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)
		det, errRes := dispatchByLanguage(deps, args)
		if errRes != nil {
			return errRes, nil
		}

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
		// Agent-health: stamp the last-green timestamp on a clean typecheck.
		if res.ExitCode == 0 {
			deps.Health.ObserveGreen()
		}
		return TextResult(formatVerifyResult(res, timeoutSec)), nil
	}
}
