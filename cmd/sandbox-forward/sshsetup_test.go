package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

// captureAuthKeysServer records the body and headers of POST
// /api/ssh-authorized-keys and returns 201.
type captureAuthKeysServer struct {
	mu           sync.Mutex
	lastBody     map[string]string
	lastHeaders  http.Header
	statusToSend int
}

func (s *captureAuthKeysServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ssh-authorized-keys", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.lastHeaders = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &s.lastBody)
		code := s.statusToSend
		if code == 0 {
			code = http.StatusCreated
		}
		w.WriteHeader(code)
	})
	return mux
}

func TestSSHSetup_HappyPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s := &captureAuthKeysServer{}
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	err := runSSHSetup(ctx, []string{
		"--server", srv.URL,
		"--bearer", "abc",
		"demo",
	})
	require.NoError(t, err)

	// Private key: exists, 0600, parses as ed25519.
	privPath := filepath.Join(home, ".config", "sandbox", "keys", "demo")
	info, err := os.Stat(privPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	privBytes, err := os.ReadFile(privPath)
	require.NoError(t, err)
	k, err := gossh.ParseRawPrivateKey(privBytes)
	require.NoError(t, err)
	assert.NotNil(t, k)

	// Public key: exists, 0644, begins with ssh-ed25519.
	pubPath := privPath + ".pub"
	pubBytes, err := os.ReadFile(pubPath)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(pubBytes), "ssh-ed25519 "))

	// POST body carried the same pubkey (trimmed).
	s.mu.Lock()
	gotBody := s.lastBody
	gotHdr := s.lastHeaders
	s.mu.Unlock()
	assert.Equal(t, strings.TrimSpace(string(pubBytes)), gotBody["public_key"])
	assert.Equal(t, "Bearer abc", gotHdr.Get("Authorization"))

	// ~/.ssh/config block present and correct.
	cfgPath := filepath.Join(home, ".ssh", "config")
	cfg, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	txt := string(cfg)
	assert.Contains(t, txt, "Host demo\n")
	assert.Contains(t, txt, "ProxyCommand sandbox-forward proxy --ssh --server "+srv.URL)
	assert.Contains(t, txt, "--bearer abc")
	assert.Contains(t, txt, "IdentityFile "+privPath)
	assert.Contains(t, txt, "User sandbox")
	assert.Contains(t, txt, "StrictHostKeyChecking no")
	assert.Contains(t, txt, "UserKnownHostsFile /dev/null")
}

func TestSSHSetup_RerunReplacesBlock_NoDuplicate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Pre-seed ~/.ssh/config with an unrelated block so we can confirm it survives.
	sshDir := filepath.Join(home, ".ssh")
	require.NoError(t, os.MkdirAll(sshDir, 0o700))
	cfgPath := filepath.Join(sshDir, "config")
	seed := "Host other\n  User alice\n\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(seed), 0o600))

	s := &captureAuthKeysServer{}
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	require.NoError(t, runSSHSetup(ctx, []string{"--server", srv.URL, "demo"}))
	require.NoError(t, runSSHSetup(ctx, []string{"--server", srv.URL, "demo"}))

	cfg, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	txt := string(cfg)

	// Exactly one "Host demo" block, unrelated block preserved.
	assert.Equal(t, 1, strings.Count(txt, "Host demo\n"))
	assert.Contains(t, txt, "Host other\n")
	assert.Contains(t, txt, "User alice")
}

func TestSSHSetup_POSTFailure_CleansKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s := &captureAuthKeysServer{statusToSend: http.StatusForbidden}
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	err := runSSHSetup(ctx, []string{"--server", srv.URL, "demo"})
	require.Error(t, err)

	// Keys must not survive a failed POST.
	privPath := filepath.Join(home, ".config", "sandbox", "keys", "demo")
	_, statErr := os.Stat(privPath)
	assert.True(t, os.IsNotExist(statErr), "private key must be removed after POST failure, got %v", statErr)
	_, statErr = os.Stat(privPath + ".pub")
	assert.True(t, os.IsNotExist(statErr), "public key must be removed after POST failure, got %v", statErr)
}

func TestSSHSetup_UnsafeName_Rejected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s := &captureAuthKeysServer{}
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	err := runSSHSetup(ctx, []string{"--server", srv.URL, "bad name"})
	require.Error(t, err)
}

func TestReplaceHostBlock_InsertsAndReplaces(t *testing.T) {
	initial := []byte("Host other\n  User alice\n\nHost demo\n  User old\n  ProxyCommand old\n\nHost last\n  User bob\n")
	newBlock := "Host demo\n  User new\n  ProxyCommand new\n"
	got := string(replaceHostBlock(initial, "demo", newBlock))
	assert.Contains(t, got, "Host other")
	assert.Contains(t, got, "User alice")
	assert.Contains(t, got, "User new")
	assert.NotContains(t, got, "User old")
	assert.Contains(t, got, "Host last")
	assert.Contains(t, got, "User bob")

	empty := replaceHostBlock(nil, "demo", newBlock)
	assert.Equal(t, newBlock, string(empty))

	appended := replaceHostBlock([]byte("Host other\n  User alice\n"), "demo", newBlock)
	appendedStr := string(appended)
	assert.Contains(t, appendedStr, "Host other")
	assert.Contains(t, appendedStr, "Host demo")
}

func TestExtractAuthArgs(t *testing.T) {
	got := extractAuthArgs([]string{
		"--server", "http://x",
		"--bearer", "b",
		"--cookie", "c=1",
		"--header=X-Env=prod",
		"--bearer-file", "/tmp/tok",
		"name",
	})
	assert.Equal(t, []string{
		"--bearer", "b",
		"--cookie", "c=1",
		"--header=X-Env=prod",
		"--bearer-file", "/tmp/tok",
	}, got)
}

func TestShellQuote(t *testing.T) {
	assert.Equal(t, "abc", shellQuote("abc"))
	assert.Equal(t, "a/b:c=d.e@f-g", shellQuote("a/b:c=d.e@f-g"))
	assert.Equal(t, "''", shellQuote(""))
	assert.Equal(t, "'a b'", shellQuote("a b"))
	assert.Equal(t, `'a'\''b'`, shellQuote("a'b"))
}
