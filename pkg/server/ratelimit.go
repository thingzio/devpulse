package server

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter implements a per-IP fixed-window token bucket with lazy expiration.
type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     int
	window   time.Duration
	done     chan struct{}
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
		done:     make(chan struct{}),
	}

	go func() {
		ticker := time.NewTicker(window * 2)
		defer ticker.Stop()
		for {
			select {
			case <-rl.done:
				return
			case <-ticker.C:
				rl.mu.Lock()
				for ip, v := range rl.visitors {
					if time.Since(v.lastSeen) > window*3 {
						delete(rl.visitors, ip)
					}
				}
				rl.mu.Unlock()
			}
		}
	}()

	return rl
}

func (rl *rateLimiter) stop() {
	close(rl.done)
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

// clientIP extracts the client IP from the request. It uses the first entry
// in the X-Forwarded-For header (the original client IP), falling back to
// RemoteAddr. The first entry is used because Cloud Run appends the true
// client IP at the front.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first, _, ok := strings.Cut(xff, ","); ok {
			if ip := strings.TrimSpace(first); ip != "" {
				return ip
			}
		}
		return strings.TrimSpace(xff)
	}
	// RemoteAddr is "host:port" or "[v6]:port"; strip the port.
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func rateLimitMiddleware(rl *rateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)

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
