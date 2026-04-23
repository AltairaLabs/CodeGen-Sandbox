package api

// Why embed an SSH server instead of shelling out to sshd?
//
// The invariant we rely on is that the SSH listener is bound to 127.0.0.1 and
// is reachable only through the already-authenticated /api/port-forward
// WebSocket tunnel. Running an in-process SSH server lets us:
//
//   - generate an ephemeral ed25519 host key at startup (no on-disk keys),
//   - authorize keys via the existing HTTP identity layer (POST
//     /api/ssh-authorized-keys with a caller's Sub) instead of managing
//     ~/.ssh/authorized_keys for a synthetic user,
//   - avoid shipping sshd / PAM / pam_unix configuration into the image, and
//   - cleanly tie the SSH server lifecycle to the api http.Server.
//
// gliderlabs/ssh wraps golang.org/x/crypto/ssh and gives us a Session
// abstraction with PTY negotiation and exec/shell mode already handled.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/creack/pty"
	gliderssh "github.com/gliderlabs/ssh"
	gossh "golang.org/x/crypto/ssh"
)

// sshSubjectContextKey is the gliderlabs/ssh context key under which the
// matching subject is stashed during public-key auth so the session handler
// can emit it for audit.
const sshSubjectContextKey = "sandbox-sub"

// authorizedKeys is a thread-safe store of authorized SSH public keys keyed
// by the uploader's identity subject.
type authorizedKeys struct {
	mu   sync.RWMutex
	keys map[string][]gossh.PublicKey
}

func newAuthorizedKeys() *authorizedKeys {
	return &authorizedKeys{keys: map[string][]gossh.PublicKey{}}
}

// Add records key under sub. Duplicate keys for the same subject are not
// de-duplicated (each registration appends; fine for an ephemeral process).
func (a *authorizedKeys) Add(sub string, key gossh.PublicKey) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keys[sub] = append(a.keys[sub], key)
}

// Match returns the subject associated with the first stored key equal to
// the given key, or ("", false) if no match.
func (a *authorizedKeys) Match(key gossh.PublicKey) (string, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for sub, ks := range a.keys {
		for _, k := range ks {
			if gliderssh.KeysEqual(k, key) {
				return sub, true
			}
		}
	}
	return "", false
}

// CountForSubject returns the number of registered keys for sub.
func (a *authorizedKeys) CountForSubject(sub string) int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.keys[sub])
}

// sshServer is the embedded SSH server: a loopback TCP listener + a
// gliderlabs/ssh Server wired to an authorizedKeys store.
type sshServer struct {
	ws       *workspace.Workspace
	listener net.Listener
	addr     string // 127.0.0.1:N
	keys     *authorizedKeys
	hostKey  gossh.Signer
	srv      *gliderssh.Server
	serveErr chan error
}

// newSSHServer binds 127.0.0.1:0, generates an ephemeral ed25519 host key,
// configures a gliderlabs ssh.Server with public-key auth against the
// returned authorizedKeys store, and starts serving in a goroutine.
// Callers close the server by invoking Close.
func newSSHServer(ws *workspace.Workspace) (*sshServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("ssh listen: %w", err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("ssh host key: %w", err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("ssh signer: %w", err)
	}

	s := &sshServer{
		ws:       ws,
		listener: ln,
		addr:     ln.Addr().String(),
		keys:     newAuthorizedKeys(),
		hostKey:  signer,
		serveErr: make(chan error, 1),
	}

	s.srv = &gliderssh.Server{
		Handler: s.handleSession,
		PublicKeyHandler: func(ctx gliderssh.Context, key gliderssh.PublicKey) bool {
			sub, ok := s.keys.Match(key)
			if !ok {
				return false
			}
			ctx.SetValue(sshSubjectContextKey, sub)
			return true
		},
	}
	s.srv.AddHostKey(signer)

	go func() {
		err := s.srv.Serve(ln)
		if errors.Is(err, gliderssh.ErrServerClosed) {
			err = nil
		}
		s.serveErr <- err
	}()

	return s, nil
}

// Addr returns the SSH listener's loopback address (127.0.0.1:N).
func (s *sshServer) Addr() string { return s.addr }

// Port parses and returns the listener's TCP port.
func (s *sshServer) Port() int {
	if tcp, ok := s.listener.Addr().(*net.TCPAddr); ok {
		return tcp.Port
	}
	return 0
}

// Close stops accepting new connections and closes the listener. Active
// sessions are terminated.
func (s *sshServer) Close() error {
	if s.srv == nil {
		return nil
	}
	err := s.srv.Close()
	// Drain the serve goroutine so the test doesn't leak it.
	<-s.serveErr
	return err
}

// handleSession is invoked by gliderlabs/ssh once a session's shell or exec
// request is accepted. It forwards stdin/stdout/stderr to /bin/bash, using a
// PTY if the client requested one.
func (s *sshServer) handleSession(sess gliderssh.Session) {
	sub, _ := sess.Context().Value(sshSubjectContextKey).(string)
	if sub == "" {
		sub = "unknown"
	}
	start := time.Now()
	code, err := s.runSession(sess, sub)
	dur := time.Since(start)
	if err != nil {
		log.Printf("api ssh sub=%s duration=%s error=%v", sub, dur, err)
	}
	log.Printf("api ssh sub=%s duration=%s exit=%d", sub, dur, code)
	_ = sess.Exit(code)
}

// runSession runs /bin/bash bound to the session. Returns the exit code and
// the first non-clean error encountered.
func (s *sshServer) runSession(sess gliderssh.Session, sub string) (int, error) {
	ptyReq, winch, isPty := sess.Pty()

	args := []string{}
	cmdStr := sess.RawCommand()
	if cmdStr != "" {
		args = append(args, "-c", cmdStr)
	}
	cmd := exec.Command("/bin/bash", args...)
	cmd.Dir = s.ws.Root()
	env := append(os.Environ(),
		"HOME="+s.ws.Root(),
		"SANDBOX_USER="+sub,
	)
	if isPty {
		term := ptyReq.Term
		if term == "" {
			term = "xterm-256color"
		}
		env = append(env, "TERM="+term)
	}
	cmd.Env = env

	if isPty {
		return s.runPTY(sess, cmd, winch)
	}
	return s.runExec(sess, cmd)
}

// runPTY runs cmd under a PTY and forwards the session stdio. Window-change
// requests from the client resize the PTY.
func (s *sshServer) runPTY(sess gliderssh.Session, cmd *exec.Cmd, winch <-chan gliderssh.Window) (int, error) {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return -1, fmt.Errorf("pty start: %w", err)
	}
	defer func() { _ = ptmx.Close() }()

	pgid := cmd.Process.Pid
	defer func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) }()

	// Forward window size changes. Closes when the session ends.
	go func() {
		for win := range winch {
			_ = pty.Setsize(ptmx, &pty.Winsize{
				Cols: uint16(win.Width),  //nolint:gosec // window width bounded
				Rows: uint16(win.Height), //nolint:gosec // window height bounded
			})
		}
	}()

	// Pump session -> PTY.
	go func() {
		_, _ = io.Copy(ptmx, sess)
		// Closing PTY's master unblocks the child and the reader below.
		_ = ptmx.Close()
	}()

	// Pump PTY -> session (blocks until PTY closes).
	_, _ = io.Copy(sess, ptmx)

	return waitExit(cmd), nil
}

// runExec runs cmd without a PTY, wiring the session streams directly.
// gliderlabs/ssh multiplexes stderr over the channel's Stderr() pipe.
func (s *sshServer) runExec(sess gliderssh.Session, cmd *exec.Cmd) (int, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return -1, err
	}
	cmd.Stdout = sess
	cmd.Stderr = sess.Stderr()

	if err := cmd.Start(); err != nil {
		return -1, err
	}
	pgid := cmd.Process.Pid
	defer func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) }()

	go func() {
		_, _ = io.Copy(stdin, sess)
		_ = stdin.Close()
	}()

	return waitExit(cmd), nil
}

// waitExit returns cmd's exit code, or -1 if it could not be started.
func waitExit(cmd *exec.Cmd) int {
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		return -1
	}
	return 0
}

// authorizedKeysHandler returns the POST /api/ssh-authorized-keys handler.
// Body: {"public_key": "ssh-ed25519 AAAA..."} — the key is parsed via
// ssh.ParseAuthorizedKey and stored under the identity subject.
func authorizedKeysHandler(keys *authorizedKeys) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id, ok := FromContext(r.Context())
		if !ok {
			http.Error(w, "identity required", http.StatusUnauthorized)
			return
		}

		var body struct {
			PublicKey string `json:"public_key"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if body.PublicKey == "" {
			http.Error(w, "public_key is required", http.StatusBadRequest)
			return
		}

		pk, _, _, _, err := gossh.ParseAuthorizedKey([]byte(body.PublicKey))
		if err != nil {
			http.Error(w, "invalid public key: "+err.Error(), http.StatusBadRequest)
			return
		}

		keys.Add(id.Sub, pk)
		log.Printf("api ssh-authorized-keys sub=%s type=%s", id.Sub, pk.Type())
		w.WriteHeader(http.StatusCreated)
	})
}

// sshPortHandler returns the GET /api/ssh-port handler.
func sshPortHandler(s *sshServer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"port": s.Port()})
	})
}
