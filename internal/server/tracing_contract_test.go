package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/altairalabs/codegen-sandbox/internal/tracing"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
)

// TestTracingContract_ToolsCallEmitsExactlyOneSpan exercises the full
// observability stack end-to-end: a server constructed via NewWithConfig
// with a SpanRecorder-backed Tracer, an MCP `tools/call` driven through
// HandleMessage (the same entrypoint the SSE transport invokes), and a
// span-recorder assertion that exactly one span lands with the canonical
// attribute set documented in operations/tracing.md.
//
// This is the guard the existing middleware-level tests don't provide:
// they wrap the middleware function directly and would still pass if a
// future refactor of the registrar dropped the middleware composition. The
// same shape covers tools/call -> registrar -> middleware -> handler ->
// span-end + span-export, so registrar wiring drift fails this test
// rather than slipping into production.
func TestTracingContract_ToolsCallEmitsExactlyOneSpan(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	tr, shutdown := tracing.NewForTest(tp)
	require.NotNil(t, tr)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	wsRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wsRoot, "hello.txt"), []byte("hi\n"), 0o600))
	ws, err := workspace.New(wsRoot)
	require.NoError(t, err)

	// ReadOnly: true keeps the registered surface minimal — Read alone is
	// enough to exercise the middleware stack and the SpanRecorder cleanup
	// stays small.
	srv, err := NewWithConfig(ws, nil, Config{Tracer: tr, ReadOnly: true})
	require.NoError(t, err)

	// tools/call for Read on the seeded file. The MCP server dispatches
	// through the registered handler — which is wrapped by the
	// observabilityRegistrar's tracing + metrics + scrub middleware — so
	// the span we assert on is the one emitted by tracingMiddleware.
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "Read",
			"arguments": map[string]any{
				"file_path": "hello.txt",
			},
		},
	}
	body, err := json.Marshal(req)
	require.NoError(t, err)

	resp := srv.MCP().HandleMessage(context.Background(), body)
	require.NotNil(t, resp)

	// Sanity: the call itself succeeded — a transport-level failure here
	// would still leave a span recorded but with status=error, which would
	// hide the wire-success branch of the contract under a misleading pass.
	jsonResp, ok := resp.(mcp.JSONRPCResponse)
	require.True(t, ok, "expected JSONRPCResponse, got %T", resp)
	result, ok := jsonResp.Result.(*mcp.CallToolResult)
	require.True(t, ok, "expected *CallToolResult, got %T", jsonResp.Result)
	require.False(t, result.IsError, "Read returned IsError=true")

	ended := rec.Ended()
	require.Len(t, ended, 1, "expected exactly one span per tools/call")
	span := ended[0]
	assert.Equal(t, "tool.Read", span.Name())
	assert.Equal(t, codes.Ok, span.Status().Code)

	attrs := attrMap(span.Attributes())
	assert.Equal(t, "Read", attrs["tool.name"].AsString())
	assert.Equal(t, "ok", attrs["tool.status"].AsString())
	require.Contains(t, attrs, "tool.duration_ms")
	assert.GreaterOrEqual(t, attrs["tool.duration_ms"].AsInt64(), int64(0))
	require.Contains(t, attrs, "tool.language", "tool.language must be present even when empty")
}

// TestTracingContract_ErrorOnHandlerSurfacesInSpan exercises the same wiring
// for the failure branch: a tools/call against an unknown file_path returns
// an MCP IsError result, and the corresponding span must record status=error
// with span-status code Error and a tool.error attribute. Together with the
// happy-path test above, this nails down both branches of deriveStatus + the
// extractErrorText fallback through the full registrar.
func TestTracingContract_ErrorOnHandlerSurfacesInSpan(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	tr, shutdown := tracing.NewForTest(tp)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	ws, err := workspace.New(t.TempDir())
	require.NoError(t, err)
	srv, err := NewWithConfig(ws, nil, Config{Tracer: tr, ReadOnly: true})
	require.NoError(t, err)

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "Read",
			"arguments": map[string]any{"file_path": "does-not-exist.txt"},
		},
	}
	body, err := json.Marshal(req)
	require.NoError(t, err)

	resp := srv.MCP().HandleMessage(context.Background(), body)
	require.NotNil(t, resp)
	jsonResp, ok := resp.(mcp.JSONRPCResponse)
	require.True(t, ok)
	result, ok := jsonResp.Result.(*mcp.CallToolResult)
	require.True(t, ok)
	require.True(t, result.IsError, "Read against missing file should return IsError=true")

	ended := rec.Ended()
	require.Len(t, ended, 1)
	span := ended[0]
	assert.Equal(t, "tool.Read", span.Name())
	assert.Equal(t, codes.Error, span.Status().Code)

	attrs := attrMap(span.Attributes())
	assert.Equal(t, "error", attrs["tool.status"].AsString())
	require.Contains(t, attrs, "tool.error", "error path must populate tool.error attribute")
	assert.NotEmpty(t, attrs["tool.error"].AsString())
}
