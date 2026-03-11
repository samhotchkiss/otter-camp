package native

import (
	"bytes"
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
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	agentsvc "github.com/samhotchkiss/otter-camp/internal/agent"
	"github.com/samhotchkiss/otter-camp/internal/assignmentrole"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/flowpolicy"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/taskorchestration"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
	"github.com/samhotchkiss/otter-camp/internal/toolargs"
)

var slugStripPattern = regexp.MustCompile(`[^a-z0-9\-]+`)

var errInvalidExecutableFlowTemplate = errors.New(flowTemplateValidationMessage)

const (
	taskDoneTerminalNodeMessage   = "task can only be marked done when its flow reaches a terminal node"
	taskOrchestrationOnlyMessage  = "task must remain orchestration-only while executable child tasks exist"
	flowTemplateValidationMessage = "flow template must define a work -> review -> completion path"
	memoryRecordEmbeddingDims     = 1536
	memoryRecordDefaultConfidence = 0.85
	memoryRecordDefaultUtility    = 0.7
	reviewPolicyErrorMessage      = "review_policy must include mode: human_review_required, human_review_preferred, or delegated_authority"
	staffPMCreationMessage        = "project manager assignments require a staff agent; create the PM with agent.create_staff (agent_type=\"pm\") or pick an existing staff PM before assigning"
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

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
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

func applyReviewPolicyInput(existing json.RawMessage, input map[string]any) (json.RawMessage, error) {
	if input == nil {
		return existing, nil
	}
	raw, ok := input["review_policy"]
	if !ok {
		return existing, nil
	}
	if raw == nil {
		return taskplan.ClearReviewPolicy(existing), nil
	}
	policy, ok := taskplan.ParseReviewPolicyValue(raw)
	if !ok {
		return nil, errors.New(reviewPolicyErrorMessage)
	}
	return taskplan.ApplyReviewPolicy(existing, policy), nil
}

func reviewPolicyResponse(policy taskplan.ReviewPolicy) map[string]any {
	out := map[string]any{
		"mode": policy.Mode,
	}
	if len(policy.Guardrails) > 0 {
		out["guardrails"] = append([]string(nil), policy.Guardrails...)
	}
	if policy.SummaryCadence != "" {
		out["summary_cadence"] = policy.SummaryCadence
	}
	if policy.Source != "" {
		out["source"] = policy.Source
	}
	return out
}

type planningProcessInputResult struct {
	applied bool
	plan    taskplan.Plan
	report  taskplan.ValidationReport
}

type parentOrchestrationInputResult struct {
	applied bool
	state   taskorchestration.ParentState
}

func readPlanningArtifactsInput(input map[string]any) ([]taskplan.ArtifactEvidence, bool, error) {
	if input == nil {
		return nil, false, nil
	}
	raw, ok := input["planning_artifacts"]
	if !ok {
		return nil, false, nil
	}
	if raw == nil {
		return nil, true, nil
	}

	parseItem := func(item map[string]any) taskplan.ArtifactEvidence {
		slug := readStringValue(item["slug"])
		title := readStringValue(item["title"])
		if slug == "" && title != "" {
			slug = normalizeSlug(title)
		}
		return taskplan.ArtifactEvidence{
			Slug:      slug,
			Title:     title,
			Summary:   readStringValue(item["summary"]),
			Sections:  readStringSliceValue(item["sections"]),
			AssetRefs: readStringSliceValue(item["asset_refs"]),
			Notes:     readStringValue(item["notes"]),
		}
	}

	switch typed := raw.(type) {
	case []map[string]any:
		out := make([]taskplan.ArtifactEvidence, 0, len(typed))
		for _, item := range typed {
			out = append(out, parseItem(item))
		}
		return out, true, nil
	case []any:
		out := make([]taskplan.ArtifactEvidence, 0, len(typed))
		for _, rawItem := range typed {
			item, ok := rawItem.(map[string]any)
			if !ok {
				return nil, false, errors.New("planning_artifacts items must be objects")
			}
			out = append(out, parseItem(item))
		}
		return out, true, nil
	default:
		return nil, false, errors.New("planning_artifacts must be an array")
	}
}

func readStringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if trimmed := strings.TrimSpace(fmt.Sprintf("%v", item)); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	default:
		return nil
	}
}

func readStringValue(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
}

func applyPlanningProcessInput(existing json.RawMessage, input map[string]any, actor executionActor) (json.RawMessage, planningProcessInputResult, error) {
	artifacts, hasArtifacts, err := readPlanningArtifactsInput(input)
	if err != nil {
		return nil, planningProcessInputResult{}, err
	}

	rawOverride, hasOverride := input["planning_override_reason"]
	var overrideReason *string
	if hasOverride {
		if rawOverride == nil {
			empty := ""
			overrideReason = &empty
		} else {
			value, ok := readString(input, "planning_override_reason")
			if !ok {
				return nil, planningProcessInputResult{}, errors.New("planning_override_reason must be a string")
			}
			overrideReason = &value
		}
	}

	rawFollowOnStopReason, hasFollowOnStopReason := input["planning_follow_on_stop_reason"]
	var followOnStopReason *string
	if hasFollowOnStopReason {
		if rawFollowOnStopReason == nil {
			empty := ""
			followOnStopReason = &empty
		} else {
			value, ok := readString(input, "planning_follow_on_stop_reason")
			if !ok {
				return nil, planningProcessInputResult{}, errors.New("planning_follow_on_stop_reason must be a string")
			}
			followOnStopReason = &value
		}
	}

	if !hasArtifacts && !hasOverride && !hasFollowOnStopReason {
		return existing, planningProcessInputResult{}, nil
	}

	metadata, plan, report, err := taskplan.ApplyProcessUpdate(existing, taskplan.ProcessUpdate{
		Artifacts:          artifacts,
		HasArtifactChanges: hasArtifacts,
		OverrideReason:     overrideReason,
		FollowOnStopReason: followOnStopReason,
		ActorType:          actor.principalType,
		ActorID:            actor.principalID,
		RecordedAt:         time.Now().UTC(),
	})
	if err != nil {
		return nil, planningProcessInputResult{}, err
	}
	return metadata, planningProcessInputResult{
		applied: true,
		plan:    plan,
		report:  report,
	}, nil
}

func hasPlanningProcessInput(input map[string]any) bool {
	if input == nil {
		return false
	}
	_, hasArtifacts := input["planning_artifacts"]
	_, hasOverride := input["planning_override_reason"]
	_, hasFollowOnStopReason := input["planning_follow_on_stop_reason"]
	return hasArtifacts || hasOverride || hasFollowOnStopReason
}

func readChildOutputVerificationsInput(input map[string]any, now time.Time) ([]taskorchestration.ChildVerification, bool, error) {
	if input == nil {
		return nil, false, nil
	}
	raw, ok := input["child_output_verifications"]
	if !ok {
		return nil, false, nil
	}
	if raw == nil {
		return nil, true, nil
	}

	parseItem := func(item map[string]any) (taskorchestration.ChildVerification, error) {
		taskID, ok := readUUID(item, "task_id")
		if !ok || taskID == uuid.Nil {
			return taskorchestration.ChildVerification{}, errors.New("child_output_verifications items must include task_id")
		}
		summary := readStringValue(item["summary"])
		if summary == "" {
			return taskorchestration.ChildVerification{}, errors.New("child_output_verifications items must include summary")
		}
		return taskorchestration.NewChildVerification(taskID, summary, now), nil
	}

	switch typed := raw.(type) {
	case []map[string]any:
		out := make([]taskorchestration.ChildVerification, 0, len(typed))
		for _, item := range typed {
			parsed, err := parseItem(item)
			if err != nil {
				return nil, false, err
			}
			out = append(out, parsed)
		}
		return out, true, nil
	case []any:
		out := make([]taskorchestration.ChildVerification, 0, len(typed))
		for _, rawItem := range typed {
			item, ok := rawItem.(map[string]any)
			if !ok {
				return nil, false, errors.New("child_output_verifications items must be objects")
			}
			parsed, err := parseItem(item)
			if err != nil {
				return nil, false, err
			}
			out = append(out, parsed)
		}
		return out, true, nil
	default:
		return nil, false, errors.New("child_output_verifications must be an array")
	}
}

func readIntegrationCheckInput(input map[string]any, now time.Time) (*taskorchestration.IntegrationCheck, bool, error) {
	if input == nil {
		return nil, false, nil
	}
	raw, ok := input["integration_check"]
	if !ok {
		return nil, false, nil
	}
	if raw == nil {
		return nil, true, nil
	}
	item, ok := raw.(map[string]any)
	if !ok {
		return nil, false, errors.New("integration_check must be an object")
	}
	status := readStringValue(item["status"])
	summary := readStringValue(item["summary"])
	if status == "" || summary == "" {
		return nil, false, errors.New("integration_check must include status and summary")
	}
	parsed := taskorchestration.NewIntegrationCheck(status, summary, now)
	if parsed == nil {
		return nil, false, errors.New("integration_check.status must be passed or failed")
	}
	return parsed, true, nil
}

func readOutcomeAssessmentInput(input map[string]any, now time.Time) (*taskorchestration.OutcomeAssessment, bool, error) {
	if input == nil {
		return nil, false, nil
	}
	raw, ok := input["outcome_assessment"]
	if !ok {
		return nil, false, nil
	}
	if raw == nil {
		return nil, true, nil
	}
	item, ok := raw.(map[string]any)
	if !ok {
		return nil, false, errors.New("outcome_assessment must be an object")
	}
	summary := readStringValue(item["summary"])
	if summary == "" {
		return nil, false, errors.New("outcome_assessment must include summary")
	}
	satisfiedRaw, ok := item["satisfied"]
	if !ok {
		return nil, false, errors.New("outcome_assessment must include satisfied")
	}
	satisfied, ok := satisfiedRaw.(bool)
	if !ok {
		return nil, false, errors.New("outcome_assessment.satisfied must be a boolean")
	}
	return taskorchestration.NewOutcomeAssessment(satisfied, summary, now), true, nil
}

func applyParentOrchestrationInput(existing json.RawMessage, input map[string]any, now time.Time) (json.RawMessage, parentOrchestrationInputResult, error) {
	childVerifications, hasChildVerifications, err := readChildOutputVerificationsInput(input, now)
	if err != nil {
		return nil, parentOrchestrationInputResult{}, err
	}
	integrationCheck, hasIntegrationCheck, err := readIntegrationCheckInput(input, now)
	if err != nil {
		return nil, parentOrchestrationInputResult{}, err
	}
	outcomeAssessment, hasOutcomeAssessment, err := readOutcomeAssessmentInput(input, now)
	if err != nil {
		return nil, parentOrchestrationInputResult{}, err
	}
	if !hasChildVerifications && !hasIntegrationCheck && !hasOutcomeAssessment {
		return existing, parentOrchestrationInputResult{}, nil
	}

	updated, err := taskorchestration.Apply(existing, taskorchestration.Update{
		ChildVerifications: childVerifications,
		IntegrationCheck:   integrationCheck,
		OutcomeAssessment:  outcomeAssessment,
	})
	if err != nil {
		return nil, parentOrchestrationInputResult{}, err
	}
	state, _ := taskorchestration.Parse(updated)
	return updated, parentOrchestrationInputResult{applied: true, state: state}, nil
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
	normalizedInput := toolargs.Normalize("file.write", input)
	wd, scope, resolved, err := e.resolveInputPath(ctx, normalizedInput, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}
	pathInput, ok := readString(normalizedInput, "path")
	if !ok || pathInput == "" {
		return map[string]any{
			"error":   "path_required",
			"message": "file.write requires a non-empty path. Provide a workspace-relative file path in `path`.",
		}, nil
	}
	if !hasNonNilKey(normalizedInput, "content") {
		return map[string]any{
			"error":   "content_required",
			"message": "file.write requires content. Provide file contents in `content`.",
		}, nil
	}
	createDirs := readBool(normalizedInput, "create_dirs", false)
	if createDirs {
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return nil, err
		}
	}

	content, _ := readString(normalizedInput, "content")
	encoding := "utf8"
	payload := []byte(content)
	if rawEncoding, ok := readString(normalizedInput, "encoding"); ok && strings.EqualFold(rawEncoding, "base64") {
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
	settings, err := applyReviewPolicyInput(json.RawMessage(`{"requires_pm_assignment_before_queue":true}`), input)
	if err != nil {
		return map[string]any{"error": err.Error()}, nil
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
		Settings:       settings,
	})
	if err != nil {
		return nil, err
	}
	if _, err := e.ensureProjectRepoBinding(ctx, created.ID); err != nil {
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

	projectResponse := map[string]any{
		"id":     created.ID,
		"slug":   created.Slug,
		"name":   created.DisplayName,
		"status": "active",
	}
	if policy, ok := taskplan.ParseReviewPolicy(created.Settings); ok {
		projectResponse["review_policy"] = reviewPolicyResponse(policy)
	}
	return map[string]any{"project": projectResponse}, nil
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
	if settings, settingsErr := applyReviewPolicyInput(current.Settings, input); settingsErr != nil {
		return map[string]any{"error": settingsErr.Error()}, nil
	} else if _, ok := input["review_policy"]; ok {
		current.Settings = settings
	}
	updated, err := e.projects.Update(ctx, current)
	if err != nil {
		return nil, err
	}
	projectResponse := map[string]any{
		"id":     updated.ID,
		"slug":   updated.Slug,
		"name":   updated.DisplayName,
		"status": "active",
	}
	if policy, ok := taskplan.ParseReviewPolicy(updated.Settings); ok {
		projectResponse["review_policy"] = reviewPolicyResponse(policy)
	}
	return map[string]any{"project": projectResponse}, nil
}

func (e *NativeToolExecutor) handleProjectArchive(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.projects == nil {
		return map[string]any{"error": "project_repository_unavailable"}, nil
	}
	projectID, ok := readUUID(input, "project_id")
	if !ok || projectID == uuid.Nil {
		return map[string]any{"error": "project_id_required"}, nil
	}
	projectRecord, err := e.projects.GetByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	if e.chatSessions != nil {
		if err := e.closeProjectScopedSessions(ctx, projectRecord.OrganizationID, projectID); err != nil {
			return nil, err
		}
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
	if _, err := e.ensureProjectRepoBinding(ctx, projectID); err != nil {
		return nil, err
	}
	var description *string
	if value, ok := readString(input, "description"); ok {
		description = &value
	}
	var parentTask *repo.ProjectTask
	parentTaskID, hasParentTaskID := readUUID(input, "parent_task_id")
	if hasParentTaskID && parentTaskID != uuid.Nil {
		loadedParent, err := e.tasks.GetByID(ctx, parentTaskID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return map[string]any{"error": "parent_task_not_found"}, nil
			}
			return nil, err
		}
		if loadedParent.ProjectID != projectID {
			return map[string]any{"error": "parent_task_project_mismatch"}, nil
		}
		parentTask = &loadedParent
	}
	var flowTemplateID *uuid.UUID
	if value, ok := readUUID(input, "flow_template_id"); ok && value != uuid.Nil {
		if err := e.validateExecutableFlowTemplate(ctx, value); err != nil {
			if errors.Is(err, errInvalidExecutableFlowTemplate) {
				return map[string]any{"error": err.Error()}, nil
			}
			return nil, err
		}
		flowTemplateID = &value
	}
	blocksScope := "none"
	if value, ok := readString(input, "blocks_scope"); ok {
		normalized, valid := normalizeTaskBlocksScope(value)
		if !valid {
			return map[string]any{"error": "blocks_scope must be one of: none, all"}, nil
		}
		blocksScope = normalized
	}
	requiresHumanReview := readBool(input, "requires_human_review", false)
	if parentTask != nil && !hasNonNilKey(input, "requires_human_review") {
		requiresHumanReview = parentTask.RequiresHumanReview
	}
	metadata, policyErr := applyReviewPolicyInput(json.RawMessage(`{}`), input)
	if policyErr != nil {
		return map[string]any{"error": policyErr.Error()}, nil
	}
	if parentTask != nil && flowTemplateID == nil && parentTask.FlowTemplateID != nil && *parentTask.FlowTemplateID != uuid.Nil {
		inheritedFlowTemplateID := *parentTask.FlowTemplateID
		flowTemplateID = &inheritedFlowTemplateID
	}
	planning, resolvedFlowTemplateID, enrichedMetadata, err := e.applyReviewRefinementPlanning(
		ctx,
		scope.organizationID,
		projectID,
		title,
		description,
		flowTemplateID,
		metadata,
	)
	if err != nil {
		return nil, err
	}
	if parentTask != nil && resolvedFlowTemplateID == nil && parentTask.FlowTemplateID != nil && *parentTask.FlowTemplateID != uuid.Nil {
		inheritedFlowTemplateID := *parentTask.FlowTemplateID
		resolvedFlowTemplateID = &inheritedFlowTemplateID
	}
	actor := actorFromContext(ctx)
	if metadataWithProcess, processUpdate, processErr := applyPlanningProcessInput(enrichedMetadata, input, actor); processErr != nil {
		if errors.Is(processErr, taskplan.ErrPlanningStateRequired) || errors.Is(processErr, taskplan.ErrPlanningOverrideNotNeeded) {
			return map[string]any{"error": processErr.Error()}, nil
		}
		return nil, processErr
	} else if processUpdate.applied {
		enrichedMetadata = metadataWithProcess
		planning = processUpdate.plan
	}
	if parentTask != nil {
		preparedChild, decompErr := taskdecomp.PrepareQueueDecomposition(taskdecomp.QueueDecompositionInput{
			ParentTaskID: parentTask.ID,
			Title:        title,
			Description:  description,
			Metadata:     enrichedMetadata,
		})
		if decompErr != nil {
			if errors.Is(decompErr, taskdecomp.ErrBoundedTaskTooLarge) {
				return map[string]any{"error": decompErr.Error()}, nil
			}
			return nil, decompErr
		}
		if preparedChild.Applied {
			return map[string]any{"error": "parent integration follow-on tasks must already be bounded before they are created"}, nil
		}
		children, childErr := e.listDecompositionChildren(ctx, *parentTask)
		if childErr != nil {
			return nil, childErr
		}
		enrichedMetadata = taskdecomp.ApplyChildMetadata(enrichedMetadata, parentTask.ID, nextManualChildWorkstreamIndex(*parentTask, children))
	}
	desiredTask := repo.ProjectTask{
		OrganizationID:      scope.organizationID,
		ProjectID:           projectID,
		Title:               title,
		Description:         description,
		BlocksScope:         blocksScope,
		FlowTemplateID:      resolvedFlowTemplateID,
		RequiresHumanReview: requiresHumanReview,
		Metadata:            enrichedMetadata,
	}
	if parentTask == nil && scope.projectID != nil && *scope.projectID == projectID {
		reused, reusedExisting, reuseErr := e.findReusableProjectScopedTask(ctx, desiredTask)
		if reuseErr != nil {
			return nil, reuseErr
		}
		if reusedExisting {
			if planning.HasSelection() {
				beforeSync := reused
				reused, planning, err = e.syncPlanningArtifacts(ctx, reused, actor)
				if err != nil {
					return nil, err
				}
				if !bytes.Equal(beforeSync.Metadata, reused.Metadata) {
					if updated, updateErr := e.tasks.Update(ctx, reused); updateErr != nil {
						return nil, updateErr
					} else {
						reused = updated
					}
				}
			}
			response := map[string]any{
				"task": map[string]any{
					"id":           reused.ID,
					"task_number":  reused.TaskNumber,
					"work_status":  reused.WorkStatus,
					"blocks_scope": reused.BlocksScope,
				},
			}
			if planning.HasSelection() {
				response["planning"] = reviewPlanningResponse(planning)
			}
			return response, nil
		}
	}
	created, err := e.tasks.Create(ctx, repo.ProjectTask{
		OrganizationID:      scope.organizationID,
		ProjectID:           projectID,
		Title:               title,
		Description:         description,
		BlocksScope:         blocksScope,
		FlowTemplateID:      resolvedFlowTemplateID,
		RequiresHumanReview: requiresHumanReview,
		CreatedByType:       actor.createdByType,
		CreatedByID:         actor.createdByPtr,
		Metadata:            enrichedMetadata,
	})
	if err != nil {
		return nil, err
	}
	if parentTask != nil {
		parentTask.Metadata = taskdecomp.AppendChildTaskID(parentTask.Metadata, created.ID)
		updatedParent, updateErr := e.tasks.Update(ctx, *parentTask)
		if updateErr != nil {
			return nil, updateErr
		}
		parentTask = &updatedParent
	}
	if planning.HasSelection() {
		created, planning, err = e.syncPlanningArtifacts(ctx, created, actor)
		if err != nil {
			return nil, err
		}
		if updated, updateErr := e.tasks.Update(ctx, created); updateErr != nil {
			return nil, updateErr
		} else {
			created = updated
		}
	}
	response := map[string]any{
		"task": map[string]any{
			"id":           created.ID,
			"task_number":  created.TaskNumber,
			"work_status":  created.WorkStatus,
			"blocks_scope": created.BlocksScope,
		},
	}
	if planning.HasSelection() {
		response["planning"] = reviewPlanningResponse(planning)
	}
	return response, nil
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
		if err := e.validateExecutableFlowTemplate(ctx, flowTemplateID); err != nil {
			if errors.Is(err, errInvalidExecutableFlowTemplate) {
				return map[string]any{"error": err.Error()}, nil
			}
			return nil, err
		}
		current.FlowTemplateID = &flowTemplateID
	}
	if assignedAgentID, ok := readUUID(input, "assigned_agent_id"); ok && assignedAgentID != uuid.Nil {
		current.AssignedAgentID = &assignedAgentID
	}
	if value, ok := readString(input, "blocks_scope"); ok {
		normalized, valid := normalizeTaskBlocksScope(value)
		if !valid {
			return map[string]any{"error": "blocks_scope must be one of: none, all"}, nil
		}
		current.BlocksScope = normalized
	}
	if metadata, policyErr := applyReviewPolicyInput(current.Metadata, input); policyErr != nil {
		return map[string]any{"error": policyErr.Error()}, nil
	} else if _, ok := input["review_policy"]; ok {
		current.Metadata = metadata
	}
	if metadataWithParent, parentUpdate, parentErr := applyParentOrchestrationInput(current.Metadata, input, time.Now().UTC()); parentErr != nil {
		return map[string]any{"error": parentErr.Error()}, nil
	} else if parentUpdate.applied {
		current.Metadata = metadataWithParent
	}
	previousStatus := strings.TrimSpace(current.WorkStatus)
	statusChanged := false
	decomposition := queueDecompositionResult{}
	planning := taskplan.Plan{}
	actor := actorFromContext(ctx)
	if hasPlanningProcessInput(input) {
		existingPlan, ok := taskplan.Parse(current.Metadata)
		if !ok || !existingPlan.HasSelection() {
			var resolvedFlowTemplateID *uuid.UUID
			planning, resolvedFlowTemplateID, current.Metadata, err = e.applyReviewRefinementPlanning(
				ctx,
				current.OrganizationID,
				current.ProjectID,
				current.Title,
				current.Description,
				current.FlowTemplateID,
				current.Metadata,
			)
			if err != nil {
				return nil, err
			}
			if current.FlowTemplateID == nil && resolvedFlowTemplateID != nil {
				current.FlowTemplateID = resolvedFlowTemplateID
			}
		}
	}
	var extraStatusPayload map[string]any
	var desiredStatus string
	if status, ok := readString(input, "work_status"); ok && status != "" {
		desiredStatus = strings.TrimSpace(status)
		if current.FlowTemplateID == nil &&
			strings.EqualFold(strings.TrimSpace(current.WorkStatus), "draft") &&
			strings.EqualFold(desiredStatus, "queued") {
			var resolvedFlowTemplateID *uuid.UUID
			planning, resolvedFlowTemplateID, current.Metadata, err = e.applyReviewRefinementPlanning(
				ctx,
				current.OrganizationID,
				current.ProjectID,
				current.Title,
				current.Description,
				current.FlowTemplateID,
				current.Metadata,
			)
			if err != nil {
				return nil, err
			}
			if resolvedFlowTemplateID != nil {
				current.FlowTemplateID = resolvedFlowTemplateID
			}
		}
		if metadataWithProcess, processUpdate, processErr := applyPlanningProcessInput(current.Metadata, input, actor); processErr != nil {
			if errors.Is(processErr, taskplan.ErrPlanningStateRequired) || errors.Is(processErr, taskplan.ErrPlanningOverrideNotNeeded) {
				return map[string]any{"error": processErr.Error()}, nil
			}
			return nil, processErr
		} else if processUpdate.applied {
			current.Metadata = metadataWithProcess
			planning = processUpdate.plan
		}
		if taskStatusRequiresFlowTemplate(desiredStatus) && current.FlowTemplateID == nil {
			return map[string]any{"error": "task requires a flow template before it can be queued"}, nil
		}
		if strings.EqualFold(desiredStatus, "queued") && current.FlowTemplateID != nil {
			if err := e.validateExecutableFlowTemplate(ctx, *current.FlowTemplateID); err != nil {
				if errors.Is(err, errInvalidExecutableFlowTemplate) {
					return map[string]any{"error": err.Error()}, nil
				}
				return nil, err
			}
		}
		if strings.EqualFold(strings.TrimSpace(current.WorkStatus), "draft") &&
			strings.EqualFold(desiredStatus, "queued") {
			decompResult, decompErr := e.applyQueueDecomposition(ctx, &current)
			if decompErr != nil {
				if errors.Is(decompErr, taskdecomp.ErrBoundedTaskTooLarge) {
					return map[string]any{"error": decompErr.Error()}, nil
				}
				return nil, decompErr
			}
			decomposition = decompResult
		}
		if strings.EqualFold(strings.TrimSpace(current.WorkStatus), "draft") &&
			strings.EqualFold(desiredStatus, "queued") &&
			e.projectRequiresPMBeforeQueue(ctx, current.ProjectID) &&
			!e.projectHasActivePM(ctx, current.ProjectID) {
			return map[string]any{"error": "project has no active PM assignment"}, nil
		}
		decompositionChildren, childErr := e.listDecompositionChildren(ctx, current)
		if childErr != nil {
			return nil, childErr
		}
		executableChildren := executableTasks(decompositionChildren)
		if strings.EqualFold(desiredStatus, "queued") && len(executableChildren) > 0 {
			if err := e.queueDecompositionChildren(ctx, current, executableChildren); err != nil {
				return nil, err
			}
			desiredStatus = previousStatus
		}
		if strings.EqualFold(desiredStatus, "in_progress") && len(executableChildren) > 0 {
			return map[string]any{"error": taskOrchestrationOnlyMessage}, nil
		}
		if parentTaskID := taskdecomp.ParseParentTaskID(current.Metadata); parentTaskID != uuid.Nil &&
			strings.EqualFold(strings.TrimSpace(previousStatus), "done") &&
			strings.EqualFold(desiredStatus, "queued") {
			reopenFeedback, hasFeedback := readString(input, "reopen_feedback")
			if !hasFeedback || strings.TrimSpace(reopenFeedback) == "" {
				return map[string]any{"error": "reopen_feedback is required when reopening a completed child task"}, nil
			}
			current.Description = appendReopenFeedback(current.Description, parentTaskID, reopenFeedback)
			extraStatusPayload = map[string]any{
				"parent_task_id":              parentTaskID,
				"parent_integration_feedback": strings.TrimSpace(reopenFeedback),
			}
		} else if _, hasFeedback := input["reopen_feedback"]; hasFeedback {
			return map[string]any{"error": "reopen_feedback can only be used when reopening a completed child task"}, nil
		}
		if strings.EqualFold(desiredStatus, "done") {
			if _, validationErr := e.validateTaskDoneTransition(ctx, current); validationErr != nil {
				return map[string]any{"error": validationErr.Error()}, nil
			}
		}
		if !strings.EqualFold(previousStatus, desiredStatus) {
			statusChanged = true
		}
	} else if metadataWithProcess, processUpdate, processErr := applyPlanningProcessInput(current.Metadata, input, actor); processErr != nil {
		if errors.Is(processErr, taskplan.ErrPlanningStateRequired) || errors.Is(processErr, taskplan.ErrPlanningOverrideNotNeeded) {
			return map[string]any{"error": processErr.Error()}, nil
		}
		return nil, processErr
	} else if processUpdate.applied {
		current.Metadata = metadataWithProcess
		planning = processUpdate.plan
	}
	if synced, syncedPlan, syncErr := e.syncPlanningArtifacts(ctx, current, actorFromContext(ctx)); syncErr != nil {
		return nil, syncErr
	} else {
		current = synced
		if syncedPlan.HasSelection() {
			planning = syncedPlan
		}
	}
	var updated repo.ProjectTask
	if statusChanged && e.taskService != nil {
		current.WorkStatus = previousStatus
		updated, err = e.tasks.Update(ctx, current)
		if err != nil {
			return nil, err
		}
		transitionActor := taskActorFromExecutionActor(actor)
		transitionActor.ExpectedFromStatus = previousStatus
		if extraStatusPayload != nil {
			if _, hasFeedback := extraStatusPayload["parent_integration_feedback"]; hasFeedback {
				transitionActor.AllowCompletedChildReopen = true
			}
		}
		transitioned, transitionErr := e.taskService.TransitionStatusWithPayload(ctx, updated.ID, desiredStatus, transitionActor, extraStatusPayload)
		if transitionErr != nil {
			return nil, transitionErr
		}
		updated = repo.ProjectTask(*transitioned)
	} else {
		if statusChanged {
			current.WorkStatus = desiredStatus
		} else {
			current.WorkStatus = previousStatus
		}
		updated, err = e.tasks.Update(ctx, current)
		if err != nil {
			return nil, err
		}
	}
	if statusChanged {
		if e.taskService != nil {
		} else {
			eventTask := current
			eventTask.WorkStatus = previousStatus
			eventTask.Metadata = updated.Metadata
			if strings.EqualFold(strings.TrimSpace(desiredStatus), "done") {
				if report, reportErr := taskplan.CompletionReport(updated.Metadata); reportErr != nil {
					if !errors.Is(reportErr, taskplan.ErrPlanningArtifactContractIncomplete) {
						return nil, reportErr
					}
				} else {
					if extraStatusPayload == nil {
						extraStatusPayload = map[string]any{}
					}
					for key, value := range report.Payload() {
						extraStatusPayload[key] = value
					}
				}
			}
			if err := e.publishTaskStatusEvents(ctx, nil, eventTask, strings.TrimSpace(updated.WorkStatus), extraStatusPayload); err != nil {
				return nil, err
			}
		}
	}
	response := map[string]any{
		"task": map[string]any{
			"id":           updated.ID,
			"task_number":  updated.TaskNumber,
			"work_status":  updated.WorkStatus,
			"blocks_scope": updated.BlocksScope,
		},
	}
	if decomposition.applied {
		response["decomposition"] = map[string]any{
			"applied":        true,
			"child_task_ids": uuidStringSlice(decomposition.childTaskIDs),
		}
	}
	if planning.HasSelection() {
		response["planning"] = reviewPlanningResponse(planning)
	}
	return response, nil
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
		if assignmentrole.IsProjectManager(assignment.Role) {
			return true
		}
	}
	return false
}

func (e *NativeToolExecutor) validateTaskDoneTransition(ctx context.Context, taskRecord repo.ProjectTask) (taskplan.ValidationReport, error) {
	children, err := e.listDecompositionChildren(ctx, taskRecord)
	if err != nil {
		return taskplan.ValidationReport{}, err
	}
	if err := taskorchestration.ValidateCompletion(taskRecord, children); err != nil {
		return taskplan.ValidationReport{}, err
	}
	if taskRecord.FlowTemplateID == nil || taskRecord.CurrentFlowNodeID == nil {
		return taskplan.ValidationReport{}, errors.New(taskDoneTerminalNodeMessage)
	}
	if e.flowNodes == nil || e.flowExecs == nil {
		return taskplan.ValidationReport{}, errors.New(taskDoneTerminalNodeMessage)
	}
	node, err := e.flowNodes.GetByID(ctx, *taskRecord.CurrentFlowNodeID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return taskplan.ValidationReport{}, errors.New(taskDoneTerminalNodeMessage)
		}
		return taskplan.ValidationReport{}, err
	}
	if node.NextNodeID != nil {
		return taskplan.ValidationReport{}, errors.New(taskDoneTerminalNodeMessage)
	}
	executions, err := e.flowExecs.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return taskplan.ValidationReport{}, errors.New(taskDoneTerminalNodeMessage)
		}
		return taskplan.ValidationReport{}, err
	}
	for _, execution := range executions {
		if execution.FlowNodeID != node.ID {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(execution.Status), "completed") {
			return taskplan.CompletionReport(taskRecord.Metadata)
		}
	}
	return taskplan.ValidationReport{}, errors.New(taskDoneTerminalNodeMessage)
}

func (e *NativeToolExecutor) validateExecutableFlowTemplate(ctx context.Context, flowTemplateID uuid.UUID) error {
	if flowTemplateID == uuid.Nil {
		return errors.New("flow_template_id_required")
	}
	if e.flowNodes == nil {
		return fmt.Errorf("native tool flow-template validation requires flow node repository")
	}
	nodes, err := e.flowNodes.GetByTemplateOrdered(ctx, flowTemplateID)
	if err != nil {
		return err
	}
	if err := flowpolicy.ValidateExecutableFlowNodes(nodes); err != nil {
		return errInvalidExecutableFlowTemplate
	}
	return nil
}

func taskStatusRequiresFlowTemplate(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "in_progress", "review", "done":
		return true
	default:
		return false
	}
}

func (e *NativeToolExecutor) applyReviewRefinementPlanning(
	ctx context.Context,
	organizationID uuid.UUID,
	projectID uuid.UUID,
	title string,
	description *string,
	flowTemplateID *uuid.UUID,
	metadata json.RawMessage,
) (taskplan.Plan, *uuid.UUID, json.RawMessage, error) {
	projectSettings := json.RawMessage(`{}`)
	if e.projects != nil {
		projectRecord, err := e.projects.GetByID(ctx, projectID)
		if err != nil {
			return taskplan.Plan{}, nil, nil, err
		}
		projectSettings = projectRecord.Settings
	}

	policy := taskplan.ResolveReviewPolicy(projectSettings, metadata)
	plan := taskplan.AnalyzeWithPolicy(title, description, policy)
	enrichedMetadata := taskplan.ApplyMetadata(metadata, plan)
	if !plan.RequiresPlanningFlow() {
		return plan, flowTemplateID, enrichedMetadata, nil
	}

	resolvedFlowTemplateID := flowTemplateID
	if resolvedFlowTemplateID != nil && *resolvedFlowTemplateID != uuid.Nil {
		return plan, resolvedFlowTemplateID, enrichedMetadata, nil
	}

	template, err := e.resolveSystemFlowTemplate(ctx, organizationID, projectID, plan.DefaultTemplateSlug)
	if err != nil {
		return taskplan.Plan{}, nil, nil, err
	}
	if err := e.validateExecutableFlowTemplate(ctx, template.ID); err != nil {
		return taskplan.Plan{}, nil, nil, err
	}
	resolvedFlowTemplateID = &template.ID
	return plan, resolvedFlowTemplateID, enrichedMetadata, nil
}

func (e *NativeToolExecutor) resolveSystemFlowTemplate(ctx context.Context, organizationID, projectID uuid.UUID, slug string) (repo.FlowTemplate, error) {
	if e.flowTemplates == nil {
		return repo.FlowTemplate{}, repo.ErrNotFound
	}

	if projectID != uuid.Nil {
		if template, err := e.flowTemplates.GetCurrentBySlug(ctx, nil, &projectID, slug); err == nil {
			return template, nil
		} else if !errors.Is(err, repo.ErrNotFound) {
			return repo.FlowTemplate{}, err
		}
	}
	if organizationID != uuid.Nil {
		if template, err := e.flowTemplates.GetCurrentBySlug(ctx, &organizationID, nil, slug); err == nil {
			return template, nil
		} else if !errors.Is(err, repo.ErrNotFound) {
			return repo.FlowTemplate{}, err
		}
	}

	return e.flowTemplates.GetCurrentBySlug(ctx, nil, nil, slug)
}

func reviewPlanningResponse(plan taskplan.Plan) map[string]any {
	report := taskplan.Evaluate(plan)
	context := map[string]any{
		"work_type":         plan.WorkType,
		"project_stage":     plan.ProjectStage,
		"evidence_maturity": plan.EvidenceMaturity,
		"risk_level":        plan.RiskLevel,
	}
	if plan.DiscoveryMode != "" {
		context["discovery_mode"] = plan.DiscoveryMode
	}
	if plan.BacklogFormat != "" {
		context["backlog_format"] = plan.BacklogFormat
	}
	response := map[string]any{
		"mode":                  plan.Mode,
		"playbook":              plan.Playbook,
		"context":               context,
		"review_policy":         reviewPolicyResponse(taskplan.ReviewPolicy{Mode: plan.ReviewPolicyMode, Guardrails: plan.Guardrails, SummaryCadence: plan.SummaryCadence}),
		"process_enforced":      plan.ProcessEnforced,
		"planned_stages":        append([]string(nil), plan.PlannedStages...),
		"artifacts":             planningArtifactResponse(plan.Artifacts),
		"artifact_contract":     planningArtifactContractResponse(nil),
		"artifact_evidence":     planningArtifactEvidenceResponse(plan.ArtifactEvidence),
		"follow_on_suggestions": append([]string(nil), plan.FollowOnSuggestions...),
		"follow_on":             planningFollowOnResponse(plan.FollowOn),
		"review_checklist":      []string{},
		"process_status":        report.ProcessStatus,
		"missing_requirements":  report.MissingRequirements(),
		"review_packet": map[string]any{
			"summary":  plan.ReviewPacket.Summary,
			"sections": append([]string(nil), plan.ReviewPacket.Sections...),
		},
		"default_template_slug": plan.DefaultTemplateSlug,
		"override":              planningOverrideResponse(plan.Override),
	}
	if report.Enforced {
		response["artifact_contract"] = planningArtifactContractResponse(report.ArtifactContract)
		response["review_checklist"] = append([]string(nil), report.ReviewChecklist...)
	}
	if plan.SummaryCadence != "" {
		response["summary_cadence"] = plan.SummaryCadence
	}
	return response
}

func planningFollowOnResponse(followOn taskplan.FollowOnPlan) map[string]any {
	response := map[string]any{
		"status": followOn.Status,
		"reason": followOn.Reason,
	}
	if followOn.StopReason != "" {
		response["stop_reason"] = followOn.StopReason
	}
	if len(followOn.Candidates) > 0 {
		candidates := make([]map[string]any, 0, len(followOn.Candidates))
		for _, candidate := range followOn.Candidates {
			item := map[string]any{
				"action_type": candidate.ActionType,
				"title":       candidate.Title,
				"summary":     candidate.Summary,
				"reason":      candidate.Reason,
			}
			if candidate.WorkStatus != "" {
				item["work_status"] = candidate.WorkStatus
			}
			if candidate.TargetPlaybook != "" {
				item["target_playbook"] = candidate.TargetPlaybook
			}
			if candidate.SourceArtifactSlug != "" {
				item["source_artifact_slug"] = candidate.SourceArtifactSlug
			}
			candidates = append(candidates, item)
		}
		response["candidates"] = candidates
	}
	return response
}

func planningArtifactResponse(artifacts []taskplan.PlannedArtifact) []map[string]any {
	out := make([]map[string]any, 0, len(artifacts))
	for _, artifact := range artifacts {
		item := map[string]any{
			"slug":  artifact.Slug,
			"title": artifact.Title,
			"kind":  artifact.Kind,
		}
		if artifact.ArtifactID != "" {
			item["artifact_id"] = artifact.ArtifactID
		}
		if artifact.RepoPath != "" {
			item["repo_path"] = artifact.RepoPath
		}
		if artifact.Version > 0 {
			item["version"] = artifact.Version
		}
		if artifact.ContentSHA256 != "" {
			item["content_sha256"] = artifact.ContentSHA256
		}
		out = append(out, item)
	}
	return out
}

func planningArtifactContractResponse(contracts []taskplan.ArtifactContract) []map[string]any {
	out := make([]map[string]any, 0, len(contracts))
	for _, contract := range contracts {
		out = append(out, map[string]any{
			"slug":              contract.Slug,
			"title":             contract.Title,
			"required_sections": append([]string(nil), contract.RequiredSections...),
		})
	}
	return out
}

func planningArtifactEvidenceResponse(artifacts []taskplan.ArtifactEvidence) []map[string]any {
	out := make([]map[string]any, 0, len(artifacts))
	for _, artifact := range artifacts {
		item := map[string]any{
			"slug":  artifact.Slug,
			"title": artifact.Title,
		}
		if artifact.Summary != "" {
			item["summary"] = artifact.Summary
		}
		if len(artifact.Sections) > 0 {
			item["sections"] = append([]string(nil), artifact.Sections...)
		}
		if len(artifact.AssetRefs) > 0 {
			item["asset_refs"] = append([]string(nil), artifact.AssetRefs...)
		}
		if artifact.Notes != "" {
			item["notes"] = artifact.Notes
		}
		out = append(out, item)
	}
	return out
}

func planningOverrideResponse(override *taskplan.PlanningOverride) map[string]any {
	if override == nil || strings.TrimSpace(override.Reason) == "" {
		return nil
	}
	return map[string]any{
		"reason":               override.Reason,
		"skipped_requirements": append([]string(nil), override.SkippedRequirements...),
		"recorded_at":          override.RecordedAt,
		"recorded_by_type":     override.RecordedByType,
		"recorded_by_id":       override.RecordedByID,
	}
}

func uuidStringSlice(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

func shouldReuseScopedSession(scopeType, mode string) bool {
	switch strings.ToLower(strings.TrimSpace(scopeType)) {
	case "project":
		return true
	case "project_task":
		return strings.EqualFold(strings.TrimSpace(mode), "sync") || strings.EqualFold(strings.TrimSpace(mode), "async")
	default:
		return false
	}
}

func chatSessionCanonicalLess(left, right repo.ChatSession) bool {
	if !left.CreatedAt.IsZero() && !right.CreatedAt.IsZero() && !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.Before(right.CreatedAt)
	}
	if left.ID != right.ID {
		return left.ID.String() < right.ID.String()
	}
	return normalizeComparableText(derefString(left.Title)) < normalizeComparableText(derefString(right.Title))
}

func isBlankTaskAsyncSession(session repo.ChatSession) bool {
	return session.CurrentTurnID == nil &&
		session.TurnCount == 0 &&
		session.MessageCount == 0 &&
		session.LastMessageAt == nil
}

func taskAsyncSessionMoreRecent(left, right repo.ChatSession) bool {
	switch {
	case left.LastMessageAt != nil && right.LastMessageAt != nil && !left.LastMessageAt.Equal(*right.LastMessageAt):
		return left.LastMessageAt.After(*right.LastMessageAt)
	case left.LastMessageAt != nil && right.LastMessageAt == nil:
		return true
	case left.LastMessageAt == nil && right.LastMessageAt != nil:
		return false
	case !left.CreatedAt.IsZero() && !right.CreatedAt.IsZero() && !left.CreatedAt.Equal(right.CreatedAt):
		return left.CreatedAt.After(right.CreatedAt)
	default:
		return left.ID.String() > right.ID.String()
	}
}

func taskCanonicalLess(left, right repo.ProjectTask) bool {
	if left.TaskNumber > 0 && right.TaskNumber > 0 && left.TaskNumber != right.TaskNumber {
		return left.TaskNumber < right.TaskNumber
	}
	if !left.CreatedAt.IsZero() && !right.CreatedAt.IsZero() && !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.Before(right.CreatedAt)
	}
	if left.ID != right.ID {
		return left.ID.String() < right.ID.String()
	}
	return normalizeComparableText(left.Title) < normalizeComparableText(right.Title)
}

func isTaskTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "done", "cancelled":
		return true
	default:
		return false
	}
}

func normalizeComparableText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func metadataObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil || payload == nil {
		return nil
	}
	return payload
}

func metadataIntValue(payload map[string]any, key string) (int, bool) {
	if payload == nil {
		return 0, false
	}
	value, ok := payload[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		result := int(typed)
		if float64(result) != typed {
			return 0, false
		}
		return result, true
	case string:
		var result int
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &result); err != nil {
			return 0, false
		}
		return result, true
	default:
		return 0, false
	}
}

func hasMeaningfulJSON(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "{}" || trimmed == "[]" {
		return false
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	switch typed := payload.(type) {
	case nil:
		return false
	case map[string]any:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	case string:
		return strings.TrimSpace(typed) != ""
	default:
		return true
	}
}

func decompositionWorkstreamIndex(task repo.ProjectTask, parentTaskID uuid.UUID) (int, bool) {
	parentID := taskdecomp.ParseParentTaskID(task.Metadata)
	if parentID == uuid.Nil || parentID != parentTaskID {
		return 0, false
	}
	index, ok := taskdecomp.ParseWorkstreamIndex(task.Metadata)
	if !ok || index < 1 {
		return 0, false
	}
	return index, true
}

func nextManualChildWorkstreamIndex(parentTask repo.ProjectTask, children []repo.ProjectTask) int {
	maxIndex := 1
	for _, child := range children {
		index, ok := decompositionWorkstreamIndex(child, parentTask.ID)
		if ok && index > maxIndex {
			maxIndex = index
		}
	}
	return maxIndex + 1
}

func appendReopenFeedback(description *string, parentTaskID uuid.UUID, feedback string) *string {
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return description
	}
	header := "Integration feedback"
	if parentTaskID != uuid.Nil {
		header = fmt.Sprintf("Integration feedback from parent %s", parentTaskID)
	}
	block := header + ":\n- " + feedback

	current := strings.TrimSpace(derefString(description))
	if current == "" {
		return &block
	}
	if strings.Contains(current, block) {
		updated := current
		return &updated
	}
	updated := current + "\n\n" + block
	return &updated
}

func (e *NativeToolExecutor) listDecompositionChildren(ctx context.Context, parentTask repo.ProjectTask) ([]repo.ProjectTask, error) {
	if e.tasks == nil {
		return nil, nil
	}

	projectTasks, err := e.tasks.ListByProject(ctx, parentTask.ProjectID)
	if err != nil {
		return nil, err
	}
	childIDSet := make(map[uuid.UUID]struct{})
	for _, childID := range taskdecomp.ParseChildTaskIDs(parentTask.Metadata) {
		childIDSet[childID] = struct{}{}
	}

	children := make([]repo.ProjectTask, 0)
	for _, projectTask := range projectTasks {
		if projectTask.ID == parentTask.ID {
			continue
		}
		if taskdecomp.ParseParentTaskID(projectTask.Metadata) == parentTask.ID {
			children = append(children, projectTask)
			delete(childIDSet, projectTask.ID)
			continue
		}
		if _, ok := childIDSet[projectTask.ID]; ok {
			children = append(children, projectTask)
			delete(childIDSet, projectTask.ID)
		}
	}

	sort.Slice(children, func(i, j int) bool {
		leftIndex, leftOK := decompositionWorkstreamIndex(children[i], parentTask.ID)
		rightIndex, rightOK := decompositionWorkstreamIndex(children[j], parentTask.ID)
		switch {
		case leftOK && rightOK && leftIndex != rightIndex:
			return leftIndex < rightIndex
		case leftOK != rightOK:
			return leftOK
		default:
			return taskCanonicalLess(children[i], children[j])
		}
	})
	return children, nil
}

func (e *NativeToolExecutor) queueDecompositionChildren(ctx context.Context, parentTask repo.ProjectTask, children []repo.ProjectTask) error {
	actor := taskActorFromExecutionActor(actorFromContext(ctx))
	for _, child := range children {
		if !strings.EqualFold(strings.TrimSpace(child.WorkStatus), "draft") {
			continue
		}
		if e.taskService != nil {
			if _, err := e.taskService.TransitionStatus(ctx, child.ID, "queued", actor); err != nil {
				return fmt.Errorf("queue decomposition child for parent %s: %w", parentTask.ID, err)
			}
			continue
		}
		queuedChild := child
		queuedChild.WorkStatus = "queued"
		if _, err := e.tasks.Update(ctx, queuedChild); err != nil {
			return fmt.Errorf("queue decomposition child for parent %s: %w", parentTask.ID, err)
		}
		if err := e.publishTaskStatusEvents(ctx, nil, child, "queued", nil); err != nil {
			return err
		}
	}
	return nil
}

func executableTasks(tasks []repo.ProjectTask) []repo.ProjectTask {
	filtered := make([]repo.ProjectTask, 0, len(tasks))
	for _, task := range tasks {
		if isTaskTerminal(task.WorkStatus) {
			continue
		}
		filtered = append(filtered, task)
	}
	return filtered
}

func (e *NativeToolExecutor) findReusableScopedSession(ctx context.Context, organizationID uuid.UUID, scopeType string, scopeID uuid.UUID, mode string) (*repo.ChatSession, error) {
	if e.chatSessions == nil || !shouldReuseScopedSession(scopeType, mode) {
		return nil, nil
	}

	sessions, err := e.chatSessions.ListByOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}

	normalizedScopeType := strings.ToLower(strings.TrimSpace(scopeType))
	normalizedMode := strings.ToLower(strings.TrimSpace(mode))
	if normalizedScopeType == "project_task" && normalizedMode == "async" {
		return e.findCurrentTaskExecutionSession(ctx, organizationID, scopeID, sessions)
	}

	var reusable *repo.ChatSession
	for i := range sessions {
		session := sessions[i]
		if session.OrganizationID != organizationID || session.ScopeID != scopeID {
			continue
		}
		active, activeErr := e.scopeBelongsToActiveProject(ctx, organizationID, session.ScopeType, session.ScopeID)
		if activeErr != nil {
			return nil, activeErr
		}
		if !active {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(session.ScopeType), normalizedScopeType) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(session.Status), "active") {
			continue
		}
		if normalizedScopeType == "project_task" && !strings.EqualFold(strings.TrimSpace(session.Mode), normalizedMode) {
			continue
		}
		if reusable == nil || chatSessionCanonicalLess(session, *reusable) {
			candidate := session
			reusable = &candidate
		}
	}
	return reusable, nil
}

func (e *NativeToolExecutor) findCurrentTaskExecutionSession(ctx context.Context, organizationID, taskID uuid.UUID, sessions []repo.ChatSession) (*repo.ChatSession, error) {
	var (
		newestBlank    *repo.ChatSession
		latestNonBlank *repo.ChatSession
		duplicates     []repo.ChatSession
	)
	for i := range sessions {
		session := sessions[i]
		if session.OrganizationID != organizationID || session.ScopeID != taskID {
			continue
		}
		active, activeErr := e.scopeBelongsToActiveProject(ctx, organizationID, session.ScopeType, session.ScopeID)
		if activeErr != nil {
			return nil, activeErr
		}
		if !active {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project_task") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(session.Mode), "async") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(session.Status), "active") {
			continue
		}
		if isBlankTaskAsyncSession(session) {
			if newestBlank == nil || taskAsyncSessionMoreRecent(session, *newestBlank) {
				if newestBlank != nil {
					duplicates = append(duplicates, *newestBlank)
				}
				candidate := session
				newestBlank = &candidate
				continue
			}
			duplicates = append(duplicates, session)
			continue
		}
		if latestNonBlank == nil || taskAsyncSessionMoreRecent(session, *latestNonBlank) {
			candidate := session
			latestNonBlank = &candidate
		}
	}

	if err := e.closeScopedSessionDuplicates(ctx, duplicates); err != nil {
		return nil, err
	}
	if newestBlank != nil && (latestNonBlank == nil || taskAsyncSessionMoreRecent(*newestBlank, *latestNonBlank)) {
		return newestBlank, nil
	}
	if latestNonBlank != nil {
		return latestNonBlank, nil
	}
	return newestBlank, nil
}

func (e *NativeToolExecutor) closeScopedSessionDuplicates(ctx context.Context, sessions []repo.ChatSession) error {
	if e.chatSessions == nil {
		return nil
	}
	for _, session := range sessions {
		if _, err := e.chatSessions.Close(ctx, session.ID); err != nil && !errors.Is(err, repo.ErrNotFound) {
			return err
		}
	}
	return nil
}

func (e *NativeToolExecutor) findReusableProjectScopedTask(ctx context.Context, desired repo.ProjectTask) (repo.ProjectTask, bool, error) {
	if e.tasks == nil {
		return repo.ProjectTask{}, false, nil
	}

	tasks, err := e.tasks.ListByProject(ctx, desired.ProjectID)
	if err != nil {
		return repo.ProjectTask{}, false, err
	}

	desiredTitle := normalizeComparableText(desired.Title)
	desiredDescription := normalizeComparableText(derefString(desired.Description))
	var reusable *repo.ProjectTask
	for i := range tasks {
		taskRecord := tasks[i]
		if isTaskTerminal(taskRecord.WorkStatus) {
			continue
		}
		if normalizeComparableText(taskRecord.Title) != desiredTitle {
			continue
		}
		existingDescription := normalizeComparableText(derefString(taskRecord.Description))
		if desiredDescription != "" && existingDescription != "" && existingDescription != desiredDescription {
			continue
		}
		if reusable == nil || taskCanonicalLess(taskRecord, *reusable) {
			candidate := taskRecord
			reusable = &candidate
		}
	}
	if reusable == nil {
		return repo.ProjectTask{}, false, nil
	}

	repaired, err := e.repairTaskIfNeeded(ctx, *reusable, desired)
	if err != nil {
		return repo.ProjectTask{}, false, err
	}
	return repaired, true, nil
}

func (e *NativeToolExecutor) repairTaskIfNeeded(ctx context.Context, existing repo.ProjectTask, desired repo.ProjectTask) (repo.ProjectTask, error) {
	if e.tasks == nil {
		return existing, nil
	}

	updated := existing
	changed := false

	if strings.TrimSpace(derefString(updated.Description)) == "" && strings.TrimSpace(derefString(desired.Description)) != "" {
		description := strings.TrimSpace(derefString(desired.Description))
		updated.Description = &description
		changed = true
	}

	if strings.EqualFold(strings.TrimSpace(updated.WorkStatus), "draft") {
		if updated.FlowTemplateID == nil && desired.FlowTemplateID != nil && *desired.FlowTemplateID != uuid.Nil {
			flowTemplateID := *desired.FlowTemplateID
			updated.FlowTemplateID = &flowTemplateID
			changed = true
		}
		existingBlocksScope, _ := normalizeTaskBlocksScope(updated.BlocksScope)
		desiredBlocksScope, _ := normalizeTaskBlocksScope(desired.BlocksScope)
		if existingBlocksScope != "all" && desiredBlocksScope == "all" {
			updated.BlocksScope = desiredBlocksScope
			changed = true
		}
		if !updated.RequiresHumanReview && desired.RequiresHumanReview {
			updated.RequiresHumanReview = true
			changed = true
		}
		if !hasMeaningfulJSON(updated.Metadata) && hasMeaningfulJSON(desired.Metadata) {
			updated.Metadata = append(json.RawMessage(nil), desired.Metadata...)
			changed = true
		}
	}

	if !changed {
		return existing, nil
	}
	return e.tasks.Update(ctx, updated)
}

type queueDecompositionResult struct {
	applied      bool
	childTaskIDs []uuid.UUID
}

func (e *NativeToolExecutor) applyQueueDecomposition(ctx context.Context, taskRecord *repo.ProjectTask) (queueDecompositionResult, error) {
	if taskRecord == nil {
		return queueDecompositionResult{}, nil
	}
	prepared, err := taskdecomp.PrepareQueueDecomposition(taskdecomp.QueueDecompositionInput{
		ParentTaskID: taskRecord.ID,
		Title:        taskRecord.Title,
		Description:  taskRecord.Description,
		Metadata:     taskRecord.Metadata,
	})
	if err != nil {
		return queueDecompositionResult{}, err
	}
	if !prepared.Applied {
		return queueDecompositionResult{}, nil
	}
	actor := actorFromContext(ctx)

	existingChildren := map[int]repo.ProjectTask{}
	projectTasks, err := e.tasks.ListByProject(ctx, taskRecord.ProjectID)
	if err != nil {
		return queueDecompositionResult{}, err
	}
	for _, projectTask := range projectTasks {
		if isTaskTerminal(projectTask.WorkStatus) {
			continue
		}
		workstreamIndex, ok := decompositionWorkstreamIndex(projectTask, taskRecord.ID)
		if !ok {
			continue
		}
		if current, exists := existingChildren[workstreamIndex]; !exists || taskCanonicalLess(projectTask, current) {
			existingChildren[workstreamIndex] = projectTask
		}
	}

	childTaskIDs := make([]uuid.UUID, 0, len(prepared.ChildDrafts))
	for _, childDraft := range prepared.ChildDrafts {
		workstreamIndex, _ := metadataIntValue(metadataObject(childDraft.Metadata), "workstream_index")
		desiredChild := repo.ProjectTask{
			OrganizationID:      taskRecord.OrganizationID,
			ProjectID:           taskRecord.ProjectID,
			Title:               childDraft.Title,
			Description:         childDraft.Description,
			WorkStatus:          "draft",
			FlowTemplateID:      taskRecord.FlowTemplateID,
			RequiresHumanReview: taskRecord.RequiresHumanReview,
			Priority:            taskRecord.Priority,
			Metadata:            childDraft.Metadata,
		}
		if existingChild, ok := existingChildren[workstreamIndex]; ok {
			repairedChild, repairErr := e.repairTaskIfNeeded(ctx, existingChild, desiredChild)
			if repairErr != nil {
				return queueDecompositionResult{}, repairErr
			}
			childTaskIDs = append(childTaskIDs, repairedChild.ID)
			continue
		}
		createdChild, createErr := e.tasks.Create(ctx, repo.ProjectTask{
			OrganizationID:      taskRecord.OrganizationID,
			ProjectID:           taskRecord.ProjectID,
			Title:               childDraft.Title,
			Description:         childDraft.Description,
			WorkStatus:          "draft",
			FlowTemplateID:      taskRecord.FlowTemplateID,
			RequiresHumanReview: taskRecord.RequiresHumanReview,
			Priority:            taskRecord.Priority,
			CreatedByType:       actor.createdByType,
			CreatedByID:         actor.createdByPtr,
			Metadata:            childDraft.Metadata,
		})
		if createErr != nil {
			return queueDecompositionResult{}, createErr
		}
		childTaskIDs = append(childTaskIDs, createdChild.ID)
		if err := e.publishTaskCreatedEvent(ctx, nil, createdChild, &taskRecord.ID, true); err != nil {
			return queueDecompositionResult{}, err
		}
	}

	primary := strings.TrimSpace(prepared.Plan.PrimaryDeliverable)
	if primary == "" {
		taskRecord.Description = nil
	} else {
		taskRecord.Description = &primary
	}
	taskRecord.Metadata = taskdecomp.ApplyMetadata(taskRecord.Metadata, prepared.Plan, prepared.SourceDescription, childTaskIDs)

	return queueDecompositionResult{
		applied:      true,
		childTaskIDs: childTaskIDs,
	}, nil
}

func normalizeTaskBlocksScope(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none":
		return "none", true
	case "all":
		return "all", true
	default:
		return "", false
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
	if err := e.publishTaskStatusEvents(ctx, nil, current, targetStatus, nil); err != nil {
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

	if err := e.publishTaskStatusEvents(ctx, tx, current, targetStatus, nil); err != nil {
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

func (e *NativeToolExecutor) publishTaskStatusEvents(ctx context.Context, tx pgx.Tx, task repo.ProjectTask, targetStatus string, extraPayload map[string]any) error {
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
	for key, value := range extraPayload {
		payload[key] = value
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

func (e *NativeToolExecutor) publishTaskCreatedEvent(ctx context.Context, tx pgx.Tx, task repo.ProjectTask, parentTaskID *uuid.UUID, decompositionApplied bool) error {
	if e.events == nil {
		return nil
	}
	actorType, actorID := domainActorFromExecutionActor(actorFromContext(ctx))
	payload := map[string]any{
		"task_id":     task.ID,
		"project_id":  task.ProjectID,
		"task_number": task.TaskNumber,
	}
	if parentTaskID != nil && *parentTaskID != uuid.Nil {
		payload["decomposition_parent"] = *parentTaskID
	}
	if decompositionApplied {
		payload["decomposition_applied"] = true
	}

	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return e.events.Publish(ctx, tx, eventbus.DomainEvent{
		OrganizationID: task.OrganizationID,
		EventType:      "task.created",
		ActorType:      actorType,
		ActorID:        actorID,
		Payload:        encodedPayload,
	})
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

func taskActorFromExecutionActor(actor executionActor) tasksvc.Actor {
	return tasksvc.Actor{
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
	if commitSHA, ok := readString(input, "commit_sha"); ok && commitSHA != "" {
		if _, err := e.flowService.RecordNodeCommit(ctx, taskID, commitSHA, ""); err != nil {
			return nil, err
		}
	}
	execution, err := e.flowService.AdvanceFlow(ctx, taskID, flowActorFromExecutionActor(actorFromContext(ctx)))
	if err != nil {
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
	if e.flowService == nil || e.flowExecs == nil {
		if e.flowServiceErr != nil {
			return nil, e.flowServiceErr
		}
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
	if decision == "approve" {
		next, err := e.flowService.AdvanceFlow(ctx, execution.TaskID, flowActorFromExecutionActor(actorFromContext(ctx)))
		if err != nil {
			return nil, err
		}
		var nextNodeID any
		if !strings.EqualFold(strings.TrimSpace(next.Status), "completed") {
			nextNodeID = next.FlowNodeID
		}
		return map[string]any{"next_node_id": nextNodeID}, nil
	}

	next, err := e.flowService.RejectFlowNode(ctx, execution.TaskID, flowActorFromExecutionActor(actorFromContext(ctx)))
	if err != nil {
		return nil, err
	}
	return map[string]any{"next_node_id": next.FlowNodeID}, nil
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
		return map[string]any{"error": flowTemplateValidationMessage}, nil
	}

	plannedNodes := make([]repo.FlowNode, 0, len(nodesRaw))
	for idx, item := range nodesRaw {
		nodeMap, mapOK := item.(map[string]any)
		if !mapOK {
			continue
		}
		nodeID := uuid.New()
		displayName, ok := readString(nodeMap, "display_name")
		if !ok || displayName == "" {
			displayName = fmt.Sprintf("Node %d", idx+1)
		}
		nodeType, typeOK := readString(nodeMap, "node_type")
		if !typeOK || nodeType == "" {
			nodeType = flowpolicy.NodeTypeWork
		}
		nodeSpec, err := flowpolicy.NormalizeNodeSpec(nodeType, readBool(nodeMap, "requires_human_review", false))
		if err != nil {
			return invalidFlowNodeTypeResult(err), nil
		}
		plannedNodes = append(plannedNodes, repo.FlowNode{
			ID:                  nodeID,
			DisplayName:         displayName,
			NodeType:            nodeSpec.NodeType,
			Position:            idx + 1,
			ToolDomains:         readStringSlice(nodeMap, "tool_domains"),
			RequiresHumanReview: nodeSpec.RequiresHumanReview,
			MaxVisits:           10,
		})
	}
	if len(plannedNodes) == 0 {
		return map[string]any{"error": flowTemplateValidationMessage}, nil
	}
	for i := 0; i < len(plannedNodes)-1; i++ {
		plannedNodes[i].NextNodeID = &plannedNodes[i+1].ID
	}
	if err := flowpolicy.ValidateExecutableFlowTemplate(&plannedNodes[0].ID, plannedNodes); err != nil {
		return map[string]any{"error": flowTemplateValidationMessage}, nil
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
	for _, plannedNode := range plannedNodes {
		created, err := e.flowNodes.Create(ctx, repo.FlowNode{
			FlowTemplateID:      template.ID,
			DisplayName:         plannedNode.DisplayName,
			NodeType:            plannedNode.NodeType,
			Position:            plannedNode.Position,
			ToolDomains:         plannedNode.ToolDomains,
			RequiresHumanReview: plannedNode.RequiresHumanReview,
			MaxVisits:           plannedNode.MaxVisits,
		})
		if err != nil {
			return nil, err
		}
		createdNodes = append(createdNodes, created)
	}
	if len(createdNodes) == 0 {
		return map[string]any{"error": flowTemplateValidationMessage}, nil
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

func invalidFlowNodeTypeResult(err error) map[string]any {
	invalidNodeType := strings.TrimSpace(err.Error())
	var invalidNodeTypeErr *flowpolicy.InvalidNodeTypeError
	if errors.As(err, &invalidNodeTypeErr) {
		invalidNodeType = invalidNodeTypeErr.NodeType
	}
	return map[string]any{
		"error":              "invalid_node_type",
		"invalid_node_type":  invalidNodeType,
		"message":            fmt.Sprintf("invalid node_type %q; valid stored node types are work, review, decision, parallel, merge. Use review with requires_human_review=true for human review, and merge for completion/success.", invalidNodeType),
		"allowed_node_types": flowpolicy.ValidNodeTypes(),
		"minimum_path":       []string{flowpolicy.NodeTypeWork, flowpolicy.NodeTypeReview, flowpolicy.NodeTypeMerge},
	}
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
	role := assignmentrole.Normalize(roleRaw)
	if role == "" {
		return map[string]any{"error": "invalid_role"}, nil
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}

	agentRecord, err := e.agents.GetByID(ctx, agentID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	agentRecord, validationResult, err := e.ensureAssignableProjectAgent(ctx, scope.organizationID, agentRecord, role)
	if err != nil {
		return nil, err
	}
	if validationResult != nil {
		return validationResult, nil
	}
	if err := agentsvc.ValidateProjectAssignmentTarget(agentRecord, role); err != nil {
		if errors.Is(err, agentsvc.ErrAssignmentStarterTrioRole) {
			return map[string]any{
				"error":   agentsvc.StarterTrioProjectRoleErrorCode,
				"message": err.Error(),
			}, nil
		}
		if errors.Is(err, agentsvc.ErrAssignmentPMRequiresStaff) {
			return map[string]any{
				"error":   "project_manager_requires_staff_agent",
				"message": staffPMCreationMessage,
			}, nil
		}
		return nil, err
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

func (e *NativeToolExecutor) ensureAssignableProjectAgent(ctx context.Context, orgID uuid.UUID, agentRecord repo.Agent, role string) (repo.Agent, map[string]any, error) {
	if !strings.EqualFold(strings.TrimSpace(role), "project_manager") {
		return agentRecord, nil, nil
	}
	if errors.Is(agentsvc.ValidateProjectAssignmentTarget(agentRecord, role), agentsvc.ErrAssignmentPMRequiresStaff) {
		return repo.Agent{}, map[string]any{
			"error":   "project_manager_requires_staff_agent",
			"message": staffPMCreationMessage,
		}, nil
	}
	if agentRecord.IsStarterTrio {
		return agentRecord, nil, nil
	}
	if !strings.EqualFold(strings.TrimSpace(agentRecord.LifecycleStatus), "draft") {
		return agentRecord, nil, nil
	}
	if e.agentService == nil {
		return agentRecord, nil, nil
	}
	if err := e.agentService.Unpause(ctx, orgID, agentRecord.ID); err != nil {
		return repo.Agent{}, nil, err
	}
	updated, err := e.agents.GetByID(ctx, agentRecord.ID)
	if err != nil {
		return repo.Agent{}, nil, err
	}
	return updated, nil, nil
}

func (e *NativeToolExecutor) handleAgentCreateStaff(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.agentService == nil {
		return map[string]any{"error": "agent_service_unavailable"}, nil
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	name, ok := readString(input, "name")
	if !ok || strings.TrimSpace(name) == "" {
		return map[string]any{"error": "name_required"}, nil
	}
	agentType, ok := readString(input, "agent_type")
	if !ok || strings.TrimSpace(agentType) == "" {
		return map[string]any{"error": "agent_type_required"}, nil
	}
	agentType = strings.ToLower(strings.TrimSpace(agentType))
	if !isAllowedNativeAgentType(agentType) {
		return map[string]any{
			"error":   "invalid_agent_type",
			"message": "agent_type must be one of pm, worker, reviewer, general",
		}, nil
	}
	systemPrompt, ok := readString(input, "system_prompt")
	if !ok || strings.TrimSpace(systemPrompt) == "" {
		return map[string]any{"error": "system_prompt_required"}, nil
	}
	operatorInstructions, _ := readString(input, "operator_instructions")
	actor := actorFromContext(ctx)
	created, err := e.agentService.Create(ctx, agentsvc.CreateAgentRequest{
		OrganizationID:       scope.organizationID,
		DisplayName:          strings.TrimSpace(name),
		SystemPrompt:         systemPrompt,
		OperatorInstructions: operatorInstructions,
		AgentType:            agentType,
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "assigned_projects", "current_task"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
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
			"agent_class":      created.AgentClass,
			"agent_type":       created.AgentType,
			"lifecycle_status": created.LifecycleStatus,
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

func isAllowedNativeAgentType(agentType string) bool {
	switch strings.ToLower(strings.TrimSpace(agentType)) {
	case "pm", "worker", "reviewer", "general":
		return true
	default:
		return false
	}
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
	scopeType, scopeID = resolveTaskBoundExecutionScope(scope, scopeType, scopeID, mode)
	active, err := e.scopeBelongsToActiveProject(ctx, scope.organizationID, scopeType, scopeID)
	if err != nil {
		return nil, err
	}
	if !active {
		return map[string]any{"error": "scope_archived"}, nil
	}
	title, _ := readString(input, "title")
	var titlePtr *string
	if title != "" {
		titlePtr = &title
	}
	created := repo.ChatSession{}
	if reusable, reuseErr := e.findReusableScopedSession(ctx, scope.organizationID, scopeType, scopeID, mode); reuseErr != nil {
		return nil, reuseErr
	} else if reusable != nil {
		created = *reusable
	} else {
		actor := actorFromContext(ctx)
		created, err = e.chatSessions.Create(ctx, repo.ChatSession{
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
			"id":         created.ID,
			"status":     created.Status,
			"mode":       created.Mode,
			"scope_type": created.ScopeType,
			"scope_id":   created.ScopeID,
		},
	}
	if len(participants) > 0 {
		result["auto_participants"] = participants
	}
	return result, nil
}

func (e *NativeToolExecutor) closeProjectScopedSessions(ctx context.Context, organizationID, projectID uuid.UUID) error {
	sessions, err := e.chatSessions.ListByOrg(ctx, organizationID)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if !strings.EqualFold(strings.TrimSpace(session.Status), "active") {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(session.ScopeType)) {
		case "project":
			if session.ScopeID != projectID {
				continue
			}
		case "project_task":
			if e.tasks == nil {
				continue
			}
			taskRecord, taskErr := e.tasks.GetByID(ctx, session.ScopeID)
			if taskErr != nil || taskRecord.ProjectID != projectID {
				continue
			}
		default:
			continue
		}
		if _, err := e.chatSessions.Close(ctx, session.ID); err != nil && !errors.Is(err, repo.ErrNotFound) {
			return err
		}
	}
	return nil
}

func (e *NativeToolExecutor) scopeBelongsToActiveProject(ctx context.Context, organizationID uuid.UUID, scopeType string, scopeID uuid.UUID) (bool, error) {
	if scopeID == uuid.Nil {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(scopeType)) {
	case "project":
		if e.projects == nil {
			return true, nil
		}
		projectRecord, err := e.projects.GetByID(ctx, scopeID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		return projectRecord.OrganizationID == organizationID && strings.EqualFold(strings.TrimSpace(projectRecord.Status), "active"), nil
	case "project_task":
		if e.tasks == nil {
			return true, nil
		}
		taskRecord, err := e.tasks.GetByID(ctx, scopeID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		if taskRecord.OrganizationID != organizationID {
			return false, nil
		}
		if e.projects == nil {
			return true, nil
		}
		projectRecord, err := e.projects.GetByID(ctx, taskRecord.ProjectID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		return projectRecord.OrganizationID == organizationID && strings.EqualFold(strings.TrimSpace(projectRecord.Status), "active"), nil
	default:
		return true, nil
	}
}

func resolveTaskBoundExecutionScope(scope workspaceScope, requestedScopeType string, requestedScopeID uuid.UUID, mode string) (string, uuid.UUID) {
	if !strings.EqualFold(strings.TrimSpace(mode), "async") {
		return requestedScopeType, requestedScopeID
	}
	if !strings.EqualFold(strings.TrimSpace(requestedScopeType), "project") {
		return requestedScopeType, requestedScopeID
	}
	if scope.taskID == nil || *scope.taskID == uuid.Nil {
		return requestedScopeType, requestedScopeID
	}
	if scope.projectID == nil || *scope.projectID == uuid.Nil {
		return requestedScopeType, requestedScopeID
	}
	if requestedScopeID != *scope.projectID {
		return requestedScopeType, requestedScopeID
	}
	return "project_task", *scope.taskID
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

	existingParticipants, err := e.participants.ListBySession(ctx, sessionID)
	if err != nil {
		existingParticipants = nil
	}
	existingAgentParticipants := make(map[uuid.UUID]struct{}, len(existingParticipants))
	for _, participant := range existingParticipants {
		if participant.RemovedAt != nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(participant.ParticipantType), "agent") {
			continue
		}
		existingAgentParticipants[participant.ParticipantID] = struct{}{}
	}

	// Sort: workers first, then PMs, then others — so the worker becomes
	// the primary responder via resolveFirstAgentParticipant.
	roleOrder := map[string]int{"worker": 0, "reviewer": 1, "project_manager": 2, "observer": 3}
	sortedAssignments := make([]repo.AgentProjectAssignment, len(assignments))
	copy(sortedAssignments, assignments)
	for i := 0; i < len(sortedAssignments)-1; i++ {
		for j := i + 1; j < len(sortedAssignments); j++ {
			oi := roleOrder[assignmentrole.Normalize(sortedAssignments[i].Role)]
			oj := roleOrder[assignmentrole.Normalize(sortedAssignments[j].Role)]
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
		if _, exists := existingAgentParticipants[a.AgentID]; exists {
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
		existingAgentParticipants[a.AgentID] = struct{}{}
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
