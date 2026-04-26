package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBGWorkerSubmitAndWait(t *testing.T) {
	w := &bgWorker{}
	var count int32
	for i := 0; i < 5; i++ {
		w.Submit("noop", time.Second, func(_ context.Context) {
			atomic.AddInt32(&count, 1)
		})
	}
	w.Wait()
	assert.Equal(t, int32(5), atomic.LoadInt32(&count))
}

func TestBGWorkerPanicIsContained(t *testing.T) {
	w := &bgWorker{}
	w.Submit("boom", time.Second, func(_ context.Context) {
		panic("boom")
	})
	// Must not deadlock or propagate the panic to the test goroutine.
	w.Wait()
}

func TestBGWorkerNilSafe(t *testing.T) {
	var w *bgWorker
	w.Submit("noop", time.Second, func(_ context.Context) {
		t.Fatal("nil worker must not run the function")
	})
	w.Wait()
}
