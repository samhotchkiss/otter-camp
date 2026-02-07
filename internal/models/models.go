// Package models defines domain models for Otter Camp.
//
// Note: The primary model definitions are in the store package alongside their
// data access methods. This package provides shared types and interfaces.
package models

import (
	"encoding/json"
	"time"
)

// Organization represents a workspace/tenant.
type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Tier      string    `json:"tier"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Tag represents a label that can be applied to tasks.
type Tag struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
}

// Comment represents a comment on a task.
type Comment struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	AuthorID  *string   `json:"author_id,omitempty"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SearchResult represents a task search result with highlighting.
type SearchResult struct {
	ID                   string          `json:"id"`
	OrgID                string          `json:"org_id"`
	ProjectID            *string         `json:"project_id,omitempty"`
	Number               int32           `json:"number"`
	Title                string          `json:"title"`
	Description          *string         `json:"description,omitempty"`
	Status               string          `json:"status"`
	Priority             string          `json:"priority"`
	Context              json.RawMessage `json:"context"`
	AssignedAgentID      *string         `json:"assigned_agent_id,omitempty"`
	ParentTaskID         *string         `json:"parent_task_id,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
	Rank                 float64         `json:"rank"`
	TitleHighlight       string          `json:"title_highlight"`
	DescriptionHighlight string          `json:"description_highlight"`
}

// TaskStatus constants.
const (
	TaskStatusQueued     = "queued"
	TaskStatusDispatched = "dispatched"
	TaskStatusInProgress = "in_progress"
	TaskStatusBlocked    = "blocked"
	TaskStatusReview     = "review"
	TaskStatusDone       = "done"
	TaskStatusCancelled  = "cancelled"
)

// TaskPriority constants.
const (
	TaskPriorityP0 = "P0"
	TaskPriorityP1 = "P1"
	TaskPriorityP2 = "P2"
	TaskPriorityP3 = "P3"
)

// AgentStatus constants.
const (
	AgentStatusActive   = "active"
	AgentStatusInactive = "inactive"
	AgentStatusBusy     = "busy"
)

// ProjectStatus constants.
const (
	ProjectStatusActive   = "active"
	ProjectStatusArchived = "archived"
	ProjectStatusPaused   = "paused"
)

// OrganizationTier constants.
const (
	TierFree       = "free"
	TierPro        = "pro"
	TierEnterprise = "enterprise"
)
