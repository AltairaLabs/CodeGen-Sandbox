package lsp

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DefaultIdleTimeout is the window after which an unused client is shut down.
const DefaultIdleTimeout = 10 * time.Minute

// CommandResolver returns the language-server argv for a detector. The
// registry is decoupled from the verify package so tests can inject stubs
// without importing the real detectors.
type CommandResolver func(language string) []string

// Registry hosts one Client per (workspace, language) pair and manages
// lifecycle: lazy spawn, idle shutdown, orderly teardown.
//
// All Registry operations are safe for concurrent use.
type Registry struct {
	resolve     CommandResolver
	idleTimeout time.Duration

	mu      sync.Mutex
	clients map[string]*Client

	sweepOnce   sync.Once
	sweepCancel context.CancelFunc
}

// NewRegistry builds an empty registry. idleTimeout <= 0 uses DefaultIdleTimeout.
func NewRegistry(resolve CommandResolver, idleTimeout time.Duration) *Registry {
	if idleTimeout <= 0 {
		idleTimeout = DefaultIdleTimeout
	}
	return &Registry{
		resolve:     resolve,
		idleTimeout: idleTimeout,
		clients:     make(map[string]*Client),
	}
}

// Get returns the client for (workspace, language), spawning it (and running
// initialize) on first call. Returns an error containing the language-server
// binary name when it's not on PATH, so callers can surface it to the agent.
func (r *Registry) Get(ctx context.Context, workspace, language string) (*Client, error) {
	argv := r.resolve(language)
	if len(argv) == 0 {
		return nil, fmt.Errorf("LSP not configured for %s", language)
	}
	key := workspace + "\x00" + language

	r.mu.Lock()
	client, ok := r.clients[key]
	if !ok {
		client = NewClient(workspace, argv)
		r.clients[key] = client
	}
	r.mu.Unlock()

	r.sweepOnce.Do(r.startSweeper)

	if err := client.Start(ctx); err != nil {
		r.mu.Lock()
		delete(r.clients, key)
		r.mu.Unlock()
		return nil, err
	}
	return client, nil
}

// Shutdown tears down every client in the registry. Safe to call multiple
// times; concurrent Get calls after Shutdown will respawn as usual.
func (r *Registry) Shutdown(ctx context.Context) {
	if r.sweepCancel != nil {
		r.sweepCancel()
	}
	r.mu.Lock()
	clients := r.clients
	r.clients = make(map[string]*Client)
	r.mu.Unlock()
	for _, c := range clients {
		_ = c.Shutdown(ctx)
	}
}

// startSweeper kicks off the background goroutine that shuts down idle
// clients. The interval is min(idleTimeout/2, 1 minute) — aggressive enough
// to reclaim resources within one timeout window, cheap enough to not burn
// goroutine wakeups.
func (r *Registry) startSweeper() {
	ctx, cancel := context.WithCancel(context.Background())
	r.sweepCancel = cancel
	interval := r.idleTimeout / 2
	if interval > time.Minute {
		interval = time.Minute
	}
	if interval < time.Second {
		interval = time.Second
	}
	go r.runSweeper(ctx, interval)
}

func (r *Registry) runSweeper(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reapIdle(ctx)
		}
	}
}

func (r *Registry) reapIdle(ctx context.Context) {
	cutoff := time.Now().Add(-r.idleTimeout)
	r.mu.Lock()
	var toKill []*Client
	for k, c := range r.clients {
		last := c.LastUsed()
		if !last.IsZero() && last.Before(cutoff) {
			toKill = append(toKill, c)
			delete(r.clients, k)
		}
	}
	r.mu.Unlock()
	for _, c := range toKill {
		_ = c.Shutdown(ctx)
	}
}
