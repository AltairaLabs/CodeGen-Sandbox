package verify_test

import (
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/verify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractGoTestsByPackage_Basic(t *testing.T) {
	// Minimal test2json stream: one pass and one fail across two
	// packages, plus some events we should ignore (package-level,
	// run/output actions, skip).
	stream := "" +
		`{"Action":"run","Package":"ex.com/a","Test":"TestAlpha"}` + "\n" +
		`{"Action":"pass","Package":"ex.com/a","Test":"TestAlpha"}` + "\n" +
		`{"Action":"run","Package":"ex.com/a","Test":"TestBeta"}` + "\n" +
		`{"Action":"fail","Package":"ex.com/a","Test":"TestBeta"}` + "\n" +
		`{"Action":"pass","Package":"ex.com/a"}` + "\n" + // package-level pass, no Test
		`{"Action":"pass","Package":"ex.com/b","Test":"TestGamma"}` + "\n" +
		`{"Action":"skip","Package":"ex.com/c","Test":"TestSkipped"}` + "\n"

	got := verify.ExtractGoTestsByPackage(stream)
	require.Len(t, got, 2, "c is skip-only → no entry; package-level pass without Test is filtered")
	assert.Equal(t, []string{"TestAlpha", "TestBeta"}, got["ex.com/a"])
	assert.Equal(t, []string{"TestGamma"}, got["ex.com/b"])
}

func TestExtractGoTestsByPackage_SubtestsCollapse(t *testing.T) {
	stream := `{"Action":"pass","Package":"p","Test":"TestTable/case_one"}` + "\n" +
		`{"Action":"pass","Package":"p","Test":"TestTable/case_two"}` + "\n" +
		`{"Action":"fail","Package":"p","Test":"TestTable/case_three"}` + "\n"
	got := verify.ExtractGoTestsByPackage(stream)
	assert.Equal(t, []string{"TestTable"}, got["p"])
}

func TestExtractGoTestsByPackage_EmptyOrMalformed(t *testing.T) {
	assert.Empty(t, verify.ExtractGoTestsByPackage(""))
	assert.Empty(t, verify.ExtractGoTestsByPackage("this is not json\n{broken"))
}

func TestExtractGoTestsByPackage_DedupesAcrossMultiplePasses(t *testing.T) {
	// -count=2 could yield two pass events for the same test in a
	// single stream; the extractor must dedupe.
	stream := `{"Action":"pass","Package":"p","Test":"TestA"}` + "\n" +
		`{"Action":"pass","Package":"p","Test":"TestA"}` + "\n"
	got := verify.ExtractGoTestsByPackage(stream)
	assert.Equal(t, []string{"TestA"}, got["p"])
}
