package tools

import (
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	shellOutputCapBytes = 1 * 1024 * 1024 // 1 MiB per stream
	// watchEventCap bounds the per-shell event log so a crash-looping
	// dev server can't grow the registry unbounded. Agents paginate via
	// since_event_id, so events that fall off the tail stop being
	// visible — we surface a "events truncated" marker in the tool body
	// when this happens so the agent can't silently miss crashes.
	watchEventCap = 1024
)

// NewShellID returns a fresh random shell identifier.
func NewShellID() string { return uuid.NewString() }

// WatchEventType enumerates the kinds of structured events watch_process
// surfaces. Matches the string types the MCP tool body uses verbatim.
type WatchEventType string

// The canonical event-type constants surfaced by watch_process_events.
// Kept as strings so the MCP tool body can emit them verbatim without a
// conversion layer.
const (
	WatchEventStarted     WatchEventType = "started"
	WatchEventError       WatchEventType = "error"
	WatchEventIdleTimeout WatchEventType = "idle_timeout"
	WatchEventExited      WatchEventType = "exited"
)

// WatchEvent is one entry in a watched shell's structured event log. ID
// is 1-based and monotonically increasing; agents pass the max ID they've
// seen back as since_event_id on the next watch_process_events poll to
// avoid re-reading events they already processed.
type WatchEvent struct {
	ID   int
	Type WatchEventType
	At   time.Time
	// Pattern is the regex source string that matched this line. Set for
	// WatchEventError; empty otherwise.
	Pattern string
	// Line is the full stderr line for WatchEventError, the kill reason
	// string for WatchEventIdleTimeout, and empty for the other types.
	Line string
	// ExitCode is set on WatchEventExited and is zero-valued on the
	// other types. Use Type to distinguish 0-exit-code from unset.
	ExitCode int
}

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

// Len returns the current number of registered background shells. Feeds the
// sandbox_background_shells Prometheus gauge; lock-held read is fine because
// the registry is not a hot path.
func (r *ShellRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.shells)
}

// BackgroundShell tracks one background-mode Bash invocation. All fields
// are guarded by mu; callers must use the accessor methods.
//
// When constructed via NewWatchedShell, BackgroundShell carries a non-nil
// watch substate carrying regex patterns, an event log, and an
// idle-timeout deadline. Plain-Bash background shells carry nil watch
// state and behave exactly as before — the watch fields are zero
// overhead when unused.
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

	// watch is non-nil only for shells started via watch_process. Kept on
	// the same struct (rather than a parallel WatchedShell type) so the
	// registry, pgid tracking, and KillShell path stay shared.
	watch *watchState
}

// watchState carries the watch_process-specific extensions that plain
// background Bash doesn't need. Every field is guarded by the enclosing
// BackgroundShell.mu.
type watchState struct {
	patterns       []*regexp.Regexp
	patternSources []string
	events         []WatchEvent
	nextEventID    int
	eventsDropped  int
	idleTimeout    time.Duration
	lastOutputAt   time.Time
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

// NewWatchedShell constructs a watch-enabled BackgroundShell. patterns
// is a list of pre-compiled regexes; patternSources is the matching list
// of raw strings (recorded on each matched event so agents can tell which
// pattern fired). idleTimeout == 0 disables the idle watchdog. The
// returned shell is ready for registration and for drain goroutines to
// start appending output / events.
func NewWatchedShell(id, command string, patterns []*regexp.Regexp, patternSources []string, idleTimeout time.Duration) *BackgroundShell {
	sh := &BackgroundShell{
		id:        id,
		command:   command,
		startedAt: time.Now(),
		watch: &watchState{
			patterns:       patterns,
			patternSources: patternSources,
			idleTimeout:    idleTimeout,
			lastOutputAt:   time.Now(),
		},
	}
	sh.appendEventLocked(WatchEventStarted, "", "", 0)
	return sh
}

// IsWatched reports whether this shell was started by watch_process.
func (s *BackgroundShell) IsWatched() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.watch != nil
}

// WatchPatterns returns the regex source strings this shell was configured
// with, or nil for non-watched shells. Intended for display in the tool
// body so agents can see the active filter.
func (s *BackgroundShell) WatchPatterns() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watch == nil {
		return nil
	}
	out := make([]string, len(s.watch.patternSources))
	copy(out, s.watch.patternSources)
	return out
}

// ObserveWatchLine appends a stderr line to the capped buffer AND matches
// it against the configured patterns, emitting a WatchEventError when
// any regex matches. lastOutputAt is refreshed so the idle watchdog
// sees activity. No-ops on non-watched shells — callers that might see
// either flavour (the watched-shell drain goroutine) should use this in
// place of AppendStderr.
//
// line is the line text without the trailing newline; the caller is
// expected to strip it before calling. newline is re-added to the raw
// stderr buffer so BashOutput continues to render the stream exactly as
// the process emitted it.
func (s *BackgroundShell) ObserveWatchLine(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stderr, s.stderrTruncated = appendCapped(s.stderr, []byte(line+"\n"), s.stderrTruncated)
	if s.watch == nil {
		return
	}
	s.watch.lastOutputAt = time.Now()
	for i, re := range s.watch.patterns {
		if re.MatchString(line) {
			src := ""
			if i < len(s.watch.patternSources) {
				src = s.watch.patternSources[i]
			}
			s.appendEventLocked(WatchEventError, src, line, 0)
			return // first match wins; avoid re-reporting the same line for overlapping patterns
		}
	}
}

// ObserveWatchStdout mirrors ObserveWatchLine for stdout, but without
// pattern matching (stderr is where dev-server errors live; stdout is
// normal log). Still refreshes lastOutputAt so long-running processes
// that only emit heartbeat logs on stdout are not killed by the idle
// watchdog.
func (s *BackgroundShell) ObserveWatchStdout(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stdout, s.stdoutTruncated = appendCapped(s.stdout, b, s.stdoutTruncated)
	if s.watch != nil && len(b) > 0 {
		s.watch.lastOutputAt = time.Now()
	}
}

// RecordWatchIdleTimeout stamps an WatchEventIdleTimeout event with the
// given reason. No-op on non-watched shells.
func (s *BackgroundShell) RecordWatchIdleTimeout(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watch == nil {
		return
	}
	s.appendEventLocked(WatchEventIdleTimeout, "", reason, 0)
}

// RecordWatchExit stamps an WatchEventExited event with the given exit
// code. No-op on non-watched shells.
func (s *BackgroundShell) RecordWatchExit(exitCode int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watch == nil {
		return
	}
	s.appendEventLocked(WatchEventExited, "", "", exitCode)
}

// LastOutputAt returns the timestamp of the most recent observed output
// (stdout or stderr). Used by the idle-watchdog goroutine. Zero time on
// non-watched shells.
func (s *BackgroundShell) LastOutputAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watch == nil {
		return time.Time{}
	}
	return s.watch.lastOutputAt
}

// IdleTimeout returns the configured idle-timeout duration (0 means
// disabled). Zero on non-watched shells.
func (s *BackgroundShell) IdleTimeout() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watch == nil {
		return 0
	}
	return s.watch.idleTimeout
}

// Events returns every watch event with ID > sinceID, plus the count of
// events that have been dropped off the tail by the event cap (caller
// surfaces this so the agent knows when it has missed events). Returns
// (nil, 0) on non-watched shells.
func (s *BackgroundShell) Events(sinceID int) ([]WatchEvent, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watch == nil {
		return nil, 0
	}
	var out []WatchEvent
	for _, ev := range s.watch.events {
		if ev.ID > sinceID {
			out = append(out, ev)
		}
	}
	return out, s.watch.eventsDropped
}

// appendEventLocked assumes s.mu is held. Enforces the per-shell event
// cap by dropping the oldest entries when full and incrementing the
// drop counter.
func (s *BackgroundShell) appendEventLocked(t WatchEventType, pattern, line string, exitCode int) {
	if s.watch == nil {
		return
	}
	s.watch.nextEventID++
	ev := WatchEvent{
		ID:       s.watch.nextEventID,
		Type:     t,
		At:       time.Now(),
		Pattern:  pattern,
		Line:     line,
		ExitCode: exitCode,
	}
	if len(s.watch.events) >= watchEventCap {
		// Drop the oldest 10% in one go so we don't re-copy on every
		// append once the cap is reached.
		drop := watchEventCap / 10
		if drop < 1 {
			drop = 1
		}
		s.watch.eventsDropped += drop
		s.watch.events = append([]WatchEvent(nil), s.watch.events[drop:]...)
	}
	s.watch.events = append(s.watch.events, ev)
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
