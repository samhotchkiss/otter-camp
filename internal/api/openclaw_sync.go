package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/ws"
)

// OpenClawSyncHandler handles real-time sync from OpenClaw
type OpenClawSyncHandler struct {
	Hub *ws.Hub
}

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
	OK             bool      `json:"ok"`
	ProcessedAt    time.Time `json:"processed_at"`
	AgentsReceived int       `json:"agents_received"`
	SessionsReceived int     `json:"sessions_received"`
	Message        string    `json:"message,omitempty"`
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

// In-memory store for now (will be replaced with DB)
var (
	currentAgentStates = make(map[string]*AgentState)
	lastSyncTime       time.Time
)

// Handle processes incoming sync data from OpenClaw
func (h *OpenClawSyncHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	// TODO: Add auth token validation
	// token := r.Header.Get("Authorization")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "failed to read body"})
		return
	}
	defer r.Body.Close()

	var payload SyncPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON: " + err.Error()})
		return
	}

	// Process agents
	agentRoles := map[string]string{
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

	agentNames := map[string]string{
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

	agentAvatars := map[string]string{
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

	// Build agent states from sessions
	for _, session := range payload.Sessions {
		// Extract agent ID from session key (e.g., "agent:main:slack:..." -> "main")
		agentID := extractAgentID(session.Key)
		if agentID == "" {
			continue
		}

		state, exists := currentAgentStates[agentID]
		if !exists {
			state = &AgentState{
				ID:     agentID,
				Name:   agentNames[agentID],
				Role:   agentRoles[agentID],
				Avatar: agentAvatars[agentID],
			}
			if state.Name == "" {
				state.Name = agentID
			}
			currentAgentStates[agentID] = state
		}

		// Update with session data
		state.Model = session.Model
		state.TotalTokens = session.TotalTokens
		state.ContextTokens = session.ContextTokens
		state.Channel = session.Channel
		state.SessionKey = session.Key
		state.UpdatedAt = time.Unix(session.UpdatedAt/1000, 0)

		// Determine status based on activity
		timeSinceUpdate := time.Since(state.UpdatedAt)
		if timeSinceUpdate < 5*time.Minute {
			state.Status = "online"
		} else if timeSinceUpdate < 30*time.Minute {
			state.Status = "busy"
		} else {
			state.Status = "offline"
			state.LastSeen = formatTimeSince(state.UpdatedAt)
		}

		// Extract current task from display name if available
		if session.DisplayName != "" {
			state.CurrentTask = session.DisplayName
		}
	}

	lastSyncTime = time.Now()

	// Broadcast to connected WebSocket clients
	if h.Hub != nil {
		broadcastPayload := map[string]interface{}{
			"type":      "agents_updated",
			"agents":    getAgentStateList(),
			"timestamp": lastSyncTime,
		}
		if data, err := json.Marshal(broadcastPayload); err == nil {
			// Broadcast to all orgs for now (single-user MVP)
			h.Hub.Broadcast("default", data)
		}
	}

	sendJSON(w, http.StatusOK, SyncResponse{
		OK:               true,
		ProcessedAt:      lastSyncTime,
		AgentsReceived:   len(payload.Agents),
		SessionsReceived: len(payload.Sessions),
		Message:          "Sync processed successfully",
	})
}

// GetAgents returns current agent states
func (h *OpenClawSyncHandler) GetAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	agents := getAgentStateList()

	sendJSON(w, http.StatusOK, map[string]interface{}{
		"agents":       agents,
		"total":        len(agents),
		"last_sync":    lastSyncTime,
		"sync_healthy": time.Since(lastSyncTime) < 2*time.Minute,
	})
}

func getAgentStateList() []AgentState {
	agents := make([]AgentState, 0, len(currentAgentStates))
	for _, state := range currentAgentStates {
		agents = append(agents, *state)
	}
	return agents
}

func extractAgentID(sessionKey string) string {
	// Session keys look like: "agent:main:slack:channel:..." or "agent:2b:main"
	// We want to extract the agent ID (e.g., "main", "2b")
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
