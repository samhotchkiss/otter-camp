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
	db *sql.DB
}

func NewConversationEmbeddingStore(db *sql.DB) *ConversationEmbeddingStore {
	return &ConversationEmbeddingStore{db: db}
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
		`SELECT id, org_id, body
		 FROM chat_messages
		 WHERE embedding IS NULL
		 ORDER BY created_at ASC, id ASC
		 LIMIT $1`,
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
		`UPDATE chat_messages
		 SET embedding = $2::vector
		 WHERE id = $1`,
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
		`SELECT id, org_id, title || E'\n\n' || content AS content
		 FROM memories
		 WHERE embedding IS NULL
		 ORDER BY created_at ASC, id ASC
		 LIMIT $1`,
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
		`UPDATE memories
		 SET embedding = $2::vector
		 WHERE id = $1`,
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
