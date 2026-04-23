package tools

import (
	"context"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
)

// searchBackendEnvVar names the env var the operator sets to select a
// WebSearch backend. Unset means "not configured"; real backends (brave,
// exa, tavily) are a follow-up plan.
const searchBackendEnvVar = "CODEGEN_SANDBOX_SEARCH_BACKEND"

// RegisterWebSearch registers the WebSearch tool.
func RegisterWebSearch(s Registrar, deps *Deps) {
	tool := mcp.NewTool("WebSearch",
		mcp.WithDescription("Search the web. Requires an operator-configured backend (Brave, Exa, Tavily) via the CODEGEN_SANDBOX_SEARCH_BACKEND env var plus the corresponding API key. If no backend is configured, this tool returns a clear error."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query.")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results to return (backend-specific default when omitted).")),
	)
	s.AddTool(tool, HandleWebSearch(deps))
}

// HandleWebSearch returns the WebSearch handler. For v1, no backends are
// wired; the handler returns a configuration error when CODEGEN_SANDBOX_
// SEARCH_BACKEND is unset. Future plans can add backends that dispatch on
// the env var value.
func HandleWebSearch(_ *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)
		query, _ := args["query"].(string)
		if query == "" {
			return ErrorResult("query is required"), nil
		}

		backend := os.Getenv(searchBackendEnvVar)
		if backend == "" {
			return ErrorResult("WebSearch not configured. Set %s=brave|exa|tavily and the corresponding API key env var.", searchBackendEnvVar), nil
		}
		// A backend value is set but no dispatch is implemented yet.
		return ErrorResult("WebSearch backend %q is not yet implemented in this build. Backend wiring is a follow-up plan.", backend), nil
	}
}
