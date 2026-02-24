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

type Secret struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Slug           string
	DisplayName    string
	Description    string
	Ciphertext     []byte
	Nonce          []byte
	KeyVersion     int
	CreatedByType  string
	CreatedByID    uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type SecretRepo struct {
	pool *pgxpool.Pool
}

func NewSecretRepo(pool *pgxpool.Pool) *SecretRepo {
	return &SecretRepo{pool: pool}
}

func (r *SecretRepo) Create(ctx context.Context, secret Secret) (Secret, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO secret (
			organization_id,
			slug,
			display_name,
			description,
			ciphertext,
			nonce,
			key_version,
			created_by_type,
			created_by_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, organization_id, slug, display_name, description, ciphertext, nonce, key_version, created_by_type, created_by_id, created_at, updated_at
	`, secret.OrganizationID, secret.Slug, secret.DisplayName, nullableText(secret.Description), secret.Ciphertext, secret.Nonce, defaultKeyVersion(secret.KeyVersion), secret.CreatedByType, secret.CreatedByID)

	created, err := scanSecret(row)
	if err != nil {
		return Secret{}, mapDBError(err)
	}
	return created, nil
}

func (r *SecretRepo) GetBySlug(ctx context.Context, organizationID uuid.UUID, slug string) (Secret, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, slug, display_name, description, ciphertext, nonce, key_version, created_by_type, created_by_id, created_at, updated_at
		FROM secret
		WHERE organization_id = $1
		  AND slug = $2
	`, organizationID, slug)

	secret, err := scanSecret(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Secret{}, ErrNotFound
	}
	if err != nil {
		return Secret{}, mapDBError(err)
	}
	return secret, nil
}

func (r *SecretRepo) List(ctx context.Context, organizationID uuid.UUID) ([]Secret, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, slug, display_name, description, ciphertext, nonce, key_version, created_by_type, created_by_id, created_at, updated_at
		FROM secret
		WHERE organization_id = $1
		ORDER BY created_at
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	secrets := make([]Secret, 0)
	for rows.Next() {
		secret, scanErr := scanSecret(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		secrets = append(secrets, secret)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return secrets, nil
}

func (r *SecretRepo) Update(ctx context.Context, secret Secret) (Secret, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE secret
		SET display_name = $3,
			description = $4,
			ciphertext = $5,
			nonce = $6,
			key_version = $7
		WHERE id = $1
		  AND organization_id = $2
		RETURNING id, organization_id, slug, display_name, description, ciphertext, nonce, key_version, created_by_type, created_by_id, created_at, updated_at
	`, secret.ID, secret.OrganizationID, secret.DisplayName, nullableText(secret.Description), secret.Ciphertext, secret.Nonce, defaultKeyVersion(secret.KeyVersion))

	updated, err := scanSecret(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Secret{}, ErrNotFound
	}
	if err != nil {
		return Secret{}, mapDBError(err)
	}
	return updated, nil
}

func (r *SecretRepo) Delete(ctx context.Context, organizationID uuid.UUID, slug string) error {
	ct, err := r.pool.Exec(ctx, `
		DELETE FROM secret
		WHERE organization_id = $1
		  AND slug = $2
	`, organizationID, slug)
	if err != nil {
		return mapDBError(err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *SecretRepo) FindTransportConfigReferences(ctx context.Context, organizationID uuid.UUID, slug string) ([]string, error) {
	exists, err := r.tableExists(ctx, "mcp_connection")
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	rows, err := r.pool.Query(ctx, `
		SELECT COALESCE(NULLIF(slug, ''), NULLIF(display_name, ''), id::text)
		FROM mcp_connection
		WHERE organization_id = $1
		  AND transport_config::text LIKE $2
		ORDER BY 1
	`, organizationID, "%ref:"+slug+"%")
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	references := make([]string, 0)
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			return nil, mapDBError(err)
		}
		references = append(references, fmt.Sprintf("mcp_connection '%s' transport_config", label))
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return references, nil
}

func (r *SecretRepo) FindSecretBindingReferences(ctx context.Context, organizationID uuid.UUID, slug string) ([]string, error) {
	connectionExists, err := r.tableExists(ctx, "mcp_connection")
	if err != nil {
		return nil, err
	}
	bindingExists, err := r.tableExists(ctx, "mcp_secret_binding")
	if err != nil {
		return nil, err
	}
	if !connectionExists || !bindingExists {
		return nil, nil
	}

	rows, err := r.pool.Query(ctx, `
		SELECT
			COALESCE(NULLIF(c.slug, ''), NULLIF(c.display_name, ''), c.id::text) AS connection_label,
			b.env_var_name
		FROM mcp_secret_binding b
		JOIN mcp_connection c ON c.id = b.connection_id
		WHERE c.organization_id = $1
		  AND b.secret_ref = $2
		ORDER BY 1, 2
	`, organizationID, "ref:"+slug)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	references := make([]string, 0)
	for rows.Next() {
		var (
			connectionLabel string
			envVarName      string
		)
		if err := rows.Scan(&connectionLabel, &envVarName); err != nil {
			return nil, mapDBError(err)
		}
		references = append(references, fmt.Sprintf("mcp_secret_binding '%s' env_var_name '%s'", connectionLabel, envVarName))
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return references, nil
}

func (r *SecretRepo) tableExists(ctx context.Context, tableName string) (bool, error) {
	var exists *string
	if err := r.pool.QueryRow(ctx, `SELECT to_regclass($1)::text`, strings.TrimSpace(tableName)).Scan(&exists); err != nil {
		return false, mapDBError(err)
	}
	return exists != nil, nil
}

func scanSecret(row pgx.Row) (Secret, error) {
	var (
		secret      Secret
		description *string
	)

	if err := row.Scan(
		&secret.ID,
		&secret.OrganizationID,
		&secret.Slug,
		&secret.DisplayName,
		&description,
		&secret.Ciphertext,
		&secret.Nonce,
		&secret.KeyVersion,
		&secret.CreatedByType,
		&secret.CreatedByID,
		&secret.CreatedAt,
		&secret.UpdatedAt,
	); err != nil {
		return Secret{}, err
	}

	if description != nil {
		secret.Description = *description
	}

	secret.Ciphertext = cloneBytes(secret.Ciphertext)
	secret.Nonce = cloneBytes(secret.Nonce)

	return secret, nil
}

func cloneBytes(value []byte) []byte {
	if len(value) == 0 {
		return nil
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out
}

func defaultKeyVersion(version int) int {
	if version <= 0 {
		return 1
	}
	return version
}

func nullableText(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
