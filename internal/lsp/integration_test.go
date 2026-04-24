//go:build integration

// Integration tests that drive REAL gopls (not the in-process mock from
// client_test.go). Run with `make test-integration` or directly via
// `go test -tags=integration ./internal/lsp/...`.
//
// The mock suite exercises wire framing + serialization. This suite
// exercises the only thing that matters in production: that our Client
// round-trips correctly against the actual gopls binary the container
// image ships. If gopls changes its response shape (see the LSP 3.16
// documentChanges migration) these tests are the fence that catches it.

package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireGopls skips the test when gopls isn't on PATH. Gopls isn't part
// of the default developer toolchain for every contributor (it's a
// separate `go install`), so skip-not-fail matches the conventions used
// by requireGo / requireGolangciLintTool in the unit suite.
func requireGopls(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH; skipping real-LSP integration test")
	}
}

// seedRenameWorkspace writes a tiny Go module with one function and a
// referencing test, returning the workspace root.
func seedRenameWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, root, "go.mod", "module example.com/probe\n\ngo 1.21\n")
	mustWrite(t, root, "probe.go", "package probe\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n")
	mustWrite(t, root, "probe_test.go", "package probe\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	return root
}

func mustWrite(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

// TestRealGopls_DefinitionReferencesRename exercises the full LSP navigation
// surface against a real gopls subprocess. The cursor sits at column 6 of
// line 3 ("func Add(..."), i.e. the 'A' in 'Add'.
func TestRealGopls_DefinitionReferencesRename(t *testing.T) {
	requireGopls(t)
	root := seedRenameWorkspace(t)

	c := NewClient(root, []string{"gopls", "serve"})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = c.Shutdown(shutdownCtx)
	}()

	t.Run("Definition", func(t *testing.T) {
		locs, err := c.Definition(ctx, "probe.go", 3, 6)
		require.NoError(t, err)
		require.NotEmpty(t, locs, "gopls returned no definition for Add")
		assert.Equal(t, "probe.go", locs[0].URI)
		assert.Equal(t, 3, locs[0].Line, "Add is declared at line 3")
	})

	t.Run("References", func(t *testing.T) {
		locs, err := c.References(ctx, "probe.go", 3, 6)
		require.NoError(t, err)
		// References includes the declaration plus at least the call site in
		// probe_test.go. Exact count depends on gopls version; assert on the
		// presence of the test-side reference, which is the agent-relevant bit.
		seenTest := false
		for _, l := range locs {
			if l.URI == "probe_test.go" {
				seenTest = true
				break
			}
		}
		assert.True(t, seenTest, "references missing probe_test.go: %+v", locs)
	})

	t.Run("Rename", func(t *testing.T) {
		edit, err := c.Rename(ctx, "probe.go", 3, 6, "Sum")
		require.NoError(t, err)
		require.NotEmpty(t, edit.Changes, "rename returned empty WorkspaceEdit")
		// Both files must be touched — the function decl in probe.go and the
		// caller in probe_test.go. This is the bug the original mock suite
		// missed: gopls returns `documentChanges`, not `changes`.
		_, haveProbe := edit.Changes["probe.go"]
		_, haveTest := edit.Changes["probe_test.go"]
		assert.True(t, haveProbe, "rename WorkspaceEdit missing probe.go: %+v", edit.Changes)
		assert.True(t, haveTest, "rename WorkspaceEdit missing probe_test.go: %+v", edit.Changes)
		// Every edit should be replacing the symbol with the new name.
		for file, edits := range edit.Changes {
			for _, e := range edits {
				assert.Equal(t, "Sum", e.NewText, "edit in %s had unexpected NewText: %+v", file, e)
			}
		}
	})
}
