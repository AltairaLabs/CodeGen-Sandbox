package search

import (
	"strings"
	"unicode"
)

// stopwords is a deliberately small fixed list; BM25's IDF already suppresses
// highly frequent terms but carving the most obvious English fillers keeps
// the per-unit token vectors tight.
var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "of": true,
	"to": true, "and": true, "or": true, "for": true, "in": true,
	"on": true, "that": true, "this": true,
}

// Tokenize splits s into lowercased tokens following these rules:
//   - Non-letter / non-digit characters become split boundaries.
//   - Each raw word is emitted whole AND split further on CamelCase /
//     snake_case / kebab-case boundaries.
//   - Stopwords are dropped.
//
// The raw word (e.g. "fileHandler") is retained alongside its splits ("file",
// "handler") so an exact-identifier query outranks a partial-split query.
func Tokenize(s string) []string {
	if s == "" {
		return nil
	}
	words := splitOnNonAlnum(s)
	out := make([]string, 0, len(words)*2)
	for _, w := range words {
		if w == "" {
			continue
		}
		lw := strings.ToLower(w)
		if !stopwords[lw] {
			out = append(out, lw)
		}
		for _, sub := range splitCamel(w) {
			ls := strings.ToLower(sub)
			if ls == lw || ls == "" || stopwords[ls] {
				continue
			}
			out = append(out, ls)
		}
	}
	return out
}

// splitOnNonAlnum splits on any non-letter / non-digit rune.
func splitOnNonAlnum(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// splitCamel splits a single word into CamelCase / acronym segments.
// "HTTPServer" → ["HTTP", "Server"]; "fileHandler" → ["file", "Handler"].
// Digits break on letter-to-digit and digit-to-letter transitions.
func splitCamel(w string) []string {
	runes := []rune(w)
	if len(runes) == 0 {
		return nil
	}
	var parts []string
	start := 0
	for i := 1; i < len(runes); i++ {
		if camelBoundary(runes, i) {
			parts = append(parts, string(runes[start:i]))
			start = i
		}
	}
	parts = append(parts, string(runes[start:]))
	return parts
}

// camelBoundary reports whether index i starts a new camel segment.
// Three triggers:
//
//	lower→upper           :  "aA"    → split
//	upper→upper→lower     :  "ABc"   → split before "Bc" (acronym end)
//	letter↔digit          :  "a1"/"1a" → split
func camelBoundary(runes []rune, i int) bool {
	prev, cur := runes[i-1], runes[i]
	if unicode.IsLower(prev) && unicode.IsUpper(cur) {
		return true
	}
	if i+1 < len(runes) && unicode.IsUpper(prev) && unicode.IsUpper(cur) && unicode.IsLower(runes[i+1]) {
		return true
	}
	if unicode.IsLetter(prev) != unicode.IsLetter(cur) {
		return true
	}
	return false
}
