//go:build integration

// Integration tests that drive REAL pyright-langserver. Run with
// `make test-integration` or `go test -tags=integration ./internal/lsp/...`.
//
// pyright-langserver ships as part of the npm `pyright` package
// (`npm i -g pyright`) or, increasingly, a Python wheel
// (`pip install pyright`). The tests skip cleanly when the binary is
// missing on PATH so the integration tier is safe to run on a partially-
// provisioned developer machine. CI installs pyright via the npm route
// alongside typescript-language-server before invoking this tier.

package lsp

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requirePyrightLangserver skips the test when pyright-langserver isn't on
// PATH. The official binary name is `pyright-langserver` regardless of
// which distribution channel installed it.
func requirePyrightLangserver(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pyright-langserver"); err != nil {
		t.Skip("pyright-langserver not on PATH; skipping real-LSP integration test")
	}
}

// seedPythonWorkspace writes a tiny package with one function and a
// referencing test. Layout:
//
//	pyrightconfig.json — tells pyright which files belong to the project
//	probe.py           — `def add(a: int, b: int) -> int: return a + b`
//	probe_test.py      — `from probe import add` plus an `add(1, 2)` call
//
// Cursor for the symbol `add` lives at line 1 of probe.py, column 5
// (`d`,`e`,`f`,` `,`a`); pyright is 0-based on the wire and
// positionParams converts.
func seedPythonWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, root, "pyrightconfig.json", `{
  "include": ["."],
  "useLibraryCodeForTypes": false
}
`)
	mustWrite(t, root, "probe.py", `def add(a: int, b: int) -> int:
    return a + b
`)
	mustWrite(t, root, "probe_test.py", `from probe import add


def test_add() -> None:
    assert add(1, 2) == 3
`)
	return root
}

// TestRealPyright_DefinitionReferencesRename exercises the LSP navigation
// surface against a real pyright-langserver subprocess.
func TestRealPyright_DefinitionReferencesRename(t *testing.T) {
	requirePyrightLangserver(t)
	root := seedPythonWorkspace(t)

	c := NewClient(root, []string{"pyright-langserver", "--stdio"})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = c.Shutdown(shutdownCtx)
	}()

	// Cursor: `def add(...)` — column 5 is the `a` of `add` (1-based:
	// d=1 e=2 f=3 sp=4 a=5).
	const (
		file = "probe.py"
		line = 1
		col  = 5
	)

	t.Run("Definition", func(t *testing.T) {
		locs, err := c.Definition(ctx, file, line, col)
		require.NoError(t, err)
		require.NotEmpty(t, locs, "pyright returned no definition for add")
		assert.Equal(t, file, locs[0].URI)
		assert.Equal(t, line, locs[0].Line, "add is declared at line %d", line)
	})

	t.Run("References", func(t *testing.T) {
		locs, err := c.References(ctx, file, line, col)
		require.NoError(t, err)
		// References must include the test-side use; that's the
		// agent-relevant signal that the workspace was indexed and not
		// just the open file.
		var seenTest bool
		for _, l := range locs {
			if l.URI == "probe_test.py" {
				seenTest = true
				break
			}
		}
		assert.True(t, seenTest, "references missing probe_test.py: %+v", locs)
	})

	t.Run("Rename", func(t *testing.T) {
		edit, err := c.Rename(ctx, file, line, col, "sum")
		require.NoError(t, err)
		require.NotEmpty(t, edit.Changes, "rename returned empty WorkspaceEdit")
		// Both files must be touched — the function decl in probe.py and
		// the caller in probe_test.py. Pyright is one of the servers that
		// uses `documentChanges`; this asserts the same normalisation
		// path the gopls test exercises.
		_, haveProbe := edit.Changes["probe.py"]
		_, haveTest := edit.Changes["probe_test.py"]
		assert.True(t, haveProbe, "rename WorkspaceEdit missing probe.py: %+v", edit.Changes)
		assert.True(t, haveTest, "rename WorkspaceEdit missing probe_test.py: %+v", edit.Changes)
		for file, edits := range edit.Changes {
			for _, e := range edits {
				assert.Equal(t, "sum", e.NewText, "edit in %s had unexpected NewText: %+v", file, e)
			}
		}
	})
}
