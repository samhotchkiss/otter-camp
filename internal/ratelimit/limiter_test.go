package ratelimit

import (
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/clock"
)

func TestLimiterBlocksAfterLimitThenResetsAfterWindow(t *testing.T) {
	start := time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(start)
	limiter := NewWithClock(2, 15*time.Minute, fakeClock)

	if !limiter.Allow("127.0.0.1") {
		t.Fatal("first attempt should be allowed")
	}
	if !limiter.Allow("127.0.0.1") {
		t.Fatal("second attempt should be allowed")
	}
	if limiter.Allow("127.0.0.1") {
		t.Fatal("third attempt should be blocked")
	}

	fakeClock.Advance(15*time.Minute + time.Second)
	if !limiter.Allow("127.0.0.1") {
		t.Fatal("attempt should be allowed after window expiry")
	}
}

func TestLimiterResetClearsState(t *testing.T) {
	limiter := New(1, 15*time.Minute, time.Now)

	if !limiter.Allow("10.0.0.1") {
		t.Fatal("first attempt should be allowed")
	}
	if limiter.Allow("10.0.0.1") {
		t.Fatal("second attempt should be blocked before reset")
	}

	limiter.Reset("10.0.0.1")
	if !limiter.Allow("10.0.0.1") {
		t.Fatal("attempt should be allowed after reset")
	}
}
