package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
)

const maxDependencyTraversalDepth = 100

var (
	ErrTaskIDRequired            = errors.New("task_id is required")
	ErrFlowNotStarted            = errors.New("task has no active flow node")
	ErrNoRejectionPath           = errors.New("current flow node does not define a rejection path")
	ErrMaxVisitsExceeded         = errors.New("max visits exceeded")
	ErrCrossLevelDependency      = errors.New("cross-level dependencies are not allowed")
	ErrSelfDependency            = errors.New("self dependency is not allowed")
	ErrCyclicDependency          = errors.New("dependency introduces a cycle")
	ErrDAGTooDeep                = errors.New("dependency graph traversal exceeded depth limit")
	ErrSubtaskIDRequired         = errors.New("subtask_id is required")
	ErrFlowNodeExecutionRequired = errors.New("flow_node_execution_id is required")
	ErrInvalidSubtaskTransition  = errors.New("invalid subtask status transition")
	ErrAgentNotFound             = errors.New("agent not found")
	ErrUserNotFound              = errors.New("user not found")
	ErrSelfReviewForbidden       = errors.New("review actor must differ from the actor who completed the work being reviewed")
)

type Actor struct {
	Type string
	ID   uuid.UUID
}

type SubtaskAssignee struct {
	Type *string
	ID   *uuid.UUID
}

type AddDependencyRequest struct {
	SourceType    string
	SourceID      uuid.UUID
	DependsOnType string
	DependsOnID   uuid.UUID
	CreatedByType string
	CreatedByID   *uuid.UUID
}

type taskCoordinator interface {
	TransitionStatus(ctx context.Context, taskID uuid.UUID, toStatus string, actor tasksvc.Actor) (*tasksvc.ProjectTask, error)
	MarkBlocked(ctx context.Context, taskID uuid.UUID, reason string, actor tasksvc.Actor) (*tasksvc.ProjectTask, error)
}

type flowTemplateRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowTemplate, error)
}

type flowNodeRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowNode, error)
}

type flowNodeExecutionRepository interface {
	Create(ctx context.Context, execution repo.FlowNodeExecution) (repo.FlowNodeExecution, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowNodeExecution, error)
	GetActive(ctx context.Context, taskID, flowNodeID uuid.UUID) (repo.FlowNodeExecution, error)
	ListByTask(ctx context.Context, taskID uuid.UUID) ([]repo.FlowNodeExecution, error)
	Complete(ctx context.Context, id uuid.UUID) (repo.FlowNodeExecution, error)
	Reject(ctx context.Context, id uuid.UUID) (repo.FlowNodeExecution, error)
	RecordCommitSHA(ctx context.Context, id uuid.UUID, commitSHA string) (repo.FlowNodeExecution, error)
	UpdateMetadata(ctx context.Context, id uuid.UUID, metadata json.RawMessage) (repo.FlowNodeExecution, error)
}

type projectTaskRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
	SetFlowNode(ctx context.Context, id uuid.UUID, flowNodeID *uuid.UUID) (repo.ProjectTask, error)
	SetBranch(ctx context.Context, id uuid.UUID, branchName *string) (repo.ProjectTask, error)
}

type projectRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

type projectSubtaskRepository interface {
	Create(ctx context.Context, subtask repo.ProjectSubtask) (repo.ProjectSubtask, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectSubtask, error)
	ListByExecution(ctx context.Context, flowNodeExecutionID uuid.UUID) ([]repo.ProjectSubtask, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) (repo.ProjectSubtask, error)
	NextSequenceNumber(ctx context.Context, flowNodeExecutionID uuid.UUID) (int, error)
}

type projectTaskDependencyRepository interface {
	Add(ctx context.Context, dependency repo.ProjectTaskDependency) (repo.ProjectTaskDependency, error)
	Remove(ctx context.Context, id uuid.UUID) error
	ListOutbound(ctx context.Context, sourceType string, sourceID uuid.UUID) ([]repo.ProjectTaskDependency, error)
	ListInbound(ctx context.Context, dependsOnType string, dependsOnID uuid.UUID) ([]repo.ProjectTaskDependency, error)
}

type inboxRepository interface {
	Create(ctx context.Context, item repo.InboxItem) (repo.InboxItem, error)
}

type taskEventRepository interface {
	Record(ctx context.Context, event repo.ProjectTaskEvent) (repo.ProjectTaskEvent, error)
}

type eventBus interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
	Subscribe(consumerName string, orgID *uuid.UUID, handler eventbus.EventHandler) eventbus.Subscription
}

type subtaskAgentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

type subtaskUserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
}

type flowSessionBridge interface {
	EnsureNodeSession(ctx context.Context, execution repo.FlowNodeExecution) (repo.ChatSession, error)
	RecordCommitSHA(ctx context.Context, flowNodeExecutionID uuid.UUID, commitSHA string) error
}

type Options struct {
	Pool *pgxpool.Pool

	FlowTemplates flowTemplateRepository
	FlowNodes     flowNodeRepository
	Executions    flowNodeExecutionRepository
	Tasks         projectTaskRepository
	Projects      projectRepository
	Subtasks      projectSubtaskRepository
	Dependencies  projectTaskDependencyRepository
	Inbox         inboxRepository
	TaskEvents    taskEventRepository
	TasksService  taskCoordinator
	Events        eventBus
	Agents        subtaskAgentRepository
	Users         subtaskUserRepository
	SessionBridge flowSessionBridge
}

type FlowExecutionService interface {
	StartFlow(ctx context.Context, taskID uuid.UUID) (*repo.FlowNodeExecution, error)
	AdvanceFlow(ctx context.Context, taskID uuid.UUID, actor Actor) (*repo.FlowNodeExecution, error)
	RejectFlowNode(ctx context.Context, taskID uuid.UUID, actor Actor) (*repo.FlowNodeExecution, error)
	RecordNodeCommit(ctx context.Context, taskID uuid.UUID, commitSHA, branchName string) (*repo.FlowNodeExecution, error)

	CreateSubtask(ctx context.Context, flowNodeExecutionID uuid.UUID, title string, description *string, assignee SubtaskAssignee) (*repo.ProjectSubtask, error)
	UpdateSubtaskStatus(ctx context.Context, subtaskID uuid.UUID, newStatus string) (*repo.ProjectSubtask, error)
	ListSubtasks(ctx context.Context, flowNodeExecutionID uuid.UUID) ([]repo.ProjectSubtask, error)

	AddDependency(ctx context.Context, req AddDependencyRequest) (*repo.ProjectTaskDependency, error)
	RemoveDependency(ctx context.Context, sourceType string, sourceID uuid.UUID, dependsOnType string, dependsOnID uuid.UUID) error
	OnTaskCancelled(ctx context.Context, taskID uuid.UUID) error
	SubscribeTaskCancellationHandler(orgID *uuid.UUID) eventbus.Subscription
}

type service struct {
	flowTemplates flowTemplateRepository
	flowNodes     flowNodeRepository
	executions    flowNodeExecutionRepository
	tasks         projectTaskRepository
	projects      projectRepository
	subtasks      projectSubtaskRepository
	dependencies  projectTaskDependencyRepository
	inbox         inboxRepository
	taskEvents    taskEventRepository
	taskService   taskCoordinator
	events        eventBus
	agents        subtaskAgentRepository
	users         subtaskUserRepository
	sessionBridge flowSessionBridge
}

type taskReviewFlowAdapter struct {
	flow FlowExecutionService
}

func (a taskReviewFlowAdapter) AdvanceTaskReview(ctx context.Context, taskID uuid.UUID, actor tasksvc.Actor) error {
	_, err := a.flow.AdvanceFlow(ctx, taskID, Actor{Type: actor.Type, ID: actor.ID})
	return err
}

func (a taskReviewFlowAdapter) RejectTaskReview(ctx context.Context, taskID uuid.UUID, actor tasksvc.Actor, _ string) error {
	_, err := a.flow.RejectFlowNode(ctx, taskID, Actor{Type: actor.Type, ID: actor.ID})
	return err
}

func NewService(opts Options) (FlowExecutionService, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("flow execution service requires a database pool")
	}
	if opts.Events == nil {
		return nil, fmt.Errorf("flow execution service requires an event bus")
	}

	svc := &service{
		events: opts.Events,
	}
	var flowReviewBindTarget any
	if opts.FlowTemplates != nil {
		svc.flowTemplates = opts.FlowTemplates
	} else {
		svc.flowTemplates = repo.NewFlowTemplateRepo(opts.Pool)
	}
	if opts.FlowNodes != nil {
		svc.flowNodes = opts.FlowNodes
	} else {
		svc.flowNodes = repo.NewFlowNodeRepo(opts.Pool)
	}
	if opts.Executions != nil {
		svc.executions = opts.Executions
	} else {
		svc.executions = repo.NewFlowNodeExecutionRepo(opts.Pool)
	}
	if opts.Tasks != nil {
		svc.tasks = opts.Tasks
	} else {
		svc.tasks = repo.NewProjectTaskRepo(opts.Pool)
	}
	if opts.Projects != nil {
		svc.projects = opts.Projects
	} else {
		svc.projects = repo.NewProjectRepo(opts.Pool)
	}
	if opts.Subtasks != nil {
		svc.subtasks = opts.Subtasks
	} else {
		svc.subtasks = repo.NewProjectSubtaskRepo(opts.Pool)
	}
	if opts.Dependencies != nil {
		svc.dependencies = opts.Dependencies
	} else {
		svc.dependencies = repo.NewProjectTaskDependencyRepo(opts.Pool)
	}
	if opts.Inbox != nil {
		svc.inbox = opts.Inbox
	} else {
		svc.inbox = repo.NewInboxItemRepo(opts.Pool)
	}
	if opts.TaskEvents != nil {
		svc.taskEvents = opts.TaskEvents
	} else {
		svc.taskEvents = repo.NewProjectTaskEventRepo(opts.Pool)
	}
	if opts.TasksService != nil {
		svc.taskService = opts.TasksService
		flowReviewBindTarget = opts.TasksService
	} else {
		taskService, err := tasksvc.NewService(tasksvc.Options{
			Pool:     opts.Pool,
			EventBus: opts.Events,
		})
		if err != nil {
			return nil, err
		}
		svc.taskService = taskService
		flowReviewBindTarget = taskService
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
	svc.sessionBridge = opts.SessionBridge
	tasksvc.AttachFlowReviewActions(flowReviewBindTarget, taskReviewFlowAdapter{flow: svc})

	return svc, nil
}

func (s *service) StartFlow(ctx context.Context, taskID uuid.UUID) (*repo.FlowNodeExecution, error) {
	if taskID == uuid.Nil {
		return nil, ErrTaskIDRequired
	}

	taskRecord, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if taskRecord.FlowTemplateID == nil {
		return nil, ErrFlowNotStarted
	}

	template, err := s.flowTemplates.GetByID(ctx, *taskRecord.FlowTemplateID)
	if err != nil {
		return nil, err
	}
	if template.StartNodeID == nil {
		return nil, ErrFlowNotStarted
	}

	if _, err := s.tasks.SetFlowNode(ctx, taskRecord.ID, template.StartNodeID); err != nil {
		return nil, err
	}

	execution, err := s.executions.Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  *template.StartNodeID,
		VisitNumber: 1,
		Status:      "active",
	})
	if err != nil {
		return nil, err
	}
	if err := s.ensureExecutionSession(ctx, &execution); err != nil {
		return nil, err
	}

	payload := map[string]any{
		"task_id":           taskRecord.ID,
		"project_id":        taskRecord.ProjectID,
		"flow_node_id":      execution.FlowNodeID,
		"flow_execution_id": execution.ID,
	}
	if err := s.publishDomainEvent(ctx, taskRecord.OrganizationID, "flow.started", "system", nil, payload); err != nil {
		return nil, err
	}

	return &execution, nil
}

func (s *service) AdvanceFlow(ctx context.Context, taskID uuid.UUID, actor Actor) (*repo.FlowNodeExecution, error) {
	taskRecord, currentNode, activeExecution, err := s.loadActiveFlowState(ctx, taskID)
	if err != nil {
		return nil, err
	}

	if currentNode.RequiresHumanReview && taskRecord.WorkStatus != "review" {
		if _, err := s.taskService.TransitionStatus(ctx, taskRecord.ID, "review", toTaskActor(actor)); err != nil {
			return nil, err
		}

		if err := s.createTaskReviewInbox(ctx, taskRecord, currentNode.ID); err != nil {
			return nil, err
		}
		return &activeExecution, nil
	}

	if err := s.validateReviewAdvanceActor(ctx, taskRecord.ID, currentNode, activeExecution, actor); err != nil {
		return nil, err
	}
	if currentNode.NextNodeID == nil {
		if err := s.validateTerminalAdvance(ctx, taskRecord.ID, activeExecution, currentNode, actor); err != nil {
			return nil, err
		}
	}
	if _, err := s.executions.UpdateMetadata(ctx, activeExecution.ID, executionMetadataWithCompletedBy(activeExecution.Metadata, actor)); err != nil {
		return nil, err
	}

	completedExecution, err := s.executions.Complete(ctx, activeExecution.ID)
	if err != nil {
		return nil, err
	}

	var nextExecution *repo.FlowNodeExecution
	if currentNode.NextNodeID == nil {
		if _, err := s.taskService.TransitionStatus(ctx, taskRecord.ID, "done", toTaskActor(actor)); err != nil {
			return nil, err
		}
		if _, err := s.tasks.SetFlowNode(ctx, taskRecord.ID, nil); err != nil {
			return nil, err
		}
	} else {
		if _, err := s.tasks.SetFlowNode(ctx, taskRecord.ID, currentNode.NextNodeID); err != nil {
			return nil, err
		}
		created, createErr := s.executions.Create(ctx, repo.FlowNodeExecution{
			TaskID:      taskRecord.ID,
			FlowNodeID:  *currentNode.NextNodeID,
			VisitNumber: 1,
			Status:      "active",
		})
		if createErr != nil {
			return nil, createErr
		}
		if err := s.ensureExecutionSession(ctx, &created); err != nil {
			return nil, err
		}
		nextExecution = &created

		nextNode, nodeErr := s.flowNodes.GetByID(ctx, *currentNode.NextNodeID)
		if nodeErr != nil {
			return nil, nodeErr
		}
		if nextNode.RequiresHumanReview {
			if _, statusErr := s.taskService.TransitionStatus(ctx, taskRecord.ID, "review", toTaskActor(actor)); statusErr != nil {
				return nil, statusErr
			}
			if err := s.createTaskReviewInbox(ctx, taskRecord, nextNode.ID); err != nil {
				return nil, err
			}
		}
	}

	payload := map[string]any{
		"task_id":                  taskRecord.ID,
		"project_id":               taskRecord.ProjectID,
		"from_flow_node_id":        currentNode.ID,
		"from_flow_execution_id":   completedExecution.ID,
		"to_flow_node_id":          currentNode.NextNodeID,
		"to_flow_execution_id":     nilUUID(nextExecution),
		"terminal_transition_done": currentNode.NextNodeID == nil,
	}
	if err := s.recordTaskEvent(ctx, taskRecord, "flow.advanced", actor, payload); err != nil {
		return nil, err
	}
	if err := s.publishDomainEvent(ctx, taskRecord.OrganizationID, "flow.advanced", actor.Type, actorIDPtr(actor), payload); err != nil {
		return nil, err
	}

	if nextExecution != nil {
		return nextExecution, nil
	}
	return &completedExecution, nil
}

func (s *service) createTaskReviewInbox(ctx context.Context, taskRecord repo.ProjectTask, flowNodeID uuid.UUID) error {
	title := "Task review required"
	body := "Flow advancement paused pending human review."
	payload, _ := json.Marshal(map[string]any{
		"task_id":      taskRecord.ID,
		"flow_node_id": flowNodeID,
	})
	created, err := s.inbox.Create(ctx, repo.InboxItem{
		OrganizationID:  taskRecord.OrganizationID,
		ItemType:        "task_review",
		SourceProjectID: &taskRecord.ProjectID,
		SourceTaskID:    &taskRecord.ID,
		CreatedByType:   "system",
		Title:           title,
		Body:            &body,
		ActionPayload:   payload,
	})
	if err != nil {
		return err
	}
	_ = s.publishDomainEvent(ctx, taskRecord.OrganizationID, "inbox.item_created", "system", nil, map[string]any{
		"inbox_item_id": created.ID,
		"item_type":     created.ItemType,
	})
	return nil
}

func (s *service) RejectFlowNode(ctx context.Context, taskID uuid.UUID, actor Actor) (*repo.FlowNodeExecution, error) {
	taskRecord, currentNode, activeExecution, err := s.loadActiveFlowState(ctx, taskID)
	if err != nil {
		return nil, err
	}

	if _, err := s.executions.UpdateMetadata(ctx, activeExecution.ID, executionMetadataWithCompletedBy(activeExecution.Metadata, actor)); err != nil {
		return nil, err
	}
	if _, err := s.executions.Reject(ctx, activeExecution.ID); err != nil {
		return nil, err
	}
	if currentNode.RejectNodeID == nil {
		return nil, ErrNoRejectionPath
	}

	rejectNode, err := s.flowNodes.GetByID(ctx, *currentNode.RejectNodeID)
	if err != nil {
		return nil, err
	}

	nextVisit, err := s.nextVisitNumber(ctx, taskRecord.ID, rejectNode.ID)
	if err != nil {
		return nil, err
	}
	if nextVisit > rejectNode.MaxVisits {
		if _, markErr := s.taskService.MarkBlocked(ctx, taskRecord.ID, "flow rejection max visits exceeded", toTaskActor(actor)); markErr != nil {
			return nil, markErr
		}
		return nil, ErrMaxVisitsExceeded
	}

	if _, err := s.tasks.SetFlowNode(ctx, taskRecord.ID, currentNode.RejectNodeID); err != nil {
		return nil, err
	}

	created, err := s.executions.Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  rejectNode.ID,
		VisitNumber: nextVisit,
		Status:      "active",
	})
	if err != nil {
		return nil, err
	}
	if err := s.ensureExecutionSession(ctx, &created); err != nil {
		return nil, err
	}

	payload := map[string]any{
		"task_id":               taskRecord.ID,
		"project_id":            taskRecord.ProjectID,
		"from_flow_node_id":     currentNode.ID,
		"to_flow_node_id":       rejectNode.ID,
		"to_flow_execution_id":  created.ID,
		"to_flow_visit_number":  created.VisitNumber,
		"rejected_execution_id": activeExecution.ID,
	}
	if err := s.recordTaskEvent(ctx, taskRecord, "flow.rejected", actor, payload); err != nil {
		return nil, err
	}
	if err := s.publishDomainEvent(ctx, taskRecord.OrganizationID, "flow.rejected", actor.Type, actorIDPtr(actor), payload); err != nil {
		return nil, err
	}

	return &created, nil
}

func (s *service) RecordNodeCommit(ctx context.Context, taskID uuid.UUID, commitSHA, branchName string) (*repo.FlowNodeExecution, error) {
	taskRecord, _, activeExecution, err := s.loadActiveFlowState(ctx, taskID)
	if err != nil {
		return nil, err
	}

	trimmedCommit := strings.TrimSpace(commitSHA)
	if s.sessionBridge != nil {
		if err := s.sessionBridge.RecordCommitSHA(ctx, activeExecution.ID, trimmedCommit); err != nil {
			return nil, err
		}
	} else {
		if _, err := s.executions.RecordCommitSHA(ctx, activeExecution.ID, trimmedCommit); err != nil {
			return nil, err
		}
	}

	updatedExecution, err := s.executions.GetByID(ctx, activeExecution.ID)
	if err != nil {
		return nil, err
	}

	if taskRecord.BranchName == nil || strings.TrimSpace(*taskRecord.BranchName) == "" {
		trimmed := strings.TrimSpace(branchName)
		if trimmed == "" {
			trimmed = fmt.Sprintf("task/%d", taskRecord.TaskNumber)
		}
		if trimmed != "" {
			if _, err := s.tasks.SetBranch(ctx, taskRecord.ID, &trimmed); err != nil {
				return nil, err
			}
		}
	}

	return &updatedExecution, nil
}

func (s *service) CreateSubtask(ctx context.Context, flowNodeExecutionID uuid.UUID, title string, description *string, assignee SubtaskAssignee) (*repo.ProjectSubtask, error) {
	if flowNodeExecutionID == uuid.Nil {
		return nil, ErrFlowNodeExecutionRequired
	}

	execution, err := s.executions.GetByID(ctx, flowNodeExecutionID)
	if err != nil {
		return nil, err
	}
	if err := s.validateSubtaskAssignee(ctx, assignee); err != nil {
		return nil, err
	}
	nextSequence, err := s.subtasks.NextSequenceNumber(ctx, flowNodeExecutionID)
	if err != nil {
		return nil, err
	}

	created, err := s.subtasks.Create(ctx, repo.ProjectSubtask{
		TaskID:              execution.TaskID,
		FlowNodeExecutionID: flowNodeExecutionID,
		Title:               strings.TrimSpace(title),
		Description:         description,
		WorkStatus:          "pending",
		SequenceNumber:      nextSequence,
		AssigneeType:        trimTypePointer(assignee.Type),
		AssigneeID:          assignee.ID,
		CreatedByType:       "system",
		CreatedByID:         nil,
	})
	if err != nil {
		return nil, err
	}

	return &created, nil
}

func (s *service) UpdateSubtaskStatus(ctx context.Context, subtaskID uuid.UUID, newStatus string) (*repo.ProjectSubtask, error) {
	if subtaskID == uuid.Nil {
		return nil, ErrSubtaskIDRequired
	}

	subtask, err := s.subtasks.GetByID(ctx, subtaskID)
	if err != nil {
		return nil, err
	}

	targetStatus := strings.TrimSpace(newStatus)
	if !isValidSubtaskTransition(subtask.WorkStatus, targetStatus) {
		return nil, ErrInvalidSubtaskTransition
	}

	updated, err := s.subtasks.UpdateStatus(ctx, subtaskID, targetStatus)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *service) ListSubtasks(ctx context.Context, flowNodeExecutionID uuid.UUID) ([]repo.ProjectSubtask, error) {
	if flowNodeExecutionID == uuid.Nil {
		return nil, ErrFlowNodeExecutionRequired
	}
	return s.subtasks.ListByExecution(ctx, flowNodeExecutionID)
}

func (s *service) AddDependency(ctx context.Context, req AddDependencyRequest) (*repo.ProjectTaskDependency, error) {
	sourceType := strings.TrimSpace(req.SourceType)
	dependsOnType := strings.TrimSpace(req.DependsOnType)

	if sourceType != dependsOnType {
		return nil, ErrCrossLevelDependency
	}
	if req.SourceID == req.DependsOnID {
		return nil, ErrSelfDependency
	}

	hasCycle, err := s.checkCycle(ctx, sourceType, req.SourceID, req.DependsOnID)
	if err != nil {
		return nil, err
	}
	if hasCycle {
		return nil, ErrCyclicDependency
	}

	created, err := s.dependencies.Add(ctx, repo.ProjectTaskDependency{
		SourceType:    sourceType,
		SourceID:      req.SourceID,
		DependsOnType: dependsOnType,
		DependsOnID:   req.DependsOnID,
		CreatedByType: strings.TrimSpace(req.CreatedByType),
		CreatedByID:   req.CreatedByID,
	})
	if err != nil {
		return nil, err
	}

	if sourceType == "project_task" {
		dependedTask, getErr := s.tasks.GetByID(ctx, req.DependsOnID)
		if getErr == nil && (dependedTask.WorkStatus == "blocked" || dependedTask.WorkStatus == "cancelled") {
			_, _ = s.taskService.MarkBlocked(ctx, req.SourceID, "dependency task is unavailable", tasksvc.Actor{Type: "system"})
		}
	}

	return &created, nil
}

func (s *service) RemoveDependency(ctx context.Context, sourceType string, sourceID uuid.UUID, dependsOnType string, dependsOnID uuid.UUID) error {
	outbound, err := s.dependencies.ListOutbound(ctx, strings.TrimSpace(sourceType), sourceID)
	if err != nil {
		return err
	}

	for _, dependency := range outbound {
		if dependency.DependsOnType == strings.TrimSpace(dependsOnType) && dependency.DependsOnID == dependsOnID {
			return s.dependencies.Remove(ctx, dependency.ID)
		}
	}

	return repo.ErrNotFound
}

func (s *service) OnTaskCancelled(ctx context.Context, taskID uuid.UUID) error {
	if taskID == uuid.Nil {
		return ErrTaskIDRequired
	}

	cancelledTask, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return err
	}
	projectRecord, err := s.projects.GetByID(ctx, cancelledTask.ProjectID)
	if err != nil {
		return err
	}

	inbound, err := s.dependencies.ListInbound(ctx, "project_task", taskID)
	if err != nil {
		return err
	}
	reason := fmt.Sprintf("dependency %s-%d was cancelled", projectRecord.Slug, cancelledTask.TaskNumber)

	for _, dependency := range inbound {
		if dependency.SourceType != "project_task" {
			continue
		}
		if _, err := s.taskService.MarkBlocked(ctx, dependency.SourceID, reason, tasksvc.Actor{Type: "system"}); err != nil {
			return err
		}
	}

	return nil
}

func (s *service) SubscribeTaskCancellationHandler(orgID *uuid.UUID) eventbus.Subscription {
	return s.events.Subscribe("flow.task-cancelled", orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		if event.EventType != "task.status_changed" {
			return nil
		}
		var payload struct {
			TaskID   uuid.UUID `json:"task_id"`
			ToStatus string    `json:"to_status"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil
		}
		if payload.ToStatus != "cancelled" || payload.TaskID == uuid.Nil {
			return nil
		}
		return s.OnTaskCancelled(ctx, payload.TaskID)
	})
}

func (s *service) ensureExecutionSession(ctx context.Context, execution *repo.FlowNodeExecution) error {
	if s.sessionBridge == nil || execution == nil {
		return nil
	}
	session, err := s.sessionBridge.EnsureNodeSession(ctx, *execution)
	if err != nil {
		return err
	}
	execution.SessionID = &session.ID
	return nil
}

func (s *service) validateSubtaskAssignee(ctx context.Context, assignee SubtaskAssignee) error {
	assigneeType := ""
	if assignee.Type != nil {
		assigneeType = strings.TrimSpace(*assignee.Type)
	}
	if assigneeType == "" {
		return nil
	}

	if assignee.ID == nil || *assignee.ID == uuid.Nil {
		switch assigneeType {
		case "agent":
			return ErrAgentNotFound
		case "human_user":
			return ErrUserNotFound
		default:
			return fmt.Errorf("invalid assignee_type %q", assigneeType)
		}
	}

	switch assigneeType {
	case "agent":
		_, err := s.agents.GetByID(ctx, *assignee.ID)
		if errors.Is(err, repo.ErrNotFound) {
			return ErrAgentNotFound
		}
		return err
	case "human_user":
		_, err := s.users.GetByID(ctx, *assignee.ID)
		if errors.Is(err, repo.ErrNotFound) {
			return ErrUserNotFound
		}
		return err
	default:
		return fmt.Errorf("invalid assignee_type %q", assigneeType)
	}
}

func (s *service) loadActiveFlowState(ctx context.Context, taskID uuid.UUID) (repo.ProjectTask, repo.FlowNode, repo.FlowNodeExecution, error) {
	if taskID == uuid.Nil {
		return repo.ProjectTask{}, repo.FlowNode{}, repo.FlowNodeExecution{}, ErrTaskIDRequired
	}

	taskRecord, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return repo.ProjectTask{}, repo.FlowNode{}, repo.FlowNodeExecution{}, err
	}

	if taskRecord.CurrentFlowNodeID == nil {
		if taskRecord.FlowTemplateID == nil {
			return repo.ProjectTask{}, repo.FlowNode{}, repo.FlowNodeExecution{}, ErrFlowNotStarted
		}
		if _, err := s.StartFlow(ctx, taskRecord.ID); err != nil {
			return repo.ProjectTask{}, repo.FlowNode{}, repo.FlowNodeExecution{}, err
		}
		taskRecord, err = s.tasks.GetByID(ctx, taskID)
		if err != nil {
			return repo.ProjectTask{}, repo.FlowNode{}, repo.FlowNodeExecution{}, err
		}
		if taskRecord.CurrentFlowNodeID == nil {
			return repo.ProjectTask{}, repo.FlowNode{}, repo.FlowNodeExecution{}, ErrFlowNotStarted
		}
	}

	currentNode, err := s.flowNodes.GetByID(ctx, *taskRecord.CurrentFlowNodeID)
	if err != nil {
		return repo.ProjectTask{}, repo.FlowNode{}, repo.FlowNodeExecution{}, err
	}

	activeExecution, err := s.executions.GetActive(ctx, taskRecord.ID, currentNode.ID)
	if errors.Is(err, repo.ErrNotFound) {
		nextVisit, visitErr := s.nextVisitNumber(ctx, taskRecord.ID, currentNode.ID)
		if visitErr != nil {
			return repo.ProjectTask{}, repo.FlowNode{}, repo.FlowNodeExecution{}, visitErr
		}
		activeExecution, err = s.executions.Create(ctx, repo.FlowNodeExecution{
			TaskID:      taskRecord.ID,
			FlowNodeID:  currentNode.ID,
			VisitNumber: nextVisit,
			Status:      "active",
		})
		if err != nil {
			return repo.ProjectTask{}, repo.FlowNode{}, repo.FlowNodeExecution{}, err
		}
		if err := s.ensureExecutionSession(ctx, &activeExecution); err != nil {
			return repo.ProjectTask{}, repo.FlowNode{}, repo.FlowNodeExecution{}, err
		}
	} else if err != nil {
		return repo.ProjectTask{}, repo.FlowNode{}, repo.FlowNodeExecution{}, err
	}

	return taskRecord, currentNode, activeExecution, nil
}

func (s *service) nextVisitNumber(ctx context.Context, taskID uuid.UUID, flowNodeID uuid.UUID) (int, error) {
	executions, err := s.executions.ListByTask(ctx, taskID)
	if err != nil {
		return 0, err
	}

	maxVisit := 0
	for _, execution := range executions {
		if execution.FlowNodeID != flowNodeID {
			continue
		}
		if execution.VisitNumber > maxVisit {
			maxVisit = execution.VisitNumber
		}
	}

	return maxVisit + 1, nil
}

type reviewExecutionProgress struct {
	hasWork            bool
	hasReviewedWork    bool
	pendingReviewWork  bool
	pendingReviewActor executionActorRef
}

type executionActorRef struct {
	Type string
	ID   uuid.UUID
}

func (s *service) validateReviewAdvanceActor(ctx context.Context, taskID uuid.UUID, currentNode repo.FlowNode, _ repo.FlowNodeExecution, actor Actor) error {
	if !strings.EqualFold(strings.TrimSpace(currentNode.NodeType), "review") {
		return nil
	}

	progress, err := s.reviewProgressForTask(ctx, taskID, nil)
	if err != nil {
		return err
	}
	if !progress.pendingReviewWork {
		return nil
	}
	if actorMatchesExecutionActor(actor, progress.pendingReviewActor) {
		return ErrSelfReviewForbidden
	}
	return nil
}

func (s *service) validateTerminalAdvance(ctx context.Context, taskID uuid.UUID, activeExecution repo.FlowNodeExecution, currentNode repo.FlowNode, actor Actor) error {
	progress, err := s.reviewProgressForTask(ctx, taskID, &repo.FlowNodeExecution{
		ID:          activeExecution.ID,
		TaskID:      activeExecution.TaskID,
		FlowNodeID:  currentNode.ID,
		VisitNumber: activeExecution.VisitNumber,
		Status:      "completed",
		SessionID:   activeExecution.SessionID,
		CommitSHA:   activeExecution.CommitSHA,
		StartedAt:   activeExecution.StartedAt,
		CompletedAt: activeExecution.CompletedAt,
		Metadata:    executionMetadataWithCompletedBy(activeExecution.Metadata, actor),
	})
	if err != nil {
		return err
	}
	if !progress.hasWork || !progress.hasReviewedWork || progress.pendingReviewWork {
		return tasksvc.ErrDoneRequiresTerminalFlow
	}
	return nil
}

func (s *service) reviewProgressForTask(ctx context.Context, taskID uuid.UUID, override *repo.FlowNodeExecution) (reviewExecutionProgress, error) {
	rows, err := s.executions.ListByTask(ctx, taskID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return reviewExecutionProgress{}, nil
		}
		return reviewExecutionProgress{}, err
	}
	if override != nil {
		replaced := false
		for i := range rows {
			if rows[i].ID != override.ID {
				continue
			}
			rows[i] = *override
			replaced = true
			break
		}
		if !replaced {
			rows = append(rows, *override)
		}
	}

	progress := reviewExecutionProgress{}
	nodeCache := make(map[uuid.UUID]repo.FlowNode, len(rows))
	for _, execution := range rows {
		if !strings.EqualFold(strings.TrimSpace(execution.Status), "completed") {
			continue
		}
		node, ok := nodeCache[execution.FlowNodeID]
		if !ok {
			loaded, err := s.flowNodes.GetByID(ctx, execution.FlowNodeID)
			if err != nil {
				return reviewExecutionProgress{}, err
			}
			node = loaded
			nodeCache[execution.FlowNodeID] = node
		}

		switch strings.ToLower(strings.TrimSpace(node.NodeType)) {
		case "work":
			progress.hasWork = true
			progress.pendingReviewWork = true
			progress.pendingReviewActor = decodeExecutionActorRef(execution.Metadata)
		case "review":
			if progress.pendingReviewWork {
				progress.hasReviewedWork = true
				progress.pendingReviewWork = false
				progress.pendingReviewActor = executionActorRef{}
			}
		}
	}

	return progress, nil
}

func executionMetadataWithCompletedBy(existing json.RawMessage, actor Actor) json.RawMessage {
	payload := map[string]any{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &payload)
	}
	completedBy := map[string]any{
		"type": normalizeTaskActorType(actor.Type),
	}
	if actor.ID != uuid.Nil {
		completedBy["id"] = actor.ID.String()
	}
	payload["completed_by"] = completedBy
	encoded, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func decodeExecutionActorRef(metadata json.RawMessage) executionActorRef {
	if len(metadata) == 0 {
		return executionActorRef{}
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return executionActorRef{}
	}
	rawCompletedBy, ok := payload["completed_by"].(map[string]any)
	if !ok {
		return executionActorRef{}
	}
	ref := executionActorRef{Type: normalizeTaskActorType(strings.TrimSpace(fmt.Sprintf("%v", rawCompletedBy["type"])))}
	if rawID, ok := rawCompletedBy["id"]; ok {
		if parsedID, err := uuid.Parse(strings.TrimSpace(fmt.Sprintf("%v", rawID))); err == nil {
			ref.ID = parsedID
		}
	}
	return ref
}

func actorMatchesExecutionActor(actor Actor, ref executionActorRef) bool {
	if ref.Type == "" || ref.ID == uuid.Nil {
		return false
	}
	return normalizeTaskActorType(actor.Type) == ref.Type && actor.ID != uuid.Nil && actor.ID == ref.ID
}

func (s *service) checkCycle(ctx context.Context, nodeType string, sourceID, dependsOnID uuid.UUID) (bool, error) {
	visited := make(map[uuid.UUID]struct{})

	var walk func(nodeID uuid.UUID, depth int) (bool, error)
	walk = func(nodeID uuid.UUID, depth int) (bool, error) {
		if depth > maxDependencyTraversalDepth {
			return false, ErrDAGTooDeep
		}
		if nodeID == sourceID {
			return true, nil
		}
		if _, ok := visited[nodeID]; ok {
			return false, nil
		}
		visited[nodeID] = struct{}{}

		outbound, err := s.dependencies.ListOutbound(ctx, nodeType, nodeID)
		if err != nil {
			return false, err
		}
		for _, edge := range outbound {
			if edge.DependsOnType != nodeType {
				continue
			}
			cycle, walkErr := walk(edge.DependsOnID, depth+1)
			if walkErr != nil {
				return false, walkErr
			}
			if cycle {
				return true, nil
			}
		}
		return false, nil
	}

	return walk(dependsOnID, 0)
}

func (s *service) recordTaskEvent(ctx context.Context, taskRecord repo.ProjectTask, eventType string, actor Actor, payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.taskEvents.Record(ctx, repo.ProjectTaskEvent{
		TaskID:    taskRecord.ID,
		ProjectID: taskRecord.ProjectID,
		EventType: strings.TrimSpace(eventType),
		ActorType: normalizeTaskActorType(actor.Type),
		ActorID:   actorIDPtr(actor),
		Payload:   encoded,
	})
	return err
}

func (s *service) publishDomainEvent(ctx context.Context, organizationID uuid.UUID, eventType, actorType string, actorID *uuid.UUID, payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	domainType, domainActorID := normalizeDomainActor(actorType, actorID)
	return s.events.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: organizationID,
		EventType:      strings.TrimSpace(eventType),
		ActorType:      domainType,
		ActorID:        domainActorID,
		Payload:        encoded,
	})
}

func isValidSubtaskTransition(from, to string) bool {
	switch strings.TrimSpace(from) {
	case "pending":
		return to == "in_progress" || to == "cancelled"
	case "in_progress":
		return to == "done" || to == "cancelled"
	default:
		return false
	}
}

func toTaskActor(actor Actor) tasksvc.Actor {
	return tasksvc.Actor{
		Type: normalizeTaskActorType(actor.Type),
		ID:   actor.ID,
	}
}

func normalizeTaskActorType(actorType string) string {
	switch strings.TrimSpace(actorType) {
	case "human_user":
		return "human_user"
	case "agent":
		return "agent"
	case "supervisor":
		return "supervisor"
	default:
		return "system"
	}
}

func normalizeDomainActor(actorType string, actorID *uuid.UUID) (string, *uuid.UUID) {
	switch normalizeTaskActorType(actorType) {
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

func actorIDPtr(actor Actor) *uuid.UUID {
	if actor.ID == uuid.Nil {
		return nil
	}
	id := actor.ID
	return &id
}

func nilUUID(execution *repo.FlowNodeExecution) *uuid.UUID {
	if execution == nil {
		return nil
	}
	id := execution.ID
	return &id
}

func trimTypePointer(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}
