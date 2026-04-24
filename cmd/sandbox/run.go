package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/api"
	"github.com/altairalabs/codegen-sandbox/internal/metrics"
	"github.com/altairalabs/codegen-sandbox/internal/server"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
)

// workspaceGaugeInterval sets the cadence at which the metrics listener
// walks the workspace to refresh its size + file-count gauges. Kept at 30s
// to align with typical Prometheus scrape intervals.
const workspaceGaugeInterval = 30 * time.Second

const (
	shutdownGraceSeconds = 10
	readHeaderTimeout    = 10 * time.Second
	idleTimeout          = 60 * time.Second
)

// Config bundles all runtime options for Run.
type Config struct {
	Addr              string
	APIAddr           string
	MetricsAddr       string
	WorkspaceRoot     string
	DevMode           bool
	EnableAPI         bool
	EnableExec        bool
	EnablePortForward bool
	EnableSSH         bool
	// SecretsDir enables the file source of the `secret` tool. See
	// internal/secrets for the full contract.
	SecretsDir string
}

// apiEnabled reports whether any human-facing API surface is requested.
func (c Config) apiEnabled() bool {
	return c.APIAddr != "" && (c.EnableAPI || c.EnableExec || c.EnablePortForward || c.EnableSSH)
}

// Run starts the sandbox MCP server on cfg.Addr, the human-facing API
// listener on cfg.APIAddr (if any flag enables it), and the Prometheus
// listener on cfg.MetricsAddr (if set). All listeners drain on ctx
// cancellation within a bounded grace window.
func Run(ctx context.Context, cfg Config) error {
	ws, err := workspace.New(cfg.WorkspaceRoot)
	if err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	metricsSurface, err := maybeBuildMetrics(cfg)
	if err != nil {
		return err
	}

	listeners, closer, err := buildListeners(ws, cfg, metricsSurface)
	if err != nil {
		return err
	}

	// Workspace-size + shell-count refresher lives as long as metrics is on.
	gaugeCtx, cancelGauge := context.WithCancel(context.Background())
	defer cancelGauge()
	if metricsSurface != nil {
		go workspaceGaugeLoop(gaugeCtx, metricsSurface, ws, listeners.sandbox.Shells())
	}

	errChans := listeners.serve()
	if err := waitForShutdown(ctx, errChans); err != nil {
		return err
	}
	return shutdownAll(listeners.servers(), closer, errChans)
}

// listenerBundle groups the three optional listeners + the bound sandbox
// server so Run can pass them around as a single value.
type listenerBundle struct {
	mcp     *http.Server
	api     *http.Server
	metrics *http.Server
	sandbox *server.Server
}

func (l *listenerBundle) servers() []*http.Server {
	return []*http.Server{l.mcp, l.api, l.metrics}
}

func (l *listenerBundle) serve() []<-chan error {
	out := make([]<-chan error, 0, 3)
	for _, s := range l.servers() {
		if s == nil {
			out = append(out, nil)
			continue
		}
		out = append(out, serve(s))
	}
	return out
}

// buildListeners constructs every listener the Config asks for and logs
// them. Packaging the construction here keeps Run's cognitive complexity
// low.
func buildListeners(ws *workspace.Workspace, cfg Config, m *metrics.Metrics) (*listenerBundle, io.Closer, error) {
	mcpSrv, sandbox, err := buildMCPServer(cfg.Addr, ws, cfg, m)
	if err != nil {
		return nil, nil, err
	}
	log.Printf("codegen-sandbox listening on %s (workspace=%s)", cfg.Addr, ws.Root())

	apiSrv, apiCloser, err := buildAPIServer(ws, cfg, m)
	if err != nil {
		return nil, nil, err
	}
	if apiSrv != nil {
		log.Printf("codegen-sandbox api listening on %s", cfg.APIAddr)
	}

	metricsSrv := buildMetricsServer(cfg.MetricsAddr, m)
	if metricsSrv != nil {
		log.Printf("codegen-sandbox metrics listening on %s", cfg.MetricsAddr)
	}

	return &listenerBundle{mcp: mcpSrv, api: apiSrv, metrics: metricsSrv, sandbox: sandbox}, apiCloser, nil
}

// maybeBuildMetrics returns a live *metrics.Metrics iff MetricsAddr is set.
func maybeBuildMetrics(cfg Config) (*metrics.Metrics, error) {
	if cfg.MetricsAddr == "" {
		return nil, nil
	}
	m, err := metrics.New()
	if err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	return m, nil
}

func buildMCPServer(addr string, ws *workspace.Workspace, cfg Config, m *metrics.Metrics) (*http.Server, *server.Server, error) {
	srv, err := server.NewWithConfig(ws, m, server.Config{SecretsDir: cfg.SecretsDir})
	if err != nil {
		return nil, nil, fmt.Errorf("server: %w", err)
	}
	// No WriteTimeout — SSE streams are long-lived.
	return &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}, srv, nil
}

func buildAPIServer(ws *workspace.Workspace, cfg Config, m *metrics.Metrics) (*http.Server, io.Closer, error) {
	if !cfg.apiEnabled() {
		return nil, nil, nil
	}
	handler, closer, err := api.New(api.Config{
		Workspace:         ws,
		DevMode:           cfg.DevMode,
		EnableAPI:         cfg.EnableAPI,
		EnableExec:        cfg.EnableExec,
		EnablePortForward: cfg.EnablePortForward,
		EnableSSH:         cfg.EnableSSH,
		Metrics:           m,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("api: %w", err)
	}
	return &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}, closer, nil
}

// buildMetricsServer returns a minimal /metrics listener, or nil if
// MetricsAddr is empty or the Metrics surface wasn't constructed.
func buildMetricsServer(addr string, m *metrics.Metrics) *http.Server {
	if addr == "" || m == nil {
		return nil
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", m.Handler())
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// workspaceGaugeLoop periodically refreshes the workspace-size and
// background-shell gauges until ctx is cancelled. A ticker rather than a
// custom Prometheus collector keeps the walk predictable (exactly one every
// 30s, regardless of scrape cadence) — Collect() would re-walk on every
// scrape, and a fast scraper could turn a dirt-cheap gauge into a CPU hog.
func workspaceGaugeLoop(ctx context.Context, m *metrics.Metrics, ws *workspace.Workspace, shells interface{ Len() int }) {
	// Prime the gauge immediately so scrapers see a real value on the very
	// first scrape rather than zero.
	refreshGauges(m, ws, shells)
	ticker := time.NewTicker(workspaceGaugeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshGauges(m, ws, shells)
		}
	}
}

func refreshGauges(m *metrics.Metrics, ws *workspace.Workspace, shells interface{ Len() int }) {
	if ws != nil {
		b, n := ws.Size()
		m.SetWorkspace(b, n)
	}
	if shells != nil {
		m.SetBackgroundShells(shells.Len())
	}
}

// waitForShutdown blocks until a listener crashes or ctx fires. The errChans
// slice is ordered [mcp, api, metrics]; nil entries are skipped so the
// reflect.Select fan-in only watches live listeners.
func waitForShutdown(ctx context.Context, errChans []<-chan error) error {
	cases := []reflect.SelectCase{{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())}}
	for _, ch := range errChans {
		if ch == nil {
			continue
		}
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch)})
	}
	idx, v, _ := reflect.Select(cases)
	if idx == 0 {
		log.Printf("shutdown signal received; draining up to %ds", shutdownGraceSeconds)
		return nil
	}
	if err, _ := v.Interface().(error); err != nil {
		return err
	}
	return nil
}

// shutdownAll drains every non-nil server and the optional API closer within
// the shared grace window, then waits for the listen goroutines.
func shutdownAll(servers []*http.Server, apiCloser io.Closer, errChans []<-chan error) error {
	// Deliberate detach: we only reach this branch because the caller's ctx
	// already fired. Using ctx here would give http.Server.Shutdown a
	// pre-cancelled context and collapse the grace window to zero.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGraceSeconds*time.Second)
	defer cancel()

	var firstErr error
	for _, s := range servers {
		if s == nil {
			continue
		}
		if err := s.Shutdown(shutdownCtx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("shutdown %s: %w", s.Addr, err)
		}
	}
	if apiCloser != nil {
		if err := apiCloser.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("api closer: %w", err)
		}
	}
	for _, ch := range errChans {
		if ch == nil {
			continue
		}
		if err := <-ch; err != nil && firstErr == nil {
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
