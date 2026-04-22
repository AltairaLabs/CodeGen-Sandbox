// Package server wires the codegen-sandbox MCP server and its HTTP+SSE transport.
package server

import (
	"net/http"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Server is the codegen sandbox MCP server.
type Server struct {
	mcp     *mcpserver.MCPServer
	sse     *mcpserver.SSEServer
	ws      *workspace.Workspace
	tracker *workspace.ReadTracker
}

// New constructs a Server bound to the given workspace.
func New(ws *workspace.Workspace) (*Server, error) {
	mcpSrv := mcpserver.NewMCPServer(
		"codegen-sandbox",
		"0.1.0",
		mcpserver.WithToolCapabilities(true),
	)
	s := &Server{
		mcp:     mcpSrv,
		ws:      ws,
		tracker: workspace.NewReadTracker(),
	}
	s.sse = mcpserver.NewSSEServer(mcpSrv)
	tools.RegisterRead(s.mcp, &tools.Deps{Workspace: s.ws, Tracker: s.tracker})
	tools.RegisterWrite(s.mcp, &tools.Deps{Workspace: s.ws, Tracker: s.tracker})
	return s, nil
}

// Handler returns the SSE http.Handler for this server.
func (s *Server) Handler() http.Handler { return s.sse }

// MCP exposes the underlying MCP server for tool registration.
func (s *Server) MCP() *mcpserver.MCPServer { return s.mcp }

// Workspace exposes the bound workspace.
func (s *Server) Workspace() *workspace.Workspace { return s.ws }

// Tracker exposes the bound read tracker.
func (s *Server) Tracker() *workspace.ReadTracker { return s.tracker }
