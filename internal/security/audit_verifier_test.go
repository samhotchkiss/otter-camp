package security

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeStateChanges struct {
	items []StateChange
}

func (f *fakeStateChanges) ListStateChanges(context.Context, uuid.UUID, time.Time) ([]StateChange, error) {
	return f.items, nil
}

type fakeAudits struct {
	present map[string]bool
}

func (f *fakeAudits) HasAuditEvent(_ context.Context, _ uuid.UUID, requestID string, _ time.Time) (bool, error) {
	return f.present[requestID], nil
}

func TestAuditVerifierVerifyCompleteness(t *testing.T) {
	orgID := uuid.New()
	now := time.Date(2026, time.February, 24, 10, 0, 0, 0, time.UTC)
	verifier := NewAuditVerifierWithSources(
		&fakeStateChanges{items: []StateChange{
			{RequestID: "req-1", OccurredAt: now},
			{RequestID: "req-2", OccurredAt: now.Add(2 * time.Second)},
		}},
		&fakeAudits{present: map[string]bool{"req-1": true}},
	)

	report := verifier.VerifyCompleteness(context.Background(), orgID, now.Add(-time.Hour))
	if report.CheckedCount != 2 {
		t.Fatalf("checked_count = %d, want 2", report.CheckedCount)
	}
	if report.GapCount != 1 {
		t.Fatalf("gap_count = %d, want 1", report.GapCount)
	}
}
