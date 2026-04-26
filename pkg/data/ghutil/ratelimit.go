package ghutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	gonet "net"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v83/github"
	"github.com/thingzio/devpulse/pkg/net"
)

const minTokenQuota = 100

// MinTokenQuota returns the minimum remaining API calls required for a token
// to be included in the pool.
func MinTokenQuota() int { return minTokenQuota }

const RateLimitThreshold = 10

const maxWait = 15 * time.Minute

// ErrRateLimited is returned when a GitHub API token has hit its rate limit.
// Callers with a token pool should exhaust this token and try another.
var ErrRateLimited = errors.New("github token rate limited")

// IsServerError reports whether err (or any error in its chain) indicates a
// GitHub server error (5xx). These are transient and worth retrying.
func IsServerError(err error) bool {
	var errResp *github.ErrorResponse
	if errors.As(err, &errResp) {
		return errResp.Response != nil && errResp.Response.StatusCode >= 500
	}
	return false
}

// IsTransient reports whether err looks like a transient network or upstream
// failure that may succeed on retry. Distinct from IsRateLimited: callers
// generally want to first WaitForRateReset (rate-limit handling) and then
// fall back to backoff for transient errors.
//
// Returns true for: GitHub 5xx, io.EOF / io.ErrUnexpectedEOF, net.Error
// timeouts, and well-known transient strings ("connection reset by peer",
// "broken pipe", "EOF"). Returns false for context cancellation/deadline
// (those reflect a deliberate caller decision) and for non-network errors.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	// Caller-driven cancellation/deadline must not be retried.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if IsServerError(err) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var nerr gonet.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return true
	}
	// Match common transient strings that don't always map to typed errors.
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection reset by peer"):
		return true
	case strings.Contains(msg, "broken pipe"):
		return true
	case strings.Contains(msg, "unexpected EOF"):
		return true
	case strings.Contains(msg, "TLS handshake timeout"):
		return true
	}
	return false
}

// IsRateLimited reports whether err (or any error in its chain) indicates a
// GitHub rate limit has been reached. Matches ErrRateLimited, github.RateLimitError,
// and github.AbuseRateLimitError.
func IsRateLimited(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrRateLimited) {
		return true
	}
	var rlErr *github.RateLimitError
	if errors.As(err, &rlErr) {
		return true
	}
	var abuseErr *github.AbuseRateLimitError
	return errors.As(err, &abuseErr)
}

func CheckRateLimit(ctx context.Context, resp *github.Response) error {
	if resp == nil {
		return nil
	}

	if resp.Rate.Remaining > RateLimitThreshold {
		return nil
	}

	resetAt := resp.Rate.Reset.Time
	wait := time.Until(resetAt)
	if wait <= 0 {
		return nil
	}
	if wait > maxWait {
		return fmt.Errorf("rate limit reset too far in the future (%v): %w", wait, ErrRateLimited)
	}

	jitter := time.Duration(rand.IntN(2000)) * time.Millisecond //nolint:gosec // jitter for rate limit backoff, not security-sensitive
	total := wait + jitter

	if resp.Rate.Remaining == 0 {
		slog.Warn("rate limit reached, pausing until reset",
			"reset_at", resetAt.Format(time.RFC3339),
			"wait_sec", total.Seconds(),
		)
	} else {
		slog.Warn("rate limit approaching, pausing until reset",
			"remaining", resp.Rate.Remaining,
			"reset_at", resetAt.Format(time.RFC3339),
			"wait_sec", total.Seconds(),
		)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(total):
		return nil
	}
}

// CheckTokenQuota calls the GitHub rate_limit API (free, no quota cost) and
// returns the remaining quota. Returns -1 on error (treat as usable).
func CheckTokenQuota(ctx context.Context, token string) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/rate_limit", nil)
	if err != nil {
		return -1
	}
	net.SetGitHubHeaders(req, token)

	resp, err := net.QuotaCheckClient.Do(req)
	if err != nil {
		return -1
	}
	defer resp.Body.Close()

	var rl struct {
		Resources struct {
			Core struct {
				Remaining int `json:"remaining"`
			} `json:"core"`
		} `json:"resources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return -1
	}
	return rl.Resources.Core.Remaining
}

// TokenQuota holds the limit and remaining fields from the GitHub rate_limit API.
type TokenQuota struct {
	Limit     int
	Remaining int
}

// CheckTokenQuotaFull calls the GitHub rate_limit API and returns both limit
// and remaining. Returns nil on error.
func CheckTokenQuotaFull(ctx context.Context, token string) *TokenQuota {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/rate_limit", nil)
	if err != nil {
		return nil
	}
	net.SetGitHubHeaders(req, token)

	resp, err := net.QuotaCheckClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var rl struct {
		Resources struct {
			Core struct {
				Limit     int `json:"limit"`
				Remaining int `json:"remaining"`
			} `json:"core"`
		} `json:"resources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return nil
	}
	return &TokenQuota{
		Limit:     rl.Resources.Core.Limit,
		Remaining: rl.Resources.Core.Remaining,
	}
}

// HasSufficientQuota returns true if the token has enough remaining API calls.
func HasSufficientQuota(ctx context.Context, token string) bool {
	remaining := CheckTokenQuota(ctx, token)
	return remaining < 0 || remaining >= minTokenQuota
}

// BackoffDuration returns the sleep duration for retry attempt `attempt`
// (0-indexed) using bounded exponential backoff with jitter. Capped at max.
//
//	attempt 0 -> base + jitter
//	attempt 1 -> 2*base + jitter
//	attempt 2 -> 4*base + jitter
//	... capped at max.
func BackoffDuration(attempt int, base, maxDelay time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	// Cap exponent to avoid overflow; 2^30 * base saturates well past maxDelay.
	if attempt > 30 {
		attempt = 30
	}
	d := base << attempt
	if d <= 0 || d > maxDelay {
		d = maxDelay
	}
	// Jitter: add up to 25% of base (or 250ms minimum) to break herd.
	jitterRange := int64(base / 4)
	if jitterRange < int64(250*time.Millisecond) {
		jitterRange = int64(250 * time.Millisecond)
	}
	jitter := time.Duration(rand.Int64N(jitterRange)) //nolint:gosec // jitter, not security-sensitive
	return d + jitter
}

// SleepWithContext sleeps for d, returning early with ctx.Err() if the
// context is canceled. Returns nil on full sleep.
func SleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// AbuseRetryAfter returns the retry-after duration if the error is a secondary
// (abuse) rate limit error. Returns 0 if the error is not an abuse rate limit.
func AbuseRetryAfter(err error) time.Duration {
	var abuse *github.AbuseRateLimitError
	if errors.As(err, &abuse) {
		d := abuse.GetRetryAfter()
		if d > 0 {
			return d
		}
		return 60 * time.Second
	}
	return 0
}

// WaitForRateReset checks whether err is a GitHub primary or secondary rate
// limit error. If so it sleeps until the reset time (plus jitter) and returns
// true so the caller can retry. Returns false for non-rate-limit errors or if
// the wait would exceed maxWait.
func WaitForRateReset(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}

	// Secondary (abuse) rate limit.
	if wait := AbuseRetryAfter(err); wait > 0 {
		if wait > maxWait {
			return false
		}
		slog.Warn("abuse rate limit hit, waiting",
			"wait_sec", wait.Seconds())
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
			return true
		}
	}

	// Primary rate limit (403 "API rate limit exceeded").
	var rlErr *github.RateLimitError
	if !errors.As(err, &rlErr) {
		return false
	}

	wait := time.Until(rlErr.Rate.Reset.Time)
	if wait <= 0 {
		return true // already past reset
	}
	if wait > maxWait {
		return false
	}

	jitter := time.Duration(rand.IntN(3000)) * time.Millisecond //nolint:gosec // jitter, not security-sensitive
	total := wait + jitter

	slog.Warn("rate limit hit, waiting for reset",
		"reset_at", rlErr.Rate.Reset.Time.Format(time.RFC3339),
		"wait_sec", total.Seconds())

	select {
	case <-ctx.Done():
		return false
	case <-time.After(total):
		return true
	}
}
