package repo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const activePMUniqueIndexName = "agent_project_assignment_active_pm_unique_idx"

var ErrPMConflict = errors.New("repo: pm conflict")

type dbExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type AgentProjectAssignment struct {
	ID             uuid.UUID
	AgentID        uuid.UUID
	ProjectID      uuid.UUID
	Role           string
	AssignedByType string
	AssignedByID   *uuid.UUID
	IsActive       bool
	AssignedAt     time.Time
	DeactivatedAt  *time.Time
}

type AgentProjectAssignmentRepo struct {
	pool *pgxpool.Pool
}

func NewAgentProjectAssignmentRepo(pool *pgxpool.Pool) *AgentProjectAssignmentRepo {
	return &AgentProjectAssignmentRepo{pool: pool}
}

func (r *AgentProjectAssignmentRepo) Assign(ctx context.Context, assignment AgentProjectAssignment) (AgentProjectAssignment, error) {
	return r.assign(ctx, r.pool, assignment)
}

func (r *AgentProjectAssignmentRepo) AssignTx(ctx context.Context, tx pgx.Tx, assignment AgentProjectAssignment) (AgentProjectAssignment, error) {
	return r.assign(ctx, tx, assignment)
}

func (r *AgentProjectAssignmentRepo) assign(ctx context.Context, db dbExecutor, assignment AgentProjectAssignment) (AgentProjectAssignment, error) {
	role := normalizeProjectAssignmentRole(assignment.Role)
	if role == "" {
		role = strings.ToLower(strings.TrimSpace(assignment.Role))
	}

	row := db.QueryRow(ctx, `
		INSERT INTO agent_project_assignment (
			agent_id,
			project_id,
			role,
			assigned_by_type,
			assigned_by_id,
			is_active,
			assigned_at,
			deactivated_at
		)
		VALUES ($1, $2, $3, $4, $5, true, now(), NULL)
		ON CONFLICT (agent_id, project_id)
		DO UPDATE
		SET
			role = EXCLUDED.role,
			assigned_by_type = EXCLUDED.assigned_by_type,
			assigned_by_id = EXCLUDED.assigned_by_id,
			is_active = true,
			assigned_at = now(),
			deactivated_at = NULL
		RETURNING
			id,
			agent_id,
			project_id,
			role,
			assigned_by_type,
			assigned_by_id,
			is_active,
			assigned_at,
			deactivated_at
	`,
		assignment.AgentID,
		assignment.ProjectID,
		role,
		assignment.AssignedByType,
		assignment.AssignedByID,
	)

	item, err := scanAgentProjectAssignment(row)
	if err != nil {
		return AgentProjectAssignment{}, mapAgentProjectAssignmentError(err)
	}
	return item, nil
}

func (r *AgentProjectAssignmentRepo) Deactivate(ctx context.Context, agentID, projectID uuid.UUID) (AgentProjectAssignment, error) {
	return r.deactivate(ctx, r.pool, agentID, projectID)
}

func (r *AgentProjectAssignmentRepo) DeactivateTx(ctx context.Context, tx pgx.Tx, agentID, projectID uuid.UUID) (AgentProjectAssignment, error) {
	return r.deactivate(ctx, tx, agentID, projectID)
}

func (r *AgentProjectAssignmentRepo) deactivate(ctx context.Context, db dbExecutor, agentID, projectID uuid.UUID) (AgentProjectAssignment, error) {
	row := db.QueryRow(ctx, `
		UPDATE agent_project_assignment
		SET
			is_active = false,
			deactivated_at = now()
		WHERE agent_id = $1
		  AND project_id = $2
		  AND is_active = true
		RETURNING
			id,
			agent_id,
			project_id,
			role,
			assigned_by_type,
			assigned_by_id,
			is_active,
			assigned_at,
			deactivated_at
	`, agentID, projectID)

	item, err := scanAgentProjectAssignment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentProjectAssignment{}, ErrNotFound
	}
	if err != nil {
		return AgentProjectAssignment{}, mapDBError(err)
	}
	return item, nil
}

func (r *AgentProjectAssignmentRepo) GetByAgentAndProject(ctx context.Context, agentID, projectID uuid.UUID) (AgentProjectAssignment, error) {
	return r.getByAgentAndProject(ctx, r.pool, agentID, projectID)
}

func (r *AgentProjectAssignmentRepo) GetByAgentAndProjectTx(ctx context.Context, tx pgx.Tx, agentID, projectID uuid.UUID) (AgentProjectAssignment, error) {
	return r.getByAgentAndProject(ctx, tx, agentID, projectID)
}

func (r *AgentProjectAssignmentRepo) getByAgentAndProject(ctx context.Context, db dbExecutor, agentID, projectID uuid.UUID) (AgentProjectAssignment, error) {
	row := db.QueryRow(ctx, `
		SELECT
			id,
			agent_id,
			project_id,
			role,
			assigned_by_type,
			assigned_by_id,
			is_active,
			assigned_at,
			deactivated_at
		FROM agent_project_assignment
		WHERE agent_id = $1
		  AND project_id = $2
	`, agentID, projectID)

	item, err := scanAgentProjectAssignment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentProjectAssignment{}, ErrNotFound
	}
	if err != nil {
		return AgentProjectAssignment{}, mapDBError(err)
	}
	return item, nil
}

func (r *AgentProjectAssignmentRepo) ListByAgent(ctx context.Context, agentID uuid.UUID) ([]AgentProjectAssignment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			agent_id,
			project_id,
			role,
			assigned_by_type,
			assigned_by_id,
			is_active,
			assigned_at,
			deactivated_at
		FROM agent_project_assignment
		WHERE agent_id = $1
		  AND is_active = true
		ORDER BY assigned_at DESC, id DESC
	`, agentID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]AgentProjectAssignment, 0)
	for rows.Next() {
		item, scanErr := scanAgentProjectAssignment(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *AgentProjectAssignmentRepo) ListByProject(ctx context.Context, projectID uuid.UUID) ([]AgentProjectAssignment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			agent_id,
			project_id,
			role,
			assigned_by_type,
			assigned_by_id,
			is_active,
			assigned_at,
			deactivated_at
		FROM agent_project_assignment
		WHERE project_id = $1
		  AND is_active = true
		ORDER BY role ASC, assigned_at DESC, id DESC
	`, projectID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]AgentProjectAssignment, 0)
	for rows.Next() {
		item, scanErr := scanAgentProjectAssignment(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *AgentProjectAssignmentRepo) GetPM(ctx context.Context, projectID uuid.UUID) (AgentProjectAssignment, error) {
	return r.getPM(ctx, r.pool, projectID)
}

func (r *AgentProjectAssignmentRepo) GetPMTx(ctx context.Context, tx pgx.Tx, projectID uuid.UUID) (AgentProjectAssignment, error) {
	return r.getPM(ctx, tx, projectID)
}

func (r *AgentProjectAssignmentRepo) getPM(ctx context.Context, db dbExecutor, projectID uuid.UUID) (AgentProjectAssignment, error) {
	row := db.QueryRow(ctx, `
		SELECT
			id,
			agent_id,
			project_id,
			role,
			assigned_by_type,
			assigned_by_id,
			is_active,
			assigned_at,
			deactivated_at
		FROM agent_project_assignment
		WHERE project_id = $1
		  AND role = 'project_manager'
		  AND is_active = true
		LIMIT 1
	`, projectID)

	item, err := scanAgentProjectAssignment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentProjectAssignment{}, ErrNotFound
	}
	if err != nil {
		return AgentProjectAssignment{}, mapDBError(err)
	}
	return item, nil
}

type AgentSkillAttachment struct {
	ID             uuid.UUID
	AgentID        uuid.UUID
	SkillID        uuid.UUID
	Priority       int
	AttachedByType string
	AttachedByID   *uuid.UUID
	IsActive       bool
	AttachedAt     time.Time
	DeactivatedAt  *time.Time
}

type AgentSkillAttachmentRepo struct {
	pool *pgxpool.Pool
}

func NewAgentSkillAttachmentRepo(pool *pgxpool.Pool) *AgentSkillAttachmentRepo {
	return &AgentSkillAttachmentRepo{pool: pool}
}

func (r *AgentSkillAttachmentRepo) Attach(ctx context.Context, attachment AgentSkillAttachment) (AgentSkillAttachment, error) {
	return r.attach(ctx, r.pool, attachment)
}

func (r *AgentSkillAttachmentRepo) AttachTx(ctx context.Context, tx pgx.Tx, attachment AgentSkillAttachment) (AgentSkillAttachment, error) {
	return r.attach(ctx, tx, attachment)
}

func (r *AgentSkillAttachmentRepo) attach(ctx context.Context, db dbExecutor, attachment AgentSkillAttachment) (AgentSkillAttachment, error) {
	row := db.QueryRow(ctx, `
		INSERT INTO agent_skill_attachment (
			agent_id,
			skill_id,
			priority,
			attached_by_type,
			attached_by_id,
			is_active,
			attached_at,
			deactivated_at
		)
		VALUES ($1, $2, $3, $4, $5, true, now(), NULL)
		ON CONFLICT (agent_id, skill_id)
		DO UPDATE
		SET
			priority = EXCLUDED.priority,
			attached_by_type = EXCLUDED.attached_by_type,
			attached_by_id = EXCLUDED.attached_by_id,
			is_active = true,
			attached_at = now(),
			deactivated_at = NULL
		RETURNING
			id,
			agent_id,
			skill_id,
			priority,
			attached_by_type,
			attached_by_id,
			is_active,
			attached_at,
			deactivated_at
	`,
		attachment.AgentID,
		attachment.SkillID,
		attachment.Priority,
		attachment.AttachedByType,
		attachment.AttachedByID,
	)

	item, err := scanAgentSkillAttachment(row)
	if err != nil {
		return AgentSkillAttachment{}, mapDBError(err)
	}
	return item, nil
}

func (r *AgentSkillAttachmentRepo) Detach(ctx context.Context, agentID, skillID uuid.UUID) (AgentSkillAttachment, error) {
	return r.detach(ctx, r.pool, agentID, skillID)
}

func (r *AgentSkillAttachmentRepo) DetachTx(ctx context.Context, tx pgx.Tx, agentID, skillID uuid.UUID) (AgentSkillAttachment, error) {
	return r.detach(ctx, tx, agentID, skillID)
}

func (r *AgentSkillAttachmentRepo) detach(ctx context.Context, db dbExecutor, agentID, skillID uuid.UUID) (AgentSkillAttachment, error) {
	row := db.QueryRow(ctx, `
		UPDATE agent_skill_attachment
		SET
			is_active = false,
			deactivated_at = now()
		WHERE agent_id = $1
		  AND skill_id = $2
		  AND is_active = true
		RETURNING
			id,
			agent_id,
			skill_id,
			priority,
			attached_by_type,
			attached_by_id,
			is_active,
			attached_at,
			deactivated_at
	`, agentID, skillID)

	item, err := scanAgentSkillAttachment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentSkillAttachment{}, ErrNotFound
	}
	if err != nil {
		return AgentSkillAttachment{}, mapDBError(err)
	}
	return item, nil
}

func (r *AgentSkillAttachmentRepo) ListByAgent(ctx context.Context, agentID uuid.UUID) ([]AgentSkillAttachment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			agent_id,
			skill_id,
			priority,
			attached_by_type,
			attached_by_id,
			is_active,
			attached_at,
			deactivated_at
		FROM agent_skill_attachment
		WHERE agent_id = $1
		  AND is_active = true
		ORDER BY priority ASC, id ASC
	`, agentID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]AgentSkillAttachment, 0)
	for rows.Next() {
		item, scanErr := scanAgentSkillAttachment(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *AgentSkillAttachmentRepo) ListBySkill(ctx context.Context, skillID uuid.UUID) ([]AgentSkillAttachment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			agent_id,
			skill_id,
			priority,
			attached_by_type,
			attached_by_id,
			is_active,
			attached_at,
			deactivated_at
		FROM agent_skill_attachment
		WHERE skill_id = $1
		  AND is_active = true
		ORDER BY priority ASC, id ASC
	`, skillID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]AgentSkillAttachment, 0)
	for rows.Next() {
		item, scanErr := scanAgentSkillAttachment(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *AgentSkillAttachmentRepo) SetPriority(ctx context.Context, agentID, skillID uuid.UUID, priority int) (AgentSkillAttachment, error) {
	return r.setPriority(ctx, r.pool, agentID, skillID, priority)
}

func (r *AgentSkillAttachmentRepo) SetPriorityTx(ctx context.Context, tx pgx.Tx, agentID, skillID uuid.UUID, priority int) (AgentSkillAttachment, error) {
	return r.setPriority(ctx, tx, agentID, skillID, priority)
}

func (r *AgentSkillAttachmentRepo) setPriority(ctx context.Context, db dbExecutor, agentID, skillID uuid.UUID, priority int) (AgentSkillAttachment, error) {
	row := db.QueryRow(ctx, `
		UPDATE agent_skill_attachment
		SET priority = $3
		WHERE agent_id = $1
		  AND skill_id = $2
		RETURNING
			id,
			agent_id,
			skill_id,
			priority,
			attached_by_type,
			attached_by_id,
			is_active,
			attached_at,
			deactivated_at
	`, agentID, skillID, priority)

	item, err := scanAgentSkillAttachment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentSkillAttachment{}, ErrNotFound
	}
	if err != nil {
		return AgentSkillAttachment{}, mapDBError(err)
	}
	return item, nil
}

func (r *AgentSkillAttachmentRepo) GetByAgentAndSkill(ctx context.Context, agentID, skillID uuid.UUID) (AgentSkillAttachment, error) {
	return r.getByAgentAndSkill(ctx, r.pool, agentID, skillID)
}

func (r *AgentSkillAttachmentRepo) GetByAgentAndSkillTx(ctx context.Context, tx pgx.Tx, agentID, skillID uuid.UUID) (AgentSkillAttachment, error) {
	return r.getByAgentAndSkill(ctx, tx, agentID, skillID)
}

func (r *AgentSkillAttachmentRepo) getByAgentAndSkill(ctx context.Context, db dbExecutor, agentID, skillID uuid.UUID) (AgentSkillAttachment, error) {
	row := db.QueryRow(ctx, `
		SELECT
			id,
			agent_id,
			skill_id,
			priority,
			attached_by_type,
			attached_by_id,
			is_active,
			attached_at,
			deactivated_at
		FROM agent_skill_attachment
		WHERE agent_id = $1
		  AND skill_id = $2
	`, agentID, skillID)

	item, err := scanAgentSkillAttachment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentSkillAttachment{}, ErrNotFound
	}
	if err != nil {
		return AgentSkillAttachment{}, mapDBError(err)
	}
	return item, nil
}

func scanAgentProjectAssignment(row pgx.Row) (AgentProjectAssignment, error) {
	var item AgentProjectAssignment
	if err := row.Scan(
		&item.ID,
		&item.AgentID,
		&item.ProjectID,
		&item.Role,
		&item.AssignedByType,
		&item.AssignedByID,
		&item.IsActive,
		&item.AssignedAt,
		&item.DeactivatedAt,
	); err != nil {
		return AgentProjectAssignment{}, err
	}
	return item, nil
}

func scanAgentSkillAttachment(row pgx.Row) (AgentSkillAttachment, error) {
	var item AgentSkillAttachment
	if err := row.Scan(
		&item.ID,
		&item.AgentID,
		&item.SkillID,
		&item.Priority,
		&item.AttachedByType,
		&item.AttachedByID,
		&item.IsActive,
		&item.AttachedAt,
		&item.DeactivatedAt,
	); err != nil {
		return AgentSkillAttachment{}, err
	}
	return item, nil
}

func mapAgentProjectAssignmentError(err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == activePMUniqueIndexName {
		return ErrPMConflict
	}

	return mapDBError(err)
}

func normalizeProjectAssignmentRole(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pm", "project_manager":
		return "project_manager"
	case "worker", "reviewer", "observer":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}
