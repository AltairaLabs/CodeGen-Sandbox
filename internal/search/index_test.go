package search_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenize_CamelCase(t *testing.T) {
	toks := search.Tokenize("fileHandler")
	assert.Contains(t, toks, "filehandler")
	assert.Contains(t, toks, "file")
	assert.Contains(t, toks, "handler")
}

func TestTokenize_SnakeCase(t *testing.T) {
	toks := search.Tokenize("http_status_code")
	assert.Contains(t, toks, "http")
	assert.Contains(t, toks, "status")
	assert.Contains(t, toks, "code")
}

func TestTokenize_Stopwords(t *testing.T) {
	toks := search.Tokenize("the status of a thing")
	assert.NotContains(t, toks, "the")
	assert.NotContains(t, toks, "of")
	assert.NotContains(t, toks, "a")
	assert.Contains(t, toks, "status")
	assert.Contains(t, toks, "thing")
}

func TestIndex_BuildAndSearch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), `package p

// FileHandler serves raw bytes; caps at 2 MiB. Returns 500 on unexpected errors.
func FileHandler() {}

// Irrelevant does nothing interesting.
func Irrelevant() {}
`)
	idx, err := search.Build(dir)
	require.NoError(t, err)
	require.NotNil(t, idx)
	require.Equal(t, 1, idx.FileCount())
	require.GreaterOrEqual(t, idx.UnitCount(), 2)

	results := idx.Search("serves bytes", 5)
	require.NotEmpty(t, results)
	assert.Equal(t, "FileHandler", results[0].Unit.Symbol)
}

func TestIndex_DocstringOutranksIdentifier(t *testing.T) {
	dir := t.TempDir()
	// a.go — "checksum" appears only in an identifier (split into tokens).
	writeFile(t, filepath.Join(dir, "a.go"), `package p

func ChecksumCompute() {}
`)
	// b.go — "checksum" appears in the docstring as a standalone word.
	writeFile(t, filepath.Join(dir, "b.go"), `package p

// Validate checksum integrity against payload.
func Validate() {}
`)
	idx, err := search.Build(dir)
	require.NoError(t, err)

	results := idx.Search("checksum integrity", 5)
	require.NotEmpty(t, results)
	assert.Equal(t, "Validate", results[0].Unit.Symbol,
		"docstring hit should outrank identifier-split-only hit")
}

func TestIndex_ExactIdentifierBeatsPartial(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), `package p

func HandleStatus() {}
func StatusLine()   {}
`)
	idx, err := search.Build(dir)
	require.NoError(t, err)

	results := idx.Search("StatusLine", 5)
	require.NotEmpty(t, results)
	assert.Equal(t, "StatusLine", results[0].Unit.Symbol)
}

func TestIndex_SkipsNodeModules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "node_modules", "vendor.go"), `package p
// SecretStuff should not be indexed.
func SecretStuff() {}
`)
	writeFile(t, filepath.Join(dir, "keep.go"), `package p
func Keep() {}
`)
	idx, err := search.Build(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, idx.FileCount())

	results := idx.Search("SecretStuff", 5)
	assert.Empty(t, results)
}

func TestIndex_EmptyQueryReturnsNothing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package p\nfunc X() {}\n")
	idx, err := search.Build(dir)
	require.NoError(t, err)
	assert.Empty(t, idx.Search("", 10))
	assert.Empty(t, idx.Search("   ", 10))
}

func TestIndex_LimitRespected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), `package p
func Alpha()   {}
func Alphabet()  {}
func Alphabetize() {}
`)
	idx, err := search.Build(dir)
	require.NoError(t, err)

	results := idx.Search("alpha", 2)
	assert.LessOrEqual(t, len(results), 2)
}

func TestIndex_NoGoFilesIsEmptyButValid(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "README.md"), "# not go")
	idx, err := search.Build(dir)
	require.NoError(t, err)
	assert.Equal(t, 0, idx.FileCount())
	assert.Empty(t, idx.Search("anything", 10))
}

func TestIndex_AddFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package p\nfunc Original() {}\n")
	idx, err := search.Build(dir)
	require.NoError(t, err)

	// Add a new file and re-index it.
	newPath := filepath.Join(dir, "b.go")
	require.NoError(t, os.WriteFile(newPath, []byte("package p\nfunc Added() {}\n"), 0o644))
	require.NoError(t, idx.AddFile(newPath))

	results := idx.Search("Added", 5)
	require.NotEmpty(t, results)
	assert.Equal(t, "Added", results[0].Unit.Symbol)
	assert.Equal(t, 2, idx.FileCount())
}

func TestIndex_RemoveFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package p\nfunc Gone() {}\n")
	writeFile(t, filepath.Join(dir, "b.go"), "package p\nfunc Kept() {}\n")
	idx, err := search.Build(dir)
	require.NoError(t, err)

	idx.RemoveFile(filepath.Join(dir, "a.go"))
	assert.Empty(t, idx.Search("Gone", 5))
	assert.NotEmpty(t, idx.Search("Kept", 5))
}
