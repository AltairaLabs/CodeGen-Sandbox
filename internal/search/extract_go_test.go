package search_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func TestExtractGo_FunctionWithDocstring(t *testing.T) {
	dir := t.TempDir()
	src := `// Package p does things.
package p

// FileHandler serves raw bytes; caps at 2 MiB. Returns 500 on unexpected errors.
func FileHandler() {}
`
	path := filepath.Join(dir, "a.go")
	writeFile(t, path, src)

	units, err := search.ExtractGoFile(path, dir)
	require.NoError(t, err)
	require.NotEmpty(t, units)

	var fn *search.Unit
	for i := range units {
		if units[i].Symbol == "FileHandler" {
			fn = &units[i]
			break
		}
	}
	require.NotNil(t, fn, "expected FileHandler in %#v", units)
	assert.Equal(t, "func", fn.Kind)
	assert.Contains(t, fn.Doc, "500")
	assert.Contains(t, fn.Signature, "func FileHandler()")
	assert.Equal(t, "a.go", fn.File)
}

func TestExtractGo_MethodWithReceiver(t *testing.T) {
	dir := t.TempDir()
	src := `package p

type Server struct{}

// Handle processes a request.
func (s *Server) Handle() {}
`
	writeFile(t, filepath.Join(dir, "b.go"), src)

	units, err := search.ExtractGoFile(filepath.Join(dir, "b.go"), dir)
	require.NoError(t, err)

	var m *search.Unit
	for i := range units {
		if units[i].Symbol == "Handle" {
			m = &units[i]
			break
		}
	}
	require.NotNil(t, m)
	assert.Equal(t, "method", m.Kind)
	assert.Contains(t, m.Signature, "*Server")
	assert.Contains(t, m.Doc, "request")
}

func TestExtractGo_TypeWithFields(t *testing.T) {
	dir := t.TempDir()
	src := `package p

// Config holds server configuration.
type Config struct {
	// Port is the listen port.
	Port int
	Host string
}
`
	writeFile(t, filepath.Join(dir, "c.go"), src)

	units, err := search.ExtractGoFile(filepath.Join(dir, "c.go"), dir)
	require.NoError(t, err)

	var typ *search.Unit
	for i := range units {
		if units[i].Symbol == "Config" {
			typ = &units[i]
			break
		}
	}
	require.NotNil(t, typ)
	assert.Equal(t, "type", typ.Kind)
	assert.Contains(t, typ.Doc, "configuration")
	assert.Contains(t, typ.Signature, "Port")
	assert.Contains(t, typ.Signature, "Host")
}

func TestExtractGo_ConstGroup(t *testing.T) {
	dir := t.TempDir()
	src := `package p

// StatusOK is the HTTP OK code.
const StatusOK = 200
`
	writeFile(t, filepath.Join(dir, "d.go"), src)

	units, err := search.ExtractGoFile(filepath.Join(dir, "d.go"), dir)
	require.NoError(t, err)

	var c *search.Unit
	for i := range units {
		if units[i].Symbol == "StatusOK" {
			c = &units[i]
			break
		}
	}
	require.NotNil(t, c)
	assert.Equal(t, "const", c.Kind)
	assert.Contains(t, c.Doc, "HTTP")
}

func TestExtractGo_VarWithComment(t *testing.T) {
	dir := t.TempDir()
	src := `package p

import "regexp"

// denyPattern matches forbidden command tokens.
var denyPattern = regexp.MustCompile("x")
`
	writeFile(t, filepath.Join(dir, "e.go"), src)

	units, err := search.ExtractGoFile(filepath.Join(dir, "e.go"), dir)
	require.NoError(t, err)

	var v *search.Unit
	for i := range units {
		if units[i].Symbol == "denyPattern" {
			v = &units[i]
			break
		}
	}
	require.NotNil(t, v)
	assert.Equal(t, "var", v.Kind)
	assert.Contains(t, v.Doc, "forbidden")
}

func TestExtractGo_SkipsNonGoFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "readme.md"), "# not go")

	units, err := search.ExtractGoFile(filepath.Join(dir, "readme.md"), dir)
	require.NoError(t, err)
	assert.Empty(t, units)
}

func TestExtractGo_FileLevelDoc(t *testing.T) {
	dir := t.TempDir()
	src := `// Package foo is a package that does foo things in the sandbox.
package foo

func Bar() {}
`
	writeFile(t, filepath.Join(dir, "f.go"), src)

	units, err := search.ExtractGoFile(filepath.Join(dir, "f.go"), dir)
	require.NoError(t, err)

	// The first unit per-file is the package-level unit carrying the file doc.
	var pkgUnit *search.Unit
	for i := range units {
		if units[i].Kind == "package" {
			pkgUnit = &units[i]
			break
		}
	}
	require.NotNil(t, pkgUnit)
	assert.Contains(t, pkgUnit.Doc, "foo things")
}
