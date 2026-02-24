package ratelimit

import (
	"sync"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/clock"
)

type Limiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	now      func() time.Time
	attempts map[string][]time.Time
}

func New(limit int, window time.Duration, now func() time.Time) *Limiter {
	if limit <= 0 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	if now == nil {
		now = time.Now
	}

	return &Limiter{
		limit:    limit,
		window:   window,
		now:      now,
		attempts: map[string][]time.Time{},
	}
}

func NewWithClock(limit int, window time.Duration, clk clock.Clock) *Limiter {
	if clk == nil {
		return New(limit, window, nil)
	}
	return New(limit, window, clk.Now)
}

func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now().UTC()
	existing := l.pruneLocked(key, now)
	if len(existing) >= l.limit {
		l.attempts[key] = existing
		return false
	}

	l.attempts[key] = append(existing, now)
	return true
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

func (l *Limiter) pruneLocked(key string, now time.Time) []time.Time {
	existing := l.attempts[key]
	if len(existing) == 0 {
		return nil
	}

	cutoff := now.Add(-l.window)
	pruned := existing[:0]
	for _, ts := range existing {
		if ts.After(cutoff) {
			pruned = append(pruned, ts)
		}
	}
	if len(pruned) == 0 {
		delete(l.attempts, key)
		return nil
	}
	return pruned
}
