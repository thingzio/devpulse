package ghutil

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/google/go-github/v83/github"
)

const RateLimitThreshold = 10

// ErrRateLimited is returned when a GitHub API token has hit its rate limit.
// Callers with a token pool should exhaust this token and try another.
var ErrRateLimited = errors.New("github token rate limited")

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
	maxWait := 15 * time.Minute
	wait := time.Until(resetAt)
	if wait <= 0 {
		return nil
	}
	if wait > maxWait {
		return fmt.Errorf("rate limit reset too far in the future: %v", wait)
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

	const maxWait = 15 * time.Minute

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
