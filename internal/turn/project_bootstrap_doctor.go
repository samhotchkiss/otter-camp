package turn

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/projectfailure"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/workspace"
)

const (
	projectBootstrapDoctorArtifactType = "file"
	projectBootstrapDoctorReportsDir   = ".ottercamp/reports/bootstrap"
)

type projectBootstrapDoctorClassification struct {
	Family         string
	ProductSummary string
}

func classifyProjectBootstrapDoctorFailure(state projectBootstrapState) projectBootstrapDoctorClassification {
	failureClass := strings.TrimSpace(projectBootstrapPrimaryFailureClass(state))
	switch failureClass {
	case projectBootstrapFailureProviderAuth:
		return projectBootstrapDoctorClassification{
			Family:         "provider_api_failure",
			ProductSummary: "Bootstrap stopped because the model provider rejected authentication before setup could finish.",
		}
	case projectBootstrapFailureProviderRateLimit:
		return projectBootstrapDoctorClassification{
			Family:         "provider_api_failure",
			ProductSummary: "Bootstrap stopped because the model provider rate-limited setup before it could finish.",
		}
	case projectBootstrapFailureProviderTransient:
		return projectBootstrapDoctorClassification{
			Family:         "provider_api_failure",
			ProductSummary: "Bootstrap stopped because the model provider/API failed transiently before setup could finish.",
		}
	case projectBootstrapFailureMissingAssignments:
		return projectBootstrapDoctorClassification{
			Family:         "bootstrap_state_machine_failure",
			ProductSummary: "Bootstrap never persisted the required project staffing assignments.",
		}
	case projectBootstrapFailureCompoundParent:
		return projectBootstrapDoctorClassification{
			Family:         "bootstrap_state_machine_failure",
			ProductSummary: "Bootstrap planned parent work without materializing bounded executable child tasks.",
		}
	case projectBootstrapFailureRepoBinding:
		return projectBootstrapDoctorClassification{
			Family:         "bootstrap_state_machine_failure",
			ProductSummary: "Bootstrap never persisted the repo/workspace binding needed for execution.",
		}
	case projectBootstrapFailureFirstWaveFlow:
		return projectBootstrapDoctorClassification{
			Family:         "bootstrap_state_machine_failure",
			ProductSummary: "Bootstrap selected first-wave work without runnable flow templates.",
		}
	case projectBootstrapFailureFirstWaveExecution:
		return projectBootstrapDoctorClassification{
			Family:         "bootstrap_state_machine_failure",
			ProductSummary: "Bootstrap selected first-wave work but never created runnable executions and jobs.",
		}
	case projectBootstrapFailureGuardrail:
		return projectBootstrapDoctorClassification{
			Family:         "bootstrap_state_machine_failure",
			ProductSummary: "Bootstrap exhausted its prompt/continuation guardrails before the required setup state was persisted.",
		}
	case projectBootstrapFailureStalled:
		return projectBootstrapDoctorClassification{
			Family:         "bootstrap_state_machine_failure",
			ProductSummary: "Bootstrap stalled before it could persist the next required setup checkpoint.",
		}
	case projectBootstrapFailureRuntime:
		return projectBootstrapDoctorClassification{
			Family:         "bootstrap_state_machine_failure",
			ProductSummary: "Bootstrap ended in a runtime failure before the next required setup checkpoint was persisted.",
		}
	default:
		return projectBootstrapDoctorClassification{
			Family:         "bootstrap_state_machine_failure",
			ProductSummary: "Bootstrap failed before it could complete the required setup state machine.",
		}
	}
}

func projectBootstrapDoctorRecordedAt(state projectBootstrapState, failure projectfailure.State, fallback time.Time) time.Time {
	if failure.RecordedAt != nil {
		return failure.RecordedAt.UTC()
	}
	if state.FailedAt != nil {
		return state.FailedAt.UTC()
	}
	if state.UpdatedAt != nil {
		return state.UpdatedAt.UTC()
	}
	if state.LastProgressAt != nil {
		return state.LastProgressAt.UTC()
	}
	if state.StartedAt != nil {
		return state.StartedAt.UTC()
	}
	return fallback.UTC()
}

func projectBootstrapDoctorFilename(recordedAt time.Time) string {
	return fmt.Sprintf("bootstrap-doctor-%s.md", recordedAt.UTC().Format("20060102T150405.000000000Z"))
}

func buildProjectBootstrapDoctorReport(projectRecord repo.Project, sessionID, messageID uuid.UUID, state projectBootstrapState, failure projectfailure.State, recordedAt time.Time) string {
	classification := classifyProjectBootstrapDoctorFailure(state)
	lines := []string{
		"# Bootstrap Doctor Report",
		"",
		"## Summary",
		fmt.Sprintf("- recorded_at: %s", recordedAt.UTC().Format(time.RFC3339Nano)),
		fmt.Sprintf("- project_id: %s", projectRecord.ID),
		fmt.Sprintf("- project_slug: %s", strings.TrimSpace(projectRecord.Slug)),
		fmt.Sprintf("- session_id: %s", sessionID),
		fmt.Sprintf("- message_id: %s", messageID),
		fmt.Sprintf("- diagnosis_family: %s", classification.Family),
		fmt.Sprintf("- diagnosis: %s", classification.ProductSummary),
		fmt.Sprintf("- source: %s", valueOrDash(failure.Source)),
		fmt.Sprintf("- action: %s", valueOrDash(failure.Action)),
		fmt.Sprintf("- highest_checkpoint_reached: %s", valueOrDash(strings.TrimSpace(state.LastSuccessfulCheckpoint))),
		fmt.Sprintf("- failure_phase: %s", valueOrDash(firstNonEmpty(strings.TrimSpace(state.FailurePhase), strings.TrimSpace(failure.FailurePhase)))),
		fmt.Sprintf("- last_checkpoint: %s", valueOrDash(firstNonEmpty(strings.TrimSpace(state.LastCheckpoint), strings.TrimSpace(failure.LastCheckpoint)))),
		fmt.Sprintf("- failure_category: %s", valueOrDash(firstNonEmpty(strings.TrimSpace(state.FailureCategory), strings.TrimSpace(failure.FailureCategory)))),
		fmt.Sprintf("- failure_class: %s", valueOrDash(projectBootstrapPrimaryFailureClass(state))),
		fmt.Sprintf("- failure_reason: %s", valueOrDash(projectBootstrapPrimaryFailureReason(state))),
		"",
		"## Persisted Counts",
		fmt.Sprintf("- assignments: %d", state.AssignmentCount),
		fmt.Sprintf("- templates: %d", state.PlannedFlowTemplateCount),
		fmt.Sprintf("- tasks: %d", state.PlannedTaskCount),
		fmt.Sprintf("- executable_child_tasks: %d", state.FirstWaveTaskCount),
		fmt.Sprintf("- executions: %d", state.FirstWaveExecutionCount),
		fmt.Sprintf("- runnable_jobs: %d", state.FirstWaveJobCount),
		"",
		"## Checkpoints",
	}

	for _, checkpoint := range state.Checkpoints {
		status := strings.TrimSpace(checkpoint.Status)
		if status == "" {
			status = projectBootstrapCheckpointStatusPending
		}
		line := fmt.Sprintf("- %s: %s", strings.TrimSpace(checkpoint.Name), status)
		if checkpoint.RecordedAt != nil {
			line += fmt.Sprintf(" @ %s", checkpoint.RecordedAt.UTC().Format(time.RFC3339Nano))
		}
		if detail := strings.TrimSpace(checkpoint.Detail); detail != "" {
			line += fmt.Sprintf(" (%s)", detail)
		}
		lines = append(lines, line)
	}

	lines = append(lines, "", "## Validation Findings")
	if len(state.ValidationFindings) == 0 {
		lines = append(lines, "- none")
	} else {
		for _, finding := range state.ValidationFindings {
			lines = append(lines, fmt.Sprintf(
				"- category=%s code=%s phase=%s blocking=%t summary=%s",
				valueOrDash(finding.Category),
				valueOrDash(finding.Code),
				valueOrDash(finding.Phase),
				finding.Blocking,
				valueOrDash(finding.Summary),
			))
		}
	}

	lines = append(lines, "", "## Retry History")
	lines = append(lines, fmt.Sprintf("- retry_budget: %d", failure.RetryBudget))
	lines = append(lines, fmt.Sprintf("- retry_attempt_count: %d", failure.RetryAttemptCount))
	if len(failure.FailureHistory) == 0 {
		lines = append(lines, "- history: none")
	} else {
		lines = append(lines, "- history:")
		for idx, entry := range failure.FailureHistory {
			recorded := "-"
			if entry.RecordedAt != nil {
				recorded = entry.RecordedAt.UTC().Format(time.RFC3339Nano)
			}
			lines = append(lines, fmt.Sprintf(
				"  %d. project_id=%s retry_attempt_count=%d failure_class=%s failure_phase=%s last_successful_checkpoint=%s setup_persisted=%t recorded_at=%s reason=%s",
				idx+1,
				valueOrDash(entry.ProjectID),
				entry.RetryAttemptCount,
				valueOrDash(entry.FailureClass),
				valueOrDash(entry.FailurePhase),
				valueOrDash(entry.LastSuccessfulCheckpoint),
				entry.SetupPersisted,
				recorded,
				valueOrDash(entry.FailureReason),
			))
		}
	}

	return strings.Join(lines, "\n") + "\n"
}

func valueOrDash(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "-"
	}
	return trimmed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (e *TurnEngine) emitProjectBootstrapDoctorReport(ctx context.Context, projectID, sessionID, messageID uuid.UUID, state projectBootstrapState) error {
	if e == nil || e.pool == nil || projectID == uuid.Nil {
		return nil
	}

	projectRecord, err := e.projects.GetByID(ctx, projectID)
	if err != nil {
		return err
	}
	failureState := projectfailure.Parse(projectRecord.Settings)
	recordedAt := projectBootstrapDoctorRecordedAt(state, failureState, e.now())
	reportBody := buildProjectBootstrapDoctorReport(projectRecord, sessionID, messageID, state, failureState, recordedAt)

	projectRoot, err := workspace.ProjectRoot(e.dataDir, projectRecord.Slug)
	if err != nil {
		return err
	}
	reportDir := filepath.Join(projectRoot, filepath.FromSlash(projectBootstrapDoctorReportsDir))
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		return err
	}
	filename := projectBootstrapDoctorFilename(recordedAt)
	reportPath := filepath.Join(reportDir, filename)
	if err := os.WriteFile(reportPath, []byte(reportBody), 0o644); err != nil {
		return err
	}

	if sessionID == uuid.Nil || messageID == uuid.Nil {
		return nil
	}

	relPath := filepath.ToSlash(filepath.Join(projectBootstrapDoctorReportsDir, filename))
	byteSize := int64(len(reportBody))
	contentType := "text/markdown"
	storageKey := relPath
	_, err = repo.NewChatArtifactRepo(e.pool).Create(ctx, repo.ChatArtifact{
		SessionID:    sessionID,
		MessageID:    messageID,
		ArtifactType: projectBootstrapDoctorArtifactType,
		Filename:     stringPtr(filename),
		ContentType:  &contentType,
		StorageKey:   &storageKey,
		ByteSize:     &byteSize,
	})
	return err
}
