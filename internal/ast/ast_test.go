package ast_test

import (
	"strings"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sample = `package probe

// Greet says hello.
func Greet(name string) string {
	return "hi, " + name
}

type T struct{}

// Run does the thing.
func (t *T) Run(x int) error {
	if x < 0 {
		return nil
	}
	return nil
}

func (t T) Close() error {
	return nil
}

// Greet duplicate to exercise ambiguity.
func Greet(name string) string { return name }
`

func TestDetect_Go(t *testing.T) {
	assert.Equal(t, "go", ast.Detect("foo.go"))
	assert.Equal(t, "go", ast.Detect("/path/to/Bar.GO"))
	assert.Equal(t, "", ast.Detect("foo.py"))
	assert.Equal(t, "", ast.Detect("Makefile"))
}

func TestLookup_Go(t *testing.T) {
	lang, ok := ast.Lookup("go")
	require.True(t, ok)
	require.NotNil(t, lang)
	assert.Equal(t, "go", lang.ID())
}

func TestParse_ValidReturnsTree(t *testing.T) {
	lang, _ := ast.Lookup("go")
	tree, err := lang.Parse([]byte(sample))
	require.NoError(t, err)
	require.NotNil(t, tree)
}

func TestParse_SyntaxError(t *testing.T) {
	lang, _ := ast.Lookup("go")
	_, err := lang.Parse([]byte("package probe\nfunc Broken() {"))
	require.Error(t, err)
}

func TestFindFunction_Unique(t *testing.T) {
	lang, _ := ast.Lookup("go")
	// Use a sample without the duplicate so the match is unique.
	src := strings.Replace(sample, "// Greet duplicate to exercise ambiguity.\nfunc Greet(name string) string { return name }\n", "", 1)
	tree, err := lang.Parse([]byte(src))
	require.NoError(t, err)

	matches, err := lang.FindFunction(tree, "Greet")
	require.NoError(t, err)
	require.Len(t, matches, 1)

	m := matches[0]
	// SigStart should point at `func`, NOT the doc comment.
	assert.Equal(t, byte('f'), src[m.SigStart])
	assert.Equal(t, byte('{'), src[m.BodyStart])
	// BodyEnd is one past the closing brace.
	assert.Equal(t, byte('}'), src[m.BodyEnd-1])
}

func TestFindFunction_Ambiguous(t *testing.T) {
	lang, _ := ast.Lookup("go")
	tree, err := lang.Parse([]byte(sample))
	require.NoError(t, err)

	matches, err := lang.FindFunction(tree, "Greet")
	require.NoError(t, err)
	require.Len(t, matches, 2, "sample contains two Greet functions")

	// FormatMatches should enumerate the lines.
	formatted := ast.FormatMatches(matches)
	assert.Contains(t, formatted, "line ")
	assert.Contains(t, formatted, ",", "multiple matches should be comma-separated")
}

func TestFindFunction_NotFound(t *testing.T) {
	lang, _ := ast.Lookup("go")
	tree, err := lang.Parse([]byte(sample))
	require.NoError(t, err)

	matches, err := lang.FindFunction(tree, "DoesNotExist")
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func TestFindFunction_ExcludesMethods(t *testing.T) {
	lang, _ := ast.Lookup("go")
	tree, err := lang.Parse([]byte(sample))
	require.NoError(t, err)

	matches, err := lang.FindFunction(tree, "Run")
	require.NoError(t, err)
	assert.Empty(t, matches, "methods must not match FindFunction")
}

func TestFindMethod_PointerReceiver(t *testing.T) {
	lang, _ := ast.Lookup("go")
	tree, err := lang.Parse([]byte(sample))
	require.NoError(t, err)

	matches, err := lang.FindMethod(tree, "*T", "Run")
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, "(*T).Run", matches[0].Name)
}

func TestFindMethod_ValueReceiverMatchedByPointerQuery(t *testing.T) {
	lang, _ := ast.Lookup("go")
	tree, err := lang.Parse([]byte(sample))
	require.NoError(t, err)

	// `(t T) Close` was declared with value receiver; query with *T must
	// still find it — the contract is "methods on this type" not "methods
	// on this exact receiver form".
	matches, err := lang.FindMethod(tree, "*T", "Close")
	require.NoError(t, err)
	require.Len(t, matches, 1)
}

func TestFindMethod_ReceiverMismatch(t *testing.T) {
	lang, _ := ast.Lookup("go")
	tree, err := lang.Parse([]byte(sample))
	require.NoError(t, err)

	matches, err := lang.FindMethod(tree, "*Other", "Run")
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func TestListMethods(t *testing.T) {
	lang, _ := ast.Lookup("go")
	tree, err := lang.Parse([]byte(sample))
	require.NoError(t, err)

	methods, err := lang.ListMethods(tree, "T")
	require.NoError(t, err)
	require.Len(t, methods, 2)
}

func TestValidateFuncDecl_Accepts(t *testing.T) {
	lang, _ := ast.Lookup("go")
	err := lang.ValidateFuncDecl("func F() error { return nil }")
	require.NoError(t, err)
}

func TestValidateFuncDecl_RejectsGarbage(t *testing.T) {
	lang, _ := ast.Lookup("go")
	err := lang.ValidateFuncDecl("func F(()) ?")
	require.Error(t, err)
}

func TestReplaceRange(t *testing.T) {
	out, err := ast.ReplaceRange([]byte("abcXYZdef"), 3, 6, []byte("123"))
	require.NoError(t, err)
	assert.Equal(t, "abc123def", string(out))
}

func TestReplaceRange_Invalid(t *testing.T) {
	_, err := ast.ReplaceRange([]byte("abc"), 4, 5, []byte("x"))
	require.Error(t, err)
}

func TestRenderNode(t *testing.T) {
	assert.Equal(t, []byte("bcd"), ast.RenderNode([]byte("abcde"), 1, 4))
	// Out-of-range inputs return nil rather than panicking.
	assert.Nil(t, ast.RenderNode([]byte("abc"), -1, 2))
	assert.Nil(t, ast.RenderNode([]byte("abc"), 0, 99))
	assert.Nil(t, ast.RenderNode([]byte("abc"), 2, 1))
}

func TestErrBadTree_Message(t *testing.T) {
	// Passing a non-Go tree into a Go method surfaces ErrBadTree — proves
	// the registry is per-language type-safe.
	lang, _ := ast.Lookup("go")
	type fakeTree struct{}
	_, err := lang.FindFunction(&fakeTree{}, "F")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tree not produced by this language")

	_, err = lang.FindMethod(&fakeTree{}, "T", "F")
	require.Error(t, err)

	_, err = lang.ListMethods(&fakeTree{}, "T")
	require.Error(t, err)
}
