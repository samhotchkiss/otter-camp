package taskdecomp

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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
	enumeratedBatchTitlePattern  = regexp.MustCompile(`\b(?:generate|create|draft|write|produce|compile|research|collect|design|build)\s+\d+\b`)
	enumeratedActionCountPattern = regexp.MustCompile(`(?i)^(generate|create|draft|write|produce|compile|research|collect|design|build)\s+(\d+)\s+(.+)$`)
	actionVerbPattern            = regexp.MustCompile(`^(?:generate|create|draft|write|produce|compile|research|collect|design|build)\b`)
	leadingTaskActionPattern     = regexp.MustCompile(`(?i)^(?:use|visit|navigate|discover|identify|build|rebuild|create|design|define|draft|write|produce|compile|research|collect|implement|migrate|import|validate|review|compare|synthesize|map|prepare|develop|generate|outline|audit|document|wire|configure|run|test|scrape|store|rewrite|establish|include)\b`)
	labelledTaskPattern          = regexp.MustCompile(`(?i)^(?:ws\d+(?:\.\d+[a-z]?)?|template\s+\d+|option\s+\d+|phase\s+\d+|wave\s+\d+|task\s+\d+)[:\-]`)
	timingOnlyPattern            = regexp.MustCompile(`(?i)^~?\s*\d+\s*(?:-|to\s+)?\d*\s*(?:min|mins|minute|minutes|hr|hrs|hour|hours)\b(?:[[:punct:]\s].*)?$`)
	enumMarkerPattern            = regexp.MustCompile(`\(\d+\)`)
	toolHeavySignals             = []string{
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
		"narrative",
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
	trimmedTitle := strings.TrimSpace(title)
	rawDescription := strings.TrimSpace(deref(description))

	titleDriven := titleSuggestsCompoundBoundedWork(trimmedTitle)
	deliverables := extractDeliverables(rawDescription)
	if titleDriven {
		titleDeliverables := inferTitleDeliverables(trimmedTitle)
		if len(titleDeliverables) > 0 {
			deliverables = append(titleDeliverables, deliverables...)
		}
	} else if len(deliverables) < 2 {
		deliverables = append(deliverables, inferTitleDeliverables(trimmedTitle)...)
	}
	if len(deliverables) < 2 {
		if !titleDriven {
			return Plan{}
		}
		deliverables = []string{trimmedTitle, trimmedTitle}
	}

	requires := len(deliverables) >= 2 || len(rawDescription) >= descriptionThresholdChars || titleDriven
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

func titleSuggestsCompoundBoundedWork(title string) bool {
	normalized := strings.ToLower(strings.TrimSpace(title))
	if normalized == "" {
		return false
	}
	if enumeratedBatchTitlePattern.MatchString(normalized) && strings.Contains(normalized, " and ") {
		if _, ok := splitCompoundActionTitle(title); ok {
			return true
		}
	}
	if enumeratedBatchTitlePattern.MatchString(normalized) && containsAny(normalized, broadScopeSignals) {
		return true
	}
	if strings.Count(normalized, " and ") >= 2 || strings.Count(normalized, " across ") >= 1 {
		if containsAny(normalized, broadScopeSignals) {
			return true
		}
	}
	if strings.Contains(normalized, " and ") {
		if _, ok := splitCompoundActionTitle(title); ok {
			return true
		}
	}
	return false
}

func inferTitleDeliverables(title string) []string {
	if !titleSuggestsCompoundBoundedWork(title) {
		if deliverables := inferLabelledColonDeliverables(title); len(deliverables) >= 2 {
			return deliverables
		}
		return nil
	}
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return nil
	}
	if deliverables := inferEnumeratedBatchDeliverables(trimmed); len(deliverables) >= 2 {
		return deliverables
	}
	if deliverables, ok := splitCompoundActionTitle(trimmed); ok {
		return deliverables
	}
	return []string{trimmed, trimmed}
}

func inferLabelledColonDeliverables(title string) []string {
	before, after, ok := strings.Cut(strings.TrimSpace(title), ":")
	if !ok {
		return nil
	}
	fields := strings.Fields(strings.TrimSpace(before))
	if len(fields) < 2 {
		return nil
	}
	verb := strings.TrimSpace(fields[0])
	if !leadingTaskActionPattern.MatchString(strings.ToLower(verb)) {
		return nil
	}
	scope := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(before), verb))
	if scope == "" {
		return nil
	}
	labels := splitEnumeratedLabels(after)
	if len(labels) < 2 {
		return nil
	}
	scope = singularizeFinalWord(scope)
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" {
			return nil
		}
		out = append(out, fmt.Sprintf("%s %s: %s", strings.Title(strings.ToLower(verb)), scope, label))
	}
	return out
}

func inferEnumeratedBatchDeliverables(title string) []string {
	matches := enumeratedActionCountPattern.FindStringSubmatch(strings.TrimSpace(title))
	if len(matches) != 4 {
		return nil
	}
	totalCount := atoiSafe(matches[2])
	if totalCount < 2 {
		return nil
	}
	verb := strings.Title(strings.TrimSpace(matches[1]))
	object := trimEnumeratedScope(matches[3])
	if object == "" {
		object = strings.TrimSpace(matches[3])
	}
	if object == "" {
		return nil
	}
	if totalCount <= maxGeneratedChildWorkUnits {
		if labelled := inferLabelledEnumeratedDeliverables(verb, strings.TrimSpace(matches[3]), totalCount); len(labelled) == totalCount {
			return labelled
		}
	}
	firstEnd := totalCount / 2
	if firstEnd < 1 {
		firstEnd = 1
	}
	if firstEnd >= totalCount {
		firstEnd = totalCount - 1
	}
	secondStart := firstEnd + 1
	return []string{
		fmt.Sprintf("%s %s %d-%d", verb, object, 1, firstEnd),
		fmt.Sprintf("%s %s %d-%d", verb, object, secondStart, totalCount),
	}
}

func inferLabelledEnumeratedDeliverables(verb string, raw string, totalCount int) []string {
	before, after, ok := strings.Cut(strings.TrimSpace(raw), ":")
	if !ok {
		return nil
	}
	labels := splitEnumeratedLabels(after)
	if len(labels) != totalCount {
		return nil
	}

	scope := strings.TrimSpace(before)
	if scope == "" {
		return nil
	}
	scope = singularizeFinalWord(scope)
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		if label == "" {
			return nil
		}
		out = append(out, fmt.Sprintf("%s %s: %s", verb, scope, label))
	}
	return out
}

func splitEnumeratedLabels(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parts := strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == ',' || r == ';'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		label := strings.TrimSpace(part)
		if label == "" {
			continue
		}
		subparts := strings.Split(label, " & ")
		for _, subpart := range subparts {
			candidate := strings.TrimSpace(subpart)
			if candidate != "" {
				out = append(out, candidate)
			}
		}
	}
	return out
}

func singularizeFinalWord(scope string) string {
	trimmed := strings.TrimSpace(scope)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if idx := strings.Index(lower, " for "); idx >= 0 {
		base := singularizeFinalWord(trimmed[:idx])
		suffix := strings.TrimSpace(trimmed[idx:])
		if base == "" {
			return trimmed
		}
		return strings.TrimSpace(base + " " + suffix)
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	last := fields[len(fields)-1]
	switch {
	case strings.HasSuffix(strings.ToLower(last), "ies") && len(last) > 3:
		fields[len(fields)-1] = last[:len(last)-3] + "y"
	case strings.HasSuffix(strings.ToLower(last), "ses") && len(last) > 3:
		fields[len(fields)-1] = last[:len(last)-2]
	case strings.HasSuffix(strings.ToLower(last), "s") && len(last) > 1:
		fields[len(fields)-1] = last[:len(last)-1]
	}
	return strings.Join(fields, " ")
}

func trimEnumeratedScope(raw string) string {
	trimmed := strings.TrimSpace(raw)
	lower := strings.ToLower(trimmed)
	for _, marker := range []string{" across ", " for ", " covering "} {
		if idx := strings.Index(lower, marker); idx >= 0 {
			return strings.TrimSpace(trimmed[:idx])
		}
	}
	return trimmed
}

func splitCompoundActionTitle(title string) ([]string, bool) {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return nil, false
	}
	lower := strings.ToLower(trimmed)
	idx := strings.Index(lower, " and ")
	if idx < 0 {
		return nil, false
	}
	left := strings.TrimSpace(trimmed[:idx])
	right := strings.TrimSpace(trimmed[idx+5:])
	if left == "" || right == "" {
		return nil, false
	}
	if !actionVerbPattern.MatchString(strings.ToLower(left)) || !actionVerbPattern.MatchString(strings.ToLower(right)) {
		return nil, false
	}
	return []string{left, right}, true
}

func PrepareQueueDecomposition(input QueueDecompositionInput) (QueueDecomposition, error) {
	if strings.TrimSpace(ParsePrimaryDeliverable(input.Metadata)) != "" {
		return QueueDecomposition{}, nil
	}
	plan := Analyze(input.Title, input.Description)
	if !plan.RequiresDecomposition {
		if sizingErr := validateBoundedTaskSize(input.Title, input.Description, input.ParentTaskID != uuid.Nil); sizingErr != nil {
			return QueueDecomposition{}, sizingErr
		}
		return QueueDecomposition{}, nil
	}

	sourceDescription := strings.TrimSpace(deref(input.Description))
	childDrafts := make([]ChildDraft, 0, len(plan.Deliverables))
	for idx, deliverable := range plan.Deliverables {
		childTitle := strings.TrimSpace(deliverable)
		if childTitle == "" {
			childTitle = strings.TrimSpace(input.Title)
		}
		if childTitle == "" {
			childTitle = input.ParentTaskID.String()
		}

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
	for _, childDraft := range childDrafts {
		if sizingErr := validateBoundedTaskSize(childDraft.Title, childDraft.Description, true); sizingErr != nil {
			return QueueDecomposition{}, sizingErr
		}
		if childPlan := Analyze(childDraft.Title, childDraft.Description); childPlan.RequiresDecomposition {
			return QueueDecomposition{}, QueueSizeError{
				EstimatedMinutes: defaultMaxTaskMinutes + 15,
				MaxMinutes:       defaultMaxTaskMinutes,
				Reason:           fmt.Sprintf("generated child task %q still requires decomposition into narrower executable work", strings.TrimSpace(childDraft.Title)),
			}
		}
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

func ApplyOrchestrationOnlyMetadata(existing json.RawMessage) json.RawMessage {
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
	payload[metadataKeyDecomposition] = decomp

	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizeJSON(existing)
	}
	return normalizeJSON(encoded)
}

func extractDeliverables(description string) []string {
	description = strings.ReplaceAll(description, "\\n", "\n")
	candidates := make([]string, 0)

	// Prefer explicit list-like authoring first.
	lines := strings.Split(description, "\n")
	if len(lines) > 1 {
		for _, line := range lines {
			item := cleanSegment(line)
			if item != "" {
				candidates = append(candidates, item)
			}
		}
	}
	if len(lines) <= 1 && len(candidates) < 2 {
		if semicolonItems := splitSegments(description, ";"); len(semicolonItems) >= 2 {
			candidates = append(candidates, semicolonItems...)
		}
	}
	if len(lines) <= 1 && len(candidates) < 2 {
		if sentenceItems := splitSegments(description, ". "); len(sentenceItems) >= 2 {
			candidates = append(candidates, sentenceItems...)
		}
	}

	seen := map[string]struct{}{}
	deduped := make([]string, 0, len(candidates))
	for _, item := range candidates {
		for _, expanded := range expandCompoundDeliverable(item) {
			normalized := strings.ToLower(strings.TrimSpace(expanded))
			if normalized == "" {
				continue
			}
			if isInstructionOnlyDeliverable(normalized) {
				continue
			}
			if !isExecutableDeliverable(strings.TrimSpace(expanded), normalized) {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			deduped = append(deduped, strings.TrimSpace(expanded))
		}
	}
	return deduped
}

func isInstructionOnlyDeliverable(normalized string) bool {
	for _, prefix := range []string{
		"this is ",
		"this task ",
		"these are ",
		"deferred task",
		"each is ",
		"each should ",
		"commit to repo",
		"commit in ",
		"must include ",
		"save as ",
		"save each as ",
		"embedded css",
		"visual-first ",
	} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	if (strings.HasPrefix(normalized, "include ") || strings.HasPrefix(normalized, "include:")) &&
		(strings.Contains(normalized, ",") || strings.Contains(normalized, ":") || strings.Contains(normalized, " and ")) {
		return true
	}
	if strings.Contains(normalized, "up to 60 min") || strings.Contains(normalized, "up to 30 min") {
		return true
	}
	if strings.Contains(normalized, "queued after all test scenarios complete") {
		return true
	}
	if strings.Contains(normalized, "gallery-style") || strings.Contains(normalized, "given prominence") {
		return true
	}
	if !(strings.HasPrefix(normalized, "each ") || strings.HasPrefix(normalized, "every ")) {
		return false
	}
	return strings.Contains(normalized, " should ") ||
		strings.Contains(normalized, " must ") ||
		strings.Contains(normalized, " needs to ") ||
		strings.Contains(normalized, " need to ")
}

func isExecutableDeliverable(item, normalized string) bool {
	if normalized == "" {
		return false
	}
	if leadingTaskActionPattern.MatchString(normalized) {
		return true
	}
	if labelledTaskPattern.MatchString(item) {
		return true
	}
	if strings.Contains(item, "—") || strings.Contains(item, " - ") {
		return true
	}
	return false
}

func expandCompoundDeliverable(item string) []string {
	trimmed := strings.TrimSpace(item)
	if trimmed == "" {
		return nil
	}
	if expanded := expandMarkdownEnumeratedBlock(trimmed); len(expanded) > 0 {
		return expanded
	}
	if expanded := expandDefineList(trimmed); len(expanded) > 0 {
		return expanded
	}
	if expanded := expandSynthesisList(trimmed); len(expanded) > 0 {
		return expanded
	}
	return []string{trimmed}
}

func expandMarkdownEnumeratedBlock(item string) []string {
	if !strings.Contains(item, "\n") {
		return nil
	}
	lines := strings.Split(item, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		cleaned := cleanSegment(line)
		if cleaned == "" {
			continue
		}
		out = append(out, cleaned)
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

func expandDefineList(item string) []string {
	lower := strings.ToLower(strings.TrimSpace(item))
	if !strings.HasPrefix(lower, "define ") {
		return nil
	}
	if strings.ContainsAny(item, "?:") {
		return nil
	}
	item = strings.TrimSpace(strings.TrimPrefix(item, "Define "))
	item = strings.TrimSpace(strings.TrimSuffix(item, "."))
	normalized := strings.ReplaceAll(item, " and ", ", ")
	parts := splitLooseList(normalized)
	if len(parts) < 3 {
		return nil
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, "Define "+part)
	}
	if len(out) < 3 {
		return nil
	}
	return out
}

func expandSynthesisList(item string) []string {
	trimmed := strings.TrimSpace(item)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "synthesize all ") {
		return nil
	}
	open := strings.Index(trimmed, "(")
	close := strings.Index(trimmed, ")")
	intoIdx := strings.Index(lower, " into ")
	if open < 0 || close <= open || intoIdx < 0 || intoIdx <= close {
		return nil
	}
	listBody := trimmed[open+1 : close]
	target := strings.TrimSpace(trimmed[intoIdx+len(" into "):])
	if target == "" {
		return nil
	}
	parts := splitLooseList(strings.ReplaceAll(listBody, " and ", ", "))
	if len(parts) < 3 {
		return nil
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, fmt.Sprintf("Draft %s section for the brief", part))
	}
	if len(out) < 3 {
		return nil
	}
	return out
}

func splitLooseList(raw string) []string {
	parts := splitTopLevelCommaList(raw)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(part), "."))
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func splitTopLevelCommaList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := make([]string, 0, 4)
	depth := 0
	start := 0
	for i, r := range raw {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, raw[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, raw[start:])
	return parts
}

func validateBoundedTaskSize(title string, description *string, parentScoped bool) error {
	estimatedMinutes, maxMinutes := estimateTaskMinutes(title, description, parentScoped)
	if estimatedMinutes <= maxMinutes {
		return nil
	}
	return QueueSizeError{
		EstimatedMinutes: estimatedMinutes,
		MaxMinutes:       maxMinutes,
		Reason:           "split the work into smaller reviewable tasks before queueing",
	}
}

func estimateTaskMinutes(title string, description *string, parentScoped bool) (int, int) {
	rawDescription := strings.TrimSpace(deref(description))
	text := strings.ToLower(strings.TrimSpace(strings.Join([]string{title, rawDescription}, " ")))
	isBoundedSectionDraft := strings.HasPrefix(strings.ToLower(strings.TrimSpace(title)), "draft ") &&
		strings.Contains(strings.ToLower(strings.TrimSpace(title)), " section for the brief")

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
	if strings.Contains(text, "detailed descriptions") {
		estimatedMinutes += 10
	}
	if strings.Contains(text, "brand narrative") {
		estimatedMinutes += 10
	}
	if matches := len(enumMarkerPattern.FindAllString(text, -1)); matches >= 3 {
		estimatedMinutes += matches * 5
	}
	if containsAny(text, broadScopeSignals) && !isBoundedSectionDraft && !shouldBypassBroadScopePenaltyForParentScopedChild(title, rawDescription, deliverables, parentScoped) {
		estimatedMinutes += 15
	}

	return estimatedMinutes, maxMinutes
}

func shouldBypassBroadScopePenaltyForParentScopedChild(title, rawDescription string, deliverables []string, parentScoped bool) bool {
	if !parentScoped {
		return false
	}
	normalizedTitle := strings.ToLower(strings.TrimSpace(title))
	if normalizedTitle == "" {
		return false
	}
	if titleSuggestsCompoundBoundedWork(title) {
		return false
	}
	if len(deliverables) > 1 {
		return false
	}
	if len(rawDescription) >= 220 {
		return false
	}
	if strings.Contains(title, ":") {
		return false
	}
	if len(enumMarkerPattern.FindAllString(strings.ToLower(rawDescription), -1)) > 0 {
		return false
	}
	return leadingTaskActionPattern.MatchString(normalizedTitle)
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
	for {
		trimmed := strings.TrimSpace(item)
		switch {
		case strings.HasPrefix(trimmed, "-"):
			item = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		case strings.HasPrefix(trimmed, "*"):
			item = strings.TrimSpace(strings.TrimPrefix(trimmed, "*"))
		default:
			item = trimmed
			goto cleanedPrefixes
		}
	}
cleanedPrefixes:
	for len(item) > 2 && item[0] >= '0' && item[0] <= '9' {
		item = strings.TrimSpace(item[1:])
		item = strings.TrimPrefix(item, ".")
		item = strings.TrimPrefix(item, ")")
		item = strings.TrimSpace(item)
	}
	item = strings.ReplaceAll(item, "**", "")
	item = strings.ReplaceAll(item, "`", "")
	lower := strings.ToLower(item)
	for _, prefix := range []string{
		"assigned to ",
		"assigned to:**",
		"assigned to:",
		"blocked on ",
		"agent:",
		"est:",
		"est. time:",
		"est. time:**",
		"target:",
		"sections:",
		"section:",
		"wave:",
		"output:",
		"estimated time:",
		"estimated time:**",
		"depends on:",
		"dependency:",
		"save to ",
	} {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	for _, prefix := range []string{"**output:**", "**agent:**", "**estimated time:**", "**depends on:**", "**dependency:**"} {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	if strings.HasPrefix(lower, "parent workstream:") {
		item = strings.TrimSpace(item[len("parent workstream:"):])
		lower = strings.ToLower(item)
	}
	if timingOnlyPattern.MatchString(lower) {
		return ""
	}
	if strings.Count(item, ",") >= 2 &&
		!strings.Contains(item, "—") &&
		!strings.Contains(item, " - ") &&
		!leadingTaskActionPattern.MatchString(lower) {
		return ""
	}
	if strings.HasSuffix(item, ":") {
		return ""
	}
	if len(item) < 10 {
		return ""
	}
	return strings.TrimSpace(item)
}

func atoiSafe(raw string) int {
	value := 0
	for _, ch := range strings.TrimSpace(raw) {
		if ch < '0' || ch > '9' {
			return 0
		}
		value = value*10 + int(ch-'0')
	}
	return value
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
