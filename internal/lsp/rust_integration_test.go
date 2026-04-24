//go:build integration

// Integration tests that drive REAL rust-analyzer (not a mock). Run with
// `make test-integration` or `go test -tags=integration ./internal/lsp/...`.
//
// rust-analyzer is a native binary distributed via rustup. The tests skip
// cleanly when the binary is missing on PATH so the integration tier is
// safe to run on a partially-provisioned developer machine. CI installs
// rust-analyzer alongside gopls via rustup before invoking this tier.

package lsp

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireRustAnalyzer skips the test when rust-analyzer isn't on PATH.
func requireRustAnalyzer(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rust-analyzer"); err != nil {
		t.Skip("rust-analyzer not on PATH; skipping real-LSP integration test")
	}
}

// seedRustWorkspace writes a tiny crate with one function and a referencing
// test. Layout:
//
//	Cargo.toml      — name=probe, edition=2021
//	src/lib.rs      — `pub fn add(a: i32, b: i32) -> i32 { a + b }` + a test
//
// The cursor for the symbol `add` lives at line 1 of src/lib.rs, column 8
// (`p`,`u`,`b`,` `,`f`,`n`,` `,`a`); rust-analyzer is 0-based on the wire
// but our positionParams converts 1-based to 0-based on the way out.
func seedRustWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, root, "Cargo.toml", `[package]
name = "probe"
version = "0.0.0"
edition = "2021"

[lib]
path = "src/lib.rs"
`)
	mustWrite(t, root, "src/lib.rs", `pub fn add(a: i32, b: i32) -> i32 { a + b }

#[cfg(test)]
mod tests {
    use super::add;

    #[test]
    fn it_adds() {
        assert_eq!(add(1, 2), 3);
    }
}
`)
	return root
}

// TestRealRustAnalyzer_DefinitionReferencesRename exercises the LSP
// navigation surface against a real rust-analyzer subprocess.
//
// Timeouts are larger than the gopls equivalent because rust-analyzer
// runs `cargo metadata` on cold start and that bootstrap can take several
// seconds even for a one-file crate.
func TestRealRustAnalyzer_DefinitionReferencesRename(t *testing.T) {
	requireRustAnalyzer(t)
	root := seedRustWorkspace(t)

	c := NewClient(root, []string{"rust-analyzer"})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = c.Shutdown(shutdownCtx)
	}()

	// Cursor: `pub fn add(...)` — column 8 is the `a` of `add` (1-based:
	// p=1 u=2 b=3 sp=4 f=5 n=6 sp=7 a=8). rust-analyzer's symbol resolver
	// works on the identifier under the cursor so any column inside `add`
	// is acceptable, but pinning to 8 keeps the test reproducible.
	const (
		file = "src/lib.rs"
		line = 1
		col  = 8
	)

	t.Run("Definition", func(t *testing.T) {
		// Allow rust-analyzer's project bootstrap to complete on the first
		// real call; nothing else is in flight.
		locs, err := c.Definition(ctx, file, line, col)
		require.NoError(t, err)
		require.NotEmpty(t, locs, "rust-analyzer returned no definition for add")
		assert.Equal(t, file, locs[0].URI)
		assert.Equal(t, line, locs[0].Line, "add is declared at line %d", line)
	})

	t.Run("References", func(t *testing.T) {
		locs, err := c.References(ctx, file, line, col)
		require.NoError(t, err)
		// References includes the declaration plus the use inside the
		// `tests` module. We assert on a use-site reference at a line
		// beyond the declaration so a regression that returns ONLY the
		// declaration fails.
		var seenUse bool
		for _, l := range locs {
			if l.URI == file && l.Line > line {
				seenUse = true
				break
			}
		}
		assert.True(t, seenUse, "references missing use-site for add: %+v", locs)
	})

	t.Run("Rename", func(t *testing.T) {
		edit, err := c.Rename(ctx, file, line, col, "sum")
		require.NoError(t, err)
		require.NotEmpty(t, edit.Changes, "rename returned empty WorkspaceEdit")
		// Both the declaration and the in-test use are in src/lib.rs so
		// only one file is touched. Multiple TextEdits within that file is
		// the agent-relevant invariant.
		edits, ok := edit.Changes[file]
		require.True(t, ok, "rename WorkspaceEdit missing %s: %+v", file, edit.Changes)
		assert.GreaterOrEqual(t, len(edits), 2, "rename should touch decl + at least one use: %+v", edits)
		for _, e := range edits {
			assert.Equal(t, "sum", e.NewText, "edit had unexpected NewText: %+v", e)
		}
	})
}
