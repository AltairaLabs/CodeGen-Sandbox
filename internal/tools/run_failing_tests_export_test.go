package tools

// ExportComposeGoRerunArgv exposes composeGoRerunArgv for tests in
// package tools_test. Kept in a _test.go file so it doesn't leak into the
// non-test build.
var ExportComposeGoRerunArgv = composeGoRerunArgv

// ExportParseRerunLimit exposes parseRerunLimit for tests in package
// tools_test.
var ExportParseRerunLimit = parseRerunLimit
