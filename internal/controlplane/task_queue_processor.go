package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
)

const (
	taskQueuedConsumerName          = "controlplane.task-queued"
	taskCompletedConsumerName       = "controlplane.task-completed"
	taskRunCancellationConsumerName = "controlplane.run-cancellation"
	flowAdvancedConsumerName        = "controlplane.flow-advanced"
	taskQueueTriggerType            = "scheduler"
	taskSupervisorTriggerType       = "supervisor"
)

type taskQueueEventSubscriber interface {
	Subscribe(consumerName string, orgID *uuid.UUID, handler eventbus.EventHandler) eventbus.Subscription
}

type taskQueueTaskRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
}

type taskQueueStatusTransitioner interface {
	TransitionStatus(ctx context.Context, taskID uuid.UUID, toStatus string, actor tasksvc.Actor) (*tasksvc.ProjectTask, error)
}

type taskQueueFlowStarter interface {
	StartFlow(ctx context.Context, taskID uuid.UUID) (*repo.FlowNodeExecution, error)
}

type taskQueueFlowExecutionRepository interface {
	GetActive(ctx context.Context, taskID, flowNodeID uuid.UUID) (repo.FlowNodeExecution, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowNodeExecution, error)
}

type taskQueueFlowNodeRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowNode, error)
}

type taskQueueAssignmentRepository interface {
	GetPM(ctx context.Context, projectID uuid.UUID) (repo.AgentProjectAssignment, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.AgentProjectAssignment, error)
}

type taskQueueRunStarter interface {
	CreateRun(ctx context.Context, input CreateRunInput) (Run, error)
	StartRun(ctx context.Context, runID uuid.UUID) error
	CompleteRun(ctx context.Context, runID uuid.UUID, output json.RawMessage) error
	ConfirmCancelled(ctx context.Context, runID uuid.UUID) error
	GetRun(ctx context.Context, runID uuid.UUID) (Run, error)
	ListRunsByTask(ctx context.Context, organizationID, taskID uuid.UUID, status, triggerType string) ([]Run, error)
}

type taskQueueChatService interface {
	CreateSession(ctx context.Context, input chat.CreateSessionInput) (*chat.ChatSession, error)
	AddParticipant(ctx context.Context, sessionID uuid.UUID, participantType string, participantID uuid.UUID, role string) (*chat.ChatParticipant, error)
	AppendMessage(ctx context.Context, input chat.AppendMessageInput) (*chat.ChatMessage, error)
	ListMessages(ctx context.Context, sessionID uuid.UUID, filter chat.MessageFilter) ([]*chat.ChatMessage, error)
}

type taskQueueSessionRepository interface {
	GetByScopeAndMode(ctx context.Context, scopeType string, scopeID uuid.UUID, mode string) (*repo.ChatSession, error)
}

type TaskQueueProcessorOptions struct {
	Events         taskQueueEventSubscriber
	Tasks          taskQueueTaskRepository
	TaskService    taskQueueStatusTransitioner
	Flow           taskQueueFlowStarter
	FlowExecutions taskQueueFlowExecutionRepository
	FlowNodes      taskQueueFlowNodeRepository
	Assignments    taskQueueAssignmentRepository
	Runs           taskQueueRunStarter
	Chats          taskQueueChatService
	Sessions       taskQueueSessionRepository
}

type TaskQueueProcessor struct {
	events         taskQueueEventSubscriber
	tasks          taskQueueTaskRepository
	taskService    taskQueueStatusTransitioner
	flow           taskQueueFlowStarter
	flowExecutions taskQueueFlowExecutionRepository
	flowNodes      taskQueueFlowNodeRepository
	assignments    taskQueueAssignmentRepository
	runs           taskQueueRunStarter
	chats          taskQueueChatService
	sessions       taskQueueSessionRepository
}

func NewTaskQueueProcessor(opts TaskQueueProcessorOptions) (*TaskQueueProcessor, error) {
	if opts.Events == nil {
		return nil, fmt.Errorf("task queue processor requires event subscriber")
	}
	if opts.Tasks == nil {
		return nil, fmt.Errorf("task queue processor requires task repository")
	}
	if opts.TaskService == nil {
		return nil, fmt.Errorf("task queue processor requires task service")
	}
	if opts.Flow == nil {
		return nil, fmt.Errorf("task queue processor requires flow service")
	}
	if opts.FlowExecutions == nil {
		return nil, fmt.Errorf("task queue processor requires flow execution repository")
	}
	if opts.FlowNodes == nil {
		return nil, fmt.Errorf("task queue processor requires flow node repository")
	}
	if opts.Assignments == nil {
		return nil, fmt.Errorf("task queue processor requires assignment repository")
	}
	if opts.Runs == nil {
		return nil, fmt.Errorf("task queue processor requires run service")
	}
	if opts.Chats == nil {
		return nil, fmt.Errorf("task queue processor requires chat service")
	}
	if opts.Sessions == nil {
		return nil, fmt.Errorf("task queue processor requires chat session repository")
	}

	return &TaskQueueProcessor{
		events:         opts.Events,
		tasks:          opts.Tasks,
		taskService:    opts.TaskService,
		flow:           opts.Flow,
		flowExecutions: opts.FlowExecutions,
		flowNodes:      opts.FlowNodes,
		assignments:    opts.Assignments,
		runs:           opts.Runs,
		chats:          opts.Chats,
		sessions:       opts.Sessions,
	}, nil
}

func (p *TaskQueueProcessor) SubscribeFlowAdvanced(orgID *uuid.UUID) eventbus.Subscription {
	return p.events.Subscribe(flowAdvancedConsumerName, orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		return p.handleFlowAdvancedEvent(ctx, event)
	})
}

func (p *TaskQueueProcessor) SubscribeTaskQueued(orgID *uuid.UUID) eventbus.Subscription {
	return p.events.Subscribe(taskQueuedConsumerName, orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		return p.handleTaskQueuedEvent(ctx, event)
	})
}

func (p *TaskQueueProcessor) handleTaskQueuedEvent(ctx context.Context, event eventbus.DomainEvent) error {
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
	if payload.TaskID == uuid.Nil || !strings.EqualFold(strings.TrimSpace(payload.ToStatus), "queued") {
		return nil
	}

	return p.processQueuedTask(ctx, event, payload.TaskID)
}

func (p *TaskQueueProcessor) processQueuedTask(ctx context.Context, event eventbus.DomainEvent, taskID uuid.UUID) error {
	taskRecord, err := p.tasks.GetByID(ctx, taskID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	status := strings.ToLower(strings.TrimSpace(taskRecord.WorkStatus))
	if status == "queued" {
		if _, err := p.taskService.TransitionStatus(ctx, taskID, "in_progress", tasksvc.Actor{Type: "system", AllowNoActiveFlow: true}); err != nil {
			var transitionErr tasksvc.ErrInvalidStatusTransition
			if !errors.As(err, &transitionErr) {
				return err
			}
		}
		taskRecord, err = p.tasks.GetByID(ctx, taskID)
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "in_progress") {
		return nil
	}

	if taskRecord.FlowTemplateID != nil {
		return p.ensureFlowRun(ctx, event, taskRecord)
	}
	if taskRecord.AssignedAgentID != nil && *taskRecord.AssignedAgentID != uuid.Nil {
		return p.ensureAssignedAgentRun(ctx, event, taskRecord)
	}
	return nil
}

func (p *TaskQueueProcessor) ensureFlowRun(ctx context.Context, event eventbus.DomainEvent, taskRecord repo.ProjectTask) error {
	if taskRecord.CurrentFlowNodeID == nil {
		started, err := p.flow.StartFlow(ctx, taskRecord.ID)
		if err != nil {
			return err
		}
		if started != nil {
			taskRecord.CurrentFlowNodeID = &started.FlowNodeID
		}
	}
	if taskRecord.CurrentFlowNodeID == nil {
		refreshed, err := p.tasks.GetByID(ctx, taskRecord.ID)
		if err != nil {
			return err
		}
		taskRecord = refreshed
	}
	if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID == uuid.Nil {
		return fmt.Errorf("task %s missing current flow node after StartFlow", taskRecord.ID)
	}

	execution, err := p.flowExecutions.GetActive(ctx, taskRecord.ID, *taskRecord.CurrentFlowNodeID)
	if err != nil {
		return err
	}

	idempotencyKey := fmt.Sprintf("task-queued:flow:%s:%s", taskRecord.ID, event.ID)
	metadata, err := json.Marshal(map[string]any{
		"source":                 "task_queue_processor",
		"task_status_event_id":   event.ID,
		"flow_node_execution_id": execution.ID,
		"run_mode":               "async",
	})
	if err != nil {
		return err
	}

	principalType := "system"
	principalID := uuid.Nil
	if taskRecord.AssignedAgentID != nil && *taskRecord.AssignedAgentID != uuid.Nil {
		principalType = "agent"
		principalID = *taskRecord.AssignedAgentID
	}

	runRecord, err := p.runs.CreateRun(ctx, CreateRunInput{
		OrganizationID: taskRecord.OrganizationID,
		PrincipalType:  principalType,
		PrincipalID:    principalID,
		TriggerType:    taskQueueTriggerType,
		ProjectID:      &taskRecord.ProjectID,
		TaskID:         &taskRecord.ID,
		FlowNodeID:     taskRecord.CurrentFlowNodeID,
		SessionID:      execution.SessionID,
		IdempotencyKey: &idempotencyKey,
		Metadata:       metadata,
	})
	if err != nil {
		return err
	}

	if err := p.runs.StartRun(ctx, runRecord.ID); err != nil && !errors.Is(err, ErrInvalidTransition) {
		return err
	}

	if taskRecord.AssignedAgentID == nil || *taskRecord.AssignedAgentID == uuid.Nil {
		return nil
	}

	// Re-fetch execution to pick up the session_id set by CreateRun → RouteRunToSession → EnsureNodeSession.
	// The local execution variable is stale (fetched before the session was created).
	if execution.SessionID == nil || *execution.SessionID == uuid.Nil {
		if refreshed, refreshErr := p.flowExecutions.GetActive(ctx, taskRecord.ID, *taskRecord.CurrentFlowNodeID); refreshErr == nil {
			execution = refreshed
		}
	}

	sessionID := execution.SessionID
	if runRecord.SessionID != nil && *runRecord.SessionID != uuid.Nil {
		sessionID = runRecord.SessionID
	}
	if sessionID == nil || *sessionID == uuid.Nil {
		return nil
	}

	if _, err := p.chats.AddParticipant(ctx, *sessionID, "agent", *taskRecord.AssignedAgentID, "responder"); err != nil && !errors.Is(err, chat.ErrAlreadyParticipant) {
		return err
	}

	hasKickoffMessage, err := p.sessionHasKickoffMessage(ctx, *sessionID, runRecord.ID)
	if err != nil {
		return err
	}
	if hasKickoffMessage {
		return nil
	}

	messageMetadata, err := json.Marshal(map[string]any{
		"source":                 "task_queue_processor",
		"run_id":                 runRecord.ID.String(),
		"task_id":                taskRecord.ID.String(),
		"flow_node_execution_id": execution.ID.String(),
	})
	if err != nil {
		return err
	}
	if _, err := p.chats.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: *sessionID,
		Role:      "user",
		Content:   buildFlowKickoffMessage(taskRecord, execution),
		Metadata:  messageMetadata,
	}); err != nil {
		return err
	}

	return nil
}

func (p *TaskQueueProcessor) ensureAssignedAgentRun(ctx context.Context, event eventbus.DomainEvent, taskRecord repo.ProjectTask) error {
	if taskRecord.AssignedAgentID == nil || *taskRecord.AssignedAgentID == uuid.Nil {
		return nil
	}

	session, err := p.sessions.GetByScopeAndMode(ctx, "project_task", taskRecord.ID, "async")
	if err != nil {
		return err
	}
	if session == nil {
		created, err := p.chats.CreateSession(ctx, chat.CreateSessionInput{
			OrganizationID: taskRecord.OrganizationID,
			ScopeType:      "project_task",
			ScopeID:        taskRecord.ID,
			Mode:           "async",
			Metadata:       json.RawMessage(`{"source":"task_queue_processor"}`),
		})
		if err != nil {
			return err
		}
		session = created
	}
	if session == nil {
		return fmt.Errorf("task %s missing async session", taskRecord.ID)
	}
	if _, err := p.chats.AddParticipant(ctx, session.ID, "agent", *taskRecord.AssignedAgentID, "responder"); err != nil && !errors.Is(err, chat.ErrAlreadyParticipant) {
		return err
	}

	idempotencyKey := fmt.Sprintf("task-queued:agent-turn:%s:%s", taskRecord.ID, event.ID)
	metadata, err := json.Marshal(map[string]any{
		"source":               "task_queue_processor",
		"task_status_event_id": event.ID,
		"run_mode":             "async",
	})
	if err != nil {
		return err
	}

	runRecord, err := p.runs.CreateRun(ctx, CreateRunInput{
		OrganizationID: taskRecord.OrganizationID,
		PrincipalType:  "agent",
		PrincipalID:    *taskRecord.AssignedAgentID,
		TriggerType:    taskQueueTriggerType,
		ProjectID:      &taskRecord.ProjectID,
		TaskID:         &taskRecord.ID,
		SessionID:      &session.ID,
		IdempotencyKey: &idempotencyKey,
		Metadata:       metadata,
	})
	if err != nil {
		return err
	}

	if err := p.runs.StartRun(ctx, runRecord.ID); err != nil && !errors.Is(err, ErrInvalidTransition) {
		return err
	}

	hasKickoffMessage, err := p.sessionHasKickoffMessage(ctx, session.ID, runRecord.ID)
	if err != nil {
		return err
	}
	if hasKickoffMessage {
		return nil
	}

	messageMetadata, err := json.Marshal(map[string]any{
		"source":  "task_queue_processor",
		"run_id":  runRecord.ID.String(),
		"task_id": taskRecord.ID.String(),
	})
	if err != nil {
		return err
	}
	if _, err := p.chats.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: session.ID,
		Role:      "user",
		Content:   buildQueueKickoffMessage(taskRecord),
		Metadata:  messageMetadata,
	}); err != nil {
		return err
	}

	return nil
}

func (p *TaskQueueProcessor) sessionHasKickoffMessage(ctx context.Context, sessionID, runID uuid.UUID) (bool, error) {
	messages, err := p.chats.ListMessages(ctx, sessionID, chat.MessageFilter{Limit: 200})
	if err != nil {
		return false, err
	}
	for _, message := range messages {
		if message == nil || !strings.EqualFold(strings.TrimSpace(message.Role), "user") || len(message.Metadata) == 0 {
			continue
		}
		var metadata map[string]any
		if err := json.Unmarshal(message.Metadata, &metadata); err != nil {
			continue
		}
		if strings.TrimSpace(valueAsString(metadata["source"])) != "task_queue_processor" {
			continue
		}
		if strings.TrimSpace(valueAsString(metadata["run_id"])) == runID.String() {
			return true, nil
		}
	}
	return false, nil
}

func buildQueueKickoffMessage(taskRecord repo.ProjectTask) string {
	title := strings.TrimSpace(taskRecord.Title)
	if title == "" {
		title = "Untitled task"
	}

	description := strings.TrimSpace(valueOrEmpty(taskRecord.Description))
	if description == "" {
		return "Start work on task: " + title
	}
	return "Start work on task: " + title + "\n\nTask description:\n" + description
}

func buildFlowKickoffMessage(taskRecord repo.ProjectTask, execution repo.FlowNodeExecution) string {
	base := buildQueueKickoffMessage(taskRecord)
	if execution.ID == uuid.Nil {
		return base
	}
	return base + "\n\nFlow node execution: " + execution.ID.String()
}

// SubscribeTaskCompleted subscribes to task terminal status events and completes
// any in_progress scheduler runs for the task. This ensures scheduler runs created
// by ensureFlowRun are properly closed when the task finishes via flow.advance.
func (p *TaskQueueProcessor) SubscribeTaskCompleted(orgID *uuid.UUID) eventbus.Subscription {
	return p.events.Subscribe(taskCompletedConsumerName, orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		return p.handleTaskCompletedEvent(ctx, event)
	})
}

func (p *TaskQueueProcessor) handleTaskCompletedEvent(ctx context.Context, event eventbus.DomainEvent) error {
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

	toStatus := strings.ToLower(strings.TrimSpace(payload.ToStatus))
	if toStatus != "done" && toStatus != "cancelled" {
		return nil
	}
	if payload.TaskID == uuid.Nil || event.OrganizationID == uuid.Nil {
		return nil
	}

	for _, triggerType := range []string{taskQueueTriggerType, taskSupervisorTriggerType} {
		// Complete any in_progress tracking runs for this task.
		if runs, err := p.runs.ListRunsByTask(ctx, event.OrganizationID, payload.TaskID, "in_progress", triggerType); err == nil {
			for _, r := range runs {
				_ = p.runs.CompleteRun(ctx, r.ID, json.RawMessage(`{"source":"task_completed_handler","task_status":"`+toStatus+`"}`))
			}
		}
		// Confirm cancellation for tracking runs that are already in cancelling state.
		if runs, err := p.runs.ListRunsByTask(ctx, event.OrganizationID, payload.TaskID, "cancelling", triggerType); err == nil {
			for _, r := range runs {
				_ = p.runs.ConfirmCancelled(ctx, r.ID)
			}
		}
	}
	return nil
}

// SubscribeRunCancellationRequested subscribes to run.cancellation_requested events
// and immediately confirms cancellation for scheduler runs (which have no live
// processing to wait for — they are pure tracking records).
func (p *TaskQueueProcessor) SubscribeRunCancellationRequested(orgID *uuid.UUID) eventbus.Subscription {
	return p.events.Subscribe(taskRunCancellationConsumerName, orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		return p.handleRunCancellationRequestedEvent(ctx, event)
	})
}

func (p *TaskQueueProcessor) handleRunCancellationRequestedEvent(ctx context.Context, event eventbus.DomainEvent) error {
	if event.EventType != "run.cancellation_requested" {
		return nil
	}
	var payload struct {
		RunID uuid.UUID `json:"run_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil
	}
	if payload.RunID == uuid.Nil {
		return nil
	}
	run, err := p.runs.GetRun(ctx, payload.RunID)
	if err != nil {
		return nil // best-effort
	}
	// Only auto-confirm for scheduler and supervisor trigger types.
	// agent_tool runs are confirmed by the turn engine after it stops processing.
	if run.TriggerType != "scheduler" && run.TriggerType != "supervisor" {
		return nil
	}
	_ = p.runs.ConfirmCancelled(ctx, run.ID)
	return nil
}

func (p *TaskQueueProcessor) handleFlowAdvancedEvent(ctx context.Context, event eventbus.DomainEvent) error {
	if event.EventType != "flow.advanced" && event.EventType != "flow.rejected" {
		return nil
	}

	var payload struct {
		TaskID                 uuid.UUID  `json:"task_id"`
		ProjectID              uuid.UUID  `json:"project_id"`
		ToFlowNodeID           *uuid.UUID `json:"to_flow_node_id"`
		ToFlowExecutionID      *uuid.UUID `json:"to_flow_execution_id"`
		TerminalTransitionDone bool       `json:"terminal_transition_done"`
		RejectionFeedback      string     `json:"rejection_feedback"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil
	}
	if payload.TaskID == uuid.Nil || payload.ProjectID == uuid.Nil || payload.TerminalTransitionDone {
		return nil
	}
	if payload.ToFlowNodeID == nil || *payload.ToFlowNodeID == uuid.Nil {
		return nil
	}
	if payload.ToFlowExecutionID == nil || *payload.ToFlowExecutionID == uuid.Nil {
		return nil
	}

	taskRecord, err := p.tasks.GetByID(ctx, payload.TaskID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	nextNode, err := p.flowNodes.GetByID(ctx, *payload.ToFlowNodeID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	nextExecution, err := p.flowExecutions.GetByID(ctx, *payload.ToFlowExecutionID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	agentID, err := p.resolveFlowTransitionAgent(ctx, taskRecord, nextNode)
	if err != nil {
		return err
	}
	if agentID == nil || *agentID == uuid.Nil {
		return nil
	}

	return p.ensureFlowTransitionRun(ctx, event, taskRecord, nextNode, nextExecution, *agentID, payload.RejectionFeedback)
}

func (p *TaskQueueProcessor) ensureFlowTransitionRun(
	ctx context.Context,
	event eventbus.DomainEvent,
	taskRecord repo.ProjectTask,
	nextNode repo.FlowNode,
	nextExecution repo.FlowNodeExecution,
	agentID uuid.UUID,
	rejectionFeedback string,
) error {
	idempotencyKey := fmt.Sprintf("flow-transition:%s", nextExecution.ID)
	metadata, err := json.Marshal(map[string]any{
		"source":                 "task_queue_processor",
		"flow_event_id":          event.ID,
		"flow_event_type":        event.EventType,
		"flow_node_execution_id": nextExecution.ID,
		"run_mode":               "async",
	})
	if err != nil {
		return err
	}

	sessionID := nextExecution.SessionID
	runRecord, err := p.runs.CreateRun(ctx, CreateRunInput{
		OrganizationID: taskRecord.OrganizationID,
		PrincipalType:  "agent",
		PrincipalID:    agentID,
		TriggerType:    taskQueueTriggerType,
		ProjectID:      &taskRecord.ProjectID,
		TaskID:         &taskRecord.ID,
		FlowNodeID:     &nextNode.ID,
		SessionID:      sessionID,
		IdempotencyKey: &idempotencyKey,
		Metadata:       metadata,
	})
	if err != nil {
		return err
	}

	if err := p.runs.StartRun(ctx, runRecord.ID); err != nil && !errors.Is(err, ErrInvalidTransition) {
		return err
	}

	if runRecord.SessionID != nil && *runRecord.SessionID != uuid.Nil {
		sessionID = runRecord.SessionID
	}
	if sessionID == nil || *sessionID == uuid.Nil {
		return nil
	}

	if _, err := p.chats.AddParticipant(ctx, *sessionID, "agent", agentID, "responder"); err != nil && !errors.Is(err, chat.ErrAlreadyParticipant) {
		return err
	}

	hasKickoffMessage, err := p.sessionHasKickoffMessage(ctx, *sessionID, runRecord.ID)
	if err != nil {
		return err
	}
	if hasKickoffMessage {
		return nil
	}

	messageMetadata, err := json.Marshal(map[string]any{
		"source":                 "task_queue_processor",
		"run_id":                 runRecord.ID.String(),
		"task_id":                taskRecord.ID.String(),
		"flow_node_execution_id": nextExecution.ID.String(),
		"flow_event_type":        event.EventType,
	})
	if err != nil {
		return err
	}
	if _, err := p.chats.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: *sessionID,
		Role:      "user",
		Content:   buildFlowTransitionKickoffMessage(taskRecord, nextNode, nextExecution, event.EventType, rejectionFeedback),
		Metadata:  messageMetadata,
	}); err != nil {
		return err
	}

	return nil
}

func (p *TaskQueueProcessor) resolveFlowTransitionAgent(ctx context.Context, taskRecord repo.ProjectTask, node repo.FlowNode) (*uuid.UUID, error) {
	actorType := strings.ToLower(strings.TrimSpace(valueOrEmpty(node.ActorType)))
	switch actorType {
	case "", "human":
		return nil, nil
	case "agent":
		if node.ActorID != nil && *node.ActorID != uuid.Nil {
			id := *node.ActorID
			return &id, nil
		}
		if taskRecord.AssignedAgentID != nil && *taskRecord.AssignedAgentID != uuid.Nil {
			id := *taskRecord.AssignedAgentID
			return &id, nil
		}
		return nil, nil
	case "project_manager":
		pm, err := p.assignments.GetPM(ctx, taskRecord.ProjectID)
		if errors.Is(err, repo.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		id := pm.AgentID
		return &id, nil
	case "role":
		assignments, err := p.assignments.ListByProject(ctx, taskRecord.ProjectID)
		if err != nil {
			return nil, err
		}
		role := resolveFlowNodeRole(node)
		for _, assignment := range assignments {
			if strings.EqualFold(strings.TrimSpace(assignment.Role), role) {
				id := assignment.AgentID
				return &id, nil
			}
		}
		return nil, nil
	default:
		return nil, nil
	}
}

func resolveFlowNodeRole(node repo.FlowNode) string {
	if role := flowNodeRoleFromMetadata(node.Metadata); role != "" {
		return role
	}
	// Flow nodes do not currently have a dedicated role field; in that case we
	// map review nodes to reviewer and all other role-based nodes to worker.
	if strings.EqualFold(strings.TrimSpace(node.NodeType), "review") {
		return "reviewer"
	}
	return "worker"
}

func flowNodeRoleFromMetadata(metadata json.RawMessage) string {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return ""
	}
	for _, key := range []string{"actor_role", "role", "project_role"} {
		if value := strings.ToLower(strings.TrimSpace(valueAsString(payload[key]))); value != "" {
			switch value {
			case "project_manager":
				return "pm"
			default:
				return value
			}
		}
	}
	return ""
}

func buildFlowTransitionKickoffMessage(
	taskRecord repo.ProjectTask,
	node repo.FlowNode,
	execution repo.FlowNodeExecution,
	eventType string,
	rejectionFeedback string,
) string {
	base := buildQueueKickoffMessage(taskRecord)
	nodeName := strings.TrimSpace(node.DisplayName)
	if nodeName != "" {
		base += "\n\nFlow node: " + nodeName
	}
	if strings.EqualFold(strings.TrimSpace(eventType), "flow.rejected") {
		base += "\n\nPrevious flow step was rejected."
		if trimmed := strings.TrimSpace(rejectionFeedback); trimmed != "" {
			base += "\nFeedback:\n" + trimmed
		}
	}
	if execution.ID != uuid.Nil {
		base += "\n\nFlow node execution: " + execution.ID.String()
	}
	return base
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func valueAsString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}
