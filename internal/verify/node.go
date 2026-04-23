package verify

// nodeDetector implements Detector for Node projects identified by a
// package.json at the workspace root.
//
// Commands assume the operator's image has `npm` + `npx` on PATH. If the
// project uses a different package manager (pnpm, yarn, bun), run_tests
// and friends will fail with "binary not found on PATH"; operators fork
// the image and adjust.
type nodeDetector struct{ root string }

// Language reports "node".
func (*nodeDetector) Language() string { return "node" }

// TestCmd returns "npm test --silent".
func (*nodeDetector) TestCmd() []string { return []string{"npm", "test", "--silent"} }

// LintCmd returns "npx eslint . --format=compact". The `compact` format
// emits single-line findings that future ParseLint variants can parse.
func (*nodeDetector) LintCmd() []string {
	return []string{"npx", "--no-install", "eslint", ".", "--format=compact"}
}

// TypecheckCmd returns "npx tsc --noEmit". Projects without TypeScript
// will see a missing-binary error; that's a signal they should run lint
// only, not typecheck.
func (*nodeDetector) TypecheckCmd() []string {
	return []string{"npx", "--no-install", "tsc", "--noEmit"}
}
