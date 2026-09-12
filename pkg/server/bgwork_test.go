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
