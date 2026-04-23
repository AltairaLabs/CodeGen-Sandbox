package search

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFromEnv_UnsetReturnsNilNil(t *testing.T) {
	t.Setenv(BackendEnvVar, "")
	b, err := NewFromEnv()
	require.NoError(t, err)
	assert.Nil(t, b)
}

func TestNewFromEnv_BraveWithKey(t *testing.T) {
	t.Setenv(BackendEnvVar, "brave")
	t.Setenv("BRAVE_API_KEY", "k")
	b, err := NewFromEnv()
	require.NoError(t, err)
	require.NotNil(t, b)
	assert.Equal(t, "brave", b.Name())
}

func TestNewFromEnv_ExaWithKey(t *testing.T) {
	t.Setenv(BackendEnvVar, "exa")
	t.Setenv("EXA_API_KEY", "k")
	b, err := NewFromEnv()
	require.NoError(t, err)
	require.NotNil(t, b)
	assert.Equal(t, "exa", b.Name())
}

func TestNewFromEnv_TavilyWithKey(t *testing.T) {
	t.Setenv(BackendEnvVar, "tavily")
	t.Setenv("TAVILY_API_KEY", "k")
	b, err := NewFromEnv()
	require.NoError(t, err)
	require.NotNil(t, b)
	assert.Equal(t, "tavily", b.Name())
}

func TestNewFromEnv_MissingKeyErrorsMentionEnvVar(t *testing.T) {
	cases := []struct {
		backend string
		envVar  string
	}{
		{"brave", "BRAVE_API_KEY"},
		{"exa", "EXA_API_KEY"},
		{"tavily", "TAVILY_API_KEY"},
	}
	for _, tc := range cases {
		t.Run(tc.backend, func(t *testing.T) {
			t.Setenv(BackendEnvVar, tc.backend)
			t.Setenv(tc.envVar, "")
			b, err := NewFromEnv()
			assert.Nil(t, b)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.envVar)
		})
	}
}

func TestNewFromEnv_UnknownBackend(t *testing.T) {
	t.Setenv(BackendEnvVar, "bogus")
	b, err := NewFromEnv()
	assert.Nil(t, b)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "unknown")
	assert.Contains(t, err.Error(), "bogus")
}
