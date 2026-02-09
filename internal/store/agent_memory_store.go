package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const (
	AgentMemoryKindDaily    = "daily"
	AgentMemoryKindLongTerm = "long_term"
	AgentMemoryKindNote     = "note"
)

type AgentMemory struct {
	ID        string     `json:"id"`
	OrgID     string     `json:"org_id"`
	AgentID   string     `json:"agent_id"`
	Kind      string     `json:"kind"`
	Date      *time.Time `json:"date,omitempty"`
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type CreateAgentMemoryInput struct {
	AgentID string
	Kind    string
	Date    *time.Time
	Content string
}

type AgentMemoryStore struct {
	db *sql.DB
}

func NewAgentMemoryStore(db *sql.DB) *AgentMemoryStore {
	return &AgentMemoryStore{db: db}
}

func (s *AgentMemoryStore) Create(ctx context.Context, input CreateAgentMemoryInput) (*AgentMemory, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	if !uuidRegex.MatchString(strings.TrimSpace(input.AgentID)) {
		return nil, fmt.Errorf("invalid agent_id")
	}

	kind, err := normalizeAgentMemoryKind(input.Kind)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return nil, fmt.Errorf("content is required")
	}

	var dateValue interface{}
	if kind == AgentMemoryKindDaily {
		if input.Date != nil {
			dateValue = input.Date.UTC().Format("2006-01-02")
		} else {
			dateValue = time.Now().UTC().Format("2006-01-02")
		}
	} else if input.Date != nil {
		dateValue = input.Date.UTC().Format("2006-01-02")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	record, err := scanAgentMemory(conn.QueryRowContext(
		ctx,
		`INSERT INTO agent_memories (org_id, agent_id, kind, date, content)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, org_id, agent_id, kind, date, content, created_at, updated_at`,
		workspaceID,
		strings.TrimSpace(input.AgentID),
		kind,
		dateValue,
		content,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create agent memory: %w", err)
	}
	return &record, nil
}

func (s *AgentMemoryStore) ListByAgent(
	ctx context.Context,
	agentID string,
	days int,
	includeLongTerm bool,
) ([]AgentMemory, []AgentMemory, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, nil, ErrNoWorkspace
	}
	if !uuidRegex.MatchString(strings.TrimSpace(agentID)) {
		return nil, nil, fmt.Errorf("invalid agent_id")
	}
	if days <= 0 {
		days = 2
	}
	if days > 30 {
		days = 30
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()

	dailyRows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, agent_id, kind, date, content, created_at, updated_at
		 FROM agent_memories
		 WHERE org_id = $1
		   AND agent_id = $2
		   AND kind = $3
		   AND (date IS NULL OR date >= (CURRENT_DATE - ($4::int - 1)))
		 ORDER BY COALESCE(date, created_at::date) DESC, created_at DESC`,
		workspaceID,
		strings.TrimSpace(agentID),
		AgentMemoryKindDaily,
		days,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list daily memories: %w", err)
	}
	defer dailyRows.Close()

	daily := make([]AgentMemory, 0)
	for dailyRows.Next() {
		record, scanErr := scanAgentMemory(dailyRows)
		if scanErr != nil {
			return nil, nil, fmt.Errorf("failed to scan daily memory: %w", scanErr)
		}
		daily = append(daily, record)
	}
	if err := dailyRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("failed to read daily memories: %w", err)
	}

	if !includeLongTerm {
		return daily, nil, nil
	}

	longTermRows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, agent_id, kind, date, content, created_at, updated_at
		 FROM agent_memories
		 WHERE org_id = $1
		   AND agent_id = $2
		   AND kind = $3
		 ORDER BY updated_at DESC, created_at DESC
		 LIMIT 20`,
		workspaceID,
		strings.TrimSpace(agentID),
		AgentMemoryKindLongTerm,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list long-term memories: %w", err)
	}
	defer longTermRows.Close()

	longTerm := make([]AgentMemory, 0)
	for longTermRows.Next() {
		record, scanErr := scanAgentMemory(longTermRows)
		if scanErr != nil {
			return nil, nil, fmt.Errorf("failed to scan long-term memory: %w", scanErr)
		}
		longTerm = append(longTerm, record)
	}
	if err := longTermRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("failed to read long-term memories: %w", err)
	}

	return daily, longTerm, nil
}

func (s *AgentMemoryStore) SearchByAgent(
	ctx context.Context,
	agentID string,
	query string,
	limit int,
) ([]AgentMemory, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	if !uuidRegex.MatchString(strings.TrimSpace(agentID)) {
		return nil, fmt.Errorf("invalid agent_id")
	}

	term := strings.TrimSpace(query)
	if term == "" {
		return nil, fmt.Errorf("query is required")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, agent_id, kind, date, content, created_at, updated_at
		 FROM agent_memories
		 WHERE org_id = $1
		   AND agent_id = $2
		   AND content ILIKE '%' || $3 || '%'
		 ORDER BY updated_at DESC, created_at DESC
		 LIMIT $4`,
		workspaceID,
		strings.TrimSpace(agentID),
		term,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to search agent memories: %w", err)
	}
	defer rows.Close()

	results := make([]AgentMemory, 0)
	for rows.Next() {
		record, scanErr := scanAgentMemory(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan searched memory: %w", scanErr)
		}
		results = append(results, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read searched memories: %w", err)
	}

	return results, nil
}

func normalizeAgentMemoryKind(value string) (string, error) {
	kind := strings.TrimSpace(strings.ToLower(value))
	switch kind {
	case AgentMemoryKindDaily, AgentMemoryKindLongTerm, AgentMemoryKindNote:
		return kind, nil
	default:
		return "", fmt.Errorf("kind must be daily, long_term, or note")
	}
}

func scanAgentMemory(scanner interface{ Scan(...any) error }) (AgentMemory, error) {
	var (
		record   AgentMemory
		dateNull sql.NullTime
	)
	err := scanner.Scan(
		&record.ID,
		&record.OrgID,
		&record.AgentID,
		&record.Kind,
		&dateNull,
		&record.Content,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return AgentMemory{}, err
	}
	if dateNull.Valid {
		dateValue := dateNull.Time.UTC()
		record.Date = &dateValue
	}
	return record, nil
}
