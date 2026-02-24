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

type HumanUserRepo struct {
	pool *pgxpool.Pool
}

func NewHumanUserRepo(pool *pgxpool.Pool) *HumanUserRepo {
	return &HumanUserRepo{pool: pool}
}

func (r *HumanUserRepo) Create(ctx context.Context, user HumanUser) (HumanUser, error) {
	settings, err := normalizeJSON(user.Settings)
	if err != nil {
		return HumanUser{}, fmt.Errorf("normalize settings: %w", err)
	}

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
	`, user.OrganizationID, user.Email, user.DisplayName, user.PasswordHash, user.Role, user.IsActive, user.FailedLoginAttempts, user.LockedUntil, user.LastLoginAt, settings)

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
		WHERE organization_id = $1 AND email = $2
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

const humanUserUpdateQuery = `
UPDATE human_user
SET email = $2,
	display_name = $3,
	role = $4,
	settings = $5::jsonb,
	password_hash = CASE WHEN $6 THEN $7 ELSE password_hash END
WHERE id = $1
RETURNING id, organization_id, email, display_name, password_hash, role, is_active, failed_login_attempts, locked_until, last_login_at, created_at, updated_at, settings
`

func (r *HumanUserRepo) Update(ctx context.Context, user HumanUser) (HumanUser, error) {
	settings, err := normalizeJSON(user.Settings)
	if err != nil {
		return HumanUser{}, fmt.Errorf("normalize settings: %w", err)
	}

	passwordHashSet, passwordHash := passwordHashUpdateArg(user.PasswordHash)
	row := r.pool.QueryRow(ctx, humanUserUpdateQuery, user.ID, user.Email, user.DisplayName, user.Role, settings, passwordHashSet, passwordHash)

	updated, err := scanHumanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return HumanUser{}, ErrNotFound
	}
	if err != nil {
		return HumanUser{}, mapDBError(err)
	}

	return updated, nil
}

func (r *HumanUserRepo) SetActive(ctx context.Context, id uuid.UUID, isActive bool) (HumanUser, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE human_user
		SET is_active = $2
		WHERE id = $1
		RETURNING id, organization_id, email, display_name, password_hash, role, is_active, failed_login_attempts, locked_until, last_login_at, created_at, updated_at, settings
	`, id, isActive)

	updated, err := scanHumanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return HumanUser{}, ErrNotFound
	}
	if err != nil {
		return HumanUser{}, mapDBError(err)
	}

	return updated, nil
}

func (r *HumanUserRepo) IncrFailedAttempts(ctx context.Context, id uuid.UUID) (HumanUser, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE human_user
		SET failed_login_attempts = failed_login_attempts + 1
		WHERE id = $1
		RETURNING id, organization_id, email, display_name, password_hash, role, is_active, failed_login_attempts, locked_until, last_login_at, created_at, updated_at, settings
	`, id)

	updated, err := scanHumanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return HumanUser{}, ErrNotFound
	}
	if err != nil {
		return HumanUser{}, mapDBError(err)
	}

	return updated, nil
}

func (r *HumanUserRepo) ResetFailedAttempts(ctx context.Context, id uuid.UUID) (HumanUser, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE human_user
		SET failed_login_attempts = 0,
			locked_until = NULL
		WHERE id = $1
		RETURNING id, organization_id, email, display_name, password_hash, role, is_active, failed_login_attempts, locked_until, last_login_at, created_at, updated_at, settings
	`, id)

	updated, err := scanHumanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return HumanUser{}, ErrNotFound
	}
	if err != nil {
		return HumanUser{}, mapDBError(err)
	}

	return updated, nil
}

func (r *HumanUserRepo) SetLockedUntil(ctx context.Context, id uuid.UUID, lockedUntil *time.Time) (HumanUser, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE human_user
		SET locked_until = $2
		WHERE id = $1
		RETURNING id, organization_id, email, display_name, password_hash, role, is_active, failed_login_attempts, locked_until, last_login_at, created_at, updated_at, settings
	`, id, lockedUntil)

	updated, err := scanHumanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return HumanUser{}, ErrNotFound
	}
	if err != nil {
		return HumanUser{}, mapDBError(err)
	}

	return updated, nil
}

func passwordHashUpdateArg(passwordHash *string) (bool, *string) {
	if passwordHash == nil {
		return false, nil
	}
	return true, passwordHash
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

	normalized, err := normalizeJSON(settingsJSON)
	if err != nil {
		return HumanUser{}, fmt.Errorf("normalize settings: %w", err)
	}
	user.Settings = normalized

	return user, nil
}

type AuthSession struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	TokenHash  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastUsedAt time.Time
	RevokedAt  *time.Time
	UserAgent  *string
	IPAddress  *string
	User       *HumanUser
}

type AuthSessionRepo struct {
	pool *pgxpool.Pool
}

func NewAuthSessionRepo(pool *pgxpool.Pool) *AuthSessionRepo {
	return &AuthSessionRepo{pool: pool}
}

func (r *AuthSessionRepo) Create(ctx context.Context, session AuthSession) (AuthSession, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO auth_session (
			user_id,
			token_hash,
			expires_at,
			revoked_at,
			user_agent,
			ip_address
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, user_id, token_hash, expires_at, created_at, last_used_at, revoked_at, user_agent, ip_address
	`, session.UserID, session.TokenHash, session.ExpiresAt, session.RevokedAt, session.UserAgent, session.IPAddress)

	created, err := scanAuthSession(row)
	if err != nil {
		return AuthSession{}, mapDBError(err)
	}

	return created, nil
}

const authSessionGetByTokenHashQuery = `
SELECT
	s.id,
	s.user_id,
	s.token_hash,
	s.expires_at,
	s.created_at,
	s.last_used_at,
	s.revoked_at,
	s.user_agent,
	s.ip_address,
	u.id,
	u.organization_id,
	u.email,
	u.display_name,
	u.password_hash,
	u.role,
	u.is_active,
	u.failed_login_attempts,
	u.locked_until,
	u.last_login_at,
	u.created_at,
	u.updated_at,
	u.settings
FROM auth_session s
JOIN human_user u ON u.id = s.user_id
WHERE s.token_hash = $1
`

// GetByTokenHash returns sessions regardless of revoked status so callers can
// inspect RevokedAt and decide whether the session is usable.
func (r *AuthSessionRepo) GetByTokenHash(ctx context.Context, tokenHash string) (AuthSession, error) {
	row := r.pool.QueryRow(ctx, authSessionGetByTokenHashQuery, tokenHash)

	session, err := scanAuthSessionWithUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthSession{}, ErrNotFound
	}
	if err != nil {
		return AuthSession{}, mapDBError(err)
	}

	return session, nil
}

func (r *AuthSessionRepo) Revoke(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `
		UPDATE auth_session
		SET revoked_at = COALESCE(revoked_at, now())
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

func (r *AuthSessionRepo) RevokeAll(ctx context.Context, userID uuid.UUID) (int64, error) {
	ct, err := r.pool.Exec(ctx, `
		UPDATE auth_session
		SET revoked_at = now()
		WHERE user_id = $1
		  AND revoked_at IS NULL
	`, userID)
	if err != nil {
		return 0, mapDBError(err)
	}
	return ct.RowsAffected(), nil
}

func (r *AuthSessionRepo) TouchLastUsed(ctx context.Context, id uuid.UUID) (AuthSession, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE auth_session
		SET last_used_at = now()
		WHERE id = $1
		RETURNING id, user_id, token_hash, expires_at, created_at, last_used_at, revoked_at, user_agent, ip_address
	`, id)

	updated, err := scanAuthSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthSession{}, ErrNotFound
	}
	if err != nil {
		return AuthSession{}, mapDBError(err)
	}

	return updated, nil
}

func (r *AuthSessionRepo) ExtendExpiry(ctx context.Context, id uuid.UUID, expiresAt time.Time) (AuthSession, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE auth_session
		SET expires_at = $2
		WHERE id = $1
		RETURNING id, user_id, token_hash, expires_at, created_at, last_used_at, revoked_at, user_agent, ip_address
	`, id, expiresAt)

	updated, err := scanAuthSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthSession{}, ErrNotFound
	}
	if err != nil {
		return AuthSession{}, mapDBError(err)
	}

	return updated, nil
}

func (r *AuthSessionRepo) ListActive(ctx context.Context, userID uuid.UUID) ([]AuthSession, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, token_hash, expires_at, created_at, last_used_at, revoked_at, user_agent, ip_address
		FROM auth_session
		WHERE user_id = $1
		  AND revoked_at IS NULL
		  AND expires_at > now()
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	sessions := make([]AuthSession, 0)
	for rows.Next() {
		session, scanErr := scanAuthSession(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		sessions = append(sessions, session)
	}

	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return sessions, nil
}

func (r *AuthSessionRepo) DeleteExpired(ctx context.Context) (int64, error) {
	ct, err := r.pool.Exec(ctx, `
		DELETE FROM auth_session
		WHERE expires_at < now()
		  AND revoked_at IS NULL
	`)
	if err != nil {
		return 0, mapDBError(err)
	}
	return ct.RowsAffected(), nil
}

func scanAuthSession(row pgx.Row) (AuthSession, error) {
	var session AuthSession
	if err := row.Scan(
		&session.ID,
		&session.UserID,
		&session.TokenHash,
		&session.ExpiresAt,
		&session.CreatedAt,
		&session.LastUsedAt,
		&session.RevokedAt,
		&session.UserAgent,
		&session.IPAddress,
	); err != nil {
		return AuthSession{}, err
	}
	return session, nil
}

func scanAuthSessionWithUser(row pgx.Row) (AuthSession, error) {
	var (
		session      AuthSession
		user         HumanUser
		settingsJSON []byte
	)

	if err := row.Scan(
		&session.ID,
		&session.UserID,
		&session.TokenHash,
		&session.ExpiresAt,
		&session.CreatedAt,
		&session.LastUsedAt,
		&session.RevokedAt,
		&session.UserAgent,
		&session.IPAddress,
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
		return AuthSession{}, err
	}

	normalized, err := normalizeJSON(settingsJSON)
	if err != nil {
		return AuthSession{}, fmt.Errorf("normalize settings: %w", err)
	}
	user.Settings = normalized

	session.User = &user
	return session, nil
}

type APIKey struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	KeyHash     string
	KeyPrefix   string
	DisplayName string
	Scopes      []string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
}

type APIKeyRepo struct {
	pool *pgxpool.Pool
}

func NewAPIKeyRepo(pool *pgxpool.Pool) *APIKeyRepo {
	return &APIKeyRepo{pool: pool}
}

func (r *APIKeyRepo) Create(ctx context.Context, apiKey APIKey) (APIKey, error) {
	scopes := apiKey.Scopes
	if scopes == nil {
		scopes = []string{}
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO api_key (
			user_id,
			key_hash,
			key_prefix,
			display_name,
			scopes,
			expires_at,
			revoked_at,
			last_used_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, user_id, key_hash, key_prefix, display_name, scopes, created_at, last_used_at, expires_at, revoked_at
	`, apiKey.UserID, apiKey.KeyHash, apiKey.KeyPrefix, apiKey.DisplayName, scopes, apiKey.ExpiresAt, apiKey.RevokedAt, apiKey.LastUsedAt)

	created, err := scanAPIKey(row)
	if err != nil {
		return APIKey{}, mapDBError(err)
	}

	return created, nil
}

func (r *APIKeyRepo) GetByKeyHash(ctx context.Context, keyHash string) (APIKey, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, user_id, key_hash, key_prefix, display_name, scopes, created_at, last_used_at, expires_at, revoked_at
		FROM api_key
		WHERE key_hash = $1
	`, keyHash)

	apiKey, err := scanAPIKey(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, ErrNotFound
	}
	if err != nil {
		return APIKey{}, mapDBError(err)
	}

	return apiKey, nil
}

func (r *APIKeyRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]APIKey, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, key_hash, key_prefix, display_name, scopes, created_at, last_used_at, expires_at, revoked_at
		FROM api_key
		WHERE user_id = $1
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	apiKeys := make([]APIKey, 0)
	for rows.Next() {
		apiKey, scanErr := scanAPIKey(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		apiKeys = append(apiKeys, apiKey)
	}

	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return apiKeys, nil
}

func (r *APIKeyRepo) Revoke(ctx context.Context, id uuid.UUID) (APIKey, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE api_key
		SET revoked_at = COALESCE(revoked_at, now())
		WHERE id = $1
		RETURNING id, user_id, key_hash, key_prefix, display_name, scopes, created_at, last_used_at, expires_at, revoked_at
	`, id)

	updated, err := scanAPIKey(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, ErrNotFound
	}
	if err != nil {
		return APIKey{}, mapDBError(err)
	}

	return updated, nil
}

func (r *APIKeyRepo) TouchLastUsed(ctx context.Context, id uuid.UUID) (APIKey, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE api_key
		SET last_used_at = now()
		WHERE id = $1
		RETURNING id, user_id, key_hash, key_prefix, display_name, scopes, created_at, last_used_at, expires_at, revoked_at
	`, id)

	updated, err := scanAPIKey(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, ErrNotFound
	}
	if err != nil {
		return APIKey{}, mapDBError(err)
	}

	return updated, nil
}

func scanAPIKey(row pgx.Row) (APIKey, error) {
	var apiKey APIKey
	if err := row.Scan(
		&apiKey.ID,
		&apiKey.UserID,
		&apiKey.KeyHash,
		&apiKey.KeyPrefix,
		&apiKey.DisplayName,
		&apiKey.Scopes,
		&apiKey.CreatedAt,
		&apiKey.LastUsedAt,
		&apiKey.ExpiresAt,
		&apiKey.RevokedAt,
	); err != nil {
		return APIKey{}, err
	}

	if apiKey.Scopes == nil {
		apiKey.Scopes = []string{}
	}

	return apiKey, nil
}

func normalizeJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid json")
	}
	return raw, nil
}
