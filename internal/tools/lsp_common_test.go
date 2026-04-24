package tools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/lsp"
	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

// The handler tests for find_definition / find_references / rename_symbol
// share a mock-LSP harness. Each test spawns a Python-style mock server
// (re-exec of the test binary) to avoid depending on gopls.

const (
	lspMockModeEnv = "GO_LSP_MOCK_MODE"
	lspMockRootEnv = "GO_LSP_MOCK_ROOT"
)

// newLSPTestDeps builds a fresh tools.Deps with a registry that spawns our
// mock LSP server. The temp workspace is set up with a go.mod so the Go
// detector activates.
func newLSPTestDeps(t *testing.T, mode string) (*tools.Deps, string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.25\n"), 0o644))

	ws, err := workspace.New(dir)
	require.NoError(t, err)

	exe := locateMockBinary(t)
	if err := os.Setenv(lspMockModeEnv, mode); err != nil {
		t.Fatalf("setenv mode: %v", err)
	}
	// Use the workspace's canonical (symlink-resolved) root so URIs the
	// mock server emits match what the client's uriToRel computes against.
	// On macOS, t.TempDir returns /var/... but workspace.New canonicalises
	// to /private/var/...; a mismatch makes uriToRel fall back to the
	// absolute path.
	if err := os.Setenv(lspMockRootEnv, ws.Root()); err != nil {
		t.Fatalf("setenv root: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Unsetenv(lspMockModeEnv)
		_ = os.Unsetenv(lspMockRootEnv)
	})

	reg := lsp.NewRegistry(func(lang string) []string {
		if lang == "go" {
			return []string{exe}
		}
		return nil
	}, time.Minute)
	t.Cleanup(func() { reg.Shutdown(context.Background()) })

	return &tools.Deps{
		Workspace:   ws,
		Tracker:     workspace.NewReadTracker(),
		LSPRegistry: reg,
	}, ws.Root()
}

// locateMockBinary compiles the dedicated mock binary on first use. The
// binary is placed in a package-scoped tempdir (not t.TempDir) so its
// lifetime spans every test in the package — t.TempDir would reap the
// binary after the test that first built it finishes.
var (
	mockBinaryPath string
	mockBinaryDir  string
)

func locateMockBinary(t *testing.T) string {
	t.Helper()
	if mockBinaryPath != "" {
		return mockBinaryPath
	}
	dir, err := os.MkdirTemp("", "lspmock-")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	mockBinaryDir = dir
	out := filepath.Join(dir, "lsp-mock")
	cmd := exec.Command("go", "build", "-o", out, "github.com/altairalabs/codegen-sandbox/internal/tools/lspmock")
	cmd.Env = os.Environ()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mock: %v: %s", err, output)
	}
	mockBinaryPath = out
	return out
}

func TestMain(m *testing.M) {
	code := m.Run()
	if mockBinaryDir != "" {
		_ = os.RemoveAll(mockBinaryDir)
	}
	os.Exit(code)
}

// callLSP is a small shim that invokes a handler with its arg map and
// returns the result, failing the test on transport errors.
func callLSP(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	require.NoError(t, err)
	return res
}

func writeSeedGoFile(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func mustContain(t *testing.T, body, substr string) {
	t.Helper()
	if !strings.Contains(body, substr) {
		t.Fatalf("missing substring %q in:\n%s", substr, body)
	}
}
