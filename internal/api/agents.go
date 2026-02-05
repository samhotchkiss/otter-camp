package api

import (
	"net/http"
)

// AgentsHandler handles agent-related API endpoints
type AgentsHandler struct{}

// DemoAgent represents an agent in demo mode
type DemoAgent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	Avatar      string `json:"avatar"`
	CurrentTask string `json:"currentTask,omitempty"`
	LastSeen    string `json:"lastSeen,omitempty"`
}

// List returns all agents (demo mode supported)
func (h *AgentsHandler) List(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	// Demo mode: return sample agents without auth
	if r.URL.Query().Get("demo") == "true" || r.URL.Query().Get("org_id") == "" {
		demoAgents := []DemoAgent{
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
				ID:          "max",
				Name:        "Max",
				Role:        "Personal Ops",
				Status:      "offline",
				Avatar:      "🏠",
				LastSeen:    "2 hours ago",
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
				ID:          "josh-s",
				Name:        "Josh S",
				Role:        "Head of Engineering",
				Status:      "offline",
				Avatar:      "⚙️",
				LastSeen:    "1 hour ago",
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
				ID:          "claudette",
				Name:        "Claudette",
				Role:        "Essie's Assistant",
				Status:      "offline",
				Avatar:      "💜",
				LastSeen:    "3 hours ago",
			},
		}
		sendJSON(w, http.StatusOK, map[string]interface{}{
			"agents": demoAgents,
			"total":  len(demoAgents),
		})
		return
	}

	// TODO: Implement real agent listing from database
	sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id required for non-demo mode"})
}
