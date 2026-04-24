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
	assert.NotContains(t, out, "[REDACTED:openai-api-key]")
}

func TestScrub_OpenAIAPIKey(t *testing.T) {
	in := "OPENAI_API_KEY=sk-" + strings.Repeat("a", 48)
	out := scrub.Scrub(in)
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
	assert.Contains(t, out, "normal=value")
}

func TestWithStats_ReportsHitsAndBytes(t *testing.T) {
	in := "aws=AKIAIOSFODNN7EXAMPLE, also AKIAZZZZZZZZZZZZZZZZ"
	out, stats := scrub.WithStats(in)
	assert.Contains(t, out, "[REDACTED:aws-access-key]")
	assert.Len(t, stats, 1)
	assert.Equal(t, "aws-access-key", stats[0].Pattern)
	assert.Equal(t, 2, stats[0].Hits)
	// Each AWS key is 20 chars; 2 keys → 40 bytes redacted.
	assert.Equal(t, 40, stats[0].BytesRedacted)
}

func TestWithStats_NoMatchReturnsEmptyStats(t *testing.T) {
	_, stats := scrub.WithStats("clean text with no secrets")
	assert.Empty(t, stats)
}

func TestScrub_MultiplePatternsOneInput(t *testing.T) {
	in := "aws=AKIAIOSFODNN7EXAMPLE, gh=ghp_" + strings.Repeat("z", 36) + ", goog=AIza" + strings.Repeat("G", 35)
	out := scrub.Scrub(in)
	assert.Contains(t, out, "[REDACTED:aws-access-key]")
	assert.Contains(t, out, "[REDACTED:github-pat]")
	assert.Contains(t, out, "[REDACTED:google-api-key]")
	assert.NotContains(t, out, "AKIAIOSFODNN7EXAMPLE")
	assert.NotContains(t, out, "ghp_zzz")
	assert.NotContains(t, out, "AIzaGG")
}
