package turn

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/projectfailure"
)

const (
	projectBootstrapCheckpointProjectCreated         = "project_created"
	projectBootstrapCheckpointStaffingPersisted      = "staffing_persisted"
	projectBootstrapCheckpointTaskTreePersisted      = "task_tree_persisted"
	projectBootstrapCheckpointFlowTemplatesPersisted = "flow_templates_persisted"
	projectBootstrapCheckpointFirstWaveSelected      = "first_wave_selected"
	projectBootstrapCheckpointFirstWaveExecutions    = "first_wave_executions_created"
	projectBootstrapCheckpointFirstWaveJobsClaimed   = "first_wave_jobs_claimed"

	projectBootstrapCheckpointStatusPending   = "pending"
	projectBootstrapCheckpointStatusCompleted = "completed"
	projectBootstrapCheckpointStatusFailed    = "failed"

	projectBootstrapFailureProviderAuth      = "provider_auth_failed"
	projectBootstrapFailureProviderRateLimit = "provider_rate_limited"
	projectBootstrapFailureProviderTransient = "provider_transient_failure"
	projectBootstrapFailureRuntime           = "bootstrap_runtime_failure"

	projectBootstrapFindingCategoryExecutionShape = "execution_shape"
	projectBootstrapFindingCategoryTaskSize       = "task_size"
	projectBootstrapFindingCategoryProviderAPI    = "provider_api"
	projectBootstrapFindingCategoryRuntime        = "runtime"
	projectBootstrapFindingCategoryPrerequisite   = "runtime_prerequisite"
)

var projectBootstrapCheckpointOrder = []string{
	projectBootstrapCheckpointProjectCreated,
	projectBootstrapCheckpointStaffingPersisted,
	projectBootstrapCheckpointTaskTreePersisted,
	projectBootstrapCheckpointFlowTemplatesPersisted,
	projectBootstrapCheckpointFirstWaveSelected,
	projectBootstrapCheckpointFirstWaveExecutions,
	projectBootstrapCheckpointFirstWaveJobsClaimed,
}

type projectBootstrapCheckpoint struct {
	Name       string     `json:"name"`
	Status     string     `json:"status,omitempty"`
	RecordedAt *time.Time `json:"recorded_at,omitempty"`
	Detail     string     `json:"detail,omitempty"`
}

type projectBootstrapValidationFinding struct {
	Category string `json:"category"`
	Code     string `json:"code"`
	Summary  string `json:"summary,omitempty"`
	Phase    string `json:"phase,omitempty"`
	Blocking bool   `json:"blocking,omitempty"`
}

type projectBootstrapProjectState struct {
	Status                   string                              `json:"status,omitempty"`
	CurrentPhase             string                              `json:"current_phase,omitempty"`
	LastSuccessfulCheckpoint string                              `json:"last_successful_checkpoint,omitempty"`
	Checkpoints              []projectBootstrapCheckpoint        `json:"checkpoints,omitempty"`
	ValidationFindings       []projectBootstrapValidationFinding `json:"validation_findings,omitempty"`
	AssignmentCount          int                                 `json:"assignment_count,omitempty"`
	ProjectManagerCount      int                                 `json:"project_manager_count,omitempty"`
	WorkerCount              int                                 `json:"worker_count,omitempty"`
	ReviewerCount            int                                 `json:"reviewer_count,omitempty"`
	PlannedTaskCount         int                                 `json:"planned_task_count,omitempty"`
	PlannedFlowTemplateCount int                                 `json:"planned_flow_template_count,omitempty"`
	FirstWaveTaskCount       int                                 `json:"first_wave_task_count,omitempty"`
	FirstWaveExecutionCount  int                                 `json:"first_wave_execution_count,omitempty"`
	FirstWaveJobCount        int                                 `json:"first_wave_job_count,omitempty"`
	FailureClass             string                              `json:"failure_class,omitempty"`
	FailureReason            string                              `json:"failure_reason,omitempty"`
	UpdatedAt                *time.Time                          `json:"updated_at,omitempty"`
	CompletedAt              *time.Time                          `json:"completed_at,omitempty"`
	FailedAt                 *time.Time                          `json:"failed_at,omitempty"`
}

func projectBootstrapStateWithDerived(previous, current projectBootstrapState) projectBootstrapState {
	previous = normalizeProjectBootstrapStateCounts(previous)
	current = normalizeProjectBootstrapStateCounts(current)
	if current.StartedAt == nil {
		current.StartedAt = cloneTimePointer(previous.StartedAt)
	}
	if current.LastProgressAt == nil {
		current.LastProgressAt = cloneTimePointer(previous.LastProgressAt)
	}

	reached := projectBootstrapReachedCheckpoints(current)
	failurePhase := projectBootstrapFailurePhase(current)
	recordedAt, hasRecordedAt := projectBootstrapStateRecordedAt(current)
	previousByName := projectBootstrapCheckpointIndex(previous.Checkpoints)

	checkpoints := make([]projectBootstrapCheckpoint, 0, len(projectBootstrapCheckpointOrder))
	lastSuccessful := ""
	prerequisitesSatisfied := true
	for _, name := range projectBootstrapCheckpointOrder {
		checkpoint := projectBootstrapCheckpoint{Name: name, Status: projectBootstrapCheckpointStatusPending}
		if previousCheckpoint, ok := previousByName[name]; ok {
			checkpoint.RecordedAt = cloneTimePointer(previousCheckpoint.RecordedAt)
		}

		completed := prerequisitesSatisfied && reached[name]
		if previousCheckpoint, ok := previousByName[name]; ok &&
			prerequisitesSatisfied &&
			projectBootstrapCheckpointCarryForwardAllowed(name, current) &&
			strings.EqualFold(strings.TrimSpace(previousCheckpoint.Status), projectBootstrapCheckpointStatusCompleted) {
			completed = true
		}

		switch {
		case completed:
			checkpoint.Status = projectBootstrapCheckpointStatusCompleted
			if checkpoint.RecordedAt == nil && hasRecordedAt {
				ts := recordedAt
				checkpoint.RecordedAt = &ts
			}
			lastSuccessful = name
		case projectBootstrapStateMarkedFailed(current) && name == failurePhase:
			checkpoint.Status = projectBootstrapCheckpointStatusFailed
			checkpoint.Detail = projectBootstrapPrimaryFailureReason(current)
			if checkpoint.RecordedAt == nil && hasRecordedAt {
				ts := recordedAt
				checkpoint.RecordedAt = &ts
			}
		}

		if !completed {
			prerequisitesSatisfied = false
		}
		checkpoints = append(checkpoints, checkpoint)
	}

	current.Checkpoints = checkpoints
	current.LastSuccessfulCheckpoint = lastSuccessful
	current.CurrentPhase = projectBootstrapCurrentPhase(checkpoints, current)
	current.ValidationFindings = projectBootstrapValidationFindings(current)
	current.Status = projectBootstrapStatusFromCheckpoints(checkpoints, current)
	return current
}

func projectBootstrapCheckpointCarryForwardAllowed(name string, state projectBootstrapState) bool {
	switch strings.TrimSpace(name) {
	case projectBootstrapCheckpointStaffingPersisted:
		return projectBootstrapStaffingStateReady(state)
	default:
		return true
	}
}

func projectBootstrapCheckpointIndex(checkpoints []projectBootstrapCheckpoint) map[string]projectBootstrapCheckpoint {
	index := make(map[string]projectBootstrapCheckpoint, len(checkpoints))
	for _, checkpoint := range checkpoints {
		name := strings.TrimSpace(checkpoint.Name)
		if name == "" {
			continue
		}
		index[name] = checkpoint
	}
	return index
}

func projectBootstrapStateRecordedAt(state projectBootstrapState) (time.Time, bool) {
	switch {
	case state.CompletedAt != nil:
		return state.CompletedAt.UTC(), true
	case state.FailedAt != nil:
		return state.FailedAt.UTC(), true
	case state.UpdatedAt != nil:
		return state.UpdatedAt.UTC(), true
	case state.LastProgressAt != nil:
		return state.LastProgressAt.UTC(), true
	case state.StartedAt != nil:
		return state.StartedAt.UTC(), true
	default:
		return time.Time{}, false
	}
}

func projectBootstrapReachedCheckpoints(state projectBootstrapState) map[string]bool {
	firstWaveExecutionReached := state.FirstWaveTaskCount > 0 && state.FirstWaveExecutionCount >= state.FirstWaveTaskCount
	firstWaveJobsReached := state.FirstWaveTaskCount > 0 && state.FirstWaveJobCount >= state.FirstWaveTaskCount
	reached := map[string]bool{
		projectBootstrapCheckpointProjectCreated: strings.TrimSpace(state.InitialMessageID) != "" ||
			state.StartedAt != nil ||
			state.UpdatedAt != nil ||
			state.CompletedAt != nil ||
			state.FailedAt != nil,
		projectBootstrapCheckpointStaffingPersisted:      projectBootstrapStaffingStateReady(state),
		projectBootstrapCheckpointTaskTreePersisted:      state.PlannedTaskCount > 0,
		projectBootstrapCheckpointFlowTemplatesPersisted: state.PlannedFlowTemplateCount > 0,
		projectBootstrapCheckpointFirstWaveSelected:      state.FirstWaveTaskCount > 0,
		projectBootstrapCheckpointFirstWaveExecutions:    firstWaveExecutionReached,
		projectBootstrapCheckpointFirstWaveJobsClaimed:   firstWaveJobsReached,
	}
	return reached
}

func projectBootstrapStaffingStateReady(state projectBootstrapState) bool {
	return projectBootstrapStaffingCountsReady(
		state.ProjectManagerCount,
		state.WorkerCount,
		state.ReviewerCount,
		state.AssignmentCount,
	)
}

func projectBootstrapMissingStaffingRoles(state projectBootstrapState) []string {
	if projectBootstrapStaffingStateReady(state) {
		return nil
	}
	missing := make([]string, 0, 3)
	if state.ProjectManagerCount == 0 {
		missing = append(missing, "project_manager")
	}
	if state.WorkerCount == 0 {
		missing = append(missing, "worker")
	}
	if state.ReviewerCount == 0 {
		missing = append(missing, "reviewer")
	}
	return missing
}

func projectBootstrapPrimaryFailureClass(state projectBootstrapState) string {
	if value := strings.TrimSpace(state.ValidationFailureClass); value != "" {
		return value
	}
	return strings.TrimSpace(state.FailureClass)
}

func projectBootstrapPrimaryFailureReason(state projectBootstrapState) string {
	if value := strings.TrimSpace(state.ValidationFailureReason); value != "" {
		return value
	}
	return strings.TrimSpace(state.FailureReason)
}

func projectBootstrapStateMarkedFailed(state projectBootstrapState) bool {
	if projectBootstrapDeferredProviderFailure(state) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(state.Status), projectBootstrapStatusFailed) ||
		strings.EqualFold(strings.TrimSpace(state.ValidationStatus), projectBootstrapValidationFailed) ||
		strings.TrimSpace(state.ValidationFailureClass) != "" ||
		strings.TrimSpace(state.FailureClass) != "" ||
		state.FailedAt != nil
}

func projectBootstrapDeferredProviderFailure(state projectBootstrapState) bool {
	if state.FailedAt != nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(state.Status), projectBootstrapStatusFailed) {
		return false
	}
	switch projectBootstrapPrimaryFailureClass(state) {
	case projectBootstrapFailureProviderRateLimit, projectBootstrapFailureProviderTransient:
		return true
	default:
		return false
	}
}

func projectBootstrapFailurePhase(state projectBootstrapState) string {
	switch projectBootstrapPrimaryFailureClass(state) {
	case projectBootstrapFailureMissingAssignments:
		return projectBootstrapCheckpointStaffingPersisted
	case projectBootstrapFailureCompoundParent:
		return projectBootstrapCheckpointFirstWaveSelected
	case projectBootstrapFailureRepoBinding, projectBootstrapFailureFirstWaveFlow:
		if state.FirstWaveTaskCount > 0 || state.PlannedTaskCount > 0 {
			return projectBootstrapCheckpointFirstWaveExecutions
		}
		return projectBootstrapCheckpointFlowTemplatesPersisted
	case projectBootstrapFailureFirstWaveExecution:
		if state.FirstWaveTaskCount > 0 && state.FirstWaveExecutionCount >= state.FirstWaveTaskCount {
			return projectBootstrapCheckpointFirstWaveJobsClaimed
		}
		return projectBootstrapCheckpointFirstWaveExecutions
	case projectBootstrapFailureStalled,
		projectBootstrapFailureGuardrail,
		projectBootstrapFailureProviderAuth,
		projectBootstrapFailureProviderRateLimit,
		projectBootstrapFailureProviderTransient,
		projectBootstrapFailureRuntime:
		return projectBootstrapNextPendingCheckpoint(state)
	default:
		if projectBootstrapStateMarkedFailed(state) {
			return projectBootstrapNextPendingCheckpoint(state)
		}
		return ""
	}
}

func projectBootstrapNextPendingCheckpoint(state projectBootstrapState) string {
	reached := projectBootstrapReachedCheckpoints(state)
	for _, checkpoint := range projectBootstrapCheckpointOrder {
		if !reached[checkpoint] {
			return checkpoint
		}
	}
	if len(projectBootstrapCheckpointOrder) == 0 {
		return ""
	}
	return projectBootstrapCheckpointOrder[len(projectBootstrapCheckpointOrder)-1]
}

func projectBootstrapCurrentPhase(checkpoints []projectBootstrapCheckpoint, state projectBootstrapState) string {
	if projectBootstrapStateMarkedFailed(state) {
		if phase := projectBootstrapFailurePhase(state); phase != "" {
			return phase
		}
	}
	if !projectBootstrapReachedCheckpoints(state)[projectBootstrapCheckpointProjectCreated] {
		return ""
	}
	for _, checkpoint := range checkpoints {
		if !strings.EqualFold(strings.TrimSpace(checkpoint.Status), projectBootstrapCheckpointStatusCompleted) {
			return checkpoint.Name
		}
	}
	if len(checkpoints) == 0 {
		return ""
	}
	return checkpoints[len(checkpoints)-1].Name
}

func projectBootstrapStatusFromCheckpoints(checkpoints []projectBootstrapCheckpoint, state projectBootstrapState) string {
	if projectBootstrapStateMarkedFailed(state) {
		return projectBootstrapStatusFailed
	}
	if len(checkpoints) != 0 &&
		strings.EqualFold(strings.TrimSpace(checkpoints[len(checkpoints)-1].Status), projectBootstrapCheckpointStatusCompleted) &&
		(!state.BootstrapTaskOutstanding || strings.TrimSpace(state.BootstrapTaskID) == "") {
		return projectBootstrapStatusCompleted
	}
	if projectBootstrapReachedCheckpoints(state)[projectBootstrapCheckpointProjectCreated] {
		return projectBootstrapStatusActive
	}
	return strings.TrimSpace(state.Status)
}

func projectBootstrapValidationFindings(state projectBootstrapState) []projectBootstrapValidationFinding {
	failureClass := projectBootstrapPrimaryFailureClass(state)
	if failureClass == "" {
		return nil
	}

	finding := projectBootstrapValidationFinding{
		Code:     failureClass,
		Summary:  projectBootstrapPrimaryFailureReason(state),
		Phase:    projectBootstrapFailurePhase(state),
		Blocking: true,
	}

	switch failureClass {
	case projectBootstrapFailureMissingAssignments:
		finding.Category = projectBootstrapFindingCategoryExecutionShape
		finding.Code = "project_assignments_missing"
	case projectBootstrapFailureCompoundParent:
		summary := strings.ToLower(finding.Summary)
		if strings.Contains(summary, "entered execution") {
			finding.Category = projectBootstrapFindingCategoryExecutionShape
			finding.Code = "parent_task_executed_with_children"
		} else if strings.Contains(summary, "did not emit any executable non-bootstrap project tasks") {
			finding.Category = projectBootstrapFindingCategoryExecutionShape
			finding.Code = "first_wave_tasks_missing"
		} else {
			finding.Category = projectBootstrapFindingCategoryTaskSize
			finding.Code = "bounded_child_tasks_required"
		}
	case projectBootstrapFailureRepoBinding:
		finding.Category = projectBootstrapFindingCategoryPrerequisite
		finding.Code = "project_repo_binding_missing"
	case projectBootstrapFailureFirstWaveFlow:
		finding.Category = projectBootstrapFindingCategoryExecutionShape
		finding.Code = "first_wave_flow_invalid"
	case projectBootstrapFailureFirstWaveExecution:
		finding.Category = projectBootstrapFindingCategoryExecutionShape
		summary := strings.ToLower(finding.Summary)
		if strings.Contains(summary, "has no assigned agent") {
			finding.Code = "first_wave_assignments_missing"
		} else if state.FirstWaveTaskCount > 0 && state.FirstWaveExecutionCount >= state.FirstWaveTaskCount {
			finding.Code = "first_wave_jobs_not_claimed"
		} else {
			finding.Code = "first_wave_executions_missing"
		}
	case projectBootstrapFailureStalled:
		finding.Category = projectBootstrapFindingCategoryRuntime
		finding.Code = "bootstrap_stalled"
	case projectBootstrapFailureGuardrail:
		finding.Category = projectBootstrapFindingCategoryRuntime
		finding.Code = "prompt_guardrail_exhausted"
	case projectBootstrapFailureProviderAuth:
		finding.Category = projectBootstrapFindingCategoryProviderAPI
		finding.Code = "authentication_failed"
	case projectBootstrapFailureProviderRateLimit:
		finding.Category = projectBootstrapFindingCategoryProviderAPI
		finding.Code = "rate_limited"
	case projectBootstrapFailureProviderTransient:
		finding.Category = projectBootstrapFindingCategoryProviderAPI
		finding.Code = "transient_failure"
	case projectBootstrapFailureRuntime:
		finding.Category = projectBootstrapFindingCategoryRuntime
		finding.Code = "turn_failed"
	default:
		finding.Category = projectBootstrapFindingCategoryRuntime
	}

	return []projectBootstrapValidationFinding{finding}
}

func projectBootstrapProjectStateSnapshot(state projectBootstrapState) projectBootstrapProjectState {
	return projectBootstrapProjectState{
		Status:                   strings.TrimSpace(state.Status),
		CurrentPhase:             strings.TrimSpace(state.CurrentPhase),
		LastSuccessfulCheckpoint: strings.TrimSpace(state.LastSuccessfulCheckpoint),
		Checkpoints:              append([]projectBootstrapCheckpoint(nil), state.Checkpoints...),
		ValidationFindings:       append([]projectBootstrapValidationFinding(nil), state.ValidationFindings...),
		AssignmentCount:          state.AssignmentCount,
		ProjectManagerCount:      state.ProjectManagerCount,
		WorkerCount:              state.WorkerCount,
		ReviewerCount:            state.ReviewerCount,
		PlannedTaskCount:         state.PlannedTaskCount,
		PlannedFlowTemplateCount: state.PlannedFlowTemplateCount,
		FirstWaveTaskCount:       state.FirstWaveTaskCount,
		FirstWaveExecutionCount:  state.FirstWaveExecutionCount,
		FirstWaveJobCount:        state.FirstWaveJobCount,
		FailureClass:             strings.TrimSpace(state.FailureClass),
		FailureReason:            strings.TrimSpace(state.FailureReason),
		UpdatedAt:                cloneTimePointer(state.UpdatedAt),
		CompletedAt:              cloneTimePointer(state.CompletedAt),
		FailedAt:                 cloneTimePointer(state.FailedAt),
	}
}

func projectBootstrapProjectStateFromSettings(settings json.RawMessage) projectBootstrapProjectState {
	if len(settings) == 0 || !json.Valid(settings) {
		return projectBootstrapProjectState{}
	}
	var decoded map[string]any
	if err := json.Unmarshal(settings, &decoded); err != nil {
		return projectBootstrapProjectState{}
	}
	raw, ok := decoded[projectBootstrapMetadataKey]
	if !ok || raw == nil {
		return projectBootstrapProjectState{}
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return projectBootstrapProjectState{}
	}
	var state projectBootstrapProjectState
	if err := json.Unmarshal(payload, &state); err != nil {
		return projectBootstrapProjectState{}
	}
	return state
}

func (e *TurnEngine) updateProjectBootstrapProjectState(ctx context.Context, projectID uuid.UUID, state projectBootstrapState) error {
	if e == nil || e.pool == nil || projectID == uuid.Nil {
		return nil
	}
	payload, err := json.Marshal(projectBootstrapProjectStateSnapshot(state))
	if err != nil {
		return err
	}
	clearAutomaticFailure := shouldClearProjectBootstrapAutomaticFailure(state)
	_, err = e.pool.Exec(ctx, `
		UPDATE project
		SET settings = CASE
			WHEN $3::boolean
				AND COALESCE(settings->'automatic_failure'->>'source', '') = $4
			THEN jsonb_set(COALESCE(settings, '{}'::jsonb) - 'automatic_failure', '{project_bootstrap}', $2::jsonb, true)
			ELSE jsonb_set(COALESCE(settings, '{}'::jsonb), '{project_bootstrap}', $2::jsonb, true)
		END
		WHERE id = $1
	`, projectID, payload, clearAutomaticFailure, projectBootstrapSource)
	return err
}

func shouldClearProjectBootstrapAutomaticFailure(state projectBootstrapState) bool {
	switch strings.TrimSpace(state.Status) {
	case projectBootstrapStatusCompleted:
		return true
	case projectBootstrapStatusActive:
		return strings.TrimSpace(state.ValidationStatus) == "" &&
			strings.TrimSpace(state.ValidationFailureClass) == "" &&
			strings.TrimSpace(state.ValidationFailureReason) == "" &&
			strings.TrimSpace(state.FailureCategory) == "" &&
			strings.TrimSpace(state.FailureClass) == "" &&
			strings.TrimSpace(state.FailurePhase) == "" &&
			strings.TrimSpace(state.FailureReason) == "" &&
			strings.TrimSpace(state.ProviderFailureClass) == "" &&
			strings.TrimSpace(state.ProviderFailureReason) == ""
	default:
		return false
	}
}

func clearBootstrapAutomaticFailureState(settings json.RawMessage) (json.RawMessage, bool) {
	failure := projectfailure.Parse(settings)
	if !strings.EqualFold(strings.TrimSpace(failure.Source), projectBootstrapSource) {
		return settings, false
	}
	var payload map[string]json.RawMessage
	if len(settings) > 0 && json.Valid(settings) {
		if err := json.Unmarshal(settings, &payload); err != nil {
			return settings, false
		}
	}
	if payload == nil {
		return settings, false
	}
	if _, ok := payload["automatic_failure"]; !ok {
		return settings, false
	}
	delete(payload, "automatic_failure")
	encoded, err := json.Marshal(payload)
	if err != nil {
		return settings, false
	}
	return json.RawMessage(encoded), true
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}
