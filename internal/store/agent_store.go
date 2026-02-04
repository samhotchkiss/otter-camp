package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

// Agent represents an agent entity.
type Agent struct {
	ID             string    `json:"id"`
	OrgID          string    `json:"org_id"`
	Slug           string    `json:"slug"`
	DisplayName    string    `json:"display_name"`
	AvatarURL      *string   `json:"avatar_url,omitempty"`
	WebhookURL     *string   `json:"webhook_url,omitempty"`
	Status         string    `json:"status"`
	SessionPattern *string   `json:"session_pattern,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// AgentStore provides workspace-isolated access to agents.
type AgentStore struct {
	db *sql.DB
}

// NewAgentStore creates a new AgentStore with the given database connection.
func NewAgentStore(db *sql.DB) *AgentStore {
	return &AgentStore{db: db}
}

const agentSelectColumns = "id, org_id, slug, display_name, avatar_url, webhook_url, status, session_pattern, created_at, updated_at"

// GetByID retrieves an agent by ID within the current workspace.
func (s *AgentStore) GetByID(ctx context.Context, id string) (*Agent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := "SELECT " + agentSelectColumns + " FROM agents WHERE id = $1"
	agent, err := scanAgent(conn.QueryRowContext(ctx, query, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get agent: %w", err)
	}

	// Defense in depth
	if agent.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	return &agent, nil
}

// GetBySlug retrieves an agent by slug within the current workspace.
func (s *AgentStore) GetBySlug(ctx context.Context, slug string) (*Agent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := "SELECT " + agentSelectColumns + " FROM agents WHERE org_id = $1 AND slug = $2"
	agent, err := scanAgent(conn.QueryRowContext(ctx, query, workspaceID, slug))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get agent by slug: %w", err)
	}

	return &agent, nil
}

// GetBySessionPattern finds an agent matching the given session string.
// This is used for agent identification from session patterns.
func (s *AgentStore) GetBySessionPattern(ctx context.Context, session string) (*Agent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `SELECT ` + agentSelectColumns + ` FROM agents 
		WHERE org_id = $1 AND session_pattern IS NOT NULL AND $2 LIKE session_pattern
		ORDER BY created_at DESC LIMIT 1`

	agent, err := scanAgent(conn.QueryRowContext(ctx, query, workspaceID, session))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get agent by session: %w", err)
	}

	return &agent, nil
}

// List retrieves all agents in the current workspace.
func (s *AgentStore) List(ctx context.Context) ([]Agent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := "SELECT " + agentSelectColumns + " FROM agents WHERE org_id = $1 ORDER BY created_at DESC"
	rows, err := conn.QueryContext(ctx, query, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to list agents: %w", err)
	}
	defer rows.Close()

	agents := make([]Agent, 0)
	for rows.Next() {
		agent, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan agent: %w", err)
		}
		agents = append(agents, agent)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading agents: %w", err)
	}

	return agents, nil
}

// CreateAgentInput defines the input for creating a new agent.
type CreateAgentInput struct {
	Slug           string
	DisplayName    string
	AvatarURL      *string
	WebhookURL     *string
	Status         string
	SessionPattern *string
}

// Create creates a new agent in the current workspace.
func (s *AgentStore) Create(ctx context.Context, input CreateAgentInput) (*Agent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `INSERT INTO agents (
		org_id, slug, display_name, avatar_url, webhook_url, status, session_pattern
	) VALUES ($1, $2, $3, $4, $5, $6, $7)
	RETURNING ` + agentSelectColumns

	args := []interface{}{
		workspaceID,
		input.Slug,
		input.DisplayName,
		nullableString(input.AvatarURL),
		nullableString(input.WebhookURL),
		input.Status,
		nullableString(input.SessionPattern),
	}

	agent, err := scanAgent(conn.QueryRowContext(ctx, query, args...))
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	return &agent, nil
}

// UpdateAgentInput defines the input for updating an agent.
type UpdateAgentInput struct {
	Slug           string
	DisplayName    string
	AvatarURL      *string
	WebhookURL     *string
	Status         string
	SessionPattern *string
}

// Update updates an agent in the current workspace.
func (s *AgentStore) Update(ctx context.Context, id string, input UpdateAgentInput) (*Agent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `UPDATE agents SET
		slug = $1, display_name = $2, avatar_url = $3, webhook_url = $4, status = $5, session_pattern = $6
	WHERE id = $7 AND org_id = $8
	RETURNING ` + agentSelectColumns

	args := []interface{}{
		input.Slug,
		input.DisplayName,
		nullableString(input.AvatarURL),
		nullableString(input.WebhookURL),
		input.Status,
		nullableString(input.SessionPattern),
		id,
		workspaceID,
	}

	agent, err := scanAgent(conn.QueryRowContext(ctx, query, args...))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to update agent: %w", err)
	}

	return &agent, nil
}

// Delete deletes an agent from the current workspace.
func (s *AgentStore) Delete(ctx context.Context, id string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	result, err := conn.ExecContext(ctx, "DELETE FROM agents WHERE id = $1 AND org_id = $2", id, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to delete agent: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check delete result: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}

	return nil
}

func scanAgent(scanner interface{ Scan(...any) error }) (Agent, error) {
	var agent Agent
	var avatarURL sql.NullString
	var webhookURL sql.NullString
	var sessionPattern sql.NullString

	err := scanner.Scan(
		&agent.ID,
		&agent.OrgID,
		&agent.Slug,
		&agent.DisplayName,
		&avatarURL,
		&webhookURL,
		&agent.Status,
		&sessionPattern,
		&agent.CreatedAt,
		&agent.UpdatedAt,
	)
	if err != nil {
		return agent, err
	}

	if avatarURL.Valid {
		agent.AvatarURL = &avatarURL.String
	}
	if webhookURL.Valid {
		agent.WebhookURL = &webhookURL.String
	}
	if sessionPattern.Valid {
		agent.SessionPattern = &sessionPattern.String
	}

	return agent, nil
}
