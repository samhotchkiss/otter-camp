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
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
)

var (
	ErrOrganizationIDRequired     = errors.New("organization_id is required")
	ErrProjectIDRequired          = errors.New("project_id is required")
	ErrTaskIDRequired             = errors.New("task_id is required")
	ErrTitleRequired              = errors.New("title is required")
	ErrInvalidCreatedByType       = errors.New("created_by_type must be human_user, agent, or system")
	ErrCreatedByIDRequired        = errors.New("created_by_id is required for human_user and agent")
	ErrAgentNotAssigned           = errors.New("assigned_agent_id is not actively assigned to the project")
	ErrHumanReviewNotRequired     = errors.New("task does not require human review")
	ErrInboxItemNotFound          = errors.New("inbox item not found")
	ErrInboxActionForbidden       = errors.New("inbox action forbidden for this user")
	ErrUnknownInboxItemType       = errors.New("unknown inbox item type")
	ErrUnknownInboxAction         = errors.New("unknown inbox action")
	ErrNoTaskBranch               = errors.New("task has no branch name")
	ErrPMNotAssigned              = errors.New("project has no active PM assignment")
	ErrInboxItemTypeInvalid       = errors.New("invalid inbox item type")
	ErrSourceTaskRequired         = errors.New("source_task_id is required for this inbox item type")
	ErrSourceProjectIDRequired    = errors.New("source_project_id is required for this inbox item type")
	ErrRequiresHumanApproval      = errors.New("task requires human approval before queueing")
	ErrFlowTemplateRequired       = errors.New("task requires a flow template before it can be queued")
	ErrFlowTemplateReviewRequired = errors.New("flow template must include at least one review node")
	ErrDoneRequiresTerminalFlow   = errors.New("task can only be marked done when its flow reaches a terminal node")
	ErrTransitionTargetRequired   = errors.New("target status is required")
	ErrActorTypeInvalidForAction  = errors.New("actor_type is invalid for action")
	ErrBrowserHandoffUnavailable  = errors.New("browser handoff service unavailable")
	ErrActiveFlowRequired         = errors.New("task requires an active flow to be in_progress")
	ErrFlowServiceUnavailable     = errors.New("flow service unavailable")
)

var validStatusTransitions = map[string]map[string]struct{}{
	"draft": {
		"queued":    {},
		"cancelled": {},
	},
	"queued": {
		"in_progress": {},
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
		"queued":    {},
		"cancelled": {},
	},
	"on_hold": {
		"in_progress": {},
		"cancelled":   {},
	},
	"review": {
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

type Actor struct {
	Type              string
	ID                uuid.UUID
	AllowNoActiveFlow bool
	AllowDoneBypass   bool
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

type ProjectTask = repo.ProjectTask
type ProjectTaskEvent = repo.ProjectTaskEvent
type InboxItem = repo.InboxItem
type MergeQueueEntry = repo.MergeQueueEntry

type CreateTaskRequest struct {
	ProjectID       uuid.UUID
	Title           string
	Description     *string
	FlowTemplateID  *uuid.UUID
	ScheduleID      *uuid.UUID
	BlocksScope     string
	AssignedAgentID *uuid.UUID
	CreatedByType   string
	CreatedByID     uuid.UUID
	Metadata        json.RawMessage
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
	Update(ctx context.Context, task repo.ProjectTask) (repo.ProjectTask, error)
}

type taskEventRepository interface {
	Record(ctx context.Context, event repo.ProjectTaskEvent) (repo.ProjectTaskEvent, error)
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

	Assignments agentProjectAssignmentRepository
	Agents      agentRepository
	Users       humanUserRepository
	Executions  flowExecutionRepository
	FlowNodes   flowNodeRepository

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

	assignments agentProjectAssignmentRepository
	agents      agentRepository
	users       humanUserRepository
	executions  flowExecutionRepository
	flowNodes   flowNodeRepository

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
		hasReviewNode, reviewErr := s.flowTemplateHasReviewNode(ctx, *req.FlowTemplateID)
		if reviewErr != nil {
			return nil, reviewErr
		}
		if !hasReviewNode {
			return nil, ErrFlowTemplateReviewRequired
		}
	}

	blocksScope := normalizeBlocksScope(req.BlocksScope)
	if blocksScope == "" {
		return nil, ErrInvalidBlocksScope
	}

	if req.AssignedAgentID != nil {
		assignment, getErr := s.assignments.GetByAgentAndProject(ctx, *req.AssignedAgentID, req.ProjectID)
		if getErr != nil || !assignment.IsActive {
			return nil, ErrAgentNotAssigned
		}
	}

	requiresHumanReview := projectRequiresHumanReview(projectRecord.Settings)
	created, err := s.tasks.Create(ctx, repo.ProjectTask{
		OrganizationID:      projectRecord.OrganizationID,
		ProjectID:           req.ProjectID,
		Title:               strings.TrimSpace(req.Title),
		Description:         req.Description,
		WorkStatus:          "draft",
		BlocksScope:         blocksScope,
		FlowTemplateID:      req.FlowTemplateID,
		ScheduleID:          req.ScheduleID,
		RequiresHumanReview: requiresHumanReview,
		CreatedByType:       createdByType,
		CreatedByID:         createdByID,
		AssignedAgentID:     req.AssignedAgentID,
		Metadata:            normalizeJSON(req.Metadata),
	})
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

func (s *service) TransitionStatus(ctx context.Context, taskID uuid.UUID, toStatus string, actor Actor) (*ProjectTask, error) {
	return s.transitionStatus(ctx, taskID, toStatus, actor, nil, false)
}

func (s *service) transitionStatus(ctx context.Context, taskID uuid.UUID, toStatus string, actor Actor, extraPayload map[string]any, approvalOverride bool) (*ProjectTask, error) {
	if taskID == uuid.Nil {
		return nil, ErrTaskIDRequired
	}

	taskRecord, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}

	from := normalizeStatus(taskRecord.WorkStatus)
	target := normalizeStatus(toStatus)
	if target == "" {
		return nil, ErrTransitionTargetRequired
	}
	if !isTransitionAllowed(from, target) {
		return nil, ErrInvalidStatusTransition{From: from, To: target}
	}
	if target == "queued" && taskRecord.RequiresHumanReview && !approvalOverride {
		return nil, ErrRequiresHumanApproval
	}
	if statusRequiresFlowTemplate(target) && taskRecord.FlowTemplateID == nil {
		return nil, ErrFlowTemplateRequired
	}
	if target == "queued" && taskRecord.FlowTemplateID != nil {
		hasReviewNode, reviewErr := s.flowTemplateHasReviewNode(ctx, *taskRecord.FlowTemplateID)
		if reviewErr != nil {
			return nil, reviewErr
		}
		if !hasReviewNode {
			return nil, ErrFlowTemplateReviewRequired
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
	if target == "in_progress" {
		allowNoActiveFlow := actor.AllowNoActiveFlow && strings.EqualFold(strings.TrimSpace(actor.Type), "system")
		hasActiveFlow, flowErr := s.hasActiveFlowExecution(ctx, taskRecord.ID)
		if flowErr != nil {
			return nil, flowErr
		}
		if !hasActiveFlow && !allowNoActiveFlow {
			return nil, ErrInProgressRequiresActiveFlow{TaskID: taskRecord.ID}
		}
	}
	if target == "done" && !actor.AllowDoneBypass {
		if err := s.validateDoneTransition(ctx, taskRecord); err != nil {
			return nil, err
		}
	}

	taskRecord.WorkStatus = target
	if target == "done" || target == "cancelled" {
		completed := s.clock.Now().UTC()
		taskRecord.CompletedAt = &completed
	} else {
		taskRecord.CompletedAt = nil
	}

	updated, err := s.tasks.Update(ctx, taskRecord)
	if err != nil {
		return nil, err
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

	if err := s.recordTaskEvent(ctx, updated, "status.changed", actorType, actorID, payload); err != nil {
		return nil, err
	}
	if err := s.publishTaskDomainEvent(ctx, updated.OrganizationID, "task.status_changed", actorType, actorID, payload); err != nil {
		return nil, err
	}

	return &updated, nil
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

	return queueDecompositionResult{
		applied:      true,
		childTaskIDs: childTaskIDs,
	}, nil
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

func (s *service) validateDoneTransition(ctx context.Context, taskRecord repo.ProjectTask) error {
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
	for _, row := range rows {
		if row.FlowNodeID != node.ID {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(row.Status), "completed") {
			return nil
		}
	}
	return ErrDoneRequiresTerminalFlow
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
	blocked, err := s.transitionStatus(ctx, taskID, "blocked", actor, map[string]any{
		"blocker_reason": strings.TrimSpace(reason),
	}, false)
	if err != nil {
		return nil, err
	}

	projectRecord, err := s.project.GetByID(ctx, blocked.ProjectID)
	if err != nil {
		return nil, err
	}

	pmAssignment, err := s.assignments.GetPM(ctx, blocked.ProjectID)
	if err != nil {
		return nil, ErrPMNotAssigned
	}
	if blocked.FlowTemplateID != nil {
		hasReviewNode, reviewErr := s.flowTemplateHasReviewNode(ctx, *blocked.FlowTemplateID)
		if reviewErr != nil {
			return nil, reviewErr
		}
		if !hasReviewNode {
			return nil, ErrFlowTemplateReviewRequired
		}
	}

	title := fmt.Sprintf("Resolve blocker for task %s-%d", projectRecord.Slug, blocked.TaskNumber)
	createdByID := pointerUUID(actor.ID)
	if strings.TrimSpace(actor.Type) == "system" {
		createdByID = nil
	}
	resolutionTask, createErr := s.tasks.Create(ctx, repo.ProjectTask{
		OrganizationID:      blocked.OrganizationID,
		ProjectID:           blocked.ProjectID,
		Title:               title,
		Description:         pointerString("Automatically created to resolve blocker"),
		WorkStatus:          "queued",
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
			if err := s.resumeTaskFromReview(ctx, taskID, actor); err != nil {
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
			if err := s.resumeTaskFromReview(ctx, taskID, actor); err != nil {
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

func (s *service) resumeTaskFromReview(ctx context.Context, taskID uuid.UUID, actor Actor) error {
	taskRecord, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "review") {
		if _, err := s.transitionStatus(ctx, taskID, "in_progress", actor, nil, false); err != nil {
			return err
		}
	}
	return nil
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
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.events.Record(ctx, repo.ProjectTaskEvent{
		TaskID:    taskRecord.ID,
		ProjectID: taskRecord.ProjectID,
		EventType: strings.TrimSpace(eventType),
		ActorType: normalizeActorTypeForTask(actorType),
		ActorID:   actorID,
		Payload:   encodedPayload,
	})
	return err
}

func (s *service) publishTaskDomainEvent(ctx context.Context, organizationID uuid.UUID, eventType, actorType string, actorID *uuid.UUID, payload map[string]any) error {
	domainActorType, domainActorID := toDomainActor(actorType, actorID)
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.eventBus.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: organizationID,
		EventType:      eventType,
		ActorType:      domainActorType,
		ActorID:        domainActorID,
		Payload:        encodedPayload,
	})
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
	if target == "cancelled" {
		return true
	}
	_, ok = allowedTargets[target]
	return ok
}

func normalizeStatus(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func statusRequiresFlowTemplate(status string) bool {
	switch normalizeStatus(status) {
	case "queued", "in_progress", "review", "done":
		return true
	default:
		return false
	}
}

func (s *service) flowTemplateHasReviewNode(ctx context.Context, flowTemplateID uuid.UUID) (bool, error) {
	if flowTemplateID == uuid.Nil {
		return false, ErrFlowTemplateRequired
	}
	if s.flowNodes == nil {
		return false, fmt.Errorf("task service flow-template validation requires flow node repository")
	}

	nodes, err := s.flowNodes.GetByTemplateOrdered(ctx, flowTemplateID)
	if err != nil {
		return false, fmt.Errorf("load flow template nodes: %w", err)
	}
	if len(nodes) == 0 {
		return false, nil
	}
	for _, node := range nodes {
		if strings.EqualFold(strings.TrimSpace(node.NodeType), "review") {
			return true, nil
		}
	}
	return false, nil
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
