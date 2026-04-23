package verify_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(data)
}

func TestParseGoTest2JSON_SingleFailure(t *testing.T) {
	stdout := loadFixture(t, "go_test_json_fail.txt")
	failures := verify.ParseGoTest2JSON(stdout)

	require.Len(t, failures, 1, "one failing test expected; passing and skipped tests must not appear")
	f := failures[0]
	assert.Equal(t, "probe/TestValidate_Empty", f.TestName)
	assert.Equal(t, "probe_test.go", f.File)
	assert.Equal(t, 7, f.Line)
	assert.Contains(t, f.Message, "expected error for empty input")
	assert.Empty(t, f.Diff, "no got/want markers in fixture → diff should be empty")
}

func TestParseGoTest2JSON_CapturesDiff(t *testing.T) {
	stdout := loadFixture(t, "go_test_json_diff.txt")
	failures := verify.ParseGoTest2JSON(stdout)

	require.Len(t, failures, 1)
	f := failures[0]
	assert.Equal(t, "probe2/TestBaz", f.TestName)
	assert.Equal(t, "a_test.go", f.File)
	assert.Equal(t, 9, f.Line)
	assert.Contains(t, f.Diff, "got: 5")
	assert.Contains(t, f.Diff, "want: 3")
}

func TestParseGoTest2JSON_MultiplePackages(t *testing.T) {
	stdout := loadFixture(t, "go_test_json_multi.txt")
	failures := verify.ParseGoTest2JSON(stdout)

	require.Len(t, failures, 2, "one failure per failing test across both packages")
	names := []string{failures[0].TestName, failures[1].TestName}
	assert.Contains(t, names, "multi/a/TestAFail")
	assert.Contains(t, names, "multi/b/TestBFail")
	assert.NotContains(t, names, "multi/b/TestBPass", "passing tests must not appear as failures")
}

func TestParseGoTest2JSON_AllPassingReturnsEmpty(t *testing.T) {
	stdout := loadFixture(t, "go_test_json_pass.txt")
	assert.Empty(t, verify.ParseGoTest2JSON(stdout))
}

func TestParseGoTest2JSON_MalformedLineIsSkipped(t *testing.T) {
	valid := loadFixture(t, "go_test_json_fail.txt")
	// Insert a garbage line midway; parser must still produce the same
	// failure count.
	lines := strings.Split(valid, "\n")
	injected := append([]string{}, lines[:3]...)
	injected = append(injected, "this line is not json at all", `{"Action":"broken","Package":`)
	injected = append(injected, lines[3:]...)
	stdout := strings.Join(injected, "\n")

	failures := verify.ParseGoTest2JSON(stdout)
	require.Len(t, failures, 1)
	assert.Equal(t, "probe/TestValidate_Empty", failures[0].TestName)
}

func TestParseGoTest2JSON_EmptyInputIsEmptySlice(t *testing.T) {
	assert.Empty(t, verify.ParseGoTest2JSON(""))
}

func TestCountGoTest2JSONPasses(t *testing.T) {
	pass := loadFixture(t, "go_test_json_pass.txt")
	// Two named TestFoo/TestBar `pass` events in the fixture.
	assert.Equal(t, 2, verify.CountGoTest2JSONPasses(pass))

	// Mixed (1 fail, 1 pass, 1 skip) — only the named-pass counts.
	mixed := loadFixture(t, "go_test_json_fail.txt")
	assert.Equal(t, 1, verify.CountGoTest2JSONPasses(mixed))

	// Malformed input contributes nothing and doesn't panic.
	assert.Equal(t, 0, verify.CountGoTest2JSONPasses("not-json\n{bad}"))
}

func TestParseGoTest2JSON_ParserIsTotal_NoPanic(t *testing.T) {
	// A deliberately adversarial soup: trailing commas, truncated JSON,
	// unicode, binary noise. The parser must not panic and must not return
	// an error (parser has no error return).
	bad := "\x00\x01not json\n{truncated\n{}\n{\"Action\":null}\n" +
		"{\"Action\":\"output\",\"Test\":\"TFoo\",\"Package\":\"p\",\"Output\":\"\\x00\"}\n"
	assert.NotPanics(t, func() { _ = verify.ParseGoTest2JSON(bad) })
}

// -------- Detector interface wrappers --------

func TestGoDetector_ParseTestFailures_Delegates(t *testing.T) {
	d := detectorForMarker(t, "go.mod")
	stdout := loadFixture(t, "go_test_json_fail.txt")
	failures := d.ParseTestFailures(stdout, "")
	require.Len(t, failures, 1)
	assert.Equal(t, "probe/TestValidate_Empty", failures[0].TestName)
}

func TestGoDetector_TestCmdUsesJSON(t *testing.T) {
	d := detectorForMarker(t, "go.mod")
	assert.Equal(t, []string{"go", "test", "-json", "-count=1", "./..."}, d.TestCmd())
}

func TestNodeDetector_ParseTestFailures_ReturnsNil(t *testing.T) {
	d := detectorForMarker(t, "package.json")
	assert.Nil(t, d.ParseTestFailures("anything", "anything"))
}

func TestPythonDetector_ParseTestFailures_ReturnsNil(t *testing.T) {
	d := detectorForMarker(t, "pyproject.toml")
	assert.Nil(t, d.ParseTestFailures("anything", "anything"))
}

func TestRustDetector_ParseTestFailures_ReturnsNil(t *testing.T) {
	d := detectorForMarker(t, "Cargo.toml")
	assert.Nil(t, d.ParseTestFailures("anything", "anything"))
}
