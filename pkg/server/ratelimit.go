package server

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter implements a per-IP sliding-window token bucket.
type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     int
	window   time.Duration
}

type visitor struct {
	tokens   int
	lastSeen time.Time
}

func newRateLimiter(rate int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate,
		window:   window,
	}

	go func() {
		ticker := time.NewTicker(window * 2)
		defer ticker.Stop()
		for range ticker.C {
			rl.mu.Lock()
			for ip, v := range rl.visitors {
				if time.Since(v.lastSeen) > window*3 {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		}
	}()

	return rl
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	v, exists := rl.visitors[ip]

	if !exists || now.Sub(v.lastSeen) > rl.window {
		rl.visitors[ip] = &visitor{tokens: rl.rate - 1, lastSeen: now}
		return true
	}

	v.lastSeen = now
	if v.tokens > 0 {
		v.tokens--
		return true
	}

	return false
}

func rateLimitMiddleware(rl *rateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.Header.Get("X-Forwarded-For")
			if ip == "" {
				ip = r.RemoteAddr
			}

			if !rl.allow(ip) {
				slog.Warn("rate limit exceeded", "ip", ip, "path", r.URL.Path)
				w.Header().Set("Retry-After", "60")
				if strings.HasPrefix(r.URL.Path, "/api/") {
					writeError(w, http.StatusTooManyRequests, "please wait a moment and try again")
				} else {
					http.Redirect(w, r, "/?err=rate_limit", http.StatusSeeOther)
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
