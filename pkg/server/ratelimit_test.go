package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimiter_Allow(t *testing.T) {
	rl := newRateLimiter(3, time.Minute)

	assert.True(t, rl.allow("10.0.0.1"), "1st request should be allowed")
	assert.True(t, rl.allow("10.0.0.1"), "2nd request should be allowed")
	assert.True(t, rl.allow("10.0.0.1"), "3rd request should be allowed")
	assert.False(t, rl.allow("10.0.0.1"), "4th request should be denied")
	assert.False(t, rl.allow("10.0.0.1"), "5th request should still be denied")
}

func TestRateLimiter_DifferentIPs(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)

	assert.True(t, rl.allow("10.0.0.1"), "IP1 first request allowed")
	assert.False(t, rl.allow("10.0.0.1"), "IP1 second request denied")
	assert.True(t, rl.allow("10.0.0.2"), "IP2 first request allowed")
	assert.False(t, rl.allow("10.0.0.2"), "IP2 second request denied")
}

func TestRateLimiter_WindowReset(t *testing.T) {
	rl := newRateLimiter(1, 50*time.Millisecond)

	assert.True(t, rl.allow("10.0.0.1"), "first request allowed")
	assert.False(t, rl.allow("10.0.0.1"), "second request denied")

	time.Sleep(60 * time.Millisecond)

	assert.True(t, rl.allow("10.0.0.1"), "request after window reset should be allowed")
}

func TestRateLimitMiddleware(t *testing.T) {
	rl := newRateLimiter(2, time.Minute)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rateLimitMiddleware(rl)(inner)

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "10.0.0.1:1234"
		handler.ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code, "request %d should succeed", i+1)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	handler.ServeHTTP(w, r)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestRateLimitMiddleware_XForwardedFor(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rateLimitMiddleware(rl)(inner)

	// First request with X-Forwarded-For header.
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodGet, "/", nil)
	r1.Header.Set("X-Forwarded-For", "203.0.113.1")
	handler.ServeHTTP(w1, r1)
	assert.Equal(t, http.StatusOK, w1.Code)

	// Second request from same forwarded IP should be denied.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.Header.Set("X-Forwarded-For", "203.0.113.1")
	handler.ServeHTTP(w2, r2)
	assert.Equal(t, http.StatusTooManyRequests, w2.Code)
}
