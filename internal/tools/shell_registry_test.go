package tools

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShellRegistry_RegisterGetRemove(t *testing.T) {
	r := NewShellRegistry()
	id := NewShellID()
	sh := NewBackgroundShell(id, "echo x")
	r.Register(sh)

	got, ok := r.Get(id)
	require.True(t, ok)
	assert.Same(t, sh, got)

	r.Remove(id)
	_, ok = r.Get(id)
	assert.False(t, ok)
}

func TestShellRegistry_ConcurrentAccess(_ *testing.T) {
	r := NewShellRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := NewShellID()
			sh := NewBackgroundShell(id, "cmd")
			r.Register(sh)
			_, _ = r.Get(id)
			r.Remove(id)
		}()
	}
	wg.Wait()
}

func TestBackgroundShell_AppendAndSnapshot(t *testing.T) {
	sh := NewBackgroundShell("id-1", "cmd")
	sh.AppendStdout([]byte("hello "))
	sh.AppendStdout([]byte("world"))
	sh.AppendStderr([]byte("oops"))

	stdout, stderr, stdoutT, stderrT, exit, running := sh.Snapshot()
	assert.Equal(t, "hello world", string(stdout))
	assert.Equal(t, "oops", string(stderr))
	assert.False(t, stdoutT)
	assert.False(t, stderrT)
	assert.Nil(t, exit)
	assert.True(t, running)

	sh.SetExit(42)
	_, _, _, _, exit2, running2 := sh.Snapshot()
	require.NotNil(t, exit2)
	assert.Equal(t, 42, *exit2)
	assert.False(t, running2)
}

func TestBackgroundShell_CapTruncates(t *testing.T) {
	sh := NewBackgroundShell("id", "cmd")
	chunk := make([]byte, 512*1024)
	sh.AppendStdout(chunk)
	sh.AppendStdout(chunk)
	sh.AppendStdout([]byte("should be truncated"))

	stdout, _, stdoutT, _, _, _ := sh.Snapshot()
	assert.Len(t, stdout, shellOutputCapBytes)
	assert.True(t, stdoutT)
}

func TestBackgroundShell_StartedAt(t *testing.T) {
	before := time.Now()
	sh := NewBackgroundShell("id", "cmd")
	assert.WithinDuration(t, before, sh.StartedAt(), time.Second)
}
