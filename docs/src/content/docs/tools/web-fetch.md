---
title: WebFetch
description: Filtered HTTP GET. Private ranges, loopback, link-local, and cloud metadata are blocked.
---

GET an http or https URL, with SSRF-defensive filtering at every redirect hop.

## Schema

| Param | Type | Required | Default |
|---|---|---|---|
| `url` | string | yes | — |
| `timeout` | number | no | 30 (clamped to 120) |

## Filtering

The URL goes through `web.CheckURL` before the fetch. Rejections:

**Schemes:** Anything other than `http` or `https`.

**Hostnames:**
- `metadata.google.internal`
- `metadata.aws.internal`
- `instance-data.ec2.internal`
- Bare `metadata`

**IP ranges (IPv4):**
- `127.0.0.0/8` loopback
- `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` RFC1918
- `169.254.0.0/16` link-local (includes `169.254.169.254` cloud metadata)
- `0.0.0.0`, multicast

**IP ranges (IPv6):**
- `::1` loopback
- `fe80::/10` link-local
- `fc00::/7` private
- `::` unspecified, multicast

**IPv4-in-IPv6:** `::ffff:127.0.0.1` is unwrapped and rejected as IPv4 loopback.

The hostname is resolved via DNS; if ANY returned IP is in a blocked range, the request is rejected (prevents DNS-based bypasses).

**Redirects:** Each hop is re-filtered. An allowed URL redirecting to `http://10.0.0.1/` is blocked on the second hop.

## Output format

```
Status: 200
Content-Type: text/html; charset=utf-8

<!doctype html>
...
```

When the response body exceeds 1 MiB:

```
Status: 200
Content-Type: application/json
Truncated: true (first 1048576 bytes)

{...
```

## Limits

- GET only. No POST, PUT, etc.
- No custom headers (sends `User-Agent: codegen-sandbox/0.1 (+WebFetch)`).
- Max 10 redirects.
- Response body capped at 1 MiB.
- Timeout defaults to 30s, capped at 120s.

## Threat model

The filter is defense-in-depth: container network policy (e.g. `--network=none` or an egress allowlist) is the real boundary. The filter prevents routine accidents — an agent fetching its cloud metadata endpoint, an internal hostname it happened to know, etc.

A DNS-rebinding TOCTOU window still exists between `CheckURL`'s `LookupIPAddr` and the http client's own resolution. The container network policy closes that gap in production deployments.

## Related

- [WebSearch](/tools/web-search/) — pluggable search backend.
- [URL filter concepts](/concepts/url-filter/) — deeper discussion of what's blocked and why.
