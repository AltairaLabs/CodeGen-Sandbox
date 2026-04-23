// Package web owns the SSRF-defensive URL filter used by the sandbox's
// WebFetch tool. The filter rejects non-http(s) schemes, private IP ranges
// (IPv4 and IPv6), loopback, link-local (including cloud metadata at
// 169.254.169.254), and a small allow-list-free set of metadata hostnames.
// Container-level network policy remains the real egress boundary; this
// layer prevents the routine accidents (agent fetches its own metadata
// endpoint, fetches an internal service by hostname, etc.).
package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// blockedHostnames are rejected regardless of their DNS resolution.
var blockedHostnames = map[string]struct{}{
	"metadata.google.internal":   {},
	"metadata.aws.internal":      {},
	"instance-data.ec2.internal": {},
	"metadata":                   {},
}

// CheckURL returns nil if rawurl is safe to fetch; a non-nil error with a
// concise reason otherwise. The caller should surface the error text to the
// agent as a tool-level ErrorResult.
func CheckURL(ctx context.Context, rawurl string) error {
	u, err := url.Parse(rawurl)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url scheme must be http or https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("url has no host")
	}
	if _, ok := blockedHostnames[strings.ToLower(host)]; ok {
		return fmt.Errorf("host %q is blocked (cloud metadata or internal service)", host)
	}

	// If host is a literal IP, check it directly.
	if ip := net.ParseIP(host); ip != nil {
		if err := checkIP(ip); err != nil {
			return err
		}
		return nil
	}

	// Hostname — resolve and check every returned IP. If any IP is in a
	// blocked range, reject. This prevents an attacker from pointing a
	// hostname at a private IP to slip past a hostname-only check.
	resolver := net.DefaultResolver
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("resolve host %q: no addresses", host)
	}
	for _, a := range addrs {
		if err := checkIP(a.IP); err != nil {
			return fmt.Errorf("host %q resolves to blocked address: %w", host, err)
		}
	}
	return nil
}

// checkIP returns a non-nil error when ip is in a range we refuse to fetch.
func checkIP(ip net.IP) error {
	// Normalise IPv4-in-IPv6 (e.g. ::ffff:127.0.0.1) to plain IPv4 for the
	// IPv4 range checks.
	if v4 := ip.To4(); v4 != nil {
		return checkIPv4(v4)
	}
	return checkIPv6(ip)
}

func checkIPv4(ip net.IP) error {
	switch {
	case ip.IsLoopback(): // 127.0.0.0/8
		return fmt.Errorf("loopback IP %s is blocked", ip)
	case ip.IsPrivate(): // 10/8, 172.16/12, 192.168/16
		return fmt.Errorf("private IP %s is blocked", ip)
	case ip.IsLinkLocalUnicast(): // 169.254.0.0/16 (incl. 169.254.169.254 cloud metadata)
		return fmt.Errorf("link-local IP %s is blocked (covers cloud metadata)", ip)
	case ip.IsUnspecified(): // 0.0.0.0
		return fmt.Errorf("unspecified IP %s is blocked", ip)
	case ip.IsMulticast():
		return fmt.Errorf("multicast IP %s is blocked", ip)
	}
	return nil
}

func checkIPv6(ip net.IP) error {
	switch {
	case ip.IsLoopback(): // ::1
		return fmt.Errorf("loopback IP %s is blocked", ip)
	case ip.IsLinkLocalUnicast(): // fe80::/10
		return fmt.Errorf("link-local IP %s is blocked", ip)
	case ip.IsPrivate(): // fc00::/7
		return fmt.Errorf("private IP %s is blocked", ip)
	case ip.IsUnspecified(): // ::
		return fmt.Errorf("unspecified IP %s is blocked", ip)
	case ip.IsMulticast():
		return fmt.Errorf("multicast IP %s is blocked", ip)
	}
	return nil
}
