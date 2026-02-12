package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

type PendingConversationSegmentationMessage struct {
	ID        string
	OrgID     string
	RoomID    string
	Body      string
	CreatedAt time.Time
}

type CreateConversationSegmentInput struct {
	OrgID      string
	RoomID     string
	Topic      string
	StartedAt  time.Time
	EndedAt    time.Time
	MessageIDs []string
}

type ConversationSegmentationStore struct {
	db *sql.DB
}

func NewConversationSegmentationStore(db *sql.DB) *ConversationSegmentationStore {
	return &ConversationSegmentationStore{db: db}
}

func (s *ConversationSegmentationStore) ListPendingConversationMessages(ctx context.Context, limit int) ([]PendingConversationSegmentationMessage, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("conversation segmentation store is not configured")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := s.db.QueryContext(
		ctx,
		`WITH ranked AS (
			SELECT
				id,
				org_id,
				room_id,
				body,
				created_at,
				ROW_NUMBER() OVER (
					PARTITION BY org_id
					ORDER BY room_id ASC, created_at ASC, id ASC
				) AS org_rank
			FROM chat_messages
			WHERE conversation_id IS NULL
		)
		 SELECT id, org_id, room_id, body, created_at
		 FROM ranked
		 ORDER BY org_rank ASC, org_id ASC, room_id ASC, created_at ASC, id ASC
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending conversation messages: %w", err)
	}
	defer rows.Close()

	pending := make([]PendingConversationSegmentationMessage, 0, limit)
	for rows.Next() {
		var row PendingConversationSegmentationMessage
		if err := rows.Scan(&row.ID, &row.OrgID, &row.RoomID, &row.Body, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan pending conversation message row: %w", err)
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading pending conversation message rows: %w", err)
	}
	return pending, nil
}

func (s *ConversationSegmentationStore) CreateConversationSegment(ctx context.Context, input CreateConversationSegmentInput) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("conversation segmentation store is not configured")
	}
	orgID := strings.TrimSpace(input.OrgID)
	roomID := strings.TrimSpace(input.RoomID)
	if !uuidRegex.MatchString(orgID) {
		return "", fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(roomID) {
		return "", fmt.Errorf("invalid room_id")
	}
	if len(input.MessageIDs) == 0 {
		return "", fmt.Errorf("message_ids are required")
	}
	startedAt := input.StartedAt.UTC()
	if startedAt.IsZero() {
		return "", fmt.Errorf("started_at is required")
	}
	endedAt := input.EndedAt.UTC()
	if endedAt.IsZero() {
		endedAt = startedAt
	}
	topic := strings.TrimSpace(input.Topic)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to begin conversation segment transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var conversationID string
	err = tx.QueryRowContext(
		ctx,
		`INSERT INTO conversations (org_id, room_id, topic, started_at, ended_at)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		orgID,
		roomID,
		topic,
		startedAt,
		endedAt,
	).Scan(&conversationID)
	if err != nil {
		return "", fmt.Errorf("failed to create conversation segment: %w", err)
	}

	result, err := tx.ExecContext(
		ctx,
		`UPDATE chat_messages
		 SET conversation_id = $4
		 WHERE id = ANY($1)
		   AND org_id = $2
		   AND room_id = $3
		   AND conversation_id IS NULL`,
		pq.Array(input.MessageIDs),
		orgID,
		roomID,
		conversationID,
	)
	if err != nil {
		return "", fmt.Errorf("failed to assign conversation segment messages: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("failed to read updated conversation message count: %w", err)
	}
	if rowsAffected == 0 {
		return "", fmt.Errorf("no pending messages were assigned to conversation segment")
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("failed to commit conversation segment transaction: %w", err)
	}
	committed = true
	return conversationID, nil
}
