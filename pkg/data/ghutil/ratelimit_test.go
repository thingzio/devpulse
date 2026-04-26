package ghutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	gonet "net"
	"net/http"
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

func TestIsServerError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		assert.False(t, IsServerError(nil))
	})

	t.Run("generic error", func(t *testing.T) {
		assert.False(t, IsServerError(errors.New("generic")))
	})

	t.Run("github 500", func(t *testing.T) {
		err := &github.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusInternalServerError},
		}
		assert.True(t, IsServerError(err))
	})

	t.Run("github 502", func(t *testing.T) {
		err := &github.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusBadGateway},
		}
		assert.True(t, IsServerError(err))
	})

	t.Run("github 422 not server error", func(t *testing.T) {
		err := &github.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusUnprocessableEntity},
		}
		assert.False(t, IsServerError(err))
	})

	t.Run("wrapped server error", func(t *testing.T) {
		inner := &github.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusInternalServerError},
		}
		err := fmt.Errorf("listing pr comments: %w", inner)
		assert.True(t, IsServerError(err))
	})
}

func TestIsRateLimited(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		assert.False(t, IsRateLimited(nil))
	})

	t.Run("generic error", func(t *testing.T) {
		assert.False(t, IsRateLimited(errors.New("generic")))
	})

	t.Run("ErrRateLimited sentinel", func(t *testing.T) {
		assert.True(t, IsRateLimited(ErrRateLimited))
	})

	t.Run("wrapped ErrRateLimited", func(t *testing.T) {
		err := fmt.Errorf("computing deep reputation: %w", ErrRateLimited)
		assert.True(t, IsRateLimited(err))
	})

	t.Run("github.RateLimitError", func(t *testing.T) {
		err := &github.RateLimitError{}
		assert.True(t, IsRateLimited(err))
	})

	t.Run("github.AbuseRateLimitError", func(t *testing.T) {
		err := &github.AbuseRateLimitError{}
		assert.True(t, IsRateLimited(err))
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

type fakeNetTimeout struct{ msg string }

func (f *fakeNetTimeout) Error() string   { return f.msg }
func (f *fakeNetTimeout) Timeout() bool   { return true }
func (f *fakeNetTimeout) Temporary() bool { return true }

func TestIsTransient(t *testing.T) {
	t.Run("nil error is not transient", func(t *testing.T) {
		assert.False(t, IsTransient(nil))
	})

	t.Run("context canceled is not transient", func(t *testing.T) {
		assert.False(t, IsTransient(context.Canceled))
	})

	t.Run("context deadline exceeded is not transient", func(t *testing.T) {
		assert.False(t, IsTransient(context.DeadlineExceeded))
		assert.False(t, IsTransient(fmt.Errorf("wrap: %w", context.DeadlineExceeded)))
	})

	t.Run("github 5xx is transient", func(t *testing.T) {
		err := &github.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusBadGateway},
		}
		assert.True(t, IsTransient(err))
		assert.True(t, IsTransient(fmt.Errorf("wrap: %w", err)))
	})

	t.Run("github 4xx is not transient", func(t *testing.T) {
		err := &github.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusNotFound},
		}
		assert.False(t, IsTransient(err))
	})

	t.Run("io.EOF is transient", func(t *testing.T) {
		assert.True(t, IsTransient(io.EOF))
		assert.True(t, IsTransient(fmt.Errorf("wrap: %w", io.EOF)))
	})

	t.Run("io.ErrUnexpectedEOF is transient", func(t *testing.T) {
		assert.True(t, IsTransient(io.ErrUnexpectedEOF))
	})

	t.Run("net.Error timeout is transient", func(t *testing.T) {
		var ne gonet.Error = &fakeNetTimeout{msg: "i/o timeout"}
		assert.True(t, IsTransient(ne))
	})

	t.Run("connection reset is transient by string match", func(t *testing.T) {
		assert.True(t, IsTransient(errors.New("read tcp: connection reset by peer")))
	})

	t.Run("broken pipe is transient by string match", func(t *testing.T) {
		assert.True(t, IsTransient(errors.New("write tcp: broken pipe")))
	})

	t.Run("generic error is not transient", func(t *testing.T) {
		assert.False(t, IsTransient(errors.New("invalid argument")))
	})
}

func TestBackoffDuration(t *testing.T) {
	base := 100 * time.Millisecond
	maxDelay := 5 * time.Second

	t.Run("attempt 0 returns at least base", func(t *testing.T) {
		d := BackoffDuration(0, base, maxDelay)
		assert.GreaterOrEqual(t, d, base)
	})

	t.Run("delays grow with attempt", func(t *testing.T) {
		// attempt N delivers >= base * 2^N (jitter is additive, not subtractive)
		for attempt := 0; attempt < 4; attempt++ {
			d := BackoffDuration(attempt, base, maxDelay)
			expected := base << attempt
			if expected > maxDelay {
				expected = maxDelay
			}
			assert.GreaterOrEqual(t, d, expected,
				"attempt %d should be >= %v", attempt, expected)
		}
	})

	t.Run("delay is capped at max", func(t *testing.T) {
		// large attempt — exponent saturates well past max. jitter can add
		// up to max(base/4, 250ms) above the cap; verify within that bound.
		d := BackoffDuration(20, base, maxDelay)
		assert.LessOrEqual(t, d, maxDelay+500*time.Millisecond)
	})

	t.Run("negative attempt is treated as zero", func(t *testing.T) {
		d := BackoffDuration(-5, base, maxDelay)
		assert.GreaterOrEqual(t, d, base)
	})
}

func TestSleepWithContext(t *testing.T) {
	t.Run("non-positive duration returns ctx err state", func(t *testing.T) {
		err := SleepWithContext(context.Background(), 0)
		assert.NoError(t, err)
	})

	t.Run("normal sleep returns nil", func(t *testing.T) {
		start := time.Now()
		err := SleepWithContext(context.Background(), 20*time.Millisecond)
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)
	})

	t.Run("canceled context returns immediately", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		start := time.Now()
		err := SleepWithContext(ctx, time.Hour)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Less(t, time.Since(start), 100*time.Millisecond)
	})
}
