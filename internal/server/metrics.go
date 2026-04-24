package server

import (
	"context"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/metrics"
	"github.com/altairalabs/codegen-sandbox/internal/scrub"
	"github.com/altairalabs/codegen-sandbox/internal/tracing"
	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// metricsMiddleware wraps a handler with latency + status instrumentation.
// Status is derived from the result: IsError → "error", nil → "ok".
// "denied" + "timeout" are out of reach from a generic wrapper (they live
// inside specific handlers like Bash), so those call sites increment the
// specialised counters themselves.
func metricsMiddleware(m *metrics.Metrics, tool string, ws *workspace.Workspace, handler mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		res, err := handler(ctx, req)
		dur := time.Since(start)

		status := deriveStatus(res, err)
		language := detectLanguage(ws)
		m.ToolCall(tool, status, language, dur)
		return res, err
	}
}

// deriveStatus maps a handler return into the closed status enum.
// "denied" and "timeout" are NOT auto-derivable — handlers that want those
// labels must call ToolCall directly. For the generic path we distinguish
// ok / error only.
func deriveStatus(res *mcp.CallToolResult, err error) string {
	if err != nil {
		return "error"
	}
	if res != nil && res.IsError {
		return "error"
	}
	return "ok"
}

// detectLanguage resolves the workspace's project language for labelling.
// The bounded enum is go / node / python / rust / "" (no detector).
func detectLanguage(ws *workspace.Workspace) string {
	if ws == nil {
		return ""
	}
	d := verify.Detect(ws.Root())
	if d == nil {
		return ""
	}
	return d.Language()
}

// observabilityRegistrar composes scrub + metrics + tracing middleware around
// every AddTool call. The layering (innermost → outermost) is:
//
//  1. scrub — redact secrets before the result leaves the sandbox
//  2. metrics — record duration + status of the scrubbed pipeline
//  3. tracing — emit one span per invocation covering the WHOLE pipeline
//
// Tracing is outermost so the span's duration accounts for scrub + metrics
// overhead; in practice both inner layers are microsecond-scale, but the
// invariant is "what the span reports matches what the caller observed."
type observabilityRegistrar struct {
	inner   *mcpserver.MCPServer
	metrics *metrics.Metrics
	tracer  *tracing.Tracer
	ws      *workspace.Workspace
}

// AddTool wires the three-layer middleware chain. See observabilityRegistrar
// for the composition order rationale.
func (r *observabilityRegistrar) AddTool(tool mcp.Tool, handler mcpserver.ToolHandlerFunc) {
	scrubbed := scrubbingHandler(handler, r.metrics)
	instrumented := metricsMiddleware(r.metrics, tool.Name, r.ws, scrubbed)
	traced := tracingMiddleware(r.tracer, tool.Name, r.ws, instrumented)
	r.inner.AddTool(tool, traced)
}

// scrubbingHandler replaces scrubMiddleware with a variant that also reports
// scrub hits and redacted byte counts to the metrics surface. Both paths
// resolve to the same scrub patterns; this one just keeps the stats.
func scrubbingHandler(handler mcpserver.ToolHandlerFunc, m *metrics.Metrics) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := handler(ctx, req)
		if err != nil || res == nil {
			return res, err
		}
		for i, c := range res.Content {
			tc, ok := c.(mcp.TextContent)
			if !ok {
				continue
			}
			scrubbed, stats := scrub.WithStats(tc.Text)
			tc.Text = scrubbed
			res.Content[i] = tc
			for _, s := range stats {
				// One hit per matched occurrence; bytes attributed to the
				// first hit per pattern so the total stays accurate without
				// double-counting.
				m.ScrubHit(s.Pattern, s.BytesRedacted)
				for j := 1; j < s.Hits; j++ {
					m.ScrubHit(s.Pattern, 0)
				}
			}
		}
		return res, nil
	}
}
