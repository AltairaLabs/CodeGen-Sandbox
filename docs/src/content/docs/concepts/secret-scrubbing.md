---
title: Secret scrubbing
description: Redacting well-known secret shapes from every tool's text output.
---

Every tool's text output passes through a regex-based scrubber before it leaves the sandbox. Well-known secret shapes are replaced with `[REDACTED:<type>]`.

## Pattern registry

Applied in order (more specific patterns first):

| Name | Shape |
|---|---|
| `aws-access-key` | `\b(?:AKIA\|ASIA)[0-9A-Z]{16}\b` |
| `github-fine-grained-pat` | `\bgithub_pat_[A-Za-z0-9_]{82,}\b` |
| `github-pat` | `\bghp_[A-Za-z0-9]{36,}\b` |
| `github-oauth` | `\bgh[ousr]_[A-Za-z0-9]{36,}\b` |
| `anthropic-api-key` | `\bsk-ant-[A-Za-z0-9_-]{20,}\b` |
| `openai-api-key` | `\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}\b` |
| `google-api-key` | `\bAIza[0-9A-Za-z_-]{35}\b` |
| `slack-token` | `\bxox[abpr]-[A-Za-z0-9-]{10,}\b` |
| `stripe-live-key` | `\b(?:sk\|rk)_live_[A-Za-z0-9]{24,}\b` |
| `jwt` | `\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b` |
| `pem-private-key` | `-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----[\s\S]*?-----END (?:[A-Z]+ )?PRIVATE KEY-----` |
| `basic-auth-url` | `[a-z][a-z0-9+.-]*://[^:\s/@]+:[^@\s]+@[\w.-]+` |
| `secret-env-assignment` | `(?i)\b(?:API_KEY\|TOKEN\|SECRET\|PASSWORD\|PASSWD\|PRIVATE_KEY)\s*=\s*\S+` |

## Where it hooks

The `server` package wraps every tool handler with `scrubMiddleware`:

```go
func scrubMiddleware(handler mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc {
    return func(ctx, req) (*mcp.CallToolResult, error) {
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
```

Wiring happens once in `server.New`: a `scrubbingRegistrar` satisfies the minimal `tools.Registrar` interface (just `AddTool`), and all `RegisterX` calls accept the interface. No tool handler needs to know scrubbing exists.

## Why pattern-based

The alternative — entropy-based detection (TruffleHog, gitleaks' `generic` rule) — catches novel secret shapes but has a higher false-positive rate. For v1, the scrubber is explicitly conservative: it catches ~95% of accidental leaks from well-known providers and misses genuinely novel shapes. Container network policy and credential rotation close the gap.

## Known limits

- **False positives.** `echo "don't use AKIAIOSFODNN7EXAMPLE"` gets redacted even though it's a demo. Acceptable cost for defense-in-depth.
- **False negatives.** A bespoke internal token in an unfamiliar format passes through untouched. If you run a specific company's sandbox, add a pattern.
- **Partial matches.** An env dump like `API_KEY=value more stuff` is redacted up to the first whitespace; `more stuff` survives. Good enough for most leaks.
- **Order dependence.** Anthropic keys (`sk-ant-`) are listed BEFORE OpenAI (`sk-`) so the more specific label wins. Re-ordering silently changes the redaction label.

## Adding a pattern

Edit `internal/scrub/scrub.go` and add an entry to the `patterns` slice. Add a test case to `scrub_test.go` showing both a positive match and a plausible non-match. Order matters — put specific patterns before general ones.

A future plan may add an `env`-driven extension mechanism so operators can add org-specific patterns without rebuilding; the v1 registry is compile-time.
