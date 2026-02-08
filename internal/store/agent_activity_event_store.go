package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

// AgentActivityEvent represents a persistent agent activity timeline event.
type AgentActivityEvent struct {
	ID          string     `json:"id"`
	OrgID       string     `json:"org_id"`
	AgentID     string     `json:"agent_id"`
	SessionKey  string     `json:"session_key,omitempty"`
	Trigger     string     `json:"trigger"`
	Channel     string     `json:"channel,omitempty"`
	Summary     string     `json:"summary"`
	Detail      string     `json:"detail,omitempty"`
	ProjectID   string     `json:"project_id,omitempty"`
	IssueID     string     `json:"issue_id,omitempty"`
	IssueNumber int        `json:"issue_number,omitempty"`
	ThreadID    string     `json:"thread_id,omitempty"`
	TokensUsed  int        `json:"tokens_used"`
	ModelUsed   string     `json:"model_used,omitempty"`
	DurationMs  int64      `json:"duration_ms"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// CreateAgentActivityEventInput defines required fields to insert one event.
type CreateAgentActivityEventInput struct {
	ID          string
	AgentID     string
	SessionKey  string
	Trigger     string
	Channel     string
	Summary     string
	Detail      string
	ProjectID   string
	IssueID     string
	IssueNumber int
	ThreadID    string
	TokensUsed  int
	ModelUsed   string
	DurationMs  int64
	Status      string
	StartedAt   time.Time
	CompletedAt *time.Time
}

// ListAgentActivityOptions controls filtering for timeline queries.
type ListAgentActivityOptions struct {
	Limit     int
	Before    *time.Time
	Trigger   string
	Channel   string
	Status    string
	ProjectID string
	AgentID   string
}

// AgentActivityEventStore provides workspace-isolated access to activity events.
type AgentActivityEventStore struct {
	db *sql.DB
}

const (
	defaultAgentActivityLimit = 50
	maxAgentActivityLimit     = 200
)

// NewAgentActivityEventStore creates a new AgentActivityEventStore.
func NewAgentActivityEventStore(db *sql.DB) *AgentActivityEventStore {
	return &AgentActivityEventStore{db: db}
}

const agentActivityEventSelectColumns = `
	id,
	org_id,
	agent_id,
	session_key,
	trigger,
	channel,
	summary,
	detail,
	project_id,
	issue_id,
	issue_number,
	thread_id,
	tokens_used,
	model_used,
	duration_ms,
	status,
	started_at,
	completed_at,
	created_at
`

// Create inserts one activity event in the current workspace context.
func (s *AgentActivityEventStore) Create(ctx context.Context, input CreateAgentActivityEventInput) (*AgentActivityEvent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `INSERT INTO agent_activity_events (
		id,
		org_id,
		agent_id,
		session_key,
		trigger,
		channel,
		summary,
		detail,
		project_id,
		issue_id,
		issue_number,
		thread_id,
		tokens_used,
		model_used,
		duration_ms,
		status,
		started_at,
		completed_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
	)
	RETURNING ` + agentActivityEventSelectColumns

	event, err := scanAgentActivityEvent(conn.QueryRowContext(
		ctx,
		query,
		strings.TrimSpace(input.ID),
		workspaceID,
		strings.TrimSpace(input.AgentID),
		nullIfEmpty(input.SessionKey),
		strings.TrimSpace(input.Trigger),
		nullIfEmpty(input.Channel),
		strings.TrimSpace(input.Summary),
		nullIfEmpty(input.Detail),
		nullIfEmpty(input.ProjectID),
		nullIfEmpty(input.IssueID),
		nullableInt(input.IssueNumber),
		nullIfEmpty(input.ThreadID),
		input.TokensUsed,
		nullIfEmpty(input.ModelUsed),
		input.DurationMs,
		nonEmptyOrDefault(input.Status, "completed"),
		input.StartedAt.UTC(),
		input.CompletedAt,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create agent activity event: %w", err)
	}

	return &event, nil
}

// ListByAgent returns timeline rows for one agent in newest-first order.
func (s *AgentActivityEventStore) ListByAgent(ctx context.Context, agentID string, opts ListAgentActivityOptions) ([]AgentActivityEvent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query, args := buildAgentActivityListQuery(workspaceID, strings.TrimSpace(agentID), opts, false)
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list agent activity events by agent: %w", err)
	}
	defer rows.Close()

	events := make([]AgentActivityEvent, 0)
	for rows.Next() {
		event, scanErr := scanAgentActivityEvent(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan agent activity event: %w", scanErr)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading agent activity events: %w", err)
	}
	return events, nil
}

// ListRecent returns recent activity events in org scope, optionally filtered by agent.
func (s *AgentActivityEventStore) ListRecent(ctx context.Context, opts ListAgentActivityOptions) ([]AgentActivityEvent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query, args := buildAgentActivityListQuery(workspaceID, "", opts, true)
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list recent agent activity events: %w", err)
	}
	defer rows.Close()

	events := make([]AgentActivityEvent, 0)
	for rows.Next() {
		event, scanErr := scanAgentActivityEvent(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan agent activity event: %w", scanErr)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading agent activity events: %w", err)
	}
	return events, nil
}

// CreateEvents inserts multiple activity events in one transaction.
func (s *AgentActivityEventStore) CreateEvents(ctx context.Context, inputs []CreateAgentActivityEventInput) error {
	if len(inputs) == 0 {
		return nil
	}

	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	query := `INSERT INTO agent_activity_events (
		id,
		org_id,
		agent_id,
		session_key,
		trigger,
		channel,
		summary,
		detail,
		project_id,
		issue_id,
		issue_number,
		thread_id,
		tokens_used,
		model_used,
		duration_ms,
		status,
		started_at,
		completed_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
	)`

	for _, input := range inputs {
		_, execErr := tx.ExecContext(
			ctx,
			query,
			strings.TrimSpace(input.ID),
			workspaceID,
			strings.TrimSpace(input.AgentID),
			nullIfEmpty(input.SessionKey),
			strings.TrimSpace(input.Trigger),
			nullIfEmpty(input.Channel),
			strings.TrimSpace(input.Summary),
			nullIfEmpty(input.Detail),
			nullIfEmpty(input.ProjectID),
			nullIfEmpty(input.IssueID),
			nullableInt(input.IssueNumber),
			nullIfEmpty(input.ThreadID),
			input.TokensUsed,
			nullIfEmpty(input.ModelUsed),
			input.DurationMs,
			nonEmptyOrDefault(input.Status, "completed"),
			input.StartedAt.UTC(),
			input.CompletedAt,
		)
		if execErr != nil {
			return fmt.Errorf("failed to batch create agent activity events: %w", execErr)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit activity event batch: %w", err)
	}
	return nil
}

// LatestByAgent returns the newest event for each agent in the current workspace.
func (s *AgentActivityEventStore) LatestByAgent(ctx context.Context) (map[string]AgentActivityEvent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `SELECT DISTINCT ON (agent_id) ` + agentActivityEventSelectColumns + `
		FROM agent_activity_events
		WHERE org_id = $1
		ORDER BY agent_id, started_at DESC, id DESC`

	rows, err := conn.QueryContext(ctx, query, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to list latest activity by agent: %w", err)
	}
	defer rows.Close()

	out := make(map[string]AgentActivityEvent)
	for rows.Next() {
		event, scanErr := scanAgentActivityEvent(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan latest activity event: %w", scanErr)
		}
		out[event.AgentID] = event
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading latest activity events: %w", err)
	}
	return out, nil
}

// CountByAgentSince returns event count for one agent since a cutoff timestamp.
func (s *AgentActivityEventStore) CountByAgentSince(ctx context.Context, agentID string, since time.Time) (int, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return 0, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	var count int
	err = conn.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
			FROM agent_activity_events
			WHERE org_id = $1 AND agent_id = $2 AND started_at >= $3`,
		workspaceID,
		strings.TrimSpace(agentID),
		since.UTC(),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count agent activity events: %w", err)
	}
	return count, nil
}

// CleanupOlderThan deletes rows before the provided timestamp in current workspace scope.
func (s *AgentActivityEventStore) CleanupOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
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
		`DELETE FROM agent_activity_events
			WHERE org_id = $1 AND started_at < $2`,
		workspaceID,
		olderThan.UTC(),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old agent activity events: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to read cleanup row count: %w", err)
	}
	return rows, nil
}

func buildAgentActivityListQuery(workspaceID, agentID string, opts ListAgentActivityOptions, allowAgentFilter bool) (string, []interface{}) {
	conditions := []string{"org_id = $1"}
	args := []interface{}{workspaceID}

	if agentID != "" {
		args = append(args, agentID)
		conditions = append(conditions, fmt.Sprintf("agent_id = $%d", len(args)))
	}
	if allowAgentFilter {
		filterAgent := strings.TrimSpace(opts.AgentID)
		if filterAgent != "" {
			args = append(args, filterAgent)
			conditions = append(conditions, fmt.Sprintf("agent_id = $%d", len(args)))
		}
	}
	if opts.Before != nil && !opts.Before.IsZero() {
		args = append(args, opts.Before.UTC())
		conditions = append(conditions, fmt.Sprintf("started_at < $%d", len(args)))
	}
	if trigger := strings.TrimSpace(opts.Trigger); trigger != "" {
		args = append(args, trigger)
		conditions = append(conditions, fmt.Sprintf("trigger = $%d", len(args)))
	}
	if channel := strings.TrimSpace(opts.Channel); channel != "" {
		args = append(args, channel)
		conditions = append(conditions, fmt.Sprintf("channel = $%d", len(args)))
	}
	if status := strings.TrimSpace(opts.Status); status != "" {
		args = append(args, status)
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(args)))
	}
	if projectID := strings.TrimSpace(opts.ProjectID); projectID != "" {
		args = append(args, projectID)
		conditions = append(conditions, fmt.Sprintf("project_id = $%d", len(args)))
	}

	limit := normalizeAgentActivityLimit(opts.Limit)
	args = append(args, limit)
	limitPos := len(args)

	query := "SELECT " + agentActivityEventSelectColumns + " FROM agent_activity_events WHERE " +
		strings.Join(conditions, " AND ") +
		fmt.Sprintf(" ORDER BY started_at DESC, id DESC LIMIT $%d", limitPos)
	return query, args
}

func scanAgentActivityEvent(scanner interface{ Scan(...any) error }) (AgentActivityEvent, error) {
	var out AgentActivityEvent
	var sessionKey sql.NullString
	var channel sql.NullString
	var detail sql.NullString
	var projectID sql.NullString
	var issueID sql.NullString
	var issueNumber sql.NullInt64
	var threadID sql.NullString
	var modelUsed sql.NullString
	var completedAt sql.NullTime

	err := scanner.Scan(
		&out.ID,
		&out.OrgID,
		&out.AgentID,
		&sessionKey,
		&out.Trigger,
		&channel,
		&out.Summary,
		&detail,
		&projectID,
		&issueID,
		&issueNumber,
		&threadID,
		&out.TokensUsed,
		&modelUsed,
		&out.DurationMs,
		&out.Status,
		&out.StartedAt,
		&completedAt,
		&out.CreatedAt,
	)
	if err != nil {
		return out, err
	}

	if sessionKey.Valid {
		out.SessionKey = sessionKey.String
	}
	if channel.Valid {
		out.Channel = channel.String
	}
	if detail.Valid {
		out.Detail = detail.String
	}
	if projectID.Valid {
		out.ProjectID = projectID.String
	}
	if issueID.Valid {
		out.IssueID = issueID.String
	}
	if issueNumber.Valid {
		out.IssueNumber = int(issueNumber.Int64)
	}
	if threadID.Valid {
		out.ThreadID = threadID.String
	}
	if modelUsed.Valid {
		out.ModelUsed = modelUsed.String
	}
	if completedAt.Valid {
		completed := completedAt.Time.UTC()
		out.CompletedAt = &completed
	}
	out.StartedAt = out.StartedAt.UTC()
	out.CreatedAt = out.CreatedAt.UTC()
	return out, nil
}

func normalizeAgentActivityLimit(limit int) int {
	if limit <= 0 {
		return defaultAgentActivityLimit
	}
	if limit > maxAgentActivityLimit {
		return maxAgentActivityLimit
	}
	return limit
}

func nullableInt(value int) interface{} {
	if value <= 0 {
		return nil
	}
	return value
}

func nullIfEmpty(value string) interface{} {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func nonEmptyOrDefault(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}
