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
