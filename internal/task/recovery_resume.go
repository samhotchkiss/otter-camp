package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
	"github.com/samhotchkiss/otter-camp/internal/workspace"
)

const (
	// Historical metadata value retained to keep existing wakeup and recovery-turn
	// handling compatible while the resume surface broadens beyond validation loops.
	RecoveryActionResumeBlockedTask = "resume_validation_blocked_task"

	RecoveryBlockerClassNotBlocked                               = "not_blocked"
	RecoveryBlockerClassFlowRejectionMaxVisits                   = "flow_rejection_max_visits"
	RecoveryBlockerClassValidationLoop                           = "deterministic_validation_loop"
	RecoveryBlockerClassDurableRecoveryCheckpoint                = taskcheckpoint.RecoveryFileWriteBlockerClassDurableCheckpoint
	RecoveryBlockerClassRepeatedNonSubstantiveRecoveryCheckpoint = taskcheckpoint.RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint
	RecoveryBlockerClassBlockedWithoutResumableState             = "blocked_without_resumable_state"

	recoveryArtifactPathPrefix = ".ottercamp/recovery/"

	reviewDecisionRequiredFailureReason = "review turn completed without calling flow.review_decision"
)

type TaskResumeBlockedStateError struct {
	BlockerClass  string
	BlockerReason string
}

func (e TaskResumeBlockedStateError) Error() string {
	blockerClass := strings.TrimSpace(e.BlockerClass)
	if blockerClass == "" {
		blockerClass = RecoveryBlockerClassBlockedWithoutResumableState
	}
	blockerReason := strings.TrimSpace(e.BlockerReason)
	if blockerReason == "" {
		return fmt.Sprintf("task blocker_class=%q is not resumable via /v1/tasks/{id}/resume", blockerClass)
	}
	return fmt.Sprintf("task blocker_class=%q is not resumable via /v1/tasks/{id}/resume: %s", blockerClass, blockerReason)
}

func (e TaskResumeBlockedStateError) Code() string {
	return TaskResumeErrorCodeForBlockerClass(e.BlockerClass)
}

func TaskResumeErrorCodeForBlockerClass(blockerClass string) string {
	normalized := normalizeTaskResumeCodeComponent(blockerClass)
	if normalized == "" {
		normalized = normalizeTaskResumeCodeComponent(RecoveryBlockerClassBlockedWithoutResumableState)
	}
	return "task_resume_" + normalized
}

func IsRecoveryResumeAction(action string) bool {
	return strings.EqualFold(strings.TrimSpace(action), RecoveryActionResumeBlockedTask)
}

type taskResumeDecision struct {
	resumable            bool
	blockerClass         string
	blockerReason        string
	clearValidationGuard bool
	validationGuard      *ValidationGuardState
	checkpoint           *taskcheckpoint.RecoveryFileWriteCheckpoint
	retryFlowNodeID      *uuid.UUID
}

func classifyTaskResumeDecision(taskRecord repo.ProjectTask, blockerReason string) taskResumeDecision {
	if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "blocked") {
		return taskResumeDecision{blockerClass: RecoveryBlockerClassNotBlocked}
	}
	blockerReason = strings.TrimSpace(blockerReason)
	if recoveryResumeReasonMatchesTerminalMalformedChild(blockerReason) {
		return taskResumeDecision{
			blockerClass:  RecoveryBlockerClassBlockedWithoutResumableState,
			blockerReason: blockerReason,
		}
	}
	if guard, ok := ParseValidationGuard(taskRecord.Metadata); ok && guard.Blocked {
		guardCopy := guard
		return taskResumeDecision{
			resumable:            true,
			blockerClass:         RecoveryBlockerClassValidationLoop,
			blockerReason:        blockerReason,
			clearValidationGuard: true,
			validationGuard:      &guardCopy,
		}
	}
	if recoveryResumeReasonMatchesReviewDecisionRequired(blockerReason) {
		syntheticGuard := ValidationGuardState{
			Fingerprint:        "flow.review_decision:required",
			AttemptFingerprint: "flow.review_decision:required:attempt",
			ToolName:           "flow.review_decision",
			FailureClass:       "tool_validation",
			FailureCode:        "review_decision_required",
			FailureReason:      reviewDecisionRequiredFailureReason,
			Count:              1,
			BlockThreshold:     1,
			Blocked:            true,
		}
		return taskResumeDecision{
			resumable:            true,
			blockerClass:         RecoveryBlockerClassValidationLoop,
			blockerReason:        blockerReason,
			clearValidationGuard: true,
			validationGuard:      &syntheticGuard,
		}
	}
	if checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata); ok && hasDurableRecoveryCheckpoint(checkpoint) {
		checkpointCopy := checkpoint
		blockerClass := taskcheckpoint.RecoveryFileWriteBlockerClass(&checkpointCopy)
		if blockerClass == "" {
			blockerClass = RecoveryBlockerClassDurableRecoveryCheckpoint
		}
		return taskResumeDecision{
			resumable:     true,
			blockerClass:  blockerClass,
			blockerReason: blockerReason,
			checkpoint:    &checkpointCopy,
		}
	}
	if strings.EqualFold(blockerReason, "flow rejection max visits exceeded") {
		return taskResumeDecision{
			blockerClass:  RecoveryBlockerClassFlowRejectionMaxVisits,
			blockerReason: blockerReason,
		}
	}
	return taskResumeDecision{
		blockerClass:  RecoveryBlockerClassBlockedWithoutResumableState,
		blockerReason: blockerReason,
	}
}

func (s *service) maybeResumeLegacyNonSubstantiveFlowRejection(ctx context.Context, taskRecord repo.ProjectTask, blockerReason string) (taskResumeDecision, bool, error) {
	if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "blocked") {
		return taskResumeDecision{}, false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(blockerReason), "flow rejection max visits exceeded") {
		return taskResumeDecision{}, false, nil
	}
	explicitFlowBlock, err := s.hasExplicitFlowRejectionMaxVisitsBlock(ctx, taskRecord.ID)
	if err != nil {
		return taskResumeDecision{}, false, err
	}
	if explicitFlowBlock {
		return taskResumeDecision{}, false, nil
	}
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata)
	if !ok || !hasDurableRecoveryCheckpoint(checkpoint) {
		return taskResumeDecision{}, false, nil
	}
	targetPath := strings.TrimSpace(checkpoint.TargetPath)
	if targetPath == "" {
		return taskResumeDecision{}, false, nil
	}
	roots, err := s.recoveryWorkspaceRoots(ctx, taskRecord)
	if err != nil {
		return taskResumeDecision{}, false, err
	}
	targetBody, exists, err := readRecoveryWorkspaceFile(roots, targetPath)
	if err != nil {
		return taskResumeDecision{}, false, err
	}
	missingTargetAfterReportedWrite := looksLikeMissingTargetAfterReportedRecoveryWrite(blockerReason, checkpoint.FailureReason)
	if exists {
		if !missingTargetAfterReportedWrite &&
			!looksLikeLegacyNonSubstantiveExecutionResultsScaffold(targetPath, targetBody) &&
			!looksLikeReviewerAssessmentDeliverablePollution(targetPath, targetBody) {
			return taskResumeDecision{}, false, nil
		}
	} else {
		artifactPath := strings.TrimSpace(checkpoint.ArtifactPath)
		if !missingTargetAfterReportedWrite || artifactPath == "" {
			return taskResumeDecision{}, false, nil
		}
		if _, artifactExists, readErr := readRecoveryWorkspaceFile(roots, artifactPath); readErr != nil {
			return taskResumeDecision{}, false, readErr
		} else if !artifactExists {
			return taskResumeDecision{}, false, nil
		}
	}
	checkpoint = taskcheckpoint.NormalizeRecoveryFileWriteCheckpoint(checkpoint)
	decision := taskResumeDecision{
		resumable:     true,
		blockerClass:  RecoveryBlockerClassDurableRecoveryCheckpoint,
		blockerReason: blockerReason,
		checkpoint:    &checkpoint,
	}
	if exists && looksLikeReviewerAssessmentDeliverablePollution(targetPath, targetBody) {
		if retryNodeID, ok := s.reviewRejectRetryNodeID(ctx, taskRecord); ok {
			decision.retryFlowNodeID = &retryNodeID
		}
	}
	return decision, true, nil
}

func (s *service) hasExplicitFlowRejectionMaxVisitsBlock(ctx context.Context, taskID uuid.UUID) (bool, error) {
	if s == nil || s.pool == nil || taskID == uuid.Nil {
		return false, nil
	}

	var explicit bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM project_task_event
			WHERE task_id = $1
			  AND (
				(event_type = 'status.changed'
					AND COALESCE(payload->>'to_status', '') = 'blocked'
					AND (
						COALESCE((payload->>'flow_rejection_max_visits')::boolean, false)
						OR COALESCE((payload->>'flow_rejection_blocked_task')::boolean, false)
					))
				OR
				(event_type = 'flow.rejected'
					AND (
						COALESCE((payload->>'max_visits_exceeded')::boolean, false)
						OR LOWER(COALESCE(NULLIF(TRIM(BOTH FROM payload->>'blocked_reason'), ''), '')) = 'flow rejection max visits exceeded'
					))
			  )
		)
	`, taskID).Scan(&explicit)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return explicit, nil
}

func (s *service) reviewRejectRetryNodeID(ctx context.Context, taskRecord repo.ProjectTask) (uuid.UUID, bool) {
	if s == nil || s.flowNodes == nil || taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID == uuid.Nil {
		return uuid.Nil, false
	}
	currentNode, err := s.flowNodes.GetByID(ctx, *taskRecord.CurrentFlowNodeID)
	if err != nil {
		return uuid.Nil, false
	}
	if !strings.EqualFold(strings.TrimSpace(currentNode.NodeType), "review") && !currentNode.RequiresHumanReview {
		return uuid.Nil, false
	}
	if currentNode.RejectNodeID != nil && *currentNode.RejectNodeID != uuid.Nil {
		return *currentNode.RejectNodeID, true
	}
	nodes, err := s.flowNodes.GetByTemplateOrdered(ctx, currentNode.FlowTemplateID)
	if err != nil {
		return uuid.Nil, false
	}
	for i, candidate := range nodes {
		if candidate.ID != currentNode.ID {
			continue
		}
		if i == 0 {
			return uuid.Nil, false
		}
		return nodes[i-1].ID, true
	}
	return uuid.Nil, false
}

func looksLikeReviewerAssessmentDeliverablePollution(targetPath, content string) bool {
	path := strings.ToLower(strings.TrimSpace(filepath.ToSlash(targetPath)))
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(path, "planning/") ||
		strings.HasPrefix(path, "review/") ||
		strings.HasPrefix(path, "reviews/") ||
		strings.HasPrefix(path, ".ottercamp/review/") ||
		strings.HasPrefix(path, ".ottercamp/reviews/") {
		return false
	}
	if !strings.HasPrefix(path, "work/") &&
		!strings.HasPrefix(path, "test/") {
		return false
	}
	if !containsAnyTaskResumeFragments(lower,
		"i have a complete picture",
		"the evidence is clear",
		"here is my review assessment",
		"here is my assessment",
		"the evidence is conclusive",
		"review findings for oc-",
		"review complete",
		"task now blocked",
		"root cause summary for pm escalation",
		"review assessment for oc-",
		"rejected — task is now blocked",
		"rejected - task is now blocked",
		"the rejection has been recorded",
		"summary of why approval was not possible",
		"pm escalation recommended",
		"review cycles exhausted",
		"this does not pass review",
		"rejecting again",
		"cannot be approved",
		"prior reviewer",
		"review cycle #",
		"reviewer's prior rejection assessment",
		"reviewer rejection summary",
	) {
		return false
	}
	return containsAnyTaskResumeFragments(lower,
		"**findings:**",
		"core deliverable missing",
		"work file contains only prior review rejection text",
		"work file was overwritten with review commentary",
		"planning artifacts remain unpopulated scaffolds",
		"planning artifacts are all bare scaffolds",
		"there is nothing to approve",
		"no deliverable exists",
		"no deliverable produced",
		"work file is self-referential review text",
		"all four planning artifacts are empty scaffolds",
		"12 work iterations",
		"10+ rejections",
		"no progress",
		"systemic failure",
		"terminal loop",
		"no further reject-rework loops are permitted",
		"the task is now blocked because",
		"rejecting.",
		"worker was stuck in a loop",
		"further rejection cycles are unlikely",
		"work file content |",
		"core deliverable (",
	)
}

func looksLikeMissingTargetAfterReportedRecoveryWrite(blockerReason, failureReason string) bool {
	combined := strings.ToLower(strings.TrimSpace(blockerReason) + "\n" + strings.TrimSpace(failureReason))
	return strings.Contains(combined, "reported success") &&
		strings.Contains(combined, "not found on disk")
}

func recoveryResumeReasonMatchesReviewDecisionRequired(blockerReason string) bool {
	normalized := strings.TrimSpace(blockerReason)
	if normalized == "" {
		return false
	}
	if strings.EqualFold(normalized, reviewDecisionRequiredFailureReason) {
		return true
	}
	prefix := reviewDecisionRequiredFailureReason + ":"
	return strings.HasPrefix(strings.ToLower(normalized), strings.ToLower(prefix))
}

func recoveryResumeReasonMatchesTerminalMalformedChild(blockerReason string) bool {
	normalized := strings.ToLower(strings.TrimSpace(blockerReason))
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "parent explicitly forbids decomposition") &&
		strings.Contains(normalized, "malformed child lane") {
		return true
	}
	return strings.Contains(normalized, "procedural child of") &&
		strings.Contains(normalized, "instead of this malformed lane")
}

func RecoveryCheckpointRequiresManualResolution(blockerReason string) bool {
	normalized := strings.ToLower(strings.TrimSpace(blockerReason))
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, "re-queue only after resolving that failure") ||
		strings.Contains(normalized, "resolve that write failure before re-queueing") ||
		strings.Contains(normalized, "re-queue only after concrete content exists")
}

func hasDurableRecoveryCheckpoint(checkpoint taskcheckpoint.RecoveryFileWriteCheckpoint) bool {
	checkpoint = taskcheckpoint.NormalizeRecoveryFileWriteCheckpoint(checkpoint)
	return taskcheckpoint.RecoveryFileWriteBlockerClass(&checkpoint) != "" ||
		strings.TrimSpace(checkpoint.TargetPath) != "" ||
		strings.TrimSpace(checkpoint.ArtifactPath) != "" ||
		strings.TrimSpace(checkpoint.FailureReason) != "" ||
		len(checkpoint.PriorFailureReasons) != 0 ||
		strings.TrimSpace(checkpoint.HistoryStartMessageID) != "" ||
		strings.TrimSpace(checkpoint.HaltTurnID) != ""
}

func looksLikeLegacyNonSubstantiveExecutionResultsScaffold(targetPath, content string) bool {
	path := strings.ToLower(strings.TrimSpace(filepath.ToSlash(targetPath)))
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(path, "planning/") ||
		strings.HasPrefix(path, "review/") ||
		strings.HasPrefix(path, "reviews/") ||
		strings.HasPrefix(path, ".ottercamp/review/") ||
		strings.HasPrefix(path, ".ottercamp/reviews/") {
		return false
	}
	if !strings.HasPrefix(path, "work/") &&
		!strings.Contains(path, "results") &&
		!strings.Contains(path, "validation") &&
		!strings.Contains(path, "report") {
		return false
	}
	if !containsAllTaskResumeFragments(lower,
		"## validation criteria",
		"## evidence expectations",
		"- define explicit pass/fail checks for each relevant stage.",
		"- note the required evidence or observable outputs for each check.",
		"- call out key failure conditions or edge cases reviewers should expect to verify.",
		"- reference the concrete files, logs, screenshots, or outputs that should exist when the work is complete.",
	) {
		return false
	}
	return !containsAnyTaskResumeFragments(lower,
		"## execution results",
		"## observed results",
		"## findings",
		"## evidence collected",
		"## test cases",
		"- observed:",
		"- result:",
		"pass/fail decision",
		"evidence file:",
		"evidence path:",
	)
}

func containsAllTaskResumeFragments(content string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(content, strings.ToLower(strings.TrimSpace(fragment))) {
			return false
		}
	}
	return true
}

func containsAnyTaskResumeFragments(content string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(content, strings.ToLower(strings.TrimSpace(fragment))) {
			return true
		}
	}
	return false
}

func (s *service) maybeRepairDurableRecoveryCheckpoint(ctx context.Context, taskRecord repo.ProjectTask, blockerReason string) (repo.ProjectTask, bool, error) {
	if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "blocked") {
		return taskRecord, false, nil
	}
	if checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata); ok && hasDurableRecoveryCheckpoint(checkpoint) {
		return taskRecord, false, nil
	}

	checkpoint, ok, err := s.rebuildDurableRecoveryCheckpointFromWorkspace(ctx, taskRecord, blockerReason)
	if err != nil {
		return taskRecord, false, err
	}
	if !ok {
		checkpoint, ok, err = s.rebuildDurableRecoveryCheckpointFromSessionState(ctx, taskRecord, blockerReason)
	}
	if err != nil || !ok {
		return taskRecord, false, err
	}

	merged, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(taskRecord.Metadata, checkpoint)
	if err != nil {
		return taskRecord, false, err
	}
	taskRecord.Metadata = merged
	updated, err := s.updateTaskMetadata(ctx, taskRecord)
	if err != nil {
		return taskRecord, false, err
	}
	return updated, true, nil
}

func (s *service) rebuildDurableRecoveryCheckpointFromWorkspace(ctx context.Context, taskRecord repo.ProjectTask, blockerReason string) (taskcheckpoint.RecoveryFileWriteCheckpoint, bool, error) {
	artifactPath, ok := recoveryArtifactPathFromBlockerReason(blockerReason)
	if !ok {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false, nil
	}
	targetPath, ok := recoveryTargetPathFromArtifact(artifactPath)
	if !ok {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false, nil
	}

	roots, err := s.recoveryWorkspaceRoots(ctx, taskRecord)
	if err != nil {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false, err
	}
	artifactDocument, artifactExists, err := readRecoveryWorkspaceFile(roots, artifactPath)
	if err != nil {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false, err
	}
	if !artifactExists {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false, nil
	}
	targetExists, err := recoveryWorkspaceFileExists(roots, targetPath)
	if err != nil {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false, err
	}
	if !targetExists {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false, nil
	}

	updatedAt := time.Now().UTC()
	if s != nil && s.clock != nil {
		updatedAt = s.clock.Now().UTC()
	}
	return taskcheckpoint.NormalizeRecoveryFileWriteCheckpoint(taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:    targetPath,
		ArtifactPath:  artifactPath,
		FailureReason: recoveryArtifactFailureReason(artifactDocument),
		UpdatedAt:     updatedAt.Format(time.RFC3339Nano),
	}), true, nil
}

func (s *service) rebuildDurableRecoveryCheckpointFromSessionState(ctx context.Context, taskRecord repo.ProjectTask, blockerReason string) (taskcheckpoint.RecoveryFileWriteCheckpoint, bool, error) {
	if s == nil || s.pool == nil {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false, nil
	}

	sessionID, ok, err := s.latestTaskRecoverySessionID(ctx, taskRecord.ID)
	if err != nil || !ok {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false, err
	}

	messages, err := repo.NewChatMessageRepo(s.pool).ListBySession(ctx, sessionID)
	if err != nil {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false, err
	}

	checkpoint, ok := recoveryCheckpointFromSessionMessages(messages, blockerReason)
	if !ok {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false, nil
	}

	roots, err := s.recoveryWorkspaceRoots(ctx, taskRecord)
	if err != nil {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false, err
	}
	if strings.TrimSpace(checkpoint.ArtifactPath) == "" && strings.TrimSpace(checkpoint.TargetPath) != "" {
		candidateArtifact := filepath.ToSlash(filepath.Join(strings.TrimSuffix(recoveryArtifactPathPrefix, "/"), filepath.FromSlash(checkpoint.TargetPath)))
		exists, existsErr := recoveryWorkspaceFileExists(roots, candidateArtifact)
		if existsErr != nil {
			return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false, existsErr
		}
		if exists {
			checkpoint.ArtifactPath = candidateArtifact
		}
	}
	if strings.TrimSpace(checkpoint.FailureReason) == "" && strings.TrimSpace(checkpoint.ArtifactPath) != "" {
		artifactDocument, artifactExists, readErr := readRecoveryWorkspaceFile(roots, checkpoint.ArtifactPath)
		if readErr != nil {
			return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false, readErr
		}
		if artifactExists {
			checkpoint.FailureReason = recoveryArtifactFailureReason(artifactDocument)
		}
	}

	updatedAt := time.Now().UTC()
	if s.clock != nil {
		updatedAt = s.clock.Now().UTC()
	}
	checkpoint.UpdatedAt = updatedAt.Format(time.RFC3339Nano)
	checkpoint = taskcheckpoint.NormalizeRecoveryFileWriteCheckpoint(checkpoint)
	if !hasDurableRecoveryCheckpoint(checkpoint) {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false, nil
	}
	return checkpoint, true, nil
}

func (s *service) latestTaskRecoverySessionID(ctx context.Context, taskID uuid.UUID) (uuid.UUID, bool, error) {
	if s == nil || s.pool == nil || taskID == uuid.Nil {
		return uuid.Nil, false, nil
	}

	var sessionID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id
		FROM chat_session
		WHERE scope_type = 'project_task'
		  AND scope_id = $1
		  AND mode = 'async'
		ORDER BY CASE WHEN status = 'active' THEN 0 ELSE 1 END, created_at DESC, id DESC
		LIMIT 1
	`, taskID).Scan(&sessionID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	return sessionID, sessionID != uuid.Nil, nil
}

func recoveryCheckpointFromSessionMessages(messages []repo.ChatMessage, blockerReason string) (taskcheckpoint.RecoveryFileWriteCheckpoint, bool) {
	checkpoint := taskcheckpoint.RecoveryFileWriteCheckpoint{}
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			continue
		}
		candidate, ok := recoveryCheckpointFromMessageMetadata(message.Metadata, blockerReason)
		if !ok {
			continue
		}
		checkpoint = candidate
		checkpoint.HistoryStartMessageID = message.ID.String()
		break
	}
	if strings.TrimSpace(checkpoint.TargetPath) == "" {
		if targetPath, ok := recoveryCheckpointTargetPathFromMessages(messages); ok {
			checkpoint.TargetPath = targetPath
		}
	}
	if strings.TrimSpace(checkpoint.TargetPath) == "" {
		if targetPath, ok := recoveryTargetPathFromArtifact(checkpoint.ArtifactPath); ok {
			checkpoint.TargetPath = targetPath
		}
	}
	if strings.TrimSpace(checkpoint.TargetPath) == "" && strings.TrimSpace(checkpoint.ArtifactPath) == "" {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false
	}
	if strings.TrimSpace(checkpoint.FailureReason) == "" {
		checkpoint.FailureReason = strings.TrimSpace(blockerReason)
	}
	if !hasDurableRecoveryCheckpoint(checkpoint) {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false
	}
	checkpoint = taskcheckpoint.NormalizeRecoveryFileWriteCheckpoint(checkpoint)
	return checkpoint, true
}

func recoveryCheckpointFromMessageMetadata(metadata json.RawMessage, blockerReason string) (taskcheckpoint.RecoveryFileWriteCheckpoint, bool) {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil || payload == nil {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false
	}
	if !IsRecoveryResumeAction(anyString(payload["recovery_action"])) {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false
	}

	checkpoint := taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:          strings.TrimSpace(anyString(payload["recovery_checkpoint_target_path"])),
		ArtifactPath:        strings.TrimSpace(anyString(payload["recovery_checkpoint_artifact_path"])),
		BlockerClass:        strings.TrimSpace(anyString(payload["recovery_blocker_class"])),
		FailureReason:       strings.TrimSpace(anyString(payload["recovery_checkpoint_failure_reason"])),
		PriorFailureReasons: anyStrings(payload["recovery_checkpoint_prior_failure_reasons"]),
	}
	if strings.TrimSpace(checkpoint.TargetPath) == "" {
		if targetPath, ok := recoveryTargetPathFromArtifact(checkpoint.ArtifactPath); ok {
			checkpoint.TargetPath = targetPath
		}
	}
	if strings.TrimSpace(checkpoint.TargetPath) == "" && strings.TrimSpace(checkpoint.ArtifactPath) == "" {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false
	}
	if strings.TrimSpace(checkpoint.FailureReason) == "" {
		checkpoint.FailureReason = strings.TrimSpace(blockerReason)
	}
	if !hasDurableRecoveryCheckpoint(checkpoint) {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false
	}
	checkpoint = taskcheckpoint.NormalizeRecoveryFileWriteCheckpoint(checkpoint)
	return checkpoint, true
}

func recoveryCheckpointTargetPathFromMessages(messages []repo.ChatMessage) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if !strings.EqualFold(strings.TrimSpace(message.Role), "tool_result") {
			continue
		}
		toolName, output, errText, ok := parseToolResultMessage(message.Content)
		if !ok || strings.TrimSpace(errText) != "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(toolName)) {
		case "file.read", "file.write", "cli.execute":
		default:
			continue
		}
		targetPath, _ := recoveryTargetPathFromToolOutput(output)
		if strings.TrimSpace(targetPath) != "" {
			return targetPath, true
		}
	}
	return "", false
}

func parseToolResultMessage(content string) (string, map[string]any, string, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", nil, "", false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil || payload == nil {
		return "", nil, "", false
	}
	toolName := strings.TrimSpace(anyString(payload["tool_name"]))
	if toolName == "" {
		return "", nil, "", false
	}
	output, _ := payload["output"].(map[string]any)
	errText := strings.TrimSpace(anyString(payload["error"]))
	return toolName, output, errText, true
}

func recoveryTargetPathFromToolOutput(output map[string]any) (string, bool) {
	if output == nil {
		return "", false
	}
	rawPath := strings.TrimSpace(anyString(output["path"]))
	if rawPath == "" {
		return "", false
	}
	normalized := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rawPath)))
	prefix := strings.TrimSuffix(recoveryArtifactPathPrefix, "/") + "/"
	if strings.HasPrefix(normalized, prefix) && len(normalized) > len(prefix) {
		return strings.TrimPrefix(normalized, prefix), true
	}
	return normalized, false
}

func anyString(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

func anyStrings(value any) []string {
	switch typed := value.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(item)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(anyString(item))
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func (s *service) recoveryWorkspaceRoots(ctx context.Context, taskRecord repo.ProjectTask) ([]string, error) {
	if s == nil || s.project == nil {
		return nil, fmt.Errorf("project repository is required for recovery workspace resolution")
	}

	projectRecord, err := s.project.GetByID(ctx, taskRecord.ProjectID)
	if err != nil {
		return nil, err
	}
	projectRoot, err := workspace.ProjectRoot("", projectRecord.Slug)
	if err != nil {
		return nil, err
	}
	if s.pool == nil || taskRecord.OrganizationID == uuid.Nil {
		return []string{projectRoot}, nil
	}

	orgRecord, err := repo.NewOrgRepo(s.pool).GetByID(ctx, taskRecord.OrganizationID)
	if err != nil {
		return []string{projectRoot}, nil
	}
	roots, err := workspace.ProjectCompatibilityRoots("", orgRecord.Slug, projectRecord.Slug)
	if err != nil {
		return nil, err
	}
	if taskRecord.TaskNumber > 0 {
		taskWorktreeRoot := filepath.Join(
			workspace.ResolveDataDir(""),
			"task-worktrees",
			strings.TrimSpace(projectRecord.Slug),
			fmt.Sprintf("task-%d", taskRecord.TaskNumber),
		)
		roots = append([]string{taskWorktreeRoot}, roots...)
	}
	if len(roots) == 0 {
		return []string{projectRoot}, nil
	}
	return roots, nil
}

func recoveryArtifactPathFromBlockerReason(reason string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(reason))
	const marker = "resume from "
	index := strings.Index(lower, marker)
	if index < 0 {
		return "", false
	}
	remainder := strings.TrimSpace(reason[index+len(marker):])
	fields := strings.Fields(remainder)
	if len(fields) == 0 {
		return "", false
	}
	candidate := strings.Trim(fields[0], " \t\r\n`\"'()[]{},;:")
	normalized := filepath.ToSlash(filepath.Clean(filepath.FromSlash(candidate)))
	if normalized == "" || normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") {
		return "", false
	}
	if !strings.HasPrefix(normalized, recoveryArtifactPathPrefix) || len(normalized) <= len(recoveryArtifactPathPrefix) {
		return "", false
	}
	return normalized, true
}

func recoveryTargetPathFromArtifact(artifactPath string) (string, bool) {
	normalized := filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(artifactPath))))
	if !strings.HasPrefix(normalized, recoveryArtifactPathPrefix) || len(normalized) <= len(recoveryArtifactPathPrefix) {
		return "", false
	}
	targetPath := strings.TrimPrefix(normalized, recoveryArtifactPathPrefix)
	if targetPath == "" || targetPath == "." || targetPath == ".." || strings.HasPrefix(targetPath, "../") {
		return "", false
	}
	return targetPath, true
}

func recoveryArtifactFailureReason(document string) string {
	trimmed := strings.TrimSpace(document)
	if trimmed == "" {
		return ""
	}
	const marker = "\n## Last Write Failure\n"
	index := strings.Index(trimmed, marker)
	if index < 0 {
		return ""
	}
	section := strings.TrimLeft(trimmed[index+len(marker):], "\n")
	if next := strings.Index(section, "\n## Draft Content"); next >= 0 {
		section = section[:next]
	}
	return strings.TrimSpace(section)
}

func readRecoveryWorkspaceFile(roots []string, relPath string) (string, bool, error) {
	var resolutionErr error
	for _, root := range roots {
		absPath, _, err := resolveRecoveryWorkspacePath(root, relPath)
		if err != nil {
			if resolutionErr == nil {
				resolutionErr = err
			}
			continue
		}
		if info, statErr := os.Stat(absPath); statErr == nil {
			if info.IsDir() {
				continue
			}
		} else if !os.IsNotExist(statErr) {
			if resolutionErr == nil {
				resolutionErr = statErr
			}
			continue
		}
		body, err := os.ReadFile(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if resolutionErr == nil {
				resolutionErr = err
			}
			continue
		}
		return string(body), true, nil
	}
	if resolutionErr != nil {
		return "", false, resolutionErr
	}
	return "", false, nil
}

func recoveryWorkspaceFileExists(roots []string, relPath string) (bool, error) {
	var resolutionErr error
	for _, root := range roots {
		absPath, _, err := resolveRecoveryWorkspacePath(root, relPath)
		if err != nil {
			if resolutionErr == nil {
				resolutionErr = err
			}
			continue
		}
		info, err := os.Stat(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if resolutionErr == nil {
				resolutionErr = err
			}
			continue
		}
		if info.IsDir() {
			continue
		}
		return true, nil
	}
	if resolutionErr != nil {
		return false, resolutionErr
	}
	return false, nil
}

func resolveRecoveryWorkspacePath(root, relOrAbsPath string) (string, string, error) {
	trimmedRoot := strings.TrimSpace(root)
	trimmedPath := strings.TrimSpace(relOrAbsPath)
	if trimmedRoot == "" {
		return "", "", fmt.Errorf("workspace root is required")
	}
	if trimmedPath == "" {
		return "", "", fmt.Errorf("target path is required")
	}

	rootAbs, err := filepath.Abs(trimmedRoot)
	if err != nil {
		return "", "", err
	}

	candidate := filepath.Clean(filepath.FromSlash(trimmedPath))
	targetAbs := candidate
	if !filepath.IsAbs(targetAbs) {
		targetAbs = filepath.Join(rootAbs, candidate)
	}
	targetAbs = filepath.Clean(targetAbs)

	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", "", err
	}
	if rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path traversal is not allowed")
	}
	return targetAbs, filepath.ToSlash(rel), nil
}

func normalizeTaskResumeCodeComponent(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range trimmed {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}
