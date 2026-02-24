package repo

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ModelUsageRollup struct {
	ID                   uuid.UUID
	OrganizationID       uuid.UUID
	RollupDate           time.Time
	RollupType           string
	RollupID             *uuid.UUID
	ModelName            *string
	InvocationPurpose    *string
	TotalInvocations     int
	TotalInputTokens     int64
	TotalOutputTokens    int64
	TotalCacheReadTokens int64
	TotalLatencyMS       int64
	TotalCostMicrocents  int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type ModelUsageRollupRepo struct {
	pool *pgxpool.Pool
}

func NewModelUsageRollupRepo(pool *pgxpool.Pool) *ModelUsageRollupRepo {
	return &ModelUsageRollupRepo{pool: pool}
}

func (r *ModelUsageRollupRepo) Upsert(ctx context.Context, rollup ModelUsageRollup) (ModelUsageRollup, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO model_usage_rollup (
			organization_id,
			rollup_date,
			rollup_type,
			rollup_id,
			model_name,
			invocation_purpose,
			total_invocations,
			total_input_tokens,
			total_output_tokens,
			total_cache_read_tokens,
			total_latency_ms,
			total_cost_microcents
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
		)
		ON CONFLICT (
			organization_id,
			rollup_date,
			rollup_type,
			rollup_id,
			model_name,
			invocation_purpose
		)
		DO UPDATE SET
			total_invocations = EXCLUDED.total_invocations,
			total_input_tokens = EXCLUDED.total_input_tokens,
			total_output_tokens = EXCLUDED.total_output_tokens,
			total_cache_read_tokens = EXCLUDED.total_cache_read_tokens,
			total_latency_ms = EXCLUDED.total_latency_ms,
			total_cost_microcents = EXCLUDED.total_cost_microcents,
			updated_at = now()
		RETURNING id, organization_id, rollup_date, rollup_type, rollup_id, model_name, invocation_purpose,
		          total_invocations, total_input_tokens, total_output_tokens, total_cache_read_tokens,
		          total_latency_ms, total_cost_microcents, created_at, updated_at
	`, rollup.OrganizationID, rollup.RollupDate.UTC(), rollup.RollupType, rollup.RollupID, trimStringPointer(rollup.ModelName), trimStringPointer(rollup.InvocationPurpose),
		rollup.TotalInvocations, rollup.TotalInputTokens, rollup.TotalOutputTokens, rollup.TotalCacheReadTokens, rollup.TotalLatencyMS, rollup.TotalCostMicrocents,
	)

	return scanModelUsageRollup(row)
}

func (r *ModelUsageRollupRepo) ListByOrg(ctx context.Context, organizationID uuid.UUID) ([]ModelUsageRollup, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, rollup_date, rollup_type, rollup_id, model_name, invocation_purpose,
		       total_invocations, total_input_tokens, total_output_tokens, total_cache_read_tokens,
		       total_latency_ms, total_cost_microcents, created_at, updated_at
		FROM model_usage_rollup
		WHERE organization_id = $1
		ORDER BY rollup_date DESC, rollup_type ASC, created_at ASC
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ModelUsageRollup, 0)
	for rows.Next() {
		item, scanErr := scanModelUsageRollup(rows)
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

func (r *ModelUsageRollupRepo) ListByRollupID(ctx context.Context, organizationID uuid.UUID, rollupType string, rollupID uuid.UUID) ([]ModelUsageRollup, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, rollup_date, rollup_type, rollup_id, model_name, invocation_purpose,
		       total_invocations, total_input_tokens, total_output_tokens, total_cache_read_tokens,
		       total_latency_ms, total_cost_microcents, created_at, updated_at
		FROM model_usage_rollup
		WHERE organization_id = $1
		  AND rollup_type = $2
		  AND rollup_id = $3
		ORDER BY rollup_date DESC, created_at ASC
	`, organizationID, rollupType, rollupID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ModelUsageRollup, 0)
	for rows.Next() {
		item, scanErr := scanModelUsageRollup(rows)
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

func (r *ModelUsageRollupRepo) GetForDate(ctx context.Context, organizationID uuid.UUID, date time.Time) ([]ModelUsageRollup, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, rollup_date, rollup_type, rollup_id, model_name, invocation_purpose,
		       total_invocations, total_input_tokens, total_output_tokens, total_cache_read_tokens,
		       total_latency_ms, total_cost_microcents, created_at, updated_at
		FROM model_usage_rollup
		WHERE organization_id = $1
		  AND rollup_date = $2
		ORDER BY rollup_type ASC, created_at ASC
	`, organizationID, date.UTC())
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ModelUsageRollup, 0)
	for rows.Next() {
		item, scanErr := scanModelUsageRollup(rows)
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

func scanModelUsageRollup(row pgx.Row) (ModelUsageRollup, error) {
	var rollup ModelUsageRollup
	if err := row.Scan(
		&rollup.ID,
		&rollup.OrganizationID,
		&rollup.RollupDate,
		&rollup.RollupType,
		&rollup.RollupID,
		&rollup.ModelName,
		&rollup.InvocationPurpose,
		&rollup.TotalInvocations,
		&rollup.TotalInputTokens,
		&rollup.TotalOutputTokens,
		&rollup.TotalCacheReadTokens,
		&rollup.TotalLatencyMS,
		&rollup.TotalCostMicrocents,
		&rollup.CreatedAt,
		&rollup.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ModelUsageRollup{}, ErrNotFound
		}
		return ModelUsageRollup{}, err
	}

	rollup.RollupDate = rollup.RollupDate.UTC()
	return rollup, nil
}
