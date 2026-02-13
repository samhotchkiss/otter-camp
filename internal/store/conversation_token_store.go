package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const defaultConversationTokenBackfillBatchSize = 200

type ConversationTokenStore struct {
	db *sql.DB
}

type RoomTokenSummary struct {
	ID          string
	Name        string
	Type        string
	TotalTokens int64
}

type ConversationTokenSummary struct {
	ID          string
	RoomID      string
	Topic       string
	TotalTokens int64
}

type RoomTokenSenderStat struct {
	SenderID    string
	SenderType  string
	TotalTokens int64
}

type RoomTokenStats struct {
	RoomID                   string
	RoomName                 string
	TotalTokens              int64
	ConversationCount        int
	AvgTokensPerConversation int64
	Last7DaysTokens          int64
	TokensBySender           []RoomTokenSenderStat
}

func NewConversationTokenStore(db *sql.DB) *ConversationTokenStore {
	return &ConversationTokenStore{db: db}
}

func (s *ConversationTokenStore) BackfillMissingTokenCounts(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = defaultConversationTokenBackfillBatchSize
	}

	result, err := s.db.ExecContext(
		ctx,
		`WITH ranked AS (
			SELECT
				id,
				org_id,
				created_at,
				ROW_NUMBER() OVER (
					PARTITION BY org_id
					ORDER BY created_at ASC, id ASC
				) AS org_rank
			  FROM chat_messages
			 WHERE token_count IS NULL
			 FOR UPDATE SKIP LOCKED
		), candidates AS (
			SELECT id
			  FROM ranked
			 ORDER BY org_rank ASC, org_id ASC, created_at ASC, id ASC
			 LIMIT $1
		)
		UPDATE chat_messages cm
		   SET token_count = otter_estimate_token_count(cm.body)
		  FROM candidates c
		 WHERE cm.id = c.id`,
		limit,
	)
	if err != nil {
		return 0, err
	}

	updated, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(updated), nil
}

func (s *ConversationTokenStore) GetRoomTokenSummary(ctx context.Context, roomID string) (*RoomTokenSummary, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	roomID = strings.TrimSpace(roomID)
	if !uuidRegex.MatchString(roomID) {
		return nil, fmt.Errorf("invalid room_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var summary RoomTokenSummary
	err = conn.QueryRowContext(
		ctx,
		`SELECT id::text, COALESCE(name, ''), type, COALESCE(total_tokens, 0)
		   FROM rooms
		  WHERE org_id = $1
		    AND id = $2`,
		workspaceID,
		roomID,
	).Scan(&summary.ID, &summary.Name, &summary.Type, &summary.TotalTokens)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return &summary, nil
}

func (s *ConversationTokenStore) GetConversationTokenSummary(ctx context.Context, conversationID string) (*ConversationTokenSummary, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conversationID = strings.TrimSpace(conversationID)
	if !uuidRegex.MatchString(conversationID) {
		return nil, fmt.Errorf("invalid conversation_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var summary ConversationTokenSummary
	err = conn.QueryRowContext(
		ctx,
		`SELECT id::text, room_id::text, COALESCE(topic, ''), COALESCE(total_tokens, 0)
		   FROM conversations
		  WHERE org_id = $1
		    AND id = $2`,
		workspaceID,
		conversationID,
	).Scan(&summary.ID, &summary.RoomID, &summary.Topic, &summary.TotalTokens)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return &summary, nil
}

func (s *ConversationTokenStore) GetRoomTokenStats(ctx context.Context, roomID string) (*RoomTokenStats, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	roomID = strings.TrimSpace(roomID)
	if !uuidRegex.MatchString(roomID) {
		return nil, fmt.Errorf("invalid room_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	stats := &RoomTokenStats{RoomID: roomID}
	err = conn.QueryRowContext(
		ctx,
		`SELECT
			COALESCE(r.name, ''),
			COALESCE(r.total_tokens, 0),
			COALESCE(c.conversation_count, 0),
			COALESCE(t.last_7_days_tokens, 0)
		FROM rooms r
		LEFT JOIN (
			SELECT room_id, COUNT(*)::INT AS conversation_count
			  FROM conversations
			 WHERE org_id = $1
			 GROUP BY room_id
		) c ON c.room_id = r.id
		LEFT JOIN (
			SELECT room_id, COALESCE(SUM(token_count), 0)::BIGINT AS last_7_days_tokens
			  FROM chat_messages
			 WHERE org_id = $1
			   AND created_at >= NOW() - INTERVAL '7 days'
			 GROUP BY room_id
		) t ON t.room_id = r.id
		WHERE r.org_id = $1
		  AND r.id = $2`,
		workspaceID,
		roomID,
	).Scan(&stats.RoomName, &stats.TotalTokens, &stats.ConversationCount, &stats.Last7DaysTokens)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if stats.ConversationCount > 0 {
		stats.AvgTokensPerConversation = stats.TotalTokens / int64(stats.ConversationCount)
	}

	rows, err := conn.QueryContext(
		ctx,
		`SELECT sender_id::text, sender_type, COALESCE(SUM(token_count), 0)::BIGINT AS token_total
		   FROM chat_messages
		  WHERE org_id = $1
		    AND room_id = $2
		  GROUP BY sender_id, sender_type
		  ORDER BY token_total DESC, sender_type ASC, sender_id ASC`,
		workspaceID,
		roomID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats.TokensBySender = make([]RoomTokenSenderStat, 0)
	for rows.Next() {
		var sender RoomTokenSenderStat
		if err := rows.Scan(&sender.SenderID, &sender.SenderType, &sender.TotalTokens); err != nil {
			return nil, err
		}
		stats.TokensBySender = append(stats.TokensBySender, sender)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return stats, nil
}
