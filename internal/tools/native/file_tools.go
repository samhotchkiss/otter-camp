package native

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
)

var utf8ReplacementBytes = []byte("\uFFFD")
var preferredDeliverableRootPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:under|in)\s+/?((?:content(?:/[A-Za-z0-9._-]+)+)/?)`),
}

type listedEntry struct {
	name       string
	path       string
	entryType  string
	sizeBytes  int64
	modifiedAt string
}

type sessionRecoveryState struct {
	targetPath string
	reviewLane bool
}

func isNotFoundLikePathError(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

func (e *NativeToolExecutor) buildNotFoundReadResponse(ctx context.Context, scope workspaceScope, requestedPath string) map[string]any {
	out := map[string]any{"error": "not_found"}
	normalizedPath := normalizeWorkspacePath(requestedPath)
	if normalizedPath == "" {
		return out
	}
	out["path"] = normalizedPath
	if targetPath := normalizeWorkspacePath(e.latestRecoveryTargetPathForSession(ctx, scope)); targetPath != "" && sameOrNestedWorkspacePath(normalizedPath, targetPath) {
		out["deliverable_path"] = targetPath
	}
	return out
}

func (e *NativeToolExecutor) handleFileRead(ctx context.Context, input map[string]any) (map[string]any, error) {
	wd, scope, resolved, err := e.resolveInputPath(ctx, input, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}

	pathInput, ok := readString(input, "path")
	if !ok || pathInput == "" {
		return map[string]any{"error": "not_found"}, nil
	}

	realPath, err := e.resolveExistingPath(wd, pathInput)
	if err != nil {
		switch {
		case errors.Is(err, ErrPathTraversal):
			return map[string]any{"error": "path_traversal"}, nil
		case isNotFoundLikePathError(err):
			return e.buildNotFoundReadResponse(ctx, scope, pathInput), nil
		default:
			return nil, err
		}
	}
	if !isWithinRoot(wd.Root(), realPath) {
		return map[string]any{"error": "path_traversal"}, nil
	}

	maxBytes := clamp(readInt(input, "max_bytes", defaultReadMaxBytes), 1, hardReadMaxBytes)
	offsetBytes := readInt(input, "offset_bytes", 0)
	if offsetBytes < 0 {
		offsetBytes = 0
	}
	encoding := "utf8"
	if raw, ok := readString(input, "encoding"); ok && strings.EqualFold(raw, "base64") {
		encoding = "base64"
	}

	file, err := os.Open(realPath)
	if err != nil {
		if isNotFoundLikePathError(err) {
			return e.buildNotFoundReadResponse(ctx, scope, pathInput), nil
		}
		return nil, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if int64(offsetBytes) > stat.Size() {
		offsetBytes = int(stat.Size())
	}
	if _, err := file.Seek(int64(offsetBytes), io.SeekStart); err != nil {
		return nil, err
	}

	limited := io.LimitReader(file, int64(maxBytes)+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	truncated := false
	if len(payload) > maxBytes {
		payload = payload[:maxBytes]
		truncated = true
	}
	bytesRead := len(payload)

	content := sanitizeUTF8TextBytes(payload)
	if encoding == "base64" {
		content = base64.StdEncoding.EncodeToString(payload)
	}

	renderedPath := renderPath(wd.Root(), resolved)
	if reject, blocked, rejectErr := e.rejectRecoveryTargetReread(ctx, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if blocked {
		reject["path"] = renderedPath
		return reject, nil
	}
	if reject, blocked, rejectErr := e.rejectExecutionFirstDeliverableReread(ctx, wd, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if blocked {
		reject["path"] = renderedPath
		return reject, nil
	}
	if reject, blocked, rejectErr := e.rejectPlaceholderDeliverableRead(ctx, scope, renderedPath, content); rejectErr != nil {
		return nil, rejectErr
	} else if blocked {
		reject["path"] = renderedPath
		return reject, nil
	}
	if reject, blocked, rejectErr := e.rejectMismatchedTaskDeliverableRead(ctx, scope, renderedPath, content); rejectErr != nil {
		return nil, rejectErr
	} else if blocked {
		reject["path"] = renderedPath
		return reject, nil
	}

	return map[string]any{
		"content":      content,
		"encoding":     encoding,
		"byte_size":    stat.Size(),
		"bytes_read":   bytesRead,
		"offset_bytes": offsetBytes,
		"truncated":    truncated,
		"path":         renderedPath,
	}, nil
}

func (e *NativeToolExecutor) rejectPlaceholderDeliverableRead(ctx context.Context, scope workspaceScope, relativePath, content string) (map[string]any, bool, error) {
	if e == nil || e.tasks == nil || scope.taskID == nil || *scope.taskID == uuid.Nil {
		return nil, false, nil
	}
	taskRecord, err := e.tasks.GetByID(ctx, *scope.taskID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	plan, ok := taskplan.Parse(taskRecord.Metadata)
	deliverablePath := e.taskExplicitDeliverablePath(ctx, taskRecord)
	if deliverablePath == "" {
		deliverablePath = e.latestRecoveryTargetPathForSession(ctx, scope)
	}
	if deliverablePath == "" &&
		(!ok || !strings.EqualFold(strings.TrimSpace(plan.Mode), taskplan.ModeExecutionFirst)) &&
		!strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "review") {
		return nil, false, nil
	}
	normalizedPath := normalizeWorkspacePath(relativePath)
	if normalizedPath == "" {
		return nil, false, nil
	}
	if !looksLikeRejectedDeliverablePlaceholder(content) &&
		!looksLikeDeliverableCompletionSummaryWithoutBody(normalizedPath, content) &&
		!looksLikeTaskBriefEchoPlaceholder(normalizedPath, content) {
		return nil, false, nil
	}
	if deliverablePath == "" {
		deliverablePath = parsePlaceholderDeliverableTarget(content)
	}
	if deliverablePath == "" && strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "review") {
		outputs := currentTaskContentMigrationOutputSet(taskRecord)
		if _, ok := outputs[normalizedPath]; ok {
			deliverablePath = normalizedPath
		}
	}
	if deliverablePath == "" || !sameOrNestedWorkspacePath(normalizedPath, deliverablePath) {
		return nil, false, nil
	}
	message := fmt.Sprintf("The explicit deliverable `%s` currently contains rejected placeholder or status narration. Do not reread `%s` for context. Overwrite it directly with the real deliverable body instead.", deliverablePath, normalizedPath)
	if workspacePathLooksDirectory(deliverablePath) {
		message = fmt.Sprintf("A file under the explicit deliverable directory `%s/` currently contains rejected placeholder or status narration. Do not reread `%s` for context. Overwrite it directly with the real deliverable body instead.", strings.TrimSuffix(deliverablePath, "/"), normalizedPath)
	}
	return map[string]any{
		"error":            "placeholder_deliverable",
		"deliverable_path": deliverablePath,
		"message":          message,
	}, true, nil
}

func (e *NativeToolExecutor) rejectMismatchedTaskDeliverableRead(ctx context.Context, scope workspaceScope, relativePath, content string) (map[string]any, bool, error) {
	if e == nil || e.tasks == nil || scope.taskID == nil || *scope.taskID == uuid.Nil {
		return nil, false, nil
	}
	taskRecord, err := e.tasks.GetByID(ctx, *scope.taskID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	targetPath := e.preferredRecoveryTargetForTask(ctx, taskRecord)
	if targetPath == "" {
		targetPath = e.latestRecoveryTargetPathForSession(ctx, scope)
	}
	normalizedPath := normalizeWorkspacePath(relativePath)
	if normalizedPath == "" || targetPath == "" || !sameOrNestedWorkspacePath(normalizedPath, targetPath) {
		return nil, false, nil
	}
	if taskAllowsPerItemDeliverableInspection(taskRecord, normalizedPath) {
		return nil, false, nil
	}
	if !reviewCheckpointOutputLooksLikeDecisionNarration(taskRecord, normalizedPath, content) &&
		!taskDraftSemanticallyMismatchesScope(taskRecord, content) {
		return nil, false, nil
	}
	return map[string]any{
		"error":            "mismatched_deliverable_context",
		"deliverable_path": targetPath,
		"message": fmt.Sprintf(
			"The current deliverable `%s` contains content that does not match this task's scope. Do not reuse `%s` as context; overwrite it with content that matches the current task title and description.",
			targetPath,
			normalizedPath,
		),
	}, true, nil
}

func reviewCheckpointOutputLooksLikeDecisionNarration(taskRecord repo.ProjectTask, normalizedPath, content string) bool {
	if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "review") {
		return false
	}
	outputs := currentTaskContentMigrationOutputSet(taskRecord)
	if _, ok := outputs[normalizeWorkspacePath(normalizedPath)]; !ok {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(content))
	if lower == "" {
		return false
	}
	if containsAnySubstring(lower,
		"the workspace is blocking further reads under",
		"evidence already gathered across this review session",
		"i have sufficient evidence from prior turns to render the decision now",
	) && containsAnySubstring(lower,
		"assessment against acceptance criteria",
		"the deliverables satisfy the task's acceptance criteria",
		"approving.",
		"review decision: approve",
		"review decision: reject",
	) {
		return true
	}
	return containsAnySubstring(lower,
		"these are the core deliverables for this task per the checkpoint",
		"both returned errors indicating they are rejected placeholders",
		"i cannot confirm the deliverables are satisfied",
	) && containsAnySubstring(lower,
		"rejecting this review",
		"review decision: reject",
		"reject the review immediately",
	)
}

func (e *NativeToolExecutor) rejectExecutionFirstDeliverableReread(ctx context.Context, wd SessionWorkDir, scope workspaceScope, relativePath string) (map[string]any, bool, error) {
	if e == nil || e.tasks == nil || scope.taskID == nil || *scope.taskID == uuid.Nil {
		return nil, false, nil
	}
	taskRecord, err := e.tasks.GetByID(ctx, *scope.taskID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "review") {
		return nil, false, nil
	}
	plan, ok := taskplan.Parse(taskRecord.Metadata)
	if !ok || !strings.EqualFold(strings.TrimSpace(plan.Mode), taskplan.ModeExecutionFirst) {
		return nil, false, nil
	}
	deliverablePath := e.taskExplicitDeliverablePath(ctx, taskRecord)
	if deliverablePath == "" {
		return nil, false, nil
	}
	normalizedPath := normalizeWorkspacePath(relativePath)
	if normalizedPath == "" || sameOrNestedWorkspacePath(normalizedPath, deliverablePath) {
		return nil, false, nil
	}
	exists, err := substantiveExplicitDeliverableExists(wd.Root(), deliverablePath)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}
	return map[string]any{
		"error":            "explicit_deliverable_focus_required",
		"deliverable_path": deliverablePath,
		"message": fmt.Sprintf(
			"This execution-first task already has a substantive explicit deliverable `%s`. Do not reread `%s` now. Revise the deliverable directly if needed, or stop and let the runtime advance the flow.",
			deliverablePath,
			normalizedPath,
		),
	}, true, nil
}

func (e *NativeToolExecutor) rejectRecoveryTargetReread(ctx context.Context, scope workspaceScope, relativePath string) (map[string]any, bool, error) {
	if e == nil || scope.taskID == nil || *scope.taskID == uuid.Nil {
		return nil, false, nil
	}
	recoveryState := e.sessionRecoveryState(ctx, scope)
	targetPath := e.latestRecoveryTargetPathForSession(ctx, scope)
	if targetPath == "" {
		return nil, false, nil
	}
	normalizedPath := normalizeWorkspacePath(relativePath)
	if normalizedPath == "" || sameOrNestedWorkspacePath(normalizedPath, targetPath) {
		return nil, false, nil
	}
	if allow, allowErr := e.allowRecoveryDeliverableRootInspection(ctx, scope, normalizedPath, targetPath, recoveryState.reviewLane); allowErr != nil {
		return nil, false, allowErr
	} else if allow {
		return nil, false, nil
	}
	return map[string]any{
		"error":            "recovery_target_focus_required",
		"deliverable_path": targetPath,
		"message": fmt.Sprintf(
			"Recovery already identified `%s` as the target deliverable. Do not reread `%s` now. Continue from the recovery target and write the deliverable body directly.",
			targetPath,
			normalizedPath,
		),
	}, true, nil
}

func (e *NativeToolExecutor) allowRecoveryDeliverableRootInspection(ctx context.Context, scope workspaceScope, normalizedPath, targetPath string, reviewLane bool) (bool, error) {
	if e == nil || e.tasks == nil || scope.taskID == nil || *scope.taskID == uuid.Nil {
		return false, nil
	}
	taskRecord, err := e.tasks.GetByID(ctx, *scope.taskID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	rootPath := e.taskPreferredDeliverableRoot(ctx, taskRecord)
	if rootPath == "" || !workspacePathWithinRoot(targetPath, rootPath) {
		return false, nil
	}
	reviewLane = reviewLane || strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "review")
	if !reviewLane && e.flowNodes != nil && taskRecord.CurrentFlowNodeID != nil && *taskRecord.CurrentFlowNodeID != uuid.Nil {
		node, nodeErr := e.flowNodes.GetByID(ctx, *taskRecord.CurrentFlowNodeID)
		if nodeErr != nil && !errors.Is(nodeErr, repo.ErrNotFound) {
			return false, nodeErr
		}
		reviewLane = nodeErr == nil && strings.EqualFold(strings.TrimSpace(node.NodeType), "review")
	}
	if checkpoint, ok := taskcheckpoint.ParseContentMigrationCheckpoint(taskRecord.Metadata); ok {
		if allow, allowErr := e.allowRecoveryCheckpointArtifactInspectionWhenTrackedOutputsComplete(ctx, taskRecord, checkpoint, normalizedPath, rootPath); allowErr != nil {
			return false, allowErr
		} else if allow {
			return true, nil
		}
		if !reviewLane {
			return false, nil
		}
	}
	if taskAllowsPerItemDeliverableInspection(taskRecord, normalizedPath) ||
		taskAllowsBatchRecoveryRootInspection(taskRecord, normalizedPath, targetPath) {
		return true, nil
	}
	if !reviewLane {
		return false, nil
	}
	return workspacePathWithinRoot(normalizedPath, rootPath), nil
}

func (e *NativeToolExecutor) allowRecoveryCheckpointArtifactInspectionWhenTrackedOutputsComplete(ctx context.Context, taskRecord repo.ProjectTask, checkpoint taskcheckpoint.ContentMigrationCheckpoint, candidatePath, rootPath string) (bool, error) {
	workspaceRoot := strings.TrimSpace(e.explicitRoot)
	if workspaceRoot == "" && e != nil && e.projects != nil {
		root, err := e.taskWorkspaceRoot(ctx, taskRecord)
		if err != nil {
			return false, nil
		}
		workspaceRoot = root
	}
	if workspaceRoot == "" || !contentMigrationCheckpointOutputsCompleteOnDisk(workspaceRoot, taskRecord, checkpoint, rootPath) {
		return false, nil
	}
	candidatePath = normalizeWorkspacePath(candidatePath)
	if candidatePath == "" {
		return false, nil
	}
	return contentMigrationCheckpointArtifactPathMatches(checkpoint, candidatePath), nil
}

func preferredTaskDeliverableRoot(taskRecord repo.ProjectTask) string {
	if taskRecord.Description == nil {
		return ""
	}
	description := strings.TrimSpace(*taskRecord.Description)
	for _, pattern := range preferredDeliverableRootPatterns {
		matches := pattern.FindAllStringSubmatch(description, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			root := normalizeExplicitDeliverablePathCandidate(match[1])
			if !looksLikePreferredDeliverableRootPath(root) {
				continue
			}
			return root
		}
	}
	return ""
}

func decompositionParentTaskID(taskRecord repo.ProjectTask) uuid.UUID {
	if len(taskRecord.Metadata) == 0 {
		return uuid.Nil
	}
	var metadata map[string]any
	if err := json.Unmarshal(taskRecord.Metadata, &metadata); err != nil {
		return uuid.Nil
	}
	parentIDText := strings.TrimSpace(readStringValue(metadata["decomposition_parent_task_id"]))
	if parentIDText == "" {
		return uuid.Nil
	}
	parentID, err := uuid.Parse(parentIDText)
	if err != nil {
		return uuid.Nil
	}
	return parentID
}

func (e *NativeToolExecutor) decompositionParentTask(ctx context.Context, taskRecord repo.ProjectTask) (repo.ProjectTask, bool) {
	if e == nil || e.tasks == nil {
		return repo.ProjectTask{}, false
	}
	parentID := decompositionParentTaskID(taskRecord)
	if parentID == uuid.Nil {
		return repo.ProjectTask{}, false
	}
	parentTask, err := e.tasks.GetByID(ctx, parentID)
	if err != nil {
		return repo.ProjectTask{}, false
	}
	return parentTask, true
}

func (e *NativeToolExecutor) taskExplicitDeliverablePath(ctx context.Context, taskRecord repo.ProjectTask) string {
	if explicit := strings.TrimSpace(parseExplicitDeliverablePath(taskRecord)); explicit != "" {
		return explicit
	}
	parentTask, ok := e.decompositionParentTask(ctx, taskRecord)
	if !ok {
		return ""
	}
	inherited := strings.TrimSpace(parseExplicitDeliverablePath(parentTask))
	if inherited == "" || !deliverableTargetMatchesTaskContract(taskRecord, inherited) {
		return ""
	}
	return inherited
}

func (e *NativeToolExecutor) taskPreferredDeliverableRoot(ctx context.Context, taskRecord repo.ProjectTask) string {
	if explicit := strings.TrimSpace(e.taskExplicitDeliverablePath(ctx, taskRecord)); explicit != "" {
		return ""
	}
	if root := strings.TrimSpace(preferredTaskDeliverableRoot(taskRecord)); root != "" {
		return root
	}
	parentTask, ok := e.decompositionParentTask(ctx, taskRecord)
	if !ok {
		return ""
	}
	if explicit := strings.TrimSpace(parseExplicitDeliverablePath(parentTask)); explicit != "" {
		return ""
	}
	return strings.TrimSpace(preferredTaskDeliverableRoot(parentTask))
}

func normalizeExplicitDeliverablePathCandidate(raw string) string {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.Trim(trimmed, "`'\"“”‘’()[]{}")
	trimmed = strings.TrimRight(trimmed, ".,:;!?")
	trimmed = strings.Trim(trimmed, "`'\"“”‘’()[]{}")
	return normalizeWorkspacePath(trimmed)
}

func looksLikePreferredDeliverableRootPath(normalized string) bool {
	normalized = normalizeWorkspacePath(normalized)
	if normalized == "" {
		return false
	}
	if !strings.HasPrefix(strings.ToLower(normalized), "content/") {
		return false
	}
	return !strings.Contains(filepath.Base(normalized), ".")
}

func workspacePathWithinRoot(path, root string) bool {
	path = normalizeWorkspacePath(path)
	root = normalizeWorkspacePath(root)
	if path == "" || root == "" {
		return false
	}
	if path == root {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/")
}

func (e *NativeToolExecutor) sessionRecoveryState(ctx context.Context, scope workspaceScope) sessionRecoveryState {
	if e == nil || e.messages == nil || scope.sessionID == nil || *scope.sessionID == uuid.Nil {
		return sessionRecoveryState{}
	}
	messages, err := e.messages.ListBySession(ctx, *scope.sessionID)
	if err != nil {
		return sessionRecoveryState{}
	}
	state := sessionRecoveryState{
		reviewLane: sessionMessagesContainReviewPrompt(messages),
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(messages[i].Role), "system") {
			continue
		}
		if target := parseRecoveryTargetPath(messages[i].Content); target != "" {
			state.targetPath = target
			return state
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		role := strings.ToLower(strings.TrimSpace(messages[i].Role))
		if role != "user" && role != "system" {
			continue
		}
		if target := parsePromptDeliverableTarget(messages[i].Content); target != "" {
			state.targetPath = target
			return state
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(messages[i].Role), "tool_result") {
			continue
		}
		if target := parseRecentDeliverableTargetFromToolResult(messages[i].Content); target != "" {
			state.targetPath = target
			return state
		}
	}
	return state
}

func sessionMessagesContainReviewPrompt(messages []repo.ChatMessage) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		role := strings.ToLower(strings.TrimSpace(messages[i].Role))
		if role != "user" && role != "system" {
			continue
		}
		if looksLikeReviewPrompt(messages[i].Content) {
			return true
		}
	}
	return false
}

func looksLikeReviewPrompt(content string) bool {
	trimmed := strings.TrimSpace(content)
	return strings.HasPrefix(trimmed, "Review only.") && strings.Contains(trimmed, "flow.review_decision")
}

func substantiveExplicitDeliverableExists(workspaceRoot, deliverablePath string) (bool, error) {
	normalized := normalizeWorkspacePath(deliverablePath)
	if normalized == "" {
		return false, nil
	}
	absPath := filepath.Join(workspaceRoot, filepath.FromSlash(normalized))
	if workspacePathLooksDirectory(normalized) {
		found := false
		walkErr := filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return filepath.SkipDir
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, statErr := d.Info()
			if statErr != nil {
				return statErr
			}
			if info.Size() > 0 {
				found = true
				return io.EOF
			}
			return nil
		})
		if errors.Is(walkErr, io.EOF) {
			return true, nil
		}
		if errors.Is(walkErr, fs.ErrNotExist) {
			return false, nil
		}
		if walkErr != nil {
			return false, walkErr
		}
		return found, nil
	}
	info, err := os.Stat(absPath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Size() > 0, nil
}

func (e *NativeToolExecutor) latestRecoveryTargetPathForSession(ctx context.Context, scope workspaceScope) string {
	if e != nil && e.tasks != nil && scope.taskID != nil && *scope.taskID != uuid.Nil {
		taskRecord, err := e.tasks.GetByID(ctx, *scope.taskID)
		if err == nil {
			if target := e.taskExplicitDeliverablePath(ctx, taskRecord); target != "" {
				return target
			}
			if checkpoint, ok := taskcheckpoint.ParseContentMigrationCheckpoint(taskRecord.Metadata); ok {
				if target := e.contentMigrationCheckpointPreferredOutputPathForSession(ctx, taskRecord, checkpoint); target != "" {
					return target
				}
			}
			if target := e.preferredRecoveryTargetForTask(ctx, taskRecord); target != "" {
				return target
			}
			if target := e.sessionRecoveryState(ctx, scope).targetPath; target != "" && deliverableTargetMatchesTaskContract(taskRecord, target) {
				return target
			}
			return ""
		}
	}
	return e.sessionRecoveryState(ctx, scope).targetPath
}

func (e *NativeToolExecutor) preferredRecoveryTargetForTask(ctx context.Context, taskRecord repo.ProjectTask) string {
	if explicit := strings.TrimSpace(e.taskExplicitDeliverablePath(ctx, taskRecord)); explicit != "" {
		return explicit
	}
	return preferredRecoveryTargetForTask(taskRecord)
}

func (e *NativeToolExecutor) contentMigrationCheckpointPreferredOutputPathForSession(ctx context.Context, taskRecord repo.ProjectTask, checkpoint taskcheckpoint.ContentMigrationCheckpoint) string {
	fallback := contentMigrationCheckpointPreferredOutputPath(taskRecord, checkpoint)
	workspaceRoot := strings.TrimSpace(e.explicitRoot)
	if workspaceRoot == "" && e != nil && e.projects != nil {
		root, err := e.taskWorkspaceRoot(ctx, taskRecord)
		if err == nil {
			workspaceRoot = root
		}
	}
	if workspaceRoot == "" {
		return fallback
	}

	for _, output := range checkpoint.Outputs {
		target := normalizeWorkspacePath(output.Path)
		if target == "" {
			continue
		}
		if !deliverableTargetMatchesTaskContract(taskRecord, target) {
			continue
		}
		if fallback == "" {
			fallback = target
		}
		exists, err := substantiveExplicitDeliverableExists(workspaceRoot, target)
		if err != nil {
			continue
		}
		if !exists {
			return target
		}
	}
	return fallback
}

func parsePromptDeliverableTarget(content string) string {
	const marker = "Start with the preferred deliverable target `"

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		idx := strings.Index(trimmed, marker)
		if idx < 0 {
			continue
		}
		remainder := trimmed[idx+len(marker):]
		end := strings.Index(remainder, "`")
		if end < 0 {
			return ""
		}
		rawTarget := remainder[:end]
		target := normalizeWorkspacePath(rawTarget)
		if !looksLikeExplicitDeliverablePath(target, rawTarget) {
			return ""
		}
		return target
	}

	return ""
}

func parseRecentDeliverableTargetFromToolResult(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return ""
	}
	if payload == nil {
		return ""
	}
	output, _ := payload["output"].(map[string]any)
	if output != nil {
		if target := normalizeWorkspacePath(readStringValue(output["deliverable_path"])); target != "" && looksLikeExplicitDeliverablePath(target, readStringValue(output["deliverable_path"])) {
			return target
		}
	}
	toolName := strings.ToLower(strings.TrimSpace(readStringValue(payload["tool_name"])))
	switch toolName {
	case "file.read", "file.write":
		if output != nil {
			if target := normalizeWorkspacePath(readStringValue(output["path"])); target != "" && looksLikeExplicitDeliverablePath(target, readStringValue(output["path"])) {
				return target
			}
		}
	}
	return ""
}

func parseRecoveryTargetPath(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "Target file:") {
			continue
		}
		target := strings.TrimSpace(strings.TrimPrefix(trimmed, "Target file:"))
		normalized := normalizeWorkspacePath(target)
		if !looksLikeExplicitDeliverablePath(normalized, target) {
			return ""
		}
		return normalized
	}
	return ""
}

func parsePlaceholderDeliverableTarget(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "**Deliverable Target**:") && !strings.HasPrefix(trimmed, "Deliverable Target:") {
			continue
		}
		target := strings.TrimSpace(strings.TrimPrefix(trimmed, "**Deliverable Target**:"))
		target = strings.TrimSpace(strings.TrimPrefix(target, "Deliverable Target:"))
		return normalizeWorkspacePath(target)
	}
	return ""
}

func preferredRecoveryTargetForTask(taskRecord repo.ProjectTask) string {
	if checkpoint, ok := taskcheckpoint.ParseContentMigrationCheckpoint(taskRecord.Metadata); ok {
		if target := contentMigrationCheckpointPreferredOutputPath(taskRecord, checkpoint); target != "" {
			return target
		}
	}
	if checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata); ok {
		if target := normalizeRecoveryCheckpointTargetForTask(taskRecord, checkpoint.TargetPath); target != "" {
			return target
		}
	}
	if explicit := parseExplicitDeliverablePath(taskRecord); explicit != "" && deliverableTargetMatchesTaskContract(taskRecord, explicit) {
		return explicit
	}
	return ""
}

func contentMigrationCheckpointPreferredOutputPath(taskRecord repo.ProjectTask, checkpoint taskcheckpoint.ContentMigrationCheckpoint) string {
	if strings.TrimSpace(parseExplicitDeliverablePath(taskRecord)) != "" || !taskExpectsMarkdownDeliverables(taskRecord) {
		return ""
	}
	root := preferredTaskDeliverableRoot(taskRecord)
	for _, output := range checkpoint.Outputs {
		target := normalizeWorkspacePath(output.Path)
		if target == "" {
			continue
		}
		if !deliverableTargetMatchesTaskContract(taskRecord, target) {
			continue
		}
		if root != "" && !workspacePathWithinRoot(target, root) {
			continue
		}
		return target
	}
	return ""
}

func normalizeRecoveryCheckpointTargetForTask(taskRecord repo.ProjectTask, rawTarget string) string {
	target := normalizeWorkspacePath(rawTarget)
	if target == "" {
		return ""
	}
	if deliverableTargetMatchesTaskContract(taskRecord, target) {
		return target
	}
	if checkpoint, ok := taskcheckpoint.ParseContentMigrationCheckpoint(taskRecord.Metadata); ok {
		if preferred := contentMigrationCheckpointPreferredOutputPath(taskRecord, checkpoint); preferred != "" {
			return preferred
		}
	}
	return ""
}

func deliverableTargetMatchesTaskContract(taskRecord repo.ProjectTask, target string) bool {
	target = normalizeWorkspacePath(target)
	if target == "" {
		return false
	}
	if explicit := parseExplicitDeliverablePath(taskRecord); explicit != "" {
		return sameOrNestedWorkspacePath(target, explicit)
	}
	root := preferredTaskDeliverableRoot(taskRecord)
	if root != "" {
		if !workspacePathWithinRoot(target, root) {
			return false
		}
		if taskExpectsMarkdownDeliverables(taskRecord) {
			return strings.HasSuffix(strings.ToLower(target), ".md")
		}
		return true
	}
	if taskTreatsPathAsDependencyArtifact(taskRecord, target) {
		return false
	}
	if taskDeliverablePathConflictsWithRole(taskRecord, target) {
		return false
	}
	return looksLikeExplicitDeliverablePath(target, target)
}

func taskTreatsPathAsDependencyArtifact(taskRecord repo.ProjectTask, target string) bool {
	target = strings.ToLower(normalizeWorkspacePath(target))
	if target == "" {
		return false
	}
	for _, raw := range taskContractDescriptionCandidates(taskRecord) {
		lower := strings.ToLower(strings.TrimSpace(raw))
		if lower == "" {
			continue
		}
		idx := strings.Index(lower, target)
		if idx < 0 {
			continue
		}
		start := max(0, idx-120)
		prefix := lower[start:idx]
		if containsAnySubstring(prefix,
			"use ",
			"using ",
			"read ",
			"reference ",
			"refer to ",
			"context ",
			"for context",
			"based on ",
			"guided by ",
			"consult ",
		) {
			return true
		}
	}
	return false
}

func taskDeliverablePathConflictsWithRole(taskRecord repo.ProjectTask, target string) bool {
	text := strings.ToLower(strings.TrimSpace(taskRecord.Title))
	if taskRecord.Description != nil {
		text += " " + strings.ToLower(strings.TrimSpace(*taskRecord.Description))
	}
	target = strings.ToLower(strings.TrimSpace(target))
	if text == "" || target == "" {
		return false
	}
	taskLooksFrontend := containsAnySubstring(text, "frontend", "widget", "chat widget", "iframe", "script tag", "html/css/js", "ui")
	taskLooksBackend := containsAnySubstring(text, "backend", "api endpoint", "api wiring", "post /api/", "endpoint", "serverless", "server.js", "api.js")
	targetLooksFrontend := containsAnySubstring(target, "widget", "index.html", ".html")
	targetLooksBackend := containsAnySubstring(target, "api.js", "server.js", "/api", "endpoint")
	if taskLooksBackend && targetLooksFrontend && !targetLooksBackend {
		return true
	}
	if taskLooksFrontend && targetLooksBackend && !targetLooksFrontend {
		return true
	}
	return false
}

func taskExpectsMarkdownDeliverables(taskRecord repo.ProjectTask) bool {
	if taskRecord.Description == nil {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(*taskRecord.Description))
	return strings.Contains(lower, "markdown file") ||
		strings.Contains(lower, "markdown files") ||
		strings.Contains(lower, "save as markdown") ||
		strings.Contains(lower, "save the article text as a clean markdown") ||
		strings.Contains(lower, "clean markdown") ||
		strings.Contains(lower, ".md file") ||
		strings.Contains(lower, ".md files")
}

func taskAllowsPerItemDeliverableInspection(taskRecord repo.ProjectTask, candidate string) bool {
	candidate = normalizeWorkspacePath(candidate)
	if candidate == "" {
		return false
	}
	root := preferredTaskDeliverableRoot(taskRecord)
	if root == "" || !workspacePathWithinRoot(candidate, root) {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(taskRecord.Title))
	if taskRecord.Description != nil {
		text += " " + strings.ToLower(strings.TrimSpace(*taskRecord.Description))
	}
	if strings.Contains(text, "deliverable:") && strings.Contains(text, "files") {
		return true
	}
	if !strings.Contains(text, "for each") {
		return false
	}
	return taskExpectsMarkdownDeliverables(taskRecord) &&
		containsAnySubstring(text,
			"post_urls",
			"save each",
			"save the markdown files",
			"under "+strings.ToLower(root),
			"in "+strings.ToLower(root),
		)
}

func taskAllowsBatchRecoveryRootInspection(taskRecord repo.ProjectTask, candidate, targetPath string) bool {
	candidate = normalizeWorkspacePath(candidate)
	targetPath = normalizeWorkspacePath(targetPath)
	if candidate == "" || targetPath == "" {
		return false
	}
	root := preferredTaskDeliverableRoot(taskRecord)
	if root == "" || !workspacePathWithinRoot(candidate, root) || !workspacePathWithinRoot(targetPath, root) {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(taskRecord.Title))
	if taskRecord.Description != nil {
		text += " " + strings.ToLower(strings.TrimSpace(*taskRecord.Description))
	}
	if !taskExpectsMarkdownDeliverables(taskRecord) {
		return false
	}
	return containsAnySubstring(text,
		"post_urls",
		"for each",
		"save as markdown",
		"markdown files",
	)
}

func contentMigrationCheckpointOutputsCompleteOnDisk(workspaceRoot string, taskRecord repo.ProjectTask, checkpoint taskcheckpoint.ContentMigrationCheckpoint, rootPath string) bool {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return false
	}
	count := 0
	for _, output := range checkpoint.Outputs {
		target := normalizeWorkspacePath(output.Path)
		if target == "" || !deliverableTargetMatchesTaskContract(taskRecord, target) {
			continue
		}
		if rootPath != "" && !workspacePathWithinRoot(target, rootPath) {
			continue
		}
		exists, err := substantiveExplicitDeliverableExists(workspaceRoot, target)
		if err != nil || !exists {
			return false
		}
		count++
	}
	return count > 0
}

func contentMigrationCheckpointArtifactPathMatches(checkpoint taskcheckpoint.ContentMigrationCheckpoint, candidatePath string) bool {
	candidatePath = normalizeWorkspacePath(candidatePath)
	if candidatePath == "" {
		return false
	}
	for _, artifact := range checkpoint.Artifacts {
		if normalizeWorkspacePath(artifact.Path) == candidatePath {
			return true
		}
	}
	for _, script := range checkpoint.Scripts {
		if normalizeWorkspacePath(script.Path) == candidatePath {
			return true
		}
	}
	return false
}

func looksLikeRejectedDeliverablePlaceholder(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || len(trimmed) > 4000 {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(trimmed, "---") {
		return false
	}
	if looksLikeNarratedTaskFileWritePlaceholder(trimmed) {
		return true
	}
	if looksLikeRuntimeOwnedCommitHandoffPlaceholder(trimmed) {
		return true
	}
	if looksLikeRuntimeAdvanceCompletionSummaryPlaceholder(trimmed) {
		return true
	}
	if looksLikeContentMigrationStatusPlaceholder(trimmed) {
		return true
	}
	if looksLikeDeliverableReviewAssessmentPlaceholder(lower) {
		return true
	}
	if looksLikeDeliverableBatchInventoryPlaceholder(trimmed, lower) {
		return true
	}
	if strings.Contains(lower, "# ready to continue oc-") &&
		(strings.Contains(lower, "deliverable target:") || strings.Contains(lower, "**deliverable target**:")) &&
		(strings.Contains(lower, "what i need from you:") || strings.Contains(lower, "**what i need from you**:")) {
		return true
	}
	if strings.Contains(lower, "**status**: task oc-") &&
		strings.Contains(lower, "currently **in_progress**") &&
		strings.Contains(lower, "target deliverable file is just a placeholder") {
		return true
	}
	if strings.HasPrefix(lower, "active task request:") &&
		strings.Contains(lower, "task description:") &&
		strings.Contains(lower, "review instruction:") &&
		strings.Contains(lower, "flow node execution:") {
		return true
	}
	if strings.Contains(lower, "task execution is already underway. reuse the existing workspace files") {
		return true
	}
	if strings.Contains(lower, "i don't see a durable draft") &&
		(strings.Contains(lower, "please provide the substantive draft") || strings.Contains(lower, "please provide the recovery artifact")) {
		return true
	}
	if containsAnySubstring(lower,
		"the workspace is blocking further reads under",
		"evidence already gathered across this review session",
		"i have sufficient evidence from prior turns to render the decision now",
	) && containsAnySubstring(lower,
		"assessment against acceptance criteria",
		"the deliverables satisfy the task's acceptance criteria",
		"approving.",
		"review decision: approve",
		"review decision: reject",
	) {
		return true
	}
	if (strings.Contains(lower, "what i need from you:") || strings.Contains(lower, "**what i need from you**:")) &&
		(strings.Contains(lower, "should i proceed") || strings.Contains(lower, "do you want me to")) {
		return true
	}
	return false
}

func looksLikeDeliverableReviewAssessmentPlaceholder(lower string) bool {
	if lower == "" {
		return false
	}
	if looksLikeDeliverableReviewMetaPlaceholder(lower) {
		return true
	}
	if looksLikeStrongDeliverableReviewerSummaryPlaceholder(lower) {
		return true
	}
	if !containsAnySubstring(lower,
		"now i have all 12 existing files read",
		"all 35 files exist in content/posts/",
		"i now have clear evidence for my review decision",
		"critical findings are already clear",
	) {
		return false
	}
	if !containsAnySubstring(lower,
		"looking at the file sizes and content",
		"let me assess their quality",
		"suspiciously small file sizes",
		"let me check the ones",
		"the file contains garbage content",
		"the other files have proper frontmatter",
	) {
		return false
	}
	return strings.Contains(lower, ".md") || strings.Contains(lower, "content/posts/")
}

func looksLikeDeliverableBatchInventoryPlaceholder(trimmed, lower string) bool {
	if trimmed == "" || lower == "" {
		return false
	}
	if !strings.HasPrefix(lower, "all ") || !strings.Contains(lower, " files exist") {
		return false
	}
	if !containsAnySubstring(lower,
		"posts 1-12",
		"posts 13-24",
		"posts 25-35",
		"0-indexed entries",
		"entries[0]",
		"entries[12]",
		"entries 0-11",
		"entries 12-23",
		"entries 24-34",
	) {
		return false
	}
	if !strings.Contains(lower, "are:") {
		return false
	}
	if !containsAnySubstring(lower,
		"\n1. ",
		"\r\n1. ",
		"\n2. ",
		"\r\n2. ",
	) {
		return false
	}
	return true
}

func (e *NativeToolExecutor) handleFileList(ctx context.Context, input map[string]any) (map[string]any, error) {
	wd, scope, resolved, err := e.resolveInputPath(ctx, input, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}

	pattern := "*"
	if raw, ok := readString(input, "pattern"); ok && raw != "" {
		pattern = raw
	}
	recursive := readBool(input, "recursive", false)

	info, err := os.Stat(resolved)
	if err != nil {
		if isNotFoundLikePathError(err) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	renderedPath := renderPath(wd.Root(), resolved)
	if reject, blocked, rejectErr := e.rejectRecoveryTargetReread(ctx, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if blocked {
		reject["path"] = renderedPath
		return reject, nil
	}
	if reject, blocked, rejectErr := e.rejectExecutionFirstDeliverableReread(ctx, wd, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if blocked {
		reject["path"] = renderedPath
		return reject, nil
	}

	entries := make([]listedEntry, 0)
	truncated := false
	baseHidden := strings.HasPrefix(filepath.Base(resolved), ".")
	collect := func(entryPath string, d fs.DirEntry) error {
		if d.IsDir() && recursive && entryPath != resolved {
			if strings.HasPrefix(d.Name(), ".") && !baseHidden {
				return filepath.SkipDir
			}
		}
		if entryPath == resolved && d.IsDir() {
			return nil
		}
		relFromInput, relErr := filepath.Rel(resolved, entryPath)
		if relErr != nil {
			return relErr
		}
		relFromInput = filepath.ToSlash(relFromInput)
		if relFromInput == "." {
			relFromInput = d.Name()
		}
		if !globMatch(pattern, relFromInput) {
			return nil
		}

		entryInfo, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		entryType := "file"
		size := entryInfo.Size()
		if d.IsDir() {
			entryType = "directory"
			size = 0
		}
		entries = append(entries, listedEntry{
			name:       d.Name(),
			path:       renderPath(wd.Root(), entryPath),
			entryType:  entryType,
			sizeBytes:  size,
			modifiedAt: formatTime(entryInfo.ModTime()),
		})
		if len(entries) >= defaultListMaxEntries {
			truncated = true
			return io.EOF
		}
		return nil
	}

	if !info.IsDir() {
		d := fs.FileInfoToDirEntry(info)
		if err := collect(resolved, d); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
	} else if recursive {
		err := filepath.WalkDir(resolved, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			return collect(path, d)
		})
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
	} else {
		dirs, err := os.ReadDir(resolved)
		if err != nil {
			return nil, err
		}
		for _, d := range dirs {
			if err := collect(filepath.Join(resolved, d.Name()), d); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				if errors.Is(err, filepath.SkipDir) {
					continue
				}
				return nil, err
			}
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].path < entries[j].path
	})
	payload := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		payload = append(payload, map[string]any{
			"name":        entry.name,
			"path":        entry.path,
			"type":        entry.entryType,
			"size_bytes":  entry.sizeBytes,
			"modified_at": entry.modifiedAt,
		})
	}

	result := map[string]any{
		"entries": payload,
		"total":   len(payload),
	}
	if truncated {
		result["truncated"] = true
	}
	return result, nil
}

func (e *NativeToolExecutor) handleFileSearch(ctx context.Context, input map[string]any) (map[string]any, error) {
	wd, scope, resolved, err := e.resolveInputPath(ctx, input, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}

	pattern, ok := readString(input, "pattern")
	if !ok || pattern == "" {
		return map[string]any{"matches": []any{}, "total_matches": 0, "truncated": false}, nil
	}
	if readBool(input, "case_insensitive", false) {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	filePattern := "*"
	if raw, ok := readString(input, "file_pattern"); ok && raw != "" {
		filePattern = raw
	}
	recursive := readBool(input, "recursive", true)
	maxResults := clamp(readInt(input, "max_results", defaultSearchMaxResult), 1, hardSearchMaxResult)

	info, err := os.Stat(resolved)
	if err != nil {
		if isNotFoundLikePathError(err) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	renderedPath := renderPath(wd.Root(), resolved)
	if reject, blocked, rejectErr := e.rejectRecoveryTargetReread(ctx, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if blocked {
		reject["path"] = renderedPath
		return reject, nil
	}

	matches := make([]map[string]any, 0)
	totalMatches := 0
	truncated := false

	searchFile := func(filePath string) error {
		realPath, err := filepath.EvalSymlinks(filePath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !isWithinRoot(wd.Root(), realPath) {
			return nil
		}

		relPath, relErr := filepath.Rel(resolved, filePath)
		if relErr != nil {
			return relErr
		}
		relPath = filepath.ToSlash(relPath)
		if !globMatch(filePattern, relPath) {
			return nil
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil
		}
		renderedPath := renderPath(wd.Root(), filePath)
		if _, blocked, rejectErr := e.rejectPlaceholderDeliverableRead(ctx, scope, renderedPath, sanitizeUTF8TextBytes(data)); rejectErr != nil {
			return rejectErr
		} else if blocked {
			return nil
		}
		lines := splitLines(data)
		for idx, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			totalMatches++
			if len(matches) >= maxResults {
				truncated = true
				continue
			}
			beforeStart := idx - 2
			if beforeStart < 0 {
				beforeStart = 0
			}
			afterEnd := idx + 3
			if afterEnd > len(lines) {
				afterEnd = len(lines)
			}
			before := append([]string(nil), lines[beforeStart:idx]...)
			after := append([]string(nil), lines[idx+1:afterEnd]...)
			matches = append(matches, map[string]any{
				"file":           renderedPath,
				"line_number":    idx + 1,
				"line_content":   line,
				"context_before": before,
				"context_after":  after,
			})
		}
		return nil
	}

	if !info.IsDir() {
		if err := searchFile(resolved); err != nil {
			return nil, err
		}
	} else if recursive {
		err := filepath.WalkDir(resolved, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if path != resolved && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			return searchFile(path)
		})
		if err != nil {
			return nil, err
		}
	} else {
		dirs, err := os.ReadDir(resolved)
		if err != nil {
			return nil, err
		}
		for _, d := range dirs {
			if d.IsDir() {
				continue
			}
			if err := searchFile(filepath.Join(resolved, d.Name())); err != nil {
				return nil, err
			}
		}
	}

	return map[string]any{
		"matches":       matches,
		"total_matches": totalMatches,
		"truncated":     truncated,
	}, nil
}

func globMatch(pattern, candidate string) bool {
	pat := filepath.ToSlash(strings.TrimSpace(pattern))
	cand := filepath.ToSlash(strings.TrimSpace(candidate))
	if cand == "" || cand == "." {
		cand = path.Base(cand)
	}
	if pat == "" || pat == "*" {
		return true
	}
	if strings.Contains(pat, "**") {
		escaped := regexp.QuoteMeta(pat)
		escaped = strings.ReplaceAll(escaped, `\*\*`, ".*")
		escaped = strings.ReplaceAll(escaped, `\*`, "[^/]*")
		escaped = strings.ReplaceAll(escaped, `\?`, ".")
		re := regexp.MustCompile("^" + escaped + "$")
		return re.MatchString(cand)
	}
	matched, err := path.Match(pat, cand)
	if err == nil && matched {
		return true
	}
	matched, err = path.Match(pat, path.Base(cand))
	return err == nil && matched
}

func splitLines(data []byte) []string {
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	normalized = bytes.ToValidUTF8(normalized, utf8ReplacementBytes)
	scanner := bufio.NewScanner(bytes.NewReader(normalized))
	lines := make([]string, 0)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func sanitizeUTF8TextBytes(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	return string(bytes.ToValidUTF8(data, utf8ReplacementBytes))
}
