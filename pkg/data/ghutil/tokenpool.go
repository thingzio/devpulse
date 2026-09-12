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
	"strings"
	"sync"
	"time"
)

// defaultExhaustionWindow is how long an exhausted token stays out of
// rotation before becoming eligible again. The GitHub primary rate limit
// resets hourly so 1h is the correct ceiling; pick a touch shorter to
// reduce the window where every token is considered exhausted.
const defaultExhaustionWindow = 55 * time.Minute

// TokenPool manages a pool of GitHub API tokens using round-robin selection.
// Safe for concurrent use. Works transparently with a single token.
// Tracks per-token usage counts for observability and supports marking
// individual tokens as exhausted (e.g. after hitting a rate limit).
//
// Exhausted tokens automatically re-enter rotation after exhaustionWindow,
// so the pool degrades temporarily rather than monotonically.
type TokenPool struct {
	mu               sync.Mutex
	tokens           []string
	counts           []int
	exhaustedUntil   []time.Time
	exhaustionWindow time.Duration
	now              func() time.Time
	current          int
}

// NewTokenPool creates a pool from one or more tokens. Tokens can be passed
// individually or as a single comma-separated string.
func NewTokenPool(tokens ...string) *TokenPool {
	var list []string
	for _, t := range tokens {
		for _, part := range strings.Split(t, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				list = append(list, part)
			}
		}
	}
	return &TokenPool{
		tokens:           list,
		counts:           make([]int, len(list)),
		exhaustedUntil:   make([]time.Time, len(list)),
		exhaustionWindow: defaultExhaustionWindow,
		now:              time.Now,
	}
}

// isExhausted reports whether token i is currently exhausted. Caller must hold p.mu.
func (p *TokenPool) isExhausted(i int) bool {
	until := p.exhaustedUntil[i]
	if until.IsZero() {
		return false
	}
	return p.now().Before(until)
}

// Token returns the next non-exhausted token in the round-robin rotation.
// Returns "" when all tokens are exhausted or the pool is empty.
func (p *TokenPool) Token() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(p.tokens)
	if n == 0 {
		return ""
	}

	// Try up to n positions to find a non-exhausted token.
	for range n {
		idx := p.current
		p.current = (idx + 1) % n
		if !p.isExhausted(idx) {
			p.counts[idx]++
			return p.tokens[idx]
		}
	}

	return "" // all exhausted
}

// Exhaust marks the given token as exhausted so Token() skips it for the
// configured exhaustion window. After the window elapses, the token is
// eligible for selection again automatically.
func (p *TokenPool) Exhaust(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, t := range p.tokens {
		if t == token {
			p.exhaustedUntil[i] = p.now().Add(p.exhaustionWindow)
			return
		}
	}
}

// ActiveCount returns the number of non-exhausted tokens.
func (p *TokenPool) ActiveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	count := 0
	for i := range p.tokens {
		if !p.isExhausted(i) {
			count++
		}
	}
	return count
}

// Size returns the total number of tokens in the pool (including exhausted).
func (p *TokenPool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.tokens)
}

// UsageCounts returns a copy of per-token call counts (indexed by pool position).
func (p *TokenPool) UsageCounts() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]int, len(p.counts))
	copy(out, p.counts)
	return out
}
