package workspace_test

import (
	"sync"
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/stretchr/testify/assert"
)

func TestTracker_UnseenPathIsNotRead(t *testing.T) {
	tr := workspace.NewReadTracker()
	assert.False(t, tr.HasBeenRead("/workspace/foo.txt"))
}

func TestTracker_MarkThenQuery(t *testing.T) {
	tr := workspace.NewReadTracker()
	tr.MarkRead("/workspace/foo.txt")
	assert.True(t, tr.HasBeenRead("/workspace/foo.txt"))
	assert.False(t, tr.HasBeenRead("/workspace/other.txt"))
}

func TestTracker_ConcurrentAccess(t *testing.T) {
	tr := workspace.NewReadTracker()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.MarkRead("/workspace/foo.txt")
			_ = tr.HasBeenRead("/workspace/foo.txt")
		}()
	}
	wg.Wait()
	assert.True(t, tr.HasBeenRead("/workspace/foo.txt"))
}
