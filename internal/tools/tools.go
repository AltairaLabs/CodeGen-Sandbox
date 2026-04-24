// Package tools hosts MCP tool handlers for the codegen sandbox.
package tools

import (
	"fmt"

	"github.com/altairalabs/codegen-sandbox/internal/lsp"
	"github.com/altairalabs/codegen-sandbox/internal/metrics"
	"github.com/altairalabs/codegen-sandbox/internal/metrics/health"
	"github.com/altairalabs/codegen-sandbox/internal/secrets"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Deps carries the dependencies a tool handler needs.
type Deps struct {
	// Workspace is the default sandbox workspace — the sole workspace
	// in single-workspace mode, and the first entry in Workspaces in
	// multi-workspace mode. Kept for call sites (verify, LSP, AST,
	// snapshots, search, render) that predate multi-workspace support
	// and have not been extended to accept a `workspace` argument yet.
	// Polyglot-aware new code should resolve via ResolveWorkspace
	// instead, which respects the agent's optional `workspace` arg.
	Workspace *workspace.Workspace
	// Workspaces is the full multi-workspace set when -workspaces is
	// configured. In single-workspace mode this contains exactly one
	// workspace (== Workspace). ResolveWorkspace is the ergonomic
	// entry point for tool handlers that accept the `workspace` arg.
	// May be nil in tests that only exercise single-workspace paths.
	Workspaces *workspace.Set
	// Tracker records which files have been read in the current session.
	Tracker *workspace.ReadTracker
	// Shells hosts background bash shells for BashOutput and KillShell.
	// May be nil in tests that don't exercise background mode.
	Shells *ShellRegistry
	// TestResults records the most recent run_tests outcome for
	// last_test_failures to retrieve. May be nil in tests that don't
	// exercise that pair.
	TestResults *TestResultStore
	// LSPRegistry hosts one language-server subprocess per
	// (workspace, language) pair. May be nil in tests that don't exercise
	// LSP navigation.
	LSPRegistry *lsp.Registry
	// Secrets resolves operator-configured credentials for the `secret`
	// tool. Nil-safe: handlers return a clear error when unset so the
	// sandbox stays useful in workspaces where no secrets are configured.
	Secrets *secrets.Store
	// Metrics is the Prometheus surface; nil disables instrumentation.
	// Every *metrics.Metrics method is nil-safe so call sites stay clean.
	Metrics *metrics.Metrics
	// Health is the agent-health tracker. Nil disables the per-verify-tool
	// ObserveGreen / ObserveTestResult hooks; every method is nil-safe.
	Health *health.Tracker
	// CoverageIndex is the session-scoped (file,line)→tests inverse
	// lookup populated by run_tests (Go) and queried by tests_covering.
	// Nil-safe via receiver methods: handlers surface a clear "not
	// configured" notice when absent.
	CoverageIndex *CoverageIndex
}

// ErrorResult wraps a user-visible message as an MCP error result.
// Tool handlers should return (ErrorResult(msg), nil) rather than a Go error
// for user-caused failures; Go errors are reserved for transport-level faults.
func ErrorResult(format string, args ...any) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf(format, args...))
}

// TextResult wraps a plain text body as an MCP tool result.
func TextResult(body string) *mcp.CallToolResult {
	return mcp.NewToolResultText(body)
}

// ToolAdder is the subset of *mcpserver.MCPServer that Register* functions
// need. Accepting an interface (rather than the concrete type) lets the
// server package wrap handlers with middleware without touching each tool
// registration individually.
type ToolAdder interface {
	AddTool(tool mcp.Tool, handler mcpserver.ToolHandlerFunc)
}
