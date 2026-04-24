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
	// \S+ is restricted to values that do NOT start with '[' so that
	// already-redacted [REDACTED:...] tokens produced by earlier passes are
	// not re-consumed and overwritten by this more general pattern.
	{"secret-env-assignment", regexp.MustCompile(`(?i)\b(?:API_KEY|TOKEN|SECRET|PASSWORD|PASSWD|PRIVATE_KEY)\s*=\s*[^\[\s]\S*`)},
}

// Scrub returns text with every match of a known-secret pattern replaced by
// `[REDACTED:<pattern-name>]`. Order of application is fixed; more specific
// patterns fire first so an Anthropic key isn't labelled as an OpenAI key.
func Scrub(text string) string {
	out, _ := WithStats(text)
	return out
}

// Stat captures one pattern's scrub contribution so callers (notably the
// metrics middleware) can attribute hit counts + redacted bytes per pattern
// without re-running the regex set themselves.
type Stat struct {
	Pattern       string
	Hits          int
	BytesRedacted int
}

// WithStats returns the scrubbed text and a per-pattern breakdown covering
// every pattern that actually matched. The Stats slice is empty when no
// patterns matched. BytesRedacted is the total length of the matched
// secret token(s) — NOT the net string-length delta — so the metric
// remains meaningful even when the `[REDACTED:...]` replacement is longer
// than the original token (as it often is).
func WithStats(text string) (string, []Stat) {
	var stats []Stat
	for _, p := range patterns {
		matches := p.re.FindAllString(text, -1)
		if len(matches) == 0 {
			continue
		}
		redacted := 0
		for _, mm := range matches {
			redacted += len(mm)
		}
		stats = append(stats, Stat{Pattern: p.name, Hits: len(matches), BytesRedacted: redacted})
		text = p.re.ReplaceAllString(text, "[REDACTED:"+p.name+"]")
	}
	return text, stats
}
