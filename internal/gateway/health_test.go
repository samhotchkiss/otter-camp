package gateway

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type mutableClock struct {
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	return c.now
}

func TestHealthCheckerTransitionsAndRecovery(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, time.February, 24, 12, 0, 0, 0, time.UTC)}
	checker := newHealthCheckerWithClock(clock.Now)
	connectionID := uuid.New()

	checker.RecordFailure(connectionID, errors.New("timeout"))
	if state := checker.GetState(connectionID); state != HealthStateHealthy {
		t.Fatalf("state after first failure = %q, want %q", state, HealthStateHealthy)
	}

	clock.now = clock.now.Add(10 * time.Second)
	checker.RecordFailure(connectionID, errors.New("timeout"))
	if state := checker.GetState(connectionID); state != HealthStateDegraded {
		t.Fatalf("state after second failure = %q, want %q", state, HealthStateDegraded)
	}

	clock.now = clock.now.Add(30 * time.Second)
	checker.RecordFailure(connectionID, ProviderHTTPError{StatusCode: 429})
	if state := checker.GetState(connectionID); state != HealthStateRateLimited {
		t.Fatalf("state after rate limit = %q, want %q", state, HealthStateRateLimited)
	}

	clock.now = clock.now.Add(5 * time.Second)
	checker.RecordFailure(connectionID, ProviderHTTPError{StatusCode: 500})
	clock.now = clock.now.Add(40 * time.Second)
	checker.RecordFailure(connectionID, ProviderHTTPError{StatusCode: 500})
	clock.now = clock.now.Add(40 * time.Second)
	checker.RecordFailure(connectionID, ProviderHTTPError{StatusCode: 500})
	if state := checker.GetState(connectionID); state != HealthStateUnavailable {
		t.Fatalf("state after 5 failures = %q, want %q", state, HealthStateUnavailable)
	}

	checker.RecordSuccess(connectionID)
	if state := checker.GetState(connectionID); state != HealthStateUnavailable {
		t.Fatalf("state before probe delay = %q, want %q", state, HealthStateUnavailable)
	}

	clock.now = clock.now.Add(2 * time.Minute)
	if state := checker.GetState(connectionID); state != HealthStateDegraded {
		t.Fatalf("state after probe delay = %q, want %q", state, HealthStateDegraded)
	}

	checker.RecordSuccess(connectionID)
	if state := checker.GetState(connectionID); state != HealthStateHealthy {
		t.Fatalf("state after recovery probe = %q, want %q", state, HealthStateHealthy)
	}
}

func TestHealthCheckerAutoRecoveryAfterBackoff(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, time.February, 24, 12, 30, 0, 0, time.UTC)}
	checker := newHealthCheckerWithClock(clock.Now)
	connectionID := uuid.New()

	checker.MarkUnavailable(connectionID)
	if state := checker.GetState(connectionID); state != HealthStateUnavailable {
		t.Fatalf("state after mark unavailable = %q, want %q", state, HealthStateUnavailable)
	}

	clock.now = clock.now.Add(500 * time.Millisecond)
	if state := checker.GetState(connectionID); state != HealthStateUnavailable {
		t.Fatalf("state before backoff elapsed = %q, want %q", state, HealthStateUnavailable)
	}

	clock.now = clock.now.Add(1 * time.Second)
	if state := checker.GetState(connectionID); state != HealthStateDegraded {
		t.Fatalf("state after backoff elapsed = %q, want %q", state, HealthStateDegraded)
	}

	checker.RecordSuccess(connectionID)
	if state := checker.GetState(connectionID); state != HealthStateHealthy {
		t.Fatalf("state after successful probe = %q, want %q", state, HealthStateHealthy)
	}
}

func TestHealthCheckerRateLimitedRecoversToDegradedAfterBackoff(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, time.February, 24, 13, 0, 0, 0, time.UTC)}
	checker := newHealthCheckerWithClock(clock.Now)
	connectionID := uuid.New()

	checker.MarkRateLimited(connectionID)
	if state := checker.GetState(connectionID); state != HealthStateRateLimited {
		t.Fatalf("state after mark rate limited = %q, want %q", state, HealthStateRateLimited)
	}

	clock.now = clock.now.Add(500 * time.Millisecond)
	if state := checker.GetState(connectionID); state != HealthStateRateLimited {
		t.Fatalf("state before rate-limit backoff elapsed = %q, want %q", state, HealthStateRateLimited)
	}

	clock.now = clock.now.Add(1 * time.Second)
	if state := checker.GetState(connectionID); state != HealthStateDegraded {
		t.Fatalf("state after rate-limit backoff elapsed = %q, want %q", state, HealthStateDegraded)
	}

	checker.RecordSuccess(connectionID)
	if state := checker.GetState(connectionID); state != HealthStateHealthy {
		t.Fatalf("state after successful recovery probe = %q, want %q", state, HealthStateHealthy)
	}
}

func TestHealthCheckerRateLimitedHonorsProviderRetryAfter(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, time.February, 24, 13, 30, 0, 0, time.UTC)}
	checker := newHealthCheckerWithClock(clock.Now)
	connectionID := uuid.New()

	checker.MarkRateLimitedFor(connectionID, 45*time.Minute)
	if state := checker.GetState(connectionID); state != HealthStateRateLimited {
		t.Fatalf("state after mark rate limited = %q, want %q", state, HealthStateRateLimited)
	}

	clock.now = clock.now.Add(44 * time.Minute)
	if state := checker.GetState(connectionID); state != HealthStateRateLimited {
		t.Fatalf("state before provider retry_after elapsed = %q, want %q", state, HealthStateRateLimited)
	}

	clock.now = clock.now.Add(1 * time.Minute)
	if state := checker.GetState(connectionID); state != HealthStateDegraded {
		t.Fatalf("state after provider retry_after elapsed = %q, want %q", state, HealthStateDegraded)
	}
}
