package repo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TokenBudget struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	ProjectID        *uuid.UUID
	Period           string
	SoftLimitTokens  *int64
	HardLimitTokens  *int64
	AlertChannel     *string
	IsEnabled        bool
	LastWarnedPeriod *string
	CreatedByType    string
	CreatedByID      uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type TokenBudgetRepo struct {
	pool *pgxpool.Pool
}

func NewTokenBudgetRepo(pool *pgxpool.Pool) *TokenBudgetRepo {
	return &TokenBudgetRepo{pool: pool}
}

func (r *TokenBudgetRepo) Create(ctx context.Context, budget TokenBudget) (TokenBudget, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO token_budget (
			organization_id,
			project_id,
			period,
			soft_limit_tokens,
			hard_limit_tokens,
			alert_channel,
			is_enabled,
			last_warned_period,
			created_by_type,
			created_by_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING
			id,
			organization_id,
			project_id,
			period,
			soft_limit_tokens,
			hard_limit_tokens,
			alert_channel,
			is_enabled,
			last_warned_period,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
	`,
		budget.OrganizationID,
		budget.ProjectID,
		normalizeBudgetPeriod(budget.Period),
		budget.SoftLimitTokens,
		budget.HardLimitTokens,
		trimStringPointer(budget.AlertChannel),
		budget.IsEnabled,
		trimStringPointer(budget.LastWarnedPeriod),
		strings.TrimSpace(budget.CreatedByType),
		budget.CreatedByID,
	)

	created, err := scanTokenBudget(row)
	if err != nil {
		return TokenBudget{}, mapDBError(err)
	}
	return created, nil
}

func (r *TokenBudgetRepo) GetByID(ctx context.Context, id uuid.UUID) (TokenBudget, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			id,
			organization_id,
			project_id,
			period,
			soft_limit_tokens,
			hard_limit_tokens,
			alert_channel,
			is_enabled,
			last_warned_period,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM token_budget
		WHERE id = $1
	`, id)

	budget, err := scanTokenBudget(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenBudget{}, ErrNotFound
	}
	if err != nil {
		return TokenBudget{}, mapDBError(err)
	}
	return budget, nil
}

func (r *TokenBudgetRepo) GetByScope(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, period string) (TokenBudget, error) {
	normalizedPeriod := normalizeBudgetPeriod(period)

	var row pgx.Row
	if projectID == nil {
		row = r.pool.QueryRow(ctx, `
			SELECT
				id,
				organization_id,
				project_id,
				period,
				soft_limit_tokens,
				hard_limit_tokens,
				alert_channel,
				is_enabled,
				last_warned_period,
				created_by_type,
				created_by_id,
				created_at,
				updated_at
			FROM token_budget
			WHERE organization_id = $1
			  AND project_id IS NULL
			  AND period = $2
		`, orgID, normalizedPeriod)
	} else {
		row = r.pool.QueryRow(ctx, `
			SELECT
				id,
				organization_id,
				project_id,
				period,
				soft_limit_tokens,
				hard_limit_tokens,
				alert_channel,
				is_enabled,
				last_warned_period,
				created_by_type,
				created_by_id,
				created_at,
				updated_at
			FROM token_budget
			WHERE organization_id = $1
			  AND project_id = $2
			  AND period = $3
		`, orgID, *projectID, normalizedPeriod)
	}

	budget, err := scanTokenBudget(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenBudget{}, ErrNotFound
	}
	if err != nil {
		return TokenBudget{}, mapDBError(err)
	}
	return budget, nil
}

func (r *TokenBudgetRepo) List(ctx context.Context, orgID uuid.UUID) ([]TokenBudget, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			organization_id,
			project_id,
			period,
			soft_limit_tokens,
			hard_limit_tokens,
			alert_channel,
			is_enabled,
			last_warned_period,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM token_budget
		WHERE organization_id = $1
		ORDER BY project_id NULLS FIRST, period ASC, created_at ASC
	`, orgID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	budgets := make([]TokenBudget, 0)
	for rows.Next() {
		budget, scanErr := scanTokenBudget(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		budgets = append(budgets, budget)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return budgets, nil
}

func (r *TokenBudgetRepo) ListAll(ctx context.Context) ([]TokenBudget, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			organization_id,
			project_id,
			period,
			soft_limit_tokens,
			hard_limit_tokens,
			alert_channel,
			is_enabled,
			last_warned_period,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM token_budget
		ORDER BY organization_id, project_id NULLS FIRST, period ASC, created_at ASC
	`)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	budgets := make([]TokenBudget, 0)
	for rows.Next() {
		budget, scanErr := scanTokenBudget(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		budgets = append(budgets, budget)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return budgets, nil
}

func (r *TokenBudgetRepo) Update(ctx context.Context, budget TokenBudget) (TokenBudget, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE token_budget
		SET
			organization_id = $2,
			project_id = $3,
			period = $4,
			soft_limit_tokens = $5,
			hard_limit_tokens = $6,
			alert_channel = $7,
			is_enabled = $8,
			last_warned_period = $9
		WHERE id = $1
		RETURNING
			id,
			organization_id,
			project_id,
			period,
			soft_limit_tokens,
			hard_limit_tokens,
			alert_channel,
			is_enabled,
			last_warned_period,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
	`,
		budget.ID,
		budget.OrganizationID,
		budget.ProjectID,
		normalizeBudgetPeriod(budget.Period),
		budget.SoftLimitTokens,
		budget.HardLimitTokens,
		trimStringPointer(budget.AlertChannel),
		budget.IsEnabled,
		trimStringPointer(budget.LastWarnedPeriod),
	)

	updated, err := scanTokenBudget(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenBudget{}, ErrNotFound
	}
	if err != nil {
		return TokenBudget{}, mapDBError(err)
	}
	return updated, nil
}

func (r *TokenBudgetRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `
		DELETE FROM token_budget
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

func scanTokenBudget(scanner interface{ Scan(dest ...any) error }) (TokenBudget, error) {
	var budget TokenBudget
	if err := scanner.Scan(
		&budget.ID,
		&budget.OrganizationID,
		&budget.ProjectID,
		&budget.Period,
		&budget.SoftLimitTokens,
		&budget.HardLimitTokens,
		&budget.AlertChannel,
		&budget.IsEnabled,
		&budget.LastWarnedPeriod,
		&budget.CreatedByType,
		&budget.CreatedByID,
		&budget.CreatedAt,
		&budget.UpdatedAt,
	); err != nil {
		return TokenBudget{}, err
	}
	return budget, nil
}

func normalizeBudgetPeriod(period string) string {
	return strings.ToLower(strings.TrimSpace(period))
}

func trimStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}
