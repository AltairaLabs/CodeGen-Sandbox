package tools

// ExportParseCoverageProfile exposes parseCoverageProfile for tests in
// package tools_test. Kept in a _test.go file so it doesn't leak into
// the non-test build.
func ExportParseCoverageProfile(body string) []ExportCoverageRecord {
	records := parseCoverageProfile(body)
	out := make([]ExportCoverageRecord, 0, len(records))
	for _, r := range records {
		out = append(out, ExportCoverageRecord{
			File:      r.File,
			Package:   r.Package,
			StartLine: r.StartLine,
			EndLine:   r.EndLine,
		})
	}
	return out
}

// ExportCoverageRecord mirrors coverageRecord for test assertions.
type ExportCoverageRecord struct {
	File      string
	Package   string
	StartLine int
	EndLine   int
}

// ExportAugmentTestCmdForCoverage exposes augmentTestCmdForCoverage to
// tests_test so we can exercise the non-Go / no-index branches without
// booting the full MCP server.
var ExportAugmentTestCmdForCoverage = augmentTestCmdForCoverage

// ExportCleanupCoverageProfile exposes cleanupCoverageProfile for
// direct branch coverage.
var ExportCleanupCoverageProfile = cleanupCoverageProfile
