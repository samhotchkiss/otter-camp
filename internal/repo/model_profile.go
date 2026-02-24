package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ModelProfile struct {
	ID                  uuid.UUID
	LogicalProfileID    string
	OrganizationID      *uuid.UUID
	Version             int
	IsCurrent           bool
	ProviderID          uuid.UUID
	ModelName           string
	ContextWindowTokens int
	MaxOutputTokens     int
	SupportsStreaming   bool
	SupportsVision      bool
	Temperature         *float64
	InvocationPurpose   string
	FallbackProfileID   *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type ModelProfileRepo struct {
	pool *pgxpool.Pool
}

func NewModelProfileRepo(pool *pgxpool.Pool) *ModelProfileRepo {
	return &ModelProfileRepo{pool: pool}
}

func (r *ModelProfileRepo) Create(ctx context.Context, profile ModelProfile) (ModelProfile, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO model_profile (
			logical_profile_id,
			organization_id,
			version,
			is_current,
			provider_id,
			model_name,
			context_window_tokens,
			max_output_tokens,
			supports_streaming,
			supports_vision,
			temperature,
			invocation_purpose,
			fallback_profile_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, logical_profile_id, organization_id, version, is_current, provider_id, model_name, context_window_tokens, max_output_tokens, supports_streaming, supports_vision, temperature, invocation_purpose, fallback_profile_id, created_at, updated_at
	`, profile.LogicalProfileID, profile.OrganizationID, defaultProfileVersion(profile.Version), profile.IsCurrent, profile.ProviderID, profile.ModelName, profile.ContextWindowTokens, profile.MaxOutputTokens, profile.SupportsStreaming, profile.SupportsVision, profile.Temperature, profile.InvocationPurpose, profile.FallbackProfileID)

	created, err := scanModelProfile(row)
	if err != nil {
		return ModelProfile{}, mapDBError(err)
	}
	return created, nil
}

func (r *ModelProfileRepo) GetCurrentByLogicalID(ctx context.Context, organizationID uuid.UUID, logicalProfileID string) (ModelProfile, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, logical_profile_id, organization_id, version, is_current, provider_id, model_name, context_window_tokens, max_output_tokens, supports_streaming, supports_vision, temperature, invocation_purpose, fallback_profile_id, created_at, updated_at
		FROM model_profile
		WHERE logical_profile_id = $2
		  AND is_current = true
		  AND (organization_id = $1 OR organization_id IS NULL)
		ORDER BY CASE WHEN organization_id = $1 THEN 0 ELSE 1 END
		LIMIT 1
	`, organizationID, strings.TrimSpace(logicalProfileID))

	profile, err := scanModelProfile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelProfile{}, ErrNotFound
	}
	if err != nil {
		return ModelProfile{}, mapDBError(err)
	}
	return profile, nil
}

func (r *ModelProfileRepo) ListCurrent(ctx context.Context, organizationID uuid.UUID) ([]ModelProfile, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, logical_profile_id, organization_id, version, is_current, provider_id, model_name, context_window_tokens, max_output_tokens, supports_streaming, supports_vision, temperature, invocation_purpose, fallback_profile_id, created_at, updated_at
		FROM model_profile
		WHERE is_current = true
		  AND (organization_id = $1 OR organization_id IS NULL)
		ORDER BY logical_profile_id, CASE WHEN organization_id = $1 THEN 0 ELSE 1 END
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ModelProfile, 0)
	for rows.Next() {
		profile, scanErr := scanModelProfile(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, profile)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *ModelProfileRepo) ListAll(ctx context.Context, organizationID uuid.UUID) ([]ModelProfile, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, logical_profile_id, organization_id, version, is_current, provider_id, model_name, context_window_tokens, max_output_tokens, supports_streaming, supports_vision, temperature, invocation_purpose, fallback_profile_id, created_at, updated_at
		FROM model_profile
		WHERE organization_id = $1 OR organization_id IS NULL
		ORDER BY logical_profile_id, version
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ModelProfile, 0)
	for rows.Next() {
		profile, scanErr := scanModelProfile(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, profile)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *ModelProfileRepo) Deprecate(ctx context.Context, currentID uuid.UUID, next ModelProfile) (ModelProfile, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ModelProfile{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	row := tx.QueryRow(ctx, `
		SELECT id, logical_profile_id, organization_id, version, is_current, provider_id, model_name, context_window_tokens, max_output_tokens, supports_streaming, supports_vision, temperature, invocation_purpose, fallback_profile_id, created_at, updated_at
		FROM model_profile
		WHERE id = $1
		FOR UPDATE
	`, currentID)

	current, err := scanModelProfile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelProfile{}, ErrNotFound
	}
	if err != nil {
		return ModelProfile{}, err
	}
	if !current.IsCurrent {
		return ModelProfile{}, ErrConflict
	}

	if _, err := tx.Exec(ctx, `
		UPDATE model_profile
		SET is_current = false
		WHERE id = $1
	`, current.ID); err != nil {
		return ModelProfile{}, mapDBError(err)
	}

	inserted := tx.QueryRow(ctx, `
		INSERT INTO model_profile (
			logical_profile_id,
			organization_id,
			version,
			is_current,
			provider_id,
			model_name,
			context_window_tokens,
			max_output_tokens,
			supports_streaming,
			supports_vision,
			temperature,
			invocation_purpose,
			fallback_profile_id
		)
		VALUES ($1, $2, $3, true, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, logical_profile_id, organization_id, version, is_current, provider_id, model_name, context_window_tokens, max_output_tokens, supports_streaming, supports_vision, temperature, invocation_purpose, fallback_profile_id, created_at, updated_at
	`,
		current.LogicalProfileID,
		current.OrganizationID,
		current.Version+1,
		next.ProviderID,
		next.ModelName,
		next.ContextWindowTokens,
		next.MaxOutputTokens,
		next.SupportsStreaming,
		next.SupportsVision,
		next.Temperature,
		next.InvocationPurpose,
		next.FallbackProfileID,
	)

	created, err := scanModelProfile(inserted)
	if err != nil {
		return ModelProfile{}, mapDBError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ModelProfile{}, fmt.Errorf("commit tx: %w", err)
	}

	return created, nil
}

func scanModelProfile(row pgx.Row) (ModelProfile, error) {
	var profile ModelProfile
	if err := row.Scan(
		&profile.ID,
		&profile.LogicalProfileID,
		&profile.OrganizationID,
		&profile.Version,
		&profile.IsCurrent,
		&profile.ProviderID,
		&profile.ModelName,
		&profile.ContextWindowTokens,
		&profile.MaxOutputTokens,
		&profile.SupportsStreaming,
		&profile.SupportsVision,
		&profile.Temperature,
		&profile.InvocationPurpose,
		&profile.FallbackProfileID,
		&profile.CreatedAt,
		&profile.UpdatedAt,
	); err != nil {
		return ModelProfile{}, err
	}
	return profile, nil
}

func defaultProfileVersion(version int) int {
	if version <= 0 {
		return 1
	}
	return version
}
