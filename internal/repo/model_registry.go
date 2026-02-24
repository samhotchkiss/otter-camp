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

func (r *ProviderConnectionRepo) Create(ctx context.Context, conn ProviderConnection) (ProviderConnection, error) {
	metadata, err := normalizeRawJSON(conn.Metadata)
	if err != nil {
		return ProviderConnection{}, fmt.Errorf("normalize metadata: %w", err)
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
	`, conn.OrganizationID, conn.ProviderID, conn.DisplayName, conn.APIKeyRef, conn.APIBaseURLOverride, conn.FailoverPriority, conn.HealthStatus, conn.IsEnabled, metadata)

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

	conn, err := scanProviderConnection(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProviderConnection{}, ErrNotFound
	}
	if err != nil {
		return ProviderConnection{}, mapDBError(err)
	}
	return conn, nil
}

func (r *ProviderConnectionRepo) List(ctx context.Context, organizationID uuid.UUID) ([]ProviderConnection, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, provider_id, display_name, api_key_ref, api_base_url_override, failover_priority, health_status, is_enabled, metadata, created_at, updated_at
		FROM provider_connection
		WHERE organization_id = $1
		ORDER BY provider_id, failover_priority, created_at
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ProviderConnection, 0)
	for rows.Next() {
		item, scanErr := scanProviderConnection(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}

	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return items, nil
}

func (r *ProviderConnectionRepo) Update(ctx context.Context, conn ProviderConnection) (ProviderConnection, error) {
	metadata, err := normalizeRawJSON(conn.Metadata)
	if err != nil {
		return ProviderConnection{}, fmt.Errorf("normalize metadata: %w", err)
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
	`, conn.ID, conn.DisplayName, conn.APIKeyRef, conn.APIBaseURLOverride, conn.FailoverPriority, metadata)

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

func (r *ProviderConnectionRepo) SetEnabled(ctx context.Context, id uuid.UUID, isEnabled bool) (ProviderConnection, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE provider_connection
		SET is_enabled = $2
		WHERE id = $1
		RETURNING id, organization_id, provider_id, display_name, api_key_ref, api_base_url_override, failover_priority, health_status, is_enabled, metadata, created_at, updated_at
	`, id, isEnabled)

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
		ORDER BY failover_priority, created_at
	`, organizationID, providerID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ProviderConnection, 0)
	for rows.Next() {
		item, scanErr := scanProviderConnection(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}

	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return items, nil
}

func scanProviderConnection(row pgx.Row) (ProviderConnection, error) {
	var (
		conn         ProviderConnection
		metadataJSON []byte
	)
	if err := row.Scan(
		&conn.ID,
		&conn.OrganizationID,
		&conn.ProviderID,
		&conn.DisplayName,
		&conn.APIKeyRef,
		&conn.APIBaseURLOverride,
		&conn.FailoverPriority,
		&conn.HealthStatus,
		&conn.IsEnabled,
		&metadataJSON,
		&conn.CreatedAt,
		&conn.UpdatedAt,
	); err != nil {
		return ProviderConnection{}, err
	}

	normalized, err := normalizeRawJSON(metadataJSON)
	if err != nil {
		return ProviderConnection{}, fmt.Errorf("normalize metadata: %w", err)
	}
	conn.Metadata = normalized

	return conn, nil
}

type ModelProfile struct {
	ID                  uuid.UUID
	LogicalProfileID    string
	OrganizationID      *uuid.UUID
	Version             int
	IsCurrent           bool
	ProviderID          uuid.UUID
	ModelName           string
	ContextWindowTokens int
	MaxOutputTokens     int
	SupportsStreaming   bool
	SupportsVision      bool
	Temperature         *float64
	InvocationPurpose   string
	FallbackProfileID   *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type ModelProfileRepo struct {
	pool *pgxpool.Pool
}

func NewModelProfileRepo(pool *pgxpool.Pool) *ModelProfileRepo {
	return &ModelProfileRepo{pool: pool}
}

func (r *ModelProfileRepo) Create(ctx context.Context, profile ModelProfile) (ModelProfile, error) {
	if profile.Version == 0 {
		profile.Version = 1
	}
	if !profile.IsCurrent {
		profile.IsCurrent = true
	}
	if profile.InvocationPurpose == "" {
		profile.InvocationPurpose = "agent_turn"
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO model_profile (
			logical_profile_id,
			organization_id,
			version,
			is_current,
			provider_id,
			model_name,
			context_window_tokens,
			max_output_tokens,
			supports_streaming,
			supports_vision,
			temperature,
			invocation_purpose,
			fallback_profile_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, logical_profile_id, organization_id, version, is_current, provider_id, model_name, context_window_tokens, max_output_tokens, supports_streaming, supports_vision, temperature, invocation_purpose, fallback_profile_id, created_at, updated_at
	`, profile.LogicalProfileID, profile.OrganizationID, profile.Version, profile.IsCurrent, profile.ProviderID, profile.ModelName, profile.ContextWindowTokens, profile.MaxOutputTokens, profile.SupportsStreaming, profile.SupportsVision, profile.Temperature, profile.InvocationPurpose, profile.FallbackProfileID)

	created, err := scanModelProfile(row)
	if err != nil {
		return ModelProfile{}, mapDBError(err)
	}
	return created, nil
}

func (r *ModelProfileRepo) GetCurrentByLogicalID(ctx context.Context, organizationID uuid.UUID, logicalProfileID string) (ModelProfile, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, logical_profile_id, organization_id, version, is_current, provider_id, model_name, context_window_tokens, max_output_tokens, supports_streaming, supports_vision, temperature, invocation_purpose, fallback_profile_id, created_at, updated_at
		FROM model_profile
		WHERE logical_profile_id = $2
		  AND is_current = true
		  AND (organization_id = $1 OR organization_id IS NULL)
		ORDER BY CASE WHEN organization_id = $1 THEN 0 ELSE 1 END, version DESC
		LIMIT 1
	`, organizationID, logicalProfileID)

	profile, err := scanModelProfile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelProfile{}, ErrNotFound
	}
	if err != nil {
		return ModelProfile{}, mapDBError(err)
	}
	return profile, nil
}

func (r *ModelProfileRepo) ListCurrent(ctx context.Context, organizationID uuid.UUID) ([]ModelProfile, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT ON (logical_profile_id)
			id, logical_profile_id, organization_id, version, is_current, provider_id, model_name, context_window_tokens, max_output_tokens, supports_streaming, supports_vision, temperature, invocation_purpose, fallback_profile_id, created_at, updated_at
		FROM model_profile
		WHERE is_current = true
		  AND (organization_id = $1 OR organization_id IS NULL)
		ORDER BY logical_profile_id, CASE WHEN organization_id = $1 THEN 0 ELSE 1 END, version DESC
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	profiles := make([]ModelProfile, 0)
	for rows.Next() {
		profile, scanErr := scanModelProfile(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		profiles = append(profiles, profile)
	}

	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return profiles, nil
}

func (r *ModelProfileRepo) ListAll(ctx context.Context, organizationID uuid.UUID) ([]ModelProfile, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, logical_profile_id, organization_id, version, is_current, provider_id, model_name, context_window_tokens, max_output_tokens, supports_streaming, supports_vision, temperature, invocation_purpose, fallback_profile_id, created_at, updated_at
		FROM model_profile
		WHERE organization_id = $1 OR organization_id IS NULL
		ORDER BY logical_profile_id, version DESC
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	profiles := make([]ModelProfile, 0)
	for rows.Next() {
		profile, scanErr := scanModelProfile(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		profiles = append(profiles, profile)
	}

	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return profiles, nil
}

func (r *ModelProfileRepo) Deprecate(ctx context.Context, currentID uuid.UUID, replacement ModelProfile) (ModelProfile, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ModelProfile{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	row := tx.QueryRow(ctx, `
		SELECT id, logical_profile_id, organization_id, version, is_current, provider_id, model_name, context_window_tokens, max_output_tokens, supports_streaming, supports_vision, temperature, invocation_purpose, fallback_profile_id, created_at, updated_at
		FROM model_profile
		WHERE id = $1
		FOR UPDATE
	`, currentID)

	current, err := scanModelProfile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelProfile{}, ErrNotFound
	}
	if err != nil {
		return ModelProfile{}, mapDBError(err)
	}
	if !current.IsCurrent {
		return ModelProfile{}, fmt.Errorf("%w: model profile is not current", ErrConflict)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE model_profile
		SET is_current = false
		WHERE id = $1
	`, currentID); err != nil {
		return ModelProfile{}, mapDBError(err)
	}

	if replacement.InvocationPurpose == "" {
		replacement.InvocationPurpose = current.InvocationPurpose
	}
	replacement.LogicalProfileID = current.LogicalProfileID
	replacement.OrganizationID = current.OrganizationID
	replacement.Version = current.Version + 1
	replacement.IsCurrent = true

	insertRow := tx.QueryRow(ctx, `
		INSERT INTO model_profile (
			logical_profile_id,
			organization_id,
			version,
			is_current,
			provider_id,
			model_name,
			context_window_tokens,
			max_output_tokens,
			supports_streaming,
			supports_vision,
			temperature,
			invocation_purpose,
			fallback_profile_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, logical_profile_id, organization_id, version, is_current, provider_id, model_name, context_window_tokens, max_output_tokens, supports_streaming, supports_vision, temperature, invocation_purpose, fallback_profile_id, created_at, updated_at
	`, replacement.LogicalProfileID, replacement.OrganizationID, replacement.Version, replacement.IsCurrent, replacement.ProviderID, replacement.ModelName, replacement.ContextWindowTokens, replacement.MaxOutputTokens, replacement.SupportsStreaming, replacement.SupportsVision, replacement.Temperature, replacement.InvocationPurpose, replacement.FallbackProfileID)

	created, err := scanModelProfile(insertRow)
	if err != nil {
		return ModelProfile{}, mapDBError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ModelProfile{}, fmt.Errorf("commit tx: %w", err)
	}

	return created, nil
}

func scanModelProfile(row pgx.Row) (ModelProfile, error) {
	var profile ModelProfile
	if err := row.Scan(
		&profile.ID,
		&profile.LogicalProfileID,
		&profile.OrganizationID,
		&profile.Version,
		&profile.IsCurrent,
		&profile.ProviderID,
		&profile.ModelName,
		&profile.ContextWindowTokens,
		&profile.MaxOutputTokens,
		&profile.SupportsStreaming,
		&profile.SupportsVision,
		&profile.Temperature,
		&profile.InvocationPurpose,
		&profile.FallbackProfileID,
		&profile.CreatedAt,
		&profile.UpdatedAt,
	); err != nil {
		return ModelProfile{}, err
	}

	return profile, nil
}

type ModelProfileAssignment struct {
	ID                uuid.UUID
	OrganizationID    uuid.UUID
	ScopeType         string
	ScopeID           uuid.UUID
	LogicalProfileID  string
	InvocationPurpose string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type ModelProfileAssignmentRepo struct {
	pool *pgxpool.Pool
}

func NewModelProfileAssignmentRepo(pool *pgxpool.Pool) *ModelProfileAssignmentRepo {
	return &ModelProfileAssignmentRepo{pool: pool}
}

func (r *ModelProfileAssignmentRepo) Upsert(ctx context.Context, assignment ModelProfileAssignment) (ModelProfileAssignment, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO model_profile_assignment (
			organization_id,
			scope_type,
			scope_id,
			logical_profile_id,
			invocation_purpose
		)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (organization_id, scope_type, scope_id, invocation_purpose)
		DO UPDATE SET
			logical_profile_id = EXCLUDED.logical_profile_id,
			updated_at = now()
		RETURNING id, organization_id, scope_type, scope_id, logical_profile_id, invocation_purpose, created_at, updated_at
	`, assignment.OrganizationID, assignment.ScopeType, assignment.ScopeID, assignment.LogicalProfileID, assignment.InvocationPurpose)

	upserted, err := scanModelProfileAssignment(row)
	if err != nil {
		return ModelProfileAssignment{}, mapDBError(err)
	}
	return upserted, nil
}

func (r *ModelProfileAssignmentRepo) GetByScope(ctx context.Context, organizationID uuid.UUID, scopeType string, scopeID uuid.UUID, purpose string) (ModelProfileAssignment, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, scope_type, scope_id, logical_profile_id, invocation_purpose, created_at, updated_at
		FROM model_profile_assignment
		WHERE organization_id = $1
		  AND scope_type = $2
		  AND scope_id = $3
		  AND invocation_purpose = $4
	`, organizationID, scopeType, scopeID, purpose)

	assignment, err := scanModelProfileAssignment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelProfileAssignment{}, ErrNotFound
	}
	if err != nil {
		return ModelProfileAssignment{}, mapDBError(err)
	}
	return assignment, nil
}

func (r *ModelProfileAssignmentRepo) ListByOrg(ctx context.Context, organizationID uuid.UUID) ([]ModelProfileAssignment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, scope_type, scope_id, logical_profile_id, invocation_purpose, created_at, updated_at
		FROM model_profile_assignment
		WHERE organization_id = $1
		ORDER BY scope_type, scope_id, invocation_purpose
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	assignments := make([]ModelProfileAssignment, 0)
	for rows.Next() {
		assignment, scanErr := scanModelProfileAssignment(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		assignments = append(assignments, assignment)
	}

	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return assignments, nil
}

func (r *ModelProfileAssignmentRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM model_profile_assignment WHERE id = $1`, id)
	if err != nil {
		return mapDBError(err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanModelProfileAssignment(row pgx.Row) (ModelProfileAssignment, error) {
	var assignment ModelProfileAssignment
	if err := row.Scan(
		&assignment.ID,
		&assignment.OrganizationID,
		&assignment.ScopeType,
		&assignment.ScopeID,
		&assignment.LogicalProfileID,
		&assignment.InvocationPurpose,
		&assignment.CreatedAt,
		&assignment.UpdatedAt,
	); err != nil {
		return ModelProfileAssignment{}, err
	}

	return assignment, nil
}

func normalizeRawJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid json")
	}
	return raw, nil
}
