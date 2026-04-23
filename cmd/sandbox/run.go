package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/api"
	"github.com/altairalabs/codegen-sandbox/internal/server"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
)

const (
	shutdownGraceSeconds = 10
	readHeaderTimeout    = 10 * time.Second
	idleTimeout          = 60 * time.Second
)

// Config bundles all runtime options for Run.
type Config struct {
	Addr              string
	APIAddr           string
	WorkspaceRoot     string
	DevMode           bool
	EnableAPI         bool
	EnableExec        bool
	EnablePortForward bool
	EnableSSH         bool
}

// apiEnabled reports whether any human-facing API surface is requested.
func (c Config) apiEnabled() bool {
	return c.APIAddr != "" && (c.EnableAPI || c.EnableExec || c.EnablePortForward || c.EnableSSH)
}

// Run starts the sandbox MCP server on cfg.Addr and, if cfg.apiEnabled is
// true, a second HTTP listener on cfg.APIAddr. Both listeners drain on ctx
// cancellation within a bounded grace window.
func Run(ctx context.Context, cfg Config) error {
	ws, err := workspace.New(cfg.WorkspaceRoot)
	if err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	mcpSrv, err := buildMCPServer(cfg.Addr, ws)
	if err != nil {
		return err
	}
	log.Printf("codegen-sandbox listening on %s (workspace=%s)", cfg.Addr, ws.Root())

	apiSrv, apiCloser, err := buildAPIServer(ws, cfg)
	if err != nil {
		return err
	}
	if apiSrv != nil {
		log.Printf("codegen-sandbox api listening on %s", cfg.APIAddr)
	}

	mcpErr := serve(mcpSrv)
	var apiErr chan error
	if apiSrv != nil {
		apiErr = serve(apiSrv)
	}

	if err := waitForShutdown(ctx, mcpErr, apiErr); err != nil {
		return err
	}
	return shutdownAll(mcpSrv, apiSrv, apiCloser, mcpErr, apiErr)
}

func buildMCPServer(addr string, ws *workspace.Workspace) (*http.Server, error) {
	srv, err := server.New(ws)
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	// No WriteTimeout — SSE streams are long-lived.
	return &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}, nil
}

func buildAPIServer(ws *workspace.Workspace, cfg Config) (*http.Server, io.Closer, error) {
	if !cfg.apiEnabled() {
		return nil, nil, nil
	}
	handler, closer, err := api.New(api.Config{
		Workspace:         ws,
		DevMode:           cfg.DevMode,
		EnableAPI:         cfg.EnableAPI,
		EnableExec:        cfg.EnableExec,
		EnablePortForward: cfg.EnablePortForward,
		EnableSSH:         cfg.EnableSSH,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("api: %w", err)
	}
	return &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}, closer, nil
}

// waitForShutdown blocks until a listener crashes or ctx fires. Returns the
// listener error (crash) or nil (graceful shutdown signal received).
func waitForShutdown(ctx context.Context, mcpErr, apiErr <-chan error) error {
	select {
	case err := <-mcpErr:
		return err
	case err := <-apiErrOrNil(apiErr):
		return err
	case <-ctx.Done():
		log.Printf("shutdown signal received; draining up to %ds", shutdownGraceSeconds)
		return nil
	}
}

// shutdownAll drains both servers and the optional API closer within the
// shared grace window, then waits for the listen goroutines.
func shutdownAll(mcpSrv, apiSrv *http.Server, apiCloser io.Closer, mcpErr, apiErr <-chan error) error {
	// Deliberate detach: we only reach this branch because the caller's ctx
	// already fired. Using ctx here would give http.Server.Shutdown a
	// pre-cancelled context and collapse the grace window to zero.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGraceSeconds*time.Second)
	defer cancel()

	var firstErr error
	if err := mcpSrv.Shutdown(shutdownCtx); err != nil {
		firstErr = fmt.Errorf("mcp shutdown: %w", err)
	}
	if apiSrv != nil {
		if err := apiSrv.Shutdown(shutdownCtx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("api shutdown: %w", err)
		}
	}
	if apiCloser != nil {
		if err := apiCloser.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("api closer: %w", err)
		}
	}
	if err := <-mcpErr; err != nil && firstErr == nil {
		firstErr = err
	}
	if apiSrv != nil {
		if err := <-apiErr; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// serve spawns ListenAndServe in a goroutine and returns a buffered channel
// that receives exactly one value (nil on clean shutdown).
func serve(s *http.Server) chan error {
	ch := make(chan error, 1)
	go func() {
		err := s.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		ch <- err
	}()
	return ch
}

// apiErrOrNil returns a channel that never fires if apiErr is nil. Allows a
// single select statement to work with an optional second listener.
func apiErrOrNil(apiErr <-chan error) <-chan error {
	if apiErr == nil {
		return nil
	}
	return apiErr
}
