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
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	agentsvc "github.com/samhotchkiss/otter-camp/internal/agent"
	"github.com/samhotchkiss/otter-camp/internal/assignmentrole"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/flowcommit"
	"github.com/samhotchkiss/otter-camp/internal/flowpolicy"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/taskorchestration"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
	"github.com/samhotchkiss/otter-camp/internal/toolargs"
)

var slugStripPattern = regexp.MustCompile(`[^a-z0-9\-]+`)
var parentChildOrdinalTitlePattern = regexp.MustCompile(`^([a-z]+)\s+(\d+)\s*:`)
var malformedParameterEchoPattern = regexp.MustCompile(`(?is)(<parameter\s+name\s*=\s*"[^"]+"\s*>|,?\s*antml:parameter>)`)
var explicitDeliverablePathPattern = regexp.MustCompile(`(?i)\b(?:deliverable|output):\s*([^\s,;]+)`)
var bootstrapWaveFamilyTitlePattern = regexp.MustCompile(`(?i)\b(?:[a-z0-9]+-)?(fw|lw)\s*[-:]?\s*(\d+)\b`)

var errInvalidExecutableFlowTemplate = errors.New(flowTemplateValidationMessage)

const (
	taskDoneTerminalNodeMessage   = "task can only be marked done when its flow reaches a terminal node"
	taskOrchestrationOnlyMessage  = "task must remain orchestration-only while executable child tasks exist"
	taskNeedsChildTasksMessage    = "task must remain orchestration-only until bounded child tasks are created"
	taskQueueChildrenDirectlyHint = "Do not activate the orchestration parent directly. Create or queue bounded executable child tasks instead, and leave the parent task in draft/integration mode."
	taskQueueDraftChildFirstHint  = "Draft execution tasks cannot move directly to in_progress. Queue the bounded child task first with work_status=\"queued\" and let normal execution promote it from there."
	bootstrapGateManagedMessage   = "bootstrap governance gate is system-managed; do not edit, assign, queue, or complete it manually"
	bootstrapSetupManagedMessage  = "bootstrap setup checklist tasks are system-managed during bootstrap; use bootstrap.setup.persist instead of raw task.update mutations"
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

func updateTaskMetadataOnly(ctx context.Context, tasks taskReader, taskRecord repo.ProjectTask) (repo.ProjectTask, error) {
	return tasks.UpdateMetadata(ctx, taskRecord.ID, taskRecord.Metadata)
}

func autoCompleteSatisfiedDraftTask(taskRecord repo.ProjectTask) (taskplan.ValidationReport, bool) {
	if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "draft") {
		return taskplan.ValidationReport{}, false
	}
	if !tasksvc.SatisfiedDraftAutoCompletable(taskRecord) {
		return taskplan.ValidationReport{}, false
	}
	report, err := taskplan.CompletionReport(taskRecord.Metadata)
	if err != nil {
		return taskplan.ValidationReport{}, false
	}
	return report, true
}

func satisfiedDraftCompletionConflict(taskRecord repo.ProjectTask) ([]string, bool) {
	if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "draft") {
		return nil, false
	}
	plan, ok := taskplan.Parse(taskRecord.Metadata)
	if !ok {
		return nil, false
	}
	state, ok := taskorchestration.Parse(taskRecord.Metadata)
	if !ok || state.OutcomeAssessment == nil || !state.OutcomeAssessment.Satisfied {
		return nil, false
	}
	contracts := taskplan.ArtifactContractForPlan(plan)
	if len(contracts) == 0 {
		return nil, false
	}
	evidenceBySlug := make(map[string]taskplan.ArtifactEvidence, len(plan.ArtifactEvidence))
	for _, evidence := range plan.ArtifactEvidence {
		slug := strings.TrimSpace(evidence.Slug)
		if slug == "" {
			continue
		}
		evidenceBySlug[slug] = evidence
	}
	missing := make([]string, 0)
	for _, contract := range contracts {
		evidence, ok := evidenceBySlug[strings.TrimSpace(contract.Slug)]
		if !ok {
			missing = append(missing, contract.Title+" artifact evidence is missing")
			continue
		}
		if strings.TrimSpace(evidence.Summary) == "" {
			missing = append(missing, contract.Title+" summary is missing")
		}
		for _, section := range contract.RequiredSections {
			if !containsFold(evidence.Sections, section) {
				missing = append(missing, contract.Title+" is missing section "+section)
			}
		}
	}
	if len(missing) == 0 {
		return nil, false
	}
	return missing, true
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func normalizeWorkspacePath(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(trimmed)))
}

func looksLikeBootstrapOrchestrationParent(title string, description *string) bool {
	titleText := strings.ToLower(strings.TrimSpace(title))
	if titleText == "" {
		return false
	}
	descriptionText := ""
	if description != nil && strings.TrimSpace(*description) != "" {
		descriptionText = strings.ToLower(strings.TrimSpace(*description))
	}
	text := titleText
	if descriptionText != "" {
		text += "\n" + descriptionText
	}
	titleLooksLikeBootstrapWorkstream := strings.HasPrefix(titleText, "workstream ") || strings.HasPrefix(titleText, "ws")
	signals := []string{
		"parent orchestration task",
		"parent orchestration container",
		"orchestration container",
		"does not do execution work itself",
		"does not perform execution work itself",
		"does not perform execution work directly",
		"validates that child tasks",
		"validates child task outputs",
		"validates child outputs",
		"owns integration verification of its children",
	}
	matches := 0
	for _, signal := range signals {
		if strings.Contains(text, signal) {
			matches++
		}
	}
	if matches >= 2 {
		return true
	}
	return titleLooksLikeBootstrapWorkstream && matches >= 1
}

func parseExplicitDeliverablePath(taskRecord repo.ProjectTask) string {
	if taskRecord.Description == nil {
		return ""
	}
	matches := explicitDeliverablePathPattern.FindStringSubmatch(strings.TrimSpace(*taskRecord.Description))
	if len(matches) < 2 {
		return ""
	}
	candidate := normalizeWorkspacePath(matches[1])
	if !looksLikeExplicitDeliverablePath(candidate, matches[1]) {
		return ""
	}
	return candidate
}

func looksLikeExplicitDeliverablePath(normalized, raw string) bool {
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "/") || strings.Contains(filepath.Base(normalized), ".") {
		return true
	}
	trimmedRaw := strings.TrimSpace(raw)
	if trimmedRaw == "" {
		return false
	}
	for _, r := range trimmedRaw {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

func sameOrNestedWorkspacePath(path, root string) bool {
	normalizedPath := normalizeWorkspacePath(path)
	normalizedRoot := normalizeWorkspacePath(root)
	if normalizedPath == "" || normalizedRoot == "" {
		return false
	}
	return normalizedPath == normalizedRoot || strings.HasPrefix(normalizedPath, normalizedRoot+"/")
}

func workspacePathLooksDirectory(path string) bool {
	normalized := normalizeWorkspacePath(path)
	if normalized == "" {
		return false
	}
	if strings.HasSuffix(normalized, "/") {
		return true
	}
	return filepath.Ext(normalized) == ""
}

func looksLikeNarratedTaskFileWritePlaceholder(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || len(trimmed) > 1600 {
		return false
	}
	lower := strings.ToLower(trimmed)
	firstLine := lower
	if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
		firstLine = strings.TrimSpace(firstLine[:idx])
	}
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "package ") || strings.HasPrefix(trimmed, "import ") {
		return false
	}
	if !containsAnySubstring(firstLine,
		"good.",
		"good!",
		"great.",
		"great!",
		"excellent.",
		"excellent!",
		"perfect.",
		"perfect!",
		"the recovery target is",
		"i can see the situation clearly",
		"now i understand",
		"based on the task description",
		"based on my",
		"now i have",
		"i now have",
		"the boundary test design file exists",
		"the target file exists",
		"let me ",
		"i'll ",
		"i will ",
	) {
		return false
	}
	if !containsAnySubstring(lower,
		"let me create",
		"let me look at",
		"let me examine",
		"let me check",
		"let me read",
		"let me write",
		"let me use",
		"let me try",
		"let me now write",
		"let me now execute",
		"let me now produce",
		"i'll create",
		"i will create",
		"i'll write",
		"i will write",
		"i'll begin by",
		"i will begin by",
		"i need to execute",
		"i need to produce",
		"i need to consolidate",
		"according to the recovery guidance",
		"now i need to verify",
		"let me check the planning artifacts",
		"writing the comprehensive",
		"writing the concrete",
		"writing the boundary test",
	) {
		return false
	}
	return containsAnySubstring(lower,
		"deliverable",
		"document",
		"placeholder",
		"recovery target",
		"planning artifacts",
		"boundary test design file",
		"ready for execution phase",
		"specification",
		"requirements",
		"implementation details",
		"validation logic",
		"the task requires me to",
		"task description and recovery instructions",
		"output: `",
		"system prompt",
		"the previous",
		"was rejected because",
		"test plan:",
		"scenario 1",
		"scenario 2",
		"scenario 3",
		"scenario 4",
		"scenario 5",
		"acceptance criteria",
		"with:\n1.",
		"with:\r\n1.",
		"1. ",
		"2. ",
		"3. ",
		"4. ",
	)
}

func looksLikeExecutionPlanFileWrite(path, content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	normalizedPath := strings.ToLower(normalizeWorkspacePath(path))
	if strings.Contains(normalizedPath, "validation-plan") &&
		containsAnySubstring(lower,
			"validation objective",
			"validation checkpoints",
			"success criteria",
			"failure mode registry",
			"validation execution plan",
		) {
		return false
	}
	if !strings.Contains(normalizedPath, "test-execution") &&
		!strings.Contains(normalizedPath, "execution-plan") &&
		!strings.Contains(lower, "execution plan") {
		return false
	}
	if !containsAnySubstring(lower,
		"scenario execution plan",
		"## scenario overview",
		"## execution phases",
		"## phase 1:",
	) {
		return false
	}
	if !containsAnySubstring(lower,
		"## acceptance criteria",
		"## success metrics",
		"- [ ]",
		"verification method",
	) {
		return false
	}
	return !containsAnySubstring(lower,
		"## observed",
		"## findings",
		"## evidence collected",
		"## execution results",
		"pass/fail decision",
	)
}

func looksLikeExecutionLogWithoutEvidence(path, content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	normalizedPath := strings.ToLower(normalizeWorkspacePath(path))
	if !strings.Contains(normalizedPath, "test-execution-") && !strings.Contains(lower, "execution log") {
		return false
	}
	if containsAnySubstring(lower,
		"## what is blocked",
		"## situation summary",
		"ready to execute — blocked",
		"ready to execute - blocked",
		"awaiting input from",
		"cannot proceed without",
		"required to proceed",
		"not available in task context",
		"status assessment",
		"work status assessment",
		"root cause of prior blocking",
		"recommended actions",
	) {
		return true
	}
	if containsAnySubstring(lower,
		"- observed: document the actual outcome",
		"- result: pass or fail",
		"- expected: record the intended",
		"- setup: define the relevant request",
	) {
		return true
	}
	return false
}

func looksLikeExecutionResultsScaffoldWithoutEvidence(path, content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	normalizedPath := strings.ToLower(normalizeWorkspacePath(path))
	if strings.HasPrefix(normalizedPath, "planning/") ||
		strings.HasPrefix(normalizedPath, "review/") ||
		strings.HasPrefix(normalizedPath, "reviews/") ||
		strings.HasPrefix(normalizedPath, ".ottercamp/review/") ||
		strings.HasPrefix(normalizedPath, ".ottercamp/reviews/") {
		return false
	}
	if !strings.HasPrefix(normalizedPath, "work/") &&
		!containsAnySubstring(normalizedPath, "results", "validation", "report") {
		return false
	}
	if !containsAnySubstring(lower,
		"## validation criteria",
		"## evidence expectations",
		"- define explicit pass/fail checks for each relevant stage.",
		"- note the required evidence or observable outputs for each check.",
		"- call out key failure conditions or edge cases reviewers should expect to verify.",
		"- reference the concrete files, logs, screenshots, or outputs that should exist when the work is complete.",
	) {
		return false
	}
	return !containsAnySubstring(lower,
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

func looksLikeExecutionSpecCompletionMemoWithoutArtifacts(path, content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	normalizedPath := strings.ToLower(normalizeWorkspacePath(path))
	if strings.HasPrefix(normalizedPath, "planning/") ||
		strings.HasPrefix(normalizedPath, "review/") ||
		strings.HasPrefix(normalizedPath, "reviews/") ||
		strings.HasPrefix(normalizedPath, ".ottercamp/review/") ||
		strings.HasPrefix(normalizedPath, ".ottercamp/reviews/") {
		return false
	}
	if !containsAnySubstring(lower,
		"- kind: prd_spec",
		"- playbook: execution_spec",
	) {
		return false
	}
	if !containsAnySubstring(lower,
		"## goals",
		"## non-goals",
		"## scope",
		"## constraints",
		"## success metrics",
	) {
		return false
	}
	if !containsAnySubstring(lower,
		"fixture completeness",
		"workflow definition",
		"review templates",
		"documentation completeness",
		"environment readiness",
		"data traceability",
		"scope is clear",
		"production-ready",
		"fixtures are created",
		"documentation is complete",
		"✓",
	) {
		return false
	}
	if containsAnySubstring(lower,
		"```",
		"`ottercamp",
		"`curl",
		"`git ",
		"`work/",
		"`planning/",
		"`review/",
		".json",
		".yaml",
		".yml",
		".csv",
		"work/",
		"planning/",
		"review/",
		"/users/",
		"{\n",
		"{\r\n",
		"[\n",
		"[\r\n",
	) {
		return false
	}
	return true
}

func looksLikeDeliverableCompletionSummaryWithoutBody(path, content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	normalizedPath := strings.ToLower(normalizeWorkspacePath(path))
	if strings.HasPrefix(normalizedPath, "planning/") ||
		strings.HasPrefix(normalizedPath, "review/") ||
		strings.HasPrefix(normalizedPath, "reviews/") ||
		strings.HasPrefix(normalizedPath, ".ottercamp/review/") ||
		strings.HasPrefix(normalizedPath, ".ottercamp/reviews/") {
		return false
	}
	if strings.Count(lower, ".md") < 2 {
		return false
	}
	if !containsAnySubstring(lower,
		"substantive deliverables produced",
		"planning artifacts",
		"quality status",
		"ready for internal review gate",
		"deliverable status",
	) {
		return false
	}
	if !containsAnySubstring(lower,
		"**test design (",
		"**prd** (",
		"**acceptance criteria** (",
		"**implementation plan** (",
		"**dependency log** (",
		"primary deliverable",
		"planning artifacts",
	) {
		return false
	}
	if containsAnySubstring(lower,
		"## test cases",
		"## rate limit",
		"## capacity",
		"## expected responses",
		"## setup",
		"## acceptance criteria",
		"## execution steps",
	) {
		return false
	}
	return true
}

func taskDraftSemanticallyMismatchesScope(taskRecord repo.ProjectTask, content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	taskTokens := taskScopeTokens(taskRecord)
	if len(taskTokens) < 2 {
		return false
	}

	var draftBuilder strings.Builder
	if heading := leadingMarkdownHeading(trimmed); heading != "" {
		draftBuilder.WriteString(heading)
		draftBuilder.WriteByte(' ')
	}
	draftBuilder.WriteString(trimmed)
	draftTokens := draftScopeTokens(draftBuilder.String())
	if len(draftTokens) == 0 {
		return false
	}
	matchCount := 0
	for token := range taskTokens {
		if _, ok := draftTokens[token]; ok {
			matchCount++
		}
	}
	requiredMatches := 1
	if len(taskTokens) >= 4 {
		requiredMatches = 2
	}
	return matchCount < requiredMatches
}

func taskScopeTokens(taskRecord repo.ProjectTask) map[string]struct{} {
	var raw strings.Builder
	raw.WriteString(strings.TrimSpace(taskRecord.Title))
	if taskRecord.Description != nil {
		if description := strings.TrimSpace(*taskRecord.Description); description != "" {
			raw.WriteByte(' ')
			raw.WriteString(description)
		}
	}
	return draftScopeTokens(raw.String())
}

func draftScopeTokens(raw string) map[string]struct{} {
	tokens := deliverableMatchTokens(raw)
	for _, stop := range []string{
		"about", "actual", "analysis", "baseline", "body", "capture", "caught", "cases",
		"complete", "completed", "concrete", "coverage", "deliverable", "documented",
		"evidence", "execute", "executed", "executing", "execution", "findings",
		"gracefully", "handling", "logged", "messages", "objective", "observed",
		"outputs", "pass", "phase", "produce", "record", "results", "review",
		"scenario", "scenarios", "status", "summary", "system", "task", "test",
		"tests", "validate", "validation", "verify", "work",
	} {
		delete(tokens, stop)
	}
	return tokens
}

func leadingMarkdownHeading(content string) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			return strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		}
		break
	}
	return ""
}

func deliverableMatchTokens(raw string) map[string]struct{} {
	cleaned := strings.ToLower(strings.TrimSpace(raw))
	cleaned = strings.TrimSuffix(cleaned, filepath.Ext(cleaned))
	replacer := strings.NewReplacer("-", " ", "_", " ", "/", " ", ".", " ", ":", " ")
	cleaned = replacer.Replace(cleaned)
	fields := strings.Fields(cleaned)
	tokens := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if strings.HasPrefix(field, "oc") && len(field) > 2 {
			if _, err := strconv.Atoi(strings.TrimLeft(field[2:], "0")); err == nil {
				continue
			}
		}
		switch field {
		case "md", "spec", "specification", "document", "draft", "deliverable", "complete", "final", "the", "and":
			continue
		}
		if len(field) < 3 {
			continue
		}
		tokens[field] = struct{}{}
	}
	return tokens
}

func containsAnySubstring(content string, needles ...string) bool {
	for _, needle := range needles {
		if needle == "" {
			continue
		}
		if strings.Contains(content, needle) {
			return true
		}
	}
	return false
}

func containsFold(values []string, needle string) bool {
	target := strings.TrimSpace(needle)
	if target == "" {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func (e *NativeToolExecutor) projectSessionDirectTaskMutationBlocked(ctx context.Context, scope workspaceScope, taskRecord repo.ProjectTask) (map[string]any, bool, error) {
	if e == nil || e.chatSessions == nil || scope.sessionID == nil || *scope.sessionID == uuid.Nil {
		return nil, false, nil
	}
	if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID == uuid.Nil {
		return nil, false, nil
	}
	session, err := e.chatSessions.GetByID(ctx, *scope.sessionID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project") {
		return nil, false, nil
	}
	return map[string]any{
		"error":   "task_lane_owned_by_project_task_session",
		"message": "This task already has an active execution lane. Do not mutate it from the project session. Let the project_task session advance the flow and write deliverables, or use flow.review_decision from the task's review lane when that lane is active.",
	}, true, nil
}

func (e *NativeToolExecutor) projectSessionBootstrapGitCommitBlocked(ctx context.Context, scope workspaceScope) (map[string]any, bool, error) {
	if e == nil || e.chatSessions == nil || e.tasks == nil || scope.sessionID == nil || *scope.sessionID == uuid.Nil || scope.projectID == nil || *scope.projectID == uuid.Nil {
		return nil, false, nil
	}
	if !e.activeProjectBootstrapSession(ctx, scope, *scope.projectID) {
		return nil, false, nil
	}
	projectTasks, err := e.tasks.ListByProject(ctx, *scope.projectID)
	if err != nil {
		return nil, false, err
	}
	for _, taskRecord := range projectTasks {
		if bootstrapGateTask(taskRecord) && !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "done") {
			return map[string]any{
				"error":   "bootstrap_git_commit_blocked",
				"message": "Bootstrap is still active for this project session. Do not use git.commit to satisfy bootstrap steps. Persist bootstrap progress through bootstrap.setup.persist using canonical completed_step_slugs such as bind-repo-environment, staff-project, decompose-workstreams, validate-task-shape, attach-validate-flow-templates, select-first-wave, and record-frank-sign-off. When recording select-first-wave and multiple executable tasks exist, include the exact selected tasks via first_wave_task_ids or first_wave_task_numbers so later-wave work stays draft.",
			}, true, nil
		}
	}
	return nil, false, nil
}

func (e *NativeToolExecutor) taskSessionGitCommitBlocked(ctx context.Context, scope workspaceScope) (map[string]any, bool, error) {
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
	return map[string]any{
		"error":   "task_git_commit_blocked",
		"message": fmt.Sprintf("Task `%s` must not call git.commit directly. Write the concrete deliverable files only and let the runtime own commit/flow completion.", strings.TrimSpace(taskRecord.Title)),
	}, true, nil
}

func (e *NativeToolExecutor) taskSessionDirectStatusBlocked(ctx context.Context, scope workspaceScope, taskRecord repo.ProjectTask, desiredStatus string) (map[string]any, bool, error) {
	if e == nil || scope.sessionID == nil || *scope.sessionID == uuid.Nil || scope.taskID == nil || *scope.taskID == uuid.Nil {
		return nil, false, nil
	}
	if taskRecord.ID == uuid.Nil || taskRecord.ID != *scope.taskID || taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID == uuid.Nil {
		return nil, false, nil
	}
	desiredStatus = strings.ToLower(strings.TrimSpace(desiredStatus))
	currentStatus := strings.ToLower(strings.TrimSpace(taskRecord.WorkStatus))
	if desiredStatus == "" || desiredStatus == currentStatus {
		return nil, false, nil
	}
	errorCode := "flow_owned_status_blocked"
	message := "This task is flow-owned. Do not change work_status with task.update from a task-scoped session. Finish the concrete deliverable work for the current node and let the runtime advance the flow."
	if desiredStatus == "done" {
		errorCode = "flow_owned_done_blocked"
		message = "This task is flow-owned. Do not mark it done with task.update. Finish the concrete deliverable work for the current node and let the runtime advance the flow."
	}
	if e.flowNodes != nil {
		if node, err := e.flowNodes.GetByID(ctx, *taskRecord.CurrentFlowNodeID); err == nil {
			if strings.EqualFold(strings.TrimSpace(node.NodeType), "review") {
				if desiredStatus == "done" {
					message = "This task is in a review node. Do not mark it done with task.update. Inspect the deliverable and use flow.review_decision to approve or reject the review step."
				} else {
					errorCode = "flow_owned_review_status_blocked"
					message = "This task is in a review node. Do not change work_status with task.update. Inspect the deliverable and use flow.review_decision to approve or reject the review step."
				}
			} else if desiredStatus == "review" {
				message = "This task is flow-owned. Do not move it to review with task.update from a task-scoped session. Finish the concrete deliverable work for the current node and let the runtime advance the flow."
			}
		} else if !errors.Is(err, repo.ErrNotFound) {
			return nil, false, err
		}
	}
	return map[string]any{
		"error":   errorCode,
		"message": message,
	}, true, nil
}

func (e *NativeToolExecutor) rejectProjectSessionExecutionMutation(ctx context.Context, scope workspaceScope, relativePath string) (map[string]any, bool, error) {
	if e == nil || e.chatSessions == nil || e.tasks == nil || scope.sessionID == nil || *scope.sessionID == uuid.Nil || scope.projectID == nil || *scope.projectID == uuid.Nil {
		return nil, false, nil
	}
	session, err := e.chatSessions.GetByID(ctx, *scope.sessionID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project") {
		return nil, false, nil
	}

	normalizedPath := strings.ToLower(normalizeWorkspacePath(relativePath))
	if normalizedPath == "" {
		return nil, false, nil
	}
	if strings.HasPrefix(normalizedPath, "bootstrap/") || strings.HasPrefix(normalizedPath, "planning/") {
		return nil, false, nil
	}

	projectTasks, err := e.tasks.ListByProject(ctx, *scope.projectID)
	if err != nil {
		return nil, false, err
	}
	hasExecutableTask := false
	bootstrapActive := false
	for _, taskRecord := range projectTasks {
		if bootstrapGateTask(taskRecord) && !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "done") {
			bootstrapActive = true
			continue
		}
		metadata := metadataObject(taskRecord.Metadata)
		if setupTask, _ := metadata["bootstrap_setup_task"].(bool); setupTask {
			continue
		}
		if isTaskTerminal(taskRecord.WorkStatus) {
			continue
		}
		hasExecutableTask = true
		break
	}
	if !hasExecutableTask {
		return nil, false, nil
	}

	message := fmt.Sprintf("Executable project tasks already exist for this project. Do not write deliverable files like `%s` from the project session. Queue or advance the specific task and let the bound project_task session write the deliverable.", normalizeWorkspacePath(relativePath))
	if bootstrapActive {
		message = fmt.Sprintf("Bootstrap has already materialized executable project tasks. Do not write deliverable files like `%s` from the project session. Keep bootstrap moving with task, assignment, flow, and bootstrap.setup.persist actions, and let project_task sessions write the deliverables.", normalizeWorkspacePath(relativePath))
	}
	return map[string]any{
		"error":   "task_execution_required",
		"message": message,
	}, true, nil
}

func (e *NativeToolExecutor) rejectExecutionFirstDeliverableMutation(ctx context.Context, scope workspaceScope, relativePath string) (map[string]any, bool, error) {
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
	if !ok || !strings.EqualFold(strings.TrimSpace(plan.Mode), taskplan.ModeExecutionFirst) {
		return nil, false, nil
	}
	deliverablePath := parseExplicitDeliverablePath(taskRecord)
	if deliverablePath == "" {
		deliverablePath = e.latestRecoveryTargetPathForSession(ctx, scope)
	}
	if deliverablePath == "" {
		return nil, false, nil
	}
	normalizedPath := normalizeWorkspacePath(relativePath)
	if normalizedPath == "" {
		return nil, false, nil
	}
	if sameOrNestedWorkspacePath(normalizedPath, deliverablePath) {
		return nil, false, nil
	}
	message := fmt.Sprintf("This execution-first task already has an explicit deliverable path `%s`. Do not write `%s` during task execution. Continue the concrete deliverable instead.", deliverablePath, normalizedPath)
	if workspacePathLooksDirectory(deliverablePath) {
		message = fmt.Sprintf("This execution-first task already has an explicit deliverable directory `%s/`. Do not write `%s` during task execution. Write a concrete file under `%s/` instead.", strings.TrimSuffix(deliverablePath, "/"), normalizedPath, strings.TrimSuffix(deliverablePath, "/"))
	}
	return map[string]any{
		"error":            "deliverable_path_required",
		"deliverable_path": deliverablePath,
		"message":          message,
	}, true, nil
}

func (e *NativeToolExecutor) rejectReviewTaskMutation(ctx context.Context, scope workspaceScope, relativePath string) (map[string]any, bool, error) {
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
	if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "review") {
		return nil, false, nil
	}
	normalizedPath := normalizeWorkspacePath(relativePath)
	if allowedReviewArtifactPath(normalizedPath) {
		return nil, false, nil
	}
	return map[string]any{
		"error":   "review_action_required",
		"message": fmt.Sprintf("This task is currently in review. Do not modify `%s` from the review lane unless it is a review-scoped markdown artifact. Inspect the existing deliverables, then call `flow.review_decision` with the active `flow_node_execution_id` and `decision=approve` or `decision=reject`. Repo-backed review notes must stay under `review/`, `reviews/`, `.ottercamp/review/`, or `.ottercamp/reviews/` and use CriticMarkup markdown annotations rather than continuing the deliverable implementation.", normalizedPath),
	}, true, nil
}

func (e *NativeToolExecutor) rejectReviewTaskCLIExecute(ctx context.Context, scope workspaceScope) (map[string]any, bool, error) {
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
	if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "review") {
		return nil, false, nil
	}
	return map[string]any{
		"error":   "review_action_required",
		"message": "This task is currently in review. Do not use cli.execute from the review lane. Inspect the existing deliverables with bounded file/git tools, then call `flow.review_decision` with the active `flow_node_execution_id` and `decision=approve` or `decision=reject`.",
	}, true, nil
}

func allowedReviewArtifactPath(path string) bool {
	normalizedPath := normalizeWorkspacePath(path)
	if normalizedPath == "" {
		return false
	}
	lowerPath := strings.ToLower(normalizedPath)
	if !strings.HasSuffix(lowerPath, ".md") && !strings.HasSuffix(lowerPath, ".markdown") && !strings.HasSuffix(lowerPath, ".mdown") {
		return false
	}
	allowedPrefixes := []string{
		"review/",
		"reviews/",
		".ottercamp/review/",
		".ottercamp/reviews/",
	}
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(lowerPath, prefix) {
			return true
		}
	}
	return false
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

func boundedTaskTooLargeResponse(title string, description *string, err error) map[string]any {
	response := map[string]any{"error": err.Error()}
	return appendSuggestedDecomposition(response, title, description)
}

func boundedTaskNeedsDecompositionResponse(title string, description *string) map[string]any {
	response := map[string]any{"error": "task still requires decomposition into bounded child tasks before it can be created as executable work"}
	return appendSuggestedDecomposition(response, title, description)
}

func appendSuggestedDecomposition(response map[string]any, title string, description *string) map[string]any {
	plan := taskdecomp.Analyze(title, description)
	if !plan.RequiresDecomposition || len(plan.Deliverables) == 0 {
		return response
	}
	suggested := map[string]any{
		"mode":        "parallel_children",
		"next_action": "Do not retry the rejected task title or minor wording variants. Replace it with the suggested child tasks below or create equivalent narrower children under the same parent.",
	}
	if primary := strings.TrimSpace(plan.PrimaryDeliverable); primary != "" {
		suggested["primary_deliverable"] = primary
	}
	childTitles := make([]string, 0, len(plan.Deliverables))
	for _, deliverable := range plan.Deliverables {
		trimmed := strings.TrimSpace(deliverable)
		if trimmed != "" {
			childTitles = append(childTitles, trimmed)
		}
	}
	if len(childTitles) > 0 {
		suggested["child_titles"] = childTitles
	}
	response["suggested_decomposition"] = suggested
	return response
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

func sanitizeStructuredTaskText(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	loc := malformedParameterEchoPattern.FindStringIndex(trimmed)
	if loc != nil {
		sanitized := strings.TrimSpace(trimmed[:loc[0]])
		sanitized = strings.TrimRight(sanitized, " \t\r\n,;\"'")
		trimmed = strings.TrimSpace(sanitized)
	}
	if strings.HasSuffix(trimmed, "\"") && strings.Count(trimmed, "\"")%2 == 1 {
		trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, "\""))
	}
	return trimmed
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
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
	renderedPath := renderPath(wd.Root(), resolved)
	if blocked, reject, rejectErr := e.rejectProjectSessionExecutionMutation(ctx, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if reject {
		return blocked, nil
	}
	if blocked, reject, rejectErr := e.rejectReviewTaskMutation(ctx, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if reject {
		return blocked, nil
	}
	if blocked, reject, rejectErr := e.rejectExecutionFirstDeliverableMutation(ctx, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if reject {
		return blocked, nil
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
	if scope.taskID != nil && *scope.taskID != uuid.Nil && looksLikeNarratedTaskFileWritePlaceholder(content) {
		return map[string]any{
			"error":   "non_substantive_content",
			"message": "file.write content appears to be task narration about planning to write the deliverable, not the deliverable body itself. Write the concrete file contents directly.",
		}, nil
	}
	if scope.taskID != nil && *scope.taskID != uuid.Nil && looksLikeExecutionPlanFileWrite(renderedPath, content) {
		return map[string]any{
			"error":   "non_substantive_content",
			"message": "file.write content appears to be an execution plan/checklist, not concrete execution evidence. Write the real execution log or results directly.",
		}, nil
	}
	if scope.taskID != nil && *scope.taskID != uuid.Nil && looksLikeExecutionLogWithoutEvidence(renderedPath, content) {
		return map[string]any{
			"error":   "non_substantive_content",
			"message": "file.write content appears to be an execution-log scaffold or blocked-status memo without concrete execution evidence. Write actual observed results, evidence, and pass/fail outcomes directly.",
		}, nil
	}
	if scope.taskID != nil && *scope.taskID != uuid.Nil && looksLikeExecutionResultsScaffoldWithoutEvidence(renderedPath, content) {
		return map[string]any{
			"error":   "non_substantive_content",
			"message": "file.write content appears to be a generic validation-results scaffold without concrete observed outcomes or evidence. Write actual results, findings, and pass/fail evidence directly.",
		}, nil
	}
	if scope.taskID != nil && *scope.taskID != uuid.Nil && looksLikeExecutionSpecCompletionMemoWithoutArtifacts(renderedPath, content) {
		return map[string]any{
			"error":   "non_substantive_content",
			"message": "file.write content appears to be a polished execution-spec completion memo without concrete artifact evidence. Write the actual fixture data, commands, paths, or other produced outputs directly.",
		}, nil
	}
	if scope.taskID != nil && *scope.taskID != uuid.Nil && looksLikeDeliverableCompletionSummaryWithoutBody(renderedPath, content) {
		return map[string]any{
			"error":   "non_substantive_content",
			"message": "file.write content appears to be a completion summary about deliverables and review readiness, not the concrete deliverable body itself. Write the actual task document or produced artifact contents directly.",
		}, nil
	}
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
				"path":      renderedPath,
				"byte_size": len(payload),
				"created":   created,
			},
		})
	}

	return map[string]any{
		"path":      renderedPath,
		"byte_size": len(payload),
		"created":   created,
	}, nil
}

func (e *NativeToolExecutor) handleFileEdit(ctx context.Context, input map[string]any) (map[string]any, error) {
	wd, scope, resolved, err := e.resolveInputPath(ctx, input, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}
	pathInput, okPath := readString(input, "path")
	if !okPath || pathInput == "" {
		return map[string]any{
			"error":   "path_required",
			"message": "file.edit requires a non-empty path. Provide a workspace-relative file path in `path`.",
		}, nil
	}
	renderedPath := renderPath(wd.Root(), resolved)
	if blocked, reject, rejectErr := e.rejectProjectSessionExecutionMutation(ctx, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if reject {
		return blocked, nil
	}
	if blocked, reject, rejectErr := e.rejectReviewTaskMutation(ctx, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if reject {
		return blocked, nil
	}
	if blocked, reject, rejectErr := e.rejectExecutionFirstDeliverableMutation(ctx, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if reject {
		return blocked, nil
	}
	oldString, okOld := readString(input, "old_string")
	newString, _ := readString(input, "new_string")
	if !okOld || oldString == "" {
		return map[string]any{"error": "old_string_required"}, nil
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
	content, err := os.ReadFile(resolved)
	if err != nil {
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
		"path":              renderedPath,
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
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	if blocked, reject, guardErr := e.projectSessionBootstrapGitCommitBlocked(ctx, scope); guardErr != nil {
		return nil, guardErr
	} else if reject {
		return blocked, nil
	}
	if blocked, reject, guardErr := e.taskSessionGitCommitBlocked(ctx, scope); guardErr != nil {
		return nil, guardErr
	} else if reject {
		return blocked, nil
	}
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
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	if blocked, reject, rejectErr := e.rejectReviewTaskCLIExecute(ctx, scope); rejectErr != nil {
		return nil, rejectErr
	} else if reject {
		return blocked, nil
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
	if e.projects == nil && e.projectService == nil {
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
	explicitSlug := ok && strings.TrimSpace(slug) != ""
	if !explicitSlug {
		slug = normalizeSlug(name)
	}
	description, _ := readString(input, "description")
	deliveryMode, ok := readString(input, "delivery_mode")
	if !ok || deliveryMode == "" {
		deliveryMode = "gated"
	}
	deliveryMode = normalizeProjectDeliveryModeInput(deliveryMode)
	settings, err := applyReviewPolicyInput(json.RawMessage(`{"requires_pm_assignment_before_queue":true}`), input)
	if err != nil {
		return map[string]any{"error": err.Error()}, nil
	}
	actor := actorFromContext(ctx)
	var created repo.Project
	candidateSlugs := []string{slug}
	if !explicitSlug {
		for _, suffix := range []string{
			strings.ToLower(uuid.NewString()[:6]),
			strings.ToLower(uuid.NewString()[:8]),
		} {
			candidateSlugs = append(candidateSlugs, normalizeSlug(slug+"-"+suffix))
		}
	}
	var createErr error
	for _, candidateSlug := range candidateSlugs {
		if e.projectService != nil {
			var record *projectsvc.Project
			record, createErr = e.projectService.Create(ctx, projectsvc.CreateProjectRequest{
				OrganizationID: scope.organizationID,
				Slug:           candidateSlug,
				DisplayName:    name,
				Description:    description,
				DeliveryMode:   deliveryMode,
				Settings:       settings,
				CreatedByType:  actor.createdByType,
				CreatedByID:    actor.createdByID,
			})
			if createErr == nil {
				created = repo.Project(*record)
				break
			}
		} else {
			var record repo.Project
			record, createErr = e.projects.Create(ctx, repo.Project{
				OrganizationID: scope.organizationID,
				Slug:           candidateSlug,
				DisplayName:    name,
				Description:    description,
				DeliveryMode:   deliveryMode,
				CreatedByType:  actor.createdByType,
				CreatedByID:    actor.createdByID,
				Settings:       settings,
			})
			if createErr == nil {
				created = record
				break
			}
		}
		if !errors.Is(createErr, projectsvc.ErrSlugTaken) || explicitSlug {
			return nil, createErr
		}
	}
	if createErr != nil {
		return nil, createErr
	}
	if e.projectService == nil {
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
		current.DeliveryMode = normalizeProjectDeliveryModeInput(deliveryMode)
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

func normalizeProjectDeliveryModeInput(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	switch trimmed {
	case "", "gated":
		return "gated"
	case "continuous", "scheduled":
		return trimmed
	case "execution_first", "validation", "agile", "execution", "project", "project_task", "async", "autonomous", "canary":
		return "gated"
	default:
		return "gated"
	}
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
	title = sanitizeStructuredTaskText(title)
	if title == "" {
		return map[string]any{"error": "title_required"}, nil
	}
	if _, err := e.ensureProjectRepoBinding(ctx, projectID); err != nil {
		return nil, err
	}
	var description *string
	if value, ok := readString(input, "description"); ok {
		value = sanitizeStructuredTaskText(value)
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
	var assignedAgentID *uuid.UUID
	if value, ok := readUUID(input, "assigned_agent_id"); ok && value != uuid.Nil {
		assignedAgentID = &value
	}
	if assignedAgentID == nil && parentTask != nil && parentTask.AssignedAgentID != nil && *parentTask.AssignedAgentID != uuid.Nil {
		inheritedAssignedAgentID := *parentTask.AssignedAgentID
		assignedAgentID = &inheritedAssignedAgentID
	}
	metadata, policyErr := applyReviewPolicyInput(json.RawMessage(`{}`), input)
	if policyErr != nil {
		return map[string]any{"error": policyErr.Error()}, nil
	}
	if parentTask != nil && flowTemplateID == nil && parentTask.FlowTemplateID != nil && *parentTask.FlowTemplateID != uuid.Nil {
		inheritedFlowTemplateID := *parentTask.FlowTemplateID
		flowTemplateID = &inheritedFlowTemplateID
	}
	var projectTasks []repo.ProjectTask
	bootstrapSetupActive := false
	if parentTask == nil {
		projectTasks, err = e.tasks.ListByProject(ctx, projectID)
		if err != nil {
			return nil, err
		}
		bootstrapSetupActive = bootstrapSetupStillActive(projectTasks)
	}
	bootstrapSessionActive := parentTask == nil && e.activeProjectBootstrapSession(ctx, scope, projectID)
	bootstrapTopLevelOrchestrationParent := parentTask == nil &&
		(bootstrapSessionActive || bootstrapSetupActive) &&
		flowTemplateID == nil &&
		looksLikeBootstrapOrchestrationParent(title, description)
	if parentTask == nil && flowTemplateID == nil && bootstrapSessionActive && !bootstrapTopLevelOrchestrationParent {
		resolvedBootstrapFlowTemplateID, err := e.resolveBootstrapWorkstreamFlowTemplate(ctx, scope, projectID)
		if err != nil {
			return nil, err
		}
		if resolvedBootstrapFlowTemplateID != nil && *resolvedBootstrapFlowTemplateID != uuid.Nil {
			flowTemplateID = resolvedBootstrapFlowTemplateID
		}
	}
	planning := taskplan.Plan{}
	resolvedFlowTemplateID := flowTemplateID
	enrichedMetadata := metadata
	if bootstrapTopLevelOrchestrationParent {
		enrichedMetadata = taskdecomp.ApplyOrchestrationOnlyMetadata(enrichedMetadata)
	}
	if !bootstrapTopLevelOrchestrationParent {
		planning, resolvedFlowTemplateID, enrichedMetadata, err = e.applyReviewRefinementPlanning(
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
	}
	if parentTask == nil && resolvedFlowTemplateID == nil && !bootstrapTopLevelOrchestrationParent {
		resolvedFlowTemplateID, err = e.resolveBootstrapWorkstreamFlowTemplate(ctx, scope, projectID)
		if err != nil {
			return nil, err
		}
	}
	if parentTask != nil && resolvedFlowTemplateID == nil && parentTask.FlowTemplateID != nil && *parentTask.FlowTemplateID != uuid.Nil {
		inheritedFlowTemplateID := *parentTask.FlowTemplateID
		resolvedFlowTemplateID = &inheritedFlowTemplateID
	}
	actor := actorFromContext(ctx)
	unlockParentTask := func() {}
	if parentTask != nil {
		unlockParentTask = e.lockParentTaskMutation(parentTask.ID)
		defer unlockParentTask()
	}
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
				return boundedTaskTooLargeResponse(title, description, decompErr), nil
			}
			return nil, decompErr
		}
		if preparedChild.Applied {
			return e.createDecomposedParentChildren(ctx, *parentTask, preparedChild, actor)
		}
		children, childErr := e.listDecompositionChildren(ctx, *parentTask)
		if childErr != nil {
			return nil, childErr
		}
		desiredChild := repo.ProjectTask{
			OrganizationID:      scope.organizationID,
			ProjectID:           projectID,
			Title:               title,
			Description:         description,
			WorkStatus:          "draft",
			FlowTemplateID:      resolvedFlowTemplateID,
			AssignedAgentID:     assignedAgentID,
			RequiresHumanReview: requiresHumanReview,
			BlocksScope:         blocksScope,
			Metadata:            enrichedMetadata,
		}
		if reusableChild, ok, reuseErr := e.findReusableParentScopedChildTask(ctx, *parentTask, children, desiredChild); reuseErr != nil {
			return nil, reuseErr
		} else if ok {
			if err := e.ensureParentChildTaskDependencyChain(ctx, *parentTask); err != nil {
				return nil, err
			}
			response := map[string]any{
				"task": map[string]any{
					"id":           reusableChild.ID,
					"task_number":  reusableChild.TaskNumber,
					"work_status":  reusableChild.WorkStatus,
					"blocks_scope": reusableChild.BlocksScope,
				},
			}
			if planning.HasSelection() {
				response["planning"] = reviewPlanningResponse(planning)
			}
			return response, nil
		}
		enrichedMetadata = taskdecomp.ApplyChildMetadata(enrichedMetadata, parentTask.ID, nextManualChildWorkstreamIndex(*parentTask, children))
	}
	if parentTask == nil && scope.sessionID != nil && *scope.sessionID != uuid.Nil && scope.projectID != nil && *scope.projectID == projectID {
		if bootstrapSetupActive {
			if assignedAgentID == nil && resolvedFlowTemplateID != nil {
				assignedAgentID, err = e.inferBootstrapExecutableAssignee(ctx, projectID)
				if err != nil {
					return nil, err
				}
			}
			prepared, decompErr := taskdecomp.PrepareQueueDecomposition(taskdecomp.QueueDecompositionInput{
				ParentTaskID: uuid.Nil,
				Title:        title,
				Description:  description,
				Metadata:     enrichedMetadata,
			})
			if decompErr != nil {
				if errors.Is(decompErr, taskdecomp.ErrBoundedTaskTooLarge) {
					return boundedTaskTooLargeResponse(title, description, decompErr), nil
				}
				return nil, decompErr
			}
			if prepared.Applied {
				return boundedTaskNeedsDecompositionResponse(title, description), nil
			}
			if resolvedFlowTemplateID != nil && blocksScope == "all" && !taskMetadataMarksOrchestrationOnly(enrichedMetadata) {
				blocksScope = "none"
			}
		}
	}
	desiredTask := repo.ProjectTask{
		OrganizationID:      scope.organizationID,
		ProjectID:           projectID,
		Title:               title,
		Description:         description,
		BlocksScope:         blocksScope,
		FlowTemplateID:      resolvedFlowTemplateID,
		AssignedAgentID:     assignedAgentID,
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
					if updated, updateErr := updateTaskMetadataOnly(ctx, e.tasks, reused); updateErr != nil {
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
	var created repo.ProjectTask
	if e.taskService != nil {
		createdRecord, createErr := e.taskService.CreateTask(ctx, tasksvc.CreateTaskRequest{
			ProjectID:           projectID,
			Title:               title,
			Description:         description,
			FlowTemplateID:      resolvedFlowTemplateID,
			AssignedAgentID:     assignedAgentID,
			BlocksScope:         blocksScope,
			CreatedByType:       actor.createdByType,
			CreatedByID:         actor.createdByID,
			RequiresHumanReview: &requiresHumanReview,
			Metadata:            enrichedMetadata,
		})
		if createErr != nil {
			return nil, createErr
		}
		created = *createdRecord
	} else {
		if e.pool != nil {
			return map[string]any{"error": "canonical_task_service_unavailable"}, nil
		}
		// Pool-less/native test executors can still fall back to direct repo creation. The
		// production runtime must use the canonical task service so create-time invariants and
		// domain events stay aligned with the task state machine.
		createdRecord, createErr := e.tasks.Create(ctx, repo.ProjectTask{
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
		if createErr != nil {
			return nil, createErr
		}
		created = createdRecord
	}
	if parentTask != nil {
		parentTask.Metadata = taskdecomp.AppendChildTaskID(parentTask.Metadata, created.ID)
		parentTask.BlocksScope = "none"
		updatedParent, updateErr := e.tasks.Update(ctx, *parentTask)
		if updateErr != nil {
			return nil, updateErr
		}
		parentTask = &updatedParent
		if err := e.ensureParentChildTaskDependencyChain(ctx, *parentTask); err != nil {
			return nil, err
		}
	}
	if planning.HasSelection() {
		created, planning, err = e.syncPlanningArtifacts(ctx, created, actor)
		if err != nil {
			return nil, err
		}
		if updated, updateErr := updateTaskMetadataOnly(ctx, e.tasks, created); updateErr != nil {
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

func (e *NativeToolExecutor) resolveBootstrapWorkstreamFlowTemplate(ctx context.Context, scope workspaceScope, projectID uuid.UUID) (*uuid.UUID, error) {
	if e.tasks == nil || scope.sessionID == nil || *scope.sessionID == uuid.Nil {
		return nil, nil
	}
	if scope.projectID == nil || *scope.projectID != projectID {
		return nil, nil
	}

	projectTasks, err := e.tasks.ListByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if !bootstrapSetupStillActive(projectTasks) {
		return nil, nil
	}

	var template repo.FlowTemplate
	for _, slug := range []string{taskplan.InternalReviewTemplate, taskplan.ReviewRefinementTemplate} {
		template, err = e.resolveSystemFlowTemplate(ctx, scope.organizationID, projectID, slug)
		if err == nil {
			break
		}
		if !errors.Is(err, repo.ErrNotFound) {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	if err := e.validateExecutableFlowTemplate(ctx, template.ID); err != nil {
		return nil, err
	}
	templateID := template.ID
	return &templateID, nil
}

func (e *NativeToolExecutor) createDecomposedParentChildren(ctx context.Context, parentTask repo.ProjectTask, prepared taskdecomp.QueueDecomposition, actor executionActor) (map[string]any, error) {
	childDrafts := append([]taskdecomp.ChildDraft(nil), prepared.ChildDrafts...)
	if len(childDrafts) == 0 {
		childDrafts = make([]taskdecomp.ChildDraft, 0, len(prepared.Plan.Deliverables))
		for idx, deliverable := range prepared.Plan.Deliverables {
			trimmed := strings.TrimSpace(deliverable)
			if trimmed == "" {
				continue
			}
			description := trimmed
			childDrafts = append(childDrafts, taskdecomp.ChildDraft{
				Title:       trimmed,
				Description: &description,
				Metadata:    taskdecomp.ApplyChildMetadata(nil, parentTask.ID, idx+2),
			})
		}
	}

	existingChildren := map[int]repo.ProjectTask{}
	existingChildrenByCanonicalTitle := map[string]repo.ProjectTask{}
	projectTasks, err := e.tasks.ListByProject(ctx, parentTask.ProjectID)
	if err != nil {
		return nil, err
	}
	for _, projectTask := range projectTasks {
		if isTaskTerminal(projectTask.WorkStatus) {
			continue
		}
		workstreamIndex, ok := decompositionWorkstreamIndex(projectTask, parentTask.ID)
		if !ok {
			continue
		}
		if current, exists := existingChildren[workstreamIndex]; !exists || taskCanonicalLess(projectTask, current) {
			existingChildren[workstreamIndex] = projectTask
		}
		if key := canonicalParentChildTitleKey(projectTask.Title); key != "" {
			if current, exists := existingChildrenByCanonicalTitle[key]; !exists || taskCanonicalLess(projectTask, current) {
				existingChildrenByCanonicalTitle[key] = projectTask
			}
		}
	}

	childTaskIDs := make([]uuid.UUID, 0, len(childDrafts))
	taskItems := make([]map[string]any, 0, len(childDrafts))
	for _, childDraft := range childDrafts {
		workstreamIndex, _ := metadataIntValue(metadataObject(childDraft.Metadata), "workstream_index")
		desiredChild := repo.ProjectTask{
			OrganizationID:      parentTask.OrganizationID,
			ProjectID:           parentTask.ProjectID,
			Title:               childDraft.Title,
			Description:         childDraft.Description,
			WorkStatus:          "draft",
			AssignedAgentID:     parentTask.AssignedAgentID,
			FlowTemplateID:      parentTask.FlowTemplateID,
			RequiresHumanReview: parentTask.RequiresHumanReview,
			Priority:            parentTask.Priority,
			Metadata:            childDraft.Metadata,
		}

		var childTask repo.ProjectTask
		if existingChild, ok := existingChildren[workstreamIndex]; ok {
			repairedChild, repairErr := e.repairTaskIfNeeded(ctx, existingChild, desiredChild)
			if repairErr != nil {
				return nil, repairErr
			}
			childTask = repairedChild
		} else if existingChild, ok := existingChildrenByCanonicalTitle[canonicalParentChildTitleKey(childDraft.Title)]; ok {
			repairedChild, repairErr := e.repairTaskIfNeeded(ctx, existingChild, desiredChild)
			if repairErr != nil {
				return nil, repairErr
			}
			childTask = repairedChild
		} else if e.taskService != nil {
			requiresHumanReview := parentTask.RequiresHumanReview
			createdRecord, createErr := e.taskService.CreateTask(ctx, tasksvc.CreateTaskRequest{
				ProjectID:           parentTask.ProjectID,
				Title:               childDraft.Title,
				Description:         childDraft.Description,
				AssignedAgentID:     parentTask.AssignedAgentID,
				FlowTemplateID:      parentTask.FlowTemplateID,
				Priority:            parentTask.Priority,
				CreatedByType:       actor.createdByType,
				CreatedByID:         actor.createdByID,
				RequiresHumanReview: &requiresHumanReview,
				Metadata:            childDraft.Metadata,
			})
			if createErr != nil {
				return nil, createErr
			}
			childTask = *createdRecord
		} else {
			if e.pool != nil {
				return nil, fmt.Errorf("canonical task service unavailable")
			}
			createdRecord, createErr := e.tasks.Create(ctx, repo.ProjectTask{
				OrganizationID:      parentTask.OrganizationID,
				ProjectID:           parentTask.ProjectID,
				Title:               childDraft.Title,
				Description:         childDraft.Description,
				WorkStatus:          "draft",
				AssignedAgentID:     parentTask.AssignedAgentID,
				FlowTemplateID:      parentTask.FlowTemplateID,
				RequiresHumanReview: parentTask.RequiresHumanReview,
				Priority:            parentTask.Priority,
				CreatedByType:       actor.createdByType,
				CreatedByID:         actor.createdByPtr,
				Metadata:            childDraft.Metadata,
			})
			if createErr != nil {
				return nil, createErr
			}
			childTask = createdRecord
		}
		if key := canonicalParentChildTitleKey(childTask.Title); key != "" {
			if current, exists := existingChildrenByCanonicalTitle[key]; !exists || taskCanonicalLess(childTask, current) {
				existingChildrenByCanonicalTitle[key] = childTask
			}
		}
		childTaskIDs = append(childTaskIDs, childTask.ID)
		taskItems = append(taskItems, map[string]any{
			"id":           childTask.ID,
			"task_number":  childTask.TaskNumber,
			"work_status":  childTask.WorkStatus,
			"blocks_scope": childTask.BlocksScope,
		})
	}

	for _, childTaskID := range childTaskIDs {
		parentTask.Metadata = taskdecomp.AppendChildTaskID(parentTask.Metadata, childTaskID)
	}
	for idx := 1; idx < len(childTaskIDs); idx++ {
		if err := e.ensureProjectTaskDependency(ctx, childTaskIDs[idx], childTaskIDs[idx-1], actor); err != nil {
			return nil, err
		}
	}
	parentTask.Metadata = taskdecomp.ApplyMetadata(parentTask.Metadata, prepared.Plan, prepared.SourceDescription, childTaskIDs)
	parentTask.BlocksScope = "none"
	updatedParent, err := e.tasks.Update(ctx, parentTask)
	if err != nil {
		return nil, err
	}
	if err := e.ensureParentChildTaskDependencyChain(ctx, updatedParent); err != nil {
		return nil, err
	}

	return map[string]any{
		"decomposition": map[string]any{
			"applied":        true,
			"child_task_ids": childTaskIDs,
		},
		"tasks": taskItems,
	}, nil
}

func (e *NativeToolExecutor) ensureProjectTaskDependency(ctx context.Context, sourceID, dependsOnID uuid.UUID, actor executionActor) error {
	if e.dependencies == nil || sourceID == uuid.Nil || dependsOnID == uuid.Nil || sourceID == dependsOnID {
		return nil
	}
	hasCycle, err := e.dependencies.CheckCycle(ctx, "project_task", sourceID, dependsOnID)
	if err != nil {
		return err
	}
	if hasCycle {
		return nil
	}
	_, err = e.dependencies.Add(ctx, repo.ProjectTaskDependency{
		SourceType:    "project_task",
		SourceID:      sourceID,
		DependsOnType: "project_task",
		DependsOnID:   dependsOnID,
		CreatedByType: actor.createdByType,
		CreatedByID:   actor.createdByPtr,
	})
	return err
}

func (e *NativeToolExecutor) ensureParentChildTaskDependencyChain(ctx context.Context, parentTask repo.ProjectTask) error {
	children, err := e.listDecompositionChildren(ctx, parentTask)
	if err != nil {
		return err
	}
	ordered := make([]repo.ProjectTask, 0, len(children))
	for _, child := range children {
		if _, ok := decompositionWorkstreamIndex(child, parentTask.ID); !ok {
			continue
		}
		ordered = append(ordered, child)
	}
	actor := actorFromContext(ctx)
	for idx := 1; idx < len(ordered); idx++ {
		if err := e.ensureProjectTaskDependency(ctx, ordered[idx].ID, ordered[idx-1].ID, actor); err != nil {
			return err
		}
	}
	return nil
}

func (e *NativeToolExecutor) lockParentTaskMutation(parentTaskID uuid.UUID) func() {
	if e == nil || parentTaskID == uuid.Nil {
		return func() {}
	}
	lockValue, _ := e.parentTaskLocks.LoadOrStore(parentTaskID, &sync.Mutex{})
	mu, _ := lockValue.(*sync.Mutex)
	if mu == nil {
		return func() {}
	}
	mu.Lock()
	return mu.Unlock
}

func (e *NativeToolExecutor) handleTaskUpdate(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.tasks == nil {
		return map[string]any{"error": "task_repository_unavailable"}, nil
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
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
	if bootstrapGateTask(current) {
		return map[string]any{
			"error":   bootstrapGateManagedMessage,
			"message": "Leave the bootstrap governance gate unchanged. Keep executable first-wave tasks in draft and continue bootstrap setup until the system auto-completes the gate after validation passes.",
		}, nil
	}
	if bootstrapSetupTask(current) {
		return map[string]any{
			"error":   bootstrapSetupManagedMessage,
			"message": "Bootstrap setup checklist tasks are system-managed while the bootstrap governance gate is active. Do not edit, reassign, or complete them through task.update. Record completed setup steps through bootstrap.setup.persist using canonical step slugs such as bind-repo-environment, staff-project, decompose-workstreams, validate-task-shape, attach-validate-flow-templates, select-first-wave, and record-frank-sign-off. When recording select-first-wave and multiple executable tasks exist, include the exact selected tasks via first_wave_task_ids or first_wave_task_numbers.",
		}, nil
	}
	if blocked, reject, guardErr := e.projectSessionDirectTaskMutationBlocked(ctx, scope, current); guardErr != nil {
		return nil, guardErr
	} else if reject {
		return blocked, nil
	}
	if title, ok := readString(input, "title"); ok && title != "" {
		title = sanitizeStructuredTaskText(title)
		current.Title = title
	}
	if description, ok := readString(input, "description"); ok {
		description = sanitizeStructuredTaskText(description)
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
	if _, ok := input["requires_human_review"]; ok {
		if !strings.EqualFold(strings.TrimSpace(current.WorkStatus), "draft") {
			return map[string]any{"error": "requires_human_review can only be changed while task is draft"}, nil
		}
		current.RequiresHumanReview = readBool(input, "requires_human_review", current.RequiresHumanReview)
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
		desiredStatus = normalizeTaskWorkStatusAlias(strings.TrimSpace(status))
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
					return boundedTaskTooLargeResponse(current.Title, current.Description, decompErr), nil
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
		requiresChildren := taskRequiresBoundedChildren(current)
		if (strings.EqualFold(desiredStatus, "queued") || strings.EqualFold(desiredStatus, "in_progress")) &&
			requiresChildren &&
			len(executableChildren) == 0 {
			return map[string]any{
				"error":   taskNeedsChildTasksMessage,
				"message": taskQueueChildrenDirectlyHint,
			}, nil
		}
		if strings.EqualFold(desiredStatus, "queued") && len(executableChildren) > 0 {
			if err := e.queueDecompositionChildren(ctx, current, executableChildren); err != nil {
				return nil, err
			}
			desiredStatus = previousStatus
		}
		if strings.EqualFold(desiredStatus, "in_progress") && len(executableChildren) > 0 {
			return map[string]any{
				"error":   taskOrchestrationOnlyMessage,
				"message": taskQueueChildrenDirectlyHint,
			}, nil
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
		if blocked, reject, guardErr := e.taskSessionDirectStatusBlocked(ctx, scope, current, desiredStatus); guardErr != nil {
			return nil, guardErr
		} else if reject {
			return blocked, nil
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
	if !statusChanged {
		if missingRequirements, conflicted := satisfiedDraftCompletionConflict(current); conflicted {
			return map[string]any{
				"error":                "draft_completion_contract_incomplete",
				"missing_requirements": missingRequirements,
				"message":              "This draft task already records a satisfied outcome assessment, so do not leave it in draft with an incomplete planning contract. Either provide the missing planning artifacts now or omit the satisfied outcome until the task is actually complete.",
			}, nil
		}
		if report, ok := autoCompleteSatisfiedDraftTask(current); ok {
			desiredStatus = "done"
			statusChanged = true
			if extraStatusPayload == nil {
				extraStatusPayload = map[string]any{}
			}
			for key, value := range report.Payload() {
				extraStatusPayload[key] = value
			}
			extraStatusPayload["auto_completed_from_metadata"] = true
		}
	}
	if gateErr := tasksvc.ValidateProjectGateTask(current); gateErr != nil {
		return map[string]any{"error": gateErr.Error()}, nil
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
		if autoCompleted, _ := extraStatusPayload["auto_completed_from_metadata"].(bool); autoCompleted && strings.EqualFold(strings.TrimSpace(desiredStatus), "done") {
			transitionActor.AllowDoneBypass = true
			transitionActor.AllowSatisfiedDraftAutoComplete = true
		}
		if extraStatusPayload != nil {
			if _, hasFeedback := extraStatusPayload["parent_integration_feedback"]; hasFeedback {
				transitionActor.AllowCompletedChildReopen = true
			}
		}
		transitioned, transitionErr := e.taskService.TransitionStatusWithPayload(ctx, updated.ID, desiredStatus, transitionActor, extraStatusPayload)
		if transitionErr != nil {
			var invalidTransition tasksvc.ErrInvalidStatusTransition
			if errors.As(transitionErr, &invalidTransition) &&
				strings.EqualFold(strings.TrimSpace(previousStatus), "draft") &&
				bootstrapDraftExecutionPromotion(desiredStatus) &&
				e.activeProjectBootstrapSession(ctx, scope, current.ProjectID) {
				return nil, fmt.Errorf("task is still in draft during active project bootstrap. Keep first-wave execution tasks in draft until setup is persisted through bootstrap.setup.persist")
			}
			if errors.As(transitionErr, &invalidTransition) &&
				strings.EqualFold(strings.TrimSpace(previousStatus), "draft") &&
				strings.EqualFold(strings.TrimSpace(desiredStatus), "in_progress") {
				return map[string]any{
					"error":   transitionErr.Error(),
					"message": taskQueueDraftChildFirstHint,
				}, nil
			}
			return nil, transitionErr
		}
		updated = repo.ProjectTask(*transitioned)
	} else {
		if statusChanged {
			if e.pool != nil {
				return map[string]any{"error": "canonical_task_service_unavailable"}, nil
			}
			// No-task-service execution is a narrow fallback/test seam; pool-backed executors auto-build
			// the canonical service and should not persist status transitions through raw task updates.
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

func normalizeTaskWorkStatusAlias(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ready", "work":
		return "queued"
	case "complete", "completed":
		return "done"
	default:
		return strings.TrimSpace(status)
	}
}

func bootstrapDraftExecutionPromotion(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "open", "in_progress":
		return true
	default:
		return false
	}
}

func bootstrapGateTask(task repo.ProjectTask) bool {
	metadata := metadataObject(task.Metadata)
	bootstrapGate, _ := metadata["bootstrap_gate"].(bool)
	return bootstrapGate
}

func bootstrapSetupTask(task repo.ProjectTask) bool {
	metadata := metadataObject(task.Metadata)
	setupTask, _ := metadata["bootstrap_setup_task"].(bool)
	return setupTask
}

func (e *NativeToolExecutor) activeProjectBootstrapSession(ctx context.Context, scope workspaceScope, projectID uuid.UUID) bool {
	if projectID == uuid.Nil || scope.sessionID == nil || *scope.sessionID == uuid.Nil || e.chatSessions == nil {
		return false
	}
	session, err := e.chatSessions.GetByID(ctx, *scope.sessionID)
	if err != nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project") || session.ScopeID != projectID {
		return false
	}
	var metadata struct {
		ProjectBootstrap struct {
			Status string `json:"status"`
		} `json:"project_bootstrap"`
	}
	if len(session.Metadata) == 0 || !json.Valid(session.Metadata) {
		return false
	}
	if err := json.Unmarshal(session.Metadata, &metadata); err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(metadata.ProjectBootstrap.Status), "active")
}

func (e *NativeToolExecutor) handleBootstrapSetupPersist(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.tasks == nil || e.taskService == nil {
		return map[string]any{"error": "task_repository_unavailable"}, nil
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	projectID, ok := readUUID(input, "project_id")
	if !ok || projectID == uuid.Nil {
		if scope.projectID == nil || *scope.projectID == uuid.Nil {
			return map[string]any{"error": "project_id_required"}, nil
		}
		projectID = *scope.projectID
	}
	stepSlugs := readStringSlice(input, "completed_step_slugs")
	if len(stepSlugs) == 0 {
		return map[string]any{"error": "completed_step_slugs_required"}, nil
	}
	stepSlugs = normalizeBootstrapStepSlugs(stepSlugs)
	if len(stepSlugs) == 0 {
		return map[string]any{"error": "completed_step_slugs_required"}, nil
	}

	projectTasks, err := e.tasks.ListByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	selectedFirstWaveTaskIDs, explicitFirstWaveSelection, selectionErr := resolveBootstrapFirstWaveSelection(input, projectTasks)
	if selectionErr != nil {
		return map[string]any{
			"error":   "invalid_first_wave_selection",
			"message": selectionErr.Error(),
		}, nil
	}
	if stringSliceContains(stepSlugs, "select-first-wave") {
		blockedTaskIDs, err := e.loadBootstrapBlockedTaskIDs(ctx, projectID)
		if err != nil {
			return nil, err
		}
		executableCandidates := bootstrapFirstWaveSelectableTasksExcludingBlocked(projectTasks, blockedTaskIDs)
		selectionHints := bootstrapFirstWaveSelectionHints(executableCandidates)
		if !explicitFirstWaveSelection && len(executableCandidates) > 1 {
			if inferred := inferBootstrapFirstWaveSelection(executableCandidates); len(inferred) > 0 {
				selectedFirstWaveTaskIDs = inferred
			}
		}
		if explicitFirstWaveSelection {
			for taskID := range selectedFirstWaveTaskIDs {
				task, ok := bootstrapTaskByID(projectTasks, taskID)
				if !ok {
					continue
				}
				if bootstrapFirstWaveTaskRequiresChildren(task) {
					return map[string]any{
						"error":                       "invalid_first_wave_selection",
						"message":                     fmt.Sprintf("Task %d (%s) is still an orchestration-only parent container. Select its bounded executable child tasks into the first wave instead of the parent.", task.TaskNumber, task.Title),
						"selectable_first_wave_tasks": selectionHints,
					}, nil
				}
				if strings.EqualFold(strings.TrimSpace(task.BlocksScope), "all") {
					return map[string]any{
						"error":                       "invalid_first_wave_selection",
						"message":                     fmt.Sprintf("Task %d (%s) uses blocks_scope=all and cannot be selected into the first wave because it would block the rest of the wave from becoming runnable.", task.TaskNumber, task.Title),
						"selectable_first_wave_tasks": selectionHints,
					}, nil
				}
				if _, blocked := blockedTaskIDs[task.ID]; blocked {
					return map[string]any{
						"error":                       "invalid_first_wave_selection",
						"message":                     fmt.Sprintf("Task %d (%s) still depends on unfinished prerequisite work and cannot be selected into the first wave until that prerequisite is completed or the first-wave subset is corrected.", task.TaskNumber, task.Title),
						"selectable_first_wave_tasks": selectionHints,
					}, nil
				}
			}
		}
		switch {
		case explicitFirstWaveSelection && len(selectedFirstWaveTaskIDs) == 0:
			return map[string]any{
				"error":                       "first_wave_task_selection_required",
				"message":                     "When persisting `select-first-wave`, include at least one selected task via `first_wave_task_ids` or `first_wave_task_numbers`.",
				"selectable_first_wave_tasks": selectionHints,
			}, nil
		case !explicitFirstWaveSelection && len(executableCandidates) > 1 && len(selectedFirstWaveTaskIDs) == 0:
			return map[string]any{
				"error":                       "first_wave_task_selection_required",
				"message":                     "When persisting `select-first-wave` with multiple executable project tasks, include the exact selected first-wave tasks via `first_wave_task_ids` or `first_wave_task_numbers` so later-wave work stays draft.",
				"selectable_first_wave_tasks": selectionHints,
			}, nil
		case !explicitFirstWaveSelection && len(executableCandidates) == 1:
			selectedFirstWaveTaskIDs = map[uuid.UUID]struct{}{executableCandidates[0].ID: {}}
		}
		if err := e.persistBootstrapFirstWaveSelection(ctx, projectTasks, selectedFirstWaveTaskIDs); err != nil {
			return nil, err
		}
	}
	tasksBySlug := make(map[string]repo.ProjectTask)
	for _, taskRecord := range projectTasks {
		metadata := metadataObject(taskRecord.Metadata)
		setupTask, _ := metadata["bootstrap_setup_task"].(bool)
		if !setupTask {
			continue
		}
		slug := strings.TrimSpace(readStringValue(metadata["bootstrap_step_slug"]))
		if slug == "" {
			continue
		}
		tasksBySlug[slug] = taskRecord
	}

	completed := make([]map[string]any, 0, len(stepSlugs))
	missing := make([]string, 0)
	signoffSummary, _ := readString(input, "sign_off_summary")
	for _, rawSlug := range stepSlugs {
		slug := normalizeBootstrapStepSlug(rawSlug)
		if slug == "" {
			continue
		}
		taskRecord, exists := tasksBySlug[slug]
		if !exists {
			if slug == "bootstrap-governance-gate" {
				completed = append(completed, map[string]any{
					"step_slug": slug,
					"status":    "accepted_noop",
					"reason":    "bootstrap governance gate completion is derived from checklist progress and validation, not persisted directly",
				})
				continue
			}
			missing = append(missing, slug)
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "done") {
			payload := map[string]any{
				"bootstrap_setup_persisted": true,
				"bootstrap_step_slug":       slug,
			}
			if strings.TrimSpace(signoffSummary) != "" {
				payload["sign_off_summary"] = strings.TrimSpace(signoffSummary)
			}
			updated, transitionErr := e.taskService.TransitionStatusWithPayload(ctx, taskRecord.ID, "done", tasksvc.Actor{
				Type:                        "system",
				AllowFlowRuntimeBypass:      true,
				AllowDoneBypass:             true,
				AllowBootstrapSetupComplete: true,
			}, payload)
			if transitionErr != nil {
				return nil, transitionErr
			}
			taskRecord = repo.ProjectTask(*updated)
			tasksBySlug[slug] = taskRecord
		}
		completed = append(completed, map[string]any{
			"task_id":     taskRecord.ID,
			"task_number": taskRecord.TaskNumber,
			"title":       taskRecord.Title,
			"step_slug":   slug,
			"work_status": taskRecord.WorkStatus,
		})
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return map[string]any{
			"error":              "unknown_bootstrap_step",
			"missing_step_slugs": missing,
			"valid_step_slugs":   validBootstrapSetupStepSlugs(),
			"message":            "Use the canonical bootstrap setup step slugs returned in valid_step_slugs.",
		}, nil
	}
	remaining := make([]string, 0)
	for _, slug := range validBootstrapSetupStepSlugs() {
		if slug == "bootstrap-governance-gate" {
			continue
		}
		taskRecord, exists := tasksBySlug[slug]
		if !exists || !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "done") {
			remaining = append(remaining, slug)
		}
	}
	sort.Slice(completed, func(i, j int) bool {
		return fmt.Sprintf("%v", completed[i]["step_slug"]) < fmt.Sprintf("%v", completed[j]["step_slug"])
	})
	blockedTaskIDs, err := e.loadBootstrapBlockedTaskIDs(ctx, projectID)
	if err != nil {
		return nil, err
	}
	selectableHints := bootstrapFirstWaveSelectionHints(bootstrapFirstWaveSelectableTasksExcludingBlocked(projectTasks, blockedTaskIDs))
	response := map[string]any{
		"project_id":               projectID,
		"status":                   "persisted",
		"completed_steps":          completed,
		"setup_checklist_complete": len(remaining) == 0,
		"remaining_step_slugs":     remaining,
	}
	if len(selectedFirstWaveTaskIDs) > 0 {
		response["selected_first_wave_task_ids"] = orderedUUIDStrings(selectedFirstWaveTaskIDs)
	}
	if stringSliceContains(remaining, "select-first-wave") && len(selectableHints) > 0 {
		response["selectable_first_wave_tasks"] = selectableHints
	}
	if len(remaining) > 0 {
		response["message"] = fmt.Sprintf("Bootstrap setup is not complete yet. Persist the remaining canonical step slugs next: %s.", strings.Join(remaining, ", "))
	} else {
		response["message"] = "Bootstrap setup checklist is fully persisted. The governance gate will complete automatically once validation passes."
	}
	return response, nil
}

func resolveBootstrapFirstWaveSelection(input map[string]any, tasks []repo.ProjectTask) (map[uuid.UUID]struct{}, bool, error) {
	selected := make(map[uuid.UUID]struct{})
	ids := readStringSlice(input, "first_wave_task_ids")
	numbers := readStringSlice(input, "first_wave_task_numbers")
	explicit := len(ids) > 0 || len(numbers) > 0
	if !explicit {
		return selected, false, nil
	}

	byID := make(map[string]repo.ProjectTask, len(tasks))
	byNumber := make(map[string]repo.ProjectTask, len(tasks))
	for _, task := range tasks {
		if task.ID != uuid.Nil {
			byID[task.ID.String()] = task
		}
		byNumber[strconv.Itoa(task.TaskNumber)] = task
	}

	for _, raw := range ids {
		parsed, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil || parsed == uuid.Nil {
			return nil, true, fmt.Errorf("invalid first-wave task id %q", raw)
		}
		task, ok := byID[parsed.String()]
		if !ok {
			return nil, true, fmt.Errorf("first-wave task id %q does not belong to this project", raw)
		}
		selected[task.ID] = struct{}{}
	}
	for _, raw := range numbers {
		task, ok := byNumber[strings.TrimSpace(raw)]
		if !ok {
			return nil, true, fmt.Errorf("first-wave task number %q does not belong to this project", raw)
		}
		selected[task.ID] = struct{}{}
	}
	return selected, true, nil
}

func bootstrapFirstWaveSelectableTasks(tasks []repo.ProjectTask) []repo.ProjectTask {
	return bootstrapFirstWaveSelectableTasksExcludingBlocked(tasks, nil)
}

func bootstrapFirstWaveSelectableTasksExcludingBlocked(tasks []repo.ProjectTask, blockedTaskIDs map[uuid.UUID]struct{}) []repo.ProjectTask {
	parentTaskIDs := bootstrapDecompositionParentTaskIDs(tasks)
	selectable := make([]repo.ProjectTask, 0, len(tasks))
	for _, task := range tasks {
		if bootstrapGateTask(task) || bootstrapSetupTask(task) {
			continue
		}
		if bootstrapFirstWaveTaskRequiresChildren(task) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(task.BlocksScope), "all") {
			continue
		}
		if _, isParent := parentTaskIDs[task.ID]; isParent {
			continue
		}
		if _, blocked := blockedTaskIDs[task.ID]; blocked {
			continue
		}
		if bootstrapFirstWaveTaskShouldStayDeferred(task) {
			continue
		}
		selectable = append(selectable, task)
	}
	return selectable
}

func bootstrapFirstWaveTaskRequiresChildren(task repo.ProjectTask) bool {
	return taskRequiresBoundedChildren(task)
}

func (e *NativeToolExecutor) loadBootstrapBlockedTaskIDs(ctx context.Context, projectID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	blocked := make(map[uuid.UUID]struct{})
	if e == nil || e.pool == nil || projectID == uuid.Nil {
		return blocked, nil
	}
	rows, err := e.pool.Query(ctx, `
		SELECT DISTINCT d.source_id
		FROM project_task_dependency d
		JOIN project_task source_task ON source_task.id = d.source_id
		JOIN project_task depends_on_task ON depends_on_task.id = d.depends_on_id
		WHERE source_task.project_id = $1
		  AND d.source_type = 'project_task'
		  AND d.depends_on_type = 'project_task'
		  AND lower(trim(depends_on_task.work_status)) NOT IN ('done', 'cancelled')
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID uuid.UUID
		if scanErr := rows.Scan(&taskID); scanErr != nil {
			return nil, scanErr
		}
		if taskID != uuid.Nil {
			blocked[taskID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return blocked, nil
}

func bootstrapDecompositionParentTaskIDs(tasks []repo.ProjectTask) map[uuid.UUID]struct{} {
	parentIDs := make(map[uuid.UUID]struct{})
	for _, task := range tasks {
		metadata := metadataObject(task.Metadata)
		parentIDText := strings.TrimSpace(readStringValue(metadata["decomposition_parent_task_id"]))
		if parentIDText == "" {
			continue
		}
		parentID, err := uuid.Parse(parentIDText)
		if err != nil || parentID == uuid.Nil {
			continue
		}
		parentIDs[parentID] = struct{}{}
	}
	return parentIDs
}

func bootstrapFirstWaveSelectionHints(tasks []repo.ProjectTask) []map[string]any {
	if len(tasks) == 0 {
		return nil
	}
	hints := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		entry := map[string]any{
			"task_id":     task.ID.String(),
			"task_number": task.TaskNumber,
			"title":       strings.TrimSpace(task.Title),
		}
		if task.AssignedAgentID != nil && *task.AssignedAgentID != uuid.Nil {
			entry["assigned_agent_id"] = task.AssignedAgentID.String()
		}
		hints = append(hints, entry)
	}
	return hints
}

func inferBootstrapFirstWaveSelection(tasks []repo.ProjectTask) map[uuid.UUID]struct{} {
	if len(tasks) == 0 {
		return nil
	}
	selected := make(map[uuid.UUID]struct{})
	known := 0
	for _, task := range tasks {
		hint, ok := bootstrapFirstWaveSelectionHint(task)
		if !ok {
			continue
		}
		known++
		if hint {
			selected[task.ID] = struct{}{}
		}
	}
	if known != len(tasks) || len(selected) == 0 {
		return nil
	}
	return selected
}

func bootstrapFirstWaveSelectionHint(task repo.ProjectTask) (bool, bool) {
	metadata := metadataObject(task.Metadata)
	if raw, ok := metadata["bootstrap_first_wave_selected"]; ok {
		if selected, valid := raw.(bool); valid {
			return selected, true
		}
	}

	title := strings.ToLower(strings.TrimSpace(task.Title))
	switch {
	case strings.HasPrefix(title, "fw-"),
		strings.HasPrefix(title, "fw:"),
		strings.HasPrefix(title, "[fw]"),
		strings.HasPrefix(title, "[fw-"),
		strings.Contains(title, "first-wave"),
		strings.Contains(title, "first wave"):
		return true, true
	case strings.HasPrefix(title, "lw-"),
		strings.HasPrefix(title, "lw:"),
		strings.HasPrefix(title, "[lw]"),
		strings.HasPrefix(title, "[lw-"),
		strings.Contains(title, "later-wave"),
		strings.Contains(title, "later wave"),
		strings.Contains(title, "deferred"),
		strings.Contains(title, "next wave"):
		return false, true
	default:
		return false, false
	}
}

func bootstrapFirstWaveTaskShouldStayDeferred(task repo.ProjectTask) bool {
	if selected, known := bootstrapFirstWaveSelectionHint(task); known {
		return !selected
	}
	title := strings.ToLower(strings.TrimSpace(task.Title))
	if title == "" {
		return false
	}
	return strings.Contains(title, "final report") ||
		strings.Contains(title, "pass/fail determination") ||
		strings.Contains(title, "summary & report") ||
		strings.Contains(title, "summary and report") ||
		(strings.Contains(title, "risk summary") && strings.Contains(title, "recommendation"))
}

func bootstrapTaskByID(tasks []repo.ProjectTask, taskID uuid.UUID) (repo.ProjectTask, bool) {
	for _, task := range tasks {
		if task.ID == taskID {
			return task, true
		}
	}
	return repo.ProjectTask{}, false
}

func (e *NativeToolExecutor) persistBootstrapFirstWaveSelection(ctx context.Context, tasks []repo.ProjectTask, selected map[uuid.UUID]struct{}) error {
	if e == nil || e.tasks == nil {
		return nil
	}
	for _, task := range tasks {
		if bootstrapGateTask(task) || bootstrapSetupTask(task) {
			continue
		}
		metadata := metadataObject(task.Metadata)
		if metadata == nil {
			metadata = make(map[string]any)
		}
		_, isSelected := selected[task.ID]
		metadata["bootstrap_first_wave_selected"] = isSelected
		updatedMetadata, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		task.Metadata = updatedMetadata
		if _, err := e.tasks.Update(ctx, task); err != nil {
			return err
		}
	}
	return nil
}

func orderedUUIDStrings(items map[uuid.UUID]struct{}) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for id := range items {
		if id == uuid.Nil {
			continue
		}
		out = append(out, id.String())
	}
	sort.Strings(out)
	return out
}

func stringSliceContains(items []string, needle string) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(needle)) {
			return true
		}
	}
	return false
}

func validBootstrapSetupStepSlugs() []string {
	return []string{
		"bootstrap-governance-gate",
		"bind-repo-environment",
		"staff-project",
		"decompose-workstreams",
		"validate-task-shape",
		"attach-validate-flow-templates",
		"select-first-wave",
		"record-frank-sign-off",
	}
}

func normalizeBootstrapStepSlugs(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		for _, slug := range expandBootstrapStepSlug(raw) {
			if slug == "" {
				continue
			}
			if _, exists := seen[slug]; exists {
				continue
			}
			seen[slug] = struct{}{}
			out = append(out, slug)
		}
	}
	return out
}

func expandBootstrapStepSlug(value string) []string {
	slug := strings.ToLower(strings.TrimSpace(value))
	switch slug {
	case "assign-staff", "assign_staff", "assign-agents", "assign_agents":
		return []string{"staff-project"}
	case "first-wave-assignments", "first_wave_assignments":
		return []string{"staff-project", "select-first-wave"}
	case "staffing-plan", "staffing_plan":
		return []string{"staff-project"}
	case "task-scaffold", "task_scaffold":
		return []string{"decompose-workstreams", "validate-task-shape"}
	case "create-tasks", "create_tasks", "create-first-wave-tasks", "create_first_wave_tasks", "first-wave-tasks", "first_wave_tasks":
		return []string{"decompose-workstreams", "validate-task-shape"}
	default:
		return []string{normalizeBootstrapStepSlug(slug)}
	}
}

func normalizeBootstrapStepSlug(value string) string {
	slug := strings.ToLower(strings.TrimSpace(value))
	switch slug {
	case "bootstrap-governance":
		return "bootstrap-governance-gate"
	case "bind-repo", "bind_repo", "bind-repo-and-environment":
		return "bind-repo-environment"
	case "staff-the-project", "staffing", "staff_project", "task_assignments", "task-assignments", "assign_tasks", "task_assignment", "staffing_assignments", "staffing-assignments":
		return "staff-project"
	case "task-decomposition", "task_decomposition", "decompose_tasks", "decompose_task_tree":
		return "decompose-workstreams"
	case "validate-sizing", "validate-task-sizing", "validate_tasks", "validate_task_shape", "dependency-wiring", "dependency_wiring":
		return "validate-task-shape"
	case "attach-flows", "attach_flows", "attach-flow-templates", "attach-validate-flows", "attach-flow-template", "attach-and-validate-flow-templates", "flow-templates", "flow_templates", "flow_attachments":
		return "attach-validate-flow-templates"
	case "first-wave-selection", "select_first_wave", "first_wave_selection", "first_wave_promotion", "first-wave-promotion":
		return "select-first-wave"
	case "frank-sign-off", "frank_sign_off", "record-sign-off":
		return "record-frank-sign-off"
	default:
		return slug
	}
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
	if flowTemplateID != nil && *flowTemplateID != uuid.Nil {
		return taskplan.Plan{}, flowTemplateID, metadata, nil
	}

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

func bootstrapWaveFamilyKey(title string) string {
	matches := bootstrapWaveFamilyTitlePattern.FindStringSubmatch(strings.TrimSpace(title))
	if len(matches) != 3 {
		return ""
	}
	return strings.ToLower(matches[1]) + strings.TrimSpace(matches[2])
}

func canonicalParentChildTitleKey(value string) string {
	normalized := normalizeComparableText(value)
	if normalized == "" {
		return ""
	}
	normalized = strings.Trim(normalized, ":- ")
	for _, suffix := range []string{" layout", " template", " design", " option"} {
		normalized = strings.TrimSpace(strings.TrimSuffix(normalized, suffix))
	}
	if matches := parentChildOrdinalTitlePattern.FindStringSubmatch(normalized); len(matches) == 3 {
		return matches[1] + " " + matches[2]
	}
	return normalized
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

func (e *NativeToolExecutor) findReusableParentScopedChildTask(ctx context.Context, parentTask repo.ProjectTask, children []repo.ProjectTask, desired repo.ProjectTask) (repo.ProjectTask, bool, error) {
	desiredKey := canonicalParentChildTitleKey(desired.Title)
	if desiredKey == "" {
		return repo.ProjectTask{}, false, nil
	}
	var reusable *repo.ProjectTask
	for i := range children {
		child := children[i]
		if isTaskTerminal(child.WorkStatus) {
			continue
		}
		if taskdecomp.ParseParentTaskID(child.Metadata) != parentTask.ID {
			continue
		}
		if canonicalParentChildTitleKey(child.Title) != desiredKey {
			continue
		}
		if reusable == nil || taskCanonicalLess(child, *reusable) {
			candidate := child
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
		// No-task-service execution is a narrow fallback/test seam; pool-backed executors auto-build
		// the canonical service and should not queue child work through raw task updates.
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

func taskRequiresBoundedChildren(task repo.ProjectTask) bool {
	if taskMetadataMarksOrchestrationOnly(task.Metadata) {
		return true
	}
	plan, ok := taskplan.Parse(task.Metadata)
	if !ok {
		return false
	}
	stopReason := strings.ToLower(strings.TrimSpace(plan.FollowOnStopReason))
	return strings.Contains(stopReason, "parent task is orchestration-only")
}

func taskMetadataMarksOrchestrationOnly(metadata json.RawMessage) bool {
	if len(metadata) == 0 {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return false
	}
	decomp, _ := payload["decomposition"].(map[string]any)
	if decomp == nil {
		return false
	}
	orchestrationOnly, _ := decomp["orchestration_only"].(bool)
	return orchestrationOnly
}

func bootstrapSetupStillActive(projectTasks []repo.ProjectTask) bool {
	for _, taskRecord := range projectTasks {
		metadata := metadataObject(taskRecord.Metadata)
		setupTask, _ := metadata["bootstrap_setup_task"].(bool)
		if !setupTask {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "done") {
			return true
		}
	}
	return false
}

func (e *NativeToolExecutor) inferBootstrapExecutableAssignee(ctx context.Context, projectID uuid.UUID) (*uuid.UUID, error) {
	if projectID == uuid.Nil || e.assignments == nil {
		return nil, nil
	}
	assignments, err := e.assignments.ListByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var activeWorkerID *uuid.UUID
	for _, assignment := range assignments {
		if !assignment.IsActive || !strings.EqualFold(strings.TrimSpace(assignment.Role), "worker") {
			continue
		}
		if activeWorkerID != nil {
			return nil, nil
		}
		id := assignment.AgentID
		activeWorkerID = &id
	}
	return activeWorkerID, nil
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
	activeExecutionID := uuid.Nil
	activeExecutionVisit := -1
	if e.flowExecs != nil {
		executions, err := e.flowExecs.ListByTask(ctx, taskID)
		if err != nil {
			return nil, err
		}
		for _, execution := range executions {
			if !strings.EqualFold(strings.TrimSpace(execution.Status), "active") {
				continue
			}
			if activeExecutionID == uuid.Nil || execution.VisitNumber > activeExecutionVisit {
				activeExecutionID = execution.ID
				activeExecutionVisit = execution.VisitNumber
			}
		}
	}
	var (
		activeExecutionSession *repo.ChatSession
		newestBlank            *repo.ChatSession
		latestNonBlank         *repo.ChatSession
		duplicates             []repo.ChatSession
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
		if activeExecutionID != uuid.Nil && sessionHasFlowExecution(session, activeExecutionID) {
			candidate := session
			activeExecutionSession = &candidate
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
	if activeExecutionSession != nil {
		return activeExecutionSession, nil
	}
	if newestBlank != nil && (latestNonBlank == nil || taskAsyncSessionMoreRecent(*newestBlank, *latestNonBlank)) {
		return newestBlank, nil
	}
	if latestNonBlank != nil {
		return latestNonBlank, nil
	}
	return newestBlank, nil
}

func sessionHasFlowExecution(session repo.ChatSession, executionID uuid.UUID) bool {
	if executionID == uuid.Nil {
		return false
	}
	metadata := metadataObject(session.Metadata)
	if metadata == nil {
		return false
	}
	raw, ok := metadata["flow_node_execution_id"]
	if !ok {
		return false
	}
	switch typed := raw.(type) {
	case string:
		parsed, err := uuid.Parse(strings.TrimSpace(typed))
		return err == nil && parsed == executionID
	default:
		return false
	}
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
	bootstrapFamilyKey := ""
	if bootstrapSetupStillActive(tasks) {
		bootstrapFamilyKey = bootstrapWaveFamilyKey(desired.Title)
	}
	var reusable *repo.ProjectTask
	for i := range tasks {
		taskRecord := tasks[i]
		if isTaskTerminal(taskRecord.WorkStatus) {
			continue
		}
		titleMatches := normalizeComparableText(taskRecord.Title) == desiredTitle
		if !titleMatches {
			if bootstrapFamilyKey == "" || bootstrapWaveFamilyKey(taskRecord.Title) != bootstrapFamilyKey {
				continue
			}
		}
		existingDescription := normalizeComparableText(derefString(taskRecord.Description))
		if desiredDescription != "" && existingDescription != "" && existingDescription != desiredDescription && titleMatches {
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
		if desiredTitle := strings.TrimSpace(desired.Title); desiredTitle != "" &&
			normalizeComparableText(updated.Title) != normalizeComparableText(desiredTitle) {
			updated.Title = desiredTitle
			changed = true
			if desiredDescription := strings.TrimSpace(derefString(desired.Description)); desiredDescription != "" {
				updated.Description = &desiredDescription
			}
		}
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
		if updated.AssignedAgentID == nil && desired.AssignedAgentID != nil && *desired.AssignedAgentID != uuid.Nil {
			assignedAgentID := *desired.AssignedAgentID
			updated.AssignedAgentID = &assignedAgentID
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
	if taskdecomp.ParseParentTaskID(taskRecord.Metadata) != uuid.Nil {
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
	existingChildrenByCanonicalTitle := map[string]repo.ProjectTask{}
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
		if key := canonicalParentChildTitleKey(projectTask.Title); key != "" {
			if current, exists := existingChildrenByCanonicalTitle[key]; !exists || taskCanonicalLess(projectTask, current) {
				existingChildrenByCanonicalTitle[key] = projectTask
			}
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
			AssignedAgentID:     taskRecord.AssignedAgentID,
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
		if existingChild, ok := existingChildrenByCanonicalTitle[canonicalParentChildTitleKey(childDraft.Title)]; ok {
			repairedChild, repairErr := e.repairTaskIfNeeded(ctx, existingChild, desiredChild)
			if repairErr != nil {
				return queueDecompositionResult{}, repairErr
			}
			childTaskIDs = append(childTaskIDs, repairedChild.ID)
			continue
		}
		var createdChild repo.ProjectTask
		if e.taskService != nil {
			requiresHumanReview := taskRecord.RequiresHumanReview
			createdRecord, createErr := e.taskService.CreateTask(ctx, tasksvc.CreateTaskRequest{
				ProjectID:           taskRecord.ProjectID,
				Title:               childDraft.Title,
				Description:         childDraft.Description,
				AssignedAgentID:     taskRecord.AssignedAgentID,
				FlowTemplateID:      taskRecord.FlowTemplateID,
				Priority:            taskRecord.Priority,
				CreatedByType:       actor.createdByType,
				CreatedByID:         actor.createdByID,
				RequiresHumanReview: &requiresHumanReview,
				Metadata:            childDraft.Metadata,
			})
			if createErr != nil {
				return queueDecompositionResult{}, createErr
			}
			createdChild = *createdRecord
		} else {
			if e.pool != nil {
				return queueDecompositionResult{}, fmt.Errorf("canonical task service unavailable")
			}
			// Pool-less/native test executors can still fall back to direct repo creation. The
			// production runtime must route decomposed child creation through the canonical task service.
			createdRecord, createErr := e.tasks.Create(ctx, repo.ProjectTask{
				OrganizationID:      taskRecord.OrganizationID,
				ProjectID:           taskRecord.ProjectID,
				Title:               childDraft.Title,
				Description:         childDraft.Description,
				WorkStatus:          "draft",
				AssignedAgentID:     taskRecord.AssignedAgentID,
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
			createdChild = createdRecord
		}
		if key := canonicalParentChildTitleKey(createdChild.Title); key != "" {
			if current, exists := existingChildrenByCanonicalTitle[key]; !exists || taskCanonicalLess(createdChild, current) {
				existingChildrenByCanonicalTitle[key] = createdChild
			}
		}
		childTaskIDs = append(childTaskIDs, createdChild.ID)
		if e.taskService == nil {
			if err := e.publishTaskCreatedEvent(ctx, nil, createdChild, &taskRecord.ID, true); err != nil {
				return queueDecompositionResult{}, err
			}
		}
	}

	primary := strings.TrimSpace(prepared.Plan.PrimaryDeliverable)
	if primary == "" {
		taskRecord.Description = nil
	} else {
		taskRecord.Description = &primary
	}
	taskRecord.Metadata = taskdecomp.ApplyMetadata(taskRecord.Metadata, prepared.Plan, prepared.SourceDescription, childTaskIDs)
	taskRecord.BlocksScope = "none"

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
	sourceType = normalizeTaskDependencyScopeType(sourceType)
	dependsOnType = normalizeTaskDependencyScopeType(dependsOnType)
	if sourceType == "" || dependsOnType == "" {
		return map[string]any{
			"error":       "invalid_dependency_type",
			"valid_types": []string{"project_task", "project_subtask"},
			"message":     "Use source_type and depends_on_type of project_task or project_subtask.",
		}, nil
	}
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

func normalizeTaskDependencyScopeType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "task", "project_task":
		return "project_task"
	case "subtask", "project_subtask":
		return "project_subtask"
	default:
		return ""
	}
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
		if taskID, hasTaskID := readUUID(input, "task_id"); hasTaskID && taskID != uuid.Nil {
			return map[string]any{
				"error":   "flow_node_execution_id_required",
				"message": "subtask.create requires an active flow_node_execution_id, not task_id. During bootstrap planning, create bounded child tasks with task.create under parent_task_id instead.",
			}, nil
		}
		return map[string]any{"error": "flow_node_execution_id_required"}, nil
	}
	title, ok := readString(input, "title")
	if !ok || title == "" {
		return map[string]any{"error": "title_required"}, nil
	}
	execution, err := e.flowExecs.GetByID(ctx, flowNodeExecutionID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{
				"error":   "flow_node_execution_not_found",
				"message": "subtask.create requires an active flow_node_execution_id from a running task execution. Do not pass task_id here. During bootstrap planning, use task.create with parent_task_id to split broad work into child tasks.",
			}, nil
		}
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
	activeExecution, err := e.flowService.EnsureActiveExecution(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if commitSHA, ok := readString(input, "commit_sha"); ok && commitSHA != "" {
		if _, err := e.flowService.RecordNodeCommit(ctx, taskID, commitSHA, ""); err != nil {
			return nil, err
		}
	} else {
		commitResult, err := e.createCanonicalExecutionCommit(ctx, *activeExecution, "", "")
		if err != nil {
			_ = e.recordCommitCloseFailure(ctx, *activeExecution, err)
			return nil, err
		}
		if _, err := e.flowService.RecordNodeCommit(ctx, taskID, commitResult.SHA, commitResult.BranchName); err != nil {
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
	reason, _ := readString(input, "reason")
	findings, _ := readString(input, "findings")
	if updatedExecution, err := e.recordReviewDecisionMetadata(ctx, execution, decision, reason, findings); err != nil {
		return nil, err
	} else {
		execution = updatedExecution
	}
	if decision == "approve" {
		dirty, err := e.reviewExecutionWorkspaceDirty(ctx, execution.TaskID)
		if err != nil {
			return nil, err
		}
		if dirty {
			autoCommitted, autoErr := e.autoCommitMissingWorkBeforeReviewApproval(ctx, execution)
			if autoErr != nil {
				return nil, autoErr
			}
			if autoCommitted {
				dirty, err = e.reviewExecutionWorkspaceDirty(ctx, execution.TaskID)
				if err != nil {
					return nil, err
				}
			}
		}
		if dirty {
			return map[string]any{
				"error":   "review_approval_requires_clean_workspace",
				"message": "Approval must close with an empty runtime-owned commit. The review workspace still has file changes. Either discard them or reject the review with findings.",
			}, nil
		}
		commitResult, err := e.createCanonicalExecutionCommit(ctx, execution, "approve", "")
		if err != nil {
			_ = e.recordCommitCloseFailure(ctx, execution, err)
			return nil, err
		}
		if _, err := e.flowService.RecordNodeCommit(ctx, execution.TaskID, commitResult.SHA, commitResult.BranchName); err != nil {
			return nil, err
		}
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

	rejectionSummary := strings.TrimSpace(reason)
	if rejectionSummary == "" {
		rejectionSummary = strings.TrimSpace(findings)
	}
	commitResult, err := e.createCanonicalExecutionCommit(ctx, execution, "reject", rejectionSummary)
	if err != nil {
		_ = e.recordCommitCloseFailure(ctx, execution, err)
		return nil, err
	}
	if _, err := e.flowService.RecordNodeCommit(ctx, execution.TaskID, commitResult.SHA, commitResult.BranchName); err != nil {
		return nil, err
	}
	next, err := e.flowService.RejectFlowNode(ctx, execution.TaskID, flowActorFromExecutionActor(actorFromContext(ctx)))
	if err != nil {
		return nil, err
	}
	if e.tasks != nil {
		taskRecord, getErr := e.tasks.GetByID(ctx, execution.TaskID)
		if getErr == nil && strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "blocked") {
			return map[string]any{
				"blocked": true,
				"message": "review rejection recorded, but the reject path has exhausted its allowed visits and the task is now blocked",
			}, nil
		}
	}
	return map[string]any{"next_node_id": next.FlowNodeID}, nil
}

func (e *NativeToolExecutor) handleFlowRecoveryDecision(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.flowExecs == nil || e.tasks == nil || e.flowNodes == nil || e.taskService == nil {
		return map[string]any{"error": "flow_recovery_unavailable"}, nil
	}
	executionID, ok := readUUID(input, "flow_node_execution_id")
	if !ok || executionID == uuid.Nil {
		return map[string]any{"error": "flow_node_execution_id_required"}, nil
	}
	decision, ok := readString(input, "decision")
	if !ok {
		return map[string]any{"error": "decision_required"}, nil
	}
	decision = strings.ToLower(strings.TrimSpace(decision))
	reason, _ := readString(input, "reason")
	reason = strings.TrimSpace(reason)

	execution, err := e.flowExecs.GetByID(ctx, executionID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"error": "flow_node_execution_not_found"}, nil
		}
		return nil, err
	}
	taskRecord, err := e.tasks.GetByID(ctx, execution.TaskID)
	if err != nil {
		return nil, err
	}
	node, err := e.flowNodes.GetByID(ctx, execution.FlowNodeID)
	if err != nil {
		return nil, err
	}

	switch decision {
	case "resume":
		if !strings.EqualFold(strings.TrimSpace(execution.Status), "active") {
			return map[string]any{"error": "resume_requires_active_execution"}, nil
		}
		return e.resumeExecutionAfterRecoveryDecision(ctx, execution, taskRecord, node, reason)
	case "retry":
		return e.retryExecutionAfterRecoveryDecision(ctx, execution, taskRecord, node, reason)
	case "block", "escalate":
		return e.recordBlockingRecoveryDecision(ctx, execution, taskRecord, decision, reason)
	default:
		return map[string]any{"error": "invalid_recovery_decision"}, nil
	}
}

func (e *NativeToolExecutor) resumeExecutionAfterRecoveryDecision(ctx context.Context, execution repo.FlowNodeExecution, taskRecord repo.ProjectTask, node repo.FlowNode, reason string) (map[string]any, error) {
	updatedExecution, err := e.recordRecoveryDecisionMetadata(ctx, execution, "resume", reason)
	if err != nil {
		return nil, err
	}
	checkpoint, _ := repo.FlowExecutionRecoveryCheckpointFromMetadata(updatedExecution.Metadata)
	if checkpoint != nil {
		checkpoint.ResumeAction = "start_new_turn"
		if reason != "" {
			checkpoint.FailureSummary = reason
		}
		now := time.Now().UTC()
		checkpoint.UpdatedAt = &now
		updatedExecution, err = e.flowExecs.UpdateMetadata(ctx, updatedExecution.ID, repo.FlowExecutionMetadataWithRecoveryCheckpoint(updatedExecution.Metadata, checkpoint))
		if err != nil {
			return nil, err
		}
	}
	waiting := recoveryDecisionRuntimeSubstate(node)
	if _, err := e.flowExecs.UpdateRuntimeSubstate(ctx, updatedExecution.ID, &waiting); err != nil {
		return nil, err
	}
	targetStatus := recoveryDecisionTaskStatus(node)
	transitioned, err := e.taskService.TransitionStatusWithPayload(ctx, taskRecord.ID, targetStatus, tasksvc.Actor{
		Type: "agent",
		ID:   actorFromContext(ctx).createdByID,
	}, map[string]any{
		"transition_source":        "flow_recovery_decision",
		"flow_node_execution_id":   updatedExecution.ID.String(),
		"recovery_decision":        "resume",
		"recovery_decision_reason": reason,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"task_id":                taskRecord.ID.String(),
		"task_work_status":       transitioned.WorkStatus,
		"flow_node_execution_id": updatedExecution.ID.String(),
		"recovery_decision":      "resume",
		"runtime_substate":       waiting,
	}, nil
}

func (e *NativeToolExecutor) retryExecutionAfterRecoveryDecision(ctx context.Context, execution repo.FlowNodeExecution, taskRecord repo.ProjectTask, node repo.FlowNode, reason string) (map[string]any, error) {
	updatedExecution, err := e.recordRecoveryDecisionMetadata(ctx, execution, "retry", reason)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(updatedExecution.Status), "active") {
		if e.pool == nil {
			return map[string]any{"error": "flow_execution_repository_unavailable"}, nil
		}
		abandoned, abandonErr := repo.NewFlowNodeExecutionRepo(e.pool).Abandon(ctx, updatedExecution.ID)
		if abandonErr != nil {
			return nil, abandonErr
		}
		updatedExecution = abandoned
	}
	if strings.EqualFold(strings.TrimSpace(node.NodeType), "review") || node.RequiresHumanReview {
		retryNodeID, retryNode, retryErr := e.recoveryDecisionRetryNode(ctx, node)
		if retryErr != nil {
			return nil, retryErr
		}
		if _, err := e.tasks.SetFlowNode(ctx, taskRecord.ID, &retryNodeID); err != nil {
			return nil, err
		}
		taskRecord.CurrentFlowNodeID = &retryNodeID
		node = retryNode
	}
	targetStatus := recoveryDecisionTaskStatus(node)
	transitionedStatus := strings.TrimSpace(taskRecord.WorkStatus)
	if !strings.EqualFold(transitionedStatus, targetStatus) {
		if strings.EqualFold(transitionedStatus, "in_progress") && strings.EqualFold(targetStatus, "queued") {
			targetStatus = transitionedStatus
		} else {
			transitioned, err := e.taskService.TransitionStatusWithPayload(ctx, taskRecord.ID, targetStatus, tasksvc.Actor{
				Type: "agent",
				ID:   actorFromContext(ctx).createdByID,
			}, map[string]any{
				"transition_source":        "flow_recovery_decision",
				"flow_node_execution_id":   updatedExecution.ID.String(),
				"recovery_decision":        "retry",
				"recovery_decision_reason": reason,
			})
			if err != nil {
				return nil, err
			}
			transitionedStatus = transitioned.WorkStatus
		}
	}
	if e.flowService == nil {
		return map[string]any{"error": "flow_service_unavailable"}, nil
	}
	nextExecution, err := e.flowService.EnsureActiveExecution(ctx, taskRecord.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"task_id":                taskRecord.ID.String(),
		"task_work_status":       transitionedStatus,
		"previous_execution_id":  updatedExecution.ID.String(),
		"flow_node_execution_id": nextExecution.ID.String(),
		"recovery_decision":      "retry",
	}, nil
}

func (e *NativeToolExecutor) recoveryDecisionRetryNode(ctx context.Context, node repo.FlowNode) (uuid.UUID, repo.FlowNode, error) {
	if strings.EqualFold(strings.TrimSpace(node.NodeType), "review") || node.RequiresHumanReview {
		if node.RejectNodeID != nil && *node.RejectNodeID != uuid.Nil {
			rejectNode, err := e.flowNodes.GetByID(ctx, *node.RejectNodeID)
			if err != nil {
				return uuid.Nil, repo.FlowNode{}, err
			}
			return rejectNode.ID, rejectNode, nil
		}
		nodes, err := e.flowNodes.GetByTemplateOrdered(ctx, node.FlowTemplateID)
		if err != nil {
			return uuid.Nil, repo.FlowNode{}, err
		}
		for i, candidate := range nodes {
			if candidate.ID != node.ID {
				continue
			}
			if i == 0 {
				break
			}
			rejectNode := nodes[i-1]
			return rejectNode.ID, rejectNode, nil
		}
	}
	return node.ID, node, nil
}

func (e *NativeToolExecutor) recordBlockingRecoveryDecision(ctx context.Context, execution repo.FlowNodeExecution, taskRecord repo.ProjectTask, decision, reason string) (map[string]any, error) {
	updatedExecution, err := e.recordRecoveryDecisionMetadata(ctx, execution, decision, reason)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(updatedExecution.Status), "active") {
		stalled := "stalled"
		if _, err := e.flowExecs.UpdateRuntimeSubstate(ctx, updatedExecution.ID, &stalled); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"task_id":                taskRecord.ID.String(),
		"task_work_status":       taskRecord.WorkStatus,
		"flow_node_execution_id": updatedExecution.ID.String(),
		"recovery_decision":      decision,
	}, nil
}

func (e *NativeToolExecutor) recordRecoveryDecisionMetadata(ctx context.Context, execution repo.FlowNodeExecution, decision, reason string) (repo.FlowNodeExecution, error) {
	if e.flowExecs == nil || execution.ID == uuid.Nil {
		return execution, nil
	}
	now := time.Now().UTC()
	metadata := repo.FlowExecutionMetadataWithRecoveryDecision(execution.Metadata, &repo.FlowExecutionRecoveryDecision{
		Decision:  strings.TrimSpace(decision),
		Reason:    strings.TrimSpace(reason),
		DecidedAt: &now,
	})
	return e.flowExecs.UpdateMetadata(ctx, execution.ID, metadata)
}

func recoveryDecisionTaskStatus(node repo.FlowNode) string {
	if strings.EqualFold(strings.TrimSpace(node.NodeType), "review") || node.RequiresHumanReview {
		return "review"
	}
	return "queued"
}

func recoveryDecisionRuntimeSubstate(node repo.FlowNode) string {
	if strings.EqualFold(strings.TrimSpace(node.NodeType), "review") || node.RequiresHumanReview {
		return "waiting_for_review"
	}
	return "waiting_for_turn"
}

func (e *NativeToolExecutor) createCanonicalExecutionCommit(ctx context.Context, execution repo.FlowNodeExecution, decision, summary string) (flowcommit.CommitResult, error) {
	taskRecord, err := e.tasks.GetByID(ctx, execution.TaskID)
	if err != nil {
		return flowcommit.CommitResult{}, err
	}
	node, err := e.flowNodes.GetByID(ctx, execution.FlowNodeID)
	if err != nil {
		return flowcommit.CommitResult{}, err
	}
	root, err := e.taskWorkspaceRoot(ctx, taskRecord)
	if err != nil {
		return flowcommit.CommitResult{}, err
	}
	branchName := strings.TrimSpace(taskBranchString(taskRecord.BranchName))
	if branchName == "" {
		branchName = fmt.Sprintf("task/%d", taskRecord.TaskNumber)
	}
	baseSHA := repo.FlowExecutionEntryHeadSHAFromMetadata(execution.Metadata)
	return flowcommit.CommitAllFromBase(ctx, root, branchName, baseSHA, canonicalExecutionCommitMessage(taskRecord, node, execution.VisitNumber, decision, summary), true)
}

func (e *NativeToolExecutor) reviewExecutionWorkspaceDirty(ctx context.Context, taskID uuid.UUID) (bool, error) {
	taskRecord, err := e.tasks.GetByID(ctx, taskID)
	if err != nil {
		return false, err
	}
	root, err := e.taskWorkspaceRoot(ctx, taskRecord)
	if err != nil {
		return false, err
	}
	return flowcommit.WorktreeDirty(ctx, root)
}

func (e *NativeToolExecutor) autoCommitMissingWorkBeforeReviewApproval(ctx context.Context, reviewExecution repo.FlowNodeExecution) (bool, error) {
	if e == nil || e.flowExecs == nil || e.flowNodes == nil {
		return false, nil
	}
	reviewNode, err := e.flowNodes.GetByID(ctx, reviewExecution.FlowNodeID)
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(strings.TrimSpace(reviewNode.NodeType), "review") {
		return false, nil
	}
	dirtyPaths, err := e.reviewExecutionWorkspaceDirtyPaths(ctx, reviewExecution.TaskID)
	if err != nil {
		return false, err
	}
	if len(dirtyPaths) == 0 {
		return false, nil
	}
	for _, path := range dirtyPaths {
		if reviewScopedWorkspacePath(path) {
			return false, nil
		}
	}
	executions, err := e.flowExecs.ListByTask(ctx, reviewExecution.TaskID)
	if err != nil {
		return false, err
	}
	var candidate *repo.FlowNodeExecution
	for i := range executions {
		execution := executions[i]
		if execution.ID == reviewExecution.ID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(execution.Status), "completed") {
			continue
		}
		if execution.CommitSHA != nil && strings.TrimSpace(*execution.CommitSHA) != "" {
			continue
		}
		node, nodeErr := e.flowNodes.GetByID(ctx, execution.FlowNodeID)
		if nodeErr != nil {
			return false, nodeErr
		}
		if strings.EqualFold(strings.TrimSpace(node.NodeType), "review") {
			continue
		}
		copied := execution
		candidate = &copied
	}
	if candidate == nil {
		return false, nil
	}
	commitResult, err := e.createCanonicalExecutionCommit(ctx, *candidate, "", "")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(commitResult.SHA) == "" {
		return false, nil
	}
	if _, err := e.flowExecs.RecordCommitSHA(ctx, candidate.ID, commitResult.SHA); err != nil {
		return false, err
	}
	return true, nil
}

func (e *NativeToolExecutor) reviewExecutionWorkspaceDirtyPaths(ctx context.Context, taskID uuid.UUID) ([]string, error) {
	taskRecord, err := e.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	root, err := e.taskWorkspaceRoot(ctx, taskRecord)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(output), "\n")
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		path = normalizeWorkspacePath(path)
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func reviewScopedWorkspacePath(path string) bool {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(path)), "/")
	switch {
	case strings.HasPrefix(normalized, "review/"),
		strings.HasPrefix(normalized, "reviews/"),
		strings.HasPrefix(normalized, ".ottercamp/review/"),
		strings.HasPrefix(normalized, ".ottercamp/reviews/"):
		return true
	default:
		return false
	}
}

func canonicalExecutionCommitMessage(taskRecord repo.ProjectTask, node repo.FlowNode, visitNumber int, decision, summary string) string {
	header := fmt.Sprintf("flow(%s:%s#%d): %s", canonicalExecutionCommitKind(node, decision), canonicalExecutionNodeSlug(node), visitNumber, canonicalExecutionCommitAction(decision, summary, taskRecord.Title))
	lines := []string{
		header,
		"",
		fmt.Sprintf("task_number: %d", taskRecord.TaskNumber),
		fmt.Sprintf("task_id: %s", taskRecord.ID),
		fmt.Sprintf("flow_node_id: %s", node.ID),
		fmt.Sprintf("node_type: %s", strings.TrimSpace(node.NodeType)),
		fmt.Sprintf("visit: %d", visitNumber),
	}
	if trimmed := strings.TrimSpace(decision); trimmed != "" {
		lines = append(lines, fmt.Sprintf("decision: %s", trimmed))
	}
	if trimmed := strings.TrimSpace(summary); trimmed != "" {
		lines = append(lines, fmt.Sprintf("summary: %s", trimmed))
	}
	return strings.Join(lines, "\n")
}

func canonicalExecutionCommitKind(node repo.FlowNode, decision string) string {
	if strings.EqualFold(strings.TrimSpace(node.NodeType), "review") {
		return "review"
	}
	return "work"
}

func canonicalExecutionCommitAction(decision, summary, fallback string) string {
	switch strings.TrimSpace(decision) {
	case "approve":
		return "approve"
	case "reject":
		if strings.TrimSpace(summary) != "" {
			return "reject - " + strings.TrimSpace(summary)
		}
		return "reject"
	default:
		if strings.TrimSpace(fallback) != "" {
			return strings.TrimSpace(fallback)
		}
		return "complete"
	}
}

func canonicalExecutionNodeSlug(node repo.FlowNode) string {
	source := strings.TrimSpace(node.DisplayName)
	if source == "" {
		source = strings.TrimSpace(node.NodeType)
	}
	source = strings.ToLower(source)
	var b strings.Builder
	lastDash := false
	for _, r := range source {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if lastDash {
			continue
		}
		b.WriteByte('-')
		lastDash = true
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "node"
	}
	return out
}

func taskBranchString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (e *NativeToolExecutor) recordReviewDecisionMetadata(ctx context.Context, execution repo.FlowNodeExecution, decision, reason, findings string) (repo.FlowNodeExecution, error) {
	if e.flowExecs == nil || execution.ID == uuid.Nil {
		return execution, nil
	}
	now := time.Now().UTC()
	metadata := repo.FlowExecutionMetadataWithReviewDecision(execution.Metadata, &repo.FlowExecutionReviewDecision{
		Decision:  strings.TrimSpace(decision),
		Reason:    strings.TrimSpace(reason),
		Findings:  strings.TrimSpace(findings),
		DecidedAt: &now,
	})
	return e.flowExecs.UpdateMetadata(ctx, execution.ID, metadata)
}

func (e *NativeToolExecutor) recordCommitCloseFailure(ctx context.Context, execution repo.FlowNodeExecution, commitErr error) error {
	if e.flowExecs == nil || execution.ID == uuid.Nil {
		return nil
	}
	now := time.Now().UTC()
	lastCommitSHA := ""
	if execution.CommitSHA != nil {
		lastCommitSHA = strings.TrimSpace(*execution.CommitSHA)
	}
	branchHeadSHA := ""
	if e.explicitRoot != "" {
		if head, err := flowcommit.HeadSHA(ctx, e.explicitRoot); err == nil {
			branchHeadSHA = strings.TrimSpace(head)
		}
	}
	targetPath := ""
	artifactRef := ""
	if execution.SessionID != nil && *execution.SessionID != uuid.Nil {
		targetPath = e.latestRecoveryTargetPathForSession(ctx, workspaceScope{sessionID: execution.SessionID})
		if targetPath != "" {
			artifactRef = filepath.ToSlash(filepath.Join(".ottercamp", "recovery", filepath.FromSlash(targetPath)))
		}
	}
	metadata := repo.FlowExecutionMetadataWithRecoveryCheckpoint(execution.Metadata, &repo.FlowExecutionRecoveryCheckpoint{
		CheckpointType: "awaiting_commit_close",
		LastCommitSHA:  lastCommitSHA,
		BranchHeadSHA:  branchHeadSHA,
		ResumeAction:   "close_execution",
		TargetPath:     targetPath,
		ArtifactRef:    artifactRef,
		FailureClass:   "product_runtime",
		FailureSummary: strings.TrimSpace(commitErr.Error()),
		UpdatedAt:      &now,
	})
	if _, err := e.flowExecs.UpdateMetadata(ctx, execution.ID, metadata); err != nil {
		return err
	}
	stalled := "stalled"
	_, err := e.flowExecs.UpdateRuntimeSubstate(ctx, execution.ID, &stalled)
	return err
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
	// Bootstrap and interactive staffing create assignable project agents as
	// draft records first. Promote them to active on assignment regardless of
	// role so later task assignment guards can rely on the project roster.
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

	// Auto-add project-assigned agents as participants for project/task sessions.
	var participants []map[string]any
	if (scopeType == "project" || scopeType == "project_task") && e.assignments != nil && e.participants != nil {
		participants = e.autoAddProjectParticipants(ctx, created.ID, scopeType, scopeID, mode)
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
// them as session participants. Ordering is mode-aware so the participant list
// matches the intended responder model for the created session.
func (e *NativeToolExecutor) autoAddProjectParticipants(ctx context.Context, sessionID uuid.UUID, scopeType string, scopeID uuid.UUID, mode string) []map[string]any {
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

	roleOrder := map[string]int{"worker": 0, "reviewer": 1, "project_manager": 2, "observer": 3}
	if strings.EqualFold(strings.TrimSpace(mode), "sync") {
		roleOrder = map[string]int{"project_manager": 0, "worker": 1, "reviewer": 2, "observer": 3}
	}
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
	existingParticipants, err := e.participants.ListBySession(ctx, sessionID)
	if err == nil {
		for _, participant := range existingParticipants {
			if participant.RemovedAt != nil {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(participant.ParticipantType), "agent") {
				continue
			}
			if participant.ParticipantID != agentID {
				continue
			}
			return map[string]any{
				"participant_id":  participant.ID,
				"already_present": true,
			}, nil
		}
	}
	created, err := e.participants.Create(ctx, repo.ChatParticipant{
		SessionID:              sessionID,
		ParticipantType:        "agent",
		ParticipantID:          agentID,
		NotificationPreference: "all",
		Role:                   "member",
	})
	if err != nil {
		if errors.Is(err, repo.ErrConflict) {
			existingParticipants, listErr := e.participants.ListBySession(ctx, sessionID)
			if listErr == nil {
				for _, participant := range existingParticipants {
					if participant.RemovedAt != nil {
						continue
					}
					if !strings.EqualFold(strings.TrimSpace(participant.ParticipantType), "agent") {
						continue
					}
					if participant.ParticipantID != agentID {
						continue
					}
					return map[string]any{
						"participant_id":  participant.ID,
						"already_present": true,
					}, nil
				}
			}
		}
		return nil, err
	}
	return map[string]any{
		"participant_id":  created.ID,
		"already_present": false,
	}, nil
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
	role = normalizeMessageSendRole(role)
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	if role == "user" && scope.agentID != nil && *scope.agentID != uuid.Nil &&
		scope.sessionID != nil && *scope.sessionID == sessionID && e.chatSessions != nil {
		if currentSession, sessionErr := e.chatSessions.GetByID(ctx, sessionID); sessionErr == nil &&
			strings.EqualFold(strings.TrimSpace(currentSession.ScopeType), "project") {
			return map[string]any{
				"error":   "same_session_project_handoff_disallowed",
				"message": "Do not use message.send to inject a user handoff back into the current project session. Treat the existing project session as the handoff channel and continue the bootstrap or execution workflow directly in this turn instead.",
			}, nil
		}
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

func normalizeMessageSendRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant", "tool_call", "tool_result", "system", "user":
		return strings.ToLower(strings.TrimSpace(role))
	default:
		return "user"
	}
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
