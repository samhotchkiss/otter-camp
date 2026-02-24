package mcp

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCircuitBreakerTransitions(t *testing.T) {
	now := time.Date(2026, 2, 24, 11, 0, 0, 0, time.UTC)
	breaker := NewCircuitBreaker(uuid.New(), CircuitBreakerConfig{
		FailureThreshold:    5,
		RecoveryTimeout:     time.Second,
		MaxConsecutiveOpens: 3,
	})

	for i := 0; i < 4; i++ {
		if err := breaker.BeforeCall(now); err != nil {
			t.Fatalf("BeforeCall pre-threshold %d: %v", i, err)
		}
		tr := breaker.RecordFailure(now.Add(time.Duration(i) * time.Millisecond))
		if tr.To != CBClosed {
			t.Fatalf("failure %d state = %s, want closed", i, tr.To)
		}
	}

	tr := breaker.RecordFailure(now.Add(5 * time.Millisecond))
	if tr.To != CBOpen {
		t.Fatalf("threshold transition state = %s, want open", tr.To)
	}

	if err := breaker.BeforeCall(now.Add(10 * time.Millisecond)); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("BeforeCall while open error = %v, want ErrCircuitOpen", err)
	}

	if err := breaker.BeginProbe(now.Add(500 * time.Millisecond)); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("BeginProbe before recovery timeout error = %v, want ErrCircuitOpen", err)
	}

	if err := breaker.BeginProbe(now.Add(1100 * time.Millisecond)); err != nil {
		t.Fatalf("BeginProbe after recovery timeout: %v", err)
	}
	if state := breaker.Snapshot(now.Add(1100 * time.Millisecond)).State; state != CBHalfOpen {
		t.Fatalf("state after BeginProbe = %s, want half-open", state)
	}

	tr = breaker.RecordSuccess(now.Add(1200 * time.Millisecond))
	if tr.To != CBClosed {
		t.Fatalf("half-open success transition = %s, want closed", tr.To)
	}
}

func TestCircuitBreakerPermanentOpenAfterMaxConsecutiveOpens(t *testing.T) {
	now := time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	breaker := NewCircuitBreaker(uuid.New(), CircuitBreakerConfig{
		FailureThreshold:    1,
		RecoveryTimeout:     time.Millisecond,
		MaxConsecutiveOpens: 3,
	})

	for i := 0; i < 3; i++ {
		tr := breaker.RecordFailure(now.Add(time.Duration(i) * time.Millisecond))
		if tr.To != CBOpen {
			t.Fatalf("open transition %d = %s, want open", i, tr.To)
		}
		if i < 2 {
			if err := breaker.BeginProbe(now.Add(time.Duration(i+1) * time.Millisecond)); err != nil {
				t.Fatalf("BeginProbe cycle %d: %v", i, err)
			}
			breaker.RecordFailure(now.Add(time.Duration(i+1) * time.Millisecond))
		}
	}

	snapshot := breaker.Snapshot(now.Add(10 * time.Millisecond))
	if !snapshot.PermanentlyOpen {
		t.Fatal("breaker should be permanently open after max consecutive opens")
	}
	if err := breaker.BeginProbe(now.Add(20 * time.Millisecond)); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("BeginProbe after permanent open error = %v, want ErrCircuitOpen", err)
	}
}
