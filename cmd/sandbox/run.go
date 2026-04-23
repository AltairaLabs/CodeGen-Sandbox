package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/server"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
)

const (
	shutdownGraceSeconds = 10
	readHeaderTimeout    = 10 * time.Second
	idleTimeout          = 60 * time.Second
)

// Run starts the sandbox MCP server on addr with workspaceRoot as the
// agent-visible workspace. It listens for ctx cancellation and drains
// inflight HTTP requests within a bounded grace window before returning.
// Returns nil on clean shutdown; non-nil on startup failure or a shutdown
// that exceeds the grace window.
func Run(ctx context.Context, addr, workspaceRoot string) error {
	ws, err := workspace.New(workspaceRoot)
	if err != nil {
		return fmt.Errorf("workspace: %w", err)
	}

	srv, err := server.New(ws)
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}

	// No WriteTimeout — SSE streams are long-lived.
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	log.Printf("codegen-sandbox listening on %s (workspace=%s)", addr, ws.Root())

	listenErr := make(chan error, 1)
	go func() {
		err := httpSrv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		listenErr <- err
	}()

	select {
	case err := <-listenErr:
		// Crash before shutdown signal.
		return err
	case <-ctx.Done():
		log.Printf("shutdown signal received; draining up to %ds", shutdownGraceSeconds)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGraceSeconds*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	// Wait for the listen goroutine to return so we don't leak it.
	return <-listenErr
}
