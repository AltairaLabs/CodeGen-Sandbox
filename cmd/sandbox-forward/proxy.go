package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// proxyConfig groups the resolved proxy subcommand flags.
type proxyConfig struct {
	server    string
	port      int
	localPort int
	ssh       bool
	stdio     bool
	auth      *authFlags
}

// runProxy is the entry point for `sandbox-forward proxy`. It parses flags,
// resolves the target port (issuing /api/ssh-port when --ssh is set), then
// delegates to either stdio-mode tunnelling or the local-listener loop.
func runProxy(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	cfg := &proxyConfig{auth: &authFlags{}}
	fs.StringVar(&cfg.server, "server", "", "sandbox server URL (https://host[:port])")
	fs.IntVar(&cfg.port, "port", 0, "remote port to tunnel to (127.0.0.1:<port> inside sandbox)")
	fs.IntVar(&cfg.localPort, "local-port", 0, "local port to listen on (default: same as --port)")
	fs.BoolVar(&cfg.ssh, "ssh", false, "resolve the SSH port via /api/ssh-port and stdio-tunnel to it (implies --stdio)")
	fs.BoolVar(&cfg.stdio, "stdio", false, "tunnel stdin/stdout instead of opening a local listener")
	cfg.auth.register(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if cfg.server == "" {
		return fmt.Errorf("--server is required")
	}
	if cfg.ssh {
		cfg.stdio = true
	}
	if !cfg.ssh && cfg.port == 0 {
		return fmt.Errorf("--port is required (or pass --ssh to resolve it)")
	}
	if cfg.localPort == 0 {
		cfg.localPort = cfg.port
	}

	headers := http.Header{}
	if err := cfg.auth.Apply(headers); err != nil {
		return err
	}

	port := cfg.port
	if cfg.ssh {
		p, err := fetchSSHPort(ctx, cfg.server, headers)
		if err != nil {
			return fmt.Errorf("resolve ssh port: %w", err)
		}
		port = p
	}

	wsTarget, err := buildPortForwardURL(cfg.server, port)
	if err != nil {
		return fmt.Errorf("build ws url: %w", err)
	}

	if cfg.stdio {
		return runStdioTunnel(ctx, wsTarget, headers, os.Stdin, os.Stdout)
	}
	return runListener(ctx, cfg.localPort, wsTarget, headers)
}

// normalizeServerURL accepts "https://host", "http://host:port", or a bare
// "host[:port]" (defaulting to https) and returns a *url.URL with scheme set
// to http or https.
func normalizeServerURL(raw string) (*url.URL, error) {
	s := strings.TrimRight(raw, "/")
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q (want http or https)", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("missing host in %q", raw)
	}
	return u, nil
}

// buildPortForwardURL converts the server URL into a ws[s]:// URL pointing at
// /api/port-forward?port=N.
func buildPortForwardURL(server string, port int) (string, error) {
	u, err := normalizeServerURL(server)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = "/api/port-forward"
	q := u.Query()
	q.Set("port", strconv.Itoa(port))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// fetchSSHPort issues GET <server>/api/ssh-port with the given headers and
// returns the port from the JSON response.
func fetchSSHPort(ctx context.Context, server string, headers http.Header) (int, error) {
	u, err := normalizeServerURL(server)
	if err != nil {
		return 0, err
	}
	u.Path = "/api/ssh-port"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, err
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return 0, fmt.Errorf("ssh-port returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var body struct {
		Port int `json:"port"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("decode ssh-port: %w", err)
	}
	if body.Port <= 0 {
		return 0, fmt.Errorf("ssh-port returned non-positive port %d", body.Port)
	}
	return body.Port, nil
}

// runListener opens a TCP listener on 127.0.0.1:localPort and, for each
// inbound connection, opens a fresh WebSocket and copies bytes in both
// directions.
func runListener(ctx context.Context, localPort int, wsTarget string, headers http.Header) error {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(localPort))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer func() { _ = ln.Close() }()
	log.Printf("sandbox-forward listening on %s -> %s", ln.Addr(), wsTarget)

	// Close the listener when ctx is cancelled so Accept returns.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				break
			}
			return fmt.Errorf("accept: %w", err)
		}
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			defer func() { _ = c.Close() }()
			if err := tunnelConn(ctx, wsTarget, headers, c, c); err != nil && ctx.Err() == nil {
				log.Printf("tunnel: %v", err)
			}
		}(conn)
	}
	wg.Wait()
	return nil
}

// runStdioTunnel wires stdin/stdout to a single WebSocket tunnel and returns
// when either side closes.
func runStdioTunnel(ctx context.Context, wsTarget string, headers http.Header, in io.Reader, out io.Writer) error {
	return tunnelConn(ctx, wsTarget, headers, in, out)
}

// tunnelConn dials the WS endpoint with headers, then pumps bytes in both
// directions (in -> WS, WS -> out). Returns when either side closes.
func tunnelConn(ctx context.Context, wsTarget string, headers http.Header, in io.Reader, out io.Writer) error {
	c, _, err := websocket.Dial(ctx, wsTarget, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	// Allow large frames — ssh can burst sizable packets.
	c.SetReadLimit(1 << 24)

	tunnelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	// in -> WS
	go func() {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, rerr := in.Read(buf)
			if n > 0 {
				if werr := c.Write(tunnelCtx, websocket.MessageBinary, buf[:n]); werr != nil {
					cancel()
					return
				}
			}
			if rerr != nil {
				// Stdin EOF or conn closed: close the WS so the other side unblocks.
				_ = c.Close(websocket.StatusNormalClosure, "")
				cancel()
				return
			}
		}
	}()

	// WS -> out
	go func() {
		defer wg.Done()
		for {
			typ, data, rerr := c.Read(tunnelCtx)
			if rerr != nil {
				cancel()
				return
			}
			if typ != websocket.MessageBinary {
				continue
			}
			if _, werr := out.Write(data); werr != nil {
				cancel()
				return
			}
		}
	}()

	wg.Wait()
	_ = c.CloseNow()
	return nil
}
