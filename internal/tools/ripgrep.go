package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// ErrRipgrepMissing is returned when the `rg` binary cannot be found on PATH.
var ErrRipgrepMissing = errors.New("ripgrep (rg) not found on PATH")

// runRipgrep invokes `rg` with the given args and cwd. Exit code 0 returns
// stdout. Exit code 1 (rg's "no matches" signal) returns empty stdout and nil.
// Exit codes >= 2 return an error wrapping stderr.
func runRipgrep(ctx context.Context, args []string, cwd string) ([]byte, error) {
	path, err := exec.LookPath("rg")
	if err != nil {
		return nil, ErrRipgrepMissing
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil, nil
	}
	return stdout.Bytes(), fmt.Errorf("rg: %w: %s", err, stderr.String())
}

// LookupRipgrep returns nil if `rg` is on PATH, or ErrRipgrepMissing otherwise.
// Intended for use by black-box tests that want to skip when rg is unavailable.
func LookupRipgrep() error {
	if _, err := exec.LookPath("rg"); err != nil {
		return ErrRipgrepMissing
	}
	return nil
}
