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
	taskQueuedConsumerName = "controlplane.task-queued"
	taskQueueTriggerType   = "scheduler"
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
}

type taskQueueRunStarter interface {
	CreateRun(ctx context.Context, input CreateRunInput) (Run, error)
	StartRun(ctx context.Context, runID uuid.UUID) error
}

type taskQueueChatService interface {
	CreateSession(ctx context.Context, input chat.CreateSessionInput) (*chat.ChatSession, error)
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
		runs:           opts.Runs,
		chats:          opts.Chats,
		sessions:       opts.Sessions,
	}, nil
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
		if _, err := p.taskService.TransitionStatus(ctx, taskID, "in_progress", tasksvc.Actor{Type: "system"}); err != nil {
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

	idempotencyKey := fmt.Sprintf("task-queued:agent-turn:%s:%s", taskRecord.ID, event.ID)
	metadata, err := json.Marshal(map[string]any{
		"source":               "task_queue_processor",
		"task_status_event_id": event.ID,
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
