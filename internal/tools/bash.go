package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const (
	defaultBashTimeoutSec = 120
	maxBashTimeoutSec     = 600
	bashOutputCapBytes    = 100 * 1024
	// bashTimeoutExitCode follows the timeout(1) convention (124) so agents
	// can distinguish a timeout from a normal non-zero exit. Shell-style
	// 128+signal codes are avoided here because they can collide with a
	// command that legitimately returns 137.
	bashTimeoutExitCode = 124
)

// RegisterBash registers the Bash tool on the given MCP server.
func RegisterBash(s *mcpserver.MCPServer, deps *Deps) {
	tool := mcp.NewTool("Bash",
		mcp.WithDescription("Run a shell command in the workspace via bash -c. Returns combined stdout+stderr. A trailing 'exit: N' line is emitted for non-zero exits. A 'timed out after Ns' marker is emitted on timeout (exit code 124), and the entire process group is killed so backgrounded children do not survive."),
		mcp.WithString("command", mcp.Required(), mcp.Description("Shell command to run.")),
		mcp.WithString("description", mcp.Required(), mcp.Description("5-10 word description of what this command does. Recorded for agent context; does not affect execution.")),
		mcp.WithNumber("timeout", mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.", defaultBashTimeoutSec, maxBashTimeoutSec))),
	)
	s.AddTool(tool, HandleBash(deps))
}

// HandleBash returns the Bash tool handler.
func HandleBash(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		command, _ := args["command"].(string)
		if command == "" {
			return ErrorResult("command is required"), nil
		}
		// description is required by the schema but has no execution effect;
		// it exists so agent-context inspection of the MCP request log shows
		// human-readable intent for each Bash call.
		if desc, _ := args["description"].(string); desc == "" {
			return ErrorResult("description is required"), nil
		}

		timeoutSec := defaultBashTimeoutSec
		if v, ok := args["timeout"].(float64); ok && int(v) > 0 {
			timeoutSec = int(v)
			if timeoutSec > maxBashTimeoutSec {
				timeoutSec = maxBashTimeoutSec
			}
		}

		execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()

		cmd := exec.CommandContext(execCtx, "bash", "-c", command)
		cmd.Dir = deps.Workspace.Root()
		// Stdin = nil routes the child's stdin to /dev/null (per exec.Cmd
		// docs); bash reads EOF immediately. Env is inherited — the container
		// runtime is responsible for scrubbing secrets at launch.
		cmd.Stdin = nil

		// Put bash and all its descendants in a fresh process group, then
		// kill the whole group on timeout. Without this, exec.CommandContext
		// only SIGKILLs the direct child, so a command like `sleep 10 & wait`
		// would outlive the timeout by keeping the wait-on-children pipe open.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if cmd.Process != nil {
				// Negative PID = process-group kill. Best-effort; swallow the
				// error because by the time we get here the process may have
				// exited on its own.
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			return nil
		}
		// Give the group a short grace window to flush output after the
		// SIGKILL; otherwise CombinedOutput can block on a still-open pipe.
		cmd.WaitDelay = 2 * time.Second

		out, runErr := cmd.CombinedOutput()

		timedOut := errors.Is(execCtx.Err(), context.DeadlineExceeded)
		exitCode := 0
		if runErr != nil {
			var exitErr *exec.ExitError
			// Check timedOut FIRST: a signal-killed process is itself an
			// *exec.ExitError (with ExitCode -1), so matching on exitErr
			// first would starve the timeout branch.
			switch {
			case timedOut:
				exitCode = bashTimeoutExitCode
			case errors.As(runErr, &exitErr):
				exitCode = exitErr.ExitCode()
			default:
				return ErrorResult("bash: %v", runErr), nil
			}
		}

		body := truncateOutput(out, bashOutputCapBytes)

		var sb strings.Builder
		sb.Write(body)
		if len(body) > 0 && !bytes.HasSuffix(body, []byte("\n")) {
			sb.WriteByte('\n')
		}
		if timedOut {
			fmt.Fprintf(&sb, "bash: timed out after %ds\n", timeoutSec)
		}
		if exitCode != 0 || timedOut {
			fmt.Fprintf(&sb, "exit: %d\n", exitCode)
		}
		return TextResult(sb.String()), nil
	}
}

// truncateOutput caps b at limit bytes, appending a marker when truncated.
func truncateOutput(b []byte, limit int) []byte {
	if len(b) <= limit {
		return b
	}
	trunc := make([]byte, 0, limit+64)
	trunc = append(trunc, b[:limit]...)
	trunc = append(trunc, fmt.Appendf(nil, "\n... (output truncated, %d bytes elided)", len(b)-limit)...)
	return trunc
}
