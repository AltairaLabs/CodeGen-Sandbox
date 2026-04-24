package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultRunScriptTimeoutSec = 300
	maxRunScriptTimeoutSec     = 1800
)

// RegisterRunScript registers the run_script tool on the given MCP
// server. The tool runs a named entry from package.json#scripts via
// the detected package manager (npm / pnpm / yarn / bun).
func RegisterRunScript(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("run_script",
		mcp.WithDescription("Run a script defined in package.json#scripts via the detected Node package manager (npm / pnpm / yarn / bun). Node-only today — Go / Python / Rust workspaces surface a clear 'only Node projects support scripts' error."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Script name as defined in package.json#scripts (e.g. \"build\", \"dev\", \"lint\").")),
		mcp.WithNumber("timeout", mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.", defaultRunScriptTimeoutSec, maxRunScriptTimeoutSec))),
	)
	s.AddTool(tool, HandleRunScript(deps))
}

// HandleRunScript returns the run_script tool handler. The behaviour
// precedence (detector → language → package.json → script lookup → run)
// matches the reference behaviour spec in issue #25 so operators get
// deterministic error messages at every failure layer.
func HandleRunScript(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		det := verify.Detect(deps.Workspace.Root())
		if det == nil {
			return ErrorResult("no supported project detected in workspace root"), nil
		}
		runner := det.ScriptRunner()
		if len(runner) == 0 {
			return ErrorResult("run_script: only Node projects support scripts today (detected: %s)", det.Language()), nil
		}

		args, _ := req.Params.Arguments.(map[string]any)
		scriptName := scriptNameArg(args)
		if scriptName == "" {
			return ErrorResult("run_script: name is required"), nil
		}
		timeoutSec := parseRunScriptTimeout(args)

		scripts, err := readPackageScripts(deps.Workspace.Root())
		if err != nil {
			return ErrorResult("run_script: %v", err), nil
		}
		if _, ok := scripts[scriptName]; !ok {
			return ErrorResult("run_script: no script named %q in package.json; available: %s",
				scriptName, joinScriptNames(scripts)), nil
		}

		cmd := append([]string{}, runner...)
		cmd = append(cmd, scriptName)

		res, err := runVerifyCmd(ctx, cmd, deps.Workspace.Root(), timeoutSec)
		if err != nil {
			return ErrorResult("run_script: %v", err), nil
		}
		return TextResult(formatVerifyResult(res, timeoutSec)), nil
	}
}

// scriptNameArg extracts the required "name" argument, returning the
// empty string when missing / wrong type so the caller can surface the
// canonical "name is required" error.
func scriptNameArg(args map[string]any) string {
	v, ok := args["name"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// parseRunScriptTimeout mirrors parseRunTestsTimeout's clamp semantics
// so the two tools behave consistently for the agent.
func parseRunScriptTimeout(args map[string]any) int {
	timeoutSec := defaultRunScriptTimeoutSec
	v, ok := args["timeout"].(float64)
	if !ok || int(v) <= 0 {
		return timeoutSec
	}
	timeoutSec = int(v)
	if timeoutSec > maxRunScriptTimeoutSec {
		timeoutSec = maxRunScriptTimeoutSec
	}
	return timeoutSec
}

// readPackageScripts reads package.json#scripts from the workspace root.
// Returns a user-visible error when the file is missing or unreadable;
// returns an empty map (no error) when package.json parses cleanly but
// has no scripts section — lets the caller surface the "no script named
// X" error consistently.
func readPackageScripts(root string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("package.json not found in workspace root")
		}
		return nil, fmt.Errorf("reading package.json: %w", err)
	}
	var parsed struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parsing package.json: %w", err)
	}
	return parsed.Scripts, nil
}

// joinScriptNames renders the "available" list for the unknown-script
// error. Alphabetised for stable output and easier agent parsing.
func joinScriptNames(scripts map[string]string) string {
	if len(scripts) == 0 {
		return "(none defined)"
	}
	names := make([]string, 0, len(scripts))
	for n := range scripts {
		names = append(names, n)
	}
	sortStringsInPlace(names)
	return strings.Join(names, ", ")
}

// sortStringsInPlace is a tiny local insertion sort so this file stays
// free of an explicit sort import just for one list. Scripts maps are
// always small (<20 entries).
func sortStringsInPlace(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
