// Package main is a test-only mock LSP server. It speaks just enough of the
// LSP 3.17 wire protocol (initialize + definition + references + rename +
// shutdown) to exercise the tools-layer LSP handlers without depending on a
// real gopls install.
//
// Behaviour switches off GO_LSP_MOCK_MODE:
//
//	"definition"  — return one location in a.go at line 42
//	"references"  — return refs in a.go + b.go
//	"rename"      — return a WorkspaceEdit renaming at line 10
//	"empty"       — return no results
//	"error"       — return an LSP error for every request
//
// File URIs are rooted at GO_LSP_MOCK_ROOT so tests see workspace-relative
// paths post-normalisation.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	mode := os.Getenv("GO_LSP_MOCK_MODE")
	if mode == "" {
		mode = "definition"
	}
	br := bufio.NewReader(os.Stdin)
	for {
		body, err := readMessage(br)
		if err != nil {
			return
		}
		var msg struct {
			ID     *int            `json:"id,omitempty"`
			Method string          `json:"method,omitempty"`
			Params json.RawMessage `json:"params,omitempty"`
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		dispatch(mode, msg.Method, msg.ID)
		if msg.Method == "exit" {
			return
		}
	}
}

func dispatch(mode, method string, id *int) {
	switch method {
	case "initialize":
		replyResult(id, map[string]any{"capabilities": map[string]any{}})
	case "shutdown":
		replyResult(id, nil)
	case "textDocument/definition":
		handleDefinition(mode, id)
	case "textDocument/references":
		handleReferences(mode, id)
	case "textDocument/rename":
		handleRename(mode, id)
	}
}

func handleDefinition(mode string, id *int) {
	switch mode {
	case "error":
		replyError(id, "mock: definition failed")
	case "empty":
		replyResult(id, []any{})
	default:
		replyResult(id, []map[string]any{
			locationJSON("a.go", 41, 2, 41, 10),
		})
	}
}

func handleReferences(mode string, id *int) {
	if mode == "error" {
		replyError(id, "mock: references failed")
		return
	}
	replyResult(id, []map[string]any{
		locationJSON("a.go", 5, 0, 5, 5),
		locationJSON("b.go", 10, 0, 10, 5),
	})
}

func handleRename(mode string, id *int) {
	if mode == "error" {
		replyError(id, "mock: rename failed")
		return
	}
	if mode == "empty" {
		replyResult(id, map[string]any{"changes": map[string]any{}})
		return
	}
	replyResult(id, map[string]any{
		"changes": map[string]any{
			fileURI("a.go"): []map[string]any{
				{
					"range": map[string]any{
						"start": map[string]any{"line": 9, "character": 4},
						"end":   map[string]any{"line": 9, "character": 12},
					},
					"newText": "VerifyToken",
				},
			},
		},
	})
}

func locationJSON(name string, sl, sc, el, ec int) map[string]any {
	return map[string]any{
		"uri": fileURI(name),
		"range": map[string]any{
			"start": map[string]any{"line": sl, "character": sc},
			"end":   map[string]any{"line": el, "character": ec},
		},
	}
}

func fileURI(name string) string {
	root := os.Getenv("GO_LSP_MOCK_ROOT")
	if root == "" {
		root = "/tmp"
	}
	return "file://" + filepath.Join(root, name)
}

func replyResult(id *int, result any) {
	if id == nil {
		return
	}
	raw, _ := json.Marshal(result)
	_ = writeMessage(os.Stdout, map[string]any{
		"jsonrpc": "2.0",
		"id":      *id,
		"result":  json.RawMessage(raw),
	})
}

func replyError(id *int, msg string) {
	if id == nil {
		return
	}
	_ = writeMessage(os.Stdout, map[string]any{
		"jsonrpc": "2.0",
		"id":      *id,
		"error":   map[string]any{"code": -32000, "message": msg},
	})
}

// readMessage / writeMessage are inlined copies of the framing helpers in
// internal/lsp/wire.go. Duplicated so this binary can live in its own
// package with no dependency on the internal/lsp test helpers.

func readMessage(br *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if length < 0 {
				return nil, fmt.Errorf("missing Content-Length")
			}
			break
		}
		if v, ok := parseContentLength(line); ok {
			length = v
		}
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(br, body); err != nil {
		return nil, err
	}
	return body, nil
}

func parseContentLength(line string) (int, bool) {
	const prefix = "Content-Length:"
	if !strings.HasPrefix(line, prefix) {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(line[len(prefix):]))
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

func writeMessage(w io.Writer, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}
