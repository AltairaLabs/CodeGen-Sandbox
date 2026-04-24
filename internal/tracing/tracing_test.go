package tracing_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/altairalabs/codegen-sandbox/internal/tracing"
)

// TestNew_EmptyEndpointIsDisabled confirms the "no OTEL configured" path
// returns nil Tracer + nil shutdown + nil error so callers can propagate the
// value unchanged.
func TestNew_EmptyEndpointIsDisabled(t *testing.T) {
	tr, shutdown, err := tracing.New(context.Background(), "")
	require.NoError(t, err)
	require.Nil(t, tr)
	require.Nil(t, shutdown)
}

// TestNilTracer_StartIsNoop asserts the nil-receiver contract: Start must
// return a valid context + a non-recording span without panicking.
func TestNilTracer_StartIsNoop(t *testing.T) {
	var tr *tracing.Tracer
	ctx, span := tr.Start(context.Background(), "Read")
	require.NotNil(t, ctx)
	require.NotNil(t, span)
	require.False(t, span.IsRecording(), "nil tracer must return a non-recording span")
	// End must be safe even on the no-op path.
	span.End()
}

// TestNewForTest_RecordsSpan wires a real SDK TracerProvider against an
// in-process recorder and confirms the Tracer emits a span with the canonical
// name + tool.name attribute.
func TestNewForTest_RecordsSpan(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	tr, shutdown := tracing.NewForTest(tp)
	require.NotNil(t, tr)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	_, span := tr.Start(context.Background(), "Edit")
	span.End()

	ended := rec.Ended()
	require.Len(t, ended, 1)
	assert.Equal(t, "tool.Edit", ended[0].Name())

	// tool.name attribute must appear on the emitted span.
	var gotName string
	for _, kv := range ended[0].Attributes() {
		if string(kv.Key) == "tool.name" {
			gotName = kv.Value.AsString()
		}
	}
	assert.Equal(t, "Edit", gotName)
}

// TestNewForTest_NilProvider is the symmetric nil-safety check on the test
// helper: passing nil returns (nil, nil) rather than panicking.
func TestNewForTest_NilProvider(t *testing.T) {
	tr, shutdown := tracing.NewForTest(nil)
	require.Nil(t, tr)
	require.Nil(t, shutdown)
}

// TestNew_ValidEndpointBuildsTracer drives the happy path of New: given a
// reachable-looking endpoint, New must construct an exporter + provider,
// return a non-nil Tracer + ShutdownFunc, and Shutdown must run cleanly.
// We point at a stub HTTP server so the batcher's eventual flush doesn't
// block or log scary errors during teardown.
func TestNew_ValidEndpointBuildsTracer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tr, shutdown, err := tracing.New(ctx, srv.URL)
	require.NoError(t, err)
	require.NotNil(t, tr)
	require.NotNil(t, shutdown)

	// Start a span to confirm the built Tracer is functional.
	_, span := tr.Start(ctx, "Probe")
	span.End()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	require.NoError(t, shutdown(shutdownCtx))
}
