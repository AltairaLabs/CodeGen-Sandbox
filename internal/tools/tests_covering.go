package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// maxTestsCoveringEntries caps the test list so a pathological query
// ("any line in main.go") doesn't drown the agent in output. Matches
// the truncation style used by last_test_failures.
const maxTestsCoveringEntries = 200

// RegisterTestsCovering registers the tests_covering tool.
func RegisterTestsCovering(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("tests_covering",
		mcp.WithDescription("Return the tests whose Go coverage profile from the most recent run_tests call touched the given file (and optionally line). v1 is Go-only; agents use this to scope run_tests / run_failing_tests to the minimum packages that exercise a change. Attribution is package-level: Go's -coverprofile is per-run aggregate, so every test that ran in a given package gets attributed coverage of every file that package touched during the run."),
		mcp.WithString("file", mcp.Required(), mcp.Description("Workspace-relative path to the source file (e.g. 'internal/tools/read.go').")),
		mcp.WithNumber("line", mcp.Description("Optional 1-based line number. Omitted = any line in the file.")),
	)
	s.AddTool(tool, HandleTestsCovering(deps))
}

// HandleTestsCovering returns the tests_covering tool handler.
func HandleTestsCovering(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if deps.CoverageIndex == nil {
			return ErrorResult("tests_covering: coverage index not configured"), nil
		}
		if deps.CoverageIndex.Empty() {
			return TextResult("no coverage data yet — run `run_tests` first"), nil
		}

		args, _ := req.Params.Arguments.(map[string]any)
		file, _ := args["file"].(string)
		if strings.TrimSpace(file) == "" {
			return ErrorResult("tests_covering: file is required"), nil
		}
		line := 0
		if v, ok := args["line"].(float64); ok && int(v) > 0 {
			line = int(v)
		}

		refs := deps.CoverageIndex.TestsCovering(file, line)
		if len(refs) == 0 {
			if line > 0 {
				return TextResult(fmt.Sprintf("no coverage found for %s:%d", file, line)), nil
			}
			return TextResult(fmt.Sprintf("no coverage found for %s", file)), nil
		}

		return TextResult(renderTestsCovering(file, line, refs)), nil
	}
}

// renderTestsCovering formats the test list grouped by package. The
// output is capped at maxTestsCoveringEntries with a truncation footer,
// matching last_test_failures' cap style.
func renderTestsCovering(file string, line int, refs []TestRef) string {
	shown, truncated := capRefs(refs, maxTestsCoveringEntries)

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d test(s) cover %s", len(refs), file)
	if line > 0 {
		fmt.Fprintf(&sb, ":%d", line)
	}
	sb.WriteString(":\n\n")

	// Group by package so the output is skimmable when many tests hit
	// the same file. Packages sorted alphabetically; tests already
	// sorted by TestsCovering.
	byPkg := map[string][]string{}
	pkgs := []string{}
	for _, r := range shown {
		if _, ok := byPkg[r.Package]; !ok {
			pkgs = append(pkgs, r.Package)
		}
		byPkg[r.Package] = append(byPkg[r.Package], r.TestName)
	}
	sort.Strings(pkgs)
	for _, pkg := range pkgs {
		label := pkg
		if label == "" {
			label = "(unknown package)"
		}
		fmt.Fprintf(&sb, "%s:\n", label)
		for _, name := range byPkg[pkg] {
			fmt.Fprintf(&sb, "  %s\n", name)
		}
	}
	if truncated > 0 {
		fmt.Fprintf(&sb, "\n... (%d more truncated)\n", truncated)
	}
	return sb.String()
}

// capRefs returns the slice clipped to maxEntries and the count of
// hidden entries (0 when the input already fits).
func capRefs(refs []TestRef, maxEntries int) ([]TestRef, int) {
	if len(refs) <= maxEntries {
		return refs, 0
	}
	return refs[:maxEntries], len(refs) - maxEntries
}
