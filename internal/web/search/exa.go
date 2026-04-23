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

const exaDefaultURL = "https://api.exa.ai/search"

type exaBackend struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewExa returns an Exa Search backend bound to the production endpoint.
func NewExa(apiKey string) Backend {
	return newExaWithBaseURL(apiKey, exaDefaultURL)
}

func newExaWithBaseURL(apiKey, baseURL string) *exaBackend {
	return &exaBackend{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 20 * time.Second},
	}
}

func (e *exaBackend) Name() string { return "exa" }

func (e *exaBackend) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	limit = effectiveLimit(limit)

	body, err := json.Marshal(map[string]any{
		"query":      query,
		"numResults": limit,
	})
	if err != nil {
		return nil, fmt.Errorf("exa: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("exa: new request: %w", err)
	}
	req.Header.Set("x-api-key", e.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exa: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("exa: status %d: %s", resp.StatusCode, string(errBody))
	}

	var payload struct {
		Results []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
			Text  string `json:"text"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("exa: decode: %w", err)
	}

	out := make([]Result, 0, len(payload.Results))
	for _, r := range payload.Results {
		out = append(out, Result{URL: r.URL, Title: r.Title, Snippet: r.Text})
	}
	return out, nil
}
