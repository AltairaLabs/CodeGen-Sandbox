package lsp

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestRegistry_ReapsIdle verifies that the sweeper shuts down a client that
// hasn't been used within idleTimeout. We force LastUsed into the past via
// the atomic counter so the test doesn't actually sleep.
func TestRegistry_ReapsIdle(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if err := os.Setenv(mockEnvKey, "definition"); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv(mockEnvKey) })

	reg := NewRegistry(func(lang string) []string {
		if lang == "go" {
			return []string{exe}
		}
		return nil
	}, 2*time.Second)
	defer reg.Shutdown(context.Background())

	root := t.TempDir()
	c, err := reg.Get(context.Background(), root, "go")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Fake staleness: rewind lastUsed to well before cutoff.
	c.lastUsedUnixNano.Store(time.Now().Add(-time.Hour).UnixNano())

	// Directly drive reapIdle (the background sweeper eventually does this,
	// but we don't want the test to depend on wall-clock sleep). This
	// exercises the same code path.
	reg.reapIdle(context.Background())

	reg.mu.Lock()
	n := len(reg.clients)
	reg.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected idle client reaped, got %d remaining", n)
	}
}

// TestRegistry_DefaultIdleTimeout verifies NewRegistry applies the default
// when zero / negative idleTimeout is passed.
func TestRegistry_DefaultIdleTimeout(t *testing.T) {
	r := NewRegistry(func(string) []string { return nil }, 0)
	if r.idleTimeout != DefaultIdleTimeout {
		t.Fatalf("want default %v, got %v", DefaultIdleTimeout, r.idleTimeout)
	}
	r2 := NewRegistry(func(string) []string { return nil }, -1)
	if r2.idleTimeout != DefaultIdleTimeout {
		t.Fatalf("want default %v on negative, got %v", DefaultIdleTimeout, r2.idleTimeout)
	}
}
