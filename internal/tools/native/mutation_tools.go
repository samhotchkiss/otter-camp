package native

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

var slugStripPattern = regexp.MustCompile(`[^a-z0-9\-]+`)

const (
	taskDoneTerminalNodeMessage   = "task can only be marked done when its flow reaches a terminal node"
	memoryRecordEmbeddingDims     = 1536
	memoryRecordDefaultConfidence = 0.85
	memoryRecordDefaultUtility    = 0.7
)

type executionActor struct {
	createdByType string
	createdByID   uuid.UUID
	createdByPtr  *uuid.UUID
	principalType string
	principalID   uuid.UUID
}

func actorFromContext(ctx context.Context) executionActor {
	execCtx := mcp.ExecutionContextFromContext(ctx)
	if execCtx.AgentID != nil && *execCtx.AgentID != uuid.Nil {
		agentID := *execCtx.AgentID
		return executionActor{
			createdByType: "agent",
			createdByID:   agentID,
			createdByPtr:  &agentID,
			principalType: "agent",
			principalID:   agentID,
		}
	}
	return executionActor{
		createdByType: "system",
		createdByID:   uuid.Nil,
		createdByPtr:  nil,
		principalType: "system",
		principalID:   uuid.Nil,
	}
}

func normalizeSlug(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	trimmed = strings.ReplaceAll(trimmed, "_", "-")
	trimmed = strings.ReplaceAll(trimmed, " ", "-")
	trimmed = slugStripPattern.ReplaceAllString(trimmed, "-")
	trimmed = strings.Trim(trimmed, "-")
	for strings.Contains(trimmed, "--") {
		trimmed = strings.ReplaceAll(trimmed, "--", "-")
	}
	if trimmed == "" {
		return "item-" + strings.ToLower(uuid.NewString()[:8])
	}
	return trimmed
}

func readStringSlice(input map[string]any, key string) []string {
	raw, ok := input[key]
	if !ok || raw == nil {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(item)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(fmt.Sprintf("%v", item))
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	default:
		return nil
	}
}

func decodePathStrings(ctx context.Context, wd SessionWorkDir, baseDir string, rawPaths []string) ([]string, error) {
	if len(rawPaths) == 0 {
		return nil, nil
	}
	_ = ctx
	out := make([]string, 0, len(rawPaths))
	for _, rawPath := range rawPaths {
		resolved, err := wd.ResolvePath(rawPath)
		if err != nil {
			if errors.Is(err, ErrPathTraversal) {
				return nil, ErrPathTraversal
			}
			return nil, err
		}
		rel, err := filepath.Rel(baseDir, resolved)
		if err != nil {
			return nil, err
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out, nil
}

func writeFileAtomically(target string, payload []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".ottercamp-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_ = tmp.Chmod(0o644)
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func hashSHA256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func deterministicMemoryEmbedding(value string) []float32 {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	vector := make([]float32, memoryRecordEmbeddingDims)
	vector[0] = float32(sum[0]) / 255.0
	vector[1] = 1
	for i := 0; i < len(sum) && i+2 < len(vector); i++ {
		vector[i+2] = float32(sum[i]) / 255.0
	}
	return vector
}

func (e *NativeToolExecutor) handleFileWrite(ctx context.Context, input map[string]any) (map[string]any, error) {
	wd, scope, resolved, err := e.resolveInputPath(ctx, input, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}
	pathInput, ok := readString(input, "path")
	if !ok || pathInput == "" {
		return map[string]any{"error": "path_required"}, nil
	}
	createDirs := readBool(input, "create_dirs", false)
	if createDirs {
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return nil, err
		}
	}

	content, _ := readString(input, "content")
	encoding := "utf8"
	payload := []byte(content)
	if rawEncoding, ok := readString(input, "encoding"); ok && strings.EqualFold(rawEncoding, "base64") {
		encoding = "base64"
		decoded, decodeErr := base64.StdEncoding.DecodeString(content)
		if decodeErr != nil {
			return map[string]any{"error": "invalid_base64"}, nil
		}
		payload = decoded
	}
	_ = encoding

	_, statErr := os.Stat(resolved)
	created := errors.Is(statErr, fs.ErrNotExist)
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return nil, statErr
	}
	if err := writeFileAtomically(resolved, payload); err != nil {
		return nil, err
	}

	if e.audit != nil {
		actor := actorFromContext(ctx)
		targetType := "file"
		targetID := uuid.New()
		_ = e.audit.Insert(ctx, repo.AuditEvent{
			OrganizationID: scope.organizationID,
			EventType:      "file_written",
			PrincipalType:  actor.principalType,
			PrincipalID:    actor.principalID,
			TargetType:     &targetType,
			TargetID:       &targetID,
			Metadata: map[string]any{
				"path":      renderPath(wd.Root(), resolved),
				"byte_size": len(payload),
				"created":   created,
			},
		})
	}

	return map[string]any{
		"path":      renderPath(wd.Root(), resolved),
		"byte_size": len(payload),
		"created":   created,
	}, nil
}

func (e *NativeToolExecutor) handleFileEdit(ctx context.Context, input map[string]any) (map[string]any, error) {
	wd, _, resolved, err := e.resolveInputPath(ctx, input, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}
	oldString, okOld := readString(input, "old_string")
	newString, _ := readString(input, "new_string")
	if !okOld || oldString == "" {
		return map[string]any{"error": "old_string_required"}, nil
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}

	replaceAll := readBool(input, "replace_all", false)
	text := string(content)
	count := strings.Count(text, oldString)
	if count == 0 {
		return map[string]any{"error": "old_string_not_found"}, nil
	}
	if !replaceAll && count > 1 {
		return map[string]any{"error": "ambiguous_match", "count": count}, nil
	}

	replaced := strings.Replace(text, oldString, newString, 1)
	replacements := 1
	if replaceAll {
		replaced = strings.ReplaceAll(text, oldString, newString)
		replacements = count
	}
	if err := writeFileAtomically(resolved, []byte(replaced)); err != nil {
		return nil, err
	}
	return map[string]any{
		"path":              renderPath(wd.Root(), resolved),
		"replacements_made": replacements,
	}, nil
}

func (e *NativeToolExecutor) handleFileDelete(ctx context.Context, input map[string]any) (map[string]any, error) {
	wd, _, resolved, err := e.resolveInputPath(ctx, input, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	if info.IsDir() {
		return map[string]any{"error": "is_directory"}, nil
	}
	if err := os.Remove(resolved); err != nil {
		return nil, err
	}
	return map[string]any{
		"path":    renderPath(wd.Root(), resolved),
		"deleted": true,
	}, nil
}

func (e *NativeToolExecutor) handleGitCommit(ctx context.Context, input map[string]any) (map[string]any, error) {
	wd, _, dir, err := e.resolveInputPath(ctx, input, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}

	branchOut, err := e.runCommand(ctx, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		if isNotGitRepoError(branchOut, err) {
			return map[string]any{"error": "not_a_git_repo"}, nil
		}
		return nil, err
	}
	branch := strings.TrimSpace(branchOut)
	// Only enforce branch protection when a remote is configured.
	// Local workspace repos don't have remotes, so agents must commit to main.
	if branch == "main" || branch == "master" {
		remoteOut, remoteErr := e.runCommand(ctx, dir, "git", "remote")
		hasRemote := remoteErr == nil && strings.TrimSpace(remoteOut) != ""
		if hasRemote {
			return map[string]any{"error": "cannot_commit_to_main"}, nil
		}
	}

	message, ok := readString(input, "message")
	if !ok || message == "" {
		return map[string]any{"error": "message_required"}, nil
	}

	emailOut, emailErr := e.runCommand(ctx, dir, "git", "config", "--get", "user.email")
	if emailErr != nil || strings.TrimSpace(emailOut) == "" {
		if _, err := e.runCommand(ctx, dir, "git", "config", "user.email", "agent@ottercamp.internal"); err != nil {
			return nil, err
		}
	}

	all := readBool(input, "all", false)
	paths := readStringSlice(input, "paths")
	switch {
	case all || len(paths) == 0:
		if _, err := e.runCommand(ctx, dir, "git", "add", "-A"); err != nil {
			return nil, err
		}
	default:
		decoded, err := decodePathStrings(ctx, wd, dir, paths)
		if err != nil {
			if errors.Is(err, ErrPathTraversal) {
				return map[string]any{"error": "path_traversal"}, nil
			}
			return nil, err
		}
		args := append([]string{"add", "--"}, decoded...)
		if _, err := e.runCommand(ctx, dir, "git", args...); err != nil {
			return nil, err
		}
	}

	if commitOut, err := e.runCommand(ctx, dir, "git", "commit", "-m", message); err != nil {
		return map[string]any{
			"error":   "commit_failed",
			"details": strings.TrimSpace(commitOut),
		}, nil
	}

	shaOut, err := e.runCommand(ctx, dir, "git", "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	sha := strings.TrimSpace(shaOut)
	shortSHA := sha
	if len(shortSHA) > 7 {
		shortSHA = shortSHA[:7]
	}
	showOut, _ := e.runCommand(ctx, dir, "git", "show", "--name-only", "--pretty=format:", "HEAD")
	filesCommitted := 0
	for _, line := range strings.Split(showOut, "\n") {
		if strings.TrimSpace(line) != "" {
			filesCommitted++
		}
	}

	return map[string]any{
		"sha":             sha,
		"short_sha":       shortSHA,
		"files_committed": filesCommitted,
		"message":         message,
	}, nil
}

func isProtectedPushBranch(branch string) bool {
	trimmed := strings.TrimSpace(branch)
	return trimmed == "main" || trimmed == "master" || strings.HasPrefix(trimmed, "shared/")
}

func (e *NativeToolExecutor) handleGitPush(ctx context.Context, input map[string]any) (map[string]any, error) {
	_, _, dir, err := e.resolveInputPath(ctx, input, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}

	remote, ok := readString(input, "remote")
	if !ok || remote == "" {
		remote = "origin"
	}
	branch, ok := readString(input, "branch")
	if !ok || branch == "" {
		out, err := e.runCommand(ctx, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			if isNotGitRepoError(out, err) {
				return map[string]any{"error": "not_a_git_repo"}, nil
			}
			return nil, err
		}
		branch = strings.TrimSpace(out)
	}

	force := readBool(input, "force", false)
	if force && isProtectedPushBranch(branch) {
		return map[string]any{
			"error":  "force_push_denied",
			"branch": branch,
		}, nil
	}

	commitsPushed := 0
	revListOut, revErr := e.runCommand(ctx, dir, "git", "rev-list", "--count", fmt.Sprintf("%s/%s..%s", remote, branch, branch))
	if revErr == nil {
		commitsPushed = readInt(map[string]any{"count": strings.TrimSpace(revListOut)}, "count", 0)
	}

	args := []string{"push"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, remote, branch)
	if pushOut, err := e.runCommand(ctx, dir, "git", args...); err != nil {
		return map[string]any{
			"error":   "push_failed",
			"details": strings.TrimSpace(pushOut),
		}, nil
	}

	return map[string]any{
		"remote":         remote,
		"branch":         branch,
		"commits_pushed": commitsPushed,
	}, nil
}

func (e *NativeToolExecutor) handleCLIExecute(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.cli == nil {
		return map[string]any{"error": "cli_executor_unavailable"}, nil
	}
	return e.cli.Execute(ctx, input)
}

func (e *NativeToolExecutor) handleMemoryRecord(ctx context.Context, input map[string]any) (map[string]any, error) {
	content, ok := readString(input, "content")
	if !ok || content == "" {
		return map[string]any{"error": "content_required"}, nil
	}
	scopeName, ok := readString(input, "scope")
	if !ok || scopeName == "" {
		scopeName = "org"
	}
	scopeName = strings.ToLower(strings.TrimSpace(scopeName))
	switch scopeName {
	case "org", "project", "task", "agent":
	default:
		scopeName = "org"
	}
	sensitivity, ok := readString(input, "sensitivity")
	if !ok || sensitivity == "" {
		sensitivity = "normal"
	}
	tags := readStringSlice(input, "tags")
	_ = tags

	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	if e.memoryRecorder != nil && scope.agentID != nil && *scope.agentID != uuid.Nil {
		memoryID, status, err := e.memoryRecorder.RecordExplicit(ctx, *scope.agentID, content, scopeName, sensitivity, tags)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"memory_id": memoryID,
			"status":    status,
		}, nil
	}
	if e.memories == nil {
		return map[string]any{"error": "memory_service_unavailable"}, nil
	}

	record := repo.Memory{
		OrganizationID: scope.organizationID,
		AgentID:        scope.agentID,
		MemoryType:     "episodic",
		Scope:          scopeName,
		Content:        content,
		ContentHash:    hashSHA256Hex(content),
		Embedding:      deterministicMemoryEmbedding(content),
		Confidence:     memoryRecordDefaultConfidence,
		UtilityScore:   memoryRecordDefaultUtility,
		Status:         "active",
		Sensitivity:    sensitivity,
		TrustTier:      0.8,
	}
	if scopeName == "project" && scope.projectID != nil {
		record.ProjectID = scope.projectID
	}
	if scopeName == "task" && scope.taskID != nil {
		record.ProjectTaskID = scope.taskID
		if scope.projectID != nil {
			record.ProjectID = scope.projectID
		}
	}
	if scopeName == "agent" {
		record.ProjectID = nil
		record.ProjectTaskID = nil
	}
	created, err := e.memories.Create(ctx, record)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"memory_id": created.ID,
		"status":    "stored",
	}, nil
}

func (e *NativeToolExecutor) handleProjectCreate(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.projects == nil {
		return map[string]any{"error": "project_repository_unavailable"}, nil
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	name, ok := readString(input, "name")
	if !ok || name == "" {
		return map[string]any{"error": "name_required"}, nil
	}
	slug, ok := readString(input, "slug")
	if !ok || slug == "" {
		slug = normalizeSlug(name)
	}
	description, _ := readString(input, "description")
	deliveryMode, ok := readString(input, "delivery_mode")
	if !ok || deliveryMode == "" {
		deliveryMode = "gated"
	}
	actor := actorFromContext(ctx)
	created, err := e.projects.Create(ctx, repo.Project{
		OrganizationID: scope.organizationID,
		Slug:           slug,
		DisplayName:    name,
		Description:    description,
		DeliveryMode:   deliveryMode,
		CreatedByType:  actor.createdByType,
		CreatedByID:    actor.createdByID,
		Settings:       json.RawMessage(`{"requires_pm_assignment_before_queue":true}`),
	})
	if err != nil {
		return nil, err
	}
	if e.events != nil {
		payload, marshalErr := json.Marshal(map[string]any{
			"project_id":                  created.ID,
			"slug":                        created.Slug,
			"requires_human_confirmation": true,
			"pm_required":                 true,
		})
		if marshalErr != nil {
			return nil, marshalErr
		}
		_ = e.events.Publish(ctx, nil, eventbus.DomainEvent{
			OrganizationID: scope.organizationID,
			EventType:      "project.created",
			ActorType:      actor.createdByType,
			ActorID:        actor.createdByPtr,
			Payload:        payload,
		})
		_ = e.events.Publish(ctx, nil, eventbus.DomainEvent{
			OrganizationID: scope.organizationID,
			EventType:      "project.staffing_needed",
			ActorType:      actor.createdByType,
			ActorID:        actor.createdByPtr,
			Payload:        payload,
		})
	}

	return map[string]any{
		"project": map[string]any{
			"id":     created.ID,
			"slug":   created.Slug,
			"name":   created.DisplayName,
			"status": "active",
		},
	}, nil
}

func (e *NativeToolExecutor) handleProjectUpdate(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.projects == nil {
		return map[string]any{"error": "project_repository_unavailable"}, nil
	}
	projectID, ok := readUUID(input, "project_id")
	if !ok || projectID == uuid.Nil {
		return map[string]any{"error": "project_id_required"}, nil
	}
	current, err := e.projects.GetByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	if name, ok := readString(input, "name"); ok && name != "" {
		current.DisplayName = name
	}
	if description, ok := readString(input, "description"); ok {
		current.Description = description
	}
	if deliveryMode, ok := readString(input, "delivery_mode"); ok && deliveryMode != "" {
		current.DeliveryMode = deliveryMode
	}
	updated, err := e.projects.Update(ctx, current)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"project": map[string]any{
			"id":     updated.ID,
			"slug":   updated.Slug,
			"name":   updated.DisplayName,
			"status": "active",
		},
	}, nil
}

func (e *NativeToolExecutor) handleProjectArchive(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.projects == nil {
		return map[string]any{"error": "project_repository_unavailable"}, nil
	}
	projectID, ok := readUUID(input, "project_id")
	if !ok || projectID == uuid.Nil {
		return map[string]any{"error": "project_id_required"}, nil
	}
	if err := e.projects.Archive(ctx, projectID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	return map[string]any{
		"project_id": projectID,
		"status":     "archived",
	}, nil
}

func (e *NativeToolExecutor) handleTaskCreate(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.tasks == nil {
		return map[string]any{"error": "task_repository_unavailable"}, nil
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	projectID, ok := readUUID(input, "project_id")
	if !ok || projectID == uuid.Nil {
		return map[string]any{"error": "project_id_required"}, nil
	}
	title, ok := readString(input, "title")
	if !ok || title == "" {
		return map[string]any{"error": "title_required"}, nil
	}
	var description *string
	if value, ok := readString(input, "description"); ok {
		description = &value
	}
	var flowTemplateID *uuid.UUID
	if value, ok := readUUID(input, "flow_template_id"); ok && value != uuid.Nil {
		hasReviewNode, reviewErr := e.flowTemplateHasReviewNode(ctx, value)
		if reviewErr != nil {
			return nil, reviewErr
		}
		if !hasReviewNode {
			return map[string]any{"error": "flow template must include at least one review node"}, nil
		}
		flowTemplateID = &value
	}
	requiresHumanReview := readBool(input, "requires_human_review", false)
	actor := actorFromContext(ctx)
	created, err := e.tasks.Create(ctx, repo.ProjectTask{
		OrganizationID:      scope.organizationID,
		ProjectID:           projectID,
		Title:               title,
		Description:         description,
		FlowTemplateID:      flowTemplateID,
		RequiresHumanReview: requiresHumanReview,
		CreatedByType:       actor.createdByType,
		CreatedByID:         actor.createdByPtr,
		Metadata:            json.RawMessage(`{}`),
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"task": map[string]any{
			"id":          created.ID,
			"task_number": created.TaskNumber,
			"work_status": created.WorkStatus,
		},
	}, nil
}

func (e *NativeToolExecutor) handleTaskUpdate(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.tasks == nil {
		return map[string]any{"error": "task_repository_unavailable"}, nil
	}
	taskID, ok := readUUID(input, "task_id")
	if !ok || taskID == uuid.Nil {
		return map[string]any{"error": "task_id_required"}, nil
	}
	current, err := e.tasks.GetByID(ctx, taskID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	if title, ok := readString(input, "title"); ok && title != "" {
		current.Title = title
	}
	if description, ok := readString(input, "description"); ok {
		current.Description = &description
	}
	if flowTemplateID, ok := readUUID(input, "flow_template_id"); ok && flowTemplateID != uuid.Nil {
		if !strings.EqualFold(strings.TrimSpace(current.WorkStatus), "draft") {
			return map[string]any{"error": "flow_template_id can only be changed while task is draft"}, nil
		}
		hasReviewNode, reviewErr := e.flowTemplateHasReviewNode(ctx, flowTemplateID)
		if reviewErr != nil {
			return nil, reviewErr
		}
		if !hasReviewNode {
			return map[string]any{"error": "flow template must include at least one review node"}, nil
		}
		current.FlowTemplateID = &flowTemplateID
	}
	if assignedAgentID, ok := readUUID(input, "assigned_agent_id"); ok && assignedAgentID != uuid.Nil {
		current.AssignedAgentID = &assignedAgentID
	}
	previousStatus := strings.TrimSpace(current.WorkStatus)
	statusChanged := false
	if status, ok := readString(input, "work_status"); ok && status != "" {
		if taskStatusRequiresFlowTemplate(status) && current.FlowTemplateID == nil {
			return map[string]any{"error": "task requires a flow template before it can be queued"}, nil
		}
		if strings.EqualFold(strings.TrimSpace(status), "queued") && current.FlowTemplateID != nil {
			hasReviewNode, reviewErr := e.flowTemplateHasReviewNode(ctx, *current.FlowTemplateID)
			if reviewErr != nil {
				return nil, reviewErr
			}
			if !hasReviewNode {
				return map[string]any{"error": "flow template must include at least one review node"}, nil
			}
		}
		if strings.EqualFold(strings.TrimSpace(current.WorkStatus), "draft") &&
			strings.EqualFold(strings.TrimSpace(status), "queued") &&
			e.projectRequiresPMBeforeQueue(ctx, current.ProjectID) &&
			!e.projectHasActivePM(ctx, current.ProjectID) {
			return map[string]any{"error": "project has no active PM assignment"}, nil
		}
		if strings.EqualFold(strings.TrimSpace(status), "done") {
			if err := e.validateTaskDoneTransition(ctx, current); err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
		}
		if !strings.EqualFold(previousStatus, strings.TrimSpace(status)) {
			statusChanged = true
		}
		current.WorkStatus = status
	}
	updated, err := e.tasks.Update(ctx, current)
	if err != nil {
		return nil, err
	}
	if statusChanged {
		eventTask := current
		eventTask.WorkStatus = previousStatus
		if err := e.publishTaskStatusEvents(ctx, nil, eventTask, strings.TrimSpace(updated.WorkStatus)); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"task": map[string]any{
			"id":          updated.ID,
			"task_number": updated.TaskNumber,
			"work_status": updated.WorkStatus,
		},
	}, nil
}

func (e *NativeToolExecutor) projectRequiresPMBeforeQueue(ctx context.Context, projectID uuid.UUID) bool {
	if e.projects == nil {
		return false
	}
	projectRecord, err := e.projects.GetByID(ctx, projectID)
	if err != nil {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal(projectRecord.Settings, &payload); err != nil {
		return false
	}
	raw, ok := payload["requires_pm_assignment_before_queue"]
	if !ok {
		return false
	}
	flag, ok := raw.(bool)
	return ok && flag
}

func (e *NativeToolExecutor) projectHasActivePM(ctx context.Context, projectID uuid.UUID) bool {
	if e.assignments == nil {
		return false
	}
	assignments, err := e.assignments.ListByProject(ctx, projectID)
	if err != nil {
		return false
	}
	for _, assignment := range assignments {
		if !assignment.IsActive {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(assignment.Role), "pm") {
			return true
		}
	}
	return false
}

func (e *NativeToolExecutor) validateTaskDoneTransition(ctx context.Context, taskRecord repo.ProjectTask) error {
	if taskRecord.FlowTemplateID == nil || taskRecord.CurrentFlowNodeID == nil {
		return errors.New(taskDoneTerminalNodeMessage)
	}
	if e.flowNodes == nil || e.flowExecs == nil {
		return errors.New(taskDoneTerminalNodeMessage)
	}
	node, err := e.flowNodes.GetByID(ctx, *taskRecord.CurrentFlowNodeID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return errors.New(taskDoneTerminalNodeMessage)
		}
		return err
	}
	if node.NextNodeID != nil {
		return errors.New(taskDoneTerminalNodeMessage)
	}
	executions, err := e.flowExecs.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return errors.New(taskDoneTerminalNodeMessage)
		}
		return err
	}
	for _, execution := range executions {
		if execution.FlowNodeID != node.ID {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(execution.Status), "completed") {
			return nil
		}
	}
	return errors.New(taskDoneTerminalNodeMessage)
}

func (e *NativeToolExecutor) flowTemplateHasReviewNode(ctx context.Context, flowTemplateID uuid.UUID) (bool, error) {
	if flowTemplateID == uuid.Nil || e.flowNodes == nil {
		return true, nil
	}
	nodes, err := e.flowNodes.GetByTemplateOrdered(ctx, flowTemplateID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return true, nil
		}
		return false, err
	}
	if len(nodes) == 0 {
		return false, nil
	}
	for _, node := range nodes {
		if strings.EqualFold(strings.TrimSpace(node.NodeType), "review") {
			return true, nil
		}
	}
	return false, nil
}

func taskStatusRequiresFlowTemplate(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "in_progress", "review", "done":
		return true
	default:
		return false
	}
}

func (e *NativeToolExecutor) handleTaskAddDependency(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.dependencies == nil {
		return map[string]any{"error": "dependency_repository_unavailable"}, nil
	}
	sourceType, _ := readString(input, "source_type")
	dependsOnType, _ := readString(input, "depends_on_type")
	sourceID, okSource := readUUID(input, "source_id")
	dependsOnID, okDepends := readUUID(input, "depends_on_id")
	if !okSource || !okDepends || sourceID == uuid.Nil || dependsOnID == uuid.Nil {
		return map[string]any{"error": "invalid_dependency"}, nil
	}
	if strings.TrimSpace(sourceType) != strings.TrimSpace(dependsOnType) {
		return map[string]any{"error": "cross_level_dependency"}, nil
	}
	if sourceID == dependsOnID {
		return map[string]any{"error": "self_dependency"}, nil
	}
	hasCycle, err := e.dependencies.CheckCycle(ctx, sourceType, sourceID, dependsOnID)
	if err != nil {
		return nil, err
	}
	if hasCycle {
		return map[string]any{"error": "cycle_detected"}, nil
	}
	actor := actorFromContext(ctx)
	created, err := e.dependencies.Add(ctx, repo.ProjectTaskDependency{
		SourceType:    sourceType,
		SourceID:      sourceID,
		DependsOnType: dependsOnType,
		DependsOnID:   dependsOnID,
		CreatedByType: actor.createdByType,
		CreatedByID:   actor.createdByPtr,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"dependency_id": created.ID}, nil
}

func (e *NativeToolExecutor) handleTaskRemoveDependency(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.dependencies == nil {
		return map[string]any{"error": "dependency_repository_unavailable"}, nil
	}
	dependencyID, ok := readUUID(input, "dependency_id")
	if !ok || dependencyID == uuid.Nil {
		return map[string]any{"error": "dependency_id_required"}, nil
	}
	if err := e.dependencies.Remove(ctx, dependencyID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"removed": false, "error": "not_found"}, nil
		}
		return nil, err
	}
	return map[string]any{"removed": true}, nil
}

func (e *NativeToolExecutor) handleSubtaskCreate(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.subtasks == nil || e.flowExecs == nil {
		return map[string]any{"error": "subtask_repository_unavailable"}, nil
	}
	flowNodeExecutionID, ok := readUUID(input, "flow_node_execution_id")
	if !ok || flowNodeExecutionID == uuid.Nil {
		return map[string]any{"error": "flow_node_execution_id_required"}, nil
	}
	title, ok := readString(input, "title")
	if !ok || title == "" {
		return map[string]any{"error": "title_required"}, nil
	}
	execution, err := e.flowExecs.GetByID(ctx, flowNodeExecutionID)
	if err != nil {
		return nil, err
	}
	var description *string
	if value, ok := readString(input, "description"); ok {
		description = &value
	}
	var assigneeType *string
	if value, ok := readString(input, "assignee_type"); ok && value != "" {
		assigneeType = &value
	}
	var assigneeID *uuid.UUID
	if value, ok := readUUID(input, "assignee_id"); ok && value != uuid.Nil {
		assigneeID = &value
	}
	actor := actorFromContext(ctx)
	created, err := e.subtasks.Create(ctx, repo.ProjectSubtask{
		TaskID:              execution.TaskID,
		FlowNodeExecutionID: flowNodeExecutionID,
		Title:               title,
		Description:         description,
		AssigneeType:        assigneeType,
		AssigneeID:          assigneeID,
		CreatedByType:       actor.createdByType,
		CreatedByID:         actor.createdByPtr,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"subtask": map[string]any{
			"id":              created.ID,
			"status":          created.WorkStatus,
			"sequence_number": created.SequenceNumber,
		},
	}, nil
}

func (e *NativeToolExecutor) handleSubtaskUpdate(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.subtasks == nil {
		return map[string]any{"error": "subtask_repository_unavailable"}, nil
	}
	subtaskID, ok := readUUID(input, "subtask_id")
	if !ok || subtaskID == uuid.Nil {
		return map[string]any{"error": "subtask_id_required"}, nil
	}
	if status, ok := readString(input, "status"); ok && status != "" {
		if _, err := e.subtasks.UpdateStatus(ctx, subtaskID, status); err != nil {
			return nil, err
		}
	}
	if e.pool != nil {
		title, hasTitle := readString(input, "title")
		description, hasDescription := readString(input, "description")
		if hasTitle || hasDescription {
			_, err := e.pool.Exec(ctx, `
				UPDATE project_subtask
				SET
					title = CASE WHEN $2::text = '' THEN title ELSE $2::text END,
					description = CASE WHEN $3::text = '' THEN description ELSE $3::text END
				WHERE id = $1
			`, subtaskID, title, description)
			if err != nil {
				return nil, err
			}
		}
	}
	updated, err := e.subtasks.GetByID(ctx, subtaskID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"subtask": map[string]any{
			"id":              updated.ID,
			"status":          updated.WorkStatus,
			"sequence_number": updated.SequenceNumber,
		},
	}, nil
}

func (e *NativeToolExecutor) completeFlowTask(ctx context.Context, taskID uuid.UUID) (map[string]any, error) {
	if e.tasks == nil {
		return map[string]any{"error": "task_repository_unavailable"}, nil
	}
	if e.pool != nil {
		return e.completeFlowTaskTx(ctx, taskID)
	}

	current, err := e.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	targetStatus := "done"
	if current.RequiresHumanReview {
		targetStatus = "review"
	}
	if _, err := e.tasks.SetFlowNode(ctx, taskID, nil); err != nil {
		return nil, err
	}
	if _, err := e.tasks.UpdateStatus(ctx, taskID, targetStatus); err != nil {
		return nil, err
	}
	if err := e.publishTaskStatusEvents(ctx, nil, current, targetStatus); err != nil {
		return nil, err
	}

	return map[string]any{
		"advanced_to_node_id": nil,
		"flow_completed":      true,
	}, nil
}

func (e *NativeToolExecutor) completeFlowTaskTx(ctx context.Context, taskID uuid.UUID) (map[string]any, error) {
	tx, err := e.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	current, err := e.getTaskForTerminalAdvanceTx(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}

	targetStatus := "done"
	if current.RequiresHumanReview {
		targetStatus = "review"
	}

	commandTag, err := tx.Exec(ctx, `
		UPDATE project_task
		SET
			current_flow_node_id = NULL,
			work_status = $2,
			completed_at = CASE WHEN $2 IN ('done', 'cancelled') THEN now() ELSE NULL END
		WHERE id = $1
	`, taskID, targetStatus)
	if err != nil {
		return nil, err
	}
	if commandTag.RowsAffected() == 0 {
		return nil, repo.ErrNotFound
	}

	if err := e.publishTaskStatusEvents(ctx, tx, current, targetStatus); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return map[string]any{
		"advanced_to_node_id": nil,
		"flow_completed":      true,
	}, nil
}

func (e *NativeToolExecutor) getTaskForTerminalAdvanceTx(ctx context.Context, tx pgx.Tx, taskID uuid.UUID) (repo.ProjectTask, error) {
	row := tx.QueryRow(ctx, `
		SELECT
			id,
			organization_id,
			project_id,
			work_status,
			requires_human_review
		FROM project_task
		WHERE id = $1
		FOR UPDATE
	`, taskID)

	var task repo.ProjectTask
	if err := row.Scan(
		&task.ID,
		&task.OrganizationID,
		&task.ProjectID,
		&task.WorkStatus,
		&task.RequiresHumanReview,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repo.ProjectTask{}, repo.ErrNotFound
		}
		return repo.ProjectTask{}, err
	}
	return task, nil
}

func (e *NativeToolExecutor) publishTaskStatusEvents(ctx context.Context, tx pgx.Tx, task repo.ProjectTask, targetStatus string) error {
	if e.events == nil {
		return nil
	}
	actorType, actorID := domainActorFromExecutionActor(actorFromContext(ctx))

	payload := map[string]any{
		"from_status": strings.TrimSpace(task.WorkStatus),
		"to_status":   strings.TrimSpace(targetStatus),
		"task_id":     task.ID,
		"project_id":  task.ProjectID,
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := e.events.Publish(ctx, tx, eventbus.DomainEvent{
		OrganizationID: task.OrganizationID,
		EventType:      "task.status_changed",
		ActorType:      actorType,
		ActorID:        actorID,
		Payload:        encodedPayload,
	}); err != nil {
		return err
	}

	if strings.TrimSpace(targetStatus) == "done" {
		if err := e.events.Publish(ctx, tx, eventbus.DomainEvent{
			OrganizationID: task.OrganizationID,
			EventType:      "task.completed",
			ActorType:      actorType,
			ActorID:        actorID,
			Payload:        encodedPayload,
		}); err != nil {
			return err
		}
	}

	return nil
}

func domainActorFromExecutionActor(actor executionActor) (string, *uuid.UUID) {
	switch strings.TrimSpace(actor.principalType) {
	case "agent":
		id := actor.principalID
		if id == uuid.Nil {
			return "system", nil
		}
		return "agent", &id
	case "supervisor":
		return "supervisor", nil
	case "human_user":
		id := actor.principalID
		if id == uuid.Nil {
			return "system", nil
		}
		return "human", &id
	default:
		return "system", nil
	}
}

func flowActorFromExecutionActor(actor executionActor) flowsvc.Actor {
	return flowsvc.Actor{
		Type: strings.TrimSpace(actor.principalType),
		ID:   actor.principalID,
	}
}

func (e *NativeToolExecutor) advanceExecutionToNode(ctx context.Context, taskID uuid.UUID, nextNodeID *uuid.UUID) (map[string]any, error) {
	if nextNodeID == nil {
		return e.completeFlowTask(ctx, taskID)
	}
	if _, err := e.tasks.SetFlowNode(ctx, taskID, nextNodeID); err != nil {
		return nil, err
	}
	if _, err := e.flowExecs.Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskID,
		FlowNodeID:  *nextNodeID,
		VisitNumber: 1,
		Status:      "active",
	}); err != nil {
		return nil, err
	}
	return map[string]any{
		"advanced_to_node_id": *nextNodeID,
		"flow_completed":      false,
	}, nil
}

func (e *NativeToolExecutor) handleFlowAdvance(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.flowService == nil {
		if e.flowServiceErr != nil {
			return nil, e.flowServiceErr
		}
		return map[string]any{"error": "flow_repository_unavailable"}, nil
	}
	flowNodeExecutionID, _ := readUUID(input, "flow_node_execution_id")
	taskID, err := e.resolveFlowAdvanceTaskID(ctx, flowNodeExecutionID)
	if err != nil {
		return nil, err
	}
	originalTask, loadOriginalTaskErr := repo.ProjectTask{}, error(nil)
	if e.tasks != nil {
		originalTask, loadOriginalTaskErr = e.tasks.GetByID(ctx, taskID)
	}
	if commitSHA, ok := readString(input, "commit_sha"); ok && commitSHA != "" {
		if _, err := e.flowService.RecordNodeCommit(ctx, taskID, commitSHA, ""); err != nil {
			return nil, err
		}
	}
	execution, err := e.flowService.AdvanceFlow(ctx, taskID, flowActorFromExecutionActor(actorFromContext(ctx)))
	if err != nil {
		if e.tasks != nil && loadOriginalTaskErr == nil {
			currentTask, currentTaskErr := e.tasks.GetByID(ctx, taskID)
			if currentTaskErr == nil {
				currentNodeID, originalNodeID := currentTask.CurrentFlowNodeID, originalTask.CurrentFlowNodeID
				nodeUnchanged := (currentNodeID == nil && originalNodeID == nil) ||
					(currentNodeID != nil && originalNodeID != nil && *currentNodeID == *originalNodeID)
				if currentTask.WorkStatus != originalTask.WorkStatus || !nodeUnchanged {
					_, _ = e.tasks.UpdateStatus(ctx, taskID, originalTask.WorkStatus)
					_, _ = e.tasks.SetFlowNode(ctx, taskID, originalTask.CurrentFlowNodeID)
				}
			}
		}
		return nil, err
	}

	flowCompleted := strings.EqualFold(strings.TrimSpace(execution.Status), "completed")
	var advancedToNodeID any
	if !flowCompleted {
		advancedToNodeID = execution.FlowNodeID
	}
	return map[string]any{
		"advanced_to_node_id": advancedToNodeID,
		"flow_completed":      flowCompleted,
	}, nil
}

func (e *NativeToolExecutor) resolveFlowAdvanceTaskID(ctx context.Context, flowNodeExecutionID uuid.UUID) (uuid.UUID, error) {
	if e.flowExecs != nil {
		execution, err := e.flowExecs.GetByID(ctx, flowNodeExecutionID)
		if err == nil {
			return execution.TaskID, nil
		}
		if !errors.Is(err, repo.ErrNotFound) {
			return uuid.Nil, err
		}
	}

	scope, err := e.resolveScope(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if scope.taskID == nil || *scope.taskID == uuid.Nil {
		return uuid.Nil, repo.ErrNotFound
	}
	return *scope.taskID, nil
}

func (e *NativeToolExecutor) handleFlowReviewDecision(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.flowExecs == nil || e.flowNodes == nil || e.tasks == nil {
		return map[string]any{"error": "flow_repository_unavailable"}, nil
	}
	flowNodeExecutionID, ok := readUUID(input, "flow_node_execution_id")
	if !ok || flowNodeExecutionID == uuid.Nil {
		return map[string]any{"error": "flow_node_execution_id_required"}, nil
	}
	decision, _ := readString(input, "decision")
	if decision != "approve" && decision != "reject" {
		return map[string]any{"error": "invalid_decision"}, nil
	}
	execution, err := e.flowExecs.GetByID(ctx, flowNodeExecutionID)
	if err != nil {
		return nil, err
	}
	currentNode, err := e.flowNodes.GetByID(ctx, execution.FlowNodeID)
	if err != nil {
		return nil, err
	}
	if decision == "approve" {
		if _, err := e.flowExecs.Complete(ctx, flowNodeExecutionID); err != nil {
			return nil, err
		}
		result, err := e.advanceExecutionToNode(ctx, execution.TaskID, currentNode.NextNodeID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"next_node_id": result["advanced_to_node_id"]}, nil
	}

	if _, err := e.flowExecs.Reject(ctx, flowNodeExecutionID); err != nil {
		return nil, err
	}
	if _, err := e.tasks.SetFlowNode(ctx, execution.TaskID, currentNode.RejectNodeID); err != nil {
		return nil, err
	}
	if currentNode.RejectNodeID != nil {
		if _, err := e.flowExecs.Create(ctx, repo.FlowNodeExecution{
			TaskID:      execution.TaskID,
			FlowNodeID:  *currentNode.RejectNodeID,
			VisitNumber: 1,
			Status:      "active",
		}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"next_node_id": currentNode.RejectNodeID}, nil
}

func (e *NativeToolExecutor) handleFlowCreateTemplate(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.flowTemplates == nil || e.flowNodes == nil {
		return map[string]any{"error": "flow_repository_unavailable"}, nil
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	projectID, ok := readUUID(input, "project_id")
	if !ok || projectID == uuid.Nil {
		return map[string]any{"error": "project_id_required"}, nil
	}
	name, ok := readString(input, "name")
	if !ok || name == "" {
		return map[string]any{"error": "name_required"}, nil
	}
	nodesRaw, hasNodes := input["nodes"].([]any)
	if !hasNodes || len(nodesRaw) == 0 {
		return map[string]any{"error": "flow template must include at least one review node"}, nil
	}
	declaredReviewNode := false
	for _, item := range nodesRaw {
		nodeMap, mapOK := item.(map[string]any)
		if !mapOK {
			continue
		}
		nodeType, typeOK := readString(nodeMap, "node_type")
		if !typeOK || nodeType == "" {
			nodeType = "work"
		}
		if strings.EqualFold(strings.TrimSpace(nodeType), "review") {
			declaredReviewNode = true
			break
		}
	}
	if !declaredReviewNode {
		return map[string]any{"error": "flow template must include at least one review node"}, nil
	}

	actor := actorFromContext(ctx)
	orgID := scope.organizationID
	template, err := e.flowTemplates.Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           normalizeSlug(name),
		DisplayName:    name,
		Description:    name,
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  actor.createdByType,
		CreatedByID:    actor.createdByID,
	})
	if err != nil {
		return nil, err
	}

	createdNodes := make([]repo.FlowNode, 0)
	hasReviewNode := false
	for idx, item := range nodesRaw {
		nodeMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		displayName, ok := readString(nodeMap, "display_name")
		if !ok || displayName == "" {
			displayName = fmt.Sprintf("Node %d", idx+1)
		}
		nodeType, ok := readString(nodeMap, "node_type")
		if !ok || nodeType == "" {
			nodeType = "work"
		}
		if strings.EqualFold(strings.TrimSpace(nodeType), "review") {
			hasReviewNode = true
		}
		created, err := e.flowNodes.Create(ctx, repo.FlowNode{
			FlowTemplateID: template.ID,
			DisplayName:    displayName,
			NodeType:       nodeType,
			Position:       idx + 1,
			ToolDomains:    readStringSlice(nodeMap, "tool_domains"),
			MaxVisits:      10,
		})
		if err != nil {
			return nil, err
		}
		createdNodes = append(createdNodes, created)
	}
	if len(createdNodes) == 0 || !hasReviewNode {
		return map[string]any{"error": "flow template must include at least one review node"}, nil
	}

	for i := 0; i < len(createdNodes)-1; i++ {
		node := createdNodes[i]
		node.NextNodeID = &createdNodes[i+1].ID
		if _, err := e.flowNodes.Update(ctx, node); err != nil {
			return nil, err
		}
	}

	template.StartNodeID = &createdNodes[0].ID
	updatedTemplate, err := e.flowTemplates.Update(ctx, template)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"template": map[string]any{
			"id":      updatedTemplate.ID,
			"version": updatedTemplate.Version,
		},
	}, nil
}

func (e *NativeToolExecutor) handleScheduleCreate(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.schedules == nil {
		return map[string]any{"error": "schedule_repository_unavailable"}, nil
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	projectID, ok := readUUID(input, "project_id")
	if !ok || projectID == uuid.Nil {
		return map[string]any{"error": "project_id_required"}, nil
	}
	flowTemplateID, ok := readUUID(input, "flow_template_id")
	if !ok || flowTemplateID == uuid.Nil {
		return map[string]any{"error": "flow_template_id_required"}, nil
	}
	cronExpression, ok := readString(input, "cron")
	if !ok || cronExpression == "" {
		return map[string]any{"error": "cron_required"}, nil
	}
	overlapPolicy, ok := readString(input, "overlap_policy")
	if !ok || overlapPolicy == "" {
		overlapPolicy = "skip"
	}
	maxDuration := int64(readInt(input, "max_duration_ms", 0))
	var maxDurationPtr *int64
	if maxDuration > 0 {
		maxDurationPtr = &maxDuration
	}
	actor := actorFromContext(ctx)
	created, err := e.schedules.Create(ctx, repo.TaskSchedule{
		OrganizationID: scope.organizationID,
		ProjectID:      projectID,
		FlowTemplateID: flowTemplateID,
		DisplayName:    "Schedule " + strings.ToLower(uuid.NewString()[:8]),
		CronExpression: cronExpression,
		OverlapPolicy:  overlapPolicy,
		MaxDurationMS:  maxDurationPtr,
		IsEnabled:      true,
		CreatedByType:  actor.createdByType,
		CreatedByID:    actor.createdByID,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"schedule": map[string]any{
			"id":           created.ID,
			"cron":         created.CronExpression,
			"next_fire_at": toRFC3339(created.NextFireAt),
		},
	}, nil
}

func (e *NativeToolExecutor) handleScheduleUpdate(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.schedules == nil {
		return map[string]any{"error": "schedule_repository_unavailable"}, nil
	}
	scheduleID, ok := readUUID(input, "schedule_id")
	if !ok || scheduleID == uuid.Nil {
		return map[string]any{"error": "schedule_id_required"}, nil
	}
	current, err := e.schedules.GetByID(ctx, scheduleID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	if cron, ok := readString(input, "cron"); ok && cron != "" {
		current.CronExpression = cron
	}
	if overlapPolicy, ok := readString(input, "overlap_policy"); ok && overlapPolicy != "" {
		current.OverlapPolicy = overlapPolicy
	}
	updated, err := e.schedules.Update(ctx, current)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"schedule": map[string]any{
			"id":           updated.ID,
			"cron":         updated.CronExpression,
			"next_fire_at": toRFC3339(updated.NextFireAt),
		},
	}, nil
}

func (e *NativeToolExecutor) handleScheduleDelete(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.schedules == nil {
		return map[string]any{"error": "schedule_repository_unavailable"}, nil
	}
	scheduleID, ok := readUUID(input, "schedule_id")
	if !ok || scheduleID == uuid.Nil {
		return map[string]any{"error": "schedule_id_required"}, nil
	}
	if err := e.schedules.Delete(ctx, scheduleID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"deleted": false, "error": "not_found"}, nil
		}
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

func normalizeProjectAssignmentRole(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pm", "worker", "reviewer", "observer":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func projectAssignmentRoleRequiresDedicatedAgent(role string) bool {
	switch role {
	case "pm", "worker", "reviewer":
		return true
	default:
		return false
	}
}

func (e *NativeToolExecutor) handleAgentAssignProject(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.assignments == nil {
		return map[string]any{"error": "assignment_repository_unavailable"}, nil
	}
	if e.agents == nil {
		return map[string]any{"error": "agent_repository_unavailable"}, nil
	}

	agentID, ok := readUUID(input, "agent_id")
	if !ok || agentID == uuid.Nil {
		return map[string]any{"error": "agent_id_required"}, nil
	}
	projectID, ok := readUUID(input, "project_id")
	if !ok || projectID == uuid.Nil {
		return map[string]any{"error": "project_id_required"}, nil
	}
	roleRaw, ok := readString(input, "role")
	if !ok || strings.TrimSpace(roleRaw) == "" {
		return map[string]any{"error": "role_required"}, nil
	}
	role := normalizeProjectAssignmentRole(roleRaw)
	if role == "" {
		return map[string]any{"error": "invalid_role"}, nil
	}

	agentRecord, err := e.agents.GetByID(ctx, agentID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	if projectAssignmentRoleRequiresDedicatedAgent(role) && agentRecord.IsStarterTrio {
		return map[string]any{"error": "starter_trio_cannot_be_assigned"}, nil
	}

	actor := actorFromContext(ctx)
	assignment, err := e.assignments.Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        agentID,
		ProjectID:      projectID,
		Role:           role,
		AssignedByType: actor.createdByType,
		AssignedByID:   actor.createdByPtr,
	})
	if err != nil {
		if errors.Is(err, repo.ErrPMConflict) {
			return map[string]any{"error": "pm_conflict"}, nil
		}
		if errors.Is(err, repo.ErrConflict) {
			return map[string]any{"error": "invalid_reference"}, nil
		}
		return nil, err
	}

	return map[string]any{
		"assignment": map[string]any{
			"assignment_id":    assignment.ID,
			"agent_id":         assignment.AgentID,
			"project_id":       assignment.ProjectID,
			"role":             assignment.Role,
			"is_active":        assignment.IsActive,
			"assigned_by_type": assignment.AssignedByType,
			"assigned_by_id":   assignment.AssignedByID,
			"assigned_at":      toRFC3339(&assignment.AssignedAt),
		},
	}, nil
}

func (e *NativeToolExecutor) handleAgentCreateTemp(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.agents == nil {
		return map[string]any{"error": "agent_repository_unavailable"}, nil
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	name, ok := readString(input, "name")
	if !ok || name == "" {
		return map[string]any{"error": "name_required"}, nil
	}
	systemPrompt, _ := readString(input, "system_prompt")
	scopeType, _ := readString(input, "scope_type")
	if scopeType != "project" {
		return map[string]any{"error": "scope_type_must_be_project"}, nil
	}
	projectID, ok := readUUID(input, "scope_id")
	if !ok || projectID == uuid.Nil {
		return map[string]any{"error": "scope_id_required"}, nil
	}
	ttlSeconds := int32(readInt(input, "ttl_seconds", 0))
	var ttlPtr *int32
	if ttlSeconds > 0 {
		ttlPtr = &ttlSeconds
	}
	actor := actorFromContext(ctx)
	created, err := e.agents.Create(ctx, repo.Agent{
		OrganizationID:       scope.organizationID,
		DisplayName:          name,
		AgentClass:           "temp",
		LifecycleStatus:      "active",
		SystemPrompt:         systemPrompt,
		OperatorInstructions: "",
		AgentType:            "general",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		TempProjectID:        &projectID,
		TempTTLSeconds:       ttlPtr,
		CreatedByType:        actor.createdByType,
		CreatedByID:          actor.createdByID,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"agent": map[string]any{
			"id":               created.ID,
			"name":             created.DisplayName,
			"lifecycle_status": created.LifecycleStatus,
		},
	}, nil
}

func (e *NativeToolExecutor) handleAgentUpdate(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.agents == nil {
		return map[string]any{"error": "agent_repository_unavailable"}, nil
	}
	agentID, ok := readUUID(input, "agent_id")
	if !ok || agentID == uuid.Nil {
		return map[string]any{"error": "agent_id_required"}, nil
	}
	current, err := e.agents.GetByID(ctx, agentID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	if systemPrompt, ok := readString(input, "system_prompt"); ok {
		current.SystemPrompt = systemPrompt
	}
	if operatorInstructions, ok := readString(input, "operator_instructions"); ok {
		current.OperatorInstructions = operatorInstructions
	}
	if allow := readStringSlice(input, "tool_allow_list"); allow != nil {
		current.ToolAllowList = allow
	}
	if deny := readStringSlice(input, "tool_deny_list"); deny != nil {
		current.ToolDenyList = deny
	}
	updated, err := e.agents.Update(ctx, current)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"agent": map[string]any{
			"id":               updated.ID,
			"name":             updated.DisplayName,
			"lifecycle_status": updated.LifecycleStatus,
		},
	}, nil
}

func (e *NativeToolExecutor) handleSessionCreate(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.chatSessions == nil {
		return map[string]any{"error": "chat_repository_unavailable"}, nil
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	scopeType, ok := readString(input, "scope_type")
	if !ok || scopeType == "" {
		return map[string]any{"error": "scope_type_required"}, nil
	}
	scopeID, ok := readUUID(input, "scope_id")
	if !ok || scopeID == uuid.Nil {
		return map[string]any{"error": "scope_id_required"}, nil
	}
	mode, ok := readString(input, "mode")
	if !ok || mode == "" {
		mode = "async"
	}
	title, _ := readString(input, "title")
	var titlePtr *string
	if title != "" {
		titlePtr = &title
	}
	actor := actorFromContext(ctx)
	created, err := e.chatSessions.Create(ctx, repo.ChatSession{
		OrganizationID: scope.organizationID,
		ScopeType:      scopeType,
		ScopeID:        scopeID,
		Mode:           mode,
		Title:          titlePtr,
		Status:         "active",
		CreatedByType:  actor.createdByType,
		CreatedByID:    actor.createdByID,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		return nil, err
	}

	// Auto-add project-assigned agents as participants for project-scoped sessions.
	// Workers are added before PMs so that resolveFirstAgentParticipant picks
	// the worker as the primary responder.
	var participants []map[string]any
	if (scopeType == "project" || scopeType == "project_task") && e.assignments != nil && e.participants != nil {
		participants = e.autoAddProjectParticipants(ctx, created.ID, scopeType, scopeID)
	}

	result := map[string]any{
		"session": map[string]any{
			"id":     created.ID,
			"status": created.Status,
			"mode":   created.Mode,
		},
	}
	if len(participants) > 0 {
		result["auto_participants"] = participants
	}
	return result, nil
}

// autoAddProjectParticipants looks up agents assigned to the project and adds
// them as session participants. Workers are added first so that
// resolveFirstAgentParticipant selects the worker as the primary responder.
func (e *NativeToolExecutor) autoAddProjectParticipants(ctx context.Context, sessionID uuid.UUID, scopeType string, scopeID uuid.UUID) []map[string]any {
	// For project_task scope, resolve the project ID from the task.
	projectID := scopeID
	if scopeType == "project_task" && e.tasks != nil {
		task, err := e.tasks.GetByID(ctx, scopeID)
		if err != nil {
			return nil
		}
		projectID = task.ProjectID
	}

	assignments, err := e.assignments.ListByProject(ctx, projectID)
	if err != nil || len(assignments) == 0 {
		return nil
	}

	// Sort: workers first, then PMs, then others — so the worker becomes
	// the primary responder via resolveFirstAgentParticipant.
	roleOrder := map[string]int{"worker": 0, "reviewer": 1, "pm": 2, "observer": 3}
	sortedAssignments := make([]repo.AgentProjectAssignment, len(assignments))
	copy(sortedAssignments, assignments)
	for i := 0; i < len(sortedAssignments)-1; i++ {
		for j := i + 1; j < len(sortedAssignments); j++ {
			oi := roleOrder[sortedAssignments[i].Role]
			oj := roleOrder[sortedAssignments[j].Role]
			if oi > oj {
				sortedAssignments[i], sortedAssignments[j] = sortedAssignments[j], sortedAssignments[i]
			}
		}
	}

	var participants []map[string]any
	for _, a := range sortedAssignments {
		if !a.IsActive {
			continue
		}
		_, err := e.participants.Create(ctx, repo.ChatParticipant{
			SessionID:              sessionID,
			ParticipantType:        "agent",
			ParticipantID:          a.AgentID,
			NotificationPreference: "all",
			Role:                   "member",
		})
		if err != nil {
			continue
		}
		participants = append(participants, map[string]any{
			"agent_id":     a.AgentID,
			"project_role": a.Role,
		})
	}

	return participants
}

func (e *NativeToolExecutor) handleSessionInviteAgent(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.participants == nil {
		return map[string]any{"error": "chat_repository_unavailable"}, nil
	}
	sessionID, ok := readUUID(input, "session_id")
	if !ok || sessionID == uuid.Nil {
		return map[string]any{"error": "session_id_required"}, nil
	}
	agentID, ok := readUUID(input, "agent_id")
	if !ok || agentID == uuid.Nil {
		return map[string]any{"error": "agent_id_required"}, nil
	}
	created, err := e.participants.Create(ctx, repo.ChatParticipant{
		SessionID:              sessionID,
		ParticipantType:        "agent",
		ParticipantID:          agentID,
		NotificationPreference: "all",
		Role:                   "member",
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"participant_id": created.ID}, nil
}

func (e *NativeToolExecutor) handleMessageSend(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.messages == nil {
		return map[string]any{"error": "chat_repository_unavailable"}, nil
	}
	sessionID, ok := readUUID(input, "session_id")
	if !ok || sessionID == uuid.Nil {
		return map[string]any{"error": "session_id_required"}, nil
	}
	content, ok := readString(input, "content")
	if !ok || content == "" {
		return map[string]any{"error": "content_required"}, nil
	}
	role, ok := readString(input, "role")
	if !ok || role == "" {
		role = "user"
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	var authorType *string
	var authorID *uuid.UUID
	actorType := "system"
	if scope.agentID != nil && *scope.agentID != uuid.Nil {
		typed := "agent"
		authorType = &typed
		authorID = scope.agentID
		actorType = "agent"
	}
	created, err := e.messages.Create(ctx, repo.ChatMessage{
		SessionID:  sessionID,
		AuthorType: authorType,
		AuthorID:   authorID,
		Role:       role,
		Content:    content,
		Status:     "pending",
		Metadata:   json.RawMessage(`{}`),
	})
	if err != nil {
		return nil, err
	}

	// Publish events so the target session triggers an agent turn.
	if e.events != nil && e.chatSessions != nil {
		session, sessErr := e.chatSessions.GetByID(ctx, sessionID)
		if sessErr == nil {
			payload, _ := json.Marshal(map[string]any{
				"session_id":      session.ID,
				"message_id":      created.ID,
				"sequence_number": created.SequenceNumber,
				"status":          created.Status,
			})
			_ = e.events.Publish(ctx, nil, eventbus.DomainEvent{
				OrganizationID: session.OrganizationID,
				EventType:      "chat.message.created",
				ActorType:      actorType,
				ActorID:        authorID,
				Payload:        payload,
			})
			if role == "user" {
				_ = e.events.Publish(ctx, nil, eventbus.DomainEvent{
					OrganizationID: session.OrganizationID,
					EventType:      "chat.message.user_sent",
					ActorType:      actorType,
					ActorID:        authorID,
					Payload:        payload,
				})
			}
		}
	}

	return map[string]any{
		"message_id": created.ID,
		"sequence":   created.SequenceNumber,
	}, nil
}

func (e *NativeToolExecutor) createDraftActionReview(ctx context.Context, action string, input map[string]any) (map[string]any, error) {
	if e.inbox == nil {
		return map[string]any{"error": "inbox_repository_unavailable"}, nil
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	actor := actorFromContext(ctx)
	payload, err := json.Marshal(map[string]any{
		"action": action,
		"input":  input,
	})
	if err != nil {
		return nil, err
	}
	created, err := e.inbox.Create(ctx, repo.InboxItem{
		OrganizationID:  scope.organizationID,
		ItemType:        "draft_action_review",
		SourceProjectID: scope.projectID,
		SourceTaskID:    scope.taskID,
		CreatedByType:   actor.createdByType,
		CreatedByID:     actor.createdByPtr,
		Title:           fmt.Sprintf("%s pending human review", action),
		ActionPayload:   payload,
	})
	if err != nil {
		return nil, err
	}
	if e.events != nil {
		evtPayload, _ := json.Marshal(map[string]any{
			"inbox_item_id": created.ID,
			"item_type":     created.ItemType,
		})
		_ = e.events.Publish(ctx, nil, eventbus.DomainEvent{
			OrganizationID: scope.organizationID,
			EventType:      "inbox.item_created",
			ActorType:      "system",
			Payload:        evtPayload,
		})
	}
	return map[string]any{
		"inbox_item_id": created.ID,
		"status":        "pending_review",
	}, nil
}

func (e *NativeToolExecutor) handleEmailCompose(ctx context.Context, input map[string]any) (map[string]any, error) {
	return e.createDraftActionReview(ctx, "email.compose", input)
}

func (e *NativeToolExecutor) handleSlackPost(ctx context.Context, input map[string]any) (map[string]any, error) {
	return e.createDraftActionReview(ctx, "slack.post", input)
}

func (e *NativeToolExecutor) handleTUINavigate(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.projects == nil || e.events == nil {
		return map[string]any{"error": "service_unavailable"}, nil
	}
	target, _ := readString(input, "target")
	targetID, _ := readString(input, "target_id")
	targetSlug, _ := readString(input, "target_slug")

	// Resolve slug to ID if needed
	if target == "project" && strings.TrimSpace(targetID) == "" && strings.TrimSpace(targetSlug) != "" {
		scope, err := e.resolveScope(ctx)
		if err == nil {
			if proj, err := e.projects.GetBySlug(ctx, scope.organizationID, strings.TrimSpace(targetSlug)); err == nil {
				targetID = proj.ID.String()
			}
		}
	}

	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]any{
		"action":    "navigate",
		"target":    target,
		"target_id": targetID,
	})
	_ = e.events.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: scope.organizationID,
		EventType:      "tui.command",
		ActorType:      "system",
		Payload:        payload,
	})
	return map[string]any{
		"status":    "navigation_requested",
		"target":    target,
		"target_id": targetID,
	}, nil
}
