// Package tracing owns the sandbox's OpenTelemetry TracerProvider and a thin,
// nil-safe wrapper around otel.Tracer so the rest of the server can emit spans
// without branching on provider presence.
//
// Nil-safety: a *Tracer value of nil is a valid no-op. The sandbox runs
// unconfigured by default (no OTLP endpoint) and every call site that reaches
// for Start can pass nil without gating every span-open behind a branch.
//
// Scope: v1 only covers the MCP tool plane. Span attributes are drawn from
// bounded enums (tool name, status, language) plus a bounded-ish error string.
// Traceparent propagation from the MCP request is intentionally out of scope —
// mcp-go does not surface traceparent headers today; every span is therefore
// a root span at this layer. See the PR body for the follow-up.
package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// serviceName is the resource-level identifier every sandbox span carries.
// Kept in one place so both the live exporter and the test helper agree.
const serviceName = "codegen-sandbox"

// Tracer is a thin wrapper around an otel.Tracer that is safe to call with a
// nil receiver. Call sites don't need to check for a configured provider.
type Tracer struct {
	inner trace.Tracer
}

// ShutdownFunc drains any pending spans. Returned by New when a provider was
// actually constructed; nil otherwise.
type ShutdownFunc func(context.Context) error

// New constructs a Tracer backed by an OTLP-HTTP exporter targeting endpoint.
// endpoint is the standard OTEL_EXPORTER_OTLP_ENDPOINT value (e.g.
// "http://otel-collector:4318"). An empty endpoint disables tracing and
// returns (nil, nil, nil) — the nil receiver path on Tracer is the no-op.
//
// The returned ShutdownFunc MUST be invoked before the process exits so any
// buffered spans leave the batch-processor.
func New(ctx context.Context, endpoint string) (*Tracer, ShutdownFunc, error) {
	if endpoint == "" {
		return nil, nil, nil
	}

	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, nil, fmt.Errorf("otlp exporter: %w", err)
	}

	// A schemaless resource sidesteps the SDK-default schema URL pinning
	// (which moves between otel SDK releases) and keeps us free of a direct
	// semconv version dependency. We set only service.name explicitly — the
	// attribute key is stable across semconv versions and is what collectors
	// route on.
	res := resource.NewSchemaless(attribute.String("service.name", serviceName))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return &Tracer{inner: tp.Tracer(serviceName)}, tp.Shutdown, nil
}

// NewForTest constructs a Tracer wired to a caller-supplied TracerProvider —
// typically one built with tracetest.NewSpanRecorder and a SyncSpanProcessor.
// Returns the TracerProvider's Shutdown fn so tests can flush deterministically.
func NewForTest(tp *sdktrace.TracerProvider) (*Tracer, ShutdownFunc) {
	if tp == nil {
		return nil, nil
	}
	return &Tracer{inner: tp.Tracer(serviceName)}, tp.Shutdown
}

// Start opens a span with the canonical tool span name ("tool.<name>") and
// returns the derived context + the span for attribute + status handling.
// A nil receiver returns (ctx, noop-span) so callers can treat the disabled
// path identically to the enabled path.
func (t *Tracer) Start(ctx context.Context, toolName string) (context.Context, trace.Span) {
	if t == nil || t.inner == nil {
		return noop.NewTracerProvider().Tracer(serviceName).Start(ctx, "tool."+toolName)
	}
	return t.inner.Start(ctx, "tool."+toolName, trace.WithAttributes(
		attribute.String("tool.name", toolName),
	))
}
