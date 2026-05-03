// Package probes ships k8s-style liveness and readiness handlers intended
// for the operator-facing /metrics listener. The endpoints are unauthenticated
// and meant to be reachable by orchestrators (kubelet, docker healthcheck) —
// keep them cheap and side-effect-free.
package probes

import (
	"net/http"
	"os"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
)

// LivenessHandler responds 200 OK to indicate the process is alive. It
// performs no real check; if the process can answer HTTP at all, it's alive.
func LivenessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
}

// ReadinessHandler stat()s every workspace root in the set, returning 200
// when all are present directories and 503 (with the failing workspace name)
// on the first error. Readiness should reflect *this* pod, not the world —
// downstream concerns (LSP servers, package managers) are deliberately not
// probed here.
func ReadinessHandler(set *workspace.Set) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if set == nil || set.Len() == 0 {
			http.Error(w, "no workspaces configured", http.StatusServiceUnavailable)
			return
		}
		for _, ws := range set.All() {
			info, err := os.Stat(ws.Root())
			if err != nil {
				http.Error(w, ws.Name()+": "+err.Error(), http.StatusServiceUnavailable)
				return
			}
			if !info.IsDir() {
				http.Error(w, ws.Name()+": not a directory", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})
}
