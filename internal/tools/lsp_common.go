package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/lsp"
	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
)

// RegisterLSPTools registers the three LSP-backed navigation tools
// (find_definition + find_references + rename_symbol). Kept as a
// convenience wrapper so existing tests / callers that want the full
// set don't have to touch every call site when the read-only / mutating
// split lands; the server uses the split halves directly so it can gate
// rename_symbol on read-only mode.
func RegisterLSPTools(s ToolAdder, deps *Deps) {
	RegisterLSPNavigation(s, deps)
	RegisterLSPRename(s, deps)
}

// RegisterLSPNavigation registers the read-only LSP navigation tools
// (find_definition + find_references). Safe to expose in read-only mode.
func RegisterLSPNavigation(s ToolAdder, deps *Deps) {
	registerFindDefinition(s, deps)
	registerFindReferences(s, deps)
}

// RegisterLSPRename registers the mutating LSP tool (rename_symbol).
// Skipped in read-only mode.
func RegisterLSPRename(s ToolAdder, deps *Deps) {
	registerRenameSymbol(s, deps)
}

const (
	errLSPFilePathRequired = "file_path is required"
	errLSPLineRequired     = "line is required (1-based)"
	errLSPColRequired      = "col is required (1-based)"
	errLSPNewNameRequired  = "new_name is required"
	errLSPNoDetector       = "no language detected in workspace — LSP navigation requires a recognised project (go.mod, etc.)"
	errLSPNotConfigured    = "LSP not configured for %s"
	lspGoplsHint           = "gopls not found on PATH. See docs/concepts/language-support for the image composition model (gopls ships in codegen-sandbox-tools-go)."
)

// lspPosArgs is the shared parameter shape for find_definition / find_references.
type lspPosArgs struct {
	filePath string
	line     int
	col      int
}

func parseLSPPosArgs(args map[string]any) (*lspPosArgs, *mcp.CallToolResult) {
	filePath, _ := args["file_path"].(string)
	if filePath == "" {
		return nil, ErrorResult(errLSPFilePathRequired)
	}
	line, ok := intArg(args, "line")
	if !ok || line <= 0 {
		return nil, ErrorResult(errLSPLineRequired)
	}
	col, ok := intArg(args, "col")
	if !ok || col <= 0 {
		return nil, ErrorResult(errLSPColRequired)
	}
	return &lspPosArgs{filePath: filePath, line: line, col: col}, nil
}

// intArg extracts a numeric argument, accepting either JSON number (float64)
// or an explicit int (some MCP clients round-trip integers).
func intArg(args map[string]any, key string) (int, bool) {
	if v, ok := args[key].(float64); ok {
		return int(v), true
	}
	if v, ok := args[key].(int); ok {
		return v, true
	}
	return 0, false
}

// resolveLSPFile resolves filePath against the workspace, verifies it exists
// and is a regular file, and returns both the absolute and workspace-relative
// forms. The workspace-relative form is what ends up in human-visible output.
func resolveLSPFile(deps *Deps, filePath string) (abs, rel string, errRes *mcp.CallToolResult) {
	a, err := deps.Workspace.Resolve(filePath)
	if err != nil {
		return "", "", ErrorResult("resolve path: %v", err)
	}
	info, err := os.Stat(a)
	if err != nil {
		return "", "", ErrorResult("stat: %v", err)
	}
	if info.IsDir() {
		return "", "", ErrorResult("path is a directory: %s", filePath)
	}
	r, err := filepath.Rel(deps.Workspace.Root(), a)
	if err != nil {
		r = filePath
	}
	return a, r, nil
}

// acquireLSPClient picks the Detector for the workspace, looks up its LSP
// argv, and returns a ready-to-use client. The returned error result is
// populated for the three failure modes (no detector / LSP not configured /
// server binary missing) and nil on success.
func acquireLSPClient(ctx context.Context, deps *Deps) (*lsp.Client, *mcp.CallToolResult) {
	if deps.LSPRegistry == nil {
		return nil, ErrorResult("LSP registry unavailable (BUG)")
	}
	detector := verify.Detect(deps.Workspace.Root())
	if detector == nil {
		return nil, ErrorResult(errLSPNoDetector)
	}
	lang := detector.Language()
	if len(detector.LSPCommand()) == 0 {
		return nil, ErrorResult(errLSPNotConfigured, lang)
	}
	c, err := deps.LSPRegistry.Get(ctx, deps.Workspace.Root(), lang)
	if err != nil {
		if strings.Contains(err.Error(), "not found on PATH") && lang == "go" {
			return nil, ErrorResult("%s", lspGoplsHint)
		}
		return nil, ErrorResult("%v", err)
	}
	return c, nil
}

// previewLine returns a single-line context snippet for (abs, line). Returns
// "" on any I/O failure — previews are decoration, not load-bearing.
func previewLine(abs string, line int) string {
	if line <= 0 {
		return ""
	}
	f, err := os.Open(abs) //nolint:gosec // workspace-contained by caller
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for i := 1; scanner.Scan(); i++ {
		if i == line {
			return strings.TrimSpace(scanner.Text())
		}
	}
	return ""
}

// absFromRel turns a location's (possibly workspace-relative) URI into an
// absolute path for preview-line lookups.
func absFromRel(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

// formatLocationList renders "N <item>s … for <symbol>" at <file:line>:
// one per line with a preview snippet. Used by find_definition / find_references.
func formatLocationList(header string, root string, locs []lsp.Location) string {
	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n")
	for i, loc := range locs {
		preview := previewLine(absFromRel(root, loc.URI), loc.Line)
		fmt.Fprintf(&sb, "  %d. %s:%d:%d", i+1, loc.URI, loc.Line, loc.Col)
		if preview != "" {
			fmt.Fprintf(&sb, "  %s", preview)
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}
