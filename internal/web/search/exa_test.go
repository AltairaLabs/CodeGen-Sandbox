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

func TestExa_HappyPath(t *testing.T) {
	var gotKey, gotContentType string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotContentType = r.Header.Get("Content-Type")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[
			{"url":"https://a.example","title":"Alpha","text":"first"},
			{"url":"https://b.example","title":"Beta","text":"second"}
		]}`)
	}))
	defer srv.Close()

	e := newExaWithBaseURL("test-key", srv.URL)
	results, err := e.Search(context.Background(), "exa query", 7)
	require.NoError(t, err)
	assert.Equal(t, "test-key", gotKey)
	assert.Equal(t, "application/json", gotContentType)
	assert.Equal(t, "exa query", gotBody["query"])
	assert.EqualValues(t, 7, gotBody["numResults"])
	require.Len(t, results, 2)
	assert.Equal(t, "https://a.example", results[0].URL)
	assert.Equal(t, "Alpha", results[0].Title)
	assert.Equal(t, "first", results[0].Snippet)
}

func TestExa_DefaultLimit(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	defer srv.Close()

	e := newExaWithBaseURL("k", srv.URL)
	_, err := e.Search(context.Background(), "q", 0)
	require.NoError(t, err)
	assert.EqualValues(t, 10, gotBody["numResults"])
}

func TestExa_NonOKStatusWrapsCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "no")
	}))
	defer srv.Close()

	e := newExaWithBaseURL("k", srv.URL)
	_, err := e.Search(context.Background(), "q", 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.Contains(t, err.Error(), "exa")
}

func TestExa_Name(t *testing.T) {
	assert.Equal(t, "exa", NewExa("k").Name())
}
