package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Workflow represents a recurring automation workflow.
type Workflow struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Trigger     WorkflowTrigger `json:"trigger"`
	Steps       []WorkflowStep  `json:"steps,omitempty"`
	Status      string          `json:"status"`
	Enabled     bool            `json:"enabled"`
	LastRun     *time.Time      `json:"last_run,omitempty"`
	NextRun     *time.Time      `json:"next_run,omitempty"`
	LastStatus  string          `json:"last_status,omitempty"`
	AgentID     string          `json:"agent_id,omitempty"`
	AgentName   string          `json:"agent_name,omitempty"`
	Source      string          `json:"source"` // "cron", "heartbeat"
}

// WorkflowTrigger describes how a workflow starts.
type WorkflowTrigger struct {
	Type  string `json:"type"`            // cron, interval, event, manual
	Every string `json:"every,omitempty"` // e.g. 5m, 15m
	Cron  string `json:"cron,omitempty"`  // cron expression
	Event string `json:"event,omitempty"` // event name
	Label string `json:"label,omitempty"` // human-readable label
}

// WorkflowStep describes a step in a workflow.
type WorkflowStep struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
}

// WorkflowsHandler handles workflow-related requests.
type WorkflowsHandler struct {
	DB              *sql.DB
	ConnectionsHandler *AdminConnectionsHandler
}

// List returns all workflows derived from OpenClaw cron jobs and agent heartbeats.
func (h *WorkflowsHandler) List(w http.ResponseWriter, r *http.Request) {
	workflows := h.buildWorkflows(r.Context())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(workflows)
}

// Toggle enables or disables a workflow.
func (h *WorkflowsHandler) Toggle(w http.ResponseWriter, r *http.Request) {
	// Forward to cron toggle if available
	if h.ConnectionsHandler != nil {
		h.ConnectionsHandler.ToggleCronJob(w, r)
		return
	}
	sendJSON(w, http.StatusNotImplemented, errorResponse{Error: "toggle not available"})
}

// Run triggers a workflow immediately.
func (h *WorkflowsHandler) Run(w http.ResponseWriter, r *http.Request) {
	if h.ConnectionsHandler != nil {
		h.ConnectionsHandler.RunCronJob(w, r)
		return
	}
	sendJSON(w, http.StatusNotImplemented, errorResponse{Error: "run not available"})
}

func (h *WorkflowsHandler) buildWorkflows(ctx context.Context) []Workflow {
	var workflows []Workflow

	// 1. Build workflows from cron jobs (primary source)
	cronJobs := h.loadCronJobs(ctx)
	for _, job := range cronJobs {
		name := job.Name
		if name == "" {
			name = job.ID
		}

		trigger := parseCronTrigger(job)
		status := "active"
		if !job.Enabled {
			status = "paused"
		}

		// Derive agent name from the cron job context
		agentID := deriveAgentFromCronName(name, job.SessionTarget)
		agentDisplayName := ""
		if agentID != "" {
			if n, ok := agentNames[agentID]; ok {
				agentDisplayName = n
			}
		}

		description := describeCronJob(name, job.PayloadType, job.SessionTarget)

		workflows = append(workflows, Workflow{
			ID:          job.ID,
			Name:        humanizeCronName(name),
			Description: description,
			Trigger:     trigger,
			Status:      status,
			Enabled:     job.Enabled,
			LastRun:     job.LastRunAt,
			NextRun:     job.NextRunAt,
			LastStatus:  job.LastStatus,
			AgentID:     agentID,
			AgentName:   agentDisplayName,
			Source:      "cron",
		})
	}

	sort.Slice(workflows, func(i, j int) bool {
		// Active before paused, then alphabetical
		if workflows[i].Status != workflows[j].Status {
			return workflows[i].Status < workflows[j].Status // "active" < "paused"
		}
		return strings.ToLower(workflows[i].Name) < strings.ToLower(workflows[j].Name)
	})

	return workflows
}

func (h *WorkflowsHandler) loadCronJobs(ctx context.Context) []OpenClawCronJobDiagnostics {
	// Try loading from the connections handler first (reuse existing logic)
	if h.ConnectionsHandler != nil {
		return h.ConnectionsHandler.loadCronJobs(ctx)
	}

	// Fallback: read from DB directly
	if h.DB != nil {
		var value string
		if err := h.DB.QueryRow(`SELECT value FROM sync_metadata WHERE key = 'openclaw_cron_jobs'`).Scan(&value); err == nil && value != "" {
			var payload []OpenClawCronJobDiagnostics
			if err := json.Unmarshal([]byte(value), &payload); err == nil {
				return payload
			}
		}
	}

	// Fallback: memory
	if len(memoryCronJobs) > 0 {
		return append([]OpenClawCronJobDiagnostics(nil), memoryCronJobs...)
	}

	return nil
}

func parseCronTrigger(job OpenClawCronJobDiagnostics) WorkflowTrigger {
	schedule := job.Schedule
	if schedule == "" {
		return WorkflowTrigger{Type: "manual", Label: "Manual"}
	}

	// Check if it looks like a duration (e.g., "5m", "15m", "1h")
	if len(schedule) >= 2 {
		lastChar := schedule[len(schedule)-1]
		if lastChar == 'm' || lastChar == 'h' || lastChar == 's' {
			return WorkflowTrigger{
				Type:  "interval",
				Every: schedule,
				Label: "Every " + schedule,
			}
		}
	}

	// Strip timezone suffix if present: "0 21 * * * (America/Denver)" → "0 21 * * *"
	cronExpr := schedule
	if idx := strings.Index(schedule, " ("); idx > 0 {
		cronExpr = strings.TrimSpace(schedule[:idx])
	}

	return WorkflowTrigger{
		Type:  "cron",
		Cron:  cronExpr,
		Label: humanizeCronSchedule(cronExpr),
	}
}

func humanizeCronSchedule(expr string) string {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return "Cron: " + expr
	}
	min, hour := parts[0], parts[1]

	// Common patterns
	if min == "*/5" && hour == "*" {
		return "Every 5 minutes"
	}
	if min == "*/15" && hour == "*" {
		return "Every 15 minutes"
	}
	if min == "*/30" && hour == "*" {
		return "Every 30 minutes"
	}
	if min == "0" && hour == "*" {
		return "Every hour"
	}
	if min == "0" {
		return "Daily at " + hour + ":00"
	}
	if hour != "*" {
		return "Daily at " + hour + ":" + padMinute(min)
	}
	return "Cron: " + expr
}

func padMinute(m string) string {
	if len(m) == 1 {
		return "0" + m
	}
	return m
}

func humanizeCronName(name string) string {
	// Turn kebab-case and snake_case into title case
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	words := strings.Fields(name)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

func describeCronJob(name, payloadType, sessionTarget string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "heartbeat"):
		return "Periodic health check and task polling"
	case strings.Contains(lower, "memory") && strings.Contains(lower, "extract"):
		return "Extracts and persists agent memories from session transcripts"
	case strings.Contains(lower, "morning") && strings.Contains(lower, "briefing"):
		return "Daily morning status summary delivered to Sam"
	case strings.Contains(lower, "evening") && strings.Contains(lower, "summary"):
		return "Daily evening wrap-up and next-day priorities"
	case strings.Contains(lower, "health") && strings.Contains(lower, "sweep"):
		return "System health monitoring and diagnostics"
	case strings.Contains(lower, "codex") && strings.Contains(lower, "progress"):
		return "Summarizes Codex implementation pipeline progress"
	case strings.Contains(lower, "github") && strings.Contains(lower, "dispatch"):
		return "Routes GitHub notifications to the right agents"
	case strings.Contains(lower, "junk") && strings.Contains(lower, "mail"):
		return "Filters and summarizes low-priority email"
	case strings.Contains(lower, "temple") && strings.Contains(lower, "ticket"):
		return "Monitors Salt Lake Temple open house ticket availability"
	default:
		if sessionTarget == "isolated" {
			return "Runs in isolated session"
		}
		if payloadType == "systemEvent" {
			return "System event injection"
		}
		return "Recurring automation"
	}
}

func deriveAgentFromCronName(name, sessionTarget string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "heartbeat"):
		return "main"
	case strings.Contains(lower, "memory"):
		return "main"
	case strings.Contains(lower, "morning") || strings.Contains(lower, "evening"):
		return "main"
	case strings.Contains(lower, "health"):
		return "main"
	case strings.Contains(lower, "codex"):
		return "main"
	case strings.Contains(lower, "github"):
		return "main"
	case strings.Contains(lower, "junk") || strings.Contains(lower, "mail"):
		return "email-mgmt"
	case strings.Contains(lower, "temple"):
		return "main"
	default:
		return "main"
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

// OpenClawAgentConfig is defined in openclaw_sync.go
