package api

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/metrics"
)

// withMetrics wraps h so every completed request is recorded on the metrics
// surface with (route, status-class) + duration. route is the fixed mux
// pattern — NOT r.URL.Path — so the label set stays bounded.
//
// WebSocket upgrades report status 101. A hijacked ResponseWriter (the
// upgrade paths in /api/exec and /api/port-forward) won't ever surface a
// status code through the wrapper, so we snapshot and record manually when
// WriteHeader is called; if nothing's called we fall back to 200.
func withMetrics(m *metrics.Metrics, route string, h http.Handler) http.Handler {
	if m == nil {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r)
		m.APIHTTPRequest(route, metrics.BucketHTTPStatus(rec.status), time.Since(start))
	})
}

// statusRecorder intercepts WriteHeader so the middleware can report a
// status-class label without parsing the underlying handler's response.
// It transparently forwards Hijacker / Flusher so WebSocket upgrade
// (/api/exec, /api/port-forward) and SSE (/api/events) keep working.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

// WriteHeader captures the status code and forwards to the real writer.
// Subsequent calls are no-ops (matching net/http semantics — only the first
// header write is honoured by the server).
func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

// Write marks the response as committed. Handlers that skip WriteHeader and
// jump straight to Write implicitly send 200 per net/http; mirror that here
// so the label is accurate.
func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

// Flush is forwarded for SSE handlers that depend on http.Flusher.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards to the underlying ResponseWriter's Hijacker so the
// WebSocket upgrade path (coder/websocket.Accept) works behind the wrapper.
// On success we record status 101 — the upgrade has completed and neither
// WriteHeader nor Write will fire.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("ResponseWriter does not implement http.Hijacker")
	}
	conn, rw, err := hj.Hijack()
	if err == nil {
		s.status = http.StatusSwitchingProtocols
		s.wrote = true
	}
	return conn, rw, err
}
