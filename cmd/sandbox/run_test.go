package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_CancelledContextExitsCleanly(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Addr: "127.0.0.1:0", WorkspaceRoot: dir})
	}()

	// Give the server a beat to bind, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err, "Run should exit cleanly on ctx cancel")
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of ctx cancel")
	}
}

func TestRun_InvalidWorkspaceReturnsError(t *testing.T) {
	err := Run(context.Background(), Config{
		Addr:          "127.0.0.1:0",
		WorkspaceRoot: "/nonexistent/codegen-sandbox-test-root",
	})
	require.Error(t, err)
}

func TestRun_WithAPIListener_CancelledContextExitsCleanly(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Addr:          "127.0.0.1:0",
			APIAddr:       "127.0.0.1:0",
			WorkspaceRoot: dir,
			DevMode:       true,
			EnableAPI:     true,
		})
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err, "Run with API listener should exit cleanly on ctx cancel")
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of ctx cancel")
	}
}

func TestRun_WithMetricsListener_CancelledContextExitsCleanly(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Addr:          "127.0.0.1:0",
			MetricsAddr:   "127.0.0.1:0",
			WorkspaceRoot: dir,
		})
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err, "Run with metrics listener should exit cleanly on ctx cancel")
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of ctx cancel")
	}
}

func TestRun_WithSSH_CancelledContextExitsCleanly(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		// Only SSH enabled; api listener still mounts because EnableSSH=true.
		done <- Run(ctx, Config{
			Addr:          "127.0.0.1:0",
			APIAddr:       "127.0.0.1:0",
			WorkspaceRoot: dir,
			DevMode:       true,
			EnableSSH:     true,
		})
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err, "Run with SSH should exit cleanly on ctx cancel")
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of ctx cancel")
	}
}
