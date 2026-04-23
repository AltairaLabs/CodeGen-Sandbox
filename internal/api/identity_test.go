package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithIdentity_HeadersPresent(t *testing.T) {
	var gotID Identity
	var gotOK bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotID, gotOK = FromContext(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	req.Header.Set("X-Forwarded-Sub", "sub-123")
	req.Header.Set("X-Forwarded-User", "alice@example.com")
	req.Header.Set("X-Forwarded-Groups", "eng,  platform ,sre")

	rr := httptest.NewRecorder()
	WithIdentity(false, next).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.True(t, gotOK, "FromContext must return ok=true")
	assert.Equal(t, "sub-123", gotID.Sub)
	assert.Equal(t, "alice@example.com", gotID.User)
	assert.Equal(t, []string{"eng", "platform", "sre"}, gotID.Groups)
}

func TestWithIdentity_HeadersAbsent_NonDevMode_Returns401(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	})

	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	rr := httptest.NewRecorder()
	WithIdentity(false, next).ServeHTTP(rr, req)

	assert.False(t, called, "next must not be called when identity is missing in non-dev mode")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	body, _ := io.ReadAll(rr.Body)
	assert.Contains(t, string(body), "missing identity headers")
}

func TestWithIdentity_HeadersAbsent_DevMode_InjectsDevIdentity(t *testing.T) {
	var gotID Identity
	var gotOK bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotID, gotOK = FromContext(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	rr := httptest.NewRecorder()
	WithIdentity(true, next).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.True(t, gotOK)
	assert.Equal(t, "dev", gotID.Sub)
	assert.Equal(t, "dev", gotID.User)
	assert.Nil(t, gotID.Groups)
}

func TestWithIdentity_OnlySubPresent_Proceeds(t *testing.T) {
	var gotID Identity
	var gotOK bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotID, gotOK = FromContext(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	req.Header.Set("X-Forwarded-Sub", "sub-only")

	rr := httptest.NewRecorder()
	WithIdentity(false, next).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.True(t, gotOK)
	assert.Equal(t, "sub-only", gotID.Sub)
	assert.Equal(t, "", gotID.User)
	assert.Empty(t, gotID.Groups)
}

func TestFromContext_Empty(t *testing.T) {
	_, ok := FromContext(httptest.NewRequest(http.MethodGet, "/", nil).Context())
	assert.False(t, ok)
}
