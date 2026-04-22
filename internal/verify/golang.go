package verify

// goDetector implements Detector for Go projects identified by a go.mod at
// the workspace root.
type goDetector struct {
	root string
}

// Language reports "go".
func (*goDetector) Language() string { return "go" }

// TestCmd returns "go test ./..." — runs every package in the module.
func (*goDetector) TestCmd() []string { return []string{"go", "test", "./..."} }

// LintCmd returns "golangci-lint run ./..." — matches the project's Makefile
// convention and the golangci-lint v2 invocation shape.
func (*goDetector) LintCmd() []string { return []string{"golangci-lint", "run", "./..."} }

// TypecheckCmd returns "go vet ./..." — Go's native "does this type-check
// and pass static checks" command.
func (*goDetector) TypecheckCmd() []string { return []string{"go", "vet", "./..."} }
