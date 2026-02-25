package security

import (
	"testing"
	"time"
)

func TestRateLimiterAllowAndRefill(t *testing.T) {
	limiter := NewRateLimiterWithWindow(100, 100*time.Millisecond, 100)
	key := "ip:127.0.0.1"

	for i := 0; i < 100; i++ {
		if !limiter.Allow(key) {
			t.Fatalf("request %d unexpectedly denied", i+1)
		}
	}
	if limiter.Allow(key) {
		t.Fatal("101st request unexpectedly allowed")
	}

	time.Sleep(110 * time.Millisecond)
	if !limiter.Allow(key) {
		t.Fatal("expected limiter to refill")
	}
}
