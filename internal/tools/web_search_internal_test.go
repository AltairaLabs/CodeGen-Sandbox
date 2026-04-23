package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/web/search"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBackend lets tests exercise handleWebSearchWith without hitting a
// real search API. ResultsFn receives the query + limit; ErrFn produces
// an error from Search. Either or neither can be set.
type fakeBackend struct {
	name      string
	resultsFn func(q string, limit int) []search.Result
	err       error
}

func (f *fakeBackend) Name() string { return f.name }
func (f *fakeBackend) Search(_ context.Context, q string, limit int) ([]search.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.resultsFn != nil {
		return f.resultsFn(q, limit), nil
	}
	return nil, nil
}

func callWith(t *testing.T, factory func() (search.Backend, error), args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := handleWebSearchWith(nil, factory)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func textBody(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}

func TestHandleWebSearchWith_HappyPath_FormatsResults(t *testing.T) {
	fake := &fakeBackend{
		name: "brave",
		resultsFn: func(q string, limit int) []search.Result {
			assert.Equal(t, "golang http", q)
			assert.Equal(t, 3, limit)
			return []search.Result{
				{URL: "https://pkg.go.dev/net/http", Title: "Package http", Snippet: "Package http provides HTTP client and server implementations."},
				{URL: "https://go.dev/", Title: "The Go Programming Language", Snippet: ""},
			}
		},
	}
	res := callWith(t, func() (search.Backend, error) { return fake, nil },
		map[string]any{"query": "golang http", "limit": float64(3)})
	require.False(t, res.IsError)

	body := textBody(t, res)
	assert.Contains(t, body, `2 results for "golang http"`)
	assert.Contains(t, body, "1. Package http")
	assert.Contains(t, body, "https://pkg.go.dev/net/http")
	assert.Contains(t, body, "Package http provides HTTP client and server implementations.")
	assert.Contains(t, body, "2. The Go Programming Language")
	assert.Contains(t, body, "https://go.dev/")
}

func TestHandleWebSearchWith_NoResults(t *testing.T) {
	fake := &fakeBackend{name: "tavily"}
	res := callWith(t, func() (search.Backend, error) { return fake, nil },
		map[string]any{"query": "some extremely niche thing"})
	require.False(t, res.IsError)
	assert.Contains(t, textBody(t, res), `no results for "some extremely niche thing"`)
}

func TestHandleWebSearchWith_BackendErrorSurfaces(t *testing.T) {
	fake := &fakeBackend{name: "exa", err: errors.New("status 429: rate limited")}
	res := callWith(t, func() (search.Backend, error) { return fake, nil },
		map[string]any{"query": "anything"})
	require.True(t, res.IsError)
	body := textBody(t, res)
	assert.Contains(t, body, "WebSearch exa")
	assert.Contains(t, body, "rate limited")
}

func TestHandleWebSearchWith_ZeroLimitUsesDefault(t *testing.T) {
	var observedLimit int
	fake := &fakeBackend{
		name: "brave",
		resultsFn: func(_ string, limit int) []search.Result {
			observedLimit = limit
			return nil
		},
	}
	// limit=0 should fall through to the backend-default (10 in the handler).
	_ = callWith(t, func() (search.Backend, error) { return fake, nil },
		map[string]any{"query": "q", "limit": float64(0)})
	assert.Equal(t, 10, observedLimit)
}

func TestHandleWebSearchWith_FactoryError(t *testing.T) {
	res := callWith(t,
		func() (search.Backend, error) { return nil, errors.New("BRAVE_API_KEY not set") },
		map[string]any{"query": "q"})
	require.True(t, res.IsError)
	assert.Contains(t, textBody(t, res), "misconfigured")
}

func TestFormatSearchResults_TitleAndURLTrimmed(t *testing.T) {
	got := formatSearchResults("q", []search.Result{
		{URL: "  https://example.com/  ", Title: "  Example  ", Snippet: "  hello  "},
	})
	assert.Contains(t, got, "1. Example\n")
	assert.Contains(t, got, "   https://example.com/\n")
	assert.Contains(t, got, "   hello\n")
	// Confirm the URL's trailing whitespace is trimmed — any `/  ` would
	// indicate TrimSpace didn't run.
	assert.NotContains(t, got, "example.com/  ")
}

func TestFlattenSnippet(t *testing.T) {
	cases := map[string]string{
		"":                      "",
		"   ":                   "",
		"hello":                 "hello",
		"hello\nworld":          "hello world",
		"one\n\ntwo":            "one two",
		"a\tb\tc":               "a b c",
		"multi   spaces   here": "multi spaces here",
		"  pad  \n  lines  ":    "pad lines",
		"crlf\r\nline\rending":  "crlf line ending",
		"tabs\tand\r\nnewlines": "tabs and newlines",
	}
	for in, want := range cases {
		t.Run(strings.ReplaceAll(in, "\n", "\\n"), func(t *testing.T) {
			assert.Equal(t, want, flattenSnippet(in))
		})
	}
}
