# Codegen Sandbox — WebFetch + URL Filter + WebSearch Implementation Plan

**Goal:** Add `WebFetch` (HTTP GET with SSRF-defensive URL filter) and a pluggable `WebSearch` (stubbed — real backend wiring is follow-up config).

**Architecture:** `internal/web/filter.go` owns the URL filter: parses the URL, rejects non-http(s) schemes, resolves the host, and rejects private/link-local/loopback IPs plus known cloud-metadata hostnames. `internal/tools/web_fetch.go` wraps an `http.Client` with a 30s timeout, runs each redirect hop through the filter (via `CheckRedirect`), caps response body at 1 MiB, and returns a formatted `Status / Content-Type / <body>` block. `internal/tools/web_search.go` ships as a pluggable stub: if `CODEGEN_SANDBOX_SEARCH_BACKEND` is unset, returns an error telling the operator how to configure it. (Real Brave/Exa/Tavily backends are a follow-up.)

**Out of scope:**
- Real WebSearch backends (Brave/Exa/Tavily) — require API keys and are operator-configured; stub shipped.
- Following `robots.txt`, respecting `Retry-After`, CORS, cookies, authentication.
- POST / JSON body requests (WebFetch is GET-only).
- Server-side response caching.

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/web/filter.go` | `CheckURL(rawurl) error` — parse + scheme check + DNS resolution + IP block check + metadata hostname check. |
| `internal/web/filter_test.go` | Per-vector tests (RFC1918, link-local, loopback, metadata hostnames, IPv6 equivalents, non-http schemes, DNS failure). |
| `internal/tools/web_fetch.go` | `RegisterWebFetch`, `HandleWebFetch`. |
| `internal/tools/web_fetch_test.go` | Uses `httptest.Server` for happy path; negative tests hit private addresses that the filter rejects without a real network call. |
| `internal/tools/web_search.go` | `RegisterWebSearch`, `HandleWebSearch` (stub returning configuration error). |
| `internal/tools/web_search_test.go` | Confirms the unconfigured error. |
| `internal/server/server.go` | Register both tools. |

---

## Task 1: URL filter

- `CheckURL(rawurl string) error` — returns nil for an allowed URL, non-nil with a concise reason otherwise.
- Rejects: non-http(s) schemes, missing host, IPs in 127/8, 10/8, 172.16/12, 192.168/16, 169.254/16, ::1, fc00::/7, fe80::/10; and hostnames `metadata.google.internal` / `metadata.aws.internal` / `instance-data.ec2.internal`.
- DNS: resolves host via `net.DefaultResolver.LookupIPAddr`; if ANY resolved IP is in a blocked range, reject (prevents DNS-rebinding bypasses at resolution time).

Tests cover: valid public URL passes; scheme `file://` rejected; explicit IP `10.1.2.3` rejected; explicit IP `127.0.0.1` rejected; explicit IPv6 `::1` rejected; hostname `metadata.google.internal` rejected; `localhost` (resolves to 127.0.0.1) rejected.

## Task 2: WebFetch tool

- Schema: `url` (required), `timeout` (optional number, default 30s, max 120s).
- Flow: CheckURL → http.NewRequest GET → client with CheckRedirect that also CheckURLs each hop → cap body at 1 MiB → return `Status: N\nContent-Type: ...\n\n<body>`.
- No POST, no custom headers beyond User-Agent.

## Task 3: WebSearch stub

- Schema: `query` (required), `limit` (optional).
- Reads env `CODEGEN_SANDBOX_SEARCH_BACKEND`. If unset: ErrorResult with "WebSearch not configured. Set CODEGEN_SANDBOX_SEARCH_BACKEND=brave|exa|tavily and the corresponding API key env var.". Real backend impls are future work.

## Task 4: Wire + smoke

- Register both tools in `server.New`.
- Live smoke: hit an external URL via tools/call (optional since it requires net access).

---

## Self-Review

Spec coverage: WebFetch with URL filter (Tasks 1-2), WebSearch pluggable backend (Task 3 as stub per the "Backend-pluggable" note in the proposal).

Known trade-offs:
- DNS-rebinding attacks between filter and fetch: mitigated by the redirect hook re-checking each hop, but a TOCTOU window between `LookupIPAddr` in the filter and the http client's own resolution still exists. Container network policy (no egress to private ranges) is the real mitigation; the filter is defense-in-depth.
- IPv4-in-IPv6 (`::ffff:127.0.0.1`) addresses — handle by checking `ip.To4()` first.
- WebSearch stub means `tools/list` shows the tool but calls fail. Acceptable for v1; setting the backend env enables it.
