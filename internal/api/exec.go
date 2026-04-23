package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// execFrame is the wire format for /api/exec. Binary stdin/stdout payloads
// are base64-encoded so the envelope stays valid JSON text.
type execFrame struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	Code int    `json:"code,omitempty"`
}

// execHandler returns an http.Handler that upgrades /api/exec to a
// WebSocket, spawns /bin/bash inside ws.Root() as a pseudo-terminal, and
// pipes stdin/stdout between the client and the PTY.
func execHandler(ws *workspace.Workspace) http.Handler {
	return execHandlerWithHook(ws, nil)
}

// execHandlerWithHook is execHandler with a test hook invoked with the
// bash child pid as soon as it starts. Production callers use execHandler.
func execHandlerWithHook(ws *workspace.Workspace, onStart func(pid int)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			// Accept already wrote a response on failure.
			return
		}
		// Use a named "normal closure" on the happy path so we don't leak
		// the ungraceful frame from CloseNow.
		defer func() { _ = c.CloseNow() }()

		sub := "unknown"
		if id, ok := FromContext(r.Context()); ok {
			sub = id.Sub
		}
		start := time.Now()

		code, err := runExecSession(r.Context(), c, ws.Root(), onStart)
		dur := time.Since(start)
		if err != nil && !isCleanClose(err) {
			log.Printf("api exec sub=%s duration=%s error=%v", sub, dur, err)
			return
		}
		log.Printf("api exec sub=%s duration=%s exit=%d", sub, dur, code)
	})
}

// runExecSession is the core loop: Start bash in a PTY, fan out PTY stdout
// to the WS, fan in WS stdin/resize frames to the PTY. Returns the child's
// exit code (or -1 if it never ran) and the first non-clean error, if any.
func runExecSession(ctx context.Context, c *websocket.Conn, workdir string, onStart func(pid int)) (int, error) {
	cmd := exec.Command("/bin/bash")
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"HOME="+workdir,
	)
	// pty.Start sets Setsid=true (and Setctty=true) before Start, which
	// puts bash in a new session and a new process group with pgid == pid.
	// That gives us the same process-group cleanup guarantee as
	// internal/tools/bash.go without the `Setpgid: true` flag — stacking
	// Setsid+Setpgid is rejected on darwin as "operation not permitted".

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return -1, err
	}
	defer func() { _ = ptmx.Close() }()

	pgid := cmd.Process.Pid
	if onStart != nil {
		onStart(pgid)
	}
	// Guarantee the process group is gone on every exit path — kill is a
	// best-effort; by the time it fires the group may already be reaped.
	defer func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}()

	// writeMu serialises all writes to the WS connection. The coder/websocket
	// contract forbids concurrent writers.
	var writeMu sync.Mutex
	writeFrame := func(ctx context.Context, f execFrame) error {
		b, err := json.Marshal(f)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		return c.Write(ctx, websocket.MessageText, b)
	}

	// Pump goroutines are tracked by a WaitGroup so we can cleanly drain
	// both at handler exit. Errors surface on buffered channels — only the
	// first event matters for the main select, which is why they're sized 1.
	var pumpWG sync.WaitGroup
	pumpWG.Add(2)

	// PTY → WS pump.
	ptyDone := make(chan error, 1)
	go func() {
		defer pumpWG.Done()
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				frame := execFrame{
					Type: "stdout",
					Data: base64.StdEncoding.EncodeToString(buf[:n]),
				}
				if werr := writeFrame(ctx, frame); werr != nil {
					ptyDone <- werr
					return
				}
			}
			if err != nil {
				ptyDone <- err
				return
			}
		}
	}()

	// WS → PTY pump. coder/websocket treats a context cancel on c.Read as
	// a fatal error and closes the connection, so we cannot cancel the
	// Read to unblock this goroutine — we must unblock it by closing the
	// PTY (which drops the child, reaping it) and then closing the WS
	// directly. That is handled below once either (a) bash exits
	// (ptyDone) or (b) this loop exits on its own.
	wsDone := make(chan error, 1)
	go func() {
		defer pumpWG.Done()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				wsDone <- err
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var f execFrame
			if json.Unmarshal(data, &f) != nil {
				// Ignore malformed frames — keep session alive.
				continue
			}
			switch f.Type {
			case "stdin":
				raw, derr := base64.StdEncoding.DecodeString(f.Data)
				if derr != nil {
					continue
				}
				if _, werr := ptmx.Write(raw); werr != nil {
					wsDone <- werr
					return
				}
			case "resize":
				if f.Cols == 0 || f.Rows == 0 {
					continue
				}
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: f.Cols, Rows: f.Rows})
			default:
				// Ignore unknown frames.
			}
		}
	}()

	// Block until either bash exits, the client disconnects, or the
	// request context is cancelled (sandbox shutdown).
	var sessionErr error
	var clientClosed bool
	select {
	case err := <-ptyDone:
		if err != nil && !errors.Is(err, io.EOF) {
			sessionErr = err
		}
	case err := <-wsDone:
		clientClosed = isCleanClose(err)
		if !clientClosed {
			sessionErr = err
		}
	case <-ctx.Done():
		sessionErr = ctx.Err()
	}

	// Tear down the child first so cmd.Wait returns promptly: close PTY
	// then SIGKILL the process group. Best-effort; the group may already
	// be reaped (e.g. bash just ran `exit`).
	_ = ptmx.Close()
	_ = syscall.Kill(-pgid, syscall.SIGKILL)

	code := 0
	if werr := cmd.Wait(); werr != nil {
		var exitErr *exec.ExitError
		if errors.As(werr, &exitErr) {
			code = exitErr.ExitCode()
			if code < 0 {
				// Signal-killed (SIGKILL from cleanup); treat as clean on
				// client-initiated close.
				if clientClosed {
					code = 0
				}
			}
		} else {
			code = -1
		}
	}

	// Write the final exit frame BEFORE closing the WS. Use a fresh,
	// bounded context so a parent-ctx cancellation (sandbox shutdown)
	// doesn't silently drop the last frame on the floor.
	wctx, wcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer wcancel()
	if werr := writeFrame(wctx, execFrame{Type: "exit", Code: code}); werr != nil {
		// If the client closed first, this is expected.
		if !clientClosed {
			log.Printf("api exec: write exit frame: %v", werr)
		}
	}
	_ = c.Close(websocket.StatusNormalClosure, "")

	// Drain both pump goroutines so they don't outlive the handler. Using a
	// WaitGroup (vs receiving from each error channel) avoids a deadlock:
	// the error channels are buffered-by-1 and each goroutine sends once,
	// so the channel whose value we already consumed in the select above
	// has no further writer and a second receive would block forever.
	pumpWG.Wait()
	return code, sessionErr
}

// isCleanClose returns true for the expected end-of-session signals:
// normal WS close, context cancellation (sandbox shutdown), or EOF.
func isCleanClose(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	status := websocket.CloseStatus(err)
	switch status {
	case websocket.StatusNormalClosure,
		websocket.StatusGoingAway,
		websocket.StatusNoStatusRcvd:
		return true
	}
	return false
}
