package httpx

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// rateLimiter is a fixed-window per-key limiter. It bounds per-instance
// request rates for sensitive endpoints (authentication). Distributed
// limiting via Redis arrives with the hardening milestone; the interface
// below is what callers depend on.
type rateLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	limit   int
	counts  map[string]*rateCount
	nowFunc func() time.Time
}

type rateCount struct {
	windowStart time.Time
	count       int
}

// NewRateLimiter creates a limiter allowing `limit` requests per window.
func NewRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		window:  window,
		limit:   limit,
		counts:  make(map[string]*rateCount),
		nowFunc: time.Now,
	}
}

// Allow reports whether the key may proceed in the current window.
func (l *rateLimiter) Allow(key string) bool {
	now := l.nowFunc()
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.counts[key]
	if !ok || now.Sub(c.windowStart) >= l.window {
		if len(l.counts) > 10_000 { // bound memory under key churn
			l.counts = make(map[string]*rateCount)
		}
		l.counts[key] = &rateCount{windowStart: now, count: 1}
		return true
	}
	if c.count >= l.limit {
		return false
	}
	c.count++
	return true
}

// RateLimit middleware rejects requests over the per-IP limit with 429.
// A trustProxy setting forwards the X-Forwarded-For decision to deployment
// configuration; disabled by default to avoid spoofing.
func RateLimit(limit int, window time.Duration, trustProxy bool) func(http.Handler) http.Handler {
	limiter := NewRateLimiter(limit, window)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr
			if trustProxy {
				if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
					ip = fwd
				}
			}
			if !limiter.Allow(ip) {
				retryAfter := int(math.Ceil(window.Seconds()))
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				Error(w, r, http.StatusTooManyRequests, "rate_limited", "too many requests — slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
