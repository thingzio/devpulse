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

package ghutil

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTokenPoolSingle(t *testing.T) {
	pool := NewTokenPool("tok1")
	assert.Equal(t, 1, pool.Size())
	assert.Equal(t, "tok1", pool.Token())
	assert.Equal(t, "tok1", pool.Token()) // single token always returns same
}

func TestNewTokenPoolMultiple(t *testing.T) {
	pool := NewTokenPool("tok1", "tok2", "tok3")
	assert.Equal(t, 3, pool.Size())
	assert.Equal(t, "tok1", pool.Token())
	assert.Equal(t, "tok2", pool.Token())
	assert.Equal(t, "tok3", pool.Token())
	assert.Equal(t, "tok1", pool.Token()) // wraps around
}

func TestNewTokenPoolCommaSeparated(t *testing.T) {
	pool := NewTokenPool("tok1,tok2,tok3")
	assert.Equal(t, 3, pool.Size())
}

func TestNewTokenPoolMixed(t *testing.T) {
	pool := NewTokenPool("tok1,tok2", "tok3")
	assert.Equal(t, 3, pool.Size())
}

func TestNewTokenPoolEmpty(t *testing.T) {
	pool := NewTokenPool("")
	assert.Equal(t, 0, pool.Size())
	assert.Equal(t, "", pool.Token())
}

func TestNewTokenPoolTrimsWhitespace(t *testing.T) {
	pool := NewTokenPool(" tok1 , tok2 ")
	assert.Equal(t, 2, pool.Size())
	assert.Equal(t, "tok1", pool.Token())
	assert.Equal(t, "tok2", pool.Token())
}

func TestTokenPoolRoundRobin(t *testing.T) {
	pool := NewTokenPool("a", "b")
	seen := make(map[string]int)
	for range 100 {
		seen[pool.Token()]++
	}
	assert.Equal(t, 50, seen["a"])
	assert.Equal(t, 50, seen["b"])
}

func TestTokenPoolNeverReturnsComma(t *testing.T) {
	pool := NewTokenPool("ghp_abc123,ghp_def456")
	for range 10 {
		tok := pool.Token()
		assert.NotContains(t, tok, ",", "Token() must return a single token, not comma-separated")
	}
}

func TestTokenPoolUsageCounts(t *testing.T) {
	pool := NewTokenPool("a", "b", "c")
	for range 9 {
		pool.Token()
	}
	counts := pool.UsageCounts()
	assert.Equal(t, []int{3, 3, 3}, counts)
}

func TestTokenPoolUsageCountsEmpty(t *testing.T) {
	pool := NewTokenPool("")
	pool.Token() // no-op on empty pool
	assert.Equal(t, []int{}, pool.UsageCounts())
}

func TestTokenPoolConcurrentAccess(t *testing.T) {
	pool := NewTokenPool("tok1", "tok2", "tok3")

	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok := pool.Token()
			require.NotEmpty(t, tok)
		}()
	}
	wg.Wait()
}

func TestTokenPoolExhaustSingle(t *testing.T) {
	pool := NewTokenPool("tok1", "tok2", "tok3")
	pool.Exhaust("tok2")
	assert.Equal(t, 2, pool.ActiveCount())
	assert.Equal(t, 3, pool.Size()) // total unchanged

	// tok2 should be skipped in rotation
	seen := make(map[string]int)
	for range 10 {
		tok := pool.Token()
		require.NotEmpty(t, tok)
		seen[tok]++
	}
	assert.Zero(t, seen["tok2"], "exhausted token should never be returned")
	assert.Greater(t, seen["tok1"], 0)
	assert.Greater(t, seen["tok3"], 0)
}

func TestTokenPoolExhaustAll(t *testing.T) {
	pool := NewTokenPool("tok1", "tok2")
	pool.Exhaust("tok1")
	pool.Exhaust("tok2")
	assert.Equal(t, 0, pool.ActiveCount())
	assert.Equal(t, "", pool.Token())
}

func TestTokenPoolExhaustUnknownToken(t *testing.T) {
	pool := NewTokenPool("tok1")
	pool.Exhaust("unknown") // should not panic
	assert.Equal(t, 1, pool.ActiveCount())
	assert.Equal(t, "tok1", pool.Token())
}

func TestTokenPoolActiveCount(t *testing.T) {
	pool := NewTokenPool("a", "b", "c", "d")
	assert.Equal(t, 4, pool.ActiveCount())
	pool.Exhaust("b")
	assert.Equal(t, 3, pool.ActiveCount())
	pool.Exhaust("d")
	assert.Equal(t, 2, pool.ActiveCount())
}

func TestTokenPoolExhaustMidRotation(t *testing.T) {
	pool := NewTokenPool("a", "b", "c")

	// Consume "a", then exhaust "b"
	assert.Equal(t, "a", pool.Token())
	pool.Exhaust("b")

	// Next should skip "b" and return "c"
	assert.Equal(t, "c", pool.Token())
	assert.Equal(t, "a", pool.Token())
}

// TestTokenPoolExhaustionRecovers verifies that an exhausted token re-enters
// the rotation once the exhaustion window has elapsed. Uses a manual clock
// to avoid sleeping.
func TestTokenPoolExhaustionRecovers(t *testing.T) {
	pool := NewTokenPool("a", "b")
	now := time.Unix(0, 0)
	pool.now = func() time.Time { return now }

	pool.Exhaust("a")
	assert.Equal(t, 1, pool.ActiveCount(), "a is exhausted")
	assert.Equal(t, "b", pool.Token())
	assert.Equal(t, "b", pool.Token(), "a still exhausted")

	// Advance past the exhaustion window.
	now = now.Add(defaultExhaustionWindow + time.Second)
	assert.Equal(t, 2, pool.ActiveCount(), "a recovered")

	seen := make(map[string]int)
	for range 4 {
		seen[pool.Token()]++
	}
	assert.Greater(t, seen["a"], 0, "a back in rotation")
	assert.Greater(t, seen["b"], 0)
}

func TestTokenPoolExhaustionWindowConfigurable(t *testing.T) {
	pool := NewTokenPool("a")
	now := time.Unix(0, 0)
	pool.now = func() time.Time { return now }
	pool.exhaustionWindow = time.Minute

	pool.Exhaust("a")
	assert.Equal(t, "", pool.Token())

	now = now.Add(time.Minute + time.Second)
	assert.Equal(t, "a", pool.Token())
}
