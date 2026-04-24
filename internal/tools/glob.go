package tools

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
)

const defaultGlobLimit = 100

// RegisterGlob registers the Glob tool on the given MCP server.
func RegisterGlob(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("Glob",
		mcp.WithDescription("Find files matching a glob pattern. Respects .gitignore. Returns paths relative to the workspace root (including the 'path' prefix when scoped), sorted by mtime (most recent first). In multi-workspace mode pass `workspace` to pick one."),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Glob pattern supporting '*', '?', '[...]', and '**'. e.g. '**/*.go' or 'src/**/*.ts'. Brace expansion and negation are NOT supported — make multiple calls for multi-extension matches.")),
		mcp.WithString("path", mcp.Description("Directory to search within (workspace-relative or absolute). Defaults to workspace root.")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of paths to return (default 100).")),
		withWorkspaceArg(),
	)
	s.AddTool(tool, HandleGlob(deps))
}

// HandleGlob returns the Glob tool handler.
func HandleGlob(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		ws, errRes := ResolveWorkspace(deps, args)
		if errRes != nil {
			return errRes, nil
		}

		pattern, _ := args["pattern"].(string)
		if pattern == "" {
			return ErrorResult("pattern is required"), nil
		}

		root := ws.Root()
		scopeArg, errRes := resolveGlobScope(ws, args, root)
		if errRes != nil {
			return errRes, nil
		}
		limit := parseGlobLimit(args)

		out, err := runRipgrep(ctx, buildGlobRgArgs(scopeArg), root)
		if err != nil {
			return ErrorResult("glob: %v", err), nil
		}

		var paths []string
		for _, p := range splitLines(out) {
			if matchDoublestar(pattern, p) {
				paths = append(paths, p)
			}
		}

		sortByMtimeDesc(root, paths)
		if len(paths) > limit {
			paths = paths[:limit]
		}
		return TextResult(strings.Join(paths, "\n")), nil
	}
}

// resolveGlobScope returns the positional scope arg for rg, or "" for the
// workspace root. The rg cwd is always the workspace root so emitted paths
// stay workspace-relative (matching Grep's contract).
func resolveGlobScope(ws *workspace.Workspace, args map[string]any, root string) (string, *mcp.CallToolResult) {
	pathArg, ok := args["path"].(string)
	if !ok || pathArg == "" {
		return "", nil
	}
	abs, err := ws.Resolve(pathArg)
	if err != nil {
		return "", ErrorResult("resolve path: %v", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", ErrorResult("stat path: %v", err)
	}
	if !info.IsDir() {
		return "", ErrorResult("path is not a directory: %s", pathArg)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", ErrorResult("relative path: %v", err)
	}
	if rel == "." {
		return "", nil
	}
	return rel, nil
}

func parseGlobLimit(args map[string]any) int {
	if v, ok := args["limit"].(float64); ok && int(v) > 0 {
		return int(v)
	}
	return defaultGlobLimit
}

// buildGlobRgArgs runs rg --files without -g so .gitignore rules are
// respected (rg's -g whitelists files and overrides ignores). We apply the
// glob pattern in Go post-hoc.
func buildGlobRgArgs(scopeArg string) []string {
	rgArgs := []string{"--files", "--no-require-git", "--color=never"}
	if scopeArg != "" {
		rgArgs = append(rgArgs, scopeArg)
	}
	return rgArgs
}

// matchDoublestar reports whether path matches the given glob pattern.
// It supports ** as "zero or more path components", consistent with rg/gitignore
// glob semantics. Single-component globs (no path separator) match the base name.
func matchDoublestar(pattern, path string) bool {
	// Normalise separators.
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)

	// A pattern with no path separator matches against the base name only.
	if !strings.Contains(pattern, "/") {
		base := path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			base = path[i+1:]
		}
		ok, _ := filepath.Match(pattern, base)
		return ok
	}

	return doublestarMatch(pattern, path)
}

// doublestarMatch is a recursive ** aware matcher operating on slash-normalised
// strings.
func doublestarMatch(pattern, path string) bool {
	for pattern != "" {
		if matched, ok := matchDoublestarPrefix(pattern, path); ok {
			return matched
		}

		var pp, qp string
		pp, pattern = popSegment(pattern)
		qp, path = popSegment(path)

		if ok, _ := filepath.Match(pp, qp); !ok {
			return false
		}
		if pattern == "" {
			return path == ""
		}
	}
	return path == ""
}

// matchDoublestarPrefix handles "**"-prefixed patterns. It reports whether a
// terminal answer was reached (ok=true) along with that answer (matched).
func matchDoublestarPrefix(pattern, path string) (matched, ok bool) {
	if pattern == "**" {
		return true, true
	}
	if !strings.HasPrefix(pattern, "**/") {
		return false, false
	}
	rest := pattern[3:]
	if doublestarMatch(rest, path) {
		return true, true
	}
	if i := strings.Index(path, "/"); i >= 0 {
		return doublestarMatch(pattern, path[i+1:]), true
	}
	return false, true
}

// popSegment splits s into (first segment, remainder) at the first '/'. If
// no '/' is present the whole string is returned as the segment and the
// remainder is empty.
func popSegment(s string) (seg, rest string) {
	i := strings.Index(s, "/")
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+1:]
}

func splitLines(b []byte) []string {
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// sortByMtimeDesc sorts `paths` (each a path relative to `cwd`) in-place by
// descending mtime, breaking ties by path lexicographic order. Paths that
// cannot be stat'd drop to the end; their relative order is undefined.
func sortByMtimeDesc(cwd string, paths []string) {
	type entry struct {
		path  string
		mtime int64
		ok    bool
	}
	entries := make([]entry, len(paths))
	for i, p := range paths {
		info, err := os.Stat(filepath.Join(cwd, p))
		if err != nil {
			entries[i] = entry{path: p}
			continue
		}
		entries[i] = entry{path: p, mtime: info.ModTime().UnixNano(), ok: true}
	}
	// The compound key (ok, mtime, path) is total, so sort.Slice is sufficient
	// — no need for the stability guarantees (and extra cost) of SliceStable.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ok != entries[j].ok {
			return entries[i].ok
		}
		if entries[i].mtime != entries[j].mtime {
			return entries[i].mtime > entries[j].mtime
		}
		return entries[i].path < entries[j].path
	})
	for i, e := range entries {
		paths[i] = e.path
	}
}
