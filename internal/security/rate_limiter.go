package security

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

type RateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	rate     rate.Limit
	burst    int
}

func NewRateLimiter(requests int, burst int) *RateLimiter {
	return NewRateLimiterWithWindow(requests, time.Minute, burst)
}

func NewRateLimiterWithWindow(requests int, window time.Duration, burst int) *RateLimiter {
	if requests <= 0 {
		requests = 1
	}
	if burst <= 0 {
		burst = requests
	}
	if window <= 0 {
		window = time.Minute
	}
	return &RateLimiter{
		limiters: make(map[string]*rate.Limiter),
		rate:     rate.Every(window / time.Duration(requests)),
		burst:    burst,
	}
}

func (l *RateLimiter) Allow(key string) bool {
	trimmed := strings.TrimSpace(key)
	if l == nil || trimmed == "" {
		return true
	}

	l.mu.Lock()
	limiter, ok := l.limiters[trimmed]
	if !ok {
		limiter = rate.NewLimiter(l.rate, l.burst)
		l.limiters[trimmed] = limiter
	}
	l.mu.Unlock()

	return limiter.Allow()
}

func PerIPRateLimitMiddleware(limiter *RateLimiter, retryAfter time.Duration) func(http.Handler) http.Handler {
	if limiter == nil {
		limiter = NewRateLimiter(100, 20)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "ip:" + clientIP(r)
			if limiter.Allow(key) {
				next.ServeHTTP(w, r)
				return
			}
			writeRateLimitResponse(w, retryAfter)
		})
	}
}

func PerAPIKeyRateLimitMiddleware(limiter *RateLimiter, retryAfter time.Duration) func(http.Handler) http.Handler {
	if limiter == nil {
		limiter = NewRateLimiter(1000, 100)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := middleware.PrincipalFromContext(r.Context())
			if !ok || principal.APIKey == nil || principal.APIKey.ID.String() == "" {
				next.ServeHTTP(w, r)
				return
			}
			if limiter.Allow("key:" + principal.APIKey.ID.String()) {
				next.ServeHTTP(w, r)
				return
			}
			writeRateLimitResponse(w, retryAfter)
		})
	}
}

func writeRateLimitResponse(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(retryAfter.Seconds())
	if seconds <= 0 {
		seconds = 60
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	api.ErrorWithDetails(w, http.StatusTooManyRequests, api.ErrCodeRateLimited, "rate_limit_exceeded", map[string]any{
		"retry_after": seconds,
	})
}

func clientIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	if forwardedFor := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwardedFor != "" {
		parts := strings.Split(forwardedFor, ",")
		if len(parts) > 0 {
			if candidate := strings.TrimSpace(parts[0]); candidate != "" {
				return candidate
			}
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	if strings.TrimSpace(host) == "" {
		return "unknown"
	}
	return strings.TrimSpace(host)
}
