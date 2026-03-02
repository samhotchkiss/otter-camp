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

type ProjectRemote struct {
	ID            uuid.UUID
	ProjectID     uuid.UUID
	Name          string
	URL           string
	IsDefault     bool
	Transport     string
	CredentialRef *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type ProjectEnvironment struct {
	ID                 uuid.UUID
	ProjectID          uuid.UUID
	Name               string
	DeliveryMode       string
	RemoteID           *uuid.UUID
	RepoURL            *string
	RepoPath           *string
	TargetBranch       string
	DeployTaskID       *uuid.UUID
	LastDeployedCommit *string
	LastDeployedAt     *time.Time
	IsActive           bool
	ScheduleID         *uuid.UUID
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ProjectRemoteRepo struct {
	pool *pgxpool.Pool
}

func NewProjectRemoteRepo(pool *pgxpool.Pool) *ProjectRemoteRepo {
	return &ProjectRemoteRepo{pool: pool}
}

func (r *ProjectRemoteRepo) Create(ctx context.Context, remote ProjectRemote) (ProjectRemote, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO project_remote (
			project_id,
			name,
			url,
			is_default,
			transport,
			credential_ref
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING
			id,
			project_id,
			name,
			url,
			is_default,
			transport,
			credential_ref,
			created_at,
			updated_at
	`,
		remote.ProjectID,
		strings.TrimSpace(remote.Name),
		strings.TrimSpace(remote.URL),
		remote.IsDefault,
		defaultRemoteTransport(remote.Transport),
		normalizeOptionalString(remote.CredentialRef),
	)

	created, err := scanProjectRemote(row)
	if err != nil {
		return ProjectRemote{}, mapDBError(err)
	}
	return created, nil
}

func (r *ProjectRemoteRepo) GetByID(ctx context.Context, id uuid.UUID) (ProjectRemote, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			id,
			project_id,
			name,
			url,
			is_default,
			transport,
			credential_ref,
			created_at,
			updated_at
		FROM project_remote
		WHERE id = $1
	`, id)

	remote, err := scanProjectRemote(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectRemote{}, ErrNotFound
	}
	if err != nil {
		return ProjectRemote{}, mapDBError(err)
	}
	return remote, nil
}

func (r *ProjectRemoteRepo) ListByProject(ctx context.Context, projectID uuid.UUID) ([]ProjectRemote, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			project_id,
			name,
			url,
			is_default,
			transport,
			credential_ref,
			created_at,
			updated_at
		FROM project_remote
		WHERE project_id = $1
		ORDER BY created_at DESC, id DESC
	`, projectID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	remotes := make([]ProjectRemote, 0)
	for rows.Next() {
		remote, scanErr := scanProjectRemote(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		remotes = append(remotes, remote)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return remotes, nil
}

func (r *ProjectRemoteRepo) Update(ctx context.Context, remote ProjectRemote) (ProjectRemote, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE project_remote
		SET
			name = $2,
			url = $3,
			is_default = $4,
			transport = $5,
			credential_ref = $6
		WHERE id = $1
		RETURNING
			id,
			project_id,
			name,
			url,
			is_default,
			transport,
			credential_ref,
			created_at,
			updated_at
	`,
		remote.ID,
		strings.TrimSpace(remote.Name),
		strings.TrimSpace(remote.URL),
		remote.IsDefault,
		defaultRemoteTransport(remote.Transport),
		normalizeOptionalString(remote.CredentialRef),
	)

	updated, err := scanProjectRemote(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectRemote{}, ErrNotFound
	}
	if err != nil {
		return ProjectRemote{}, mapDBError(err)
	}
	return updated, nil
}

func (r *ProjectRemoteRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM project_remote
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

func (r *ProjectRemoteRepo) GetDefault(ctx context.Context, projectID uuid.UUID) (ProjectRemote, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			id,
			project_id,
			name,
			url,
			is_default,
			transport,
			credential_ref,
			created_at,
			updated_at
		FROM project_remote
		WHERE project_id = $1
		  AND is_default = true
		LIMIT 1
	`, projectID)

	remote, err := scanProjectRemote(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectRemote{}, ErrNotFound
	}
	if err != nil {
		return ProjectRemote{}, mapDBError(err)
	}
	return remote, nil
}

func (r *ProjectRemoteRepo) SetDefault(ctx context.Context, projectID, remoteID uuid.UUID) (ProjectRemote, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ProjectRemote{}, mapDBError(err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `
		UPDATE project_remote
		SET is_default = false
		WHERE project_id = $1
		  AND is_default = true
		  AND id <> $2
	`, projectID, remoteID); err != nil {
		return ProjectRemote{}, mapDBError(err)
	}

	row := tx.QueryRow(ctx, `
		UPDATE project_remote
		SET is_default = true
		WHERE id = $1
		  AND project_id = $2
		RETURNING
			id,
			project_id,
			name,
			url,
			is_default,
			transport,
			credential_ref,
			created_at,
			updated_at
	`, remoteID, projectID)

	updated, err := scanProjectRemote(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectRemote{}, ErrNotFound
	}
	if err != nil {
		return ProjectRemote{}, mapDBError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ProjectRemote{}, mapDBError(err)
	}
	return updated, nil
}

type ProjectEnvironmentRepo struct {
	pool *pgxpool.Pool
}

func NewProjectEnvironmentRepo(pool *pgxpool.Pool) *ProjectEnvironmentRepo {
	return &ProjectEnvironmentRepo{pool: pool}
}

func (r *ProjectEnvironmentRepo) Create(ctx context.Context, environment ProjectEnvironment) (ProjectEnvironment, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO project_environment (
			project_id,
			name,
			delivery_mode,
			remote_id,
			repo_url,
			repo_path,
			target_branch,
			deploy_task_id,
			last_deployed_commit,
			last_deployed_at,
			is_active,
			schedule_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING
			id,
			project_id,
			name,
			delivery_mode,
			remote_id,
			repo_url,
			repo_path,
			target_branch,
			deploy_task_id,
			last_deployed_commit,
			last_deployed_at,
			is_active,
			schedule_id,
			created_at,
			updated_at
	`,
		environment.ProjectID,
		strings.TrimSpace(environment.Name),
		strings.TrimSpace(environment.DeliveryMode),
		environment.RemoteID,
		normalizeOptionalString(environment.RepoURL),
		normalizeOptionalString(environment.RepoPath),
		defaultTargetBranch(environment.TargetBranch),
		environment.DeployTaskID,
		normalizeOptionalString(environment.LastDeployedCommit),
		environment.LastDeployedAt,
		environment.IsActive,
		environment.ScheduleID,
	)

	created, err := scanProjectEnvironment(row)
	if err != nil {
		return ProjectEnvironment{}, mapDBError(err)
	}
	return created, nil
}

func (r *ProjectEnvironmentRepo) GetByID(ctx context.Context, id uuid.UUID) (ProjectEnvironment, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			id,
			project_id,
			name,
			delivery_mode,
			remote_id,
			repo_url,
			repo_path,
			target_branch,
			deploy_task_id,
			last_deployed_commit,
			last_deployed_at,
			is_active,
			schedule_id,
			created_at,
			updated_at
		FROM project_environment
		WHERE id = $1
	`, id)

	environment, err := scanProjectEnvironment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironment{}, ErrNotFound
	}
	if err != nil {
		return ProjectEnvironment{}, mapDBError(err)
	}
	return environment, nil
}

func (r *ProjectEnvironmentRepo) ListByProject(ctx context.Context, projectID uuid.UUID) ([]ProjectEnvironment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			project_id,
			name,
			delivery_mode,
			remote_id,
			repo_url,
			repo_path,
			target_branch,
			deploy_task_id,
			last_deployed_commit,
			last_deployed_at,
			is_active,
			schedule_id,
			created_at,
			updated_at
		FROM project_environment
		WHERE project_id = $1
		ORDER BY created_at DESC, id DESC
	`, projectID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	environments := make([]ProjectEnvironment, 0)
	for rows.Next() {
		environment, scanErr := scanProjectEnvironment(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		environments = append(environments, environment)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return environments, nil
}

func (r *ProjectEnvironmentRepo) Update(ctx context.Context, environment ProjectEnvironment) (ProjectEnvironment, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE project_environment
		SET
			name = $2,
			delivery_mode = $3,
			remote_id = $4,
			repo_url = $5,
			repo_path = $6,
			target_branch = $7,
			is_active = $8,
			schedule_id = $9
		WHERE id = $1
		RETURNING
			id,
			project_id,
			name,
			delivery_mode,
			remote_id,
			repo_url,
			repo_path,
			target_branch,
			deploy_task_id,
			last_deployed_commit,
			last_deployed_at,
			is_active,
			schedule_id,
			created_at,
			updated_at
	`,
		environment.ID,
		strings.TrimSpace(environment.Name),
		strings.TrimSpace(environment.DeliveryMode),
		environment.RemoteID,
		normalizeOptionalString(environment.RepoURL),
		normalizeOptionalString(environment.RepoPath),
		defaultTargetBranch(environment.TargetBranch),
		environment.IsActive,
		environment.ScheduleID,
	)

	updated, err := scanProjectEnvironment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironment{}, ErrNotFound
	}
	if err != nil {
		return ProjectEnvironment{}, mapDBError(err)
	}
	return updated, nil
}

func (r *ProjectEnvironmentRepo) SetDeployTask(ctx context.Context, environmentID uuid.UUID, deployTaskID *uuid.UUID) (ProjectEnvironment, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE project_environment
		SET deploy_task_id = $2
		WHERE id = $1
		RETURNING
			id,
			project_id,
			name,
			delivery_mode,
			remote_id,
			repo_url,
			repo_path,
			target_branch,
			deploy_task_id,
			last_deployed_commit,
			last_deployed_at,
			is_active,
			schedule_id,
			created_at,
			updated_at
	`, environmentID, deployTaskID)

	updated, err := scanProjectEnvironment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironment{}, ErrNotFound
	}
	if err != nil {
		return ProjectEnvironment{}, mapDBError(err)
	}
	return updated, nil
}

func (r *ProjectEnvironmentRepo) RecordDeployment(ctx context.Context, environmentID uuid.UUID, commitSHA string, deployedAt time.Time) (ProjectEnvironment, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE project_environment
		SET
			last_deployed_commit = $2,
			last_deployed_at = $3
		WHERE id = $1
		RETURNING
			id,
			project_id,
			name,
			delivery_mode,
			remote_id,
			repo_url,
			repo_path,
			target_branch,
			deploy_task_id,
			last_deployed_commit,
			last_deployed_at,
			is_active,
			schedule_id,
			created_at,
			updated_at
	`, environmentID, strings.TrimSpace(commitSHA), deployedAt.UTC())

	updated, err := scanProjectEnvironment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironment{}, ErrNotFound
	}
	if err != nil {
		return ProjectEnvironment{}, mapDBError(err)
	}
	return updated, nil
}

func (r *ProjectEnvironmentRepo) GetActiveByMode(ctx context.Context, mode string) ([]ProjectEnvironment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			project_id,
			name,
			delivery_mode,
			remote_id,
			repo_url,
			repo_path,
			target_branch,
			deploy_task_id,
			last_deployed_commit,
			last_deployed_at,
			is_active,
			schedule_id,
			created_at,
			updated_at
		FROM project_environment
		WHERE delivery_mode = $1
		  AND is_active = true
		ORDER BY created_at DESC, id DESC
	`, strings.TrimSpace(mode))
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	environments := make([]ProjectEnvironment, 0)
	for rows.Next() {
		environment, scanErr := scanProjectEnvironment(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		environments = append(environments, environment)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return environments, nil
}

func scanProjectRemote(row pgx.Row) (ProjectRemote, error) {
	var remote ProjectRemote
	if err := row.Scan(
		&remote.ID,
		&remote.ProjectID,
		&remote.Name,
		&remote.URL,
		&remote.IsDefault,
		&remote.Transport,
		&remote.CredentialRef,
		&remote.CreatedAt,
		&remote.UpdatedAt,
	); err != nil {
		return ProjectRemote{}, err
	}
	return remote, nil
}

func scanProjectEnvironment(row pgx.Row) (ProjectEnvironment, error) {
	var environment ProjectEnvironment
	if err := row.Scan(
		&environment.ID,
		&environment.ProjectID,
		&environment.Name,
		&environment.DeliveryMode,
		&environment.RemoteID,
		&environment.RepoURL,
		&environment.RepoPath,
		&environment.TargetBranch,
		&environment.DeployTaskID,
		&environment.LastDeployedCommit,
		&environment.LastDeployedAt,
		&environment.IsActive,
		&environment.ScheduleID,
		&environment.CreatedAt,
		&environment.UpdatedAt,
	); err != nil {
		return ProjectEnvironment{}, err
	}
	return environment, nil
}

func defaultRemoteTransport(transport string) string {
	trimmed := strings.TrimSpace(transport)
	if trimmed == "" {
		return "https"
	}
	return trimmed
}

func normalizeOptionalString(input *string) *string {
	if input == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*input)
	return &trimmed
}
