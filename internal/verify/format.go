package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// ErrFormatterMissing is returned by FormatCheck when the detected project
// has a non-nil FormatCheckCmd but the first element of that argv isn't on
// PATH. Callers surface a single-line advisory message ("post-edit format:
// <binary> not found on PATH") rather than failing the originating Edit.
var ErrFormatterMissing = errors.New("formatter binary not found on PATH")

// FormatResult captures the outcome of a single-file format check. Output
// is the formatter's combined stdout+stderr (bounded by the caller before
// display), OK is true iff the formatter exited 0 (i.e. the file is
// already formatted). Binary is the resolved first argv token — useful for
// the "<binary> not found on PATH" advisory line.
type FormatResult struct {
	Binary string
	OK     bool
	Output string
}

// FormatCheck runs the detected project's per-file format check and
// returns the result. Returns:
//
//   - (nil, nil) when the workspace has no detector OR the detector's
//     FormatCheckCmd returns nil — both mean "no formatter wired for this
//     language", which the caller surfaces as "no format section".
//   - (&FormatResult{Binary: <name>}, ErrFormatterMissing) when the
//     detector advertises a formatter whose binary isn't on PATH. The
//     caller surfaces a one-line advisory; Edit itself does not fail.
//   - (result, nil) when the formatter runs. result.OK reflects exit 0;
//     result.Output is the combined stdout+stderr.
//   - (result, err) on timeout or spawn failure — caller treats as
//     best-effort.
//
// The relFile argument is the workspace-relative path to the edited file;
// it is passed as the last element of the detector's argv.
func FormatCheck(ctx context.Context, root, relFile string, timeoutSec int) (*FormatResult, error) {
	det := Detect(root)
	if det == nil {
		return nil, nil
	}
	argv := det.FormatCheckCmd(relFile)
	if len(argv) == 0 {
		return nil, nil
	}
	binary := argv[0]
	if _, err := exec.LookPath(binary); err != nil {
		return &FormatResult{Binary: binary}, ErrFormatterMissing
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	c := exec.CommandContext(execCtx, argv[0], argv[1:]...)
	c.Dir = root
	c.Stdin = nil
	var combined bytes.Buffer
	c.Stdout = &combined
	c.Stderr = &combined
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	c.WaitDelay = 2 * time.Second

	runErr := c.Run()

	result := &FormatResult{
		Binary: binary,
		Output: combined.String(),
		OK:     runErr == nil,
	}

	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		return result, fmt.Errorf("format: timed out after %ds", timeoutSec)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return result, fmt.Errorf("format: %w", runErr)
		}
		// Any non-zero exit is "file not formatted" or a formatter-level
		// error. Either way the caller shows the output — no separate
		// error path needed.
	}
	return result, nil
}
