package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// readMessage reads one LSP-framed JSON-RPC message from br.
//
// Framing (from LSP 3.17):
//
//	Content-Length: N\r\n
//	Content-Type: application/vscode-jsonrpc; charset=utf-8\r\n  (optional)
//	\r\n
//	<N bytes of UTF-8 JSON>
//
// All fields but Content-Length are optional and ignored here.
func readMessage(br *bufio.Reader) ([]byte, error) {
	n, err := readContentLength(br)
	if err != nil {
		return nil, err
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(br, body); err != nil {
		return nil, fmt.Errorf("lsp: read body: %w", err)
	}
	return body, nil
}

func readContentLength(br *bufio.Reader) (int, error) {
	length := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return 0, fmt.Errorf("lsp: read header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if length < 0 {
				return 0, fmt.Errorf("lsp: missing Content-Length header")
			}
			return length, nil
		}
		if v, ok := parseContentLengthLine(line); ok {
			length = v
		}
	}
}

func parseContentLengthLine(line string) (int, bool) {
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

// writeMessage encodes payload as a JSON-RPC LSP frame and writes it to w.
func writeMessage(w io.Writer, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("lsp: marshal: %w", err)
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(w, header); err != nil {
		return fmt.Errorf("lsp: write header: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("lsp: write body: %w", err)
	}
	return nil
}
