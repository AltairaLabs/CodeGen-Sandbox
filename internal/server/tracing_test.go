package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/altairalabs/codegen-sandbox/internal/tracing"
)

// TestTracingMiddleware_OKSpanAttributes wires a real SDK TracerProvider
// against an in-process recorder and asserts an ok handler produces one span
// with the canonical attribute set (tool.name, tool.status=ok, duration,
// language) and span-status code Ok.
func TestTracingMiddleware_OKSpanAttributes(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	tr, shutdown := tracing.NewForTest(tp)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	handler := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}
	wrapped := tracingMiddleware(tr, "Read", nil, handler)

	_, err := wrapped(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)

	ended := rec.Ended()
	require.Len(t, ended, 1)
	span := ended[0]
	assert.Equal(t, "tool.Read", span.Name())
	assert.Equal(t, codes.Ok, span.Status().Code)

	attrs := attrMap(span.Attributes())
	assert.Equal(t, "Read", attrs["tool.name"].AsString())
	assert.Equal(t, "ok", attrs["tool.status"].AsString())
	assert.Equal(t, "", attrs["tool.language"].AsString(), "nil workspace → empty language")
	// duration_ms is non-negative; don't assert positive because a fast handler
	// can finish inside a millisecond on a hot path.
	require.NotNil(t, attrs["tool.duration_ms"])
	assert.GreaterOrEqual(t, attrs["tool.duration_ms"].AsInt64(), int64(0))
}

// TestTracingMiddleware_ErrorFromGoError asserts a Go-level error propagates
// into tool.error + status=error + span-status code Error.
func TestTracingMiddleware_ErrorFromGoError(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	tr, shutdown := tracing.NewForTest(tp)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	bang := errors.New("internal boom")
	handler := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, bang
	}
	wrapped := tracingMiddleware(tr, "Edit", nil, handler)

	_, err := wrapped(context.Background(), mcp.CallToolRequest{})
	require.ErrorIs(t, err, bang)

	ended := rec.Ended()
	require.Len(t, ended, 1)
	span := ended[0]
	assert.Equal(t, codes.Error, span.Status().Code)

	attrs := attrMap(span.Attributes())
	assert.Equal(t, "error", attrs["tool.status"].AsString())
	assert.Equal(t, "internal boom", attrs["tool.error"].AsString())
}

// TestTracingMiddleware_ErrorFromToolResult asserts an IsError result (Go err
// is nil, MCP tool returned a structured error) surfaces the Content[0] text
// in tool.error.
func TestTracingMiddleware_ErrorFromToolResult(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	tr, shutdown := tracing.NewForTest(tp)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	handler := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("bad input"), nil
	}
	wrapped := tracingMiddleware(tr, "run_tests", nil, handler)

	_, err := wrapped(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)

	ended := rec.Ended()
	require.Len(t, ended, 1)
	span := ended[0]
	assert.Equal(t, codes.Error, span.Status().Code)
	attrs := attrMap(span.Attributes())
	assert.Equal(t, "error", attrs["tool.status"].AsString())
	assert.Equal(t, "bad input", attrs["tool.error"].AsString())
}

// TestTracingMiddleware_ErrorTruncated confirms very long error strings are
// clipped to maxSpanErrorChars so a single bad handler can't blow up span
// payload size.
func TestTracingMiddleware_ErrorTruncated(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	tr, shutdown := tracing.NewForTest(tp)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	giant := strings.Repeat("x", 2048)
	handler := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError(giant), nil
	}
	wrapped := tracingMiddleware(tr, "Bash", nil, handler)
	_, _ = wrapped(context.Background(), mcp.CallToolRequest{})

	ended := rec.Ended()
	require.Len(t, ended, 1)
	attrs := attrMap(ended[0].Attributes())
	got := attrs["tool.error"].AsString()
	assert.LessOrEqual(t, len(got), maxSpanErrorChars)
	assert.True(t, strings.HasSuffix(got, "…"), "expected truncation suffix")
}

// TestTracingMiddleware_NilTracerIsSafe is the hard nil-safety assertion: a
// nil *tracing.Tracer passed into the middleware must not panic and must
// still invoke the inner handler.
func TestTracingMiddleware_NilTracerIsSafe(t *testing.T) {
	var tr *tracing.Tracer
	called := false
	handler := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	}
	wrapped := tracingMiddleware(tr, "Read", nil, handler)

	res, err := wrapped(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, called)
}

// TestExtractErrorText covers the branches not reached via the middleware:
// non-IsError results, empty Content, and non-text Content entries.
func TestExtractErrorText(t *testing.T) {
	// Non-error result → empty string.
	assert.Empty(t, extractErrorText(mcp.NewToolResultText("fine"), nil))
	// Nil result + nil err → empty string.
	assert.Empty(t, extractErrorText(nil, nil))
	// IsError but empty Content → empty string.
	assert.Empty(t, extractErrorText(&mcp.CallToolResult{IsError: true}, nil))
	// IsError with non-TextContent → empty string.
	res := &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{mcp.ImageContent{Type: "image", MIMEType: "image/png"}},
	}
	assert.Empty(t, extractErrorText(res, nil))
	// Happy path — err wins over result text.
	assert.Equal(t, "boom", extractErrorText(mcp.NewToolResultError("structured"), errors.New("boom")))
}

// TestTruncate covers the edge cases around the ellipsis clipping: a
// non-positive cap returns the input unchanged, a cap smaller than the
// ellipsis byte-length returns a raw byte clip, and the normal case
// produces an ellipsis-suffixed output that stays under the cap.
func TestTruncate(t *testing.T) {
	// n <= 0 → unchanged.
	assert.Equal(t, "hello", truncate("hello", 0))
	assert.Equal(t, "hello", truncate("hello", -3))
	// len(s) <= n → unchanged.
	assert.Equal(t, "hi", truncate("hi", 100))
	// n < len(ellipsis) → raw byte clip (no suffix).
	assert.Equal(t, "he", truncate("hello", 2))
	// Normal case → ellipsis-suffixed, total bytes ≤ n.
	got := truncate(strings.Repeat("x", 100), 10)
	assert.LessOrEqual(t, len(got), 10)
	assert.True(t, strings.HasSuffix(got, "…"))
}

// attrMap flattens a span's attribute slice into a name→value map for
// test-side lookups. Tests don't care about attribute order; they care about
// presence and value.
func attrMap(kvs []attribute.KeyValue) map[string]attribute.Value {
	out := make(map[string]attribute.Value, len(kvs))
	for _, kv := range kvs {
		out[string(kv.Key)] = kv.Value
	}
	return out
}
