// Package tools hosts MCP tool handlers for the codegen sandbox.
package tools

import (
	"fmt"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Deps carries the dependencies a tool handler needs.
type Deps struct {
	// Workspace is the sandbox workspace used for path containment.
	Workspace *workspace.Workspace
	// Tracker records which files have been read in the current session.
	Tracker *workspace.ReadTracker
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

// Registrar is the subset of *mcpserver.MCPServer that Register* functions
// need. Accepting an interface (rather than the concrete type) lets the
// server package wrap handlers with middleware without touching each tool
// registration individually.
type Registrar interface {
	AddTool(tool mcp.Tool, handler mcpserver.ToolHandlerFunc)
}
