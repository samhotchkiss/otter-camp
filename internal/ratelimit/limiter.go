package ratelimit

import (
	"strings"
	"sync"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/clock"
)

type Limiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	limit    int
	window   time.Duration
	clock    clock.Clock
}

func New(limit int, window time.Duration, clk clock.Clock) *Limiter {
	if limit <= 0 {
		limit = 20
	}
	if window <= 0 {
		window = 15 * time.Minute
	}
	if clk == nil {
		clk = clock.Real{}
	}

	return &Limiter{
		attempts: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
		clock:    clk,
	}
}

func (l *Limiter) Allow(key string) bool {
	normalized := strings.TrimSpace(key)
	if normalized == "" {
		return false
	}

	now := l.clock.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	history := l.attempts[normalized]
	pruned := history[:0]
	for _, ts := range history {
		if !ts.Before(cutoff) {
			pruned = append(pruned, ts)
		}
	}

	if len(pruned) >= l.limit {
		l.attempts[normalized] = append([]time.Time(nil), pruned...)
		return false
	}

	pruned = append(pruned, now)
	l.attempts[normalized] = append([]time.Time(nil), pruned...)
	return true
}
