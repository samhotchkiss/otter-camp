package ratelimit

import (
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/clock"
)

func TestLimiterAllowAndWindowExpiry(t *testing.T) {
	fakeClock := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	limiter := New(2, 15*time.Minute, fakeClock)

	if !limiter.Allow("127.0.0.1") {
		t.Fatal("first attempt should be allowed")
	}
	if !limiter.Allow("127.0.0.1") {
		t.Fatal("second attempt should be allowed")
	}
	if limiter.Allow("127.0.0.1") {
		t.Fatal("third attempt should be blocked within window")
	}

	fakeClock.Advance(16 * time.Minute)
	if !limiter.Allow("127.0.0.1") {
		t.Fatal("attempt should be allowed after window expiry")
	}
}

func TestLimiterRejectsEmptyKey(t *testing.T) {
	limiter := New(20, 15*time.Minute, clock.NewFake(time.Now()))
	if limiter.Allow("   ") {
		t.Fatal("empty key should not be allowed")
	}
}
