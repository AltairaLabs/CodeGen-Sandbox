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
