//go:build integration

package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/projectfailure"
	"github.com/samhotchkiss/otter-camp/internal/projectpause"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
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
	sessionTurns, err := repo.NewChatTurnRepo(pool).ListBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	liveTurnCount := 0
	for _, turn := range sessionTurns {
		if turn.Status == "pending" || turn.Status == "in_progress" {
			liveTurnCount++
		}
	}
	if liveTurnCount != 1 {
		t.Fatalf("live turn count = %d, want 1", liveTurnCount)
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

func TestSupervisor_StrandedActiveExecutionSkipsUnresolvedRecoveryCheckpoint(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord := seedRunProject(t, ctx, pool, org.ID)
	worker := seedSupervisorAgent(t, ctx, pool, org.ID, "Recovery Worker")
	template, node := seedSupervisorFlowTemplateNode(t, ctx, pool, org.ID, projectRecord.ID, nil, nil)

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

	turn, err := chatService.CreateTurn(ctx, session.ID, worker.ID)
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if err := chatService.StartTurn(ctx, turn.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if _, err := chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "system",
		Content:   "[Recovery turn halted: file.write for `docs/content-strategy.md` was retried without `content` after one correction. Resume from `.ottercamp/recovery/docs/content-strategy.md` and only retry the final write after the file body exists.]",
	}); err != nil {
		t.Fatalf("AppendMessage recovery halt: %v", err)
	}
	stopReason := "model_error"
	if _, err := repo.NewChatTurnRepo(pool).SetStopReason(ctx, turn.ID, &stopReason); err != nil {
		t.Fatalf("SetStopReason: %v", err)
	}
	if err := chatService.CompleteTurn(ctx, turn.ID); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	checkpointMetadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(taskRecord.Metadata, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:            "docs/content-strategy.md",
		ArtifactPath:          ".ottercamp/recovery/docs/content-strategy.md",
		HistoryStartMessageID: uuid.NewString(),
		HaltTurnID:            turn.ID.String(),
		UpdatedAt:             time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	taskRecord.Metadata = checkpointMetadata
	if _, err := repo.NewProjectTaskRepo(pool).Update(ctx, taskRecord); err != nil {
		t.Fatalf("Update task checkpoint metadata: %v", err)
	}

	staleAt := time.Now().UTC().Add(-5 * time.Minute)
	backdateStrandedExecutionFixture(t, ctx, pool, taskRecord.ID, session.ID, execution.ID, staleAt)

	runService, err := NewRunService(RunServiceOptions{
		Pool:     pool,
		EventBus: bus,
		Policy:   allowRunCreationPolicy{},
		Logger:   newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewRunService: %v", err)
	}
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

	var recoveryCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run
		WHERE task_id = $1
		  AND trigger_type = 'supervisor'
		  AND metadata->>'stranded_execution' = 'true'
	`, taskRecord.ID).Scan(&recoveryCount); err != nil {
		t.Fatalf("count stranded recovery runs: %v", err)
	}
	if recoveryCount != 0 {
		t.Fatalf("stranded recovery runs = %d, want 0 while unresolved recovery checkpoint is current", recoveryCount)
	}

	updatedTask, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "queued" {
		t.Fatalf("task work_status = %q, want queued", updatedTask.WorkStatus)
	}

	updatedExecution, err := repo.NewFlowNodeExecutionRepo(pool).GetByID(ctx, execution.ID)
	if err != nil {
		t.Fatalf("GetByID execution: %v", err)
	}
	if updatedExecution.Status != "active" {
		t.Fatalf("execution status = %q, want active", updatedExecution.Status)
	}

	updatedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetByID session: %v", err)
	}
	if updatedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil after halted turn completed", *updatedSession.CurrentTurnID)
	}

	haltedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("GetByID halted turn: %v", err)
	}
	if haltedTurn.Status != "completed" {
		t.Fatalf("halted turn status = %q, want completed", haltedTurn.Status)
	}
}

func TestSupervisor_StrandedActiveExecutionSkipsDuplicateLiveRecoveryKickoff(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord := seedRunProject(t, ctx, pool, org.ID)
	template, node := seedSupervisorFlowTemplateNode(t, ctx, pool, org.ID, projectRecord.ID, nil, nil)
	worker := seedSupervisorAgent(t, ctx, pool, org.ID, "Recovery Worker")

	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         projectRecord.ID,
		Title:             "Recover stranded execution once",
		WorkStatus:        "in_progress",
		CurrentFlowNodeID: &node.ID,
		FlowTemplateID:    &template.ID,
		AssignedAgentID:   &worker.ID,
		CreatedByType:     "system",
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

	msgMeta, err := json.Marshal(map[string]any{
		"source":                 "supervisor",
		"run_id":                 uuid.NewString(),
		"reason":                 strandedExecutionRecoveryReason,
		"flow_node_execution_id": execution.ID.String(),
		"stranded_execution":     true,
	})
	if err != nil {
		t.Fatalf("Marshal supervisor kickoff metadata: %v", err)
	}
	message, err := chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Metadata:  msgMeta,
	})
	if err != nil {
		t.Fatalf("AppendMessage supervisor kickoff: %v", err)
	}
	recoveryTurn, _, err := repo.NewChatTurnRepo(pool).CreateForMessageAttempt(ctx, session.ID, worker.ID, message.ID, 0)
	if err != nil {
		t.Fatalf("CreateForMessageAttempt: %v", err)
	}
	if recoveryTurn.Status != "pending" {
		t.Fatalf("recovery turn status = %q, want pending", recoveryTurn.Status)
	}

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

	var recoveryRunCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run
		WHERE task_id = $1
		  AND trigger_type = 'supervisor'
		  AND metadata->>'stranded_execution' = 'true'
	`, taskRecord.ID).Scan(&recoveryRunCount); err != nil {
		t.Fatalf("count stranded recovery runs: %v", err)
	}
	if recoveryRunCount != 0 {
		t.Fatalf("stranded recovery runs = %d, want 0 when live recovery kickoff already exists", recoveryRunCount)
	}

	messages, err := repo.NewChatMessageRepo(pool).ListBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	kickoffCount := 0
	for _, item := range messages {
		if strings.TrimSpace(item.Content) != "supervisor recovery: resume task" {
			continue
		}
		var metadata map[string]any
		if len(item.Metadata) == 0 || !json.Valid(item.Metadata) {
			continue
		}
		if err := json.Unmarshal(item.Metadata, &metadata); err != nil {
			t.Fatalf("Unmarshal kickoff metadata: %v", err)
		}
		if fmt.Sprint(metadata["flow_node_execution_id"]) == execution.ID.String() {
			kickoffCount++
		}
	}
	if kickoffCount != 1 {
		t.Fatalf("kickoff count = %d, want 1", kickoffCount)
	}

	updatedStarted, err := NewRunRepository(pool).Get(ctx, started.Run.ID)
	if err != nil {
		t.Fatalf("Get started run: %v", err)
	}
	if updatedStarted.Status != "in_progress" {
		t.Fatalf("started run status = %q, want in_progress when live recovery kickoff already exists", updatedStarted.Status)
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

func TestSupervisor_StrandedActiveExecutionRecoversBlankReviewNodeWithReviewerAssignment(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord := seedRunProject(t, ctx, pool, org.ID)
	template, node := seedSupervisorFlowTemplateNode(t, ctx, pool, org.ID, projectRecord.ID, nil, nil)
	node.NodeType = "review"
	updatedNode, err := repo.NewFlowNodeRepo(pool).Update(ctx, node)
	if err != nil {
		t.Fatalf("update flow node: %v", err)
	}
	node = updatedNode

	worker := seedSupervisorAgent(t, ctx, pool, org.ID, "Review Worker")
	reviewer := seedSupervisorAgent(t, ctx, pool, org.ID, "Review Reviewer")
	if _, err := repo.NewAgentProjectAssignmentRepo(pool).Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        worker.ID,
		ProjectID:      projectRecord.ID,
		Role:           "worker",
		AssignedByType: "system",
	}); err != nil {
		t.Fatalf("assign worker: %v", err)
	}
	if _, err := repo.NewAgentProjectAssignmentRepo(pool).Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        reviewer.ID,
		ProjectID:      projectRecord.ID,
		Role:           "reviewer",
		AssignedByType: "system",
	}); err != nil {
		t.Fatalf("assign reviewer: %v", err)
	}

	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         projectRecord.ID,
		Title:             "Recover blank review execution",
		WorkStatus:        "review",
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
	if currentTurn.RespondingID != reviewer.ID {
		t.Fatalf("current turn responding_id = %s, want reviewer %s", currentTurn.RespondingID, reviewer.ID)
	}
}

func TestSupervisor_StrandedActiveExecutionRecoversLiveTurnWithoutRunOwnership(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord := seedRunProject(t, ctx, pool, org.ID)
	worker := seedSupervisorAgent(t, ctx, pool, org.ID, "Recovery Worker")
	template, node := seedSupervisorFlowTemplateNode(t, ctx, pool, org.ID, projectRecord.ID, nil, nil)

	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         projectRecord.ID,
		Title:             "Recover stranded live turn without run",
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

	turn, err := chatService.CreateTurn(ctx, session.ID, worker.ID)
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	userMessage, err := chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue the task",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := repo.NewChatTurnRepo(pool).SetTriggerMessageID(ctx, turn.ID, &userMessage.ID); err != nil {
		t.Fatalf("SetTriggerMessageID: %v", err)
	}
	if err := chatService.StartTurn(ctx, turn.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	staleAt := time.Now().UTC().Add(-5 * time.Minute)
	backdateStrandedExecutionFixture(t, ctx, pool, taskRecord.ID, session.ID, execution.ID, staleAt)
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = $2
		WHERE id = $1
	`, turn.ID, staleAt); err != nil {
		t.Fatalf("backdate turn: %v", err)
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

	recoveryRunID := loadLatestStrandedRecoveryRunID(t, ctx, pool, taskRecord.ID)
	if recoveryRunID == uuid.Nil {
		t.Fatal("expected stranded recovery supervisor run")
	}

	staleTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("GetByID stale turn: %v", err)
	}
	if staleTurn.Status != "failed" {
		t.Fatalf("stale turn status = %q, want failed", staleTurn.Status)
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetByID session: %v", err)
	}
	if refreshedSession.CurrentTurnID == nil || *refreshedSession.CurrentTurnID == uuid.Nil {
		t.Fatal("expected supervisor recovery to create a fresh current turn")
	}
	if *refreshedSession.CurrentTurnID == turn.ID {
		t.Fatalf("current_turn_id = %s, want fresh recovery turn", *refreshedSession.CurrentTurnID)
	}

	currentTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, *refreshedSession.CurrentTurnID)
	if err != nil {
		t.Fatalf("GetByID current turn: %v", err)
	}
	if currentTurn.Status != "pending" {
		t.Fatalf("current turn status = %q, want pending", currentTurn.Status)
	}

	messages, err := repo.NewChatMessageRepo(pool).ListBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	if !hasSupervisorRecoveryKickoff(messages, recoveryRunID, execution.ID) {
		t.Fatal("expected supervisor recovery kickoff message")
	}
}

func TestSupervisor_StrandedActiveExecutionRepairsClosedExecutionSession(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord := seedRunProject(t, ctx, pool, org.ID)
	template, node := seedSupervisorFlowTemplateNode(t, ctx, pool, org.ID, projectRecord.ID, nil, nil)
	worker := seedSupervisorAgent(t, ctx, pool, org.ID, "Recovery Worker")
	if _, err := repo.NewAgentProjectAssignmentRepo(pool).Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        worker.ID,
		ProjectID:      projectRecord.ID,
		Role:           "worker",
		AssignedByType: "system",
	}); err != nil {
		t.Fatalf("assign worker: %v", err)
	}

	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         projectRecord.ID,
		Title:             "Recover stranded execution with closed session",
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
	if err := chatService.CloseSession(ctx, session.ID); err != nil {
		t.Fatalf("CloseSession: %v", err)
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
	if refreshedExecution.SessionID == nil || *refreshedExecution.SessionID == uuid.Nil {
		t.Fatal("expected repaired execution session")
	}
	if *refreshedExecution.SessionID == session.ID {
		t.Fatalf("execution session_id = %s, want repaired session distinct from closed session %s", *refreshedExecution.SessionID, session.ID)
	}

	repairedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, *refreshedExecution.SessionID)
	if err != nil {
		t.Fatalf("GetByID repaired session: %v", err)
	}
	if repairedSession.Status != "active" {
		t.Fatalf("repaired session status = %q, want active", repairedSession.Status)
	}
	if repairedSession.CurrentTurnID == nil || *repairedSession.CurrentTurnID == uuid.Nil {
		t.Fatal("expected supervisor recovery to create a live turn on repaired session")
	}

	messages, err := repo.NewChatMessageRepo(pool).ListBySession(ctx, repairedSession.ID)
	if err != nil {
		t.Fatalf("ListBySession repaired messages: %v", err)
	}
	if !hasSupervisorRecoveryKickoff(messages, recoveryRunID, execution.ID) {
		t.Fatal("expected supervisor recovery kickoff message on repaired session")
	}
}

func TestSupervisor_ImpossibleInProgressTaskWithoutRuntimeOrExecutionBlocksTask(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord := seedRunProject(t, ctx, pool, org.ID)
	worker := seedSupervisorAgent(t, ctx, pool, org.ID, "Impossible State Worker")
	template, node := seedSupervisorFlowTemplateNode(t, ctx, pool, org.ID, projectRecord.ID, nil, nil)

	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         projectRecord.ID,
		Title:             "Impossible live task",
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
	if _, err := pool.Exec(ctx, `UPDATE project_task SET updated_at = $2 WHERE id = $1`, taskRecord.ID, time.Now().UTC().Add(-20*time.Minute)); err != nil {
		t.Fatalf("backdate impossible task: %v", err)
	}

	bus := eventbus.New(pool, newDiscardLogger(), eventbus.Config{})
	runService, err := NewRunService(RunServiceOptions{
		Pool:     pool,
		EventBus: bus,
		Policy:   allowRunCreationPolicy{},
		Logger:   newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewRunService: %v", err)
	}

	supervisor, err := NewSupervisor(SupervisorOptions{
		Pool:       pool,
		RunService: runService,
		EventBus:   bus,
		Logger:     newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := supervisor.detectImpossibleLiveTasks(ctx); err != nil {
		t.Fatalf("detectImpossibleLiveTasks: %v", err)
	}

	updatedTask, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != node.ID {
		t.Fatalf("current_flow_node_id = %v, want %s after impossible-state block", updatedTask.CurrentFlowNodeID, node.ID)
	}

	executions, err := repo.NewFlowNodeExecutionRepo(pool).ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("list flow executions: %v", err)
	}
	if len(executions) != 0 {
		t.Fatalf("flow execution count = %d, want 0 for impossible-state fixture", len(executions))
	}

	var eventType string
	var payload json.RawMessage
	if err := pool.QueryRow(ctx, `
		SELECT event_type, payload
		FROM project_task_event
		WHERE task_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, taskRecord.ID).Scan(&eventType, &payload); err != nil {
		t.Fatalf("load latest task event: %v", err)
	}
	if eventType != "status.changed" {
		t.Fatalf("latest event_type = %q, want status.changed", eventType)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal latest payload: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", decoded["blocker_reason"])); got == "" || !strings.Contains(got, "impossible live task state") {
		t.Fatalf("blocker_reason = %q, want impossible live task detail", got)
	}

	updatedProject, err := repo.NewProjectRepo(pool).GetByID(ctx, projectRecord.ID)
	if err != nil {
		t.Fatalf("GetByID project: %v", err)
	}
	pauseState := projectpause.Parse(updatedProject.Settings)
	if !pauseState.IsPaused {
		t.Fatalf("project pause state = %+v, want paused", pauseState)
	}
	if !strings.Contains(pauseState.Reason, "impossible live task state") {
		t.Fatalf("project pause reason = %q, want impossible live task detail", pauseState.Reason)
	}
	failureState := projectfailure.Parse(updatedProject.Settings)
	if failureState.Action != "pause" {
		t.Fatalf("automatic failure action = %q, want pause", failureState.Action)
	}
	if failureState.Source != "execution_runtime" {
		t.Fatalf("automatic failure source = %q, want execution_runtime", failureState.Source)
	}
	if failureState.FailureCategory != "execution_runtime" {
		t.Fatalf("automatic failure category = %q, want execution_runtime", failureState.FailureCategory)
	}
	if failureState.FailureClass != "impossible_live_task_state" {
		t.Fatalf("automatic failure class = %q, want impossible_live_task_state", failureState.FailureClass)
	}
	if !strings.Contains(failureState.FailureReason, "impossible live task state") {
		t.Fatalf("automatic failure reason = %q, want impossible live task detail", failureState.FailureReason)
	}
}

func TestSupervisor_UnpausedLegacyImpossibleLiveProjectGetsPaused(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord := seedRunProject(t, ctx, pool, org.ID)
	worker := seedSupervisorAgent(t, ctx, pool, org.ID, "Legacy Impossible Worker")
	template, _ := seedSupervisorFlowTemplateNode(t, ctx, pool, org.ID, projectRecord.ID, nil, nil)

	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       projectRecord.ID,
		Title:           "Legacy impossible blocked task",
		WorkStatus:      "blocked",
		FlowTemplateID:  &template.ID,
		AssignedAgentID: &worker.ID,
		CreatedByType:   "system",
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE project_task SET updated_at = $2 WHERE id = $1`, taskRecord.ID, time.Now().UTC().Add(-20*time.Minute)); err != nil {
		t.Fatalf("backdate blocked task: %v", err)
	}

	bus := eventbus.New(pool, newDiscardLogger(), eventbus.Config{})
	runService, err := NewRunService(RunServiceOptions{
		Pool:     pool,
		EventBus: bus,
		Policy:   allowRunCreationPolicy{},
		Logger:   newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewRunService: %v", err)
	}

	supervisor, err := NewSupervisor(SupervisorOptions{
		Pool:       pool,
		RunService: runService,
		EventBus:   bus,
		Logger:     newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := supervisor.detectUnpausedImpossibleLiveProjects(ctx); err != nil {
		t.Fatalf("detectUnpausedImpossibleLiveProjects: %v", err)
	}

	updatedProject, err := repo.NewProjectRepo(pool).GetByID(ctx, projectRecord.ID)
	if err != nil {
		t.Fatalf("GetByID project: %v", err)
	}
	pauseState := projectpause.Parse(updatedProject.Settings)
	if !pauseState.IsPaused {
		t.Fatalf("project pause state = %+v, want paused", pauseState)
	}
	if !strings.Contains(pauseState.Reason, "impossible live task state") {
		t.Fatalf("project pause reason = %q, want impossible live task detail", pauseState.Reason)
	}
	failureState := projectfailure.Parse(updatedProject.Settings)
	if failureState.FailureClass != "impossible_live_task_state" {
		t.Fatalf("automatic failure class = %q, want impossible_live_task_state", failureState.FailureClass)
	}
	if !strings.Contains(failureState.FailureReason, "impossible live task state") {
		t.Fatalf("automatic failure reason = %q, want impossible live task detail", failureState.FailureReason)
	}
}

func TestSupervisor_ResumableBlockedTaskGetsAutoResumed(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord, _ := seedRunProjectTaskWithPM(t, ctx, pool, org.ID)
	worker := seedSupervisorAgent(t, ctx, pool, org.ID, "Resumable Blocked Worker")
	template := seedTaskQueueFlowTemplate(t, ctx, pool, org.ID, projectRecord.ID)
	if template.StartNodeID == nil || *template.StartNodeID == uuid.Nil {
		t.Fatal("expected executable flow template start node")
	}

	taskRepo := repo.NewProjectTaskRepo(pool)
	taskRecord, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         projectRecord.ID,
		Title:             "Auto-resume validation blocked task",
		WorkStatus:        "in_progress",
		FlowTemplateID:    &template.ID,
		CurrentFlowNodeID: template.StartNodeID,
		AssignedAgentID:   &worker.ID,
		CreatedByType:     "system",
		Metadata:          json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:     pool,
		EventBus: eventbus.New(pool, newDiscardLogger(), eventbus.Config{}),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := taskService.MarkBlocked(ctx, taskRecord.ID, "deterministic tool validation loop blocked after 3 identical failures: file.write (content_required)", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}

	blockedTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID blocked task: %v", err)
	}
	guardedMetadata, err := tasksvc.MergeValidationGuardMetadata(blockedTask.Metadata, tasksvc.ValidationGuardState{
		InitialMessageID:   uuid.NewString(),
		Fingerprint:        "file.write:content_required",
		AttemptFingerprint: "file.write:content_required:attempt",
		ToolName:           "file.write",
		FailureClass:       "tool_validation",
		FailureCode:        "content_required",
		FailureReason:      "file.write requires content",
		Count:              3,
		BlockThreshold:     3,
		Blocked:            true,
	})
	if err != nil {
		t.Fatalf("MergeValidationGuardMetadata: %v", err)
	}
	blockedTask.Metadata = guardedMetadata
	if _, err := taskRepo.Update(ctx, blockedTask); err != nil {
		t.Fatalf("update guarded task: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE project_task SET updated_at = $2 WHERE id = $1`, taskRecord.ID, time.Now().UTC().Add(-2*time.Minute)); err != nil {
		t.Fatalf("backdate blocked task: %v", err)
	}

	bus := eventbus.New(pool, newDiscardLogger(), eventbus.Config{})
	runService, err := NewRunService(RunServiceOptions{
		Pool:     pool,
		EventBus: bus,
		Policy:   allowRunCreationPolicy{},
		Logger:   newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewRunService: %v", err)
	}

	supervisor, err := NewSupervisor(SupervisorOptions{
		Pool:       pool,
		RunService: runService,
		EventBus:   bus,
		Logger:     newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := supervisor.detectResumableBlockedTasks(ctx); err != nil {
		t.Fatalf("detectResumableBlockedTasks: %v", err)
	}

	updatedTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "queued" {
		t.Fatalf("task work_status = %q, want queued", updatedTask.WorkStatus)
	}
	if _, ok := tasksvc.ParseValidationGuard(updatedTask.Metadata); ok {
		t.Fatalf("expected validation guard to be cleared, metadata=%s", string(updatedTask.Metadata))
	}

	var eventType string
	var payload json.RawMessage
	if err := pool.QueryRow(ctx, `
		SELECT event_type, payload
		FROM project_task_event
		WHERE task_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, taskRecord.ID).Scan(&eventType, &payload); err != nil {
		t.Fatalf("load latest task event: %v", err)
	}
	if eventType != "status.changed" {
		t.Fatalf("latest event_type = %q, want status.changed", eventType)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal latest payload: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", decoded["recovery_action"])); got != tasksvc.RecoveryActionResumeBlockedTask {
		t.Fatalf("recovery_action = %q, want %q", got, tasksvc.RecoveryActionResumeBlockedTask)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", decoded["validation_failure_code"])); got != "content_required" {
		t.Fatalf("validation_failure_code = %q, want content_required", got)
	}
}

func TestSupervisor_StrandedActiveExecutionFailureFailsWithoutTaskService(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord := seedRunProject(t, ctx, pool, org.ID)
	template, node := seedSupervisorFlowTemplateNode(t, ctx, pool, org.ID, projectRecord.ID, nil, nil)

	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         projectRecord.ID,
		Title:             "Stranded execution without task service",
		WorkStatus:        "in_progress",
		CurrentFlowNodeID: &node.ID,
		FlowTemplateID:    &template.ID,
		CreatedByType:     "system",
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
	supervisor.taskService = nil

	err = supervisor.detectStrandedActiveExecutions(ctx)
	if err == nil || !strings.Contains(err.Error(), errMissingTaskTransitionServiceForStrandedExecution) {
		t.Fatalf("detectStrandedActiveExecutions error = %v, want contains %q", err, errMissingTaskTransitionServiceForStrandedExecution)
	}

	updatedTask, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", updatedTask.WorkStatus)
	}

	updatedExecution, err := repo.NewFlowNodeExecutionRepo(pool).GetByID(ctx, execution.ID)
	if err != nil {
		t.Fatalf("GetByID execution: %v", err)
	}
	if updatedExecution.Status != "active" {
		t.Fatalf("execution status = %q, want active", updatedExecution.Status)
	}

	updatedRun, err := NewRunRepository(pool).Get(ctx, started.Run.ID)
	if err != nil {
		t.Fatalf("Get started run: %v", err)
	}
	if updatedRun.Status != "failed" {
		t.Fatalf("started run status = %q, want failed", updatedRun.Status)
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
