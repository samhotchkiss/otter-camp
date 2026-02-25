package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/budget"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

var (
	ErrRunIDRequired          = errors.New("run_id is required")
	ErrRunStepIDRequired      = errors.New("run_step_id is required")
	ErrInvalidTransition      = errors.New("invalid state transition")
	ErrTerminalState          = errors.New("run is already in terminal state")
	ErrMaxAttemptsExceeded    = errors.New("max attempts exceeded")
	ErrPolicyDenied           = errors.New("run creation denied by policy")
	ErrBudgetExceeded         = errors.New("run creation blocked by budget gate")
	ErrInvalidRetryTrigger    = errors.New("invalid retry trigger")
	ErrInvalidRequestedByType = errors.New("invalid requested_by actor_type")
)

var runTerminalStatuses = map[string]struct{}{
	"completed":   {},
	"failed":      {},
	"timed_out":   {},
	"cancelled":   {},
	"dead_letter": {},
}

type Principal struct {
	Type string
	ID   uuid.UUID
}

type CreateRunInput struct {
	OrganizationID uuid.UUID
	PrincipalType  string
	PrincipalID    uuid.UUID
	TriggerType    string
	ProjectID      *uuid.UUID
	TaskID         *uuid.UUID
	FlowNodeID     *uuid.UUID
	SessionID      *uuid.UUID
	TurnID         *uuid.UUID
	IdempotencyKey *string
	Metadata       json.RawMessage
}

type CancelRequestActor struct {
	Type string
	ID   uuid.UUID
}

type RunCreationPolicyDecision struct {
	Allowed bool
	Reason  string
}

type RunCreationPolicyService interface {
	EvaluateRunCreation(ctx context.Context, organizationID uuid.UUID, principal Principal) (RunCreationPolicyDecision, error)
}

type eventPublisher interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
}

type taskEventRecorder interface {
	Record(ctx context.Context, event repo.ProjectTaskEvent) (repo.ProjectTaskEvent, error)
}

type inboxCreator interface {
	Create(ctx context.Context, item repo.InboxItem) (repo.InboxItem, error)
}

type assignmentRepository interface {
	GetPM(ctx context.Context, projectID uuid.UUID) (repo.AgentProjectAssignment, error)
}

type agentReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

type userReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.HumanUser, error)
}

type runRepository interface {
	Create(ctx context.Context, run Run) (Run, error)
	Get(ctx context.Context, id uuid.UUID) (Run, error)
	GetByIdempotencyKey(ctx context.Context, organizationID uuid.UUID, key string) (Run, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, expectedVersion int, status string, failureReason, failureClass *string) (Run, error)
}

type runStepRepository interface {
	Create(ctx context.Context, step RunStep) (RunStep, error)
	Get(ctx context.Context, id uuid.UUID) (RunStep, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) (RunStep, error)
	ListByRun(ctx context.Context, runID uuid.UUID) ([]RunStep, error)
}

type runAttemptRepository interface {
	Create(ctx context.Context, attempt RunAttempt) (RunAttempt, error)
	GetLatestByStep(ctx context.Context, runStepID uuid.UUID) (RunAttempt, error)
}

type runEventRepository interface {
	Append(ctx context.Context, event RunEvent) (RunEvent, error)
	GetLatestHeartbeat(ctx context.Context, runID uuid.UUID) (RunEvent, error)
}

type budgetChecker interface {
	CheckBudget(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, agentID *uuid.UUID, estimatedTokens int64) (*budget.BudgetCheckResult, error)
}

type runSessionRouter interface {
	RouteRunToSession(ctx context.Context, run projectsvc.FlowRun) error
}

type RunService interface {
	CreateRun(ctx context.Context, input CreateRunInput) (Run, error)
	StartRun(ctx context.Context, runID uuid.UUID) error
	CompleteRun(ctx context.Context, runID uuid.UUID, output json.RawMessage) error
	FailRun(ctx context.Context, runID uuid.UUID, reason, failureClass string) error
	RequestCancel(ctx context.Context, runID uuid.UUID, requestedBy CancelRequestActor) error
	ConfirmCancelled(ctx context.Context, runID uuid.UUID) error
	PauseRun(ctx context.Context, runID uuid.UUID) error
	ResumeRun(ctx context.Context, runID uuid.UUID) error
	CreateRetryAttempt(ctx context.Context, runStepID uuid.UUID, trigger string) (RunAttempt, error)
	DeadLetter(ctx context.Context, runID uuid.UUID) error
	EmitHeartbeat(ctx context.Context, runID uuid.UUID, runAttemptID *uuid.UUID) error
	CreateStep(ctx context.Context, runID uuid.UUID, toolName, toolTier string) (RunStep, error)
	StartStep(ctx context.Context, stepID uuid.UUID) error
	CompleteStep(ctx context.Context, stepID uuid.UUID) error
	FailStep(ctx context.Context, stepID uuid.UUID, reason string) error
	GetRun(ctx context.Context, runID uuid.UUID) (Run, error)
}

type RunServiceOptions struct {
	Pool *pgxpool.Pool

	Runs     runRepository
	RunSteps runStepRepository
	Attempts runAttemptRepository
	RunEvent runEventRepository

	TaskEvents  taskEventRecorder
	Inbox       inboxCreator
	Assignments assignmentRepository
	Agents      agentReader
	Users       userReader

	EventBus      eventPublisher
	Policy        RunCreationPolicyService
	Budget        budgetChecker
	SessionBridge runSessionRouter
	Clock         clock.Clock
	Logger        *slog.Logger
}

type runService struct {
	runs     runRepository
	runSteps runStepRepository
	attempts runAttemptRepository
	events   runEventRepository

	taskEvents  taskEventRecorder
	inbox       inboxCreator
	assignments assignmentRepository
	agents      agentReader
	users       userReader

	eventBus      eventPublisher
	policy        RunCreationPolicyService
	budget        budgetChecker
	sessionBridge runSessionRouter
	clock         clock.Clock
	logger        *slog.Logger
}

func NewRunService(opts RunServiceOptions) (RunService, error) {
	requiresPool := opts.Runs == nil || opts.RunSteps == nil || opts.Attempts == nil || opts.RunEvent == nil ||
		opts.TaskEvents == nil || opts.Inbox == nil || opts.Assignments == nil || opts.Agents == nil || opts.Users == nil
	if opts.Pool == nil && requiresPool {
		return nil, fmt.Errorf("run service requires a database pool")
	}
	if opts.EventBus == nil {
		return nil, fmt.Errorf("run service requires an event bus")
	}

	svc := &runService{
		eventBus:      opts.EventBus,
		policy:        opts.Policy,
		budget:        opts.Budget,
		sessionBridge: opts.SessionBridge,
		clock:         opts.Clock,
		logger:        opts.Logger,
	}
	if svc.clock == nil {
		svc.clock = clock.Real{}
	}
	if svc.logger == nil {
		svc.logger = slog.Default()
	}
	if opts.Runs != nil {
		svc.runs = opts.Runs
	} else {
		svc.runs = NewRunRepository(opts.Pool)
	}
	if opts.RunSteps != nil {
		svc.runSteps = opts.RunSteps
	} else {
		svc.runSteps = NewRunStepRepository(opts.Pool)
	}
	if opts.Attempts != nil {
		svc.attempts = opts.Attempts
	} else {
		svc.attempts = NewRunAttemptRepository(opts.Pool)
	}
	if opts.RunEvent != nil {
		svc.events = opts.RunEvent
	} else {
		svc.events = NewRunEventRepository(opts.Pool)
	}
	if opts.TaskEvents != nil {
		svc.taskEvents = opts.TaskEvents
	} else {
		svc.taskEvents = repo.NewProjectTaskEventRepo(opts.Pool)
	}
	if opts.Inbox != nil {
		svc.inbox = opts.Inbox
	} else {
		svc.inbox = repo.NewInboxItemRepo(opts.Pool)
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
	return svc, nil
}

func (s *runService) GetRun(ctx context.Context, runID uuid.UUID) (Run, error) {
	if runID == uuid.Nil {
		return Run{}, ErrRunIDRequired
	}
	return s.runs.Get(ctx, runID)
}

func (s *runService) CreateRun(ctx context.Context, input CreateRunInput) (Run, error) {
	if input.OrganizationID == uuid.Nil {
		return Run{}, fmt.Errorf("organization_id is required")
	}

	idempotencyKey := normalizeStringPointer(input.IdempotencyKey)
	if idempotencyKey != nil {
		existing, err := s.runs.GetByIdempotencyKey(ctx, input.OrganizationID, *idempotencyKey)
		if err == nil {
			if _, terminal := runTerminalStatuses[existing.Status]; !terminal || existing.Status == "completed" || existing.Status == "cancelled" || existing.Status == "timed_out" {
				if existing.Status != "failed" && existing.Status != "dead_letter" {
					return existing, nil
				}
			}
		} else if !errors.Is(err, ErrNotFound) {
			return Run{}, err
		}
	}

	principalType := normalizePrincipalType(input.PrincipalType)
	if principalType == "" {
		return Run{}, fmt.Errorf("principal_type is required")
	}

	if input.PrincipalID == uuid.Nil && principalType != "system" {
		return Run{}, fmt.Errorf("principal_id is required")
	}
	principalID := input.PrincipalID
	if principalType == "system" && principalID == uuid.Nil {
		principalID = uuid.Nil
	}

	triggerType := normalizeTriggerType(input.TriggerType)
	if triggerType == "" {
		return Run{}, fmt.Errorf("trigger_type is required")
	}

	policyDecision, err := s.evaluateRunCreationPolicy(ctx, input.OrganizationID, Principal{
		Type: principalType,
		ID:   principalID,
	})
	if err != nil {
		return Run{}, err
	}
	if !policyDecision.Allowed {
		reason := strings.TrimSpace(policyDecision.Reason)
		if reason == "" {
			reason = "policy denied"
		}
		created, createErr := s.createRejectedRun(ctx, input, principalType, principalID, triggerType, "policy_denied", reason, "policy_denied")
		if createErr != nil {
			return Run{}, createErr
		}
		return created, fmt.Errorf("%w: %s", ErrPolicyDenied, reason)
	}

	var (
		softBudgetWarning bool
		budgetLevel       string
	)
	if s.budget != nil {
		agentID := actorToAgentID(principalType, principalID)
		result, checkErr := s.budget.CheckBudget(ctx, input.OrganizationID, input.ProjectID, agentID, 0)
		if checkErr != nil {
			return Run{}, checkErr
		}
		if result != nil {
			if !result.Allowed && result.HardLimitHit {
				reason := "budget hard limit exceeded"
				created, createErr := s.createRejectedRun(ctx, input, principalType, principalID, triggerType, "budget_exceeded", reason, "budget_exceeded")
				if createErr != nil {
					return Run{}, createErr
				}
				return created, fmt.Errorf("%w: %s", ErrBudgetExceeded, reason)
			}
			if result.SoftLimitHit {
				softBudgetWarning = true
				budgetLevel = "unknown"
			}
		}
	}

	created, err := s.runs.Create(ctx, Run{
		OrganizationID: input.OrganizationID,
		ProjectID:      input.ProjectID,
		TaskID:         input.TaskID,
		FlowNodeID:     input.FlowNodeID,
		SessionID:      input.SessionID,
		TurnID:         input.TurnID,
		PrincipalType:  principalType,
		PrincipalID:    principalID,
		Status:         "created",
		IdempotencyKey: idempotencyKey,
		TriggerType:    triggerType,
		Metadata:       normalizeJSON(input.Metadata, json.RawMessage(`{}`)),
	})
	if err != nil {
		if idempotencyKey != nil && errors.Is(err, ErrConflict) {
			existing, getErr := s.runs.GetByIdempotencyKey(ctx, input.OrganizationID, *idempotencyKey)
			if getErr == nil {
				return existing, nil
			}
		}
		return Run{}, err
	}

	if err := s.publishDomainEvent(ctx, created.OrganizationID, "run.created", "system", nil, map[string]any{
		"run_id":          created.ID.String(),
		"organization_id": created.OrganizationID.String(),
		"task_id":         uuidToString(created.TaskID),
	}); err != nil {
		return Run{}, err
	}
	if s.sessionBridge != nil && created.FlowNodeID != nil {
		if err := s.sessionBridge.RouteRunToSession(ctx, projectsvc.FlowRun{
			ID:         created.ID,
			TaskID:     created.TaskID,
			FlowNodeID: created.FlowNodeID,
			Summary:    created.TriggerType,
		}); err != nil {
			return Run{}, err
		}
	}

	if softBudgetWarning {
		payload := map[string]any{
			"soft_limit_hit": true,
			"level":          budgetLevel,
			"run_id":         created.ID.String(),
		}
		if _, appendErr := s.appendRunEvent(ctx, created.ID, nil, nil, "budget_exceeded", "system", nil, payload); appendErr != nil {
			return Run{}, appendErr
		}
	}

	return created, nil
}

func (s *runService) StartRun(ctx context.Context, runID uuid.UUID) error {
	updated, err := s.transitionRun(ctx, runID, map[string]struct{}{"created": {}}, "in_progress", nil, nil)
	if err != nil {
		return err
	}
	if _, err := s.appendRunEvent(ctx, updated.ID, nil, nil, "run_started", "system", nil, nil); err != nil {
		return err
	}
	return s.publishDomainEvent(ctx, updated.OrganizationID, "run.started", "system", nil, map[string]any{
		"run_id": createdID(updated.ID),
	})
}

func (s *runService) CompleteRun(ctx context.Context, runID uuid.UUID, output json.RawMessage) error {
	updated, err := s.transitionRun(ctx, runID, map[string]struct{}{"in_progress": {}}, "completed", nil, nil)
	if err != nil {
		return err
	}
	var payload map[string]any
	if len(output) > 0 {
		payload = map[string]any{
			"output": json.RawMessage(output),
		}
	}
	if _, err := s.appendRunEvent(ctx, updated.ID, nil, nil, "run_completed", "system", nil, payload); err != nil {
		return err
	}
	return s.publishDomainEvent(ctx, updated.OrganizationID, "run.completed", "system", nil, map[string]any{
		"run_id": createdID(updated.ID),
	})
}

func (s *runService) FailRun(ctx context.Context, runID uuid.UUID, reason, failureClass string) error {
	failureClass = normalizeFailureClass(failureClass)
	if failureClass == "" {
		failureClass = "transient"
	}
	targetStatus := "failed"
	eventType := "run_failed"
	if failureClass == "timeout" {
		targetStatus = "timed_out"
		eventType = "run_timed_out"
	}

	reason = strings.TrimSpace(reason)
	var reasonPtr *string
	if reason != "" {
		reasonPtr = &reason
	}
	classCopy := failureClass
	updated, err := s.transitionRun(ctx, runID, map[string]struct{}{"in_progress": {}, "paused": {}}, targetStatus, reasonPtr, &classCopy)
	if err != nil {
		return err
	}

	payload := map[string]any{
		"failure_class": failureClass,
	}
	if reason != "" {
		payload["reason"] = reason
	}
	if _, err := s.appendRunEvent(ctx, updated.ID, nil, nil, eventType, "system", nil, payload); err != nil {
		return err
	}
	return s.publishDomainEvent(ctx, updated.OrganizationID, "run.failed", "system", nil, map[string]any{
		"run_id":        createdID(updated.ID),
		"failure_class": failureClass,
		"reason":        reason,
	})
}

func (s *runService) RequestCancel(ctx context.Context, runID uuid.UUID, requestedBy CancelRequestActor) error {
	if runID == uuid.Nil {
		return ErrRunIDRequired
	}

	requestActorType, requestActorID, err := normalizeRunEventActor(requestedBy.Type, requestedBy.ID)
	if err != nil {
		return err
	}

	current, err := s.runs.Get(ctx, runID)
	if err != nil {
		return err
	}

	payload := map[string]any{
		"requested_by": map[string]any{
			"actor_type": requestActorType,
			"actor_id":   uuidPointerToString(requestActorID),
		},
	}

	if _, terminal := runTerminalStatuses[current.Status]; terminal {
		return ErrTerminalState
	}

	if current.Status == "created" {
		updated, transitionErr := s.transitionRun(ctx, runID, map[string]struct{}{"created": {}}, "cancelled", nil, nil)
		if transitionErr != nil {
			return transitionErr
		}
		if _, appendErr := s.appendRunEvent(ctx, updated.ID, nil, nil, "run_cancelled", requestActorType, requestActorID, payload); appendErr != nil {
			return appendErr
		}
		return s.publishDomainEvent(ctx, updated.OrganizationID, "run.cancelled", requestActorType, requestActorID, map[string]any{
			"run_id":       createdID(updated.ID),
			"requested_by": payload["requested_by"],
		})
	}

	updated, transitionErr := s.transitionRun(ctx, runID, map[string]struct{}{"in_progress": {}, "paused": {}}, "cancelling", nil, nil)
	if transitionErr != nil {
		return transitionErr
	}
	if _, appendErr := s.appendRunEvent(ctx, updated.ID, nil, nil, "run_cancelled", requestActorType, requestActorID, payload); appendErr != nil {
		return appendErr
	}
	return s.publishDomainEvent(ctx, updated.OrganizationID, "run.cancellation_requested", requestActorType, requestActorID, map[string]any{
		"run_id":       createdID(updated.ID),
		"requested_by": payload["requested_by"],
	})
}

func (s *runService) ConfirmCancelled(ctx context.Context, runID uuid.UUID) error {
	updated, err := s.transitionRun(ctx, runID, map[string]struct{}{"cancelling": {}}, "cancelled", nil, nil)
	if err != nil {
		return err
	}
	if _, err := s.appendRunEvent(ctx, updated.ID, nil, nil, "run_cancelled", "system", nil, nil); err != nil {
		return err
	}
	return s.publishDomainEvent(ctx, updated.OrganizationID, "run.cancelled", "system", nil, map[string]any{
		"run_id": createdID(updated.ID),
	})
}

func (s *runService) PauseRun(ctx context.Context, runID uuid.UUID) error {
	updated, err := s.transitionRun(ctx, runID, map[string]struct{}{"in_progress": {}}, "paused", nil, nil)
	if err != nil {
		return err
	}
	if _, err := s.appendRunEvent(ctx, updated.ID, nil, nil, "run_paused", "system", nil, nil); err != nil {
		return err
	}
	return s.publishDomainEvent(ctx, updated.OrganizationID, "run.paused", "system", nil, map[string]any{
		"run_id": createdID(updated.ID),
	})
}

func (s *runService) ResumeRun(ctx context.Context, runID uuid.UUID) error {
	updated, err := s.transitionRun(ctx, runID, map[string]struct{}{"paused": {}}, "in_progress", nil, nil)
	if err != nil {
		return err
	}
	if _, err := s.appendRunEvent(ctx, updated.ID, nil, nil, "run_started", "system", nil, map[string]any{"resumed": true}); err != nil {
		return err
	}
	return s.publishDomainEvent(ctx, updated.OrganizationID, "run.resumed", "system", nil, map[string]any{
		"run_id": createdID(updated.ID),
	})
}

func (s *runService) CreateRetryAttempt(ctx context.Context, runStepID uuid.UUID, trigger string) (RunAttempt, error) {
	if runStepID == uuid.Nil {
		return RunAttempt{}, ErrRunStepIDRequired
	}
	trigger = normalizeRetryTrigger(trigger)
	if trigger == "" {
		return RunAttempt{}, ErrInvalidRetryTrigger
	}

	step, err := s.runSteps.Get(ctx, runStepID)
	if err != nil {
		return RunAttempt{}, err
	}
	runRecord, err := s.runs.Get(ctx, step.RunID)
	if err != nil {
		return RunAttempt{}, err
	}

	attemptNumber := 1
	workerType := "internal"
	latest, latestErr := s.attempts.GetLatestByStep(ctx, runStepID)
	if latestErr == nil {
		attemptNumber = latest.AttemptNumber + 1
		if latest.WorkerType != nil && strings.TrimSpace(*latest.WorkerType) != "" {
			workerType = strings.TrimSpace(strings.ToLower(*latest.WorkerType))
		}
	} else if !errors.Is(latestErr, ErrNotFound) {
		return RunAttempt{}, latestErr
	}

	maxAttempts := maxAttemptsForWorker(workerType)
	if attemptNumber > maxAttempts {
		reason := fmt.Sprintf("max retry attempts exceeded for worker_type=%s", workerType)
		if err := s.FailRun(ctx, runRecord.ID, reason, "permanent"); err != nil && !errors.Is(err, ErrInvalidTransition) {
			return RunAttempt{}, err
		}
		if err := s.DeadLetter(ctx, runRecord.ID); err != nil && !errors.Is(err, ErrInvalidTransition) {
			return RunAttempt{}, err
		}
		return RunAttempt{}, ErrMaxAttemptsExceeded
	}

	worker := workerType
	created, err := s.attempts.Create(ctx, RunAttempt{
		RunStepID:     runStepID,
		AttemptNumber: attemptNumber,
		Trigger:       trigger,
		Status:        "pending",
		WorkerType:    &worker,
		Metadata:      json.RawMessage(`{}`),
	})
	if err != nil {
		return RunAttempt{}, err
	}

	if trigger == "supervisor_recovery" {
		if _, err := s.appendRunEvent(ctx, runRecord.ID, &step.ID, &created.ID, "supervisor_recovery", "supervisor", nil, map[string]any{
			"run_step_id":    step.ID.String(),
			"run_attempt_id": created.ID.String(),
		}); err != nil {
			return RunAttempt{}, err
		}
	}

	return created, nil
}

func (s *runService) DeadLetter(ctx context.Context, runID uuid.UUID) error {
	updated, err := s.transitionRun(ctx, runID, map[string]struct{}{"failed": {}}, "dead_letter", nil, nil)
	if err != nil {
		return err
	}
	handler := NewDeadLetterHandler(s)
	return handler.Handle(ctx, updated.ID, stringPointerValue(updated.FailureReason))
}

func (s *runService) EmitHeartbeat(ctx context.Context, runID uuid.UUID, runAttemptID *uuid.UUID) error {
	if runID == uuid.Nil {
		return ErrRunIDRequired
	}
	_, err := s.appendRunEvent(ctx, runID, nil, runAttemptID, "heartbeat", "system", nil, map[string]any{
		"ts": s.clock.Now().UTC().Format(time.RFC3339Nano),
	})
	return err
}

func (s *runService) CreateStep(ctx context.Context, runID uuid.UUID, toolName, toolTier string) (RunStep, error) {
	if runID == uuid.Nil {
		return RunStep{}, ErrRunIDRequired
	}
	if _, err := s.runs.Get(ctx, runID); err != nil {
		return RunStep{}, err
	}

	for attempt := 0; attempt < 3; attempt++ {
		steps, err := s.runSteps.ListByRun(ctx, runID)
		if err != nil {
			return RunStep{}, err
		}
		nextNumber := 1
		for _, existing := range steps {
			if existing.StepNumber >= nextNumber {
				nextNumber = existing.StepNumber + 1
			}
		}

		var namePtr, tierPtr *string
		if trimmed := strings.TrimSpace(toolName); trimmed != "" {
			namePtr = &trimmed
		}
		if trimmed := strings.TrimSpace(toolTier); trimmed != "" {
			tierPtr = &trimmed
		}
		created, createErr := s.runSteps.Create(ctx, RunStep{
			RunID:      runID,
			StepNumber: nextNumber,
			Status:     "pending",
			ToolName:   namePtr,
			ToolTier:   tierPtr,
			Metadata:   json.RawMessage(`{}`),
		})
		if createErr == nil {
			return created, nil
		}
		if !errors.Is(createErr, ErrConflict) {
			return RunStep{}, createErr
		}
		if sleepErr := sleepWithContext(ctx, transitionBackoff(attempt)); sleepErr != nil {
			return RunStep{}, sleepErr
		}
	}
	return RunStep{}, ErrConflict
}

func (s *runService) StartStep(ctx context.Context, stepID uuid.UUID) error {
	updated, err := s.transitionStep(ctx, stepID, map[string]struct{}{"pending": {}}, "in_progress")
	if err != nil {
		return err
	}
	_, err = s.appendRunEvent(ctx, updated.RunID, &updated.ID, nil, "step_started", "system", nil, map[string]any{
		"run_step_id": updated.ID.String(),
		"step_number": updated.StepNumber,
	})
	return err
}

func (s *runService) CompleteStep(ctx context.Context, stepID uuid.UUID) error {
	updated, err := s.transitionStep(ctx, stepID, map[string]struct{}{"in_progress": {}}, "completed")
	if err != nil {
		return err
	}
	_, err = s.appendRunEvent(ctx, updated.RunID, &updated.ID, nil, "step_completed", "system", nil, map[string]any{
		"run_step_id": updated.ID.String(),
		"step_number": updated.StepNumber,
	})
	return err
}

func (s *runService) FailStep(ctx context.Context, stepID uuid.UUID, reason string) error {
	updated, err := s.transitionStep(ctx, stepID, map[string]struct{}{"in_progress": {}}, "failed")
	if err != nil {
		return err
	}
	_, err = s.appendRunEvent(ctx, updated.RunID, &updated.ID, nil, "step_failed", "system", nil, map[string]any{
		"run_step_id": updated.ID.String(),
		"step_number": updated.StepNumber,
		"reason":      strings.TrimSpace(reason),
	})
	return err
}

func (s *runService) createRejectedRun(ctx context.Context, input CreateRunInput, principalType string, principalID uuid.UUID, triggerType, failureClass, reason, eventType string) (Run, error) {
	reason = strings.TrimSpace(reason)
	failureClass = normalizeFailureClass(failureClass)
	now := s.clock.Now().UTC()
	created, err := s.runs.Create(ctx, Run{
		OrganizationID: input.OrganizationID,
		ProjectID:      input.ProjectID,
		TaskID:         input.TaskID,
		FlowNodeID:     input.FlowNodeID,
		SessionID:      input.SessionID,
		TurnID:         input.TurnID,
		PrincipalType:  principalType,
		PrincipalID:    principalID,
		Status:         "failed",
		IdempotencyKey: normalizeStringPointer(input.IdempotencyKey),
		TriggerType:    triggerType,
		FailureReason:  normalizeStringPointer(&reason),
		FailureClass:   normalizeStringPointer(&failureClass),
		Metadata:       normalizeJSON(input.Metadata, json.RawMessage(`{}`)),
		CompletedAt:    &now,
	})
	if err != nil {
		return Run{}, err
	}
	payload := map[string]any{
		"failure_class": failureClass,
		"reason":        reason,
		"run_id":        created.ID.String(),
	}
	if _, appendErr := s.appendRunEvent(ctx, created.ID, nil, nil, eventType, "system", nil, payload); appendErr != nil {
		return Run{}, appendErr
	}
	if publishErr := s.publishDomainEvent(ctx, created.OrganizationID, "run.failed", "system", nil, payload); publishErr != nil {
		return Run{}, publishErr
	}
	return created, nil
}

func (s *runService) transitionRun(ctx context.Context, runID uuid.UUID, allowed map[string]struct{}, toStatus string, failureReason, failureClass *string) (Run, error) {
	if runID == uuid.Nil {
		return Run{}, ErrRunIDRequired
	}

	for attempt := 0; attempt < 3; attempt++ {
		current, err := s.runs.Get(ctx, runID)
		if err != nil {
			return Run{}, err
		}
		if _, ok := allowed[current.Status]; !ok {
			return Run{}, fmt.Errorf("%w: run %s -> %s", ErrInvalidTransition, current.Status, toStatus)
		}

		updated, updateErr := s.runs.UpdateStatus(ctx, runID, current.Version, toStatus, failureReason, failureClass)
		if updateErr == nil {
			return updated, nil
		}
		if !errors.Is(updateErr, ErrConflict) {
			return Run{}, updateErr
		}
		if sleepErr := sleepWithContext(ctx, transitionBackoff(attempt)); sleepErr != nil {
			return Run{}, sleepErr
		}
	}
	return Run{}, ErrConflict
}

func (s *runService) transitionStep(ctx context.Context, stepID uuid.UUID, allowed map[string]struct{}, toStatus string) (RunStep, error) {
	current, err := s.runSteps.Get(ctx, stepID)
	if err != nil {
		return RunStep{}, err
	}
	if _, ok := allowed[current.Status]; !ok {
		return RunStep{}, fmt.Errorf("%w: run_step %s -> %s", ErrInvalidTransition, current.Status, toStatus)
	}
	return s.runSteps.UpdateStatus(ctx, stepID, toStatus)
}

func (s *runService) appendRunEvent(ctx context.Context, runID uuid.UUID, runStepID, runAttemptID *uuid.UUID, eventType, actorType string, actorID *uuid.UUID, payload map[string]any) (RunEvent, error) {
	normalizedActorType, normalizedActorID, err := normalizeRunEventActor(actorType, uuidPointerValue(actorID))
	if err != nil {
		return RunEvent{}, err
	}
	encodedPayload, err := marshalPayload(payload)
	if err != nil {
		return RunEvent{}, err
	}
	return s.events.Append(ctx, RunEvent{
		RunID:        runID,
		RunStepID:    runStepID,
		RunAttemptID: runAttemptID,
		EventType:    eventType,
		ActorType:    normalizedActorType,
		ActorID:      normalizedActorID,
		Payload:      encodedPayload,
	})
}

func (s *runService) publishDomainEvent(ctx context.Context, organizationID uuid.UUID, eventType, actorType string, actorID *uuid.UUID, payload map[string]any) error {
	encodedPayload, err := marshalPayload(payload)
	if err != nil {
		return err
	}
	normalizedActorType, normalizedActorID, err := normalizeDomainActor(actorType, actorID)
	if err != nil {
		return err
	}
	return s.eventBus.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: organizationID,
		EventType:      strings.TrimSpace(eventType),
		ActorType:      normalizedActorType,
		ActorID:        normalizedActorID,
		Payload:        encodedPayload,
	})
}

func (s *runService) evaluateRunCreationPolicy(ctx context.Context, organizationID uuid.UUID, principal Principal) (RunCreationPolicyDecision, error) {
	if s.policy == nil {
		return RunCreationPolicyDecision{
			Allowed: true,
			Reason:  "policy service not configured",
		}, nil
	}
	return s.policy.EvaluateRunCreation(ctx, organizationID, principal)
}

func (s *runService) recordRunDeadLetteredTaskEvent(ctx context.Context, runRecord Run, actorType string) error {
	if s.taskEvents == nil || runRecord.TaskID == nil || runRecord.ProjectID == nil {
		return nil
	}
	payload, err := marshalPayload(map[string]any{
		"run_id":        runRecord.ID.String(),
		"failure_class": stringPointerValue(runRecord.FailureClass),
		"reason":        stringPointerValue(runRecord.FailureReason),
	})
	if err != nil {
		return err
	}
	_, err = s.taskEvents.Record(ctx, repo.ProjectTaskEvent{
		TaskID:    *runRecord.TaskID,
		ProjectID: *runRecord.ProjectID,
		EventType: "run_dead_lettered",
		ActorType: actorType,
		Payload:   payload,
	})
	return err
}

func (s *runService) createEscalationInboxItem(ctx context.Context, runRecord Run, actorType string, publishSupervisorEscalation bool) error {
	if s.inbox == nil || runRecord.ProjectID == nil {
		return nil
	}

	targetUserID, err := s.resolvePMTargetUser(ctx, *runRecord.ProjectID)
	if err != nil {
		return err
	}

	payloadMap := map[string]any{
		"run_id":            runRecord.ID.String(),
		"failure_class":     stringPointerValue(runRecord.FailureClass),
		"reason":            stringPointerValue(runRecord.FailureReason),
		"urgency":           "urgent",
		"requested_type":    "escalation",
		"source_event_type": "run_dead_lettered",
	}
	payload, err := marshalPayload(payloadMap)
	if err != nil {
		return err
	}

	title := "Run dead-lettered"
	body := "A run reached dead-letter state and requires PM escalation."
	item := repo.InboxItem{
		OrganizationID:  runRecord.OrganizationID,
		TargetUserID:    targetUserID,
		ItemType:        "escalation",
		SourceProjectID: runRecord.ProjectID,
		SourceTaskID:    runRecord.TaskID,
		CreatedByType:   actorType,
		CreatedByID:     nil,
		Title:           title,
		Body:            &body,
		ActionPayload:   payload,
	}
	if _, err := s.inbox.Create(ctx, item); err != nil {
		if errors.Is(err, repo.ErrConflict) {
			item.ItemType = "system_alert"
			if _, fallbackErr := s.inbox.Create(ctx, item); fallbackErr != nil {
				return fallbackErr
			}
		} else {
			return err
		}
	}

	if publishSupervisorEscalation {
		return s.publishDomainEvent(ctx, runRecord.OrganizationID, "supervisor.escalation_created", "supervisor", nil, map[string]any{
			"run_id": runRecord.ID.String(),
		})
	}
	return nil
}

func (s *runService) createBlockerInboxItem(ctx context.Context, runRecord Run, actorType, title, body string, urgent bool) error {
	if s.inbox == nil || runRecord.ProjectID == nil {
		return nil
	}

	targetUserID, err := s.resolvePMTargetUser(ctx, *runRecord.ProjectID)
	if err != nil {
		return err
	}

	urgency := "normal"
	if urgent {
		urgency = "urgent"
	}
	payloadMap := map[string]any{
		"run_id":            runRecord.ID.String(),
		"task_id":           uuidToString(runRecord.TaskID),
		"flow_node_id":      uuidToString(runRecord.FlowNodeID),
		"urgency":           urgency,
		"requested_type":    "blocker",
		"source_event_type": "supervisor_recovery",
	}
	payload, err := marshalPayload(payloadMap)
	if err != nil {
		return err
	}

	item := repo.InboxItem{
		OrganizationID:  runRecord.OrganizationID,
		TargetUserID:    targetUserID,
		ItemType:        "blocker",
		SourceProjectID: runRecord.ProjectID,
		SourceTaskID:    runRecord.TaskID,
		CreatedByType:   actorType,
		CreatedByID:     nil,
		Title:           strings.TrimSpace(title),
		Body:            normalizeStringPointer(&body),
		ActionPayload:   payload,
	}
	if _, err := s.inbox.Create(ctx, item); err != nil {
		if errors.Is(err, repo.ErrConflict) {
			item.ItemType = "blocker_filed"
			if _, fallbackErr := s.inbox.Create(ctx, item); fallbackErr != nil {
				return fallbackErr
			}
		} else {
			return err
		}
	}
	return nil
}

func (s *runService) resolvePMTargetUser(ctx context.Context, projectID uuid.UUID) (*uuid.UUID, error) {
	if s.assignments == nil || s.agents == nil || s.users == nil {
		return nil, nil
	}

	pmAssignment, err := s.assignments.GetPM(ctx, projectID)
	if err != nil {
		return nil, nil
	}
	pmAgent, err := s.agents.GetByID(ctx, pmAssignment.AgentID)
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
		if user.IsActive && (role == "owner" || role == "admin") {
			id := user.ID
			return &id, nil
		}
	}
	return nil, nil
}

func normalizePrincipalType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "human_user":
		return "human_user"
	case "agent":
		return "agent"
	case "system":
		return "system"
	default:
		return ""
	}
}

func normalizeTriggerType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "chat_turn":
		return "chat_turn"
	case "scheduler":
		return "scheduler"
	case "api":
		return "api"
	case "supervisor":
		return "supervisor"
	case "agent_tool":
		return "agent_tool"
	default:
		return ""
	}
}

func normalizeFailureClass(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "transient":
		return "transient"
	case "permanent":
		return "permanent"
	case "policy_denied":
		return "policy_denied"
	case "budget_exceeded":
		return "budget_exceeded"
	case "timeout":
		return "timeout"
	default:
		return ""
	}
}

func normalizeRetryTrigger(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "retry_transient":
		return "retry_transient"
	case "retry_policy":
		return "retry_policy"
	case "supervisor_recovery":
		return "supervisor_recovery"
	default:
		return ""
	}
}

func maxAttemptsForWorker(workerType string) int {
	return MaxAttemptsForDomain(workerType)
}

func actorToAgentID(principalType string, principalID uuid.UUID) *uuid.UUID {
	if principalType != "agent" || principalID == uuid.Nil {
		return nil
	}
	id := principalID
	return &id
}

func normalizeRunEventActor(actorType string, actorID uuid.UUID) (string, *uuid.UUID, error) {
	switch strings.ToLower(strings.TrimSpace(actorType)) {
	case "human_user":
		if actorID == uuid.Nil {
			return "", nil, ErrInvalidRequestedByType
		}
		id := actorID
		return "human_user", &id, nil
	case "agent":
		if actorID == uuid.Nil {
			return "", nil, ErrInvalidRequestedByType
		}
		id := actorID
		return "agent", &id, nil
	case "system":
		return "system", nil, nil
	case "supervisor":
		return "supervisor", nil, nil
	default:
		return "", nil, ErrInvalidRequestedByType
	}
}

func normalizeDomainActor(actorType string, actorID *uuid.UUID) (string, *uuid.UUID, error) {
	switch strings.ToLower(strings.TrimSpace(actorType)) {
	case "human_user":
		if actorID == nil || *actorID == uuid.Nil {
			return "", nil, ErrInvalidRequestedByType
		}
		id := *actorID
		return "human", &id, nil
	case "agent":
		if actorID == nil || *actorID == uuid.Nil {
			return "", nil, ErrInvalidRequestedByType
		}
		id := *actorID
		return "agent", &id, nil
	case "supervisor":
		return "supervisor", nil, nil
	case "system":
		return "system", nil, nil
	default:
		return "", nil, ErrInvalidRequestedByType
	}
}

func marshalPayload(payload map[string]any) (json.RawMessage, error) {
	if payload == nil {
		return json.RawMessage(`{}`), nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func normalizeStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func transitionBackoff(attempt int) time.Duration {
	switch attempt {
	case 0:
		return 5 * time.Millisecond
	case 1:
		return 10 * time.Millisecond
	default:
		return 20 * time.Millisecond
	}
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func uuidPointerValue(value *uuid.UUID) uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	return *value
}

func uuidToString(value *uuid.UUID) string {
	if value == nil || *value == uuid.Nil {
		return ""
	}
	return value.String()
}

func uuidPointerToString(value *uuid.UUID) string {
	if value == nil || *value == uuid.Nil {
		return ""
	}
	return value.String()
}

func stringPointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func createdID(id uuid.UUID) string {
	return id.String()
}
