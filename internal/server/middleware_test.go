package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScrubMiddleware_RedactsTextContent(t *testing.T) {
	inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("leaked AKIAIOSFODNN7EXAMPLE sorry"), nil
	}
	wrapped := scrubMiddleware(inner)

	res, err := wrapped(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Len(t, res.Content, 1)
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.NotContains(t, tc.Text, "AKIAIOSFODNN7EXAMPLE")
	assert.Contains(t, tc.Text, "[REDACTED:aws-access-key]")
}

func TestScrubMiddleware_PreservesErrorResultShape(t *testing.T) {
	inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("failed with token ghp_" +
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), nil
	}
	wrapped := scrubMiddleware(inner)

	res, err := wrapped(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError, "error flag should survive scrubbing")
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "[REDACTED:github-pat]")
}

func TestScrubMiddleware_PassesThroughGoError(t *testing.T) {
	wantErr := assert.AnError
	inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, wantErr
	}
	wrapped := scrubMiddleware(inner)

	res, err := wrapped(context.Background(), mcp.CallToolRequest{})
	assert.ErrorIs(t, err, wantErr)
	assert.Nil(t, res)
}

func TestScrubMiddleware_LeavesCleanTextAlone(t *testing.T) {
	inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("nothing secret here"), nil
	}
	wrapped := scrubMiddleware(inner)

	res, _ := wrapped(context.Background(), mcp.CallToolRequest{})
	tc := res.Content[0].(mcp.TextContent)
	assert.Equal(t, "nothing secret here", tc.Text)
}

// Defense in depth: if a secret value happens to match a known scrub shape
// (e.g. operator mounts a Brave API key literal), the `secret` tool's
// response is still redacted on the way out of the MCP server. The caller
// is supposed to feed the return into the downstream API directly, not log
// it — so redaction here is a belt on the existing braces.
func TestScrubMiddleware_RedactsSecretToolOutput(t *testing.T) {
	// Shape matches the github-pat pattern registered in internal/scrub.
	leakedShape := "ghp_" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(leakedShape), nil
	}
	wrapped := scrubMiddleware(inner)

	res, err := wrapped(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)
	tc := res.Content[0].(mcp.TextContent)
	assert.NotContains(t, tc.Text, leakedShape)
	assert.Contains(t, tc.Text, "[REDACTED:github-pat]")
}
