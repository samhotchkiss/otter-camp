package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type AdminAgentsHandler struct {
	DB    *sql.DB
	Store *store.AgentStore
}

type adminAgentSummary struct {
	ID               string `json:"id"`
	WorkspaceAgentID string `json:"workspace_agent_id"`
	Name             string `json:"name"`
	Status           string `json:"status"`
	Model            string `json:"model,omitempty"`
	HeartbeatEvery   string `json:"heartbeat_every,omitempty"`
	Channel          string `json:"channel,omitempty"`
	SessionKey       string `json:"session_key,omitempty"`
	LastSeen         string `json:"last_seen,omitempty"`
}

type adminAgentSyncDetails struct {
	CurrentTask   string     `json:"current_task,omitempty"`
	ContextTokens int        `json:"context_tokens,omitempty"`
	TotalTokens   int        `json:"total_tokens,omitempty"`
	LastSeen      string     `json:"last_seen,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

type adminAgentsListResponse struct {
	Agents []adminAgentSummary `json:"agents"`
	Total  int                 `json:"total"`
}

type adminAgentDetailResponse struct {
	Agent adminAgentSummary      `json:"agent"`
	Sync  *adminAgentSyncDetails `json:"sync,omitempty"`
}

type adminAgentRow struct {
	WorkspaceAgentID string
	Slug             string
	DisplayName      string
	WorkspaceStatus  string
	HeartbeatEvery   sql.NullString
	SyncName         sql.NullString
	SyncModel        sql.NullString
	SyncChannel      sql.NullString
	SyncSessionKey   sql.NullString
	SyncLastSeen     sql.NullString
	SyncCurrentTask  sql.NullString
	SyncStatus       sql.NullString
	SyncUpdatedAt    sql.NullTime
	ContextTokens    sql.NullInt64
	TotalTokens      sql.NullInt64
}

var errAdminAgentForbidden = errors.New("agent belongs to a different workspace")

func (h *AdminAgentsHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}
	if h.DB == nil || h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	rows, err := h.listRows(r.Context(), workspaceID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load admin agents"})
		return
	}

	items := make([]adminAgentSummary, 0, len(rows))
	for _, row := range rows {
		items = append(items, rowToAgentSummary(row))
	}

	sendJSON(w, http.StatusOK, adminAgentsListResponse{
		Agents: items,
		Total:  len(items),
	})
}

func (h *AdminAgentsHandler) Get(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}
	if h.DB == nil || h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	identifier := strings.TrimSpace(chi.URLParam(r, "id"))
	if identifier == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "agent id is required"})
		return
	}

	row, err := h.getRow(r.Context(), workspaceID, identifier)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "agent not found"})
		case errors.Is(err, errAdminAgentForbidden):
			sendJSON(w, http.StatusForbidden, errorResponse{Error: "agent belongs to a different workspace"})
		default:
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load admin agent"})
		}
		return
	}

	payload := adminAgentDetailResponse{
		Agent: rowToAgentSummary(*row),
	}
	if row.SyncUpdatedAt.Valid || strings.TrimSpace(row.SyncCurrentTask.String) != "" || row.ContextTokens.Valid || row.TotalTokens.Valid {
		var updatedAt *time.Time
		if row.SyncUpdatedAt.Valid {
			ts := row.SyncUpdatedAt.Time.UTC()
			updatedAt = &ts
		}
		payload.Sync = &adminAgentSyncDetails{
			CurrentTask:   strings.TrimSpace(row.SyncCurrentTask.String),
			ContextTokens: int(row.ContextTokens.Int64),
			TotalTokens:   int(row.TotalTokens.Int64),
			LastSeen:      strings.TrimSpace(row.SyncLastSeen.String),
			UpdatedAt:     updatedAt,
		}
	}

	sendJSON(w, http.StatusOK, payload)
}

func (h *AdminAgentsHandler) listRows(ctx context.Context, workspaceID string) ([]adminAgentRow, error) {
	query := `
		SELECT
			a.id::text AS workspace_agent_id,
			a.slug,
			COALESCE(a.display_name, '') AS display_name,
			COALESCE(a.status, '') AS workspace_status,
			c.heartbeat_every,
			s.name,
			s.model,
			s.channel,
			s.session_key,
			s.last_seen,
			s.current_task,
			s.status,
			s.updated_at,
			s.context_tokens,
			s.total_tokens
		FROM agents a
		LEFT JOIN openclaw_agent_configs c ON c.id = a.slug
		LEFT JOIN agent_sync_state s ON s.id = a.slug
		WHERE a.org_id = $1
		ORDER BY LOWER(COALESCE(NULLIF(s.name, ''), NULLIF(a.display_name, ''), a.slug)) ASC, a.slug ASC`
	rows, err := h.DB.QueryContext(ctx, query, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]adminAgentRow, 0, 16)
	for rows.Next() {
		row, err := scanAdminAgentRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (h *AdminAgentsHandler) getRow(ctx context.Context, workspaceID, identifier string) (*adminAgentRow, error) {
	query := `
		SELECT
			a.id::text AS workspace_agent_id,
			a.slug,
			COALESCE(a.display_name, '') AS display_name,
			COALESCE(a.status, '') AS workspace_status,
			c.heartbeat_every,
			s.name,
			s.model,
			s.channel,
			s.session_key,
			s.last_seen,
			s.current_task,
			s.status,
			s.updated_at,
			s.context_tokens,
			s.total_tokens
		FROM agents a
		LEFT JOIN openclaw_agent_configs c ON c.id = a.slug
		LEFT JOIN agent_sync_state s ON s.id = a.slug
		WHERE a.org_id = $1
		  AND (a.id::text = $2 OR a.slug = $2)
		LIMIT 1`
	row, err := scanAdminAgentRow(h.DB.QueryRowContext(ctx, query, workspaceID, identifier))
	if err == nil {
		return &row, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	var exists bool
	if err := h.DB.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM agents WHERE id::text = $1 OR slug = $1)`,
		identifier,
	).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, errAdminAgentForbidden
	}
	return nil, store.ErrNotFound
}

func rowToAgentSummary(row adminAgentRow) adminAgentSummary {
	status := normalizeWorkspaceAgentStatus(row.WorkspaceStatus)
	if row.SyncUpdatedAt.Valid {
		status = deriveAgentStatus(row.SyncUpdatedAt.Time.UTC(), int(row.ContextTokens.Int64))
	}

	name := strings.TrimSpace(row.SyncName.String)
	if name == "" {
		name = strings.TrimSpace(row.DisplayName)
	}
	if name == "" {
		name = strings.TrimSpace(row.Slug)
	}

	return adminAgentSummary{
		ID:               strings.TrimSpace(row.Slug),
		WorkspaceAgentID: strings.TrimSpace(row.WorkspaceAgentID),
		Name:             name,
		Status:           status,
		Model:            strings.TrimSpace(row.SyncModel.String),
		HeartbeatEvery:   strings.TrimSpace(row.HeartbeatEvery.String),
		Channel:          strings.TrimSpace(row.SyncChannel.String),
		SessionKey:       strings.TrimSpace(row.SyncSessionKey.String),
		LastSeen:         strings.TrimSpace(row.SyncLastSeen.String),
	}
}

func normalizeWorkspaceAgentStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "online", "active":
		return "online"
	case "busy", "working":
		return "busy"
	default:
		return "offline"
	}
}

func scanAdminAgentRow(scanner interface{ Scan(...any) error }) (adminAgentRow, error) {
	var row adminAgentRow
	err := scanner.Scan(
		&row.WorkspaceAgentID,
		&row.Slug,
		&row.DisplayName,
		&row.WorkspaceStatus,
		&row.HeartbeatEvery,
		&row.SyncName,
		&row.SyncModel,
		&row.SyncChannel,
		&row.SyncSessionKey,
		&row.SyncLastSeen,
		&row.SyncCurrentTask,
		&row.SyncStatus,
		&row.SyncUpdatedAt,
		&row.ContextTokens,
		&row.TotalTokens,
	)
	return row, err
}
