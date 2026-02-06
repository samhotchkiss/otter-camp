package api

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

// Workflow represents an ongoing automation workflow.
type Workflow struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Trigger WorkflowTrigger `json:"trigger"`
	Steps   []WorkflowStep  `json:"steps"`
	Status  string          `json:"status"`
	LastRun *time.Time      `json:"last_run,omitempty"`
}

// WorkflowTrigger describes how a workflow starts.
type WorkflowTrigger struct {
	Type  string `json:"type"`             // cron, event, manual
	Every string `json:"every,omitempty"`  // e.g. 5m
	Cron  string `json:"cron,omitempty"`   // cron expression
	Event string `json:"event,omitempty"`  // event name
	Label string `json:"label,omitempty"`  // human-readable label
}

// WorkflowStep describes a step in a workflow.
type WorkflowStep struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
}

// WorkflowsHandler handles workflow-related requests.
type WorkflowsHandler struct {
	DB *sql.DB
}

// List returns all workflows.
func (h *WorkflowsHandler) List(w http.ResponseWriter, r *http.Request) {
	demo := r.URL.Query().Get("demo") == "true"

	if demo {
		json.NewEncoder(w).Encode(getDemoWorkflows())
		return
	}

	db := h.DB
	if db == nil {
		if dbConn, err := store.DB(); err == nil {
			db = dbConn
		}
	}

	workflows, err := getOpenClawWorkflows(db)
	if err != nil {
		log.Printf("Failed to load workflows: %v", err)
		workflows = []Workflow{}
	}

	json.NewEncoder(w).Encode(workflows)
}

func getOpenClawWorkflows(db *sql.DB) ([]Workflow, error) {
	configs, err := getOpenClawAgentConfigs(db)
	if err != nil {
		return nil, err
	}

	workflows := make([]Workflow, 0, len(configs))
	for _, config := range configs {
		name := agentNames[config.ID]
		if name == "" {
			name = config.ID
		}

		trigger := WorkflowTrigger{Type: "manual"}
		if config.HeartbeatEvery != "" {
			trigger = WorkflowTrigger{
				Type:  "cron",
				Every: config.HeartbeatEvery,
				Label: "Every " + config.HeartbeatEvery,
			}
		}

		lastRun, agentStatus := lookupAgentLastRun(db, config.ID)
		status := "active"
		if agentStatus == "offline" {
			status = "paused"
		}

		steps := []WorkflowStep{
			{
				ID:   config.ID + "-run",
				Name: "Run agent workflow",
				Kind: "agent",
			},
		}

		workflows = append(workflows, Workflow{
			ID:      "openclaw-" + config.ID,
			Name:    name + " workflow",
			Trigger: trigger,
			Steps:   steps,
			Status:  status,
			LastRun: lastRun,
		})
	}

	sort.Slice(workflows, func(i, j int) bool {
		return workflows[i].Name < workflows[j].Name
	})

	return workflows, nil
}

func getOpenClawAgentConfigs(db *sql.DB) ([]OpenClawAgentConfig, error) {
	if db == nil {
		configs := make([]OpenClawAgentConfig, 0, len(memoryAgentConfigs))
		for _, config := range memoryAgentConfigs {
			configs = append(configs, *config)
		}
		return configs, nil
	}

	rows, err := db.Query(`
		SELECT id, heartbeat_every, updated_at
		FROM openclaw_agent_configs
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configs := []OpenClawAgentConfig{}
	for rows.Next() {
		var config OpenClawAgentConfig
		var heartbeat sql.NullString
		if err := rows.Scan(&config.ID, &heartbeat, &config.UpdatedAt); err != nil {
			continue
		}
		if heartbeat.Valid {
			config.HeartbeatEvery = heartbeat.String
		}
		configs = append(configs, config)
	}

	return configs, rows.Err()
}

func lookupAgentLastRun(db *sql.DB, agentID string) (*time.Time, string) {
	if db == nil {
		if state, ok := memoryAgentStates[agentID]; ok {
			lastRun := state.UpdatedAt
			return &lastRun, state.Status
		}
		return nil, ""
	}

	var updatedAt time.Time
	var status sql.NullString
	err := db.QueryRow(`
		SELECT updated_at, status
		FROM agent_sync_state
		WHERE id = $1
	`, agentID).Scan(&updatedAt, &status)
	if err != nil {
		return nil, ""
	}

	statusValue := ""
	if status.Valid {
		statusValue = status.String
	}

	return &updatedAt, statusValue
}

func getDemoWorkflows() []Workflow {
	now := time.Now()

	return []Workflow{
		{
			ID:   "demo-nova-twitter",
			Name: "Twitter Engagement",
			Trigger: WorkflowTrigger{
				Type:  "cron",
				Every: "5m",
				Label: "Every 5 minutes",
			},
			Steps: []WorkflowStep{
				{ID: "step-1", Name: "Scan mentions", Kind: "social"},
				{ID: "step-2", Name: "Draft response", Kind: "social"},
				{ID: "step-3", Name: "Publish update", Kind: "social"},
			},
			Status:  "active",
			LastRun: ptrTime(now.Add(-15 * time.Minute)),
		},
		{
			ID:   "demo-penny-email",
			Name: "Inbox Triage",
			Trigger: WorkflowTrigger{
				Type:  "event",
				Event: "New email received",
				Label: "On new email",
			},
			Steps: []WorkflowStep{
				{ID: "step-1", Name: "Classify sender", Kind: "email"},
				{ID: "step-2", Name: "Flag urgent threads", Kind: "email"},
			},
			Status:  "active",
			LastRun: ptrTime(now.Add(-45 * time.Minute)),
		},
		{
			ID:   "demo-stone-content",
			Name: "Content Review",
			Trigger: WorkflowTrigger{
				Type:  "manual",
				Label: "Manual",
			},
			Steps: []WorkflowStep{
				{ID: "step-1", Name: "Draft outline", Kind: "content"},
				{ID: "step-2", Name: "Write section", Kind: "content"},
			},
			Status:  "paused",
			LastRun: ptrTime(now.Add(-6 * time.Hour)),
		},
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
