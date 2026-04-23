package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultLastFailuresLimit = 50
	maxLastFailuresLimit     = 500
)

// RegisterLastTestFailures registers the last_test_failures tool.
func RegisterLastTestFailures(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("last_test_failures",
		mcp.WithDescription("Return the structured failure list from the most recent run_tests call in this session. Go only today; other languages surface a 'not supported' notice. Output has one block per failure (TestName, file:line, Message, optional diff)."),
		mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Maximum entries to return. Default %d, clamped to %d.", defaultLastFailuresLimit, maxLastFailuresLimit))),
	)
	s.AddTool(tool, HandleLastTestFailures(deps))
}

// HandleLastTestFailures returns the last_test_failures tool handler.
func HandleLastTestFailures(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if deps.TestResults == nil {
			return ErrorResult("last_test_failures: store not configured"), nil
		}
		result, ok := deps.TestResults.Get()
		if !ok {
			return TextResult(msgNoRunYet), nil
		}

		args, _ := req.Params.Arguments.(map[string]any)
		limit := parseLastFailuresLimit(args)

		return TextResult(renderLastFailures(result, limit)), nil
	}
}

const msgNoRunYet = "no run_tests call yet in this session"

func parseLastFailuresLimit(args map[string]any) int {
	limit := defaultLastFailuresLimit
	v, ok := args["limit"].(float64)
	if !ok || int(v) <= 0 {
		return limit
	}
	limit = int(v)
	if limit > maxLastFailuresLimit {
		limit = maxLastFailuresLimit
	}
	return limit
}

// renderLastFailures chooses between three messages (unsupported language,
// no failures, failure list) so the handler body stays flat.
func renderLastFailures(r TestResult, limit int) string {
	age := humanizeSince(r.At)
	if !r.Supported {
		return fmt.Sprintf("no structured failures available for %s (last run_tests %s ago)", r.Language, age)
	}
	if len(r.Failures) == 0 {
		return fmt.Sprintf("last run_tests had no failures (%s, %s, %s ago)", formatTestCount(r.TestsPassed), r.Language, age)
	}
	return renderFailureList(r, limit, age)
}

// renderFailureList builds the block-per-failure view.
func renderFailureList(r TestResult, limit int, age string) string {
	var sb strings.Builder
	shown := r.Failures
	truncated := false
	if len(shown) > limit {
		shown = shown[:limit]
		truncated = true
	}
	fmt.Fprintf(&sb, "%d test failure(s) from last run_tests call (%s, %s ago):\n\n",
		len(r.Failures), r.Language, age)
	for i, f := range shown {
		writeFailureBlock(&sb, i+1, f)
	}
	if truncated {
		fmt.Fprintf(&sb, "(%d more entries truncated; pass a larger limit to see them)\n",
			len(r.Failures)-len(shown))
	}
	return sb.String()
}

// writeFailureBlock renders a single failure entry.
func writeFailureBlock(sb *strings.Builder, index int, f verify.TestFailure) {
	fmt.Fprintf(sb, "%d. %s\n", index, f.TestName)
	if f.File != "" && f.Line > 0 {
		fmt.Fprintf(sb, "   %s:%d\n", f.File, f.Line)
	}
	if f.Message != "" {
		fmt.Fprintf(sb, "   %s\n", f.Message)
	}
	if f.Diff != "" {
		sb.WriteString("   --- diff ---\n")
		for _, line := range strings.Split(f.Diff, "\n") {
			fmt.Fprintf(sb, "   %s\n", line)
		}
	}
	sb.WriteByte('\n')
}

// formatTestCount renders the "<N> tests passed" segment, with a graceful
// fallback for detectors that don't expose a pass count.
func formatTestCount(passed int) string {
	if passed < 0 {
		return "passed"
	}
	return fmt.Sprintf("%d tests passed", passed)
}

// humanizeSince returns a coarse human-friendly duration ("3s", "1m42s",
// "2h15m") so the output stays compact — humans don't need millisecond
// precision to orient themselves after a test run.
func humanizeSince(t time.Time) string {
	d := time.Since(t)
	if d < time.Second {
		return "just now"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
