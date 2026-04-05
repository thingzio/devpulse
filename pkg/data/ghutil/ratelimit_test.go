package ghutil

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-github/v83/github"
	"github.com/stretchr/testify/assert"
)

func TestCheckRateLimit_Nil(t *testing.T) {
	start := time.Now()
	err := CheckRateLimit(context.Background(), nil)
	assert.NoError(t, err)
	assert.Less(t, time.Since(start), time.Second, "nil response should return immediately")
}

func TestCheckRateLimit_HighRemaining(t *testing.T) {
	resp := &github.Response{
		Rate: github.Rate{
			Remaining: 100,
			Limit:     5000,
			Reset:     github.Timestamp{Time: time.Now().Add(time.Hour)},
		},
	}
	start := time.Now()
	err := CheckRateLimit(context.Background(), resp)
	assert.NoError(t, err)
	assert.Less(t, time.Since(start), time.Second, "high remaining should not sleep")
}

func TestCheckRateLimit_ResetInPast(t *testing.T) {
	resp := &github.Response{
		Rate: github.Rate{
			Remaining: 0,
			Limit:     5000,
			Reset:     github.Timestamp{Time: time.Now().Add(-time.Hour)},
		},
	}
	start := time.Now()
	err := CheckRateLimit(context.Background(), resp)
	assert.NoError(t, err)
	assert.Less(t, time.Since(start), time.Second, "past reset should not sleep")
}

func TestWaitForRateReset(t *testing.T) {
	t.Run("nil error returns false", func(t *testing.T) {
		assert.False(t, WaitForRateReset(context.Background(), nil))
	})

	t.Run("non-rate-limit error returns false", func(t *testing.T) {
		assert.False(t, WaitForRateReset(context.Background(), errors.New("generic")))
	})

	t.Run("rate limit error with past reset returns true immediately", func(t *testing.T) {
		err := &github.RateLimitError{
			Rate: github.Rate{
				Reset: github.Timestamp{Time: time.Now().Add(-time.Second)},
			},
		}
		start := time.Now()
		assert.True(t, WaitForRateReset(context.Background(), err))
		assert.Less(t, time.Since(start), time.Second)
	})

	t.Run("rate limit error with near reset waits and returns true", func(t *testing.T) {
		err := &github.RateLimitError{
			Rate: github.Rate{
				Reset: github.Timestamp{Time: time.Now().Add(100 * time.Millisecond)},
			},
		}
		start := time.Now()
		assert.True(t, WaitForRateReset(context.Background(), err))
		assert.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond)
	})

	t.Run("rate limit error with far reset returns false", func(t *testing.T) {
		err := &github.RateLimitError{
			Rate: github.Rate{
				Reset: github.Timestamp{Time: time.Now().Add(20 * time.Minute)},
			},
		}
		start := time.Now()
		assert.False(t, WaitForRateReset(context.Background(), err))
		assert.Less(t, time.Since(start), time.Second)
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := &github.RateLimitError{
			Rate: github.Rate{
				Reset: github.Timestamp{Time: time.Now().Add(5 * time.Second)},
			},
		}
		assert.False(t, WaitForRateReset(ctx, err))
	})
}

func TestAbuseRetryAfter(t *testing.T) {
	t.Run("nil error returns zero", func(t *testing.T) {
		assert.Equal(t, time.Duration(0), AbuseRetryAfter(nil))
	})

	t.Run("non-abuse error returns zero", func(t *testing.T) {
		assert.Equal(t, time.Duration(0), AbuseRetryAfter(errors.New("generic error")))
	})

	t.Run("abuse error with retry-after returns that duration", func(t *testing.T) {
		d := 30 * time.Second
		err := &github.AbuseRateLimitError{RetryAfter: &d}
		assert.Equal(t, 30*time.Second, AbuseRetryAfter(err))
	})

	t.Run("abuse error with nil retry-after returns 60s default", func(t *testing.T) {
		err := &github.AbuseRateLimitError{RetryAfter: nil}
		assert.Equal(t, 60*time.Second, AbuseRetryAfter(err))
	})
}
