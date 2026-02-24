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

type HumanUser struct {
	ID                  uuid.UUID
	OrganizationID      uuid.UUID
	Email               string
	DisplayName         string
	PasswordHash        *string
	Role                string
	IsActive            bool
	FailedLoginAttempts int
	LockedUntil         *time.Time
	LastLoginAt         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	Settings            json.RawMessage
}

type HumanUserUpdate struct {
	ID           uuid.UUID
	Email        string
	DisplayName  string
	PasswordHash *string
	Role         string
	Settings     json.RawMessage
	LastLoginAt  *time.Time
}

type HumanUserRepo struct {
	pool dbQuerier
}

func NewHumanUserRepo(pool *pgxpool.Pool) *HumanUserRepo {
	return &HumanUserRepo{pool: pool}
}

func newHumanUserRepoWithQuerier(q dbQuerier) *HumanUserRepo {
	return &HumanUserRepo{pool: q}
}

func (r *HumanUserRepo) Create(ctx context.Context, user HumanUser) (HumanUser, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO human_user (
			organization_id,
			email,
			display_name,
			password_hash,
			role,
			is_active,
			failed_login_attempts,
			locked_until,
			last_login_at,
			settings
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)
		RETURNING id, organization_id, email, display_name, password_hash, role, is_active, failed_login_attempts, locked_until, last_login_at, created_at, updated_at, settings
	`, user.OrganizationID, user.Email, user.DisplayName, user.PasswordHash, user.Role, user.IsActive, user.FailedLoginAttempts, user.LockedUntil, user.LastLoginAt, normalizeSettings(user.Settings))

	created, err := scanHumanUser(row)
	if err != nil {
		return HumanUser{}, mapDBError(err)
	}
	return created, nil
}

func (r *HumanUserRepo) GetByID(ctx context.Context, id uuid.UUID) (HumanUser, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, email, display_name, password_hash, role, is_active, failed_login_attempts, locked_until, last_login_at, created_at, updated_at, settings
		FROM human_user
		WHERE id = $1
	`, id)

	user, err := scanHumanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return HumanUser{}, ErrNotFound
	}
	if err != nil {
		return HumanUser{}, mapDBError(err)
	}
	return user, nil
}

func (r *HumanUserRepo) GetByEmail(ctx context.Context, organizationID uuid.UUID, email string) (HumanUser, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, email, display_name, password_hash, role, is_active, failed_login_attempts, locked_until, last_login_at, created_at, updated_at, settings
		FROM human_user
		WHERE organization_id = $1
		  AND email = $2
	`, organizationID, email)

	user, err := scanHumanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return HumanUser{}, ErrNotFound
	}
	if err != nil {
		return HumanUser{}, mapDBError(err)
	}
	return user, nil
}

func (r *HumanUserRepo) List(ctx context.Context, organizationID uuid.UUID) ([]HumanUser, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, email, display_name, password_hash, role, is_active, failed_login_attempts, locked_until, last_login_at, created_at, updated_at, settings
		FROM human_user
		WHERE organization_id = $1
		ORDER BY created_at
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	users := make([]HumanUser, 0)
	for rows.Next() {
		user, scanErr := scanHumanUser(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		users = append(users, user)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return users, nil
}

func (r *HumanUserRepo) Update(ctx context.Context, user HumanUserUpdate) (HumanUser, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE human_user
		SET email = $2,
			display_name = $3,
			password_hash = COALESCE($4, password_hash),
			role = $5,
			settings = $6::jsonb,
			last_login_at = COALESCE($7, last_login_at)
		WHERE id = $1
		RETURNING id, organization_id, email, display_name, password_hash, role, is_active, failed_login_attempts, locked_until, last_login_at, created_at, updated_at, settings
	`, user.ID, user.Email, user.DisplayName, user.PasswordHash, user.Role, normalizeSettings(user.Settings), user.LastLoginAt)

	updated, err := scanHumanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return HumanUser{}, ErrNotFound
	}
	if err != nil {
		return HumanUser{}, mapDBError(err)
	}
	return updated, nil
}

func (r *HumanUserRepo) SetActive(ctx context.Context, id uuid.UUID, active bool) (HumanUser, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE human_user
		SET is_active = $2
		WHERE id = $1
		RETURNING id, organization_id, email, display_name, password_hash, role, is_active, failed_login_attempts, locked_until, last_login_at, created_at, updated_at, settings
	`, id, active)

	user, err := scanHumanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return HumanUser{}, ErrNotFound
	}
	if err != nil {
		return HumanUser{}, mapDBError(err)
	}
	return user, nil
}

func (r *HumanUserRepo) IncrFailedAttempts(ctx context.Context, id uuid.UUID) (HumanUser, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE human_user
		SET failed_login_attempts = failed_login_attempts + 1
		WHERE id = $1
		RETURNING id, organization_id, email, display_name, password_hash, role, is_active, failed_login_attempts, locked_until, last_login_at, created_at, updated_at, settings
	`, id)

	user, err := scanHumanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return HumanUser{}, ErrNotFound
	}
	if err != nil {
		return HumanUser{}, mapDBError(err)
	}
	return user, nil
}

func (r *HumanUserRepo) ResetFailedAttempts(ctx context.Context, id uuid.UUID) (HumanUser, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE human_user
		SET failed_login_attempts = 0
		WHERE id = $1
		RETURNING id, organization_id, email, display_name, password_hash, role, is_active, failed_login_attempts, locked_until, last_login_at, created_at, updated_at, settings
	`, id)

	user, err := scanHumanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return HumanUser{}, ErrNotFound
	}
	if err != nil {
		return HumanUser{}, mapDBError(err)
	}
	return user, nil
}

func (r *HumanUserRepo) SetLockedUntil(ctx context.Context, id uuid.UUID, lockedUntil *time.Time) (HumanUser, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE human_user
		SET locked_until = $2
		WHERE id = $1
		RETURNING id, organization_id, email, display_name, password_hash, role, is_active, failed_login_attempts, locked_until, last_login_at, created_at, updated_at, settings
	`, id, lockedUntil)

	user, err := scanHumanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return HumanUser{}, ErrNotFound
	}
	if err != nil {
		return HumanUser{}, mapDBError(err)
	}
	return user, nil
}

func normalizeSettings(settings json.RawMessage) json.RawMessage {
	if len(settings) == 0 {
		return json.RawMessage(`{}`)
	}
	return settings
}

func scanHumanUser(row pgx.Row) (HumanUser, error) {
	var (
		user         HumanUser
		settingsJSON []byte
	)

	if err := row.Scan(
		&user.ID,
		&user.OrganizationID,
		&user.Email,
		&user.DisplayName,
		&user.PasswordHash,
		&user.Role,
		&user.IsActive,
		&user.FailedLoginAttempts,
		&user.LockedUntil,
		&user.LastLoginAt,
		&user.CreatedAt,
		&user.UpdatedAt,
		&settingsJSON,
	); err != nil {
		return HumanUser{}, err
	}

	if len(settingsJSON) == 0 {
		settingsJSON = []byte(`{}`)
	}
	if !json.Valid(settingsJSON) {
		return HumanUser{}, fmt.Errorf("invalid settings json")
	}
	user.Settings = json.RawMessage(settingsJSON)

	return user, nil
}
