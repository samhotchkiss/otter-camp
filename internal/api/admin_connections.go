package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

type openClawConnectionStatus interface {
	IsConnected() bool
	SendToOpenClaw(event interface{}) error
}

type AdminConnectionsHandler struct {
	DB              *sql.DB
	OpenClawHandler openClawConnectionStatus
	EventStore      *store.ConnectionEventStore
}

type adminConnectionsSession struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Status        string    `json:"status"`
	Model         string    `json:"model,omitempty"`
	ContextTokens int       `json:"context_tokens,omitempty"`
	TotalTokens   int       `json:"total_tokens,omitempty"`
	Channel       string    `json:"channel,omitempty"`
	SessionKey    string    `json:"session_key,omitempty"`
	LastSeen      string    `json:"last_seen,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
	Stalled       bool      `json:"stalled"`
}

type adminConnectionsSessionSummary struct {
	Total   int `json:"total"`
	Online  int `json:"online"`
	Busy    int `json:"busy"`
	Offline int `json:"offline"`
	Stalled int `json:"stalled"`
}

type adminConnectionsBridgeStatus struct {
	Connected   bool                       `json:"connected"`
	LastSync    *time.Time                 `json:"last_sync,omitempty"`
	SyncHealthy bool                       `json:"sync_healthy"`
	Diagnostics *OpenClawBridgeDiagnostics `json:"diagnostics,omitempty"`
}

type adminConnectionsResponse struct {
	Bridge      adminConnectionsBridgeStatus   `json:"bridge"`
	Host        *OpenClawHostDiagnostics       `json:"host,omitempty"`
	Sessions    []adminConnectionsSession      `json:"sessions"`
	Summary     adminConnectionsSessionSummary `json:"summary"`
	GeneratedAt time.Time                      `json:"generated_at"`
}

type adminConnectionEventsResponse struct {
	Events []store.ConnectionEvent `json:"events"`
	Total  int                     `json:"total"`
}

type adminDiagnosticCheck struct {
	Key     string `json:"key"`
	Status  string `json:"status"` // pass|warn|fail
	Message string `json:"message"`
}

type adminDiagnosticsResponse struct {
	Checks      []adminDiagnosticCheck `json:"checks"`
	GeneratedAt time.Time              `json:"generated_at"`
}

type adminLogsResponse struct {
	Items []adminLogItem `json:"items"`
	Total int            `json:"total"`
}

type adminLogItem struct {
	ID        string          `json:"id"`
	Timestamp time.Time       `json:"timestamp"`
	Level     string          `json:"level"`
	EventType string          `json:"event_type"`
	Message   string          `json:"message"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

type openClawAdminCommandEvent struct {
	Type      string                   `json:"type"`
	Timestamp time.Time                `json:"timestamp"`
	OrgID     string                   `json:"org_id"`
	Data      openClawAdminCommandData `json:"data"`
}

type openClawAdminCommandData struct {
	CommandID  string `json:"command_id"`
	Action     string `json:"action"`
	AgentID    string `json:"agent_id,omitempty"`
	SessionKey string `json:"session_key,omitempty"`
}

type adminCommandDispatchResponse struct {
	OK        bool   `json:"ok"`
	Queued    bool   `json:"queued"`
	CommandID string `json:"command_id"`
	Action    string `json:"action"`
	Message   string `json:"message"`
}

const (
	adminCommandActionGatewayRestart = "gateway.restart"
	adminCommandActionAgentPing      = "agent.ping"
	adminCommandActionAgentReset     = "agent.reset"
)

var sensitiveTokenPattern = regexp.MustCompile(`(?i)(oc_git_[a-z0-9]+|bearer\s+[a-z0-9._-]+)`)

func (h *AdminConnectionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	sessions := h.loadSessions(r.Context())
	summary := summarizeSessions(sessions)
	lastSync := h.loadLastSync(r.Context())
	hostDiag := h.loadHostDiagnostics(r.Context())
	bridgeDiag := h.loadBridgeDiagnostics(r.Context())

	var connected bool
	if h.OpenClawHandler != nil {
		connected = h.OpenClawHandler.IsConnected()
	}

	syncHealthy := false
	if lastSync != nil {
		syncHealthy = time.Since(*lastSync) < 2*time.Minute
	}

	sendJSON(w, http.StatusOK, adminConnectionsResponse{
		Bridge: adminConnectionsBridgeStatus{
			Connected:   connected,
			LastSync:    lastSync,
			SyncHealthy: syncHealthy,
			Diagnostics: bridgeDiag,
		},
		Host:        hostDiag,
		Sessions:    sessions,
		Summary:     summary,
		GeneratedAt: time.Now().UTC(),
	})
}

func (h *AdminConnectionsHandler) loadSessions(_ context.Context) []adminConnectionsSession {
	if h.DB == nil {
		sessions := make([]adminConnectionsSession, 0, len(memoryAgentStates))
		for _, state := range memoryAgentStates {
			sessions = append(sessions, adminConnectionsSession{
				ID:            state.ID,
				Name:          state.Name,
				Status:        state.Status,
				Model:         state.Model,
				ContextTokens: state.ContextTokens,
				TotalTokens:   state.TotalTokens,
				Channel:       state.Channel,
				SessionKey:    state.SessionKey,
				LastSeen:      state.LastSeen,
				UpdatedAt:     state.UpdatedAt,
				Stalled:       isSessionStalled(state.ContextTokens, state.UpdatedAt),
			})
		}
		sort.Slice(sessions, func(i, j int) bool {
			return sessions[i].Name < sessions[j].Name
		})
		return sessions
	}

	rows, err := h.DB.Query(`
		SELECT id, name, status, model, context_tokens, total_tokens, channel, session_key, last_seen, updated_at
		FROM agent_sync_state
		ORDER BY name
	`)
	if err != nil {
		return []adminConnectionsSession{}
	}
	defer rows.Close()

	out := make([]adminConnectionsSession, 0, 32)
	for rows.Next() {
		var (
			session                    adminConnectionsSession
			model, channel, sessionKey sql.NullString
			lastSeen                   sql.NullString
			contextTokens, totalTokens sql.NullInt64
		)
		if err := rows.Scan(
			&session.ID,
			&session.Name,
			&session.Status,
			&model,
			&contextTokens,
			&totalTokens,
			&channel,
			&sessionKey,
			&lastSeen,
			&session.UpdatedAt,
		); err != nil {
			continue
		}
		if model.Valid {
			session.Model = model.String
		}
		if contextTokens.Valid {
			session.ContextTokens = int(contextTokens.Int64)
		}
		if totalTokens.Valid {
			session.TotalTokens = int(totalTokens.Int64)
		}
		if channel.Valid {
			session.Channel = channel.String
		}
		if sessionKey.Valid {
			session.SessionKey = sessionKey.String
		}
		if lastSeen.Valid {
			session.LastSeen = lastSeen.String
		}
		session.Stalled = isSessionStalled(session.ContextTokens, session.UpdatedAt)
		out = append(out, session)
	}
	return out
}

func (h *AdminConnectionsHandler) loadLastSync(_ context.Context) *time.Time {
	if h.DB != nil {
		var value string
		if err := h.DB.QueryRow(`SELECT value FROM sync_metadata WHERE key = 'last_sync'`).Scan(&value); err == nil {
			if parsed, parseErr := time.Parse(time.RFC3339, value); parseErr == nil {
				parsedUTC := parsed.UTC()
				return &parsedUTC
			}
		}
	}
	if memoryLastSync.IsZero() {
		return nil
	}
	last := memoryLastSync.UTC()
	return &last
}

func (h *AdminConnectionsHandler) loadHostDiagnostics(_ context.Context) *OpenClawHostDiagnostics {
	var value string
	if h.DB != nil {
		if err := h.DB.QueryRow(`SELECT value FROM sync_metadata WHERE key = 'openclaw_host_diagnostics'`).Scan(&value); err == nil && value != "" {
			var payload OpenClawHostDiagnostics
			if unmarshalErr := json.Unmarshal([]byte(value), &payload); unmarshalErr == nil {
				return &payload
			}
		}
	}
	if memoryHostDiag == nil {
		return nil
	}
	host := *memoryHostDiag
	return &host
}

func (h *AdminConnectionsHandler) loadBridgeDiagnostics(_ context.Context) *OpenClawBridgeDiagnostics {
	var value string
	if h.DB != nil {
		if err := h.DB.QueryRow(`SELECT value FROM sync_metadata WHERE key = 'openclaw_bridge_diagnostics'`).Scan(&value); err == nil && value != "" {
			var payload OpenClawBridgeDiagnostics
			if unmarshalErr := json.Unmarshal([]byte(value), &payload); unmarshalErr == nil {
				return &payload
			}
		}
	}
	if memoryBridgeDiag == nil {
		return nil
	}
	bridge := *memoryBridgeDiag
	return &bridge
}

func summarizeSessions(sessions []adminConnectionsSession) adminConnectionsSessionSummary {
	summary := adminConnectionsSessionSummary{
		Total: len(sessions),
	}
	for _, session := range sessions {
		switch session.Status {
		case "online":
			summary.Online++
		case "busy":
			summary.Busy++
		default:
			summary.Offline++
		}
		if session.Stalled {
			summary.Stalled++
		}
	}
	return summary
}

func isSessionStalled(contextTokens int, updatedAt time.Time) bool {
	if contextTokens > 150000 {
		return true
	}
	if updatedAt.IsZero() {
		return false
	}
	return time.Since(updatedAt) > 2*time.Hour
}

func (h *AdminConnectionsHandler) RestartGateway(w http.ResponseWriter, r *http.Request) {
	h.dispatchAdminCommand(w, r, adminCommandActionGatewayRestart, "")
}

func (h *AdminConnectionsHandler) PingAgent(w http.ResponseWriter, r *http.Request) {
	agentID := strings.TrimSpace(chi.URLParam(r, "id"))
	if agentID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "agent id is required"})
		return
	}
	h.dispatchAdminCommand(w, r, adminCommandActionAgentPing, agentID)
}

func (h *AdminConnectionsHandler) ResetAgent(w http.ResponseWriter, r *http.Request) {
	agentID := strings.TrimSpace(chi.URLParam(r, "id"))
	if agentID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "agent id is required"})
		return
	}
	h.dispatchAdminCommand(w, r, adminCommandActionAgentReset, agentID)
}

func (h *AdminConnectionsHandler) dispatchAdminCommand(
	w http.ResponseWriter,
	r *http.Request,
	action string,
	agentID string,
) {
	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	commandID := fmt.Sprintf("cmd-%d", time.Now().UTC().UnixNano())
	event := openClawAdminCommandEvent{
		Type:      "admin.command",
		Timestamp: time.Now().UTC(),
		OrgID:     workspaceID,
		Data: openClawAdminCommandData{
			CommandID: commandID,
			Action:    action,
			AgentID:   strings.TrimSpace(agentID),
		},
	}
	if event.Data.AgentID != "" {
		event.Data.SessionKey = fmt.Sprintf("agent:%s:main", event.Data.AgentID)
	}

	dedupeKey := fmt.Sprintf("admin.command:%s", commandID)
	queuedForRetry := false
	if h.DB != nil {
		queued, err := enqueueOpenClawDispatchEvent(r.Context(), h.DB, workspaceID, event.Type, dedupeKey, event)
		if err == nil {
			queuedForRetry = queued
		}
	}

	if h.OpenClawHandler == nil {
		if queuedForRetry {
			h.logConnectionEventBestEffort(r.Context(), workspaceID, store.CreateConnectionEventInput{
				EventType: "admin.command.queued",
				Severity:  store.ConnectionEventSeverityWarning,
				Message:   fmt.Sprintf("%s queued while bridge was unavailable", action),
				Metadata:  mustMarshalJSON(map[string]string{"command_id": commandID, "action": action, "agent_id": agentID}),
			})
			sendJSON(w, http.StatusAccepted, adminCommandDispatchResponse{
				OK:        true,
				Queued:    true,
				CommandID: commandID,
				Action:    action,
				Message:   "bridge unavailable; command queued",
			})
			return
		}
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "openclaw bridge unavailable"})
		return
	}

	if err := h.OpenClawHandler.SendToOpenClaw(event); err != nil {
		if queuedForRetry {
			h.logConnectionEventBestEffort(r.Context(), workspaceID, store.CreateConnectionEventInput{
				EventType: "admin.command.queued",
				Severity:  store.ConnectionEventSeverityWarning,
				Message:   fmt.Sprintf("%s queued after bridge delivery failure", action),
				Metadata: mustMarshalJSON(map[string]string{
					"command_id": commandID,
					"action":     action,
					"agent_id":   agentID,
					"error":      err.Error(),
				}),
			})
			sendJSON(w, http.StatusAccepted, adminCommandDispatchResponse{
				OK:        true,
				Queued:    true,
				CommandID: commandID,
				Action:    action,
				Message:   "bridge unavailable; command queued",
			})
			return
		}
		if errors.Is(err, ws.ErrOpenClawNotConnected) {
			sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "openclaw bridge unavailable"})
			return
		}
		sendJSON(w, http.StatusBadGateway, errorResponse{Error: "failed to dispatch command to bridge"})
		return
	}

	if queuedForRetry {
		_ = markOpenClawDispatchDeliveredByKey(r.Context(), h.DB, dedupeKey)
	}
	h.logConnectionEventBestEffort(r.Context(), workspaceID, store.CreateConnectionEventInput{
		EventType: "admin.command.dispatched",
		Severity:  store.ConnectionEventSeverityInfo,
		Message:   fmt.Sprintf("%s dispatched to bridge", action),
		Metadata: mustMarshalJSON(map[string]string{
			"command_id": commandID,
			"action":     action,
			"agent_id":   agentID,
		}),
	})

	sendJSON(w, http.StatusOK, adminCommandDispatchResponse{
		OK:        true,
		Queued:    false,
		CommandID: commandID,
		Action:    action,
		Message:   "command dispatched",
	})
}

func (h *AdminConnectionsHandler) logConnectionEventBestEffort(
	ctx context.Context,
	workspaceID string,
	input store.CreateConnectionEventInput,
) {
	if h.EventStore == nil || workspaceID == "" {
		return
	}
	_, _ = h.EventStore.CreateWithWorkspaceID(ctx, workspaceID, input)
}

func mustMarshalJSON(value interface{}) json.RawMessage {
	payload, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return payload
}

func (h *AdminConnectionsHandler) RunDiagnostics(w http.ResponseWriter, r *http.Request) {
	checks := make([]adminDiagnosticCheck, 0, 8)

	connected := h.OpenClawHandler != nil && h.OpenClawHandler.IsConnected()
	if connected {
		checks = append(checks, adminDiagnosticCheck{
			Key:     "bridge.connection",
			Status:  "pass",
			Message: "OpenClaw bridge websocket connected",
		})
	} else {
		checks = append(checks, adminDiagnosticCheck{
			Key:     "bridge.connection",
			Status:  "fail",
			Message: "OpenClaw bridge websocket disconnected",
		})
	}

	lastSync := h.loadLastSync(r.Context())
	if lastSync == nil {
		checks = append(checks, adminDiagnosticCheck{
			Key:     "sync.freshness",
			Status:  "fail",
			Message: "No sync timestamp available",
		})
	} else if time.Since(*lastSync) < 2*time.Minute {
		checks = append(checks, adminDiagnosticCheck{
			Key:     "sync.freshness",
			Status:  "pass",
			Message: "Last sync is within freshness window",
		})
	} else {
		checks = append(checks, adminDiagnosticCheck{
			Key:     "sync.freshness",
			Status:  "warn",
			Message: "Last sync is stale",
		})
	}

	bridgeDiag := h.loadBridgeDiagnostics(r.Context())
	if bridgeDiag != nil {
		switch {
		case bridgeDiag.DispatchQueueDepth > 25:
			checks = append(checks, adminDiagnosticCheck{
				Key:     "dispatch.queue_depth",
				Status:  "fail",
				Message: fmt.Sprintf("Dispatch queue depth high (%d)", bridgeDiag.DispatchQueueDepth),
			})
		case bridgeDiag.DispatchQueueDepth > 0:
			checks = append(checks, adminDiagnosticCheck{
				Key:     "dispatch.queue_depth",
				Status:  "warn",
				Message: fmt.Sprintf("Dispatch queue has pending jobs (%d)", bridgeDiag.DispatchQueueDepth),
			})
		default:
			checks = append(checks, adminDiagnosticCheck{
				Key:     "dispatch.queue_depth",
				Status:  "pass",
				Message: "Dispatch queue is clear",
			})
		}
	}

	host := h.loadHostDiagnostics(r.Context())
	if host == nil || host.MemoryTotalBytes <= 0 {
		checks = append(checks, adminDiagnosticCheck{
			Key:     "host.memory",
			Status:  "warn",
			Message: "Memory diagnostics unavailable",
		})
	} else {
		usage := float64(host.MemoryUsedBytes) / float64(host.MemoryTotalBytes)
		switch {
		case usage >= 0.9:
			checks = append(checks, adminDiagnosticCheck{
				Key:     "host.memory",
				Status:  "fail",
				Message: fmt.Sprintf("Memory pressure high (%.0f%% used)", usage*100),
			})
		case usage >= 0.75:
			checks = append(checks, adminDiagnosticCheck{
				Key:     "host.memory",
				Status:  "warn",
				Message: fmt.Sprintf("Memory usage elevated (%.0f%% used)", usage*100),
			})
		default:
			checks = append(checks, adminDiagnosticCheck{
				Key:     "host.memory",
				Status:  "pass",
				Message: fmt.Sprintf("Memory usage healthy (%.0f%% used)", usage*100),
			})
		}
	}

	if host == nil || host.DiskFreeBytes <= 0 {
		checks = append(checks, adminDiagnosticCheck{
			Key:     "host.disk",
			Status:  "warn",
			Message: "Disk diagnostics unavailable",
		})
	} else {
		freeGB := float64(host.DiskFreeBytes) / (1024 * 1024 * 1024)
		switch {
		case freeGB < 25:
			checks = append(checks, adminDiagnosticCheck{
				Key:     "host.disk",
				Status:  "fail",
				Message: fmt.Sprintf("Low disk free space (%.1f GB)", freeGB),
			})
		case freeGB < 100:
			checks = append(checks, adminDiagnosticCheck{
				Key:     "host.disk",
				Status:  "warn",
				Message: fmt.Sprintf("Disk free space warning (%.1f GB)", freeGB),
			})
		default:
			checks = append(checks, adminDiagnosticCheck{
				Key:     "host.disk",
				Status:  "pass",
				Message: fmt.Sprintf("Disk free space healthy (%.1f GB)", freeGB),
			})
		}
	}

	sendJSON(w, http.StatusOK, adminDiagnosticsResponse{
		Checks:      checks,
		GeneratedAt: time.Now().UTC(),
	})
}

func (h *AdminConnectionsHandler) GetLogs(w http.ResponseWriter, r *http.Request) {
	if h.EventStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "connection event store unavailable"})
		return
	}

	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be a positive integer"})
			return
		}
		if parsed > 1000 {
			parsed = 1000
		}
		limit = parsed
	}
	levelFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("level")))
	search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	events, err := h.EventStore.List(r.Context(), limit)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNoWorkspace):
			sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		case errors.Is(err, store.ErrForbidden):
			sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		default:
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list logs"})
		}
		return
	}

	items := make([]adminLogItem, 0, len(events))
	for _, event := range events {
		level := strings.ToLower(strings.TrimSpace(event.Severity))
		if levelFilter != "" && level != levelFilter {
			continue
		}
		message := redactSensitive(event.Message)
		if search != "" && !strings.Contains(strings.ToLower(message), search) && !strings.Contains(strings.ToLower(event.EventType), search) {
			continue
		}
		items = append(items, adminLogItem{
			ID:        event.ID,
			Timestamp: event.CreatedAt,
			Level:     level,
			EventType: event.EventType,
			Message:   message,
			Metadata:  redactSensitiveJSON(event.Metadata),
		})
	}

	sendJSON(w, http.StatusOK, adminLogsResponse{
		Items: items,
		Total: len(items),
	})
}

func redactSensitive(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return sensitiveTokenPattern.ReplaceAllString(trimmed, "[REDACTED]")
}

func redactSensitiveJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	redacted := redactSensitive(string(raw))
	if strings.TrimSpace(redacted) == "" {
		return json.RawMessage(`{}`)
	}
	if !json.Valid([]byte(redacted)) {
		payload, _ := json.Marshal(map[string]string{"raw": redacted})
		return payload
	}
	return json.RawMessage(redacted)
}

func (h *AdminConnectionsHandler) GetEvents(w http.ResponseWriter, r *http.Request) {
	if h.EventStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "connection event store unavailable"})
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be a positive integer"})
			return
		}
		limit = parsed
	}

	events, err := h.EventStore.List(r.Context(), limit)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNoWorkspace):
			sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		case errors.Is(err, store.ErrForbidden):
			sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		default:
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list connection events"})
		}
		return
	}

	sendJSON(w, http.StatusOK, adminConnectionEventsResponse{
		Events: events,
		Total:  len(events),
	})
}
