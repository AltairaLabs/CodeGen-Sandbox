// Package lsp is a minimal LSP JSON-RPC 2.0 client for the codegen sandbox.
//
// The sandbox doesn't need the full LSP surface — only initialize, definition,
// references, rename, and shutdown. Everything else is out of scope.
//
// Wire protocol: raw JSON-RPC 2.0 over stdio with LSP's "Content-Length: N"
// framing (https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/).
// We roll the framing ourselves rather than pull a dependency — the entire
// wire path is under 200 LOC.
package lsp

import (
	"encoding/json"
)

// Location is a workspace-relative source range returned by the server.
// Fields mirror LSP's Location shape but with workspace-relative URIs
// post-normalisation and 1-based line/column (LSP uses 0-based; we convert
// on the way out).
type Location struct {
	URI     string `json:"uri"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	EndLine int    `json:"endLine"`
	EndCol  int    `json:"endCol"`
}

// WorkspaceEdit is the structured rename result — a map of URI to per-file
// textual edits. The sandbox does NOT apply these automatically; it surfaces
// them so the agent can review + re-read + re-edit via the normal Edit tool.
type WorkspaceEdit struct {
	// Changes keys are workspace-relative file paths (not URIs).
	Changes map[string][]TextEdit `json:"changes"`
}

// TextEdit is one edit within a file's WorkspaceEdit. Line/col are 1-based
// inclusive-start, exclusive-end (same convention as the rest of the sandbox).
type TextEdit struct {
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	EndLine int    `json:"endLine"`
	EndCol  int    `json:"endCol"`
	NewText string `json:"newText"`
}

// jsonrpcRequest / jsonrpcResponse are the on-wire shapes. They're exported
// as lowercase fields only in JSON; the Go types stay unexported to keep the
// public surface of the package small.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"` // omitted for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"` // set for server → client notifications
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// lspPosition / lspRange / lspLocation mirror the on-wire LSP shapes before
// we normalise them to the 1-based Location form above.
type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspTextEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

type lspWorkspaceEdit struct {
	Changes map[string][]lspTextEdit `json:"changes"`
}
