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

type OrganizationSettings struct {
	Agents    OrganizationAgentsSettings    `json:"agents,omitempty"`
	Redaction OrganizationRedactionSettings `json:"redaction,omitempty"`
}

type OrganizationAgentsSettings struct {
	MaxConcurrentTemps int `json:"max_concurrent_temps,omitempty"`
}

type OrganizationRedactionSettings struct {
	Enabled bool   `json:"enabled,omitempty"`
	Policy  string `json:"policy,omitempty"`
}

type Organization struct {
	ID          uuid.UUID
	Slug        string
	DisplayName string
	Settings    OrganizationSettings
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ArchivedAt  *time.Time
}

type OrgRepo struct {
	pool *pgxpool.Pool
}

func NewOrgRepo(pool *pgxpool.Pool) *OrgRepo {
	return &OrgRepo{pool: pool}
}

func (r *OrgRepo) Create(ctx context.Context, org Organization) (Organization, error) {
	settingsJSON, err := json.Marshal(org.Settings)
	if err != nil {
		return Organization{}, fmt.Errorf("marshal settings: %w", err)
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO organization (slug, display_name, settings)
		VALUES ($1, $2, $3::jsonb)
		RETURNING id, slug, display_name, settings, created_at, updated_at, archived_at
	`, org.Slug, org.DisplayName, settingsJSON)

	created, err := scanOrganization(row)
	if err != nil {
		return Organization{}, mapDBError(err)
	}
	return created, nil
}

func (r *OrgRepo) GetByID(ctx context.Context, id uuid.UUID) (Organization, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, slug, display_name, settings, created_at, updated_at, archived_at
		FROM organization
		WHERE id = $1
	`, id)

	org, err := scanOrganization(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, mapDBError(err)
	}
	return org, nil
}

func (r *OrgRepo) GetBySlug(ctx context.Context, slug string) (Organization, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, slug, display_name, settings, created_at, updated_at, archived_at
		FROM organization
		WHERE slug = $1
	`, slug)

	org, err := scanOrganization(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, mapDBError(err)
	}
	return org, nil
}

func (r *OrgRepo) List(ctx context.Context) ([]Organization, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, slug, display_name, settings, created_at, updated_at, archived_at
		FROM organization
		ORDER BY created_at
	`)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	orgs := make([]Organization, 0)
	for rows.Next() {
		org, scanErr := scanOrganization(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		orgs = append(orgs, org)
	}

	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return orgs, nil
}

func (r *OrgRepo) Archive(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `
		UPDATE organization
		SET archived_at = now()
		WHERE id = $1 AND archived_at IS NULL
	`, id)
	if err != nil {
		return mapDBError(err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *OrgRepo) Update(ctx context.Context, org Organization) (Organization, error) {
	settingsJSON, err := json.Marshal(org.Settings)
	if err != nil {
		return Organization{}, fmt.Errorf("marshal settings: %w", err)
	}

	row := r.pool.QueryRow(ctx, `
		UPDATE organization
		SET slug = $2,
		    display_name = $3,
		    settings = $4::jsonb
		WHERE id = $1
		RETURNING id, slug, display_name, settings, created_at, updated_at, archived_at
	`, org.ID, org.Slug, org.DisplayName, settingsJSON)

	updated, err := scanOrganization(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, mapDBError(err)
	}
	return updated, nil
}

func (r *OrgRepo) UpdateSettings(ctx context.Context, id uuid.UUID, settings OrganizationSettings) (Organization, error) {
	replaced := replaceOrganizationSettings(OrganizationSettings{}, settings)
	settingsJSON, err := json.Marshal(replaced)
	if err != nil {
		return Organization{}, fmt.Errorf("marshal settings: %w", err)
	}

	row := r.pool.QueryRow(ctx, `
		UPDATE organization
		SET settings = $2::jsonb
		WHERE id = $1
		RETURNING id, slug, display_name, settings, created_at, updated_at, archived_at
	`, id, settingsJSON)

	updated, err := scanOrganization(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, mapDBError(err)
	}
	return updated, nil
}

func replaceOrganizationSettings(_ OrganizationSettings, next OrganizationSettings) OrganizationSettings {
	return next
}

func scanOrganization(row pgx.Row) (Organization, error) {
	var (
		org          Organization
		settingsJSON []byte
	)

	if err := row.Scan(
		&org.ID,
		&org.Slug,
		&org.DisplayName,
		&settingsJSON,
		&org.CreatedAt,
		&org.UpdatedAt,
		&org.ArchivedAt,
	); err != nil {
		return Organization{}, err
	}

	if err := json.Unmarshal(settingsJSON, &org.Settings); err != nil {
		return Organization{}, fmt.Errorf("unmarshal settings: %w", err)
	}

	return org, nil
}
