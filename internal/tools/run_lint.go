package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultRunLintTimeoutSec = 120
	maxRunLintTimeoutSec     = 600
)

// RegisterRunLint registers the run_lint tool on the given MCP server.
func RegisterRunLint(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("run_lint",
		mcp.WithDescription("Run the project's linter. Returns structured findings as 'file:line:col:rule: message' followed by 'N findings'. Project type is detected from the workspace root (currently: Go via go.mod, uses golangci-lint)."),
		mcp.WithNumber("timeout", mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.", defaultRunLintTimeoutSec, maxRunLintTimeoutSec))),
	)
	s.AddTool(tool, HandleRunLint(deps))
}

// HandleRunLint returns the run_lint tool handler.
func HandleRunLint(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		det := verify.Detect(deps.Workspace.Root())
		if det == nil {
			return ErrorResult("no supported project detected in workspace root"), nil
		}

		args, _ := req.Params.Arguments.(map[string]any)
		timeoutSec := parseLintTimeout(args)

		findings, err := verify.Lint(ctx, deps.Workspace.Root(), timeoutSec)
		if errRes := lintErrorResult(det, findings, err); errRes != nil {
			return errRes, nil
		}

		// Happy path OR partial-findings-with-error: emit the findings we
		// have. When a timeout or exit ≥ 2 left the linter mid-run, findings
		// may still be informative — include them plus a trailing marker
		// so the caller knows the run was incomplete.
		body := formatFindings(findings)
		if err != nil {
			body += fmt.Sprintf("(lint incomplete: %v)\n", err)
		}
		// Agent-health: zero findings + no error is the green path that
		// stamps the last-green timestamp.
		if err == nil && len(findings) == 0 {
			deps.Health.ObserveGreen()
		}
		return TextResult(body), nil
	}
}

func parseLintTimeout(args map[string]any) int {
	timeoutSec := defaultRunLintTimeoutSec
	v, ok := args["timeout"].(float64)
	if !ok || int(v) <= 0 {
		return timeoutSec
	}
	timeoutSec = int(v)
	if timeoutSec > maxRunLintTimeoutSec {
		timeoutSec = maxRunLintTimeoutSec
	}
	return timeoutSec
}

func lintErrorResult(det verify.Detector, findings []verify.LintFinding, err error) *mcp.CallToolResult {
	if errors.Is(err, verify.ErrLinterMissing) {
		binary := "<unknown>"
		if cmd := det.LintCmd(); len(cmd) > 0 {
			binary = cmd[0]
		}
		return ErrorResult("linter not installed: %s", binary)
	}
	if err != nil && len(findings) == 0 {
		return ErrorResult("run_lint: %v", err)
	}
	return nil
}

// formatFindings renders a []LintFinding as agent-facing text with one
// finding per line plus a trailing "N findings" summary.
func formatFindings(findings []verify.LintFinding) string {
	var sb strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&sb, "%s:%d:%d:%s: %s\n", f.File, f.Line, f.Column, f.Rule, f.Message)
	}
	fmt.Fprintf(&sb, "%d findings\n", len(findings))
	return sb.String()
}
