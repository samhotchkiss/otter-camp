package jobqueue

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBackoffDelay(t *testing.T) {
	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{attempts: 0, want: 1 * time.Second},
		{attempts: 1, want: 1 * time.Second},
		{attempts: 2, want: 2 * time.Second},
		{attempts: 3, want: 4 * time.Second},
		{attempts: 20, want: 5 * time.Minute},
	}

	for _, tc := range tests {
		if got := backoffDelay(tc.attempts); got != tc.want {
			t.Fatalf("backoffDelay(%d) = %s, want %s", tc.attempts, got, tc.want)
		}
	}
}

func TestBuildWorkerIDFormat(t *testing.T) {
	id := buildWorkerID()
	parts := strings.Split(id, "-")
	if len(parts) < 3 {
		t.Fatalf("worker id %q should contain hostname, pid, uuid", id)
	}
}

func TestAgentTurnRateLimitDelay(t *testing.T) {
	tests := []struct {
		name      string
		attempts  int
		retryHint time.Duration
		want      time.Duration
	}{
		{name: "first attempt minimum", attempts: 1, want: 30 * time.Second},
		{name: "second attempt minimum", attempts: 2, want: 60 * time.Second},
		{name: "third attempt minimum", attempts: 3, want: 120 * time.Second},
		{name: "fourth attempt continues exponential", attempts: 4, want: 240 * time.Second},
		{name: "retry after extends first attempt", attempts: 1, retryHint: 45 * time.Second, want: 45 * time.Second},
		{name: "retry after shorter than minimum ignored", attempts: 2, retryHint: 10 * time.Second, want: 60 * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentTurnRateLimitDelay(tc.attempts, tc.retryHint); got != tc.want {
				t.Fatalf("agentTurnRateLimitDelay(%d, %s) = %s, want %s", tc.attempts, tc.retryHint, got, tc.want)
			}
		})
	}
}

func TestRetryAttemptLimit(t *testing.T) {
	tests := []struct {
		name        string
		job         Job
		rateLimited bool
		want        int
	}{
		{
			name:        "rate-limited agent_turn raises low max attempts",
			job:         Job{JobType: "agent_turn", MaxAttempts: 3},
			rateLimited: true,
			want:        6,
		},
		{
			name:        "rate-limited agent_turn keeps high max attempts",
			job:         Job{JobType: "agent_turn", MaxAttempts: 8},
			rateLimited: true,
			want:        8,
		},
		{
			name:        "non-agent job unchanged",
			job:         Job{JobType: "not_agent_turn", MaxAttempts: 3},
			rateLimited: true,
			want:        3,
		},
		{
			name:        "non-rate-limited unchanged",
			job:         Job{JobType: "agent_turn", MaxAttempts: 3},
			rateLimited: false,
			want:        3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryAttemptLimit(tc.job, tc.rateLimited); got != tc.want {
				t.Fatalf("retryAttemptLimit(%+v, %t) = %d, want %d", tc.job, tc.rateLimited, got, tc.want)
			}
		})
	}
}

func TestClaimHeartbeatInterval(t *testing.T) {
	tests := []struct {
		name   string
		worker Worker
		job    Job
		want   time.Duration
	}{
		{
			name:   "generic jobs use configured stale threshold",
			worker: Worker{staleClaimThreshold: 90 * time.Second},
			job:    Job{JobType: "test.job"},
			want:   30 * time.Second,
		},
		{
			name:   "agent turns protect bootstrap threshold",
			worker: Worker{staleClaimThreshold: 10 * time.Minute},
			job:    Job{JobType: agentTurnJobType},
			want:   projectBootstrapStaleThreshold / 3,
		},
		{
			name:   "tiny thresholds clamp to minimum tick",
			worker: Worker{staleClaimThreshold: 9 * time.Millisecond},
			job:    Job{JobType: "test.job"},
			want:   10 * time.Millisecond,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.worker.claimHeartbeatInterval(tc.job); got != tc.want {
				t.Fatalf("claimHeartbeatInterval(%+v) = %s, want %s", tc.job, got, tc.want)
			}
		})
	}
}

func TestRateLimitRetryAfter(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantDelay   time.Duration
		wantLimited bool
	}{
		{
			name:        "typed rate-limit error with retry hint",
			err:         stubRateLimitError{retryAfter: 75 * time.Second},
			wantDelay:   75 * time.Second,
			wantLimited: true,
		},
		{
			name:        "text-only rate-limit error",
			err:         errors.New("provider RATE LIMIT exceeded"),
			wantDelay:   0,
			wantLimited: true,
		},
		{
			name:        "non-rate-limit error",
			err:         errors.New("boom"),
			wantDelay:   0,
			wantLimited: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotDelay, gotLimited := rateLimitRetryAfter(tc.err)
			if gotDelay != tc.wantDelay || gotLimited != tc.wantLimited {
				t.Fatalf("rateLimitRetryAfter(%v) = (%s, %t), want (%s, %t)", tc.err, gotDelay, gotLimited, tc.wantDelay, tc.wantLimited)
			}
		})
	}
}

func TestDeriveJobKeysForRollupUpdate(t *testing.T) {
	orgID := uuid.New()
	payload := []byte(fmt.Sprintf(`{"org_id":"%s","rollup_date":"2026-03-22"}`, orgID))

	dedupeKey, groupKey := deriveJobKeys("rollup_update", payload)
	want := "rollup_update:" + orgID.String() + ":2026-03-22"
	if dedupeKey != want {
		t.Fatalf("dedupe_key = %q, want %q", dedupeKey, want)
	}
	if groupKey != want {
		t.Fatalf("group_key = %q, want %q", groupKey, want)
	}
}

type stubRateLimitError struct {
	retryAfter time.Duration
}

func (e stubRateLimitError) Error() string {
	return "rate limited"
}

func (e stubRateLimitError) RateLimitRetryAfter() time.Duration {
	return e.retryAfter
}
