//go:build linux || darwin

package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

// generateSSHKeypair returns an ephemeral ed25519 keypair: the
// gossh.Signer for the client side and the public key in authorized-keys
// text form ("ssh-ed25519 AAAA...").
func generateSSHKeypair(t *testing.T) (gossh.Signer, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := gossh.NewSignerFromKey(priv)
	require.NoError(t, err)
	sshPub, err := gossh.NewPublicKey(pub)
	require.NoError(t, err)
	return signer, strings.TrimSpace(string(gossh.MarshalAuthorizedKey(sshPub)))
}

// newWorkspace constructs a Workspace rooted at a fresh temp dir.
func newWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()
	ws, err := workspace.New(t.TempDir())
	require.NoError(t, err)
	return ws
}

func TestAuthorizedKeysHandler_ValidKey_Stores(t *testing.T) {
	keys := newAuthorizedKeys()
	h := WithIdentity(true, authorizedKeysHandler(keys))

	_, pubText := generateSSHKeypair(t)
	body, _ := json.Marshal(map[string]string{"public_key": pubText})
	req := httptest.NewRequest(http.MethodPost, "/api/ssh-authorized-keys", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	assert.Equal(t, 1, keys.CountForSubject("dev"))
}

func TestAuthorizedKeysHandler_InvalidKey_400(t *testing.T) {
	keys := newAuthorizedKeys()
	h := WithIdentity(true, authorizedKeysHandler(keys))

	body := []byte(`{"public_key":"not-a-real-key"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/ssh-authorized-keys", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, 0, keys.CountForSubject("dev"))
}

func TestAuthorizedKeysHandler_MissingBody_400(t *testing.T) {
	keys := newAuthorizedKeys()
	h := WithIdentity(true, authorizedKeysHandler(keys))

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/ssh-authorized-keys", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestSSHPortHandler_ReturnsListenerPort(t *testing.T) {
	ws := newWorkspace(t)
	srv, err := newSSHServer(ws)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })

	h := sshPortHandler(srv)
	req := httptest.NewRequest(http.MethodGet, "/api/ssh-port", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]int
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	port := got["port"]
	assert.Greater(t, port, 0)
	assert.Equal(t, srv.Port(), port)
}

func TestSSHServer_ExecCommand_AuthorizedKey(t *testing.T) {
	ws := newWorkspace(t)
	srv, err := newSSHServer(ws)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })

	signer, pubText := generateSSHKeypair(t)
	pk, _, _, _, err := gossh.ParseAuthorizedKey([]byte(pubText))
	require.NoError(t, err)
	srv.keys.Add("tester", pk)

	cfg := &gossh.ClientConfig{
		User:            "sandbox",
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec // test-only
		Timeout:         5 * time.Second,
	}

	client, err := dialSSH(srv.Addr(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	sess, err := client.NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	var stdout bytes.Buffer
	sess.Stdout = &stdout
	require.NoError(t, sess.Run("echo hello"))
	assert.Contains(t, stdout.String(), "hello")
}

func TestSSHServer_UnregisteredKey_Rejected(t *testing.T) {
	ws := newWorkspace(t)
	srv, err := newSSHServer(ws)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })

	signer, _ := generateSSHKeypair(t)
	// intentionally do NOT register.
	cfg := &gossh.ClientConfig{
		User:            "sandbox",
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec // test-only
		Timeout:         3 * time.Second,
	}
	_, err = dialSSH(srv.Addr(), cfg)
	require.Error(t, err, "handshake must fail with unregistered key")
}

// dialSSH wraps gossh.Dial with a short overall deadline so a broken server
// does not hang the test suite.
func dialSSH(addr string, cfg *gossh.ClientConfig) (*gossh.Client, error) {
	type dialResult struct {
		c   *gossh.Client
		err error
	}
	ch := make(chan dialResult, 1)
	go func() {
		c, err := gossh.Dial("tcp", addr, cfg)
		ch <- dialResult{c, err}
	}()
	select {
	case r := <-ch:
		return r.c, r.err
	case <-time.After(5 * time.Second):
		return nil, context.DeadlineExceeded
	}
}

// TestSSHServer_EnvAndHome verifies that HOME / SANDBOX_USER are set in the
// session's /bin/bash environment, and that commands execute in ws.Root().
func TestSSHServer_EnvAndHome(t *testing.T) {
	ws := newWorkspace(t)
	srv, err := newSSHServer(ws)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })

	signer, pubText := generateSSHKeypair(t)
	pk, _, _, _, err := gossh.ParseAuthorizedKey([]byte(pubText))
	require.NoError(t, err)
	srv.keys.Add("alice", pk)

	cfg := &gossh.ClientConfig{
		User:            "sandbox",
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec // test-only
		Timeout:         5 * time.Second,
	}
	client, err := dialSSH(srv.Addr(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	sess, err := client.NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	var stdout bytes.Buffer
	sess.Stdout = &stdout
	require.NoError(t, sess.Run("echo $SANDBOX_USER:$HOME:$(pwd)"))

	got := stdout.String()
	assert.Contains(t, got, "alice:")
	assert.Contains(t, got, ws.Root())
}

// silence unused-import guard if io ever drops.
var _ = io.Discard
