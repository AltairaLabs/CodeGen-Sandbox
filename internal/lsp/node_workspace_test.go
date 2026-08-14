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
// resolvable from the seeded temp workspace, and fails loudly and specifically
// when that install cannot serve tsserver.
//
// The failure this guards against is not obvious. typescript-language-server
// 5.x resolves tsserver from `typescript/lib/tsserver.js`. TypeScript 7 is the
// native rewrite and does NOT ship that file — its lib/ contains
// getExePath.js and a native binary instead. So an unpinned
// `npm i -g typescript` silently started installing a TypeScript that the
// language server cannot use, and every run failed with:
//
//	Could not find a valid TypeScript installation. Please ensure that the
//	"typescript" dependency is installed in the workspace or that a valid
//	`tsserver.path` is specified.
//
// That message points at the workspace, which is misleading — the workspace
// was fine; the TypeScript version was wrong. CI pins typescript@^5 for this
// reason. The explicit tsserver.js check below turns a confusing runtime
// failure into a skip that names the actual cause.
func linkGlobalTypeScript(t *testing.T, root string) {
	t.Helper()

	out, err := exec.Command("npm", "root", "-g").Output()
	if err != nil {
		t.Skip("npm not on PATH; skipping real-LSP integration test")
	}

	globalTS := filepath.Join(strings.TrimSpace(string(out)), "typescript")
	if _, err := os.Stat(globalTS); err != nil {
		t.Skip("global `typescript` package not installed (npm i -g 'typescript@^5'); skipping real-LSP integration test")
	}

	// The version check that matters: tsserver.js, not the version string.
	if _, err := os.Stat(filepath.Join(globalTS, "lib", "tsserver.js")); err != nil {
		t.Skipf(
			"global typescript has no lib/tsserver.js at %s — typescript-language-server "+
				"cannot use it. TypeScript 7 dropped tsserver.js; install typescript@^5.",
			globalTS,
		)
	}

	nodeModules := filepath.Join(root, "node_modules")
	require.NoError(t, os.MkdirAll(nodeModules, 0o755))
	require.NoError(t, os.Symlink(globalTS, filepath.Join(nodeModules, "typescript")))
}
