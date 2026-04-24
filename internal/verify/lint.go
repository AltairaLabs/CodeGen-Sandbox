package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrLinterMissing is returned by Lint when the detected project's lint
// binary cannot be found on PATH.
var ErrLinterMissing = errors.New("linter binary not found on PATH")

// LintFinding is a single structured diagnostic emitted by the linter.
type LintFinding struct {
	File    string
	Line    int
	Column  int
	Rule    string
	Message string
}

// lintLineRe matches golangci-lint v2's default output format:
//
//	path/to/file.go:LINE:COL: free-form message (rulename)
//
// The rule group is the LAST parenthesized token on the line, so a message
// that itself contains parentheses is preserved intact.
var lintLineRe = regexp.MustCompile(
	`^(?P<file>[^:]+):(?P<line>\d+):(?P<col>\d+):\s+(?P<msg>.+?)\s+\((?P<rule>[A-Za-z][A-Za-z0-9_\-]*)\)\s*$`,
)

// ParseLint extracts structured findings from linter output. Lines that
// don't match the expected format (context lines, summary block, banners)
// are silently ignored.
func ParseLint(text string) []LintFinding {
	var findings []LintFinding
	for _, line := range strings.Split(text, "\n") {
		m := lintLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lineNo, _ := strconv.Atoi(m[lintLineRe.SubexpIndex("line")])
		col, _ := strconv.Atoi(m[lintLineRe.SubexpIndex("col")])
		findings = append(findings, LintFinding{
			File:    m[lintLineRe.SubexpIndex("file")],
			Line:    lineNo,
			Column:  col,
			Rule:    m[lintLineRe.SubexpIndex("rule")],
			Message: m[lintLineRe.SubexpIndex("msg")],
		})
	}
	return findings
}

// Lint runs the project's linter using whatever Detect() returns first.
// Single-language-workspace convenience wrapper around LintWith — for
// polyglot-aware callers (which want to dispatch on a `language` arg) use
// LintWith and pass the detector chosen by your dispatch.
func Lint(ctx context.Context, root string, timeoutSec int) ([]LintFinding, error) {
	det := Detect(root)
	if det == nil {
		return nil, nil
	}
	return LintWith(ctx, det, root, timeoutSec)
}

// LintWith runs the given detector's linter and returns parsed findings.
// Returns (nil, nil) when the detector exposes no LintCmd. Returns
// (nil, ErrLinterMissing) when the linter binary isn't installed. Returns
// (findings, nil) on exit 0 or exit 1 (the latter is the linter's
// "findings exist" convention). Returns (findings, err) on timeout or
// spawn error — callers treat this as best-effort.
func LintWith(ctx context.Context, det Detector, root string, timeoutSec int) ([]LintFinding, error) {
	if det == nil {
		return nil, nil
	}
	cmd := det.LintCmd()
	if len(cmd) == 0 {
		return nil, nil
	}
	if _, err := exec.LookPath(cmd[0]); err != nil {
		return nil, ErrLinterMissing
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	c := exec.CommandContext(execCtx, cmd[0], cmd[1:]...)
	c.Dir = root
	c.Stdin = nil
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	c.WaitDelay = 2 * time.Second

	runErr := c.Run()

	// Parse whatever we got regardless of exit code — most linters emit
	// findings on stdout (golangci-lint, ruff, eslint) while some emit on
	// stderr (clippy). Each Detector.ParseLint picks the right stream.
	findings := det.ParseLint(stdout.String(), stderr.String())

	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		return findings, fmt.Errorf("lint: timed out after %ds", timeoutSec)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return findings, fmt.Errorf("lint: %w", runErr)
		}
		// Exit 1 == "findings exist" (golangci-lint, ruff, eslint default).
		// Exit >= 2 is a genuine linter failure (bad config, crashed, etc.).
		if exitErr.ExitCode() >= 2 {
			return findings, fmt.Errorf("lint: %w (stderr: %s)", runErr, stderr.String())
		}
	}
	return findings, nil
}
