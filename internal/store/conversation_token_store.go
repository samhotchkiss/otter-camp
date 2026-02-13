package store

import (
	"context"
	"database/sql"
)

const defaultConversationTokenBackfillBatchSize = 200

type ConversationTokenStore struct {
	db *sql.DB
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
		`WITH candidates AS (
			SELECT id
			  FROM chat_messages
			 WHERE token_count IS NULL
			 ORDER BY created_at ASC, id ASC
			 LIMIT $1
			 FOR UPDATE SKIP LOCKED
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
