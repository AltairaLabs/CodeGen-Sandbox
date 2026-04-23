package web_test

import (
	"context"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/web"
	"github.com/stretchr/testify/assert"
)

func TestCheckURL_AllowsPublicHttps(t *testing.T) {
	err := web.CheckURL(context.Background(), "https://example.com/path?q=1")
	assert.NoError(t, err)
}

func TestCheckURL_RejectsNonHTTPScheme(t *testing.T) {
	cases := []string{"file:///etc/passwd", "ftp://example.com", "gopher://x.y", "javascript:alert(1)"}
	for _, u := range cases {
		err := web.CheckURL(context.Background(), u)
		assert.Error(t, err, "should reject %q", u)
	}
}

func TestCheckURL_RejectsPrivateIPv4(t *testing.T) {
	cases := []string{
		"http://10.0.0.1",
		"http://172.16.5.7",
		"http://192.168.1.1",
	}
	for _, u := range cases {
		err := web.CheckURL(context.Background(), u)
		assert.Error(t, err, "should reject %q", u)
	}
}

func TestCheckURL_RejectsLoopback(t *testing.T) {
	err := web.CheckURL(context.Background(), "http://127.0.0.1:8080/admin")
	assert.Error(t, err)
}

func TestCheckURL_RejectsLinkLocalCloudMetadata(t *testing.T) {
	err := web.CheckURL(context.Background(), "http://169.254.169.254/latest/meta-data/")
	assert.Error(t, err)
}

func TestCheckURL_RejectsIPv6Loopback(t *testing.T) {
	err := web.CheckURL(context.Background(), "http://[::1]/x")
	assert.Error(t, err)
}

func TestCheckURL_RejectsIPv4MappedIPv6Loopback(t *testing.T) {
	// ::ffff:127.0.0.1 — IPv4-mapped IPv6 address must be unwrapped before
	// the private/loopback check, otherwise the wrapper form slips through.
	err := web.CheckURL(context.Background(), "http://[::ffff:127.0.0.1]/")
	assert.Error(t, err)
}

func TestCheckURL_RejectsMetadataHostnames(t *testing.T) {
	cases := []string{
		"http://metadata.google.internal/",
		"http://metadata.aws.internal/",
		"http://metadata/",
	}
	for _, u := range cases {
		err := web.CheckURL(context.Background(), u)
		assert.Error(t, err, "should reject %q", u)
	}
}

func TestCheckURL_RejectsLocalhost(t *testing.T) {
	// localhost resolves to 127.0.0.1 — the DNS-resolution pass must catch it.
	err := web.CheckURL(context.Background(), "http://localhost/")
	assert.Error(t, err)
}

func TestCheckURL_RejectsEmptyHost(t *testing.T) {
	err := web.CheckURL(context.Background(), "http:///path")
	assert.Error(t, err)
}

func TestCheckURL_RejectsMalformed(t *testing.T) {
	err := web.CheckURL(context.Background(), "ht!tp://badly formed")
	assert.Error(t, err)
}
