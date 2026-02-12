package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

var validEllieMemoryKinds = map[string]struct{}{
	"technical_decision": {},
	"process_decision":   {},
	"preference":         {},
	"fact":               {},
	"lesson":             {},
	"pattern":            {},
	"anti_pattern":       {},
	"correction":         {},
	"process_outcome":    {},
	"context":            {},
}

var validEllieMemoryStatuses = map[string]struct{}{
	"active":     {},
	"deprecated": {},
	"archived":   {},
}

type CreateEllieExtractedMemoryInput struct {
	OrgID                string
	Kind                 string
	Title                string
	Content              string
	Metadata             json.RawMessage
	Importance           int
	Confidence           float64
	Status               string
	Sensitivity          string
	OccurredAt           time.Time
	SourceConversationID *string
	SourceProjectID      *string
}

type EllieIngestionStore struct {
	db *sql.DB
}

func NewEllieIngestionStore(db *sql.DB) *EllieIngestionStore {
	return &EllieIngestionStore{db: db}
}

func (s *EllieIngestionStore) CreateEllieExtractedMemory(ctx context.Context, input CreateEllieExtractedMemoryInput) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("ellie ingestion store is not configured")
	}

	orgID := strings.TrimSpace(input.OrgID)
	if !uuidRegex.MatchString(orgID) {
		return "", fmt.Errorf("invalid org_id")
	}

	kind := strings.TrimSpace(strings.ToLower(input.Kind))
	if _, ok := validEllieMemoryKinds[kind]; !ok {
		return "", fmt.Errorf("invalid kind")
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		return "", fmt.Errorf("title is required")
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return "", fmt.Errorf("content is required")
	}

	importance := input.Importance
	if importance == 0 {
		importance = 3
	}
	if importance < 1 || importance > 5 {
		return "", fmt.Errorf("invalid importance")
	}

	confidence := input.Confidence
	if confidence == 0 {
		confidence = 0.5
	}
	if math.IsNaN(confidence) || confidence < 0 || confidence > 1 {
		return "", fmt.Errorf("invalid confidence")
	}

	status := strings.TrimSpace(strings.ToLower(input.Status))
	if status == "" {
		status = "active"
	}
	if _, ok := validEllieMemoryStatuses[status]; !ok {
		return "", fmt.Errorf("invalid status")
	}

	sensitivity, err := normalizeEllieSensitivity(input.Sensitivity)
	if err != nil {
		return "", err
	}

	occurredAt := input.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	sourceConversationID, err := normalizeOptionalEllieUUID(input.SourceConversationID)
	if err != nil {
		return "", fmt.Errorf("source_conversation_id: %w", err)
	}
	sourceProjectID, err := normalizeOptionalEllieUUID(input.SourceProjectID)
	if err != nil {
		return "", fmt.Errorf("source_project_id: %w", err)
	}

	metadata := normalizeJSONMap(input.Metadata)

	var memoryID string
	err = s.db.QueryRowContext(
		ctx,
		`INSERT INTO memories (
			org_id, kind, title, content, metadata, importance, confidence,
			status, source_conversation_id, source_project_id, occurred_at, sensitivity
		) VALUES (
			$1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9, $10, $11, $12
		)
		RETURNING id`,
		orgID,
		kind,
		title,
		content,
		metadata,
		importance,
		confidence,
		status,
		sourceConversationID,
		sourceProjectID,
		occurredAt,
		sensitivity,
	).Scan(&memoryID)
	if err != nil {
		return "", fmt.Errorf("failed to create ellie extracted memory: %w", err)
	}

	return memoryID, nil
}

func normalizeOptionalEllieUUID(value *string) (interface{}, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	if !uuidRegex.MatchString(trimmed) {
		return nil, fmt.Errorf("invalid uuid")
	}
	return trimmed, nil
}
