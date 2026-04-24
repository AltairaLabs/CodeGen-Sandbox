package server

import (
	"context"

	"github.com/altairalabs/codegen-sandbox/internal/scrub"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// scrubMiddleware wraps a handler so every TextContent in its CallToolResult
// passes through the secret scrubber before it leaves the sandbox. Non-text
// content (images, resources) is unchanged. Go-level errors propagate
// unmodified — scrubbing only applies to successful tool results.
func scrubMiddleware(handler mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := handler(ctx, req)
		if err != nil || res == nil {
			return res, err
		}
		for i, c := range res.Content {
			if tc, ok := c.(mcp.TextContent); ok {
				tc.Text = scrub.Scrub(tc.Text)
				res.Content[i] = tc
			}
		}
		return res, nil
	}
}
