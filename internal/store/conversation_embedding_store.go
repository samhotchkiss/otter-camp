package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrConversationEmbeddingVectorEmpty = errors.New("embedding vector is empty")

type PendingChatMessageEmbedding struct {
	ID    string
	OrgID string
	Body  string
}

type PendingMemoryEmbedding struct {
	ID      string
	OrgID   string
	Content string
}

type ConversationEmbeddingStore struct {
	db              *sql.DB
	targetDimension int
}

func NewConversationEmbeddingStore(db *sql.DB) *ConversationEmbeddingStore {
	return NewConversationEmbeddingStoreWithDimension(db, legacyEmbeddingDimension)
}

func NewConversationEmbeddingStoreWithDimension(db *sql.DB, targetDimension int) *ConversationEmbeddingStore {
	return &ConversationEmbeddingStore{
		db:              db,
		targetDimension: normalizeEmbeddingDimension(targetDimension),
	}
}

func (s *ConversationEmbeddingStore) embeddingColumn() string {
	if s == nil {
		return embeddingColumnForDimension(legacyEmbeddingDimension)
	}
	return embeddingColumnForDimension(s.targetDimension)
}

func (s *ConversationEmbeddingStore) ListPendingChatMessages(ctx context.Context, limit int) ([]PendingChatMessageEmbedding, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("conversation embedding store is not configured")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	rows, err := s.db.QueryContext(
		ctx,
		fmt.Sprintf(`WITH ranked AS (
			SELECT
				id,
				org_id,
				body,
				ROW_NUMBER() OVER (
					PARTITION BY org_id
					ORDER BY created_at ASC, id ASC
				) AS org_rank
			FROM chat_messages
			WHERE %s IS NULL
		)
		 SELECT id, org_id, body
		 FROM ranked
		 ORDER BY org_rank ASC, org_id ASC, id ASC
		 LIMIT $1`, s.embeddingColumn()),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending chat message embeddings: %w", err)
	}
	defer rows.Close()

	pending := make([]PendingChatMessageEmbedding, 0, limit)
	for rows.Next() {
		var row PendingChatMessageEmbedding
		if err := rows.Scan(&row.ID, &row.OrgID, &row.Body); err != nil {
			return nil, fmt.Errorf("failed to scan pending chat message embedding row: %w", err)
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading pending chat message embeddings: %w", err)
	}
	return pending, nil
}

func (s *ConversationEmbeddingStore) CountPendingEmbeddings(ctx context.Context, orgID string) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("conversation embedding store is not configured")
	}

	orgID = strings.TrimSpace(orgID)
	column := s.embeddingColumn()
	var (
		query string
		args  []any
	)
	if orgID == "" {
		query = fmt.Sprintf(
			`SELECT
			  (SELECT COUNT(*) FROM chat_messages WHERE %s IS NULL) +
			  (SELECT COUNT(*) FROM memories WHERE %s IS NULL)`,
			column,
			column,
		)
		args = nil
	} else {
		query = fmt.Sprintf(
			`SELECT
			  (SELECT COUNT(*) FROM chat_messages WHERE org_id = $1 AND %s IS NULL) +
			  (SELECT COUNT(*) FROM memories WHERE org_id = $1 AND %s IS NULL)`,
			column,
			column,
		)
		args = []any{orgID}
	}

	var pending int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&pending); err != nil {
		return 0, fmt.Errorf("failed to count pending embeddings: %w", err)
	}
	if pending < 0 {
		pending = 0
	}
	return pending, nil
}

func (s *ConversationEmbeddingStore) UpdateChatMessageEmbedding(ctx context.Context, messageID string, embedding []float64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("conversation embedding store is not configured")
	}
	vectorLiteral, err := formatVectorLiteral(embedding)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(
		ctx,
		fmt.Sprintf(`UPDATE chat_messages
		 SET %s = $2::vector
		 WHERE id = $1`, s.embeddingColumn()),
		strings.TrimSpace(messageID),
		vectorLiteral,
	)
	if err != nil {
		return fmt.Errorf("failed to update chat message embedding: %w", err)
	}
	return nil
}

func (s *ConversationEmbeddingStore) ListPendingMemories(ctx context.Context, limit int) ([]PendingMemoryEmbedding, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("conversation embedding store is not configured")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	rows, err := s.db.QueryContext(
		ctx,
		fmt.Sprintf(`WITH ranked AS (
			SELECT
				id,
				org_id,
				title || E'\n\n' || content AS content,
				ROW_NUMBER() OVER (
					PARTITION BY org_id
					ORDER BY created_at ASC, id ASC
				) AS org_rank
			FROM memories
			WHERE %s IS NULL
		)
		 SELECT id, org_id, content
		 FROM ranked
		 ORDER BY org_rank ASC, org_id ASC, id ASC
		 LIMIT $1`, s.embeddingColumn()),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending memory embeddings: %w", err)
	}
	defer rows.Close()

	pending := make([]PendingMemoryEmbedding, 0, limit)
	for rows.Next() {
		var row PendingMemoryEmbedding
		if err := rows.Scan(&row.ID, &row.OrgID, &row.Content); err != nil {
			return nil, fmt.Errorf("failed to scan pending memory embedding row: %w", err)
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading pending memory embeddings: %w", err)
	}
	return pending, nil
}

func (s *ConversationEmbeddingStore) UpdateMemoryEmbedding(ctx context.Context, memoryID string, embedding []float64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("conversation embedding store is not configured")
	}
	vectorLiteral, err := formatVectorLiteral(embedding)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(
		ctx,
		fmt.Sprintf(`UPDATE memories
		 SET %s = $2::vector
		 WHERE id = $1`, s.embeddingColumn()),
		strings.TrimSpace(memoryID),
		vectorLiteral,
	)
	if err != nil {
		return fmt.Errorf("failed to update memory embedding: %w", err)
	}
	return nil
}

func formatVectorLiteral(values []float64) (string, error) {
	if len(values) == 0 {
		return "", ErrConversationEmbeddingVectorEmpty
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.FormatFloat(value, 'f', -1, 64))
	}
	return "[" + strings.Join(parts, ",") + "]", nil
}
