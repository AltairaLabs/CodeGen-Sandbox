package tools

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/lsp"
	"github.com/mark3labs/mcp-go/mcp"
)

func registerRenameSymbol(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("rename_symbol",
		mcp.WithDescription("Compute the structured rename of the symbol at file_path:line:col → new_name using the language server. Does NOT apply the edit — returns a diff for the agent to review before committing via Edit. Requires a prior Read of the file."),
		mcp.WithString("file_path", mcp.Required(), mcp.Description("Absolute or workspace-relative path to the source file.")),
		mcp.WithNumber("line", mcp.Required(), mcp.Description("1-based line number of the cursor position.")),
		mcp.WithNumber("col", mcp.Required(), mcp.Description("1-based column number of the cursor position.")),
		mcp.WithString("new_name", mcp.Required(), mcp.Description("The new identifier. Language-server validates legality.")),
	)
	s.AddTool(tool, HandleRenameSymbol(deps))
}

// HandleRenameSymbol returns the rename_symbol handler.
func HandleRenameSymbol(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)
		parsed, newName, errRes := parseRenameArgs(args)
		if errRes != nil {
			return errRes, nil
		}

		abs, rel, errRes := resolveLSPFile(deps, parsed.filePath)
		if errRes != nil {
			return errRes, nil
		}
		if deps.Tracker != nil && !deps.Tracker.HasBeenRead(abs) {
			return ErrorResult("refusing to rename from %s: Read it first", parsed.filePath), nil
		}

		client, errRes := acquireLSPClient(ctx, deps)
		if errRes != nil {
			return errRes, nil
		}

		edit, err := client.Rename(ctx, rel, parsed.line, parsed.col, newName)
		if err != nil {
			return ErrorResult("rename_symbol: %v", err), nil
		}
		if len(edit.Changes) == 0 {
			return TextResult(fmt.Sprintf("no rename available at %s:%d:%d", rel, parsed.line, parsed.col)), nil
		}

		return TextResult(formatRenameEdit(deps.Workspace.Root(), newName, edit)), nil
	}
}

func parseRenameArgs(args map[string]any) (*lspPosArgs, string, *mcp.CallToolResult) {
	parsed, errRes := parseLSPPosArgs(args)
	if errRes != nil {
		return nil, "", errRes
	}
	newName, _ := args["new_name"].(string)
	if newName == "" {
		return nil, "", ErrorResult(errLSPNewNameRequired)
	}
	return parsed, newName, nil
}

// formatRenameEdit renders the workspace edit as a header + per-file diff
// block. The diff is computed by applying each TextEdit to the file content
// (in reverse order, so earlier positions remain valid) and emitting a
// unified-ish hunk per modified range.
func formatRenameEdit(root, newName string, edit lsp.WorkspaceEdit) string {
	files := make([]string, 0, len(edit.Changes))
	for f := range edit.Changes {
		files = append(files, f)
	}
	sort.Strings(files)

	var sb strings.Builder
	fmt.Fprintf(&sb, "Rename → %q would touch %d file(s):\n", newName, len(files))
	for _, f := range files {
		fmt.Fprintf(&sb, "  - %s (%d edit(s))\n", f, len(edit.Changes[f]))
	}
	sb.WriteString("\nReview the diff below; apply via Edit once approved:\n\n")
	for _, f := range files {
		sb.WriteString(renderFileDiff(root, f, edit.Changes[f]))
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// renderFileDiff reads the file and produces a unified-diff-shaped block
// showing each edit's before/after. Returns a placeholder block if the file
// can't be read (edit still tells the agent what the LSP proposed).
func renderFileDiff(root, rel string, edits []lsp.TextEdit) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- a/%s\n+++ b/%s\n", rel, rel)
	abs := absFromRel(root, rel)
	data, err := os.ReadFile(abs) //nolint:gosec // workspace-contained
	if err != nil {
		fmt.Fprintf(&sb, "(file unreadable: %v)\n", err)
		return sb.String()
	}
	lines := strings.Split(string(data), "\n")

	// Sort by start position so hunks are in file order. LSP doesn't
	// guarantee ordering.
	sorted := make([]lsp.TextEdit, len(edits))
	copy(sorted, edits)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Line != sorted[j].Line {
			return sorted[i].Line < sorted[j].Line
		}
		return sorted[i].Col < sorted[j].Col
	})
	for _, e := range sorted {
		writeHunk(&sb, lines, e)
	}
	return sb.String()
}

// writeHunk emits one @@ hunk for a single TextEdit. We show the line that
// contains the edit (for single-line ranges) as `-` and `+` lines.
func writeHunk(sb *strings.Builder, lines []string, e lsp.TextEdit) {
	idx := e.Line - 1
	if idx < 0 || idx >= len(lines) {
		fmt.Fprintf(sb, "@@ %d,1 @@\n+%s\n", e.Line, e.NewText)
		return
	}
	before := lines[idx]
	endCol := e.EndCol - 1
	startCol := e.Col - 1
	if endCol > len(before) {
		endCol = len(before)
	}
	if startCol > len(before) {
		startCol = len(before)
	}
	if startCol > endCol {
		startCol = endCol
	}
	after := before[:startCol] + e.NewText + before[endCol:]
	fmt.Fprintf(sb, "@@ -%d,1 +%d,1 @@\n-%s\n+%s\n", e.Line, e.Line, before, after)
}
