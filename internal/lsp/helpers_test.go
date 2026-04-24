package lsp

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeLocations_Null(t *testing.T) {
	got, err := decodeLocations(nil, "/workspace")
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = decodeLocations(json.RawMessage("null"), "/workspace")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestDecodeLocations_Array(t *testing.T) {
	raw := json.RawMessage(`[
		{"uri":"file:///workspace/foo.go","range":{"start":{"line":5,"character":3},"end":{"line":5,"character":10}}},
		{"uri":"file:///workspace/bar.go","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}
	]`)
	got, err := decodeLocations(raw, "/workspace")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "foo.go", got[0].URI)
	assert.Equal(t, 6, got[0].Line) // 1-based
	assert.Equal(t, 4, got[0].Col)
	assert.Equal(t, "bar.go", got[1].URI)
	assert.Equal(t, 1, got[1].Line)
}

func TestDecodeLocations_SingleObject(t *testing.T) {
	raw := json.RawMessage(`{"uri":"file:///workspace/x.go","range":{"start":{"line":2,"character":0},"end":{"line":2,"character":5}}}`)
	got, err := decodeLocations(raw, "/workspace")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "x.go", got[0].URI)
	assert.Equal(t, 3, got[0].Line)
}

func TestDecodeLocations_Malformed(t *testing.T) {
	_, err := decodeLocations(json.RawMessage(`[{"bad":`), "/workspace")
	assert.Error(t, err)

	_, err = decodeLocations(json.RawMessage(`{"bad":`), "/workspace")
	assert.Error(t, err)
}

func TestDecodeWorkspaceEdit_Null(t *testing.T) {
	we, err := decodeWorkspaceEdit(nil, "/workspace")
	require.NoError(t, err)
	assert.Empty(t, we.Changes)

	we, err = decodeWorkspaceEdit(json.RawMessage("null"), "/workspace")
	require.NoError(t, err)
	assert.Empty(t, we.Changes)
}

func TestDecodeWorkspaceEdit_Happy(t *testing.T) {
	raw := json.RawMessage(`{"changes":{
		"file:///workspace/a.go":[
			{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":5}},"newText":"hello"}
		]
	}}`)
	we, err := decodeWorkspaceEdit(raw, "/workspace")
	require.NoError(t, err)
	edits, ok := we.Changes["a.go"]
	require.True(t, ok, "expected edits keyed by workspace-relative path")
	require.Len(t, edits, 1)
	assert.Equal(t, 1, edits[0].Line)
	assert.Equal(t, "hello", edits[0].NewText)
}

// TestDecodeWorkspaceEdit_DocumentChanges exercises the `documentChanges`
// form that gopls returns (LSP 3.16+). Before this branch of the decoder
// existed the rename_symbol tool returned "no rename available" against
// real gopls — see the P0 integration smoke that caught it.
func TestDecodeWorkspaceEdit_DocumentChanges(t *testing.T) {
	raw := json.RawMessage(`{"documentChanges":[
		{
			"textDocument":{"version":0,"uri":"file:///workspace/a.go"},
			"edits":[{"range":{"start":{"line":2,"character":5},"end":{"line":2,"character":8}},"newText":"Sum"}]
		},
		{
			"textDocument":{"version":0,"uri":"file:///workspace/b.go"},
			"edits":[{"range":{"start":{"line":5,"character":4},"end":{"line":5,"character":7}},"newText":"Sum"}]
		}
	]}`)
	we, err := decodeWorkspaceEdit(raw, "/workspace")
	require.NoError(t, err)
	aEdits, aOK := we.Changes["a.go"]
	bEdits, bOK := we.Changes["b.go"]
	require.True(t, aOK, "missing a.go: %+v", we.Changes)
	require.True(t, bOK, "missing b.go: %+v", we.Changes)
	require.Len(t, aEdits, 1)
	require.Len(t, bEdits, 1)
	assert.Equal(t, 3, aEdits[0].Line) // 0-based line 2 → 1-based 3
	assert.Equal(t, "Sum", aEdits[0].NewText)
	assert.Equal(t, "Sum", bEdits[0].NewText)
}

// TestDecodeWorkspaceEdit_BothForms confirms the decoder merges `changes`
// and `documentChanges` into the same output map when a server returns
// both (rare but spec-legal).
func TestDecodeWorkspaceEdit_BothForms(t *testing.T) {
	raw := json.RawMessage(`{
		"changes":{"file:///workspace/a.go":[
			{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"x"}
		]},
		"documentChanges":[{
			"textDocument":{"version":0,"uri":"file:///workspace/b.go"},
			"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"y"}]
		}]
	}`)
	we, err := decodeWorkspaceEdit(raw, "/workspace")
	require.NoError(t, err)
	assert.Len(t, we.Changes, 2)
	assert.Equal(t, "x", we.Changes["a.go"][0].NewText)
	assert.Equal(t, "y", we.Changes["b.go"][0].NewText)
}

func TestURIToPathAndRel(t *testing.T) {
	// uriToPath
	assert.Equal(t, "/workspace/foo.go", uriToPath("file:///workspace/foo.go"))
	assert.Equal(t, "not a uri", uriToPath("not a uri"))     // not a URI → pass-through
	assert.Equal(t, "http://ex/x", uriToPath("http://ex/x")) // non-file scheme → pass-through

	// uriToRel: inside workspace → relative; outside → absolute path unchanged
	assert.Equal(t, "foo.go", uriToRel("file:///workspace/foo.go", "/workspace"))
	assert.Equal(t, "/other/x.go", uriToRel("file:///other/x.go", "/workspace"))
}

func TestAbsFile(t *testing.T) {
	root := "/ws"
	assert.Equal(t, filepath.Join(root, "sub/foo.go"), absFile(root, "sub/foo.go"))
	assert.Equal(t, "/abs/path.go", absFile(root, "/abs/path.go"))
}

func TestPathToURI(t *testing.T) {
	assert.Equal(t, "file:///workspace/foo.go", pathToURI("/workspace/foo.go"))
}

func TestClientWorkspaceAndLastUsed(t *testing.T) {
	c := NewClient("/some/ws", []string{"gopls", "serve"})
	assert.Equal(t, "/some/ws", c.Workspace())

	// Zero-value LastUsed until a request has completed.
	assert.True(t, c.LastUsed().IsZero())

	// Manually bump the atomic, simulating a completed request.
	c.lastUsedUnixNano.Store(time.Now().UnixNano())
	assert.False(t, c.LastUsed().IsZero())
}
