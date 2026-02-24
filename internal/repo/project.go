package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const nilUUIDLiteral = "00000000-0000-0000-0000-000000000000"

type Project struct {
	ID                   uuid.UUID
	OrganizationID       uuid.UUID
	Slug                 string
	DisplayName          string
	Description          string
	DeliveryMode         string
	DeployFlowTemplateID *uuid.UUID
	Settings             json.RawMessage
	CreatedByType        string
	CreatedByID          uuid.UUID
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type FlowTemplate struct {
	ID             uuid.UUID
	OrganizationID *uuid.UUID
	ProjectID      *uuid.UUID
	Slug           string
	DisplayName    string
	Description    string
	IsCurrent      bool
	Version        int
	StartNodeID    *uuid.UUID
	IsSystem       bool
	CreatedByType  string
	CreatedByID    uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type TaskSchedule struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	ProjectID      uuid.UUID
	FlowTemplateID uuid.UUID
	DisplayName    string
	CronExpression string
	OverlapPolicy  string
	MaxDurationMS  *int64
	IsEnabled      bool
	LastFiredAt    *time.Time
	NextFireAt     *time.Time
	CreatedByType  string
	CreatedByID    uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ProjectRepo struct {
	pool *pgxpool.Pool
}

func NewProjectRepo(pool *pgxpool.Pool) *ProjectRepo {
	return &ProjectRepo{pool: pool}
}

func (r *ProjectRepo) Create(ctx context.Context, project Project) (Project, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO project (
			organization_id,
			slug,
			display_name,
			description,
			delivery_mode,
			deploy_flow_template_id,
			settings,
			created_by_type,
			created_by_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9)
		RETURNING id, organization_id, slug, display_name, description, delivery_mode, deploy_flow_template_id, settings, created_by_type, created_by_id, created_at, updated_at
	`,
		project.OrganizationID,
		project.Slug,
		project.DisplayName,
		project.Description,
		project.DeliveryMode,
		project.DeployFlowTemplateID,
		normalizeProjectSettingsJSON(project.Settings),
		project.CreatedByType,
		project.CreatedByID,
	)

	created, err := scanProject(row)
	if err != nil {
		return Project{}, mapDBError(err)
	}
	return created, nil
}

func (r *ProjectRepo) GetByID(ctx context.Context, id uuid.UUID) (Project, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, slug, display_name, description, delivery_mode, deploy_flow_template_id, settings, created_by_type, created_by_id, created_at, updated_at
		FROM project
		WHERE id = $1
	`, id)

	project, err := scanProject(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, mapDBError(err)
	}
	return project, nil
}

func (r *ProjectRepo) GetBySlug(ctx context.Context, organizationID uuid.UUID, slug string) (Project, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, slug, display_name, description, delivery_mode, deploy_flow_template_id, settings, created_by_type, created_by_id, created_at, updated_at
		FROM project
		WHERE organization_id = $1
		  AND slug = $2
	`, organizationID, strings.TrimSpace(slug))

	project, err := scanProject(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, mapDBError(err)
	}
	return project, nil
}

func (r *ProjectRepo) List(ctx context.Context, organizationID uuid.UUID) ([]Project, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, slug, display_name, description, delivery_mode, deploy_flow_template_id, settings, created_by_type, created_by_id, created_at, updated_at
		FROM project
		WHERE organization_id = $1
		ORDER BY created_at DESC, id DESC
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	projects := make([]Project, 0)
	for rows.Next() {
		project, scanErr := scanProject(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		projects = append(projects, project)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return projects, nil
}

func (r *ProjectRepo) Update(ctx context.Context, project Project) (Project, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE project
		SET
			slug = $2,
			display_name = $3,
			description = $4,
			delivery_mode = $5,
			deploy_flow_template_id = $6,
			settings = $7::jsonb
		WHERE id = $1
		RETURNING id, organization_id, slug, display_name, description, delivery_mode, deploy_flow_template_id, settings, created_by_type, created_by_id, created_at, updated_at
	`,
		project.ID,
		project.Slug,
		project.DisplayName,
		project.Description,
		project.DeliveryMode,
		project.DeployFlowTemplateID,
		normalizeProjectSettingsJSON(project.Settings),
	)

	updated, err := scanProject(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, mapDBError(err)
	}
	return updated, nil
}

func (r *ProjectRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `
		DELETE FROM project
		WHERE id = $1
	`, id)
	if err != nil {
		return mapDBError(err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type FlowTemplateRepo struct {
	pool *pgxpool.Pool
}

func NewFlowTemplateRepo(pool *pgxpool.Pool) *FlowTemplateRepo {
	return &FlowTemplateRepo{pool: pool}
}

func (r *FlowTemplateRepo) Create(ctx context.Context, template FlowTemplate) (FlowTemplate, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO flow_template (
			organization_id,
			project_id,
			slug,
			display_name,
			description,
			is_current,
			version,
			start_node_id,
			is_system,
			created_by_type,
			created_by_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, organization_id, project_id, slug, display_name, description, is_current, version, start_node_id, is_system, created_by_type, created_by_id, created_at, updated_at
	`,
		template.OrganizationID,
		template.ProjectID,
		template.Slug,
		template.DisplayName,
		template.Description,
		defaultCurrent(template.IsCurrent),
		defaultVersion(template.Version),
		template.StartNodeID,
		template.IsSystem,
		template.CreatedByType,
		template.CreatedByID,
	)

	created, err := scanFlowTemplate(row)
	if err != nil {
		return FlowTemplate{}, mapDBError(err)
	}
	return created, nil
}

func (r *FlowTemplateRepo) GetByID(ctx context.Context, id uuid.UUID) (FlowTemplate, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, project_id, slug, display_name, description, is_current, version, start_node_id, is_system, created_by_type, created_by_id, created_at, updated_at
		FROM flow_template
		WHERE id = $1
	`, id)

	template, err := scanFlowTemplate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowTemplate{}, ErrNotFound
	}
	if err != nil {
		return FlowTemplate{}, mapDBError(err)
	}
	return template, nil
}

func (r *FlowTemplateRepo) GetCurrentBySlug(ctx context.Context, organizationID, projectID *uuid.UUID, slug string) (FlowTemplate, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, project_id, slug, display_name, description, is_current, version, start_node_id, is_system, created_by_type, created_by_id, created_at, updated_at
		FROM flow_template
		WHERE slug = $3
		  AND is_current = true
		  AND ((organization_id IS NULL AND $1::uuid IS NULL) OR organization_id = $1)
		  AND ((project_id IS NULL AND $2::uuid IS NULL) OR project_id = $2)
		LIMIT 1
	`, organizationID, projectID, strings.TrimSpace(slug))

	template, err := scanFlowTemplate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowTemplate{}, ErrNotFound
	}
	if err != nil {
		return FlowTemplate{}, mapDBError(err)
	}
	return template, nil
}

func (r *FlowTemplateRepo) ListCurrent(ctx context.Context, organizationID, projectID *uuid.UUID) ([]FlowTemplate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, project_id, slug, display_name, description, is_current, version, start_node_id, is_system, created_by_type, created_by_id, created_at, updated_at
		FROM flow_template
		WHERE is_current = true
		  AND ((organization_id IS NULL AND $1::uuid IS NULL) OR organization_id = $1)
		  AND ((project_id IS NULL AND $2::uuid IS NULL) OR project_id = $2)
		ORDER BY created_at ASC, id ASC
	`, organizationID, projectID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	templates := make([]FlowTemplate, 0)
	for rows.Next() {
		template, scanErr := scanFlowTemplate(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		templates = append(templates, template)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return templates, nil
}

func (r *FlowTemplateRepo) ListAll(ctx context.Context, organizationID, projectID *uuid.UUID, slug string) ([]FlowTemplate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, project_id, slug, display_name, description, is_current, version, start_node_id, is_system, created_by_type, created_by_id, created_at, updated_at
		FROM flow_template
		WHERE ((organization_id IS NULL AND $1::uuid IS NULL) OR organization_id = $1)
		  AND ((project_id IS NULL AND $2::uuid IS NULL) OR project_id = $2)
		  AND ($3 = '' OR slug = $3)
		ORDER BY version ASC, created_at ASC, id ASC
	`, organizationID, projectID, strings.TrimSpace(slug))
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	templates := make([]FlowTemplate, 0)
	for rows.Next() {
		template, scanErr := scanFlowTemplate(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		templates = append(templates, template)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return templates, nil
}

func (r *FlowTemplateRepo) Update(ctx context.Context, template FlowTemplate) (FlowTemplate, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE flow_template
		SET
			slug = $2,
			display_name = $3,
			description = $4,
			start_node_id = $5
		WHERE id = $1
		RETURNING id, organization_id, project_id, slug, display_name, description, is_current, version, start_node_id, is_system, created_by_type, created_by_id, created_at, updated_at
	`,
		template.ID,
		strings.TrimSpace(template.Slug),
		strings.TrimSpace(template.DisplayName),
		template.Description,
		template.StartNodeID,
	)

	updated, err := scanFlowTemplate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowTemplate{}, ErrNotFound
	}
	if err != nil {
		return FlowTemplate{}, mapDBError(err)
	}
	return updated, nil
}

func (r *FlowTemplateRepo) Deprecate(ctx context.Context, currentID uuid.UUID, next FlowTemplate) (FlowTemplate, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return FlowTemplate{}, fmt.Errorf("begin deprecate transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	row := tx.QueryRow(ctx, `
		SELECT id, organization_id, project_id, slug, display_name, description, is_current, version, start_node_id, is_system, created_by_type, created_by_id, created_at, updated_at
		FROM flow_template
		WHERE id = $1
		FOR UPDATE
	`, currentID)

	current, err := scanFlowTemplate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowTemplate{}, ErrNotFound
	}
	if err != nil {
		return FlowTemplate{}, mapDBError(err)
	}

	if !current.IsCurrent {
		latest, latestErr := r.getCurrentByScopeAndSlugTx(ctx, tx, current.OrganizationID, current.ProjectID, current.Slug)
		if latestErr == nil {
			if err := tx.Commit(ctx); err != nil {
				return FlowTemplate{}, fmt.Errorf("commit deprecate idempotent transaction: %w", err)
			}
			return latest, nil
		}
		if !errors.Is(latestErr, ErrNotFound) {
			return FlowTemplate{}, latestErr
		}
		if err := tx.Commit(ctx); err != nil {
			return FlowTemplate{}, fmt.Errorf("commit deprecate no-current transaction: %w", err)
		}
		return current, nil
	}

	nextTemplate := mergeFlowTemplateForDeprecate(current, next)
	result, err := tx.Exec(ctx, `
		UPDATE flow_template
		SET is_current = false
		WHERE id = $1
	`, current.ID)
	if err != nil {
		return FlowTemplate{}, mapDBError(err)
	}
	if result.RowsAffected() == 0 {
		return FlowTemplate{}, ErrNotFound
	}

	insertedRow := tx.QueryRow(ctx, `
		INSERT INTO flow_template (
			organization_id,
			project_id,
			slug,
			display_name,
			description,
			is_current,
			version,
			start_node_id,
			is_system,
			created_by_type,
			created_by_id
		)
		VALUES ($1, $2, $3, $4, $5, true, $6, $7, $8, $9, $10)
		RETURNING id, organization_id, project_id, slug, display_name, description, is_current, version, start_node_id, is_system, created_by_type, created_by_id, created_at, updated_at
	`,
		nextTemplate.OrganizationID,
		nextTemplate.ProjectID,
		nextTemplate.Slug,
		nextTemplate.DisplayName,
		nextTemplate.Description,
		nextTemplate.Version,
		nextTemplate.StartNodeID,
		nextTemplate.IsSystem,
		nextTemplate.CreatedByType,
		nextTemplate.CreatedByID,
	)

	created, scanErr := scanFlowTemplate(insertedRow)
	if scanErr != nil {
		return FlowTemplate{}, mapDBError(scanErr)
	}

	if err := tx.Commit(ctx); err != nil {
		return FlowTemplate{}, fmt.Errorf("commit deprecate transaction: %w", err)
	}
	return created, nil
}

func (r *FlowTemplateRepo) BulkUpsertBySlug(ctx context.Context, templates []FlowTemplate) ([]FlowTemplate, error) {
	if len(templates) == 0 {
		return nil, nil
	}

	var sb strings.Builder
	sb.WriteString(`
		INSERT INTO flow_template (
			organization_id,
			project_id,
			slug,
			display_name,
			description,
			is_current,
			version,
			start_node_id,
			is_system,
			created_by_type,
			created_by_id
		) VALUES
	`)

	args := make([]any, 0, len(templates)*10)
	for i, template := range templates {
		if i > 0 {
			sb.WriteString(",")
		}
		pos := i*10 + 1
		sb.WriteString(fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,true,$%d,$%d,$%d,$%d,$%d)",
			pos,
			pos+1,
			pos+2,
			pos+3,
			pos+4,
			pos+5,
			pos+6,
			pos+7,
			pos+8,
			pos+9,
		))

		args = append(args,
			template.OrganizationID,
			template.ProjectID,
			template.Slug,
			template.DisplayName,
			template.Description,
			defaultVersion(template.Version),
			template.StartNodeID,
			template.IsSystem,
			template.CreatedByType,
			template.CreatedByID,
		)
	}

	sb.WriteString(`
		ON CONFLICT (
			COALESCE(organization_id, '` + nilUUIDLiteral + `'::uuid),
			COALESCE(project_id, '` + nilUUIDLiteral + `'::uuid),
			slug
		) WHERE is_current = true
		DO UPDATE SET
			display_name = EXCLUDED.display_name,
			description = EXCLUDED.description,
			start_node_id = EXCLUDED.start_node_id,
			is_system = EXCLUDED.is_system
		RETURNING id, organization_id, project_id, slug, display_name, description, is_current, version, start_node_id, is_system, created_by_type, created_by_id, created_at, updated_at
	`)

	rows, err := r.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	upserted := make([]FlowTemplate, 0, len(templates))
	for rows.Next() {
		template, scanErr := scanFlowTemplate(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		upserted = append(upserted, template)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return upserted, nil
}

func (r *FlowTemplateRepo) getCurrentByScopeAndSlugTx(ctx context.Context, tx pgx.Tx, organizationID, projectID *uuid.UUID, slug string) (FlowTemplate, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, organization_id, project_id, slug, display_name, description, is_current, version, start_node_id, is_system, created_by_type, created_by_id, created_at, updated_at
		FROM flow_template
		WHERE slug = $3
		  AND is_current = true
		  AND ((organization_id IS NULL AND $1::uuid IS NULL) OR organization_id = $1)
		  AND ((project_id IS NULL AND $2::uuid IS NULL) OR project_id = $2)
		ORDER BY version DESC
		LIMIT 1
	`, organizationID, projectID, slug)

	template, err := scanFlowTemplate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowTemplate{}, ErrNotFound
	}
	if err != nil {
		return FlowTemplate{}, mapDBError(err)
	}
	return template, nil
}

func mergeFlowTemplateForDeprecate(current FlowTemplate, next FlowTemplate) FlowTemplate {
	merged := current
	merged.ID = uuid.Nil
	merged.IsCurrent = true
	merged.Version = current.Version + 1

	if strings.TrimSpace(next.DisplayName) != "" {
		merged.DisplayName = strings.TrimSpace(next.DisplayName)
	}
	if next.Description != "" {
		merged.Description = next.Description
	}
	if next.StartNodeID != nil {
		merged.StartNodeID = next.StartNodeID
	}
	if next.CreatedByID != uuid.Nil {
		merged.CreatedByID = next.CreatedByID
	}
	if strings.TrimSpace(next.CreatedByType) != "" {
		merged.CreatedByType = strings.TrimSpace(next.CreatedByType)
	}
	merged.IsSystem = next.IsSystem || current.IsSystem

	return merged
}

type TaskScheduleRepo struct {
	pool *pgxpool.Pool
}

func NewTaskScheduleRepo(pool *pgxpool.Pool) *TaskScheduleRepo {
	return &TaskScheduleRepo{pool: pool}
}

func (r *TaskScheduleRepo) Create(ctx context.Context, schedule TaskSchedule) (TaskSchedule, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO task_schedule (
			organization_id,
			project_id,
			flow_template_id,
			display_name,
			cron_expression,
			overlap_policy,
			max_duration_ms,
			is_enabled,
			last_fired_at,
			next_fire_at,
			created_by_type,
			created_by_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, organization_id, project_id, flow_template_id, display_name, cron_expression, overlap_policy, max_duration_ms, is_enabled, last_fired_at, next_fire_at, created_by_type, created_by_id, created_at, updated_at
	`,
		schedule.OrganizationID,
		schedule.ProjectID,
		schedule.FlowTemplateID,
		schedule.DisplayName,
		schedule.CronExpression,
		defaultOverlapPolicy(schedule.OverlapPolicy),
		schedule.MaxDurationMS,
		defaultEnabled(schedule.IsEnabled),
		schedule.LastFiredAt,
		schedule.NextFireAt,
		schedule.CreatedByType,
		schedule.CreatedByID,
	)

	created, err := scanTaskSchedule(row)
	if err != nil {
		return TaskSchedule{}, mapDBError(err)
	}
	return created, nil
}

func (r *TaskScheduleRepo) GetByID(ctx context.Context, id uuid.UUID) (TaskSchedule, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, project_id, flow_template_id, display_name, cron_expression, overlap_policy, max_duration_ms, is_enabled, last_fired_at, next_fire_at, created_by_type, created_by_id, created_at, updated_at
		FROM task_schedule
		WHERE id = $1
	`, id)

	schedule, err := scanTaskSchedule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return TaskSchedule{}, ErrNotFound
	}
	if err != nil {
		return TaskSchedule{}, mapDBError(err)
	}
	return schedule, nil
}

func (r *TaskScheduleRepo) List(ctx context.Context, projectID uuid.UUID) ([]TaskSchedule, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, project_id, flow_template_id, display_name, cron_expression, overlap_policy, max_duration_ms, is_enabled, last_fired_at, next_fire_at, created_by_type, created_by_id, created_at, updated_at
		FROM task_schedule
		WHERE project_id = $1
		ORDER BY created_at ASC, id ASC
	`, projectID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	schedules := make([]TaskSchedule, 0)
	for rows.Next() {
		schedule, scanErr := scanTaskSchedule(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		schedules = append(schedules, schedule)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return schedules, nil
}

func (r *TaskScheduleRepo) Update(ctx context.Context, schedule TaskSchedule) (TaskSchedule, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE task_schedule
		SET
			flow_template_id = $2,
			display_name = $3,
			cron_expression = $4,
			overlap_policy = $5,
			max_duration_ms = $6,
			next_fire_at = $7
		WHERE id = $1
		RETURNING id, organization_id, project_id, flow_template_id, display_name, cron_expression, overlap_policy, max_duration_ms, is_enabled, last_fired_at, next_fire_at, created_by_type, created_by_id, created_at, updated_at
	`,
		schedule.ID,
		schedule.FlowTemplateID,
		schedule.DisplayName,
		schedule.CronExpression,
		defaultOverlapPolicy(schedule.OverlapPolicy),
		schedule.MaxDurationMS,
		schedule.NextFireAt,
	)

	updated, err := scanTaskSchedule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return TaskSchedule{}, ErrNotFound
	}
	if err != nil {
		return TaskSchedule{}, mapDBError(err)
	}
	return updated, nil
}

func (r *TaskScheduleRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `
		DELETE FROM task_schedule
		WHERE id = $1
	`, id)
	if err != nil {
		return mapDBError(err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *TaskScheduleRepo) Enable(ctx context.Context, id uuid.UUID) (TaskSchedule, error) {
	return r.setEnabled(ctx, id, true)
}

func (r *TaskScheduleRepo) Disable(ctx context.Context, id uuid.UUID) (TaskSchedule, error) {
	return r.setEnabled(ctx, id, false)
}

func (r *TaskScheduleRepo) setEnabled(ctx context.Context, id uuid.UUID, enabled bool) (TaskSchedule, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE task_schedule
		SET is_enabled = $2
		WHERE id = $1
		RETURNING id, organization_id, project_id, flow_template_id, display_name, cron_expression, overlap_policy, max_duration_ms, is_enabled, last_fired_at, next_fire_at, created_by_type, created_by_id, created_at, updated_at
	`, id, enabled)

	schedule, err := scanTaskSchedule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return TaskSchedule{}, ErrNotFound
	}
	if err != nil {
		return TaskSchedule{}, mapDBError(err)
	}
	return schedule, nil
}

func (r *TaskScheduleRepo) UpdateLastFired(ctx context.Context, id uuid.UUID, lastFiredAt time.Time, nextFireAt *time.Time) (TaskSchedule, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE task_schedule
		SET
			last_fired_at = $2,
			next_fire_at = $3
		WHERE id = $1
		RETURNING id, organization_id, project_id, flow_template_id, display_name, cron_expression, overlap_policy, max_duration_ms, is_enabled, last_fired_at, next_fire_at, created_by_type, created_by_id, created_at, updated_at
	`, id, lastFiredAt.UTC(), nextFireAt)

	updated, err := scanTaskSchedule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return TaskSchedule{}, ErrNotFound
	}
	if err != nil {
		return TaskSchedule{}, mapDBError(err)
	}
	return updated, nil
}

func (r *TaskScheduleRepo) ListDue(ctx context.Context, now time.Time, limit int) ([]TaskSchedule, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, project_id, flow_template_id, display_name, cron_expression, overlap_policy, max_duration_ms, is_enabled, last_fired_at, next_fire_at, created_by_type, created_by_id, created_at, updated_at
		FROM task_schedule
		WHERE is_enabled = true
		  AND next_fire_at IS NOT NULL
		  AND next_fire_at <= $1
		ORDER BY next_fire_at ASC, id ASC
		LIMIT $2
	`, now.UTC(), limit)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	due := make([]TaskSchedule, 0)
	for rows.Next() {
		schedule, scanErr := scanTaskSchedule(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		due = append(due, schedule)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return due, nil
}

func normalizeProjectSettingsJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`{}`)
	}
	return value
}

func defaultVersion(version int) int {
	if version <= 0 {
		return 1
	}
	return version
}

func defaultCurrent(isCurrent bool) bool {
	if !isCurrent {
		return true
	}
	return isCurrent
}

func defaultEnabled(enabled bool) bool {
	return enabled
}

func defaultOverlapPolicy(policy string) string {
	trimmed := strings.TrimSpace(policy)
	if trimmed == "" {
		return "skip"
	}
	return trimmed
}

func scanProject(row pgx.Row) (Project, error) {
	var project Project
	if err := row.Scan(
		&project.ID,
		&project.OrganizationID,
		&project.Slug,
		&project.DisplayName,
		&project.Description,
		&project.DeliveryMode,
		&project.DeployFlowTemplateID,
		&project.Settings,
		&project.CreatedByType,
		&project.CreatedByID,
		&project.CreatedAt,
		&project.UpdatedAt,
	); err != nil {
		return Project{}, err
	}
	if len(project.Settings) == 0 {
		project.Settings = json.RawMessage(`{}`)
	}
	return project, nil
}

func scanFlowTemplate(row pgx.Row) (FlowTemplate, error) {
	var template FlowTemplate
	if err := row.Scan(
		&template.ID,
		&template.OrganizationID,
		&template.ProjectID,
		&template.Slug,
		&template.DisplayName,
		&template.Description,
		&template.IsCurrent,
		&template.Version,
		&template.StartNodeID,
		&template.IsSystem,
		&template.CreatedByType,
		&template.CreatedByID,
		&template.CreatedAt,
		&template.UpdatedAt,
	); err != nil {
		return FlowTemplate{}, err
	}
	return template, nil
}

func scanTaskSchedule(row pgx.Row) (TaskSchedule, error) {
	var schedule TaskSchedule
	if err := row.Scan(
		&schedule.ID,
		&schedule.OrganizationID,
		&schedule.ProjectID,
		&schedule.FlowTemplateID,
		&schedule.DisplayName,
		&schedule.CronExpression,
		&schedule.OverlapPolicy,
		&schedule.MaxDurationMS,
		&schedule.IsEnabled,
		&schedule.LastFiredAt,
		&schedule.NextFireAt,
		&schedule.CreatedByType,
		&schedule.CreatedByID,
		&schedule.CreatedAt,
		&schedule.UpdatedAt,
	); err != nil {
		return TaskSchedule{}, err
	}
	return schedule, nil
}
