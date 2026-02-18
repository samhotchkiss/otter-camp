package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	Hub                 *ws.Hub
	DB                  *sql.DB
	EmissionBuffer      *EmissionBuffer
	EmissionBroadcaster emissionBroadcaster
}

const maxOpenClawSyncBodySize = 2 << 20 // 2 MB

const (
	progressLogEmissionSeenSoftThreshold = 4000
	progressLogEmissionSeenHardCap       = 8000
	progressLogEmissionSeenOldestDivisor = 4 // evict oldest 25% when hard cap is exceeded
)

// In-memory fallback store (used when DB is unavailable)
var (
	memoryStateMu       sync.RWMutex
	memoryAgentStates   = make(map[string]*AgentState)
	memoryAgentConfigs  = make(map[string]*OpenClawAgentConfig)
	memoryLastSync      time.Time
	memoryHostDiag      *OpenClawHostDiagnostics
	memoryBridgeDiag    *OpenClawBridgeDiagnostics
	memoryCronJobs      []OpenClawCronJobDiagnostics
	memoryProcesses     []OpenClawProcessDiagnostics
	memoryConfig        *openClawConfigSnapshotRecord
	memoryConfigHistory []openClawConfigSnapshotRecord

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

type OpenClawConfigSnapshot struct {
	Path       string      `json:"path,omitempty"`
	Source     string      `json:"source,omitempty"`
	CapturedAt time.Time   `json:"captured_at,omitempty"`
	Data       interface{} `json:"data,omitempty"`
}

type openClawConfigSnapshotRecord struct {
	Hash       string          `json:"hash"`
	Source     string          `json:"source,omitempty"`
	Path       string          `json:"path,omitempty"`
	CapturedAt time.Time       `json:"captured_at"`
	Data       json.RawMessage `json:"data"`
}

const (
	syncMetadataOpenClawConfigSnapshotKey = "openclaw_config_snapshot"
	syncMetadataOpenClawConfigHistoryKey  = "openclaw_config_history"
	syncMetadataOpenClawLegacyImportKey   = "openclaw_legacy_workspace_import"
	maxOpenClawConfigHistoryEntries       = 50
)

const (
	legacyTransitionFilename          = "LEGACY_TRANSITION.md"
	legacyTransitionMarker            = "OtterCamp Legacy Transition"
	legacyTransitionAgentsPointer     = "## OtterCamp Legacy Transition\nThis workspace is legacy context. Read `LEGACY_TRANSITION.md` before doing work.\n"
	legacyTransitionChecklistTemplate = "# LEGACY_TRANSITION.md\n\n" +
		"OtterCamp + Chameleon is now the active execution path for this workspace.\n" +
		"This workspace is retained as legacy context only.\n\n" +
		"Required checklist:\n" +
		"1. If task has no project, create one before writing deliverables (`otter project create ...`).\n" +
		"2. Clone/open the project repo and do all file writes inside that repo.\n" +
		"3. Commit with the required message format and push.\n" +
		"4. Link commit(s) back to the OtterCamp task/issue.\n" +
		"5. Do not keep final work product in the legacy workspace.\n"
)

var commitLegacyWorkspaceTx = func(tx *sql.Tx) error {
	return tx.Commit()
}

var renameFileForAtomicWrite = os.Rename
var removeFileForAtomicWrite = os.Remove
var createTempFileForAtomicWrite = os.CreateTemp

type openClawLegacyWorkspaceDescriptor struct {
	ID        string
	Name      string
	Workspace string
	IsDefault bool
}

type openClawLegacyWorkspaceFiles struct {
	Soul      string
	Identity  string
	Tools     string
	Agents    string
	LongTerm  string
	DailyByID map[string]string
}

type openClawLegacyImportReport struct {
	ImportedAgents             int      `json:"imported_agents"`
	ImportedLongTermMemories   int      `json:"imported_long_term_memories"`
	ImportedDailyMemories      int      `json:"imported_daily_memories"`
	TransitionFilesGenerated   int      `json:"transition_files_generated"`
	SkippedWorkspaceCount      int      `json:"skipped_workspace_count"`
	WorkspaceWarnings          []string `json:"workspace_warnings,omitempty"`
	ProcessedWorkspaceCount    int      `json:"processed_workspace_count"`
	ProcessedRetiredWorkspaces int      `json:"processed_retired_workspaces"`
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
	Config           *OpenClawConfigSnapshot      `json:"config,omitempty"`
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

	// Resolve DB early so session-based auth can work (hosted bridge).
	db := h.DB
	if db == nil {
		if dbConn, err := store.DB(); err == nil {
			db = dbConn
		}
	}

	if status, err := requireOpenClawSyncAuth(r.Context(), db, r); err != nil {
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
	workspaceID, err := resolveOpenClawSyncWorkspaceID(r)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	processedCount := 0
	now := time.Now()
	syncBroadcaster := h.resolveEmissionBroadcaster()

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
				broadcastEmissionEvent(syncBroadcaster, workspaceID, emission)
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
				broadcastEmissionEvent(syncBroadcaster, workspaceID, emission)
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

		updatedAt := time.Unix(session.UpdatedAt/1000, 0)
		status := deriveAgentStatus(updatedAt, session.TotalTokens)

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
		// Only overwrite if this session is more recent than the stored one
		memoryStateMu.Lock()
		if existing, ok := memoryAgentStates[agentID]; !ok || updatedAt.After(existing.UpdatedAt) {
			memoryAgentStates[agentID] = agentState
		}
		memoryStateMu.Unlock()
		processedCount++

		// Also persist to database if available
		if db != nil && workspaceID != "" {
			_, err := db.Exec(`
				INSERT INTO agent_sync_state 
					(org_id, id, name, role, status, avatar, current_task, last_seen, model, 
					 total_tokens, context_tokens, channel, session_key, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
				ON CONFLICT (org_id, id) DO UPDATE SET
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
				WHERE agent_sync_state.updated_at < EXCLUDED.updated_at
			`, workspaceID, agentID, name, role, status, avatar, currentTask, lastSeen,
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
			memoryStateMu.Lock()
			memoryAgentConfigs[agent.ID] = config
			memoryStateMu.Unlock()

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
	memoryStateMu.Lock()
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
	if payload.Config != nil {
		snapshot, err := normalizeOpenClawConfigSnapshot(*payload.Config, now, payload.Source)
		if err != nil {
			log.Printf("Failed to normalize OpenClaw config snapshot: %v", err)
		} else {
			memoryConfig = snapshot
			memoryConfigHistory = appendConfigHistorySnapshot(memoryConfigHistory, *snapshot)
			if db != nil {
				if err := persistOpenClawConfigSnapshot(r.Context(), db, *snapshot, now); err != nil {
					log.Printf("Failed to persist OpenClaw config snapshot metadata: %v", err)
				}
			}
		}
	}
	memoryStateMu.Unlock()
	if payload.Config != nil && db != nil && workspaceID != "" {
		report, err := importLegacyOpenClawWorkspaces(r.Context(), db, workspaceID, payload.Config.Data)
		if err != nil {
			log.Printf("Failed to import legacy OpenClaw workspaces: %v", err)
		} else if report != nil {
			if err := upsertSyncMetadataJSON(r.Context(), db, syncMetadataOpenClawLegacyImportKey, report, now); err != nil {
				log.Printf("Failed to persist legacy workspace import metadata: %v", err)
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

	if status, err := requireOpenClawSyncAuth(r.Context(), db, r); err != nil {
		sendJSON(w, status, errorResponse{Error: err.Error()})
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

	if status, err := requireOpenClawSyncAuth(r.Context(), db, r); err != nil {
		sendJSON(w, status, errorResponse{Error: err.Error()})
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

func requireOpenClawSyncAuth(ctx context.Context, db *sql.DB, r *http.Request) (int, error) {
	secret := resolveOpenClawSyncSecret()
	if secret == "" && db == nil {
		// No shared secret configured and no DB available for session validation.
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
	if secret != "" && subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1 {
		return http.StatusOK, nil
	}

	// Allow authenticated user session tokens as an alternative to the shared sync secret.
	// This is required for hosted onboarding flows where the bridge only has the user's session token.
	if db == nil {
		return http.StatusUnauthorized, fmt.Errorf("invalid authentication")
	}

	workspaceID, err := resolveOpenClawSyncWorkspaceID(r)
	if err != nil {
		return http.StatusBadRequest, err
	}
	if workspaceID != "" {
		ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, workspaceID)
	}
	if _, err := requireSessionIdentity(ctx, db, r); err != nil {
		switch {
		case errors.Is(err, errMissingAuthentication),
			errors.Is(err, errInvalidSessionToken),
			errors.Is(err, errAuthentication):
			return http.StatusUnauthorized, fmt.Errorf("invalid authentication")
		case errors.Is(err, errWorkspaceMismatch):
			return http.StatusForbidden, err
		default:
			return http.StatusUnauthorized, fmt.Errorf("invalid authentication")
		}
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

func resolveOpenClawSyncWorkspaceID(r *http.Request) (string, error) {
	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(r.Header.Get("X-Workspace-ID"))
	}
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(r.Header.Get("X-Org-ID"))
	}
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(r.URL.Query().Get("org_id"))
	}
	if workspaceID == "" {
		return "", nil
	}
	if !uuidRegex.MatchString(workspaceID) {
		return "", fmt.Errorf("workspace id must be a UUID")
	}
	return workspaceID, nil
}

func (h *OpenClawSyncHandler) resolveEmissionBroadcaster() emissionBroadcaster {
	if h.EmissionBroadcaster != nil {
		return h.EmissionBroadcaster
	}
	if h.Hub != nil {
		return h.Hub
	}
	return nil
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
	var lastSync *time.Time
	if db != nil {
		var lastSyncStr string
		err := db.QueryRow(`SELECT value FROM sync_metadata WHERE key = 'last_sync'`).Scan(&lastSyncStr)
		if err == nil {
			if parsed, parseErr := time.Parse(time.RFC3339, lastSyncStr); parseErr == nil {
				parsedUTC := parsed.UTC()
				lastSync = &parsedUTC
			}
		}
	}
	// Fall back to memory if DB didn't have data
	if lastSync == nil {
		lastSync = memoryLastSyncSnapshot()
	}
	freshness := deriveBridgeFreshness(lastSync, time.Now().UTC(), false, false)

	response := map[string]interface{}{
		"agents":        agents,
		"total":         len(agents),
		"sync_healthy":  freshness.SyncHealthy,
		"bridge_status": string(freshness.Status),
	}
	if lastSync != nil {
		response["last_sync"] = lastSync
	}
	if freshness.LastSyncAgeSeconds != nil {
		response["last_sync_age_seconds"] = *freshness.LastSyncAgeSeconds
	}

	sendJSON(w, http.StatusOK, response)
}

func (h *OpenClawSyncHandler) getAgentsFromDB(db *sql.DB) ([]AgentState, error) {
	// If no database, return from in-memory store
	if db == nil {
		return memoryAgentStatesSnapshot(), nil
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
			a.CurrentTask = normalizeCurrentTask(currentTask.String)
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
		a.Status = deriveAgentStatus(a.UpdatedAt, a.ContextTokens)

		agents = append(agents, a)
	}

	return agents, rows.Err()
}

func memoryLastSyncSnapshot() *time.Time {
	memoryStateMu.RLock()
	defer memoryStateMu.RUnlock()
	if memoryLastSync.IsZero() {
		return nil
	}
	last := memoryLastSync.UTC()
	return &last
}

func memoryAgentStatesSnapshot() []AgentState {
	memoryStateMu.RLock()
	defer memoryStateMu.RUnlock()
	agents := make([]AgentState, 0, len(memoryAgentStates))
	for _, state := range memoryAgentStates {
		if state == nil {
			continue
		}
		agents = append(agents, *state)
	}
	return agents
}

func memoryHostDiagSnapshot() *OpenClawHostDiagnostics {
	memoryStateMu.RLock()
	defer memoryStateMu.RUnlock()
	if memoryHostDiag == nil {
		return nil
	}
	host := *memoryHostDiag
	return &host
}

func memoryBridgeDiagSnapshot() *OpenClawBridgeDiagnostics {
	memoryStateMu.RLock()
	defer memoryStateMu.RUnlock()
	if memoryBridgeDiag == nil {
		return nil
	}
	bridge := *memoryBridgeDiag
	return &bridge
}

func memoryCronJobsSnapshot() []OpenClawCronJobDiagnostics {
	memoryStateMu.RLock()
	defer memoryStateMu.RUnlock()
	if len(memoryCronJobs) == 0 {
		return []OpenClawCronJobDiagnostics{}
	}
	return append([]OpenClawCronJobDiagnostics(nil), memoryCronJobs...)
}

func memoryProcessesSnapshot() []OpenClawProcessDiagnostics {
	memoryStateMu.RLock()
	defer memoryStateMu.RUnlock()
	if len(memoryProcesses) == 0 {
		return []OpenClawProcessDiagnostics{}
	}
	return append([]OpenClawProcessDiagnostics(nil), memoryProcesses...)
}

func memoryConfigSnapshot() *openClawConfigSnapshotRecord {
	memoryStateMu.RLock()
	defer memoryStateMu.RUnlock()
	if memoryConfig == nil {
		return nil
	}
	cloned := *memoryConfig
	cloned.Data = append(json.RawMessage(nil), memoryConfig.Data...)
	return &cloned
}

func memoryConfigHistorySnapshot() []openClawConfigSnapshotRecord {
	memoryStateMu.RLock()
	defer memoryStateMu.RUnlock()
	if len(memoryConfigHistory) == 0 {
		return nil
	}
	history := make([]openClawConfigSnapshotRecord, 0, len(memoryConfigHistory))
	for _, snapshot := range memoryConfigHistory {
		cloned := snapshot
		cloned.Data = append(json.RawMessage(nil), snapshot.Data...)
		history = append(history, cloned)
	}
	return history
}

func extractAgentID(sessionKey string) string {
	return ExtractSessionAgentIdentity(sessionKey)
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

func deriveAgentStatus(updatedAt time.Time, contextTokens int) string {
	if updatedAt.IsZero() {
		return "offline"
	}
	if isSessionStalled(contextTokens, updatedAt) {
		return "offline"
	}
	elapsed := time.Since(updatedAt)
	if elapsed < 2*time.Minute {
		return "online"
	}
	if elapsed < 30*time.Minute {
		return "busy"
	}
	return "offline"
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

func normalizeOpenClawConfigSnapshot(
	snapshot OpenClawConfigSnapshot,
	now time.Time,
	fallbackSource string,
) (*openClawConfigSnapshotRecord, error) {
	dataBytes, err := canonicalizeOpenClawConfigData(snapshot.Data)
	if err != nil {
		return nil, err
	}

	hash := sha256.Sum256(dataBytes)
	source := strings.TrimSpace(snapshot.Source)
	if source == "" {
		source = strings.TrimSpace(fallbackSource)
	}
	if source == "" {
		source = "bridge"
	}
	capturedAt := snapshot.CapturedAt.UTC()
	if capturedAt.IsZero() {
		capturedAt = now.UTC()
	}

	return &openClawConfigSnapshotRecord{
		Hash:       hex.EncodeToString(hash[:]),
		Source:     source,
		Path:       strings.TrimSpace(snapshot.Path),
		CapturedAt: capturedAt,
		Data:       append(json.RawMessage(nil), dataBytes...),
	}, nil
}

func canonicalizeOpenClawConfigData(data interface{}) (json.RawMessage, error) {
	if data == nil {
		return json.RawMessage("null"), nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var normalized interface{}
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(canonical), nil
}

func appendConfigHistorySnapshot(
	history []openClawConfigSnapshotRecord,
	snapshot openClawConfigSnapshotRecord,
) []openClawConfigSnapshotRecord {
	out := make([]openClawConfigSnapshotRecord, len(history))
	copy(out, history)
	if len(out) > 0 && out[len(out)-1].Hash == snapshot.Hash {
		return out
	}
	out = append(out, snapshot)
	if len(out) > maxOpenClawConfigHistoryEntries {
		out = out[len(out)-maxOpenClawConfigHistoryEntries:]
	}
	return out
}

func loadOpenClawConfigHistory(ctx context.Context, db *sql.DB) ([]openClawConfigSnapshotRecord, error) {
	if db == nil {
		return nil, nil
	}
	var raw string
	err := db.QueryRowContext(
		ctx,
		`SELECT value FROM sync_metadata WHERE key = $1`,
		syncMetadataOpenClawConfigHistoryKey,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return []openClawConfigSnapshotRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return []openClawConfigSnapshotRecord{}, nil
	}
	var history []openClawConfigSnapshotRecord
	if err := json.Unmarshal([]byte(raw), &history); err != nil {
		return nil, err
	}
	return history, nil
}

func persistOpenClawConfigSnapshot(
	ctx context.Context,
	db *sql.DB,
	snapshot openClawConfigSnapshotRecord,
	now time.Time,
) error {
	if db == nil {
		return nil
	}
	if err := upsertSyncMetadataJSON(ctx, db, syncMetadataOpenClawConfigSnapshotKey, snapshot, now); err != nil {
		return err
	}
	history, err := loadOpenClawConfigHistory(ctx, db)
	if err != nil {
		return err
	}
	history = appendConfigHistorySnapshot(history, snapshot)
	return upsertSyncMetadataJSON(ctx, db, syncMetadataOpenClawConfigHistoryKey, history, now)
}

func importLegacyOpenClawWorkspaces(
	ctx context.Context,
	db *sql.DB,
	workspaceID string,
	configData interface{},
) (*openClawLegacyImportReport, error) {
	if db == nil {
		return nil, nil
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, nil
	}

	descriptors := extractOpenClawConfigWorkspaceDescriptors(configData)
	if len(descriptors) == 0 {
		return nil, nil
	}

	report := &openClawLegacyImportReport{}
	primaryIdx := resolvePrimaryWorkspaceDescriptorIndex(descriptors)
	retiredWorkspacePaths := make([]string, 0, len(descriptors))

	tx, err := store.WithWorkspaceIDTx(ctx, db, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	for idx, descriptor := range descriptors {
		report.ProcessedWorkspaceCount++
		workspacePathRaw := strings.TrimSpace(descriptor.Workspace)
		if workspacePathRaw == "" {
			report.SkippedWorkspaceCount++
			continue
		}
		workspacePath, pathErr := sanitizeLegacyWorkspacePath(workspacePathRaw)
		if pathErr != nil {
			report.SkippedWorkspaceCount++
			report.WorkspaceWarnings = append(report.WorkspaceWarnings, fmt.Sprintf("%s: %v", workspacePathRaw, pathErr))
			continue
		}

		files, readErr := loadLegacyWorkspaceFiles(workspacePath)
		if readErr != nil {
			report.SkippedWorkspaceCount++
			report.WorkspaceWarnings = append(report.WorkspaceWarnings, fmt.Sprintf("%s: %v", workspacePath, readErr))
			continue
		}

		agentID, upsertErr := upsertLegacyWorkspaceAgent(ctx, tx, workspaceID, descriptor, files)
		if upsertErr != nil {
			report.SkippedWorkspaceCount++
			report.WorkspaceWarnings = append(report.WorkspaceWarnings, fmt.Sprintf("%s: %v", workspacePath, upsertErr))
			continue
		}
		report.ImportedAgents++

		longTermCount, dailyCount, memoryErr := upsertLegacyWorkspaceMemories(ctx, tx, workspaceID, agentID, files)
		if memoryErr != nil {
			report.WorkspaceWarnings = append(report.WorkspaceWarnings, fmt.Sprintf("%s: %v", workspacePath, memoryErr))
		}
		report.ImportedLongTermMemories += longTermCount
		report.ImportedDailyMemories += dailyCount

		isRetired := idx != primaryIdx && !strings.EqualFold(strings.TrimSpace(descriptor.ID), "chameleon")
		if isRetired {
			report.ProcessedRetiredWorkspaces++
			retiredWorkspacePaths = append(retiredWorkspacePaths, workspacePath)
		}
	}

	if err := commitLegacyWorkspaceTx(tx); err != nil {
		return nil, err
	}

	for _, workspacePath := range retiredWorkspacePaths {
		if transitionErr := ensureLegacyTransitionFiles(workspacePath); transitionErr != nil {
			report.WorkspaceWarnings = append(report.WorkspaceWarnings, fmt.Sprintf("%s: %v", workspacePath, transitionErr))
		} else {
			report.TransitionFilesGenerated++
		}
	}

	return report, nil
}

func extractOpenClawConfigWorkspaceDescriptors(configData interface{}) []openClawLegacyWorkspaceDescriptor {
	rootMap, ok := configData.(map[string]interface{})
	if !ok || rootMap == nil {
		return nil
	}

	agentsNode, ok := rootMap["agents"]
	if !ok {
		return nil
	}
	descriptors := parseWorkspaceDescriptorsFromAgentsNode(agentsNode)
	if len(descriptors) == 0 {
		return nil
	}
	return descriptors
}

func parseWorkspaceDescriptorsFromAgentsNode(node interface{}) []openClawLegacyWorkspaceDescriptor {
	switch typed := node.(type) {
	case []interface{}:
		return parseWorkspaceDescriptorsFromAgentList(typed)
	case map[string]interface{}:
		if listNode, ok := typed["list"]; ok {
			if entries, ok := listNode.([]interface{}); ok {
				return parseWorkspaceDescriptorsFromAgentList(entries)
			}
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make([]openClawLegacyWorkspaceDescriptor, 0, len(keys))
		for _, key := range keys {
			entryMap, ok := typed[key].(map[string]interface{})
			if !ok {
				continue
			}
			descriptor, ok := parseWorkspaceDescriptor(entryMap, key)
			if !ok {
				continue
			}
			out = append(out, descriptor)
		}
		return out
	default:
		return nil
	}
}

func parseWorkspaceDescriptorsFromAgentList(list []interface{}) []openClawLegacyWorkspaceDescriptor {
	out := make([]openClawLegacyWorkspaceDescriptor, 0, len(list))
	for _, item := range list {
		entryMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		descriptor, ok := parseWorkspaceDescriptor(entryMap, "")
		if !ok {
			continue
		}
		out = append(out, descriptor)
	}
	return out
}

func parseWorkspaceDescriptor(entry map[string]interface{}, fallbackID string) (openClawLegacyWorkspaceDescriptor, bool) {
	id := strings.TrimSpace(toString(entry["id"]))
	if id == "" {
		id = strings.TrimSpace(fallbackID)
	}
	workspace := strings.TrimSpace(toString(entry["workspace"]))
	if workspace == "" {
		workspace = strings.TrimSpace(toString(entry["workspace_path"]))
	}
	if workspace == "" {
		return openClawLegacyWorkspaceDescriptor{}, false
	}

	name := strings.TrimSpace(toString(entry["name"]))
	isDefault, _ := entry["default"].(bool)
	return openClawLegacyWorkspaceDescriptor{
		ID:        id,
		Name:      name,
		Workspace: workspace,
		IsDefault: isDefault,
	}, true
}

func resolvePrimaryWorkspaceDescriptorIndex(descriptors []openClawLegacyWorkspaceDescriptor) int {
	for idx, descriptor := range descriptors {
		if descriptor.IsDefault {
			return idx
		}
	}
	if len(descriptors) == 0 {
		return -1
	}
	return 0
}

func loadLegacyWorkspaceFiles(workspacePath string) (openClawLegacyWorkspaceFiles, error) {
	workspacePath, err := sanitizeLegacyWorkspacePath(workspacePath)
	if err != nil {
		return openClawLegacyWorkspaceFiles{}, err
	}
	info, err := os.Stat(workspacePath)
	if err != nil {
		return openClawLegacyWorkspaceFiles{}, err
	}
	if !info.IsDir() {
		return openClawLegacyWorkspaceFiles{}, fmt.Errorf("workspace path is not a directory")
	}

	soul, err := readOptionalWorkspaceFile(filepath.Join(workspacePath, "SOUL.md"))
	if err != nil {
		return openClawLegacyWorkspaceFiles{}, err
	}
	identity, err := readOptionalWorkspaceFile(filepath.Join(workspacePath, "IDENTITY.md"))
	if err != nil {
		return openClawLegacyWorkspaceFiles{}, err
	}
	tools, err := readOptionalWorkspaceFile(filepath.Join(workspacePath, "TOOLS.md"))
	if err != nil {
		return openClawLegacyWorkspaceFiles{}, err
	}
	agents, err := readOptionalWorkspaceFile(filepath.Join(workspacePath, "AGENTS.md"))
	if err != nil {
		return openClawLegacyWorkspaceFiles{}, err
	}
	longTerm, err := readOptionalWorkspaceFile(filepath.Join(workspacePath, "MEMORY.md"))
	if err != nil {
		return openClawLegacyWorkspaceFiles{}, err
	}
	daily, err := readLegacyWorkspaceDailyMemories(filepath.Join(workspacePath, "memory"))
	if err != nil {
		return openClawLegacyWorkspaceFiles{}, err
	}

	return openClawLegacyWorkspaceFiles{
		Soul:      soul,
		Identity:  identity,
		Tools:     tools,
		Agents:    agents,
		LongTerm:  longTerm,
		DailyByID: daily,
	}, nil
}

func sanitizeLegacyWorkspacePath(workspacePath string) (string, error) {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return "", fmt.Errorf("workspace path is empty")
	}
	if containsParentTraversalSegment(workspacePath) {
		return "", fmt.Errorf("workspace path must not contain '..' traversal segments")
	}

	cleaned := filepath.Clean(workspacePath)
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absPath)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("workspace path must not be a symlink")
	}

	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", err
	}
	return resolvedPath, nil
}

func containsParentTraversalSegment(workspacePath string) bool {
	normalized := strings.ReplaceAll(workspacePath, `\`, `/`)
	for _, segment := range strings.Split(normalized, "/") {
		if strings.TrimSpace(segment) == ".." {
			return true
		}
	}
	return false
}

func readOptionalWorkspaceFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readLegacyWorkspaceDailyMemories(memoryDir string) (map[string]string, error) {
	entries, err := os.ReadDir(memoryDir)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}

	out := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		datePart := strings.TrimSuffix(name, ".md")
		if _, err := time.Parse("2006-01-02", datePart); err != nil {
			continue
		}
		content, err := readOptionalWorkspaceFile(filepath.Join(memoryDir, name))
		if err != nil {
			return nil, err
		}
		if content == "" {
			continue
		}
		out[datePart] = content
	}
	return out, nil
}

func upsertLegacyWorkspaceAgent(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	descriptor openClawLegacyWorkspaceDescriptor,
	files openClawLegacyWorkspaceFiles,
) (string, error) {
	slug := normalizeLegacyWorkspaceSlug(descriptor.ID)
	if slug == "" {
		base := strings.TrimPrefix(filepath.Base(strings.TrimSpace(descriptor.Workspace)), "workspace-")
		slug = normalizeLegacyWorkspaceSlug(base)
	}
	if slug == "" {
		return "", fmt.Errorf("unable to resolve agent slug from workspace")
	}

	displayName := strings.TrimSpace(descriptor.Name)
	if displayName == "" {
		displayName = humanizeLegacyWorkspaceSlug(slug)
	}
	instructions := buildLegacyWorkspaceInstructions(files.Agents, files.Tools)

	var agentID string
	err := tx.QueryRowContext(
		ctx,
		`INSERT INTO agents (
			org_id, slug, display_name, status, soul_md, identity_md, instructions_md
		) VALUES (
			$1, $2, $3, 'active', $4, $5, $6
		)
		ON CONFLICT (org_id, slug) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			status = 'active',
			soul_md = COALESCE(EXCLUDED.soul_md, agents.soul_md),
			identity_md = COALESCE(EXCLUDED.identity_md, agents.identity_md),
			instructions_md = COALESCE(EXCLUDED.instructions_md, agents.instructions_md),
			updated_at = NOW()
		RETURNING id::text`,
		workspaceID,
		slug,
		displayName,
		nullableImportText(files.Soul),
		nullableImportText(files.Identity),
		nullableImportText(instructions),
	).Scan(&agentID)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(agentID), nil
}

func upsertLegacyWorkspaceMemories(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	agentID string,
	files openClawLegacyWorkspaceFiles,
) (int, int, error) {
	longTermCount := 0
	dailyCount := 0

	if strings.TrimSpace(files.LongTerm) != "" {
		if err := upsertLegacyWorkspaceMemory(ctx, tx, workspaceID, agentID, "long_term", nil, files.LongTerm); err != nil {
			return longTermCount, dailyCount, err
		}
		longTermCount = 1
	}

	dates := make([]string, 0, len(files.DailyByID))
	for day := range files.DailyByID {
		dates = append(dates, day)
	}
	sort.Strings(dates)
	for _, day := range dates {
		content := strings.TrimSpace(files.DailyByID[day])
		if content == "" {
			continue
		}
		dateValue, err := time.Parse("2006-01-02", day)
		if err != nil {
			continue
		}
		if err := upsertLegacyWorkspaceMemory(ctx, tx, workspaceID, agentID, "daily", &dateValue, content); err != nil {
			return longTermCount, dailyCount, err
		}
		dailyCount++
	}

	return longTermCount, dailyCount, nil
}

func upsertLegacyWorkspaceMemory(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	agentID string,
	kind string,
	dateValue *time.Time,
	content string,
) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}

	var (
		updateQuery string
		updateArgs  []interface{}
		insertArgs  []interface{}
	)
	if dateValue == nil {
		updateQuery = `
			UPDATE agent_memories
			SET content = $3, updated_at = NOW()
			WHERE org_id = $1
			  AND agent_id = $2
			  AND kind = $4
			  AND date IS NULL`
		updateArgs = []interface{}{workspaceID, agentID, content, kind}
		insertArgs = []interface{}{workspaceID, agentID, kind, content}
	} else {
		dateUTC := dateValue.UTC().Format("2006-01-02")
		updateQuery = `
			UPDATE agent_memories
			SET content = $3, updated_at = NOW()
			WHERE org_id = $1
			  AND agent_id = $2
			  AND kind = $4
			  AND date = $5`
		updateArgs = []interface{}{workspaceID, agentID, content, kind, dateUTC}
		insertArgs = []interface{}{workspaceID, agentID, kind, dateUTC, content}
	}

	result, err := tx.ExecContext(ctx, updateQuery, updateArgs...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows > 0 {
		return nil
	}

	if dateValue == nil {
		_, err = tx.ExecContext(
			ctx,
			`INSERT INTO agent_memories (org_id, agent_id, kind, content)
			 VALUES ($1, $2, $3, $4)`,
			insertArgs...,
		)
		return err
	}

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO agent_memories (org_id, agent_id, kind, date, content)
		 VALUES ($1, $2, $3, $4, $5)`,
		insertArgs...,
	)
	return err
}

func ensureLegacyTransitionFiles(workspacePath string) error {
	workspacePath, err := sanitizeLegacyWorkspacePath(workspacePath)
	if err != nil {
		return err
	}
	info, err := os.Stat(workspacePath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace path is not a directory")
	}

	transitionPath := filepath.Join(workspacePath, legacyTransitionFilename)
	if _, err := os.Stat(transitionPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if writeErr := writeFileAtomically(transitionPath, []byte(strings.TrimSpace(legacyTransitionChecklistTemplate)+"\n"), 0o644); writeErr != nil {
			return writeErr
		}
	}

	agentsPath := filepath.Join(workspacePath, "AGENTS.md")
	agentsBody, err := os.ReadFile(agentsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	current := string(agentsBody)
	if strings.Contains(current, legacyTransitionMarker) {
		return nil
	}

	var merged string
	current = strings.TrimSpace(current)
	if current == "" {
		merged = legacyTransitionAgentsPointer
	} else {
		merged = strings.TrimSpace(legacyTransitionAgentsPointer) + "\n\n" + current + "\n"
	}
	return writeFileAtomically(agentsPath, []byte(merged), 0o644)
}

func writeFileAtomically(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tempFile, err := createTempFileForAtomicWrite(dir, "."+base+".tmp-*")
	if err != nil {
		return err
	}

	tempPath := tempFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = removeFileForAtomicWrite(tempPath)
		}
	}()

	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Chmod(perm); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := renameFileForAtomicWrite(tempPath, path); err != nil {
		return err
	}

	cleanup = false
	return nil
}

func buildLegacyWorkspaceInstructions(agentsContent, toolsContent string) string {
	parts := make([]string, 0, 2)
	if trimmed := strings.TrimSpace(agentsContent); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if trimmed := strings.TrimSpace(toolsContent); trimmed != "" {
		parts = append(parts, trimmed)
	}
	return strings.Join(parts, "\n\n---\n\n")
}

func nullableImportText(value string) interface{} {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func normalizeLegacyWorkspaceSlug(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	var builder strings.Builder
	lastDash := false
	for _, ch := range raw {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			builder.WriteRune(ch)
			lastDash = false
			continue
		}
		if lastDash {
			continue
		}
		builder.WriteRune('-')
		lastDash = true
	}
	slug := strings.Trim(builder.String(), "-")
	return slug
}

func humanizeLegacyWorkspaceSlug(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return "Agent"
	}
	parts := strings.Split(slug, "-")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if len(part) == 1 {
			out = append(out, strings.ToUpper(part))
			continue
		}
		out = append(out, strings.ToUpper(part[:1])+part[1:])
	}
	if len(out) == 0 {
		return "Agent"
	}
	return strings.Join(out, " ")
}

func toString(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
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
	pruneProgressLogEmissionSeen(now)
	return true
}

func pruneProgressLogEmissionSeen(now time.Time) {
	if len(progressLogEmissionSeen) <= progressLogEmissionSeenSoftThreshold {
		return
	}

	cutoff := now.Add(-24 * time.Hour)
	for key, seenAt := range progressLogEmissionSeen {
		if seenAt.Before(cutoff) {
			delete(progressLogEmissionSeen, key)
		}
	}

	if len(progressLogEmissionSeen) <= progressLogEmissionSeenHardCap {
		return
	}

	type progressEntry struct {
		id     string
		seenAt time.Time
	}

	entries := make([]progressEntry, 0, len(progressLogEmissionSeen))
	for key, seenAt := range progressLogEmissionSeen {
		entries = append(entries, progressEntry{id: key, seenAt: seenAt})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].seenAt.Equal(entries[j].seenAt) {
			return entries[i].id < entries[j].id
		}
		return entries[i].seenAt.Before(entries[j].seenAt)
	})

	evictCount := len(entries) / progressLogEmissionSeenOldestDivisor
	if evictCount < 1 {
		evictCount = 1
	}
	for i := 0; i < evictCount && i < len(entries); i++ {
		delete(progressLogEmissionSeen, entries[i].id)
	}
}

func resetProgressLogEmissionSeen() {
	progressLogEmissionMu.Lock()
	defer progressLogEmissionMu.Unlock()
	progressLogEmissionSeen = make(map[string]time.Time)
}
