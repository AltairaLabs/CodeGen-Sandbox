package server

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/altairalabs/codegen-sandbox/internal/tracing"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// maxSpanErrorChars bounds the tool.error attribute so a malicious or
// accidentally-huge error string can't blow up per-span payload size.
const maxSpanErrorChars = 512

// tracingMiddleware wraps a handler so every invocation is recorded as one
// OpenTelemetry span named "tool.<name>". Attributes follow the v1 attribute
// set documented in docs/operations/tracing.md: tool.name, tool.status,
// tool.duration_ms, tool.language, and (on error) tool.error. A nil Tracer
// produces a no-op span via the tracing package's nil-receiver contract; the
// middleware always wraps every handler regardless of tracer presence so the
// composition shape is deterministic.
func tracingMiddleware(t *tracing.Tracer, tool string, ws *workspace.Workspace, handler mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		ctx, span := t.Start(ctx, tool)
		defer span.End()

		res, err := handler(ctx, req)
		dur := time.Since(start)

		status := deriveStatus(res, err)
		language := detectLanguage(ws)

		span.SetAttributes(
			attribute.String("tool.name", tool),
			attribute.String("tool.status", status),
			attribute.Int64("tool.duration_ms", dur.Milliseconds()),
			attribute.String("tool.language", language),
		)

		if status == "error" {
			errText := extractErrorText(res, err)
			if errText != "" {
				span.SetAttributes(attribute.String("tool.error", truncate(errText, maxSpanErrorChars)))
			}
			span.SetStatus(codes.Error, shortErrorDescription(errText))
		} else {
			span.SetStatus(codes.Ok, "")
		}
		return res, err
	}
}

// extractErrorText pulls a human-readable error string from either the Go
// error or the MCP result's first content block. The MCP surface represents
// tool-side failures as CallToolResult{IsError: true} with text in Content[0],
// so we fall back to that when err == nil.
func extractErrorText(res *mcp.CallToolResult, err error) string {
	if err != nil {
		return err.Error()
	}
	if res == nil || !res.IsError || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// shortErrorDescription produces a terse OTel span-status description. The
// span-status description is meant to be human-readable and bounded; we reuse
// the truncated error text rather than inventing a second enum.
func shortErrorDescription(errText string) string {
	const maxDesc = 120
	return truncate(errText, maxDesc)
}

// truncate clips s to at most n bytes and suffixes the clipped value with "…"
// so dashboards still signal that the string was cut. The ellipsis is three
// bytes in UTF-8; the returned string's total byte length never exceeds n.
func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	const ellipsis = "…"
	if n < len(ellipsis) {
		return s[:n]
	}
	return s[:n-len(ellipsis)] + ellipsis
}
