package taskdecomp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	metadataKeyDecomposition               = "decomposition"
	descriptionThresholdChars              = 320
	maxGeneratedChildWorkUnits             = 5
	QueueDecompositionModeParallelChildren = "parallel_children"
	defaultMaxTaskMinutes                  = 30
	extendedMaxTaskMinutes                 = 60
)

var ErrBoundedTaskTooLarge = errors.New("task exceeds bounded size policy and must be split before queueing")

var (
	toolHeavySignals = []string{
		"api",
		"cli",
		"command",
		"database",
		"deploy",
		"integration",
		"migration",
		"script",
		"terminal",
		"webhook",
	}
	externalBoundSignals = []string{
		"approval",
		"customer",
		"dependency",
		"external",
		"partner",
		"stakeholder",
		"vendor",
		"wait for",
	}
	broadScopeSignals = []string{
		"distribution",
		"go-to-market",
		"gtm",
		"ideation",
		"messaging",
		"persona",
		"personas",
		"pillar",
		"pillars",
		"positioning",
		"strategy",
	}
)

type QueueSizeError struct {
	EstimatedMinutes int
	MaxMinutes       int
	Reason           string
}

func (e QueueSizeError) Error() string {
	reason := strings.TrimSpace(e.Reason)
	if reason == "" {
		reason = "split the work into smaller reviewable tasks before queueing"
	}
	return fmt.Sprintf("task exceeds bounded size policy (estimated %d minutes > %d minute limit): %s", e.EstimatedMinutes, e.MaxMinutes, reason)
}

func (e QueueSizeError) Is(target error) bool {
	return target == ErrBoundedTaskTooLarge
}

type Plan struct {
	RequiresDecomposition bool
	PrimaryDeliverable    string
	ChildDeliverables     []string
	Deliverables          []string
}

type QueueDecompositionInput struct {
	ParentTaskID uuid.UUID
	Title        string
	Description  *string
	Metadata     json.RawMessage
}

type ChildDraft struct {
	Title       string
	Description *string
	Metadata    json.RawMessage
}

type QueueDecomposition struct {
	Applied           bool
	Plan              Plan
	SourceDescription string
	ChildDrafts       []ChildDraft
}

func Analyze(title string, description *string) Plan {
	rawDescription := strings.TrimSpace(deref(description))
	if rawDescription == "" {
		return Plan{}
	}

	deliverables := extractDeliverables(rawDescription)
	if len(deliverables) < 2 {
		return Plan{}
	}

	requires := len(deliverables) >= 2 || len(rawDescription) >= descriptionThresholdChars
	if !requires {
		return Plan{}
	}

	primary := deliverables[0]
	children := deliverables[1:]
	if len(children) > maxGeneratedChildWorkUnits {
		children = children[:maxGeneratedChildWorkUnits]
	}
	all := make([]string, 0, 1+len(children))
	all = append(all, primary)
	all = append(all, children...)

	return Plan{
		RequiresDecomposition: true,
		PrimaryDeliverable:    primary,
		ChildDeliverables:     children,
		Deliverables:          all,
	}
}

func PrepareQueueDecomposition(input QueueDecompositionInput) (QueueDecomposition, error) {
	if strings.TrimSpace(ParsePrimaryDeliverable(input.Metadata)) != "" {
		return QueueDecomposition{}, nil
	}
	plan := Analyze(input.Title, input.Description)
	if !plan.RequiresDecomposition {
		if sizingErr := validateBoundedTaskSize(input.Title, input.Description); sizingErr != nil {
			return QueueDecomposition{}, sizingErr
		}
		return QueueDecomposition{}, nil
	}

	sourceDescription := strings.TrimSpace(deref(input.Description))
	childDrafts := make([]ChildDraft, 0, len(plan.ChildDeliverables))
	for idx, deliverable := range plan.ChildDeliverables {
		childTitle := strings.TrimSpace(input.Title)
		if childTitle == "" {
			childTitle = input.ParentTaskID.String()
		}
		childTitle = fmt.Sprintf("%s (Workstream %d)", childTitle, idx+2)

		childMetadataRaw, err := json.Marshal(map[string]any{
			"decomposition_parent_task_id": input.ParentTaskID.String(),
			"workstream_index":             idx + 2,
		})
		if err != nil {
			return QueueDecomposition{}, err
		}
		childDrafts = append(childDrafts, ChildDraft{
			Title:       childTitle,
			Description: strPtr(strings.TrimSpace(deliverable)),
			Metadata:    normalizeJSON(childMetadataRaw),
		})
	}

	return QueueDecomposition{
		Applied:           true,
		Plan:              plan,
		SourceDescription: sourceDescription,
		ChildDrafts:       childDrafts,
	}, nil
}

func ParsePrimaryDeliverable(metadata json.RawMessage) string {
	decomp := decompositionObject(metadata)
	if decomp == nil {
		return ""
	}
	primary, _ := decomp["primary_deliverable"].(string)
	return strings.TrimSpace(primary)
}

func ParseChildTaskIDs(metadata json.RawMessage) []uuid.UUID {
	decomp := decompositionObject(metadata)
	if decomp == nil {
		return nil
	}
	rawChildIDs, ok := decomp["child_task_ids"].([]any)
	if !ok {
		return nil
	}

	childIDs := make([]uuid.UUID, 0, len(rawChildIDs))
	for _, rawChildID := range rawChildIDs {
		value, ok := rawChildID.(string)
		if !ok {
			continue
		}
		childID, err := uuid.Parse(strings.TrimSpace(value))
		if err != nil || childID == uuid.Nil {
			continue
		}
		childIDs = append(childIDs, childID)
	}
	return childIDs
}

func ParseParentTaskID(metadata json.RawMessage) uuid.UUID {
	payload := metadataObject(metadata)
	if payload == nil {
		return uuid.Nil
	}
	rawParentID, _ := payload["decomposition_parent_task_id"].(string)
	parentID, err := uuid.Parse(strings.TrimSpace(rawParentID))
	if err != nil {
		return uuid.Nil
	}
	return parentID
}

func ParseWorkstreamIndex(metadata json.RawMessage) (int, bool) {
	payload := metadataObject(metadata)
	if payload == nil {
		return 0, false
	}
	return metadataIntValue(payload["workstream_index"])
}

func ApplyMetadata(existing json.RawMessage, plan Plan, sourceDescription string, childTaskIDs []uuid.UUID) json.RawMessage {
	payload := map[string]any{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}

	childIDs := make([]string, 0, len(childTaskIDs))
	for _, childID := range childTaskIDs {
		childIDs = append(childIDs, childID.String())
	}

	mode := ParseQueueDecompositionMode(existing)
	if mode == "" {
		mode = QueueDecompositionModeParallelChildren
	}

	payload[metadataKeyDecomposition] = map[string]any{
		"applied":             true,
		"mode":                mode,
		"orchestration_only":  true,
		"primary_deliverable": strings.TrimSpace(plan.PrimaryDeliverable),
		"deliverables":        append([]string(nil), plan.Deliverables...),
		"source_description":  strings.TrimSpace(sourceDescription),
		"child_task_ids":      childIDs,
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizeJSON(existing)
	}
	return normalizeJSON(encoded)
}

func ApplyChildMetadata(existing json.RawMessage, parentTaskID uuid.UUID, workstreamIndex int) json.RawMessage {
	payload := metadataObject(existing)
	if payload == nil {
		payload = map[string]any{}
	}
	if parentTaskID != uuid.Nil {
		payload["decomposition_parent_task_id"] = parentTaskID.String()
	}
	if workstreamIndex > 0 {
		payload["workstream_index"] = workstreamIndex
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizeJSON(existing)
	}
	return normalizeJSON(encoded)
}

func AppendChildTaskID(existing json.RawMessage, childTaskID uuid.UUID) json.RawMessage {
	if childTaskID == uuid.Nil {
		return normalizeJSON(existing)
	}

	payload := metadataObject(existing)
	if payload == nil {
		payload = map[string]any{}
	}

	decomp, _ := payload[metadataKeyDecomposition].(map[string]any)
	if decomp == nil {
		decomp = map[string]any{}
	}
	decomp["applied"] = true
	decomp["orchestration_only"] = true
	if normalized := normalizeQueueDecompositionMode(fmt.Sprintf("%v", decomp["mode"])); normalized != "" {
		decomp["mode"] = normalized
	} else {
		decomp["mode"] = QueueDecompositionModeParallelChildren
	}

	childIDs := make([]string, 0, 1)
	seen := map[string]struct{}{}
	for _, existingChildID := range ParseChildTaskIDs(existing) {
		value := existingChildID.String()
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		childIDs = append(childIDs, value)
	}
	value := childTaskID.String()
	if _, ok := seen[value]; !ok {
		childIDs = append(childIDs, value)
	}
	decomp["child_task_ids"] = childIDs
	payload[metadataKeyDecomposition] = decomp

	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizeJSON(existing)
	}
	return normalizeJSON(encoded)
}

func ParseQueueDecompositionMode(metadata json.RawMessage) string {
	decomp := decompositionObject(metadata)
	if decomp == nil {
		return ""
	}
	mode, _ := decomp["mode"].(string)
	return normalizeQueueDecompositionMode(mode)
}

func QueueDecompositionRequested(metadata json.RawMessage) bool {
	return ParseQueueDecompositionMode(metadata) == QueueDecompositionModeParallelChildren
}

func ApplyQueueDecompositionMode(existing json.RawMessage, mode string) json.RawMessage {
	payload := map[string]any{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}

	decomp, _ := payload[metadataKeyDecomposition].(map[string]any)
	if decomp == nil {
		decomp = map[string]any{}
	}

	if normalized := normalizeQueueDecompositionMode(mode); normalized != "" {
		decomp["mode"] = normalized
		payload[metadataKeyDecomposition] = decomp
	} else {
		delete(decomp, "mode")
		if len(decomp) == 0 {
			delete(payload, metadataKeyDecomposition)
		} else {
			payload[metadataKeyDecomposition] = decomp
		}
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizeJSON(existing)
	}
	return normalizeJSON(encoded)
}

func extractDeliverables(description string) []string {
	candidates := make([]string, 0)

	// Prefer explicit list-like authoring first.
	for _, line := range strings.Split(description, "\n") {
		item := cleanSegment(line)
		if item != "" {
			candidates = append(candidates, item)
		}
	}
	if len(candidates) < 2 {
		if semicolonItems := splitSegments(description, ";"); len(semicolonItems) >= 2 {
			candidates = append(candidates, semicolonItems...)
		}
	}
	if len(candidates) < 2 {
		if sentenceItems := splitSegments(description, ". "); len(sentenceItems) >= 2 {
			candidates = append(candidates, sentenceItems...)
		}
	}

	seen := map[string]struct{}{}
	deduped := make([]string, 0, len(candidates))
	for _, item := range candidates {
		normalized := strings.ToLower(strings.TrimSpace(item))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		deduped = append(deduped, strings.TrimSpace(item))
	}
	return deduped
}

func validateBoundedTaskSize(title string, description *string) error {
	estimatedMinutes, maxMinutes := estimateTaskMinutes(title, description)
	if estimatedMinutes <= maxMinutes {
		return nil
	}
	return QueueSizeError{
		EstimatedMinutes: estimatedMinutes,
		MaxMinutes:       maxMinutes,
		Reason:           "split the work into smaller reviewable tasks before queueing",
	}
}

func estimateTaskMinutes(title string, description *string) (int, int) {
	rawDescription := strings.TrimSpace(deref(description))
	text := strings.ToLower(strings.TrimSpace(strings.Join([]string{title, rawDescription}, " ")))

	maxMinutes := defaultMaxTaskMinutes
	if containsAny(text, toolHeavySignals) || containsAny(text, externalBoundSignals) {
		maxMinutes = extendedMaxTaskMinutes
	}

	estimatedMinutes := 20
	deliverables := extractDeliverables(rawDescription)
	if extraDeliverables := len(deliverables) - 1; extraDeliverables > 0 {
		estimatedMinutes += extraDeliverables * 15
	}
	if len(rawDescription) >= 220 {
		estimatedMinutes += 10
	}
	if len(rawDescription) >= descriptionThresholdChars {
		estimatedMinutes += 10
	}
	if len(rawDescription) >= 520 {
		estimatedMinutes += 10
	}
	if strings.Count(text, " and ") >= 2 || strings.Count(text, " plus ") >= 2 {
		estimatedMinutes += 10
	}
	if containsAny(text, broadScopeSignals) {
		estimatedMinutes += 15
	}

	return estimatedMinutes, maxMinutes
}

func containsAny(text string, signals []string) bool {
	for _, signal := range signals {
		if strings.Contains(text, signal) {
			return true
		}
	}
	return false
}

func splitSegments(raw, delimiter string) []string {
	parts := strings.Split(raw, delimiter)
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := cleanSegment(part); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func metadataObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	return payload
}

func decompositionObject(metadata json.RawMessage) map[string]any {
	payload := metadataObject(metadata)
	if payload == nil {
		return nil
	}
	raw, ok := payload[metadataKeyDecomposition]
	if !ok {
		return nil
	}
	decomp, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return decomp
}

func metadataIntValue(raw any) (int, bool) {
	switch typed := raw.(type) {
	case int:
		if typed > 0 {
			return typed, true
		}
	case int32:
		if typed > 0 {
			return int(typed), true
		}
	case int64:
		if typed > 0 {
			return int(typed), true
		}
	case float64:
		integer := int(typed)
		if typed == float64(integer) && integer > 0 {
			return integer, true
		}
	}
	return 0, false
}

func cleanSegment(raw string) string {
	item := strings.TrimSpace(raw)
	item = strings.TrimPrefix(item, "-")
	item = strings.TrimPrefix(item, "*")
	item = strings.TrimSpace(item)
	for len(item) > 2 && item[0] >= '0' && item[0] <= '9' {
		item = strings.TrimSpace(item[1:])
		item = strings.TrimPrefix(item, ".")
		item = strings.TrimPrefix(item, ")")
		item = strings.TrimSpace(item)
	}
	if len(item) < 10 {
		return ""
	}
	return strings.TrimSpace(item)
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func strPtr(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func normalizeQueueDecompositionMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case QueueDecompositionModeParallelChildren:
		return QueueDecompositionModeParallelChildren
	default:
		return ""
	}
}

func normalizeJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`{}`)
	}
	return value
}
