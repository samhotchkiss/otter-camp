package api

import (
	"database/sql"
	"log"
	"net/http"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

// AgentsHandler handles agent-related API endpoints
type AgentsHandler struct {
	Store *store.AgentStore
	DB    *sql.DB
}

// AgentResponse represents an agent in the API response
type AgentResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	Avatar      string `json:"avatar"`
	CurrentTask string `json:"currentTask,omitempty"`
	LastSeen    string `json:"lastSeen,omitempty"`
}

// List returns all agents (demo mode supported, Postgres when available)
func (h *AgentsHandler) List(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	// Demo mode: return sample agents without auth
	if r.URL.Query().Get("demo") == "true" {
		h.sendDemoAgents(w)
		return
	}

	// Try real mode if store is available and workspace is set
	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if h.Store != nil && workspaceID != "" {
		agents, err := h.Store.List(r.Context())
		if err != nil {
			log.Printf("Failed to list agents from store: %v, falling back to demo", err)
			h.sendDemoAgents(w)
			return
		}

		// Map store agents to response format
		responseAgents := make([]AgentResponse, 0, len(agents))
		for _, agent := range agents {
			resp := AgentResponse{
				ID:     agent.ID,
				Name:   agent.DisplayName,
				Role:   agent.Slug, // Use slug as role for now
				Status: agent.Status,
				Avatar: "🤖", // Default emoji
			}

			if agent.AvatarURL != nil && *agent.AvatarURL != "" {
				resp.Avatar = *agent.AvatarURL
			}

			// Calculate last seen from UpdatedAt
			if agent.Status == "offline" {
				resp.LastSeen = normalizeLastSeenTimestamp(agent.UpdatedAt)
			}

			responseAgents = append(responseAgents, resp)
		}

		sendJSON(w, http.StatusOK, map[string]interface{}{
			"agents": responseAgents,
			"total":  len(responseAgents),
		})
		return
	}

	// No workspace or no store - fall back to demo
	h.sendDemoAgents(w)
}

// sendDemoAgents returns hardcoded demo agents
func (h *AgentsHandler) sendDemoAgents(w http.ResponseWriter) {
	now := time.Now()
	demoAgents := []AgentResponse{
		{
			ID:          "frank",
			Name:        "Frank",
			Role:        "Chief of Staff",
			Status:      "online",
			Avatar:      "🎯",
			CurrentTask: "Coordinating OtterCamp deployment",
		},
		{
			ID:          "derek",
			Name:        "Derek",
			Role:        "Engineering Lead",
			Status:      "online",
			Avatar:      "🏗️",
			CurrentTask: "Building API endpoints",
		},
		{
			ID:          "jeff-g",
			Name:        "Jeff G",
			Role:        "Head of Design",
			Status:      "online",
			Avatar:      "🎨",
			CurrentTask: "Design spec reviews",
		},
		{
			ID:          "nova",
			Name:        "Nova",
			Role:        "Social Media",
			Status:      "online",
			Avatar:      "✨",
			CurrentTask: "Scheduling tweets",
		},
		{
			ID:          "stone",
			Name:        "Stone",
			Role:        "Content",
			Status:      "busy",
			Avatar:      "🪨",
			CurrentTask: "Writing blog post",
		},
		{
			ID:          "ivy",
			Name:        "Ivy",
			Role:        "ItsAlive Product",
			Status:      "online",
			Avatar:      "🌿",
			CurrentTask: "Awaiting deploy approval",
		},
		{
			ID:       "max",
			Name:     "Max",
			Role:     "Personal Ops",
			Status:   "offline",
			Avatar:   "🏠",
			LastSeen: normalizeLastSeenTimestamp(now.Add(-2 * time.Hour)),
		},
		{
			ID:          "penny",
			Name:        "Penny",
			Role:        "Email Manager",
			Status:      "online",
			Avatar:      "📧",
			CurrentTask: "Triaging inbox",
		},
		{
			ID:          "beau-h",
			Name:        "Beau H",
			Role:        "Markets & Trading",
			Status:      "online",
			Avatar:      "📈",
			CurrentTask: "Monitoring watchlist",
		},
		{
			ID:       "josh-s",
			Name:     "Josh S",
			Role:     "Head of Engineering",
			Status:   "offline",
			Avatar:   "⚙️",
			LastSeen: normalizeLastSeenTimestamp(now.Add(-1 * time.Hour)),
		},
		{
			ID:          "jeremy-h",
			Name:        "Jeremy H",
			Role:        "Head of QC",
			Status:      "online",
			Avatar:      "🔍",
			CurrentTask: "Code review queue",
		},
		{
			ID:       "claudette",
			Name:     "Claudette",
			Role:     "Essie's Assistant",
			Status:   "offline",
			Avatar:   "💜",
			LastSeen: normalizeLastSeenTimestamp(now.Add(-3 * time.Hour)),
		},
	}
	sendJSON(w, http.StatusOK, map[string]interface{}{
		"agents": demoAgents,
		"total":  len(demoAgents),
	})
}
