// Package secrets provides a read-only store for operator-configured
// credentials exposed to the agent via the `secret` MCP tool. The store's job
// is to resolve a name → value lookup from two sources — file-mounted
// (e.g. k8s Secret volume) and operator-set env vars — without ever placing
// the values in the agent's subprocess environment.
package secrets

import (
	"os"
	"strings"
)

// Source identifies where a resolved secret came from. Returned alongside the
// value for audit logging.
type Source string

const (
	// SourceEnv is the CODEGEN_SANDBOX_SECRET_<NAME> environment variable form.
	SourceEnv Source = "env"
	// SourceFile is a file under the -secrets-dir directory.
	SourceFile Source = "file"

	// envPrefix is the prefix operators set to expose a secret via env.
	envPrefix = "CODEGEN_SANDBOX_SECRET_"
)

// Store resolves secret names to their values. Construct with New.
type Store struct {
	// envMap maps lowercased-name → value for env-sourced secrets.
	envMap map[string]string
	// dir is the (optional) file-source directory. Empty string disables it.
	dir string
}

// New builds a Store from a file-source directory (empty string = disabled)
// and an env slice in the form returned by os.Environ. The returned Store is
// safe for concurrent read access; no locking is needed because it is not
// mutated after construction.
func New(dir string, env []string) *Store {
	return &Store{
		envMap: parseEnv(env),
		dir:    dir,
	}
}

// Get returns the value for name (case-insensitive). The second return is
// false if no such secret is configured, in which case value is "" and source
// is "". Env source wins over file source when the same name is configured in
// both — this lets operators override a mounted secret for debugging without
// remounting the volume.
func (s *Store) Get(name string) (value string, ok bool, src Source) {
	if s == nil {
		return "", false, ""
	}
	key := strings.ToLower(name)
	if v, found := s.envMap[key]; found {
		return v, true, SourceEnv
	}
	if s.dir == "" {
		return "", false, ""
	}
	if !isSafeName(key) {
		return "", false, ""
	}
	// ReadFile is safe here because isSafeName rejects any basename with a
	// path separator, dot-prefix, or empty value — so /dir/key cannot escape
	// the secrets dir.
	b, err := os.ReadFile(s.dir + string(os.PathSeparator) + key) //nolint:gosec // basename validated by isSafeName
	if err != nil {
		return "", false, ""
	}
	return strings.TrimRight(string(b), "\n"), true, SourceFile
}

// Names returns the union of configured secret names across both sources,
// lowercased. Order is unspecified; callers that want a deterministic list
// should sort.
func (s *Store) Names() []string {
	if s == nil {
		return nil
	}
	set := make(map[string]struct{}, len(s.envMap))
	for k := range s.envMap {
		set[k] = struct{}{}
	}
	if s.dir != "" {
		entries, err := os.ReadDir(s.dir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := e.Name()
				if !isSafeName(name) {
					continue
				}
				set[name] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// parseEnv extracts CODEGEN_SANDBOX_SECRET_<NAME>=<value> entries from env
// and returns {lowercased-name: value}. Unrelated variables are ignored.
func parseEnv(env []string) map[string]string {
	out := make(map[string]string)
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		key, value := kv[:eq], kv[eq+1:]
		if !strings.HasPrefix(key, envPrefix) {
			continue
		}
		name := strings.ToLower(key[len(envPrefix):])
		if name == "" {
			continue
		}
		out[name] = value
	}
	return out
}

// isSafeName reports whether a basename is safe to use as a file-source key.
// Rejects empty strings, dotfiles, anything containing a path separator, and
// anything that would escape the dir. This is belt-and-braces — the caller
// already lowercased and scoped input — but cheap insurance against a future
// refactor that forgets.
func isSafeName(name string) bool {
	if name == "" || name[0] == '.' {
		return false
	}
	if strings.ContainsAny(name, "/\\") {
		return false
	}
	if name == "." || name == ".." {
		return false
	}
	return true
}
