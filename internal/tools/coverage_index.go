package tools

import (
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// CoverageIndex is a session-scoped inverse index of
// (workspace-relative file path, 1-based line) -> {test names}. It is
// populated by run_tests after every Go test invocation that produces a
// coverage profile, and read by tests_covering. Reset on server restart —
// there's no persistence.
//
// v1 attribution is package-level: Go's standard -coverprofile is
// per-run aggregate (not per-test), so each file:line touched by any
// record is mapped to every test that ran in that file's package during
// the same run. Agents can then scope run_failing_tests / run_tests to
// those packages.
type CoverageIndex struct {
	mu sync.RWMutex
	// byFile maps workspace-relative file path -> line -> set of
	// qualified test names ("pkg/import/path:TestName").
	byFile map[string]map[int]map[string]struct{}
}

// TestRef identifies a single test by its package import path and name.
type TestRef struct {
	Package  string
	TestName string
}

// NewCoverageIndex constructs an empty index.
func NewCoverageIndex() *CoverageIndex {
	return &CoverageIndex{byFile: map[string]map[int]map[string]struct{}{}}
}

// Empty reports whether Ingest has produced any entries. Nil-safe.
func (c *CoverageIndex) Empty() bool {
	if c == nil {
		return true
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byFile) == 0
}

// Ingest parses a Go coverage profile at profilePath, intersects each
// record's package with testsByPackage (map keyed by Go import path,
// value = test names that ran in that package), and replaces the stored
// index with the result. Each run_tests call overwrites the prior
// contents — same contract as TestResultStore.
//
// Profile records that can't be matched to the workspace (no touched
// file) or to any test-run (package absent from testsByPackage) are
// silently dropped. Nil-safe receiver: no-op when c is nil.
func (c *CoverageIndex) Ingest(profilePath string, testsByPackage map[string][]string) {
	if c == nil {
		return
	}
	raw, err := os.ReadFile(profilePath) //nolint:gosec // profile path is internally allocated
	if err != nil {
		return
	}
	records := parseCoverageProfile(string(raw))
	next := buildCoverageByFile(records, testsByPackage)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.byFile = next
}

// TestsCovering returns the tests whose package touched the given file
// (and, when line > 0, that specific line). Returns nil when no tests
// cover the query. Nil-safe receiver.
//
// line <= 0 means "any line in the file".
func (c *CoverageIndex) TestsCovering(file string, line int) []TestRef {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	byLine, ok := c.byFile[file]
	if !ok {
		return nil
	}
	seen := map[string]struct{}{}
	for ln, tests := range byLine {
		if line > 0 && ln != line {
			continue
		}
		for t := range tests {
			seen[t] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]TestRef, 0, len(keys))
	for _, k := range keys {
		pkg, name := splitQualifiedTestName(k)
		out = append(out, TestRef{Package: pkg, TestName: name})
	}
	return out
}

// coverageRecord is one line of a Go coverage profile, parsed. File is
// the raw profile path (module-qualified), Package is the import path
// (File minus the trailing /<file.go>).
type coverageRecord struct {
	File      string
	Package   string
	StartLine int
	EndLine   int
}

// parseCoverageProfile reads the text-format profile body and returns
// every well-formed record. Malformed lines (wrong field count, bad
// numbers, missing colon) are dropped silently — the format is stable
// but we don't want one bad byte to kill the ingest.
//
// Profile shape:
//
//	mode: set
//	<pkg>/<file.go>:<startLine>.<startCol>,<endLine>.<endCol> <numStmts> <count>
func parseCoverageProfile(body string) []coverageRecord {
	var out []coverageRecord
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "mode:") {
			continue
		}
		rec, ok := parseCoverageRecord(trimmed)
		if !ok {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// parseCoverageRecord parses one non-header line of a cover profile.
// Returns (record, false) on any malformation.
func parseCoverageRecord(line string) (coverageRecord, bool) {
	// Split into 3 whitespace fields: location, numStmts, count.
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return coverageRecord{}, false
	}
	location := fields[0]
	colon := strings.LastIndex(location, ":")
	if colon <= 0 || colon == len(location)-1 {
		return coverageRecord{}, false
	}
	filePath := location[:colon]
	rangeSpec := location[colon+1:]
	// rangeSpec: startLine.startCol,endLine.endCol
	comma := strings.Index(rangeSpec, ",")
	if comma <= 0 {
		return coverageRecord{}, false
	}
	startLine, ok := parseLinePart(rangeSpec[:comma])
	if !ok {
		return coverageRecord{}, false
	}
	endLine, ok := parseLinePart(rangeSpec[comma+1:])
	if !ok {
		return coverageRecord{}, false
	}
	pkg := ""
	if slash := strings.LastIndex(filePath, "/"); slash > 0 {
		pkg = filePath[:slash]
	}
	return coverageRecord{
		File:      filePath,
		Package:   pkg,
		StartLine: startLine,
		EndLine:   endLine,
	}, true
}

// parseLinePart extracts the integer line number before the "." in a
// "line.col" fragment. Returns (0, false) if the fragment is malformed.
func parseLinePart(s string) (int, bool) {
	dot := strings.Index(s, ".")
	if dot <= 0 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:dot])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// buildCoverageByFile transforms parsed records + testsByPackage into
// the byFile map. For each record:
//  1. Resolve the workspace-relative file key (profile paths are
//     module-qualified like "example.com/foo/bar.go"; we key by the
//     portion after the module prefix — everything from the package
//     segment onward — but also index by the raw path so callers can
//     match either way).
//  2. Find the test set for the record's package.
//  3. Fan-out every line in [StartLine, EndLine] to the test set.
func buildCoverageByFile(records []coverageRecord, testsByPackage map[string][]string) map[string]map[int]map[string]struct{} {
	out := map[string]map[int]map[string]struct{}{}
	for _, rec := range records {
		tests := testsByPackage[rec.Package]
		if len(tests) == 0 {
			continue
		}
		if rec.EndLine < rec.StartLine {
			continue
		}
		keys := fileKeysFor(rec.File)
		for _, key := range keys {
			ensureFileEntry(out, key)
			for ln := rec.StartLine; ln <= rec.EndLine; ln++ {
				lineEntry := ensureLineEntry(out[key], ln)
				for _, tname := range tests {
					qualified := rec.Package + ":" + tname
					lineEntry[qualified] = struct{}{}
				}
			}
		}
	}
	return out
}

// fileKeysFor returns every key a caller might query by: the raw
// module-qualified path and the path stripped to just the package's
// final segment + filename. Querying by "workspace-relative" path from
// the tool handler means that for a workspace that happens to be the
// module root (common), the file name alone matches — and for
// subdirectory queries ("internal/tools/read.go") the key strips the
// module prefix.
func fileKeysFor(profileFile string) []string {
	keys := []string{profileFile}
	// Module-prefix-stripped variants: split at each "/" and offer every
	// suffix as a candidate key. Prevents us from having to know the
	// module path at ingest time.
	for i := 0; i < len(profileFile); i++ {
		if profileFile[i] == '/' && i+1 < len(profileFile) {
			keys = append(keys, profileFile[i+1:])
		}
	}
	return keys
}

// ensureFileEntry materializes out[key] if absent.
func ensureFileEntry(m map[string]map[int]map[string]struct{}, key string) {
	if _, ok := m[key]; !ok {
		m[key] = map[int]map[string]struct{}{}
	}
}

// ensureLineEntry materializes m[ln] if absent and returns the line set.
func ensureLineEntry(m map[int]map[string]struct{}, ln int) map[string]struct{} {
	if existing, ok := m[ln]; ok {
		return existing
	}
	set := map[string]struct{}{}
	m[ln] = set
	return set
}
