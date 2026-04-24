package tools_test

import (
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindReferences_Happy(t *testing.T) {
	deps, root := newLSPTestDeps(t, "references")
	writeSeedGoFile(t, root, "a.go", "package testmod\n\nfunc ValidateToken() {}\n")
	writeSeedGoFile(t, root, "b.go", "package testmod\n\n// caller\nfunc Caller() {\n  ValidateToken()\n}\n")

	res := callLSP(t, tools.HandleFindReferences(deps), map[string]any{
		"file_path": "a.go",
		"line":      float64(3),
		"col":       float64(6),
	})
	require.False(t, res.IsError, textOf(t, res))
	body := textOf(t, res)
	mustContain(t, body, "Found 2 reference")
	mustContain(t, body, "a.go:")
	mustContain(t, body, "b.go:")
}

func TestFindReferences_MissingLine(t *testing.T) {
	deps, root := newLSPTestDeps(t, "references")
	writeSeedGoFile(t, root, "a.go", "package testmod\n")
	res := callLSP(t, tools.HandleFindReferences(deps), map[string]any{
		"file_path": "a.go",
		"col":       float64(1),
	})
	assert.True(t, res.IsError)
}

func TestFindReferences_MissingCol(t *testing.T) {
	deps, root := newLSPTestDeps(t, "references")
	writeSeedGoFile(t, root, "a.go", "package testmod\n")
	res := callLSP(t, tools.HandleFindReferences(deps), map[string]any{
		"file_path": "a.go",
		"line":      float64(1),
	})
	assert.True(t, res.IsError)
}
