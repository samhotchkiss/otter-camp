//go:build integration

package controlplane

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestSupervisor_StuckRun_Detection(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord, taskRecord := seedRunProjectTaskWithPM(t, ctx, pool, org.ID)
	flowNodeID := seedSupervisorFlowNode(t, ctx, pool, org.ID, projectRecord.ID)

	now := time.Date(2026, 2, 25, 15, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	svc := newRunServiceIntegrationWithClock(t, pool, fakeClock)

	runRecord, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		ProjectID:      &projectRecord.ID,
		TaskID:         &taskRecord.ID,
		FlowNodeID:     &flowNodeID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := svc.StartRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE run SET updated_at = $2 WHERE id = $1`, runRecord.ID, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("backdate run.updated_at: %v", err)
	}

	supervisor, err := NewSupervisor(SupervisorOptions{
		Pool:       pool,
		RunService: svc,
		EventBus:   eventbus.New(pool, newDiscardLogger(), eventbus.Config{}),
		Clock:      fakeClock,
		Logger:     newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := supervisor.detectStuckRuns(ctx); err != nil {
		t.Fatalf("detectStuckRuns: %v", err)
	}

	if count := countSupervisorEventsForRun(t, ctx, pool, runRecord.ID); count == 0 {
		t.Fatal("expected supervisor-authored run_event for stuck run")
	}

	var recoveryCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run
		WHERE organization_id = $1
		  AND trigger_type = 'supervisor'
		  AND metadata->>'supervisor_recovery_from' = $2
	`, org.ID, runRecord.ID.String()).Scan(&recoveryCount); err != nil {
		t.Fatalf("count supervisor recovery runs: %v", err)
	}
	if recoveryCount == 0 {
		t.Fatal("expected supervisor recovery run")
	}
}

func TestSupervisor_OrphanedRun_Recovery(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord, taskRecord := seedRunProjectTaskWithPM(t, ctx, pool, org.ID)
	flowNodeID := seedSupervisorFlowNode(t, ctx, pool, org.ID, projectRecord.ID)
	svc := newRunServiceIntegration(t, pool)

	runRecord, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		ProjectID:      &projectRecord.ID,
		TaskID:         &taskRecord.ID,
		FlowNodeID:     &flowNodeID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := svc.StartRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE run SET updated_at = now() - interval '11 minutes' WHERE id = $1`, runRecord.ID); err != nil {
		t.Fatalf("backdate run.updated_at: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE run_event SET created_at = now() - interval '11 minutes' WHERE run_id = $1`, runRecord.ID); err != nil {
		t.Fatalf("backdate run_event.created_at: %v", err)
	}

	supervisor, err := NewSupervisor(SupervisorOptions{
		Pool:       pool,
		RunService: svc,
		EventBus:   eventbus.New(pool, newDiscardLogger(), eventbus.Config{}),
		Logger:     newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := supervisor.detectOrphanedRuns(ctx); err != nil {
		t.Fatalf("detectOrphanedRuns: %v", err)
	}

	updatedRun, err := NewRunRepository(pool).Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if updatedRun.Status != "failed" {
		t.Fatalf("run status = %s, want failed", updatedRun.Status)
	}
	if count := countSupervisorEventsForRun(t, ctx, pool, runRecord.ID); count == 0 {
		t.Fatal("expected supervisor-authored run_event for orphan recovery")
	}

	var recoveryCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run
		WHERE organization_id = $1
		  AND trigger_type = 'supervisor'
		  AND metadata->>'supervisor_recovery_from' = $2
	`, org.ID, runRecord.ID.String()).Scan(&recoveryCount); err != nil {
		t.Fatalf("count supervisor recovery runs: %v", err)
	}
	if recoveryCount == 0 {
		t.Fatal("expected supervisor recovery run for orphaned run")
	}
}

func TestSupervisor_StaleCreatedRun_CancelledAndLogged(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)

	now := time.Date(2026, 2, 26, 18, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	svc := newRunServiceIntegrationWithClock(t, pool, fakeClock)
	runRepo := NewRunRepository(pool)

	staleRun, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "supervisor",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun stale run: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE run SET updated_at = $2 WHERE id = $1`, staleRun.ID, now.Add(-6*time.Minute)); err != nil {
		t.Fatalf("backdate stale run updated_at: %v", err)
	}

	recentRun, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "supervisor",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun recent run: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE run SET updated_at = $2 WHERE id = $1`, recentRun.ID, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("set recent run updated_at: %v", err)
	}

	supervisor, err := NewSupervisor(SupervisorOptions{
		Pool:       pool,
		RunService: svc,
		EventBus:   eventbus.New(pool, newDiscardLogger(), eventbus.Config{}),
		Clock:      fakeClock,
		Logger:     newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := supervisor.detectStaleCreatedRuns(ctx); err != nil {
		t.Fatalf("detectStaleCreatedRuns: %v", err)
	}

	updatedStaleRun, err := runRepo.Get(ctx, staleRun.ID)
	if err != nil {
		t.Fatalf("Get stale run: %v", err)
	}
	if updatedStaleRun.Status != "cancelled" {
		t.Fatalf("stale run status = %s, want cancelled", updatedStaleRun.Status)
	}

	var reason string
	if err := pool.QueryRow(ctx, `
		SELECT payload->>'reason'
		FROM run_event
		WHERE run_id = $1
		  AND event_type = 'supervisor_recovery'
		  AND actor_type = 'supervisor'
		ORDER BY sequence DESC
		LIMIT 1
	`, staleRun.ID).Scan(&reason); err != nil {
		t.Fatalf("load supervisor_recovery reason: %v", err)
	}
	if reason != "created_timeout_exceeded" {
		t.Fatalf("supervisor recovery reason = %q, want %q", reason, "created_timeout_exceeded")
	}

	updatedRecentRun, err := runRepo.Get(ctx, recentRun.ID)
	if err != nil {
		t.Fatalf("Get recent run: %v", err)
	}
	if updatedRecentRun.Status != "created" {
		t.Fatalf("recent run status = %s, want created", updatedRecentRun.Status)
	}

	var recentRecoveryEvents int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run_event
		WHERE run_id = $1
		  AND event_type = 'supervisor_recovery'
	`, recentRun.ID).Scan(&recentRecoveryEvents); err != nil {
		t.Fatalf("count recent supervisor_recovery events: %v", err)
	}
	if recentRecoveryEvents != 0 {
		t.Fatalf("recent run supervisor_recovery events = %d, want 0", recentRecoveryEvents)
	}
}

func TestSupervisor_RecoveryUsesRuntimeStateToAvoidDuplicateResume(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord, taskRecord := seedRunProjectTaskWithPM(t, ctx, pool, org.ID)
	flowNodeID := seedSupervisorFlowNode(t, ctx, pool, org.ID, projectRecord.ID)

	now := time.Date(2026, 2, 26, 19, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	runService := newRunServiceIntegrationWithClock(t, pool, fakeClock)
	wakeSvc := runService.(interface {
		CreateExecutionWakeup(context.Context, executionWakeupInput) (executionWakeupResult, error)
	})

	workerID := uuid.New()
	reviewerID := uuid.New()

	started, err := wakeSvc.CreateExecutionWakeup(ctx, executionWakeupInput{
		CreateRunInput: CreateRunInput{
			OrganizationID: org.ID,
			ProjectID:      &projectRecord.ID,
			TaskID:         &taskRecord.ID,
			FlowNodeID:     &flowNodeID,
			PrincipalType:  "agent",
			PrincipalID:    workerID,
			TriggerType:    "scheduler",
			Metadata:       json.RawMessage(`{"run_mode":"async"}`),
		},
		WakeupSource: "task_queue_processor",
		WakeupKind:   "flow_current",
	})
	if err != nil {
		t.Fatalf("CreateExecutionWakeup started: %v", err)
	}

	deferred, err := wakeSvc.CreateExecutionWakeup(ctx, executionWakeupInput{
		CreateRunInput: CreateRunInput{
			OrganizationID: org.ID,
			ProjectID:      &projectRecord.ID,
			TaskID:         &taskRecord.ID,
			FlowNodeID:     &flowNodeID,
			PrincipalType:  "agent",
			PrincipalID:    reviewerID,
			TriggerType:    "scheduler",
			Metadata:       json.RawMessage(`{"run_mode":"async"}`),
		},
		WakeupSource: "task_queue_processor",
		WakeupKind:   "flow_transition",
	})
	if err != nil {
		t.Fatalf("CreateExecutionWakeup deferred: %v", err)
	}
	if deferred.Decision != executionWakeupDeferred {
		t.Fatalf("deferred decision = %q, want %q", deferred.Decision, executionWakeupDeferred)
	}

	if _, err := pool.Exec(ctx, `UPDATE run SET updated_at = $2 WHERE id = $1`, started.Run.ID, now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("backdate started run updated_at: %v", err)
	}

	promoted, err := wakeSvc.CreateExecutionWakeup(ctx, executionWakeupInput{
		CreateRunInput: CreateRunInput{
			OrganizationID: org.ID,
			ProjectID:      &projectRecord.ID,
			TaskID:         &taskRecord.ID,
			FlowNodeID:     &flowNodeID,
			PrincipalType:  "agent",
			PrincipalID:    reviewerID,
			TriggerType:    "scheduler",
			Metadata:       json.RawMessage(`{"run_mode":"async"}`),
		},
		WakeupSource: "task_queue_processor",
		WakeupKind:   "flow_transition",
	})
	if err != nil {
		t.Fatalf("CreateExecutionWakeup promote: %v", err)
	}
	if promoted.Decision != executionWakeupPromoted {
		t.Fatalf("promoted decision = %q, want %q", promoted.Decision, executionWakeupPromoted)
	}

	state, err := NewRuntimeStateRepository(pool).GetByScope(ctx, "task", taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByScope runtime state: %v", err)
	}
	if state.ActiveRunID == nil || *state.ActiveRunID != promoted.Run.ID {
		t.Fatalf("runtime active_run_id = %v, want %s", state.ActiveRunID, promoted.Run.ID)
	}

	supervisor, err := NewSupervisor(SupervisorOptions{
		Pool:       pool,
		RunService: runService,
		EventBus:   eventbus.New(pool, newDiscardLogger(), eventbus.Config{}),
		Clock:      fakeClock,
		Logger:     newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := supervisor.recoverRun(ctx, started.Run, "heartbeat silence exceeded"); err != nil {
		t.Fatalf("recoverRun: %v", err)
	}

	var recoveryCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run
		WHERE organization_id = $1
		  AND trigger_type = 'supervisor'
		  AND metadata->>'supervisor_recovery_from' = $2
	`, org.ID, started.Run.ID.String()).Scan(&recoveryCount); err != nil {
		t.Fatalf("count supervisor recovery runs: %v", err)
	}
	if recoveryCount != 0 {
		t.Fatalf("supervisor recovery run count = %d, want 0", recoveryCount)
	}
}

func TestSupervisor_MaxRecoveryAttempts(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord := seedRunProject(t, ctx, pool, org.ID)
	taskRecord := seedRunTask(t, ctx, pool, org.ID, projectRecord.ID)
	flowNodeID := seedSupervisorFlowNode(t, ctx, pool, org.ID, projectRecord.ID)

	now := time.Date(2026, 2, 25, 16, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	svc := newRunServiceIntegrationWithClock(t, pool, fakeClock)

	runRepo := NewRunRepository(pool)
	for i := 0; i < 3; i++ {
		_, err := runRepo.Create(ctx, Run{
			OrganizationID: org.ID,
			ProjectID:      nil,
			TaskID:         &taskRecord.ID,
			FlowNodeID:     &flowNodeID,
			PrincipalType:  "system",
			PrincipalID:    uuid.Nil,
			Status:         "dead_letter",
			TriggerType:    "api",
			Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
		})
		if err != nil {
			t.Fatalf("seed dead-letter run #%d: %v", i+1, err)
		}
	}

	stuckRun, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		ProjectID:      nil,
		TaskID:         &taskRecord.ID,
		FlowNodeID:     &flowNodeID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun stuck run: %v", err)
	}
	if err := svc.StartRun(ctx, stuckRun.ID); err != nil {
		t.Fatalf("StartRun stuck run: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE run SET updated_at = $2 WHERE id = $1`, stuckRun.ID, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("backdate stuck run.updated_at: %v", err)
	}

	supervisor, err := NewSupervisor(SupervisorOptions{
		Pool:       pool,
		RunService: svc,
		EventBus:   eventbus.New(pool, newDiscardLogger(), eventbus.Config{}),
		Clock:      fakeClock,
		Logger:     newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := supervisor.detectStuckRuns(ctx); err != nil {
		t.Fatalf("detectStuckRuns: %v", err)
	}

	var newRecoveryCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run
		WHERE organization_id = $1
		  AND trigger_type = 'supervisor'
		  AND metadata->>'supervisor_recovery_from' = $2
	`, org.ID, stuckRun.ID.String()).Scan(&newRecoveryCount); err != nil {
		t.Fatalf("count recovery runs: %v", err)
	}
	if newRecoveryCount != 0 {
		t.Fatalf("supervisor recovery runs = %d, want 0 after max recovery attempts", newRecoveryCount)
	}

	if count := countSupervisorEventsForRun(t, ctx, pool, stuckRun.ID); count == 0 {
		t.Fatal("expected supervisor-authored run_event after max recovery attempts")
	}
}

func TestSupervisor_PausedRun_Exempt(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	svc := newRunServiceIntegration(t, pool)

	runRecord, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := svc.StartRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := svc.PauseRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("PauseRun: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE run SET updated_at = now() - interval '2 hours' WHERE id = $1`, runRecord.ID); err != nil {
		t.Fatalf("backdate paused run.updated_at: %v", err)
	}

	supervisor, err := NewSupervisor(SupervisorOptions{
		Pool:       pool,
		RunService: svc,
		EventBus:   eventbus.New(pool, newDiscardLogger(), eventbus.Config{}),
		Logger:     newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := supervisor.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	updatedRun, err := NewRunRepository(pool).Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get paused run: %v", err)
	}
	if updatedRun.Status != "paused" {
		t.Fatalf("paused run status = %s, want paused", updatedRun.Status)
	}
	if count := countSupervisorEventsForRun(t, ctx, pool, runRecord.ID); count != 0 {
		t.Fatalf("supervisor events for paused run = %d, want 0", count)
	}
}

func TestSupervisor_StrandedActiveExecutionRecoveryRestoresLiveTurn(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord := seedRunProject(t, ctx, pool, org.ID)
	template, node := seedSupervisorFlowTemplateNode(t, ctx, pool, org.ID, projectRecord.ID, nil, nil)
	worker := seedSupervisorAgent(t, ctx, pool, org.ID, "Recovery Worker")

	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         projectRecord.ID,
		Title:             "Recover stranded execution",
		WorkStatus:        "in_progress",
		CurrentFlowNodeID: &node.ID,
		FlowTemplateID:    &template.ID,
		AssignedAgentID:   &worker.ID,
		CreatedByType:     "system",
		CreatedByID:       nil,
		Metadata:          json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	bus := eventbus.New(pool, newDiscardLogger(), eventbus.Config{})
	chatService, err := chat.NewService(chat.Options{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("chat.NewService: %v", err)
	}
	session, err := chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Metadata:       json.RawMessage(`{"source":"supervisor_integration"}`),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := chatService.AddParticipant(ctx, session.ID, "agent", worker.ID, "responder"); err != nil {
		t.Fatalf("AddParticipant: %v", err)
	}

	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  node.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create flow execution: %v", err)
	}

	runService, err := NewRunService(RunServiceOptions{
		Pool:     pool,
		EventBus: bus,
		Policy:   allowRunCreationPolicy{},
		Logger:   newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewRunService: %v", err)
	}
	wakeSvc := runService.(interface {
		CreateExecutionWakeup(context.Context, executionWakeupInput) (executionWakeupResult, error)
	})
	started, err := wakeSvc.CreateExecutionWakeup(ctx, executionWakeupInput{
		CreateRunInput: CreateRunInput{
			OrganizationID: org.ID,
			ProjectID:      &projectRecord.ID,
			TaskID:         &taskRecord.ID,
			FlowNodeID:     &node.ID,
			SessionID:      &session.ID,
			PrincipalType:  "agent",
			PrincipalID:    worker.ID,
			TriggerType:    "scheduler",
			Metadata:       json.RawMessage(`{"run_mode":"async"}`),
		},
		WakeupSource: "task_queue_processor",
		WakeupKind:   "flow_current",
		WakeupPayload: map[string]any{
			"flow_node_execution_id": execution.ID.String(),
		},
	})
	if err != nil {
		t.Fatalf("CreateExecutionWakeup: %v", err)
	}

	staleAt := time.Now().UTC().Add(-5 * time.Minute)
	backdateStrandedExecutionFixture(t, ctx, pool, taskRecord.ID, session.ID, execution.ID, staleAt)

	supervisor, err := NewSupervisor(SupervisorOptions{
		Pool:        pool,
		RunService:  runService,
		ChatService: chatService,
		EventBus:    bus,
		Logger:      newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := supervisor.detectStrandedActiveExecutions(ctx); err != nil {
		t.Fatalf("detectStrandedActiveExecutions: %v", err)
	}

	updatedStarted, err := NewRunRepository(pool).Get(ctx, started.Run.ID)
	if err != nil {
		t.Fatalf("Get started run: %v", err)
	}
	if updatedStarted.Status != "failed" {
		t.Fatalf("started run status = %q, want failed", updatedStarted.Status)
	}

	recoveryRunID := loadLatestStrandedRecoveryRunID(t, ctx, pool, taskRecord.ID)
	if recoveryRunID == uuid.Nil {
		t.Fatal("expected stranded recovery supervisor run")
	}

	runtimeState, err := NewRuntimeStateRepository(pool).GetByScope(ctx, "task", taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByScope runtime state: %v", err)
	}
	if runtimeState.ActiveRunID == nil || *runtimeState.ActiveRunID != recoveryRunID {
		t.Fatalf("runtime active_run_id = %v, want %s", runtimeState.ActiveRunID, recoveryRunID)
	}
	if contract := runtimeState.Contract(); contract.Status != "active" {
		t.Fatalf("runtime status = %q, want active", contract.Status)
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetByID session: %v", err)
	}
	if refreshedSession.CurrentTurnID == nil || *refreshedSession.CurrentTurnID == uuid.Nil {
		t.Fatal("expected supervisor recovery to restore current_turn_id")
	}

	currentTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, *refreshedSession.CurrentTurnID)
	if err != nil {
		t.Fatalf("GetByID current turn: %v", err)
	}
	if currentTurn.Status != "pending" {
		t.Fatalf("current turn status = %q, want pending", currentTurn.Status)
	}
	if currentTurn.RespondingID != worker.ID {
		t.Fatalf("current turn responding_id = %s, want %s", currentTurn.RespondingID, worker.ID)
	}

	messages, err := repo.NewChatMessageRepo(pool).ListBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	if !hasSupervisorRecoveryKickoff(messages, recoveryRunID, execution.ID) {
		t.Fatal("expected supervisor recovery kickoff message")
	}

	refreshedTask, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if refreshedTask.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", refreshedTask.WorkStatus)
	}

	refreshedExecution, err := repo.NewFlowNodeExecutionRepo(pool).GetByID(ctx, execution.ID)
	if err != nil {
		t.Fatalf("GetByID execution: %v", err)
	}
	if refreshedExecution.Status != "active" {
		t.Fatalf("execution status = %q, want active", refreshedExecution.Status)
	}
}

func TestSupervisor_StrandedActiveExecutionFailureBlocksTaskAndAbandonsExecution(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord := seedRunProject(t, ctx, pool, org.ID)
	template, node := seedSupervisorFlowTemplateNode(t, ctx, pool, org.ID, projectRecord.ID, nil, nil)

	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         projectRecord.ID,
		Title:             "Stranded execution without recovery agent",
		WorkStatus:        "in_progress",
		CurrentFlowNodeID: &node.ID,
		FlowTemplateID:    &template.ID,
		CreatedByType:     "system",
		CreatedByID:       nil,
		Metadata:          json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	bus := eventbus.New(pool, newDiscardLogger(), eventbus.Config{})
	chatService, err := chat.NewService(chat.Options{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("chat.NewService: %v", err)
	}
	session, err := chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Metadata:       json.RawMessage(`{"source":"supervisor_integration"}`),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  node.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create flow execution: %v", err)
	}

	runService, err := NewRunService(RunServiceOptions{
		Pool:     pool,
		EventBus: bus,
		Policy:   allowRunCreationPolicy{},
		Logger:   newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewRunService: %v", err)
	}
	wakeSvc := runService.(interface {
		CreateExecutionWakeup(context.Context, executionWakeupInput) (executionWakeupResult, error)
	})
	started, err := wakeSvc.CreateExecutionWakeup(ctx, executionWakeupInput{
		CreateRunInput: CreateRunInput{
			OrganizationID: org.ID,
			ProjectID:      &projectRecord.ID,
			TaskID:         &taskRecord.ID,
			FlowNodeID:     &node.ID,
			SessionID:      &session.ID,
			PrincipalType:  "system",
			PrincipalID:    uuid.Nil,
			TriggerType:    "scheduler",
			Metadata:       json.RawMessage(`{"run_mode":"async"}`),
		},
		WakeupSource: "task_queue_processor",
		WakeupKind:   "flow_current",
		WakeupPayload: map[string]any{
			"flow_node_execution_id": execution.ID.String(),
		},
	})
	if err != nil {
		t.Fatalf("CreateExecutionWakeup: %v", err)
	}

	staleAt := time.Now().UTC().Add(-5 * time.Minute)
	backdateStrandedExecutionFixture(t, ctx, pool, taskRecord.ID, session.ID, execution.ID, staleAt)

	supervisor, err := NewSupervisor(SupervisorOptions{
		Pool:        pool,
		RunService:  runService,
		ChatService: chatService,
		EventBus:    bus,
		Logger:      newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := supervisor.detectStrandedActiveExecutions(ctx); err != nil {
		t.Fatalf("detectStrandedActiveExecutions: %v", err)
	}

	updatedTask, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}

	updatedExecution, err := repo.NewFlowNodeExecutionRepo(pool).GetByID(ctx, execution.ID)
	if err != nil {
		t.Fatalf("GetByID execution: %v", err)
	}
	if updatedExecution.Status != "abandoned" {
		t.Fatalf("execution status = %q, want abandoned", updatedExecution.Status)
	}

	updatedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetByID session: %v", err)
	}
	if updatedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", *updatedSession.CurrentTurnID)
	}

	updatedRun, err := NewRunRepository(pool).Get(ctx, started.Run.ID)
	if err != nil {
		t.Fatalf("Get started run: %v", err)
	}
	if updatedRun.Status != "failed" {
		t.Fatalf("started run status = %q, want failed", updatedRun.Status)
	}

	runtimeState, err := NewRuntimeStateRepository(pool).GetByScope(ctx, "task", taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByScope runtime state: %v", err)
	}
	contract := runtimeState.Contract()
	if contract.Status != "stranded" {
		t.Fatalf("runtime status = %q, want stranded", contract.Status)
	}
	if contract.ResumeDisposition != "terminal" {
		t.Fatalf("runtime resume_disposition = %q, want terminal", contract.ResumeDisposition)
	}
	if !strings.Contains(contract.FailureReason, "no recovery agent") {
		t.Fatalf("runtime failure_reason = %q, want no recovery agent detail", contract.FailureReason)
	}
}

func countSupervisorEventsForRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID uuid.UUID) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run_event
		WHERE run_id = $1
		  AND actor_type = 'supervisor'
	`, runID).Scan(&count); err != nil {
		t.Fatalf("count supervisor run_events: %v", err)
	}
	return count
}

func seedSupervisorFlowNode(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID uuid.UUID) uuid.UUID {
	t.Helper()

	_, node := seedSupervisorFlowTemplateNode(t, ctx, pool, orgID, projectID, nil, nil)
	return node.ID
}

func seedSupervisorFlowTemplateNode(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	orgID, projectID uuid.UUID,
	actorType *string,
	actorID *uuid.UUID,
) (repo.FlowTemplate, repo.FlowNode) {
	t.Helper()

	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "cp-supervisor-flow-" + uuid.NewString()[:8],
		DisplayName:    "Supervisor Flow",
		Description:    "integration flow",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	node, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Supervisor Node",
		NodeType:       "work",
		Position:       1,
		ActorType:      actorType,
		ActorID:        actorID,
		MCPTools:       []repo.FlowNodeMCPTool{},
		ToolDomains:    []string{},
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}

	template.StartNodeID = &node.ID
	updatedTemplate, err := repo.NewFlowTemplateRepo(pool).Update(ctx, template)
	if err != nil {
		t.Fatalf("set flow template start node: %v", err)
	}
	return updatedTemplate, node
}

func seedSupervisorAgent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, displayName string) repo.Agent {
	t.Helper()

	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          displayName,
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent
}

func backdateStrandedExecutionFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskID, sessionID, executionID uuid.UUID, ts time.Time) {
	t.Helper()

	if _, err := pool.Exec(ctx, `UPDATE project_task SET updated_at = $2 WHERE id = $1`, taskID, ts); err != nil {
		t.Fatalf("backdate project_task.updated_at: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE chat_session SET updated_at = $2, last_message_at = NULL WHERE id = $1`, sessionID, ts); err != nil {
		t.Fatalf("backdate chat_session.updated_at: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE flow_node_execution SET started_at = $2 WHERE id = $1`, executionID, ts); err != nil {
		t.Fatalf("backdate flow_node_execution.started_at: %v", err)
	}
}

func loadLatestStrandedRecoveryRunID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskID uuid.UUID) uuid.UUID {
	t.Helper()

	var runID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT id
		FROM run
		WHERE task_id = $1
		  AND trigger_type = 'supervisor'
		  AND metadata->>'stranded_execution' = 'true'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, taskID).Scan(&runID); err != nil {
		t.Fatalf("load stranded recovery run: %v", err)
	}
	return runID
}

func hasSupervisorRecoveryKickoff(messages []repo.ChatMessage, runID, executionID uuid.UUID) bool {
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "user") || len(message.Metadata) == 0 {
			continue
		}
		var metadata map[string]any
		if err := json.Unmarshal(message.Metadata, &metadata); err != nil {
			continue
		}
		if strings.TrimSpace(valueAsString(metadata["source"])) != "supervisor" {
			continue
		}
		if strings.TrimSpace(valueAsString(metadata["run_id"])) != runID.String() {
			continue
		}
		if strings.TrimSpace(valueAsString(metadata["flow_node_execution_id"])) != executionID.String() {
			continue
		}
		return true
	}
	return false
}
