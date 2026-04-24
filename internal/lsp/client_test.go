package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The tests in this file drive a mock LSP server that's a Go subprocess of
// the test binary itself. TestMain dispatches to runMockServer when the
// GO_LSP_MOCK env var is set; tests set the env var + argv accordingly.
//
// This pattern is the stdlib's own technique (see os/exec's own test
// subprocess) and keeps the mock server's source in this file — no extra
// binaries to build.

const mockEnvKey = "GO_LSP_MOCK_MODE"
const mockRootEnvKey = "GO_LSP_MOCK_ROOT"

func TestMain(m *testing.M) {
	if mode := os.Getenv(mockEnvKey); mode != "" {
		runMockServer(mode)
		return
	}
	os.Exit(m.Run())
}

// newMockClient spawns a fresh Client bound to an in-test mock server.
// The returned Client has its subprocess argv set to re-exec the test
// binary with GO_LSP_MOCK_MODE=<mode>.
func newMockClient(t *testing.T, mode, workspace string) *Client {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	c := NewClient(workspace, []string{exe})
	c.argv = []string{exe}
	// Swap the exec.LookPath lookup with a closure that uses our path
	// directly — we can't LookPath an arbitrary absolute path reliably
	// across CI filesystems. Instead, we set up the command in doStart;
	// since exe is absolute, LookPath accepts it.
	origEnv := os.Getenv(mockEnvKey)
	if err := os.Setenv(mockEnvKey, mode); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	t.Cleanup(func() { _ = os.Setenv(mockEnvKey, origEnv) })
	return c
}

func TestClient_Definition(t *testing.T) {
	root := t.TempDir()
	if err := os.Setenv(mockRootEnvKey, root); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv(mockRootEnvKey) })
	c := newMockClient(t, "definition", root)
	defer func() { _ = c.Shutdown(context.Background()) }()

	locs, err := c.Definition(context.Background(), "a.go", 10, 5)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("want 1 location, got %d", len(locs))
	}
	got := locs[0]
	if got.URI != "a.go" || got.Line != 42 || got.Col != 3 {
		t.Fatalf("unexpected location: %+v", got)
	}
}

func TestClient_References(t *testing.T) {
	root := t.TempDir()
	if err := os.Setenv(mockRootEnvKey, root); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv(mockRootEnvKey) })
	c := newMockClient(t, "references", root)
	defer func() { _ = c.Shutdown(context.Background()) }()

	locs, err := c.References(context.Background(), "a.go", 10, 5)
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if len(locs) != 2 {
		t.Fatalf("want 2 locations, got %d", len(locs))
	}
	if locs[0].URI != "a.go" || locs[1].URI != "b.go" {
		t.Fatalf("unexpected refs: %+v", locs)
	}
}

func TestClient_Rename(t *testing.T) {
	root := t.TempDir()
	if err := os.Setenv(mockRootEnvKey, root); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv(mockRootEnvKey) })
	c := newMockClient(t, "rename", root)
	defer func() { _ = c.Shutdown(context.Background()) }()

	edit, err := c.Rename(context.Background(), "a.go", 10, 5, "NewName")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	edits, ok := edit.Changes["a.go"]
	if !ok {
		t.Fatalf("missing a.go in changes: %+v", edit.Changes)
	}
	if len(edits) != 1 || edits[0].NewText != "NewName" {
		t.Fatalf("unexpected edits: %+v", edits)
	}
	if edits[0].Line != 10 || edits[0].Col != 5 {
		t.Fatalf("unexpected 1-based conversion: %+v", edits[0])
	}
}

func TestClient_ErrorFromServer(t *testing.T) {
	root := t.TempDir()
	c := newMockClient(t, "error", root)
	defer func() { _ = c.Shutdown(context.Background()) }()

	_, err := c.Definition(context.Background(), "a.go", 1, 1)
	if err == nil || !strings.Contains(err.Error(), "server broke") {
		t.Fatalf("expected server error, got %v", err)
	}
}

func TestClient_NotFoundBinary(t *testing.T) {
	c := NewClient(t.TempDir(), []string{"this-binary-does-not-exist-xyzzy"})
	err := c.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestClient_ContextCancel(t *testing.T) {
	c := newMockClient(t, "hang", t.TempDir())
	defer func() { _ = c.Shutdown(context.Background()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.Definition(ctx, "a.go", 1, 1)
	if err == nil {
		t.Fatal("expected ctx error")
	}
}

func TestRegistry_GetAndShutdown(t *testing.T) {
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	origEnv := os.Getenv(mockEnvKey)
	if err := os.Setenv(mockEnvKey, "definition"); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	if err := os.Setenv(mockRootEnvKey, root); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv(mockEnvKey, origEnv)
		_ = os.Unsetenv(mockRootEnvKey)
	})

	reg := NewRegistry(func(lang string) []string {
		if lang == "go" {
			return []string{exe}
		}
		return nil
	}, time.Minute)
	defer reg.Shutdown(context.Background())

	c, err := reg.Get(context.Background(), root, "go")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := c.Definition(context.Background(), "a.go", 1, 1); err != nil {
		t.Fatalf("Definition: %v", err)
	}

	c2, err := reg.Get(context.Background(), root, "go")
	if err != nil {
		t.Fatalf("Get again: %v", err)
	}
	if c != c2 {
		t.Fatalf("expected same client from second Get")
	}

	if _, err := reg.Get(context.Background(), root, "python"); err == nil {
		t.Fatal("expected 'LSP not configured' for python")
	}
}

// --- mock server ---

type mockMessage struct {
	ID     *int            `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

// runMockServer is re-entered by TestMain when GO_LSP_MOCK_MODE is set.
// It implements just enough of the LSP wire protocol to exercise Client.
func runMockServer(mode string) {
	br := bufio.NewReader(os.Stdin)
	for {
		body, err := readMessage(br)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // io.EOF is returned as sentinel
				return
			}
			return
		}
		var msg mockMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			replyResult(msg.ID, map[string]any{"capabilities": map[string]any{}})
		case "initialized", "exit":
			if msg.Method == "exit" {
				return
			}
		case "shutdown":
			replyResult(msg.ID, nil)
		case "textDocument/definition":
			mockHandleDefinition(mode, msg.ID)
		case "textDocument/references":
			mockHandleReferences(mode, msg.ID)
		case "textDocument/rename":
			mockHandleRename(mode, msg.ID)
		}
	}
}

func mockFileURI(name string) string {
	root := os.Getenv(mockRootEnvKey)
	if root == "" {
		root = "/tmp"
	}
	return "file://" + filepath.Join(root, name)
}

func mockHandleDefinition(mode string, id *int) {
	switch mode {
	case "error":
		replyError(id, "server broke")
	case "hang":
		// Don't reply; the client's ctx deadline will fire.
	default:
		replyResult(id, []lspLocation{
			{URI: mockFileURI("a.go"), Range: lspRange{
				Start: lspPosition{Line: 41, Character: 2},
				End:   lspPosition{Line: 41, Character: 10},
			}},
		})
	}
}

func mockHandleReferences(_ string, id *int) {
	replyResult(id, []lspLocation{
		{URI: mockFileURI("a.go"), Range: lspRange{
			Start: lspPosition{Line: 0, Character: 0},
			End:   lspPosition{Line: 0, Character: 5},
		}},
		{URI: mockFileURI("b.go"), Range: lspRange{
			Start: lspPosition{Line: 5, Character: 0},
			End:   lspPosition{Line: 5, Character: 5},
		}},
	})
}

func mockHandleRename(_ string, id *int) {
	replyResult(id, lspWorkspaceEdit{
		Changes: map[string][]lspTextEdit{
			mockFileURI("a.go"): {
				{
					Range: lspRange{
						Start: lspPosition{Line: 9, Character: 4},
						End:   lspPosition{Line: 9, Character: 12},
					},
					NewText: "NewName",
				},
			},
		},
	})
}

func replyResult(id *int, result any) {
	if id == nil {
		return
	}
	raw, _ := json.Marshal(result)
	_ = writeMessage(os.Stdout, jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  raw,
	})
}

func replyError(id *int, msg string) {
	if id == nil {
		return
	}
	_ = writeMessage(os.Stdout, jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: -32000, Message: msg},
	})
}
