package api

import (
	"net/http"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
)

// Config bundles the inputs needed to build the API handler. Later tasks will
// add fields for port-forward/ssh toggles and a shell registry; for now only
// the read-only surface plus optional exec is exposed.
type Config struct {
	Workspace  *workspace.Workspace
	DevMode    bool
	EnableAPI  bool // tree/file/events
	EnableExec bool // /api/exec (WebSocket PTY)
}

// New returns an http.Handler mounting the API routes at /api/*. All routes
// are wrapped in the identity middleware. Routes whose backing feature flag
// is false are not registered.
func New(cfg Config) http.Handler {
	mux := http.NewServeMux()
	if cfg.EnableAPI && cfg.Workspace != nil {
		mux.Handle("/api/tree", treeHandler(cfg.Workspace))
		mux.Handle("/api/file", fileHandler(cfg.Workspace))
		mux.Handle("/api/events", eventsHandler(cfg.Workspace))
	}
	if cfg.EnableExec && cfg.Workspace != nil {
		mux.Handle("/api/exec", execHandler(cfg.Workspace))
	}
	return WithIdentity(cfg.DevMode, mux)
}
