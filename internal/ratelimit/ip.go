package ratelimit

import (
	"strings"
	"sync"
	"time"

	oclock "github.com/samhotchkiss/otter-camp/internal/clock"
)

const (
	DefaultLoginWindow = 15 * time.Minute
	DefaultLoginLimit  = 20
)

type IPLimiter struct {
	mu      sync.Mutex
	clock   oclock.Clock
	limit   int
	window  time.Duration
	entries map[string][]time.Time
}

func NewIPLimiter(clock oclock.Clock, limit int, window time.Duration) *IPLimiter {
	if clock == nil {
		clock = oclock.Real{}
	}
	if limit <= 0 {
		limit = DefaultLoginLimit
	}
	if window <= 0 {
		window = DefaultLoginWindow
	}

	return &IPLimiter{
		clock:   clock,
		limit:   limit,
		window:  window,
		entries: make(map[string][]time.Time),
	}
}

// Allow records one attempt for key and reports if it is within limit.
func (l *IPLimiter) Allow(key string) bool {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return true
	}

	now := l.clock.Now().UTC()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	attempts := l.entries[trimmed]
	filtered := attempts[:0]
	for _, ts := range attempts {
		if !ts.Before(cutoff) {
			filtered = append(filtered, ts)
		}
	}

	if len(filtered) >= l.limit {
		if len(filtered) == 0 {
			delete(l.entries, trimmed)
		} else {
			l.entries[trimmed] = append([]time.Time(nil), filtered...)
		}
		return false
	}

	filtered = append(filtered, now)
	l.entries[trimmed] = append([]time.Time(nil), filtered...)
	return true
}

func (l *IPLimiter) Reset(key string) {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, trimmed)
}
