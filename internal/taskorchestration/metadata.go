package taskorchestration

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
)

const metadataKey = "parent_orchestration"

var ErrParentCompletionRequirements = errors.New("parent task requires child verification and passed integration before completion")

type ChildVerification struct {
	TaskID     string `json:"task_id"`
	Summary    string `json:"summary"`
	VerifiedAt string `json:"verified_at,omitempty"`
}

type IntegrationCheck struct {
	Status    string `json:"status"`
	Summary   string `json:"summary"`
	CheckedAt string `json:"checked_at,omitempty"`
}

type OutcomeAssessment struct {
	Satisfied  bool   `json:"satisfied"`
	Summary    string `json:"summary"`
	AssessedAt string `json:"assessed_at,omitempty"`
}

type ParentState struct {
	ChildVerifications []ChildVerification `json:"child_verifications,omitempty"`
	IntegrationCheck   *IntegrationCheck   `json:"integration_check,omitempty"`
	OutcomeAssessment  *OutcomeAssessment  `json:"outcome_assessment,omitempty"`
}

type Update struct {
	ChildVerifications []ChildVerification
	IntegrationCheck   *IntegrationCheck
	OutcomeAssessment  *OutcomeAssessment
}

type ChildStatus struct {
	TaskID    uuid.UUID
	Label     string
	Status    string
	Verified  bool
	Summary   string
	Terminal  bool
	Completed bool
}

type CompletionStatus struct {
	IsParent          bool
	Children          []ChildStatus
	IntegrationCheck  *IntegrationCheck
	OutcomeAssessment *OutcomeAssessment
	Missing           []string
}

type CompletionError struct {
	Reasons []string
}

func (e CompletionError) Error() string {
	if len(e.Reasons) == 0 {
		return ErrParentCompletionRequirements.Error()
	}
	return fmt.Sprintf("%s: %s", ErrParentCompletionRequirements.Error(), strings.Join(e.Reasons, "; "))
}

func (e CompletionError) Is(target error) bool {
	return target == ErrParentCompletionRequirements
}

func Parse(metadata json.RawMessage) (ParentState, bool) {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return ParentState{}, false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return ParentState{}, false
	}
	raw, ok := payload[metadataKey]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return ParentState{}, false
	}
	var state ParentState
	if err := json.Unmarshal(raw, &state); err != nil {
		return ParentState{}, false
	}
	state.ChildVerifications = compactVerifications(state.ChildVerifications)
	if state.IntegrationCheck != nil {
		normalized := normalizeIntegrationStatus(state.IntegrationCheck.Status)
		if normalized == "" {
			state.IntegrationCheck = nil
		} else {
			copyValue := *state.IntegrationCheck
			copyValue.Status = normalized
			copyValue.Summary = strings.TrimSpace(copyValue.Summary)
			copyValue.CheckedAt = strings.TrimSpace(copyValue.CheckedAt)
			state.IntegrationCheck = &copyValue
		}
	}
	if state.OutcomeAssessment != nil {
		copyValue := *state.OutcomeAssessment
		copyValue.Summary = strings.TrimSpace(copyValue.Summary)
		copyValue.AssessedAt = strings.TrimSpace(copyValue.AssessedAt)
		state.OutcomeAssessment = &copyValue
	}
	return state, len(state.ChildVerifications) > 0 || state.IntegrationCheck != nil || state.OutcomeAssessment != nil
}

func Apply(existing json.RawMessage, update Update) (json.RawMessage, error) {
	payload := map[string]any{}
	if len(existing) > 0 && json.Valid(existing) {
		if err := json.Unmarshal(existing, &payload); err != nil {
			return nil, err
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}

	current, _ := Parse(existing)
	if len(update.ChildVerifications) > 0 {
		current.ChildVerifications = mergeVerifications(current.ChildVerifications, update.ChildVerifications)
	}
	if update.IntegrationCheck != nil {
		normalized := normalizeIntegrationStatus(update.IntegrationCheck.Status)
		if normalized == "" {
			return nil, fmt.Errorf("integration_check.status must be passed or failed")
		}
		copyValue := *update.IntegrationCheck
		copyValue.Status = normalized
		copyValue.Summary = strings.TrimSpace(copyValue.Summary)
		copyValue.CheckedAt = strings.TrimSpace(copyValue.CheckedAt)
		current.IntegrationCheck = &copyValue
	}
	if update.OutcomeAssessment != nil {
		copyValue := *update.OutcomeAssessment
		copyValue.Summary = strings.TrimSpace(copyValue.Summary)
		copyValue.AssessedAt = strings.TrimSpace(copyValue.AssessedAt)
		current.OutcomeAssessment = &copyValue
	}

	payload[metadataKey] = current
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

func Evaluate(parentTask repo.ProjectTask, childTasks []repo.ProjectTask) CompletionStatus {
	childIDs := taskdecomp.ParseChildTaskIDs(parentTask.Metadata)
	isParent := len(childIDs) > 0 || len(childTasks) > 0
	if !isParent {
		return CompletionStatus{}
	}

	state, _ := Parse(parentTask.Metadata)
	verified := map[uuid.UUID]ChildVerification{}
	for _, item := range state.ChildVerifications {
		taskID, err := uuid.Parse(strings.TrimSpace(item.TaskID))
		if err != nil || taskID == uuid.Nil {
			continue
		}
		verified[taskID] = item
	}

	result := CompletionStatus{
		IsParent:          true,
		Children:          make([]ChildStatus, 0, len(childTasks)),
		IntegrationCheck:  state.IntegrationCheck,
		OutcomeAssessment: state.OutcomeAssessment,
		Missing:           make([]string, 0),
	}

	var (
		hasExecutableChild        bool
		hasCompletedChild         bool
		hasCompletedCloseoutChild bool
		hasRecordedVerification   bool
		missingLabels             []string
	)
	for _, child := range childTasks {
		if childTaskClosesParent(parentTask, child) {
			hasCompletedCloseoutChild = true
			break
		}
	}
	for _, child := range childTasks {
		status := normalizeStatus(child.WorkStatus)
		terminal := status == "done" || status == "cancelled"
		if !terminal && blockedMalformedChildDoesNotBlockParentCompletion(parentTask, child) {
			terminal = true
		}
		if !terminal && status == "blocked" && state.OutcomeAssessment != nil && state.OutcomeAssessment.Satisfied {
			terminal = true
		}
		if !terminal && hasCompletedCloseoutChild && status == "blocked" {
			terminal = true
		}
		completed := status == "done"
		recorded, ok := verified[child.ID]
		if ok {
			hasRecordedVerification = true
		}
		result.Children = append(result.Children, ChildStatus{
			TaskID:    child.ID,
			Label:     taskLabel(child),
			Status:    status,
			Verified:  ok,
			Summary:   strings.TrimSpace(recorded.Summary),
			Terminal:  terminal,
			Completed: completed,
		})
		if !terminal {
			hasExecutableChild = true
		}
		if completed {
			hasCompletedChild = true
			if !ok {
				missingLabels = append(missingLabels, taskLabel(child))
			}
		}
	}

	if hasExecutableChild {
		result.Missing = append(result.Missing, "all child tasks must complete before the parent can finish integration")
	}
	if !hasCompletedChild && !allowsVerifiedCloseoutWithoutCompletedChild(state, len(childTasks), hasExecutableChild, hasRecordedVerification) {
		result.Missing = append(result.Missing, "no completed child tasks are available for parent verification")
	}
	if len(missingLabels) > 0 {
		result.Missing = append(result.Missing, "verify child outputs for "+strings.Join(missingLabels, ", "))
	}
	switch {
	case state.IntegrationCheck == nil:
		result.Missing = append(result.Missing, "record a passed integration or end-to-end check")
	case normalizeIntegrationStatus(state.IntegrationCheck.Status) != "passed":
		result.Missing = append(result.Missing, "integration or end-to-end verification has not passed; reopen a child task with feedback or create a new bounded child task")
	}
	switch {
	case state.OutcomeAssessment == nil:
		result.Missing = append(result.Missing, "record that the stated parent outcome is satisfied")
	case !state.OutcomeAssessment.Satisfied:
		result.Missing = append(result.Missing, "the stated parent outcome is not yet satisfied")
	}

	return result
}

func ValidateCompletion(parentTask repo.ProjectTask, childTasks []repo.ProjectTask) error {
	status := Evaluate(parentTask, childTasks)
	if !status.IsParent || len(status.Missing) == 0 {
		return nil
	}
	return CompletionError{Reasons: status.Missing}
}

func NewChildVerification(taskID uuid.UUID, summary string, now time.Time) ChildVerification {
	return ChildVerification{
		TaskID:     taskID.String(),
		Summary:    strings.TrimSpace(summary),
		VerifiedAt: now.UTC().Format(time.RFC3339Nano),
	}
}

func NewIntegrationCheck(status, summary string, now time.Time) *IntegrationCheck {
	normalized := normalizeIntegrationStatus(status)
	if normalized == "" {
		return nil
	}
	return &IntegrationCheck{
		Status:    normalized,
		Summary:   strings.TrimSpace(summary),
		CheckedAt: now.UTC().Format(time.RFC3339Nano),
	}
}

func NewOutcomeAssessment(satisfied bool, summary string, now time.Time) *OutcomeAssessment {
	return &OutcomeAssessment{
		Satisfied:  satisfied,
		Summary:    strings.TrimSpace(summary),
		AssessedAt: now.UTC().Format(time.RFC3339Nano),
	}
}

func mergeVerifications(existing, updates []ChildVerification) []ChildVerification {
	merged := compactVerifications(existing)
	byID := make(map[string]int, len(merged))
	for idx, item := range merged {
		byID[strings.TrimSpace(item.TaskID)] = idx
	}
	for _, item := range compactVerifications(updates) {
		key := strings.TrimSpace(item.TaskID)
		if idx, ok := byID[key]; ok {
			merged[idx] = item
			continue
		}
		byID[key] = len(merged)
		merged = append(merged, item)
	}
	return merged
}

func compactVerifications(items []ChildVerification) []ChildVerification {
	if len(items) == 0 {
		return nil
	}
	out := make([]ChildVerification, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		taskID := strings.TrimSpace(item.TaskID)
		summary := strings.TrimSpace(item.Summary)
		if taskID == "" || summary == "" {
			continue
		}
		if _, err := uuid.Parse(taskID); err != nil {
			continue
		}
		if _, ok := seen[taskID]; ok {
			continue
		}
		seen[taskID] = struct{}{}
		item.TaskID = taskID
		item.Summary = summary
		item.VerifiedAt = strings.TrimSpace(item.VerifiedAt)
		out = append(out, item)
	}
	return out
}

func normalizeIntegrationStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "passed", "pass":
		return "passed"
	case "failed", "fail":
		return "failed"
	default:
		return ""
	}
}

func normalizeStatus(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func taskLabel(task repo.ProjectTask) string {
	title := strings.TrimSpace(task.Title)
	switch {
	case task.TaskNumber > 0 && title != "":
		return fmt.Sprintf("OC-%d (%s)", task.TaskNumber, title)
	case task.TaskNumber > 0:
		return fmt.Sprintf("OC-%d", task.TaskNumber)
	case title != "":
		return title
	default:
		return task.ID.String()
	}
}

func blockedMalformedChildDoesNotBlockParentCompletion(parentTask, childTask repo.ProjectTask) bool {
	if normalizeStatus(childTask.WorkStatus) != "blocked" {
		return false
	}
	if taskdecomp.TaskLooksProceduralInstructionArtifact(strings.TrimSpace(childTask.Title), childTask.Description) {
		return true
	}
	return parentSourceDescriptionForbidsDecomposition(parentTask.Metadata)
}

func childTaskClosesParent(parentTask, childTask repo.ProjectTask) bool {
	if normalizeStatus(childTask.WorkStatus) != "done" {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(childTask.Title))
	if childTask.Description != nil {
		if description := strings.ToLower(strings.TrimSpace(*childTask.Description)); description != "" {
			text += "\n" + description
		}
	}
	if !containsAnyCloseoutSignal(text) {
		return false
	}
	return strings.Contains(text, fmt.Sprintf("oc-%d", parentTask.TaskNumber)) ||
		strings.Contains(text, fmt.Sprintf("parent %d", parentTask.TaskNumber)) ||
		strings.Contains(text, "confirms the parent") ||
		strings.Contains(text, "parent can close") ||
		strings.Contains(text, "parent can be closed") ||
		strings.Contains(text, "parent can be closed out")
}

func containsAnyCloseoutSignal(text string) bool {
	for _, needle := range []string{
		"close parent",
		"close out parent",
		"close out oc-",
		"parent can close",
		"parent can be closed",
		"parent can be closed out",
		"mark parent",
		"parent complete",
		"complete the parent",
		"confirms the parent",
		"mark done immediately",
		"delivered",
	} {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func allowsVerifiedCloseoutWithoutCompletedChild(state ParentState, childCount int, hasExecutableChild, hasRecordedVerification bool) bool {
	if childCount == 0 || hasExecutableChild || !hasRecordedVerification {
		return false
	}
	if state.OutcomeAssessment == nil || !state.OutcomeAssessment.Satisfied {
		return false
	}
	return state.IntegrationCheck != nil && normalizeIntegrationStatus(state.IntegrationCheck.Status) == "passed"
}

func parentSourceDescriptionForbidsDecomposition(metadata json.RawMessage) bool {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return false
	}
	raw, ok := payload["decomposition"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var decomp map[string]any
	if err := json.Unmarshal(raw, &decomp); err != nil {
		return false
	}
	sourceDescription, _ := decomp["source_description"].(string)
	return taskdecomp.DescriptionForbidsDecomposition(strings.TrimSpace(sourceDescription))
}
