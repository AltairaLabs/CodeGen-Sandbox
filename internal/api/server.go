package api

import (
	"io"
	"net/http"

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

	// Self-describing endpoints are always registered. The spec catalogues
	// every route including -enable-* gated ones, so operators can discover
	// the full surface without flipping flags to find it.
	mux.HandleFunc("/api/openapi.yaml", openAPIHandler)
	mux.HandleFunc("/api/docs", docsHandler)

	if cfg.EnableAPI && cfg.Workspace != nil {
		mux.Handle("/api/tree", treeHandler(cfg.Workspace))
		mux.Handle("/api/file", fileHandler(cfg.Workspace))
		mux.Handle("/api/events", eventsHandler(cfg.Workspace))
		mux.Handle("/api/download", downloadHandler(cfg.Workspace))
	}
	if cfg.EnableExec && cfg.Workspace != nil {
		mux.Handle("/api/exec", execHandler(cfg.Workspace))
	}
	if cfg.EnablePortForward {
		mux.Handle("/api/port-forward", portForwardHandler())
	}

	var closer io.Closer = nopCloser{}
	if cfg.EnableSSH && cfg.Workspace != nil {
		ssh, err := newSSHServer(cfg.Workspace)
		if err != nil {
			return nil, nil, err
		}
		mux.Handle("/api/ssh-authorized-keys", authorizedKeysHandler(ssh.keys))
		mux.Handle("/api/ssh-port", sshPortHandler(ssh))
		closer = ssh
	}

	return WithIdentity(cfg.DevMode, mux), closer, nil
}
