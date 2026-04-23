package api

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const (
	portForwardMinPort  = 1024
	portForwardMaxPort  = 65535
	portForwardDialWait = 5 * time.Second
	portForwardBufSize  = 32 * 1024
)

// portForwardHandler returns an http.Handler for /api/port-forward. It upgrades
// the request to a WebSocket and bridges binary frames to a TCP connection on
// 127.0.0.1:<port>.
//
// Security invariant: the target host is hard-coded to 127.0.0.1 (loopback).
// There is no host query parameter and none can be added — this endpoint must
// never become an outbound SSRF proxy. Only services listening inside the
// sandbox are reachable.
func portForwardHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		port, ok := parsePortForwardPort(r.URL.Query().Get("port"))
		if !ok {
			http.Error(w, "invalid port", http.StatusBadRequest)
			return
		}

		sub := "unknown"
		if id, ok := FromContext(r.Context()); ok {
			sub = id.Sub
		}

		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			// Accept already wrote a response on failure.
			return
		}
		defer func() { _ = c.CloseNow() }()

		start := time.Now()

		dialCtx, cancel := context.WithTimeout(r.Context(), portForwardDialWait)
		var d net.Dialer
		// Loopback-only by design; see security invariant on this handler.
		tcp, err := d.DialContext(dialCtx, "tcp", "127.0.0.1:"+strconv.Itoa(port))
		cancel()
		if err != nil {
			log.Printf("api port-forward sub=%s port=%d dial-error=%v", sub, port, err)
			_ = c.Close(websocket.StatusInternalError, "dial failed")
			return
		}

		bytesIn, bytesOut := runPortForward(r.Context(), c, tcp)
		dur := time.Since(start)
		log.Printf("api port-forward sub=%s port=%d in=%d out=%d dur=%s", sub, port, bytesIn, bytesOut, dur)
	})
}

// parsePortForwardPort validates the port query-string value. Returns the
// parsed port and true on success, or (0, false) if missing / non-numeric /
// outside the [1024, 65535] range.
func parsePortForwardPort(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	if n < portForwardMinPort || n > portForwardMaxPort {
		return 0, false
	}
	return n, true
}

// runPortForward pumps bytes between the WebSocket and the TCP connection in
// both directions until either side closes. It returns (bytesIn, bytesOut)
// measured in bytes written to TCP (in) and bytes written to WS (out). Both
// pump goroutines exit before this function returns.
func runPortForward(ctx context.Context, c *websocket.Conn, tcp net.Conn) (int64, int64) {
	var bytesIn, bytesOut atomic.Int64

	var wg sync.WaitGroup
	wg.Add(2)

	// WS -> TCP: read binary frames from the WS, write to TCP.
	go func() {
		defer wg.Done()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				// Closing the TCP side unblocks the other pump.
				_ = tcp.Close()
				return
			}
			if typ != websocket.MessageBinary {
				// Ignore non-binary frames (e.g. stray text).
				continue
			}
			n, werr := tcp.Write(data)
			if n > 0 {
				bytesIn.Add(int64(n))
			}
			if werr != nil {
				_ = tcp.Close()
				return
			}
		}
	}()

	// TCP -> WS: read from TCP, write binary frames to WS.
	go func() {
		defer wg.Done()
		buf := make([]byte, portForwardBufSize)
		for {
			n, err := tcp.Read(buf)
			if n > 0 {
				if c.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
					_ = tcp.Close()
					return
				}
				bytesOut.Add(int64(n))
			}
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
					// Log non-EOF reads for diagnostics; still fall through
					// to close the WS so the client learns the session ended.
					log.Printf("api port-forward tcp read: %v", err)
				}
				_ = c.Close(websocket.StatusNormalClosure, "")
				return
			}
		}
	}()

	wg.Wait()
	// Belt-and-braces: make sure both sides are closed on return.
	_ = tcp.Close()
	return bytesIn.Load(), bytesOut.Load()
}
