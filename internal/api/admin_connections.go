package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

type openClawConnectionStatus interface {
	IsConnected() bool
}

type AdminConnectionsHandler struct {
	DB              *sql.DB
	OpenClawHandler openClawConnectionStatus
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
