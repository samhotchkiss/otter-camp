package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestToolExecutionAuditVerifierFlagsMissingCompletedAt(t *testing.T) {
	runID := uuid.New()
	startedAt := time.Now().UTC()
	execution := ToolExecution{
		ID:             uuid.New(),
		RunID:          &runID,
		ToolName:       "file.read",
		ToolTier:       "tier2",
		ToolDomain:     "native",
		PolicyDecision: "allowed",
		Status:         "failed",
		StartedAt:      &startedAt,
		CompletedAt:    nil,
	}

	verifier := &ToolExecutionAuditVerifier{
		executions: fakeToolExecutionListByRunRepository{
			rows: []ToolExecution{execution},
		},
	}
	report := verifier.Verify(context.Background(), runID)
	if len(report.Violations) == 0 {
		t.Fatal("expected at least one audit violation")
	}
	found := false
	for _, violation := range report.Violations {
		if violation.ExecutionID == execution.ID && violation.Invariant == "completed_at" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected completed_at violation, got %+v", report.Violations)
	}
}

type fakeToolExecutionListByRunRepository struct {
	rows []ToolExecution
	err  error
}

func (f fakeToolExecutionListByRunRepository) ListByRun(context.Context, uuid.UUID) ([]ToolExecution, error) {
	return f.rows, f.err
}
