// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

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
