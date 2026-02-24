package repo

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ModelProfileAssignment struct {
	ID                uuid.UUID
	OrganizationID    uuid.UUID
	ScopeType         string
	ScopeID           uuid.UUID
	LogicalProfileID  string
	InvocationPurpose string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type ModelProfileAssignmentRepo struct {
	pool *pgxpool.Pool
}

func NewModelProfileAssignmentRepo(pool *pgxpool.Pool) *ModelProfileAssignmentRepo {
	return &ModelProfileAssignmentRepo{pool: pool}
}

func (r *ModelProfileAssignmentRepo) Upsert(ctx context.Context, assignment ModelProfileAssignment) (ModelProfileAssignment, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO model_profile_assignment (
			organization_id,
			scope_type,
			scope_id,
			logical_profile_id,
			invocation_purpose
		)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (organization_id, scope_type, scope_id, invocation_purpose)
		DO UPDATE SET
			logical_profile_id = EXCLUDED.logical_profile_id
		RETURNING id, organization_id, scope_type, scope_id, logical_profile_id, invocation_purpose, created_at, updated_at
	`, assignment.OrganizationID, assignment.ScopeType, assignment.ScopeID, assignment.LogicalProfileID, assignment.InvocationPurpose)

	updated, err := scanModelProfileAssignment(row)
	if err != nil {
		return ModelProfileAssignment{}, mapDBError(err)
	}
	return updated, nil
}

func (r *ModelProfileAssignmentRepo) GetByScope(ctx context.Context, organizationID uuid.UUID, scopeType string, scopeID uuid.UUID, invocationPurpose string) (ModelProfileAssignment, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, scope_type, scope_id, logical_profile_id, invocation_purpose, created_at, updated_at
		FROM model_profile_assignment
		WHERE organization_id = $1
		  AND scope_type = $2
		  AND scope_id = $3
		  AND invocation_purpose = $4
	`, organizationID, scopeType, scopeID, invocationPurpose)

	assignment, err := scanModelProfileAssignment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelProfileAssignment{}, ErrNotFound
	}
	if err != nil {
		return ModelProfileAssignment{}, mapDBError(err)
	}
	return assignment, nil
}

func (r *ModelProfileAssignmentRepo) ListByOrg(ctx context.Context, organizationID uuid.UUID) ([]ModelProfileAssignment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, scope_type, scope_id, logical_profile_id, invocation_purpose, created_at, updated_at
		FROM model_profile_assignment
		WHERE organization_id = $1
		ORDER BY created_at
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ModelProfileAssignment, 0)
	for rows.Next() {
		assignment, scanErr := scanModelProfileAssignment(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, assignment)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *ModelProfileAssignmentRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM model_profile_assignment
		WHERE id = $1
	`, id)
	if err != nil {
		return mapDBError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanModelProfileAssignment(row pgx.Row) (ModelProfileAssignment, error) {
	var assignment ModelProfileAssignment
	if err := row.Scan(
		&assignment.ID,
		&assignment.OrganizationID,
		&assignment.ScopeType,
		&assignment.ScopeID,
		&assignment.LogicalProfileID,
		&assignment.InvocationPurpose,
		&assignment.CreatedAt,
		&assignment.UpdatedAt,
	); err != nil {
		return ModelProfileAssignment{}, err
	}
	return assignment, nil
}
