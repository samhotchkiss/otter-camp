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

type EllieRoomIngestionCandidate struct {
	OrgID  string
	RoomID string
}

type EllieRoomCursor struct {
	OrgID                string
	RoomID               string
	LastMessageID        string
	LastMessageCreatedAt time.Time
}

type EllieIngestionMessage struct {
	ID             string
	OrgID          string
	RoomID         string
	Body           string
	CreatedAt      time.Time
	ConversationID *string
}

type UpsertEllieRoomCursorInput struct {
	OrgID                string
	RoomID               string
	LastMessageID        string
	LastMessageCreatedAt time.Time
}

type CreateEllieExtractedMemoryInput struct {
	OrgID                string
	Kind                 string
	Title                string
	Content              string
	Metadata             json.RawMessage
	Importance           int
	Confidence           float64
	SourceConversationID *string
	OccurredAt           time.Time
}

type EllieIngestionStore struct {
	db *sql.DB
}

func NewEllieIngestionStore(db *sql.DB) *EllieIngestionStore {
	return &EllieIngestionStore{db: db}
}

func (s *EllieIngestionStore) ListRoomsForIngestion(ctx context.Context, limit int) ([]EllieRoomIngestionCandidate, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie ingestion store is not configured")
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT org_id, room_id
		 FROM chat_messages
		 GROUP BY org_id, room_id
		 ORDER BY MAX(created_at) ASC, room_id ASC
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list ellie ingestion rooms: %w", err)
	}
	defer rows.Close()

	candidates := make([]EllieRoomIngestionCandidate, 0, limit)
	for rows.Next() {
		var row EllieRoomIngestionCandidate
		if err := rows.Scan(&row.OrgID, &row.RoomID); err != nil {
			return nil, fmt.Errorf("failed to scan ellie ingestion room: %w", err)
		}
		candidates = append(candidates, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading ellie ingestion rooms: %w", err)
	}
	return candidates, nil
}

func (s *EllieIngestionStore) GetRoomCursor(ctx context.Context, orgID, roomID string) (*EllieRoomCursor, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie ingestion store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	roomID = strings.TrimSpace(roomID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(roomID) {
		return nil, fmt.Errorf("invalid room_id")
	}

	var (
		row           EllieRoomCursor
		lastMessageID sql.NullString
	)
	err := s.db.QueryRowContext(
		ctx,
		`SELECT org_id, source_id, last_message_id::text, COALESCE(last_message_created_at, TIMESTAMPTZ '1970-01-01')
		 FROM ellie_ingestion_cursors
		 WHERE org_id = $1
		   AND source_type = 'room'
		   AND source_id = $2`,
		orgID,
		roomID,
	).Scan(&row.OrgID, &row.RoomID, &lastMessageID, &row.LastMessageCreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to load ellie room cursor: %w", err)
	}
	if lastMessageID.Valid {
		row.LastMessageID = lastMessageID.String
	}
	return &row, nil
}

func (s *EllieIngestionStore) ListRoomMessagesSince(
	ctx context.Context,
	orgID,
	roomID string,
	afterCreatedAt *time.Time,
	afterMessageID *string,
	limit int,
) ([]EllieIngestionMessage, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie ingestion store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	roomID = strings.TrimSpace(roomID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(roomID) {
		return nil, fmt.Errorf("invalid room_id")
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}

	args := []any{orgID, roomID}
	where := `WHERE org_id = $1 AND room_id = $2`
	if afterCreatedAt != nil && afterMessageID != nil && strings.TrimSpace(*afterMessageID) != "" {
		where += ` AND (created_at, id) > ($3, $4)`
		args = append(args, afterCreatedAt.UTC(), strings.TrimSpace(*afterMessageID))
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, org_id, room_id, body, created_at, conversation_id::text
		 FROM chat_messages
		 `+where+`
		 ORDER BY created_at ASC, id ASC
		 LIMIT $`+fmt.Sprint(len(args)),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list ellie room messages: %w", err)
	}
	defer rows.Close()

	messages := make([]EllieIngestionMessage, 0, limit)
	for rows.Next() {
		var (
			row            EllieIngestionMessage
			conversationID sql.NullString
		)
		if err := rows.Scan(&row.ID, &row.OrgID, &row.RoomID, &row.Body, &row.CreatedAt, &conversationID); err != nil {
			return nil, fmt.Errorf("failed to scan ellie room message: %w", err)
		}
		if conversationID.Valid {
			value := strings.TrimSpace(conversationID.String)
			if value != "" {
				row.ConversationID = &value
			}
		}
		messages = append(messages, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading ellie room messages: %w", err)
	}
	return messages, nil
}

func (s *EllieIngestionStore) InsertExtractedMemory(ctx context.Context, input CreateEllieExtractedMemoryInput) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("ellie ingestion store is not configured")
	}
	orgID := strings.TrimSpace(input.OrgID)
	if !uuidRegex.MatchString(orgID) {
		return false, fmt.Errorf("invalid org_id")
	}
	kind := strings.TrimSpace(input.Kind)
	title := strings.TrimSpace(input.Title)
	content := strings.TrimSpace(input.Content)
	if kind == "" || title == "" || content == "" {
		return false, fmt.Errorf("kind, title, and content are required")
	}
	importance := input.Importance
	if importance <= 0 {
		importance = 3
	}
	if importance > 5 {
		importance = 5
	}
	confidence := input.Confidence
	if math.IsNaN(confidence) || confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}
	occurredAt := input.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	var sourceConversation any
	if input.SourceConversationID != nil {
		sourceConversationID := strings.TrimSpace(*input.SourceConversationID)
		if sourceConversationID != "" {
			sourceConversation = sourceConversationID
		}
	}

	metadata := input.Metadata
	if len(strings.TrimSpace(string(metadata))) == 0 {
		metadata = json.RawMessage(`{}`)
	}

	result, err := s.db.ExecContext(
		ctx,
		`INSERT INTO memories (
			org_id,
			kind,
			title,
			content,
			metadata,
			importance,
			confidence,
			status,
			source_conversation_id,
			occurred_at
		) VALUES (
			$1, $2, $3, $4, $5::jsonb, $6, $7, 'active', $8, $9
		)
		ON CONFLICT (org_id, content_hash) WHERE status = 'active' DO NOTHING`,
		orgID,
		kind,
		title,
		content,
		metadata,
		importance,
		confidence,
		sourceConversation,
		occurredAt,
	)
	if err != nil {
		return false, fmt.Errorf("failed to insert ellie extracted memory: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to read ellie extracted memory insert count: %w", err)
	}
	return rowsAffected > 0, nil
}

func (s *EllieIngestionStore) UpsertRoomCursor(ctx context.Context, input UpsertEllieRoomCursorInput) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("ellie ingestion store is not configured")
	}
	orgID := strings.TrimSpace(input.OrgID)
	roomID := strings.TrimSpace(input.RoomID)
	if !uuidRegex.MatchString(orgID) {
		return fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(roomID) {
		return fmt.Errorf("invalid room_id")
	}
	occurredAt := input.LastMessageCreatedAt.UTC()
	if occurredAt.IsZero() {
		return fmt.Errorf("last_message_created_at is required")
	}

	var lastMessageID any
	trimmedMessageID := strings.TrimSpace(input.LastMessageID)
	if trimmedMessageID != "" {
		if !uuidRegex.MatchString(trimmedMessageID) {
			return fmt.Errorf("invalid last_message_id")
		}
		lastMessageID = trimmedMessageID
	}

	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO ellie_ingestion_cursors (
			org_id,
			source_type,
			source_id,
			last_message_id,
			last_message_created_at
		) VALUES (
			$1,
			'room',
			$2,
			$3,
			$4
		)
		ON CONFLICT (org_id, source_type, source_id) DO UPDATE
		SET
			last_message_id = EXCLUDED.last_message_id,
			last_message_created_at = EXCLUDED.last_message_created_at,
			updated_at = NOW()`,
		orgID,
		roomID,
		lastMessageID,
		occurredAt,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert ellie room cursor: %w", err)
	}
	return nil
}
