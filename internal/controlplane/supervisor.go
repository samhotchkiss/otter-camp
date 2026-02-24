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
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
)

const (
	defaultSupervisorPollInterval = 60 * time.Second
	syncHeartbeatSilenceThreshold = 90 * time.Second
	asyncHeartbeatSilenceLimit    = 5 * time.Minute
	orphanedEventSilenceLimit     = 10 * time.Minute
	pausedTimeoutLimit            = 24 * time.Hour
	urgentBlockerEscalationAfter  = 4 * time.Hour
	normalBlockerEscalationAfter  = 24 * time.Hour
	supervisorRecoveryMaxAttempts = 3
)

type supervisorRunRepository interface {
	ListInProgressUpdatedBefore(ctx context.Context, before time.Time) ([]Run, error)
	ListPausedUpdatedBefore(ctx context.Context, before time.Time) ([]Run, error)
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

type SupervisorOptions struct {
	Pool *pgxpool.Pool

	RunService RunService
	Runs       supervisorRunRepository
	RunEvents  supervisorRunEventRepository

	EventBus eventbus.EventBus
	Clock    clock.Clock
	Logger   *slog.Logger

	PollInterval time.Duration
}

type Supervisor struct {
	pool *pgxpool.Pool

	runService RunService
	runs       supervisorRunRepository
	events     supervisorRunEventRepository
	notifier   deadLetterNotifier
	eventBus   eventbus.EventBus
	clock      clock.Clock
	logger     *slog.Logger

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
	if err := s.detectOrphanedRuns(ctx); err != nil {
		return err
	}
	if err := s.detectStalePaused(ctx); err != nil {
		return err
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

		if recoverErr := s.recoverRun(ctx, runRecord, "heartbeat silence exceeded"); recoverErr != nil {
			return recoverErr
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

func (s *Supervisor) recoverRun(ctx context.Context, runRecord Run, reason string) error {
	if runRecord.TaskID != nil && runRecord.FlowNodeID != nil {
		deadLetterCount, err := s.runs.CountDeadLetterByTaskFlowNode(ctx, *runRecord.TaskID, *runRecord.FlowNodeID)
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
	encodedMetadata, err := json.Marshal(metadata)
	if err != nil {
		return err
	}

	created, err := s.runService.CreateRun(ctx, CreateRunInput{
		OrganizationID: runRecord.OrganizationID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "supervisor",
		ProjectID:      runRecord.ProjectID,
		TaskID:         runRecord.TaskID,
		FlowNodeID:     runRecord.FlowNodeID,
		SessionID:      runRecord.SessionID,
		TurnID:         runRecord.TurnID,
		Metadata:       encodedMetadata,
	})
	if err != nil {
		if fileErr := s.fileBlocker(ctx, runRecord, fmt.Sprintf("supervisor recovery failed: %v", err)); fileErr != nil {
			return fileErr
		}
		return s.maybeEscalateStaleBlocker(ctx, runRecord)
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
	if strings.TrimSpace(event.EventType) != "run.failed" {
		return nil
	}

	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil
	}
	deadLettered, _ := payload["dead_lettered"].(bool)
	if !deadLettered {
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
