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

// SettingsData holds the full settings snapshot for the settings dashboard.
type SettingsData struct {
	Profiles  []ModelProfileItem
	Providers []ProviderItem
	Secrets   []SecretItem
}

// ModelProfileItem represents a single model profile (high-capability, standard, haiku).
type ModelProfileItem struct {
	LogicalID    string
	ProviderID   string
	ProviderName string
	ModelName    string
}

// ProviderItem represents a model provider (Anthropic, OpenAI).
type ProviderItem struct {
	ID          string
	Slug        string
	DisplayName string
	IsEnabled   bool
	Connections []ConnectionItem
}

// ConnectionItem represents a provider connection with auth configuration.
type ConnectionItem struct {
	ID          string
	ProviderID  string
	DisplayName string
	AuthMode    string
	IsEnabled   bool
	Health      string
}

// SecretItem represents a stored secret reference.
type SecretItem struct {
	Slug        string
	DisplayName string
}

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
	Slug        string
	DisplayName string
	IsPaused    bool
	PauseReason string
	UpdatedAt   time.Time
}

// SidebarTaskItem represents an open task under a project in the sidebar.
type SidebarTaskItem struct {
	ID         string
	Title      string
	WorkStatus string
	TaskNumber int
	Priority   int
}

// FlowStep represents a single step in a task's flow pipeline.
type FlowStep struct {
	Name     string // display_name of the flow node
	NodeType string // "work" or "review"
	Status   string // "completed", "active", "pending"
}

type TaskSubtaskCounts struct {
	Total      int
	Done       int
	InProgress int
	Pending    int
	Blocked    int
	Cancelled  int
}

// SubtaskItem represents a subtask within a task.
type SubtaskItem struct {
	Title  string
	Status string // "pending", "in_progress", "done"
}

type TaskFlowExecution struct {
	ID            string
	FlowNodeID    string
	VisitNumber   int
	Status        string
	State         string
	SessionID     string
	StartedAt     time.Time
	CompletedAt   *time.Time
	SubtaskCounts TaskSubtaskCounts
	Subtasks      []SubtaskItem
}

type TaskFlowEdge struct {
	FromNodeID string
	ToNodeID   string
	Kind       string
	IsBackEdge bool
}

type TaskFlowNode struct {
	ID                string
	Name              string
	NodeType          string
	Position          int
	ActorType         string
	ActorLabel        string
	State             string
	IsCurrent         bool
	NextNodeID        string
	RejectNodeID      string
	VisitCount        int
	CompletedVisits   int
	RejectedVisits    int
	SessionID         string
	LatestExecutionID string
	SubtaskCounts     TaskSubtaskCounts
	Executions        []TaskFlowExecution
}

type TaskPlanningArtifact struct {
	Slug  string
	Title string
}

// TaskDependency represents a dependency relationship between tasks.
type TaskDependency struct {
	TaskID    string // UUID of the related task
	TaskTitle string // resolved display title (e.g. "OC-5: Build landing page")
	Direction string // "depends_on" or "blocks"
}

// TaskEvent represents a single timestamped event in a task's lifecycle.
type TaskEvent struct {
	EventType  string // "task.created", "status.changed", "task.review_rejected"
	ActorType  string // "human_user", "agent", "system", "supervisor"
	FlowNodeID string
	Payload    map[string]any // event-specific data (from_status, to_status, reason, etc.)
	CreatedAt  time.Time
}

// TaskDetailItem is the full task record fetched on demand when the user selects a task.
type TaskDetailItem struct {
	ID                       string
	ProjectID                string
	TaskNumber               int
	Title                    string
	Description              string
	WorkStatus               string
	Priority                 int
	SessionID                string // preferred execution session (active when present, otherwise recent)
	DiscussionSessionID      string
	ActiveExecutionSessionID        string
	RecentExecutionSessionID        string
	AgentName                string // display_name of the assigned agent, if any
	FlowNodeName             string // current flow node display_name, if any
	FlowCurrentNodeID        string
	FlowNodes                []TaskFlowNode
	FlowEdges                []TaskFlowEdge
	PlanningPlaybook         string
	PlanningWorkType         string
	PlanningProjectStage     string
	PlanningEvidenceMaturity string
	PlanningRiskLevel        string
	PlanningArtifacts        []TaskPlanningArtifact
	PlanningFollowOns        []string
	PlanningProcessStatus    string
	PlanningChecklist        []string
	PlanningMissing          []string
	PlanningOverrideReason   string
	RequiresHumanReview      bool // whether this task requires human review
	BlockedReason            string
	RecoveryHint             string
	BranchName               string // git branch name, if any
	FlowSteps                []FlowStep
	SubtaskItems             []SubtaskItem
	Dependencies             []TaskDependency
	Events                   []TaskEvent
}

type OperatorDashboardSummary struct {
	Health          string
	QuietHealthy    bool
	ActiveProjects  int
	ActiveTasks     int
	ActiveRuns      int
	StaleTasks      int
	StaleExecutions int
	BlockedItems    int
	RecentFailures  int
}

type OperatorDashboardThresholds struct {
	StaleExecutionSeconds int
	StaleTaskSeconds      int
}

type OperatorDashboardSection struct {
	Count      int
	TotalCount int
	Items      []OperatorDashboardItem
}

type OperatorDashboardRef struct {
	ID    string
	Label string
}

type OperatorDashboardTaskRef struct {
	ID         string
	TaskNumber int
	Label      string
}

type OperatorDashboardLinks struct {
	Project string
	Task    string
	Run     string
}

type OperatorDashboardItem struct {
	Shortcut        int
	Kind            string
	Title           string
	Summary         string
	Status          string
	Project         *OperatorDashboardRef
	Task            *OperatorDashboardTaskRef
	Run             *OperatorDashboardRef
	UpdatedAt       time.Time
	AgeSeconds      int
	StaleForSeconds int
	Links           OperatorDashboardLinks
}

type OperatorDashboardData struct {
	Summary        OperatorDashboardSummary
	Active         OperatorDashboardSection
	Stale          OperatorDashboardSection
	Blocked        OperatorDashboardSection
	RecentFailures OperatorDashboardSection
	RecentActivity OperatorDashboardSection
	Thresholds     OperatorDashboardThresholds
	ServerTime     time.Time
}

type RuntimeHints struct {
	ModifierReliabilityUncertain bool
	BinaryStale                  bool
	BinaryMetadataWarning        bool
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
	LoadOperatorDashboard        func(ctx context.Context) (*OperatorDashboardData, error)
	LoadAgents                   func(ctx context.Context) ([]string, error) // returns "name=lifecycle_status" strings
	LoadInboxItems               func(ctx context.Context) ([]InboxSummaryItem, error)
	// ActOnInboxItem sends approve/reject/defer/dismiss for a specific inbox item ID.
	// EX-160: required so that keyboard shortcuts in the inbox view reach the server.
	ActOnInboxItem func(ctx context.Context, itemID, action string) error
	// ConnectProjectRemote links a GitHub repo URL to the active project via
	// POST /v1/projects/{projectID}/remotes.
	ConnectProjectRemote func(ctx context.Context, projectID, repoURL string) error
	// ResetOrgSession archives the current org session and creates a new one.
	// Returns the new session UUID string.
	ResetOrgSession func(ctx context.Context, currentSessionID string) (string, error)

	// Settings dashboard callbacks
	LoadSettings         func(ctx context.Context) (*SettingsData, error)
	CreateSecret         func(ctx context.Context, slug, value string) error
	DeleteSecret         func(ctx context.Context, slug string) error
	UpdateModelProfile   func(ctx context.Context, profileID, providerID, modelName string) error
	UpdateConnectionAuth func(ctx context.Context, providerID, connectionID, authMode, secretSlug string) error
	CreateConnection     func(ctx context.Context, providerID, displayName, apiKeySecretSlug string, failoverPriority int) error

	// Login authenticates with email/password, creates an admin-scoped API key,
	// saves it to credentials, and hot-swaps the API client.
	Login func(ctx context.Context, email, password string) error
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
