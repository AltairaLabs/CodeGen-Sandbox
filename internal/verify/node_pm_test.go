package verify_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedNodeProject writes a package.json (plus optional lock files) so
// Detect returns the Node detector. lockFiles is a list of filenames
// to touch (e.g. "pnpm-lock.yaml"); each gets an empty-content marker.
// pkgJSON controls the package.json body — pass "{}" when no scripts.
func seedNodeProject(t *testing.T, pkgJSON string, lockFiles ...string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0o644))
	for _, name := range lockFiles {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("lock\n"), 0o644))
	}
	return dir
}

func TestNodeDetector_PackageManager_PnpmLock(t *testing.T) {
	d := verify.Detect(seedNodeProject(t, "{}", "pnpm-lock.yaml"))
	require.NotNil(t, d)
	assert.Equal(t, "pnpm", d.PackageManager())
	assert.Equal(t, []string{"pnpm", "run"}, d.ScriptRunner())
}

func TestNodeDetector_PackageManager_YarnLock(t *testing.T) {
	d := verify.Detect(seedNodeProject(t, "{}", "yarn.lock"))
	require.NotNil(t, d)
	assert.Equal(t, "yarn", d.PackageManager())
	assert.Equal(t, []string{"yarn"}, d.ScriptRunner())
}

func TestNodeDetector_PackageManager_BunLock(t *testing.T) {
	d := verify.Detect(seedNodeProject(t, "{}", "bun.lockb"))
	require.NotNil(t, d)
	assert.Equal(t, "bun", d.PackageManager())
	assert.Equal(t, []string{"bun", "run"}, d.ScriptRunner())
}

func TestNodeDetector_PackageManager_NpmLock(t *testing.T) {
	d := verify.Detect(seedNodeProject(t, "{}", "package-lock.json"))
	require.NotNil(t, d)
	assert.Equal(t, "npm", d.PackageManager())
	assert.Equal(t, []string{"npm", "run"}, d.ScriptRunner())
}

func TestNodeDetector_PackageManager_NoLockFallsBackToNpm(t *testing.T) {
	d := verify.Detect(seedNodeProject(t, "{}"))
	require.NotNil(t, d)
	assert.Equal(t, "npm", d.PackageManager())
	assert.Equal(t, []string{"npm", "run"}, d.ScriptRunner())
}

// Precedence: pnpm-lock.yaml wins over package-lock.json when both are
// present (e.g. a project migrating from npm to pnpm with both files
// temporarily present during review).
func TestNodeDetector_PackageManager_PnpmWinsOverNpm(t *testing.T) {
	d := verify.Detect(seedNodeProject(t, "{}", "pnpm-lock.yaml", "package-lock.json"))
	require.NotNil(t, d)
	assert.Equal(t, "pnpm", d.PackageManager())
}

// yarn beats bun and npm when both lock files are present.
func TestNodeDetector_PackageManager_YarnWinsOverBunAndNpm(t *testing.T) {
	d := verify.Detect(seedNodeProject(t, "{}", "yarn.lock", "bun.lockb", "package-lock.json"))
	require.NotNil(t, d)
	assert.Equal(t, "yarn", d.PackageManager())
}

func TestNodeDetector_ScriptAwareCmds_UsePnpmWhenLockPresent(t *testing.T) {
	pkg := `{"scripts":{"test":"jest","lint":"eslint .","typecheck":"tsc --noEmit"}}`
	d := verify.Detect(seedNodeProject(t, pkg, "pnpm-lock.yaml"))
	require.NotNil(t, d)
	assert.Equal(t, []string{"pnpm", "run", "test"}, d.TestCmd())
	assert.Equal(t, []string{"pnpm", "run", "lint"}, d.LintCmd())
	assert.Equal(t, []string{"pnpm", "run", "typecheck"}, d.TypecheckCmd())
}

func TestNodeDetector_ScriptAwareCmds_YarnOmitsRun(t *testing.T) {
	pkg := `{"scripts":{"test":"jest"}}`
	d := verify.Detect(seedNodeProject(t, pkg, "yarn.lock"))
	require.NotNil(t, d)
	assert.Equal(t, []string{"yarn", "test"}, d.TestCmd())
}

// When scripts.test is absent, the detector keeps the historical default.
func TestNodeDetector_TestCmd_FallsBackWhenNoTestScript(t *testing.T) {
	d := verify.Detect(seedNodeProject(t, `{"scripts":{"build":"next build"}}`))
	require.NotNil(t, d)
	assert.Equal(t, []string{"npm", "test", "--silent"}, d.TestCmd())
}

func TestNodeDetector_LintCmd_FallsBackWhenNoLintScript(t *testing.T) {
	d := verify.Detect(seedNodeProject(t, `{"scripts":{"build":"next build"}}`))
	require.NotNil(t, d)
	assert.Equal(t, []string{"npx", "--no-install", "eslint", ".", "--format=compact"}, d.LintCmd())
}

func TestNodeDetector_TypecheckCmd_FallsBackWhenNoTypecheckScript(t *testing.T) {
	d := verify.Detect(seedNodeProject(t, `{"scripts":{"build":"next build"}}`))
	require.NotNil(t, d)
	assert.Equal(t, []string{"npx", "--no-install", "tsc", "--noEmit"}, d.TypecheckCmd())
}

// Malformed package.json: the detector still constructs cleanly and
// falls back to hardcoded defaults. No panic, no error propagation.
func TestNodeDetector_MalformedPackageJSON_FallsBack(t *testing.T) {
	d := verify.Detect(seedNodeProject(t, "{this is not json"))
	require.NotNil(t, d)
	assert.Equal(t, []string{"npm", "test", "--silent"}, d.TestCmd())
	assert.Equal(t, []string{"npx", "--no-install", "eslint", ".", "--format=compact"}, d.LintCmd())
	assert.Equal(t, []string{"npx", "--no-install", "tsc", "--noEmit"}, d.TypecheckCmd())
}

// scripts present but not a JSON object (e.g. scripts: null) should
// degrade cleanly to the defaults.
func TestNodeDetector_ScriptsNotObject_FallsBack(t *testing.T) {
	d := verify.Detect(seedNodeProject(t, `{"scripts":null}`))
	require.NotNil(t, d)
	assert.Equal(t, []string{"npm", "test", "--silent"}, d.TestCmd())
}

// Empty scripts object: nothing is defined, defaults apply.
func TestNodeDetector_EmptyScriptsObject_FallsBack(t *testing.T) {
	d := verify.Detect(seedNodeProject(t, `{"scripts":{}}`))
	require.NotNil(t, d)
	assert.Equal(t, []string{"npm", "test", "--silent"}, d.TestCmd())
}

// Every other detector returns "" / nil for PackageManager / ScriptRunner.
func TestOtherDetectors_NoPackageManagerOrScriptRunner(t *testing.T) {
	cases := []struct {
		name   string
		marker string
		body   string
	}{
		{"go", "go.mod", "module probe\n"},
		{"rust", "Cargo.toml", "[package]\nname='probe'\n"},
		{"python", "pyproject.toml", "[project]\nname='probe'\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, tc.marker), []byte(tc.body), 0o644))

			d := verify.Detect(dir)
			require.NotNil(t, d)
			assert.Equal(t, "", d.PackageManager())
			assert.Nil(t, d.ScriptRunner())
		})
	}
}
