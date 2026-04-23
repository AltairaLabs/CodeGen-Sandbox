---
title: WebSearch
description: Pluggable web search. Brave, Exa, Tavily backends configured via env var.
---

Search the web. Backend-pluggable; operator provides the API key.

:::caution[Stub in current build]
The v1 sandbox ships the tool registration and error paths, but no backend dispatch. Setting `CODEGEN_SANDBOX_SEARCH_BACKEND=brave|exa|tavily` currently returns `backend X is not yet implemented in this build`. Real backend wiring is a follow-up config plan.
:::

## Schema

| Param | Type | Required | Notes |
|---|---|---|---|
| `query` | string | yes | Search query. |
| `limit` | number | no | Backend-specific default when omitted. |

## Configuration

Two env vars control WebSearch:

- `CODEGEN_SANDBOX_SEARCH_BACKEND` — one of `brave`, `exa`, `tavily`. Unset means WebSearch is disabled.
- `CODEGEN_SANDBOX_<BACKEND>_API_KEY` — the API key for the chosen backend.

When the env var is unset:

```
WebSearch not configured. Set CODEGEN_SANDBOX_SEARCH_BACKEND=brave|exa|tavily
and the corresponding API key env var.
```

## Why a stub now

The proposal says "Backend-pluggable (Brave / Exa / Tavily)". Each real backend needs an API key, which is an operator-supplied secret — wiring them in the v1 binary would couple the sandbox to specific vendors without a way for operators to opt out. The stub approach keeps the tool visible in `tools/list` (so agents know it exists) while deferring the pluggability to config.

## Related

- [WebFetch](/tools/web-fetch/) — for fetching a URL directly after a search result.
- [Extending](/operations/extending/) — how to add a new WebSearch backend.
