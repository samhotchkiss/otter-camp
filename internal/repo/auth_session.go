package repo

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
	pool dbQuerier
}

func NewAuthSessionRepo(pool *pgxpool.Pool) *AuthSessionRepo {
	return &AuthSessionRepo{pool: pool}
}

func newAuthSessionRepoWithQuerier(q dbQuerier) *AuthSessionRepo {
	return &AuthSessionRepo{pool: q}
}

func (r *AuthSessionRepo) Create(ctx context.Context, session AuthSession) (AuthSession, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO auth_session (
			user_id,
			token_hash,
			expires_at,
			user_agent,
			ip_address
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, user_id, token_hash, expires_at, created_at, last_used_at, revoked_at, user_agent, ip_address
	`, session.UserID, session.TokenHash, session.ExpiresAt, session.UserAgent, session.IPAddress)

	created, err := scanAuthSession(row)
	if err != nil {
		return AuthSession{}, mapDBError(err)
	}
	return created, nil
}

func (r *AuthSessionRepo) GetByTokenHash(ctx context.Context, tokenHash string) (AuthSession, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			s.id, s.user_id, s.token_hash, s.expires_at, s.created_at, s.last_used_at, s.revoked_at, s.user_agent, s.ip_address,
			u.id, u.organization_id, u.email, u.display_name, u.password_hash, u.role, u.is_active, u.failed_login_attempts, u.locked_until, u.last_login_at, u.created_at, u.updated_at, u.settings
		FROM auth_session s
		JOIN human_user u ON u.id = s.user_id
		WHERE s.token_hash = $1
	`, tokenHash)

	session, err := scanAuthSessionWithUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthSession{}, ErrNotFound
	}
	if err != nil {
		return AuthSession{}, mapDBError(err)
	}
	return session, nil
}

func (r *AuthSessionRepo) Revoke(ctx context.Context, id uuid.UUID) (AuthSession, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE auth_session
		SET revoked_at = COALESCE(revoked_at, now())
		WHERE id = $1
		RETURNING id, user_id, token_hash, expires_at, created_at, last_used_at, revoked_at, user_agent, ip_address
	`, id)

	session, err := scanAuthSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthSession{}, ErrNotFound
	}
	if err != nil {
		return AuthSession{}, mapDBError(err)
	}
	return session, nil
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

func (r *AuthSessionRepo) TouchLastUsed(ctx context.Context, id uuid.UUID, lastUsedAt time.Time) (AuthSession, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE auth_session
		SET last_used_at = $2
		WHERE id = $1
		RETURNING id, user_id, token_hash, expires_at, created_at, last_used_at, revoked_at, user_agent, ip_address
	`, id, lastUsedAt)

	session, err := scanAuthSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthSession{}, ErrNotFound
	}
	if err != nil {
		return AuthSession{}, mapDBError(err)
	}
	return session, nil
}

func (r *AuthSessionRepo) ExtendExpiry(ctx context.Context, id uuid.UUID, expiresAt time.Time) (AuthSession, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE auth_session
		SET expires_at = $2
		WHERE id = $1
		RETURNING id, user_id, token_hash, expires_at, created_at, last_used_at, revoked_at, user_agent, ip_address
	`, id, expiresAt)

	session, err := scanAuthSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthSession{}, ErrNotFound
	}
	if err != nil {
		return AuthSession{}, mapDBError(err)
	}
	return session, nil
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
	var session AuthSession
	var user HumanUser
	var settingsJSON []byte

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

	if len(settingsJSON) == 0 {
		settingsJSON = []byte(`{}`)
	}
	user.Settings = settingsJSON
	session.User = &user

	return session, nil
}
