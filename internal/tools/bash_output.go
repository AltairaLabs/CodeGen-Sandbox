package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// RegisterBashOutput registers the BashOutput tool.
func RegisterBashOutput(s Registrar, deps *Deps) {
	tool := mcp.NewTool("BashOutput",
		mcp.WithDescription("Return the current stdout, stderr, and status of a background shell started via Bash with run_in_background=true. Each call returns the FULL captured buffer (up to 1 MiB per stream); agents should grep client-side for specifics."),
		mcp.WithString("shell_id", mcp.Required(), mcp.Description("Shell identifier returned by Bash in background mode.")),
	)
	s.AddTool(tool, HandleBashOutput(deps))
}

// HandleBashOutput returns the BashOutput handler.
func HandleBashOutput(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		stdout, stderr, stdoutT, stderrT, exit, running := sh.Snapshot()

		var sb strings.Builder
		fmt.Fprintf(&sb, "command: %s\n", sh.Command())
		if running {
			sb.WriteString("status: running\n")
		} else {
			fmt.Fprintf(&sb, "status: completed (exit %d)\n", *exit)
		}
		fmt.Fprintf(&sb, "started: %s\n\n", sh.StartedAt().Format(time.RFC3339))
		fmt.Fprintf(&sb, "--- stdout (%d bytes)%s ---\n%s", len(stdout), truncMarker(stdoutT), stdout)
		if len(stdout) > 0 && stdout[len(stdout)-1] != '\n' {
			sb.WriteByte('\n')
		}
		fmt.Fprintf(&sb, "--- stderr (%d bytes)%s ---\n%s", len(stderr), truncMarker(stderrT), stderr)
		if len(stderr) > 0 && stderr[len(stderr)-1] != '\n' {
			sb.WriteByte('\n')
		}
		return TextResult(sb.String()), nil
	}
}

func truncMarker(truncated bool) string {
	if truncated {
		return " [TRUNCATED]"
	}
	return ""
}
