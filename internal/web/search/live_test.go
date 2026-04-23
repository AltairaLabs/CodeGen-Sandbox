//go:build live

// Package search contains live-API integration tests that hit the real
// Brave / Exa / Tavily endpoints. These are gated behind the `live` build
// tag so CI's default `go test ./...` does NOT run them — the assertions
// reach the public internet and the vendors' API keys are operator-owned.
//
// To run:
//
//	BRAVE_API_KEY=... EXA_API_KEY=... TAVILY_API_KEY=... \
//	go test -tags=live -run TestLive ./internal/web/search/...
//
// Each test skips cleanly when its backend's API key env var is unset, so
// it's fine to run with just one key configured.
package search

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

const liveQuery = "golang http context cancellation"

// assertLiveResults runs a smoke-grade happy-path check: we hit the real
// endpoint and confirm the response parses, has at least one result, and
// that URL / Title / Snippet are populated. This is a contract-shape
// check, not a relevance check — vendors change their rankings freely.
func assertLiveResults(t *testing.T, b Backend, query string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results, err := b.Search(ctx, query, 5)
	if err != nil {
		t.Fatalf("%s live Search(%q): %v", b.Name(), query, err)
	}
	if len(results) == 0 {
		t.Fatalf("%s live Search(%q): 0 results", b.Name(), query)
	}
	for i, r := range results {
		if !strings.HasPrefix(r.URL, "http://") && !strings.HasPrefix(r.URL, "https://") {
			t.Errorf("%s live result[%d]: URL is not http(s): %q", b.Name(), i, r.URL)
		}
		if strings.TrimSpace(r.Title) == "" {
			t.Errorf("%s live result[%d]: empty Title", b.Name(), i)
		}
		// Snippet is best-effort across vendors — some return empty for
		// certain result types. Log for visibility but don't fail.
		if strings.TrimSpace(r.Snippet) == "" {
			t.Logf("%s live result[%d]: empty Snippet (accepted)", b.Name(), i)
		}
	}
	t.Logf("%s live: %d results for %q", b.Name(), len(results), query)
}

func TestLive_Brave(t *testing.T) {
	key := os.Getenv("BRAVE_API_KEY")
	if key == "" {
		t.Skip("BRAVE_API_KEY not set")
	}
	assertLiveResults(t, NewBrave(key), liveQuery)
}

func TestLive_Exa(t *testing.T) {
	key := os.Getenv("EXA_API_KEY")
	if key == "" {
		t.Skip("EXA_API_KEY not set")
	}
	assertLiveResults(t, NewExa(key), liveQuery)
}

func TestLive_Tavily(t *testing.T) {
	key := os.Getenv("TAVILY_API_KEY")
	if key == "" {
		t.Skip("TAVILY_API_KEY not set")
	}
	assertLiveResults(t, NewTavily(key), liveQuery)
}

// TestLive_FromEnv exercises NewFromEnv against the real configured
// backend. Useful to verify the full "operator sets env vars → backend
// searches" path in one go.
func TestLive_FromEnv(t *testing.T) {
	if os.Getenv("CODEGEN_SANDBOX_SEARCH_BACKEND") == "" {
		t.Skip("CODEGEN_SANDBOX_SEARCH_BACKEND not set")
	}
	b, err := NewFromEnv()
	if err != nil {
		t.Fatalf("NewFromEnv: %v", err)
	}
	if b == nil {
		t.Fatal("NewFromEnv returned nil with env set")
	}
	assertLiveResults(t, b, liveQuery)
}
