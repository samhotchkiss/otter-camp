package task

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
)

const (
	// Historical metadata value retained to keep existing wakeup and recovery-turn
	// handling compatible while the resume surface broadens beyond validation loops.
	RecoveryActionResumeBlockedTask = "resume_validation_blocked_task"

	RecoveryBlockerClassNotBlocked                   = "not_blocked"
	RecoveryBlockerClassValidationLoop               = "deterministic_validation_loop"
	RecoveryBlockerClassDurableRecoveryCheckpoint    = "durable_recovery_checkpoint"
	RecoveryBlockerClassBlockedWithoutResumableState = "blocked_without_resumable_state"
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
}

func classifyTaskResumeDecision(taskRecord repo.ProjectTask, blockerReason string) taskResumeDecision {
	if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "blocked") {
		return taskResumeDecision{blockerClass: RecoveryBlockerClassNotBlocked}
	}
	if guard, ok := ParseValidationGuard(taskRecord.Metadata); ok && guard.Blocked {
		guardCopy := guard
		return taskResumeDecision{
			resumable:            true,
			blockerClass:         RecoveryBlockerClassValidationLoop,
			blockerReason:        strings.TrimSpace(blockerReason),
			clearValidationGuard: true,
			validationGuard:      &guardCopy,
		}
	}
	if checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata); ok && hasDurableRecoveryCheckpoint(checkpoint) {
		checkpointCopy := checkpoint
		return taskResumeDecision{
			resumable:     true,
			blockerClass:  RecoveryBlockerClassDurableRecoveryCheckpoint,
			blockerReason: strings.TrimSpace(blockerReason),
			checkpoint:    &checkpointCopy,
		}
	}
	return taskResumeDecision{
		blockerClass:  RecoveryBlockerClassBlockedWithoutResumableState,
		blockerReason: strings.TrimSpace(blockerReason),
	}
}

func hasDurableRecoveryCheckpoint(checkpoint taskcheckpoint.RecoveryFileWriteCheckpoint) bool {
	return strings.TrimSpace(checkpoint.TargetPath) != "" ||
		strings.TrimSpace(checkpoint.ArtifactPath) != "" ||
		strings.TrimSpace(checkpoint.FailureReason) != "" ||
		strings.TrimSpace(checkpoint.HistoryStartMessageID) != "" ||
		strings.TrimSpace(checkpoint.HaltTurnID) != ""
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
