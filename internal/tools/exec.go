package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

const verifyOutputCapBytes = 500 * 1024

// verifyTimeoutExitCode follows the timeout(1) convention (124) so agents
// can distinguish a timeout from a normal non-zero exit. Consistent with
// bashTimeoutExitCode in bash.go.
const verifyTimeoutExitCode = 124

// execResult is the output of a single verify subprocess call.
type execResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	TimedOut bool
}

// runVerifyCmd runs cmd in cwd with a timeout. Captures stdout and stderr
// separately (for structured parsing). Kills the whole process group on
// timeout. Caps each stream independently at verifyOutputCapBytes.
//
// Returns a non-nil error only for transport/spawn faults (e.g. binary not
// found). A non-zero exit code is a successful invocation with a negative
// result — the caller decides how to surface it.
func runVerifyCmd(ctx context.Context, cmd []string, cwd string, timeoutSec int) (execResult, error) {
	if len(cmd) == 0 {
		return execResult{}, errors.New("empty command")
	}

	if _, err := exec.LookPath(cmd[0]); err != nil {
		return execResult{}, fmt.Errorf("%s: not found on PATH", cmd[0])
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	c := exec.CommandContext(execCtx, cmd[0], cmd[1:]...)
	c.Dir = cwd
	c.Stdin = nil

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	// Same process-group-kill pattern as the Bash tool: Setpgid so
	// descendants (test-binary subprocesses, etc.) land in a fresh group,
	// then kill that group on ctx cancel.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	c.WaitDelay = 2 * time.Second

	runErr := c.Run()

	res := execResult{
		Stdout:   truncateOutput(stdout.Bytes(), verifyOutputCapBytes),
		Stderr:   truncateOutput(stderr.Bytes(), verifyOutputCapBytes),
		TimedOut: errors.Is(execCtx.Err(), context.DeadlineExceeded),
	}
	switch {
	case res.TimedOut:
		res.ExitCode = verifyTimeoutExitCode
	case runErr == nil:
		res.ExitCode = 0
	default:
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		} else {
			return res, fmt.Errorf("%s: %w", cmd[0], runErr)
		}
	}
	return res, nil
}
