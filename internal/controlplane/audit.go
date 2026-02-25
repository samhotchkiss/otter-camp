package controlplane

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditViolation struct {
	ExecutionID uuid.UUID `json:"execution_id"`
	Invariant   string    `json:"invariant"`
	Message     string    `json:"message"`
}

type AuditReport struct {
	RunID       uuid.UUID        `json:"run_id"`
	ScannedRows int              `json:"scanned_rows"`
	Violations  []AuditViolation `json:"violations"`
	ScanError   *string          `json:"scan_error,omitempty"`
}

type toolExecutionListByRunRepository interface {
	ListByRun(ctx context.Context, runID uuid.UUID) ([]ToolExecution, error)
}

type ToolExecutionAuditVerifier struct {
	executions toolExecutionListByRunRepository
}

func NewToolExecutionAuditVerifier(pool *pgxpool.Pool) *ToolExecutionAuditVerifier {
	if pool == nil {
		return &ToolExecutionAuditVerifier{}
	}
	return &ToolExecutionAuditVerifier{
		executions: NewToolExecutionRepository(pool),
	}
}

func (v *ToolExecutionAuditVerifier) Verify(ctx context.Context, runID uuid.UUID) AuditReport {
	report := AuditReport{
		RunID:      runID,
		Violations: make([]AuditViolation, 0),
	}
	if v == nil || v.executions == nil || runID == uuid.Nil {
		return report
	}

	rows, err := v.executions.ListByRun(ctx, runID)
	if err != nil {
		msg := err.Error()
		report.ScanError = &msg
		return report
	}
	report.ScannedRows = len(rows)

	for _, exec := range rows {
		toolTier := strings.ToLower(strings.TrimSpace(exec.ToolTier))
		policyDecision := strings.ToLower(strings.TrimSpace(exec.PolicyDecision))
		status := strings.ToLower(strings.TrimSpace(exec.Status))

		if toolTier == "tier1" && policyDecision != "not_checked" {
			report.addViolation(exec.ID, "policy_decision", "tier1 tool execution must use policy_decision=not_checked")
		}
		if toolTier == "tier2" && policyDecision == "not_checked" {
			report.addViolation(exec.ID, "policy_decision", "tier2 tool execution must use policy_decision allowed|denied")
		}

		if status == "in_progress" && exec.StartedAt == nil {
			report.addViolation(exec.ID, "started_at", "in_progress tool execution is missing started_at")
		}

		if !isTerminalToolExecutionStatus(status) {
			continue
		}
		if exec.CompletedAt == nil {
			report.addViolation(exec.ID, "completed_at", "terminal tool execution is missing completed_at")
			continue
		}
		if exec.StartedAt == nil {
			report.addViolation(exec.ID, "started_at", "terminal tool execution is missing started_at")
			continue
		}
		expectedDuration := int(exec.CompletedAt.Sub(*exec.StartedAt).Milliseconds())
		if expectedDuration < 0 {
			expectedDuration = 0
		}
		if exec.DurationMS == nil {
			report.addViolation(exec.ID, "duration_ms", fmt.Sprintf("duration_ms is nil; expected %d", expectedDuration))
			continue
		}
		if *exec.DurationMS != expectedDuration {
			report.addViolation(exec.ID, "duration_ms", fmt.Sprintf("duration_ms=%d expected=%d", *exec.DurationMS, expectedDuration))
		}
	}

	return report
}

func (r *AuditReport) addViolation(executionID uuid.UUID, invariant, message string) {
	r.Violations = append(r.Violations, AuditViolation{
		ExecutionID: executionID,
		Invariant:   strings.TrimSpace(invariant),
		Message:     strings.TrimSpace(message),
	})
}

func isTerminalToolExecutionStatus(status string) bool {
	switch status {
	case "completed", "failed", "policy_denied", "timed_out":
		return true
	default:
		return false
	}
}
