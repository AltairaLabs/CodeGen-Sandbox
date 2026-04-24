package tools

import (
	"sort"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/mark3labs/mcp-go/mcp"
)

// languageArgDescription is the shared schema description for the
// optional `language` argument that polyglot-aware verify tools accept.
// Centralised so run_lint / run_tests / run_typecheck / run_failing_tests
// / run_script all describe the arg consistently.
const languageArgDescription = "Project language to dispatch to: \"go\", \"node\", \"python\", or \"rust\". " +
	"Optional in single-language workspaces (the sole detected project is used). " +
	"Required in polyglot workspaces — the call returns an actionable error listing the detected " +
	"set if omitted, so the agent picks one explicitly rather than the sandbox guessing."

// dispatchByLanguage picks the Detector that the agent's request targets.
//
// Polyglot policy (#19):
//
//   - 0 detectors → ErrorResult ("no supported project detected").
//   - 1 detector + no language hint → use it (single-language workspaces
//     keep their pre-polyglot behaviour).
//   - N detectors + no language hint → ErrorResult listing the detected
//     languages so the agent picks one explicitly.
//   - language hint present → look up the matching detector, ErrorResult
//     listing the detected set if it isn't there.
//
// Returns (detector, nil) on success; (nil, errResult) on every error
// path. Callers forward errResult straight to MCP — it's already shaped
// as a tool-result error.
func dispatchByLanguage(deps *Deps, args map[string]any) (verify.Detector, *mcp.CallToolResult) {
	all := verify.DetectAll(deps.Workspace.Root())
	if len(all) == 0 {
		return nil, ErrorResult("no supported project detected in workspace root")
	}

	hint, _ := args["language"].(string)
	hint = strings.TrimSpace(strings.ToLower(hint))

	if hint == "" {
		if len(all) == 1 {
			return all[0], nil
		}
		return nil, ErrorResult(
			"polyglot workspace: %d project types detected (%s) — pass `language` to pick one",
			len(all), formatLanguageList(all),
		)
	}

	for _, d := range all {
		if d.Language() == hint {
			return d, nil
		}
	}
	return nil, ErrorResult(
		"language %q not detected in workspace; detected: %s",
		hint, formatLanguageList(all),
	)
}

// formatLanguageList renders a stable, comma-separated list of detector
// languages for use in error messages. Sort order is alphabetic so error
// strings are deterministic across detector-registration changes.
func formatLanguageList(detectors []verify.Detector) string {
	names := make([]string, 0, len(detectors))
	for _, d := range detectors {
		names = append(names, d.Language())
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// withLanguageArg returns the mcp.WithString option that adds the
// `language` argument to a verify-tool schema. Single source of truth so
// every tool that accepts `language` describes it the same way.
func withLanguageArg() mcp.ToolOption {
	return mcp.WithString("language", mcp.Description(languageArgDescription))
}
