package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// authFlags holds parsed --bearer / --bearer-file / --cookie / --header values.
// All four compose: the Apply method writes bearer + every cookie + every
// custom header onto an http.Header.
type authFlags struct {
	bearer     string
	bearerFile string
	cookies    []string // each "NAME=VALUE"
	headers    []string // each "KEY=VALUE"
}

// register wires the shared auth flags onto fs. Call once per flag.FlagSet.
func (a *authFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&a.bearer, "bearer", "", "Bearer token (sets Authorization: Bearer <value>)")
	fs.StringVar(&a.bearerFile, "bearer-file", "", "Path to file containing bearer token (trailing newline trimmed)")
	fs.Func("cookie", "Cookie NAME=VALUE (repeatable)", func(v string) error {
		if !strings.Contains(v, "=") {
			return fmt.Errorf("--cookie must be NAME=VALUE (got %q)", v)
		}
		a.cookies = append(a.cookies, v)
		return nil
	})
	fs.Func("header", "HTTP header KEY=VALUE (repeatable)", func(v string) error {
		if !strings.Contains(v, "=") {
			return fmt.Errorf("--header must be KEY=VALUE (got %q)", v)
		}
		a.headers = append(a.headers, v)
		return nil
	})
}

// resolveBearer returns the bearer value, reading from bearerFile if set.
// A trailing newline on the file content is trimmed so `--bearer-file tok`
// works for files written by `echo $TOKEN > tok`.
func (a *authFlags) resolveBearer() (string, error) {
	if a.bearer != "" && a.bearerFile != "" {
		return "", fmt.Errorf("--bearer and --bearer-file are mutually exclusive")
	}
	if a.bearerFile != "" {
		b, err := os.ReadFile(a.bearerFile)
		if err != nil {
			return "", fmt.Errorf("read bearer file: %w", err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return a.bearer, nil
}

// Apply writes all configured headers onto h. Returns an error if the bearer
// file cannot be read or a cookie/header is malformed (the latter cannot
// happen in practice because register validates on parse, but Apply re-checks
// so tests can construct authFlags directly).
func (a *authFlags) Apply(h http.Header) error {
	bearer, err := a.resolveBearer()
	if err != nil {
		return err
	}
	if bearer != "" {
		h.Set("Authorization", "Bearer "+bearer)
	}
	for _, c := range a.cookies {
		if !strings.Contains(c, "=") {
			return fmt.Errorf("malformed cookie %q", c)
		}
		// Append (don't Set) so multiple --cookie values accumulate.
		h.Add("Cookie", c)
	}
	for _, kv := range a.headers {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("malformed header %q", kv)
		}
		h.Add(k, v)
	}
	return nil
}
