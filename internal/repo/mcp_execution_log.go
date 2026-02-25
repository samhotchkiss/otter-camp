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

type MCPExecutionLog struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	MCPConnectionID  uuid.UUID
	MCPToolCatalogID *uuid.UUID
	ToolExecutionID  *uuid.UUID
	RunID            *uuid.UUID
	AgentID          *uuid.UUID
	Method           string
	ToolName         *string
	ResourceURI      *string
	RequestPayload   json.RawMessage
	ResponsePayload  json.RawMessage
	Status           string
	ErrorMessage     *string
	LatencyMS        *int
	CreatedAt        time.Time
}

type MCPExecutionLogRepo struct {
	pool *pgxpool.Pool
}

func NewMCPExecutionLogRepo(pool *pgxpool.Pool) *MCPExecutionLogRepo {
	return &MCPExecutionLogRepo{pool: pool}
}

func (r *MCPExecutionLogRepo) Create(ctx context.Context, entry MCPExecutionLog) (MCPExecutionLog, error) {
	status := normalizeMCPExecutionLogStatus(entry.Status)
	if status == "" {
		return MCPExecutionLog{}, fmt.Errorf("invalid status")
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO mcp_execution_log (
			organization_id,
			mcp_connection_id,
			mcp_tool_catalog_id,
			tool_execution_id,
			run_id,
			agent_id,
			method,
			tool_name,
			resource_uri,
			request_payload,
			response_payload,
			status,
			error_message,
			latency_ms
		)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9,
			$10::jsonb, NULLIF($11::jsonb, 'null'::jsonb),
			$12, $13, $14
		)
		RETURNING id, organization_id, mcp_connection_id, mcp_tool_catalog_id, tool_execution_id, run_id, agent_id,
			      method, tool_name, resource_uri, request_payload, response_payload, status, error_message,
			      latency_ms, created_at
	`,
		entry.OrganizationID,
		entry.MCPConnectionID,
		entry.MCPToolCatalogID,
		entry.ToolExecutionID,
		entry.RunID,
		entry.AgentID,
		strings.TrimSpace(entry.Method),
		normalizeStringPointer(entry.ToolName),
		normalizeStringPointer(entry.ResourceURI),
		normalizeRawJSON(entry.RequestPayload),
		nullableJSON(entry.ResponsePayload),
		status,
		normalizeStringPointer(entry.ErrorMessage),
		entry.LatencyMS,
	)

	created, err := scanMCPExecutionLog(row)
	if err != nil {
		return MCPExecutionLog{}, mapDBError(err)
	}
	return created, nil
}

func (r *MCPExecutionLogRepo) Complete(ctx context.Context, id uuid.UUID, status string, responsePayload json.RawMessage, errorMessage *string, latencyMS *int) (MCPExecutionLog, error) {
	status = normalizeMCPExecutionLogStatus(status)
	if status == "" {
		return MCPExecutionLog{}, fmt.Errorf("invalid status")
	}

	row := r.pool.QueryRow(ctx, `
		UPDATE mcp_execution_log
		SET status = $2,
		    response_payload = NULLIF($3::jsonb, 'null'::jsonb),
		    error_message = $4,
		    latency_ms = $5
		WHERE id = $1
		RETURNING id, organization_id, mcp_connection_id, mcp_tool_catalog_id, tool_execution_id, run_id, agent_id,
			      method, tool_name, resource_uri, request_payload, response_payload, status, error_message,
			      latency_ms, created_at
	`, id, status, nullableJSON(responsePayload), normalizeStringPointer(errorMessage), latencyMS)

	updated, err := scanMCPExecutionLog(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPExecutionLog{}, ErrNotFound
	}
	if err != nil {
		return MCPExecutionLog{}, mapDBError(err)
	}
	return updated, nil
}

func (r *MCPExecutionLogRepo) Get(ctx context.Context, id uuid.UUID) (MCPExecutionLog, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, mcp_connection_id, mcp_tool_catalog_id, tool_execution_id, run_id, agent_id,
		       method, tool_name, resource_uri, request_payload, response_payload, status, error_message,
		       latency_ms, created_at
		FROM mcp_execution_log
		WHERE id = $1
	`, id)

	entry, err := scanMCPExecutionLog(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPExecutionLog{}, ErrNotFound
	}
	if err != nil {
		return MCPExecutionLog{}, mapDBError(err)
	}
	return entry, nil
}

func (r *MCPExecutionLogRepo) ListByConnection(ctx context.Context, connectionID uuid.UUID) ([]MCPExecutionLog, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, mcp_connection_id, mcp_tool_catalog_id, tool_execution_id, run_id, agent_id,
		       method, tool_name, resource_uri, request_payload, response_payload, status, error_message,
		       latency_ms, created_at
		FROM mcp_execution_log
		WHERE mcp_connection_id = $1
		ORDER BY created_at DESC, id DESC
	`, connectionID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	entries := make([]MCPExecutionLog, 0)
	for rows.Next() {
		entry, scanErr := scanMCPExecutionLog(rows)
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

func (r *MCPExecutionLogRepo) ListByRun(ctx context.Context, runID uuid.UUID) ([]MCPExecutionLog, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, mcp_connection_id, mcp_tool_catalog_id, tool_execution_id, run_id, agent_id,
		       method, tool_name, resource_uri, request_payload, response_payload, status, error_message,
		       latency_ms, created_at
		FROM mcp_execution_log
		WHERE run_id = $1
		ORDER BY created_at DESC, id DESC
	`, runID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	entries := make([]MCPExecutionLog, 0)
	for rows.Next() {
		entry, scanErr := scanMCPExecutionLog(rows)
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

func scanMCPExecutionLog(row pgx.Row) (MCPExecutionLog, error) {
	var entry MCPExecutionLog
	if err := row.Scan(
		&entry.ID,
		&entry.OrganizationID,
		&entry.MCPConnectionID,
		&entry.MCPToolCatalogID,
		&entry.ToolExecutionID,
		&entry.RunID,
		&entry.AgentID,
		&entry.Method,
		&entry.ToolName,
		&entry.ResourceURI,
		&entry.RequestPayload,
		&entry.ResponsePayload,
		&entry.Status,
		&entry.ErrorMessage,
		&entry.LatencyMS,
		&entry.CreatedAt,
	); err != nil {
		return MCPExecutionLog{}, err
	}

	entry.RequestPayload = normalizeRawJSON(entry.RequestPayload)
	entry.ResponsePayload = normalizeNullableRawJSON(entry.ResponsePayload)
	return entry, nil
}

func normalizeMCPExecutionLogStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "pending", "success", "error", "timeout", "circuit_open":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return ""
	}
}

func normalizeRawJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 || !json.Valid(value) {
		return json.RawMessage(`{}`)
	}
	return value
}

func normalizeNullableRawJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 || !json.Valid(value) {
		return nil
	}
	return value
}

func normalizeStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func nullableJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 || !json.Valid(value) {
		return nil
	}
	return value
}
