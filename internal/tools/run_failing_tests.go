package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultRerunLimit          = 50
	maxRerunLimit              = 200
	maxRerunPackagesPositional = 10
)

// RegisterRunFailingTests registers the run_failing_tests tool.
func RegisterRunFailingTests(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("run_failing_tests",
		mcp.WithDescription("Rerun only the tests that failed in the most recent run_tests call in this session. Go only today; other languages surface a 'not supported' notice. In a polyglot workspace pass `language` to pick one. On success, the structured-failure store is overwritten with the rerun's results so a follow-up last_test_failures call reflects the fresh state."),
		mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Maximum distinct test names to include in the rerun filter. Default %d, clamped to %d.", defaultRerunLimit, maxRerunLimit))),
		mcp.WithNumber("timeout", mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.", defaultRunTestsTimeoutSec, maxRunTestsTimeoutSec))),
		withLanguageArg(),
	)
	s.AddTool(tool, HandleRunFailingTests(deps))
}

// HandleRunFailingTests returns the run_failing_tests tool handler.
func HandleRunFailingTests(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if deps.TestResults == nil {
			return ErrorResult("run_failing_tests: store not configured"), nil
		}
		stored, ok := deps.TestResults.Get()
		if !ok {
			return TextResult(msgNoRunYet), nil
		}

		args, _ := req.Params.Arguments.(map[string]any)
		det, errRes := dispatchByLanguage(deps, args)
		if errRes != nil {
			return errRes, nil
		}
		if !detectorSupportsStructuredFailures(det) {
			return TextResult(fmt.Sprintf("run_failing_tests: %s detector has no structured failures; rerun run_tests manually", det.Language())), nil
		}
		if len(stored.Failures) == 0 {
			return TextResult(fmt.Sprintf("last run_tests had no failures — nothing to rerun (%s)", stored.Language)), nil
		}

		limit := parseRerunLimit(args)
		timeoutSec := parseRunTestsTimeout(args)

		argv := composeGoRerunArgv(stored.Failures, limit)
		if len(argv) == 0 {
			return TextResult(fmt.Sprintf("last run_tests had no failures — nothing to rerun (%s)", stored.Language)), nil
		}

		res, err := runVerifyCmd(ctx, argv, deps.Workspace.Root(), timeoutSec)
		if err != nil {
			return ErrorResult("run_failing_tests: %v", err), nil
		}
		recordTestResult(deps, det, res)
		return TextResult(formatVerifyResult(res, timeoutSec)), nil
	}
}

// parseRerunLimit reads the optional "limit" arg and clamps it to the
// [1, maxRerunLimit] range. Defaults to defaultRerunLimit when missing or
// non-positive.
func parseRerunLimit(args map[string]any) int {
	limit := defaultRerunLimit
	v, ok := args["limit"].(float64)
	if !ok || int(v) <= 0 {
		return limit
	}
	limit = int(v)
	if limit > maxRerunLimit {
		limit = maxRerunLimit
	}
	return limit
}

// composeGoRerunArgv builds a `go test -json -count=1 -run <regex> <packages>`
// invocation targeting only the tests in failures. Package targeting:
// positional package args when the set is ≤ maxRerunPackagesPositional,
// else fall back to "./..." so argv stays bounded. Empty failures → nil.
func composeGoRerunArgv(failures []verify.TestFailure, limit int) []string {
	if len(failures) == 0 {
		return nil
	}
	testSet, pkgSet := extractRerunTargets(failures)
	if len(testSet) == 0 {
		return nil
	}
	if limit > 0 && len(testSet) > limit {
		testSet = testSet[:limit]
	}
	regex := "^(" + strings.Join(testSet, "|") + ")$"

	argv := []string{"go", "test", "-json", "-count=1", "-run", regex}
	if len(pkgSet) > 0 && len(pkgSet) <= maxRerunPackagesPositional {
		argv = append(argv, pkgSet...)
	} else {
		argv = append(argv, "./...")
	}
	return argv
}

// extractRerunTargets turns a failure list into (sorted unique parent-test
// names, sorted unique package import paths). TestName format is
// "<pkg-import-path>:<TestName>" or "<pkg>:<TestName>/<subtest>/..." — Go's
// -run regex matches the parent test, so we strip the slash-suffix.
func extractRerunTargets(failures []verify.TestFailure) ([]string, []string) {
	tests := make(map[string]struct{}, len(failures))
	pkgs := make(map[string]struct{})
	for _, f := range failures {
		if f.TestName == "" {
			continue
		}
		pkg, leaf := splitQualifiedTestName(f.TestName)
		parent := strings.SplitN(leaf, "/", 2)[0]
		if parent == "" {
			continue
		}
		tests[parent] = struct{}{}
		if pkg != "" {
			pkgs[pkg] = struct{}{}
		}
	}
	return sortedKeys(tests), sortedKeys(pkgs)
}

// splitQualifiedTestName splits "pkg:Test" into (pkg, Test). When there is
// no colon the whole string is treated as the test name and pkg is "".
func splitQualifiedTestName(name string) (string, string) {
	i := strings.Index(name, ":")
	if i < 0 {
		return "", name
	}
	return name[:i], name[i+1:]
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
