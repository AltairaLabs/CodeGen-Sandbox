package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSplitMethodName_EdgeCases covers every branch of the method-name parser
// in one table-driven test instead of splitting into six handler-level tests.
func TestSplitMethodName_EdgeCases(t *testing.T) {
	cases := []struct {
		name         string
		wantOK       bool
		wantReceiver string
		wantMethod   string
	}{
		{"Foo", false, "", ""},                        // plain function — not a method
		{"(*T).Foo", true, "*T", "Foo"},               // pointer method
		{"(T).Foo", true, "T", "Foo"},                 // value method
		{"(*pkg.T).Method", true, "*pkg.T", "Method"}, // qualified type
		{"(", false, "", ""},                          // malformed
		{"()", false, "", ""},                         // empty receiver
		{"(T).", false, "", ""},                       // empty method
		{"(T)Foo", false, "", ""},                     // missing dot
		{"T.Foo", false, "", ""},                      // missing parens
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recv, meth, ok := splitMethodName(tc.name)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantReceiver, recv)
				assert.Equal(t, tc.wantMethod, meth)
			}
		})
	}
}

// TestReindentMethod covers the indent-preservation helper without needing to
// construct a (syntactically impossible) indented top-level Go declaration.
func TestReindentMethod(t *testing.T) {
	// Zero indent → text unchanged.
	assert.Equal(t, "func X() {}", reindentMethod("func X() {}", ""))

	// Non-zero indent → every non-blank line gets the prefix.
	in := "func X() {\n\treturn\n}"
	out := reindentMethod(in, "\t")
	// Every non-blank line starts with the indent.
	for _, ln := range strings.Split(out, "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		assert.True(t, strings.HasPrefix(ln, "\t"), "line %q missing indent", ln)
	}

	// Blank lines stay blank (no trailing spaces injected).
	assert.Equal(t, "\t\tfunc X() {}\n\n\t\treturn\n", reindentMethod("func X() {}\n\nreturn\n", "\t\t"))
}

// TestLineOfOffset_Boundaries exercises the offset-to-line helper.
func TestLineOfOffset_Boundaries(t *testing.T) {
	src := []byte("alpha\nbeta\ngamma\n")
	assert.Equal(t, 1, lineOfOffset(src, 0))
	assert.Equal(t, 1, lineOfOffset(src, 3))
	assert.Equal(t, 2, lineOfOffset(src, 6))
	assert.Equal(t, 3, lineOfOffset(src, 11))
	// Offset past end is clamped.
	assert.Equal(t, 4, lineOfOffset(src, 10_000))
}

// TestLineIndentAtOffset covers the indent-detection helper used to preserve
// existing indentation on insertion.
func TestLineIndentAtOffset(t *testing.T) {
	src := []byte("line0\n\tline1\n\t\tline2\n")
	// Start of line0 — no indent.
	assert.Equal(t, "", lineIndentAtOffset(src, 0))
	// `l` of line1 — has single-tab indent.
	assert.Equal(t, "\t", lineIndentAtOffset(src, 7))
	// `l` of line2 — has double-tab indent.
	assert.Equal(t, "\t\t", lineIndentAtOffset(src, 15))
}

// TestEndsWithWhitespace covers the tiny helper used to avoid doubled spacing
// when splicing a signature back in.
func TestEndsWithWhitespace(t *testing.T) {
	assert.False(t, endsWithWhitespace(""))
	assert.False(t, endsWithWhitespace("foo"))
	assert.True(t, endsWithWhitespace("foo "))
	assert.True(t, endsWithWhitespace("foo\t"))
	assert.True(t, endsWithWhitespace("foo\n"))
}
