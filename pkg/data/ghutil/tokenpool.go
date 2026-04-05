package ghutil

import (
	"strings"
	"sync"
)

// TokenPool manages a pool of GitHub API tokens using round-robin selection.
// Safe for concurrent use. Works transparently with a single token.
// Tracks per-token usage counts for observability.
type TokenPool struct {
	mu      sync.Mutex
	tokens  []string
	counts  []int
	current int
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
	return &TokenPool{tokens: list, counts: make([]int, len(list))}
}

// Token returns the next token in the round-robin rotation.
func (p *TokenPool) Token() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.tokens) == 0 {
		return ""
	}

	idx := p.current
	p.counts[idx]++
	p.current = (idx + 1) % len(p.tokens)
	return p.tokens[idx]
}

// Size returns the number of tokens in the pool.
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
