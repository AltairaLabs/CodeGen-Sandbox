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

// Run starts the sandbox MCP server on addr and, if apiAddr is non-empty and
// any of enableAPI/enableExec/enablePortForward/enableSSH is true, a second
// HTTP listener exposing the human-facing /api/* routes on apiAddr. Both
// listeners drain on ctx cancellation within a bounded grace window.
func Run(ctx context.Context, addr, apiAddr, workspaceRoot string, devMode, enableAPI, enableExec, enablePortForward, enableSSH bool) error {
	ws, err := workspace.New(workspaceRoot)
	if err != nil {
		return fmt.Errorf("workspace: %w", err)
	}

	srv, err := server.New(ws)
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}

	// No WriteTimeout — SSE streams are long-lived.
	mcpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	log.Printf("codegen-sandbox listening on %s (workspace=%s)", addr, ws.Root())

	var apiSrv *http.Server
	var apiCloser io.Closer
	if apiAddr != "" && (enableAPI || enableExec || enablePortForward || enableSSH) {
		handler, closer, err := api.New(api.Config{
			Workspace:         ws,
			DevMode:           devMode,
			EnableAPI:         enableAPI,
			EnableExec:        enableExec,
			EnablePortForward: enablePortForward,
			EnableSSH:         enableSSH,
		})
		if err != nil {
			return fmt.Errorf("api: %w", err)
		}
		apiCloser = closer
		apiSrv = &http.Server{
			Addr:              apiAddr,
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       idleTimeout,
		}
		log.Printf("codegen-sandbox api listening on %s", apiAddr)
	}

	mcpErr := serve(mcpSrv)
	var apiErr chan error
	if apiSrv != nil {
		apiErr = serve(apiSrv)
	}

	select {
	case err := <-mcpErr:
		return err
	case err := <-apiErrOrNil(apiErr):
		return err
	case <-ctx.Done():
		log.Printf("shutdown signal received; draining up to %ds", shutdownGraceSeconds)
	}

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

	// Drain the listen goroutines so we don't leak them.
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
func apiErrOrNil(apiErr chan error) <-chan error {
	if apiErr == nil {
		return nil
	}
	return apiErr
}
