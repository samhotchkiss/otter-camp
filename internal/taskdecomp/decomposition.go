package taskdecomp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	metadataKeyDecomposition   = "decomposition"
	descriptionThresholdChars  = 320
	maxGeneratedChildWorkUnits = 5
)

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

	requires := len(rawDescription) >= descriptionThresholdChars || len(deliverables) >= 3
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
	if len(metadata) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return ""
	}
	raw, ok := payload[metadataKeyDecomposition]
	if !ok {
		return ""
	}
	decomp, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	primary, _ := decomp["primary_deliverable"].(string)
	return strings.TrimSpace(primary)
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

	payload[metadataKeyDecomposition] = map[string]any{
		"applied":             true,
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

func normalizeJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`{}`)
	}
	return value
}
