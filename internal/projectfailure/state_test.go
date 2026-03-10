package projectfailure

import (
	"encoding/json"
	"testing"
	"time"
)

func TestApplyAndParsePreservesRetryEscalationFields(t *testing.T) {
	recordedAt := time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)
	settings, err := Apply(nil, State{
		Action:                   "archive",
		Source:                   "project_bootstrap",
		FailureCategory:          "bootstrap_product",
		FailureClass:             "bootstrap_runtime_failure",
		FailurePhase:             "task_tree_persisted",
		LastCheckpoint:           "task_tree_persisted",
		LastSuccessfulCheckpoint: "staffing_persisted",
		FailureReason:            "bootstrap setup failed before first-wave execution",
		SetupPersisted:           true,
		RetryBudget:              2,
		RetryAttemptCount:        2,
		FailureHistory: []FailureHistoryEntry{{
			ProjectID:                "project-123",
			RetryAttemptCount:        1,
			FailureCategory:          "bootstrap_product",
			FailureClass:             "bootstrap_runtime_failure",
			FailurePhase:             "task_tree_persisted",
			LastCheckpoint:           "task_tree_persisted",
			LastSuccessfulCheckpoint: "staffing_persisted",
			FailureReason:            "prior bootstrap failure",
			SetupPersisted:           true,
			RecordedAt:               &recordedAt,
		}},
		RecordedAt: &recordedAt,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(settings, &payload); err != nil {
		t.Fatalf("Unmarshal settings: %v", err)
	}
	if _, ok := payload["automatic_failure"]; !ok {
		t.Fatalf("automatic_failure missing from %s", string(settings))
	}

	parsed := Parse(settings)
	if parsed.LastSuccessfulCheckpoint != "staffing_persisted" {
		t.Fatalf("LastSuccessfulCheckpoint = %q, want staffing_persisted", parsed.LastSuccessfulCheckpoint)
	}
	if !parsed.SetupPersisted {
		t.Fatal("SetupPersisted = false, want true")
	}
	if parsed.RetryBudget != 2 {
		t.Fatalf("RetryBudget = %d, want 2", parsed.RetryBudget)
	}
	if parsed.RetryAttemptCount != 2 {
		t.Fatalf("RetryAttemptCount = %d, want 2", parsed.RetryAttemptCount)
	}
	if len(parsed.FailureHistory) != 1 {
		t.Fatalf("FailureHistory len = %d, want 1", len(parsed.FailureHistory))
	}
	if parsed.FailureHistory[0].ProjectID != "project-123" {
		t.Fatalf("FailureHistory[0].ProjectID = %q, want project-123", parsed.FailureHistory[0].ProjectID)
	}
	if parsed.FailureHistory[0].LastSuccessfulCheckpoint != "staffing_persisted" {
		t.Fatalf("FailureHistory[0].LastSuccessfulCheckpoint = %q, want staffing_persisted", parsed.FailureHistory[0].LastSuccessfulCheckpoint)
	}
}
