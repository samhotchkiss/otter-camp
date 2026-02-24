package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ModelProvider struct {
	ID                uuid.UUID
	Slug              string
	DisplayName       string
	APIBaseURL        string
	SupportedFeatures []string
	IsEnabled         bool
	Metadata          json.RawMessage
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type ModelProviderRepo struct {
	pool *pgxpool.Pool
}

func NewModelProviderRepo(pool *pgxpool.Pool) *ModelProviderRepo {
	return &ModelProviderRepo{pool: pool}
}

func (r *ModelProviderRepo) Create(ctx context.Context, provider ModelProvider) (ModelProvider, error) {
	metadata := provider.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	supportedFeatures := provider.SupportedFeatures
	if supportedFeatures == nil {
		supportedFeatures = []string{}
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO model_provider (
			slug,
			display_name,
			api_base_url,
			supported_features,
			is_enabled,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
		RETURNING id, slug, display_name, api_base_url, supported_features, is_enabled, metadata, created_at, updated_at
	`, provider.Slug, provider.DisplayName, provider.APIBaseURL, supportedFeatures, provider.IsEnabled, metadata)

	created, err := scanModelProvider(row)
	if err != nil {
		return ModelProvider{}, mapDBError(err)
	}
	return created, nil
}

func (r *ModelProviderRepo) GetByID(ctx context.Context, id uuid.UUID) (ModelProvider, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, slug, display_name, api_base_url, supported_features, is_enabled, metadata, created_at, updated_at
		FROM model_provider
		WHERE id = $1
	`, id)

	provider, err := scanModelProvider(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelProvider{}, ErrNotFound
	}
	if err != nil {
		return ModelProvider{}, mapDBError(err)
	}
	return provider, nil
}

func (r *ModelProviderRepo) GetBySlug(ctx context.Context, slug string) (ModelProvider, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, slug, display_name, api_base_url, supported_features, is_enabled, metadata, created_at, updated_at
		FROM model_provider
		WHERE slug = $1
	`, slug)

	provider, err := scanModelProvider(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelProvider{}, ErrNotFound
	}
	if err != nil {
		return ModelProvider{}, mapDBError(err)
	}
	return provider, nil
}

func (r *ModelProviderRepo) List(ctx context.Context) ([]ModelProvider, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, slug, display_name, api_base_url, supported_features, is_enabled, metadata, created_at, updated_at
		FROM model_provider
		ORDER BY created_at
	`)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	providers := make([]ModelProvider, 0)
	for rows.Next() {
		provider, scanErr := scanModelProvider(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		providers = append(providers, provider)
	}

	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return providers, nil
}

func (r *ModelProviderRepo) SetEnabled(ctx context.Context, id uuid.UUID, enabled bool) (ModelProvider, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE model_provider
		SET is_enabled = $2
		WHERE id = $1
		RETURNING id, slug, display_name, api_base_url, supported_features, is_enabled, metadata, created_at, updated_at
	`, id, enabled)

	provider, err := scanModelProvider(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelProvider{}, ErrNotFound
	}
	if err != nil {
		return ModelProvider{}, mapDBError(err)
	}
	return provider, nil
}

func scanModelProvider(row pgx.Row) (ModelProvider, error) {
	var provider ModelProvider
	if err := row.Scan(
		&provider.ID,
		&provider.Slug,
		&provider.DisplayName,
		&provider.APIBaseURL,
		&provider.SupportedFeatures,
		&provider.IsEnabled,
		&provider.Metadata,
		&provider.CreatedAt,
		&provider.UpdatedAt,
	); err != nil {
		return ModelProvider{}, err
	}

	if len(provider.Metadata) == 0 {
		provider.Metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(provider.Metadata) {
		return ModelProvider{}, fmt.Errorf("invalid metadata json")
	}

	return provider, nil
}
