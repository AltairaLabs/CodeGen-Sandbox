// Package search provides pluggable WebSearch backends for the codegen
// sandbox. A Backend is chosen at server startup via the
// CODEGEN_SANDBOX_SEARCH_BACKEND env var; each concrete backend wraps a
// single external HTTP search API (Brave, Exa, Tavily).
package search

import (
	"context"
	"fmt"
	"os"
)

// Result is a single normalised search result surfaced to the agent.
type Result struct {
	URL     string
	Title   string
	Snippet string
}

// Backend is the common interface each search provider implements.
type Backend interface {
	// Name returns the backend identifier ("brave" | "exa" | "tavily").
	Name() string
	// Search runs the query against the remote API. When limit <= 0 the
	// backend picks a sensible default (10 for all current backends).
	Search(ctx context.Context, query string, limit int) ([]Result, error)
}

const (
	// BackendEnvVar names the operator-set env var selecting a backend.
	BackendEnvVar = "CODEGEN_SANDBOX_SEARCH_BACKEND"

	braveAPIKeyEnv  = "BRAVE_API_KEY"
	exaAPIKeyEnv    = "EXA_API_KEY"
	tavilyAPIKeyEnv = "TAVILY_API_KEY"

	defaultLimit = 10
)

// backendSpec describes how to instantiate a concrete Backend from its
// API-key env var.
type backendSpec struct {
	apiKeyEnv string
	build     func(apiKey string) Backend
}

var backendSpecs = map[string]backendSpec{
	"brave":  {braveAPIKeyEnv, NewBrave},
	"exa":    {exaAPIKeyEnv, NewExa},
	"tavily": {tavilyAPIKeyEnv, NewTavily},
}

// NewFromEnv selects a backend based on CODEGEN_SANDBOX_SEARCH_BACKEND.
// Returns (nil, nil) when the env var is unset — the caller distinguishes
// "not configured" from real errors. Returns a non-nil error when a backend
// name is set but the corresponding API key env var is missing, or when the
// name is unknown.
func NewFromEnv() (Backend, error) {
	name := os.Getenv(BackendEnvVar)
	if name == "" {
		return nil, nil
	}
	spec, ok := backendSpecs[name]
	if !ok {
		return nil, fmt.Errorf("unknown WebSearch backend %q (supported: brave, exa, tavily)", name)
	}
	key := os.Getenv(spec.apiKeyEnv)
	if key == "" {
		return nil, fmt.Errorf("backend %q selected but %s is not set", name, spec.apiKeyEnv)
	}
	return spec.build(key), nil
}

// effectiveLimit normalises non-positive limits to defaultLimit.
func effectiveLimit(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	return limit
}
