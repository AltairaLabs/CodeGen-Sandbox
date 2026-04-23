package search

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrave_HappyPath(t *testing.T) {
	var gotAuth, gotAccept, gotQuery, gotCount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("X-Subscription-Token")
		gotAccept = r.Header.Get("Accept")
		gotQuery = r.URL.Query().Get("q")
		gotCount = r.URL.Query().Get("count")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"web":{"results":[
			{"url":"https://a.example","title":"Alpha","description":"first"},
			{"url":"https://b.example","title":"Beta","description":"second"}
		]}}`)
	}))
	defer srv.Close()

	b := newBraveWithBaseURL("test-key", srv.URL)
	results, err := b.Search(context.Background(), "golang http", 5)
	require.NoError(t, err)
	assert.Equal(t, "test-key", gotAuth)
	assert.Equal(t, "application/json", gotAccept)
	assert.Equal(t, "golang http", gotQuery)
	assert.Equal(t, "5", gotCount)
	require.Len(t, results, 2)
	assert.Equal(t, "https://a.example", results[0].URL)
	assert.Equal(t, "Alpha", results[0].Title)
	assert.Equal(t, "first", results[0].Snippet)
	assert.Equal(t, "Beta", results[1].Title)
}

func TestBrave_DefaultLimitWhenZero(t *testing.T) {
	var gotCount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCount = r.URL.Query().Get("count")
		_, _ = io.WriteString(w, `{"web":{"results":[]}}`)
	}))
	defer srv.Close()

	b := newBraveWithBaseURL("k", srv.URL)
	_, err := b.Search(context.Background(), "q", 0)
	require.NoError(t, err)
	assert.Equal(t, "10", gotCount)
}

func TestBrave_NonOKStatusWrapsCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "rate limited")
	}))
	defer srv.Close()

	b := newBraveWithBaseURL("k", srv.URL)
	_, err := b.Search(context.Background(), "q", 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "418")
	assert.True(t, strings.Contains(err.Error(), "brave"))
}

func TestBrave_Name(t *testing.T) {
	assert.Equal(t, "brave", NewBrave("k").Name())
}
