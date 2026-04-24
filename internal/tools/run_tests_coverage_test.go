package tools_test

import (
	"os"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findDetector returns the first detector whose language matches.
func findDetector(t *testing.T, language string) verify.Detector {
	t.Helper()
	for _, d := range verify.AllDetectors() {
		if d.Language() == language {
			return d
		}
	}
	t.Fatalf("no detector for language %q", language)
	return nil
}

func TestAugmentTestCmd_NonGoPassthrough(t *testing.T) {
	det := findDetector(t, "node")
	deps := &tools.Deps{CoverageIndex: tools.NewCoverageIndex()}
	got, path := tools.ExportAugmentTestCmdForCoverage(det, deps)
	assert.Equal(t, det.TestCmd(), got, "non-Go detectors must pass through unchanged")
	assert.Empty(t, path)
}

func TestAugmentTestCmd_GoWithoutIndex(t *testing.T) {
	det := findDetector(t, "go")
	deps := &tools.Deps{} // no CoverageIndex
	got, path := tools.ExportAugmentTestCmdForCoverage(det, deps)
	assert.Equal(t, det.TestCmd(), got, "nil coverage index → unchanged argv")
	assert.Empty(t, path)
}

func TestAugmentTestCmd_GoWithIndexAddsFlag(t *testing.T) {
	det := findDetector(t, "go")
	deps := &tools.Deps{CoverageIndex: tools.NewCoverageIndex()}
	got, path := tools.ExportAugmentTestCmdForCoverage(det, deps)
	t.Cleanup(func() { _ = os.Remove(path) })

	require.NotEmpty(t, path)
	// Argv shape: "go", "test", "-coverprofile=<path>", ...rest of base.
	require.GreaterOrEqual(t, len(got), 3)
	assert.Equal(t, "go", got[0])
	assert.Equal(t, "test", got[1])
	assert.Contains(t, got[2], "-coverprofile=")
	assert.Contains(t, got[2], path)
	// The detector's original flags still follow.
	assert.Contains(t, got, "-json")
	assert.Contains(t, got, "-count=1")
	assert.Contains(t, got, "./...")
}

func TestCleanupCoverageProfile_EmptyPath(_ *testing.T) {
	// Must not panic, must not call os.Remove on empty.
	tools.ExportCleanupCoverageProfile("")
}

func TestCleanupCoverageProfile_RemovesFile(t *testing.T) {
	f, err := os.CreateTemp("", "cov-cleanup-*")
	require.NoError(t, err)
	_ = f.Close()
	path := f.Name()

	tools.ExportCleanupCoverageProfile(path)
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "cleanup should have removed %s", path)
}
