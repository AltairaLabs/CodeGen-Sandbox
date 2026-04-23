package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTavily_HappyPath(t *testing.T) {
	var gotContentType string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[
			{"url":"https://a.example","title":"Alpha","content":"first"},
			{"url":"https://b.example","title":"Beta","content":"second"}
		]}`)
	}))
	defer srv.Close()

	tb := newTavilyWithBaseURL("test-key", srv.URL)
	results, err := tb.Search(context.Background(), "tavily query", 4)
	require.NoError(t, err)
	assert.Equal(t, "application/json", gotContentType)
	assert.Equal(t, "test-key", gotBody["api_key"])
	assert.Equal(t, "tavily query", gotBody["query"])
	assert.EqualValues(t, 4, gotBody["max_results"])
	assert.Equal(t, false, gotBody["include_answer"])
	require.Len(t, results, 2)
	assert.Equal(t, "https://a.example", results[0].URL)
	assert.Equal(t, "Alpha", results[0].Title)
	assert.Equal(t, "first", results[0].Snippet)
}

func TestTavily_DefaultLimit(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	defer srv.Close()

	tb := newTavilyWithBaseURL("k", srv.URL)
	_, err := tb.Search(context.Background(), "q", 0)
	require.NoError(t, err)
	assert.EqualValues(t, 10, gotBody["max_results"])
}

func TestTavily_NonOKStatusWrapsCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "nope")
	}))
	defer srv.Close()

	tb := newTavilyWithBaseURL("k", srv.URL)
	_, err := tb.Search(context.Background(), "q", 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
	assert.Contains(t, err.Error(), "tavily")
}

func TestTavily_Name(t *testing.T) {
	assert.Equal(t, "tavily", NewTavily("k").Name())
}
