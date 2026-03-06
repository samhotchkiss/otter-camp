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

type PlanningArtifact struct {
	ID                  uuid.UUID
	OrganizationID      uuid.UUID
	ProjectID           uuid.UUID
	SourceTaskID        uuid.UUID
	ArtifactKind        string
	ArtifactSlug        string
	Title               string
	RepoPath            string
	CurrentVersion      int
	LatestContentSHA256 string
	CreatedByType       string
	CreatedByID         *uuid.UUID
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type PlanningArtifactVersion struct {
	ID            uuid.UUID
	ArtifactID    uuid.UUID
	SourceTaskID  uuid.UUID
	VersionNumber int
	Title         string
	RepoPath      string
	ContentSHA256 string
	ByteSize      int
	CreatedByType string
	CreatedByID   *uuid.UUID
	CreatedAt     time.Time
}

type PlanningArtifactUpsert struct {
	OrganizationID      uuid.UUID
	ProjectID           uuid.UUID
	SourceTaskID        uuid.UUID
	ArtifactKind        string
	ArtifactSlug        string
	Title               string
	RepoPath            string
	LatestContentSHA256 string
	ByteSize            int
	CreatedByType       string
	CreatedByID         *uuid.UUID
}

type PlanningArtifactRepo struct {
	pool *pgxpool.Pool
}

func NewPlanningArtifactRepo(pool *pgxpool.Pool) *PlanningArtifactRepo {
	return &PlanningArtifactRepo{pool: pool}
}

func (r *PlanningArtifactRepo) UpsertVersion(ctx context.Context, artifact PlanningArtifactUpsert) (PlanningArtifact, bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return PlanningArtifact{}, false, mapDBError(err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	existing, err := r.getByProjectPath(ctx, tx, artifact.ProjectID, artifact.RepoPath)
	if errors.Is(err, ErrNotFound) {
		created, createErr := r.insertArtifact(ctx, tx, artifact)
		if createErr != nil {
			return PlanningArtifact{}, false, createErr
		}
		if _, versionErr := r.insertVersion(ctx, tx, created.ID, 1, artifact); versionErr != nil {
			return PlanningArtifact{}, false, versionErr
		}
		if err := tx.Commit(ctx); err != nil {
			return PlanningArtifact{}, false, mapDBError(err)
		}
		return created, true, nil
	}
	if err != nil {
		return PlanningArtifact{}, false, err
	}

	versionChanged := false
	nextVersion := existing.CurrentVersion
	if existing.LatestContentSHA256 != strings.TrimSpace(artifact.LatestContentSHA256) {
		nextVersion++
		if _, versionErr := r.insertVersion(ctx, tx, existing.ID, nextVersion, artifact); versionErr != nil {
			return PlanningArtifact{}, false, versionErr
		}
		versionChanged = true
	}

	row := tx.QueryRow(ctx, `
		UPDATE planning_artifact
		SET
			artifact_kind = $2,
			artifact_slug = $3,
			title = $4,
			current_version = $5,
			latest_content_sha256 = $6
		WHERE id = $1
		RETURNING
			id,
			organization_id,
			project_id,
			source_task_id,
			artifact_kind,
			artifact_slug,
			title,
			repo_path,
			current_version,
			latest_content_sha256,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
	`,
		existing.ID,
		strings.TrimSpace(artifact.ArtifactKind),
		strings.TrimSpace(artifact.ArtifactSlug),
		strings.TrimSpace(artifact.Title),
		nextVersion,
		strings.TrimSpace(artifact.LatestContentSHA256),
	)

	updated, err := scanPlanningArtifact(row)
	if err != nil {
		return PlanningArtifact{}, false, mapDBError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PlanningArtifact{}, false, mapDBError(err)
	}
	return updated, versionChanged, nil
}

func (r *PlanningArtifactRepo) ListByProject(ctx context.Context, projectID uuid.UUID) ([]PlanningArtifact, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			organization_id,
			project_id,
			source_task_id,
			artifact_kind,
			artifact_slug,
			title,
			repo_path,
			current_version,
			latest_content_sha256,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM planning_artifact
		WHERE project_id = $1
		ORDER BY updated_at DESC, id DESC
	`, projectID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	artifacts := make([]PlanningArtifact, 0)
	for rows.Next() {
		artifact, scanErr := scanPlanningArtifact(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		artifacts = append(artifacts, artifact)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return artifacts, nil
}

func (r *PlanningArtifactRepo) ListBySourceTask(ctx context.Context, taskID uuid.UUID) ([]PlanningArtifact, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			organization_id,
			project_id,
			source_task_id,
			artifact_kind,
			artifact_slug,
			title,
			repo_path,
			current_version,
			latest_content_sha256,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM planning_artifact
		WHERE source_task_id = $1
		ORDER BY updated_at DESC, id DESC
	`, taskID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	artifacts := make([]PlanningArtifact, 0)
	for rows.Next() {
		artifact, scanErr := scanPlanningArtifact(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		artifacts = append(artifacts, artifact)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return artifacts, nil
}

func (r *PlanningArtifactRepo) ListVersions(ctx context.Context, artifactID uuid.UUID) ([]PlanningArtifactVersion, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			artifact_id,
			source_task_id,
			version_number,
			title,
			repo_path,
			content_sha256,
			byte_size,
			created_by_type,
			created_by_id,
			created_at
		FROM planning_artifact_version
		WHERE artifact_id = $1
		ORDER BY version_number DESC, id DESC
	`, artifactID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	versions := make([]PlanningArtifactVersion, 0)
	for rows.Next() {
		version, scanErr := scanPlanningArtifactVersion(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		versions = append(versions, version)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return versions, nil
}

func (r *PlanningArtifactRepo) getByProjectPath(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, repoPath string) (PlanningArtifact, error) {
	row := tx.QueryRow(ctx, `
		SELECT
			id,
			organization_id,
			project_id,
			source_task_id,
			artifact_kind,
			artifact_slug,
			title,
			repo_path,
			current_version,
			latest_content_sha256,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM planning_artifact
		WHERE project_id = $1
		  AND repo_path = $2
		FOR UPDATE
	`, projectID, strings.TrimSpace(repoPath))

	artifact, err := scanPlanningArtifact(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanningArtifact{}, ErrNotFound
	}
	if err != nil {
		return PlanningArtifact{}, mapDBError(err)
	}
	return artifact, nil
}

func (r *PlanningArtifactRepo) insertArtifact(ctx context.Context, tx pgx.Tx, artifact PlanningArtifactUpsert) (PlanningArtifact, error) {
	row := tx.QueryRow(ctx, `
		INSERT INTO planning_artifact (
			organization_id,
			project_id,
			source_task_id,
			artifact_kind,
			artifact_slug,
			title,
			repo_path,
			current_version,
			latest_content_sha256,
			created_by_type,
			created_by_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8, $9, $10)
		RETURNING
			id,
			organization_id,
			project_id,
			source_task_id,
			artifact_kind,
			artifact_slug,
			title,
			repo_path,
			current_version,
			latest_content_sha256,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
	`,
		artifact.OrganizationID,
		artifact.ProjectID,
		artifact.SourceTaskID,
		strings.TrimSpace(artifact.ArtifactKind),
		strings.TrimSpace(artifact.ArtifactSlug),
		strings.TrimSpace(artifact.Title),
		strings.TrimSpace(artifact.RepoPath),
		strings.TrimSpace(artifact.LatestContentSHA256),
		strings.TrimSpace(artifact.CreatedByType),
		artifact.CreatedByID,
	)

	created, err := scanPlanningArtifact(row)
	if err != nil {
		return PlanningArtifact{}, mapDBError(err)
	}
	return created, nil
}

func (r *PlanningArtifactRepo) insertVersion(ctx context.Context, tx pgx.Tx, artifactID uuid.UUID, versionNumber int, artifact PlanningArtifactUpsert) (PlanningArtifactVersion, error) {
	row := tx.QueryRow(ctx, `
		INSERT INTO planning_artifact_version (
			artifact_id,
			source_task_id,
			version_number,
			title,
			repo_path,
			content_sha256,
			byte_size,
			created_by_type,
			created_by_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING
			id,
			artifact_id,
			source_task_id,
			version_number,
			title,
			repo_path,
			content_sha256,
			byte_size,
			created_by_type,
			created_by_id,
			created_at
	`,
		artifactID,
		artifact.SourceTaskID,
		versionNumber,
		strings.TrimSpace(artifact.Title),
		strings.TrimSpace(artifact.RepoPath),
		strings.TrimSpace(artifact.LatestContentSHA256),
		artifact.ByteSize,
		strings.TrimSpace(artifact.CreatedByType),
		artifact.CreatedByID,
	)

	created, err := scanPlanningArtifactVersion(row)
	if err != nil {
		return PlanningArtifactVersion{}, mapDBError(err)
	}
	return created, nil
}

type planningArtifactScanner interface {
	Scan(dest ...any) error
}

func scanPlanningArtifact(row planningArtifactScanner) (PlanningArtifact, error) {
	var artifact PlanningArtifact
	err := row.Scan(
		&artifact.ID,
		&artifact.OrganizationID,
		&artifact.ProjectID,
		&artifact.SourceTaskID,
		&artifact.ArtifactKind,
		&artifact.ArtifactSlug,
		&artifact.Title,
		&artifact.RepoPath,
		&artifact.CurrentVersion,
		&artifact.LatestContentSHA256,
		&artifact.CreatedByType,
		&artifact.CreatedByID,
		&artifact.CreatedAt,
		&artifact.UpdatedAt,
	)
	if err != nil {
		return PlanningArtifact{}, err
	}
	return artifact, nil
}

func scanPlanningArtifactVersion(row planningArtifactScanner) (PlanningArtifactVersion, error) {
	var version PlanningArtifactVersion
	err := row.Scan(
		&version.ID,
		&version.ArtifactID,
		&version.SourceTaskID,
		&version.VersionNumber,
		&version.Title,
		&version.RepoPath,
		&version.ContentSHA256,
		&version.ByteSize,
		&version.CreatedByType,
		&version.CreatedByID,
		&version.CreatedAt,
	)
	if err != nil {
		return PlanningArtifactVersion{}, err
	}
	return version, nil
}
