package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
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
func RegisterBash(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("Bash",
		mcp.WithDescription("Run a shell command in the workspace via bash -c. Runs from the workspace root (use `cd subdir && ...` to move); stdin is closed; env is inherited from the server. Returns combined stdout+stderr, capped at 100 KiB with a '... (output truncated, N bytes elided)' marker — late output such as the last few lines of a build log may be elided. A trailing 'exit: N' line is emitted for non-zero exits. A 'timed out after Ns' marker is emitted on timeout (exit code 124), and the entire process group is killed so backgrounded children do not survive. A small set of dangerous tokens (sudo, su, shutdown, reboot, halt, poweroff, chroot, mount, umount, mkfs) at plausible command positions are rejected before the command runs."),
		mcp.WithString("command", mcp.Required(), mcp.Description("Shell command to run.")),
		mcp.WithString("description", mcp.Required(), mcp.Description("5-10 word description of what this command does. Recorded for agent context; does not affect execution.")),
		mcp.WithNumber("timeout", mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.", defaultBashTimeoutSec, maxBashTimeoutSec))),
		mcp.WithBoolean("run_in_background", mcp.Description("If true, spawn the command in the background and return a shell_id immediately. Use BashOutput to poll and KillShell to terminate.")),
	)
	s.AddTool(tool, HandleBash(deps))
}

// HandleBash returns the Bash tool handler.
func HandleBash(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		command, errRes := validateBashArgs(args)
		if errRes != nil {
			return errRes, nil
		}

		if bg, _ := args["run_in_background"].(bool); bg {
			return handleBashBackground(deps, command)
		}

		timeoutSec := parseBashTimeout(args)
		execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()

		cmd := newBashForegroundCmd(execCtx, deps.Workspace.Root(), command)
		out, runErr := cmd.CombinedOutput()

		timedOut := errors.Is(execCtx.Err(), context.DeadlineExceeded)
		exitCode, errRes := bashExitCode(runErr, timedOut)
		if errRes != nil {
			return errRes, nil
		}
		return TextResult(formatBashOutput(out, exitCode, timedOut, timeoutSec)), nil
	}
}

func validateBashArgs(args map[string]any) (string, *mcp.CallToolResult) {
	command, _ := args["command"].(string)
	if command == "" {
		return "", ErrorResult("command is required")
	}
	// description is required by the schema but has no execution effect;
	// it exists so agent-context inspection of the MCP request log shows
	// human-readable intent for each Bash call.
	if desc, _ := args["description"].(string); desc == "" {
		return "", ErrorResult("description is required")
	}
	if reason := denyReason(command); reason != "" {
		return "", ErrorResult("command rejected: %s", reason)
	}
	return command, nil
}

func parseBashTimeout(args map[string]any) int {
	timeoutSec := defaultBashTimeoutSec
	v, ok := args["timeout"].(float64)
	if !ok || int(v) <= 0 {
		return timeoutSec
	}
	timeoutSec = int(v)
	if timeoutSec > maxBashTimeoutSec {
		timeoutSec = maxBashTimeoutSec
	}
	return timeoutSec
}

func newBashForegroundCmd(ctx context.Context, dir, command string) *exec.Cmd {
	// Absolute path — don't resolve "bash" via $PATH. The image ships
	// /bin/bash; relying on $PATH means a poisoned PATH entry could redirect
	// every tool call (sonar: gosecurity:S4036).
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", command)
	cmd.Dir = dir
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
	// Give the group a short grace window to flush output after the SIGKILL;
	// otherwise CombinedOutput can block on a still-open pipe.
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

func bashExitCode(runErr error, timedOut bool) (int, *mcp.CallToolResult) {
	if runErr == nil {
		return 0, nil
	}
	// Check timedOut FIRST: a signal-killed process is itself an
	// *exec.ExitError (with ExitCode -1), so matching on exitErr first would
	// starve the timeout branch.
	if timedOut {
		return bashTimeoutExitCode, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, ErrorResult("bash: %v", runErr)
}

func formatBashOutput(out []byte, exitCode int, timedOut bool, timeoutSec int) string {
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
	return sb.String()
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

// denyPattern matches denylisted command tokens at plausible command
// positions. This is a defense-in-depth layer: the container is the real
// trust boundary. Quoted subcommands (e.g. bash -c "sudo ...") are
// intentionally NOT caught to avoid false positives on echo/printf of the
// same tokens. Determined attackers can trivially bypass via $(echo su)do.
var denyPattern = regexp.MustCompile(
	`(?:^|[\s;&|(])\s*(sudo|su|shutdown|reboot|halt|poweroff|chroot|mount|umount|mkfs(?:\.\w+)?)(?:$|[\s;&|)])`,
)

// denyReason returns a non-empty reason string if command matches the denylist.
func denyReason(command string) string {
	if m := denyPattern.FindStringSubmatch(command); m != nil {
		return fmt.Sprintf("command uses denylisted token %q", m[1])
	}
	return ""
}

// handleBashBackground launches command as a background shell, registers it
// with deps.Shells, and returns the shell ID immediately. Goroutines drain
// stdout/stderr into the shell's capped buffers and record the exit code
// when the process finishes.
func handleBashBackground(deps *Deps, command string) (*mcp.CallToolResult, error) {
	if deps.Shells == nil {
		return ErrorResult("background shells not configured"), nil
	}
	id := NewShellID()
	sh := NewBackgroundShell(id, command)
	deps.Shells.Register(sh)

	// Absolute path — see newBashForegroundCmd for why.
	cmd := exec.Command("/bin/bash", "-c", command)
	cmd.Dir = deps.Workspace.Root()
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutPipe, stderrPipe, errRes := openBashPipes(cmd)
	if errRes != nil {
		deps.Shells.Remove(id)
		return errRes, nil
	}

	if err := cmd.Start(); err != nil {
		deps.Shells.Remove(id)
		return ErrorResult("bash-bg start: %v", err), nil
	}
	// After Setpgid + Start, the child's pid is also its process group id.
	sh.SetPgid(cmd.Process.Pid)

	go drainPipe(stdoutPipe, sh.AppendStdout)
	go drainPipe(stderrPipe, sh.AppendStderr)
	go func() { sh.SetExit(waitExitCode(cmd)) }()

	return TextResult(fmt.Sprintf("shell_id: %s\nstarted in background: %s\n", id, command)), nil
}

func openBashPipes(cmd *exec.Cmd) (stdout, stderr io.Reader, errRes *mcp.CallToolResult) {
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, ErrorResult("bash-bg stdout: %v", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, ErrorResult("bash-bg stderr: %v", err)
	}
	return stdoutPipe, stderrPipe, nil
}

func drainPipe(r io.Reader, appendFn func([]byte)) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			appendFn(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func waitExitCode(cmd *exec.Cmd) int {
	waitErr := cmd.Wait()
	if waitErr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
