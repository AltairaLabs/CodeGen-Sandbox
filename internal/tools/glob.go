package tools

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const defaultGlobLimit = 100

// RegisterGlob registers the Glob tool on the given MCP server.
func RegisterGlob(s Registrar, deps *Deps) {
	tool := mcp.NewTool("Glob",
		mcp.WithDescription("Find files matching a glob pattern. Respects .gitignore. Returns paths relative to the workspace root (including the 'path' prefix when scoped), sorted by mtime (most recent first)."),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Glob pattern supporting '*', '?', '[...]', and '**'. e.g. '**/*.go' or 'src/**/*.ts'. Brace expansion and negation are NOT supported — make multiple calls for multi-extension matches.")),
		mcp.WithString("path", mcp.Description("Directory to search within (workspace-relative or absolute). Defaults to workspace root.")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of paths to return (default 100).")),
	)
	s.AddTool(tool, HandleGlob(deps))
}

// HandleGlob returns the Glob tool handler.
func HandleGlob(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		pattern, _ := args["pattern"].(string)
		if pattern == "" {
			return ErrorResult("pattern is required"), nil
		}

		root := deps.Workspace.Root()

		// Keep cwd at the workspace root so emitted paths are always
		// workspace-relative, matching Grep's contract. If the caller scoped
		// the search to a subdirectory via `path`, pass it as a positional
		// arg to rg (which prefixes the scope in its output).
		var scopeArg string
		if pathArg, ok := args["path"].(string); ok && pathArg != "" {
			abs, err := deps.Workspace.Resolve(pathArg)
			if err != nil {
				return ErrorResult("resolve path: %v", err), nil
			}
			info, err := os.Stat(abs)
			if err != nil {
				return ErrorResult("stat path: %v", err), nil
			}
			if !info.IsDir() {
				return ErrorResult("path is not a directory: %s", pathArg), nil
			}
			rel, err := filepath.Rel(root, abs)
			if err != nil {
				return ErrorResult("relative path: %v", err), nil
			}
			if rel != "." {
				scopeArg = rel
			}
		}

		limit := defaultGlobLimit
		if v, ok := args["limit"].(float64); ok && int(v) > 0 {
			limit = int(v)
		}

		// Run rg --files without a -g filter so that .gitignore rules are
		// respected (rg's -g whitelists files which overrides ignore rules).
		// We apply the glob pattern ourselves in Go after getting the list.
		rgArgs := []string{
			"--files",
			"--no-require-git",
			"--color=never",
		}
		if scopeArg != "" {
			rgArgs = append(rgArgs, scopeArg)
		}
		out, err := runRipgrep(ctx, rgArgs, root)
		if err != nil {
			return ErrorResult("glob: %v", err), nil
		}

		all := splitLines(out)
		var paths []string
		for _, p := range all {
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
		// Consume leading "**" segment.
		if strings.HasPrefix(pattern, "**/") {
			rest := pattern[3:]
			// ** matches zero path components (try without consuming any of path)
			if doublestarMatch(rest, path) {
				return true
			}
			// or skip one component of path and recurse
			if i := strings.Index(path, "/"); i >= 0 {
				return doublestarMatch(pattern, path[i+1:])
			}
			return false
		}
		if pattern == "**" {
			return true
		}

		// Extract next segment from pattern.
		pi := strings.Index(pattern, "/")
		pp := pattern
		if pi >= 0 {
			pp = pattern[:pi]
			pattern = pattern[pi+1:]
		} else {
			pattern = ""
		}

		// Extract next segment from path.
		qi := strings.Index(path, "/")
		qp := path
		if qi >= 0 {
			qp = path[:qi]
			path = path[qi+1:]
		} else {
			path = ""
		}

		ok, _ := filepath.Match(pp, qp)
		if !ok {
			return false
		}

		// If pattern is exhausted, path must also be exhausted.
		if pattern == "" {
			return path == ""
		}
	}
	return path == ""
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
