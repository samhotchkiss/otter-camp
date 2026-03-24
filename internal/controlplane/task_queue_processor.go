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
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/projectpause"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/taskorchestration"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
)

const (
	taskQueuedConsumerName          = "controlplane.task-queued"
	taskCompletedConsumerName       = "controlplane.task-completed"
	taskRunCancellationConsumerName = "controlplane.run-cancellation"
	flowAdvancedConsumerName        = "controlplane.flow-advanced"
	projectResumedConsumerName      = "controlplane.project-resumed"
	projectArchivedConsumerName     = "controlplane.project-archived"
	turnCompletedConsumerName       = "controlplane.turn-completed"
	turnCancelledConsumerName       = "controlplane.turn-cancelled"
	taskQueueTriggerType            = "scheduler"
	taskSupervisorTriggerType       = "supervisor"
	asyncDecisionPolicyName         = "async_forward_progress"
)

type taskQueueEventSubscriber interface {
	Subscribe(consumerName string, orgID *uuid.UUID, handler eventbus.EventHandler) eventbus.Subscription
}

type taskQueueTaskRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
	ListByProject(ctx context.Context, projectID uuid.UUID, statuses ...string) ([]repo.ProjectTask, error)
}

type taskQueueProjectRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

type taskQueueStatusTransitioner interface {
	TransitionStatus(ctx context.Context, taskID uuid.UUID, toStatus string, actor tasksvc.Actor) (*tasksvc.ProjectTask, error)
	CreateInboxItem(ctx context.Context, req tasksvc.CreateInboxItemRequest) (*tasksvc.InboxItem, error)
	RequestHumanApproval(ctx context.Context, taskID uuid.UUID) (*tasksvc.InboxItem, error)
}

type taskQueueFlowStarter interface {
	StartFlow(ctx context.Context, taskID uuid.UUID) (*repo.FlowNodeExecution, error)
	EnsureActiveExecution(ctx context.Context, taskID uuid.UUID) (*repo.FlowNodeExecution, error)
	PauseAtReviewCheckpoint(ctx context.Context, taskID uuid.UUID, actor flowsvc.Actor) (*repo.FlowNodeExecution, error)
	AdvanceFlow(ctx context.Context, taskID uuid.UUID, actor flowsvc.Actor) (*repo.FlowNodeExecution, error)
}

type taskQueueFlowExecutionRepository interface {
	GetActive(ctx context.Context, taskID, flowNodeID uuid.UUID) (repo.FlowNodeExecution, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowNodeExecution, error)
	ListByTask(ctx context.Context, taskID uuid.UUID) ([]repo.FlowNodeExecution, error)
	Abandon(ctx context.Context, id uuid.UUID) (repo.FlowNodeExecution, error)
	UpdateMetadata(ctx context.Context, id uuid.UUID, metadata json.RawMessage) (repo.FlowNodeExecution, error)
	UpdateRuntimeSubstate(ctx context.Context, id uuid.UUID, runtimeSubstate *string) (repo.FlowNodeExecution, error)
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
	CreateExecutionWakeup(ctx context.Context, input executionWakeupInput) (executionWakeupResult, error)
	StartRun(ctx context.Context, runID uuid.UUID) error
	CompleteRun(ctx context.Context, runID uuid.UUID, output json.RawMessage) error
	FailRun(ctx context.Context, runID uuid.UUID, reason, failureClass string) error
	ConfirmCancelled(ctx context.Context, runID uuid.UUID) error
	GetRun(ctx context.Context, runID uuid.UUID) (Run, error)
	ListRunsByTask(ctx context.Context, organizationID, taskID uuid.UUID, status, triggerType string) ([]Run, error)
	ReleaseExecutionOwner(ctx context.Context, taskID, sessionID uuid.UUID, reason string) (executionWakeupResult, error)
	ReleaseExecutionOwnerForRun(ctx context.Context, taskID, sessionID, runID uuid.UUID, reason string) (executionWakeupResult, error)
	RetireRuntimeStateForTask(ctx context.Context, taskID uuid.UUID, reason string) error
	RetireRuntimeStateForProject(ctx context.Context, projectID uuid.UUID, reason string) error
}

type taskQueueDeferredWakeupReleaser interface {
	ReleaseExecutionOwnerHoldingDeferred(ctx context.Context, taskID, sessionID uuid.UUID, reason string) (executionWakeupResult, error)
	ReleaseExecutionOwnerForRunHoldingDeferred(ctx context.Context, taskID, sessionID, runID uuid.UUID, reason string) (executionWakeupResult, error)
}

type taskQueueDeferredProjectPromoter interface {
	PromoteDeferredWakeupsForProject(ctx context.Context, projectID uuid.UUID) ([]Run, error)
}

type taskQueueChatService interface {
	GetSession(ctx context.Context, id uuid.UUID) (*chat.ChatSession, error)
	GetMessage(ctx context.Context, id uuid.UUID) (*chat.ChatMessage, error)
	GetTurn(ctx context.Context, id uuid.UUID) (*chat.ChatTurn, error)
	CreateSession(ctx context.Context, input chat.CreateSessionInput) (*chat.ChatSession, error)
	GetOrCreateNodeSession(ctx context.Context, flowNodeExecutionID, agentID uuid.UUID) (*chat.ChatSession, error)
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
	Projects       taskQueueProjectRepository
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
	projects       taskQueueProjectRepository
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
	if opts.Projects == nil {
		return nil, fmt.Errorf("task queue processor requires project repository")
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
		projects:       opts.Projects,
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

func (p *TaskQueueProcessor) SubscribeProjectResumed(orgID *uuid.UUID) eventbus.Subscription {
	return p.events.Subscribe(projectResumedConsumerName, orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		return p.handleProjectResumedEvent(ctx, event)
	})
}

func (p *TaskQueueProcessor) SubscribeProjectArchived(orgID *uuid.UUID) eventbus.Subscription {
	return p.events.Subscribe(projectArchivedConsumerName, orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		return p.handleProjectArchivedEvent(ctx, event)
	})
}

func (p *TaskQueueProcessor) SubscribeTurnCompletedWakeups(orgID *uuid.UUID) eventbus.Subscription {
	return p.events.Subscribe(turnCompletedConsumerName, orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		return p.handleTurnTerminalEvent(ctx, event)
	})
}

func (p *TaskQueueProcessor) SubscribeTurnCancelledWakeups(orgID *uuid.UUID) eventbus.Subscription {
	return p.events.Subscribe(turnCancelledConsumerName, orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		return p.handleTurnTerminalEvent(ctx, event)
	})
}

func (p *TaskQueueProcessor) handleTaskQueuedEvent(ctx context.Context, event eventbus.DomainEvent) error {
	if event.EventType != "task.status_changed" {
		return nil
	}

	var payload struct {
		TaskID            uuid.UUID  `json:"task_id"`
		ProjectID         uuid.UUID  `json:"project_id"`
		ToStatus          string     `json:"to_status"`
		TransitionSource  string     `json:"transition_source"`
		FlowEventType     string     `json:"flow_event_type"`
		ToFlowExecutionID *uuid.UUID `json:"to_flow_execution_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil
	}
	if payload.TaskID == uuid.Nil || !taskStatusStartsAsyncWork(payload.ToStatus) {
		return nil
	}
	if statusChangeOwnedByFlowTransition(payload.TransitionSource, payload.FlowEventType, payload.ToFlowExecutionID) {
		return nil
	}

	return p.processQueuedTask(ctx, event, payload.TaskID)
}

func statusChangeOwnedByFlowTransition(transitionSource, flowEventType string, toFlowExecutionID *uuid.UUID) bool {
	if !strings.EqualFold(strings.TrimSpace(transitionSource), "flow_transition") {
		return false
	}
	if toFlowExecutionID == nil || *toFlowExecutionID == uuid.Nil {
		return false
	}
	switch strings.TrimSpace(flowEventType) {
	case "flow.advanced", "flow.rejected":
		return true
	default:
		return false
	}
}

func taskStatusStartsAsyncWork(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "in_progress", "review":
		return true
	default:
		return false
	}
}

func (p *TaskQueueProcessor) processQueuedTask(ctx context.Context, event eventbus.DomainEvent, taskID uuid.UUID) error {
	taskRecord, err := p.tasks.GetByID(ctx, taskID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if paused, pauseErr := p.projectPaused(ctx, taskRecord.ProjectID); pauseErr != nil {
		return pauseErr
	} else if paused {
		return nil
	}
	if blocked, blockErr := p.isBlockedByOutstandingProjectGate(ctx, taskRecord); blockErr != nil {
		return blockErr
	} else if blocked {
		return nil
	}

	status := strings.ToLower(strings.TrimSpace(taskRecord.WorkStatus))
	if status == "queued" {
		if _, err := p.ensureTaskFlowExecutionState(ctx, taskRecord); err != nil {
			return err
		}
		targetStatus, err := p.queuedTaskRuntimeStatus(ctx, taskRecord)
		if err != nil {
			return err
		}
		if _, err := p.taskService.TransitionStatus(ctx, taskID, targetStatus, tasksvc.Actor{Type: "system", ExpectedFromStatus: "queued"}); err != nil {
			if errors.Is(err, tasksvc.ErrRequiresHumanApproval) {
				if _, approvalErr := p.taskService.RequestHumanApproval(ctx, taskID); approvalErr != nil && !errors.Is(approvalErr, tasksvc.ErrHumanReviewNotRequired) {
					return approvalErr
				}
				if _, holdErr := p.taskService.TransitionStatus(ctx, taskID, "on_hold", tasksvc.Actor{Type: "system", ExpectedFromStatus: "queued"}); holdErr != nil {
					var holdTransitionErr tasksvc.ErrInvalidStatusTransition
					if !errors.As(holdErr, &holdTransitionErr) && !errors.Is(holdErr, repo.ErrConflict) {
						return holdErr
					}
				}
			} else {
				var transitionErr tasksvc.ErrInvalidStatusTransition
				if !errors.As(err, &transitionErr) && !errors.Is(err, repo.ErrConflict) {
					return err
				}
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
		if taskRecord.FlowTemplateID != nil && strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "review") {
			_, err := p.ensureTaskFlowExecutionState(ctx, taskRecord)
			return err
		}
		return nil
	}
	if _, err := p.ensureTaskFlowExecutionState(ctx, taskRecord); err != nil {
		return err
	}
	taskRecord, err = p.tasks.GetByID(ctx, taskID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if paused, err := p.applyAsyncDecisionPolicy(ctx, event, taskRecord); err != nil {
		return err
	} else if paused {
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

func (p *TaskQueueProcessor) queuedTaskRuntimeStatus(ctx context.Context, taskRecord repo.ProjectTask) (string, error) {
	if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID == uuid.Nil || p.flowNodes == nil {
		return "in_progress", nil
	}
	currentNode, err := p.flowNodes.GetByID(ctx, *taskRecord.CurrentFlowNodeID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return "in_progress", nil
		}
		return "", err
	}
	if strings.EqualFold(strings.TrimSpace(currentNode.NodeType), "review") || currentNode.RequiresHumanReview {
		return "review", nil
	}
	return "in_progress", nil
}

func (p *TaskQueueProcessor) isBlockedByOutstandingProjectGate(ctx context.Context, taskRecord repo.ProjectTask) (bool, error) {
	projectTasks, err := p.tasks.ListByProject(ctx, taskRecord.ProjectID)
	if err != nil {
		return false, err
	}
	gateTask := lowestOutstandingGateTask(projectTasks)
	if gateTask == nil {
		return false, nil
	}
	return gateTask.ID != taskRecord.ID, nil
}

func (p *TaskQueueProcessor) processNextEligibleQueuedTask(ctx context.Context, event eventbus.DomainEvent, projectID uuid.UUID) error {
	projectTasks, err := p.tasks.ListByProject(ctx, projectID)
	if err != nil {
		return err
	}
	next := selectNextQueuedTaskUnderProjectGate(projectTasks)
	if next == nil {
		return nil
	}
	return p.processQueuedTask(ctx, event, next.ID)
}

func (p *TaskQueueProcessor) applyAsyncDecisionPolicy(ctx context.Context, event eventbus.DomainEvent, taskRecord repo.ProjectTask) (bool, error) {
	decision := taskplan.AssessAsyncDecision(taskRecord.Title, taskRecord.Description)
	if !decision.NeedsReviewArtifact() {
		return false, nil
	}
	if err := p.createAsyncDecisionArtifact(ctx, taskRecord, decision); err != nil {
		return false, err
	}
	if !decision.PausesTask() {
		return false, nil
	}
	if _, err := p.ensureTaskFlowExecutionState(ctx, taskRecord); err != nil {
		return false, err
	}
	if taskRecord.FlowTemplateID != nil {
		if _, err := p.flow.PauseAtReviewCheckpoint(ctx, taskRecord.ID, flowsvc.Actor{Type: "system"}); err != nil {
			return false, err
		}
	} else {
		if _, err := p.taskService.TransitionStatus(ctx, taskRecord.ID, "review", tasksvc.Actor{Type: "system"}); err != nil {
			var transitionErr tasksvc.ErrInvalidStatusTransition
			if !errors.As(err, &transitionErr) {
				return false, err
			}
		}
	}
	if err := p.processNextEligibleQueuedTask(ctx, event, taskRecord.ProjectID); err != nil {
		return false, err
	}
	return true, nil
}

func (p *TaskQueueProcessor) ensureTaskFlowExecutionState(ctx context.Context, taskRecord repo.ProjectTask) (*repo.FlowNodeExecution, error) {
	if taskRecord.FlowTemplateID == nil {
		return nil, nil
	}
	if p.flow != nil {
		return p.flow.EnsureActiveExecution(ctx, taskRecord.ID)
	}
	if taskRecord.CurrentFlowNodeID == nil || p.flowExecutions == nil {
		return nil, nil
	}
	execution, err := p.flowExecutions.GetActive(ctx, taskRecord.ID, *taskRecord.CurrentFlowNodeID)
	if err != nil {
		return nil, err
	}
	return &execution, nil
}

func (p *TaskQueueProcessor) createAsyncDecisionArtifact(ctx context.Context, taskRecord repo.ProjectTask, decision taskplan.AsyncDecision) error {
	title, body := buildAsyncDecisionArtifact(taskRecord, decision)
	payload, err := json.Marshal(map[string]any{
		"policy":       asyncDecisionPolicyName,
		"outcome":      decision.Outcome,
		"reason":       decision.Reason,
		"task_id":      taskRecord.ID.String(),
		"project_id":   taskRecord.ProjectID.String(),
		"task_number":  taskRecord.TaskNumber,
		"task_title":   strings.TrimSpace(taskRecord.Title),
		"blocks_scope": strings.TrimSpace(taskRecord.BlocksScope),
	})
	if err != nil {
		return err
	}
	_, err = p.taskService.CreateInboxItem(ctx, tasksvc.CreateInboxItemRequest{
		OrganizationID:  taskRecord.OrganizationID,
		TargetUserID:    nil,
		ItemType:        "system_alert",
		SourceProjectID: &taskRecord.ProjectID,
		SourceTaskID:    &taskRecord.ID,
		CreatedByType:   "system",
		Title:           title,
		Body:            &body,
		ActionPayload:   payload,
	})
	return err
}

func buildAsyncDecisionArtifact(taskRecord repo.ProjectTask, decision taskplan.AsyncDecision) (string, string) {
	taskLabel := fmt.Sprintf("task #%d", taskRecord.TaskNumber)
	if taskRecord.TaskNumber == 0 {
		taskLabel = "task"
	}

	statusLine := "Async work will continue while this is surfaced for later review."
	switch decision.Outcome {
	case taskplan.AsyncDecisionPrepareForReview:
		statusLine = "The task has been paused in review at its human checkpoint."
	case taskplan.AsyncDecisionHardStop:
		statusLine = "The task has been paused in review because this decision should not be guessed."
	}

	title := "Async review note for " + taskLabel
	switch decision.Outcome {
	case taskplan.AsyncDecisionPrepareForReview:
		title = "Review checkpoint for " + taskLabel
	case taskplan.AsyncDecisionHardStop:
		title = "Hard stop for " + taskLabel
	}

	taskTitle := strings.TrimSpace(taskRecord.Title)
	if taskTitle == "" {
		taskTitle = "Untitled task"
	}
	body := fmt.Sprintf("%s: %s\n\nOutcome: %s\nReason: %s\n\n%s", taskLabel, taskTitle, decision.Outcome, decision.Reason, statusLine)
	return title, body
}

func lowestOutstandingGateTask(tasks []repo.ProjectTask) *repo.ProjectTask {
	canonicalBootstrapGateID, hasCanonicalBootstrapGate := canonicalBootstrapGateTaskID(tasks)
	var selected *repo.ProjectTask
	for _, taskRecord := range tasks {
		if !strings.EqualFold(strings.TrimSpace(taskRecord.BlocksScope), "all") {
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
			if err := tasksvc.ValidateProjectGateTask(taskRecord); err != nil {
				continue
			}
		}
		if selected == nil || taskRecord.TaskNumber < selected.TaskNumber {
			clone := taskRecord
			selected = &clone
		}
	}
	return selected
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

func selectNextQueuedTaskUnderProjectGate(tasks []repo.ProjectTask) *repo.ProjectTask {
	if gate := lowestOutstandingGateTask(tasks); gate != nil {
		if strings.EqualFold(strings.TrimSpace(gate.WorkStatus), "queued") {
			clone := *gate
			return &clone
		}
		return nil
	}

	var selected *repo.ProjectTask
	for _, taskRecord := range tasks {
		if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "queued") {
			continue
		}
		if selected == nil || taskRecord.TaskNumber < selected.TaskNumber {
			clone := taskRecord
			selected = &clone
		}
	}
	return selected
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

func (p *TaskQueueProcessor) ensureFlowRun(ctx context.Context, event eventbus.DomainEvent, taskRecord repo.ProjectTask) error {
	execution, err := p.ensureTaskFlowExecutionState(ctx, taskRecord)
	if err != nil {
		return err
	}
	if execution == nil {
		return fmt.Errorf("task %s missing active flow execution after EnsureActiveExecution", taskRecord.ID)
	}
	taskRecord.CurrentFlowNodeID = &execution.FlowNodeID

	idempotencyKey := fmt.Sprintf("task-queued:flow:%s:%s", taskRecord.ID, event.ID)
	metadata, err := json.Marshal(map[string]any{
		"source":                 "task_queue_processor",
		"task_status_event_id":   event.ID,
		"flow_node_execution_id": execution.ID,
		"run_mode":               "async",
		"wake_kind":              "flow_current",
	})
	if err != nil {
		return err
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return err
	}
	appendValidationRecoveryMetadata(payload, event.Payload)
	metadata, err = json.Marshal(payload)
	if err != nil {
		return err
	}

	principalType := "system"
	principalID := uuid.Nil
	if taskRecord.AssignedAgentID != nil && *taskRecord.AssignedAgentID != uuid.Nil {
		principalType = "agent"
		principalID = *taskRecord.AssignedAgentID
	}
	repairedExecution, err := p.repairFlowExecutionSession(ctx, taskRecord, *execution, principalID)
	if err != nil {
		return err
	}
	execution = &repairedExecution

	wakeupPayload := map[string]any{
		"source":                 "task_queue_processor",
		"task_status_event_id":   event.ID.String(),
		"flow_node_execution_id": execution.ID.String(),
		"run_mode":               "async",
	}
	appendValidationRecoveryMetadata(wakeupPayload, event.Payload)

	result, err := p.runs.CreateExecutionWakeup(ctx, executionWakeupInput{
		CreateRunInput: CreateRunInput{
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
		},
		WakeupSource:  "task_queue_processor",
		WakeupKind:    "flow_current",
		WakeupPayload: wakeupPayload,
	})
	if err != nil {
		return err
	}
	if execution.ID != uuid.Nil {
		if _, err := p.flowExecutions.UpdateMetadata(ctx, execution.ID, repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{
			RunID: &result.Run.ID,
		})); err != nil {
			return err
		}
	}
	if !result.shouldDispatch() {
		return nil
	}
	return p.dispatchWakeupRun(ctx, result.Run)
}

func (p *TaskQueueProcessor) ensureAssignedAgentRun(ctx context.Context, event eventbus.DomainEvent, taskRecord repo.ProjectTask) error {
	if taskOwnedByFlowExecution(taskRecord) {
		return nil
	}
	if taskRecord.AssignedAgentID == nil || *taskRecord.AssignedAgentID == uuid.Nil {
		return nil
	}

	session, err := p.ensureCanonicalTaskAsyncSession(ctx, taskRecord)
	if err != nil {
		return err
	}
	if session == nil {
		return fmt.Errorf("task %s missing async session", taskRecord.ID)
	}
	idempotencyKey := fmt.Sprintf("task-queued:agent-turn:%s:%s", taskRecord.ID, event.ID)
	metadata, err := json.Marshal(map[string]any{
		"source":               "task_queue_processor",
		"task_status_event_id": event.ID,
		"run_mode":             "async",
		"wake_kind":            "assigned_task",
	})
	if err != nil {
		return err
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return err
	}
	appendValidationRecoveryMetadata(payload, event.Payload)
	metadata, err = json.Marshal(payload)
	if err != nil {
		return err
	}

	wakeupPayload := map[string]any{
		"source":               "task_queue_processor",
		"task_status_event_id": event.ID.String(),
		"run_mode":             "async",
	}
	appendValidationRecoveryMetadata(wakeupPayload, event.Payload)

	result, err := p.runs.CreateExecutionWakeup(ctx, executionWakeupInput{
		CreateRunInput: CreateRunInput{
			OrganizationID: taskRecord.OrganizationID,
			PrincipalType:  "agent",
			PrincipalID:    *taskRecord.AssignedAgentID,
			TriggerType:    taskQueueTriggerType,
			ProjectID:      &taskRecord.ProjectID,
			TaskID:         &taskRecord.ID,
			SessionID:      &session.ID,
			IdempotencyKey: &idempotencyKey,
			Metadata:       metadata,
		},
		WakeupSource:  "task_queue_processor",
		WakeupKind:    "assigned_task",
		WakeupPayload: wakeupPayload,
	})
	if err != nil {
		return err
	}
	if !result.shouldDispatch() {
		return nil
	}
	return p.dispatchWakeupRun(ctx, result.Run)
}

func (p *TaskQueueProcessor) ensureCanonicalTaskAsyncSession(ctx context.Context, taskRecord repo.ProjectTask) (*repo.ChatSession, error) {
	if taskRecord.ID == uuid.Nil {
		return nil, fmt.Errorf("task id is required")
	}
	return p.chats.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: taskRecord.OrganizationID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Metadata:       json.RawMessage(`{"source":"task_queue_processor"}`),
	})
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

func (p *TaskQueueProcessor) dispatchWakeupRun(ctx context.Context, runRecord Run) error {
	switch strings.TrimSpace(executionWakeupSource(runRecord.Metadata)) {
	case "task_queue_processor":
		return p.dispatchTaskQueueWakeup(ctx, runRecord)
	case "supervisor":
		return p.dispatchSupervisorWakeup(ctx, runRecord)
	default:
		return nil
	}
}

func (p *TaskQueueProcessor) dispatchTaskQueueWakeup(ctx context.Context, runRecord Run) error {
	if runRecord.TaskID == nil || *runRecord.TaskID == uuid.Nil {
		return nil
	}
	taskRecord, err := p.tasks.GetByID(ctx, *runRecord.TaskID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	switch strings.TrimSpace(executionWakeupKind(runRecord.Metadata)) {
	case "assigned_task":
		if taskOwnedByFlowExecution(taskRecord) {
			return nil
		}
		session, err := p.ensureCanonicalTaskAsyncSession(ctx, taskRecord)
		if err != nil {
			return err
		}
		if session == nil || session.ID == uuid.Nil {
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
		var payload map[string]any
		if err := json.Unmarshal(messageMetadata, &payload); err != nil {
			return err
		}
		appendWakeupRecoveryMetadata(payload, runRecord.Metadata)
		messageMetadata, err = json.Marshal(payload)
		if err != nil {
			return err
		}
		return p.appendWakeupKickoff(ctx, runRecord, session.ID, buildQueueKickoffMessage(taskRecord), messageMetadata)
	case "flow_current":
		executionID, ok := metadataUUIDValue(runRecord.Metadata, "flow_node_execution_id")
		if !ok {
			return nil
		}
		execution, err := p.flowExecutions.GetByID(ctx, executionID)
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		principalID := runRecord.PrincipalID
		if principalID == uuid.Nil && taskRecord.AssignedAgentID != nil {
			principalID = *taskRecord.AssignedAgentID
		}
		execution, err = p.repairFlowExecutionSession(ctx, taskRecord, execution, principalID)
		if err != nil {
			return err
		}
		if execution.SessionID == nil || *execution.SessionID == uuid.Nil {
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
		var payload map[string]any
		if err := json.Unmarshal(messageMetadata, &payload); err != nil {
			return err
		}
		appendWakeupRecoveryMetadata(payload, runRecord.Metadata)
		messageMetadata, err = json.Marshal(payload)
		if err != nil {
			return err
		}
		var flowNode *repo.FlowNode
		if p.flowNodes != nil {
			if node, nodeErr := p.flowNodes.GetByID(ctx, execution.FlowNodeID); nodeErr == nil {
				flowNode = &node
			}
		}
		if err := p.appendWakeupKickoff(ctx, runRecord, *execution.SessionID, buildFlowKickoffMessage(taskRecord, execution, flowNode), messageMetadata); err != nil {
			return err
		}
		return p.markExecutionRunning(ctx, execution.ID)
	case "flow_transition":
		executionID, ok := metadataUUIDValue(runRecord.Metadata, "flow_node_execution_id")
		if !ok {
			return nil
		}
		execution, err := p.flowExecutions.GetByID(ctx, executionID)
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if runRecord.FlowNodeID == nil || *runRecord.FlowNodeID == uuid.Nil {
			return nil
		}
		node, err := p.flowNodes.GetByID(ctx, *runRecord.FlowNodeID)
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		principalID := runRecord.PrincipalID
		if principalID == uuid.Nil && taskRecord.AssignedAgentID != nil {
			principalID = *taskRecord.AssignedAgentID
		}
		execution, err = p.repairFlowExecutionSession(ctx, taskRecord, execution, principalID)
		if err != nil {
			return err
		}
		if execution.SessionID == nil || *execution.SessionID == uuid.Nil {
			return nil
		}
		eventType := metadataStringValue(runRecord.Metadata, "flow_event_type")
		rejectionFeedback := metadataStringValue(runRecord.Metadata, "rejection_feedback")
		messageMetadata, err := json.Marshal(map[string]any{
			"source":                 "task_queue_processor",
			"run_id":                 runRecord.ID.String(),
			"task_id":                taskRecord.ID.String(),
			"flow_node_execution_id": execution.ID.String(),
			"flow_event_type":        eventType,
		})
		if err != nil {
			return err
		}
		if err := p.appendWakeupKickoff(ctx, runRecord, *execution.SessionID, buildFlowTransitionKickoffMessage(taskRecord, node, execution, eventType, rejectionFeedback), messageMetadata); err != nil {
			return err
		}
		return p.markExecutionRunning(ctx, execution.ID)
	default:
		return nil
	}
}

func (p *TaskQueueProcessor) markExecutionRunning(ctx context.Context, executionID uuid.UUID) error {
	if executionID == uuid.Nil || p.flowExecutions == nil {
		return nil
	}
	running := "running"
	_, err := p.flowExecutions.UpdateRuntimeSubstate(ctx, executionID, &running)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	return err
}

func (p *TaskQueueProcessor) dispatchSupervisorWakeup(ctx context.Context, runRecord Run) error {
	if runRecord.SessionID == nil || *runRecord.SessionID == uuid.Nil {
		return nil
	}
	messageMetadata, err := json.Marshal(map[string]any{
		"source": "supervisor",
		"run_id": runRecord.ID.String(),
		"reason": metadataStringValue(runRecord.Metadata, "supervisor_recovery_reason"),
	})
	if err != nil {
		return err
	}
	return p.appendWakeupKickoff(ctx, runRecord, *runRecord.SessionID, "supervisor recovery: resume task", messageMetadata)
}

func (p *TaskQueueProcessor) appendWakeupKickoff(ctx context.Context, runRecord Run, sessionID uuid.UUID, content string, metadata json.RawMessage) error {
	if sessionID == uuid.Nil {
		return nil
	}
	if runRecord.PrincipalType == "agent" && runRecord.PrincipalID != uuid.Nil {
		if _, err := p.chats.AddParticipant(ctx, sessionID, "agent", runRecord.PrincipalID, "responder"); err != nil && !errors.Is(err, chat.ErrAlreadyParticipant) {
			return err
		}
	}
	hasKickoffMessage, err := p.sessionHasKickoffMessage(ctx, sessionID, runRecord.ID)
	if err != nil {
		return err
	}
	if hasKickoffMessage {
		return nil
	}
	_, err = p.chats.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: sessionID,
		Role:      "user",
		Content:   content,
		Metadata:  metadata,
	})
	return err
}

func taskOwnedByFlowExecution(taskRecord repo.ProjectTask) bool {
	if taskRecord.FlowTemplateID != nil && *taskRecord.FlowTemplateID != uuid.Nil {
		return true
	}
	return taskRecord.CurrentFlowNodeID != nil && *taskRecord.CurrentFlowNodeID != uuid.Nil
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

func buildFlowKickoffMessage(taskRecord repo.ProjectTask, execution repo.FlowNodeExecution, node *repo.FlowNode) string {
	base := buildQueueKickoffMessage(taskRecord)
	if node != nil && strings.EqualFold(strings.TrimSpace(node.NodeType), "review") {
		title := strings.TrimSpace(taskRecord.Title)
		if title == "" {
			title = "Untitled task"
		}
		description := strings.TrimSpace(valueOrEmpty(taskRecord.Description))
		base = "Start review on task: " + title
		if description != "" {
			base += "\n\nTask description:\n" + description
		}
		base += "\n\nReview instruction:\nInspect the current deliverables and use flow.review_decision to approve or reject this review step."
	}
	if execution.ID == uuid.Nil {
		return base
	}
	return base + "\n\nFlow node execution: " + execution.ID.String()
}

// SubscribeTaskCompleted subscribes to task status events that should settle
// tracking runs. Terminal task outcomes complete or retire runtime state; blocked
// outcomes fail the active tracking runs so supervisor recovery does not churn.
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
		TaskID        uuid.UUID `json:"task_id"`
		ProjectID     uuid.UUID `json:"project_id"`
		ToStatus      string    `json:"to_status"`
		BlockerReason string    `json:"blocker_reason"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil
	}

	toStatus := strings.ToLower(strings.TrimSpace(payload.ToStatus))
	if toStatus != "done" && toStatus != "cancelled" && toStatus != "blocked" {
		return nil
	}
	if payload.TaskID == uuid.Nil || event.OrganizationID == uuid.Nil {
		return nil
	}
	var completedTask repo.ProjectTask
	if p.tasks != nil {
		var err error
		completedTask, err = p.tasks.GetByID(ctx, payload.TaskID)
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
	}

	for _, triggerType := range []string{taskQueueTriggerType, taskSupervisorTriggerType} {
		// Complete terminal task tracking runs, or fail them permanently when the
		// task moved to blocked so recovery does not keep waking the same work.
		if runs, err := p.runs.ListRunsByTask(ctx, event.OrganizationID, payload.TaskID, "in_progress", triggerType); err == nil {
			for _, r := range runs {
				if toStatus == "blocked" {
					reason := strings.TrimSpace(payload.BlockerReason)
					if reason == "" {
						reason = "task blocked"
					}
					_ = p.runs.FailRun(ctx, r.ID, reason, string(FailureClassPermanent))
					continue
				}
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
	if toStatus == "blocked" {
		if err := p.abandonActiveFlowExecutionsForTask(ctx, payload.TaskID); err != nil {
			return err
		}
		if err := p.runs.RetireRuntimeStateForTask(ctx, payload.TaskID, toStatus); err != nil {
			return err
		}
		if payload.ProjectID != uuid.Nil {
			if err := p.processNextEligibleQueuedTask(ctx, event, payload.ProjectID); err != nil {
				return err
			}
		}
		return nil
	}
	if toStatus == "done" {
		if err := p.maybeAutoCompleteParentTask(ctx, completedTask); err != nil {
			return err
		}
		if err := p.maybeAutoCompleteDormantParentTasks(ctx, completedTask.ProjectID); err != nil {
			return err
		}
		if err := p.maybeAutoCompleteBootstrapPlanningTasks(ctx, completedTask.ProjectID); err != nil {
			return err
		}
	}
	if err := p.runs.RetireRuntimeStateForTask(ctx, payload.TaskID, toStatus); err != nil {
		return err
	}
	if payload.ProjectID != uuid.Nil {
		if err := p.processNextEligibleQueuedTask(ctx, event, payload.ProjectID); err != nil {
			return err
		}
	}
	return nil
}

func (p *TaskQueueProcessor) abandonActiveFlowExecutionsForTask(ctx context.Context, taskID uuid.UUID) error {
	if p.flowExecutions == nil || taskID == uuid.Nil {
		return nil
	}
	executions, err := p.flowExecutions.ListByTask(ctx, taskID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, execution := range executions {
		if !strings.EqualFold(strings.TrimSpace(execution.Status), "active") {
			continue
		}
		if _, err := p.flowExecutions.Abandon(ctx, execution.ID); err != nil && !errors.Is(err, repo.ErrNotFound) {
			return err
		}
	}
	return nil
}

func (p *TaskQueueProcessor) maybeAutoCompleteParentTask(ctx context.Context, completedTask repo.ProjectTask) error {
	parentID := taskdecomp.ParseParentTaskID(completedTask.Metadata)
	if parentID == uuid.Nil || p.taskService == nil {
		return nil
	}
	_, err := p.taskService.TransitionStatus(ctx, parentID, "done", tasksvc.Actor{
		Type:                           "system",
		AllowOrchestrationAutoComplete: true,
	})
	if err == nil || errors.Is(err, repo.ErrConflict) || errors.Is(err, taskorchestration.ErrParentCompletionRequirements) {
		return nil
	}
	var invalidTransition tasksvc.ErrInvalidStatusTransition
	if errors.As(err, &invalidTransition) {
		return nil
	}
	return err
}

func (p *TaskQueueProcessor) maybeAutoCompleteDormantParentTasks(ctx context.Context, projectID uuid.UUID) error {
	if projectID == uuid.Nil || p.tasks == nil || p.taskService == nil {
		return nil
	}
	for pass := 0; pass < 4; pass++ {
		tasks, err := p.tasks.ListByProject(ctx, projectID, "draft")
		if err != nil {
			return err
		}
		progressed := false
		for _, task := range tasks {
			if len(taskdecomp.ParseChildTaskIDs(task.Metadata)) == 0 {
				continue
			}
			_, err := p.taskService.TransitionStatus(ctx, task.ID, "done", tasksvc.Actor{
				Type:                           "system",
				AllowOrchestrationAutoComplete: true,
			})
			if err == nil {
				progressed = true
				continue
			}
			if errors.Is(err, repo.ErrConflict) || errors.Is(err, taskorchestration.ErrParentCompletionRequirements) {
				continue
			}
			var invalidTransition tasksvc.ErrInvalidStatusTransition
			if errors.As(err, &invalidTransition) {
				continue
			}
			return err
		}
		if !progressed {
			return nil
		}
	}
	return nil
}

func (p *TaskQueueProcessor) maybeAutoCompleteBootstrapPlanningTasks(ctx context.Context, projectID uuid.UUID) error {
	if projectID == uuid.Nil || p.tasks == nil || p.taskService == nil {
		return nil
	}
	tasks, err := p.tasks.ListByProject(ctx, projectID, "draft")
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if !strings.HasPrefix(strings.TrimSpace(task.Title), "Bootstrap:") {
			continue
		}
		_, err := p.taskService.TransitionStatus(ctx, task.ID, "done", tasksvc.Actor{
			Type:                               "system",
			AllowBootstrapPlanningAutoComplete: true,
		})
		if err == nil || errors.Is(err, repo.ErrConflict) || errors.Is(err, taskplan.ErrPlanningArtifactContractIncomplete) {
			continue
		}
		var invalidTransition tasksvc.ErrInvalidStatusTransition
		if errors.As(err, &invalidTransition) {
			continue
		}
		return err
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
		HoldForAsyncReview     bool       `json:"hold_for_async_review"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil
	}
	if payload.TaskID == uuid.Nil || payload.ProjectID == uuid.Nil || payload.TerminalTransitionDone {
		return nil
	}
	if paused, err := p.projectPaused(ctx, payload.ProjectID); err != nil {
		return err
	} else if paused {
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
	if !taskFlowEventMatchesRuntime(taskRecord, nextNode, nextExecution) {
		return nil
	}
	if payload.HoldForAsyncReview {
		return nil
	}
	if nextNode.NextNodeID == nil && !strings.EqualFold(strings.TrimSpace(nextNode.NodeType), "review") && !nextNode.RequiresHumanReview {
		if p.flow == nil {
			return nil
		}
		_, err := p.flow.AdvanceFlow(ctx, taskRecord.ID, flowsvc.Actor{Type: "system"})
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

func (p *TaskQueueProcessor) handleProjectResumedEvent(ctx context.Context, event eventbus.DomainEvent) error {
	if event.EventType != "project.resumed" {
		return nil
	}

	var payload struct {
		ProjectID uuid.UUID `json:"project_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil
	}
	if payload.ProjectID == uuid.Nil {
		return nil
	}
	if promoter, ok := p.runs.(taskQueueDeferredProjectPromoter); ok {
		runs, err := promoter.PromoteDeferredWakeupsForProject(ctx, payload.ProjectID)
		if err != nil {
			return err
		}
		for _, runRecord := range runs {
			if err := p.dispatchWakeupRun(ctx, runRecord); err != nil {
				return err
			}
		}
	}
	if err := p.maybeAutoCompleteDormantParentTasks(ctx, payload.ProjectID); err != nil {
		return err
	}
	if err := p.maybeAutoCompleteBootstrapPlanningTasks(ctx, payload.ProjectID); err != nil {
		return err
	}
	return p.processNextEligibleQueuedTask(ctx, event, payload.ProjectID)
}

func (p *TaskQueueProcessor) handleProjectArchivedEvent(ctx context.Context, event eventbus.DomainEvent) error {
	if event.EventType != "project.archived" {
		return nil
	}

	var payload struct {
		ProjectID uuid.UUID `json:"project_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil
	}
	if payload.ProjectID == uuid.Nil {
		return nil
	}
	return p.runs.RetireRuntimeStateForProject(ctx, payload.ProjectID, "project_archived")
}

func (p *TaskQueueProcessor) handleTurnTerminalEvent(ctx context.Context, event eventbus.DomainEvent) error {
	if event.EventType != "chat.turn.completed" && event.EventType != "chat.turn.cancelled" {
		return nil
	}

	var payload struct {
		SessionID uuid.UUID `json:"session_id"`
		TurnID    uuid.UUID `json:"turn_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil
	}
	if payload.SessionID == uuid.Nil {
		return nil
	}

	session, err := p.chats.GetSession(ctx, payload.SessionID)
	if errors.Is(err, repo.ErrNotFound) || session == nil {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project_task") || session.ScopeID == uuid.Nil {
		return nil
	}
	taskRecord, err := p.tasks.GetByID(ctx, session.ScopeID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	releaseRunID, err := p.resolveTurnRunID(ctx, payload.TurnID)
	if err != nil {
		return err
	}
	if paused, err := p.projectPaused(ctx, taskRecord.ProjectID); err != nil {
		return err
	} else if paused {
		if releaser, ok := p.runs.(taskQueueDeferredWakeupReleaser); ok {
			if releaseRunID != uuid.Nil {
				_, err = releaser.ReleaseExecutionOwnerForRunHoldingDeferred(ctx, session.ScopeID, session.ID, releaseRunID, event.EventType)
			} else {
				_, err = releaser.ReleaseExecutionOwnerHoldingDeferred(ctx, session.ScopeID, session.ID, event.EventType)
			}
			return err
		}
		return nil
	}
	var result executionWakeupResult
	if releaseRunID != uuid.Nil {
		result, err = p.runs.ReleaseExecutionOwnerForRun(ctx, session.ScopeID, session.ID, releaseRunID, event.EventType)
	} else {
		result, err = p.runs.ReleaseExecutionOwner(ctx, session.ScopeID, session.ID, event.EventType)
	}
	if err != nil {
		return err
	}
	if !taskStatusStartsAsyncWork(taskRecord.WorkStatus) {
		return nil
	}
	if !result.shouldDispatch() {
		return nil
	}
	return p.dispatchWakeupRun(ctx, result.Run)
}

func (p *TaskQueueProcessor) resolveTurnRunID(ctx context.Context, turnID uuid.UUID) (uuid.UUID, error) {
	if turnID == uuid.Nil {
		return uuid.Nil, nil
	}
	turn, err := p.chats.GetTurn(ctx, turnID)
	if errors.Is(err, repo.ErrNotFound) || turn == nil {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	if turn.TriggerMessageID == nil || *turn.TriggerMessageID == uuid.Nil {
		return uuid.Nil, nil
	}
	message, err := p.chats.GetMessage(ctx, *turn.TriggerMessageID)
	if errors.Is(err, repo.ErrNotFound) || message == nil {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	runID, _ := metadataUUIDValue(message.Metadata, "run_id")
	return runID, nil
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
		"wake_kind":              "flow_transition",
		"rejection_feedback":     strings.TrimSpace(rejectionFeedback),
	})
	if err != nil {
		return err
	}

	sessionID := nextExecution.SessionID
	repairedExecution, err := p.repairFlowExecutionSession(ctx, taskRecord, nextExecution, agentID)
	if err != nil {
		return err
	}
	sessionID = repairedExecution.SessionID
	result, err := p.runs.CreateExecutionWakeup(ctx, executionWakeupInput{
		CreateRunInput: CreateRunInput{
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
		},
		WakeupSource: "task_queue_processor",
		WakeupKind:   "flow_transition",
		WakeupPayload: map[string]any{
			"source":                 "task_queue_processor",
			"flow_event_id":          event.ID.String(),
			"flow_event_type":        event.EventType,
			"flow_node_execution_id": nextExecution.ID.String(),
			"run_mode":               "async",
			"rejection_feedback":     strings.TrimSpace(rejectionFeedback),
		},
	})
	if err != nil {
		return err
	}
	if !result.shouldDispatch() {
		return nil
	}
	return p.dispatchWakeupRun(ctx, result.Run)
}

func (p *TaskQueueProcessor) repairFlowExecutionSession(ctx context.Context, taskRecord repo.ProjectTask, execution repo.FlowNodeExecution, agentID uuid.UUID) (repo.FlowNodeExecution, error) {
	if execution.SessionID != nil && *execution.SessionID != uuid.Nil {
		session, err := p.chats.GetSession(ctx, *execution.SessionID)
		switch {
		case err == nil:
			if session != nil && session.ID != uuid.Nil &&
				!strings.EqualFold(strings.TrimSpace(session.Status), "closed") &&
				!strings.EqualFold(strings.TrimSpace(session.Status), "archived") {
				return execution, nil
			}
		case errors.Is(err, repo.ErrNotFound):
			// Repair missing execution sessions by creating a fresh node session below.
		default:
			return execution, err
		}
	}
	if agentID == uuid.Nil {
		return execution, fmt.Errorf("task %s execution %s missing session_id and no agent available to repair it", taskRecord.ID, execution.ID)
	}
	session, err := p.chats.GetOrCreateNodeSession(ctx, execution.ID, agentID)
	if err != nil {
		return execution, err
	}
	if session == nil || session.ID == uuid.Nil {
		return execution, fmt.Errorf("task %s execution %s failed to repair node session", taskRecord.ID, execution.ID)
	}
	execution.SessionID = &session.ID
	return execution, nil
}

func taskFlowEventMatchesRuntime(taskRecord repo.ProjectTask, nextNode repo.FlowNode, nextExecution repo.FlowNodeExecution) bool {
	if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != nextNode.ID {
		return false
	}
	if nextExecution.TaskID != taskRecord.ID || nextExecution.FlowNodeID != nextNode.ID {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(nextExecution.Status), "active") {
		return false
	}
	expectedStatus := "in_progress"
	if strings.EqualFold(strings.TrimSpace(nextNode.NodeType), "review") || nextNode.RequiresHumanReview {
		expectedStatus = "review"
	}
	return strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), expectedStatus)
}

func (p *TaskQueueProcessor) resolveFlowTransitionAgent(ctx context.Context, taskRecord repo.ProjectTask, node repo.FlowNode) (*uuid.UUID, error) {
	actorType := strings.ToLower(strings.TrimSpace(valueOrEmpty(node.ActorType)))
	switch actorType {
	case "":
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
	case "human":
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

func (p *TaskQueueProcessor) projectPaused(ctx context.Context, projectID uuid.UUID) (bool, error) {
	if p.projects == nil || projectID == uuid.Nil {
		return false, nil
	}
	projectRecord, err := p.projects.GetByID(ctx, projectID)
	if errors.Is(err, repo.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if strings.EqualFold(strings.TrimSpace(projectRecord.Status), "archived") {
		return true, nil
	}
	return projectpause.Parse(projectRecord.Settings).IsPaused, nil
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
			case "project_manager", "pm":
				return "project_manager"
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
	if strings.EqualFold(strings.TrimSpace(node.NodeType), "review") || node.RequiresHumanReview {
		title := strings.TrimSpace(taskRecord.Title)
		if title == "" {
			title = "Untitled task"
		}
		description := strings.TrimSpace(valueOrEmpty(taskRecord.Description))
		base = "Start review on task: " + title
		if description != "" {
			base += "\n\nTask description:\n" + description
		}
		base += "\n\nReview instruction:\nInspect the current deliverables and use flow.review_decision to approve or reject this review step."
	}
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

func valueAsStrings(value any) []string {
	switch typed := value.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(item)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(valueAsString(item))
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func metadataStringValue(metadata json.RawMessage, key string) string {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(valueAsString(payload[strings.TrimSpace(key)]))
}

func metadataUUIDValue(metadata json.RawMessage, key string) (uuid.UUID, bool) {
	raw := metadataStringValue(metadata, key)
	if raw == "" {
		return uuid.Nil, false
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return parsed, true
}

func appendValidationRecoveryMetadata(payload map[string]any, eventPayload json.RawMessage) {
	if payload == nil || len(eventPayload) == 0 || !json.Valid(eventPayload) {
		return
	}
	var decoded map[string]any
	if err := json.Unmarshal(eventPayload, &decoded); err != nil {
		return
	}
	action := strings.TrimSpace(valueAsString(decoded["recovery_action"]))
	if !tasksvc.IsRecoveryResumeAction(action) {
		return
	}
	payload["recovery_action"] = action
	for _, key := range []string{
		"recovery_blocker_class",
		"validation_tool_name",
		"validation_failure_code",
		"validation_failure_reason",
		"recovery_checkpoint_target_path",
		"recovery_checkpoint_artifact_path",
		"recovery_checkpoint_failure_reason",
	} {
		if value := strings.TrimSpace(valueAsString(decoded[key])); value != "" {
			payload[key] = value
		}
	}
	if values := valueAsStrings(decoded["recovery_checkpoint_prior_failure_reasons"]); len(values) != 0 {
		payload["recovery_checkpoint_prior_failure_reasons"] = values
	}
}

func appendWakeupRecoveryMetadata(payload map[string]any, metadata json.RawMessage) {
	if payload == nil {
		return
	}
	action := metadataStringValue(metadata, "recovery_action")
	if !tasksvc.IsRecoveryResumeAction(action) {
		return
	}
	payload["recovery_action"] = action
	for _, key := range []string{
		"recovery_blocker_class",
		"validation_tool_name",
		"validation_failure_code",
		"validation_failure_reason",
		"recovery_checkpoint_target_path",
		"recovery_checkpoint_artifact_path",
		"recovery_checkpoint_failure_reason",
	} {
		if value := metadataStringValue(metadata, key); value != "" {
			payload[key] = value
		}
	}
	if len(metadata) != 0 && json.Valid(metadata) {
		var decoded map[string]any
		if err := json.Unmarshal(metadata, &decoded); err == nil {
			if values := valueAsStrings(decoded["recovery_checkpoint_prior_failure_reasons"]); len(values) != 0 {
				payload["recovery_checkpoint_prior_failure_reasons"] = values
			}
		}
	}
}
