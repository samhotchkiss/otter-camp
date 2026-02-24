package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MCPConnection struct {
	ID              uuid.UUID
	OrganizationID  uuid.UUID
	ProjectID       *uuid.UUID
	DisplayName     string
	Slug            string
	Transport       string
	TransportConfig json.RawMessage
	Status          string
	IsEnabled       bool
	LastHealthyAt   *time.Time
	CreatedByType   string
	CreatedByID     uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type MCPToolCatalogEntry struct {
	ID           uuid.UUID
	ConnectionID uuid.UUID
	ToolName     string
	Description  string
	InputSchema  json.RawMessage
	IsEnabled    bool
	Metadata     json.RawMessage
	DiscoveredAt time.Time
	UpdatedAt    time.Time
}

type MCPSecretBinding struct {
	ID           uuid.UUID
	ConnectionID uuid.UUID
	SecretRef    string
	EnvVarName   string
	CreatedAt    time.Time
}

type MCPToolCatalogDiff struct {
	AddedCount   int
	UpdatedCount int
	RemovedCount int
}

type MCPConnectionRepo struct {
	pool *pgxpool.Pool
}

func NewMCPConnectionRepo(pool *pgxpool.Pool) *MCPConnectionRepo {
	return &MCPConnectionRepo{pool: pool}
}

func (r *MCPConnectionRepo) Create(ctx context.Context, connection MCPConnection) (MCPConnection, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO mcp_connection (
			organization_id,
			project_id,
			display_name,
			slug,
			transport,
			transport_config,
			status,
			is_enabled,
			last_healthy_at,
			created_by_type,
			created_by_id
		)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11)
		RETURNING id, organization_id, project_id, display_name, slug, transport, transport_config, status, is_enabled, last_healthy_at, created_by_type, created_by_id, created_at, updated_at
	`,
		connection.OrganizationID,
		connection.ProjectID,
		connection.DisplayName,
		connection.Slug,
		connection.Transport,
		normalizeMCPJSON(connection.TransportConfig, json.RawMessage(`{}`)),
		defaultMCPConnectionStatus(connection.Status),
		defaultMCPConnectionEnabled(connection.IsEnabled),
		connection.LastHealthyAt,
		connection.CreatedByType,
		connection.CreatedByID,
	)

	created, err := scanMCPConnection(row)
	if err != nil {
		return MCPConnection{}, mapDBError(err)
	}
	return created, nil
}

func (r *MCPConnectionRepo) GetByID(ctx context.Context, id uuid.UUID) (MCPConnection, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, project_id, display_name, slug, transport, transport_config, status, is_enabled, last_healthy_at, created_by_type, created_by_id, created_at, updated_at
		FROM mcp_connection
		WHERE id = $1
	`, id)

	connection, err := scanMCPConnection(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPConnection{}, ErrNotFound
	}
	if err != nil {
		return MCPConnection{}, mapDBError(err)
	}
	return connection, nil
}

func (r *MCPConnectionRepo) GetBySlug(ctx context.Context, organizationID uuid.UUID, slug string) (MCPConnection, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, project_id, display_name, slug, transport, transport_config, status, is_enabled, last_healthy_at, created_by_type, created_by_id, created_at, updated_at
		FROM mcp_connection
		WHERE organization_id = $1
		  AND slug = $2
	`, organizationID, strings.TrimSpace(slug))

	connection, err := scanMCPConnection(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPConnection{}, ErrNotFound
	}
	if err != nil {
		return MCPConnection{}, mapDBError(err)
	}
	return connection, nil
}

func (r *MCPConnectionRepo) List(ctx context.Context, organizationID uuid.UUID) ([]MCPConnection, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, project_id, display_name, slug, transport, transport_config, status, is_enabled, last_healthy_at, created_by_type, created_by_id, created_at, updated_at
		FROM mcp_connection
		WHERE organization_id = $1
		ORDER BY created_at ASC, id ASC
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	connections := make([]MCPConnection, 0)
	for rows.Next() {
		connection, scanErr := scanMCPConnection(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		connections = append(connections, connection)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return connections, nil
}

func (r *MCPConnectionRepo) SetStatus(ctx context.Context, id uuid.UUID, status string) (MCPConnection, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE mcp_connection
		SET status = $2
		WHERE id = $1
		RETURNING id, organization_id, project_id, display_name, slug, transport, transport_config, status, is_enabled, last_healthy_at, created_by_type, created_by_id, created_at, updated_at
	`, id, strings.TrimSpace(status))

	connection, err := scanMCPConnection(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPConnection{}, ErrNotFound
	}
	if err != nil {
		return MCPConnection{}, mapDBError(err)
	}
	return connection, nil
}

func (r *MCPConnectionRepo) Enable(ctx context.Context, id uuid.UUID) (MCPConnection, error) {
	return r.setEnabled(ctx, id, true)
}

func (r *MCPConnectionRepo) Disable(ctx context.Context, id uuid.UUID) (MCPConnection, error) {
	return r.setEnabled(ctx, id, false)
}

func (r *MCPConnectionRepo) setEnabled(ctx context.Context, id uuid.UUID, enabled bool) (MCPConnection, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE mcp_connection
		SET is_enabled = $2
		WHERE id = $1
		RETURNING id, organization_id, project_id, display_name, slug, transport, transport_config, status, is_enabled, last_healthy_at, created_by_type, created_by_id, created_at, updated_at
	`, id, enabled)

	connection, err := scanMCPConnection(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPConnection{}, ErrNotFound
	}
	if err != nil {
		return MCPConnection{}, mapDBError(err)
	}
	return connection, nil
}

func (r *MCPConnectionRepo) UpdateLastHealthy(ctx context.Context, id uuid.UUID, lastHealthyAt time.Time) (MCPConnection, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE mcp_connection
		SET last_healthy_at = $2
		WHERE id = $1
		RETURNING id, organization_id, project_id, display_name, slug, transport, transport_config, status, is_enabled, last_healthy_at, created_by_type, created_by_id, created_at, updated_at
	`, id, lastHealthyAt.UTC())

	connection, err := scanMCPConnection(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPConnection{}, ErrNotFound
	}
	if err != nil {
		return MCPConnection{}, mapDBError(err)
	}
	return connection, nil
}

func (r *MCPConnectionRepo) Update(ctx context.Context, connection MCPConnection) (MCPConnection, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE mcp_connection
		SET
			project_id = $2,
			display_name = $3,
			slug = $4,
			transport = $5,
			transport_config = $6::jsonb,
			status = $7,
			is_enabled = $8
		WHERE id = $1
		RETURNING id, organization_id, project_id, display_name, slug, transport, transport_config, status, is_enabled, last_healthy_at, created_by_type, created_by_id, created_at, updated_at
	`,
		connection.ID,
		connection.ProjectID,
		connection.DisplayName,
		connection.Slug,
		connection.Transport,
		normalizeMCPJSON(connection.TransportConfig, json.RawMessage(`{}`)),
		defaultMCPConnectionStatus(connection.Status),
		connection.IsEnabled,
	)

	updated, err := scanMCPConnection(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPConnection{}, ErrNotFound
	}
	if err != nil {
		return MCPConnection{}, mapDBError(err)
	}
	return updated, nil
}

func (r *MCPConnectionRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `
		DELETE FROM mcp_connection
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

type MCPToolCatalogRepo struct {
	pool *pgxpool.Pool
}

func NewMCPToolCatalogRepo(pool *pgxpool.Pool) *MCPToolCatalogRepo {
	return &MCPToolCatalogRepo{pool: pool}
}

func (r *MCPToolCatalogRepo) BulkUpsert(ctx context.Context, connectionID uuid.UUID, manifest []MCPToolCatalogEntry) (MCPToolCatalogDiff, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return MCPToolCatalogDiff{}, fmt.Errorf("begin mcp tool catalog transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	existingRows, err := tx.Query(ctx, `
		SELECT id, connection_id, tool_name, description, input_schema, is_enabled, metadata, discovered_at, updated_at
		FROM mcp_tool_catalog
		WHERE connection_id = $1
	`, connectionID)
	if err != nil {
		return MCPToolCatalogDiff{}, mapDBError(err)
	}
	defer existingRows.Close()

	existingByName := make(map[string]MCPToolCatalogEntry)
	for existingRows.Next() {
		entry, scanErr := scanMCPToolCatalogEntry(existingRows)
		if scanErr != nil {
			return MCPToolCatalogDiff{}, mapDBError(scanErr)
		}
		existingByName[entry.ToolName] = entry
	}
	if existingRows.Err() != nil {
		return MCPToolCatalogDiff{}, mapDBError(existingRows.Err())
	}

	added, updated, removed, err := planCatalogDiff(existingByName, manifest)
	if err != nil {
		return MCPToolCatalogDiff{}, err
	}

	for _, entry := range updated {
		_, updateErr := tx.Exec(ctx, `
			UPDATE mcp_tool_catalog
			SET description = $2,
				input_schema = $3::jsonb,
				metadata = $4::jsonb
			WHERE id = $1
		`,
			entry.ID,
			entry.Description,
			normalizeMCPJSON(entry.InputSchema, json.RawMessage(`{}`)),
			normalizeMCPJSON(entry.Metadata, json.RawMessage(`{}`)),
		)
		if updateErr != nil {
			return MCPToolCatalogDiff{}, mapDBError(updateErr)
		}
	}

	for _, entry := range added {
		_, insertErr := tx.Exec(ctx, `
			INSERT INTO mcp_tool_catalog (
				connection_id,
				tool_name,
				description,
				input_schema,
				is_enabled,
				metadata
			)
			VALUES ($1, $2, $3, $4::jsonb, false, $5::jsonb)
		`,
			connectionID,
			entry.ToolName,
			entry.Description,
			normalizeMCPJSON(entry.InputSchema, json.RawMessage(`{}`)),
			normalizeMCPJSON(entry.Metadata, json.RawMessage(`{}`)),
		)
		if insertErr != nil {
			return MCPToolCatalogDiff{}, mapDBError(insertErr)
		}
	}

	for _, existing := range removed {
		if _, disableErr := tx.Exec(ctx, `
			UPDATE mcp_tool_catalog
			SET is_enabled = false
			WHERE id = $1
		`, existing.ID); disableErr != nil {
			return MCPToolCatalogDiff{}, mapDBError(disableErr)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return MCPToolCatalogDiff{}, fmt.Errorf("commit mcp tool catalog transaction: %w", err)
	}

	return MCPToolCatalogDiff{
		AddedCount:   len(added),
		UpdatedCount: len(updated),
		RemovedCount: len(removed),
	}, nil
}

func (r *MCPToolCatalogRepo) GetByID(ctx context.Context, id uuid.UUID) (MCPToolCatalogEntry, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, connection_id, tool_name, description, input_schema, is_enabled, metadata, discovered_at, updated_at
		FROM mcp_tool_catalog
		WHERE id = $1
	`, id)

	entry, err := scanMCPToolCatalogEntry(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPToolCatalogEntry{}, ErrNotFound
	}
	if err != nil {
		return MCPToolCatalogEntry{}, mapDBError(err)
	}
	return entry, nil
}

func (r *MCPToolCatalogRepo) ListByConnection(ctx context.Context, connectionID uuid.UUID) ([]MCPToolCatalogEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, connection_id, tool_name, description, input_schema, is_enabled, metadata, discovered_at, updated_at
		FROM mcp_tool_catalog
		WHERE connection_id = $1
		ORDER BY tool_name ASC, id ASC
	`, connectionID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	entries := make([]MCPToolCatalogEntry, 0)
	for rows.Next() {
		entry, scanErr := scanMCPToolCatalogEntry(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		entries = append(entries, entry)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return entries, nil
}

func (r *MCPToolCatalogRepo) Enable(ctx context.Context, id uuid.UUID) (MCPToolCatalogEntry, error) {
	return r.setEntryEnabled(ctx, id, true)
}

func (r *MCPToolCatalogRepo) Disable(ctx context.Context, id uuid.UUID) (MCPToolCatalogEntry, error) {
	return r.setEntryEnabled(ctx, id, false)
}

func (r *MCPToolCatalogRepo) setEntryEnabled(ctx context.Context, id uuid.UUID, enabled bool) (MCPToolCatalogEntry, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE mcp_tool_catalog
		SET is_enabled = $2
		WHERE id = $1
		RETURNING id, connection_id, tool_name, description, input_schema, is_enabled, metadata, discovered_at, updated_at
	`, id, enabled)

	entry, err := scanMCPToolCatalogEntry(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPToolCatalogEntry{}, ErrNotFound
	}
	if err != nil {
		return MCPToolCatalogEntry{}, mapDBError(err)
	}
	return entry, nil
}

func (r *MCPToolCatalogRepo) GetEnabled(ctx context.Context, connectionID uuid.UUID) ([]MCPToolCatalogEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, connection_id, tool_name, description, input_schema, is_enabled, metadata, discovered_at, updated_at
		FROM mcp_tool_catalog
		WHERE connection_id = $1
		  AND is_enabled = true
		ORDER BY tool_name ASC, id ASC
	`, connectionID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	entries := make([]MCPToolCatalogEntry, 0)
	for rows.Next() {
		entry, scanErr := scanMCPToolCatalogEntry(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		entries = append(entries, entry)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return entries, nil
}

type MCPSecretBindingRepo struct {
	pool *pgxpool.Pool
}

func NewMCPSecretBindingRepo(pool *pgxpool.Pool) *MCPSecretBindingRepo {
	return &MCPSecretBindingRepo{pool: pool}
}

func (r *MCPSecretBindingRepo) Create(ctx context.Context, binding MCPSecretBinding) (MCPSecretBinding, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO mcp_secret_binding (
			connection_id,
			secret_ref,
			env_var_name
		)
		VALUES ($1, $2, $3)
		RETURNING id, connection_id, secret_ref, env_var_name, created_at
	`, binding.ConnectionID, binding.SecretRef, binding.EnvVarName)

	created, err := scanMCPSecretBinding(row)
	if err != nil {
		return MCPSecretBinding{}, mapDBError(err)
	}
	return created, nil
}

func (r *MCPSecretBindingRepo) GetByConnection(ctx context.Context, connectionID uuid.UUID) ([]MCPSecretBinding, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, connection_id, secret_ref, env_var_name, created_at
		FROM mcp_secret_binding
		WHERE connection_id = $1
		ORDER BY env_var_name ASC, id ASC
	`, connectionID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	bindings := make([]MCPSecretBinding, 0)
	for rows.Next() {
		binding, scanErr := scanMCPSecretBinding(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		bindings = append(bindings, binding)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return bindings, nil
}

func (r *MCPSecretBindingRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `
		DELETE FROM mcp_secret_binding
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

func normalizeMCPJSON(raw, fallback json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return fallback
	}
	return raw
}

func defaultMCPConnectionStatus(status string) string {
	trimmed := strings.TrimSpace(status)
	if trimmed == "" {
		return "configuring"
	}
	return trimmed
}

func defaultMCPConnectionEnabled(enabled bool) bool {
	return enabled
}

func scanMCPConnection(row pgx.Row) (MCPConnection, error) {
	var connection MCPConnection
	if err := row.Scan(
		&connection.ID,
		&connection.OrganizationID,
		&connection.ProjectID,
		&connection.DisplayName,
		&connection.Slug,
		&connection.Transport,
		&connection.TransportConfig,
		&connection.Status,
		&connection.IsEnabled,
		&connection.LastHealthyAt,
		&connection.CreatedByType,
		&connection.CreatedByID,
		&connection.CreatedAt,
		&connection.UpdatedAt,
	); err != nil {
		return MCPConnection{}, err
	}
	if len(connection.TransportConfig) == 0 {
		connection.TransportConfig = json.RawMessage(`{}`)
	}
	return connection, nil
}

func scanMCPToolCatalogEntry(row pgx.Row) (MCPToolCatalogEntry, error) {
	var entry MCPToolCatalogEntry
	if err := row.Scan(
		&entry.ID,
		&entry.ConnectionID,
		&entry.ToolName,
		&entry.Description,
		&entry.InputSchema,
		&entry.IsEnabled,
		&entry.Metadata,
		&entry.DiscoveredAt,
		&entry.UpdatedAt,
	); err != nil {
		return MCPToolCatalogEntry{}, err
	}
	if len(entry.InputSchema) == 0 {
		entry.InputSchema = json.RawMessage(`{}`)
	}
	if len(entry.Metadata) == 0 {
		entry.Metadata = json.RawMessage(`{}`)
	}
	return entry, nil
}

func scanMCPSecretBinding(row pgx.Row) (MCPSecretBinding, error) {
	var binding MCPSecretBinding
	if err := row.Scan(
		&binding.ID,
		&binding.ConnectionID,
		&binding.SecretRef,
		&binding.EnvVarName,
		&binding.CreatedAt,
	); err != nil {
		return MCPSecretBinding{}, err
	}
	return binding, nil
}

func planCatalogDiff(existingByName map[string]MCPToolCatalogEntry, manifest []MCPToolCatalogEntry) (added []MCPToolCatalogEntry, updated []MCPToolCatalogEntry, removed []MCPToolCatalogEntry, err error) {
	seen := make(map[string]struct{}, len(manifest))

	for _, entry := range manifest {
		toolName := strings.TrimSpace(entry.ToolName)
		if toolName == "" {
			return nil, nil, nil, fmt.Errorf("tool_name is required")
		}
		seen[toolName] = struct{}{}
		entry.ToolName = toolName

		if existing, ok := existingByName[toolName]; ok {
			entry.ID = existing.ID
			entry.IsEnabled = existing.IsEnabled
			updated = append(updated, entry)
			continue
		}

		added = append(added, entry)
	}

	for toolName, existing := range existingByName {
		if _, ok := seen[toolName]; ok {
			continue
		}
		removed = append(removed, existing)
	}

	return added, updated, removed, nil
}

func sortMCPToolNames(entries []MCPToolCatalogEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.ToolName)
	}
	sort.Strings(names)
	return names
}
