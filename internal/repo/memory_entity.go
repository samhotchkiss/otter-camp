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

type MemoryEntity struct {
	ID                uuid.UUID
	OrganizationID    uuid.UUID
	CanonicalName     string
	EntityType        string
	SynthesisMemoryID *uuid.UUID
	Metadata          json.RawMessage
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type MemoryEntityRepo struct {
	pool *pgxpool.Pool
}

func NewMemoryEntityRepo(pool *pgxpool.Pool) *MemoryEntityRepo {
	return &MemoryEntityRepo{pool: pool}
}

func (r *MemoryEntityRepo) Create(ctx context.Context, entity MemoryEntity) (MemoryEntity, error) {
	metadata := entity.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(metadata) {
		return MemoryEntity{}, fmt.Errorf("invalid metadata json")
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO memory_entity (
			organization_id,
			canonical_name,
			entity_type,
			synthesis_memory_id,
			metadata
		)
		VALUES ($1, $2, COALESCE(NULLIF($3, ''), 'general'), $4, $5::jsonb)
		RETURNING id, organization_id, canonical_name, entity_type, synthesis_memory_id, metadata, created_at, updated_at
	`, entity.OrganizationID, strings.TrimSpace(entity.CanonicalName), strings.TrimSpace(entity.EntityType), entity.SynthesisMemoryID, metadata)

	created, err := scanMemoryEntity(row)
	if err != nil {
		return MemoryEntity{}, mapDBError(err)
	}
	return created, nil
}

func (r *MemoryEntityRepo) GetByID(ctx context.Context, id uuid.UUID) (MemoryEntity, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, canonical_name, entity_type, synthesis_memory_id, metadata, created_at, updated_at
		FROM memory_entity
		WHERE id = $1
	`, id)

	entity, err := scanMemoryEntity(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemoryEntity{}, ErrNotFound
	}
	if err != nil {
		return MemoryEntity{}, mapDBError(err)
	}
	return entity, nil
}

func (r *MemoryEntityRepo) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]MemoryEntity, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, canonical_name, entity_type, synthesis_memory_id, metadata, created_at, updated_at
		FROM memory_entity
		WHERE organization_id = $1
		ORDER BY created_at ASC
	`, orgID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]MemoryEntity, 0)
	for rows.Next() {
		item, scanErr := scanMemoryEntity(rows)
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

func (r *MemoryEntityRepo) UpdateCanonicalName(ctx context.Context, id uuid.UUID, canonicalName string) (MemoryEntity, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE memory_entity
		SET canonical_name = $2
		WHERE id = $1
		RETURNING id, organization_id, canonical_name, entity_type, synthesis_memory_id, metadata, created_at, updated_at
	`, id, strings.TrimSpace(canonicalName))

	updated, err := scanMemoryEntity(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemoryEntity{}, ErrNotFound
	}
	if err != nil {
		return MemoryEntity{}, mapDBError(err)
	}
	return updated, nil
}

func (r *MemoryEntityRepo) UpdateSynthesisMemoryID(ctx context.Context, id uuid.UUID, synthesisMemoryID *uuid.UUID) (MemoryEntity, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE memory_entity
		SET synthesis_memory_id = $2
		WHERE id = $1
		RETURNING id, organization_id, canonical_name, entity_type, synthesis_memory_id, metadata, created_at, updated_at
	`, id, synthesisMemoryID)

	updated, err := scanMemoryEntity(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemoryEntity{}, ErrNotFound
	}
	if err != nil {
		return MemoryEntity{}, mapDBError(err)
	}
	return updated, nil
}

func scanMemoryEntity(row pgx.Row) (MemoryEntity, error) {
	var (
		item         MemoryEntity
		metadataJSON []byte
	)
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.CanonicalName,
		&item.EntityType,
		&item.SynthesisMemoryID,
		&metadataJSON,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return MemoryEntity{}, err
	}
	if len(metadataJSON) == 0 {
		item.Metadata = json.RawMessage(`{}`)
	} else {
		item.Metadata = metadataJSON
	}
	return item, nil
}
