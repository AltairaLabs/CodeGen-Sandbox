package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/web/search"
	"github.com/mark3labs/mcp-go/mcp"
)

// searchBackendEnvVar names the env var the operator sets to select a
// WebSearch backend. Unset means "not configured"; supported backend names
// are brave, exa, tavily — each requires its own API key env var.
const searchBackendEnvVar = search.BackendEnvVar

// RegisterWebSearch registers the WebSearch tool.
func RegisterWebSearch(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("WebSearch",
		mcp.WithDescription("Search the web. Requires an operator-configured backend (Brave, Exa, Tavily) via the CODEGEN_SANDBOX_SEARCH_BACKEND env var plus the corresponding API key. If no backend is configured, this tool returns a clear error."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query.")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results to return (backend default: 10).")),
	)
	s.AddTool(tool, HandleWebSearch(deps))
}

// HandleWebSearch returns the WebSearch handler backed by search.NewFromEnv.
// Prefer this entry point in production.
func HandleWebSearch(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return handleWebSearchWith(deps, search.NewFromEnv)
}

// handleWebSearchWith is HandleWebSearch with the backend-factory injected
// so tests can swap it for a fake without relying on real API keys.
func handleWebSearchWith(_ *Deps, factory func() (search.Backend, error)) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)
		query, _ := args["query"].(string)
		if query == "" {
			return ErrorResult("query is required"), nil
		}

		backend, err := factory()
		if err != nil {
			return ErrorResult("WebSearch misconfigured: %v", err), nil
		}
		if backend == nil {
			return ErrorResult("WebSearch not configured. Set %s=brave|exa|tavily and the corresponding API key env var (BRAVE_API_KEY / EXA_API_KEY / TAVILY_API_KEY).", searchBackendEnvVar), nil
		}

		limit := 10
		if v, ok := args["limit"].(float64); ok && int(v) > 0 {
			limit = int(v)
		}

		results, err := backend.Search(ctx, query, limit)
		if err != nil {
			return ErrorResult("WebSearch %s: %v", backend.Name(), err), nil
		}

		return TextResult(formatSearchResults(query, results)), nil
	}
}

// formatSearchResults produces a compact agent-readable block. The title
// sits on its own line, the URL on its own (easy to extract), and the
// snippet is flattened to a single line so downstream scrapers can parse
// results with one-line-per-field regexes.
func formatSearchResults(query string, results []search.Result) string {
	if len(results) == 0 {
		return fmt.Sprintf("no results for %q", query)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d results for %q:\n\n", len(results), query)
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, strings.TrimSpace(r.Title))
		fmt.Fprintf(&sb, "   %s\n", strings.TrimSpace(r.URL))
		if snip := flattenSnippet(r.Snippet); snip != "" {
			fmt.Fprintf(&sb, "   %s\n", snip)
		}
		if i < len(results)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// flattenSnippet trims whitespace and collapses embedded newlines/tabs into
// single spaces so every snippet occupies exactly one line.
func flattenSnippet(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ")
	s = r.Replace(s)
	// Collapse runs of spaces.
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}
