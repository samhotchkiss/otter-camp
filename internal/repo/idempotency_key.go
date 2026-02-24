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

var ErrIdempotencyConflict = errors.New("repo: idempotency conflict")

type IdempotencyKey struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	KeyHash        string
	RequestHash    string
	ResponseStatus int
	ResponseBody   json.RawMessage
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

type IdempotencyKeyRepo struct {
	pool *pgxpool.Pool
}

func NewIdempotencyKeyRepo(pool *pgxpool.Pool) *IdempotencyKeyRepo {
	return &IdempotencyKeyRepo{pool: pool}
}

func (r *IdempotencyKeyRepo) Store(ctx context.Context, key IdempotencyKey) (IdempotencyKey, error) {
	if key.OrganizationID == uuid.Nil {
		return IdempotencyKey{}, fmt.Errorf("organization_id is required")
	}
	if strings.TrimSpace(key.KeyHash) == "" {
		return IdempotencyKey{}, fmt.Errorf("key_hash is required")
	}
	if strings.TrimSpace(key.RequestHash) == "" {
		return IdempotencyKey{}, fmt.Errorf("request_hash is required")
	}

	expiresAt := key.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = time.Now().UTC().Add(24 * time.Hour)
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO idempotency_key (
			organization_id,
			key_hash,
			request_hash,
			response_status,
			response_body,
			expires_at
		)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6)
		ON CONFLICT (organization_id, key_hash) DO NOTHING
		RETURNING id, organization_id, key_hash, request_hash, response_status, response_body, created_at, expires_at
	`, key.OrganizationID, key.KeyHash, key.RequestHash, key.ResponseStatus, normalizeResponseBody(key.ResponseBody), expiresAt)

	stored, err := scanIdempotencyKey(row)
	if err == nil {
		return stored, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return IdempotencyKey{}, mapDBError(err)
	}

	existing, err := r.Get(ctx, key.OrganizationID, key.KeyHash)
	if err != nil {
		return IdempotencyKey{}, err
	}
	if hashesConflict(existing.RequestHash, key.RequestHash) {
		return IdempotencyKey{}, ErrIdempotencyConflict
	}

	return existing, nil
}

func (r *IdempotencyKeyRepo) Get(ctx context.Context, organizationID uuid.UUID, keyHash string) (IdempotencyKey, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, key_hash, request_hash, response_status, response_body, created_at, expires_at
		FROM idempotency_key
		WHERE organization_id = $1
		  AND key_hash = $2
	`, organizationID, keyHash)

	found, err := scanIdempotencyKey(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return IdempotencyKey{}, ErrNotFound
	}
	if err != nil {
		return IdempotencyKey{}, mapDBError(err)
	}
	return found, nil
}

func (r *IdempotencyKeyRepo) CheckConflict(ctx context.Context, organizationID uuid.UUID, keyHash, requestHash string) error {
	existing, err := r.Get(ctx, organizationID, keyHash)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	if hashesConflict(existing.RequestHash, requestHash) {
		return ErrIdempotencyConflict
	}

	return nil
}

func scanIdempotencyKey(row pgx.Row) (IdempotencyKey, error) {
	var (
		key      IdempotencyKey
		response []byte
	)

	if err := row.Scan(
		&key.ID,
		&key.OrganizationID,
		&key.KeyHash,
		&key.RequestHash,
		&key.ResponseStatus,
		&response,
		&key.CreatedAt,
		&key.ExpiresAt,
	); err != nil {
		return IdempotencyKey{}, err
	}

	if len(response) > 0 {
		key.ResponseBody = json.RawMessage(response)
	}

	return key, nil
}

func normalizeResponseBody(body json.RawMessage) json.RawMessage {
	if len(body) == 0 {
		return nil
	}
	return body
}

func hashesConflict(existingRequestHash, incomingRequestHash string) bool {
	return strings.TrimSpace(existingRequestHash) != strings.TrimSpace(incomingRequestHash)
}
