---
title: URL filter
description: SSRF-defensive filtering applied to every WebFetch request and redirect hop.
---

The URL filter (`internal/web/filter.go`, exposed via `CheckURL`) runs on every [WebFetch](/tools/web-fetch/) request and on every HTTP redirect hop.

## What it blocks

- **Non-http(s) schemes.** `file://`, `ftp://`, `gopher://`, `javascript:`, etc.
- **Empty hostname.** Malformed URLs with no host.
- **Denylisted hostnames:** `metadata.google.internal`, `metadata.aws.internal`, `instance-data.ec2.internal`, bare `metadata`.
- **IPv4 ranges:** `127.0.0.0/8`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16`, `0.0.0.0`, multicast.
- **IPv6 ranges:** `::1`, `fe80::/10`, `fc00::/7`, `::`, multicast.
- **IPv4-in-IPv6:** `::ffff:127.0.0.1` is unwrapped and rejected as IPv4 loopback.

## DNS resolution

For hostnames, `net.DefaultResolver.LookupIPAddr` resolves them. If ANY returned IP falls in a blocked range, the request is rejected. This catches:

- `localhost` → resolves to `127.0.0.1` → rejected.
- A public DNS record an attacker controls that points to a private IP (DNS rebinding bait) → rejected.

## Redirects

The `http.Client` used by WebFetch installs a `CheckRedirect` hook that re-runs `CheckURL` for each hop. An allowed URL that redirects to `http://10.0.0.1/` is rejected on the second hop, not the first.

## Known gaps

**DNS-rebinding TOCTOU.** Between `CheckURL`'s `LookupIPAddr` and the http client's own resolution, a malicious DNS record could serve different IPs. The window is small (millisecond scale), but a determined attacker with control over a DNS zone could exploit it. Mitigation: **container network policy** (drop egress to private ranges at the firewall) closes this gap entirely. The in-process filter is defense-in-depth.

**IPv6 edge cases.** Some link-local IPv6 addresses include a zone identifier (`fe80::1%eth0`). Go's `net.ParseIP` handles these; the filter categorises them as link-local. Untested edge: IPv4-compatible IPv6 (`::a.b.c.d`, deprecated but parseable) — not specifically tested, but the IPv4 `To4()` unwrap handles them.

**Resolver behaviour.** `net.DefaultResolver` uses the container's `/etc/resolv.conf`. In a pure-egress-blocked container with no DNS, `CheckURL` for a hostname would fail with a DNS error, which is surfaced to the agent as `resolve host %q: ...`.

## Tests

The test suite at `internal/web/filter_test.go` covers:

- Public https URL (allowed).
- Every scheme in the denylist.
- Each IPv4 private range.
- IPv4 loopback.
- Cloud metadata IP.
- IPv6 loopback.
- IPv4-mapped IPv6.
- Metadata hostnames.
- `localhost` (DNS resolution).
- Empty host, malformed URL.
