package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CapabilityPolicy struct {
	ID             uuid.UUID
	PolicyLayer    string
	OrganizationID *uuid.UUID
	ProjectID      *uuid.UUID
	AgentID        *uuid.UUID
	Capability     string
	Effect         string
	Conditions     json.RawMessage
	Priority       int
	CreatedByType  string
	CreatedByID    *uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type CapabilityPolicyRepo struct {
	pool *pgxpool.Pool
}

func NewCapabilityPolicyRepo(pool *pgxpool.Pool) *CapabilityPolicyRepo {
	return &CapabilityPolicyRepo{pool: pool}
}

func (r *CapabilityPolicyRepo) Create(ctx context.Context, policy CapabilityPolicy) (CapabilityPolicy, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO capability_policy (
			policy_layer,
			organization_id,
			project_id,
			agent_id,
			capability,
			effect,
			conditions,
			priority,
			created_by_type,
			created_by_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10)
		RETURNING
			id,
			policy_layer,
			organization_id,
			project_id,
			agent_id,
			capability,
			effect,
			conditions,
			priority,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
	`,
		strings.TrimSpace(policy.PolicyLayer),
		policy.OrganizationID,
		policy.ProjectID,
		policy.AgentID,
		strings.TrimSpace(policy.Capability),
		strings.TrimSpace(policy.Effect),
		normalizePolicyConditions(policy.Conditions),
		defaultPolicyPriority(policy.Priority),
		strings.TrimSpace(policy.CreatedByType),
		policy.CreatedByID,
	)

	created, err := scanCapabilityPolicy(row)
	if err != nil {
		return CapabilityPolicy{}, mapDBError(err)
	}
	return created, nil
}

func (r *CapabilityPolicyRepo) GetByID(ctx context.Context, id uuid.UUID) (CapabilityPolicy, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			id,
			policy_layer,
			organization_id,
			project_id,
			agent_id,
			capability,
			effect,
			conditions,
			priority,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM capability_policy
		WHERE id = $1
	`, id)

	policy, err := scanCapabilityPolicy(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return CapabilityPolicy{}, ErrNotFound
	}
	if err != nil {
		return CapabilityPolicy{}, mapDBError(err)
	}
	return policy, nil
}

func (r *CapabilityPolicyRepo) ListByLayer(ctx context.Context, layer string, capability string, organizationID, projectID, agentID *uuid.UUID) ([]CapabilityPolicy, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			policy_layer,
			organization_id,
			project_id,
			agent_id,
			capability,
			effect,
			conditions,
			priority,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM capability_policy
		WHERE policy_layer = $1
		  AND ($2::text = '' OR capability = $2)
		  AND (
		  	($1 = 'instance')
		  	OR (($3::uuid IS NULL AND organization_id IS NULL) OR organization_id = $3)
		  )
		  AND (
		  	$1 <> 'project'
		  	OR project_id = $4
		  )
		  AND (
		  	$1 <> 'agent_profile'
		  	OR agent_id = $5
		  )
		ORDER BY priority ASC, created_at ASC, id ASC
	`,
		strings.TrimSpace(layer),
		strings.TrimSpace(capability),
		organizationID,
		projectID,
		agentID,
	)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	policies := make([]CapabilityPolicy, 0)
	for rows.Next() {
		item, scanErr := scanCapabilityPolicy(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		policies = append(policies, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return policies, nil
}

func (r *CapabilityPolicyRepo) ListForEvaluation(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, agentID *uuid.UUID, capability string) ([]CapabilityPolicy, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			policy_layer,
			organization_id,
			project_id,
			agent_id,
			capability,
			effect,
			conditions,
			priority,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM capability_policy
		WHERE capability = $1
		  AND (
		  	policy_layer = 'instance'
		  	OR (policy_layer = 'org' AND organization_id = $2)
		  	OR (policy_layer = 'project' AND $3::uuid IS NOT NULL AND project_id = $3)
		  	OR (policy_layer = 'agent_profile' AND $4::uuid IS NOT NULL AND agent_id = $4)
		  	OR (
		  		policy_layer = 'request'
		  		AND organization_id = $2
		  		AND (project_id IS NULL OR project_id = $3)
		  		AND (agent_id IS NULL OR agent_id = $4)
		  	)
		  )
		ORDER BY
			CASE policy_layer
				WHEN 'instance' THEN 1
				WHEN 'org' THEN 2
				WHEN 'project' THEN 3
				WHEN 'agent_profile' THEN 4
				WHEN 'request' THEN 5
				ELSE 6
			END ASC,
			priority ASC,
			created_at ASC,
			id ASC
	`, strings.TrimSpace(capability), orgID, projectID, agentID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	policies := make([]CapabilityPolicy, 0)
	for rows.Next() {
		item, scanErr := scanCapabilityPolicy(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		policies = append(policies, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return policies, nil
}

func (r *CapabilityPolicyRepo) Update(ctx context.Context, policy CapabilityPolicy) (CapabilityPolicy, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE capability_policy
		SET
			policy_layer = $2,
			organization_id = $3,
			project_id = $4,
			agent_id = $5,
			capability = $6,
			effect = $7,
			conditions = $8::jsonb,
			priority = $9
		WHERE id = $1
		RETURNING
			id,
			policy_layer,
			organization_id,
			project_id,
			agent_id,
			capability,
			effect,
			conditions,
			priority,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
	`,
		policy.ID,
		strings.TrimSpace(policy.PolicyLayer),
		policy.OrganizationID,
		policy.ProjectID,
		policy.AgentID,
		strings.TrimSpace(policy.Capability),
		strings.TrimSpace(policy.Effect),
		normalizePolicyConditions(policy.Conditions),
		defaultPolicyPriority(policy.Priority),
	)

	updated, err := scanCapabilityPolicy(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return CapabilityPolicy{}, ErrNotFound
	}
	if err != nil {
		return CapabilityPolicy{}, mapDBError(err)
	}
	return updated, nil
}

func (r *CapabilityPolicyRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `
		DELETE FROM capability_policy
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

func scanCapabilityPolicy(row pgx.Row) (CapabilityPolicy, error) {
	var policy CapabilityPolicy
	if err := row.Scan(
		&policy.ID,
		&policy.PolicyLayer,
		&policy.OrganizationID,
		&policy.ProjectID,
		&policy.AgentID,
		&policy.Capability,
		&policy.Effect,
		&policy.Conditions,
		&policy.Priority,
		&policy.CreatedByType,
		&policy.CreatedByID,
		&policy.CreatedAt,
		&policy.UpdatedAt,
	); err != nil {
		return CapabilityPolicy{}, err
	}
	policy.Conditions = normalizePolicyConditions(policy.Conditions)
	return policy, nil
}

func normalizePolicyConditions(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

func defaultPolicyPriority(priority int) int {
	if priority <= 0 {
		return 100
	}
	return priority
}
