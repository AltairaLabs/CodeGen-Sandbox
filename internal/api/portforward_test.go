//go:build linux || darwin

package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// startEchoServer starts a trivial TCP echo server on 127.0.0.1 and returns its
// port plus a WaitGroup that the test can use to confirm the accept loop has
// exited (i.e. no connection goroutines leaked).
func startEchoServer(t *testing.T) (port int, accepted *sync.WaitGroup) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	accepted = &sync.WaitGroup{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			go func(c net.Conn) {
				defer accepted.Done()
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	return ln.Addr().(*net.TCPAddr).Port, accepted
}

// freeClosedPort returns a port that was momentarily bound and then closed, so
// the test can dial it and expect "connection refused".
func freeClosedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

func dialPortForward(t *testing.T, srvURL string, port string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u := wsURL(srvURL) + "/api/port-forward?port=" + port
	return websocket.Dial(ctx, u, nil)
}

func TestPortForward_HappyPath_Echo(t *testing.T) {
	port, _ := startEchoServer(t)

	srv := httptest.NewServer(portForwardHandler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, _, err := dialPortForward(t, srv.URL, strconv.Itoa(port))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.CloseNow() })

	payload := []byte("hello port-forward\n")
	require.NoError(t, c.Write(ctx, websocket.MessageBinary, payload))

	// Read back what the echo server returned — may arrive in fragments.
	got := make([]byte, 0, len(payload))
	for len(got) < len(payload) {
		typ, data, err := c.Read(ctx)
		require.NoError(t, err)
		require.Equal(t, websocket.MessageBinary, typ)
		got = append(got, data...)
	}
	require.Equal(t, payload, got[:len(payload)])

	require.NoError(t, c.Close(websocket.StatusNormalClosure, ""))
}

func TestPortForward_InvalidPort(t *testing.T) {
	srv := httptest.NewServer(portForwardHandler())
	t.Cleanup(srv.Close)

	cases := []struct {
		name  string
		query string
	}{
		{"missing", ""},
		{"non-numeric", "?port=abc"},
		{"privileged-22", "?port=22"},
		{"too-large", "?port=70000"},
		{"zero", "?port=0"},
		{"negative", "?port=-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/port-forward"+tc.query, nil)
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			t.Cleanup(func() { _ = resp.Body.Close() })
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestPortForward_DialFailure_ClosesCleanly(t *testing.T) {
	port := freeClosedPort(t)

	srv := httptest.NewServer(portForwardHandler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Dial should succeed (upgrade happens before the TCP dial), but the
	// server must close the WS promptly once the dial fails.
	c, _, err := dialPortForward(t, srv.URL, strconv.Itoa(port))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.CloseNow() })

	// A read must return a close error within the context deadline — the
	// handler must not hang.
	_, _, err = c.Read(ctx)
	require.Error(t, err)
	require.NotEqual(t, context.DeadlineExceeded, ctx.Err(), "handler hung on dial failure")
}

func TestPortForward_ClientClosesFirst_NoGoroutineLeak(t *testing.T) {
	port, accepted := startEchoServer(t)

	srv := httptest.NewServer(portForwardHandler())
	t.Cleanup(srv.Close)

	before := runtime.NumGoroutine()

	for i := 0; i < 3; i++ {
		c, _, err := dialPortForward(t, srv.URL, strconv.Itoa(port))
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		require.NoError(t, c.Write(ctx, websocket.MessageBinary, []byte("ping\n")))
		// Read at least one echo frame to guarantee both pumps are live.
		_, _, err = c.Read(ctx)
		require.NoError(t, err)
		cancel()

		// Client closes first.
		require.NoError(t, c.Close(websocket.StatusNormalClosure, ""))
	}

	// Give the server a moment to tear down.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Also wait for the echo server's per-connection handlers to exit —
	// confirms the TCP side was closed when the WS closed.
	done := make(chan struct{})
	go func() {
		accepted.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("echo server connections still open — TCP not closed after WS close")
	}

	after := runtime.NumGoroutine()
	// Allow a small slack for runtime-managed goroutines.
	require.LessOrEqualf(t, after, before+4, "goroutine leak: before=%d after=%d", before, after)
}

func TestPortForward_TCPClosesFirst(t *testing.T) {
	// Listener that closes the connection immediately after one byte is read.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 1)
		_, _ = conn.Read(buf)
		_ = conn.Close()
	}()

	srv := httptest.NewServer(portForwardHandler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, _, err := dialPortForward(t, srv.URL, strconv.Itoa(port))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.CloseNow() })

	require.NoError(t, c.Write(ctx, websocket.MessageBinary, []byte("x")))

	// Drain until close.
	deadline := time.Now().Add(3 * time.Second)
	var sawClose bool
	for time.Now().Before(deadline) {
		_, _, err := c.Read(ctx)
		if err != nil {
			sawClose = true
			break
		}
	}
	require.True(t, sawClose, "WS did not close after TCP backend closed")
}
