package jobqueue

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestClaimHeartbeatIntervalClampsAgentTurnToHeartbeatGrace(t *testing.T) {
	worker := New(nil, nil, Config{
		StaleClaimThreshold: 5 * time.Minute,
	})

	got := worker.claimHeartbeatInterval(Job{JobType: agentTurnJobType})
	want := claimedAgentTurnHeartbeatGrace / 3
	if got != want {
		t.Fatalf("claimHeartbeatInterval(agent_turn) = %v, want %v", got, want)
	}
}

func TestAgentTurnRateLimitDelayCapsProviderHintAtBackoffCap(t *testing.T) {
	got := agentTurnRateLimitDelay(1, 42*time.Hour)
	if got != agentTurnRateLimitBackoffCap {
		t.Fatalf("agentTurnRateLimitDelay(1, 42h) = %v, want %v", got, agentTurnRateLimitBackoffCap)
	}
}

func TestAgentTurnRateLimitDelayUsesProviderHintWhenBelowBackoffCap(t *testing.T) {
	hint := 10 * time.Minute
	got := agentTurnRateLimitDelay(1, hint)
	if got != hint {
		t.Fatalf("agentTurnRateLimitDelay(1, %v) = %v, want %v", hint, got, hint)
	}
}

func TestRejitteredRateLimitedRunAfterClampsOversizedRunAfter(t *testing.T) {
	now := time.Date(2026, time.March, 25, 2, 0, 0, 0, time.UTC)
	runAfter := now.Add(42 * time.Hour)
	got := rejitteredRateLimitedRunAfter(now, runAfter, uuid.Nil, uuid.Nil, 1, true)
	want := now.Add(agentTurnRateLimitBackoffCap)
	if !got.Equal(want) {
		t.Fatalf("rejitteredRateLimitedRunAfter oversized = %v, want %v", got, want)
	}
}
