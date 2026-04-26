package server

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// bgWorker tracks fire-and-forget goroutines spawned by HTTP handlers so the
// server can drain them at shutdown. Each submitted task gets a detached
// context with a timeout so request cancellation does not abort work that
// must outlive the request (sample seeding, cache warming, etc.).
type bgWorker struct {
	wg sync.WaitGroup
}

var bg = &bgWorker{}

// Submit runs fn in a goroutine. The provided timeout bounds fn's work; the
// context passed to fn is detached from any caller context so the request
// returning does not abort fn. Panics are logged but do not crash the server.
func (b *bgWorker) Submit(name string, timeout time.Duration, fn func(ctx context.Context)) {
	if b == nil {
		return
	}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("background task panic",
					"task", name, "panic", rec, "stack", string(debug.Stack()))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		fn(ctx)
	}()
}

// Wait blocks until all submitted tasks have returned.
func (b *bgWorker) Wait() {
	if b == nil {
		return
	}
	b.wg.Wait()
}
