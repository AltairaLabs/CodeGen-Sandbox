package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultRunTestsTimeoutSec = 300
	maxRunTestsTimeoutSec     = 1800
)

// RegisterRunTests registers the run_tests tool on the given MCP server.
func RegisterRunTests(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("run_tests",
		mcp.WithDescription("Run the project's test suite. Project type is detected from the workspace root (currently: Go via go.mod). Returns combined stdout+stderr plus a trailing 'exit: N' line. For Go projects the runner uses `-json` so the companion `last_test_failures` tool can surface structured failure records."),
		mcp.WithNumber("timeout", mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.", defaultRunTestsTimeoutSec, maxRunTestsTimeoutSec))),
	)
	s.AddTool(tool, HandleRunTests(deps))
}

// HandleRunTests returns the run_tests tool handler.
func HandleRunTests(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		det := verify.Detect(deps.Workspace.Root())
		if det == nil {
			return ErrorResult("no supported project detected in workspace root"), nil
		}

		args, _ := req.Params.Arguments.(map[string]any)
		timeoutSec := parseRunTestsTimeout(args)

		res, err := runVerifyCmd(ctx, det.TestCmd(), deps.Workspace.Root(), timeoutSec)
		if err != nil {
			return ErrorResult("run_tests: %v", err), nil
		}

		recordTestResult(deps, det, res)
		// Agent-health hooks. Failure count feeds the streak gauge; exit=0
		// stamps the last-green timestamp so the time-since-last-green gauge
		// can tick from zero.
		failures := det.ParseTestFailures(string(res.Stdout), string(res.Stderr))
		deps.Health.ObserveTestResult(len(failures))
		if res.ExitCode == 0 {
			deps.Health.ObserveGreen()
		}
		return TextResult(formatVerifyResult(res, timeoutSec)), nil
	}
}

func parseRunTestsTimeout(args map[string]any) int {
	timeoutSec := defaultRunTestsTimeoutSec
	v, ok := args["timeout"].(float64)
	if !ok || int(v) <= 0 {
		return timeoutSec
	}
	timeoutSec = int(v)
	if timeoutSec > maxRunTestsTimeoutSec {
		timeoutSec = maxRunTestsTimeoutSec
	}
	return timeoutSec
}

// recordTestResult populates the session-scoped TestResultStore with the
// parsed failure list. No-ops when the store isn't wired (tests that don't
// exercise last_test_failures).
func recordTestResult(deps *Deps, det verify.Detector, res execResult) {
	if deps.TestResults == nil {
		return
	}
	failures := det.ParseTestFailures(string(res.Stdout), string(res.Stderr))
	deps.TestResults.Set(TestResult{
		Language:    det.Language(),
		Failures:    failures,
		TestsPassed: testsPassedCount(det, string(res.Stdout)),
		At:          time.Now(),
		Supported:   len(failures) > 0 || detectorSupportsStructuredFailures(det),
	})
}

// testsPassedCount returns the number of passing tests reported by the
// detector's structured output. -1 when the detector has no countable
// signal, so the formatter renders "N tests" instead of a misleading zero.
func testsPassedCount(det verify.Detector, stdout string) int {
	if det.Language() == languageGo {
		return verify.CountGoTest2JSONPasses(stdout)
	}
	return -1
}

// detectorSupportsStructuredFailures distinguishes "detector has no parser"
// from "detector ran and emitted zero failures". v1: Go is the only
// supported detector. Expanding this list is the only change future
// detectors need when they ship their parser.
func detectorSupportsStructuredFailures(det verify.Detector) bool {
	return det.Language() == languageGo
}

// languageGo is hoisted because it's referenced by both the support
// predicate and the pass-count helper.
const languageGo = "go"
