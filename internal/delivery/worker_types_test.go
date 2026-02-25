package delivery

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestComputePushRetryDelay(t *testing.T) {
	if got := computePushRetryDelay(1); got != 5*time.Second {
		t.Fatalf("attempt 1 delay = %s, want %s", got, 5*time.Second)
	}
	if got := computePushRetryDelay(2); got != 10*time.Second {
		t.Fatalf("attempt 2 delay = %s, want %s", got, 10*time.Second)
	}
	if got := computePushRetryDelay(3); got != 20*time.Second {
		t.Fatalf("attempt 3 delay = %s, want %s", got, 20*time.Second)
	}
}

func TestIsWithinWindow(t *testing.T) {
	now := time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)
	inside := now.Add(4 * time.Minute)
	outside := now.Add(7 * time.Minute)

	if !isWithinWindow(now, &inside, 5*time.Minute) {
		t.Fatal("expected inside window to return true")
	}
	if isWithinWindow(now, &outside, 5*time.Minute) {
		t.Fatal("expected outside window to return false")
	}
}

func TestMetadataExtractionHelpers(t *testing.T) {
	mergeID := uuid.New()
	triggerTaskID := uuid.New()
	metadata, err := json.Marshal(map[string]any{
		"task_type": "deploy",
		"delivery": map[string]any{
			"commit_sha":           "abc123",
			"merge_queue_entry_id": mergeID.String(),
			"triggered_by_task_id": triggerTaskID.String(),
		},
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	if got := extractCommitSHA(metadata); got != "abc123" {
		t.Fatalf("extractCommitSHA = %q, want abc123", got)
	}
	ids := extractMergeQueueEntryIDs(metadata)
	if len(ids) != 1 || ids[0] != mergeID {
		t.Fatalf("extractMergeQueueEntryIDs = %v, want [%s]", ids, mergeID)
	}
	if got := extractTriggeredByTaskID(metadata); got != triggerTaskID {
		t.Fatalf("extractTriggeredByTaskID = %s, want %s", got, triggerTaskID)
	}
}
