package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// defaultWatchPatterns is the baseline regex set that watch_process uses
// when the agent doesn't supply its own. Tuned to the common Node /
// Python / Go dev-server crash signatures — agents free to override.
var defaultWatchPatterns = []string{
	`^Error:`,
	`^Fatal:`,
	`^panic:`,
	`\buncaught\b`,
	`UnhandledPromiseRejection`,
}

const (
	maxWatchIdleTimeoutSec = 3600
	maxWatchPatterns       = 32
	// watchIdleCheckInterval is how often the idle-watchdog goroutine
	// compares lastOutputAt against the configured deadline. Short
	// enough that a 5s idle timeout still fires within a second or so;
	// long enough that a long-idle shell doesn't burn CPU on wakeups.
	watchIdleCheckInterval = 1 * time.Second
)

// RegisterWatchProcess registers the watch_process tool on the given MCP
// server. Complements Bash(run_in_background=true) + BashOutput for the
// "tail a dev server and tell me when it crashes" workflow that plain
// byte-level polling can't satisfy cleanly.
func RegisterWatchProcess(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("watch_process",
		mcp.WithDescription(
			"Spawn a long-running command in the background with regex-based crash surfacing on stderr. "+
				"Returns a shell_id immediately. Poll watch_process_events for structured events "+
				"(started / error / idle_timeout / exited). Use KillShell to terminate. Raw stdout+stderr "+
				"are still available via BashOutput for the same shell_id. "+
				"Registered under the same registry as Bash background shells, so shell_id semantics match.",
		),
		mcp.WithString("command", mcp.Required(),
			mcp.Description("Shell command to run (passed to /bin/bash -c).")),
		mcp.WithString("description", mcp.Required(),
			mcp.Description("5-10 word description of what this watcher is for. Recorded for agent context; does not affect execution.")),
		mcp.WithArray("error_patterns",
			mcp.Description(fmt.Sprintf(
				"Optional list of Go-regex strings matched against each stderr line. A match emits a structured \"error\" event. "+
					"Defaults to %q. Cap: %d patterns. Submit an empty list to disable pattern matching entirely.",
				defaultWatchPatterns, maxWatchPatterns,
			)),
			mcp.Items(map[string]any{"type": "string"})),
		mcp.WithNumber("idle_timeout_seconds",
			mcp.Description(fmt.Sprintf(
				"If > 0, kill the process when no output has been observed for this many seconds (useful for dev servers that hang). "+
					"Default 0 (disabled); clamped to a maximum of %d.",
				maxWatchIdleTimeoutSec,
			))),
	)
	s.AddTool(tool, HandleWatchProcess(deps))
}

// RegisterWatchProcessEvents registers the watch_process_events tool.
func RegisterWatchProcessEvents(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("watch_process_events",
		mcp.WithDescription(
			"Return structured events since last poll for a process started via watch_process. "+
				"Pass since_event_id equal to the max ID seen on the previous call to avoid re-reading "+
				"events already processed; omit or 0 on the first poll to receive everything so far.",
		),
		mcp.WithString("shell_id", mcp.Required(),
			mcp.Description("Shell identifier returned by watch_process.")),
		mcp.WithNumber("since_event_id",
			mcp.Description("Only return events with ID > this value. Default 0 (all events).")),
	)
	s.AddTool(tool, HandleWatchProcessEvents(deps))
}

// HandleWatchProcess returns the watch_process tool handler.
func HandleWatchProcess(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if deps.Shells == nil {
			return ErrorResult("background shells not configured"), nil
		}

		args, _ := req.Params.Arguments.(map[string]any)
		command, errRes := validateWatchArgs(deps, args)
		if errRes != nil {
			return errRes, nil
		}
		patterns, patternSources, errRes := parseWatchPatterns(args)
		if errRes != nil {
			return errRes, nil
		}
		idleTimeout := parseWatchIdleTimeout(args)

		id := NewShellID()
		sh := NewWatchedShell(id, command, patterns, patternSources, idleTimeout)
		deps.Shells.Register(sh)

		// Share /bin/bash -c + Setpgid + stdout/stderr pipe plumbing with
		// background Bash — both surfaces spawn identically-shaped child
		// processes and any divergence would be a bug.
		cmd, stdoutPipe, stderrPipe, errRes := startBackgroundBashCmd(deps, sh, command, "watch_process")
		if errRes != nil {
			return errRes, nil
		}

		go drainWatchStdout(stdoutPipe, sh)
		go drainWatchStderr(stderrPipe, sh)
		go watchLifecycle(cmd, sh)
		if idleTimeout > 0 {
			go watchIdleWatchdog(sh)
		}

		return TextResult(formatWatchStarted(id, command, patternSources, idleTimeout)), nil
	}
}

// HandleWatchProcessEvents returns the watch_process_events handler.
func HandleWatchProcessEvents(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if deps.Shells == nil {
			return ErrorResult("background shells not configured"), nil
		}
		args, _ := req.Params.Arguments.(map[string]any)
		id, _ := args["shell_id"].(string)
		if id == "" {
			return ErrorResult("shell_id is required"), nil
		}
		sh, ok := deps.Shells.Get(id)
		if !ok {
			return ErrorResult("unknown shell_id: %s", id), nil
		}
		if !sh.IsWatched() {
			return ErrorResult("shell_id %s was not started by watch_process; use BashOutput instead", id), nil
		}
		since := 0
		if v, ok := args["since_event_id"].(float64); ok && int(v) > 0 {
			since = int(v)
		}
		events, dropped := sh.Events(since)
		_, _, _, _, exit, running := sh.Snapshot()
		return TextResult(formatWatchEvents(sh, events, dropped, exit, running)), nil
	}
}

func validateWatchArgs(deps *Deps, args map[string]any) (string, *mcp.CallToolResult) {
	command, _ := args["command"].(string)
	if command == "" {
		return "", ErrorResult("command is required")
	}
	if desc, _ := args["description"].(string); desc == "" {
		return "", ErrorResult("description is required")
	}
	if token, reason := denyDetails(command); reason != "" {
		deps.Metrics.DenylistHit(token)
		return "", ErrorResult("command rejected: %s", reason)
	}
	return command, nil
}

// parseWatchPatterns returns (compiled, sources, errRes). When the arg is
// missing or nil, the defaults are used. An empty list explicitly
// disables pattern matching. Invalid regexes surface a clear error
// naming the offending source.
func parseWatchPatterns(args map[string]any) ([]*regexp.Regexp, []string, *mcp.CallToolResult) {
	raw, present := args["error_patterns"]
	if !present {
		return compilePatterns(defaultWatchPatterns)
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, nil, ErrorResult("error_patterns must be an array of strings")
	}
	if len(list) > maxWatchPatterns {
		return nil, nil, ErrorResult("too many error_patterns (%d > %d)", len(list), maxWatchPatterns)
	}
	sources := make([]string, 0, len(list))
	for i, v := range list {
		s, ok := v.(string)
		if !ok {
			return nil, nil, ErrorResult("error_patterns[%d] is not a string", i)
		}
		if s == "" {
			return nil, nil, ErrorResult("error_patterns[%d] is empty", i)
		}
		sources = append(sources, s)
	}
	return compilePatterns(sources)
}

// compilePatterns takes a list of regex source strings and returns the
// compiled form plus the source list (in the same order). Surfaces a
// clear per-pattern error on compile failure.
func compilePatterns(sources []string) ([]*regexp.Regexp, []string, *mcp.CallToolResult) {
	out := make([]*regexp.Regexp, 0, len(sources))
	for i, s := range sources {
		re, err := regexp.Compile(s)
		if err != nil {
			return nil, nil, ErrorResult("error_patterns[%d] %q failed to compile: %v", i, s, err)
		}
		out = append(out, re)
	}
	return out, sources, nil
}

func parseWatchIdleTimeout(args map[string]any) time.Duration {
	v, ok := args["idle_timeout_seconds"].(float64)
	if !ok || int(v) <= 0 {
		return 0
	}
	secs := int(v)
	if secs > maxWatchIdleTimeoutSec {
		secs = maxWatchIdleTimeoutSec
	}
	return time.Duration(secs) * time.Second
}

// drainWatchStderr reads stderr line-by-line via bufio.Scanner so each
// line can be matched against the configured regex set. Lines longer
// than bufio.MaxScanTokenSize are silently skipped, matching
// handleBashBackground's drainPipe contract that caps don't surface as
// errors.
func drainWatchStderr(r io.Reader, sh *BackgroundShell) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		sh.ObserveWatchLine(scanner.Text())
	}
}

// drainWatchStdout reads stdout byte-by-byte (no line processing needed;
// stdout isn't pattern-matched). Still refreshes lastOutputAt so long-
// running processes that emit heartbeat logs on stdout are not killed
// by the idle watchdog.
func drainWatchStdout(r io.Reader, sh *BackgroundShell) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			sh.ObserveWatchStdout(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// watchLifecycle waits for the process to exit and stamps a structured
// exited event plus the normal exit code.
func watchLifecycle(cmd *exec.Cmd, sh *BackgroundShell) {
	exit := waitExitCode(cmd)
	sh.RecordWatchExit(exit)
	sh.SetExit(exit)
}

// watchIdleWatchdog polls lastOutputAt and kills the process group when
// the configured idle timeout has elapsed with no output. Stops when the
// process has exited (detected via Snapshot's running flag).
func watchIdleWatchdog(sh *BackgroundShell) {
	ticker := time.NewTicker(watchIdleCheckInterval)
	defer ticker.Stop()
	timeout := sh.IdleTimeout()
	if timeout <= 0 {
		return
	}
	for range ticker.C {
		_, _, _, _, _, running := sh.Snapshot()
		if !running {
			return
		}
		last := sh.LastOutputAt()
		if last.IsZero() {
			continue
		}
		if time.Since(last) < timeout {
			continue
		}
		reason := fmt.Sprintf("no output for %s (configured idle_timeout=%s)", time.Since(last).Round(time.Second), timeout)
		sh.RecordWatchIdleTimeout(reason)
		// Best-effort kill of the whole process group. The lifecycle
		// goroutine will stamp WatchEventExited once the child actually
		// reaps.
		if pgid := sh.Pgid(); pgid > 0 {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
		return
	}
}

func formatWatchStarted(id, command string, patterns []string, idleTimeout time.Duration) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "shell_id: %s\n", id)
	fmt.Fprintf(&sb, "started in background: %s\n", command)
	if len(patterns) == 0 {
		sb.WriteString("error_patterns: (none — pattern matching disabled)\n")
	} else {
		fmt.Fprintf(&sb, "error_patterns: %s\n", strings.Join(patterns, " | "))
	}
	if idleTimeout > 0 {
		fmt.Fprintf(&sb, "idle_timeout: %s\n", idleTimeout)
	} else {
		sb.WriteString("idle_timeout: (disabled)\n")
	}
	sb.WriteString("\nPoll watch_process_events with this shell_id for structured events. KillShell terminates the process group.\n")
	return sb.String()
}

func formatWatchEvents(sh *BackgroundShell, events []WatchEvent, dropped int, exit *int, running bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "shell_id: %s\n", sh.ID())
	fmt.Fprintf(&sb, "command: %s\n", sh.Command())
	if running {
		sb.WriteString("status: running\n")
	} else {
		fmt.Fprintf(&sb, "status: completed (exit %d)\n", *exit)
	}
	if patterns := sh.WatchPatterns(); len(patterns) > 0 {
		fmt.Fprintf(&sb, "error_patterns: %s\n", strings.Join(patterns, " | "))
	}
	if dropped > 0 {
		fmt.Fprintf(&sb, "NOTE: %d earlier events dropped (per-shell cap of %d reached)\n", dropped, watchEventCap)
	}
	fmt.Fprintf(&sb, "\n--- events (%d) ---\n", len(events))
	for _, ev := range events {
		formatWatchEventLine(&sb, ev)
	}
	return sb.String()
}

func formatWatchEventLine(sb *strings.Builder, ev WatchEvent) {
	ts := ev.At.Format(time.RFC3339)
	switch ev.Type {
	case WatchEventStarted:
		fmt.Fprintf(sb, "[%d] %s  %s\n", ev.ID, ts, ev.Type)
	case WatchEventError:
		if ev.Pattern != "" {
			fmt.Fprintf(sb, "[%d] %s  %s  (matched %q)  %s\n", ev.ID, ts, ev.Type, ev.Pattern, ev.Line)
		} else {
			fmt.Fprintf(sb, "[%d] %s  %s  %s\n", ev.ID, ts, ev.Type, ev.Line)
		}
	case WatchEventIdleTimeout:
		fmt.Fprintf(sb, "[%d] %s  %s  %s\n", ev.ID, ts, ev.Type, ev.Line)
	case WatchEventExited:
		fmt.Fprintf(sb, "[%d] %s  %s  exit=%d\n", ev.ID, ts, ev.Type, ev.ExitCode)
	default:
		fmt.Fprintf(sb, "[%d] %s  %s\n", ev.ID, ts, ev.Type)
	}
}
