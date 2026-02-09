package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const (
	PipelineRolePlanner  = "planner"
	PipelineRoleWorker   = "worker"
	PipelineRoleReviewer = "reviewer"
)

var pipelineRoleOrder = []string{
	PipelineRolePlanner,
	PipelineRoleWorker,
	PipelineRoleReviewer,
}

type PipelineRoleAssignment struct {
	ID        string     `json:"id"`
	OrgID     string     `json:"org_id"`
	ProjectID string     `json:"project_id"`
	Role      string     `json:"role"`
	AgentID   *string    `json:"agent_id,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type UpsertPipelineRoleAssignmentInput struct {
	ProjectID string
	Role      string
	AgentID   *string
}

type PipelineRoleStore struct {
	db *sql.DB
}

func NewPipelineRoleStore(db *sql.DB) *PipelineRoleStore {
	return &PipelineRoleStore{db: db}
}

func normalizePipelineRoleAssignmentInput(input UpsertPipelineRoleAssignmentInput) (UpsertPipelineRoleAssignmentInput, error) {
	projectID := strings.TrimSpace(input.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return UpsertPipelineRoleAssignmentInput{}, fmt.Errorf("invalid project_id")
	}

	role := strings.ToLower(strings.TrimSpace(input.Role))
	switch role {
	case PipelineRolePlanner, PipelineRoleWorker, PipelineRoleReviewer:
	default:
		return UpsertPipelineRoleAssignmentInput{}, fmt.Errorf("role must be planner, worker, or reviewer")
	}

	agentID := normalizeOptionalPipelineAgentID(input.AgentID)
	if agentID != nil && !uuidRegex.MatchString(*agentID) {
		return UpsertPipelineRoleAssignmentInput{}, fmt.Errorf("invalid agent_id")
	}

	return UpsertPipelineRoleAssignmentInput{
		ProjectID: projectID,
		Role:      role,
		AgentID:   agentID,
	}, nil
}

func normalizeOptionalPipelineAgentID(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func (s *PipelineRoleStore) Upsert(ctx context.Context, input UpsertPipelineRoleAssignmentInput) (*PipelineRoleAssignment, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	normalized, err := normalizePipelineRoleAssignmentInput(input)
	if err != nil {
		return nil, err
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := ensurePipelineProjectVisible(ctx, conn, normalized.ProjectID); err != nil {
		return nil, err
	}

	if normalized.AgentID != nil {
		if err := ensurePipelineAgentVisible(ctx, conn, *normalized.AgentID); err != nil {
			return nil, err
		}
	}

	return upsertPipelineRoleAssignment(ctx, conn, workspaceID, normalized)
}

func (s *PipelineRoleStore) UpsertBatch(ctx context.Context, inputs []UpsertPipelineRoleAssignmentInput) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}
	if len(inputs) == 0 {
		return nil
	}

	normalized := make([]UpsertPipelineRoleAssignmentInput, 0, len(inputs))
	for _, input := range inputs {
		clean, err := normalizePipelineRoleAssignmentInput(input)
		if err != nil {
			return err
		}
		normalized = append(normalized, clean)
	}

	projectID := normalized[0].ProjectID
	for _, input := range normalized[1:] {
		if input.ProjectID != projectID {
			return fmt.Errorf("all pipeline role updates must target the same project")
		}
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := ensurePipelineProjectVisible(ctx, tx, projectID); err != nil {
		return err
	}

	for _, input := range normalized {
		if input.AgentID != nil {
			if err := ensurePipelineAgentVisible(ctx, tx, *input.AgentID); err != nil {
				return err
			}
		}
		if _, err := upsertPipelineRoleAssignment(ctx, tx, workspaceID, input); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *PipelineRoleStore) ListByProject(ctx context.Context, projectID string) ([]PipelineRoleAssignment, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
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
		`SELECT id, org_id, project_id, role, agent_id, created_at, updated_at
			FROM issue_role_assignments
			WHERE project_id = $1`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list pipeline role assignments: %w", err)
	}
	defer rows.Close()

	assignmentsByRole := make(map[string]PipelineRoleAssignment, len(pipelineRoleOrder))
	for rows.Next() {
		assignment, scanErr := scanPipelineRoleAssignment(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan pipeline role assignment: %w", scanErr)
		}
		assignmentsByRole[assignment.Role] = assignment
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading pipeline role assignments: %w", err)
	}

	ordered := make([]PipelineRoleAssignment, 0, len(pipelineRoleOrder))
	for _, role := range pipelineRoleOrder {
		if assignment, ok := assignmentsByRole[role]; ok {
			ordered = append(ordered, assignment)
			continue
		}
		ordered = append(ordered, PipelineRoleAssignment{
			OrgID:     workspaceID,
			ProjectID: projectID,
			Role:      role,
			AgentID:   nil,
		})
	}
	return ordered, nil
}

func ensurePipelineProjectVisible(ctx context.Context, conn Querier, projectID string) error {
	var exists bool
	if err := conn.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM projects WHERE id = $1)`,
		projectID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("failed to validate project: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func ensurePipelineAgentVisible(ctx context.Context, conn Querier, agentID string) error {
	var exists bool
	if err := conn.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM agents WHERE id = $1)`,
		agentID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("failed to validate agent: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func upsertPipelineRoleAssignment(ctx context.Context, querier Querier, workspaceID string, normalized UpsertPipelineRoleAssignmentInput) (*PipelineRoleAssignment, error) {
	row := querier.QueryRowContext(
		ctx,
		`INSERT INTO issue_role_assignments (
			org_id, project_id, role, agent_id
		) VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, role)
		DO UPDATE SET agent_id = EXCLUDED.agent_id
		RETURNING id, org_id, project_id, role, agent_id, created_at, updated_at`,
		workspaceID,
		normalized.ProjectID,
		normalized.Role,
		nullableString(normalized.AgentID),
	)

	assignment, err := scanPipelineRoleAssignment(row)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert pipeline role assignment: %w", err)
	}
	return &assignment, nil
}

func scanPipelineRoleAssignment(scanner interface{ Scan(...any) error }) (PipelineRoleAssignment, error) {
	var assignment PipelineRoleAssignment
	var agentID sql.NullString
	var createdAt time.Time
	var updatedAt time.Time

	if err := scanner.Scan(
		&assignment.ID,
		&assignment.OrgID,
		&assignment.ProjectID,
		&assignment.Role,
		&agentID,
		&createdAt,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PipelineRoleAssignment{}, err
		}
		return PipelineRoleAssignment{}, err
	}

	if agentID.Valid {
		assignment.AgentID = &agentID.String
	}
	assignment.CreatedAt = &createdAt
	assignment.UpdatedAt = &updatedAt
	return assignment, nil
}
