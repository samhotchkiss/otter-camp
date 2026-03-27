package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/flowpolicy"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/taskorchestration"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
)

var (
	ErrOrganizationIDRequired              = errors.New("organization_id is required")
	ErrProjectIDRequired                   = errors.New("project_id is required")
	ErrTaskIDRequired                      = errors.New("task_id is required")
	ErrTitleRequired                       = errors.New("title is required")
	ErrInvalidCreatedByType                = errors.New("created_by_type must be human_user, agent, or system")
	ErrCreatedByIDRequired                 = errors.New("created_by_id is required for human_user and agent")
	ErrAgentNotAssigned                    = errors.New("assigned_agent_id is not actively assigned to the project")
	ErrHumanReviewNotRequired              = errors.New("task does not require human review")
	ErrInboxItemNotFound                   = errors.New("inbox item not found")
	ErrInboxActionForbidden                = errors.New("inbox action forbidden for this user")
	ErrUnknownInboxItemType                = errors.New("unknown inbox item type")
	ErrUnknownInboxAction                  = errors.New("unknown inbox action")
	ErrNoTaskBranch                        = errors.New("task has no branch name")
	ErrPMNotAssigned                       = errors.New("project has no active PM assignment")
	ErrInboxItemTypeInvalid                = errors.New("invalid inbox item type")
	ErrSourceTaskRequired                  = errors.New("source_task_id is required for this inbox item type")
	ErrSourceProjectIDRequired             = errors.New("source_project_id is required for this inbox item type")
	ErrRequiresHumanApproval               = errors.New("task requires human approval before queueing")
	ErrFlowTemplateRequired                = errors.New("task requires a flow template before it can be queued")
	ErrFlowTemplateReviewRequired          = errors.New("flow template must define a work -> review -> completion path")
	ErrProjectGateBlockingQueue            = errors.New("task is blocked by an outstanding project gate and cannot be queued yet")
	ErrProjectGateBlockingCreate           = errors.New("task is blocked by an outstanding project gate and cannot be created yet")
	ErrProjectGateExecutionPathRequired    = errors.New("non-bootstrap tasks with blocks_scope=all must include an assigned agent, executable flow template, or human review path")
	ErrBootstrapTaskAssigneeRequired       = errors.New("bootstrap executable non-bootstrap tasks must include assigned_agent_id for an active project assignee unless the task is explicitly orchestration-only")
	ErrInvalidBlocksScope                  = errors.New("blocks_scope must be one of: none, all")
	ErrDoneRequiresTerminalFlow            = errors.New("task can only be marked done when its flow reaches a terminal node")
	ErrTransitionTargetRequired            = errors.New("target status is required")
	ErrActorTypeInvalidForAction           = errors.New("actor_type is invalid for action")
	ErrBrowserHandoffUnavailable           = errors.New("browser handoff service unavailable")
	ErrActiveFlowRequired                  = errors.New("task requires an active flow to be in_progress")
	ErrFlowServiceUnavailable              = errors.New("flow service unavailable")
	ErrTaskMustRemainOrchestrationOnly     = errors.New("task must remain orchestration-only while executable child tasks exist")
	ErrTaskFlowStateInvariant              = errors.New("task flow state invariant violated")
	ErrParentIntegrationFeedbackRequired   = errors.New("completed child reopen requires parent integration feedback")
	ErrBlockedInProgressRequiresHumanActor = errors.New("blocked to in_progress requires direct human operator continuation")
)

var validStatusTransitions = map[string]map[string]struct{}{
	"draft": {
		"queued":    {},
		"cancelled": {},
	},
	"queued": {
		"in_progress": {},
		"review":      {},
		"on_hold":     {},
		"cancelled":   {},
	},
	"in_progress": {
		"blocked":   {},
		"on_hold":   {},
		"review":    {},
		"done":      {},
		"cancelled": {},
	},
	"blocked": {
		"queued":      {},
		"in_progress": {},
		"review":      {},
		"on_hold":     {},
		"cancelled":   {},
	},
	"on_hold": {
		"queued":    {},
		"cancelled": {},
	},
	"review": {
		"blocked":     {},
		"done":        {},
		"in_progress": {},
		"cancelled":   {},
	},
	"done":      {},
	"cancelled": {},
}

var validInboxItemTypes = map[string]struct{}{
	"task_review":             {},
	"human_approval_required": {},
	"blocker_filed":           {},
	"comment_mention":         {},
	"draft_action_review":     {},
	"browser_handoff":         {},
	"system_alert":            {},
}

var inboxItemTypesRequiringSourceTask = map[string]struct{}{
	"task_review":             {},
	"human_approval_required": {},
	"blocker_filed":           {},
	"comment_mention":         {},
	"draft_action_review":     {},
	"browser_handoff":         {},
}

type ErrInvalidStatusTransition struct {
	From string
	To   string
}

func (e ErrInvalidStatusTransition) Error() string {
	return fmt.Sprintf("invalid status transition from %q to %q", e.From, e.To)
}

type ErrTaskFlowStateConflict struct {
	TaskID            uuid.UUID
	WorkStatus        string
	TargetStatus      string
	CurrentFlowNodeID *uuid.UUID
	CurrentNodeType   string
	Reason            string
}

func (e ErrTaskFlowStateConflict) Error() string {
	reason := strings.TrimSpace(e.Reason)
	if reason == "" {
		reason = "task flow state does not match the requested runtime transition"
	}
	return reason
}

func (e ErrTaskFlowStateConflict) Is(target error) bool {
	return target == ErrTaskFlowStateInvariant
}

type Actor struct {
	Type                               string
	ID                                 uuid.UUID
	ExpectedFromStatus                 string
	AllowNoActiveFlow                  bool
	AllowFlowRuntimeBypass             bool
	AllowDoneBypass                    bool
	AllowSatisfiedDraftAutoComplete    bool
	AllowOrchestrationAutoComplete     bool
	AllowBootstrapPlanningAutoComplete bool
	AllowGateBypass                    bool
	AllowBootstrapGateAutoComplete     bool
	AllowBootstrapSetupComplete        bool
	AllowCompletedChildReopen          bool
}

type ErrInProgressRequiresActiveFlow struct {
	TaskID uuid.UUID
}

func (e ErrInProgressRequiresActiveFlow) Error() string {
	return ErrActiveFlowRequired.Error()
}

func (e ErrInProgressRequiresActiveFlow) Is(target error) bool {
	return target == ErrActiveFlowRequired
}

type ErrQueueBlockedByProjectGate struct {
	GateTaskID     uuid.UUID
	GateTaskNumber int
	GateTaskTitle  string
}

func (e ErrQueueBlockedByProjectGate) Error() string {
	if strings.EqualFold(strings.TrimSpace(e.GateTaskTitle), "Bootstrap governance gate") {
		if e.GateTaskNumber > 0 {
			return fmt.Sprintf("task is blocked by outstanding project gate task %d (%s) and cannot be queued yet. During project bootstrap, keep first-wave tasks in draft until setup is persisted through bootstrap.setup.persist.", e.GateTaskNumber, strings.TrimSpace(e.GateTaskTitle))
		}
		return "task is blocked by the outstanding Bootstrap governance gate and cannot be queued yet. During project bootstrap, keep first-wave tasks in draft until setup is persisted through bootstrap.setup.persist."
	}
	switch {
	case e.GateTaskNumber > 0 && strings.TrimSpace(e.GateTaskTitle) != "":
		return fmt.Sprintf("task is blocked by outstanding project gate task %d (%s) and cannot be queued yet", e.GateTaskNumber, strings.TrimSpace(e.GateTaskTitle))
	case e.GateTaskNumber > 0:
		return fmt.Sprintf("task is blocked by outstanding project gate task %d and cannot be queued yet", e.GateTaskNumber)
	default:
		return ErrProjectGateBlockingQueue.Error()
	}
}

func (e ErrQueueBlockedByProjectGate) Is(target error) bool {
	return target == ErrProjectGateBlockingQueue
}

type ErrCreateBlockedByProjectGate struct {
	GateTaskID     uuid.UUID
	GateTaskNumber int
	GateTaskTitle  string
}

func (e ErrCreateBlockedByProjectGate) Error() string {
	switch {
	case e.GateTaskNumber > 0 && strings.TrimSpace(e.GateTaskTitle) != "":
		return fmt.Sprintf("task creation is blocked by outstanding project gate task %d (%s)", e.GateTaskNumber, strings.TrimSpace(e.GateTaskTitle))
	case e.GateTaskNumber > 0:
		return fmt.Sprintf("task creation is blocked by outstanding project gate task %d", e.GateTaskNumber)
	default:
		return ErrProjectGateBlockingCreate.Error()
	}
}

func (e ErrCreateBlockedByProjectGate) Is(target error) bool {
	return target == ErrProjectGateBlockingCreate
}

type ProjectTask = repo.ProjectTask
type ProjectTaskEvent = repo.ProjectTaskEvent
type InboxItem = repo.InboxItem
type MergeQueueEntry = repo.MergeQueueEntry

type CreateTaskRequest struct {
	ProjectID           uuid.UUID
	Title               string
	Description         *string
	FlowTemplateID      *uuid.UUID
	ScheduleID          *uuid.UUID
	BlocksScope         string
	Priority            int
	AssignedAgentID     *uuid.UUID
	CreatedByType       string
	CreatedByID         uuid.UUID
	RequiresHumanReview *bool
	Metadata            json.RawMessage
}

type CreateInboxItemRequest struct {
	OrganizationID  uuid.UUID
	TargetUserID    *uuid.UUID
	ItemType        string
	SourceProjectID *uuid.UUID
	SourceTaskID    *uuid.UUID
	CreatedByType   string
	CreatedByID     *uuid.UUID
	Title           string
	Body            *string
	ActionPayload   json.RawMessage
	ExpiresAt       *time.Time
}

type TaskService interface {
	CreateTask(ctx context.Context, req CreateTaskRequest) (*ProjectTask, error)
	TransitionStatus(ctx context.Context, taskID uuid.UUID, toStatus string, actor Actor) (*ProjectTask, error)
	TransitionStatusWithPayload(ctx context.Context, taskID uuid.UUID, toStatus string, actor Actor, extraPayload map[string]any) (*ProjectTask, error)
	ResumeValidationBlockedTask(ctx context.Context, taskID uuid.UUID, actor Actor) (*ProjectTask, error)
	RequestHumanApproval(ctx context.Context, taskID uuid.UUID) (*InboxItem, error)
	ApproveTask(ctx context.Context, taskID, actedByUserID uuid.UUID) (*ProjectTask, error)
	RejectTask(ctx context.Context, taskID, actedByUserID uuid.UUID, reason string) (*ProjectTask, error)
	MarkBlocked(ctx context.Context, taskID uuid.UUID, reason string, actor Actor) (*ProjectTask, error)
	CreateInboxItem(ctx context.Context, req CreateInboxItemRequest) (*InboxItem, error)
	ActOnInboxItem(ctx context.Context, itemID, userID uuid.UUID, action string, payload json.RawMessage) error
	EnqueueForMerge(ctx context.Context, taskID uuid.UUID) (*MergeQueueEntry, error)
	DequeueFromMerge(ctx context.Context, entryID uuid.UUID, reason string) (*MergeQueueEntry, error)
	GetMergeQueueStatus(ctx context.Context, projectID uuid.UUID) ([]*MergeQueueEntry, error)
}

type taskRepository interface {
	Create(ctx context.Context, task repo.ProjectTask) (repo.ProjectTask, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
	ListByProject(ctx context.Context, projectID uuid.UUID, statuses ...string) ([]repo.ProjectTask, error)
	Update(ctx context.Context, task repo.ProjectTask) (repo.ProjectTask, error)
}

type taskMetadataUpdater interface {
	UpdateMetadata(ctx context.Context, id uuid.UUID, metadata json.RawMessage) (repo.ProjectTask, error)
}

type taskRepositoryTx interface {
	UpdateTx(ctx context.Context, tx pgx.Tx, task repo.ProjectTask) (repo.ProjectTask, error)
}

type taskEventRepository interface {
	Record(ctx context.Context, event repo.ProjectTaskEvent) (repo.ProjectTaskEvent, error)
}

type taskEventRepositoryTx interface {
	RecordTx(ctx context.Context, tx pgx.Tx, event repo.ProjectTaskEvent) (repo.ProjectTaskEvent, error)
}

type inboxRepository interface {
	Create(ctx context.Context, item repo.InboxItem) (repo.InboxItem, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.InboxItem, error)
	ListForUser(ctx context.Context, organizationID, userID uuid.UUID, options repo.InboxListOptions) ([]repo.InboxItem, error)
	MarkActed(ctx context.Context, id, actedByID uuid.UUID) (repo.InboxItem, error)
}

type mergeQueueRepository interface {
	Enqueue(ctx context.Context, entry repo.MergeQueueEntry) (repo.MergeQueueEntry, error)
	ListActive(ctx context.Context, projectID uuid.UUID) ([]repo.MergeQueueEntry, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, failureReason *string, mergedAt *time.Time) (repo.MergeQueueEntry, error)
	Archive(ctx context.Context, id uuid.UUID, archivedAt time.Time) (repo.MergeQueueEntry, error)
}

type projectRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

type agentProjectAssignmentRepository interface {
	GetByAgentAndProject(ctx context.Context, agentID, projectID uuid.UUID) (repo.AgentProjectAssignment, error)
	GetPM(ctx context.Context, projectID uuid.UUID) (repo.AgentProjectAssignment, error)
}

type agentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

type humanUserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.HumanUser, error)
}

type eventPublisher interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
}

type flowExecutionRepository interface {
	ListByTask(ctx context.Context, taskID uuid.UUID) ([]repo.FlowNodeExecution, error)
}

type flowNodeRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowNode, error)
	GetByTemplateOrdered(ctx context.Context, flowTemplateID uuid.UUID) ([]repo.FlowNode, error)
}

type flowTemplateRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowTemplate, error)
	ListCurrent(ctx context.Context, organizationID, projectID *uuid.UUID) ([]repo.FlowTemplate, error)
}

type browserHandoffCompleter interface {
	CompleteHandoffByInboxItem(ctx context.Context, inboxItemID, actedByUserID uuid.UUID, actionDecision string) error
}

type FlowReviewActions interface {
	AdvanceTaskReview(ctx context.Context, taskID uuid.UUID, actor Actor) error
	RejectTaskReview(ctx context.Context, taskID uuid.UUID, actor Actor, reason string) error
}

type Options struct {
	Pool *pgxpool.Pool

	Tasks   taskRepository
	Events  taskEventRepository
	Inbox   inboxRepository
	Queue   mergeQueueRepository
	Project projectRepository

	Assignments   agentProjectAssignmentRepository
	Agents        agentRepository
	Users         humanUserRepository
	Executions    flowExecutionRepository
	FlowNodes     flowNodeRepository
	FlowTemplates flowTemplateRepository

	EventBus eventPublisher
	Clock    clock.Clock

	BrowserHandoffs browserHandoffCompleter
	FlowReviews     FlowReviewActions
}

type service struct {
	pool *pgxpool.Pool

	tasks   taskRepository
	events  taskEventRepository
	inbox   inboxRepository
	queue   mergeQueueRepository
	project projectRepository

	assignments   agentProjectAssignmentRepository
	agents        agentRepository
	users         humanUserRepository
	executions    flowExecutionRepository
	flowNodes     flowNodeRepository
	flowTemplates flowTemplateRepository

	eventBus eventPublisher
	clock    clock.Clock

	browserHandoffs browserHandoffCompleter
	flowReviews     FlowReviewActions
}

func NewService(opts Options) (TaskService, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("task service requires a database pool")
	}
	if opts.EventBus == nil {
		return nil, fmt.Errorf("task service requires an event bus")
	}

	svc := &service{
		pool:     opts.Pool,
		eventBus: opts.EventBus,
		clock:    opts.Clock,

		browserHandoffs: opts.BrowserHandoffs,
		flowReviews:     opts.FlowReviews,
	}
	if svc.clock == nil {
		svc.clock = clock.Real{}
	}
	if opts.Tasks != nil {
		svc.tasks = opts.Tasks
	} else {
		svc.tasks = repo.NewProjectTaskRepo(opts.Pool)
	}
	if opts.Events != nil {
		svc.events = opts.Events
	} else {
		svc.events = repo.NewProjectTaskEventRepo(opts.Pool)
	}
	if opts.Inbox != nil {
		svc.inbox = opts.Inbox
	} else {
		svc.inbox = repo.NewInboxItemRepo(opts.Pool)
	}
	if opts.Queue != nil {
		svc.queue = opts.Queue
	} else {
		svc.queue = repo.NewMergeQueueEntryRepo(opts.Pool)
	}
	if opts.Project != nil {
		svc.project = opts.Project
	} else {
		svc.project = repo.NewProjectRepo(opts.Pool)
	}
	if opts.Assignments != nil {
		svc.assignments = opts.Assignments
	} else {
		svc.assignments = repo.NewAgentProjectAssignmentRepo(opts.Pool)
	}
	if opts.Agents != nil {
		svc.agents = opts.Agents
	} else {
		svc.agents = repo.NewAgentRepo(opts.Pool)
	}
	if opts.Users != nil {
		svc.users = opts.Users
	} else {
		svc.users = repo.NewHumanUserRepo(opts.Pool)
	}
	if opts.Executions != nil {
		svc.executions = opts.Executions
	} else {
		svc.executions = repo.NewFlowNodeExecutionRepo(opts.Pool)
	}
	if opts.FlowNodes != nil {
		svc.flowNodes = opts.FlowNodes
	} else {
		svc.flowNodes = repo.NewFlowNodeRepo(opts.Pool)
	}
	if opts.FlowTemplates != nil {
		svc.flowTemplates = opts.FlowTemplates
	} else {
		svc.flowTemplates = repo.NewFlowTemplateRepo(opts.Pool)
	}

	return svc, nil
}

func AttachFlowReviewActions(taskService any, actions FlowReviewActions) {
	if impl, ok := taskService.(*service); ok {
		impl.flowReviews = actions
	}
}

func (s *service) CreateTask(ctx context.Context, req CreateTaskRequest) (*ProjectTask, error) {
	if req.ProjectID == uuid.Nil {
		return nil, ErrProjectIDRequired
	}
	if strings.TrimSpace(req.Title) == "" {
		return nil, ErrTitleRequired
	}

	createdByType, createdByID, err := normalizeCreatedBy(req.CreatedByType, req.CreatedByID)
	if err != nil {
		return nil, err
	}

	projectRecord, err := s.project.GetByID(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if req.FlowTemplateID != nil && *req.FlowTemplateID != uuid.Nil {
		if err := s.validateExecutableFlowTemplate(ctx, *req.FlowTemplateID); err != nil {
			return nil, ErrFlowTemplateReviewRequired
		}
	}

	blocksScope := normalizeBlocksScope(req.BlocksScope)
	if blocksScope == "" {
		return nil, ErrInvalidBlocksScope
	}
	normalizedMetadata := normalizeJSON(req.Metadata)
	if s.shouldNormalizeOrchestrationParentTask(ctx, req.Title, normalizedMetadata, req.FlowTemplateID) {
		normalizedMetadata = taskdecomp.ApplyOrchestrationOnlyMetadata(normalizedMetadata)
		if blocksScope == "all" {
			blocksScope = "none"
		}
	}
	if err := s.ensureCreateEligible(ctx, req.ProjectID, normalizedMetadata); err != nil {
		return nil, err
	}
	if s.bootstrapExecutableTaskNeedsAssignment(ctx, req.ProjectID, normalizedMetadata) && (req.AssignedAgentID == nil || *req.AssignedAgentID == uuid.Nil) {
		return nil, ErrBootstrapTaskAssigneeRequired
	}

	if req.AssignedAgentID != nil {
		assignment, getErr := s.assignments.GetByAgentAndProject(ctx, *req.AssignedAgentID, req.ProjectID)
		if getErr != nil || !assignment.IsActive {
			if s.allowBootstrapSetupStarterTrioAssignment(ctx, *req.AssignedAgentID, normalizedMetadata) {
				goto assignedAgentValidated
			}
			return nil, ErrAgentNotAssigned
		}
	}
assignedAgentValidated:

	requiresHumanReview := projectRequiresHumanReview(projectRecord.Settings)
	if req.RequiresHumanReview != nil {
		requiresHumanReview = *req.RequiresHumanReview
	}
	taskRecord := repo.ProjectTask{
		OrganizationID:      projectRecord.OrganizationID,
		ProjectID:           req.ProjectID,
		Title:               strings.TrimSpace(req.Title),
		Description:         req.Description,
		WorkStatus:          "draft",
		BlocksScope:         blocksScope,
		FlowTemplateID:      req.FlowTemplateID,
		ScheduleID:          req.ScheduleID,
		Priority:            req.Priority,
		RequiresHumanReview: requiresHumanReview,
		CreatedByType:       createdByType,
		CreatedByID:         createdByID,
		AssignedAgentID:     req.AssignedAgentID,
		Metadata:            normalizedMetadata,
	}
	if err := ValidateProjectGateTask(taskRecord); err != nil {
		return nil, err
	}
	created, err := s.tasks.Create(ctx, taskRecord)
	if err != nil {
		return nil, err
	}

	if err := s.recordTaskEvent(ctx, created, "task.created", createdByType, createdByID, map[string]any{
		"task_id":     created.ID,
		"task_number": created.TaskNumber,
	}); err != nil {
		return nil, err
	}

	if err := s.publishTaskDomainEvent(ctx, created.OrganizationID, "task.created", createdByType, createdByID, map[string]any{
		"task_id":         created.ID,
		"project_id":      created.ProjectID,
		"task_number":     created.TaskNumber,
		"requires_review": created.RequiresHumanReview,
	}); err != nil {
		return nil, err
	}

	return &created, nil
}

func (s *service) bootstrapExecutableTaskNeedsAssignment(ctx context.Context, projectID uuid.UUID, metadata json.RawMessage) bool {
	if s == nil || projectID == uuid.Nil {
		return false
	}
	if bootstrapGateCreationExempt(metadata) {
		return false
	}
	payload := taskMetadataMap(metadata)
	if decomp, _ := payload["decomposition"].(map[string]any); decomp != nil {
		if orchestrationOnly, _ := decomp["orchestration_only"].(bool); orchestrationOnly {
			return false
		}
	}
	gateTask, err := s.lowestOutstandingProjectGate(ctx, projectID)
	if err != nil || gateTask == nil {
		return false
	}
	return bootstrapPlanningCreateAllowed(*gateTask)
}

func (s *service) allowBootstrapSetupStarterTrioAssignment(ctx context.Context, agentID uuid.UUID, metadata json.RawMessage) bool {
	if s == nil || s.agents == nil || agentID == uuid.Nil {
		return false
	}
	payload := taskMetadataMap(metadata)
	setupTask, _ := payload["bootstrap_setup_task"].(bool)
	if !setupTask {
		return false
	}
	agentRecord, err := s.agents.GetByID(ctx, agentID)
	if err != nil {
		return false
	}
	return agentRecord.IsStarterTrio
}

func (s *service) ensureCreateEligible(ctx context.Context, projectID uuid.UUID, metadata json.RawMessage) error {
	if bootstrapGateCreationExempt(metadata) {
		return nil
	}
	gateTask, err := s.lowestOutstandingProjectGate(ctx, projectID)
	if err != nil {
		return err
	}
	if gateTask == nil {
		return nil
	}
	if bootstrapPlanningCreateAllowed(*gateTask) {
		return nil
	}
	return ErrCreateBlockedByProjectGate{
		GateTaskID:     gateTask.ID,
		GateTaskNumber: gateTask.TaskNumber,
		GateTaskTitle:  gateTask.Title,
	}
}

func (s *service) TransitionStatus(ctx context.Context, taskID uuid.UUID, toStatus string, actor Actor) (*ProjectTask, error) {
	return s.transitionStatus(ctx, taskID, toStatus, actor, nil, false)
}

func (s *service) TransitionStatusWithPayload(ctx context.Context, taskID uuid.UUID, toStatus string, actor Actor, extraPayload map[string]any) (*ProjectTask, error) {
	return s.transitionStatus(ctx, taskID, toStatus, actor, extraPayload, false)
}

func (s *service) TransitionStatusWithPayloadTx(ctx context.Context, tx pgx.Tx, taskRecord repo.ProjectTask, toStatus string, actor Actor, extraPayload map[string]any) (*ProjectTask, error) {
	return s.transitionTaskRecordTx(ctx, tx, taskRecord, toStatus, actor, extraPayload, false)
}

func (s *service) transitionStatus(ctx context.Context, taskID uuid.UUID, toStatus string, actor Actor, extraPayload map[string]any, approvalOverride bool) (*ProjectTask, error) {
	if taskID == uuid.Nil {
		return nil, ErrTaskIDRequired
	}

	taskRecord, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return s.transitionTaskRecordTxWithRetry(ctx, nil, taskRecord, toStatus, actor, extraPayload, approvalOverride, true)
}

func (s *service) transitionTaskRecord(ctx context.Context, taskRecord repo.ProjectTask, toStatus string, actor Actor, extraPayload map[string]any, approvalOverride bool) (*ProjectTask, error) {
	return s.transitionTaskRecordTxWithRetry(ctx, nil, taskRecord, toStatus, actor, extraPayload, approvalOverride, true)
}

func (s *service) transitionTaskRecordTx(ctx context.Context, tx pgx.Tx, taskRecord repo.ProjectTask, toStatus string, actor Actor, extraPayload map[string]any, approvalOverride bool) (*ProjectTask, error) {
	return s.transitionTaskRecordTxWithRetry(ctx, tx, taskRecord, toStatus, actor, extraPayload, approvalOverride, false)
}

func (s *service) transitionTaskRecordTxWithRetry(ctx context.Context, tx pgx.Tx, taskRecord repo.ProjectTask, toStatus string, actor Actor, extraPayload map[string]any, approvalOverride bool, retryOnConflict bool) (*ProjectTask, error) {
	from := normalizeStatus(taskRecord.WorkStatus)
	target := normalizeStatus(toStatus)
	expectedFrom := normalizeStatus(actor.ExpectedFromStatus)
	if target == "" {
		return nil, ErrTransitionTargetRequired
	}
	if expectedFrom != "" && from != expectedFrom {
		return nil, fmt.Errorf("%w: expected current status %q before transition to %q, found %q", repo.ErrConflict, expectedFrom, target, from)
	}
	bootstrapGateAutoComplete := allowsBootstrapGateAutoComplete(taskRecord, from, target, actor)
	bootstrapSetupComplete := allowsBootstrapSetupComplete(taskRecord, from, target, actor)
	bootstrapPlanningAutoComplete := allowsBootstrapPlanningAutoComplete(taskRecord, from, target, actor)
	satisfiedDraftAutoComplete := allowsSatisfiedDraftAutoComplete(taskRecord, from, target, actor)
	childReopen := allowsCompletedChildReopen(taskRecord, from, target, actor)
	orchestrationAutoComplete := allowsOrchestrationAutoComplete(taskRecord, from, target, actor)
	flowRejectedOrchestrationReset := allowsFlowRejectedOrchestrationReset(taskRecord, from, target, actor, extraPayload)
	if target == "done" && taskRecord.FlowTemplateID == nil &&
		(orchestrationAutoComplete || bootstrapPlanningAutoComplete || satisfiedDraftAutoComplete || actor.AllowDoneBypass) {
		resolvedFlowTemplateID, resolveErr := s.resolveCompletionFlowTemplateID(ctx, taskRecord)
		if resolveErr != nil {
			return nil, resolveErr
		}
		taskRecord.FlowTemplateID = resolvedFlowTemplateID
	}
	if !isTransitionAllowed(from, target) && !bootstrapGateAutoComplete && !bootstrapSetupComplete && !bootstrapPlanningAutoComplete && !satisfiedDraftAutoComplete && !childReopen && !orchestrationAutoComplete && !flowRejectedOrchestrationReset {
		return nil, ErrInvalidStatusTransition{From: from, To: target}
	}
	if target == "queued" && taskRecord.RequiresHumanReview && !approvalOverride {
		return nil, ErrRequiresHumanApproval
	}
	if childReopen {
		feedback := strings.TrimSpace(extraPayloadString(extraPayload, "parent_integration_feedback"))
		if feedback == "" {
			return nil, ErrParentIntegrationFeedbackRequired
		}
		taskRecord.Metadata = applyParentIntegrationFeedback(taskRecord.Metadata, feedback, s.clock.Now().UTC())
	}
	if target == "queued" {
		if err := ValidateProjectGateTask(taskRecord); err != nil {
			return nil, err
		}
		if err := s.ensureQueueEligible(ctx, taskRecord, actor); err != nil {
			return nil, err
		}
	}
	if statusRequiresFlowTemplate(target) && taskRecord.FlowTemplateID == nil &&
		!(target == "done" && (orchestrationAutoComplete || bootstrapPlanningAutoComplete || satisfiedDraftAutoComplete || actor.AllowDoneBypass)) {
		return nil, ErrFlowTemplateRequired
	}
	if target == "queued" && taskRecord.FlowTemplateID != nil {
		if err := s.validateExecutableFlowTemplate(ctx, *taskRecord.FlowTemplateID); err != nil {
			return nil, ErrFlowTemplateReviewRequired
		}
	}
	if !actor.AllowFlowRuntimeBypass && !bootstrapSetupComplete && !bootstrapPlanningAutoComplete && !satisfiedDraftAutoComplete && !orchestrationAutoComplete {
		if err := s.validateFlowRuntimeStatus(ctx, taskRecord, target); err != nil {
			return nil, err
		}
	}

	decomposition := queueDecompositionResult{}
	if from == "draft" && target == "queued" {
		decompositionResult, decompErr := s.applyQueueDecomposition(ctx, &taskRecord)
		if decompErr != nil {
			return nil, decompErr
		}
		decomposition = decompositionResult

		requiresPM, pmReqErr := s.projectRequiresPMBeforeQueue(ctx, taskRecord.ProjectID)
		if pmReqErr != nil {
			return nil, pmReqErr
		}
		if requiresPM {
			if _, pmErr := s.assignments.GetPM(ctx, taskRecord.ProjectID); pmErr != nil {
				if errors.Is(pmErr, repo.ErrNotFound) {
					return nil, ErrPMNotAssigned
				}
				return nil, pmErr
			}
		}
	}
	executableChildren := []repo.ProjectTask(nil)
	if target == "queued" || target == "in_progress" {
		decompositionChildren, childErr := s.listDecompositionChildren(ctx, taskRecord)
		if childErr != nil {
			return nil, childErr
		}
		executableChildren = executableProjectTasks(decompositionChildren)
	}
	if target == "queued" && len(executableChildren) > 0 {
		if err := s.queueDecompositionChildren(ctx, taskRecord, executableChildren, actor); err != nil {
			return nil, err
		}
		updated, err := s.updateTaskMetadata(ctx, taskRecord)
		if err != nil {
			return nil, err
		}
		return &updated, nil
	}
	if target == "in_progress" && len(executableChildren) > 0 {
		return nil, ErrTaskMustRemainOrchestrationOnly
	}
	if target == "in_progress" {
		if from == "blocked" && normalizeActorTypeForTask(actor.Type) != "human_user" {
			return nil, ErrBlockedInProgressRequiresHumanActor
		}
		allowNoActiveFlow := actor.AllowNoActiveFlow && !strings.EqualFold(strings.TrimSpace(from), "blocked")
		hasActiveFlow, flowErr := s.hasActiveFlowExecution(ctx, taskRecord.ID)
		if flowErr != nil {
			return nil, flowErr
		}
		if !hasActiveFlow && !allowNoActiveFlow {
			return nil, ErrInProgressRequiresActiveFlow{TaskID: taskRecord.ID}
		}
	}
	if target == "done" && !actor.AllowDoneBypass && !bootstrapGateAutoComplete && !bootstrapSetupComplete {
		if enriched, enrichErr := enrichPlanningArtifactEvidence(taskRecord, s.clock.Now().UTC()); enrichErr != nil {
			return nil, enrichErr
		} else {
			taskRecord = enriched
		}
		if orchestrationAutoComplete {
			if enriched, enrichErr := s.enrichOrchestrationAutoCompleteMetadata(ctx, taskRecord); enrichErr != nil {
				return nil, enrichErr
			} else {
				taskRecord = enriched
			}
		}
		if report, reportErr := taskplan.CompletionReport(taskRecord.Metadata); reportErr != nil {
			return nil, reportErr
		} else if payload := report.Payload(); len(payload) > 0 {
			if extraPayload == nil {
				extraPayload = map[string]any{}
			}
			for key, value := range payload {
				extraPayload[key] = value
			}
		}
		if err := s.validateDoneTransition(ctx, taskRecord, orchestrationAutoComplete || bootstrapPlanningAutoComplete || satisfiedDraftAutoComplete || actor.AllowFlowRuntimeBypass); err != nil {
			return nil, err
		}
	}

	taskRecord.WorkStatus = target
	if target == "done" || target == "cancelled" {
		if _, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata); ok {
			cleared, clearErr := taskcheckpoint.ClearRecoveryFileWriteCheckpoint(taskRecord.Metadata)
			if clearErr != nil {
				return nil, clearErr
			}
			taskRecord.Metadata = cleared
		}
		completed := s.clock.Now().UTC()
		taskRecord.CompletedAt = &completed
	} else {
		taskRecord.CompletedAt = nil
	}

	updated, err := s.updateTaskTx(ctx, tx, taskRecord)
	if err != nil {
		if retryOnConflict && tx == nil && expectedFrom == "" && errors.Is(err, repo.ErrConflict) {
			latest, getErr := s.tasks.GetByID(ctx, taskRecord.ID)
			if getErr == nil {
				return s.transitionTaskRecordTxWithRetry(ctx, nil, latest, toStatus, actor, extraPayload, approvalOverride, false)
			}
		}
		return nil, err
	}
	if tx != nil && target == "cancelled" {
		return nil, fmt.Errorf("transactional task cancellation is not supported")
	}
	if target == "cancelled" {
		if err := s.archiveActiveMergeEntriesForTask(ctx, updated); err != nil {
			return nil, err
		}
	}

	actorType, actorID, err := normalizeActionActor(actor.Type, actor.ID)
	if err != nil {
		return nil, err
	}

	payload := map[string]any{
		"from_status": from,
		"to_status":   target,
		"task_id":     updated.ID,
		"project_id":  updated.ProjectID,
	}
	if decomposition.applied {
		payload["decomposition_applied"] = true
		payload["decomposition_child_task_ids"] = uuidStrings(decomposition.childTaskIDs)
	}
	for key, value := range extraPayload {
		payload[key] = value
	}

	if err := s.recordTaskEventTx(ctx, tx, updated, "status.changed", actorType, actorID, payload); err != nil {
		return nil, err
	}
	if err := s.publishTaskDomainEventTx(ctx, tx, updated.OrganizationID, "task.status_changed", actorType, actorID, payload); err != nil {
		return nil, err
	}

	return &updated, nil
}

func (s *service) ResumeValidationBlockedTask(ctx context.Context, taskID uuid.UUID, actor Actor) (*ProjectTask, error) {
	if taskID == uuid.Nil {
		return nil, ErrTaskIDRequired
	}

	taskRecord, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}

	blockerReason := s.loadLatestBlockedTaskReason(ctx, taskRecord.ID)
	checkpointRebuilt := false
	taskRecord, checkpointRebuilt, err = s.maybeRepairDurableRecoveryCheckpoint(ctx, taskRecord, blockerReason)
	if err != nil {
		return nil, err
	}

	decision := classifyTaskResumeDecision(taskRecord, blockerReason)
	if strings.EqualFold(strings.TrimSpace(blockerReason), "flow rejection max visits exceeded") {
		legacyDecision, ok, legacyErr := s.maybeResumeLegacyNonSubstantiveFlowRejection(ctx, taskRecord, blockerReason)
		if legacyErr != nil {
			return nil, legacyErr
		}
		if ok {
			decision = legacyDecision
		}
	}
	if !decision.resumable {
		return nil, TaskResumeBlockedStateError{
			BlockerClass:  decision.blockerClass,
			BlockerReason: decision.blockerReason,
		}
	}

	if decision.clearValidationGuard {
		cleared, clearErr := ClearValidationGuardMetadata(taskRecord.Metadata)
		if clearErr != nil {
			return nil, clearErr
		}
		taskRecord.Metadata = cleared
	}
	if decision.retryFlowNodeID != nil && *decision.retryFlowNodeID != uuid.Nil {
		taskRecord.CurrentFlowNodeID = decision.retryFlowNodeID
		updatedTask, updateErr := s.tasks.Update(ctx, taskRecord)
		if updateErr != nil {
			return nil, updateErr
		}
		taskRecord = updatedTask
	}
	targetStatus := resumeBlockedTaskTargetStatus(ctx, s.flowNodes, taskRecord)
	if targetStatus == "review" {
		if _, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata); !ok {
			decision.checkpoint = nil
		} else {
			cleared, clearErr := taskcheckpoint.ClearRecoveryFileWriteCheckpoint(taskRecord.Metadata)
			if clearErr != nil {
				return nil, clearErr
			}
			taskRecord.Metadata = cleared
			updatedTask, updateErr := s.updateTaskMetadata(ctx, taskRecord)
			if updateErr != nil {
				return nil, updateErr
			}
			taskRecord = updatedTask
			decision.checkpoint = nil
		}
	}

	payload := map[string]any{
		"recovery_action":        RecoveryActionResumeBlockedTask,
		"recovery_blocker_class": decision.blockerClass,
	}
	if checkpointRebuilt {
		payload["recovery_checkpoint_rebuilt"] = true
	}
	if reason := strings.TrimSpace(decision.blockerReason); reason != "" {
		payload["previous_blocker_reason"] = reason
	}
	if guard := decision.validationGuard; guard != nil {
		payload["cleared_validation_guard"] = true
		payload["validation_tool_name"] = strings.TrimSpace(guard.ToolName)
		payload["validation_failure_code"] = strings.TrimSpace(guard.FailureCode)
		payload["validation_failure_reason"] = strings.TrimSpace(guard.FailureReason)
	}
	if checkpoint := decision.checkpoint; checkpoint != nil {
		payload["recovery_checkpoint_present"] = true
		for key, value := range recoveryCheckpointPayload(*checkpoint) {
			payload[key] = value
		}
	}
	if decision.retryFlowNodeID != nil && *decision.retryFlowNodeID != uuid.Nil {
		payload["recovery_retry_flow_node_id"] = decision.retryFlowNodeID.String()
	}
	resumeActor := actor
	resumeActor.AllowGateBypass = true
	return s.transitionTaskRecord(ctx, taskRecord, targetStatus, resumeActor, payload, false)
}

func resumeBlockedTaskTargetStatus(ctx context.Context, flowNodes flowNodeRepository, taskRecord repo.ProjectTask) string {
	if flowNodes == nil || taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID == uuid.Nil {
		return "queued"
	}
	currentNode, err := flowNodes.GetByID(ctx, *taskRecord.CurrentFlowNodeID)
	if err != nil {
		return "queued"
	}
	switch expectedTaskRuntimeStatusForNode(currentNode) {
	case "review":
		return "review"
	default:
		return "queued"
	}
}

func (s *service) loadLatestBlockedTaskReason(ctx context.Context, taskID uuid.UUID) string {
	if s == nil || s.pool == nil || taskID == uuid.Nil {
		return ""
	}

	var reason string
	err := s.pool.QueryRow(ctx, `
		WITH latest_blocked AS (
			SELECT id, created_at, payload
			FROM project_task_event
			WHERE task_id = $1
			  AND event_type = 'status.changed'
			  AND COALESCE(payload->>'to_status', '') = 'blocked'
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		),
		latest_flow_rejected AS (
			SELECT id, created_at, payload
			FROM project_task_event
			WHERE task_id = $1
			  AND event_type = 'flow.rejected'
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		)
		SELECT COALESCE(
			NULLIF(TRIM(BOTH FROM latest_blocked.payload->>'blocker_reason'), ''),
			CASE
				WHEN latest_flow_rejected.created_at IS NOT NULL
				 AND (latest_blocked.created_at IS NULL OR latest_flow_rejected.created_at >= latest_blocked.created_at)
				THEN NULLIF(TRIM(BOTH FROM latest_flow_rejected.payload->>'blocked_reason'), '')
				ELSE ''
			END,
			''
		)
		FROM latest_blocked
		FULL OUTER JOIN latest_flow_rejected ON TRUE
		LIMIT 1
	`, taskID).Scan(&reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	if err != nil {
		return ""
	}
	return strings.TrimSpace(reason)
}

type queueDecompositionResult struct {
	applied      bool
	childTaskIDs []uuid.UUID
}

func (s *service) applyQueueDecomposition(ctx context.Context, taskRecord *repo.ProjectTask) (queueDecompositionResult, error) {
	if taskRecord == nil {
		return queueDecompositionResult{}, nil
	}
	prepared, err := taskdecomp.PrepareQueueDecomposition(taskdecomp.QueueDecompositionInput{
		ParentTaskID: taskRecord.ID,
		Title:        taskRecord.Title,
		Description:  taskRecord.Description,
		Metadata:     taskRecord.Metadata,
	})
	if err != nil {
		return queueDecompositionResult{}, err
	}
	if !prepared.Applied {
		return queueDecompositionResult{}, nil
	}

	childTaskIDs := make([]uuid.UUID, 0, len(prepared.ChildDrafts))
	for _, childDraft := range prepared.ChildDrafts {
		createdChild, createErr := s.tasks.Create(ctx, repo.ProjectTask{
			OrganizationID:      taskRecord.OrganizationID,
			ProjectID:           taskRecord.ProjectID,
			Title:               childDraft.Title,
			Description:         childDraft.Description,
			WorkStatus:          "draft",
			FlowTemplateID:      taskRecord.FlowTemplateID,
			ScheduleID:          taskRecord.ScheduleID,
			RequiresHumanReview: taskRecord.RequiresHumanReview,
			Priority:            taskRecord.Priority,
			CreatedByType:       taskRecord.CreatedByType,
			CreatedByID:         taskRecord.CreatedByID,
			Metadata:            childDraft.Metadata,
		})
		if createErr != nil {
			return queueDecompositionResult{}, createErr
		}
		childTaskIDs = append(childTaskIDs, createdChild.ID)

		if err := s.recordTaskEvent(ctx, createdChild, "task.created", taskRecord.CreatedByType, taskRecord.CreatedByID, map[string]any{
			"task_id":               createdChild.ID,
			"task_number":           createdChild.TaskNumber,
			"decomposition_parent":  taskRecord.ID,
			"decomposition_applied": true,
		}); err != nil {
			return queueDecompositionResult{}, err
		}
		if err := s.publishTaskDomainEvent(ctx, createdChild.OrganizationID, "task.created", taskRecord.CreatedByType, taskRecord.CreatedByID, map[string]any{
			"task_id":               createdChild.ID,
			"project_id":            createdChild.ProjectID,
			"task_number":           createdChild.TaskNumber,
			"decomposition_parent":  taskRecord.ID,
			"decomposition_applied": true,
		}); err != nil {
			return queueDecompositionResult{}, err
		}
	}

	taskRecord.Description = pointerString(prepared.Plan.PrimaryDeliverable)
	taskRecord.Metadata = taskdecomp.ApplyMetadata(taskRecord.Metadata, prepared.Plan, prepared.SourceDescription, childTaskIDs)
	taskRecord.BlocksScope = "none"

	return queueDecompositionResult{
		applied:      true,
		childTaskIDs: childTaskIDs,
	}, nil
}

func (s *service) listDecompositionChildren(ctx context.Context, parentTask repo.ProjectTask) ([]repo.ProjectTask, error) {
	projectTasks, err := s.tasks.ListByProject(ctx, parentTask.ProjectID)
	if err != nil {
		return nil, err
	}

	childIDSet := make(map[uuid.UUID]struct{})
	for _, childID := range taskdecomp.ParseChildTaskIDs(parentTask.Metadata) {
		childIDSet[childID] = struct{}{}
	}

	children := make([]repo.ProjectTask, 0)
	for _, projectTask := range projectTasks {
		if projectTask.ID == parentTask.ID {
			continue
		}
		if taskdecomp.ParseParentTaskID(projectTask.Metadata) == parentTask.ID {
			children = append(children, projectTask)
			delete(childIDSet, projectTask.ID)
			continue
		}
		if _, ok := childIDSet[projectTask.ID]; ok {
			children = append(children, projectTask)
			delete(childIDSet, projectTask.ID)
		}
	}

	sort.Slice(children, func(i, j int) bool {
		return decompositionTaskLess(children[i], children[j])
	})
	return children, nil
}

func (s *service) queueDecompositionChildren(ctx context.Context, parentTask repo.ProjectTask, children []repo.ProjectTask, actor Actor) error {
	for _, child := range children {
		if normalizeStatus(child.WorkStatus) != "draft" {
			continue
		}
		if _, err := s.transitionTaskRecord(ctx, child, "queued", actor, nil, false); err != nil {
			return fmt.Errorf("queue decomposition child for parent %s: %w", parentTask.ID, err)
		}
	}
	return nil
}

func executableProjectTasks(tasks []repo.ProjectTask) []repo.ProjectTask {
	filtered := make([]repo.ProjectTask, 0, len(tasks))
	for _, taskRecord := range tasks {
		if isTerminalTaskStatus(taskRecord.WorkStatus) {
			continue
		}
		filtered = append(filtered, taskRecord)
	}
	return filtered
}

func isTerminalTaskStatus(status string) bool {
	switch normalizeStatus(status) {
	case "done", "cancelled":
		return true
	default:
		return false
	}
}

func decompositionTaskLess(left, right repo.ProjectTask) bool {
	leftIndex, leftOK := taskdecomp.ParseWorkstreamIndex(left.Metadata)
	rightIndex, rightOK := taskdecomp.ParseWorkstreamIndex(right.Metadata)
	switch {
	case leftOK && rightOK && leftIndex != rightIndex:
		return leftIndex < rightIndex
	case leftOK != rightOK:
		return leftOK
	case left.TaskNumber != right.TaskNumber:
		return left.TaskNumber < right.TaskNumber
	default:
		return left.ID.String() < right.ID.String()
	}
}

func (s *service) projectRequiresPMBeforeQueue(ctx context.Context, projectID uuid.UUID) (bool, error) {
	if s.project == nil {
		return false, nil
	}
	projectRecord, err := s.project.GetByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return projectRequiresPMBeforeQueueSetting(projectRecord.Settings), nil
}

func (s *service) ensureQueueEligible(ctx context.Context, taskRecord repo.ProjectTask, actor Actor) error {
	if actor.AllowGateBypass {
		return nil
	}
	gateTask, err := s.lowestOutstandingProjectGate(ctx, taskRecord.ProjectID)
	if err != nil {
		return err
	}
	if gateTask == nil || gateTask.ID == taskRecord.ID {
		return nil
	}
	return ErrQueueBlockedByProjectGate{
		GateTaskID:     gateTask.ID,
		GateTaskNumber: gateTask.TaskNumber,
		GateTaskTitle:  gateTask.Title,
	}
}

func (s *service) lowestOutstandingProjectGate(ctx context.Context, projectID uuid.UUID) (*repo.ProjectTask, error) {
	projectTasks, err := s.tasks.ListByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}

	canonicalBootstrapGateID, hasCanonicalBootstrapGate := canonicalBootstrapGateTaskID(projectTasks)
	var selected *repo.ProjectTask
	for _, taskRecord := range projectTasks {
		if !strings.EqualFold(strings.TrimSpace(taskRecord.BlocksScope), "all") {
			continue
		}
		if looksLikeProjectContinuationMetaDraft(taskRecord.Title, taskRecord.Description) {
			continue
		}
		if taskIsBootstrapGate(taskRecord) && hasCanonicalBootstrapGate && taskRecord.ID != canonicalBootstrapGateID {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(taskRecord.WorkStatus))
		if status == "done" || status == "cancelled" {
			continue
		}
		if status == "draft" && !taskIsBootstrapGate(taskRecord) {
			if err := ValidateProjectGateTask(taskRecord); err != nil {
				continue
			}
		}
		if selected == nil || taskRecord.TaskNumber < selected.TaskNumber {
			clone := taskRecord
			selected = &clone
		}
	}
	return selected, nil
}

func canonicalBootstrapGateTaskID(tasks []repo.ProjectTask) (uuid.UUID, bool) {
	bootstrapGates := make([]repo.ProjectTask, 0)
	canonicalSetupBySlug := make(map[string]repo.ProjectTask)
	earliestCompletedSetupNumber := 0

	for _, taskRecord := range tasks {
		if taskIsBootstrapGate(taskRecord) {
			bootstrapGates = append(bootstrapGates, taskRecord)
		}
		stepSlug := bootstrapStepSlug(taskRecord)
		if stepSlug == "" {
			continue
		}
		current, ok := canonicalSetupBySlug[stepSlug]
		if !ok || shouldPreferCanonicalBootstrapScaffoldTask(taskRecord, current) {
			canonicalSetupBySlug[stepSlug] = taskRecord
		}
	}
	if len(bootstrapGates) == 0 {
		return uuid.Nil, false
	}

	for _, taskRecord := range canonicalSetupBySlug {
		if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "done") {
			continue
		}
		if earliestCompletedSetupNumber == 0 || taskRecord.TaskNumber < earliestCompletedSetupNumber {
			earliestCompletedSetupNumber = taskRecord.TaskNumber
		}
	}

	var selected repo.ProjectTask
	selectedSet := false
	if earliestCompletedSetupNumber > 0 {
		for _, taskRecord := range bootstrapGates {
			if taskRecord.TaskNumber >= earliestCompletedSetupNumber {
				continue
			}
			if !selectedSet || taskRecord.TaskNumber > selected.TaskNumber {
				selected = taskRecord
				selectedSet = true
			}
		}
	}
	if selectedSet {
		return selected.ID, true
	}

	for _, taskRecord := range bootstrapGates {
		if !selectedSet || taskRecord.TaskNumber < selected.TaskNumber {
			selected = taskRecord
			selectedSet = true
		}
	}
	if !selectedSet {
		return uuid.Nil, false
	}
	return selected.ID, true
}

func taskIsBootstrapGate(taskRecord repo.ProjectTask) bool {
	if len(taskRecord.Metadata) == 0 || !json.Valid(taskRecord.Metadata) {
		return false
	}
	var metadata map[string]any
	if err := json.Unmarshal(taskRecord.Metadata, &metadata); err != nil {
		return false
	}
	raw, ok := metadata["bootstrap_gate"].(bool)
	return ok && raw
}

func bootstrapStepSlug(taskRecord repo.ProjectTask) string {
	if len(taskRecord.Metadata) == 0 || !json.Valid(taskRecord.Metadata) {
		return ""
	}
	var metadata map[string]any
	if err := json.Unmarshal(taskRecord.Metadata, &metadata); err != nil {
		return ""
	}
	raw, ok := metadata["bootstrap_step_slug"]
	if !ok {
		return ""
	}
	value, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func shouldPreferCanonicalBootstrapScaffoldTask(candidate, current repo.ProjectTask) bool {
	candidateDone := strings.EqualFold(strings.TrimSpace(candidate.WorkStatus), "done")
	currentDone := strings.EqualFold(strings.TrimSpace(current.WorkStatus), "done")
	if candidateDone != currentDone {
		return candidateDone
	}
	return candidate.TaskNumber < current.TaskNumber
}

func projectRequiresPMBeforeQueueSetting(settings json.RawMessage) bool {
	var payload map[string]any
	if err := json.Unmarshal(settings, &payload); err != nil {
		return false
	}
	raw, ok := payload["requires_pm_assignment_before_queue"]
	if !ok {
		return false
	}
	flag, ok := raw.(bool)
	return ok && flag
}

func allowsOrchestrationAutoComplete(taskRecord repo.ProjectTask, from, target string, actor Actor) bool {
	if !actor.AllowOrchestrationAutoComplete {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(actor.Type), "system") {
		return false
	}
	if normalizeStatus(target) != "done" {
		return false
	}
	if len(taskdecomp.ParseChildTaskIDs(taskRecord.Metadata)) == 0 {
		return false
	}
	switch normalizeStatus(from) {
	case "draft", "queued", "in_progress", "review":
		return true
	default:
		return false
	}
}

func allowsFlowRejectedOrchestrationReset(taskRecord repo.ProjectTask, from, target string, actor Actor, extraPayload map[string]any) bool {
	if !actor.AllowFlowRuntimeBypass {
		return false
	}
	if normalizeStatus(from) != "review" || normalizeStatus(target) != "draft" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(extraPayloadString(extraPayload, "flow_event_type")), "flow.rejected") {
		return false
	}
	return taskRequiresBoundedChildren(taskRecord)
}

func allowsBootstrapPlanningAutoComplete(taskRecord repo.ProjectTask, from, target string, actor Actor) bool {
	if !actor.AllowBootstrapPlanningAutoComplete {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(actor.Type), "system") {
		return false
	}
	if normalizeStatus(target) != "done" {
		return false
	}
	if !strings.HasPrefix(strings.TrimSpace(taskRecord.Title), "Bootstrap:") {
		return false
	}
	plan, ok := taskplan.Parse(taskRecord.Metadata)
	if !ok || !plan.ProcessEnforced || len(plan.Artifacts) == 0 {
		return false
	}
	switch normalizeStatus(from) {
	case "draft", "queued", "in_progress", "review":
		return true
	default:
		return false
	}
}

func SatisfiedDraftAutoCompletable(taskRecord repo.ProjectTask) bool {
	state, ok := taskorchestration.Parse(taskRecord.Metadata)
	if !ok || state.OutcomeAssessment == nil || !state.OutcomeAssessment.Satisfied {
		return false
	}
	plan, ok := taskplan.Parse(taskRecord.Metadata)
	if !ok || len(plan.ArtifactEvidence) == 0 {
		return false
	}
	if _, err := taskplan.CompletionReport(taskRecord.Metadata); err != nil {
		return false
	}
	prepared, err := taskdecomp.PrepareQueueDecomposition(taskdecomp.QueueDecompositionInput{
		ParentTaskID: taskRecord.ID,
		Title:        taskRecord.Title,
		Description:  taskRecord.Description,
		Metadata:     taskRecord.Metadata,
	})
	if err != nil {
		return false
	}
	return !prepared.Applied
}

func allowsSatisfiedDraftAutoComplete(taskRecord repo.ProjectTask, from, target string, actor Actor) bool {
	if !actor.AllowSatisfiedDraftAutoComplete {
		return false
	}
	if normalizeStatus(from) != "draft" || normalizeStatus(target) != "done" {
		return false
	}
	return SatisfiedDraftAutoCompletable(taskRecord)
}

func (s *service) validateDoneTransition(ctx context.Context, taskRecord repo.ProjectTask, allowFlowCompletionBypass bool) error {
	children, err := s.listDecompositionChildren(ctx, taskRecord)
	if err != nil {
		return err
	}
	if err := taskorchestration.ValidateCompletion(taskRecord, children); err != nil {
		return err
	}
	if allowFlowCompletionBypass {
		return nil
	}
	if taskRecord.FlowTemplateID == nil || taskRecord.CurrentFlowNodeID == nil {
		return ErrDoneRequiresTerminalFlow
	}
	if s.flowNodes == nil || s.executions == nil {
		return ErrDoneRequiresTerminalFlow
	}
	node, err := s.flowNodes.GetByID(ctx, *taskRecord.CurrentFlowNodeID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrDoneRequiresTerminalFlow
		}
		return err
	}
	if node.NextNodeID != nil {
		return ErrDoneRequiresTerminalFlow
	}
	rows, err := s.executions.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrDoneRequiresTerminalFlow
		}
		return err
	}
	terminalCompleted := false
	progress, err := s.reviewProgressFromExecutions(ctx, rows)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.FlowNodeID != node.ID {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(row.Status), "completed") {
			terminalCompleted = true
			break
		}
	}
	if !terminalCompleted || !progress.hasWork || !progress.hasReviewedWork || progress.pendingReviewWork {
		return ErrDoneRequiresTerminalFlow
	}
	return nil
}

func (s *service) enrichOrchestrationAutoCompleteMetadata(ctx context.Context, taskRecord repo.ProjectTask) (repo.ProjectTask, error) {
	children, err := s.listDecompositionChildren(ctx, taskRecord)
	if err != nil {
		return taskRecord, err
	}
	if !isOrchestrationOnlyParentTask(taskRecord, children) {
		return taskRecord, nil
	}

	now := s.clock.Now().UTC()
	if taskRecord, err = enrichPlanningArtifactEvidence(taskRecord, now); err != nil {
		return taskRecord, err
	}
	state, _ := taskorchestration.Parse(taskRecord.Metadata)
	update := taskorchestration.Update{}

	if verifications := missingChildVerifications(children, state, now); len(verifications) > 0 {
		update.ChildVerifications = verifications
	}
	if state.IntegrationCheck == nil {
		update.IntegrationCheck = taskorchestration.NewIntegrationCheck("passed", orchestrationIntegrationSummary(taskRecord), now)
	}
	if state.OutcomeAssessment == nil {
		update.OutcomeAssessment = taskorchestration.NewOutcomeAssessment(true, orchestrationOutcomeSummary(taskRecord), now)
	}
	if len(update.ChildVerifications) == 0 && update.IntegrationCheck == nil && update.OutcomeAssessment == nil {
		return taskRecord, nil
	}

	metadata, err := taskorchestration.Apply(taskRecord.Metadata, update)
	if err != nil {
		return taskRecord, err
	}
	taskRecord.Metadata = metadata
	return taskRecord, nil
}

func enrichPlanningArtifactEvidence(taskRecord repo.ProjectTask, now time.Time) (repo.ProjectTask, error) {
	plan, ok := taskplan.Parse(taskRecord.Metadata)
	if !ok || !plan.ProcessEnforced || len(plan.Artifacts) == 0 {
		return taskRecord, nil
	}

	contracts := taskplan.ArtifactContractForPlan(plan)
	if len(contracts) == 0 {
		return taskRecord, nil
	}

	artifactsBySlug := make(map[string]taskplan.PlannedArtifact, len(plan.Artifacts))
	for _, artifact := range plan.Artifacts {
		slug := strings.TrimSpace(artifact.Slug)
		if slug == "" {
			continue
		}
		artifactsBySlug[slug] = artifact
	}

	updates := make([]taskplan.ArtifactEvidence, 0, len(contracts))
	for _, contract := range contracts {
		artifact, ok := artifactsBySlug[strings.TrimSpace(contract.Slug)]
		if !ok {
			continue
		}
		title := strings.TrimSpace(artifact.Title)
		if title == "" {
			title = strings.TrimSpace(contract.Title)
		}
		summary := "Recorded artifact for completion."
		if path := strings.TrimSpace(artifact.RepoPath); path != "" {
			summary = "Recorded artifact at " + path + "."
		} else if title != "" {
			summary = "Recorded artifact for " + title + "."
		}
		updates = append(updates, taskplan.ArtifactEvidence{
			Slug:     contract.Slug,
			Title:    title,
			Summary:  summary,
			Sections: append([]string(nil), contract.RequiredSections...),
			Notes:    "Auto-synthesized from recorded planning artifacts during task completion.",
		})
	}
	if len(updates) == 0 {
		return taskRecord, nil
	}

	metadata, _, _, err := taskplan.ApplyProcessUpdate(taskRecord.Metadata, taskplan.ProcessUpdate{
		Artifacts:          updates,
		HasArtifactChanges: true,
		ActorType:          "system",
		RecordedAt:         now,
	})
	if err != nil {
		return taskRecord, err
	}
	taskRecord.Metadata = metadata
	return taskRecord, nil
}

func missingChildVerifications(children []repo.ProjectTask, state taskorchestration.ParentState, now time.Time) []taskorchestration.ChildVerification {
	verified := map[string]struct{}{}
	for _, item := range state.ChildVerifications {
		verified[strings.TrimSpace(item.TaskID)] = struct{}{}
	}
	out := make([]taskorchestration.ChildVerification, 0, len(children))
	for _, child := range children {
		if normalizeStatus(child.WorkStatus) != "done" {
			continue
		}
		if _, ok := verified[child.ID.String()]; ok {
			continue
		}
		summary := "Verified completed child output."
		if title := strings.TrimSpace(child.Title); title != "" {
			summary = "Verified completed child output for " + title + "."
		}
		out = append(out, taskorchestration.NewChildVerification(child.ID, summary, now))
	}
	return out
}

func orchestrationIntegrationSummary(taskRecord repo.ProjectTask) string {
	if primary := strings.TrimSpace(taskdecomp.ParsePrimaryDeliverable(taskRecord.Metadata)); primary != "" {
		return "Validated completed child outputs against parent deliverable: " + primary + "."
	}
	if title := strings.TrimSpace(taskRecord.Title); title != "" {
		return "Validated completed child outputs against parent task: " + title + "."
	}
	return "Validated completed child outputs against the parent integration outcome."
}

func orchestrationOutcomeSummary(taskRecord repo.ProjectTask) string {
	if primary := strings.TrimSpace(taskdecomp.ParsePrimaryDeliverable(taskRecord.Metadata)); primary != "" {
		return "Parent deliverable satisfied: " + primary + "."
	}
	if title := strings.TrimSpace(taskRecord.Title); title != "" {
		return "Parent task outcome satisfied for " + title + "."
	}
	return "Parent task outcome is satisfied."
}

func (s *service) resolveCompletionFlowTemplateID(ctx context.Context, taskRecord repo.ProjectTask) (*uuid.UUID, error) {
	if taskRecord.FlowTemplateID != nil && *taskRecord.FlowTemplateID != uuid.Nil {
		return taskRecord.FlowTemplateID, nil
	}
	if s.project != nil {
		projectRecord, err := s.project.GetByID(ctx, taskRecord.ProjectID)
		if err != nil && !errors.Is(err, repo.ErrNotFound) {
			return nil, err
		}
		if err == nil && projectRecord.DeployFlowTemplateID != nil && *projectRecord.DeployFlowTemplateID != uuid.Nil {
			return projectRecord.DeployFlowTemplateID, nil
		}
	}
	if s.flowTemplates == nil {
		return nil, nil
	}
	projectTemplates, err := s.flowTemplates.ListCurrent(ctx, &taskRecord.OrganizationID, &taskRecord.ProjectID)
	if err != nil {
		return nil, err
	}
	var selected *uuid.UUID
	for _, template := range projectTemplates {
		if !strings.EqualFold(strings.TrimSpace(template.Slug), "bootstrap-governance-gate") {
			id := template.ID
			return &id, nil
		}
		if selected == nil && template.ID != uuid.Nil {
			id := template.ID
			selected = &id
		}
	}
	return selected, nil
}

func isOrchestrationOnlyParentTask(taskRecord repo.ProjectTask, children []repo.ProjectTask) bool {
	if len(children) == 0 || len(taskdecomp.ParseChildTaskIDs(taskRecord.Metadata)) == 0 {
		return false
	}
	metadata := taskMetadataMap(taskRecord.Metadata)
	decomp, _ := metadata["decomposition"].(map[string]any)
	orchestrationOnly, _ := decomp["orchestration_only"].(bool)
	return orchestrationOnly
}

func taskRequiresBoundedChildren(taskRecord repo.ProjectTask) bool {
	metadata := taskMetadataMap(taskRecord.Metadata)
	decomp, _ := metadata["decomposition"].(map[string]any)
	orchestrationOnly, _ := decomp["orchestration_only"].(bool)
	if orchestrationOnly {
		return true
	}
	plan, ok := taskplan.Parse(taskRecord.Metadata)
	if !ok {
		return false
	}
	stopReason := strings.ToLower(strings.TrimSpace(plan.FollowOnStopReason))
	return strings.Contains(stopReason, "parent task is orchestration-only")
}

func isBootstrapPlanningTask(taskRecord repo.ProjectTask) bool {
	if !strings.HasPrefix(strings.TrimSpace(taskRecord.Title), "Bootstrap:") {
		return false
	}
	plan, ok := taskplan.Parse(taskRecord.Metadata)
	return ok && plan.ProcessEnforced && len(plan.Artifacts) > 0
}

type executionReviewProgress struct {
	hasWork           bool
	hasReviewedWork   bool
	pendingReviewWork bool
}

func (s *service) reviewProgressFromExecutions(ctx context.Context, executions []repo.FlowNodeExecution) (executionReviewProgress, error) {
	progress := executionReviewProgress{}
	nodeCache := make(map[uuid.UUID]repo.FlowNode, len(executions))

	for _, execution := range executions {
		if !strings.EqualFold(strings.TrimSpace(execution.Status), "completed") {
			continue
		}
		node, ok := nodeCache[execution.FlowNodeID]
		if !ok {
			loaded, err := s.flowNodes.GetByID(ctx, execution.FlowNodeID)
			if err != nil {
				if errors.Is(err, repo.ErrNotFound) {
					return executionReviewProgress{}, ErrDoneRequiresTerminalFlow
				}
				return executionReviewProgress{}, err
			}
			node = loaded
			nodeCache[execution.FlowNodeID] = node
		}

		switch strings.ToLower(strings.TrimSpace(node.NodeType)) {
		case "work":
			progress.hasWork = true
			progress.pendingReviewWork = true
		case "review":
			if progress.pendingReviewWork {
				progress.hasReviewedWork = true
				progress.pendingReviewWork = false
			}
		}
	}

	return progress, nil
}

func (s *service) hasActiveFlowExecution(ctx context.Context, taskID uuid.UUID) (bool, error) {
	if s.executions == nil {
		return false, nil
	}
	rows, err := s.executions.ListByTask(ctx, taskID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.Status), "active") {
			return true, nil
		}
	}
	return false, nil
}

func (s *service) RequestHumanApproval(ctx context.Context, taskID uuid.UUID) (*InboxItem, error) {
	taskRecord, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if !taskRecord.RequiresHumanReview {
		return nil, ErrHumanReviewNotRequired
	}

	targetUserID, err := s.resolvePMTargetUser(ctx, taskRecord.ProjectID)
	if err != nil {
		return nil, err
	}

	title := "Human approval required"
	body := "Task requires human approval before queueing."
	return s.CreateInboxItem(ctx, CreateInboxItemRequest{
		OrganizationID:  taskRecord.OrganizationID,
		TargetUserID:    targetUserID,
		ItemType:        "human_approval_required",
		SourceProjectID: &taskRecord.ProjectID,
		SourceTaskID:    &taskRecord.ID,
		CreatedByType:   "system",
		Title:           title,
		Body:            &body,
		ActionPayload:   json.RawMessage(`{"action":"approve_or_reject"}`),
	})
}

func (s *service) ApproveTask(ctx context.Context, taskID, actedByUserID uuid.UUID) (*ProjectTask, error) {
	taskRecord, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}

	item, err := s.findPendingApprovalItem(ctx, taskRecord, actedByUserID)
	if err != nil {
		return nil, err
	}
	if _, err := s.inbox.MarkActed(ctx, item.ID, actedByUserID); err != nil {
		return nil, err
	}

	return s.transitionStatus(ctx, taskID, "queued", Actor{Type: "human_user", ID: actedByUserID}, nil, true)
}

func (s *service) RejectTask(ctx context.Context, taskID, actedByUserID uuid.UUID, reason string) (*ProjectTask, error) {
	taskRecord, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}

	item, err := s.findPendingApprovalItem(ctx, taskRecord, actedByUserID)
	if err != nil {
		return nil, err
	}
	if _, err := s.inbox.MarkActed(ctx, item.ID, actedByUserID); err != nil {
		return nil, err
	}

	payload := map[string]any{
		"reason": strings.TrimSpace(reason),
	}
	if err := s.recordTaskEvent(ctx, taskRecord, "task.review_rejected", "human_user", &actedByUserID, payload); err != nil {
		return nil, err
	}
	if err := s.publishTaskDomainEvent(ctx, taskRecord.OrganizationID, "task.review_rejected", "human_user", &actedByUserID, map[string]any{
		"task_id": taskRecord.ID,
		"reason":  strings.TrimSpace(reason),
	}); err != nil {
		return nil, err
	}

	return &taskRecord, nil
}

func (s *service) MarkBlocked(ctx context.Context, taskID uuid.UUID, reason string, actor Actor) (*ProjectTask, error) {
	taskRecord, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}

	payload := map[string]any{
		"blocker_reason": strings.TrimSpace(reason),
	}
	if checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata); ok {
		for key, value := range recoveryCheckpointPayload(checkpoint) {
			payload[key] = value
		}
	}

	blocked, err := s.transitionTaskRecord(ctx, taskRecord, "blocked", actor, payload, false)
	if err != nil {
		return nil, err
	}

	createdByID := pointerUUID(actor.ID)
	if strings.TrimSpace(actor.Type) == "system" {
		createdByID = nil
	}
	if blockerPolicyAutoCreateResolutionTask(blocked.Metadata) {
		projectRecord, err := s.project.GetByID(ctx, blocked.ProjectID)
		if err != nil {
			return nil, err
		}

		pmAssignment, err := s.assignments.GetPM(ctx, blocked.ProjectID)
		if err != nil {
			return nil, ErrPMNotAssigned
		}
		if blocked.FlowTemplateID != nil {
			if err := s.validateExecutableFlowTemplate(ctx, *blocked.FlowTemplateID); err != nil {
				return nil, ErrFlowTemplateReviewRequired
			}
		}

		title := fmt.Sprintf("Resolve blocker for task %s-%d", projectRecord.Slug, blocked.TaskNumber)
		resolutionTask, createErr := s.tasks.Create(ctx, repo.ProjectTask{
			OrganizationID:      blocked.OrganizationID,
			ProjectID:           blocked.ProjectID,
			Title:               title,
			Description:         pointerString("Automatically created to resolve blocker"),
			WorkStatus:          "draft",
			FlowTemplateID:      blocked.FlowTemplateID,
			CreatedByType:       normalizeActorTypeForTask(actor.Type),
			CreatedByID:         createdByID,
			AssignedAgentID:     &pmAssignment.AgentID,
			RequiresHumanReview: false,
			Metadata:            json.RawMessage(`{"auto_created":"mark_blocked"}`),
		})
		if createErr != nil {
			return nil, createErr
		}

		if err := s.recordTaskEvent(ctx, resolutionTask, "task.created", normalizeActorTypeForTask(actor.Type), createdByID, map[string]any{
			"task_id":     resolutionTask.ID,
			"task_number": resolutionTask.TaskNumber,
		}); err != nil {
			return nil, err
		}
		if err := s.publishTaskDomainEvent(ctx, resolutionTask.OrganizationID, "task.created", normalizeActorTypeForTask(actor.Type), createdByID, map[string]any{
			"task_id":         resolutionTask.ID,
			"project_id":      resolutionTask.ProjectID,
			"task_number":     resolutionTask.TaskNumber,
			"auto_resolution": true,
		}); err != nil {
			return nil, err
		}
		if _, queueErr := s.TransitionStatus(ctx, resolutionTask.ID, "queued", Actor{Type: actor.Type, ID: actor.ID}); queueErr != nil {
			return nil, queueErr
		}
	}

	targetUserID, err := s.resolvePMTargetUser(ctx, blocked.ProjectID)
	if err != nil {
		return nil, err
	}
	inboxTitle := "Blocker filed"
	inboxBody := "A blocker was filed and requires PM attention."
	if _, err := s.CreateInboxItem(ctx, CreateInboxItemRequest{
		OrganizationID:  blocked.OrganizationID,
		TargetUserID:    targetUserID,
		ItemType:        "blocker_filed",
		SourceProjectID: &blocked.ProjectID,
		SourceTaskID:    &blocked.ID,
		CreatedByType:   normalizeActorTypeForTask(actor.Type),
		CreatedByID:     createdByID,
		Title:           inboxTitle,
		Body:            &inboxBody,
		ActionPayload:   json.RawMessage(fmt.Sprintf(`{"reason":%q}`, strings.TrimSpace(reason))),
	}); err != nil {
		return nil, err
	}

	return blocked, nil
}

func (s *service) CreateInboxItem(ctx context.Context, req CreateInboxItemRequest) (*InboxItem, error) {
	itemType := strings.TrimSpace(req.ItemType)
	if _, ok := validInboxItemTypes[itemType]; !ok {
		return nil, ErrInboxItemTypeInvalid
	}
	if req.OrganizationID == uuid.Nil {
		return nil, ErrOrganizationIDRequired
	}
	if _, needsSourceTask := inboxItemTypesRequiringSourceTask[itemType]; needsSourceTask {
		if req.SourceTaskID == nil || *req.SourceTaskID == uuid.Nil {
			return nil, ErrSourceTaskRequired
		}
	}
	if req.SourceTaskID != nil && (req.SourceProjectID == nil || *req.SourceProjectID == uuid.Nil) {
		taskRecord, err := s.tasks.GetByID(ctx, *req.SourceTaskID)
		if err != nil {
			return nil, err
		}
		req.SourceProjectID = &taskRecord.ProjectID
	}
	if req.SourceTaskID != nil && req.SourceProjectID == nil {
		return nil, ErrSourceProjectIDRequired
	}

	createdByType := normalizeActorTypeForTask(req.CreatedByType)
	if createdByType == "" {
		createdByType = "system"
	}

	created, err := s.inbox.Create(ctx, repo.InboxItem{
		OrganizationID:  req.OrganizationID,
		TargetUserID:    req.TargetUserID,
		ItemType:        itemType,
		SourceProjectID: req.SourceProjectID,
		SourceTaskID:    req.SourceTaskID,
		CreatedByType:   createdByType,
		CreatedByID:     req.CreatedByID,
		Title:           strings.TrimSpace(req.Title),
		Body:            req.Body,
		ActionPayload:   normalizeJSON(req.ActionPayload),
		ExpiresAt:       req.ExpiresAt,
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (s *service) ActOnInboxItem(ctx context.Context, itemID, userID uuid.UUID, action string, payload json.RawMessage) error {
	if itemID == uuid.Nil {
		return ErrInboxItemNotFound
	}
	if userID == uuid.Nil {
		return ErrInboxActionForbidden
	}

	item, err := s.inbox.GetByID(ctx, itemID)
	if err != nil {
		return err
	}
	if item.TargetUserID != nil && *item.TargetUserID != userID {
		return ErrInboxActionForbidden
	}

	normalizedAction := strings.ToLower(strings.TrimSpace(action))
	switch item.ItemType {
	case "human_approval_required":
		if item.SourceTaskID == nil {
			return ErrSourceTaskRequired
		}
		switch normalizedAction {
		case "approve":
			_, err := s.ApproveTask(ctx, *item.SourceTaskID, userID)
			return err
		case "reject":
			reason := extractReason(payload)
			_, err := s.RejectTask(ctx, *item.SourceTaskID, userID, reason)
			return err
		case "dismiss":
			_, err := s.inbox.MarkActed(ctx, item.ID, userID)
			return err
		default:
			return ErrUnknownInboxAction
		}
	case "browser_handoff":
		switch normalizedAction {
		case "dismiss", "ack", "acknowledge":
			_, err := s.inbox.MarkActed(ctx, item.ID, userID)
			return err
		case "completed", "cancelled":
			if s.browserHandoffs == nil {
				return ErrBrowserHandoffUnavailable
			}
			return s.browserHandoffs.CompleteHandoffByInboxItem(ctx, item.ID, userID, normalizedAction)
		default:
			return ErrUnknownInboxAction
		}
	case "task_review":
		taskID, err := extractTaskReviewTaskID(item)
		if err != nil {
			return err
		}
		actor := Actor{Type: "human_user", ID: userID}
		switch normalizedAction {
		case "approve":
			if s.flowReviews == nil {
				return ErrFlowServiceUnavailable
			}
			if err := s.flowReviews.AdvanceTaskReview(ctx, taskID, actor); err != nil {
				return err
			}
			_, err := s.inbox.MarkActed(ctx, item.ID, userID)
			return err
		case "reject":
			if s.flowReviews == nil {
				return ErrFlowServiceUnavailable
			}
			reason := extractReason(payload)
			if err := s.flowReviews.RejectTaskReview(ctx, taskID, actor, reason); err != nil {
				return err
			}
			if err := s.recordTaskReviewRejection(ctx, taskID, userID, reason); err != nil {
				return err
			}
			_, err := s.inbox.MarkActed(ctx, item.ID, userID)
			return err
		case "dismiss", "ack", "acknowledge":
			_, err := s.inbox.MarkActed(ctx, item.ID, userID)
			return err
		default:
			return ErrUnknownInboxAction
		}
	case "blocker_filed", "comment_mention", "draft_action_review", "system_alert":
		switch normalizedAction {
		case "dismiss", "ack", "acknowledge":
			_, err := s.inbox.MarkActed(ctx, item.ID, userID)
			return err
		default:
			return ErrUnknownInboxAction
		}
	default:
		return ErrUnknownInboxItemType
	}
}

func extractTaskReviewTaskID(item repo.InboxItem) (uuid.UUID, error) {
	if len(item.ActionPayload) > 0 {
		var payload map[string]any
		if err := json.Unmarshal(item.ActionPayload, &payload); err == nil {
			if rawTaskID, ok := payload["task_id"]; ok {
				if parsedTaskID, err := uuid.Parse(strings.TrimSpace(fmt.Sprintf("%v", rawTaskID))); err == nil {
					return parsedTaskID, nil
				}
			}
		}
	}
	if item.SourceTaskID != nil && *item.SourceTaskID != uuid.Nil {
		return *item.SourceTaskID, nil
	}
	return uuid.Nil, ErrSourceTaskRequired
}

func (s *service) recordTaskReviewRejection(ctx context.Context, taskID, userID uuid.UUID, reason string) error {
	taskRecord, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"task_id": taskID,
		"reason":  strings.TrimSpace(reason),
	}
	if err := s.recordTaskEvent(ctx, taskRecord, "task.review_rejected", "human_user", &userID, payload); err != nil {
		return err
	}
	return s.publishTaskDomainEvent(ctx, taskRecord.OrganizationID, "task.review_rejected", "human_user", &userID, payload)
}

func (s *service) EnqueueForMerge(ctx context.Context, taskID uuid.UUID) (*MergeQueueEntry, error) {
	taskRecord, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if taskRecord.BranchName == nil || strings.TrimSpace(*taskRecord.BranchName) == "" {
		return nil, ErrNoTaskBranch
	}

	created, err := s.queue.Enqueue(ctx, repo.MergeQueueEntry{
		ProjectID:  taskRecord.ProjectID,
		TaskID:     taskRecord.ID,
		BranchName: strings.TrimSpace(*taskRecord.BranchName),
		Status:     "queued",
		Metadata:   json.RawMessage(`{}`),
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (s *service) DequeueFromMerge(ctx context.Context, entryID uuid.UUID, reason string) (*MergeQueueEntry, error) {
	trimmedReason := strings.TrimSpace(reason)
	var reasonPtr *string
	if trimmedReason != "" {
		reasonPtr = &trimmedReason
	}
	if _, err := s.queue.UpdateStatus(ctx, entryID, "skipped", reasonPtr, nil); err != nil {
		return nil, err
	}

	archived, err := s.queue.Archive(ctx, entryID, s.clock.Now().UTC())
	if err != nil {
		return nil, err
	}
	return &archived, nil
}

func (s *service) archiveActiveMergeEntriesForTask(ctx context.Context, taskRecord repo.ProjectTask) error {
	active, err := s.queue.ListActive(ctx, taskRecord.ProjectID)
	if err != nil {
		return err
	}

	for _, entry := range active {
		if entry.TaskID != taskRecord.ID {
			continue
		}
		if entry.ArchivedAt != nil {
			continue
		}
		if _, err := s.queue.Archive(ctx, entry.ID, s.clock.Now().UTC()); err != nil {
			return err
		}
	}

	return nil
}

func (s *service) GetMergeQueueStatus(ctx context.Context, projectID uuid.UUID) ([]*MergeQueueEntry, error) {
	if projectID == uuid.Nil {
		return nil, ErrProjectIDRequired
	}

	items, err := s.queue.ListActive(ctx, projectID)
	if err != nil {
		return nil, err
	}

	result := make([]*MergeQueueEntry, 0, len(items))
	for i := range items {
		item := items[i]
		result = append(result, &item)
	}
	return result, nil
}

func (s *service) resolvePMTargetUser(ctx context.Context, projectID uuid.UUID) (*uuid.UUID, error) {
	pm, err := s.assignments.GetPM(ctx, projectID)
	if err != nil {
		return nil, ErrPMNotAssigned
	}

	pmAgent, err := s.agents.GetByID(ctx, pm.AgentID)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(pmAgent.AgentClass, "temp") {
		return nil, nil
	}

	if strings.EqualFold(pmAgent.CreatedByType, "human_user") && pmAgent.CreatedByID != uuid.Nil {
		user, getErr := s.users.GetByID(ctx, pmAgent.CreatedByID)
		if getErr == nil && user.OrganizationID == pmAgent.OrganizationID {
			id := user.ID
			return &id, nil
		}
	}

	users, err := s.users.List(ctx, pmAgent.OrganizationID)
	if err != nil {
		return nil, err
	}
	sort.Slice(users, func(i, j int) bool {
		return users[i].CreatedAt.Before(users[j].CreatedAt)
	})
	for _, user := range users {
		role := strings.ToLower(strings.TrimSpace(user.Role))
		if (role == "admin" || role == "owner") && user.IsActive {
			id := user.ID
			return &id, nil
		}
	}

	return nil, nil
}

func (s *service) findPendingApprovalItem(ctx context.Context, taskRecord repo.ProjectTask, actedByUserID uuid.UUID) (*repo.InboxItem, error) {
	items, err := s.inbox.ListForUser(ctx, taskRecord.OrganizationID, actedByUserID, repo.InboxListOptions{
		IncludeActed: true,
		ItemType:     "human_approval_required",
		Limit:        200,
	})
	if err != nil {
		return nil, err
	}
	for i := range items {
		item := items[i]
		if item.SourceTaskID == nil || *item.SourceTaskID != taskRecord.ID {
			continue
		}
		if item.IsActed {
			continue
		}
		return &item, nil
	}
	return nil, ErrInboxItemNotFound
}

func (s *service) recordTaskEvent(ctx context.Context, taskRecord repo.ProjectTask, eventType, actorType string, actorID *uuid.UUID, payload map[string]any) error {
	return s.recordTaskEventTx(ctx, nil, taskRecord, eventType, actorType, actorID, payload)
}

func (s *service) recordTaskEventTx(ctx context.Context, tx pgx.Tx, taskRecord repo.ProjectTask, eventType, actorType string, actorID *uuid.UUID, payload map[string]any) error {
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	event := repo.ProjectTaskEvent{
		TaskID:    taskRecord.ID,
		ProjectID: taskRecord.ProjectID,
		EventType: strings.TrimSpace(eventType),
		ActorType: normalizeActorTypeForTask(actorType),
		ActorID:   actorID,
		Payload:   encodedPayload,
	}
	if tx != nil {
		recorder, ok := s.events.(taskEventRepositoryTx)
		if !ok {
			return fmt.Errorf("task event repository does not support transactions")
		}
		_, err = recorder.RecordTx(ctx, tx, event)
		return err
	}
	_, err = s.events.Record(ctx, event)
	return err
}

func (s *service) publishTaskDomainEvent(ctx context.Context, organizationID uuid.UUID, eventType, actorType string, actorID *uuid.UUID, payload map[string]any) error {
	return s.publishTaskDomainEventTx(ctx, nil, organizationID, eventType, actorType, actorID, payload)
}

func (s *service) publishTaskDomainEventTx(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, eventType, actorType string, actorID *uuid.UUID, payload map[string]any) error {
	domainActorType, domainActorID := toDomainActor(actorType, actorID)
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.eventBus.Publish(ctx, tx, eventbus.DomainEvent{
		OrganizationID: organizationID,
		EventType:      eventType,
		ActorType:      domainActorType,
		ActorID:        domainActorID,
		Payload:        encodedPayload,
	})
}

func (s *service) updateTaskTx(ctx context.Context, tx pgx.Tx, taskRecord repo.ProjectTask) (repo.ProjectTask, error) {
	if tx != nil {
		updater, ok := s.tasks.(taskRepositoryTx)
		if !ok {
			return repo.ProjectTask{}, fmt.Errorf("task repository does not support transactions")
		}
		return updater.UpdateTx(ctx, tx, taskRecord)
	}
	return s.tasks.Update(ctx, taskRecord)
}

func (s *service) updateTaskMetadata(ctx context.Context, taskRecord repo.ProjectTask) (repo.ProjectTask, error) {
	if updater, ok := s.tasks.(taskMetadataUpdater); ok {
		return updater.UpdateMetadata(ctx, taskRecord.ID, taskRecord.Metadata)
	}
	return s.tasks.Update(ctx, taskRecord)
}

func normalizeCreatedBy(actorType string, actorID uuid.UUID) (string, *uuid.UUID, error) {
	normalizedType := normalizeActorTypeForTask(actorType)
	switch normalizedType {
	case "human_user", "agent":
		if actorID == uuid.Nil {
			return "", nil, ErrCreatedByIDRequired
		}
		return normalizedType, &actorID, nil
	case "system":
		if actorID == uuid.Nil {
			return normalizedType, nil, nil
		}
		return normalizedType, &actorID, nil
	default:
		return "", nil, ErrInvalidCreatedByType
	}
}

func normalizeActionActor(actorType string, actorID uuid.UUID) (string, *uuid.UUID, error) {
	normalized := normalizeActorTypeForTask(actorType)
	switch normalized {
	case "human_user", "agent":
		if actorID == uuid.Nil {
			return "", nil, ErrCreatedByIDRequired
		}
		return normalized, &actorID, nil
	case "system":
		if actorID == uuid.Nil {
			return normalized, nil, nil
		}
		return normalized, &actorID, nil
	case "supervisor":
		return "supervisor", nil, nil
	default:
		return "", nil, ErrActorTypeInvalidForAction
	}
}

func normalizeActorTypeForTask(actorType string) string {
	switch strings.ToLower(strings.TrimSpace(actorType)) {
	case "human_user":
		return "human_user"
	case "agent":
		return "agent"
	case "system":
		return "system"
	case "supervisor":
		return "supervisor"
	default:
		return ""
	}
}

func toDomainActor(actorType string, actorID *uuid.UUID) (string, *uuid.UUID) {
	switch normalizeActorTypeForTask(actorType) {
	case "human_user":
		return "human", actorID
	case "agent":
		return "agent", actorID
	case "supervisor":
		return "supervisor", nil
	default:
		return "system", nil
	}
}

func isTransitionAllowed(fromStatus, toStatus string) bool {
	from := normalizeStatus(fromStatus)
	target := normalizeStatus(toStatus)
	allowedTargets, ok := validStatusTransitions[from]
	if !ok {
		return false
	}
	_, ok = allowedTargets[target]
	return ok
}

func allowsBootstrapGateAutoComplete(taskRecord repo.ProjectTask, from, target string, actor Actor) bool {
	if !actor.AllowBootstrapGateAutoComplete {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(actor.Type), "system") {
		return false
	}
	if normalizeStatus(from) != "draft" || normalizeStatus(target) != "done" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(taskRecord.BlocksScope), "all") {
		return false
	}
	metadata := taskMetadataMap(taskRecord.Metadata)
	bootstrapGate, _ := metadata["bootstrap_gate"].(bool)
	return bootstrapGate
}

func ValidateProjectGateTask(taskRecord repo.ProjectTask) error {
	if !projectGateNeedsExecutionPath(taskRecord) {
		return nil
	}
	if projectGateHasExecutionPath(taskRecord) {
		return nil
	}
	return ErrProjectGateExecutionPathRequired
}

func projectGateNeedsExecutionPath(taskRecord repo.ProjectTask) bool {
	if !strings.EqualFold(strings.TrimSpace(taskRecord.BlocksScope), "all") {
		return false
	}
	metadata := taskMetadataMap(taskRecord.Metadata)
	bootstrapGate, _ := metadata["bootstrap_gate"].(bool)
	return !bootstrapGate
}

func projectGateHasExecutionPath(taskRecord repo.ProjectTask) bool {
	if taskRecord.FlowTemplateID != nil && *taskRecord.FlowTemplateID != uuid.Nil {
		return true
	}
	if taskRecord.AssignedAgentID != nil && *taskRecord.AssignedAgentID != uuid.Nil {
		return true
	}
	if taskRecord.CurrentFlowNodeID != nil && *taskRecord.CurrentFlowNodeID != uuid.Nil {
		return true
	}
	return taskRecord.RequiresHumanReview
}

func looksLikeProjectContinuationMetaDraft(title string, description *string) bool {
	descriptionText := ""
	if description != nil {
		descriptionText = *description
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(title+" "+descriptionText)), " "))
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "review and promote draft tasks") {
		return true
	}
	if strings.Contains(normalized, "review and validate integration test results") {
		return true
	}
	if strings.Contains(normalized, "analyze results of integration test") {
		return true
	}
	if strings.Contains(normalized, "review and update pipeline test results") {
		return true
	}
	if strings.Contains(normalized, "review and validate pipeline integration test results") {
		return true
	}
	if strings.Contains(normalized, "review and approve e2e test results") {
		return true
	}
	if strings.Contains(normalized, "review and approve end to end test results") {
		return true
	}
	if strings.Contains(normalized, "review test results and identify issues") {
		return true
	}
	if strings.Contains(normalized, "analyze test results and document findings") {
		return true
	}
	if strings.Contains(normalized, "analyze test results and identify issues") {
		return true
	}
	if strings.Contains(normalized, "analyze test results and prepare report") {
		return true
	}
	if strings.Contains(normalized, "select and decompose next bounded task") {
		return true
	}
	if strings.Contains(normalized, "review and prepare for next phase") {
		return true
	}
	if strings.Contains(normalized, "review and promote task ") {
		return true
	}
	if strings.Contains(normalized, "prepare for next phase of project execution") {
		return true
	}
	if strings.Contains(normalized, "end to end pipeline integration test") &&
		(strings.Contains(normalized, "review") ||
			strings.Contains(normalized, "validate") ||
			strings.Contains(normalized, "verify the outcomes") ||
			strings.Contains(normalized, "analyze") ||
			strings.Contains(normalized, "inspect") ||
			strings.Contains(normalized, "update") ||
			strings.Contains(normalized, "document findings") ||
			strings.Contains(normalized, "document detailed findings") ||
			strings.Contains(normalized, "identify any issues or anomalies") ||
			strings.Contains(normalized, "identify any issues or failures") ||
			strings.Contains(normalized, "document any findings") ||
			strings.Contains(normalized, "required updates") ||
			strings.Contains(normalized, "all components are functioning correctly") ||
			strings.Contains(normalized, "results")) {
		return true
	}
	if strings.Contains(normalized, "next runnable bounded task") &&
		(strings.Contains(normalized, "decompose") ||
			strings.Contains(normalized, "assign") ||
			strings.Contains(normalized, "queue it for execution")) {
		return true
	}
	if strings.Contains(normalized, "prepare the next set of tasks") &&
		(strings.Contains(normalized, "inspect the results") ||
			strings.Contains(normalized, "integration test") ||
			strings.Contains(normalized, "next phase")) {
		return true
	}
	if strings.Contains(normalized, "transition it to the next phase") &&
		(strings.Contains(normalized, "mark as complete") ||
			strings.Contains(normalized, "review the output of task")) {
		return true
	}
	if strings.Contains(normalized, "remaining draft task") &&
		(strings.Contains(normalized, "review") ||
			strings.Contains(normalized, "promote") ||
			strings.Contains(normalized, "runnable") ||
			strings.Contains(normalized, "next phase")) {
		return true
	}
	if strings.Contains(normalized, "draft project task") &&
		(strings.Contains(normalized, "inspect") || strings.Contains(normalized, "promote")) {
		return true
	}
	return false
}

func bootstrapPlanningCreateAllowed(taskRecord repo.ProjectTask) bool {
	if !strings.EqualFold(strings.TrimSpace(taskRecord.BlocksScope), "all") {
		return false
	}
	metadata := taskMetadataMap(taskRecord.Metadata)
	bootstrapGate, _ := metadata["bootstrap_gate"].(bool)
	return bootstrapGate
}

func allowsBootstrapSetupComplete(taskRecord repo.ProjectTask, from, target string, actor Actor) bool {
	if !actor.AllowBootstrapSetupComplete {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(actor.Type), "system") {
		return false
	}
	if normalizeStatus(target) != "done" {
		return false
	}
	metadata := taskMetadataMap(taskRecord.Metadata)
	bootstrapStep, _ := metadata["bootstrap_setup_task"].(bool)
	if !bootstrapStep {
		return false
	}
	return !isTerminalTaskStatus(from)
}

func bootstrapGateCreationExempt(metadata json.RawMessage) bool {
	payload := taskMetadataMap(metadata)
	if bootstrapGate, _ := payload["bootstrap_gate"].(bool); bootstrapGate {
		return true
	}
	if bootstrapStep, _ := payload["bootstrap_setup_task"].(bool); bootstrapStep {
		return true
	}
	return false
}

func allowsCompletedChildReopen(taskRecord repo.ProjectTask, from, target string, actor Actor) bool {
	if !actor.AllowCompletedChildReopen {
		return false
	}
	switch normalizeActorTypeForTask(actor.Type) {
	case "agent", "human_user", "system":
	default:
		return false
	}
	if normalizeStatus(from) != "done" || normalizeStatus(target) != "queued" {
		return false
	}
	return taskdecomp.ParseParentTaskID(taskRecord.Metadata) != uuid.Nil
}

func taskMetadataMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	metadata := make(map[string]any)
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return map[string]any{}
	}
	return metadata
}

func normalizeStatus(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func extraPayloadString(payload map[string]any, key string) string {
	if len(payload) == 0 {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", payload[key]))
}

func applyParentIntegrationFeedback(existing json.RawMessage, feedback string, now time.Time) json.RawMessage {
	metadata := taskMetadataMap(existing)
	metadata["parent_integration_feedback"] = strings.TrimSpace(feedback)
	metadata["parent_integration_feedback_recorded_at"] = now.UTC().Format(time.RFC3339Nano)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return existing
	}
	return encoded
}

func statusRequiresFlowTemplate(status string) bool {
	switch normalizeStatus(status) {
	case "queued", "in_progress", "review", "done":
		return true
	default:
		return false
	}
}

func (s *service) validateFlowRuntimeStatus(ctx context.Context, taskRecord repo.ProjectTask, targetStatus string) error {
	target := normalizeStatus(targetStatus)
	if taskRecord.FlowTemplateID == nil {
		return nil
	}
	if target != "in_progress" && target != "review" {
		return nil
	}
	if taskRecord.CurrentFlowNodeID == nil {
		if target == "review" {
			return ErrTaskFlowStateConflict{
				TaskID:            taskRecord.ID,
				WorkStatus:        normalizeStatus(taskRecord.WorkStatus),
				TargetStatus:      target,
				CurrentFlowNodeID: nil,
				Reason:            "task cannot enter review without a current flow node",
			}
		}
		return nil
	}
	if s.flowNodes == nil {
		return ErrTaskFlowStateConflict{
			TaskID:            taskRecord.ID,
			WorkStatus:        normalizeStatus(taskRecord.WorkStatus),
			TargetStatus:      target,
			CurrentFlowNodeID: taskRecord.CurrentFlowNodeID,
			Reason:            "task flow validation requires flow node repository",
		}
	}
	currentNode, err := s.flowNodes.GetByID(ctx, *taskRecord.CurrentFlowNodeID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrTaskFlowStateConflict{
				TaskID:            taskRecord.ID,
				WorkStatus:        normalizeStatus(taskRecord.WorkStatus),
				TargetStatus:      target,
				CurrentFlowNodeID: taskRecord.CurrentFlowNodeID,
				Reason:            "task current flow node is missing",
			}
		}
		return err
	}
	expected := expectedTaskRuntimeStatusForNode(currentNode)
	if target == expected {
		return nil
	}
	return ErrTaskFlowStateConflict{
		TaskID:            taskRecord.ID,
		WorkStatus:        normalizeStatus(taskRecord.WorkStatus),
		TargetStatus:      target,
		CurrentFlowNodeID: taskRecord.CurrentFlowNodeID,
		CurrentNodeType:   normalizeStatus(currentNode.NodeType),
		Reason:            fmt.Sprintf("task cannot enter %s while current flow node %s requires %s", target, normalizeStatus(currentNode.NodeType), expected),
	}
}

func expectedTaskRuntimeStatusForNode(node repo.FlowNode) string {
	if strings.EqualFold(strings.TrimSpace(node.NodeType), "review") || node.RequiresHumanReview {
		return "review"
	}
	return "in_progress"
}

func (s *service) validateExecutableFlowTemplate(ctx context.Context, flowTemplateID uuid.UUID) error {
	if flowTemplateID == uuid.Nil {
		return ErrFlowTemplateRequired
	}
	if s.flowNodes == nil {
		return fmt.Errorf("task service flow-template validation requires flow node repository")
	}
	if s.flowTemplates == nil {
		return fmt.Errorf("task service flow-template validation requires flow template repository")
	}

	template, err := s.flowTemplates.GetByID(ctx, flowTemplateID)
	if err != nil {
		return fmt.Errorf("load flow template: %w", err)
	}
	nodes, err := s.flowNodes.GetByTemplateOrdered(ctx, flowTemplateID)
	if err != nil {
		return fmt.Errorf("load flow template nodes: %w", err)
	}
	return flowpolicy.ValidateExecutableFlowTemplate(template.StartNodeID, nodes)
}

func normalizeJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`{}`)
	}
	return value
}

func normalizeBlocksScope(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none":
		return "none"
	case "all":
		return "all"
	default:
		return ""
	}
}

func (s *service) shouldNormalizeOrchestrationParentTask(ctx context.Context, title string, metadata json.RawMessage, flowTemplateID *uuid.UUID) bool {
	normalizedTitle := strings.ToLower(strings.TrimSpace(title))
	if strings.Contains(normalizedTitle, "parent orchestration") ||
		strings.Contains(normalizedTitle, "master orchestration") ||
		strings.Contains(normalizedTitle, "parent workstream") {
		return true
	}
	if flowTemplateID != nil && *flowTemplateID != uuid.Nil && s.flowTemplates != nil {
		template, err := s.flowTemplates.GetByID(ctx, *flowTemplateID)
		if err == nil {
			slug := strings.ToLower(strings.TrimSpace(template.Slug))
			displayName := strings.ToLower(strings.TrimSpace(template.DisplayName))
			if strings.Contains(slug, "orchestration") || strings.Contains(displayName, "orchestration") {
				return true
			}
		}
	}
	plan, ok := taskplan.Parse(metadata)
	if !ok {
		return false
	}
	stopReason := strings.ToLower(strings.TrimSpace(plan.FollowOnStopReason))
	return strings.Contains(stopReason, "parent task is orchestration-only")
}

func extractReason(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return ""
	}
	raw, _ := parsed["reason"].(string)
	return strings.TrimSpace(raw)
}

func pointerUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	copyID := id
	return &copyID
}

func pointerString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func recoveryCheckpointPayload(checkpoint taskcheckpoint.RecoveryFileWriteCheckpoint) map[string]any {
	checkpoint = taskcheckpoint.NormalizeRecoveryFileWriteCheckpoint(checkpoint)
	payload := map[string]any{}
	if blockerClass := taskcheckpoint.RecoveryFileWriteBlockerClass(&checkpoint); blockerClass != "" {
		payload["recovery_blocker_class"] = blockerClass
	}
	if targetPath := strings.TrimSpace(checkpoint.TargetPath); targetPath != "" {
		payload["recovery_checkpoint_target_path"] = targetPath
	}
	if artifactPath := strings.TrimSpace(checkpoint.ArtifactPath); artifactPath != "" {
		payload["recovery_checkpoint_artifact_path"] = artifactPath
	}
	if failureReason := strings.TrimSpace(checkpoint.FailureReason); failureReason != "" {
		payload["recovery_checkpoint_failure_reason"] = failureReason
	}
	if len(checkpoint.PriorFailureReasons) != 0 {
		payload["recovery_checkpoint_prior_failure_reasons"] = append([]string(nil), checkpoint.PriorFailureReasons...)
	}
	return payload
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

func projectRequiresHumanReview(settings json.RawMessage) bool {
	if len(settings) == 0 {
		return false
	}
	var parsed map[string]any
	if err := json.Unmarshal(settings, &parsed); err != nil {
		return false
	}
	if direct, ok := parsed["requires_human_review"].(bool); ok {
		return direct
	}
	// Thin-spec judgment:
	// PM scope storage is not explicitly modeled yet, so support common nested keys.
	for _, key := range []string{"project_manager", "pm", "pm_scope", "pm_scopes"} {
		raw, ok := parsed[key]
		if !ok {
			continue
		}
		nested, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if flag, ok := nested["requires_human_review"].(bool); ok {
			return flag
		}
	}
	return false
}
