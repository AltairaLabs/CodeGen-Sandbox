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
		done <- Run(ctx, "127.0.0.1:0", dir)
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
	err := Run(context.Background(), "127.0.0.1:0", "/nonexistent/codegen-sandbox-test-root")
	require.Error(t, err)
}
