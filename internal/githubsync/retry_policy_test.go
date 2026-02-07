package githubsync

import (
	"errors"
	"fmt"
	"testing"
	"time"

	ghapi "github.com/samhotchkiss/otter-camp/internal/github"
)

func TestClassifyErrorRetryability(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantClass     string
		wantRetryable bool
	}{
		{
			name:          "rate limit error is retryable",
			err:           &ghapi.RateLimitError{StatusCode: 429},
			wantClass:     RetryClassRateLimited,
			wantRetryable: true,
		},
		{
			name:          "http 503 is retryable",
			err:           &ghapi.HTTPError{StatusCode: 503, Body: "unavailable"},
			wantClass:     RetryClassUpstream5xx,
			wantRetryable: true,
		},
		{
			name:          "http 400 is terminal",
			err:           &ghapi.HTTPError{StatusCode: 400, Body: "bad request"},
			wantClass:     RetryClassTerminal,
			wantRetryable: false,
		},
		{
			name:          "permanent wrapper is terminal",
			err:           &PermanentError{Err: errors.New("invalid payload")},
			wantClass:     RetryClassTerminal,
			wantRetryable: false,
		},
		{
			name:          "generic timeout message retries",
			err:           fmt.Errorf("request timeout while syncing"),
			wantClass:     RetryClassNetwork,
			wantRetryable: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			classification := ClassifyError(tc.err)
			if classification.Class != tc.wantClass {
				t.Fatalf("expected class %q, got %q", tc.wantClass, classification.Class)
			}
			if classification.Retryable != tc.wantRetryable {
				t.Fatalf("expected retryable=%v, got %v", tc.wantRetryable, classification.Retryable)
			}
		})
	}
}

func TestBackoffBoundsAndJitter(t *testing.T) {
	base := time.Second
	max := 30 * time.Second

	low := computeBackoff(base, max, 3, 0.2, 0)
	high := computeBackoff(base, max, 3, 0.2, 1)

	if low < 3200*time.Millisecond || low > 4*time.Second {
		t.Fatalf("expected low jitter delay in [3.2s,4s], got %s", low)
	}
	if high < 4*time.Second || high > 4800*time.Millisecond {
		t.Fatalf("expected high jitter delay in [4s,4.8s], got %s", high)
	}

	capped := computeBackoff(base, max, 20, 0.2, 0.5)
	if capped > max {
		t.Fatalf("expected capped delay <= %s, got %s", max, capped)
	}
}

func TestRetryPolicyDecideExhaustion(t *testing.T) {
	policy := DefaultRetryPolicy().WithRandom(func() float64 { return 0.5 })
	now := time.Date(2026, 2, 6, 11, 0, 0, 0, time.UTC)

	decision := policy.Decide("webhook_event", 4, &ghapi.HTTPError{StatusCode: 503}, now)
	if !decision.Retryable {
		t.Fatalf("expected retryable decision")
	}
	if !decision.Exhausted {
		t.Fatalf("expected exhausted decision at max attempt")
	}
	if decision.NextAttemptAt != nil {
		t.Fatalf("expected no next attempt when exhausted")
	}

	nextDecision := policy.Decide("webhook_event", 1, &ghapi.HTTPError{StatusCode: 503}, now)
	if nextDecision.Exhausted {
		t.Fatalf("did not expect exhaustion on first attempt")
	}
	if nextDecision.NextAttemptAt == nil {
		t.Fatalf("expected next attempt timestamp")
	}
	if nextDecision.Delay <= 0 {
		t.Fatalf("expected positive delay")
	}
}
