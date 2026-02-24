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

type Skill struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	ProjectID      *uuid.UUID
	Slug           string
	DisplayName    string
	Description    string
	FilePath       string
	Version        int
	IsActive       bool
	CreatedByType  string
	CreatedByID    uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type SkillRepo struct {
	pool *pgxpool.Pool
}

func NewSkillRepo(pool *pgxpool.Pool) *SkillRepo {
	return &SkillRepo{pool: pool}
}

func (r *SkillRepo) Create(ctx context.Context, skill Skill) (Skill, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO skill (
			organization_id,
			project_id,
			slug,
			display_name,
			description,
			file_path,
			version,
			is_active,
			created_by_type,
			created_by_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, organization_id, project_id, slug, display_name, description, file_path, version, is_active, created_by_type, created_by_id, created_at, updated_at
	`, skill.OrganizationID, skill.ProjectID, skill.Slug, skill.DisplayName, skill.Description, skill.FilePath, defaultSkillVersion(skill.Version), defaultSkillActive(skill.IsActive), skill.CreatedByType, skill.CreatedByID)

	created, err := scanSkill(row)
	if err != nil {
		return Skill{}, mapDBError(err)
	}
	return created, nil
}

func (r *SkillRepo) GetByID(ctx context.Context, id uuid.UUID) (Skill, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, project_id, slug, display_name, description, file_path, version, is_active, created_by_type, created_by_id, created_at, updated_at
		FROM skill
		WHERE id = $1
	`, id)

	skill, err := scanSkill(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Skill{}, ErrNotFound
	}
	if err != nil {
		return Skill{}, mapDBError(err)
	}
	return skill, nil
}

func (r *SkillRepo) GetBySlug(ctx context.Context, organizationID uuid.UUID, projectID *uuid.UUID, slug string) (Skill, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, project_id, slug, display_name, description, file_path, version, is_active, created_by_type, created_by_id, created_at, updated_at
		FROM skill
		WHERE organization_id = $1
		  AND slug = $2
		  AND (
		  	(project_id IS NULL AND $3::uuid IS NULL)
			OR project_id = $3
		  )
	`, organizationID, slug, projectID)

	skill, err := scanSkill(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Skill{}, ErrNotFound
	}
	if err != nil {
		return Skill{}, mapDBError(err)
	}
	return skill, nil
}

func (r *SkillRepo) ListByOrg(ctx context.Context, organizationID uuid.UUID, includeInactive bool) ([]Skill, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, project_id, slug, display_name, description, file_path, version, is_active, created_by_type, created_by_id, created_at, updated_at
		FROM skill
		WHERE organization_id = $1
		  AND ($2 OR is_active = true)
		ORDER BY created_at
	`, organizationID, includeInactive)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	skills := make([]Skill, 0)
	for rows.Next() {
		skill, scanErr := scanSkill(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		skills = append(skills, skill)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return skills, nil
}

func (r *SkillRepo) ListByProject(ctx context.Context, projectID uuid.UUID, includeInactive bool) ([]Skill, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, project_id, slug, display_name, description, file_path, version, is_active, created_by_type, created_by_id, created_at, updated_at
		FROM skill
		WHERE project_id = $1
		  AND ($2 OR is_active = true)
		ORDER BY created_at
	`, projectID, includeInactive)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	skills := make([]Skill, 0)
	for rows.Next() {
		skill, scanErr := scanSkill(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		skills = append(skills, skill)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return skills, nil
}

func (r *SkillRepo) Update(ctx context.Context, skill Skill) (Skill, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE skill
		SET display_name = $2,
			description = $3,
			file_path = $4,
			version = version + 1
		WHERE id = $1
		RETURNING id, organization_id, project_id, slug, display_name, description, file_path, version, is_active, created_by_type, created_by_id, created_at, updated_at
	`, skill.ID, skill.DisplayName, skill.Description, skill.FilePath)

	updated, err := scanSkill(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Skill{}, ErrNotFound
	}
	if err != nil {
		return Skill{}, mapDBError(err)
	}
	return updated, nil
}

func (r *SkillRepo) SetActive(ctx context.Context, id uuid.UUID, active bool) (Skill, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE skill
		SET is_active = $2
		WHERE id = $1
		RETURNING id, organization_id, project_id, slug, display_name, description, file_path, version, is_active, created_by_type, created_by_id, created_at, updated_at
	`, id, active)

	skill, err := scanSkill(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Skill{}, ErrNotFound
	}
	if err != nil {
		return Skill{}, mapDBError(err)
	}
	return skill, nil
}

func (r *SkillRepo) BulkUpsertBySlug(ctx context.Context, skills []Skill) ([]Skill, error) {
	if len(skills) == 0 {
		return nil, nil
	}

	var sb strings.Builder
	sb.WriteString(`
		INSERT INTO skill (
			organization_id,
			project_id,
			slug,
			display_name,
			description,
			file_path,
			version,
			is_active,
			created_by_type,
			created_by_id
		) VALUES
	`)

	args := make([]any, 0, len(skills)*10)
	for i, skill := range skills {
		if skill.ProjectID != nil {
			return nil, fmt.Errorf("bulk upsert only supports org-scoped skills")
		}

		if i > 0 {
			sb.WriteString(",")
		}

		placeholder := i*10 + 1
		sb.WriteString(fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			placeholder,
			placeholder+1,
			placeholder+2,
			placeholder+3,
			placeholder+4,
			placeholder+5,
			placeholder+6,
			placeholder+7,
			placeholder+8,
			placeholder+9,
		))

		args = append(args,
			skill.OrganizationID,
			skill.ProjectID,
			skill.Slug,
			skill.DisplayName,
			skill.Description,
			skill.FilePath,
			defaultSkillVersion(skill.Version),
			defaultSkillActive(skill.IsActive),
			skill.CreatedByType,
			skill.CreatedByID,
		)
	}

	sb.WriteString(`
		ON CONFLICT (organization_id, slug) WHERE project_id IS NULL
		DO UPDATE SET
			display_name = EXCLUDED.display_name,
			description = EXCLUDED.description,
			file_path = EXCLUDED.file_path,
			version = skill.version + 1,
			is_active = EXCLUDED.is_active
		RETURNING id, organization_id, project_id, slug, display_name, description, file_path, version, is_active, created_by_type, created_by_id, created_at, updated_at
	`)

	rows, err := r.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	upserted := make([]Skill, 0, len(skills))
	for rows.Next() {
		skill, scanErr := scanSkill(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		upserted = append(upserted, skill)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return upserted, nil
}

func defaultSkillVersion(version int) int {
	if version <= 0 {
		return 1
	}
	return version
}

func defaultSkillActive(active bool) bool {
	return active
}

func scanSkill(row pgx.Row) (Skill, error) {
	var skill Skill
	if err := row.Scan(
		&skill.ID,
		&skill.OrganizationID,
		&skill.ProjectID,
		&skill.Slug,
		&skill.DisplayName,
		&skill.Description,
		&skill.FilePath,
		&skill.Version,
		&skill.IsActive,
		&skill.CreatedByType,
		&skill.CreatedByID,
		&skill.CreatedAt,
		&skill.UpdatedAt,
	); err != nil {
		return Skill{}, err
	}
	return skill, nil
}
