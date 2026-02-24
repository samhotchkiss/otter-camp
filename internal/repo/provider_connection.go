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

type ProviderConnection struct {
	ID                 uuid.UUID
	OrganizationID     uuid.UUID
	ProviderID         uuid.UUID
	DisplayName        string
	APIKeyRef          string
	APIBaseURLOverride *string
	FailoverPriority   int
	HealthStatus       string
	IsEnabled          bool
	Metadata           json.RawMessage
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ProviderConnectionRepo struct {
	pool *pgxpool.Pool
}

func NewProviderConnectionRepo(pool *pgxpool.Pool) *ProviderConnectionRepo {
	return &ProviderConnectionRepo{pool: pool}
}

func (r *ProviderConnectionRepo) Create(ctx context.Context, connection ProviderConnection) (ProviderConnection, error) {
	metadata := connection.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}

	failoverPriority := connection.FailoverPriority
	if failoverPriority == 0 {
		failoverPriority = 100
	}

	healthStatus := connection.HealthStatus
	if healthStatus == "" {
		healthStatus = "healthy"
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO provider_connection (
			organization_id,
			provider_id,
			display_name,
			api_key_ref,
			api_base_url_override,
			failover_priority,
			health_status,
			is_enabled,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
		RETURNING id, organization_id, provider_id, display_name, api_key_ref, api_base_url_override, failover_priority, health_status, is_enabled, metadata, created_at, updated_at
	`, connection.OrganizationID, connection.ProviderID, connection.DisplayName, connection.APIKeyRef, connection.APIBaseURLOverride, failoverPriority, healthStatus, connection.IsEnabled, metadata)

	created, err := scanProviderConnection(row)
	if err != nil {
		return ProviderConnection{}, mapDBError(err)
	}
	return created, nil
}

func (r *ProviderConnectionRepo) GetByID(ctx context.Context, id uuid.UUID) (ProviderConnection, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, provider_id, display_name, api_key_ref, api_base_url_override, failover_priority, health_status, is_enabled, metadata, created_at, updated_at
		FROM provider_connection
		WHERE id = $1
	`, id)

	connection, err := scanProviderConnection(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProviderConnection{}, ErrNotFound
	}
	if err != nil {
		return ProviderConnection{}, mapDBError(err)
	}
	return connection, nil
}

func (r *ProviderConnectionRepo) List(ctx context.Context, organizationID uuid.UUID) ([]ProviderConnection, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, provider_id, display_name, api_key_ref, api_base_url_override, failover_priority, health_status, is_enabled, metadata, created_at, updated_at
		FROM provider_connection
		WHERE organization_id = $1
		ORDER BY failover_priority ASC, created_at ASC
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ProviderConnection, 0)
	for rows.Next() {
		connection, scanErr := scanProviderConnection(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, connection)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *ProviderConnectionRepo) Update(ctx context.Context, connection ProviderConnection) (ProviderConnection, error) {
	metadata := connection.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}

	row := r.pool.QueryRow(ctx, `
		UPDATE provider_connection
		SET display_name = $2,
			api_key_ref = $3,
			api_base_url_override = $4,
			failover_priority = $5,
			metadata = $6::jsonb
		WHERE id = $1
		RETURNING id, organization_id, provider_id, display_name, api_key_ref, api_base_url_override, failover_priority, health_status, is_enabled, metadata, created_at, updated_at
	`, connection.ID, connection.DisplayName, connection.APIKeyRef, connection.APIBaseURLOverride, connection.FailoverPriority, metadata)

	updated, err := scanProviderConnection(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProviderConnection{}, ErrNotFound
	}
	if err != nil {
		return ProviderConnection{}, mapDBError(err)
	}
	return updated, nil
}

func (r *ProviderConnectionRepo) SetHealthStatus(ctx context.Context, id uuid.UUID, healthStatus string) (ProviderConnection, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE provider_connection
		SET health_status = $2
		WHERE id = $1
		RETURNING id, organization_id, provider_id, display_name, api_key_ref, api_base_url_override, failover_priority, health_status, is_enabled, metadata, created_at, updated_at
	`, id, healthStatus)

	updated, err := scanProviderConnection(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProviderConnection{}, ErrNotFound
	}
	if err != nil {
		return ProviderConnection{}, mapDBError(err)
	}
	return updated, nil
}

func (r *ProviderConnectionRepo) SetEnabled(ctx context.Context, id uuid.UUID, enabled bool) (ProviderConnection, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE provider_connection
		SET is_enabled = $2
		WHERE id = $1
		RETURNING id, organization_id, provider_id, display_name, api_key_ref, api_base_url_override, failover_priority, health_status, is_enabled, metadata, created_at, updated_at
	`, id, enabled)

	updated, err := scanProviderConnection(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProviderConnection{}, ErrNotFound
	}
	if err != nil {
		return ProviderConnection{}, mapDBError(err)
	}
	return updated, nil
}

func (r *ProviderConnectionRepo) ListByProvider(ctx context.Context, organizationID, providerID uuid.UUID) ([]ProviderConnection, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, provider_id, display_name, api_key_ref, api_base_url_override, failover_priority, health_status, is_enabled, metadata, created_at, updated_at
		FROM provider_connection
		WHERE organization_id = $1
		  AND provider_id = $2
		ORDER BY failover_priority ASC, created_at ASC
	`, organizationID, providerID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ProviderConnection, 0)
	for rows.Next() {
		connection, scanErr := scanProviderConnection(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, connection)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return items, nil
}

func scanProviderConnection(row pgx.Row) (ProviderConnection, error) {
	var connection ProviderConnection
	if err := row.Scan(
		&connection.ID,
		&connection.OrganizationID,
		&connection.ProviderID,
		&connection.DisplayName,
		&connection.APIKeyRef,
		&connection.APIBaseURLOverride,
		&connection.FailoverPriority,
		&connection.HealthStatus,
		&connection.IsEnabled,
		&connection.Metadata,
		&connection.CreatedAt,
		&connection.UpdatedAt,
	); err != nil {
		return ProviderConnection{}, err
	}

	if len(connection.Metadata) == 0 {
		connection.Metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(connection.Metadata) {
		return ProviderConnection{}, fmt.Errorf("invalid metadata json")
	}

	return connection, nil
}
