package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// RegisterGrep registers the Grep tool on the given MCP server.
func RegisterGrep(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("Grep",
		mcp.WithDescription("Search file contents with a regex. ripgrep-backed; respects .gitignore. Returns matches in the requested output_mode."),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Regex (Rust regex syntax).")),
		mcp.WithString("path", mcp.Description("File or directory to search. Defaults to workspace root.")),
		mcp.WithString("glob", mcp.Description("Glob filter, e.g. '*.go'.")),
		mcp.WithBoolean("case_insensitive", mcp.Description("Case-insensitive match.")),
		mcp.WithString("output_mode", mcp.Description("One of 'content' (default), 'files_with_matches', 'count'.")),
		mcp.WithNumber("head_limit", mcp.Description("Truncate output to this many lines. 0 or unset means no limit. For files_with_matches/count modes, bounds the number of files reported.")),
	)
	s.AddTool(tool, HandleGrep(deps))
}

// HandleGrep returns the Grep tool handler.
func HandleGrep(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		pattern, _ := args["pattern"].(string)
		if pattern == "" {
			return ErrorResult("pattern is required"), nil
		}

		rgArgs, errRes := buildGrepRgArgs(args, pattern)
		if errRes != nil {
			return errRes, nil
		}

		cwd := deps.Workspace.Root()
		scopeRel, errRes := resolveGrepScope(deps, args)
		if errRes != nil {
			return errRes, nil
		}
		if scopeRel != "" {
			rgArgs = append(rgArgs, scopeRel)
		}

		out, err := runRipgrep(ctx, rgArgs, cwd)
		if err != nil {
			return ErrorResult("grep: %v", err), nil
		}

		body := string(out)
		if v, ok := args["head_limit"].(float64); ok && int(v) > 0 {
			body = truncateLines(body, int(v))
		}
		return TextResult(body), nil
	}
}

func buildGrepRgArgs(args map[string]any, pattern string) ([]string, *mcp.CallToolResult) {
	mode := "content"
	if v, ok := args["output_mode"].(string); ok && v != "" {
		mode = v
	}
	modeArgs, err := grepModeArgs(mode)
	if err != nil {
		return nil, ErrorResult("%v", err)
	}
	rgArgs := []string{"--no-require-git", "--color=never"}
	rgArgs = append(rgArgs, modeArgs...)
	if v, _ := args["case_insensitive"].(bool); v {
		rgArgs = append(rgArgs, "-i")
	}
	if glob, ok := args["glob"].(string); ok && glob != "" {
		rgArgs = append(rgArgs, "-g", glob)
	}
	rgArgs = append(rgArgs, "--", pattern)
	return rgArgs, nil
}

func resolveGrepScope(deps *Deps, args map[string]any) (string, *mcp.CallToolResult) {
	pathArg, ok := args["path"].(string)
	if !ok || pathArg == "" {
		return "", nil
	}
	abs, err := deps.Workspace.Resolve(pathArg)
	if err != nil {
		return "", ErrorResult("resolve path: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", ErrorResult("stat path: %v", err)
	}
	rel, err := relToRoot(deps.Workspace.Root(), abs)
	if err != nil {
		return "", ErrorResult("relative path: %v", err)
	}
	return rel, nil
}

func grepModeArgs(mode string) ([]string, error) {
	switch mode {
	case "content":
		return []string{"-n", "--no-heading"}, nil
	case "files_with_matches":
		return []string{"-l"}, nil
	case "count":
		return []string{"-c"}, nil
	default:
		return nil, &unknownModeError{mode: mode}
	}
}

type unknownModeError struct{ mode string }

func (e *unknownModeError) Error() string {
	return "unknown output_mode: " + e.mode + " (valid: content, files_with_matches, count)"
}

func relToRoot(root, abs string) (string, error) {
	if abs == root {
		return "", nil
	}
	return filepath.Rel(root, abs)
}

func truncateLines(s string, n int) string {
	lines := strings.SplitAfter(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "")
}
