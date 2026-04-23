package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const tavilyDefaultURL = "https://api.tavily.com/search"

type tavilyBackend struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewTavily returns a Tavily Search backend bound to the production endpoint.
func NewTavily(apiKey string) Backend {
	return newTavilyWithBaseURL(apiKey, tavilyDefaultURL)
}

func newTavilyWithBaseURL(apiKey, baseURL string) *tavilyBackend {
	return &tavilyBackend{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 20 * time.Second},
	}
}

func (t *tavilyBackend) Name() string { return "tavily" }

func (t *tavilyBackend) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	limit = effectiveLimit(limit)

	body, err := json.Marshal(map[string]any{
		"api_key":        t.apiKey,
		"query":          query,
		"max_results":    limit,
		"include_answer": false,
	})
	if err != nil {
		return nil, fmt.Errorf("tavily: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("tavily: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("tavily: status %d: %s", resp.StatusCode, string(errBody))
	}

	var payload struct {
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("tavily: decode: %w", err)
	}

	out := make([]Result, 0, len(payload.Results))
	for _, r := range payload.Results {
		out = append(out, Result{URL: r.URL, Title: r.Title, Snippet: r.Content})
	}
	return out, nil
}
