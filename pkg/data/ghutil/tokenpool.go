package ghutil

import (
	"strings"
	"sync"
)

// TokenPool manages a pool of GitHub API tokens using round-robin selection.
// Safe for concurrent use. Works transparently with a single token.
// Tracks per-token usage counts for observability and supports marking
// individual tokens as exhausted (e.g. after hitting a rate limit).
type TokenPool struct {
	mu        sync.Mutex
	tokens    []string
	counts    []int
	exhausted []bool
	current   int
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
		tokens:    list,
		counts:    make([]int, len(list)),
		exhausted: make([]bool, len(list)),
	}
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
		if !p.exhausted[idx] {
			p.counts[idx]++
			return p.tokens[idx]
		}
	}

	return "" // all exhausted
}

// Exhaust marks the given token as exhausted so Token() skips it.
func (p *TokenPool) Exhaust(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, t := range p.tokens {
		if t == token {
			p.exhausted[i] = true
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
		if !p.exhausted[i] {
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
