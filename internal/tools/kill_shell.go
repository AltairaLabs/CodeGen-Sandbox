package tools

import (
	"context"
	"syscall"

	"github.com/mark3labs/mcp-go/mcp"
)

// RegisterKillShell registers the KillShell tool.
func RegisterKillShell(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("KillShell",
		mcp.WithDescription("Kill a background shell started via Bash with run_in_background=true. Sends SIGKILL to the shell's process group (catches backgrounded children) and removes it from the registry. Subsequent BashOutput calls for the killed shell return 'unknown shell_id'."),
		mcp.WithString("shell_id", mcp.Required(), mcp.Description("Shell identifier returned by Bash in background mode.")),
	)
	s.AddTool(tool, HandleKillShell(deps))
}

// HandleKillShell returns the KillShell handler.
func HandleKillShell(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if deps.Shells == nil {
			return ErrorResult("background shells not configured"), nil
		}
		args, _ := req.Params.Arguments.(map[string]any)
		id, _ := args["shell_id"].(string)
		if id == "" {
			return ErrorResult("shell_id is required"), nil
		}
		sh, ok := deps.Shells.Get(id)
		if !ok {
			return ErrorResult("unknown shell_id: %s", id), nil
		}
		if pgid := sh.Pgid(); pgid > 0 {
			// Negative PID = process-group kill. Best-effort; by the time we
			// get here the process may already have exited on its own.
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
		deps.Shells.Remove(id)
		return TextResult("killed: " + id + "\n"), nil
	}
}
