package tui

import (
	"context"
	"strconv"
	"strings"
	"time"
)

const (
	defaultColdOpenDuration         = 1100 * time.Millisecond
	defaultTourDuration             = 2 * time.Minute
	defaultMemorySteadyStateBoundMB = 128
)

// InboxSummaryItem is a single unacted inbox item for display in the inbox view.
type InboxSummaryItem struct {
	ID      string
	TaskID  string
	Summary string
}

// SidebarChatItem represents a recent chat session for the sidebar.
type SidebarChatItem struct {
	SessionID   string
	DisplayName string
	UpdatedAt   time.Time
	ScopeType   string // "organization", "project", "project_task"
	ScopeID     string // task ID for project_task, project ID for project scope
	WorkStatus  string // task work_status for project_task sessions (todo, in_progress, done, etc.)
}

// SidebarProjectItem represents a project for the sidebar.
type SidebarProjectItem struct {
	ID          string
	DisplayName string
	UpdatedAt   time.Time
}

// SidebarTaskItem represents an open task under a project in the sidebar.
type SidebarTaskItem struct {
	ID         string
	Title      string
	WorkStatus string
	TaskNumber int
}

// FlowStep represents a single step in a task's flow pipeline.
type FlowStep struct {
	Name     string // display_name of the flow node
	NodeType string // "work" or "review"
	Status   string // "completed", "active", "pending"
}

// SubtaskItem represents a subtask within a task.
type SubtaskItem struct {
	Title  string
	Status string // "pending", "in_progress", "done"
}

// TaskDependency represents a dependency relationship between tasks.
type TaskDependency struct {
	TaskID     string // UUID of the related task
	TaskTitle  string // resolved display title (e.g. "OC-5: Build landing page")
	Direction  string // "depends_on" or "blocks"
}

// TaskDetailItem is the full task record fetched on demand when the user selects a task.
type TaskDetailItem struct {
	ID                  string
	TaskNumber          int
	Title               string
	Description         string
	WorkStatus          string
	SessionID           string
	AgentName           string // display_name of the assigned agent, if any
	FlowNodeName        string // current flow node display_name, if any
	RequiresHumanReview bool   // whether this task requires human review
	BranchName          string // git branch name, if any
	FlowSteps           []FlowStep
	SubtaskItems        []SubtaskItem
	Dependencies        []TaskDependency
}

type RuntimeHints struct {
	ModifierReliabilityUncertain bool
	FirstRun                     bool
	Clock                        func() time.Time
	ColdOpenDuration             time.Duration
	TourDuration                 time.Duration
	MemorySteadyStateBoundBytes  uint64
	DisableMemorySampler         bool
	SendChatMessage              func(ctx context.Context, sessionID, content string) error
	CancelChatTurn               func(ctx context.Context, sessionID string) error
	LoadChatHistory              func(ctx context.Context, sessionID string) ([]ChatMessage, error)
	LoadOrgSession               func(ctx context.Context) (string, error)
	LoadInboxCount               func(ctx context.Context) (int, error)
	LoadRecentChats              func(ctx context.Context) ([]SidebarChatItem, error)
	LoadProjects                 func(ctx context.Context) ([]SidebarProjectItem, error)
	LoadProjectTasks             func(ctx context.Context, projectID string) ([]SidebarTaskItem, error)
	LoadProjectDetail            func(ctx context.Context, projectID string) (*ProjectDetail, error)
	LoadTaskDetail               func(ctx context.Context, taskID string) (*TaskDetailItem, error)
	LoadAgents                   func(ctx context.Context) ([]string, error) // returns "name=lifecycle_status" strings
	LoadInboxItems               func(ctx context.Context) ([]InboxSummaryItem, error)
	// ActOnInboxItem sends approve/reject/defer/dismiss for a specific inbox item ID.
	// EX-160: required so that keyboard shortcuts in the inbox view reach the server.
	ActOnInboxItem func(ctx context.Context, itemID, action string) error
}

func (h RuntimeHints) now() time.Time {
	if h.Clock != nil {
		return h.Clock().UTC()
	}
	return time.Now().UTC()
}

func (h RuntimeHints) coldOpenDuration() time.Duration {
	value := h.ColdOpenDuration
	if value <= 0 {
		value = defaultColdOpenDuration
	}
	if value > 1200*time.Millisecond {
		return 1200 * time.Millisecond
	}
	return value
}

func (h RuntimeHints) tourDuration() time.Duration {
	if h.TourDuration <= 0 {
		return defaultTourDuration
	}
	return h.TourDuration
}

func (h RuntimeHints) memoryBoundBytes() uint64 {
	if h.MemorySteadyStateBoundBytes > 0 {
		return h.MemorySteadyStateBoundBytes
	}
	return uint64(defaultMemorySteadyStateBoundMB) * 1024 * 1024
}

func DetectRuntimeHints(getenv func(string) string) RuntimeHints {
	tmux := strings.TrimSpace(getenv("TMUX")) != "" || strings.TrimSpace(getenv("STY")) != ""
	term := strings.ToLower(strings.TrimSpace(getenv("TERM")))
	if strings.Contains(term, "screen") || strings.Contains(term, "tmux") {
		tmux = true
	}

	memoryBound := uint64(defaultMemorySteadyStateBoundMB) * 1024 * 1024
	rawMemoryBound := strings.TrimSpace(getenv("OTTERCAMP_TUI_MEMORY_BOUND_MB"))
	if rawMemoryBound != "" {
		if parsed, err := strconv.ParseUint(rawMemoryBound, 10, 64); err == nil && parsed > 0 {
			memoryBound = parsed * 1024 * 1024
		}
	}

	return RuntimeHints{
		ModifierReliabilityUncertain: tmux,
		ColdOpenDuration:             defaultColdOpenDuration,
		TourDuration:                 defaultTourDuration,
		MemorySteadyStateBoundBytes:  memoryBound,
	}
}
