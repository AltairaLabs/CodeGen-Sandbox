# Codegen Sandbox — Secret Scrubbing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an MCP middleware layer that scrubs well-known secret token shapes (AWS keys, GitHub PATs, OpenAI/Anthropic keys, Google API keys, JWTs, Slack tokens, Stripe keys, PEM private keys, basic-auth URLs, and `KEY=value` env forms) from every tool's text output before it leaves the sandbox.

**Architecture:** A new `internal/scrub` package owns a compiled pattern registry and a single `Scrub(text)` function. `internal/tools/tools.go` gains a tiny `Registrar` interface matching `*mcpserver.MCPServer.AddTool`'s shape; the nine existing `Register*` functions accept this interface instead of the concrete type. `internal/server/middleware.go` introduces a `scrubbingRegistrar` that wraps every handler with a small post-processing pass before delegating to the underlying MCP server. The wiring is one-line at `Server.New`.

**Tech Stack:** Go 1.25+, `github.com/mark3labs/mcp-go` v0.49.0, `github.com/stretchr/testify`.

**Out of scope for this plan:**
- Custom per-project pattern overrides — the pattern list is hard-coded for v1. A future plan can add workspace-local or env-var-driven extensions.
- Structured-field scrubbing — today every `CallToolResult.Content` is `mcp.TextContent`; non-text content (images, resources) is untouched. If a future tool returns binary content, the middleware is a no-op for it.
- Allowlists / per-tool opt-out — every tool output is scrubbed; no escape hatch.
- Full entropy-based detection (TruffleHog-style) — shape-only patterns. False-negatives on high-entropy secrets that don't match a known shape are accepted: container isolation remains the real trust boundary.
- Scrubbing the agent's own arguments — we scrub outputs going TO the agent, not inputs coming FROM it. The agent already knows its own inputs.

---

## File Structure

Files introduced or modified by this plan:

| Path | Responsibility |
|---|---|
| `internal/scrub/scrub.go` | Compiled regex registry + `Scrub(text string) string`. Each pattern redacts to `[REDACTED:<type>]`. |
| `internal/scrub/scrub_test.go` | One table-driven test per pattern + a combined "many patterns in one string" test + a no-op happy-path test. |
| `internal/tools/tools.go` | Add a small `Registrar` interface matching `*mcpserver.MCPServer.AddTool`'s signature. |
| `internal/tools/read.go`, `write.go`, `edit.go`, `glob.go`, `grep.go`, `bash.go`, `run_tests.go`, `run_lint.go`, `run_typecheck.go` | One-line signature change on each `Register*` function: `s *mcpserver.MCPServer` → `s Registrar`. Function bodies unchanged. |
| `internal/server/middleware.go` | `scrubMiddleware(handler) handler` wrapper that walks the `CallToolResult.Content` and scrubs each text block. `scrubbingRegistrar` struct implementing `tools.Registrar` — forwards `AddTool` to the underlying `*mcpserver.MCPServer` after wrapping the handler. |
| `internal/server/middleware_test.go` | Middleware tests — a stub handler returns known-secret text; the wrapper redacts it. |
| `internal/server/server.go` | In `New`, wire each `tools.Register*` call to go through a `scrubbingRegistrar` instead of `s.mcp`. |

Design rule: `internal/scrub` knows nothing about MCP; it's a pure string transform. The server package owns the middleware glue. The tools package loses its hard dependency on `*mcpserver.MCPServer` (now accepts any `Registrar`), which also makes the handlers easier to test in isolation.

---

## Task 1: Scrub package

**Files:**
- Create: `internal/scrub/scrub.go`
- Test: `internal/scrub/scrub_test.go`

**Contract of `Scrub(text string) string`:**
- Applies each compiled pattern in a fixed order.
- Each match is replaced with `[REDACTED:<type>]` where `<type>` is the pattern name (e.g. `aws-access-key`, `github-pat`, `bearer-basic-auth`).
- A pattern can match multiple times in the same input; all matches are redacted.
- Pattern order matters when patterns overlap; list more-specific patterns before more-general ones.
- If no patterns match, returns the input unchanged (same string, not a copy — but callers should not rely on identity).

**Pattern registry (exactly this list, in this order):**

| Name | Pattern (Go regex) |
|---|---|
| `aws-access-key` | `\b(?:AKIA|ASIA)[0-9A-Z]{16}\b` |
| `github-fine-grained-pat` | `\bgithub_pat_[A-Za-z0-9_]{82,}\b` |
| `github-pat` | `\bghp_[A-Za-z0-9]{36,}\b` |
| `github-oauth` | `\bgh[ousr]_[A-Za-z0-9]{36,}\b` |
| `anthropic-api-key` | `\bsk-ant-[A-Za-z0-9_-]{20,}\b` |
| `openai-api-key` | `\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}\b` |
| `google-api-key` | `\bAIza[0-9A-Za-z_-]{35}\b` |
| `slack-token` | `\bxox[abpr]-[A-Za-z0-9-]{10,}\b` |
| `stripe-live-key` | `\b(?:sk|rk)_live_[A-Za-z0-9]{24,}\b` |
| `jwt` | `\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b` |
| `pem-private-key` | `-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----[\s\S]*?-----END (?:[A-Z]+ )?PRIVATE KEY-----` |
| `basic-auth-url` | `[a-z][a-z0-9+.-]*://[^:\s/@]+:[^@\s]+@[\w.-]+` |
| `secret-env-assignment` | `(?i)\b(?:API_KEY\|TOKEN\|SECRET\|PASSWORD\|PASSWD\|PRIVATE_KEY)\s*=\s*\S+` |

Note on the last pattern: the env-assignment one redacts the WHOLE `KEY=value` span (not just the value), because replacing inside a `(key)=(\S+)` group in Go regex with partial preservation is awkward; redacting the whole assignment is safer and the agent still sees that something was filtered.

- [ ] **Step 1: Write the failing tests**

Create `internal/scrub/scrub_test.go`:
```go
package scrub_test

import (
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/scrub"
	"github.com/stretchr/testify/assert"
)

func TestScrub_Unchanged(t *testing.T) {
	in := "nothing to see here — just some source code\nlike func foo() {}"
	assert.Equal(t, in, scrub.Scrub(in))
}

func TestScrub_AWSAccessKey(t *testing.T) {
	in := "AWS key: AKIAIOSFODNN7EXAMPLE leaked"
	out := scrub.Scrub(in)
	assert.NotContains(t, out, "AKIAIOSFODNN7EXAMPLE")
	assert.Contains(t, out, "[REDACTED:aws-access-key]")
}

func TestScrub_GithubPAT(t *testing.T) {
	in := "token=ghp_" + strings.Repeat("a", 36) + " end"
	out := scrub.Scrub(in)
	assert.NotContains(t, out, "ghp_")
	assert.Contains(t, out, "[REDACTED:github-pat]")
}

func TestScrub_GithubFineGrainedPAT(t *testing.T) {
	in := "tok=github_pat_" + strings.Repeat("A", 82) + " end"
	out := scrub.Scrub(in)
	assert.NotContains(t, out, "github_pat_A")
	assert.Contains(t, out, "[REDACTED:github-fine-grained-pat]")
}

func TestScrub_GithubOAuth(t *testing.T) {
	in := "app=gho_" + strings.Repeat("x", 36) + " svc=ghs_" + strings.Repeat("y", 36)
	out := scrub.Scrub(in)
	assert.NotContains(t, out, "gho_xxx")
	assert.NotContains(t, out, "ghs_yyy")
	assert.Equal(t, 2, strings.Count(out, "[REDACTED:github-oauth]"))
}

func TestScrub_AnthropicAPIKey(t *testing.T) {
	in := "key=sk-ant-" + strings.Repeat("a", 30)
	out := scrub.Scrub(in)
	assert.Contains(t, out, "[REDACTED:anthropic-api-key]")
	// Must match anthropic BEFORE the generic openai "sk-" pattern.
	assert.NotContains(t, out, "[REDACTED:openai-api-key]")
}

func TestScrub_OpenAIAPIKey(t *testing.T) {
	in := "OPENAI_API_KEY=sk-" + strings.Repeat("a", 48)
	out := scrub.Scrub(in)
	// The env-assignment pattern also fires here; either redaction is acceptable
	// as long as the raw key is gone.
	assert.NotContains(t, out, strings.Repeat("a", 48))
}

func TestScrub_OpenAIProjectKey(t *testing.T) {
	in := "key=sk-proj-" + strings.Repeat("a", 30)
	out := scrub.Scrub(in)
	assert.Contains(t, out, "[REDACTED:openai-api-key]")
}

func TestScrub_GoogleAPIKey(t *testing.T) {
	in := "url=https://example.com?key=AIza" + strings.Repeat("A", 35)
	out := scrub.Scrub(in)
	assert.Contains(t, out, "[REDACTED:google-api-key]")
	assert.NotContains(t, out, "AIzaAAAAAAAAA")
}

func TestScrub_SlackToken(t *testing.T) {
	in := "hook: xoxb-" + strings.Repeat("1", 20)
	out := scrub.Scrub(in)
	assert.Contains(t, out, "[REDACTED:slack-token]")
}

func TestScrub_StripeLiveKey(t *testing.T) {
	in := "cfg: sk_live_" + strings.Repeat("X", 30)
	out := scrub.Scrub(in)
	assert.Contains(t, out, "[REDACTED:stripe-live-key]")
}

func TestScrub_JWT(t *testing.T) {
	in := "Bearer eyJ" + strings.Repeat("a", 20) + ".eyJ" + strings.Repeat("b", 20) + "." + strings.Repeat("c", 20)
	out := scrub.Scrub(in)
	assert.Contains(t, out, "[REDACTED:jwt]")
	assert.NotContains(t, out, "eyJaaaa")
}

func TestScrub_PEMPrivateKey(t *testing.T) {
	in := "pem:\n-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA...\n-----END RSA PRIVATE KEY-----\ntail"
	out := scrub.Scrub(in)
	assert.Contains(t, out, "[REDACTED:pem-private-key]")
	assert.NotContains(t, out, "MIIEpAIBAAKCAQEA")
}

func TestScrub_BasicAuthURL(t *testing.T) {
	in := "conn: redis://user:passw0rd!@db.internal:6379/0"
	out := scrub.Scrub(in)
	assert.Contains(t, out, "[REDACTED:basic-auth-url]")
	assert.NotContains(t, out, "passw0rd")
}

func TestScrub_SecretEnvAssignment(t *testing.T) {
	in := "API_KEY=super-secret-42\nTOKEN=abc123\nnormal=value\nPASSWORD=hunter2"
	out := scrub.Scrub(in)
	assert.NotContains(t, out, "super-secret-42")
	assert.NotContains(t, out, "hunter2")
	assert.Contains(t, out, "[REDACTED:secret-env-assignment]")
	// Non-secret assignment survives.
	assert.Contains(t, out, "normal=value")
}

func TestScrub_MultiplePatternsOneInput(t *testing.T) {
	in := "aws=AKIAIOSFODNN7EXAMPLE, gh=ghp_" + strings.Repeat("z", 36) + ", goog=AIza" + strings.Repeat("G", 35)
	out := scrub.Scrub(in)
	assert.Contains(t, out, "[REDACTED:aws-access-key]")
	assert.Contains(t, out, "[REDACTED:github-pat]")
	assert.Contains(t, out, "[REDACTED:google-api-key]")
	// All three raw secrets are gone.
	assert.NotContains(t, out, "AKIAIOSFODNN7EXAMPLE")
	assert.NotContains(t, out, "ghp_zzz")
	assert.NotContains(t, out, "AIzaGG")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/scrub/...
```
Expected: compile error — package `scrub` does not exist.

- [ ] **Step 3: Implement `scrub.go`**

Create `internal/scrub/scrub.go`:
```go
// Package scrub redacts well-known secret token shapes from text before the
// sandbox returns it to the agent. The pattern set is shape-based (not
// entropy-based) and intentionally conservative — container isolation
// remains the real trust boundary; this layer prevents routine accidents
// (stray API keys in logs, `env` dumps via Bash, etc.).
package scrub

import "regexp"

type pattern struct {
	name string
	re   *regexp.Regexp
}

// Order matters: more specific patterns should appear BEFORE more general
// ones (e.g. `sk-ant-...` before the generic `sk-...` OpenAI pattern).
var patterns = []pattern{
	{"aws-access-key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"github-fine-grained-pat", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{82,}\b`)},
	{"github-pat", regexp.MustCompile(`\bghp_[A-Za-z0-9]{36,}\b`)},
	{"github-oauth", regexp.MustCompile(`\bgh[ousr]_[A-Za-z0-9]{36,}\b`)},
	{"anthropic-api-key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`)},
	{"openai-api-key", regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}\b`)},
	{"google-api-key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[abpr]-[A-Za-z0-9-]{10,}\b`)},
	{"stripe-live-key", regexp.MustCompile(`\b(?:sk|rk)_live_[A-Za-z0-9]{24,}\b`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)},
	{"pem-private-key", regexp.MustCompile(`-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----[\s\S]*?-----END (?:[A-Z]+ )?PRIVATE KEY-----`)},
	{"basic-auth-url", regexp.MustCompile(`[a-z][a-z0-9+.-]*://[^:\s/@]+:[^@\s]+@[\w.-]+`)},
	{"secret-env-assignment", regexp.MustCompile(`(?i)\b(?:API_KEY|TOKEN|SECRET|PASSWORD|PASSWD|PRIVATE_KEY)\s*=\s*\S+`)},
}

// Scrub returns text with every match of a known-secret pattern replaced by
// `[REDACTED:<pattern-name>]`. Order of application is fixed; more specific
// patterns fire first so an Anthropic key isn't labelled as an OpenAI key.
func Scrub(text string) string {
	for _, p := range patterns {
		text = p.re.ReplaceAllString(text, "[REDACTED:"+p.name+"]")
	}
	return text
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/scrub/... -v
```
Expected: all tests PASS.

- [ ] **Step 5: Lint**

Run:
```bash
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox lint
```
Expected: 0 issues.

- [ ] **Step 6: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/scrub/scrub.go internal/scrub/scrub_test.go
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(scrub): add secret pattern registry with Scrub(text) helper
EOF
```

---

## Task 2: Registrar interface + scrubMiddleware + wire-up

**Files:**
- Modify: `internal/tools/tools.go` — add `Registrar` interface.
- Modify: `internal/tools/read.go`, `write.go`, `edit.go`, `glob.go`, `grep.go`, `bash.go`, `run_tests.go`, `run_lint.go`, `run_typecheck.go` — change `Register*`'s first parameter from `*mcpserver.MCPServer` to `Registrar`.
- Create: `internal/server/middleware.go` — `scrubMiddleware` + `scrubbingRegistrar`.
- Create: `internal/server/middleware_test.go` — wrapper tests.
- Modify: `internal/server/server.go` — construct a `scrubbingRegistrar` and pass it to each `tools.Register*` call.

**Contract of `scrubMiddleware`:**
- Signature: `func scrubMiddleware(handler mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc`
- On every call:
  1. Invoke the underlying handler.
  2. If the handler returned a Go `error`, pass it through unchanged.
  3. If `res` is non-nil, iterate `res.Content` and replace every `mcp.TextContent`'s `.Text` with `scrub.Scrub(.Text)`.
  4. Return the possibly-mutated `res` and the handler's `error`.
- Non-text content (images, resources) passes through unchanged.

**Contract of `scrubbingRegistrar`:**
- Implements `tools.Registrar`.
- `AddTool(tool, handler)` delegates to the wrapped `*mcpserver.MCPServer.AddTool(tool, scrubMiddleware(handler))`.

**Note on `mcpserver.ToolHandlerFunc`:** mcp-go v0.49 exposes the handler type as `server.ToolHandlerFunc` in its `server` package. If the name differs in your installed version, substitute the explicit signature `func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)` everywhere — Go's structural typing means the `Registrar` interface doesn't care which name you use, only that the shape matches.

- [ ] **Step 1: Write the failing middleware tests**

Create `internal/server/middleware_test.go`:
```go
package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScrubMiddleware_RedactsTextContent(t *testing.T) {
	inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("leaked AKIAIOSFODNN7EXAMPLE sorry"), nil
	}
	wrapped := scrubMiddleware(inner)

	res, err := wrapped(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Len(t, res.Content, 1)
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.NotContains(t, tc.Text, "AKIAIOSFODNN7EXAMPLE")
	assert.Contains(t, tc.Text, "[REDACTED:aws-access-key]")
}

func TestScrubMiddleware_PreservesErrorResultShape(t *testing.T) {
	inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("failed with token ghp_" +
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), nil
	}
	wrapped := scrubMiddleware(inner)

	res, err := wrapped(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError, "error flag should survive scrubbing")
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "[REDACTED:github-pat]")
}

func TestScrubMiddleware_PassesThroughGoError(t *testing.T) {
	wantErr := assert.AnError
	inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, wantErr
	}
	wrapped := scrubMiddleware(inner)

	res, err := wrapped(context.Background(), mcp.CallToolRequest{})
	assert.ErrorIs(t, err, wantErr)
	assert.Nil(t, res)
}

func TestScrubMiddleware_LeavesCleanTextAlone(t *testing.T) {
	inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("nothing secret here"), nil
	}
	wrapped := scrubMiddleware(inner)

	res, _ := wrapped(context.Background(), mcp.CallToolRequest{})
	tc := res.Content[0].(mcp.TextContent)
	assert.Equal(t, "nothing secret here", tc.Text)
}
```

Note: this file is `package server` (white-box) so it can see the unexported `scrubMiddleware`.

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./internal/server/... -run TestScrubMiddleware
```
Expected: compile error — `scrubMiddleware` undefined.

- [ ] **Step 3: Implement the middleware**

Create `internal/server/middleware.go`:
```go
package server

import (
	"context"

	"github.com/altairalabs/codegen-sandbox/internal/scrub"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// scrubMiddleware wraps a handler so every TextContent in its CallToolResult
// passes through the secret scrubber before it leaves the sandbox. Non-text
// content (images, resources) is unchanged. Go-level errors propagate
// unmodified — scrubbing only applies to successful tool results.
func scrubMiddleware(handler mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := handler(ctx, req)
		if err != nil || res == nil {
			return res, err
		}
		for i, c := range res.Content {
			if tc, ok := c.(mcp.TextContent); ok {
				tc.Text = scrub.Scrub(tc.Text)
				res.Content[i] = tc
			}
		}
		return res, nil
	}
}

// scrubbingRegistrar implements tools.Registrar by forwarding AddTool to an
// underlying MCPServer, wrapping the handler with scrubMiddleware first. It
// is how Server.New applies scrubbing uniformly across every tool.
type scrubbingRegistrar struct {
	inner *mcpserver.MCPServer
}

// AddTool wraps handler with scrubMiddleware and forwards to inner.AddTool.
func (r *scrubbingRegistrar) AddTool(tool mcp.Tool, handler mcpserver.ToolHandlerFunc) {
	r.inner.AddTool(tool, scrubMiddleware(handler))
}
```

- [ ] **Step 4: Add the `Registrar` interface in tools package**

Append to `internal/tools/tools.go`:
```go
// Registrar is the subset of *mcpserver.MCPServer that Register* functions
// actually need. Accepting an interface (rather than the concrete type)
// lets the server package wrap handlers with middleware without touching
// each tool registration individually.
type Registrar interface {
	AddTool(tool mcp.Tool, handler mcpserver.ToolHandlerFunc)
}
```

This requires imports for `"github.com/mark3labs/mcp-go/mcp"` and `mcpserver "github.com/mark3labs/mcp-go/server"`. If tools.go doesn't already have them, add them.

- [ ] **Step 5: Change each `Register*` function's signature**

In each of these nine files, change the first parameter from `s *mcpserver.MCPServer` to `s Registrar`. The function body is unchanged — `s.AddTool(...)` works identically through the interface.

Files: `internal/tools/read.go`, `write.go`, `edit.go`, `glob.go`, `grep.go`, `bash.go`, `run_tests.go`, `run_lint.go`, `run_typecheck.go`.

Concretely, each function's first line changes from:
```go
func RegisterRead(s *mcpserver.MCPServer, deps *Deps) {
```
to:
```go
func RegisterRead(s Registrar, deps *Deps) {
```

The `mcpserver` import may become unused in a file after this change (if the file only referenced `*mcpserver.MCPServer`). If so, remove it. If the file still needs `mcpserver` for its handler signature, leave it.

- [ ] **Step 6: Wire the scrubbingRegistrar into `Server.New`**

In `internal/server/server.go`, modify `New` so all tool registrations flow through a `scrubbingRegistrar`. Find the block that currently looks like:
```go
tools.RegisterRead(s.mcp, &tools.Deps{Workspace: s.ws, Tracker: s.tracker})
tools.RegisterWrite(s.mcp, &tools.Deps{Workspace: s.ws, Tracker: s.tracker})
...
```

Replace it with:
```go
reg := &scrubbingRegistrar{inner: s.mcp}
deps := &tools.Deps{Workspace: s.ws, Tracker: s.tracker}
tools.RegisterRead(reg, deps)
tools.RegisterWrite(reg, deps)
tools.RegisterEdit(reg, deps)
tools.RegisterGlob(reg, deps)
tools.RegisterGrep(reg, deps)
tools.RegisterBash(reg, deps)
tools.RegisterRunTests(reg, deps)
tools.RegisterRunLint(reg, deps)
tools.RegisterRunTypecheck(reg, deps)
```

This also DRYs the `&tools.Deps{...}` allocation (previous reviewers flagged the nine copies as a minor smell).

- [ ] **Step 7: Run tests to verify they pass**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox test ./... -race -count=1
```
Expected: all tests pass, including the four new middleware tests.

- [ ] **Step 8: Lint**

Run:
```bash
make -C /Users/chaholl/repos/altairalabs/codegen-sandbox lint
```
Expected: 0 issues.

- [ ] **Step 9: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add internal/tools internal/server
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -F - <<'EOF'
feat(server): scrub secrets from every tool's text output via middleware

Introduces a tools.Registrar interface (minimal AddTool shape) so tool
registration functions no longer depend on the concrete *mcpserver.MCPServer
type. server.New now constructs a scrubbingRegistrar that wraps every
handler with scrubMiddleware before registering it on the underlying MCP
server. Result: every Read, Write, Edit, Glob, Grep, Bash, run_tests,
run_lint, and run_typecheck response is passed through the scrub package's
secret-pattern registry before leaving the sandbox.
EOF
```

---

## Task 3: End-to-end smoke via live MCP call

**Files:** none (manual verification)

- [ ] **Step 1: Build the binary**

Run:
```bash
go -C /Users/chaholl/repos/altairalabs/codegen-sandbox build -o /Users/chaholl/repos/altairalabs/codegen-sandbox/bin/sandbox ./cmd/sandbox
```
Expected: exit 0.

- [ ] **Step 2: Seed a temp workspace with a file containing a fake secret**

Run:
```bash
mkdir -p /tmp/codegen-sandbox-scrub-smoke
cat > /tmp/codegen-sandbox-scrub-smoke/secrets.txt <<'EOF'
aws key: AKIAIOSFODNN7EXAMPLE
github token: ghp_abcdefghijklmnopqrstuvwxyz0123456789
nothing special here
EOF
```

- [ ] **Step 3: Start the server**

Run:
```bash
/Users/chaholl/repos/altairalabs/codegen-sandbox/bin/sandbox -addr=127.0.0.1:18084 -workspace=/tmp/codegen-sandbox-scrub-smoke >/tmp/sandbox-scrub-smoke.log 2>&1 &
echo $! > /tmp/sandbox-scrub-smoke.pid
```
Use `run_in_background` when invoking this through the Bash tool.

- [ ] **Step 4: Initialize and call Read on secrets.txt**

Run:
```bash
curl -sS -N --max-time 4 --output /tmp/sandbox-scrub-sse.txt http://127.0.0.1:18084/sse 2>/dev/null &
SSEPID=$!
sleep 0.3
SESSION_URL=$(grep -o 'data:.*' /tmp/sandbox-scrub-sse.txt | head -1 | sed 's|data: *||' | tr -d '\r\n ')
curl -sS -X POST "http://127.0.0.1:18084${SESSION_URL}" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"smoke","version":"0"},"capabilities":{}}}' >/dev/null
curl -sS -X POST "http://127.0.0.1:18084${SESSION_URL}" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' >/dev/null
curl -sS -X POST "http://127.0.0.1:18084${SESSION_URL}" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"Read","arguments":{"file_path":"secrets.txt"}}}' >/dev/null
sleep 1
kill $SSEPID 2>/dev/null || true
echo '--- Read result ---'
grep -o '"text":"[^"]*"' /tmp/sandbox-scrub-sse.txt | head -1
```
Expected: the emitted text shows `[REDACTED:aws-access-key]` and `[REDACTED:github-pat]` and does NOT contain `AKIAIOSFODNN7EXAMPLE` or the raw `ghp_...` token.

- [ ] **Step 5: Stop the server**

Run:
```bash
kill "$(cat /tmp/sandbox-scrub-smoke.pid)" 2>/dev/null || true
```

- [ ] **Step 6: No commit** (no file changes).

---

## Self-Review Notes

**Spec coverage:**
- Secret-scrub middleware at the MCP entry/exit — Tasks 1+2.
- Applies to every tool — Task 2 integrates at registration time, so coverage is automatic.
- End-to-end wire verification — Task 3.

**Deliberately deferred:**
- Custom pattern extensions via config or env var.
- Entropy-based detection (TruffleHog style).
- Allowlists / per-tool opt-out.
- Scrubbing of input arguments (only outputs).
- Structured / binary content (no-op for now).

**Placeholder scan:** no TBDs, no "implement later", no "add appropriate error handling". Each step has concrete code or commands.

**Type consistency:**
- `Registrar` interface's `AddTool(tool mcp.Tool, handler mcpserver.ToolHandlerFunc)` matches `*mcpserver.MCPServer.AddTool`'s signature; structural satisfaction means existing call sites still work.
- `scrubMiddleware` returns the same `mcpserver.ToolHandlerFunc` type it accepts — composable.
- `scrubbingRegistrar.AddTool` signature matches the interface exactly.
- `scrub.Scrub` is `func(string) string` — plain string transform, no package-level state beyond the compiled regex registry.

**Known trade-offs documented in the plan:**
- Pattern-based detection has false-negatives on novel shapes — container isolation remains the real boundary.
- `secret-env-assignment` redacts the entire `KEY=value` span rather than just the value (Go regex replacement doesn't easily preserve captured prefixes cleanly).
- Pattern order is load-bearing — Anthropic keys must be listed before the generic OpenAI `sk-` pattern.
