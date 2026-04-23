package tools

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const shellOutputCapBytes = 1 * 1024 * 1024 // 1 MiB per stream

// NewShellID returns a fresh random shell identifier.
func NewShellID() string { return uuid.NewString() }

// ShellRegistry is a goroutine-safe map of shell ID to BackgroundShell.
type ShellRegistry struct {
	mu     sync.RWMutex
	shells map[string]*BackgroundShell
}

// NewShellRegistry constructs an empty registry.
func NewShellRegistry() *ShellRegistry {
	return &ShellRegistry{shells: make(map[string]*BackgroundShell)}
}

// Register adds shell to the registry under shell.ID().
func (r *ShellRegistry) Register(shell *BackgroundShell) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shells[shell.ID()] = shell
}

// Get returns the shell for id, or (nil, false) if absent.
func (r *ShellRegistry) Get(id string) (*BackgroundShell, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sh, ok := r.shells[id]
	return sh, ok
}

// Remove deletes the shell with the given id (no-op if absent).
func (r *ShellRegistry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.shells, id)
}

// BackgroundShell tracks one background-mode Bash invocation. All fields
// are guarded by mu; callers must use the accessor methods.
type BackgroundShell struct {
	mu sync.Mutex

	id        string
	command   string
	startedAt time.Time
	pgid      int

	stdout          []byte
	stdoutTruncated bool
	stderr          []byte
	stderrTruncated bool

	exitCode *int
}

// NewBackgroundShell constructs a BackgroundShell with the given id and
// command, timestamped now. Output buffers and exit code start empty; the
// caller (typically bash.handleBashBackground) wires up reader goroutines
// before returning to the MCP caller.
func NewBackgroundShell(id, command string) *BackgroundShell {
	return &BackgroundShell{
		id:        id,
		command:   command,
		startedAt: time.Now(),
	}
}

// ID returns the shell's unique identifier.
func (s *BackgroundShell) ID() string { return s.id }

// Command returns the original command string the shell was launched with.
func (s *BackgroundShell) Command() string { return s.command }

// StartedAt returns the wall-clock time at which the shell was registered.
func (s *BackgroundShell) StartedAt() time.Time { return s.startedAt }

// Pgid returns the process group ID (0 until SetPgid has been called).
func (s *BackgroundShell) Pgid() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pgid
}

// SetPgid records the process group leader's pid, which doubles as the
// group ID after Setpgid.
func (s *BackgroundShell) SetPgid(pgid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pgid = pgid
}

// AppendStdout appends b to the captured stdout, capped at shellOutputCapBytes.
func (s *BackgroundShell) AppendStdout(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stdout, s.stdoutTruncated = appendCapped(s.stdout, b, s.stdoutTruncated)
}

// AppendStderr appends b to the captured stderr, capped at shellOutputCapBytes.
func (s *BackgroundShell) AppendStderr(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stderr, s.stderrTruncated = appendCapped(s.stderr, b, s.stderrTruncated)
}

func appendCapped(dst, src []byte, alreadyTruncated bool) ([]byte, bool) {
	if alreadyTruncated {
		return dst, true
	}
	remaining := shellOutputCapBytes - len(dst)
	if remaining <= 0 {
		return dst, true
	}
	if len(src) <= remaining {
		return append(dst, src...), false
	}
	return append(dst, src[:remaining]...), true
}

// SetExit marks the shell as completed with the given exit code.
func (s *BackgroundShell) SetExit(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exitCode = &code
}

// Snapshot atomically reads the current state. stdout/stderr are returned
// by copy so callers can mutate them safely. exitCode is nil while running.
func (s *BackgroundShell) Snapshot() (stdout, stderr []byte, stdoutTruncated, stderrTruncated bool, exitCode *int, running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stdout = append([]byte(nil), s.stdout...)
	stderr = append([]byte(nil), s.stderr...)
	stdoutTruncated = s.stdoutTruncated
	stderrTruncated = s.stderrTruncated
	if s.exitCode != nil {
		ec := *s.exitCode
		exitCode = &ec
	}
	running = s.exitCode == nil
	return
}
