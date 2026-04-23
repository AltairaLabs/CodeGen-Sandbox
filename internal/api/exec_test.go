//go:build linux || darwin

package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// wsURL converts an http:// httptest.Server URL to ws://.
func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

// readFrame reads one text JSON frame and decodes it.
func readFrame(ctx context.Context, t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	typ, data, err := c.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, websocket.MessageText, typ)
	var msg map[string]any
	require.NoError(t, json.Unmarshal(data, &msg))
	return msg
}

// writeFrame JSON-encodes v and writes it as a text frame.
func writeFrame(ctx context.Context, t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, c.Write(ctx, websocket.MessageText, b))
}

func TestExecHandler_HappyPath_EchoAndExit(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	srv := httptest.NewServer(execHandler(ws))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, wsURL(srv.URL), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.CloseNow() })

	sendStdin(ctx, t, c, "echo hello\n")
	require.True(t, waitForStdoutContains(ctx, t, c, "hello", 5*time.Second), "did not see 'hello' on stdout")

	sendStdin(ctx, t, c, "exit\n")
	require.True(t, waitForExitFrame(ctx, c, 5*time.Second), "did not receive exit frame")
}

// sendStdin writes a "stdin" frame containing the given shell text.
func sendStdin(ctx context.Context, t *testing.T, c *websocket.Conn, text string) {
	t.Helper()
	writeFrame(ctx, t, c, map[string]any{
		"type": "stdin",
		"data": base64.StdEncoding.EncodeToString([]byte(text)),
	})
}

// waitForStdoutContains reads frames until a stdout frame containing substr
// arrives or the deadline is reached.
func waitForStdoutContains(ctx context.Context, t *testing.T, c *websocket.Conn, substr string, d time.Duration) bool {
	t.Helper()
	for deadline := time.Now().Add(d); time.Now().Before(deadline); {
		msg := readFrame(ctx, t, c)
		if msg["type"] != "stdout" {
			continue
		}
		raw, _ := msg["data"].(string)
		dec, err := base64.StdEncoding.DecodeString(raw)
		require.NoError(t, err)
		if strings.Contains(string(dec), substr) {
			return true
		}
	}
	return false
}

// waitForExitFrame drains frames until an "exit" frame arrives or the server
// closes the WS. Tolerates closure as end-of-session.
func waitForExitFrame(ctx context.Context, c *websocket.Conn, d time.Duration) bool {
	for deadline := time.Now().Add(d); time.Now().Before(deadline); {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return false
		}
		if typ != websocket.MessageText {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg["type"] == "exit" {
			return true
		}
	}
	return false
}

func TestExecHandler_ResizeFrame_NoError(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	srv := httptest.NewServer(execHandler(ws))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, wsURL(srv.URL), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.CloseNow() })

	writeFrame(ctx, t, c, map[string]any{
		"type": "resize",
		"cols": 120,
		"rows": 40,
	})

	// Follow with an echo to confirm the session is still alive.
	writeFrame(ctx, t, c, map[string]any{
		"type": "stdin",
		"data": base64.StdEncoding.EncodeToString([]byte("echo ok\n")),
	})
	sawOK := false
	for deadline := time.Now().Add(3 * time.Second); !sawOK && time.Now().Before(deadline); {
		msg := readFrame(ctx, t, c)
		if msg["type"] == "stdout" {
			raw, _ := msg["data"].(string)
			dec, _ := base64.StdEncoding.DecodeString(raw)
			if strings.Contains(string(dec), "ok") {
				sawOK = true
			}
		}
	}
	require.True(t, sawOK, "session did not survive resize")
}

func TestExecHandler_ClientDisconnectKillsProcess(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	// pidCh captures the child process pid via a test-only hook.
	pidCh := make(chan int, 1)
	srv := httptest.NewServer(execHandlerWithHook(ws, func(pid int) { pidCh <- pid }))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, wsURL(srv.URL), nil)
	require.NoError(t, err)

	// Start a long-running command.
	writeFrame(ctx, t, c, map[string]any{
		"type": "stdin",
		"data": base64.StdEncoding.EncodeToString([]byte("sleep 30\n")),
	})

	var pid int
	select {
	case pid = <-pidCh:
	case <-time.After(3 * time.Second):
		t.Fatal("did not receive child pid via hook")
	}

	// Give bash a moment to actually spawn the sleep child.
	time.Sleep(300 * time.Millisecond)

	// Disconnect client.
	require.NoError(t, c.CloseNow())

	// Poll — the parent bash should be reaped quickly via process-group kill.
	alive := func() bool {
		// Signal 0 probes; non-nil error means the process is gone or we
		// don't own it any more. On Linux/macOS that's effectively dead.
		return syscall.Kill(pid, syscall.Signal(0)) == nil
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && alive() {
		time.Sleep(100 * time.Millisecond)
	}
	require.False(t, alive(), "bash pid %d still alive after disconnect", pid)
}

func TestExecHandler_MalformedFrame_DoesNotCrash(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	srv := httptest.NewServer(execHandler(ws))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, wsURL(srv.URL), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.CloseNow() })

	// Unknown type — server logs+ignores.
	writeFrame(ctx, t, c, map[string]any{"type": "bogus"})

	// Follow with a valid stdin to confirm the session survived.
	writeFrame(ctx, t, c, map[string]any{
		"type": "stdin",
		"data": base64.StdEncoding.EncodeToString([]byte("echo still-alive\n")),
	})

	sawStillAlive := false
	for deadline := time.Now().Add(3 * time.Second); !sawStillAlive && time.Now().Before(deadline); {
		msg := readFrame(ctx, t, c)
		if msg["type"] == "stdout" {
			raw, _ := msg["data"].(string)
			dec, _ := base64.StdEncoding.DecodeString(raw)
			if strings.Contains(string(dec), "still-alive") {
				sawStillAlive = true
			}
		}
	}
	require.True(t, sawStillAlive, "session did not survive malformed frame")
}
