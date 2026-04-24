package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTruncateFormatOutput_Unchanged(t *testing.T) {
	in := "line1\nline2\nline3"
	assert.Equal(t, in, truncateFormatOutput(in, 5))
}

func TestTruncateFormatOutput_TruncatesWithFooter(t *testing.T) {
	// 10 input lines, cap at 3 → keep 3 + footer. The retained lines carry a
	// distinctive prefix so the substring count isn't polluted by the word
	// "lines" that appears in the footer.
	in := "alpha\nbravo\ncharlie\ndelta\necho\nfoxtrot\ngolf\nhotel\nindia\njuliet"
	got := truncateFormatOutput(in, 3)
	assert.Contains(t, got, "alpha")
	assert.Contains(t, got, "bravo")
	assert.Contains(t, got, "charlie")
	assert.NotContains(t, got, "delta", "lines past the cap must be dropped")
	assert.NotContains(t, got, "juliet")
	// Footer announces the truncation count (10 input - 3 kept = 7 truncated).
	assert.Contains(t, got, "... (7 lines truncated)")
}

func TestTruncateFormatOutput_ExactLimit(t *testing.T) {
	// Exactly maxLines → no footer.
	in := "a\nb\nc"
	assert.Equal(t, in, truncateFormatOutput(in, 3))
	assert.NotContains(t, truncateFormatOutput(in, 3), "truncated")
}
