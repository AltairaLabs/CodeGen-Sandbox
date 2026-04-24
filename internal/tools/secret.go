package tools

import (
	"context"
	"log"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// mcpSub is the audit `sub=` tag for MCP-surface calls. The MCP server has
// no per-caller auth today (PromptKit opens a single SSE session); a single
// constant keeps the audit line shape aligned with the api/ package while
// making clear where the call originated.
const mcpSub = "mcp"

// RegisterSecrets registers the `secret` and `secrets_available` tools.
func RegisterSecrets(s ToolAdder, deps *Deps) {
	secretTool := mcp.NewTool("secret",
		mcp.WithDescription("Return the value of a named operator-configured secret. "+
			"Names are case-insensitive. Every call is audit-logged (name + length + source, never the value). "+
			"Use secrets_available to discover configured names."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Secret name (case-insensitive).")),
	)
	s.AddTool(secretTool, HandleSecret(deps))

	listTool := mcp.NewTool("secrets_available",
		mcp.WithDescription("List the names of configured secrets. Values are not returned."),
	)
	s.AddTool(listTool, HandleSecretsAvailable(deps))
}

// HandleSecret returns the handler for the `secret` tool. The handler is
// nil-safe: deps.Secrets == nil yields "secrets not configured" rather than
// a panic, so a sandbox started without -secrets-dir / env entries stays
// useful for non-credential workflows.
func HandleSecret(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)
		name, _ := args["name"].(string)
		if name == "" {
			return ErrorResult("name is required"), nil
		}
		if !isValidSecretName(name) {
			return ErrorResult("invalid secret name: %s", name), nil
		}
		if deps.Secrets == nil {
			return ErrorResult("secrets not configured"), nil
		}

		value, ok, src := deps.Secrets.Get(name)
		if !ok {
			available := sortedNames(deps)
			return ErrorResult("unknown secret: %s (available: %s)", strings.ToLower(name), strings.Join(available, ", ")), nil
		}

		// Audit first, return second — even if the response write fails
		// downstream, the access is on record.
		log.Printf("api secret sub=%s name=%s len=%d source=%s", mcpSub, strings.ToLower(name), len(value), src)
		return TextResult(value), nil
	}
}

// HandleSecretsAvailable returns the handler for the `secrets_available`
// tool. Safe when deps.Secrets is nil — returns an empty list rather than
// an error, consistent with "no secrets are configured here".
func HandleSecretsAvailable(deps *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return TextResult(strings.Join(sortedNames(deps), "\n")), nil
	}
}

func sortedNames(deps *Deps) []string {
	if deps.Secrets == nil {
		return nil
	}
	names := deps.Secrets.Names()
	sort.Strings(names)
	return names
}

// isValidSecretName enforces the narrow name grammar at the tool-call layer
// so path-traversal attempts never reach the store. Permitted: ASCII
// letters, digits, underscore, dash. Names must be non-empty.
func isValidSecretName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}
