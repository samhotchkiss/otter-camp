package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/projectfailure"
	"github.com/samhotchkiss/otter-camp/internal/projectpause"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
)

const (
	defaultSupervisorPollInterval = 60 * time.Second
	syncHeartbeatSilenceThreshold = 90 * time.Second
	asyncHeartbeatSilenceLimit    = 5 * time.Minute
	createdRunStaleLimit          = 5 * time.Minute
	orphanedEventSilenceLimit     = 10 * time.Minute
	pausedTimeoutLimit            = 24 * time.Hour
	strandedExecutionGraceLimit   = 30 * time.Second
	impossibleLiveTaskGraceLimit  = 15 * time.Minute
	urgentBlockerEscalationAfter  = 4 * time.Hour
	normalBlockerEscalationAfter  = 24 * time.Hour
	supervisorRecoveryMaxAttempts = 3
)

type supervisorRunRepository interface {
	ListInProgressUpdatedBefore(ctx context.Context, before time.Time) ([]Run, error)
	ListPausedUpdatedBefore(ctx context.Context, before time.Time) ([]Run, error)
	ListCreatedByTriggerUpdatedBefore(ctx context.Context, triggerType string, before time.Time) ([]Run, error)
	ListOrphanedInProgress(ctx context.Context, since time.Time) ([]Run, error)
	CountDeadLetterByTaskFlowNode(ctx context.Context, taskID, flowNodeID uuid.UUID) (int, error)
}

type supervisorRunEventRepository interface {
	Append(ctx context.Context, event RunEvent) (RunEvent, error)
	GetLatestHeartbeat(ctx context.Context, runID uuid.UUID) (RunEvent, error)
}

type deadLetterNotifier interface {
	recordRunDeadLetteredTaskEvent(ctx context.Context, runRecord Run, actorType string) error
	createEscalationInboxItem(ctx context.Context, runRecord Run, actorType string, publishSupervisorEscalation bool) error
	createBlockerInboxItem(ctx context.Context, runRecord Run, actorType, title, body string, urgent bool) error
}

type supervisorChatService interface {
	GetSession(ctx context.Context, sessionID uuid.UUID) (*chat.ChatSession, error)
	AddParticipant(ctx context.Context, sessionID uuid.UUID, participantType string, participantID uuid.UUID, role string) (*chat.ChatParticipant, error)
	AppendMessage(ctx context.Context, input chat.AppendMessageInput) (*chat.ChatMessage, error)
}

type supervisorTaskService interface {
	TransitionStatus(ctx context.Context, taskID uuid.UUID, toStatus string, actor tasksvc.Actor) (*tasksvc.ProjectTask, error)
	TransitionStatusWithPayload(ctx context.Context, taskID uuid.UUID, toStatus string, actor tasksvc.Actor, extraPayload map[string]any) (*tasksvc.ProjectTask, error)
}

type supervisorProjectService interface {
	Pause(ctx context.Context, orgID, projectID uuid.UUID, req projectsvc.PauseProjectRequest) (*projectsvc.Project, error)
}

type SupervisorOptions struct {
	Pool *pgxpool.Pool

	RunService  RunService
	Runs        supervisorRunRepository
	RunEvents   supervisorRunEventRepository
	ChatService supervisorChatService
	TaskService supervisorTaskService
	ProjectService supervisorProjectService

	EventBus eventbus.EventBus
	Clock    clock.Clock
	Logger   *slog.Logger

	PollInterval time.Duration
}

type Supervisor struct {
	pool *pgxpool.Pool

	runService  RunService
	runs        supervisorRunRepository
	events      supervisorRunEventRepository
	notifier    deadLetterNotifier
	chatService supervisorChatService
	taskService supervisorTaskService
	projectService supervisorProjectService
	eventBus    eventbus.EventBus
	clock       clock.Clock
	logger      *slog.Logger

	pollInterval time.Duration

	mu           sync.Mutex
	cancel       context.CancelFunc
	done         chan struct{}
	subscription eventbus.Subscription
}

func NewSupervisor(opts SupervisorOptions) (*Supervisor, error) {
	if opts.RunService == nil {
		return nil, fmt.Errorf("supervisor requires a run service")
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultSupervisorPollInterval
	}

	supervisor := &Supervisor{
		pool:         opts.Pool,
		runService:   opts.RunService,
		eventBus:     opts.EventBus,
		clock:        opts.Clock,
		logger:       opts.Logger,
		pollInterval: opts.PollInterval,
	}

	if opts.Runs != nil {
		supervisor.runs = opts.Runs
	}
	if opts.RunEvents != nil {
		supervisor.events = opts.RunEvents
	}
	if opts.ChatService != nil {
		supervisor.chatService = opts.ChatService
	}
	if opts.TaskService != nil {
		supervisor.taskService = opts.TaskService
	}
	if opts.ProjectService != nil {
		supervisor.projectService = opts.ProjectService
	}

	if internalService, ok := opts.RunService.(*runService); ok {
		if runRepo, castOK := internalService.runs.(*RunRepository); castOK {
			if supervisor.runs == nil {
				supervisor.runs = runRepo
			}
			if supervisor.pool == nil {
				supervisor.pool = runRepo.pool
			}
		}
		if eventRepo, castOK := internalService.events.(*RunEventRepository); castOK {
			if supervisor.events == nil {
				supervisor.events = eventRepo
			}
		}
		supervisor.notifier = internalService
	}
	if supervisor.taskService == nil && supervisor.pool != nil && supervisor.eventBus != nil {
		taskService, err := tasksvc.NewService(tasksvc.Options{
			Pool:     supervisor.pool,
			EventBus: supervisor.eventBus,
		})
		if err != nil {
			return nil, err
		}
		supervisor.taskService = taskService
	}
	if supervisor.projectService == nil && supervisor.pool != nil && supervisor.eventBus != nil {
		projectService, err := projectsvc.NewService(projectsvc.Options{
			Pool:   supervisor.pool,
			Events: supervisor.eventBus,
		})
		if err != nil {
			return nil, err
		}
		supervisor.projectService = projectService
	}

	if supervisor.runs == nil {
		return nil, fmt.Errorf("supervisor requires run repository")
	}
	if supervisor.events == nil {
		return nil, fmt.Errorf("supervisor requires run event repository")
	}
	return supervisor, nil
}

func (s *Supervisor) Start(ctx context.Context) {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})

	if s.eventBus != nil {
		s.subscription = s.eventBus.Subscribe("controlplane.supervisor.deadletter", nil, func(handlerCtx context.Context, event eventbus.DomainEvent) error {
			return s.handleDeadLetterEvent(handlerCtx, event)
		})
	}
	done := s.done
	s.mu.Unlock()

	go func() {
		defer close(done)
		defer func() {
			if s.eventBus != nil {
				s.eventBus.Unsubscribe(s.subscription)
			}
		}()

		ticker := time.NewTicker(s.pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				if err := s.tick(runCtx); err != nil && runCtx.Err() == nil {
					s.logger.Error("supervisor tick failed", "error", err)
				}
			}
		}
	}()
}

func (s *Supervisor) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.done = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (s *Supervisor) tick(ctx context.Context) error {
	if err := s.detectStuckRuns(ctx); err != nil {
		return err
	}
	if err := s.detectStaleCreatedRuns(ctx); err != nil {
		return err
	}
	if err := s.detectOrphanedRuns(ctx); err != nil {
		return err
	}
	if err := s.detectStalePaused(ctx); err != nil {
		return err
	}
	if err := s.detectStrandedActiveExecutions(ctx); err != nil {
		return err
	}
	if err := s.detectImpossibleLiveTasks(ctx); err != nil {
		return err
	}
	if err := s.detectExpiredBrowserHandoffs(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Supervisor) detectImpossibleLiveTasks(ctx context.Context) error {
	if s.pool == nil || s.taskService == nil {
		return nil
	}

	cutoff := s.clock.Now().UTC().Add(-impossibleLiveTaskGraceLimit)
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.project_id, t.organization_id
		FROM project_task t
		LEFT JOIN runtime_state rs
		  ON rs.scope_type = 'task'
		 AND rs.scope_id = t.id
		 AND rs.active_run_id IS NOT NULL
		LEFT JOIN flow_node_execution e
		  ON e.task_id = t.id
		 AND e.status = 'active'
		WHERE t.work_status = 'in_progress'
		  AND t.updated_at < $1
		  AND rs.id IS NULL
		  AND e.id IS NULL
		ORDER BY t.updated_at ASC, t.id ASC
	`, cutoff)
	if err != nil {
		return err
	}
	defer rows.Close()

	actor := tasksvc.Actor{
		Type:                  "supervisor",
		AllowFlowRuntimeBypass: true,
	}
	const impossibleLiveTaskReason = "supervisor detected impossible live task state: task remained in_progress without a runtime owner or active flow execution"
	for rows.Next() {
		var taskID, projectID, organizationID uuid.UUID
		if scanErr := rows.Scan(&taskID, &projectID, &organizationID); scanErr != nil {
			return scanErr
		}
		if taskID == uuid.Nil {
			continue
		}
		if _, transitionErr := s.taskService.TransitionStatusWithPayload(ctx, taskID, "blocked", actor, map[string]any{
			"blocker_reason": impossibleLiveTaskReason,
		}); transitionErr != nil {
			var invalid tasksvc.ErrInvalidStatusTransition
			if errors.As(transitionErr, &invalid) {
				continue
			}
			return transitionErr
		}
		if err := s.pauseProjectForImpossibleLiveTask(ctx, organizationID, projectID, impossibleLiveTaskReason); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Supervisor) pauseProjectForImpossibleLiveTask(ctx context.Context, organizationID, projectID uuid.UUID, reason string) error {
	if s.pool == nil || s.projectService == nil || organizationID == uuid.Nil || projectID == uuid.Nil {
		return nil
	}

	projectRepo := repo.NewProjectRepo(s.pool)
	projectRecord, err := projectRepo.GetByID(ctx, projectID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(projectRecord.Status), "archived") {
		return nil
	}
	if pauseState := projectpause.Parse(projectRecord.Settings); pauseState.IsPaused {
		return nil
	}

	now := s.clock.Now().UTC()
	settings, err := projectfailure.Apply(projectRecord.Settings, projectfailure.State{
		Action:          "pause",
		Source:          "execution_runtime",
		FailureCategory: "execution_runtime",
		FailureClass:    "impossible_live_task_state",
		FailureReason:   strings.TrimSpace(reason),
		RecordedAt:      &now,
	})
	if err != nil {
		return err
	}
	projectRecord.Settings = settings
	if _, err := projectRepo.Update(ctx, projectRecord); err != nil {
		return err
	}

	_, err = s.projectService.Pause(ctx, organizationID, projectID, projectsvc.PauseProjectRequest{
		Reason:       strings.TrimSpace(reason),
		Metadata:     projectfailure.State{Action: "pause", Source: "execution_runtime", FailureCategory: "execution_runtime", FailureClass: "impossible_live_task_state", FailureReason: strings.TrimSpace(reason), RecordedAt: &now}.JSON(),
		PausedByType: "system",
	})
	return err
}

func (s *Supervisor) detectStaleCreatedRuns(ctx context.Context) error {
	now := s.clock.Now().UTC()
	staleRuns, err := s.runs.ListCreatedByTriggerUpdatedBefore(ctx, "supervisor", now.Add(-createdRunStaleLimit))
	if err != nil {
		return err
	}

	for _, runRecord := range staleRuns {
		cancelErr := s.runService.RequestCancel(ctx, runRecord.ID, CancelRequestActor{Type: "supervisor"})
		if cancelErr != nil && !errors.Is(cancelErr, ErrInvalidTransition) && !errors.Is(cancelErr, ErrTerminalState) {
			return cancelErr
		}
		if cancelErr != nil {
			continue
		}

		if _, appendErr := s.events.Append(ctx, RunEvent{
			RunID:     runRecord.ID,
			EventType: "supervisor_recovery",
			ActorType: "supervisor",
			Payload:   []byte(`{"reason":"created_timeout_exceeded"}`),
		}); appendErr != nil {
			return appendErr
		}
	}
	return nil
}

func (s *Supervisor) detectStuckRuns(ctx context.Context) error {
	now := s.clock.Now().UTC()
	candidates, err := s.runs.ListInProgressUpdatedBefore(ctx, now.Add(-syncHeartbeatSilenceThreshold))
	if err != nil {
		return err
	}

	for _, runRecord := range candidates {
		if runRecord.Status == "paused" {
			continue
		}
		threshold := heartbeatSilenceThreshold(runRecord)
		if now.Sub(runRecord.UpdatedAt) <= threshold {
			continue
		}

		latestHeartbeat, hbErr := s.events.GetLatestHeartbeat(ctx, runRecord.ID)
		if hbErr == nil {
			if now.Sub(latestHeartbeat.CreatedAt) <= threshold {
				continue
			}
		} else if !errors.Is(hbErr, ErrNotFound) {
			return hbErr
		}

		if _, appendErr := s.events.Append(ctx, RunEvent{
			RunID:     runRecord.ID,
			EventType: "supervisor_recovery",
			ActorType: "supervisor",
			Payload:   []byte(`{"reason":"heartbeat_silence"}`),
		}); appendErr != nil {
			return appendErr
		}
		if publishErr := s.publishRecoveryInitiated(ctx, runRecord, "heartbeat_silence"); publishErr != nil {
			return publishErr
		}

		if recoverErr := s.recoverRun(ctx, runRecord, "heartbeat silence exceeded"); recoverErr != nil {
			return recoverErr
		}

		// Fail the original stuck run so it won't be detected again on the next tick.
		// recoverRun creates a fresh recovery run; the stuck run should no longer be in_progress.
		if failErr := s.runService.FailRun(ctx, runRecord.ID, "supervisor recovery: heartbeat silence exceeded", "transient"); failErr != nil && !errors.Is(failErr, ErrInvalidTransition) {
			s.logger.Warn("supervisor: failed to transition stuck run after recovery", "run_id", runRecord.ID, "error", failErr)
		}
	}
	return nil
}

func (s *Supervisor) detectOrphanedRuns(ctx context.Context) error {
	now := s.clock.Now().UTC()
	orphanedRuns, err := s.runs.ListOrphanedInProgress(ctx, now.Add(-orphanedEventSilenceLimit))
	if err != nil {
		return err
	}

	for _, runRecord := range orphanedRuns {
		failErr := s.runService.FailRun(ctx, runRecord.ID, "orphaned: no events for 10 minutes", "transient")
		if failErr != nil && !errors.Is(failErr, ErrInvalidTransition) {
			return failErr
		}
		if _, appendErr := s.events.Append(ctx, RunEvent{
			RunID:     runRecord.ID,
			EventType: "supervisor_recovery",
			ActorType: "supervisor",
			Payload:   []byte(`{"reason":"orphaned_run"}`),
		}); appendErr != nil {
			return appendErr
		}
		if recoverErr := s.recoverRun(ctx, runRecord, "orphaned: no events for 10 minutes"); recoverErr != nil {
			return recoverErr
		}
	}
	return nil
}

func (s *Supervisor) detectStalePaused(ctx context.Context) error {
	now := s.clock.Now().UTC()
	pausedRuns, err := s.runs.ListPausedUpdatedBefore(ctx, now.Add(-pausedTimeoutLimit))
	if err != nil {
		return err
	}

	for _, runRecord := range pausedRuns {
		if _, appendErr := s.events.Append(ctx, RunEvent{
			RunID:     runRecord.ID,
			EventType: "supervisor_recovery",
			ActorType: "supervisor",
			Payload:   []byte(`{"reason":"paused_timeout_exceeded"}`),
		}); appendErr != nil {
			return appendErr
		}
		if failErr := s.runService.FailRun(ctx, runRecord.ID, "paused timeout exceeded", "timeout"); failErr != nil && !errors.Is(failErr, ErrInvalidTransition) {
			return failErr
		}
	}
	return nil
}

func (s *Supervisor) detectExpiredBrowserHandoffs(ctx context.Context) error {
	if s.pool == nil {
		return nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, inbox_item_id, target_user_id
		FROM browser_handoff
		WHERE status = 'pending'
		  AND expires_at < $1
		ORDER BY expires_at ASC, id ASC
	`, s.clock.Now().UTC())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "browser_handoff") {
			return nil
		}
		return err
	}
	defer rows.Close()

	type expiredHandoff struct {
		id         uuid.UUID
		runID      uuid.UUID
		inboxItem  uuid.UUID
		targetUser uuid.UUID
	}
	expired := make([]expiredHandoff, 0)
	for rows.Next() {
		var item expiredHandoff
		if scanErr := rows.Scan(&item.id, &item.runID, &item.inboxItem, &item.targetUser); scanErr != nil {
			return scanErr
		}
		expired = append(expired, item)
	}
	if rows.Err() != nil {
		return rows.Err()
	}

	for _, item := range expired {
		tag, updateErr := s.pool.Exec(ctx, `
			UPDATE browser_handoff
			SET status = 'expired',
			    completed_at = COALESCE(completed_at, now())
			WHERE id = $1
			  AND status = 'pending'
		`, item.id)
		if updateErr != nil {
			return updateErr
		}
		if tag.RowsAffected() == 0 {
			continue
		}

		if _, markErr := s.pool.Exec(ctx, `
			UPDATE inbox_item
			SET is_acted = true,
			    acted_at = COALESCE(acted_at, now()),
			    acted_by_id = COALESCE(acted_by_id, $2)
			WHERE id = $1
			  AND is_acted = false
		`, item.inboxItem, item.targetUser); markErr != nil {
			return markErr
		}

		if failErr := s.runService.FailRun(ctx, item.runID, "browser handoff expired", "timeout"); failErr != nil && !errors.Is(failErr, ErrInvalidTransition) {
			return failErr
		}

		if s.notifier != nil {
			runRecord, getErr := s.runService.GetRun(ctx, item.runID)
			if getErr != nil {
				if errors.Is(getErr, ErrNotFound) {
					continue
				}
				return getErr
			}
			if escalateErr := s.notifier.createEscalationInboxItem(ctx, runRecord, "supervisor", true); escalateErr != nil {
				return escalateErr
			}
		}
	}
	return nil
}

func (s *Supervisor) recoverRun(ctx context.Context, runRecord Run, reason string) error {
	// Skip recovery for runs whose session is closed or archived — there's
	// nothing useful to recover and creating new turns wastes LLM calls.
	if s.chatService != nil && runRecord.SessionID != nil && *runRecord.SessionID != uuid.Nil {
		if session, err := s.chatService.GetSession(ctx, *runRecord.SessionID); err == nil {
			if strings.EqualFold(session.Status, "closed") || strings.EqualFold(session.Status, "archived") {
				s.logger.Info("supervisor: skipping recovery for closed session", "run_id", runRecord.ID, "session_id", *runRecord.SessionID)
				return nil
			}
		}
	}

	// Never create a recovery run for a run that is itself a supervisor
	// recovery. This prevents infinite cascade: supervisor creates recovery
	// run B for stuck run A, then B gets stuck, supervisor creates C, etc.
	// Instead, just fail the stuck recovery run — the original task can be
	// retried via the normal blocker/escalation flow.
	if strings.EqualFold(runRecord.TriggerType, "supervisor") {
		s.logger.Info("supervisor: skipping re-recovery of supervisor run", "run_id", runRecord.ID)
		return nil
	}

	resolvedTaskID := runRecord.TaskID
	resolvedSessionID := runRecord.SessionID
	resolvedFlowNodeID := runRecord.FlowNodeID
	runtimeMetadata := map[string]any{}
	if runtimeReader, ok := s.runService.(interface {
		GetExecutionRuntimeState(context.Context, uuid.UUID, uuid.UUID) (RuntimeState, error)
	}); ok {
		state, stateErr := runtimeReader.GetExecutionRuntimeState(ctx, uuidPointerValue(runRecord.TaskID), uuidPointerValue(runRecord.SessionID))
		if stateErr == nil {
			contract := state.Contract()
			if state.ActiveRunID != nil && *state.ActiveRunID != uuid.Nil && *state.ActiveRunID != runRecord.ID {
				s.logger.Info("supervisor: runtime state already has a different active owner", "run_id", runRecord.ID, "active_run_id", *state.ActiveRunID)
				return nil
			}
			if contract.Status == "retired" || contract.Status == "terminal" {
				s.logger.Info("supervisor: skipping recovery for retired runtime state", "run_id", runRecord.ID, "runtime_status", contract.Status)
				return nil
			}
			if contract.TaskID != nil && *contract.TaskID != uuid.Nil {
				resolvedTaskID = contract.TaskID
			}
			if contract.SessionID != nil && *contract.SessionID != uuid.Nil {
				resolvedSessionID = contract.SessionID
			}
			if contract.FlowNodeID != nil && *contract.FlowNodeID != uuid.Nil {
				resolvedFlowNodeID = contract.FlowNodeID
			}
			if contract.FlowNodeExecutionID != nil && *contract.FlowNodeExecutionID != uuid.Nil {
				runtimeMetadata["flow_node_execution_id"] = contract.FlowNodeExecutionID.String()
			}
			if contract.ProviderSessionID != "" {
				runtimeMetadata["provider_session_id"] = contract.ProviderSessionID
			}
			if contract.Status != "" {
				runtimeMetadata["runtime_status"] = contract.Status
			}
			if contract.PendingWakeReason != "" {
				runtimeMetadata["runtime_pending_wake_reason"] = contract.PendingWakeReason
			}
		} else if !errors.Is(stateErr, ErrNotFound) {
			return stateErr
		}
	}

	if resolvedTaskID != nil && resolvedFlowNodeID != nil {
		deadLetterCount, err := s.runs.CountDeadLetterByTaskFlowNode(ctx, *resolvedTaskID, *resolvedFlowNodeID)
		if err != nil {
			return err
		}
		if deadLetterCount >= supervisorRecoveryMaxAttempts {
			if err := s.fileBlocker(ctx, runRecord, reason); err != nil {
				return err
			}
			return s.maybeEscalateStaleBlocker(ctx, runRecord)
		}
	}

	metadata := map[string]any{
		"run_mode":                   runMode(runRecord),
		"supervisor_recovery_from":   runRecord.ID.String(),
		"supervisor_recovery_reason": strings.TrimSpace(reason),
	}
	for key, value := range runtimeMetadata {
		metadata[key] = value
	}
	encodedMetadata, err := json.Marshal(metadata)
	if err != nil {
		return err
	}

	var created Run
	dispatchRecoveryMessage := true
	if coordinator, ok := s.runService.(interface {
		CreateExecutionWakeup(context.Context, executionWakeupInput) (executionWakeupResult, error)
	}); ok {
		result, wakeErr := coordinator.CreateExecutionWakeup(ctx, executionWakeupInput{
			CreateRunInput: CreateRunInput{
				OrganizationID: runRecord.OrganizationID,
				PrincipalType:  "system",
				PrincipalID:    uuid.Nil,
				TriggerType:    "supervisor",
				ProjectID:      runRecord.ProjectID,
				TaskID:         resolvedTaskID,
				FlowNodeID:     resolvedFlowNodeID,
				SessionID:      resolvedSessionID,
				TurnID:         runRecord.TurnID,
				Metadata:       encodedMetadata,
			},
			WakeupSource: "supervisor",
			WakeupKind:   "supervisor_recovery",
			WakeupPayload: map[string]any{
				"source":                     "supervisor",
				"run_mode":                   runMode(runRecord),
				"supervisor_recovery_from":   runRecord.ID.String(),
				"supervisor_recovery_reason": strings.TrimSpace(reason),
			},
		})
		if wakeErr != nil {
			if fileErr := s.fileBlocker(ctx, runRecord, fmt.Sprintf("supervisor recovery failed: %v", wakeErr)); fileErr != nil {
				return fileErr
			}
			return s.maybeEscalateStaleBlocker(ctx, runRecord)
		}
		created = result.Run
		dispatchRecoveryMessage = result.shouldDispatch()
	} else {
		var createErr error
		created, createErr = s.runService.CreateRun(ctx, CreateRunInput{
			OrganizationID: runRecord.OrganizationID,
			PrincipalType:  "system",
			PrincipalID:    uuid.Nil,
			TriggerType:    "supervisor",
			ProjectID:      runRecord.ProjectID,
			TaskID:         resolvedTaskID,
			FlowNodeID:     resolvedFlowNodeID,
			SessionID:      resolvedSessionID,
			TurnID:         runRecord.TurnID,
			Metadata:       encodedMetadata,
		})
		if createErr != nil {
			if fileErr := s.fileBlocker(ctx, runRecord, fmt.Sprintf("supervisor recovery failed: %v", createErr)); fileErr != nil {
				return fileErr
			}
			return s.maybeEscalateStaleBlocker(ctx, runRecord)
		}

		if startErr := s.runService.StartRun(ctx, created.ID); startErr != nil && !errors.Is(startErr, ErrInvalidTransition) {
			s.logger.Warn("supervisor: failed to start recovery run", "recovery_run_id", created.ID, "error", startErr)
		}
	}

	if dispatchRecoveryMessage && s.chatService != nil && created.SessionID != nil && *created.SessionID != uuid.Nil {
		sessionID := *created.SessionID
		if strings.EqualFold(runRecord.PrincipalType, "agent") && runRecord.PrincipalID != uuid.Nil {
			if _, addErr := s.chatService.AddParticipant(ctx, sessionID, "agent", runRecord.PrincipalID, "responder"); addErr != nil && !errors.Is(addErr, chat.ErrAlreadyParticipant) {
				s.logger.Warn("supervisor: failed to add agent participant for recovery", "session_id", sessionID, "agent_id", runRecord.PrincipalID, "error", addErr)
			}
		}
		msgMeta, _ := json.Marshal(map[string]any{
			"source": "supervisor",
			"run_id": created.ID.String(),
			"reason": strings.TrimSpace(reason),
		})
		if _, msgErr := s.chatService.AppendMessage(ctx, chat.AppendMessageInput{
			SessionID: sessionID,
			Role:      "user",
			Content:   "supervisor recovery: resume task",
			Metadata:  msgMeta,
		}); msgErr != nil {
			s.logger.Warn("supervisor: failed to append recovery kickoff message", "session_id", sessionID, "error", msgErr)
		}
	}

	payload := map[string]any{
		"recovered_run_id": created.ID.String(),
		"reason":           strings.TrimSpace(reason),
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.events.Append(ctx, RunEvent{
		RunID:     runRecord.ID,
		EventType: "supervisor_recovery",
		ActorType: "supervisor",
		Payload:   encodedPayload,
	})
	return err
}

func (s *Supervisor) fileBlocker(ctx context.Context, runRecord Run, reason string) error {
	if s.notifier == nil {
		return nil
	}

	body := fmt.Sprintf("Run recovery halted after max dead-letter attempts: %s", strings.TrimSpace(reason))
	if err := s.notifier.createBlockerInboxItem(ctx, runRecord, "supervisor", "Run recovery blocked", body, true); err != nil {
		return err
	}
	payload := map[string]any{
		"action": "blocker_filed",
		"reason": strings.TrimSpace(reason),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.events.Append(ctx, RunEvent{
		RunID:     runRecord.ID,
		EventType: "supervisor_recovery",
		ActorType: "supervisor",
		Payload:   encoded,
	})
	return err
}

func (s *Supervisor) maybeEscalateStaleBlocker(ctx context.Context, runRecord Run) error {
	if s.notifier == nil || s.pool == nil || runRecord.TaskID == nil {
		return nil
	}

	oldestBlockerCreatedAt, urgency, err := s.loadOldestOpenBlocker(ctx, runRecord.OrganizationID, *runRecord.TaskID)
	if err != nil {
		return err
	}
	if oldestBlockerCreatedAt == nil {
		return nil
	}

	maxAge := normalBlockerEscalationAfter
	if strings.EqualFold(urgency, "urgent") {
		maxAge = urgentBlockerEscalationAfter
	}
	if s.clock.Now().UTC().Sub(*oldestBlockerCreatedAt) <= maxAge {
		return nil
	}

	exists, err := s.hasOpenEscalation(ctx, runRecord.OrganizationID, *runRecord.TaskID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	return s.notifier.createEscalationInboxItem(ctx, runRecord, "supervisor", true)
}

func (s *Supervisor) handleDeadLetterEvent(ctx context.Context, event eventbus.DomainEvent) error {
	if strings.TrimSpace(event.EventType) != "run.dead_lettered" {
		return nil
	}

	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil
	}
	runIDRaw, _ := payload["run_id"].(string)
	runID, err := uuid.Parse(strings.TrimSpace(runIDRaw))
	if err != nil {
		return nil
	}

	runRecord, err := s.runService.GetRun(ctx, runID)
	if err != nil {
		return err
	}

	notificationPayload, err := json.Marshal(map[string]any{
		"action": "dead_letter_notification",
		"run_id": runID.String(),
	})
	if err != nil {
		return err
	}
	if _, err := s.events.Append(ctx, RunEvent{
		RunID:     runID,
		EventType: "supervisor_recovery",
		ActorType: "supervisor",
		Payload:   notificationPayload,
	}); err != nil {
		return err
	}

	if s.notifier != nil {
		if err := s.notifier.recordRunDeadLetteredTaskEvent(ctx, runRecord, "supervisor"); err != nil {
			return err
		}
		if err := s.notifier.createEscalationInboxItem(ctx, runRecord, "supervisor", true); err != nil {
			return err
		}
	}
	return nil
}

func (s *Supervisor) publishRecoveryInitiated(ctx context.Context, runRecord Run, reason string) error {
	if s == nil || s.eventBus == nil {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"run_id": runRecord.ID.String(),
		"reason": strings.TrimSpace(reason),
	})
	if err != nil {
		return err
	}
	return s.eventBus.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: runRecord.OrganizationID,
		EventType:      "run.supervisor.recovery_initiated",
		ActorType:      "supervisor",
		Payload:        payload,
	})
}

func (s *Supervisor) loadOldestOpenBlocker(ctx context.Context, organizationID, taskID uuid.UUID) (*time.Time, string, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT created_at, COALESCE(action_payload->>'urgency', 'normal') AS urgency
		FROM inbox_item
		WHERE organization_id = $1
		  AND source_task_id = $2
		  AND is_acted = false
		  AND (
		  	item_type IN ('blocker', 'blocker_filed')
		    OR (item_type = 'system_alert' AND action_payload->>'requested_type' = 'blocker')
		  )
		ORDER BY created_at ASC, id ASC
		LIMIT 1
	`, organizationID, taskID)

	var createdAt time.Time
	var urgency string
	if err := row.Scan(&createdAt, &urgency); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", nil
		}
		return nil, "", err
	}
	createdAt = createdAt.UTC()
	return &createdAt, strings.TrimSpace(urgency), nil
}

func (s *Supervisor) hasOpenEscalation(ctx context.Context, organizationID, taskID uuid.UUID) (bool, error) {
	var count int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM inbox_item
		WHERE organization_id = $1
		  AND source_task_id = $2
		  AND is_acted = false
		  AND (
		  	item_type = 'escalation'
		    OR (item_type = 'system_alert' AND action_payload->>'requested_type' = 'escalation')
		  )
	`, organizationID, taskID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func heartbeatSilenceThreshold(runRecord Run) time.Duration {
	if strings.EqualFold(runMode(runRecord), "async") {
		return asyncHeartbeatSilenceLimit
	}
	return syncHeartbeatSilenceThreshold
}

func runMode(runRecord Run) string {
	if len(runRecord.Metadata) == 0 {
		return "sync"
	}
	var metadata map[string]any
	if err := json.Unmarshal(runRecord.Metadata, &metadata); err != nil {
		return "sync"
	}
	modeRaw, _ := metadata["run_mode"].(string)
	mode := strings.ToLower(strings.TrimSpace(modeRaw))
	if mode == "async" {
		return "async"
	}
	return "sync"
}
