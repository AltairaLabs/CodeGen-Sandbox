package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Client is a minimal LSP 3.17 client bound to one workspace root and one
// language server subprocess. Operations are serialised through a single
// request writer; concurrent callers block.
//
// Zero value is not valid — use NewClient.
type Client struct {
	workspace string
	argv      []string

	// startOnce guards the subprocess launch + initialize handshake so
	// concurrent first callers don't race the spawn. Populated lazily.
	startOnce sync.Once
	startErr  error

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	writeMu sync.Mutex
	nextID  atomic.Int64

	// inflight is keyed by request ID. Each entry's channel receives the
	// raw response payload.
	inflightMu sync.Mutex
	inflight   map[int]chan jsonrpcResponse

	// closed is flipped by Shutdown() / readLoop exit to short-circuit
	// subsequent calls with a clear error.
	closed atomic.Bool

	// lastUsed is updated on every successful request; registries use it
	// to implement idle-shutdown timeouts.
	lastUsedUnixNano atomic.Int64
}

// NewClient constructs (but does not spawn) a Client. workspace must be an
// absolute path; argv is the language-server launch command (e.g. {"gopls",
// "serve"}). The subprocess is spawned on the first call that requires it.
func NewClient(workspace string, argv []string) *Client {
	return &Client{
		workspace: workspace,
		argv:      argv,
		inflight:  make(map[int]chan jsonrpcResponse),
	}
}

// Workspace returns the absolute workspace path this client was built for.
func (c *Client) Workspace() string { return c.workspace }

// LastUsed returns the time of the most recent completed request, or the
// zero time if none have completed yet. Registries use it to drive idle
// shutdown.
func (c *Client) LastUsed() time.Time {
	v := c.lastUsedUnixNano.Load()
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(0, v)
}

// ErrClosed is returned by methods on a client whose subprocess has already
// exited (either via explicit Shutdown or because the server died).
var ErrClosed = errors.New("lsp: client closed")

// Start spawns the subprocess, runs the LSP initialize handshake, and
// blocks until the server is ready for requests. Subsequent calls are
// no-ops returning the stored error (if any) from the first attempt.
func (c *Client) Start(ctx context.Context) error {
	c.startOnce.Do(func() { c.startErr = c.doStart(ctx) })
	return c.startErr
}

func (c *Client) doStart(ctx context.Context) error {
	if len(c.argv) == 0 {
		return errors.New("lsp: empty argv")
	}
	bin, err := exec.LookPath(c.argv[0])
	if err != nil {
		return fmt.Errorf("lsp: %s not found on PATH", c.argv[0])
	}
	cmd := exec.Command(bin, c.argv[1:]...) //nolint:gosec // argv is operator-controlled via Detector, not user input
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("lsp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("lsp: stdout pipe: %w", err)
	}
	cmd.Stderr = io.Discard // server logs are noisy; drop unless debugging
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("lsp: start %s: %w", c.argv[0], err)
	}
	c.cmd = cmd
	c.stdin = stdin
	c.stdout = bufio.NewReader(stdout)

	go c.readLoop()

	return c.handshake(ctx)
}

func (c *Client) handshake(ctx context.Context) error {
	params := map[string]any{
		"processId": nil,
		"rootUri":   pathToURI(c.workspace),
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"definition": map[string]any{},
				"references": map[string]any{},
				"rename":     map[string]any{},
			},
		},
	}
	if _, err := c.call(ctx, "initialize", params); err != nil {
		return fmt.Errorf("lsp: initialize: %w", err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		return fmt.Errorf("lsp: initialized: %w", err)
	}
	return nil
}

// readLoop drains the server's stdout, dispatching responses to the waiting
// inflight channel or (for server-initiated notifications) silently
// discarding them. Exits when stdout closes.
func (c *Client) readLoop() {
	for {
		body, err := readMessage(c.stdout)
		if err != nil {
			c.failAllInflight(err)
			return
		}
		var resp jsonrpcResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			continue // unparseable frame — best we can do is skip
		}
		if resp.ID == nil {
			continue // server notification — ignored
		}
		c.deliver(*resp.ID, resp)
	}
}

func (c *Client) deliver(id int, resp jsonrpcResponse) {
	c.inflightMu.Lock()
	ch, ok := c.inflight[id]
	delete(c.inflight, id)
	c.inflightMu.Unlock()
	if ok {
		ch <- resp
	}
}

func (c *Client) failAllInflight(err error) {
	c.closed.Store(true)
	c.inflightMu.Lock()
	for id, ch := range c.inflight {
		ch <- jsonrpcResponse{Error: &jsonrpcError{Code: -1, Message: err.Error()}}
		delete(c.inflight, id)
	}
	c.inflightMu.Unlock()
}

// call issues a request and blocks for the response (or ctx cancellation).
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, ErrClosed
	}
	id := int(c.nextID.Add(1))
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("lsp: marshal params: %w", err)
	}
	ch := make(chan jsonrpcResponse, 1)
	c.inflightMu.Lock()
	c.inflight[id] = ch
	c.inflightMu.Unlock()

	c.writeMu.Lock()
	werr := writeMessage(c.stdin, jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  raw,
	})
	c.writeMu.Unlock()
	if werr != nil {
		c.inflightMu.Lock()
		delete(c.inflight, id)
		c.inflightMu.Unlock()
		return nil, werr
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("lsp: %s: %s", method, resp.Error.Message)
		}
		c.lastUsedUnixNano.Store(time.Now().UnixNano())
		return resp.Result, nil
	case <-ctx.Done():
		c.inflightMu.Lock()
		delete(c.inflight, id)
		c.inflightMu.Unlock()
		return nil, ctx.Err()
	}
}

// notify sends a fire-and-forget notification (no ID, no response).
func (c *Client) notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("lsp: marshal notify params: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeMessage(c.stdin, jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  raw,
	})
}

// Definition returns the locations where the symbol at file:line:col is defined.
// line/col are 1-based (consistent with the rest of the sandbox); the wire
// protocol converts to LSP's 0-based form.
func (c *Client) Definition(ctx context.Context, file string, line, col int) ([]Location, error) {
	if err := c.Start(ctx); err != nil {
		return nil, err
	}
	params := positionParams(c.workspace, file, line, col)
	raw, err := c.call(ctx, "textDocument/definition", params)
	if err != nil {
		return nil, err
	}
	return decodeLocations(raw, c.workspace)
}

// References returns all references (including the declaration) to the symbol
// at file:line:col.
func (c *Client) References(ctx context.Context, file string, line, col int) ([]Location, error) {
	if err := c.Start(ctx); err != nil {
		return nil, err
	}
	params := referenceParams(c.workspace, file, line, col)
	raw, err := c.call(ctx, "textDocument/references", params)
	if err != nil {
		return nil, err
	}
	return decodeLocations(raw, c.workspace)
}

// Rename returns the structured WorkspaceEdit for renaming the symbol at
// file:line:col to newName. The sandbox does NOT apply the edit — callers
// surface it as a diff for the agent to review.
func (c *Client) Rename(ctx context.Context, file string, line, col int, newName string) (WorkspaceEdit, error) {
	if err := c.Start(ctx); err != nil {
		return WorkspaceEdit{}, err
	}
	params := renameParams(c.workspace, file, line, col, newName)
	raw, err := c.call(ctx, "textDocument/rename", params)
	if err != nil {
		return WorkspaceEdit{}, err
	}
	return decodeWorkspaceEdit(raw, c.workspace)
}

// Shutdown issues the LSP shutdown + exit handshake and waits for the
// subprocess to exit. Safe to call multiple times; subsequent calls no-op.
func (c *Client) Shutdown(ctx context.Context) error {
	if c.closed.Swap(true) {
		return nil
	}
	// Best effort — a broken server may not respond to shutdown. We cap
	// the wait at a short window and kill if it overruns.
	shutdownCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, _ = c.call(shutdownCtx, "shutdown", nil)
	_ = c.notify("exit", nil)
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case <-done:
		return nil
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
		return nil
	}
}

// --- helpers ---

func positionParams(root, file string, line, col int) map[string]any {
	return map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(absFile(root, file))},
		"position":     map[string]any{"line": line - 1, "character": col - 1},
	}
}

func referenceParams(root, file string, line, col int) map[string]any {
	p := positionParams(root, file, line, col)
	p["context"] = map[string]any{"includeDeclaration": true}
	return p
}

func renameParams(root, file string, line, col int, newName string) map[string]any {
	p := positionParams(root, file, line, col)
	p["newName"] = newName
	return p
}

func decodeLocations(raw json.RawMessage, root string) ([]Location, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	// The server may return Location, Location[], or LocationLink[]; we
	// only handle the first two. A leading '[' is the array form.
	if raw[0] == '[' {
		var locs []lspLocation
		if err := json.Unmarshal(raw, &locs); err != nil {
			return nil, fmt.Errorf("lsp: decode locations: %w", err)
		}
		return toLocations(locs, root), nil
	}
	var loc lspLocation
	if err := json.Unmarshal(raw, &loc); err != nil {
		return nil, fmt.Errorf("lsp: decode location: %w", err)
	}
	return toLocations([]lspLocation{loc}, root), nil
}

func decodeWorkspaceEdit(raw json.RawMessage, root string) (WorkspaceEdit, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return WorkspaceEdit{Changes: map[string][]TextEdit{}}, nil
	}
	var edit lspWorkspaceEdit
	if err := json.Unmarshal(raw, &edit); err != nil {
		return WorkspaceEdit{}, fmt.Errorf("lsp: decode workspace edit: %w", err)
	}
	out := WorkspaceEdit{Changes: make(map[string][]TextEdit, len(edit.Changes))}
	for uri, edits := range edit.Changes {
		rel := uriToRel(uri, root)
		converted := make([]TextEdit, 0, len(edits))
		for _, e := range edits {
			converted = append(converted, TextEdit{
				Line:    e.Range.Start.Line + 1,
				Col:     e.Range.Start.Character + 1,
				EndLine: e.Range.End.Line + 1,
				EndCol:  e.Range.End.Character + 1,
				NewText: e.NewText,
			})
		}
		out.Changes[rel] = converted
	}
	return out, nil
}

func toLocations(locs []lspLocation, root string) []Location {
	out := make([]Location, 0, len(locs))
	for _, l := range locs {
		out = append(out, Location{
			URI:     uriToRel(l.URI, root),
			Line:    l.Range.Start.Line + 1,
			Col:     l.Range.Start.Character + 1,
			EndLine: l.Range.End.Line + 1,
			EndCol:  l.Range.End.Character + 1,
		})
	}
	return out
}

// pathToURI converts an absolute filesystem path to an LSP `file://` URI.
// POSIX-only; the sandbox always runs in Linux containers.
func pathToURI(p string) string {
	u := &url.URL{Scheme: "file", Path: p}
	return u.String()
}

// uriToRel extracts the path from an LSP file URI and returns it relative
// to the workspace root (or the original path if conversion isn't possible).
func uriToRel(uri, root string) string {
	path := uriToPath(uri)
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	return u.Path
}

// absFile returns the absolute path for file, treating relative paths as
// relative to the workspace root.
func absFile(root, file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(root, file)
}
