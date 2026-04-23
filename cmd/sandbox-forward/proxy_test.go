package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startEchoTCPServer starts a 127.0.0.1 TCP echo server and returns its port.
func startEchoTCPServer(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// fakePortForwardServer mimics the real /api/port-forward endpoint: it
// upgrades to WS, dials 127.0.0.1:<port> and shuttles bytes. It also captures
// the headers of the upgrade request so tests can assert auth forwarding.
type fakePortForwardServer struct {
	mu             sync.Mutex
	capturedHdrs   http.Header
	sshPort        int
	capturedSSHHdr http.Header
}

func newFakeServer(sshPort int) *fakePortForwardServer {
	return &fakePortForwardServer{sshPort: sshPort}
}

func (f *fakePortForwardServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/port-forward", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.capturedHdrs = r.Header.Clone()
		f.mu.Unlock()

		portStr := r.URL.Query().Get("port")
		port, err := strconv.Atoi(portStr)
		if err != nil {
			http.Error(w, "bad port", http.StatusBadRequest)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()

		tcp, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			_ = c.Close(websocket.StatusInternalError, "dial failed")
			return
		}
		defer func() { _ = tcp.Close() }()

		ctx := r.Context()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for {
				typ, data, err := c.Read(ctx)
				if err != nil {
					_ = tcp.Close()
					return
				}
				if typ != websocket.MessageBinary {
					continue
				}
				if _, err := tcp.Write(data); err != nil {
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			buf := make([]byte, 32*1024)
			for {
				n, err := tcp.Read(buf)
				if n > 0 {
					if c.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
						return
					}
				}
				if err != nil {
					_ = c.Close(websocket.StatusNormalClosure, "")
					return
				}
			}
		}()
		wg.Wait()
	})
	mux.HandleFunc("/api/ssh-port", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.capturedSSHHdr = r.Header.Clone()
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"port": f.sshPort})
	})
	return mux
}

func TestBuildPortForwardURL(t *testing.T) {
	cases := []struct {
		server string
		port   int
		want   string
	}{
		{"http://127.0.0.1:18080", 8765, "ws://127.0.0.1:18080/api/port-forward?port=8765"},
		{"https://sandbox.example.com", 2345, "wss://sandbox.example.com/api/port-forward?port=2345"},
		{"sandbox.example.com", 22, "wss://sandbox.example.com/api/port-forward?port=22"},
		{"https://host/", 80, "wss://host/api/port-forward?port=80"},
	}
	for _, tc := range cases {
		t.Run(tc.server, func(t *testing.T) {
			got, err := buildPortForwardURL(tc.server, tc.port)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNormalizeServerURL_Rejects(t *testing.T) {
	cases := []string{
		"ftp://host",
		"://",
	}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			_, err := normalizeServerURL(tc)
			assert.Error(t, err)
		})
	}
}

func TestProxy_Listener_Echo_WithAuth(t *testing.T) {
	echoPort := startEchoTCPServer(t)
	fake := newFakeServer(0)
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	// Pick a free local port.
	lnTmp, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	localPort := lnTmp.Addr().(*net.TCPAddr).Port
	require.NoError(t, lnTmp.Close())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() {
		done <- runProxy(ctx, []string{
			"--server", srv.URL,
			"--port", strconv.Itoa(echoPort),
			"--local-port", strconv.Itoa(localPort),
			"--bearer", "tok-xyz",
			"--cookie", "session=s1",
		})
	}()

	// Wait for the listener to come up.
	var conn net.Conn
	for i := 0; i < 50; i++ {
		c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(localPort))
		if err == nil {
			conn = c
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.NotNil(t, conn, "listener never came up")
	t.Cleanup(func() { _ = conn.Close() })

	_, err = conn.Write([]byte("hello\n"))
	require.NoError(t, err)

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(buf[:n]))

	// Verify headers made it through.
	fake.mu.Lock()
	hdrs := fake.capturedHdrs
	fake.mu.Unlock()
	assert.Equal(t, "Bearer tok-xyz", hdrs.Get("Authorization"))
	assert.Equal(t, "session=s1", hdrs.Get("Cookie"))

	// Close the client, then cancel to unwind the listener.
	_ = conn.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil && !strings.Contains(err.Error(), "canceled") {
			t.Logf("runProxy returned: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runProxy did not return after ctx cancel")
	}
}

// syncBuffer is a bytes.Buffer guarded by a mutex so the test goroutine and
// the tunnel goroutine can share an io.Writer without tripping -race.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestProxy_Stdio_Echo(t *testing.T) {
	echoPort := startEchoTCPServer(t)
	fake := newFakeServer(0)
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	wsURL, err := buildPortForwardURL(srv.URL, echoPort)
	require.NoError(t, err)

	stdinR, stdinW := io.Pipe()
	var stdout syncBuffer

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() {
		done <- runStdioTunnel(ctx, wsURL, http.Header{}, stdinR, &stdout)
	}()

	_, err = stdinW.Write([]byte("stdio-echo\n"))
	require.NoError(t, err)

	// Wait for echo.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(stdout.String(), "stdio-echo\n") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	assert.Contains(t, stdout.String(), "stdio-echo\n")

	// Close stdin → stdio pump sees EOF, closes the WS, returns.
	_ = stdinW.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("runStdioTunnel did not return after stdin close")
	}
}

// TestProxy_Stdio_RemoteClose_UnblocksStdinPump guards the fix for a hang
// where the remote closing the WebSocket left the in→WS pump blocked on
// an uncancellable Read (os.Stdin / io.Pipe). runStdioTunnel must close
// the input reader when WS→out returns.
func TestProxy_Stdio_RemoteClose_UnblocksStdinPump(t *testing.T) {
	// A handler that accepts the WS, reads nothing, and closes immediately.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = c.Close(websocket.StatusNormalClosure, "bye")
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	wsURL, err := buildPortForwardURL(srv.URL, 8080)
	require.NoError(t, err)

	// stdinW is never closed by the test. If the fix regresses,
	// runStdioTunnel blocks forever on stdinR.Read.
	stdinR, stdinW := io.Pipe()
	t.Cleanup(func() { _ = stdinW.Close() })
	var stdout syncBuffer

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- runStdioTunnel(ctx, wsURL, http.Header{}, stdinR, &stdout) }()

	select {
	case <-done:
		// Good: function returned after remote close even though stdin is still open.
	case <-time.After(2 * time.Second):
		t.Fatal("runStdioTunnel did not return after remote close; in-pump likely stuck on stdin Read")
	}
}

func TestProxy_SSH_ResolvesPort(t *testing.T) {
	echoPort := startEchoTCPServer(t)
	fake := newFakeServer(echoPort)
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	stdinR, stdinW := io.Pipe()
	var stdout syncBuffer

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() {
		// Direct call via runProxy (stdio path); feeds through fetchSSHPort.
		// We inject stdin/stdout by stubbing os.Stdin/Stdout; simplest is to
		// use runStdioTunnel directly after resolving ourselves.
		port, err := fetchSSHPort(ctx, srv.URL, http.Header{"X-Probe": []string{"1"}})
		if err != nil {
			done <- err
			return
		}
		u, err := buildPortForwardURL(srv.URL, port)
		if err != nil {
			done <- err
			return
		}
		done <- runStdioTunnel(ctx, u, http.Header{}, stdinR, &stdout)
	}()

	_, err := stdinW.Write([]byte("ping\n"))
	require.NoError(t, err)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(stdout.String(), "ping\n") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	assert.Contains(t, stdout.String(), "ping\n")

	_ = stdinW.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stdio tunnel did not return")
	}

	// Confirm the probe header reached /api/ssh-port.
	fake.mu.Lock()
	hdr := fake.capturedSSHHdr
	fake.mu.Unlock()
	assert.Equal(t, "1", hdr.Get("X-Probe"))
}
