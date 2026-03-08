package task

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
	"github.com/samhotchkiss/otter-camp/internal/workspace"
)

const (
	// Historical metadata value retained to keep existing wakeup and recovery-turn
	// handling compatible while the resume surface broadens beyond validation loops.
	RecoveryActionResumeBlockedTask = "resume_validation_blocked_task"

	RecoveryBlockerClassNotBlocked                   = "not_blocked"
	RecoveryBlockerClassValidationLoop               = "deterministic_validation_loop"
	RecoveryBlockerClassDurableRecoveryCheckpoint    = "durable_recovery_checkpoint"
	RecoveryBlockerClassBlockedWithoutResumableState = "blocked_without_resumable_state"

	recoveryArtifactPathPrefix = ".ottercamp/recovery/"
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

func (s *service) maybeRepairDurableRecoveryCheckpoint(ctx context.Context, taskRecord repo.ProjectTask, blockerReason string) (repo.ProjectTask, bool, error) {
	if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "blocked") {
		return taskRecord, false, nil
	}
	if checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata); ok && hasDurableRecoveryCheckpoint(checkpoint) {
		return taskRecord, false, nil
	}

	checkpoint, ok, err := s.rebuildDurableRecoveryCheckpointFromWorkspace(ctx, taskRecord, blockerReason)
	if err != nil || !ok {
		return taskRecord, false, err
	}

	merged, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(taskRecord.Metadata, checkpoint)
	if err != nil {
		return taskRecord, false, err
	}
	taskRecord.Metadata = merged
	updated, err := s.tasks.Update(ctx, taskRecord)
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
	return taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:    targetPath,
		ArtifactPath:  artifactPath,
		FailureReason: recoveryArtifactFailureReason(artifactDocument),
		UpdatedAt:     updatedAt.Format(time.RFC3339Nano),
	}, true, nil
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
			if resolutionErr == nil {
				resolutionErr = fmt.Errorf("%s resolved to a directory", relPath)
			}
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
