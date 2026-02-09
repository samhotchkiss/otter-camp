package api

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

// OpenClawSyncHandler handles real-time sync from OpenClaw
type OpenClawSyncHandler struct {
	Hub            *ws.Hub
	DB             *sql.DB
	EmissionBuffer *EmissionBuffer
}

const maxOpenClawSyncBodySize = 2 << 20 // 2 MB

// In-memory fallback store (used when DB is unavailable)
var (
	memoryAgentStates  = make(map[string]*AgentState)
	memoryAgentConfigs = make(map[string]*OpenClawAgentConfig)
	memoryLastSync     time.Time
	memoryHostDiag     *OpenClawHostDiagnostics
	memoryBridgeDiag   *OpenClawBridgeDiagnostics
	memoryCronJobs     []OpenClawCronJobDiagnostics
	memoryProcesses    []OpenClawProcessDiagnostics

	progressLogEmissionMu   sync.Mutex
	progressLogEmissionSeen = make(map[string]time.Time)
)

var (
	progressLogIssuePattern      = regexp.MustCompile(`(?i)\bissue\s*#\s*(\d+)\b`)
	progressLogLooseIssuePattern = regexp.MustCompile(`#(\d+)\b`)
	progressLogProgressPattern   = regexp.MustCompile(`\b(\d+)\s*/\s*(\d+)\b`)
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

// OpenClawAgentConfig stores agent schedule details used by workflows.
type OpenClawAgentConfig struct {
	ID             string    `json:"id"`
	HeartbeatEvery string    `json:"heartbeat_every,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type OpenClawHostNetworkInterface struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Family  string `json:"family"`
}

// OpenClawHostDiagnostics captures bridge host health metadata for diagnostics pages.
type OpenClawHostDiagnostics struct {
	Hostname             string                         `json:"hostname,omitempty"`
	OS                   string                         `json:"os,omitempty"`
	Arch                 string                         `json:"arch,omitempty"`
	Platform             string                         `json:"platform,omitempty"`
	UptimeSeconds        int64                          `json:"uptime_seconds,omitempty"`
	LoadAvg              []float64                      `json:"load_avg,omitempty"`
	CPUModel             string                         `json:"cpu_model,omitempty"`
	CPUCores             int                            `json:"cpu_cores,omitempty"`
	MemoryTotalBytes     int64                          `json:"memory_total_bytes,omitempty"`
	MemoryUsedBytes      int64                          `json:"memory_used_bytes,omitempty"`
	MemoryAvailableBytes int64                          `json:"memory_available_bytes,omitempty"`
	DiskTotalBytes       int64                          `json:"disk_total_bytes,omitempty"`
	DiskUsedBytes        int64                          `json:"disk_used_bytes,omitempty"`
	DiskFreeBytes        int64                          `json:"disk_free_bytes,omitempty"`
	NetworkInterfaces    []OpenClawHostNetworkInterface `json:"network_interfaces,omitempty"`
	GatewayPID           int                            `json:"gateway_pid,omitempty"`
	GatewayVersion       string                         `json:"gateway_version,omitempty"`
	GatewayBuild         string                         `json:"gateway_build,omitempty"`
	GatewayPort          int                            `json:"gateway_port,omitempty"`
	GatewayUptimeSeconds int64                          `json:"gateway_uptime_seconds,omitempty"`
	NodeVersion          string                         `json:"node_version,omitempty"`
	OllamaVersion        string                         `json:"ollama_version,omitempty"`
	OllamaModelsLoaded   []string                       `json:"ollama_models_loaded,omitempty"`
}

// OpenClawBridgeDiagnostics captures runtime bridge metrics.
type OpenClawBridgeDiagnostics struct {
	UptimeSeconds      int64 `json:"uptime_seconds,omitempty"`
	ReconnectCount     int   `json:"reconnect_count,omitempty"`
	LastSyncDurationMS int64 `json:"last_sync_duration_ms,omitempty"`
	SyncCountTotal     int64 `json:"sync_count_total,omitempty"`
	DispatchQueueDepth int   `json:"dispatch_queue_depth,omitempty"`
	ErrorsLastHour     int   `json:"errors_last_hour,omitempty"`
}

// OpenClawCronJobDiagnostics captures cron scheduler state from bridge sync snapshots.
type OpenClawCronJobDiagnostics struct {
	ID            string     `json:"id"`
	Name          string     `json:"name,omitempty"`
	Schedule      string     `json:"schedule,omitempty"`
	SessionTarget string     `json:"session_target,omitempty"`
	PayloadType   string     `json:"payload_type,omitempty"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
	LastStatus    string     `json:"last_status,omitempty"`
	NextRunAt     *time.Time `json:"next_run_at,omitempty"`
	Enabled       bool       `json:"enabled"`
}

// OpenClawProcessDiagnostics captures active process state from bridge sync snapshots.
type OpenClawProcessDiagnostics struct {
	ID              string     `json:"id"`
	Command         string     `json:"command,omitempty"`
	PID             int        `json:"pid,omitempty"`
	Status          string     `json:"status,omitempty"`
	DurationSeconds int64      `json:"duration_seconds,omitempty"`
	AgentID         string     `json:"agent_id,omitempty"`
	SessionKey      string     `json:"session_key,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
}

// SyncPayload is the payload sent from OpenClaw bridge
type SyncPayload struct {
	Type             string                       `json:"type"` // "full" or "delta"
	Timestamp        time.Time                    `json:"timestamp"`
	Agents           []OpenClawAgent              `json:"agents,omitempty"`
	Sessions         []OpenClawSession            `json:"sessions,omitempty"`
	Emissions        []Emission                   `json:"emissions,omitempty"`
	ProgressLogLines []string                     `json:"progress_log_lines,omitempty"`
	Host             *OpenClawHostDiagnostics     `json:"host,omitempty"`
	Bridge           *OpenClawBridgeDiagnostics   `json:"bridge,omitempty"`
	CronJobs         []OpenClawCronJobDiagnostics `json:"cron_jobs,omitempty"`
	Processes        []OpenClawProcessDiagnostics `json:"processes,omitempty"`
	Source           string                       `json:"source"` // "bridge" or "webhook"
}

// SyncResponse is returned after processing sync
type SyncResponse struct {
	OK               bool      `json:"ok"`
	ProcessedAt      time.Time `json:"processed_at"`
	AgentsReceived   int       `json:"agents_received"`
	SessionsReceived int       `json:"sessions_received"`
	Message          string    `json:"message,omitempty"`
}

type openClawDispatchQueuePullResponse struct {
	Jobs []openClawDispatchQueueJobPayload `json:"jobs"`
}

type openClawDispatchQueueJobPayload struct {
	ID         int64           `json:"id"`
	EventType  string          `json:"event_type"`
	Payload    json.RawMessage `json:"payload"`
	ClaimToken string          `json:"claim_token"`
	Attempts   int             `json:"attempts"`
}

type openClawDispatchQueueAckRequest struct {
	ClaimToken string `json:"claim_token"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
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
	workspaceID := resolveOpenClawSyncWorkspaceID(r)

	// Get database - use store's DB() for connection pooling
	db := h.DB
	if db == nil {
		if dbConn, err := store.DB(); err == nil {
			db = dbConn
		}
	}

	processedCount := 0
	now := time.Now()

	if h.EmissionBuffer != nil && len(payload.Emissions) > 0 {
		if workspaceID == "" {
			log.Printf("openclaw sync emissions dropped: missing workspace context")
		} else {
			for _, raw := range payload.Emissions {
				emission, err := normalizeEmission(raw)
				if err != nil {
					log.Printf("openclaw sync emission dropped: %v", err)
					continue
				}
				emission.OrgID = workspaceID
				h.EmissionBuffer.Push(emission)
			}
		}
	}
	if h.EmissionBuffer != nil && len(payload.ProgressLogLines) > 0 {
		if workspaceID == "" {
			log.Printf("openclaw sync progress-log emissions dropped: missing workspace context")
		} else {
			for _, rawLine := range payload.ProgressLogLines {
				emission, ok := progressLogLineToEmission(rawLine)
				if !ok {
					continue
				}
				if !markProgressLogEmissionSeen(emission.ID) {
					continue
				}
				emission.OrgID = workspaceID
				h.EmissionBuffer.Push(emission)
			}
		}
	}

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

		// Calculate status based on sync freshness vs activity
		updatedAt := time.Unix(session.UpdatedAt/1000, 0)
		timeSinceUpdate := time.Since(updatedAt)
		syncAt := payload.Timestamp
		if syncAt.IsZero() {
			syncAt = now
		}
		timeSinceSync := time.Since(syncAt)
		var status string
		if timeSinceSync < 2*time.Minute {
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

	// Update agent configs
	if len(payload.Agents) > 0 {
		for _, agent := range payload.Agents {
			if agent.ID == "" {
				continue
			}
			config := &OpenClawAgentConfig{
				ID:             agent.ID,
				HeartbeatEvery: agent.Heartbeat.Every,
				UpdatedAt:      now,
			}
			memoryAgentConfigs[agent.ID] = config

			if db != nil {
				_, err := db.Exec(`
					INSERT INTO openclaw_agent_configs (id, heartbeat_every, updated_at)
					VALUES ($1, $2, $3)
					ON CONFLICT (id) DO UPDATE SET
						heartbeat_every = EXCLUDED.heartbeat_every,
						updated_at = EXCLUDED.updated_at
				`, agent.ID, agent.Heartbeat.Every, now)
				if err != nil {
					log.Printf("Failed to upsert agent config %s: %v", agent.ID, err)
				}
			}
		}
	}

	// Update sync metadata
	memoryLastSync = now // Always update memory
	if payload.Host != nil {
		host := *payload.Host
		memoryHostDiag = &host
		if db != nil {
			if err := upsertSyncMetadataJSON(r.Context(), db, "openclaw_host_diagnostics", payload.Host, now); err != nil {
				log.Printf("Failed to persist host diagnostics metadata: %v", err)
			}
		}
	}
	if payload.Bridge != nil {
		bridge := *payload.Bridge
		memoryBridgeDiag = &bridge
		if db != nil {
			if err := upsertSyncMetadataJSON(r.Context(), db, "openclaw_bridge_diagnostics", payload.Bridge, now); err != nil {
				log.Printf("Failed to persist bridge diagnostics metadata: %v", err)
			}
		}
	}
	if payload.CronJobs != nil {
		memoryCronJobs = append([]OpenClawCronJobDiagnostics(nil), payload.CronJobs...)
		if db != nil {
			if err := upsertSyncMetadataJSON(r.Context(), db, "openclaw_cron_jobs", payload.CronJobs, now); err != nil {
				log.Printf("Failed to persist cron diagnostics metadata: %v", err)
			}
		}
	}
	if payload.Processes != nil {
		memoryProcesses = append([]OpenClawProcessDiagnostics(nil), payload.Processes...)
		if db != nil {
			if err := upsertSyncMetadataJSON(r.Context(), db, "openclaw_processes", payload.Processes, now); err != nil {
				log.Printf("Failed to persist process diagnostics metadata: %v", err)
			}
		}
	}
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

func (h *OpenClawSyncHandler) PullDispatchQueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	if status, err := requireOpenClawSyncAuth(r); err != nil {
		sendJSON(w, status, errorResponse{Error: err.Error()})
		return
	}

	db := h.DB
	if db == nil {
		if dbConn, err := store.DB(); err == nil {
			db = dbConn
		}
	}
	if db == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database unavailable"})
		return
	}

	limit := defaultOpenClawDispatchPullLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
			return
		}
		limit = parsed
	}
	limit = sanitizeOpenClawDispatchPullLimit(limit)

	jobs, err := claimOpenClawDispatchJobs(r.Context(), db, limit)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to claim dispatch jobs"})
		return
	}

	payload := make([]openClawDispatchQueueJobPayload, 0, len(jobs))
	for _, job := range jobs {
		payload = append(payload, openClawDispatchQueueJobPayload{
			ID:         job.ID,
			EventType:  job.EventType,
			Payload:    job.Payload,
			ClaimToken: job.ClaimToken,
			Attempts:   job.Attempts,
		})
	}

	sendJSON(w, http.StatusOK, openClawDispatchQueuePullResponse{Jobs: payload})
}

func (h *OpenClawSyncHandler) AckDispatchQueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	if status, err := requireOpenClawSyncAuth(r); err != nil {
		sendJSON(w, status, errorResponse{Error: err.Error()})
		return
	}

	rawID := strings.TrimSpace(chi.URLParam(r, "id"))
	if rawID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "id is required"})
		return
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid id"})
		return
	}

	var req openClawDispatchQueueAckRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}
	req.ClaimToken = strings.TrimSpace(req.ClaimToken)
	if req.ClaimToken == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "claim_token is required"})
		return
	}

	db := h.DB
	if db == nil {
		if dbConn, err := store.DB(); err == nil {
			db = dbConn
		}
	}
	if db == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database unavailable"})
		return
	}

	ok, err := ackOpenClawDispatchJob(r.Context(), db, id, req.ClaimToken, req.Success, req.Error)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to acknowledge dispatch job"})
		return
	}
	if !ok {
		sendJSON(w, http.StatusConflict, errorResponse{Error: "dispatch claim is invalid or expired"})
		return
	}

	sendJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func requireOpenClawSyncAuth(r *http.Request) (int, error) {
	secret := resolveOpenClawSyncSecret()
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

func resolveOpenClawSyncSecret() string {
	// Preferred variable name.
	secret := strings.TrimSpace(os.Getenv("OPENCLAW_SYNC_SECRET"))
	if secret != "" {
		return secret
	}
	// Backward compatibility.
	secret = strings.TrimSpace(os.Getenv("OPENCLAW_SYNC_TOKEN"))
	if secret != "" {
		return secret
	}
	// Legacy fallback.
	return strings.TrimSpace(os.Getenv("OPENCLAW_WEBHOOK_SECRET"))
}

func resolveOpenClawSyncWorkspaceID(r *http.Request) string {
	if workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context())); workspaceID != "" {
		return workspaceID
	}
	if workspaceID := strings.TrimSpace(r.Header.Get("X-Workspace-ID")); workspaceID != "" {
		return workspaceID
	}
	if workspaceID := strings.TrimSpace(r.Header.Get("X-Org-ID")); workspaceID != "" {
		return workspaceID
	}
	if workspaceID := strings.TrimSpace(r.URL.Query().Get("org_id")); workspaceID != "" {
		return workspaceID
	}
	return ""
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

func upsertSyncMetadataJSON(ctx context.Context, db *sql.DB, key string, value interface{}, now time.Time) error {
	if db == nil {
		return nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(
		ctx,
		`INSERT INTO sync_metadata (key, value, updated_at)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		key,
		string(payload),
		now,
	)
	return err
}

func progressLogLineToEmission(rawLine string) (Emission, bool) {
	trimmed := strings.TrimSpace(rawLine)
	if trimmed == "" {
		return Emission{}, false
	}

	normalized := strings.TrimSpace(strings.TrimLeft(trimmed, "-*"))
	normalized = strings.TrimSpace(strings.TrimLeft(normalized, "#"))
	if normalized == "" {
		return Emission{}, false
	}

	timestamp := time.Now().UTC()
	content := normalized
	if strings.HasPrefix(content, "[") {
		if closing := strings.Index(content, "]"); closing > 1 {
			if parsed, ok := parseProgressLogTimestamp(content[1:closing]); ok {
				timestamp = parsed
			}
			content = strings.TrimSpace(content[closing+1:])
		}
	}
	if content == "" {
		return Emission{}, false
	}

	summary := progressLogSummaryFromContent(content)
	if summary == "" {
		return Emission{}, false
	}
	if len(summary) > 200 {
		summary = summary[:200]
	}

	kind := "log"
	lowerContent := strings.ToLower(content)
	if strings.HasPrefix(strings.TrimSpace(rawLine), "##") {
		kind = "milestone"
	}
	if strings.Contains(lowerContent, "error") || strings.Contains(lowerContent, "failed") || strings.Contains(lowerContent, "blocker") {
		kind = "error"
	} else if strings.Contains(lowerContent, "completed") || strings.Contains(lowerContent, "closed") || strings.Contains(lowerContent, "merged") || strings.Contains(lowerContent, "approved") || strings.Contains(lowerContent, "moved") {
		kind = "milestone"
	}

	progress := progressFromLine(content)
	if progress != nil {
		kind = "progress"
	}

	emission := Emission{
		ID:         progressLogEmissionID(trimmed),
		SourceType: "codex",
		SourceID:   "codex-progress-log",
		Kind:       kind,
		Summary:    summary,
		Timestamp:  timestamp,
		Progress:   progress,
	}

	if issueNumber := extractIssueNumberFromProgressLine(content); issueNumber > 0 {
		n := int64(issueNumber)
		emission.Scope = &EmissionScope{IssueNumber: &n}
	}

	return emission, true
}

func parseProgressLogTimestamp(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}

	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04 MST",
		"2006-01-02 15:04:05 MST",
		"2006-01-02 15:04 -0700",
		"2006-01-02 15:04:05 -0700",
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed.UTC(), true
		}
	}

	localLayouts := []string{
		"2006-01-02 15:04",
		"2006-01-02 15:04:05",
	}
	for _, layout := range localLayouts {
		parsed, err := time.ParseInLocation(layout, raw, time.Local)
		if err == nil {
			return parsed.UTC(), true
		}
	}

	return time.Time{}, false
}

func progressLogSummaryFromContent(content string) string {
	parts := strings.Split(content, "|")
	if len(parts) == 1 {
		return strings.TrimSpace(content)
	}

	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		piece := strings.TrimSpace(part)
		if piece == "" {
			continue
		}
		lower := strings.ToLower(piece)
		if strings.HasPrefix(lower, "tests:") || strings.HasPrefix(lower, "commit ") || strings.HasPrefix(lower, "commit:") {
			continue
		}
		filtered = append(filtered, piece)
	}
	if len(filtered) == 0 {
		return strings.TrimSpace(content)
	}
	if len(filtered) > 3 {
		filtered = filtered[:3]
	}
	return strings.Join(filtered, " | ")
}

func progressFromLine(content string) *EmissionProgress {
	matches := progressLogProgressPattern.FindStringSubmatch(content)
	if len(matches) != 3 {
		return nil
	}

	current, err := strconv.Atoi(matches[1])
	if err != nil {
		return nil
	}
	total, err := strconv.Atoi(matches[2])
	if err != nil {
		return nil
	}
	if total <= 0 || current < 0 || current > total {
		return nil
	}

	progress := &EmissionProgress{
		Current: current,
		Total:   total,
	}
	lower := strings.ToLower(content)
	switch {
	case strings.Contains(lower, "sub-issue"):
		unit := "sub-issues"
		progress.Unit = &unit
	case strings.Contains(lower, "test"):
		unit := "tests"
		progress.Unit = &unit
	case strings.Contains(lower, "file"):
		unit := "files"
		progress.Unit = &unit
	}
	return progress
}

func extractIssueNumberFromProgressLine(content string) int {
	matches := progressLogIssuePattern.FindStringSubmatch(content)
	if len(matches) == 2 {
		if value, err := strconv.Atoi(matches[1]); err == nil && value > 0 {
			return value
		}
	}

	loose := progressLogLooseIssuePattern.FindStringSubmatch(content)
	if len(loose) == 2 {
		if value, err := strconv.Atoi(loose[1]); err == nil && value > 0 {
			return value
		}
	}
	return 0
}

func progressLogEmissionID(line string) string {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(strings.ToLower(strings.Join(strings.Fields(line), " "))))
	return fmt.Sprintf("progress-log-%x", hash.Sum64())
}

func markProgressLogEmissionSeen(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}

	progressLogEmissionMu.Lock()
	defer progressLogEmissionMu.Unlock()

	if _, exists := progressLogEmissionSeen[id]; exists {
		return false
	}

	now := time.Now()
	progressLogEmissionSeen[id] = now
	if len(progressLogEmissionSeen) <= 4000 {
		return true
	}

	cutoff := now.Add(-24 * time.Hour)
	for key, seenAt := range progressLogEmissionSeen {
		if seenAt.Before(cutoff) {
			delete(progressLogEmissionSeen, key)
		}
	}
	return true
}

func resetProgressLogEmissionSeen() {
	progressLogEmissionMu.Lock()
	defer progressLogEmissionMu.Unlock()
	progressLogEmissionSeen = make(map[string]time.Time)
}
