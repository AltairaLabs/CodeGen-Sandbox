package tools

import (
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
)

// workspaceArgDescription is the shared schema description for the
// optional `workspace` argument that multi-workspace-aware tools accept.
// Centralised so every polyglot-of-repos tool describes it consistently.
const workspaceArgDescription = "Name of the workspace to target when this sandbox was started with " +
	"-workspaces (multiple roots). " +
	"Optional in single-workspace mode (the sole workspace is used). " +
	"Required in multi-workspace mode — the call returns an actionable error listing the " +
	"configured names if omitted, so the agent picks one explicitly rather than the sandbox guessing."

// ResolveWorkspace picks the Workspace this tool call targets.
//
// Multi-workspace policy (#23):
//
//   - 0 workspaces configured → falls back to deps.Workspace (the pre-
//     multi-workspace single-slot default) so tests and embedders that
//     haven't populated Workspaces still work. An ErrorResult when even
//     deps.Workspace is nil.
//   - 1 workspace + no hint → use it (single-workspace workspaces keep
//     their pre-multi-workspace behaviour — no `workspace` arg needed).
//   - N workspaces + no hint → ErrorResult listing the configured names
//     so the agent picks one explicitly.
//   - hint present → look up the matching workspace, ErrorResult listing
//     the configured set if it isn't there.
//
// Returns (ws, nil) on success; (nil, errResult) on every error path.
// Callers forward errResult straight to MCP — it's already shaped as a
// tool-result error.
func ResolveWorkspace(deps *Deps, args map[string]any) (*workspace.Workspace, *mcp.CallToolResult) {
	hint, _ := args["workspace"].(string)
	hint = strings.TrimSpace(hint)

	// Back-compat: embedders / tests that haven't populated the Set still
	// get the single Workspace they constructed. Keeps unit tests from
	// needing a Set for every Deps construction.
	if deps.Workspaces == nil || deps.Workspaces.Len() == 0 {
		if deps.Workspace == nil {
			return nil, ErrorResult("no workspace configured (BUG)")
		}
		if hint != "" && deps.Workspace.Name() != "" && hint != deps.Workspace.Name() {
			return nil, ErrorResult("unknown workspace %q; configured: %s", hint, deps.Workspace.Name())
		}
		return deps.Workspace, nil
	}

	if hint == "" {
		if def := deps.Workspaces.Default(); def != nil {
			return def, nil
		}
		return nil, ErrorResult(
			"multi-workspace sandbox: %d workspaces configured (%s) — pass `workspace` to pick one",
			deps.Workspaces.Len(), strings.Join(deps.Workspaces.SortedNames(), ", "),
		)
	}

	ws, err := deps.Workspaces.Get(hint)
	if err != nil {
		return nil, ErrorResult(
			"unknown workspace %q; configured: %s",
			hint, strings.Join(deps.Workspaces.SortedNames(), ", "),
		)
	}
	return ws, nil
}

// withWorkspaceArg returns the mcp.WithString option that adds the
// `workspace` argument to a multi-workspace-aware tool schema. Single
// source of truth so every tool that accepts `workspace` describes it
// the same way.
func withWorkspaceArg() mcp.ToolOption {
	return mcp.WithString("workspace", mcp.Description(workspaceArgDescription))
}
