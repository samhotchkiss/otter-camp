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
	PipelineStepTypeAgentWork   = "agent_work"
	PipelineStepTypeAgentReview = "agent_review"
	PipelineStepTypeHumanReview = "human_review"

	IssuePipelineResultCompleted = "completed"
	IssuePipelineResultRejected  = "rejected"
	IssuePipelineResultSkipped   = "skipped"
)

type PipelineStep struct {
	ID              string     `json:"id"`
	OrgID           string     `json:"org_id"`
	ProjectID       string     `json:"project_id"`
	StepNumber      int        `json:"step_number"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	AssignedAgentID *string    `json:"assigned_agent_id,omitempty"`
	StepType        string     `json:"step_type"`
	AutoAdvance     bool       `json:"auto_advance"`
	CreatedAt       *time.Time `json:"created_at,omitempty"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
}

type CreatePipelineStepInput struct {
	ProjectID       string
	StepNumber      int
	Name            string
	Description     string
	AssignedAgentID *string
	StepType        string
	AutoAdvance     bool
}

type UpdatePipelineStepInput struct {
	StepID string

	SetStepNumber bool
	StepNumber    int

	SetName bool
	Name    string

	SetDescription bool
	Description    string

	SetAssignedAgentID bool
	AssignedAgentID    *string

	SetStepType bool
	StepType    string

	SetAutoAdvance bool
	AutoAdvance    bool
}

type PipelineStepStaffingAssignment struct {
	StepID          string
	AssignedAgentID *string
}

type CreateIssuePipelineHistoryInput struct {
	IssueID     string
	StepID      string
	AgentID     *string
	StartedAt   time.Time
	CompletedAt *time.Time
	Result      string
	Notes       string
}

type IssuePipelineHistoryEntry struct {
	ID          string     `json:"id"`
	OrgID       string     `json:"org_id"`
	IssueID     string     `json:"issue_id"`
	StepID      string     `json:"step_id"`
	AgentID     *string    `json:"agent_id,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Result      string     `json:"result"`
	Notes       string     `json:"notes"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

type UpdateIssuePipelineStateInput struct {
	IssueID               string
	CurrentPipelineStepID *string
	PipelineStartedAt     *time.Time
	PipelineCompletedAt   *time.Time
}

type IssuePipelineState struct {
	IssueID               string     `json:"issue_id"`
	ProjectID             string     `json:"project_id"`
	CurrentPipelineStepID *string    `json:"current_pipeline_step_id,omitempty"`
	PipelineStartedAt     *time.Time `json:"pipeline_started_at,omitempty"`
	PipelineCompletedAt   *time.Time `json:"pipeline_completed_at,omitempty"`
}

type PipelineStepStore struct {
	db *sql.DB
}

func NewPipelineStepStore(db *sql.DB) *PipelineStepStore {
	return &PipelineStepStore{db: db}
}

func normalizePipelineStepType(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func isValidPipelineStepType(stepType string) bool {
	switch normalizePipelineStepType(stepType) {
	case PipelineStepTypeAgentWork, PipelineStepTypeAgentReview, PipelineStepTypeHumanReview:
		return true
	default:
		return false
	}
}

func normalizeIssuePipelineResult(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func isValidIssuePipelineResult(result string) bool {
	switch normalizeIssuePipelineResult(result) {
	case IssuePipelineResultCompleted, IssuePipelineResultRejected, IssuePipelineResultSkipped:
		return true
	default:
		return false
	}
}

func normalizeCreatePipelineStepInput(input CreatePipelineStepInput) (CreatePipelineStepInput, error) {
	projectID := strings.TrimSpace(input.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return CreatePipelineStepInput{}, fmt.Errorf("%w: invalid project_id", ErrValidation)
	}
	if input.StepNumber <= 0 {
		return CreatePipelineStepInput{}, fmt.Errorf("%w: step_number must be greater than zero", ErrValidation)
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return CreatePipelineStepInput{}, fmt.Errorf("%w: name is required", ErrValidation)
	}
	stepType := normalizePipelineStepType(input.StepType)
	if !isValidPipelineStepType(stepType) {
		return CreatePipelineStepInput{}, fmt.Errorf("%w: invalid step_type", ErrValidation)
	}

	var assignedAgentID *string
	if input.AssignedAgentID != nil {
		trimmed := strings.TrimSpace(*input.AssignedAgentID)
		if trimmed != "" {
			if !uuidRegex.MatchString(trimmed) {
				return CreatePipelineStepInput{}, fmt.Errorf("%w: invalid assigned_agent_id", ErrValidation)
			}
			assignedAgentID = &trimmed
		}
	}

	return CreatePipelineStepInput{
		ProjectID:       projectID,
		StepNumber:      input.StepNumber,
		Name:            name,
		Description:     strings.TrimSpace(input.Description),
		AssignedAgentID: assignedAgentID,
		StepType:        stepType,
		AutoAdvance:     input.AutoAdvance,
	}, nil
}

func (s *PipelineStepStore) CreateStep(ctx context.Context, input CreatePipelineStepInput) (*PipelineStep, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	normalized, err := normalizeCreatePipelineStepInput(input)
	if err != nil {
		return nil, err
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := ensurePipelineProjectVisible(ctx, tx, normalized.ProjectID); err != nil {
		return nil, err
	}
	if normalized.AssignedAgentID != nil {
		if err := ensurePipelineAgentVisible(ctx, tx, *normalized.AssignedAgentID); err != nil {
			return nil, err
		}
	}

	record, err := scanPipelineStep(tx.QueryRowContext(
		ctx,
		`INSERT INTO pipeline_steps (
			org_id, project_id, step_number, name, description, assigned_agent_id, step_type, auto_advance
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, org_id, project_id, step_number, name, description, assigned_agent_id, step_type, auto_advance, created_at, updated_at`,
		workspaceID,
		normalized.ProjectID,
		normalized.StepNumber,
		normalized.Name,
		normalized.Description,
		nullableString(normalized.AssignedAgentID),
		normalized.StepType,
		normalized.AutoAdvance,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create pipeline step: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *PipelineStepStore) ListStepsByProject(ctx context.Context, projectID string) ([]PipelineStep, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("%w: invalid project_id", ErrValidation)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := ensurePipelineProjectVisible(ctx, conn, projectID); err != nil {
		return nil, err
	}

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, project_id, step_number, name, description, assigned_agent_id, step_type, auto_advance, created_at, updated_at
		 FROM pipeline_steps
		 WHERE org_id = $1 AND project_id = $2
		 ORDER BY step_number ASC, created_at ASC`,
		workspaceID,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list pipeline steps: %w", err)
	}
	defer rows.Close()

	out := make([]PipelineStep, 0)
	for rows.Next() {
		step, scanErr := scanPipelineStep(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan pipeline step: %w", scanErr)
		}
		out = append(out, step)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading pipeline steps: %w", err)
	}
	return out, nil
}

func (s *PipelineStepStore) ReorderSteps(ctx context.Context, projectID string, orderedStepIDs []string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}
	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return fmt.Errorf("%w: invalid project_id", ErrValidation)
	}
	if len(orderedStepIDs) == 0 {
		return fmt.Errorf("%w: ordered step ids are required", ErrValidation)
	}

	normalizedIDs := make([]string, 0, len(orderedStepIDs))
	seen := make(map[string]struct{}, len(orderedStepIDs))
	for _, raw := range orderedStepIDs {
		id := strings.TrimSpace(raw)
		if !uuidRegex.MatchString(id) {
			return fmt.Errorf("%w: invalid step id in reorder list", ErrValidation)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("%w: duplicate step id in reorder list", ErrValidation)
		}
		seen[id] = struct{}{}
		normalizedIDs = append(normalizedIDs, id)
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := ensurePipelineProjectVisible(ctx, tx, projectID); err != nil {
		return err
	}

	rows, err := tx.QueryContext(
		ctx,
		`SELECT id FROM pipeline_steps WHERE org_id = $1 AND project_id = $2`,
		workspaceID,
		projectID,
	)
	if err != nil {
		return fmt.Errorf("failed to read existing pipeline steps: %w", err)
	}
	defer rows.Close()

	existing := make(map[string]struct{})
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return fmt.Errorf("failed to scan pipeline step id: %w", scanErr)
		}
		existing[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed reading pipeline step ids: %w", err)
	}

	if len(existing) != len(normalizedIDs) {
		return fmt.Errorf("%w: reorder list must include all project step ids", ErrValidation)
	}
	for _, id := range normalizedIDs {
		if _, ok := existing[id]; !ok {
			return fmt.Errorf("%w: reorder list includes step outside project", ErrValidation)
		}
	}

	for index, id := range normalizedIDs {
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE pipeline_steps
			 SET step_number = $1
			 WHERE org_id = $2 AND project_id = $3 AND id = $4`,
			index+1,
			workspaceID,
			projectID,
			id,
		); err != nil {
			return fmt.Errorf("failed to reorder pipeline steps: %w", err)
		}
	}

	return tx.Commit()
}

func (s *PipelineStepStore) UpdateStep(ctx context.Context, input UpdatePipelineStepInput) (*PipelineStep, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	stepID := strings.TrimSpace(input.StepID)
	if !uuidRegex.MatchString(stepID) {
		return nil, fmt.Errorf("%w: invalid step_id", ErrValidation)
	}

	assignments := []string{}
	args := []any{}
	argPosition := 1

	if input.SetStepNumber {
		if input.StepNumber <= 0 {
			return nil, fmt.Errorf("%w: step_number must be greater than zero", ErrValidation)
		}
		assignments = append(assignments, fmt.Sprintf("step_number = $%d", argPosition))
		args = append(args, input.StepNumber)
		argPosition++
	}
	if input.SetName {
		trimmed := strings.TrimSpace(input.Name)
		if trimmed == "" {
			return nil, fmt.Errorf("%w: name is required", ErrValidation)
		}
		assignments = append(assignments, fmt.Sprintf("name = $%d", argPosition))
		args = append(args, trimmed)
		argPosition++
	}
	if input.SetDescription {
		assignments = append(assignments, fmt.Sprintf("description = $%d", argPosition))
		args = append(args, strings.TrimSpace(input.Description))
		argPosition++
	}
	if input.SetAssignedAgentID {
		var normalized *string
		if input.AssignedAgentID != nil {
			trimmed := strings.TrimSpace(*input.AssignedAgentID)
			if trimmed != "" {
				if !uuidRegex.MatchString(trimmed) {
					return nil, fmt.Errorf("%w: invalid assigned_agent_id", ErrValidation)
				}
				normalized = &trimmed
			}
		}
		input.AssignedAgentID = normalized
		assignments = append(assignments, fmt.Sprintf("assigned_agent_id = $%d", argPosition))
		args = append(args, nullableString(input.AssignedAgentID))
		argPosition++
	}
	if input.SetStepType {
		stepType := normalizePipelineStepType(input.StepType)
		if !isValidPipelineStepType(stepType) {
			return nil, fmt.Errorf("%w: invalid step_type", ErrValidation)
		}
		assignments = append(assignments, fmt.Sprintf("step_type = $%d", argPosition))
		args = append(args, stepType)
		argPosition++
	}
	if input.SetAutoAdvance {
		assignments = append(assignments, fmt.Sprintf("auto_advance = $%d", argPosition))
		args = append(args, input.AutoAdvance)
		argPosition++
	}

	if len(assignments) == 0 {
		return nil, fmt.Errorf("%w: no fields to update", ErrValidation)
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if input.SetAssignedAgentID && input.AssignedAgentID != nil {
		if err := ensurePipelineAgentVisible(ctx, tx, *input.AssignedAgentID); err != nil {
			return nil, err
		}
	}
	if err := ensurePipelineStepVisible(ctx, tx, stepID); err != nil {
		return nil, err
	}

	whereStepArg := argPosition
	whereOrgArg := argPosition + 1
	args = append(args, stepID, workspaceID)

	query := fmt.Sprintf(
		`UPDATE pipeline_steps
		 SET %s
		 WHERE id = $%d AND org_id = $%d
		 RETURNING id, org_id, project_id, step_number, name, description, assigned_agent_id, step_type, auto_advance, created_at, updated_at`,
		strings.Join(assignments, ", "),
		whereStepArg,
		whereOrgArg,
	)

	record, err := scanPipelineStep(tx.QueryRowContext(ctx, query, args...))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to update pipeline step: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *PipelineStepStore) DeleteStep(ctx context.Context, stepID string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}
	stepID = strings.TrimSpace(stepID)
	if !uuidRegex.MatchString(stepID) {
		return fmt.Errorf("%w: invalid step_id", ErrValidation)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	result, err := conn.ExecContext(
		ctx,
		`DELETE FROM pipeline_steps WHERE id = $1 AND org_id = $2`,
		stepID,
		workspaceID,
	)
	if err != nil {
		return fmt.Errorf("failed to delete pipeline step: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read pipeline step delete result: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PipelineStepStore) ApplyStaffingPlan(ctx context.Context, projectID string, assignments []PipelineStepStaffingAssignment) ([]PipelineStep, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("%w: invalid project_id", ErrValidation)
	}
	if len(assignments) == 0 {
		return nil, fmt.Errorf("%w: assignments are required", ErrValidation)
	}

	normalized := make([]PipelineStepStaffingAssignment, 0, len(assignments))
	seen := make(map[string]struct{}, len(assignments))
	for _, assignment := range assignments {
		stepID := strings.TrimSpace(assignment.StepID)
		if !uuidRegex.MatchString(stepID) {
			return nil, fmt.Errorf("%w: invalid step_id", ErrValidation)
		}
		if _, exists := seen[stepID]; exists {
			return nil, fmt.Errorf("%w: duplicate step_id in staffing plan", ErrValidation)
		}
		seen[stepID] = struct{}{}

		var assignedAgentID *string
		if assignment.AssignedAgentID != nil {
			trimmed := strings.TrimSpace(*assignment.AssignedAgentID)
			if trimmed != "" {
				if !uuidRegex.MatchString(trimmed) {
					return nil, fmt.Errorf("%w: invalid assigned_agent_id", ErrValidation)
				}
				assignedAgentID = &trimmed
			}
		}

		normalized = append(normalized, PipelineStepStaffingAssignment{
			StepID:          stepID,
			AssignedAgentID: assignedAgentID,
		})
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := ensurePipelineProjectVisible(ctx, tx, projectID); err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(
		ctx,
		`SELECT id FROM pipeline_steps WHERE org_id = $1 AND project_id = $2`,
		workspaceID,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to read existing pipeline steps: %w", err)
	}
	defer rows.Close()

	existingStepIDs := make(map[string]struct{})
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, fmt.Errorf("failed to scan existing pipeline step: %w", scanErr)
		}
		existingStepIDs[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading existing pipeline steps: %w", err)
	}

	for _, assignment := range normalized {
		if _, ok := existingStepIDs[assignment.StepID]; !ok {
			return nil, ErrNotFound
		}
		if assignment.AssignedAgentID != nil {
			if err := ensurePipelineAgentVisible(ctx, tx, *assignment.AssignedAgentID); err != nil {
				return nil, err
			}
		}
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE pipeline_steps
			 SET assigned_agent_id = $1
			 WHERE org_id = $2 AND project_id = $3 AND id = $4`,
			nullableString(assignment.AssignedAgentID),
			workspaceID,
			projectID,
			assignment.StepID,
		); err != nil {
			return nil, fmt.Errorf("failed to apply staffing assignment: %w", err)
		}
	}

	rows, err = tx.QueryContext(
		ctx,
		`SELECT id, org_id, project_id, step_number, name, description, assigned_agent_id, step_type, auto_advance, created_at, updated_at
		 FROM pipeline_steps
		 WHERE org_id = $1 AND project_id = $2
		 ORDER BY step_number ASC, created_at ASC`,
		workspaceID,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load staffed pipeline steps: %w", err)
	}
	defer rows.Close()

	updated := make([]PipelineStep, 0)
	for rows.Next() {
		step, scanErr := scanPipelineStep(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan staffed pipeline step: %w", scanErr)
		}
		updated = append(updated, step)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading staffed pipeline steps: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *PipelineStepStore) AppendIssuePipelineHistory(ctx context.Context, input CreateIssuePipelineHistoryInput) (*IssuePipelineHistoryEntry, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID := strings.TrimSpace(input.IssueID)
	stepID := strings.TrimSpace(input.StepID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("%w: invalid issue_id", ErrValidation)
	}
	if !uuidRegex.MatchString(stepID) {
		return nil, fmt.Errorf("%w: invalid step_id", ErrValidation)
	}
	result := normalizeIssuePipelineResult(input.Result)
	if !isValidIssuePipelineResult(result) {
		return nil, fmt.Errorf("%w: invalid result", ErrValidation)
	}
	if input.StartedAt.IsZero() {
		return nil, fmt.Errorf("%w: started_at is required", ErrValidation)
	}

	var agentID *string
	if input.AgentID != nil {
		trimmed := strings.TrimSpace(*input.AgentID)
		if trimmed != "" {
			if !uuidRegex.MatchString(trimmed) {
				return nil, fmt.Errorf("%w: invalid agent_id", ErrValidation)
			}
			agentID = &trimmed
		}
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := ensurePipelineIssueVisible(ctx, tx, issueID); err != nil {
		return nil, err
	}
	if err := ensurePipelineStepVisible(ctx, tx, stepID); err != nil {
		return nil, err
	}
	if agentID != nil {
		if err := ensurePipelineAgentVisible(ctx, tx, *agentID); err != nil {
			return nil, err
		}
	}

	record, err := scanIssuePipelineHistoryEntry(tx.QueryRowContext(
		ctx,
		`INSERT INTO issue_pipeline_history (
			org_id, issue_id, step_id, agent_id, started_at, completed_at, result, notes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, org_id, issue_id, step_id, agent_id, started_at, completed_at, result, notes, created_at, updated_at`,
		workspaceID,
		issueID,
		stepID,
		nullableString(agentID),
		input.StartedAt.UTC(),
		nullableTime(input.CompletedAt),
		result,
		strings.TrimSpace(input.Notes),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to append issue pipeline history: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *PipelineStepStore) ListIssuePipelineHistory(ctx context.Context, issueID string) ([]IssuePipelineHistoryEntry, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("%w: invalid issue_id", ErrValidation)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := ensurePipelineIssueVisible(ctx, conn, issueID); err != nil {
		return nil, err
	}

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, issue_id, step_id, agent_id, started_at, completed_at, result, notes, created_at, updated_at
		 FROM issue_pipeline_history
		 WHERE org_id = $1 AND issue_id = $2
		 ORDER BY started_at ASC, created_at ASC`,
		workspaceID,
		issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list issue pipeline history: %w", err)
	}
	defer rows.Close()

	out := make([]IssuePipelineHistoryEntry, 0)
	for rows.Next() {
		item, scanErr := scanIssuePipelineHistoryEntry(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan issue pipeline history: %w", scanErr)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading issue pipeline history: %w", err)
	}
	return out, nil
}

func (s *PipelineStepStore) UpdateIssuePipelineState(ctx context.Context, input UpdateIssuePipelineStateInput) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	issueID := strings.TrimSpace(input.IssueID)
	if !uuidRegex.MatchString(issueID) {
		return fmt.Errorf("%w: invalid issue_id", ErrValidation)
	}

	var stepID *string
	if input.CurrentPipelineStepID != nil {
		trimmed := strings.TrimSpace(*input.CurrentPipelineStepID)
		if trimmed != "" {
			if !uuidRegex.MatchString(trimmed) {
				return fmt.Errorf("%w: invalid current_pipeline_step_id", ErrValidation)
			}
			stepID = &trimmed
		}
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := ensurePipelineIssueVisible(ctx, tx, issueID); err != nil {
		return err
	}
	if stepID != nil {
		if err := ensurePipelineStepVisible(ctx, tx, *stepID); err != nil {
			return err
		}
	}

	result, err := tx.ExecContext(
		ctx,
		`UPDATE project_issues
		 SET current_pipeline_step_id = $1,
		     pipeline_started_at = $2,
		     pipeline_completed_at = $3
		 WHERE org_id = $4 AND id = $5`,
		nullableString(stepID),
		nullableTime(input.PipelineStartedAt),
		nullableTime(input.PipelineCompletedAt),
		workspaceID,
		issueID,
	)
	if err != nil {
		return fmt.Errorf("failed to update issue pipeline state: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read issue pipeline update result: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}

	return tx.Commit()
}

func (s *PipelineStepStore) GetIssuePipelineState(ctx context.Context, issueID string) (*IssuePipelineState, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("%w: invalid issue_id", ErrValidation)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var record IssuePipelineState
	var currentStepID sql.NullString
	var startedAt sql.NullTime
	var completedAt sql.NullTime
	if err := conn.QueryRowContext(
		ctx,
		`SELECT id, project_id, current_pipeline_step_id, pipeline_started_at, pipeline_completed_at
		 FROM project_issues
		 WHERE org_id = $1 AND id = $2`,
		workspaceID,
		issueID,
	).Scan(
		&record.IssueID,
		&record.ProjectID,
		&currentStepID,
		&startedAt,
		&completedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get issue pipeline state: %w", err)
	}

	if currentStepID.Valid {
		record.CurrentPipelineStepID = &currentStepID.String
	}
	if startedAt.Valid {
		record.PipelineStartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		record.PipelineCompletedAt = &completedAt.Time
	}
	return &record, nil
}

func ensurePipelineIssueVisible(ctx context.Context, conn Querier, issueID string) error {
	var exists bool
	if err := conn.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM project_issues WHERE id = $1)`,
		issueID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("failed to validate issue: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func ensurePipelineStepVisible(ctx context.Context, conn Querier, stepID string) error {
	var exists bool
	if err := conn.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM pipeline_steps WHERE id = $1)`,
		stepID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("failed to validate pipeline step: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func scanPipelineStep(scanner interface{ Scan(...any) error }) (PipelineStep, error) {
	var step PipelineStep
	var assignedAgentID sql.NullString
	var createdAt time.Time
	var updatedAt time.Time

	err := scanner.Scan(
		&step.ID,
		&step.OrgID,
		&step.ProjectID,
		&step.StepNumber,
		&step.Name,
		&step.Description,
		&assignedAgentID,
		&step.StepType,
		&step.AutoAdvance,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return step, err
	}
	if assignedAgentID.Valid {
		step.AssignedAgentID = &assignedAgentID.String
	}
	step.StepType = normalizePipelineStepType(step.StepType)
	step.CreatedAt = &createdAt
	step.UpdatedAt = &updatedAt
	return step, nil
}

func scanIssuePipelineHistoryEntry(scanner interface{ Scan(...any) error }) (IssuePipelineHistoryEntry, error) {
	var entry IssuePipelineHistoryEntry
	var agentID sql.NullString
	var completedAt sql.NullTime
	var createdAt time.Time
	var updatedAt time.Time

	err := scanner.Scan(
		&entry.ID,
		&entry.OrgID,
		&entry.IssueID,
		&entry.StepID,
		&agentID,
		&entry.StartedAt,
		&completedAt,
		&entry.Result,
		&entry.Notes,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return entry, err
	}
	if agentID.Valid {
		entry.AgentID = &agentID.String
	}
	if completedAt.Valid {
		entry.CompletedAt = &completedAt.Time
	}
	entry.Result = normalizeIssuePipelineResult(entry.Result)
	entry.CreatedAt = &createdAt
	entry.UpdatedAt = &updatedAt
	return entry, nil
}
