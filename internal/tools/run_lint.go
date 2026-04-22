package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const (
	defaultRunLintTimeoutSec = 120
	maxRunLintTimeoutSec     = 600
)

// RegisterRunLint registers the run_lint tool on the given MCP server.
func RegisterRunLint(s *mcpserver.MCPServer, deps *Deps) {
	tool := mcp.NewTool("run_lint",
		mcp.WithDescription("Run the project's linter. Returns structured findings as 'file:line:col:rule: message' followed by 'N findings'. Project type is detected from the workspace root (currently: Go via go.mod, uses golangci-lint)."),
		mcp.WithNumber("timeout", mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.", defaultRunLintTimeoutSec, maxRunLintTimeoutSec))),
	)
	s.AddTool(tool, HandleRunLint(deps))
}

// HandleRunLint returns the run_lint tool handler.
func HandleRunLint(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if verify.Detect(deps.Workspace.Root()) == nil {
			return ErrorResult("no supported project detected in workspace root"), nil
		}

		args, _ := req.Params.Arguments.(map[string]any)
		timeoutSec := defaultRunLintTimeoutSec
		if v, ok := args["timeout"].(float64); ok && int(v) > 0 {
			timeoutSec = int(v)
			if timeoutSec > maxRunLintTimeoutSec {
				timeoutSec = maxRunLintTimeoutSec
			}
		}

		findings, err := verify.Lint(ctx, deps.Workspace.Root(), timeoutSec)
		if err != nil {
			if errors.Is(err, verify.ErrLinterMissing) {
				return ErrorResult("linter not installed on PATH"), nil
			}
			return ErrorResult("run_lint: %v", err), nil
		}

		return TextResult(formatFindings(findings)), nil
	}
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
