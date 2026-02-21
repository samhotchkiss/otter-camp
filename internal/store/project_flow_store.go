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
	FlowRolePlanner  = PipelineRolePlanner
	FlowRoleWorker   = PipelineRoleWorker
	FlowRoleReviewer = PipelineRoleReviewer
	FlowRoleHuman    = "human"
	FlowRoleAgent    = "agent"

	FlowNodeTypeWork   = "work"
	FlowNodeTypeReview = "review"

	FlowActorTypeRole           = "role"
	FlowActorTypeProjectManager = "project_manager"
	FlowActorTypeHuman          = "human"
	FlowActorTypeAgent          = "agent"
)

var allowedFlowRoles = map[string]struct{}{
	FlowRolePlanner:  {},
	FlowRoleWorker:   {},
	FlowRoleReviewer: {},
	FlowRoleHuman:    {},
	FlowRoleAgent:    {},
}

var allowedFlowNodeTypes = map[string]struct{}{
	FlowNodeTypeWork:   {},
	FlowNodeTypeReview: {},
}

var allowedFlowActorTypes = map[string]struct{}{
	FlowActorTypeRole:           {},
	FlowActorTypeProjectManager: {},
	FlowActorTypeHuman:          {},
	FlowActorTypeAgent:          {},
}

type ProjectFlowTemplate struct {
	ID          string    `json:"id"`
	OrgID       string    `json:"org_id"`
	ProjectID   string    `json:"project_id"`
	Name        string    `json:"name"`
	Description *string   `json:"description,omitempty"`
	IsDefault   bool      `json:"is_default"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ProjectFlowTemplateStep struct {
	ID             string    `json:"id"`
	OrgID          string    `json:"org_id"`
	FlowTemplateID string    `json:"flow_template_id"`
	StepOrder      int       `json:"step_order"`
	StepKey        string    `json:"step_key"`
	Label          string    `json:"label"`
	Role           string    `json:"role"`
	NodeType       string    `json:"node_type"`
	Objective      string    `json:"objective"`
	ActorType      string    `json:"actor_type"`
	ActorValue     *string   `json:"actor_value,omitempty"`
	NextStepKey    *string   `json:"next_step_key,omitempty"`
	RejectStepKey  *string   `json:"reject_step_key,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type CreateProjectFlowTemplateInput struct {
	ProjectID   string
	Name        string
	Description *string
	IsDefault   bool
}

type UpdateProjectFlowTemplateInput struct {
	TemplateID  string
	Name        *string
	Description *string
	IsDefault   *bool
}

type CreateProjectFlowTemplateStepInput struct {
	StepKey       string
	Label         string
	Role          string
	NodeType      string
	Objective     *string
	ActorType     string
	ActorValue    *string
	NextStepKey   *string
	RejectStepKey *string
}

type ProjectFlowStore struct {
	db *sql.DB
}

func NewProjectFlowStore(db *sql.DB) *ProjectFlowStore {
	return &ProjectFlowStore{db: db}
}

func normalizeFlowStepKey(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%w: step_key is required", ErrValidation)
	}
	normalized := strings.ToLower(trimmed)
	normalized = strings.ReplaceAll(normalized, " ", "_")
	return normalized, nil
}

func normalizeFlowRole(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return "", fmt.Errorf("%w: role is required", ErrValidation)
	}
	if _, ok := allowedFlowRoles[normalized]; !ok {
		return "", fmt.Errorf("%w: invalid role", ErrValidation)
	}
	return normalized, nil
}

func normalizeFlowNodeType(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return FlowNodeTypeWork, nil
	}
	if _, ok := allowedFlowNodeTypes[normalized]; !ok {
		return "", fmt.Errorf("%w: invalid node_type", ErrValidation)
	}
	return normalized, nil
}

func normalizeFlowActorType(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return "", nil
	}
	if _, ok := allowedFlowActorTypes[normalized]; !ok {
		return "", fmt.Errorf("%w: invalid actor_type", ErrValidation)
	}
	return normalized, nil
}

func normalizeFlowOptionalStepKey(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized, err := normalizeFlowStepKey(*value)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func normalizeFlowOptionalText(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func deriveLegacyRole(actorType string, actorValue *string) string {
	switch actorType {
	case FlowActorTypeRole:
		if actorValue != nil {
			return strings.ToLower(strings.TrimSpace(*actorValue))
		}
		return FlowRoleWorker
	case FlowActorTypeHuman:
		return FlowRoleHuman
	default:
		return FlowRoleAgent
	}
}

func (s *ProjectFlowStore) ListTemplatesByProject(ctx context.Context, projectID string) ([]ProjectFlowTemplate, error) {
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

	if err := ensureProjectVisible(ctx, conn, projectID); err != nil {
		return nil, err
	}

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, project_id, name, description, is_default, created_at, updated_at
			FROM project_flow_templates
			WHERE project_id = $1
			ORDER BY is_default DESC, created_at ASC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list flow templates: %w", err)
	}
	defer rows.Close()

	out := make([]ProjectFlowTemplate, 0)
	for rows.Next() {
		template, scanErr := scanProjectFlowTemplate(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan flow template: %w", scanErr)
		}
		if template.OrgID != workspaceID {
			continue
		}
		out = append(out, template)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading flow templates: %w", err)
	}
	return out, nil
}

func (s *ProjectFlowStore) GetTemplateByID(ctx context.Context, templateID string) (*ProjectFlowTemplate, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	templateID = strings.TrimSpace(templateID)
	if !uuidRegex.MatchString(templateID) {
		return nil, fmt.Errorf("%w: invalid template_id", ErrValidation)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	template, err := scanProjectFlowTemplate(conn.QueryRowContext(
		ctx,
		`SELECT id, org_id, project_id, name, description, is_default, created_at, updated_at
			FROM project_flow_templates
			WHERE id = $1`,
		templateID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load flow template: %w", err)
	}
	if template.OrgID != workspaceID {
		return nil, ErrForbidden
	}
	return &template, nil
}

func (s *ProjectFlowStore) GetDefaultTemplateForProject(ctx context.Context, projectID string) (*ProjectFlowTemplate, error) {
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

	template, err := scanProjectFlowTemplate(conn.QueryRowContext(
		ctx,
		`SELECT id, org_id, project_id, name, description, is_default, created_at, updated_at
			FROM project_flow_templates
			WHERE project_id = $1 AND is_default = true
			LIMIT 1`,
		projectID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load default flow template: %w", err)
	}
	if template.OrgID != workspaceID {
		return nil, ErrForbidden
	}
	return &template, nil
}

func (s *ProjectFlowStore) CreateTemplate(ctx context.Context, input CreateProjectFlowTemplateInput) (*ProjectFlowTemplate, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	projectID := strings.TrimSpace(input.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("%w: invalid project_id", ErrValidation)
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrValidation)
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := ensureProjectVisible(ctx, tx, projectID); err != nil {
		return nil, err
	}

	if input.IsDefault {
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE project_flow_templates SET is_default = false WHERE project_id = $1`,
			projectID,
		); err != nil {
			return nil, fmt.Errorf("failed to clear default flow template: %w", err)
		}
	}

	template, err := scanProjectFlowTemplate(tx.QueryRowContext(
		ctx,
		`INSERT INTO project_flow_templates (
			org_id, project_id, name, description, is_default
		) VALUES ($1,$2,$3,$4,$5)
		RETURNING id, org_id, project_id, name, description, is_default, created_at, updated_at`,
		workspaceID,
		projectID,
		name,
		nullableString(input.Description),
		input.IsDefault,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create flow template: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &template, nil
}

func (s *ProjectFlowStore) UpdateTemplate(ctx context.Context, input UpdateProjectFlowTemplateInput) (*ProjectFlowTemplate, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	templateID := strings.TrimSpace(input.TemplateID)
	if !uuidRegex.MatchString(templateID) {
		return nil, fmt.Errorf("%w: invalid template_id", ErrValidation)
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	current, err := scanProjectFlowTemplate(tx.QueryRowContext(
		ctx,
		`SELECT id, org_id, project_id, name, description, is_default, created_at, updated_at
			FROM project_flow_templates
			WHERE id = $1
			FOR UPDATE`,
		templateID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load flow template: %w", err)
	}
	if current.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	nextName := current.Name
	if input.Name != nil {
		trimmed := strings.TrimSpace(*input.Name)
		if trimmed == "" {
			return nil, fmt.Errorf("%w: name cannot be blank", ErrValidation)
		}
		nextName = trimmed
	}

	nextDescription := current.Description
	if input.Description != nil {
		nextDescription = normalizeOptionalIssueText(input.Description)
	}

	nextDefault := current.IsDefault
	if input.IsDefault != nil {
		nextDefault = *input.IsDefault
	}

	if nextDefault && !current.IsDefault {
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE project_flow_templates SET is_default = false WHERE project_id = $1`,
			current.ProjectID,
		); err != nil {
			return nil, fmt.Errorf("failed to clear default flow template: %w", err)
		}
	}

	updated, err := scanProjectFlowTemplate(tx.QueryRowContext(
		ctx,
		`UPDATE project_flow_templates
			SET name = $2,
				description = $3,
				is_default = $4,
				updated_at = NOW()
			WHERE id = $1
			RETURNING id, org_id, project_id, name, description, is_default, created_at, updated_at`,
		templateID,
		nextName,
		nullableString(nextDescription),
		nextDefault,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to update flow template: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *ProjectFlowStore) DeleteTemplate(ctx context.Context, templateID string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}
	templateID = strings.TrimSpace(templateID)
	if !uuidRegex.MatchString(templateID) {
		return fmt.Errorf("%w: invalid template_id", ErrValidation)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	var orgID string
	if err := conn.QueryRowContext(
		ctx,
		`SELECT org_id FROM project_flow_templates WHERE id = $1`,
		templateID,
	).Scan(&orgID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to check flow template: %w", err)
	}
	if orgID != workspaceID {
		return ErrForbidden
	}

	_, err = conn.ExecContext(ctx, `DELETE FROM project_flow_templates WHERE id = $1`, templateID)
	if err != nil {
		return fmt.Errorf("failed to delete flow template: %w", err)
	}
	return nil
}

func (s *ProjectFlowStore) ListTemplateSteps(ctx context.Context, templateID string) ([]ProjectFlowTemplateStep, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	templateID = strings.TrimSpace(templateID)
	if !uuidRegex.MatchString(templateID) {
		return nil, fmt.Errorf("%w: invalid template_id", ErrValidation)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, flow_template_id, step_order, step_key, label, role,
				node_type, objective, actor_type, actor_value, next_step_key, reject_step_key,
				created_at, updated_at
			FROM project_flow_template_steps
			WHERE flow_template_id = $1
			ORDER BY step_order ASC`,
		templateID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list flow steps: %w", err)
	}
	defer rows.Close()

	out := make([]ProjectFlowTemplateStep, 0)
	for rows.Next() {
		step, scanErr := scanProjectFlowTemplateStep(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan flow step: %w", scanErr)
		}
		if step.OrgID != workspaceID {
			continue
		}
		out = append(out, step)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading flow steps: %w", err)
	}
	return out, nil
}

func (s *ProjectFlowStore) ReplaceTemplateSteps(ctx context.Context, templateID string, inputs []CreateProjectFlowTemplateStepInput) ([]ProjectFlowTemplateStep, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	templateID = strings.TrimSpace(templateID)
	if !uuidRegex.MatchString(templateID) {
		return nil, fmt.Errorf("%w: invalid template_id", ErrValidation)
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%w: at least one step is required", ErrValidation)
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var orgID string
	if err := tx.QueryRowContext(
		ctx,
		`SELECT org_id FROM project_flow_templates WHERE id = $1`,
		templateID,
	).Scan(&orgID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to check flow template: %w", err)
	}
	if orgID != workspaceID {
		return nil, ErrForbidden
	}

	stepKeys := make(map[string]struct{}, len(inputs))
	normalized := make([]CreateProjectFlowTemplateStepInput, 0, len(inputs))
	for _, input := range inputs {
		key, err := normalizeFlowStepKey(input.StepKey)
		if err != nil {
			return nil, err
		}
		if _, exists := stepKeys[key]; exists {
			return nil, fmt.Errorf("%w: duplicate step_key", ErrValidation)
		}
		stepKeys[key] = struct{}{}

		label := strings.TrimSpace(input.Label)
		if label == "" {
			return nil, fmt.Errorf("%w: label is required", ErrValidation)
		}

		nodeType, err := normalizeFlowNodeType(input.NodeType)
		if err != nil {
			return nil, err
		}

		objective := normalizeFlowOptionalText(input.Objective)
		if objective == nil {
			objective = &label
		}

		actorType, err := normalizeFlowActorType(input.ActorType)
		if err != nil {
			return nil, err
		}
		actorValue := normalizeFlowOptionalText(input.ActorValue)
		if actorType == "" {
			role, roleErr := normalizeFlowRole(input.Role)
			if roleErr != nil {
				return nil, roleErr
			}
			switch role {
			case FlowRolePlanner, FlowRoleWorker, FlowRoleReviewer:
				actorType = FlowActorTypeRole
				actorValue = &role
			case FlowRoleHuman:
				actorType = FlowActorTypeHuman
				actorValue = nil
			default:
				actorType = FlowActorTypeAgent
			}
		}

		switch actorType {
		case FlowActorTypeRole:
			if actorValue == nil {
				return nil, fmt.Errorf("%w: actor_value is required when actor_type is role", ErrValidation)
			}
			roleValue, roleErr := normalizeFlowRole(*actorValue)
			if roleErr != nil {
				return nil, roleErr
			}
			if roleValue != FlowRolePlanner && roleValue != FlowRoleWorker && roleValue != FlowRoleReviewer {
				return nil, fmt.Errorf("%w: role actor_value must be planner, worker, or reviewer", ErrValidation)
			}
			actorValue = &roleValue
		case FlowActorTypeProjectManager, FlowActorTypeHuman:
			if actorValue != nil {
				return nil, fmt.Errorf("%w: actor_value must be empty for actor_type %s", ErrValidation, actorType)
			}
		case FlowActorTypeAgent:
			if actorValue != nil {
				trimmed := strings.TrimSpace(*actorValue)
				if trimmed != "" && !uuidRegex.MatchString(trimmed) {
					return nil, fmt.Errorf("%w: actor_value must be a valid agent id when actor_type is agent", ErrValidation)
				}
				if trimmed == "" {
					actorValue = nil
				} else {
					actorValue = &trimmed
				}
			}
		default:
			return nil, fmt.Errorf("%w: actor_type is required", ErrValidation)
		}

		nextStepKey, nextErr := normalizeFlowOptionalStepKey(input.NextStepKey)
		if nextErr != nil {
			return nil, nextErr
		}
		rejectStepKey, rejectErr := normalizeFlowOptionalStepKey(input.RejectStepKey)
		if rejectErr != nil {
			return nil, rejectErr
		}
		if nodeType == FlowNodeTypeReview && rejectStepKey == nil {
			return nil, fmt.Errorf("%w: reject_step_key is required for review nodes", ErrValidation)
		}
		if nodeType != FlowNodeTypeReview && rejectStepKey != nil {
			return nil, fmt.Errorf("%w: reject_step_key is only allowed for review nodes", ErrValidation)
		}

		normalized = append(normalized, CreateProjectFlowTemplateStepInput{
			StepKey:       key,
			Label:         label,
			Role:          deriveLegacyRole(actorType, actorValue),
			NodeType:      nodeType,
			Objective:     objective,
			ActorType:     actorType,
			ActorValue:    actorValue,
			NextStepKey:   nextStepKey,
			RejectStepKey: rejectStepKey,
		})
	}

	for _, step := range normalized {
		if step.NextStepKey != nil {
			if _, exists := stepKeys[*step.NextStepKey]; !exists {
				return nil, fmt.Errorf("%w: next_step_key references unknown step", ErrValidation)
			}
		}
		if step.RejectStepKey != nil {
			if _, exists := stepKeys[*step.RejectStepKey]; !exists {
				return nil, fmt.Errorf("%w: reject_step_key references unknown step", ErrValidation)
			}
		}
	}

	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM project_flow_template_steps WHERE flow_template_id = $1`,
		templateID,
	); err != nil {
		return nil, fmt.Errorf("failed to clear flow steps: %w", err)
	}

	out := make([]ProjectFlowTemplateStep, 0, len(normalized))
	for index, step := range normalized {
		record, err := scanProjectFlowTemplateStep(tx.QueryRowContext(
			ctx,
			`INSERT INTO project_flow_template_steps (
				org_id, flow_template_id, step_order, step_key, label, role,
				node_type, objective, actor_type, actor_value, next_step_key, reject_step_key
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			RETURNING id, org_id, flow_template_id, step_order, step_key, label, role,
				node_type, objective, actor_type, actor_value, next_step_key, reject_step_key,
				created_at, updated_at`,
			workspaceID,
			templateID,
			index,
			step.StepKey,
			step.Label,
			step.Role,
			step.NodeType,
			step.Objective,
			step.ActorType,
			nullableString(step.ActorValue),
			nullableString(step.NextStepKey),
			nullableString(step.RejectStepKey),
		))
		if err != nil {
			return nil, fmt.Errorf("failed to insert flow step: %w", err)
		}
		out = append(out, record)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanProjectFlowTemplate(scanner interface{ Scan(...any) error }) (ProjectFlowTemplate, error) {
	var template ProjectFlowTemplate
	var description sql.NullString
	if err := scanner.Scan(
		&template.ID,
		&template.OrgID,
		&template.ProjectID,
		&template.Name,
		&description,
		&template.IsDefault,
		&template.CreatedAt,
		&template.UpdatedAt,
	); err != nil {
		return template, err
	}
	if description.Valid {
		template.Description = &description.String
	}
	return template, nil
}

func scanProjectFlowTemplateStep(scanner interface{ Scan(...any) error }) (ProjectFlowTemplateStep, error) {
	var step ProjectFlowTemplateStep
	var actorValue sql.NullString
	var nextStepKey sql.NullString
	var rejectStepKey sql.NullString
	if err := scanner.Scan(
		&step.ID,
		&step.OrgID,
		&step.FlowTemplateID,
		&step.StepOrder,
		&step.StepKey,
		&step.Label,
		&step.Role,
		&step.NodeType,
		&step.Objective,
		&step.ActorType,
		&actorValue,
		&nextStepKey,
		&rejectStepKey,
		&step.CreatedAt,
		&step.UpdatedAt,
	); err != nil {
		return step, err
	}
	if actorValue.Valid {
		step.ActorValue = &actorValue.String
	}
	if nextStepKey.Valid {
		step.NextStepKey = &nextStepKey.String
	}
	if rejectStepKey.Valid {
		step.RejectStepKey = &rejectStepKey.String
	}
	return step, nil
}
