//go:build integration

// Integration tests that run real `cargo clippy` against a seeded crate
// and feed its output through rustDetector.ParseLint.
//
// Unit coverage in this package asserts on canned output strings. This
// tier asserts on what the live binary actually emits, so a clippy
// upgrade that changes its diagnostic format is caught at CI time.

package verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireCargo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo not on PATH; skipping real-clippy integration test")
	}
	// `cargo clippy` is a separate rustup component that has to be
	// installed explicitly. A bare cargo install won't have it; rather
	// than failing with "no such subcommand" we skip cleanly so the
	// integration tier stays safe to run on a partially-provisioned
	// machine.
	if err := exec.Command("cargo", "clippy", "--version").Run(); err != nil {
		t.Skip("cargo clippy not installed (rustup component add clippy); skipping")
	}
}

// TestRealClippy_ParsesOutput seeds a tiny crate with one guaranteed
// clippy lint (an unused variable), runs `cargo clippy
// --message-format=short`, and confirms that rustDetector.ParseLint
// surfaces at least one warning on src/lib.rs.
//
// The clippy `unused_variables` lint has been stable across releases;
// we don't pin to a specific clippy version. Runtime is dominated by
// the cold cargo build (~15-30s on a warm Rust toolchain cache).
func TestRealClippy_ParsesOutput(t *testing.T) {
	requireCargo(t)

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "Cargo.toml"),
		[]byte(`[package]
name = "probe"
version = "0.0.0"
edition = "2021"

[lib]
path = "src/lib.rs"
`), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "lib.rs"),
		[]byte(`pub fn add(a: i32, b: i32) -> i32 {
    let unused = 42;
    a + b
}
`), 0o644))

	// Mirror the detector's LintCmd invocation precisely so the parser
	// sees exactly the output it would in production.
	cmd := exec.Command("cargo", "clippy",
		"--all-targets",
		"--message-format=short",
		"--", "-D", "warnings",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		// Keep cargo from spamming user-level config / hooks. CARGO_HOME +
		// CARGO_TARGET_DIR live under the tempdir so the test is
		// hermetic across reruns.
		"CARGO_HOME="+filepath.Join(root, ".cargo"),
		"CARGO_TARGET_DIR="+filepath.Join(root, "target"),
	)
	out, runErr := cmd.CombinedOutput()
	// Clippy exits non-zero whenever findings exist (cargo uses 101 for
	// "compilation failed" and `-D warnings` turns lint findings into
	// compile errors). That's the expected path here. A non-ExitError is
	// real trouble — the subprocess didn't launch or an unrelated I/O
	// error occurred.
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			t.Fatalf("cargo clippy failed: %v\n%s", runErr, string(out))
		}
	}

	// rustDetector parses stderr, but `cargo clippy` (run via Cmd) emits
	// the diagnostic lines on stderr which CombinedOutput merges. Feed the
	// merged buffer in via the stderr argument.
	det := &rustDetector{}
	findings := det.ParseLint("", string(out))
	require.NotEmpty(t, findings, "clippy emitted no parseable findings — output was:\n%s", out)

	var seenLib bool
	for _, f := range findings {
		if filepath.Base(f.File) == "lib.rs" {
			seenLib = true
			assert.Positive(t, f.Line, "expected 1-based line number on lib.rs finding")
			// The detector captures severity into Rule (warning|error)
			// because clippy's short format doesn't include the rule
			// name. Either is acceptable here — we just need to know
			// the parser successfully extracted a structured field.
			assert.Contains(t, []string{"warning", "error"}, f.Rule, "Rule should be severity tag, got %q", f.Rule)
			break
		}
	}
	assert.True(t, seenLib, "no clippy finding on src/lib.rs in parsed findings: %+v", findings)
}
