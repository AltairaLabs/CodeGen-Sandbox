//go:build integration

// Integration tests that drive REAL typescript-language-server. Run with
// `make test-integration` or `go test -tags=integration ./internal/lsp/...`.
//
// typescript-language-server ships via npm and requires a `typescript`
// peer dependency at runtime (`npm i -g typescript-language-server
// typescript`). The tests skip cleanly when the binary is missing on
// PATH so the integration tier is safe to run on a partially-provisioned
// developer machine. CI installs both packages globally before invoking
// this tier.

package lsp

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireTypeScriptLangserver skips the test when typescript-language-server
// isn't on PATH. The binary name is fixed by the npm package.
func requireTypeScriptLangserver(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH; skipping real-LSP integration test")
	}
}

// seedNodeWorkspace writes a tiny TypeScript project. Layout:
//
//	package.json   — minimal, name=probe, type=commonjs
//	tsconfig.json  — strict, includes the two .ts files
//	probe.ts       — `export function add(a: number, b: number): number { return a + b }`
//	probe_test.ts  — imports add, calls it
//
// Cursor for the symbol `add` lives at line 1 of probe.ts, column 17
// (the `a` of `add` after `export function `). e=1 x=2 p=3 o=4 r=5 t=6
// sp=7 f=8 u=9 n=10 c=11 t=12 i=13 o=14 n=15 sp=16 a=17.
func seedNodeWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, root, "package.json", `{
  "name": "probe",
  "version": "0.0.0",
  "private": true,
  "type": "commonjs"
}
`)
	mustWrite(t, root, "tsconfig.json", `{
  "compilerOptions": {
    "target": "ES2020",
    "module": "CommonJS",
    "moduleResolution": "node",
    "strict": true,
    "esModuleInterop": true
  },
  "include": ["probe.ts", "probe_test.ts"]
}
`)
	mustWrite(t, root, "probe.ts", `export function add(a: number, b: number): number { return a + b }
`)
	mustWrite(t, root, "probe_test.ts", `import { add } from "./probe";

export const result = add(1, 2);
`)
	return root
}

// TestRealTypeScriptLangserver_DefinitionReferencesRename exercises the
// LSP navigation surface against a real typescript-language-server
// subprocess.
func TestRealTypeScriptLangserver_DefinitionReferencesRename(t *testing.T) {
	requireTypeScriptLangserver(t)
	root := seedNodeWorkspace(t)

	c := NewClient(root, []string{"typescript-language-server", "--stdio"})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = c.Shutdown(shutdownCtx)
	}()

	// tsserver only resolves cross-file References + Rename for files
	// it's seen via didOpen — open probe_test.ts up front so the
	// import graph is wired before the queries fire.
	require.NoError(t, c.Start(ctx))
	c.ensureOpen("probe.ts")
	c.ensureOpen("probe_test.ts")

	const (
		file = "probe.ts"
		line = 1
		col  = 17
	)

	t.Run("Definition", func(t *testing.T) {
		locs, err := c.Definition(ctx, file, line, col)
		require.NoError(t, err)
		require.NotEmpty(t, locs, "tsserver returned no definition for add")
		assert.Equal(t, file, locs[0].URI)
		assert.Equal(t, line, locs[0].Line, "add is declared at line %d", line)
	})

	t.Run("References", func(t *testing.T) {
		locs, err := c.References(ctx, file, line, col)
		require.NoError(t, err)
		var seenTest bool
		for _, l := range locs {
			if l.URI == "probe_test.ts" {
				seenTest = true
				break
			}
		}
		assert.True(t, seenTest, "references missing probe_test.ts: %+v", locs)
	})

	t.Run("Rename", func(t *testing.T) {
		edit, err := c.Rename(ctx, file, line, col, "sum")
		require.NoError(t, err)
		require.NotEmpty(t, edit.Changes, "rename returned empty WorkspaceEdit")
		_, haveProbe := edit.Changes["probe.ts"]
		_, haveTest := edit.Changes["probe_test.ts"]
		assert.True(t, haveProbe, "rename WorkspaceEdit missing probe.ts: %+v", edit.Changes)
		assert.True(t, haveTest, "rename WorkspaceEdit missing probe_test.ts: %+v", edit.Changes)
		for file, edits := range edit.Changes {
			for _, e := range edits {
				assert.Equal(t, "sum", e.NewText, "edit in %s had unexpected NewText: %+v", file, e)
			}
		}
	})
}
