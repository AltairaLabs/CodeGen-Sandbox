package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	gossh "golang.org/x/crypto/ssh"
)

// sshSetupConfig groups the resolved ssh-setup flags.
type sshSetupConfig struct {
	name        string
	server      string
	auth        *authFlags
	rawAuthArgs []string
}

// runSSHSetup is the entry point for `sandbox-forward ssh-setup`.
func runSSHSetup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ssh-setup", flag.ContinueOnError)
	cfg := &sshSetupConfig{auth: &authFlags{}}
	fs.StringVar(&cfg.server, "server", "", "sandbox server URL")
	cfg.auth.register(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Capture the raw auth flags so the ~/.ssh/config ProxyCommand can re-use them.
	cfg.rawAuthArgs = extractAuthArgs(args)

	if fs.NArg() != 1 {
		return fmt.Errorf("ssh-setup requires exactly one NAME argument")
	}
	cfg.name = fs.Arg(0)
	if !isSafeHostName(cfg.name) {
		return fmt.Errorf("NAME must match [A-Za-z0-9_-]+ (got %q)", cfg.name)
	}
	if cfg.server == "" {
		return fmt.Errorf("--server is required")
	}

	headers := http.Header{}
	if err := cfg.auth.Apply(headers); err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home dir: %w", err)
	}

	keysDir := filepath.Join(home, ".config", "sandbox", "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		return fmt.Errorf("mkdir keys dir: %w", err)
	}
	privPath := filepath.Join(keysDir, cfg.name)
	pubPath := privPath + ".pub"

	privPEM, pubAuthorized, err := generateEd25519KeyFiles(cfg.name)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}
	if err := os.WriteFile(pubPath, []byte(pubAuthorized+"\n"), 0o644); err != nil {
		// Roll back the private key so we don't leave half-state.
		_ = os.Remove(privPath)
		return fmt.Errorf("write public key: %w", err)
	}

	if err := postAuthorizedKey(ctx, cfg.server, headers, pubAuthorized); err != nil {
		_ = os.Remove(privPath)
		_ = os.Remove(pubPath)
		return fmt.Errorf("POST /api/ssh-authorized-keys: %w", err)
	}

	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return fmt.Errorf("mkdir .ssh: %w", err)
	}
	cfgPath := filepath.Join(sshDir, "config")
	block := buildSSHConfigBlock(cfg.name, cfg.server, privPath, cfg.rawAuthArgs)
	if err := upsertSSHConfigBlock(cfgPath, cfg.name, block); err != nil {
		return fmt.Errorf("update ssh config: %w", err)
	}

	fmt.Printf("Wrote private key:   %s\n", privPath)
	fmt.Printf("Wrote public key:    %s\n", pubPath)
	fmt.Printf("Updated ssh config:  %s (Host %s)\n", cfgPath, cfg.name)
	fmt.Printf("\nTry: ssh %s\n", cfg.name)
	return nil
}

// isSafeHostName restricts NAME to characters that are safe in a shell-quoted
// ProxyCommand line and in an ssh config Host directive.
func isSafeHostName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// generateEd25519KeyFiles returns the OpenSSH-PEM-encoded private key and the
// authorized-keys text for a fresh ed25519 keypair, using comment as the SSH
// key comment.
func generateEd25519KeyFiles(comment string) (privPEM []byte, pubAuthorized string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	block, err := gossh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, "", err
	}
	sshPub, err := gossh.NewPublicKey(pub)
	if err != nil {
		return nil, "", err
	}
	return pem.EncodeToMemory(block), strings.TrimSpace(string(gossh.MarshalAuthorizedKey(sshPub))), nil
}

// postAuthorizedKey POSTs the pubkey to /api/ssh-authorized-keys. Any non-2xx
// response is treated as failure.
func postAuthorizedKey(ctx context.Context, server string, headers http.Header, pubAuthorized string) error {
	u, err := normalizeServerURL(server)
	if err != nil {
		return err
	}
	u.Path = "/api/ssh-authorized-keys"

	body, _ := json.Marshal(map[string]string{"public_key": pubAuthorized})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// extractAuthArgs returns the subset of args that specify auth flags, in the
// order they appear. Used to pass the original --bearer / --cookie / --header
// flags through into the ~/.ssh/config ProxyCommand.
func extractAuthArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--bearer", "--bearer-file", "--cookie", "--header",
			"-bearer", "-bearer-file", "-cookie", "-header":
			out = append(out, a)
			if i+1 < len(args) {
				out = append(out, args[i+1])
				i++
			}
		default:
			// Also handle --flag=value form.
			for _, p := range []string{
				"--bearer=", "--bearer-file=", "--cookie=", "--header=",
				"-bearer=", "-bearer-file=", "-cookie=", "-header=",
			} {
				if strings.HasPrefix(a, p) {
					out = append(out, a)
					break
				}
			}
		}
	}
	return out
}

// buildSSHConfigBlock renders the Host block for ~/.ssh/config. The returned
// string always ends with a newline.
func buildSSHConfigBlock(name, server, keyPath string, authArgs []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Host %s\n", name)
	fmt.Fprintf(&b, "  ProxyCommand sandbox-forward proxy --ssh --server %s", server)
	for _, a := range authArgs {
		fmt.Fprintf(&b, " %s", shellQuote(a))
	}
	b.WriteString("\n")
	b.WriteString("  User sandbox\n")
	fmt.Fprintf(&b, "  IdentityFile %s\n", keyPath)
	b.WriteString("  StrictHostKeyChecking no\n")
	b.WriteString("  UserKnownHostsFile /dev/null\n")
	return b.String()
}

// shellQuote wraps s in single quotes if it contains shell-metacharacters,
// escaping any embedded single quote. Plain tokens are returned unchanged.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '=' || r == '@':
		default:
			safe = false
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// upsertSSHConfigBlock atomically replaces a `Host <name>` block in cfgPath,
// or appends one if no existing block matches. Writes via temp-file rename so
// readers never see a partial file. Other blocks are left untouched.
func upsertSSHConfigBlock(cfgPath, name, block string) error {
	var existing []byte
	b, err := os.ReadFile(cfgPath)
	switch {
	case err == nil:
		existing = b
	case errors.Is(err, os.ErrNotExist):
		existing = nil
	default:
		return err
	}

	updated := replaceHostBlock(existing, name, block)

	// Atomic write via temp file in the same directory.
	dir := filepath.Dir(cfgPath)
	tmp, err := os.CreateTemp(dir, ".sandbox-ssh-config-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if anything below fails.
	defer func() {
		if _, statErr := os.Stat(tmpName); statErr == nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(updated); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, cfgPath)
}

// replaceHostBlock returns existing with the block for `Host <name>` replaced
// by newBlock, or with newBlock appended if no such block exists.
//
// A "Host <name>" block begins at a line matching exactly `Host <name>`
// (optionally with extra whitespace) and ends at the next top-level `Host `
// line or EOF. This is a conservative parser: it only recognises `Host`
// lines that are the first non-whitespace token on the line.
func replaceHostBlock(existing []byte, name, newBlock string) []byte {
	if len(existing) == 0 {
		return []byte(newBlock)
	}
	lines := strings.Split(string(existing), "\n")
	start, end := findHostBlock(lines, name)
	if start < 0 {
		// Append. Ensure separation with a blank line if the file doesn't end with one.
		var out strings.Builder
		out.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			out.WriteString("\n")
		}
		if !strings.HasSuffix(string(existing), "\n\n") && len(existing) > 0 {
			out.WriteString("\n")
		}
		out.WriteString(newBlock)
		return []byte(out.String())
	}
	var out strings.Builder
	for i := 0; i < start; i++ {
		out.WriteString(lines[i])
		if i < len(lines)-1 {
			out.WriteString("\n")
		}
	}
	// newBlock already ends with \n.
	out.WriteString(newBlock)
	for i := end; i < len(lines); i++ {
		// Skip the trailing empty string that Split produces after a final "\n".
		if i == len(lines)-1 && lines[i] == "" {
			continue
		}
		out.WriteString(lines[i])
		if i < len(lines)-1 {
			out.WriteString("\n")
		}
	}
	// Ensure final newline.
	s := out.String()
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return []byte(s)
}

// findHostBlock returns the [start, end) line indices of the `Host <name>`
// block, or (-1, -1) if not found. end is the index of the first line after
// the block (a subsequent `Host ` line or len(lines)).
func findHostBlock(lines []string, name string) (int, int) {
	start := -1
	for i, ln := range lines {
		if !isHostLine(ln) {
			continue
		}
		got := hostLineName(ln)
		if start < 0 && got == name {
			start = i
			continue
		}
		if start >= 0 {
			return start, i
		}
	}
	if start >= 0 {
		return start, len(lines)
	}
	return -1, -1
}

// isHostLine reports whether trimmed(ln) begins with "Host " (case-insensitive).
func isHostLine(ln string) bool {
	t := strings.TrimLeft(ln, " \t")
	if len(t) < 5 {
		return false
	}
	return strings.EqualFold(t[:5], "Host ") || strings.EqualFold(t[:5], "Host\t")
}

// hostLineName returns the first Host pattern on a `Host ...` line.
func hostLineName(ln string) string {
	t := strings.TrimLeft(ln, " \t")
	t = t[5:] // drop "Host " / "Host\t"
	t = strings.TrimLeft(t, " \t")
	// Take first whitespace-delimited token — Host can have multiple patterns,
	// but for our idempotency check we match when the first one equals name.
	for i, r := range t {
		if r == ' ' || r == '\t' {
			return t[:i]
		}
	}
	return t
}
