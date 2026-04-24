package api

import (
	"io"
	"net/http"

	"github.com/altairalabs/codegen-sandbox/internal/metrics"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
)

// Config bundles the inputs needed to build the API handler.
type Config struct {
	Workspace         *workspace.Workspace
	DevMode           bool
	EnableAPI         bool // tree/file/events
	EnableExec        bool // /api/exec (WebSocket PTY)
	EnablePortForward bool // /api/port-forward (WebSocket TCP tunnel to 127.0.0.1:<port>)
	EnableSSH         bool // embedded SSH server + /api/ssh-authorized-keys + /api/ssh-port
	Metrics           *metrics.Metrics
}

// nopCloser is returned when New has no background resource to close.
type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// New returns an http.Handler mounting the API routes at /api/* and a Closer
// that releases any background resources (currently: the embedded SSH
// listener when EnableSSH is true). All routes are wrapped in the identity
// middleware. Routes whose backing feature flag is false are not registered.
func New(cfg Config) (http.Handler, io.Closer, error) {
	mux := http.NewServeMux()
	route := routeMounter(mux, cfg.Metrics)

	// Self-describing endpoints are always registered. The spec catalogues
	// every route including -enable-* gated ones, so operators can discover
	// the full surface without flipping flags to find it.
	route("/api/openapi.yaml", http.HandlerFunc(openAPIHandler))
	route("/api/docs", http.HandlerFunc(docsHandler))

	if cfg.EnableAPI && cfg.Workspace != nil {
		route("/api/tree", treeHandler(cfg.Workspace))
		route("/api/file", fileHandler(cfg.Workspace))
		route("/api/events", eventsHandler(cfg.Workspace, cfg.Metrics))
		route("/api/download", downloadHandler(cfg.Workspace))
	}
	if cfg.EnableExec && cfg.Workspace != nil {
		route("/api/exec", execHandler(cfg.Workspace, cfg.Metrics))
	}
	if cfg.EnablePortForward {
		route("/api/port-forward", portForwardHandler(cfg.Metrics))
	}

	var closer io.Closer = nopCloser{}
	if cfg.EnableSSH && cfg.Workspace != nil {
		ssh, err := newSSHServer(cfg.Workspace)
		if err != nil {
			return nil, nil, err
		}
		route("/api/ssh-authorized-keys", authorizedKeysHandler(ssh.keys))
		route("/api/ssh-port", sshPortHandler(ssh))
		closer = ssh
	}

	return WithIdentity(cfg.DevMode, mux), closer, nil
}

// routeMounter returns a helper that mounts each handler behind
// withMetrics(pattern). Keeping the mux + metrics pair closed over in one
// place avoids repeating the wrap at every call site.
func routeMounter(mux *http.ServeMux, m *metrics.Metrics) func(pattern string, h http.Handler) {
	return func(pattern string, h http.Handler) {
		mux.Handle(pattern, withMetrics(m, pattern, h))
	}
}
