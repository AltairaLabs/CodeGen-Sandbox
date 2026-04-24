package server_test

import (
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/server"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/require"
)

func TestServer_New(t *testing.T) {
	ws, err := workspace.New(t.TempDir())
	require.NoError(t, err)

	srv, err := server.New(ws, nil)
	require.NoError(t, err)
	require.NotNil(t, srv)
	require.NotNil(t, srv.Handler())
}
