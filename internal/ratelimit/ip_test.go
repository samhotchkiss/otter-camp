package ratelimit

import (
	"testing"
	"time"

	oclock "github.com/samhotchkiss/otter-camp/internal/clock"
)

func TestIPLimiterAllowsWithinLimitThenBlocks(t *testing.T) {
	fakeClock := oclock.NewFake(time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC))
	limiter := NewIPLimiter(fakeClock, 3, 15*time.Minute)

	if !limiter.Allow("127.0.0.1") {
		t.Fatal("attempt 1 should be allowed")
	}
	if !limiter.Allow("127.0.0.1") {
		t.Fatal("attempt 2 should be allowed")
	}
	if !limiter.Allow("127.0.0.1") {
		t.Fatal("attempt 3 should be allowed")
	}
	if limiter.Allow("127.0.0.1") {
		t.Fatal("attempt 4 should be blocked")
	}
}

func TestIPLimiterWindowExpiry(t *testing.T) {
	fakeClock := oclock.NewFake(time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC))
	limiter := NewIPLimiter(fakeClock, 2, 15*time.Minute)

	if !limiter.Allow("10.0.0.1") || !limiter.Allow("10.0.0.1") {
		t.Fatal("first two attempts should be allowed")
	}
	if limiter.Allow("10.0.0.1") {
		t.Fatal("third attempt should be blocked before window expiry")
	}

	fakeClock.Advance(16 * time.Minute)
	if !limiter.Allow("10.0.0.1") {
		t.Fatal("attempt should be allowed after window expiry")
	}
}

func TestIPLimiterReset(t *testing.T) {
	fakeClock := oclock.NewFake(time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC))
	limiter := NewIPLimiter(fakeClock, 1, 15*time.Minute)

	if !limiter.Allow("192.168.1.1") {
		t.Fatal("first attempt should be allowed")
	}
	if limiter.Allow("192.168.1.1") {
		t.Fatal("second attempt should be blocked")
	}

	limiter.Reset("192.168.1.1")
	if !limiter.Allow("192.168.1.1") {
		t.Fatal("attempt should be allowed after reset")
	}
}
