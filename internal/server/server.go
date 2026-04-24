// Package server wires the codegen-sandbox MCP server and its HTTP+SSE transport.
package server

import (
	"net/http"
	"os"

	"github.com/altairalabs/codegen-sandbox/internal/lsp"
	"github.com/altairalabs/codegen-sandbox/internal/metrics"
	"github.com/altairalabs/codegen-sandbox/internal/metrics/health"
	"github.com/altairalabs/codegen-sandbox/internal/secrets"
	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/altairalabs/codegen-sandbox/internal/tracing"
	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Config bundles the knobs New accepts beyond the workspace.
type Config struct {
	// SecretsDir is the directory hosting one-file-per-secret mounts
	// (e.g. a k8s Secret volume). Empty string disables the file source;
	// CODEGEN_SANDBOX_SECRET_* env vars still work.
	SecretsDir string

	// Tracer, when non-nil, is threaded into the observability middleware so
	// every MCP tool invocation emits an OTel span. A nil Tracer is the
	// no-op path — see internal/tracing for the nil-receiver contract.
	Tracer *tracing.Tracer

	// HealthTracker, when non-nil, receives a per-tool Observe hook from the
	// middleware plus ObserveGreen / ObserveTestResult hooks from the verify
	// tools. Nil disables agent-health instrumentation (every Tracker method
	// is nil-safe).
	HealthTracker *health.Tracker

	// ReadOnly switches the sandbox into scoped-exploration mode: only the
	// non-mutating tools are registered, and tools/list reflects this so
	// agents discover their reduced capability honestly. See #21 and
	// docs/concepts/readonly-mode for the contract.
	ReadOnly bool
}

// Server is the codegen sandbox MCP server.
type Server struct {
	mcp     *mcpserver.MCPServer
	sse     *mcpserver.SSEServer
	ws      *workspace.Workspace
	tracker *workspace.ReadTracker
	lspReg  *lsp.Registry
	shells  *tools.ShellRegistry
}

// New constructs a Server bound to the given workspace with default config.
// m may be nil; every *metrics.Metrics method is nil-safe so unconfigured
// embedders don't need a sentinel.
func New(ws *workspace.Workspace, m *metrics.Metrics) (*Server, error) {
	return NewWithConfig(ws, m, Config{})
}

// NewWithConfig constructs a Server bound to the given workspace and config.
// m may be nil; every *metrics.Metrics method is nil-safe.
func NewWithConfig(ws *workspace.Workspace, m *metrics.Metrics, cfg Config) (*Server, error) {
	mcpSrv := mcpserver.NewMCPServer(
		"codegen-sandbox",
		"0.1.0",
		mcpserver.WithToolCapabilities(true),
	)
	s := &Server{
		mcp:     mcpSrv,
		ws:      ws,
		tracker: workspace.NewReadTracker(),
		lspReg:  lsp.NewRegistry(resolveLSPCommand, 0),
		shells:  tools.NewShellRegistry(),
	}
	s.sse = mcpserver.NewSSEServer(mcpSrv)

	// Every tool handler is wrapped with scrub + metrics + tracing middleware
	// through the registrar. See observabilityRegistrar for the composition
	// order rationale.
	reg := &observabilityRegistrar{inner: s.mcp, metrics: m, health: cfg.HealthTracker, tracer: cfg.Tracer, ws: s.ws}
	deps := &tools.Deps{
		Workspace:     s.ws,
		Tracker:       s.tracker,
		Shells:        s.shells,
		TestResults:   tools.NewTestResultStore(),
		CoverageIndex: tools.NewCoverageIndex(),
		LSPRegistry:   s.lspReg,
		Secrets:       secrets.New(cfg.SecretsDir, os.Environ()),
		Metrics:       m,
		Health:        cfg.HealthTracker,
	}
	registerToolsForMode(reg, deps, cfg.ReadOnly)
	// Web tools (WebFetch / WebSearch) are NOT registered here. They are
	// stateless and don't need the sandbox's filesystem or process
	// namespace, so operators hook up vendor MCP servers alongside this
	// sandbox (Brave / Exa / Tavily each publish their own; the MCP
	// project ships a reference `fetch` server). See
	// docs/concepts/non-sandbox-tools for the rationale.
	return s, nil
}

// Handler returns the SSE http.Handler for this server.
func (s *Server) Handler() http.Handler { return s.sse }

// MCP exposes the underlying MCP server for tool registration.
func (s *Server) MCP() *mcpserver.MCPServer { return s.mcp }

// Workspace exposes the bound workspace.
func (s *Server) Workspace() *workspace.Workspace { return s.ws }

// Tracker exposes the bound read tracker.
func (s *Server) Tracker() *workspace.ReadTracker { return s.tracker }

// LSPRegistry exposes the server's LSP client registry for graceful
// shutdown coordination from the process entrypoint.
func (s *Server) LSPRegistry() *lsp.Registry { return s.lspReg }

// Shells exposes the server's background-shell registry so callers (metrics
// timer, tests) can read or poll its state.
func (s *Server) Shells() *tools.ShellRegistry { return s.shells }

// registerToolsForMode wires the MCP tool surface for either full or
// read-only mode.
//
// Read-only contract: only the tools that cannot mutate the workspace
// (or its git refs, or the agent's session shells) are registered. The
// MCP `tools/list` response then reflects the reduced surface honestly,
// so a subagent dispatched in this mode discovers its own capability
// boundary rather than calling a missing tool and getting a transport
// error. See docs/concepts/readonly-mode for the full contract and #21
// for the rationale.
//
// The split tracks each tool's authority:
//
//   - Read tools (Read, Glob, Grep, search_code, find_definition,
//     find_references, snapshot_list, snapshot_diff,
//     last_test_failures, tests_covering, secret) — always registered.
//   - Mutating tools (Write, Edit, Bash, BashOutput, KillShell, run_*,
//     snapshot_create, snapshot_restore, rename_symbol, the AST edit
//     trio, render_*) — registered only when ReadOnly is false.
//
// Note that `secret` reads operator-configured credentials but does not
// mutate the workspace, so it stays in the read-only set.
func registerToolsForMode(reg tools.ToolAdder, deps *tools.Deps, readOnly bool) {
	// Read tools — present in every mode.
	tools.RegisterRead(reg, deps)
	tools.RegisterGlob(reg, deps)
	tools.RegisterGrep(reg, deps)
	tools.RegisterSearchCode(reg, deps)
	tools.RegisterLSPNavigation(reg, deps)
	tools.RegisterSnapshotsReadOnly(reg, deps)
	tools.RegisterLastTestFailures(reg, deps)
	tools.RegisterTestsCovering(reg, deps)
	tools.RegisterSecrets(reg, deps)

	if readOnly {
		return
	}

	// Mutating tools — only in full mode.
	tools.RegisterWrite(reg, deps)
	tools.RegisterEdit(reg, deps)
	tools.RegisterBash(reg, deps)
	tools.RegisterBashOutput(reg, deps)
	tools.RegisterKillShell(reg, deps)
	tools.RegisterRunTests(reg, deps)
	tools.RegisterRunLint(reg, deps)
	tools.RegisterRunTypecheck(reg, deps)
	tools.RegisterRunScript(reg, deps)
	tools.RegisterRunFailingTests(reg, deps)
	tools.RegisterSnapshotsMutating(reg, deps)
	tools.RegisterASTEdits(reg, deps)
	tools.RegisterLSPRename(reg, deps)
	tools.RegisterRender(reg, deps)
}

// resolveLSPCommand maps a Detector.Language() to its language-server argv.
// Kept in sync with each Detector's LSPCommand(); single source of truth
// lives on the Detector, this switch is the Registry-side adapter for the
// (workspace-root independent) language → argv lookup the Registry needs.
func resolveLSPCommand(language string) []string {
	for _, d := range verify.AllDetectors() {
		if d.Language() == language {
			return d.LSPCommand()
		}
	}
	return nil
}
