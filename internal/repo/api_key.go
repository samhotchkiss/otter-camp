package repo

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
	pool dbQuerier
}

func NewAPIKeyRepo(pool *pgxpool.Pool) *APIKeyRepo {
	return &APIKeyRepo{pool: pool}
}

func newAPIKeyRepoWithQuerier(q dbQuerier) *APIKeyRepo {
	return &APIKeyRepo{pool: q}
}

func (r *APIKeyRepo) Create(ctx context.Context, key APIKey) (APIKey, error) {
	scopes := key.Scopes
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
			expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, user_id, key_hash, key_prefix, display_name, scopes, created_at, last_used_at, expires_at, revoked_at
	`, key.UserID, key.KeyHash, key.KeyPrefix, key.DisplayName, scopes, key.ExpiresAt)

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

	key, err := scanAPIKey(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, ErrNotFound
	}
	if err != nil {
		return APIKey{}, mapDBError(err)
	}
	return key, nil
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

	keys := make([]APIKey, 0)
	for rows.Next() {
		key, scanErr := scanAPIKey(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		keys = append(keys, key)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return keys, nil
}

func (r *APIKeyRepo) Revoke(ctx context.Context, id uuid.UUID) (APIKey, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE api_key
		SET revoked_at = COALESCE(revoked_at, now())
		WHERE id = $1
		RETURNING id, user_id, key_hash, key_prefix, display_name, scopes, created_at, last_used_at, expires_at, revoked_at
	`, id)

	key, err := scanAPIKey(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, ErrNotFound
	}
	if err != nil {
		return APIKey{}, mapDBError(err)
	}
	return key, nil
}

func scanAPIKey(row pgx.Row) (APIKey, error) {
	var key APIKey
	if err := row.Scan(
		&key.ID,
		&key.UserID,
		&key.KeyHash,
		&key.KeyPrefix,
		&key.DisplayName,
		&key.Scopes,
		&key.CreatedAt,
		&key.LastUsedAt,
		&key.ExpiresAt,
		&key.RevokedAt,
	); err != nil {
		return APIKey{}, err
	}
	return key, nil
}
