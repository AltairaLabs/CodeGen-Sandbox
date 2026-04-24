package api

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/require"
)

func TestEventsHandler_StreamsFileEvents(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	srv := httptest.NewServer(eventsHandler(ws, nil))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	// Give the watcher a moment to register.
	time.Sleep(200 * time.Millisecond)

	// Create a file and wait for the event.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hi"), 0o600))

	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE line: %v", err)
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		payload = strings.TrimRight(payload, "\n")

		var ev fsEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		if ev.Path == "new.txt" {
			return
		}
	}
	t.Fatal("did not receive fsnotify event for new.txt within deadline")
}

func TestEventsHandler_ClientDisconnect_Clean(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	require.NoError(t, err)

	srv := httptest.NewServer(eventsHandler(ws, nil))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)

	// Disconnect immediately.
	require.NoError(t, resp.Body.Close())
	// If the handler leaks, srv.Close will block. t.Cleanup enforces a timeout
	// via the overall test timeout; we just assert we can reach here promptly.
}
