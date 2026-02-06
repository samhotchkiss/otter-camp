package api

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

// OpenClawSyncHandler handles real-time sync from OpenClaw
type OpenClawSyncHandler struct {
	Hub *ws.Hub
	DB  *sql.DB
}

const maxOpenClawSyncBodySize = 2 << 20 // 2 MB

// In-memory fallback store (used when DB is unavailable)
var (
	memoryAgentStates = make(map[string]*AgentState)
	memoryLastSync    time.Time
)

// OpenClawSession represents a session from OpenClaw's sessions_list
type OpenClawSession struct {
	Key             string                 `json:"key"`
	Kind            string                 `json:"kind"`
	Channel         string                 `json:"channel"`
	DisplayName     string                 `json:"displayName,omitempty"`
	DeliveryContext map[string]interface{} `json:"deliveryContext,omitempty"`
	UpdatedAt       int64                  `json:"updatedAt"`
	SessionID       string                 `json:"sessionId"`
	Model           string                 `json:"model"`
	ContextTokens   int                    `json:"contextTokens"`
	TotalTokens     int                    `json:"totalTokens"`
	SystemSent      bool                   `json:"systemSent"`
	AbortedLastRun  bool                   `json:"abortedLastRun"`
	LastChannel     string                 `json:"lastChannel,omitempty"`
	LastTo          string                 `json:"lastTo,omitempty"`
	LastAccountId   string                 `json:"lastAccountId,omitempty"`
	TranscriptPath  string                 `json:"transcriptPath,omitempty"`
}

// OpenClawAgent represents an agent from OpenClaw config
type OpenClawAgent struct {
	ID        string `json:"id"`
	Heartbeat struct {
		Every string `json:"every"`
	} `json:"heartbeat,omitempty"`
	Model struct {
		Primary   string   `json:"primary"`
		Fallbacks []string `json:"fallbacks"`
	} `json:"model,omitempty"`
}

// SyncPayload is the payload sent from OpenClaw bridge
type SyncPayload struct {
	Type      string            `json:"type"` // "full" or "delta"
	Timestamp time.Time         `json:"timestamp"`
	Agents    []OpenClawAgent   `json:"agents,omitempty"`
	Sessions  []OpenClawSession `json:"sessions,omitempty"`
	Source    string            `json:"source"` // "bridge" or "webhook"
}

// SyncResponse is returned after processing sync
type SyncResponse struct {
	OK               bool      `json:"ok"`
	ProcessedAt      time.Time `json:"processed_at"`
	AgentsReceived   int       `json:"agents_received"`
	SessionsReceived int       `json:"sessions_received"`
	Message          string    `json:"message,omitempty"`
}

// AgentState represents the current state of an agent for the frontend
type AgentState struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Role          string    `json:"role,omitempty"`
	Status        string    `json:"status"` // online, busy, offline
	Avatar        string    `json:"avatar,omitempty"`
	CurrentTask   string    `json:"currentTask,omitempty"`
	LastSeen      string    `json:"lastSeen,omitempty"`
	Model         string    `json:"model,omitempty"`
	TotalTokens   int       `json:"totalTokens,omitempty"`
	ContextTokens int       `json:"contextTokens,omitempty"`
	Channel       string    `json:"channel,omitempty"`
	SessionKey    string    `json:"sessionKey,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Agent metadata mappings
var agentRoles = map[string]string{
	"main":           "Chief of Staff",
	"2b":             "Engineering Lead",
	"avatar-design":  "Head of Design",
	"ai-updates":     "Social Media",
	"three-stones":   "Content",
	"itsalive":       "ItsAlive Product",
	"personal":       "Personal Ops",
	"email-mgmt":     "Email Manager",
	"trading":        "Markets & Trading",
	"technonymous":   "Head of Engineering",
	"personal-brand": "Head of QC",
	"essie":          "Essie's Assistant",
	"pearl":          "Infrastructure",
}

var agentNames = map[string]string{
	"main":           "Frank",
	"2b":             "Derek",
	"avatar-design":  "Jeff G",
	"ai-updates":     "Nova",
	"three-stones":   "Stone",
	"itsalive":       "Ivy",
	"personal":       "Max",
	"email-mgmt":     "Penny",
	"trading":        "Beau H",
	"technonymous":   "Josh S",
	"personal-brand": "Jeremy H",
	"essie":          "Claudette",
	"pearl":          "Pearl",
}

var agentAvatars = map[string]string{
	"main":           "🎯",
	"2b":             "🏗️",
	"avatar-design":  "🎨",
	"ai-updates":     "✨",
	"three-stones":   "🪨",
	"itsalive":       "🌿",
	"personal":       "🏠",
	"email-mgmt":     "📧",
	"trading":        "📈",
	"technonymous":   "⚙️",
	"personal-brand": "🔍",
	"essie":          "💜",
	"pearl":          "🔮",
}

// Handle processes incoming sync data from OpenClaw
func (h *OpenClawSyncHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	if status, err := requireOpenClawSyncAuth(r); err != nil {
		sendJSON(w, status, errorResponse{Error: err.Error()})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxOpenClawSyncBodySize+1))
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "failed to read body"})
		return
	}
	defer r.Body.Close()
	if len(body) > maxOpenClawSyncBodySize {
		sendJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "payload too large"})
		return
	}

	var payload SyncPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON: " + err.Error()})
		return
	}

	// Get database - use store's DB() for connection pooling
	db := h.DB
	if db == nil {
		if dbConn, err := store.DB(); err == nil {
			db = dbConn
		}
	}

	processedCount := 0
	now := time.Now()

	// Process sessions into agent states
	for _, session := range payload.Sessions {
		agentID := extractAgentID(session.Key)
		if agentID == "" {
			continue
		}

		name := agentNames[agentID]
		if name == "" {
			name = agentID
		}
		role := agentRoles[agentID]
		avatar := agentAvatars[agentID]

		// Calculate status based on activity
		updatedAt := time.Unix(session.UpdatedAt/1000, 0)
		timeSinceUpdate := time.Since(updatedAt)
		var status string
		if timeSinceUpdate < 5*time.Minute {
			status = "online"
		} else if timeSinceUpdate < 30*time.Minute {
			status = "busy"
		} else {
			status = "offline"
		}

		lastSeen := normalizeLastSeenTimestamp(updatedAt)
		currentTask := normalizeCurrentTask(session.DisplayName)

		// Build agent state
		agentState := &AgentState{
			ID:            agentID,
			Name:          name,
			Role:          role,
			Status:        status,
			Avatar:        avatar,
			CurrentTask:   currentTask,
			LastSeen:      lastSeen,
			Model:         session.Model,
			TotalTokens:   session.TotalTokens,
			ContextTokens: session.ContextTokens,
			Channel:       session.Channel,
			SessionKey:    session.Key,
			UpdatedAt:     updatedAt,
		}

		// Always update in-memory store (fallback when DB unavailable)
		memoryAgentStates[agentID] = agentState
		processedCount++

		// Also persist to database if available
		if db != nil {
			_, err := db.Exec(`
				INSERT INTO agent_sync_state 
					(id, name, role, status, avatar, current_task, last_seen, model, 
					 total_tokens, context_tokens, channel, session_key, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
				ON CONFLICT (id) DO UPDATE SET
					name = EXCLUDED.name,
					role = EXCLUDED.role,
					status = EXCLUDED.status,
					avatar = EXCLUDED.avatar,
					current_task = EXCLUDED.current_task,
					last_seen = EXCLUDED.last_seen,
					model = EXCLUDED.model,
					total_tokens = EXCLUDED.total_tokens,
					context_tokens = EXCLUDED.context_tokens,
					channel = EXCLUDED.channel,
					session_key = EXCLUDED.session_key,
					updated_at = EXCLUDED.updated_at
			`, agentID, name, role, status, avatar, currentTask, lastSeen,
				session.Model, session.TotalTokens, session.ContextTokens,
				session.Channel, session.Key, updatedAt)

			if err != nil {
				log.Printf("Failed to upsert agent %s to DB: %v", agentID, err)
			}
		}
	}

	// Update sync metadata
	memoryLastSync = now // Always update memory
	if db != nil {
		_, _ = db.Exec(`
			INSERT INTO sync_metadata (key, value, updated_at)
			VALUES ('last_sync', $1, $2)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at
		`, now.Format(time.RFC3339), now)
	}

	// Broadcast to connected WebSocket clients
	if h.Hub != nil {
		agents, _ := h.getAgentsFromDB(db)
		broadcastPayload := map[string]interface{}{
			"type":      "agents_updated",
			"agents":    agents,
			"timestamp": now,
		}
		if data, err := json.Marshal(broadcastPayload); err == nil {
			h.Hub.Broadcast("default", data)
		}
	}

	sendJSON(w, http.StatusOK, SyncResponse{
		OK:               true,
		ProcessedAt:      now,
		AgentsReceived:   len(payload.Agents),
		SessionsReceived: processedCount,
		Message:          fmt.Sprintf("Synced %d agents to database", processedCount),
	})
}

func requireOpenClawSyncAuth(r *http.Request) (int, error) {
	secret := strings.TrimSpace(os.Getenv("OPENCLAW_SYNC_TOKEN"))
	if secret == "" {
		secret = strings.TrimSpace(os.Getenv("OPENCLAW_WEBHOOK_SECRET"))
	}
	if secret == "" {
		return http.StatusServiceUnavailable, fmt.Errorf("sync authentication is not configured")
	}

	token := strings.TrimSpace(r.Header.Get("X-OpenClaw-Token"))
	if token == "" {
		token = strings.TrimSpace(r.Header.Get("X-Sync-Token"))
	}
	if token == "" {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		}
	}

	if token == "" {
		return http.StatusUnauthorized, fmt.Errorf("missing authentication")
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(secret)) != 1 {
		return http.StatusUnauthorized, fmt.Errorf("invalid authentication")
	}

	return http.StatusOK, nil
}

// GetAgents returns current agent states
func (h *OpenClawSyncHandler) GetAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	db := h.DB
	if db == nil {
		if dbConn, err := store.DB(); err == nil {
			db = dbConn
		}
	}

	agents, err := h.getAgentsFromDB(db)
	if err != nil {
		log.Printf("Failed to get agents from DB: %v", err)
	}

	// Get last sync time (try DB first, fall back to memory)
	var lastSync time.Time
	var syncHealthy bool
	if db != nil {
		var lastSyncStr string
		err := db.QueryRow(`SELECT value FROM sync_metadata WHERE key = 'last_sync'`).Scan(&lastSyncStr)
		if err == nil {
			lastSync, _ = time.Parse(time.RFC3339, lastSyncStr)
			syncHealthy = time.Since(lastSync) < 2*time.Minute
		}
	}
	// Fall back to memory if DB didn't have data
	if lastSync.IsZero() && !memoryLastSync.IsZero() {
		lastSync = memoryLastSync
		syncHealthy = time.Since(lastSync) < 2*time.Minute
	}

	sendJSON(w, http.StatusOK, map[string]interface{}{
		"agents":       agents,
		"total":        len(agents),
		"last_sync":    lastSync,
		"sync_healthy": syncHealthy,
	})
}

func (h *OpenClawSyncHandler) getAgentsFromDB(db *sql.DB) ([]AgentState, error) {
	// If no database, return from in-memory store
	if db == nil {
		agents := make([]AgentState, 0, len(memoryAgentStates))
		for _, state := range memoryAgentStates {
			agents = append(agents, *state)
		}
		return agents, nil
	}

	rows, err := db.Query(`
		SELECT id, name, role, status, avatar, current_task, last_seen, 
		       model, total_tokens, context_tokens, channel, session_key, updated_at
		FROM agent_sync_state
		ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	agents := []AgentState{}
	for rows.Next() {
		var a AgentState
		var role, avatar, currentTask, lastSeen, model, channel, sessionKey sql.NullString
		var totalTokens, contextTokens sql.NullInt64

		err := rows.Scan(&a.ID, &a.Name, &role, &a.Status, &avatar, &currentTask,
			&lastSeen, &model, &totalTokens, &contextTokens, &channel, &sessionKey, &a.UpdatedAt)
		if err != nil {
			continue
		}

		if role.Valid {
			a.Role = role.String
		}
		if avatar.Valid {
			a.Avatar = avatar.String
		}
		if currentTask.Valid {
			a.CurrentTask = currentTask.String
		}
		if lastSeen.Valid {
			a.LastSeen = lastSeen.String
		}
		if model.Valid {
			a.Model = model.String
		}
		if channel.Valid {
			a.Channel = channel.String
		}
		if sessionKey.Valid {
			a.SessionKey = sessionKey.String
		}
		if totalTokens.Valid {
			a.TotalTokens = int(totalTokens.Int64)
		}
		if contextTokens.Valid {
			a.ContextTokens = int(contextTokens.Int64)
		}

		agents = append(agents, a)
	}

	return agents, rows.Err()
}

func extractAgentID(sessionKey string) string {
	// Session keys look like: "agent:main:slack:channel:..." or "agent:2b:main"
	if len(sessionKey) < 7 || sessionKey[:6] != "agent:" {
		return ""
	}
	rest := sessionKey[6:]
	for i, c := range rest {
		if c == ':' {
			return rest[:i]
		}
	}
	return rest
}

func formatTimeSince(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", days)
}
