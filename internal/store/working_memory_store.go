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

var (
	ErrWorkingMemoryInvalidAgentID  = errors.New("working memory agent_id is invalid")
	ErrWorkingMemorySessionRequired = errors.New("working memory session_key is required")
	ErrWorkingMemoryContentRequired = errors.New("working memory content is required")
)

type WorkingMemoryEntry struct {
	ID         string          `json:"id"`
	OrgID      string          `json:"org_id"`
	AgentID    string          `json:"agent_id"`
	SessionKey string          `json:"session_key"`
	Content    string          `json:"content"`
	Metadata   json.RawMessage `json:"metadata"`
	CreatedAt  time.Time       `json:"created_at"`
	ExpiresAt  time.Time       `json:"expires_at"`
}

type CreateWorkingMemoryInput struct {
	AgentID    string
	SessionKey string
	Content    string
	Metadata   json.RawMessage
	ExpiresAt  *time.Time
}

type WorkingMemoryStore struct {
	db *sql.DB
}

func NewWorkingMemoryStore(db *sql.DB) *WorkingMemoryStore {
	return &WorkingMemoryStore{db: db}
}

func (s *WorkingMemoryStore) Create(ctx context.Context, input CreateWorkingMemoryInput) (*WorkingMemoryEntry, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	agentID := strings.TrimSpace(input.AgentID)
	if !uuidRegex.MatchString(agentID) {
		return nil, ErrWorkingMemoryInvalidAgentID
	}

	sessionKey := strings.TrimSpace(input.SessionKey)
	if sessionKey == "" {
		return nil, ErrWorkingMemorySessionRequired
	}

	content := strings.TrimSpace(input.Content)
	if content == "" {
		return nil, ErrWorkingMemoryContentRequired
	}

	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	if input.ExpiresAt != nil {
		expiresAt = input.ExpiresAt.UTC()
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	entry, err := scanWorkingMemoryEntry(conn.QueryRowContext(
		ctx,
		`INSERT INTO working_memory (org_id, agent_id, session_key, content, metadata, expires_at)
		 VALUES ($1, $2, $3, $4, $5::jsonb, $6)
		 RETURNING id, org_id, agent_id, session_key, content, metadata, created_at, expires_at`,
		workspaceID,
		agentID,
		sessionKey,
		content,
		normalizeJSONMap(input.Metadata),
		expiresAt,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create working memory entry: %w", err)
	}
	return &entry, nil
}

func (s *WorkingMemoryStore) ListBySession(
	ctx context.Context,
	agentID string,
	sessionKey string,
	limit int,
) ([]WorkingMemoryEntry, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	normalizedAgentID := strings.TrimSpace(agentID)
	if !uuidRegex.MatchString(normalizedAgentID) {
		return nil, ErrWorkingMemoryInvalidAgentID
	}

	normalizedSessionKey := strings.TrimSpace(sessionKey)
	if normalizedSessionKey == "" {
		return nil, ErrWorkingMemorySessionRequired
	}

	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, agent_id, session_key, content, metadata, created_at, expires_at
		 FROM working_memory
		 WHERE org_id = $1
		   AND agent_id = $2
		   AND session_key = $3
		   AND (expires_at IS NULL OR expires_at > NOW())
		 ORDER BY created_at DESC
		 LIMIT $4`,
		workspaceID,
		normalizedAgentID,
		normalizedSessionKey,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list working memory entries: %w", err)
	}
	defer rows.Close()

	entries := make([]WorkingMemoryEntry, 0)
	for rows.Next() {
		entry, scanErr := scanWorkingMemoryEntry(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan working memory entry: %w", scanErr)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate working memory entries: %w", err)
	}
	return entries, nil
}

func (s *WorkingMemoryStore) CleanupExpired(ctx context.Context, before time.Time) (int64, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return 0, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	result, err := conn.ExecContext(
		ctx,
		`DELETE FROM working_memory
		 WHERE org_id = $1
		   AND expires_at < $2`,
		workspaceID,
		before.UTC(),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired working memory entries: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to read cleanup result: %w", err)
	}
	return rowsAffected, nil
}

func scanWorkingMemoryEntry(scanner interface{ Scan(...any) error }) (WorkingMemoryEntry, error) {
	var entry WorkingMemoryEntry
	var metadataBytes []byte
	err := scanner.Scan(
		&entry.ID,
		&entry.OrgID,
		&entry.AgentID,
		&entry.SessionKey,
		&entry.Content,
		&metadataBytes,
		&entry.CreatedAt,
		&entry.ExpiresAt,
	)
	if err != nil {
		return WorkingMemoryEntry{}, err
	}
	if len(metadataBytes) == 0 {
		entry.Metadata = json.RawMessage(`{}`)
	} else {
		entry.Metadata = json.RawMessage(metadataBytes)
	}
	return entry, nil
}
