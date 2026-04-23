package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const braveDefaultURL = "https://api.search.brave.com/res/v1/web/search"

type braveBackend struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewBrave returns a Brave Search backend bound to the production endpoint.
func NewBrave(apiKey string) Backend {
	return newBraveWithBaseURL(apiKey, braveDefaultURL)
}

func newBraveWithBaseURL(apiKey, baseURL string) *braveBackend {
	return &braveBackend{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 20 * time.Second},
	}
}

func (b *braveBackend) Name() string { return "brave" }

func (b *braveBackend) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	limit = effectiveLimit(limit)

	u, err := url.Parse(b.baseURL)
	if err != nil {
		return nil, fmt.Errorf("brave: parse base url: %w", err)
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("count", strconv.Itoa(limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("brave: new request: %w", err)
	}
	req.Header.Set("X-Subscription-Token", b.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("brave: status %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Web struct {
			Results []struct {
				URL         string `json:"url"`
				Title       string `json:"title"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("brave: decode: %w", err)
	}

	out := make([]Result, 0, len(payload.Web.Results))
	for _, r := range payload.Web.Results {
		out = append(out, Result{URL: r.URL, Title: r.Title, Snippet: r.Description})
	}
	return out, nil
}
