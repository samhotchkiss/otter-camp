package api

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
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

type agentWhoAmIAgentPayload struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Role   string `json:"role,omitempty"`
	Emoji  string `json:"emoji,omitempty"`
	Avatar string `json:"avatar,omitempty"`
}

type agentWhoAmITaskPointer struct {
	Project string `json:"project,omitempty"`
	Issue   string `json:"issue,omitempty"`
	Title   string `json:"title"`
	Status  string `json:"status"`
}

type agentWhoAmIResponse struct {
	Profile             string                   `json:"profile"`
	Agent               agentWhoAmIAgentPayload  `json:"agent"`
	Soul                string                   `json:"soul,omitempty"`
	Identity            string                   `json:"identity,omitempty"`
	Instructions        string                   `json:"instructions,omitempty"`
	SoulSummary         string                   `json:"soul_summary,omitempty"`
	IdentitySummary     string                   `json:"identity_summary,omitempty"`
	InstructionsSummary string                   `json:"instructions_summary,omitempty"`
	ActiveTasks         []agentWhoAmITaskPointer `json:"active_tasks,omitempty"`
}

type whoAmIIdentityFields struct {
	Role         string
	Emoji        string
	Soul         string
	Identity     string
	Instructions string
}

const (
	whoAmIProfileCompact            = "compact"
	whoAmIProfileFull               = "full"
	defaultWhoAmIRole               = "Agent"
	defaultWhoAmIEmoji              = "🤖"
	whoAmICompactSummaryLimit       = 600
	whoAmIFullPayloadFieldCharLimit = 12000
	whoAmIMaxActiveTasks            = 8
)

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

// WhoAmI returns profile-scoped identity context for an agent.
func (h *AgentsHandler) WhoAmI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}
	if h.Store == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	agentID := strings.TrimSpace(chi.URLParam(r, "id"))
	if !uuidRegex.MatchString(agentID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "agent id must be a UUID"})
		return
	}

	profile := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("profile")))
	if profile == "" {
		profile = whoAmIProfileCompact
	}
	if profile != whoAmIProfileCompact && profile != whoAmIProfileFull {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "profile must be compact or full"})
		return
	}

	sessionKey := strings.TrimSpace(r.URL.Query().Get("session_key"))
	if sessionKey != "" {
		sessionAgentID, ok := ExtractChameleonSessionAgentID(sessionKey)
		if !ok {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "session_key must match canonical chameleon format"})
			return
		}
		if !strings.EqualFold(sessionAgentID, agentID) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "session agent does not match requested agent"})
			return
		}
	}

	agent, err := h.Store.GetByID(r.Context(), agentID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "agent not found"})
		case errors.Is(err, store.ErrForbidden):
			sendJSON(w, http.StatusForbidden, errorResponse{Error: "agent belongs to a different workspace"})
		default:
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load agent"})
		}
		return
	}

	identity, err := h.loadWhoAmIIdentityFields(r, workspaceID, agentID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load agent identity"})
		return
	}

	response := agentWhoAmIResponse{
		Profile: profile,
		Agent: agentWhoAmIAgentPayload{
			ID:     agent.ID,
			Name:   strings.TrimSpace(agent.DisplayName),
			Role:   fallbackString(identity.Role, defaultWhoAmIRole),
			Emoji:  fallbackString(identity.Emoji, defaultWhoAmIEmoji),
			Avatar: fallbackString(trimmedPointerString(agent.AvatarURL), ""),
		},
		ActiveTasks: h.listWhoAmIActiveTasks(r, workspaceID, agentID),
	}

	if response.Agent.Name == "" {
		response.Agent.Name = strings.TrimSpace(agent.Slug)
	}
	if response.Agent.Name == "" {
		response.Agent.Name = "Agent"
	}

	if profile == whoAmIProfileCompact {
		response.SoulSummary = summarizeWhoAmIText(identity.Soul, whoAmICompactSummaryLimit)
		response.IdentitySummary = summarizeWhoAmIText(identity.Identity, whoAmICompactSummaryLimit)
		response.InstructionsSummary = summarizeWhoAmIText(identity.Instructions, whoAmICompactSummaryLimit)
		sendJSON(w, http.StatusOK, response)
		return
	}

	response.Soul = capWhoAmIText(identity.Soul, whoAmIFullPayloadFieldCharLimit)
	response.Identity = capWhoAmIText(identity.Identity, whoAmIFullPayloadFieldCharLimit)
	response.Instructions = capWhoAmIText(identity.Instructions, whoAmIFullPayloadFieldCharLimit)
	sendJSON(w, http.StatusOK, response)
}

func (h *AgentsHandler) loadWhoAmIIdentityFields(r *http.Request, workspaceID, agentID string) (whoAmIIdentityFields, error) {
	var identity whoAmIIdentityFields
	err := h.DB.QueryRowContext(
		r.Context(),
		`SELECT
			COALESCE(role, ''),
			COALESCE(emoji, ''),
			COALESCE(soul_md, ''),
			COALESCE(identity_md, ''),
			COALESCE(instructions_md, '')
		FROM agents
		WHERE id = $1 AND org_id = $2`,
		agentID,
		workspaceID,
	).Scan(
		&identity.Role,
		&identity.Emoji,
		&identity.Soul,
		&identity.Identity,
		&identity.Instructions,
	)
	if err != nil {
		return whoAmIIdentityFields{}, err
	}
	return identity, nil
}

func (h *AgentsHandler) listWhoAmIActiveTasks(r *http.Request, workspaceID, agentID string) []agentWhoAmITaskPointer {
	rows, err := h.DB.QueryContext(
		r.Context(),
		`SELECT
			COALESCE(p.name, ''),
			t.number,
			t.title,
			t.status
		FROM tasks t
		LEFT JOIN projects p ON p.id = t.project_id
		WHERE t.org_id = $1
		  AND t.assigned_agent_id = $2
		  AND t.status <> 'done'
		ORDER BY t.updated_at DESC
		LIMIT $3`,
		workspaceID,
		agentID,
		whoAmIMaxActiveTasks,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	pointers := make([]agentWhoAmITaskPointer, 0)
	for rows.Next() {
		var (
			project string
			number  sql.NullInt64
			title   string
			status  string
		)
		if scanErr := rows.Scan(&project, &number, &title, &status); scanErr != nil {
			continue
		}
		pointer := agentWhoAmITaskPointer{
			Project: strings.TrimSpace(project),
			Title:   strings.TrimSpace(title),
			Status:  strings.TrimSpace(status),
		}
		if number.Valid && number.Int64 > 0 {
			pointer.Issue = "#" + strconv.FormatInt(number.Int64, 10)
		}
		pointers = append(pointers, pointer)
	}
	if len(pointers) == 0 {
		return nil
	}
	return pointers
}

func summarizeWhoAmIText(value string, limit int) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	return capWhoAmIText(trimmed, limit)
}

func capWhoAmIText(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	if limit <= 0 || len(trimmed) <= limit {
		return trimmed
	}
	if limit <= 3 {
		return trimmed[:limit]
	}
	return trimmed[:limit-3] + "..."
}

func fallbackString(primary string, fallback string) string {
	trimmed := strings.TrimSpace(primary)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func trimmedPointerString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
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
