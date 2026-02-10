package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const (
	MemoryEventTypeMemoryCreated       = "memory.created"
	MemoryEventTypeMemoryPromoted      = "memory.promoted"
	MemoryEventTypeMemoryArchived      = "memory.archived"
	MemoryEventTypeKnowledgeShared     = "knowledge.shared"
	MemoryEventTypeKnowledgeConfirmed  = "knowledge.confirmed"
	MemoryEventTypeKnowledgeContradict = "knowledge.contradicted"
	MemoryEventTypeCompactionDetected  = "compaction.detected"
	MemoryEventTypeCompactionRecovered = "compaction.recovered"
)

var (
	ErrMemoryEventTypeInvalid = errors.New("memory event type is invalid")
)

var memoryEventTypes = map[string]struct{}{
	MemoryEventTypeMemoryCreated:       {},
	MemoryEventTypeMemoryPromoted:      {},
	MemoryEventTypeMemoryArchived:      {},
	MemoryEventTypeKnowledgeShared:     {},
	MemoryEventTypeKnowledgeConfirmed:  {},
	MemoryEventTypeKnowledgeContradict: {},
	MemoryEventTypeCompactionDetected:  {},
	MemoryEventTypeCompactionRecovered: {},
}

type MemoryEvent struct {
	ID        int64           `json:"id"`
	OrgID     string          `json:"org_id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type PublishMemoryEventInput struct {
	EventType string
	Payload   json.RawMessage
}

type ListMemoryEventsParams struct {
	Since *time.Time
	Types []string
	Limit int
}

type MemoryEventsStore struct {
	db *sql.DB
}

func NewMemoryEventsStore(db *sql.DB) *MemoryEventsStore {
	return &MemoryEventsStore{db: db}
}

func (s *MemoryEventsStore) Publish(ctx context.Context, input PublishMemoryEventInput) (*MemoryEvent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	eventType := strings.TrimSpace(strings.ToLower(input.EventType))
	if _, ok := memoryEventTypes[eventType]; !ok {
		return nil, ErrMemoryEventTypeInvalid
	}

	payload := normalizeJSONMap(input.Payload)

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	event, err := scanMemoryEvent(conn.QueryRowContext(
		ctx,
		`INSERT INTO memory_events (org_id, event_type, payload)
		 VALUES ($1, $2, $3::jsonb)
		 RETURNING id, org_id, event_type, payload, created_at`,
		workspaceID,
		eventType,
		payload,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to publish memory event: %w", err)
	}
	return &event, nil
}

func (s *MemoryEventsStore) List(ctx context.Context, params ListMemoryEventsParams) ([]MemoryEvent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	eventTypes, err := normalizeMemoryEventTypes(params.Types)
	if err != nil {
		return nil, err
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	var since interface{}
	if params.Since != nil {
		since = params.Since.UTC()
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, event_type, payload, created_at
		 FROM memory_events
		 WHERE org_id = $1
		   AND ($2::timestamptz IS NULL OR created_at > $2)
		   AND (
			COALESCE(array_length($3::text[], 1), 0) = 0
			OR event_type = ANY($3::text[])
		   )
		 ORDER BY created_at DESC, id DESC
		 LIMIT $4`,
		workspaceID,
		since,
		pq.Array(eventTypes),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list memory events: %w", err)
	}
	defer rows.Close()

	events := make([]MemoryEvent, 0)
	for rows.Next() {
		event, scanErr := scanMemoryEvent(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan memory event: %w", scanErr)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate memory events: %w", err)
	}
	return events, nil
}

func normalizeMemoryEventTypes(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}

	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		eventType := strings.TrimSpace(strings.ToLower(value))
		if _, ok := memoryEventTypes[eventType]; !ok {
			return nil, ErrMemoryEventTypeInvalid
		}
		if _, ok := seen[eventType]; ok {
			continue
		}
		seen[eventType] = struct{}{}
		out = append(out, eventType)
	}
	return out, nil
}

func scanMemoryEvent(scanner interface{ Scan(...any) error }) (MemoryEvent, error) {
	var event MemoryEvent
	var payloadBytes []byte
	err := scanner.Scan(
		&event.ID,
		&event.OrgID,
		&event.EventType,
		&payloadBytes,
		&event.CreatedAt,
	)
	if err != nil {
		return MemoryEvent{}, err
	}
	if len(payloadBytes) == 0 {
		event.Payload = json.RawMessage(`{}`)
	} else {
		event.Payload = json.RawMessage(payloadBytes)
	}
	return event, nil
}
