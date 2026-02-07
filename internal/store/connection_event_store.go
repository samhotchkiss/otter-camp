package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const (
	ConnectionEventSeverityInfo    = "info"
	ConnectionEventSeverityWarning = "warning"
	ConnectionEventSeverityError   = "error"
)

type ConnectionEvent struct {
	ID        string          `json:"id"`
	OrgID     string          `json:"org_id"`
	EventType string          `json:"event_type"`
	Severity  string          `json:"severity"`
	Message   string          `json:"message"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
}

type CreateConnectionEventInput struct {
	EventType string
	Severity  string
	Message   string
	Metadata  json.RawMessage
}

type ConnectionEventStore struct {
	db *sql.DB
}

func NewConnectionEventStore(db *sql.DB) *ConnectionEventStore {
	return &ConnectionEventStore{db: db}
}

const connectionEventColumns = `
	id,
	org_id,
	event_type,
	severity,
	message,
	metadata,
	created_at
`

func (s *ConnectionEventStore) Create(ctx context.Context, input CreateConnectionEventInput) (*ConnectionEvent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	event, err := scanConnectionEvent(conn.QueryRowContext(
		ctx,
		`INSERT INTO connection_events (org_id, event_type, severity, message, metadata)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING`+connectionEventColumns,
		workspaceID,
		strings.TrimSpace(input.EventType),
		normalizeConnectionEventSeverity(input.Severity),
		strings.TrimSpace(input.Message),
		normalizeConnectionEventMetadata(input.Metadata),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create connection event: %w", err)
	}
	return &event, nil
}

func (s *ConnectionEventStore) CreateWithWorkspaceID(
	ctx context.Context,
	workspaceID string,
	input CreateConnectionEventInput,
) (*ConnectionEvent, error) {
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspaceID(ctx, s.db, workspaceID)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	event, err := scanConnectionEvent(conn.QueryRowContext(
		ctx,
		`INSERT INTO connection_events (org_id, event_type, severity, message, metadata)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING`+connectionEventColumns,
		workspaceID,
		strings.TrimSpace(input.EventType),
		normalizeConnectionEventSeverity(input.Severity),
		strings.TrimSpace(input.Message),
		normalizeConnectionEventMetadata(input.Metadata),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create connection event with workspace id: %w", err)
	}
	return &event, nil
}

func (s *ConnectionEventStore) List(ctx context.Context, limit int) ([]ConnectionEvent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	rows, err := conn.QueryContext(
		ctx,
		`SELECT `+connectionEventColumns+`
		 FROM connection_events
		 WHERE org_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2`,
		workspaceID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list connection events: %w", err)
	}
	defer rows.Close()

	events := make([]ConnectionEvent, 0, limit)
	for rows.Next() {
		event, scanErr := scanConnectionEvent(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan connection event: %w", scanErr)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate connection events: %w", err)
	}

	return events, nil
}

func scanConnectionEvent(scanner interface{ Scan(...any) error }) (ConnectionEvent, error) {
	var (
		event         ConnectionEvent
		metadataBytes []byte
	)
	err := scanner.Scan(
		&event.ID,
		&event.OrgID,
		&event.EventType,
		&event.Severity,
		&event.Message,
		&metadataBytes,
		&event.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return event, ErrNotFound
		}
		return event, err
	}

	if len(metadataBytes) == 0 {
		event.Metadata = json.RawMessage(`{}`)
	} else {
		event.Metadata = json.RawMessage(metadataBytes)
	}
	return event, nil
}

func normalizeConnectionEventSeverity(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case ConnectionEventSeverityWarning:
		return ConnectionEventSeverityWarning
	case ConnectionEventSeverityError:
		return ConnectionEventSeverityError
	default:
		return ConnectionEventSeverityInfo
	}
}

func normalizeConnectionEventMetadata(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`)
	}
	return raw
}
