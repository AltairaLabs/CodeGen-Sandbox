package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/search"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultSearchLimit     = 20
	maxSearchLimit         = 100
	coldStartBudgetSec     = 5
	docExcerptMaxChars     = 200
	emptyWorkspaceMessage  = `no Go files found — semantic search currently Go-only`
	noResultsMessageFormat = `no results for %q`
)

// indexCache keyed by workspace root. Tests create fresh Workspaces under
// t.TempDir so cache collisions aren't a concern; production uses exactly
// one Workspace per server instance.
var (
	indexCacheMu sync.Mutex
	indexCache   = map[string]*cachedIndex{}
)

type cachedIndex struct {
	idx         *search.Index
	buildTook   time.Duration
	buildNoted  bool
	buildFailed bool
}

// RegisterSearchCode registers the search_code tool on the given MCP server.
func RegisterSearchCode(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("search_code",
		mcp.WithDescription("Semantic-ish search over Go symbols and docstrings using BM25. Indexes Go source files (functions, methods, types, consts, vars) with their preceding doc comments. v1 = Go only; non-Go workspaces return a clear 'Go-only' message."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Free-form search text. Tokenised by CamelCase / snake_case / kebab-case; stopwords dropped.")),
		mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Maximum number of results to return. Default %d, clamped to %d.", defaultSearchLimit, maxSearchLimit))),
	)
	s.AddTool(tool, HandleSearchCode(deps))
}

// HandleSearchCode returns the search_code handler.
func HandleSearchCode(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)
		query, _ := args["query"].(string)
		if strings.TrimSpace(query) == "" {
			return ErrorResult("query is required"), nil
		}
		limit := parseSearchLimit(args)

		cached, firstBuild := getOrBuildIndex(ctx, deps.Workspace.Root())
		if cached.buildFailed {
			return ErrorResult("search_code: failed to build index"), nil
		}

		if cached.idx.FileCount() == 0 {
			return TextResult(emptyWorkspaceMessage), nil
		}

		results := cached.idx.Search(query, limit)
		body := formatResults(query, results, firstBuild, cached.buildTook)
		return TextResult(body), nil
	}
}

// getOrBuildIndex returns the singleton Index for the given workspace root,
// building on first call. firstBuild is true only on the call that actually
// triggered the build; subsequent calls see false.
func getOrBuildIndex(ctx context.Context, root string) (*cachedIndex, bool) {
	indexCacheMu.Lock()
	if c, ok := indexCache[root]; ok {
		firstBuild := !c.buildNoted
		c.buildNoted = true
		indexCacheMu.Unlock()
		return c, firstBuild
	}
	indexCacheMu.Unlock()

	start := time.Now()
	idx, err := search.Build(root)
	took := time.Since(start)
	c := &cachedIndex{idx: idx, buildTook: took, buildFailed: err != nil}
	if err == nil {
		// Lazy-start the watcher so stale indexes don't linger after files
		// change mid-session.
		_ = idx.Watch(ctx)
	}

	indexCacheMu.Lock()
	// Second writer wins only if we lost a race; that's fine.
	if existing, ok := indexCache[root]; ok {
		firstBuild := !existing.buildNoted
		existing.buildNoted = true
		indexCacheMu.Unlock()
		return existing, firstBuild
	}
	indexCache[root] = c
	c.buildNoted = true
	indexCacheMu.Unlock()
	return c, true
}

// parseSearchLimit clamps limit to [1, maxSearchLimit]; unset or <=0 returns
// the default.
func parseSearchLimit(args map[string]any) int {
	v, ok := args["limit"].(float64)
	if !ok || int(v) <= 0 {
		return defaultSearchLimit
	}
	n := int(v)
	if n > maxSearchLimit {
		return maxSearchLimit
	}
	return n
}

// formatResults renders results into the display format described in the
// issue (numbered entries with file, symbol, kind, signature, doc excerpt,
// score).
func formatResults(query string, results []search.Result, firstBuild bool, buildTook time.Duration) string {
	var sb strings.Builder
	if len(results) == 0 {
		fmt.Fprintf(&sb, noResultsMessageFormat+"\n", query)
	} else {
		fmt.Fprintf(&sb, "%d results for %q:\n\n", len(results), query)
		for i, r := range results {
			writeResult(&sb, i+1, r)
		}
	}
	if firstBuild {
		note := fmt.Sprintf("(first call: index built in %s)\n", buildTook.Round(time.Millisecond))
		if buildTook > coldStartBudgetSec*time.Second {
			note = fmt.Sprintf("(first call: index built in %s — exceeds %ds cold-start budget)\n", buildTook.Round(time.Millisecond), coldStartBudgetSec)
		}
		sb.WriteString("\n")
		sb.WriteString(note)
	}
	return sb.String()
}

// writeResult renders one numbered entry.
func writeResult(sb *strings.Builder, n int, r search.Result) {
	u := r.Unit
	fmt.Fprintf(sb, "%d. %s   %s  %s\n", n, u.File, u.Symbol, symbolKindTag(u))
	if u.Signature != "" {
		fmt.Fprintf(sb, "   %s\n", u.Signature)
	}
	if u.Doc != "" {
		fmt.Fprintf(sb, "   %s\n", truncateExcerpt(u.Doc))
	}
	fmt.Fprintf(sb, "   score %.2f\n\n", r.Score)
}

// symbolKindTag renders the kind with a leading bracket so the output stays
// scannable (e.g. "[func]").
func symbolKindTag(u search.Unit) string {
	if u.Kind == "" {
		return ""
	}
	return "[" + u.Kind + "]"
}

func truncateExcerpt(s string) string {
	if len(s) <= docExcerptMaxChars {
		return s
	}
	return s[:docExcerptMaxChars]
}
