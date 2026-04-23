// Package api hosts the HTTP API that fronts the sandbox (separate from the
// MCP server). The routing service terminates auth and forwards trusted
// identity headers to this backend.
package api

import (
	"context"
	"log"
	"net/http"
	"strings"
)

// Identity is the trusted caller identity extracted from the routing
// service's forwarded headers.
type Identity struct {
	Sub    string
	User   string
	Groups []string
}

type identityKey struct{}

const (
	headerSub    = "X-Forwarded-Sub"
	headerUser   = "X-Forwarded-User"
	headerGroups = "X-Forwarded-Groups"
)

// WithIdentity returns a middleware that extracts the forwarded identity
// headers into the request context. When devMode is true and the headers
// are absent, a placeholder dev identity is injected. When devMode is
// false and X-Forwarded-Sub is absent, the request is rejected with 401.
func WithIdentity(devMode bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub := r.Header.Get(headerSub)
		var id Identity
		switch {
		case sub != "":
			id = Identity{
				Sub:    sub,
				User:   r.Header.Get(headerUser),
				Groups: parseGroups(r.Header.Get(headerGroups)),
			}
		case devMode:
			id = Identity{Sub: "dev", User: "dev"}
		default:
			http.Error(w, "missing identity headers", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), identityKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
		log.Printf("api sub=%s route=%s", id.Sub, r.URL.Path)
	})
}

// FromContext returns the Identity stored by WithIdentity, if any.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

func parseGroups(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
