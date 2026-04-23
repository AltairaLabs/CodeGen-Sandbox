---
title: WebSearch
description: "Pluggable web search. Brave, Exa, and Tavily backends selected via env var."
---

Search the web from inside the sandbox. Backend-pluggable — the operator picks one (or swaps) by setting env vars; the agent's query flow is the same regardless of which backend answers.

## Schema

| Param | Type | Required | Notes |
|---|---|---|---|
| `query` | string | yes | Search query. |
| `limit` | number | no | Max results to return (default 10). |

## Configuration

Set exactly two env vars on the sandbox process — `CODEGEN_SANDBOX_SEARCH_BACKEND` picks the backend; the backend's own key env var authorises requests.

### Brave

```bash
CODEGEN_SANDBOX_SEARCH_BACKEND=brave
BRAVE_API_KEY=<brave-api-key>
```

Sign up at [brave.com/search/api](https://brave.com/search/api/). Free tier: 2000 queries/month.

### Exa

```bash
CODEGEN_SANDBOX_SEARCH_BACKEND=exa
EXA_API_KEY=<exa-api-key>
```

Sign up at [exa.ai](https://exa.ai/). Free tier: 1000 requests/month. Neural / semantic search — good for "find me conceptually-related content" queries.

### Tavily

```bash
CODEGEN_SANDBOX_SEARCH_BACKEND=tavily
TAVILY_API_KEY=<tavily-api-key>
```

Sign up at [tavily.com](https://tavily.com/). Free tier: 1000 calls/month. Results are pre-processed for LLM consumption — tight snippets, deduplication.

### When nothing is configured

The tool stays registered (so agents discover it via `tools/list`) but every call returns:

```
WebSearch not configured. Set CODEGEN_SANDBOX_SEARCH_BACKEND=brave|exa|tavily
and the corresponding API key env var (BRAVE_API_KEY / EXA_API_KEY / TAVILY_API_KEY).
```

### Misconfiguration errors

| Symptom | Cause |
|---|---|
| `WebSearch misconfigured: backend "brave" selected but BRAVE_API_KEY is not set` | Backend selected, key env var missing or empty. |
| `WebSearch misconfigured: unknown WebSearch backend "…"` | Backend name is typo'd; only `brave`, `exa`, `tavily` are accepted. |
| `WebSearch brave: status 401: …` | API key is invalid or revoked. |
| `WebSearch exa: status 429: …` | Rate limit hit — check the vendor's quota dashboard. |

## Passing env vars in common deployments

**`docker run`:**

```bash
docker run -e CODEGEN_SANDBOX_SEARCH_BACKEND=brave \
           -e BRAVE_API_KEY=$BRAVE_API_KEY \
           ghcr.io/altairalabs/codegen-sandbox:latest
```

**Kubernetes (via a `Secret`):**

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: sandbox-search
stringData:
  BRAVE_API_KEY: "<your key>"
---
# ... in the container spec ...
env:
  - name: CODEGEN_SANDBOX_SEARCH_BACKEND
    value: "brave"
  - name: BRAVE_API_KEY
    valueFrom:
      secretKeyRef:
        name: sandbox-search
        key: BRAVE_API_KEY
```

**docker-compose:**

```yaml
environment:
  CODEGEN_SANDBOX_SEARCH_BACKEND: brave
  BRAVE_API_KEY: ${BRAVE_API_KEY}
```

## Why not Google or Bing?

Neither offers a usable general-web API any more:

- Google's Custom Search JSON API is restricted to Programmable Search Engines and has ranking / quota limits that make it poorly-suited to agent loops; Gemini's "Grounding with Google Search" is model-coupled (Gemini API only).
- Microsoft retired the Bing Search API in August 2025; its successor is Azure-OpenAI-only.

Brave runs its own index, Exa and Tavily run over the open web with their own crawl. All three publish stable JSON APIs meant for programmatic access. Scraping `google.com` directly isn't a viable backend — it violates Google's ToS and triggers captcha / IP-ban responses within tens of queries.

## Output format

Results come back as a compact text block:

```
3 results for "golang http context":

1. Package http - net/http - Go Packages
   https://pkg.go.dev/net/http
   Package http provides HTTP client and server implementations.

2. The Go Programming Language
   https://go.dev/
   Go is an open-source programming language supported by Google.

3. …
```

URLs live on their own line so downstream `Bash` or `WebFetch` calls can extract them with a line-oriented regex.

Empty result sets return `no results for "<query>"` (not an error — search is a legitimate miss case).

## Live-API tests

A separate test suite under `//go:build live` verifies each backend against its real API. They're not in the default CI run. To invoke locally:

```bash
BRAVE_API_KEY=... EXA_API_KEY=... TAVILY_API_KEY=... make test-live
```

Each test skips cleanly when its backend's key env var is unset — safe to run with just the one you care about.

## Adding a new backend

`internal/web/search/search.go` defines the `Backend` interface. Drop a new file beside `brave.go` / `exa.go` / `tavily.go` with a `NewX(apiKey) Backend` constructor, register it in `backendSpecs` at the top of `search.go`, and you're done.

See [Extending](/operations/extending/) for repo-structure conventions.

## Related

- [WebFetch](/tools/web-fetch/) — fetch a URL directly after a search result.
- [Extending](/operations/extending/) — add a new WebSearch backend (or a new tool).
