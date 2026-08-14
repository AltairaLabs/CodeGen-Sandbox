//go:build integration

package lsp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// linkGlobalTypeScript makes the globally-installed `typescript` package
// resolvable from the seeded temp workspace.
//
// typescript-language-server resolves tsserver from the workspace's own
// node_modules, or from an explicit `tsserver.path` init option. Older
// releases ALSO fell back to a globally-installed typescript, and that
// fallback is what seedNodeWorkspace was relying on — it writes a
// tsconfig.json and two .ts files but never a node_modules.
//
// Current releases dropped that fallback, so the server starts, then fails
// initialize with:
//
//	Could not find a valid TypeScript installation. Please ensure that the
//	"typescript" dependency is installed in the workspace or that a valid
//	`tsserver.path` is specified.
//
// CI installs typescript-language-server unpinned (`npm i -g …`), so this
// broke with no change to this repo. Symlinking the global package into the
// workspace makes the fixture self-sufficient and independent of whichever
// language-server version the runner resolves.
func linkGlobalTypeScript(t *testing.T, root string) {
	t.Helper()

	out, err := exec.Command("npm", "root", "-g").Output()
	if err != nil {
		t.Skip("npm not on PATH; skipping real-LSP integration test")
	}

	globalTS := filepath.Join(strings.TrimSpace(string(out)), "typescript")
	if _, err := os.Stat(globalTS); err != nil {
		t.Skip("global `typescript` package not installed (npm i -g typescript); skipping real-LSP integration test")
	}

	nodeModules := filepath.Join(root, "node_modules")
	require.NoError(t, os.MkdirAll(nodeModules, 0o755))
	require.NoError(t, os.Symlink(globalTS, filepath.Join(nodeModules, "typescript")))
}
