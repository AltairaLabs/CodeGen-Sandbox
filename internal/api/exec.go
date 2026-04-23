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

// execSession owns the per-request state for an /api/exec WebSocket: the PTY
// master, the WebSocket connection, the serialised writer, and a WaitGroup for
// the two pump goroutines.
type execSession struct {
	c       *websocket.Conn
	ptmx    *os.File
	cmd     *exec.Cmd
	pgid    int
	writeMu sync.Mutex
	pumpWG  sync.WaitGroup
	ptyDone chan error
	wsDone  chan error
}

// runExecSession is the core loop: Start bash in a PTY, fan out PTY stdout
// to the WS, fan in WS stdin/resize frames to the PTY. Returns the child's
// exit code (or -1 if it never ran) and the first non-clean error, if any.
func runExecSession(ctx context.Context, c *websocket.Conn, workdir string, onStart func(pid int)) (int, error) {
	sess, err := startExecSession(c, workdir)
	if err != nil {
		return -1, err
	}
	defer func() { _ = sess.ptmx.Close() }()
	// Guarantee the process group is gone on every exit path — kill is a
	// best-effort; by the time it fires the group may already be reaped.
	defer func() { _ = syscall.Kill(-sess.pgid, syscall.SIGKILL) }()

	if onStart != nil {
		onStart(sess.pgid)
	}

	sess.pumpWG.Add(2)
	go sess.pumpPTYToWS(ctx)
	go sess.pumpWSToPTY(ctx)

	clientClosed, sessionErr := sess.awaitTermination(ctx)

	// Tear down the child first so cmd.Wait returns promptly.
	_ = sess.ptmx.Close()
	_ = syscall.Kill(-sess.pgid, syscall.SIGKILL)

	code := sess.waitExitCode(clientClosed)
	sess.writeExitAndClose(code, clientClosed)

	// Drain both pumps so they don't outlive the handler. WaitGroup avoids
	// a deadlock where one error channel has no further writer.
	sess.pumpWG.Wait()
	return code, sessionErr
}

// startExecSession spawns /bin/bash under a PTY and returns the session state.
func startExecSession(c *websocket.Conn, workdir string) (*execSession, error) {
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
		return nil, err
	}
	return &execSession{
		c:       c,
		ptmx:    ptmx,
		cmd:     cmd,
		pgid:    cmd.Process.Pid,
		ptyDone: make(chan error, 1),
		wsDone:  make(chan error, 1),
	}, nil
}

// writeFrame serialises WS writes. coder/websocket forbids concurrent writers.
func (s *execSession) writeFrame(ctx context.Context, f execFrame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.c.Write(ctx, websocket.MessageText, b)
}

func (s *execSession) pumpPTYToWS(ctx context.Context) {
	defer s.pumpWG.Done()
	buf := make([]byte, 32*1024)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			frame := execFrame{
				Type: "stdout",
				Data: base64.StdEncoding.EncodeToString(buf[:n]),
			}
			if werr := s.writeFrame(ctx, frame); werr != nil {
				s.ptyDone <- werr
				return
			}
		}
		if err != nil {
			s.ptyDone <- err
			return
		}
	}
}

// pumpWSToPTY reads WS frames and routes them to the PTY. coder/websocket
// treats a context cancel on c.Read as a fatal error, so this goroutine is
// unblocked by closing the PTY/WS rather than cancelling the context.
func (s *execSession) pumpWSToPTY(ctx context.Context) {
	defer s.pumpWG.Done()
	for {
		typ, data, err := s.c.Read(ctx)
		if err != nil {
			s.wsDone <- err
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var f execFrame
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		if werr := s.applyClientFrame(f); werr != nil {
			s.wsDone <- werr
			return
		}
	}
}

// applyClientFrame handles one parsed frame from the client. Returns a
// non-nil error only when the session must terminate (PTY write failed).
func (s *execSession) applyClientFrame(f execFrame) error {
	switch f.Type {
	case "stdin":
		raw, derr := base64.StdEncoding.DecodeString(f.Data)
		if derr != nil {
			return nil
		}
		if _, werr := s.ptmx.Write(raw); werr != nil {
			return werr
		}
	case "resize":
		if f.Cols == 0 || f.Rows == 0 {
			return nil
		}
		_ = pty.Setsize(s.ptmx, &pty.Winsize{Cols: f.Cols, Rows: f.Rows})
	}
	return nil
}

// awaitTermination blocks until bash exits, the client disconnects, or the
// request context is cancelled. Returns whether the client closed cleanly
// and the session error (if any).
func (s *execSession) awaitTermination(ctx context.Context) (bool, error) {
	select {
	case err := <-s.ptyDone:
		if err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
		return false, nil
	case err := <-s.wsDone:
		if isCleanClose(err) {
			return true, nil
		}
		return false, err
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// waitExitCode reaps the child and returns the exit code, normalising
// signal-kill (< 0) to 0 on client-initiated close.
func (s *execSession) waitExitCode(clientClosed bool) int {
	werr := s.cmd.Wait()
	if werr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if !errors.As(werr, &exitErr) {
		return -1
	}
	code := exitErr.ExitCode()
	if code < 0 && clientClosed {
		return 0
	}
	return code
}

// writeExitAndClose sends the final exit frame with a bounded context so a
// parent cancel doesn't drop it, then closes the WS normally.
func (s *execSession) writeExitAndClose(code int, clientClosed bool) {
	wctx, wcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer wcancel()
	if werr := s.writeFrame(wctx, execFrame{Type: "exit", Code: code}); werr != nil {
		if !clientClosed {
			log.Printf("api exec: write exit frame: %v", werr)
		}
	}
	_ = s.c.Close(websocket.StatusNormalClosure, "")
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
