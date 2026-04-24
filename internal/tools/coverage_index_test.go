package tools_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeProfile writes a Go cover profile body into a temp file and
// returns the path.
func writeProfile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cover.out")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func TestNewCoverageIndex_StartsEmpty(t *testing.T) {
	idx := tools.NewCoverageIndex()
	require.NotNil(t, idx)
	assert.True(t, idx.Empty())
}

func TestCoverageIndex_NilReceiverSafety(t *testing.T) {
	var idx *tools.CoverageIndex
	assert.True(t, idx.Empty())
	assert.Nil(t, idx.TestsCovering("anything.go", 0))
	// Ingest on a nil receiver is a no-op (doesn't panic).
	idx.Ingest("/nonexistent.out", map[string][]string{})
}

func TestCoverageIndex_IngestAndQueryAnyLine(t *testing.T) {
	profile := "mode: set\n" +
		"example.com/pkg/foo.go:10.2,12.5 3 1\n" +
		"example.com/pkg/bar.go:1.1,4.20 2 0\n"
	path := writeProfile(t, profile)

	idx := tools.NewCoverageIndex()
	idx.Ingest(path, map[string][]string{
		"example.com/pkg": {"TestAlpha", "TestBeta"},
	})
	assert.False(t, idx.Empty())

	// Query the package-relative key first — this is what a workspace
	// inside the module root normally surfaces.
	refs := idx.TestsCovering("pkg/foo.go", 0)
	require.Len(t, refs, 2)
	assert.Equal(t, "example.com/pkg", refs[0].Package)
	assert.Equal(t, "TestAlpha", refs[0].TestName)
	assert.Equal(t, "TestBeta", refs[1].TestName)

	// Raw profile path should also match.
	rawRefs := idx.TestsCovering("example.com/pkg/foo.go", 0)
	assert.Len(t, rawRefs, 2)
}

func TestCoverageIndex_LineScopedQuery(t *testing.T) {
	profile := "mode: set\n" +
		"m/pkg/foo.go:10.2,12.5 3 1\n"
	path := writeProfile(t, profile)

	idx := tools.NewCoverageIndex()
	idx.Ingest(path, map[string][]string{
		"m/pkg": {"TestOnly"},
	})

	// Line inside the record's [10, 12] range.
	refs := idx.TestsCovering("pkg/foo.go", 11)
	require.Len(t, refs, 1)
	assert.Equal(t, "TestOnly", refs[0].TestName)

	// Line at the boundary (start).
	assert.Len(t, idx.TestsCovering("pkg/foo.go", 10), 1)
	// Line at the boundary (end).
	assert.Len(t, idx.TestsCovering("pkg/foo.go", 12), 1)

	// Line outside the range.
	assert.Nil(t, idx.TestsCovering("pkg/foo.go", 99))
}

func TestCoverageIndex_MissingFileReturnsNil(t *testing.T) {
	idx := tools.NewCoverageIndex()
	idx.Ingest(writeProfile(t, "mode: set\nm/pkg/a.go:1.1,2.2 1 1\n"),
		map[string][]string{"m/pkg": {"T"}})
	assert.Nil(t, idx.TestsCovering("does/not/exist.go", 0))
}

func TestCoverageIndex_IngestReplacesPriorData(t *testing.T) {
	idx := tools.NewCoverageIndex()

	// First run: foo.go covered by TestA.
	first := writeProfile(t, "mode: set\nm/pkg/foo.go:1.1,5.1 1 1\n")
	idx.Ingest(first, map[string][]string{"m/pkg": {"TestA"}})
	assert.Len(t, idx.TestsCovering("pkg/foo.go", 0), 1)

	// Second run: only bar.go is covered. foo.go entries must be gone.
	second := writeProfile(t, "mode: set\nm/pkg/bar.go:1.1,5.1 1 1\n")
	idx.Ingest(second, map[string][]string{"m/pkg": {"TestB"}})
	assert.Nil(t, idx.TestsCovering("pkg/foo.go", 0),
		"second Ingest should have replaced, not merged, the first")
	assert.Len(t, idx.TestsCovering("pkg/bar.go", 0), 1)
}

func TestCoverageIndex_IngestMissingProfileIsNoOp(t *testing.T) {
	idx := tools.NewCoverageIndex()
	idx.Ingest("/nonexistent/path.out", map[string][]string{"p": {"T"}})
	assert.True(t, idx.Empty())
}

func TestCoverageIndex_IgnoresUnknownPackages(t *testing.T) {
	// Profile lists m/pkg but testsByPackage has no tests for it.
	path := writeProfile(t, "mode: set\nm/pkg/foo.go:1.1,5.1 1 1\n")

	idx := tools.NewCoverageIndex()
	idx.Ingest(path, map[string][]string{"other/pkg": {"TestUnrelated"}})
	assert.True(t, idx.Empty(), "packages with no test set must not be indexed")
}

func TestCoverageIndex_Deduplicates(t *testing.T) {
	// Two overlapping records, same package, same tests. Line 5 is in
	// both ranges; TestA should appear only once.
	profile := "mode: set\n" +
		"m/pkg/foo.go:1.1,10.5 3 1\n" +
		"m/pkg/foo.go:5.1,8.2 2 2\n"
	path := writeProfile(t, profile)

	idx := tools.NewCoverageIndex()
	idx.Ingest(path, map[string][]string{"m/pkg": {"TestA"}})

	refs := idx.TestsCovering("pkg/foo.go", 5)
	assert.Len(t, refs, 1)
}

func TestParseCoverageProfile_MalformedLinesSkipped(t *testing.T) {
	body := "mode: atomic\n" +
		"\n" +
		"   \n" + // whitespace-only
		"m/pkg/good.go:1.1,2.2 1 1\n" +
		"garbage without colon\n" +
		"m/pkg/bad.go:xx.1,2.2 1 1\n" + // bad startLine
		"m/pkg/bad.go:1.1,xx.2 1 1\n" + // bad endLine
		"m/pkg/bad.go:1.1 1 1\n" + // missing range comma
		"m/pkg/bad.go 1 1\n" + // missing colon
		"m/pkg/good2.go:5.1,7.3 2 1\n"
	records := tools.ExportParseCoverageProfile(body)
	require.Len(t, records, 2)
	assert.Equal(t, "m/pkg/good.go", records[0].File)
	assert.Equal(t, 1, records[0].StartLine)
	assert.Equal(t, 2, records[0].EndLine)
	assert.Equal(t, "m/pkg", records[0].Package)
	assert.Equal(t, "m/pkg/good2.go", records[1].File)
}

func TestParseCoverageProfile_ToleratesAllModeHeaders(t *testing.T) {
	for _, header := range []string{"mode: set", "mode: count", "mode: atomic"} {
		body := header + "\nm/pkg/a.go:1.1,2.2 1 1\n"
		records := tools.ExportParseCoverageProfile(body)
		assert.Len(t, records, 1, "mode %q", header)
	}
}

func TestParseCoverageProfile_NoDotInLineSpec(t *testing.T) {
	// A line fragment without "." — parseLinePart short-circuits on the
	// "dot <= 0" branch. Record should be dropped.
	body := "mode: set\nm/pkg/a.go:1,2.2 1 1\n"
	records := tools.ExportParseCoverageProfile(body)
	assert.Empty(t, records)
}

func TestParseCoverageProfile_RecordWithNegativeLine(t *testing.T) {
	// parseLinePart also rejects 0 and negative line numbers.
	body := "mode: set\nm/pkg/a.go:0.1,2.2 1 1\n"
	records := tools.ExportParseCoverageProfile(body)
	assert.Empty(t, records)
}

func TestCoverageIndex_IngestSkipsInvertedRanges(t *testing.T) {
	// A malformed record where EndLine < StartLine should be skipped.
	// (Compiler shouldn't emit these, but be defensive.)
	profile := "mode: set\nm/pkg/foo.go:10.1,5.2 1 1\n"
	path := writeProfile(t, profile)
	idx := tools.NewCoverageIndex()
	idx.Ingest(path, map[string][]string{"m/pkg": {"TestA"}})
	assert.True(t, idx.Empty())
}

func TestCoverageIndex_TrailingColonInLocationDropped(t *testing.T) {
	// Location ends with ":" — parseCoverageRecord rejects.
	body := "mode: set\nm/pkg/a.go: 1 1\n"
	records := tools.ExportParseCoverageProfile(body)
	assert.Empty(t, records)
}
