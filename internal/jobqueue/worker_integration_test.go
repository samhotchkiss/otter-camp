//go:build integration

package jobqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestJobWorkerProcessesEnqueuedJobs(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         100 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	var handled atomic.Int32
	worker.Register("test.process", func(context.Context, Job) error {
		handled.Add(1)
		return nil
	})

	for i := 0; i < 5; i++ {
		if _, err := worker.Enqueue(context.Background(), nil, "test.process", 100, map[string]any{"n": i}, nil); err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	defer func() {
		cancel()
		_ = worker.Stop()
	}()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if handled.Load() == 5 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if handled.Load() != 5 {
		t.Fatalf("processed %d jobs, want 5", handled.Load())
	}

	var doneCount int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM job_queue WHERE status = 'done'`).Scan(&doneCount); err != nil {
		t.Fatalf("count done jobs failed: %v", err)
	}
	if doneCount != 5 {
		t.Fatalf("done jobs = %d, want 5", doneCount)
	}
}

func TestJobWorkerProcessesBatchConcurrently(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		BatchSize:            3,
		PollInterval:         100 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	release := make(chan struct{})
	var started atomic.Int32
	worker.Register("test.concurrent", func(context.Context, Job) error {
		started.Add(1)
		<-release
		return nil
	})

	for i := 0; i < 3; i++ {
		if _, err := worker.Enqueue(context.Background(), nil, "test.concurrent", 100, map[string]any{"n": i}, nil); err != nil {
			t.Fatalf("enqueue concurrent job %d failed: %v", i+1, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	released := false
	defer func() {
		cancel()
		if !released {
			close(release)
		}
		_ = worker.Stop()
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if started.Load() == 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if started.Load() != 3 {
		t.Fatalf("started concurrent jobs = %d, want 3 claimed/executing together", started.Load())
	}

	close(release)
	released = true
	waitForDoneJobs(t, pool, 3, 5*time.Second)
}

func TestJobWorkerRefillsFreedSlotBeforeWholeBatchFinishes(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		BatchSize:            2,
		PollInterval:         100 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	slowRelease := make(chan struct{})
	firstFastStarted := make(chan struct{}, 1)
	secondFastStarted := make(chan struct{}, 1)
	var fastCount atomic.Int32

	worker.Register("test.refill.slow", func(context.Context, Job) error {
		<-slowRelease
		return nil
	})
	worker.Register("test.refill.fast", func(context.Context, Job) error {
		switch fastCount.Add(1) {
		case 1:
			firstFastStarted <- struct{}{}
		case 2:
			secondFastStarted <- struct{}{}
		}
		return nil
	})

	if _, err := worker.Enqueue(context.Background(), nil, "test.refill.slow", 100, map[string]any{"n": 1}, nil); err != nil {
		t.Fatalf("enqueue slow failed: %v", err)
	}
	if _, err := worker.Enqueue(context.Background(), nil, "test.refill.fast", 90, map[string]any{"n": 2}, nil); err != nil {
		t.Fatalf("enqueue first fast failed: %v", err)
	}
	if _, err := worker.Enqueue(context.Background(), nil, "test.refill.fast", 80, map[string]any{"n": 3}, nil); err != nil {
		t.Fatalf("enqueue second fast failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	defer func() {
		cancel()
		close(slowRelease)
		_ = worker.Stop()
	}()

	select {
	case <-firstFastStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first fast job to start")
	}

	select {
	case <-secondFastStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second fast job did not start while slow batch peer was still running")
	}
}

func TestJobWorkerPollsNewJobsWhileEarlierClaimedJobStillRunning(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		BatchSize:            2,
		PollInterval:         20 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	slowRelease := make(chan struct{})
	slowStarted := make(chan struct{}, 1)
	fastStarted := make(chan struct{}, 1)

	worker.Register("test.arrival.slow", func(context.Context, Job) error {
		select {
		case slowStarted <- struct{}{}:
		default:
		}
		<-slowRelease
		return nil
	})
	worker.Register("test.arrival.fast", func(context.Context, Job) error {
		select {
		case fastStarted <- struct{}{}:
		default:
		}
		return nil
	})

	if _, err := worker.Enqueue(context.Background(), nil, "test.arrival.slow", 100, map[string]any{"n": 1}, nil); err != nil {
		t.Fatalf("enqueue slow failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	defer func() {
		cancel()
		close(slowRelease)
		_ = worker.Stop()
	}()

	select {
	case <-slowStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for slow job to start")
	}

	if _, err := worker.Enqueue(context.Background(), nil, "test.arrival.fast", 90, map[string]any{"n": 2}, nil); err != nil {
		t.Fatalf("enqueue fast after slow start failed: %v", err)
	}

	select {
	case <-fastStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("fast job did not start while earlier claimed job was still running")
	}
}

func TestJobWorkerPeriodicCleanupClosesTerminalProjectTaskAsyncSessions(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "periodic-terminal-project-task-session-cleanup-worker",
		PollInterval:         50 * time.Millisecond,
		StaleScanInterval:    50 * time.Millisecond,
		CleanupEnqueuePeriod: time.Hour,
	})

	org := createOrgForJobQueue(t, pool, "periodic-terminal-project-task-session-cleanup")
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "periodic-terminal-project-task-session-cleanup-" + uuid.NewString()[:8],
		DisplayName:    "Periodic Terminal Project Task Session Cleanup",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Periodic Cleanup Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You handle terminal session cleanup.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	description := "Verify periodic worker cleanup closes async task sessions for terminal tasks."
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Periodic terminal task cleanup",
		Description:    &description,
		WorkStatus:     "draft",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE project_task
		SET work_status = 'cancelled'
		WHERE id = $1
	`, task.ID); err != nil {
		t.Fatalf("mark task cancelled: %v", err)
	}

	var sessionID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO chat_session (
			organization_id, scope_type, scope_id, mode, status,
			created_by_type, created_by_id, metadata
		)
		VALUES ($1, 'project_task', $2, 'async', 'active', 'system', $3, '{}'::jsonb)
		RETURNING id
	`, org.ID, task.ID, uuid.Nil).Scan(&sessionID); err != nil {
		t.Fatalf("insert terminal task session: %v", err)
	}
	var turnID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO chat_turn (
			session_id, turn_number, responding_type, responding_id, status
		)
		VALUES ($1, 1, 'agent', $2, 'in_progress')
		RETURNING id
	`, sessionID, agent.ID).Scan(&turnID); err != nil {
		t.Fatalf("insert in-progress turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_session
		SET current_turn_id = $2
		WHERE id = $1
	`, sessionID, turnID); err != nil {
		t.Fatalf("set current_turn_id: %v", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	startWorker(worker, runCtx)
	defer func() {
		cancel()
		_ = worker.Stop()
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var (
			status        string
			currentTurnID *uuid.UUID
			closedAt      *time.Time
		)
		if err := pool.QueryRow(ctx, `
			SELECT status, current_turn_id, closed_at
			FROM chat_session
			WHERE id = $1
		`, sessionID).Scan(&status, &currentTurnID, &closedAt); err != nil {
			t.Fatalf("query session: %v", err)
		}
		if status == "closed" {
			if currentTurnID != nil {
				t.Fatalf("current_turn_id = %v, want nil", currentTurnID)
			}
			if closedAt == nil {
				t.Fatal("closed_at is nil")
			}

			var turnStatus string
			var stopReason *string
			if err := pool.QueryRow(ctx, `
				SELECT status, stop_reason
				FROM chat_turn
				WHERE id = $1
			`, turnID).Scan(&turnStatus, &stopReason); err != nil {
				t.Fatalf("query turn: %v", err)
			}
			if turnStatus != "cancelled" {
				t.Fatalf("turn status = %q, want cancelled", turnStatus)
			}
			if stopReason == nil || *stopReason != "session_closed" {
				t.Fatalf("turn stop_reason = %v, want session_closed", stopReason)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatal("timed out waiting for periodic terminal session cleanup")
}

func TestJobWorkerRetireClosedAsyncSessionRunsFailsNonTerminalTaskRuns(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})
	worker.startupAt = time.Now().UTC().Add(-10 * time.Minute)

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "retire-closed-session-runs",
		DisplayName: "Retire Closed Session Runs",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "retire-closed-session-runs-" + uuid.NewString()[:8],
		DisplayName:    "Retire Closed Session Runs",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Worker Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "Handle closed session cleanup.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "retire-closed-session-runs-template",
		DisplayName:    "Retire Closed Session Runs Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Closed session run should fail",
		WorkStatus:     "in_progress",
		BlocksScope:    "task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).Close(ctx, session.ID); err != nil {
		t.Fatalf("close session: %v", err)
	}
	startedAt := time.Now().UTC()
	var runID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO run (
			organization_id, project_id, task_id, session_id,
			principal_type, principal_id, status, trigger_type, metadata, started_at
		)
		VALUES ($1, $2, $3, $4, 'agent', $5, 'in_progress', 'supervisor', '{"source":"supervisor"}'::jsonb, $6)
		RETURNING id
	`, org.ID, project.ID, task.ID, session.ID, agent.ID, startedAt).Scan(&runID); err != nil {
		t.Fatalf("create run: %v", err)
	}

	retired, err := worker.RetireClosedAsyncSessionRuns(ctx)
	if err != nil {
		t.Fatalf("RetireClosedAsyncSessionRuns: %v", err)
	}
	if retired != 1 {
		t.Fatalf("retired runs = %d, want 1", retired)
	}

	var (
		status        string
		failureClass  *string
		failureReason *string
		completedAt   *time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, failure_class, failure_reason, completed_at
		FROM run
		WHERE id = $1
	`, runID).Scan(&status, &failureClass, &failureReason, &completedAt); err != nil {
		t.Fatalf("query run: %v", err)
	}
	if status != "failed" {
		t.Fatalf("run status = %q, want failed", status)
	}
	if failureClass == nil || *failureClass != "transient" {
		t.Fatalf("run failure_class = %v, want transient", failureClass)
	}
	if failureReason == nil || *failureReason != "async session closed without a live task turn" {
		t.Fatalf("run failure_reason = %v, want async session closed without a live task turn", failureReason)
	}
	if completedAt == nil {
		t.Fatal("completed_at is nil")
	}
}

func TestJobWorkerRetireClosedAsyncSessionRunsCompletesDoneTaskRuns(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "retire-done-task-runs",
		DisplayName: "Retire Done Task Runs",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "retire-done-task-runs-" + uuid.NewString()[:8],
		DisplayName:    "Retire Done Task Runs",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Worker Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "Handle closed session cleanup.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "retire-done-task-runs-template",
		DisplayName:    "Retire Done Task Runs Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	completedAt := time.Now().UTC()
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Closed session run should complete",
		WorkStatus:     "done",
		BlocksScope:    "task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
		Metadata:       json.RawMessage(`{}`),
		CompletedAt:    &completedAt,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).Close(ctx, session.ID); err != nil {
		t.Fatalf("close session: %v", err)
	}
	startedAt := time.Now().UTC()
	var runID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO run (
			organization_id, project_id, task_id, session_id,
			principal_type, principal_id, status, trigger_type, metadata, started_at
		)
		VALUES ($1, $2, $3, $4, 'agent', $5, 'in_progress', 'supervisor', '{"source":"supervisor"}'::jsonb, $6)
		RETURNING id
	`, org.ID, project.ID, task.ID, session.ID, agent.ID, startedAt).Scan(&runID); err != nil {
		t.Fatalf("create run: %v", err)
	}

	retired, err := worker.RetireClosedAsyncSessionRuns(ctx)
	if err != nil {
		t.Fatalf("RetireClosedAsyncSessionRuns: %v", err)
	}
	if retired != 1 {
		t.Fatalf("retired runs = %d, want 1", retired)
	}

	var (
		status        string
		failureClass  *string
		failureReason *string
		runCompleted  *time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, failure_class, failure_reason, completed_at
		FROM run
		WHERE id = $1
	`, runID).Scan(&status, &failureClass, &failureReason, &runCompleted); err != nil {
		t.Fatalf("query run: %v", err)
	}
	if status != "completed" {
		t.Fatalf("run status = %q, want completed", status)
	}
	if failureClass != nil {
		t.Fatalf("run failure_class = %v, want nil", failureClass)
	}
	if failureReason != nil {
		t.Fatalf("run failure_reason = %v, want nil", failureReason)
	}
	if runCompleted == nil {
		t.Fatal("completed_at is nil")
	}
}

func TestJobWorkerReservesSlotsForAgentTurnsOverBackgroundJobs(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		BatchSize:            6,
		PollInterval:         20 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	backgroundRelease := make(chan struct{})
	agentRelease := make(chan struct{})
	agentStarted := make(chan struct{}, 1)
	initialAgentStarted := make(chan struct{}, 1)
	var backgroundCount atomic.Int32
	var agentCount atomic.Int32

	worker.Register("test.background", func(context.Context, Job) error {
		backgroundCount.Add(1)
		<-backgroundRelease
		return nil
	})
	worker.Register(agentTurnJobType, func(context.Context, Job) error {
		switch agentCount.Add(1) {
		case 1:
			select {
			case initialAgentStarted <- struct{}{}:
			default:
			}
			<-agentRelease
		default:
			select {
			case agentStarted <- struct{}{}:
			default:
			}
		}
		return nil
	})

	sessionID := uuid.New()
	messageID := uuid.New()
	if _, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, map[string]any{
		"session_id": sessionID,
		"message_id": messageID,
	}, nil); err != nil {
		t.Fatalf("enqueue initial agent_turn failed: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := worker.Enqueue(context.Background(), nil, "test.background", 60, map[string]any{"n": i}, nil); err != nil {
			t.Fatalf("enqueue background %d failed: %v", i+1, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	backgroundClosed := false
	agentClosed := false
	defer func() {
		cancel()
		if !backgroundClosed {
			close(backgroundRelease)
		}
		if !agentClosed {
			close(agentRelease)
		}
		_ = worker.Stop()
	}()

	select {
	case <-initialAgentStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("initial agent_turn did not start")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && backgroundCount.Load() < 2 {
		time.Sleep(20 * time.Millisecond)
	}
	if backgroundCount.Load() != 2 {
		t.Fatalf("background started = %d, want 2 with reserved agent slots", backgroundCount.Load())
	}

	if _, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, map[string]any{
		"session_id": uuid.New(),
		"message_id": uuid.New(),
	}, nil); err != nil {
		t.Fatalf("enqueue agent_turn failed: %v", err)
	}

	close(agentRelease)
	agentClosed = true
	select {
	case <-agentStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("agent_turn did not start while background jobs were occupying reserved slots")
	}
}

func TestJobWorkerPrefersNonMaintenanceBackgroundJobsOverMaintenance(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		BatchSize:            6,
		PollInterval:         20 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	maintenanceRelease := make(chan struct{})
	foregroundStarted := make(chan struct{}, 1)
	maintenanceStarted := make(chan struct{}, 8)

	worker.Register("test.foreground", func(context.Context, Job) error {
		select {
		case foregroundStarted <- struct{}{}:
		default:
		}
		return nil
	})
	worker.Register(rollupUpdateJobType, func(context.Context, Job) error {
		select {
		case maintenanceStarted <- struct{}{}:
		default:
		}
		<-maintenanceRelease
		return nil
	})

	orgID := uuid.New()
	for i := 0; i < 5; i++ {
		if _, err := worker.Enqueue(context.Background(), nil, rollupUpdateJobType, 60, map[string]any{
			"org_id":      orgID,
			"rollup_date": fmt.Sprintf("2026-03-%02d", i+1),
		}, nil); err != nil {
			t.Fatalf("enqueue maintenance %d failed: %v", i+1, err)
		}
	}
	if _, err := worker.Enqueue(context.Background(), nil, "test.foreground", 50, map[string]any{"n": 1}, nil); err != nil {
		t.Fatalf("enqueue foreground failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	maintenanceClosed := false
	defer func() {
		cancel()
		if !maintenanceClosed {
			close(maintenanceRelease)
		}
		_ = worker.Stop()
	}()

	select {
	case <-foregroundStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("foreground job did not start while maintenance jobs were pending")
	}

	close(maintenanceRelease)
	maintenanceClosed = true
}

func TestJobWorkerClaimPendingLimitClaimsSingleJobEvenWhenBatchSizeIsLarger(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		BatchSize:            10,
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	if _, err := worker.Enqueue(context.Background(), nil, "test.slow", 100, map[string]any{"n": 1}, nil); err != nil {
		t.Fatalf("enqueue slow failed: %v", err)
	}
	if _, err := worker.Enqueue(context.Background(), nil, "test.fast", 90, map[string]any{"n": 2}, nil); err != nil {
		t.Fatalf("enqueue fast failed: %v", err)
	}

	claimed, err := worker.claimPendingLimit(context.Background(), 1)
	if err != nil {
		t.Fatalf("claimPendingLimit failed: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(claimed))
	}
	if claimed[0].JobType != "test.slow" {
		t.Fatalf("claimed job type = %s, want test.slow", claimed[0].JobType)
	}

	var (
		slowClaimed int
		fastPending int
		fastClaimed int
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM job_queue WHERE job_type = 'test.slow' AND status = 'claimed'
	`).Scan(&slowClaimed); err != nil {
		t.Fatalf("count slow claimed: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM job_queue WHERE job_type = 'test.fast' AND status = 'pending'
	`).Scan(&fastPending); err != nil {
		t.Fatalf("count fast pending: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM job_queue WHERE job_type = 'test.fast' AND status = 'claimed'
	`).Scan(&fastClaimed); err != nil {
		t.Fatalf("count fast claimed: %v", err)
	}
	if slowClaimed != 1 {
		t.Fatalf("slow claimed = %d, want 1", slowClaimed)
	}
	if fastPending != 1 {
		t.Fatalf("fast pending while slow blocked = %d, want 1", fastPending)
	}
	if fastClaimed != 0 {
		t.Fatalf("fast claimed while slow blocked = %d, want 0", fastClaimed)
	}
}

func TestAgentTurnEnqueueDedupesActiveAttempt(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	sessionID := uuid.New()
	messageID := uuid.New()
	payload := map[string]any{
		"session_id": sessionID,
		"message_id": messageID,
	}

	firstID, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, payload, nil)
	if err != nil {
		t.Fatalf("enqueue first agent_turn: %v", err)
	}
	secondID, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, payload, nil)
	if err != nil {
		t.Fatalf("enqueue duplicate agent_turn: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("duplicate enqueue id = %s, want %s", secondID, firstID)
	}

	var activeRows int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM job_queue
		WHERE dedupe_key = $1
		  AND status IN ('pending', 'claimed')
	`, AgentTurnAttemptKey(sessionID, messageID, 0)).Scan(&activeRows); err != nil {
		t.Fatalf("count active deduped rows: %v", err)
	}
	if activeRows != 1 {
		t.Fatalf("active deduped rows = %d, want 1", activeRows)
	}
}

func TestAgentTurnDuplicateEnqueueWhileClaimedDoesNotCreateSecondLiveClaim(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "agent-turn-claim",
		BatchSize:            10,
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	sessionID := uuid.New()
	messageID := uuid.New()
	payload := map[string]any{
		"session_id": sessionID,
		"message_id": messageID,
	}

	firstID, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, payload, nil)
	if err != nil {
		t.Fatalf("enqueue first agent_turn: %v", err)
	}
	claimed, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claim pending agent_turn: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed rows = %d, want 1", len(claimed))
	}
	if claimed[0].ID != firstID {
		t.Fatalf("claimed row id = %s, want %s", claimed[0].ID, firstID)
	}

	secondID, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, payload, nil)
	if err != nil {
		t.Fatalf("enqueue duplicate while claimed: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("duplicate claimed enqueue id = %s, want %s", secondID, firstID)
	}

	again, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claim pending after duplicate enqueue: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second claim count = %d, want 0", len(again))
	}

	var activeRows int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM job_queue
		WHERE dedupe_key = $1
		  AND status IN ('pending', 'claimed')
	`, AgentTurnAttemptKey(sessionID, messageID, 0)).Scan(&activeRows); err != nil {
		t.Fatalf("count claimed deduped rows: %v", err)
	}
	if activeRows != 1 {
		t.Fatalf("active claimed rows = %d, want 1", activeRows)
	}
}

func TestAgentTurnRetryEnqueueCollapsesPendingGroupToNewestAttempt(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	sessionID := uuid.New()
	messageID := uuid.New()

	firstRunAfter := time.Now().UTC().Add(30 * time.Minute)
	secondRunAfter := firstRunAfter.Add(30 * time.Minute)
	thirdRunAfter := secondRunAfter.Add(30 * time.Minute)

	if _, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, agentTurnKeyPayload{
		SessionID:  sessionID,
		MessageID:  messageID,
		RetryCount: 0,
	}, &firstRunAfter); err != nil {
		t.Fatalf("enqueue first retry attempt: %v", err)
	}
	if _, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, agentTurnKeyPayload{
		SessionID:  sessionID,
		MessageID:  messageID,
		RetryCount: 1,
	}, &secondRunAfter); err != nil {
		t.Fatalf("enqueue second retry attempt: %v", err)
	}
	if _, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, agentTurnKeyPayload{
		SessionID:  sessionID,
		MessageID:  messageID,
		RetryCount: 2,
	}, &thirdRunAfter); err != nil {
		t.Fatalf("enqueue third retry attempt: %v", err)
	}

	var (
		activeRows int
		rawPayload []byte
		runAfter   time.Time
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM job_queue
		WHERE group_key = $1
		  AND status = 'pending'
	`, AgentTurnGroupKey(sessionID, messageID)).Scan(&activeRows); err != nil {
		t.Fatalf("count pending group rows: %v", err)
	}
	if activeRows != 1 {
		t.Fatalf("pending group rows = %d, want 1", activeRows)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT payload, run_after
		FROM job_queue
		WHERE group_key = $1
		  AND status = 'pending'
	`, AgentTurnGroupKey(sessionID, messageID)).Scan(&rawPayload, &runAfter); err != nil {
		t.Fatalf("load collapsed pending row: %v", err)
	}

	var payload agentTurnKeyPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		t.Fatalf("unmarshal collapsed pending payload: %v", err)
	}
	if payload.RetryCount != 2 {
		t.Fatalf("retry_count = %d, want 2", payload.RetryCount)
	}
	if !runAfter.Equal(thirdRunAfter) {
		t.Fatalf("run_after = %s, want %s", runAfter, thirdRunAfter)
	}
}

func TestChatSummarizeEnqueueDedupesActiveSession(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	sessionID := uuid.New()
	payload := map[string]any{
		"session_id":          sessionID,
		"layer_budget_tokens": 130000,
	}

	firstID, err := worker.Enqueue(context.Background(), nil, "chat_summarize", 60, payload, nil)
	if err != nil {
		t.Fatalf("enqueue first chat_summarize: %v", err)
	}
	secondID, err := worker.Enqueue(context.Background(), nil, "chat_summarize", 60, payload, nil)
	if err != nil {
		t.Fatalf("enqueue duplicate chat_summarize: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("duplicate enqueue id = %s, want %s", secondID, firstID)
	}

	var activeRows int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM job_queue
		WHERE dedupe_key = $1
		  AND status IN ('pending', 'claimed')
	`, "chat_summarize:"+sessionID.String()).Scan(&activeRows); err != nil {
		t.Fatalf("count active deduped rows: %v", err)
	}
	if activeRows != 1 {
		t.Fatalf("active deduped rows = %d, want 1", activeRows)
	}
}

func TestRollupUpdateEnqueueDedupesByOrgAndDate(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	orgID := uuid.New()
	payload := map[string]any{
		"org_id":      orgID,
		"rollup_date": "2026-03-22",
	}

	firstID, err := worker.Enqueue(context.Background(), nil, "rollup_update", 50, payload, nil)
	if err != nil {
		t.Fatalf("enqueue first rollup_update: %v", err)
	}
	secondID, err := worker.Enqueue(context.Background(), nil, "rollup_update", 50, payload, nil)
	if err != nil {
		t.Fatalf("enqueue duplicate rollup_update: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("duplicate enqueue id = %s, want %s", secondID, firstID)
	}

	var activeRows int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM job_queue
		WHERE dedupe_key = $1
		  AND status IN ('pending', 'claimed')
	`, "rollup_update:"+orgID.String()+":2026-03-22").Scan(&activeRows); err != nil {
		t.Fatalf("count active deduped rows: %v", err)
	}
	if activeRows != 1 {
		t.Fatalf("active deduped rows = %d, want 1", activeRows)
	}
}

func TestJobWorkerSkipLockedAcrossTwoWorkers(t *testing.T) {
	pool := testdb.New(t)

	workerA := New(pool, nil, Config{
		WorkerID:             "worker-a",
		PollInterval:         100 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})
	workerB := New(pool, nil, Config{
		WorkerID:             "worker-b",
		PollInterval:         100 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	var (
		mu     sync.Mutex
		counts = make(map[uuid.UUID]int)
	)
	handler := func(_ context.Context, job Job) error {
		mu.Lock()
		counts[job.ID]++
		mu.Unlock()
		time.Sleep(25 * time.Millisecond)
		return nil
	}
	workerA.Register("test.locked", handler)
	workerB.Register("test.locked", handler)

	for i := 0; i < 20; i++ {
		if _, err := workerA.Enqueue(context.Background(), nil, "test.locked", 100, map[string]any{"i": i}, nil); err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(workerA, ctx)
	startWorker(workerB, ctx)
	defer func() {
		cancel()
		_ = workerA.Stop()
		_ = workerB.Stop()
	}()

	waitForDoneJobs(t, pool, 20, 10*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(counts) != 20 {
		t.Fatalf("unique handled jobs = %d, want 20", len(counts))
	}
	for id, n := range counts {
		if n != 1 {
			t.Fatalf("job %s handled %d times, want 1", id, n)
		}
	}
}

func TestJobFailureTransitionsAndStaleRecovery(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	worker.Register("test.fail", func(context.Context, Job) error {
		return errors.New("transient")
	})

	if _, err := worker.Enqueue(context.Background(), nil, "test.fail", 100, nil, nil); err != nil {
		t.Fatalf("enqueue fail job: %v", err)
	}

	jobs, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claimPending failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("claimed %d jobs, want 1", len(jobs))
	}

	if err := worker.executeClaimedJob(context.Background(), jobs[0]); err != nil {
		t.Fatalf("executeClaimedJob transient failure path failed: %v", err)
	}

	var (
		status   string
		attempts int
		runAfter time.Time
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT status, attempts, run_after
		FROM job_queue
		WHERE id = $1
	`, jobs[0].ID).Scan(&status, &attempts, &runAfter); err != nil {
		t.Fatalf("query transient failure job failed: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status after transient failure = %q, want pending", status)
	}
	if attempts != 1 {
		t.Fatalf("attempts after first claim = %d, want 1", attempts)
	}
	if !runAfter.After(time.Now().Add(500 * time.Millisecond)) {
		t.Fatalf("run_after should be backed off into the future, got %s", runAfter)
	}

	var deadID uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO job_queue (job_type, max_attempts, status, attempts, run_after)
		VALUES ('test.dead', 1, 'pending', 0, now())
		RETURNING id
	`).Scan(&deadID); err != nil {
		t.Fatalf("insert dead-letter job failed: %v", err)
	}
	worker.Register("test.dead", func(context.Context, Job) error { return errors.New("permanent") })

	claimed, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claim pending for dead-letter test failed: %v", err)
	}
	if len(claimed) == 0 {
		t.Fatal("expected claimed dead-letter test job")
	}

	found := false
	for _, job := range claimed {
		if job.ID == deadID {
			found = true
			if err := worker.executeClaimedJob(context.Background(), job); err != nil {
				t.Fatalf("executeClaimedJob dead-letter path failed: %v", err)
			}
			break
		}
	}
	if !found {
		t.Fatalf("did not claim expected job %s", deadID)
	}

	if err := pool.QueryRow(context.Background(), `SELECT status FROM job_queue WHERE id = $1`, deadID).Scan(&status); err != nil {
		t.Fatalf("query dead-letter status failed: %v", err)
	}
	if status != "dead_letter" {
		t.Fatalf("status after max-attempt failure = %q, want dead_letter", status)
	}

	var staleID uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO job_queue (job_type, status, claimed_by, claimed_at, attempts, max_attempts, run_after)
		VALUES ('test.stale', 'claimed', 'dead-worker', now() - interval '10 minutes', 1, 3, now())
		RETURNING id
	`).Scan(&staleID); err != nil {
		t.Fatalf("insert stale-claim job failed: %v", err)
	}

	if _, err := worker.RecoverStaleClaims(context.Background()); err != nil {
		t.Fatalf("RecoverStaleClaims failed: %v", err)
	}

	var (
		claimedBy *string
		claimedAt *time.Time
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT status, claimed_by, claimed_at
		FROM job_queue
		WHERE id = $1
	`, staleID).Scan(&status, &claimedBy, &claimedAt); err != nil {
		t.Fatalf("query stale-claim row failed: %v", err)
	}
	if status != "pending" {
		t.Fatalf("stale-claim status = %q, want pending", status)
	}
	if claimedBy != nil || claimedAt != nil {
		t.Fatalf("stale claim fields should be cleared, got claimed_by=%v claimed_at=%v", claimedBy, claimedAt)
	}
}

func TestJobWorkerHeartbeatPreventsStaleClaimRecoveryForRunningJob(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "heartbeat-worker",
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		StaleClaimThreshold:  45 * time.Millisecond,
		CleanupEnqueuePeriod: time.Hour,
	})

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var handled atomic.Int32
	worker.Register("test.heartbeat", func(context.Context, Job) error {
		handled.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return nil
	})

	jobID := testdb.EnqueueJob(t, pool, "test.heartbeat", 100, map[string]any{"n": 1})
	jobs, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claimPending failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(jobs))
	}

	done := make(chan error, 1)
	go func() {
		done <- worker.executeClaimedJob(context.Background(), jobs[0])
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for long-running handler to start")
	}

	time.Sleep(120 * time.Millisecond)

	recovered, err := worker.RecoverStaleClaims(context.Background())
	if err != nil {
		t.Fatalf("RecoverStaleClaims failed: %v", err)
	}
	if recovered != 0 {
		t.Fatalf("recovered rows = %d, want 0 for heartbeat-refreshed claim", recovered)
	}

	var status string
	var claimedBy *string
	if err := pool.QueryRow(context.Background(), `
		SELECT status, claimed_by
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&status, &claimedBy); err != nil {
		t.Fatalf("query running heartbeat job failed: %v", err)
	}
	if status != "claimed" {
		t.Fatalf("running job status = %q, want claimed", status)
	}
	if claimedBy == nil || *claimedBy != "heartbeat-worker" {
		t.Fatalf("running job claimed_by = %v, want heartbeat-worker", claimedBy)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("executeClaimedJob returned error: %v", err)
	}

	if err := pool.QueryRow(context.Background(), `
		SELECT status
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&status); err != nil {
		t.Fatalf("query completed heartbeat job failed: %v", err)
	}
	if status != "done" {
		t.Fatalf("completed heartbeat job status = %q, want done", status)
	}
	if handled.Load() != 1 {
		t.Fatalf("handled count = %d, want 1", handled.Load())
	}
}

func TestJobWorkerRecoverStaleClaimsReleasesForeignAgentTurnClaimsQuickly(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "new-worker",
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	var claimedID uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO job_queue (job_type, status, claimed_by, claimed_at, attempts, max_attempts, run_after, payload)
		VALUES (
			'agent_turn',
			'claimed',
			'dead-worker',
			now() - interval '45 seconds',
			1,
			3,
			now(),
			'{"session_id":"11111111-1111-1111-1111-111111111111","message_id":"22222222-2222-2222-2222-222222222222"}'::jsonb
		)
		RETURNING id
	`).Scan(&claimedID); err != nil {
		t.Fatalf("insert foreign claimed agent_turn failed: %v", err)
	}

	recovered, err := worker.RecoverStaleClaims(context.Background())
	if err != nil {
		t.Fatalf("RecoverStaleClaims failed: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered rows = %d, want 1", recovered)
	}

	var (
		status    string
		claimedBy *string
		claimedAt *time.Time
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT status, claimed_by, claimed_at
		FROM job_queue
		WHERE id = $1
	`, claimedID).Scan(&status, &claimedBy, &claimedAt); err != nil {
		t.Fatalf("query recovered foreign claim failed: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status = %q, want pending", status)
	}
	if claimedBy != nil || claimedAt != nil {
		t.Fatalf("claimed fields should be cleared, got claimed_by=%v claimed_at=%v", claimedBy, claimedAt)
	}
}

func TestJobWorkerReleaseClaimsForWorkerRequeuesGracefulShutdownClaims(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "graceful-stop-worker",
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	jobID := testdb.EnqueueJob(t, pool, "test.release", 100, map[string]any{"n": 1})
	jobs, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claimPending failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(jobs))
	}
	if jobs[0].ID != jobID {
		t.Fatalf("claimed job id = %s, want %s", jobs[0].ID, jobID)
	}

	released, err := worker.releaseClaimsForWorker(context.Background())
	if err != nil {
		t.Fatalf("releaseClaimsForWorker failed: %v", err)
	}
	if released != 1 {
		t.Fatalf("released claims = %d, want 1", released)
	}

	var (
		status    string
		claimedBy *string
		claimedAt *time.Time
		lastError *string
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT status, claimed_by, claimed_at, last_error
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&status, &claimedBy, &claimedAt, &lastError); err != nil {
		t.Fatalf("query released job failed: %v", err)
	}
	if status != "pending" {
		t.Fatalf("released job status = %q, want pending", status)
	}
	if claimedBy != nil || claimedAt != nil {
		t.Fatalf("released claim fields should be cleared, got claimed_by=%v claimed_at=%v", claimedBy, claimedAt)
	}
	if lastError == nil || *lastError != "released on worker shutdown" {
		t.Fatalf("released job last_error = %v, want release marker", lastError)
	}
}

func TestJobWorkerProcessAvailableJobsRecoversStaleClaimsBeforeClaiming(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "recover-before-claim",
		BatchSize:            2,
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	var staleID uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO job_queue (job_type, status, claimed_by, claimed_at, attempts, max_attempts, run_after, payload)
		VALUES ('test.stale', 'claimed', 'dead-worker', now() - interval '10 minutes', 1, 3, now(), '{}'::jsonb)
		RETURNING id
	`).Scan(&staleID); err != nil {
		t.Fatalf("insert stale claim failed: %v", err)
	}

	started := make(chan struct{}, 1)
	worker.Register("test.stale", func(context.Context, Job) error { return nil })
	worker.Register("test.fresh", func(context.Context, Job) error {
		select {
		case started <- struct{}{}:
		default:
		}
		return nil
	})

	if _, err := worker.Enqueue(context.Background(), nil, "test.fresh", 100, map[string]any{"n": 1}, nil); err != nil {
		t.Fatalf("enqueue fresh job failed: %v", err)
	}

	if err := worker.processAvailableJobs(context.Background()); err != nil {
		t.Fatalf("processAvailableJobs failed: %v", err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("fresh job did not start after stale claim recovery")
	}

	var status string
	if err := pool.QueryRow(context.Background(), `
		SELECT status
		FROM job_queue
		WHERE id = $1
	`, staleID).Scan(&status); err != nil {
		t.Fatalf("query stale claim status failed: %v", err)
	}
	if status != "pending" && status != "done" {
		t.Fatalf("stale claim status = %q, want pending or done", status)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsClearsClaimedClosedSessions(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-closed-session",
		DisplayName: "Purge Closed Session",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).Close(ctx, session.ID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, claimed_by, claimed_at, payload, run_after)
		VALUES ('agent_turn', 'claimed', 'dead-worker', now(), $1::jsonb, now())
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s"}`, session.ID, uuid.New())).Scan(&jobID); err != nil {
		t.Fatalf("insert claimed closed-session job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 1 {
		t.Fatalf("purged jobs = %d, want 1", purged)
	}

	var (
		status    string
		claimedBy *string
		claimedAt *time.Time
		lastError *string
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, claimed_by, claimed_at, last_error
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&status, &claimedBy, &claimedAt, &lastError); err != nil {
		t.Fatalf("query purged job: %v", err)
	}
	if status != "dead_letter" {
		t.Fatalf("status after purge = %q, want dead_letter", status)
	}
	if claimedBy != nil || claimedAt != nil {
		t.Fatalf("claimed fields after purge = claimed_by:%v claimed_at:%v, want nil", claimedBy, claimedAt)
	}
	if lastError == nil || *lastError != "purged at worker startup: session closed" {
		t.Fatalf("last_error = %v, want session closed purge message", lastError)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsCollapsesSupersededBootstrapContinuations(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-bootstrap-duplicates",
		DisplayName: "Purge Bootstrap Duplicates",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "bootstrap-project",
		DisplayName:    "Bootstrap Project",
		Description:    "Project for bootstrap continuation cleanup",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	cancelledTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   uuid.New(),
		Status:         "cancelled",
	})
	if err != nil {
		t.Fatalf("create cancelled turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_session
		SET current_turn_id = $2
		WHERE id = $1
	`, session.ID, cancelledTurn.ID); err != nil {
		t.Fatalf("set current_turn_id: %v", err)
	}

	messageRepo := repo.NewChatMessageRepo(pool)
	messageIDs := make([]uuid.UUID, 0, 3)
	for i := 0; i < 3; i++ {
		msg, err := messageRepo.Create(ctx, repo.ChatMessage{
			SessionID: session.ID,
			Role:      "user",
			Content:   fmt.Sprintf("Bootstrap continuation %d", i+1),
			Status:    "pending",
			Metadata:  json.RawMessage(`{"source":"project_bootstrap"}`),
		})
		if err != nil {
			t.Fatalf("create bootstrap continuation message %d: %v", i+1, err)
		}
		messageIDs = append(messageIDs, msg.ID)
	}

	jobIDs := make([]uuid.UUID, 0, len(messageIDs))
	base := time.Now().UTC().Add(-time.Minute)
	for i, messageID := range messageIDs {
		var jobID uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO job_queue (job_type, status, payload, run_after, priority)
			VALUES ('agent_turn', 'pending', $1::jsonb, $2, 70)
			RETURNING id
		`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s"}`, session.ID, messageID), base.Add(time.Duration(i)*time.Second)).Scan(&jobID); err != nil {
			t.Fatalf("insert bootstrap continuation job %d: %v", i+1, err)
		}
		jobIDs = append(jobIDs, jobID)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 2 {
		t.Fatalf("purged jobs = %d, want 2 superseded bootstrap continuations", purged)
	}

	for i := 0; i < len(jobIDs)-1; i++ {
		var status string
		var lastError *string
		if err := pool.QueryRow(ctx, `
			SELECT status, last_error
			FROM job_queue
			WHERE id = $1
		`, jobIDs[i]).Scan(&status, &lastError); err != nil {
			t.Fatalf("query purged bootstrap continuation %d: %v", i+1, err)
		}
		if status != "dead_letter" {
			t.Fatalf("bootstrap continuation %d status = %q, want dead_letter", i+1, status)
		}
		if lastError == nil || *lastError != "purged at worker startup: superseded bootstrap continuation" {
			t.Fatalf("bootstrap continuation %d last_error = %v, want superseded-bootstrap marker", i+1, lastError)
		}
	}

	var newestStatus string
	if err := pool.QueryRow(ctx, `
		SELECT status
		FROM job_queue
		WHERE id = $1
	`, jobIDs[len(jobIDs)-1]).Scan(&newestStatus); err != nil {
		t.Fatalf("query newest bootstrap continuation: %v", err)
	}
	if newestStatus != "pending" {
		t.Fatalf("newest bootstrap continuation status = %q, want pending", newestStatus)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsKeepsLiveSupervisorRecoveryTurn(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-live-supervisor-recovery",
		DisplayName: "Purge Live Supervisor Recovery",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-pending-turns-project",
		DisplayName:    "Requeue Pending Turns Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Pending task turn",
		WorkStatus:     "draft",
		BlocksScope:    "task",
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Resume the stranded task execution.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create supervisor message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, message.ID)).Scan(&jobID); err != nil {
		t.Fatalf("insert supervisor recovery job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 0 {
		t.Fatalf("purged jobs = %d, want 0", purged)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("query supervisor recovery job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("supervisor recovery job status = %q, want pending", status)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsKeepsSupervisorRetryJob(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-supervisor-retry-job",
		DisplayName: "Purge Supervisor Retry Job",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Resume the stranded task execution.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create supervisor message: %v", err)
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority)
		VALUES ('agent_turn', 'pending', $1::jsonb, now() + interval '15 minutes', 70)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1}`, session.ID, message.ID)).Scan(&jobID); err != nil {
		t.Fatalf("insert supervisor retry job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 0 {
		t.Fatalf("purged jobs = %d, want 0", purged)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("query supervisor retry job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("supervisor retry job status = %q, want pending", status)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsKeepsSupervisorRecoveryJobForActiveExecutionWithoutTurn(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-supervisor-recovery-active-execution-without-turn",
		DisplayName: "Purge Supervisor Recovery Active Execution Without Turn",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "purge-supervisor-recovery-active-execution-without-turn-project",
		DisplayName:    "Purge Supervisor Recovery Active Execution Without Turn Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "purge-supervisor-recovery-active-execution-without-turn-template",
		DisplayName:    "Purge Supervisor Recovery Active Execution Without Turn Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Keep supervisor recovery job for active execution without live turn",
		WorkStatus:      "review",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	}); err != nil {
		t.Fatalf("create active execution: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create supervisor message: %v", err)
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, message.ID)).Scan(&jobID); err != nil {
		t.Fatalf("insert supervisor recovery job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 0 {
		t.Fatalf("purged jobs = %d, want 0", purged)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("query supervisor recovery job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("supervisor recovery job status = %q, want pending", status)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsKeepsProjectSupervisorPMRecoveryJob(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-project-supervisor-pm-recovery",
		DisplayName: "Purge Project Supervisor PM Recovery",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "purge-project-supervisor-pm-recovery-project",
		DisplayName:    "Purge Project Supervisor PM Recovery Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: inspect stranded execution and use flow.recovery_decision",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","supervisor_pm_recovery":true,"recovery_disposition":"await_pm_decision","flow_node_execution_id":"11111111-1111-1111-1111-111111111111"}`),
	})
	if err != nil {
		t.Fatalf("create project supervisor message: %v", err)
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, message.ID)).Scan(&jobID); err != nil {
		t.Fatalf("insert project supervisor recovery job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 0 {
		t.Fatalf("purged jobs = %d, want 0", purged)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("query project supervisor recovery job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("project supervisor recovery job status = %q, want pending", status)
	}
}

func TestJobWorkerRejitterPendingRateLimitedAgentTurns(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "rejitter-pending-rate-limited-agent-turns",
		DisplayName: "Rejitter Pending Rate Limited Agent Turns",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "assistant",
		Content:   "[Rate limited, retrying in 15m...]",
		Status:    "final",
	})
	if err != nil {
		t.Fatalf("create rate limit message: %v", err)
	}

	originalRunAfter := time.Now().UTC().Add(15 * time.Minute).Truncate(time.Second)
	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority)
		VALUES ('agent_turn', 'pending', $1::jsonb, $2, 70)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1}`, session.ID, message.ID), originalRunAfter).Scan(&jobID); err != nil {
		t.Fatalf("insert pending retry job: %v", err)
	}

	rejittered, err := worker.RejitterPendingRateLimitedAgentTurns(ctx)
	if err != nil {
		t.Fatalf("RejitterPendingRateLimitedAgentTurns: %v", err)
	}
	if rejittered != 1 {
		t.Fatalf("rejittered jobs = %d, want 1", rejittered)
	}

	var (
		runAfter      time.Time
		jitterApplied bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT run_after,
		       COALESCE((payload->>'rate_limit_jitter_applied')::boolean, false)
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&runAfter, &jitterApplied); err != nil {
		t.Fatalf("query rejittered job: %v", err)
	}
	wantRunAfter := rejitteredRateLimitedRunAfter(worker.clock.Now().UTC(), originalRunAfter, session.ID, message.ID, 1, false)
	if !runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", runAfter, wantRunAfter)
	}
	if !jitterApplied {
		t.Fatal("expected rate_limit_jitter_applied flag to be set")
	}

	rejittered, err = worker.RejitterPendingRateLimitedAgentTurns(ctx)
	if err != nil {
		t.Fatalf("RejitterPendingRateLimitedAgentTurns second pass: %v", err)
	}
	if rejittered != 0 {
		t.Fatalf("rejittered jobs on second pass = %d, want 0", rejittered)
	}
}

func TestJobWorkerRejitterPendingRateLimitedAgentTurnsClampsAlreadyJitteredOversizedRunAfter(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "rejitter-pending-rate-limited-agent-turns-clamp-oversized",
		DisplayName: "Rejitter Pending Rate Limited Agent Turns Clamp Oversized",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "assistant",
		Content:   "[Rate limited, retrying in 42h...]",
		Status:    "final",
	})
	if err != nil {
		t.Fatalf("create rate limit message: %v", err)
	}

	originalRunAfter := time.Now().UTC().Add(42 * time.Hour).Truncate(time.Second)
	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority)
		VALUES ('agent_turn', 'pending', $1::jsonb, $2, 70)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1,"rate_limit_jitter_applied":true}`, session.ID, message.ID), originalRunAfter).Scan(&jobID); err != nil {
		t.Fatalf("insert pending oversized retry job: %v", err)
	}

	rejittered, err := worker.RejitterPendingRateLimitedAgentTurns(ctx)
	if err != nil {
		t.Fatalf("RejitterPendingRateLimitedAgentTurns: %v", err)
	}
	if rejittered != 1 {
		t.Fatalf("rejittered jobs = %d, want 1", rejittered)
	}

	var runAfter time.Time
	if err := pool.QueryRow(ctx, `
		SELECT run_after
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&runAfter); err != nil {
		t.Fatalf("query clamped job: %v", err)
	}
	maxAllowed := worker.clock.Now().UTC().Add(agentTurnRateLimitBackoffCap + time.Minute)
	if runAfter.After(maxAllowed) {
		t.Fatalf("run_after = %s, want clamped near <= %s", runAfter, maxAllowed)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsCollapsesSupersededProjectTaskContinuations(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-project-task-continuations",
		DisplayName: "Purge Project Task Continuations",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	messageIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, messageID := range messageIDs {
		runAfter := time.Now().UTC().Add(time.Duration(i+1) * time.Hour)
		createdAt := time.Now().UTC().Add(time.Duration(i) * time.Minute)
		if _, err := pool.Exec(ctx, `
			INSERT INTO chat_message (id, session_id, sequence_number, role, content, status, created_at, updated_at)
			VALUES ($1, $2, $3, 'system', $4, 'final', $5, $5)
		`, messageID, session.ID, i+1, fmt.Sprintf("[Continuation %d]", i+1), createdAt); err != nil {
			t.Fatalf("insert continuation message %d: %v", i+1, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO job_queue (job_type, status, payload, run_after, priority, created_at, updated_at, group_key, dedupe_key)
			VALUES ('agent_turn', 'pending', $1::jsonb, $2, 70, $3, $3, $4, $5)
		`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1}`, session.ID, messageID), runAfter, createdAt,
			fmt.Sprintf("agent_turn:%s:%s", session.ID, messageID),
			fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, messageID, 1),
		); err != nil {
			t.Fatalf("insert pending continuation job %d: %v", i+1, err)
		}
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 2 {
		t.Fatalf("purged jobs = %d, want 2", purged)
	}

	rows, err := pool.Query(ctx, `
		SELECT status, COALESCE(last_error, '')
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at ASC
	`, session.ID)
	if err != nil {
		t.Fatalf("query continuation jobs: %v", err)
	}
	defer rows.Close()

	var statuses []string
	var errors []string
	for rows.Next() {
		var status, lastError string
		if err := rows.Scan(&status, &lastError); err != nil {
			t.Fatalf("scan continuation jobs: %v", err)
		}
		statuses = append(statuses, status)
		errors = append(errors, lastError)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate continuation jobs: %v", err)
	}
	if len(statuses) != 3 {
		t.Fatalf("continuation jobs = %d, want 3", len(statuses))
	}
	if statuses[0] != "dead_letter" || errors[0] != "purged stale project_task continuation" {
		t.Fatalf("oldest job = (%q, %q), want dead_letter/purged stale project_task continuation", statuses[0], errors[0])
	}
	if statuses[1] != "dead_letter" || errors[1] != "purged stale project_task continuation" {
		t.Fatalf("middle job = (%q, %q), want dead_letter/purged stale project_task continuation", statuses[1], errors[1])
	}
	if statuses[2] != "pending" {
		t.Fatalf("newest job status = %q, want pending", statuses[2])
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsKeepsLiveProjectTaskContinuationFromExecutionMetadata(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-live-project-task-continuation-live-owner",
		DisplayName: "Purge Live Project Task Continuation Live Owner",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Continuation Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You continue task work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "purge-live-project-task-continuation-live-owner-project",
		DisplayName:    "Purge Live Project Task Continuation Live Owner Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "purge-live-project-task-continuation-live-owner-template",
		DisplayName:    "Purge Live Project Task Continuation Live Owner Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Pending task continuation",
		WorkStatus:     "in_progress",
		BlocksScope:    "task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rootMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue from the compressed context.",
		Status:    "final",
	})
	if err != nil {
		t.Fatalf("create root message: %v", err)
	}
	rootTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &rootMessage.ID,
	})
	if err != nil {
		t.Fatalf("create root turn: %v", err)
	}
	continuationTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     2,
		RespondingType: "agent",
		RespondingID:   agent.ID,
		Status:         "pending",
	})
	if err != nil {
		t.Fatalf("create continuation turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}
	metadata := repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{TurnID: &continuationTurn.ID})
	if _, err := repo.NewFlowNodeExecutionRepo(pool).UpdateMetadata(ctx, execution.ID, metadata); err != nil {
		t.Fatalf("set live turn metadata: %v", err)
	}

	messageIDs := []uuid.UUID{uuid.New(), uuid.New()}
	for i, messageID := range messageIDs {
		runAfter := time.Now().UTC().Add(time.Duration(i+1) * time.Hour)
		createdAt := time.Now().UTC().Add(time.Duration(i) * time.Minute)
		if _, err := pool.Exec(ctx, `
			INSERT INTO chat_message (id, session_id, sequence_number, role, content, status, created_at, updated_at)
			VALUES ($1, $2, $3, 'system', $4, 'final', $5, $5)
		`, messageID, session.ID, i+2, fmt.Sprintf("[Continuation %d]", i+1), createdAt); err != nil {
			t.Fatalf("insert continuation message %d: %v", i+1, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO job_queue (job_type, status, payload, run_after, priority, created_at, updated_at, group_key, dedupe_key)
			VALUES ('agent_turn', 'pending', $1::jsonb, $2, 70, $3, $3, $4, $5)
		`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1}`, session.ID, messageID), runAfter, createdAt,
			fmt.Sprintf("agent_turn:%s:%s", session.ID, messageID),
			fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, messageID, 1),
		); err != nil {
			t.Fatalf("insert pending continuation job %d: %v", i+1, err)
		}
	}
	if rootTurn.TriggerMessageID == nil || *rootTurn.TriggerMessageID != rootMessage.ID {
		t.Fatalf("root turn trigger_message_id = %v, want %s", rootTurn.TriggerMessageID, rootMessage.ID)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 0 {
		t.Fatalf("purged jobs = %d, want 0", purged)
	}

	var pendingJobs int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status = 'pending'
		  AND (payload->>'session_id')::uuid = $1
	`, session.ID).Scan(&pendingJobs); err != nil {
		t.Fatalf("count pending continuation jobs: %v", err)
	}
	if pendingJobs != 2 {
		t.Fatalf("pending continuation jobs = %d, want 2", pendingJobs)
	}
}

func TestJobWorkerRequeueStrandedSupervisorRecoveryTurns(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-stranded-supervisor-recovery",
		DisplayName: "Requeue Stranded Supervisor Recovery",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-pending-turns-project",
		DisplayName:    "Requeue Pending Turns Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Pending task turn",
		WorkStatus:     "draft",
		BlocksScope:    "task",
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Resume the stranded task execution.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create supervisor message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	requeued, err := worker.RequeueStrandedSupervisorRecoveryTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueStrandedSupervisorRecoveryTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued turns = %d, want 1", requeued)
	}

	var (
		status    string
		messageID uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, (payload->>'message_id')::uuid
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &messageID); err != nil {
		t.Fatalf("query requeued supervisor recovery job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if messageID != message.ID {
		t.Fatalf("requeued message_id = %s, want %s", messageID, message.ID)
	}
}

func TestJobWorkerRequeueStrandedSupervisorRecoveryTurnsUsesExecutionMetadataLiveTurn(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-stranded-supervisor-live-owner",
		DisplayName: "Requeue Stranded Supervisor Live Owner",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-stranded-supervisor-live-owner-project",
		DisplayName:    "Requeue Stranded Supervisor Live Owner Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-stranded-supervisor-live-owner-template",
		DisplayName:    "Requeue Stranded Supervisor Live Owner Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover stranded supervisor turn from execution owner metadata",
		WorkStatus:      "review",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create supervisor kickoff message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create pending recovery turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}
	metadata := repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{TurnID: &turn.ID})
	if _, err := repo.NewFlowNodeExecutionRepo(pool).UpdateMetadata(ctx, execution.ID, metadata); err != nil {
		t.Fatalf("set live turn metadata: %v", err)
	}

	requeued, err := worker.RequeueStrandedSupervisorRecoveryTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueStrandedSupervisorRecoveryTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued turns = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, (payload->>'message_id')::uuid, (payload->>'session_id')::uuid
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID); err != nil {
		t.Fatalf("query requeued supervisor recovery job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued message_id = %s, want %s", requeuedMsgID, message.ID)
	}
}

func TestJobWorkerRequeueStrandedSupervisorRecoveryTurnsSkipsBlockedTasks(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-stranded-supervisor-blocked-task",
		DisplayName: "Requeue Stranded Supervisor Blocked Task",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Blocked Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "Recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-stranded-supervisor-blocked-task-project",
		DisplayName:    "Requeue Stranded Supervisor Blocked Task Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-stranded-supervisor-blocked-task-template",
		DisplayName:    "Requeue Stranded Supervisor Blocked Task Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         project.ID,
		Title:             "Blocked task",
		WorkStatus:        "blocked",
		BlocksScope:       "task",
		FlowTemplateID:    &template.ID,
		CurrentFlowNodeID: &flowNode.ID,
		CreatedByType:     "system",
		CreatedByID:       &agent.ID,
		AssignedAgentID:   &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create supervisor message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("update current turn: %v", err)
	}

	requeued, err := worker.RequeueStrandedSupervisorRecoveryTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueStrandedSupervisorRecoveryTurns: %v", err)
	}
	if requeued != 0 {
		t.Fatalf("requeued turns = %d, want 0 for blocked task", requeued)
	}

	var pendingJobs int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status = 'pending'
		  AND (payload->>'session_id')::uuid = $1
	`, session.ID).Scan(&pendingJobs); err != nil {
		t.Fatalf("count pending jobs: %v", err)
	}
	if pendingJobs != 0 {
		t.Fatalf("pending jobs = %d, want 0", pendingJobs)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsRemovesTerminalMessageAttemptDispatches(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-terminal-message-attempt-dispatch",
		DisplayName: "Purge Terminal Message Attempt Dispatch",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Terminal Dispatch Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You handle stale dispatch cleanup.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "claim-skip-duplicate-live-message-attempt-project",
		DisplayName:    "Claim Skip Duplicate Live Message Attempt Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "claim-skip-duplicate-live-message-attempt-template",
		DisplayName:    "Claim Skip Duplicate Live Message Attempt Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         project.ID,
		Title:             "Claim skip duplicate live message attempt",
		WorkStatus:        "in_progress",
		BlocksScope:       "task",
		FlowTemplateID:    &template.ID,
		CurrentFlowNodeID: &flowNode.ID,
		CreatedByType:     "system",
		CreatedByID:       &agent.ID,
		AssignedAgentID:   &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "resume task",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create message: %v", err)
	}

	if _, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &message.ID,
		RetryCount:       1,
	}); err != nil {
		t.Fatalf("Create completed turn: %v", err)
	}

	liveMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "current live task turn",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create live message: %v", err)
	}
	liveTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       2,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &liveMessage.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("Create live turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &liveTurn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	var staleJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1}`, session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, message.ID, 1),
	).Scan(&staleJobID); err != nil {
		t.Fatalf("insert stale job: %v", err)
	}

	var liveJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":2}`, session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, message.ID, 2),
	).Scan(&liveJobID); err != nil {
		t.Fatalf("insert live job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 1 {
		t.Fatalf("purged jobs = %d, want 1", purged)
	}

	var staleStatus, liveStatus string
	var staleError, liveError *string
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error
		FROM job_queue
		WHERE id = $1
	`, staleJobID).Scan(&staleStatus, &staleError); err != nil {
		t.Fatalf("query stale job: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error
		FROM job_queue
		WHERE id = $1
	`, liveJobID).Scan(&liveStatus, &liveError); err != nil {
		t.Fatalf("query live job: %v", err)
	}
	staleErrorValue := "<nil>"
	if staleError != nil {
		staleErrorValue = *staleError
	}
	if staleStatus != "dead_letter" || staleError == nil || *staleError != "purged stale terminal message-attempt dispatch" {
		t.Fatalf("stale job = (%q, %q), want dead_letter/purged stale terminal message-attempt dispatch", staleStatus, staleErrorValue)
	}
	if liveStatus != "pending" || liveError != nil {
		t.Fatalf("live job = (%q, %v), want pending/<nil>", liveStatus, liveError)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsRemovesDuplicateLiveMessageAttemptDispatches(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-live-message-attempt-dispatch",
		DisplayName: "Purge Live Message Attempt Dispatch",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Live Dispatch Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You handle duplicate live dispatch cleanup.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "claim-skip-duplicate-live-message-attempt-project",
		DisplayName:    "Claim Skip Duplicate Live Message Attempt Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "claim-skip-duplicate-live-message-attempt-template",
		DisplayName:    "Claim Skip Duplicate Live Message Attempt Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         project.ID,
		Title:             "Claim skip duplicate live message attempt",
		WorkStatus:        "in_progress",
		BlocksScope:       "task",
		FlowTemplateID:    &template.ID,
		CurrentFlowNodeID: &flowNode.ID,
		CreatedByType:     "system",
		CreatedByID:       &agent.ID,
		AssignedAgentID:   &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue task",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create message: %v", err)
	}

	liveTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("Create live turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &liveTurn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	var staleJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, message.ID, 0),
	).Scan(&staleJobID); err != nil {
		t.Fatalf("insert stale job: %v", err)
	}

	otherMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "another task prompt",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create other message: %v", err)
	}
	var otherJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, otherMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, otherMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, otherMessage.ID, 0),
	).Scan(&otherJobID); err != nil {
		t.Fatalf("insert other job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 1 {
		t.Fatalf("purged jobs = %d, want 1", purged)
	}

	var staleStatus, otherStatus string
	var staleError, otherError *string
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error
		FROM job_queue
		WHERE id = $1
	`, staleJobID).Scan(&staleStatus, &staleError); err != nil {
		t.Fatalf("query stale job: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error
		FROM job_queue
		WHERE id = $1
	`, otherJobID).Scan(&otherStatus, &otherError); err != nil {
		t.Fatalf("query other job: %v", err)
	}
	staleErrorValue := "<nil>"
	if staleError != nil {
		staleErrorValue = *staleError
	}
	if staleStatus != "dead_letter" || staleError == nil || *staleError != "purged duplicate live message-attempt dispatch" {
		t.Fatalf("stale job = (%q, %q), want dead_letter/purged duplicate live message-attempt dispatch", staleStatus, staleErrorValue)
	}
	if otherStatus != "pending" || otherError != nil {
		t.Fatalf("other job = (%q, %v), want pending/<nil>", otherStatus, otherError)
	}
}

func TestJobWorkerClaimPendingAgentTurnsSkipsDuplicateLiveMessageAttemptDispatch(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-skip-duplicate-live-message-attempt",
		DisplayName: "Claim Skip Duplicate Live Message Attempt",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Claim Guard Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You handle duplicate claim cleanup.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "claim-skip-duplicate-live-message-attempt-project",
		DisplayName:    "Claim Skip Duplicate Live Message Attempt Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "claim-skip-duplicate-live-message-attempt-template",
		DisplayName:    "Claim Skip Duplicate Live Message Attempt Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         project.ID,
		Title:             "Claim skip duplicate live message attempt",
		WorkStatus:        "in_progress",
		BlocksScope:       "task",
		FlowTemplateID:    &template.ID,
		CurrentFlowNodeID: &flowNode.ID,
		CreatedByType:     "system",
		CreatedByID:       &agent.ID,
		AssignedAgentID:   &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	duplicateMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "duplicate task continuation",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create duplicate message: %v", err)
	}
	liveTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &duplicateMessage.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("Create live turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &liveTurn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}
	runID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO run (
			id,
			organization_id,
			project_id,
			task_id,
			flow_node_id,
			session_id,
			turn_id,
			principal_type,
			principal_id,
			status,
			trigger_type,
			version,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'agent', $8, 'in_progress', 'scheduler', 1, '{}'::jsonb)
	`, runID, org.ID, project.ID, taskRecord.ID, flowNode.ID, session.ID, liveTurn.ID, agent.ID); err != nil {
		t.Fatalf("insert live run: %v", err)
	}
	metadata := repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{RunID: &runID, TurnID: &liveTurn.ID})
	if _, err := repo.NewFlowNodeExecutionRepo(pool).UpdateMetadata(ctx, execution.ID, metadata); err != nil {
		t.Fatalf("set live owner metadata: %v", err)
	}
	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "claim-skip-duplicate-live-message-attempt-provider",
		DisplayName: "Claim Skip Duplicate Live Message Attempt Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	if _, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:  org.ID,
		ModelProviderID: provider.ID,
		Status:          "in_flight",
		ModelName:       "test-model",
		AgentID:         &agent.ID,
		SessionID:       &session.ID,
		TurnID:          &liveTurn.ID,
		RunID:           &runID,
	}); err != nil {
		t.Fatalf("create live model invocation: %v", err)
	}

	otherMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "fresh task continuation",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create other message: %v", err)
	}

	var duplicateJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, duplicateMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, duplicateMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, duplicateMessage.ID, 0),
	).Scan(&duplicateJobID); err != nil {
		t.Fatalf("insert duplicate job: %v", err)
	}
	var freshJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, otherMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, otherMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, otherMessage.ID, 0),
	).Scan(&freshJobID); err != nil {
		t.Fatalf("insert fresh job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 10)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed jobs = %d, want 0 while session has live turn", len(claimed))
	}

	var duplicateStatus, freshStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, duplicateJobID).Scan(&duplicateStatus); err != nil {
		t.Fatalf("query duplicate job: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, freshJobID).Scan(&freshStatus); err != nil {
		t.Fatalf("query fresh job: %v", err)
	}
	if duplicateStatus != "pending" {
		t.Fatalf("duplicate job status = %q, want pending", duplicateStatus)
	}
	if freshStatus != "pending" {
		t.Fatalf("fresh job status = %q, want pending", freshStatus)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsPurgesDuplicateLiveExecutionDispatchAcrossMessages(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-duplicate-live-execution-dispatch",
		DisplayName: "Purge Duplicate Live Execution Dispatch",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Duplicate Execution Guard Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You handle duplicate execution dispatch cleanup.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	executionID := uuid.New()
	activeMetadata, err := json.Marshal(map[string]any{
		"source":                 "task_queue_processor",
		"run_id":                 uuid.NewString(),
		"flow_node_execution_id": executionID.String(),
	})
	if err != nil {
		t.Fatalf("marshal active metadata: %v", err)
	}
	activeMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "start review on task",
		Status:    "pending",
		Metadata:  activeMetadata,
	})
	if err != nil {
		t.Fatalf("Create active message: %v", err)
	}
	liveTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &activeMessage.ID,
		RetryCount:       1,
	})
	if err != nil {
		t.Fatalf("Create live turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &liveTurn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	duplicateMetadata, err := json.Marshal(map[string]any{
		"source":                 "task_queue_processor",
		"run_id":                 uuid.NewString(),
		"flow_node_execution_id": executionID.String(),
	})
	if err != nil {
		t.Fatalf("marshal duplicate metadata: %v", err)
	}
	duplicateMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "start review on task duplicate",
		Status:    "pending",
		Metadata:  duplicateMetadata,
	})
	if err != nil {
		t.Fatalf("Create duplicate message: %v", err)
	}

	otherExecutionID := uuid.New()
	otherMetadata, err := json.Marshal(map[string]any{
		"source":                 "task_queue_processor",
		"run_id":                 uuid.NewString(),
		"flow_node_execution_id": otherExecutionID.String(),
	})
	if err != nil {
		t.Fatalf("marshal other metadata: %v", err)
	}
	otherMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "start review on task other execution",
		Status:    "pending",
		Metadata:  otherMetadata,
	})
	if err != nil {
		t.Fatalf("Create other message: %v", err)
	}

	var duplicateJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1,"flow_node_execution_id":"%s"}`, session.ID, duplicateMessage.ID, executionID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, duplicateMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, duplicateMessage.ID, 1),
	).Scan(&duplicateJobID); err != nil {
		t.Fatalf("insert duplicate execution job: %v", err)
	}
	var otherJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1,"flow_node_execution_id":"%s"}`, session.ID, otherMessage.ID, otherExecutionID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, otherMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, otherMessage.ID, 1),
	).Scan(&otherJobID); err != nil {
		t.Fatalf("insert other execution job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 1 {
		t.Fatalf("purged jobs = %d, want 1", purged)
	}

	var duplicateStatus, otherStatus string
	var duplicateError, otherError *string
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error
		FROM job_queue
		WHERE id = $1
	`, duplicateJobID).Scan(&duplicateStatus, &duplicateError); err != nil {
		t.Fatalf("query duplicate job: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error
		FROM job_queue
		WHERE id = $1
	`, otherJobID).Scan(&otherStatus, &otherError); err != nil {
		t.Fatalf("query other job: %v", err)
	}
	duplicateErrorValue := "<nil>"
	if duplicateError != nil {
		duplicateErrorValue = *duplicateError
	}
	if duplicateStatus != "dead_letter" || duplicateError == nil || *duplicateError != "purged duplicate live message-attempt dispatch" {
		t.Fatalf("duplicate job = (%q, %q), want dead_letter/purged duplicate live message-attempt dispatch", duplicateStatus, duplicateErrorValue)
	}
	if otherStatus != "pending" || otherError != nil {
		t.Fatalf("other job = (%q, %v), want pending/<nil>", otherStatus, otherError)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsKeepsClaimedLiveMessageAttemptDispatch(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID: "test-worker",
	})

	ctx := context.Background()
	sessionRepo := repo.NewChatSessionRepo(pool)
	messageRepo := repo.NewChatMessageRepo(pool)
	turnRepo := repo.NewChatTurnRepo(pool)
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-claimed-live-message-attempt",
		DisplayName: "Purge Claimed Live Message Attempt",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Claimed Dispatch Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You execute task turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	session, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Status:         "active",
		Mode:           "async",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	message, err := messageRepo.Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "resume active task",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create message: %v", err)
	}
	liveTurn, err := turnRepo.Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("Create live turn: %v", err)
	}
	if _, err := sessionRepo.UpdateCurrentTurn(ctx, session.ID, &liveTurn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	var claimedJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, claimed_by, claimed_at, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'claimed', 'test-worker', now(), $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, message.ID, 0),
	).Scan(&claimedJobID); err != nil {
		t.Fatalf("insert claimed job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}

	var status string
	var lastError *string
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error
		FROM job_queue
		WHERE id = $1
	`, claimedJobID).Scan(&status, &lastError); err != nil {
		t.Fatalf("query claimed job: %v", err)
	}
	if status != "claimed" {
		t.Fatalf("claimed job status = %q, want claimed (purged=%d)", status, purged)
	}
	if lastError != nil {
		t.Fatalf("claimed job last_error = %v, want nil", *lastError)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsPurgesDuplicateClaimedLiveExecutionDispatchAcrossMessages(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID: "test-worker",
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-duplicate-claimed-live-execution-dispatch",
		DisplayName: "Purge Duplicate Claimed Live Execution Dispatch",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Claimed Duplicate Guard Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You clean up duplicate claimed dispatches.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	executionID := uuid.New()
	oldMetadata, err := json.Marshal(map[string]any{
		"source":                 "task_queue_processor",
		"run_id":                 uuid.NewString(),
		"flow_node_execution_id": executionID.String(),
	})
	if err != nil {
		t.Fatalf("marshal old metadata: %v", err)
	}
	oldMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "start review on task",
		Status:    "pending",
		Metadata:  oldMetadata,
	})
	if err != nil {
		t.Fatalf("Create old message: %v", err)
	}

	newMetadata, err := json.Marshal(map[string]any{
		"source":                 "task_review_action",
		"run_id":                 uuid.NewString(),
		"flow_node_execution_id": executionID.String(),
		"synthetic_user_message": true,
	})
	if err != nil {
		t.Fatalf("marshal new metadata: %v", err)
	}
	newMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Review only. Inspect the current deliverables and use flow.review_decision.",
		Status:    "pending",
		Metadata:  newMetadata,
	})
	if err != nil {
		t.Fatalf("Create new message: %v", err)
	}

	liveTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &newMessage.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("Create live turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &liveTurn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	var oldJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, claimed_by, claimed_at, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'claimed', 'test-worker', now(), $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0,"flow_node_execution_id":"%s"}`, session.ID, oldMessage.ID, executionID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, oldMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, oldMessage.ID, 0),
	).Scan(&oldJobID); err != nil {
		t.Fatalf("insert old claimed job: %v", err)
	}
	var newJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, claimed_by, claimed_at, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'claimed', 'test-worker', now(), $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0,"flow_node_execution_id":"%s"}`, session.ID, newMessage.ID, executionID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, newMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, newMessage.ID, 0),
	).Scan(&newJobID); err != nil {
		t.Fatalf("insert new claimed job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged < 1 {
		t.Fatalf("purged jobs = %d, want at least 1", purged)
	}

	var oldStatus string
	var oldError *string
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error
		FROM job_queue
		WHERE id = $1
	`, oldJobID).Scan(&oldStatus, &oldError); err != nil {
		t.Fatalf("query old claimed job: %v", err)
	}
	if oldStatus != "dead_letter" || oldError == nil || *oldError != "purged duplicate live message-attempt dispatch" {
		value := "<nil>"
		if oldError != nil {
			value = *oldError
		}
		t.Fatalf("old claimed job = (%q, %q), want dead_letter/purged duplicate live message-attempt dispatch", oldStatus, value)
	}

	var newStatus string
	var newError *string
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error
		FROM job_queue
		WHERE id = $1
	`, newJobID).Scan(&newStatus, &newError); err != nil {
		t.Fatalf("query new claimed job: %v", err)
	}
	if newStatus != "claimed" {
		t.Fatalf("new claimed job status = %q, want claimed", newStatus)
	}
	if newError != nil {
		t.Fatalf("new claimed job last_error = %v, want nil", *newError)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsKeepsSinglePendingLiveMessageAttemptDispatch(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID: "test-worker",
	})

	ctx := context.Background()
	sessionRepo := repo.NewChatSessionRepo(pool)
	messageRepo := repo.NewChatMessageRepo(pool)
	turnRepo := repo.NewChatTurnRepo(pool)
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-pending-live-message-attempt",
		DisplayName: "Purge Pending Live Message Attempt",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Pending Dispatch Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You execute task turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	session, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Status:         "active",
		Mode:           "async",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	message, err := messageRepo.Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "resume active task",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create message: %v", err)
	}
	liveTurn, err := turnRepo.Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("Create live turn: %v", err)
	}
	if _, err := sessionRepo.UpdateCurrentTurn(ctx, session.ID, &liveTurn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	var pendingJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, message.ID, 0),
	).Scan(&pendingJobID); err != nil {
		t.Fatalf("insert pending job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}

	var status string
	var lastError *string
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error
		FROM job_queue
		WHERE id = $1
	`, pendingJobID).Scan(&status, &lastError); err != nil {
		t.Fatalf("query pending job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("pending job status = %q, want pending (purged=%d)", status, purged)
	}
	if lastError != nil {
		t.Fatalf("pending job last_error = %v, want nil", *lastError)
	}
}

func TestJobWorkerClaimPendingAgentTurnsDeadLettersTerminalMessageAttemptDispatch(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-dead-letter-terminal-message-attempt",
		DisplayName: "Claim Dead Letter Terminal Message Attempt",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Terminal Attempt Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You handle stale terminal dispatch cleanup.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "resume task",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create message: %v", err)
	}

	terminalTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "failed",
		TriggerMessageID: &message.ID,
		RetryCount:       2,
	})
	if err != nil {
		t.Fatalf("Create terminal turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &terminalTurn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	var staleJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":2}`, session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, message.ID, 2),
	).Scan(&staleJobID); err != nil {
		t.Fatalf("insert stale job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 10)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed jobs = %d, want 0", len(claimed))
	}

	var staleStatus string
	var staleError *string
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error
		FROM job_queue
		WHERE id = $1
	`, staleJobID).Scan(&staleStatus, &staleError); err != nil {
		t.Fatalf("query stale job: %v", err)
	}
	staleErrorValue := "<nil>"
	if staleError != nil {
		staleErrorValue = *staleError
	}
	if staleStatus != "dead_letter" || staleError == nil || *staleError != "purged stale terminal message-attempt dispatch during claim" {
		t.Fatalf("stale job = (%q, %q), want dead_letter/purged stale terminal message-attempt dispatch during claim", staleStatus, staleErrorValue)
	}
}

func TestJobWorkerClaimPendingAgentTurnsClaimsOnlyOneJobPerSession(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-one-job-per-session",
		DisplayName: "Claim One Job Per Session",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	firstMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "first pending prompt",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create first message: %v", err)
	}
	secondMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "second pending prompt",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create second message: %v", err)
	}

	var firstJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, firstMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, firstMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, firstMessage.ID, 0),
	).Scan(&firstJobID); err != nil {
		t.Fatalf("insert first job: %v", err)
	}
	var secondJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, secondMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, secondMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, secondMessage.ID, 0),
	).Scan(&secondJobID); err != nil {
		t.Fatalf("insert second job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 10)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(claimed))
	}

	var firstStatus, secondStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, firstJobID).Scan(&firstStatus); err != nil {
		t.Fatalf("query first job: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, secondJobID).Scan(&secondStatus); err != nil {
		t.Fatalf("query second job: %v", err)
	}
	claimedID := claimed[0].ID
	if claimedID != firstJobID {
		t.Fatalf("claimed job = %s, want first job %s", claimedID, firstJobID)
	}
	if firstStatus != "claimed" {
		t.Fatalf("first job status = %q, want claimed", firstStatus)
	}
	if secondStatus != "pending" {
		t.Fatalf("second job status = %q, want pending", secondStatus)
	}
}

func TestJobWorkerClaimPendingAgentTurnsPrioritizesFreshOrgWorkOverProjectContinuation(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-prioritizes-org-over-project-continuation",
		DisplayName: "Claim Prioritizes Org Over Project Continuation",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "claim-prioritizes-org-over-project-continuation-" + uuid.NewString()[:8],
		DisplayName:    "Claim Prioritizes Org Over Project Continuation",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	projectSession, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project session: %v", err)
	}
	projectMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: projectSession.ID,
		Role:      "user",
		Content:   "Continue the project orchestration from the prior continuation summary.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","synthetic_user_message":true}`),
	})
	if err != nil {
		t.Fatalf("create project continuation message: %v", err)
	}
	orgSession, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create org session: %v", err)
	}
	orgMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: orgSession.ID,
		Role:      "user",
		Content:   "Create a fresh validation canary project now.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create org message: %v", err)
	}

	var projectJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, projectSession.ID, projectMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", projectSession.ID, projectMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", projectSession.ID, projectMessage.ID, 0),
	).Scan(&projectJobID); err != nil {
		t.Fatalf("insert project continuation job: %v", err)
	}
	var orgJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, orgSession.ID, orgMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", orgSession.ID, orgMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", orgSession.ID, orgMessage.ID, 0),
	).Scan(&orgJobID); err != nil {
		t.Fatalf("insert org job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 1)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(claimed))
	}
	if claimed[0].ID != orgJobID {
		t.Fatalf("claimed job = %s, want org job %s", claimed[0].ID, orgJobID)
	}

	var projectStatus, orgStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, projectJobID).Scan(&projectStatus); err != nil {
		t.Fatalf("query project job: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, orgJobID).Scan(&orgStatus); err != nil {
		t.Fatalf("query org job: %v", err)
	}
	if projectStatus != "pending" {
		t.Fatalf("project continuation job status = %q, want pending", projectStatus)
	}
	if orgStatus != "claimed" {
		t.Fatalf("org job status = %q, want claimed", orgStatus)
	}
}

func TestJobWorkerClaimPendingAgentTurnsPrefersNewestProjectContinuation(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-prefers-newest-project-continuation",
		DisplayName: "Claim Prefers Newest Project Continuation",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	oldProject, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "claim-prefers-newest-project-continuation-old-" + uuid.NewString()[:8],
		DisplayName:    "Old Project Continuation",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create old project: %v", err)
	}
	newProject, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "claim-prefers-newest-project-continuation-new-" + uuid.NewString()[:8],
		DisplayName:    "New Project Continuation",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create new project: %v", err)
	}

	createProjectSession := func(projectID uuid.UUID, createdBy uuid.UUID) repo.ChatSession {
		session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
			OrganizationID: org.ID,
			ScopeType:      "project",
			ScopeID:        projectID,
			Mode:           "async",
			Status:         "active",
			CreatedByType:  "system",
			CreatedByID:    createdBy,
			Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
		})
		if err != nil {
			t.Fatalf("create project session: %v", err)
		}
		return session
	}

	oldSession := createProjectSession(oldProject.ID, uuid.New())
	newSession := createProjectSession(newProject.ID, uuid.New())
	creatorID := uuid.Nil

	if _, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      oldProject.ID,
		Title:          "Old draft task",
		WorkStatus:     "draft",
		BlocksScope:    "task",
		CreatedByType:  "system",
		CreatedByID:    &creatorID,
	}); err != nil {
		t.Fatalf("create old open task: %v", err)
	}
	if _, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      newProject.ID,
		Title:          "New draft task",
		WorkStatus:     "draft",
		BlocksScope:    "task",
		CreatedByType:  "system",
		CreatedByID:    &creatorID,
	}); err != nil {
		t.Fatalf("create new open task: %v", err)
	}

	oldMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: oldSession.ID,
		Role:      "user",
		Content:   "Continue project execution.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","synthetic_user_message":true}`),
	})
	if err != nil {
		t.Fatalf("create old project message: %v", err)
	}
	newMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: newSession.ID,
		Role:      "user",
		Content:   "Continue project execution.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","synthetic_user_message":true}`),
	})
	if err != nil {
		t.Fatalf("create new project message: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_message
		SET created_at = now() - interval '2 hours'
		WHERE id = $1
	`, oldMessage.ID); err != nil {
		t.Fatalf("age old project message: %v", err)
	}

	var oldJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now() - interval '2 hours', 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, oldSession.ID, oldMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", oldSession.ID, oldMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", oldSession.ID, oldMessage.ID, 0),
	).Scan(&oldJobID); err != nil {
		t.Fatalf("insert old project job: %v", err)
	}
	var newJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, newSession.ID, newMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", newSession.ID, newMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", newSession.ID, newMessage.ID, 0),
	).Scan(&newJobID); err != nil {
		t.Fatalf("insert new project job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 1)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(claimed))
	}
	if claimed[0].ID != newJobID {
		t.Fatalf("claimed job = %s, want newest project job %s (old=%s)", claimed[0].ID, newJobID, oldJobID)
	}
}

func TestJobWorkerClaimPendingAgentTurnsSkipsOlderProjectBootstrapWhenNewerSameSessionAlreadyClaimed(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-skips-older-project-bootstrap-claimed",
		DisplayName: "Claim Skips Older Project Bootstrap Claimed",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "claim-skips-older-project-bootstrap-claimed-" + uuid.NewString()[:8],
		DisplayName:    "Claim Skips Older Project Bootstrap Claimed",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"active"}}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"active"}}`),
	})
	if err != nil {
		t.Fatalf("create project session: %v", err)
	}

	olderMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue bounded project bootstrap turn 2.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_bootstrap","auto_continue":true}`),
	})
	if err != nil {
		t.Fatalf("create older bootstrap message: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	newerMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue active project bootstrap from persisted state.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_bootstrap","auto_continue":true}`),
	})
	if err != nil {
		t.Fatalf("create newer bootstrap message: %v", err)
	}

	var olderJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, olderMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, olderMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, olderMessage.ID, 0),
	).Scan(&olderJobID); err != nil {
		t.Fatalf("insert older bootstrap job: %v", err)
	}
	var newerJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, newerMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, newerMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, newerMessage.ID, 0),
	).Scan(&newerJobID); err != nil {
		t.Fatalf("insert newer bootstrap job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 1)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns first: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed first jobs = %d, want 1", len(claimed))
	}
	if claimed[0].ID != newerJobID {
		t.Fatalf("first claimed job = %s, want newer bootstrap job %s", claimed[0].ID, newerJobID)
	}

	claimed, err = worker.claimPendingAgentTurns(ctx, 1)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns second: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed second jobs = %d, want 0 while newer session job is already claimed", len(claimed))
	}

	var olderStatus, newerStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, olderJobID).Scan(&olderStatus); err != nil {
		t.Fatalf("query older job: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, newerJobID).Scan(&newerStatus); err != nil {
		t.Fatalf("query newer job: %v", err)
	}
	if olderStatus != "pending" {
		t.Fatalf("older job status = %q, want pending", olderStatus)
	}
	if newerStatus != "claimed" {
		t.Fatalf("newer job status = %q, want claimed", newerStatus)
	}
}

func TestJobWorkerClaimPendingAgentTurnsCapsConcurrentProjectContinuations(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-caps-concurrent-project-continuations",
		DisplayName: "Claim Caps Concurrent Project Continuations",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Continuation Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You continue projects.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "claim-caps-concurrent-project-continuations-provider",
		DisplayName: "Claim Caps Concurrent Project Continuations Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	for i := 0; i < maxInFlightProjectContinuations; i++ {
		project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
			OrganizationID: org.ID,
			Slug:           fmt.Sprintf("claim-caps-concurrent-project-continuations-live-%d-%s", i, uuid.NewString()[:8]),
			DisplayName:    fmt.Sprintf("Live Project %d", i),
			DeliveryMode:   "gated",
			CreatedByType:  "system",
			CreatedByID:    uuid.Nil,
			Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
		})
		if err != nil {
			t.Fatalf("create live project %d: %v", i, err)
		}
		session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
			OrganizationID: org.ID,
			ScopeType:      "project",
			ScopeID:        project.ID,
			Mode:           "async",
			Status:         "active",
			CreatedByType:  "system",
			CreatedByID:    uuid.New(),
			Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
		})
		if err != nil {
			t.Fatalf("create live project session %d: %v", i, err)
		}
		if _, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
			OrganizationID:    org.ID,
			ModelProviderID:   provider.ID,
			InvocationPurpose: "agent_turn",
			Status:            "in_flight",
			ModelName:         "test-model",
			AgentID:           &agent.ID,
			ProjectID:         &project.ID,
			SessionID:         &session.ID,
		}); err != nil {
			t.Fatalf("create live project invocation %d: %v", i, err)
		}
	}

	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "claim-caps-concurrent-project-continuations-pending-" + uuid.NewString()[:8],
		DisplayName:    "Pending Project Continuation",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create pending project: %v", err)
	}
	projectSession, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create pending project session: %v", err)
	}
	projectMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: projectSession.ID,
		Role:      "user",
		Content:   "Continue project execution.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","synthetic_user_message":true}`),
	})
	if err != nil {
		t.Fatalf("create pending project continuation message: %v", err)
	}
	var projectJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, projectSession.ID, projectMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", projectSession.ID, projectMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", projectSession.ID, projectMessage.ID, 0),
	).Scan(&projectJobID); err != nil {
		t.Fatalf("insert pending project continuation job: %v", err)
	}

	orgSession, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create org session: %v", err)
	}
	orgMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: orgSession.ID,
		Role:      "user",
		Content:   "Create a fresh validation canary project now.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create org message: %v", err)
	}
	var orgJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, orgSession.ID, orgMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", orgSession.ID, orgMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", orgSession.ID, orgMessage.ID, 0),
	).Scan(&orgJobID); err != nil {
		t.Fatalf("insert org job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 2)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(claimed))
	}
	if claimed[0].ID != orgJobID {
		t.Fatalf("claimed job = %s, want org job %s", claimed[0].ID, orgJobID)
	}

	var projectStatus, orgStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, projectJobID).Scan(&projectStatus); err != nil {
		t.Fatalf("query project job: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, orgJobID).Scan(&orgStatus); err != nil {
		t.Fatalf("query org job: %v", err)
	}
	if projectStatus != "pending" {
		t.Fatalf("project continuation job status = %q, want pending", projectStatus)
	}
	if orgStatus != "claimed" {
		t.Fatalf("org job status = %q, want claimed", orgStatus)
	}
}

func TestJobWorkerClaimPendingAgentTurnsIgnoresInFlightProjectInvocationOnFailedTurn(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-ignores-failed-turn-project-invocation",
		DisplayName: "Claim Ignores Failed Turn Project Invocation",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Continuation Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You continue projects.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "claim-ignores-failed-turn-project-invocation-provider",
		DisplayName: "Claim Ignores Failed Turn Project Invocation Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	for i := 0; i < maxInFlightProjectContinuations-1; i++ {
		project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
			OrganizationID: org.ID,
			Slug:           fmt.Sprintf("claim-ignores-failed-turn-live-%d-%s", i, uuid.NewString()[:8]),
			DisplayName:    fmt.Sprintf("Live Project %d", i),
			DeliveryMode:   "gated",
			CreatedByType:  "system",
			CreatedByID:    uuid.Nil,
			Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
		})
		if err != nil {
			t.Fatalf("create live project %d: %v", i, err)
		}
		session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
			OrganizationID: org.ID,
			ScopeType:      "project",
			ScopeID:        project.ID,
			Mode:           "async",
			Status:         "active",
			CreatedByType:  "system",
			CreatedByID:    uuid.New(),
			Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
		})
		if err != nil {
			t.Fatalf("create live project session %d: %v", i, err)
		}
		if _, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
			OrganizationID:    org.ID,
			ModelProviderID:   provider.ID,
			InvocationPurpose: "agent_turn",
			Status:            "in_flight",
			ModelName:         "test-model",
			AgentID:           &agent.ID,
			ProjectID:         &project.ID,
			SessionID:         &session.ID,
		}); err != nil {
			t.Fatalf("create live project invocation %d: %v", i, err)
		}
	}

	staleProject, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "claim-ignores-failed-turn-stale-" + uuid.NewString()[:8],
		DisplayName:    "Stale Failed Turn Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create stale project: %v", err)
	}
	staleSession, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        staleProject.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create stale session: %v", err)
	}
	staleMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: staleSession.ID,
		Role:      "user",
		Content:   "Continue project execution.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","synthetic_user_message":true}`),
	})
	if err != nil {
		t.Fatalf("create stale message: %v", err)
	}
	staleTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        staleSession.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "failed",
		TriggerMessageID: &staleMessage.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create stale turn: %v", err)
	}
	if _, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &staleProject.ID,
		SessionID:         &staleSession.ID,
		TurnID:            &staleTurn.ID,
	}); err != nil {
		t.Fatalf("create stale failed-turn invocation: %v", err)
	}

	pendingProject, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "claim-ignores-failed-turn-pending-" + uuid.NewString()[:8],
		DisplayName:    "Pending Project Continuation",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create pending project: %v", err)
	}
	pendingSession, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        pendingProject.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create pending session: %v", err)
	}
	creatorID := uuid.Nil
	if _, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      pendingProject.ID,
		Title:          "Pending draft task",
		WorkStatus:     "draft",
		BlocksScope:    "task",
		CreatedByType:  "system",
		CreatedByID:    &creatorID,
	}); err != nil {
		t.Fatalf("create pending open task: %v", err)
	}
	pendingMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: pendingSession.ID,
		Role:      "user",
		Content:   "Continue project execution.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","synthetic_user_message":true}`),
	})
	if err != nil {
		t.Fatalf("create pending project continuation message: %v", err)
	}
	var pendingJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, pendingSession.ID, pendingMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", pendingSession.ID, pendingMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", pendingSession.ID, pendingMessage.ID, 0),
	).Scan(&pendingJobID); err != nil {
		t.Fatalf("insert pending project continuation job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 1)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(claimed))
	}
	if claimed[0].ID != pendingJobID {
		t.Fatalf("claimed job id = %s, want pending job %s", claimed[0].ID, pendingJobID)
	}
}

func TestJobWorkerClaimPendingAgentTurnsSkipsStaleProjectBootstrapAndContinuationJobs(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-skips-stale-project-jobs",
		DisplayName: "Claim Skips Stale Project Jobs",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "claim-skips-stale-project-jobs-" + uuid.NewString()[:8],
		DisplayName:    "Claim Skips Stale Project Jobs",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	projectSession, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create project session: %v", err)
	}
	bootstrapMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: projectSession.ID,
		Role:      "user",
		Content:   "Continue bootstrap.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_bootstrap","auto_continue":true}`),
	})
	if err != nil {
		t.Fatalf("create bootstrap message: %v", err)
	}
	continuationMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: projectSession.ID,
		Role:      "user",
		Content:   "Continue project execution.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","synthetic_user_message":true}`),
	})
	if err != nil {
		t.Fatalf("create project continuation message: %v", err)
	}
	if _, err := worker.Enqueue(ctx, nil, agentTurnJobType, 70, agentTurnKeyPayload{
		SessionID:  projectSession.ID,
		MessageID:  bootstrapMessage.ID,
		RetryCount: 0,
	}, nil); err != nil {
		t.Fatalf("enqueue bootstrap job: %v", err)
	}
	if _, err := worker.Enqueue(ctx, nil, agentTurnJobType, 70, agentTurnKeyPayload{
		SessionID:  projectSession.ID,
		MessageID:  continuationMessage.ID,
		RetryCount: 0,
	}, nil); err != nil {
		t.Fatalf("enqueue continuation job: %v", err)
	}

	orgSession, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create org session: %v", err)
	}
	orgMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: orgSession.ID,
		Role:      "user",
		Content:   "Create a fresh canary project.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create org message: %v", err)
	}
	orgJobID, err := worker.Enqueue(ctx, nil, agentTurnJobType, 70, agentTurnKeyPayload{
		SessionID:  orgSession.ID,
		MessageID:  orgMessage.ID,
		RetryCount: 0,
	}, nil)
	if err != nil {
		t.Fatalf("enqueue org job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 3)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(claimed))
	}
	if claimed[0].ID != orgJobID {
		t.Fatalf("claimed job = %s, want org job %s", claimed[0].ID, orgJobID)
	}

	var pendingProjectJobs int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status = 'pending'
		  AND (payload->>'session_id')::uuid = $1
	`, projectSession.ID).Scan(&pendingProjectJobs); err != nil {
		t.Fatalf("count pending project jobs: %v", err)
	}
	if pendingProjectJobs != 2 {
		t.Fatalf("pending project jobs = %d, want 2", pendingProjectJobs)
	}
}

func TestJobWorkerClaimPendingAgentTurnsDeadLettersClosedSessions(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-skip-closed-session",
		DisplayName: "Claim Skip Closed Session",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "stale pending continuation",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create message: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).Close(ctx, session.ID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, message.ID, 0),
	).Scan(&jobID); err != nil {
		t.Fatalf("insert closed-session job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 10)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed jobs = %d, want 0 for closed session", len(claimed))
	}

	var status string
	var lastError *string
	if err := pool.QueryRow(ctx, `SELECT status, last_error FROM job_queue WHERE id = $1`, jobID).Scan(&status, &lastError); err != nil {
		t.Fatalf("query closed-session job: %v", err)
	}
	lastErrorValue := "<nil>"
	if lastError != nil {
		lastErrorValue = *lastError
	}
	if status != "dead_letter" || lastError == nil || *lastError != "purged closed-session agent_turn dispatch during claim" {
		t.Fatalf("closed-session job = (%q, %q), want dead_letter/purged closed-session agent_turn dispatch during claim", status, lastErrorValue)
	}
}

func TestJobWorkerClaimPendingAgentTurnsAllowsMatchingPendingCurrentTurn(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-matching-pending-current-turn",
		DisplayName: "Claim Matching Pending Current Turn",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Pending Turn Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You continue pending turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue this pending task turn",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("Create pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, message.ID, 0),
	).Scan(&jobID); err != nil {
		t.Fatalf("insert pending job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 10)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(claimed))
	}
	if claimed[0].ID != jobID {
		t.Fatalf("claimed job = %s, want %s", claimed[0].ID, jobID)
	}
}

func TestJobWorkerClaimPendingAgentTurnsIgnoresStalePendingTurnWithoutActiveExecution(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-ignore-stale-pending-turn-without-active-execution",
		DisplayName: "Claim Ignore Stale Pending Turn Without Active Execution",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Pending Turn Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You continue pending turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	staleMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "stale pending task turn",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create stale message: %v", err)
	}
	staleTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &staleMessage.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("Create stale pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &staleTurn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	freshMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "fresh task continuation",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create fresh message: %v", err)
	}

	var freshJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, freshMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, freshMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, freshMessage.ID, 0),
	).Scan(&freshJobID); err != nil {
		t.Fatalf("insert fresh job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 10)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(claimed))
	}
	if claimed[0].ID != freshJobID {
		t.Fatalf("claimed job = %s, want %s", claimed[0].ID, freshJobID)
	}
}

func TestJobWorkerClaimPendingAgentTurnsIgnoresStalePendingCurrentTurnWithoutLiveOwnership(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-ignore-stale-pending-current-turn-with-active-execution",
		DisplayName: "Claim Ignore Stale Pending Current Turn With Active Execution",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Pending Turn Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You continue pending turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "claim-stale-pending-current-turn-project",
		DisplayName:    "Claim Stale Pending Current Turn Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "claim-stale-pending-current-turn-template",
		DisplayName:    "Claim Stale Pending Current Turn Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Claim stale pending current turn with active execution",
		WorkStatus:      "in_progress",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	staleMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "stale pending task turn",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create stale message: %v", err)
	}
	staleTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &staleMessage.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("Create stale pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &staleTurn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}
	if _, err := repo.NewFlowNodeExecutionRepo(pool).UpdateMetadata(ctx, execution.ID, repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{})); err != nil {
		t.Fatalf("clear live owner metadata: %v", err)
	}

	freshMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "system",
		Content:   "[Continuation summary] Continue the active task directly.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create fresh message: %v", err)
	}

	var freshJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1,"flow_node_execution_id":"%s"}`, session.ID, freshMessage.ID, execution.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, freshMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, freshMessage.ID, 1),
	).Scan(&freshJobID); err != nil {
		t.Fatalf("insert fresh job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 10)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(claimed))
	}
	if claimed[0].ID != freshJobID {
		t.Fatalf("claimed job = %s, want %s", claimed[0].ID, freshJobID)
	}
}

func TestJobWorkerClaimPendingAgentTurnsIgnoresStaleInProgressTurnWithoutLiveInvocation(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-ignore-stale-in-progress-turn-without-live-invocation",
		DisplayName: "Claim Ignore Stale In Progress Turn Without Live Invocation",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Claim Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover stale in-progress task turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "claim-ignore-stale-in-progress-turn-project",
		DisplayName:    "Claim Ignore Stale In Progress Turn Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "claim-ignore-stale-in-progress-turn-template",
		DisplayName:    "Claim Ignore Stale In Progress Turn Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         project.ID,
		Title:             "Claim ignores stale in-progress turn without live invocation",
		WorkStatus:        "review",
		BlocksScope:       "task",
		FlowTemplateID:    &template.ID,
		CurrentFlowNodeID: &flowNode.ID,
		CreatedByType:     "system",
		CreatedByID:       &agent.ID,
		AssignedAgentID:   &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	staleMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "stale review retry",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create stale message: %v", err)
	}
	staleTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &staleMessage.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("Create stale in-progress turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &staleTurn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}

	runID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO run (
			id,
			organization_id,
			project_id,
			task_id,
			flow_node_id,
			session_id,
			turn_id,
			principal_type,
			principal_id,
			status,
			trigger_type,
			version,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'agent', $8, 'in_progress', 'scheduler', 1, '{}'::jsonb)
	`, runID, org.ID, project.ID, taskRecord.ID, flowNode.ID, session.ID, staleTurn.ID, agent.ID); err != nil {
		t.Fatalf("create stale run: %v", err)
	}
	metadata := repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{RunID: &runID, TurnID: &staleTurn.ID})
	if _, err := repo.NewFlowNodeExecutionRepo(pool).UpdateMetadata(ctx, execution.ID, metadata); err != nil {
		t.Fatalf("set live owner metadata: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "claim-ignore-stale-in-progress-turn-provider",
		DisplayName: "Claim Ignore Stale In Progress Turn Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	invocationCompletedAt := time.Now().UTC().Add(-30 * time.Minute)
	errorCode := "context_canceled"
	errorMessage := "context canceled"
	if _, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "failed",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &staleTurn.ID,
		RunID:             &runID,
		ErrorCode:         &errorCode,
		ErrorMessage:      &errorMessage,
		CompletedAt:       &invocationCompletedAt,
	}); err != nil {
		t.Fatalf("create failed invocation: %v", err)
	}

	freshMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "system",
		Content:   "[Review action required] Call flow.review_decision for the active review node execution.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create fresh message: %v", err)
	}

	var freshJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1,"flow_node_execution_id":"%s"}`, session.ID, freshMessage.ID, execution.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, freshMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, freshMessage.ID, 1),
	).Scan(&freshJobID); err != nil {
		t.Fatalf("insert fresh job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 10)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(claimed))
	}
	if claimed[0].ID != freshJobID {
		t.Fatalf("claimed job = %s, want %s", claimed[0].ID, freshJobID)
	}
}

func TestJobWorkerClaimPendingAgentTurnsPrefersNewestTaskSessionMessage(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-prefers-newest-task-session-message",
		DisplayName: "Claim Prefers Newest Task Session Message",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "claim-prefers-newest-task-session-message-" + uuid.NewString()[:8],
		DisplayName:    "Claim Prefers Newest Task Session Message",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Newest Message Claim Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "Prefer the newest task-session retry message.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "claim-prefers-newest-task-session-message-template",
		DisplayName:    "Claim Prefers Newest Task Session Message Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Newest task-session message wins",
		WorkStatus:      "review",
		FlowTemplateID:  &template.ID,
		AssignedAgentID: &agent.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create task session: %v", err)
	}

	olderMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Review only. Inspect the deliverable and use flow.review_decision.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"task_review_action"}`),
	})
	if err != nil {
		t.Fatalf("create older review message: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	newerMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor"}`),
	})
	if err != nil {
		t.Fatalf("create newer supervisor message: %v", err)
	}

	var olderJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":2}`, session.ID, olderMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, olderMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, olderMessage.ID, 2),
	).Scan(&olderJobID); err != nil {
		t.Fatalf("insert older review retry job: %v", err)
	}
	var newerJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, newerMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, newerMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, newerMessage.ID, 0),
	).Scan(&newerJobID); err != nil {
		t.Fatalf("insert newer supervisor recovery job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 1)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(claimed))
	}
	if claimed[0].ID != newerJobID {
		t.Fatalf("claimed job = %s, want newer supervisor recovery job %s", claimed[0].ID, newerJobID)
	}

	var olderStatus, newerStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, olderJobID).Scan(&olderStatus); err != nil {
		t.Fatalf("query older job: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, newerJobID).Scan(&newerStatus); err != nil {
		t.Fatalf("query newer job: %v", err)
	}
	if olderStatus != "pending" {
		t.Fatalf("older job status = %q, want pending", olderStatus)
	}
	if newerStatus != "claimed" {
		t.Fatalf("newer job status = %q, want claimed", newerStatus)
	}
}

func TestJobWorkerRequeueStrandedSupervisorRecoveryTurnsSkipsPausedAndArchivedProjects(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-stranded-supervisor-skip-paused",
		DisplayName: "Requeue Stranded Supervisor Skip Paused",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Supervisor Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover stranded supervisor turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	createStrandedSupervisorSession := func(projectID uuid.UUID) uuid.UUID {
		t.Helper()
		task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
			OrganizationID: org.ID,
			ProjectID:      projectID,
			Title:          "Recover stranded supervisor task",
			WorkStatus:     "draft",
			BlocksScope:    "task",
			CreatedByType:  "system",
			CreatedByID:    &agent.ID,
		})
		if err != nil {
			t.Fatalf("create project task: %v", err)
		}
		session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
			OrganizationID: org.ID,
			ScopeType:      "project_task",
			ScopeID:        task.ID,
			Mode:           "async",
			Status:         "active",
			CreatedByType:  "system",
			CreatedByID:    uuid.New(),
		})
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
			SessionID: session.ID,
			Role:      "user",
			Content:   "supervisor recovery: resume task",
			Status:    "pending",
			Metadata:  json.RawMessage(`{"source":"supervisor"}`),
		})
		if err != nil {
			t.Fatalf("create supervisor message: %v", err)
		}
		turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
			SessionID:        session.ID,
			TurnNumber:       1,
			RespondingType:   "agent",
			RespondingID:     agent.ID,
			Status:           "pending",
			TriggerMessageID: &message.ID,
		})
		if err != nil {
			t.Fatalf("create pending turn: %v", err)
		}
		if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
			t.Fatalf("update current turn: %v", err)
		}
		return session.ID
	}

	pausedProject, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "stranded-paused-project",
		DisplayName:    "Stranded Paused Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       []byte(`{"pause":{"is_paused":true,"reason":"operator pause"}}`),
	})
	if err != nil {
		t.Fatalf("create paused project: %v", err)
	}
	archivedProject, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "stranded-archived-project",
		DisplayName:    "Stranded Archived Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create archived project: %v", err)
	}
	if err := repo.NewProjectRepo(pool).Archive(ctx, archivedProject.ID); err != nil {
		t.Fatalf("archive project: %v", err)
	}

	pausedSessionID := createStrandedSupervisorSession(pausedProject.ID)
	archivedSessionID := createStrandedSupervisorSession(archivedProject.ID)

	requeued, err := worker.RequeueStrandedSupervisorRecoveryTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueStrandedSupervisorRecoveryTurns: %v", err)
	}
	if requeued != 0 {
		t.Fatalf("requeued turns = %d, want 0", requeued)
	}

	for _, sessionID := range []uuid.UUID{pausedSessionID, archivedSessionID} {
		var count int
		if err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM job_queue
			WHERE job_type = 'agent_turn'
			  AND (payload->>'session_id')::uuid = $1
		`, sessionID).Scan(&count); err != nil {
			t.Fatalf("count requeued jobs for session %s: %v", sessionID, err)
		}
		if count != 0 {
			t.Fatalf("requeued jobs for session %s = %d, want 0", sessionID, count)
		}
	}
}

func TestJobWorkerRequeueStrandedUserMessageTurns(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-stranded-user-message",
		DisplayName: "Requeue Stranded User Message",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "human_user",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	cancelledMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Old cancelled kickoff",
		Status:    "pending",
		Metadata:  json.RawMessage(fmt.Sprintf(`{"agent_turn_dispatch":{"cancelled_at":%q,"cancel_reason":"user_cancelled"}}`, time.Now().UTC().Format(time.RFC3339Nano))),
	})
	if err != nil {
		t.Fatalf("create cancelled message: %v", err)
	}
	cancelledTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "cancelled",
		TriggerMessageID: &cancelledMessage.ID,
	})
	if err != nil {
		t.Fatalf("create cancelled turn: %v", err)
	}
	if cancelledTurn.TriggerMessageID == nil || *cancelledTurn.TriggerMessageID != cancelledMessage.ID {
		t.Fatalf("cancelled turn trigger_message_id = %v, want %s", cancelledTurn.TriggerMessageID, cancelledMessage.ID)
	}

	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Start a fresh Sam.blog project from scratch.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create stranded user message: %v", err)
	}

	requeued, err := worker.RequeueStrandedUserMessageTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueStrandedUserMessageTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued turns = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, (payload->>'message_id')::uuid, (payload->>'session_id')::uuid
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID); err != nil {
		t.Fatalf("query requeued user message job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued message_id = %s, want %s", requeuedMsgID, message.ID)
	}
}

func TestJobWorkerRequeueStrandedUserMessageTurnsIgnoresNewerFailedAssistantStub(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-stranded-user-message-newer-assistant",
		DisplayName: "Requeue Stranded User Message Newer Assistant",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Continuation Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "human_user",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	summaryMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "system",
		Content:   "[Continuation summary] Active organization request: create rerun 30.",
		Status:    "final",
	})
	if err != nil {
		t.Fatalf("create summary message: %v", err)
	}
	if _, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "failed",
		TriggerMessageID: &summaryMessage.ID,
		ErrorMessage: func() *string {
			value := "worker cleanup failed stale in_flight model invocation without live in-progress turn"
			return &value
		}(),
	}); err != nil {
		t.Fatalf("create failed summary turn: %v", err)
	}
	userMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the active organization request now from the continuation summary above.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create stranded continuation user message: %v", err)
	}
	if _, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "assistant",
		Content:   "I'll",
		Status:    "failed",
		TurnID:    nil,
	}); err != nil {
		t.Fatalf("create failed assistant stub: %v", err)
	}

	requeued, err := worker.RequeueStrandedUserMessageTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueStrandedUserMessageTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued turns = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, (payload->>'message_id')::uuid, (payload->>'session_id')::uuid
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID); err != nil {
		t.Fatalf("query requeued user message job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != userMessage.ID {
		t.Fatalf("requeued message_id = %s, want %s", requeuedMsgID, userMessage.ID)
	}
}

func TestJobWorkerRequeuePendingTurnsWithoutJobs(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-pending-turns-without-jobs",
		DisplayName: "Requeue Pending Turns Without Jobs",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-pending-turns-project",
		DisplayName:    "Requeue Pending Turns Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Pending task turn",
		WorkStatus:     "draft",
		BlocksScope:    "task",
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Resume the pending task turn.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create user message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	requeued, err := worker.RequeuePendingTurnsWithoutJobs(ctx)
	if err != nil {
		t.Fatalf("RequeuePendingTurnsWithoutJobs: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued turns = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID); err != nil {
		t.Fatalf("query requeued pending turn job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued message_id = %s, want %s", requeuedMsgID, message.ID)
	}
}

func TestJobWorkerRequeuePendingTurnsWithoutJobsSkipsPausedAndArchivedProjects(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-pending-turns-skip-paused",
		DisplayName: "Requeue Pending Turns Skip Paused",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	createPendingTaskSession := func(projectID uuid.UUID) uuid.UUID {
		t.Helper()
		taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
			OrganizationID: org.ID,
			ProjectID:      projectID,
			Title:          "Pending task turn",
			WorkStatus:     "draft",
			BlocksScope:    "task",
			CreatedByType:  "system",
			CreatedByID:    &agent.ID,
		})
		if err != nil {
			t.Fatalf("create task: %v", err)
		}
		session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
			OrganizationID: org.ID,
			ScopeType:      "project_task",
			ScopeID:        taskRecord.ID,
			Mode:           "async",
			Status:         "active",
			CreatedByType:  "system",
			CreatedByID:    uuid.New(),
		})
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
			SessionID: session.ID,
			Role:      "user",
			Content:   "Resume the pending task turn.",
			Status:    "pending",
		})
		if err != nil {
			t.Fatalf("create user message: %v", err)
		}
		turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
			SessionID:        session.ID,
			TurnNumber:       1,
			RespondingType:   "agent",
			RespondingID:     agent.ID,
			Status:           "pending",
			TriggerMessageID: &message.ID,
		})
		if err != nil {
			t.Fatalf("create pending turn: %v", err)
		}
		if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
			t.Fatalf("update current turn: %v", err)
		}
		return session.ID
	}

	pausedProject, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "paused-project",
		DisplayName:    "Paused Project",
		DeliveryMode:   "gated",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       []byte(`{"pause":{"is_paused":true,"reason":"operator pause"}}`),
	})
	if err != nil {
		t.Fatalf("create paused project: %v", err)
	}
	archivedProject, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "archived-project",
		DisplayName:    "Archived Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create archived project: %v", err)
	}
	if err := repo.NewProjectRepo(pool).Archive(ctx, archivedProject.ID); err != nil {
		t.Fatalf("archive project: %v", err)
	}

	pausedSessionID := createPendingTaskSession(pausedProject.ID)
	archivedSessionID := createPendingTaskSession(archivedProject.ID)

	requeued, err := worker.RequeuePendingTurnsWithoutJobs(ctx)
	if err != nil {
		t.Fatalf("RequeuePendingTurnsWithoutJobs: %v", err)
	}
	if requeued != 0 {
		t.Fatalf("requeued turns = %d, want 0", requeued)
	}

	for _, sessionID := range []uuid.UUID{pausedSessionID, archivedSessionID} {
		var count int
		if err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM job_queue
			WHERE job_type = 'agent_turn'
			  AND (payload->>'session_id')::uuid = $1
		`, sessionID).Scan(&count); err != nil {
			t.Fatalf("count requeued jobs for session %s: %v", sessionID, err)
		}
		if count != 0 {
			t.Fatalf("requeued jobs for session %s = %d, want 0", sessionID, count)
		}
	}
}

func TestJobWorkerRequeuePendingTurnsWithoutJobsRequeuesAfterProjectResume(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-pending-turns-after-resume",
		DisplayName: "Requeue Pending Turns After Resume",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "paused-then-resumed-project",
		DisplayName:    "Paused Then Resumed Project",
		DeliveryMode:   "gated",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       []byte(`{"pause":{"is_paused":true,"reason":"operator pause"}}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Pending task turn",
		WorkStatus:     "draft",
		BlocksScope:    "task",
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Resume the pending task turn.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create user message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("update current turn: %v", err)
	}

	requeued, err := worker.RequeuePendingTurnsWithoutJobs(ctx)
	if err != nil {
		t.Fatalf("RequeuePendingTurnsWithoutJobs while paused: %v", err)
	}
	if requeued != 0 {
		t.Fatalf("requeued turns while paused = %d, want 0", requeued)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE project
		SET settings = '{}'::jsonb
		WHERE id = $1
	`, project.ID); err != nil {
		t.Fatalf("resume project: %v", err)
	}

	requeued, err = worker.RequeuePendingTurnsWithoutJobs(ctx)
	if err != nil {
		t.Fatalf("RequeuePendingTurnsWithoutJobs after resume: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued turns after resume = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
		requeuedExecID *uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       CASE
		         WHEN COALESCE(payload->>'flow_node_execution_id', '') = '' THEN NULL
		         ELSE (payload->>'flow_node_execution_id')::uuid
		       END
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID, &requeuedExecID); err != nil {
		t.Fatalf("query requeued pending turn job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued message_id = %s, want %s", requeuedMsgID, message.ID)
	}
}

func TestJobWorkerRequeuePendingTurnsWithoutJobsUsesExecutionMetadataLiveTurn(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-pending-turns-live-owner",
		DisplayName: "Requeue Pending Turns Live Owner",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-pending-turns-live-owner-project",
		DisplayName:    "Requeue Pending Turns Live Owner Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-pending-turns-live-owner-template",
		DisplayName:    "Requeue Pending Turns Live Owner Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Pending task turn from execution owner metadata",
		WorkStatus:      "in_progress",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Resume the pending task turn.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create user message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}
	metadata := repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{TurnID: &turn.ID})
	if _, err := repo.NewFlowNodeExecutionRepo(pool).UpdateMetadata(ctx, execution.ID, metadata); err != nil {
		t.Fatalf("set live turn metadata: %v", err)
	}

	requeued, err := worker.RequeuePendingTurnsWithoutJobs(ctx)
	if err != nil {
		t.Fatalf("RequeuePendingTurnsWithoutJobs: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued turns = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
		requeuedExecID *uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       CASE
		         WHEN COALESCE(payload->>'flow_node_execution_id', '') = '' THEN NULL
		         ELSE (payload->>'flow_node_execution_id')::uuid
		       END
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID, &requeuedExecID); err != nil {
		t.Fatalf("query requeued pending turn job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued message_id = %s, want %s", requeuedMsgID, message.ID)
	}
	if requeuedExecID == nil || *requeuedExecID != execution.ID {
		t.Fatalf("requeued flow_node_execution_id = %v, want %s", requeuedExecID, execution.ID)
	}
}

func TestJobWorkerRequeuePendingTurnsWithoutJobsIgnoresFailedExecutionLiveTurn(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-pending-turns-failed-live-owner",
		DisplayName: "Requeue Pending Turns Failed Live Owner",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-pending-turns-failed-live-owner-project",
		DisplayName:    "Requeue Pending Turns Failed Live Owner Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-pending-turns-failed-live-owner-template",
		DisplayName:    "Requeue Pending Turns Failed Live Owner Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Pending task turn masked by failed live turn",
		WorkStatus:      "in_progress",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	failedMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Old failed turn message.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create failed message: %v", err)
	}
	failedTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "failed",
		TriggerMessageID: &failedMessage.ID,
	})
	if err != nil {
		t.Fatalf("create failed turn: %v", err)
	}
	pendingMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Resume the pending task turn.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create pending message: %v", err)
	}
	pendingTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       2,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &pendingMessage.ID,
	})
	if err != nil {
		t.Fatalf("create pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &pendingTurn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}
	metadata := repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{TurnID: &failedTurn.ID})
	if _, err := repo.NewFlowNodeExecutionRepo(pool).UpdateMetadata(ctx, execution.ID, metadata); err != nil {
		t.Fatalf("set failed live turn metadata: %v", err)
	}

	requeued, err := worker.RequeuePendingTurnsWithoutJobs(ctx)
	if err != nil {
		t.Fatalf("RequeuePendingTurnsWithoutJobs: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued turns = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
		requeuedExecID *uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       CASE
		         WHEN COALESCE(payload->>'flow_node_execution_id', '') = '' THEN NULL
		         ELSE (payload->>'flow_node_execution_id')::uuid
		       END
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID, &requeuedExecID); err != nil {
		t.Fatalf("query requeued pending turn job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != pendingMessage.ID {
		t.Fatalf("requeued message_id = %s, want %s", requeuedMsgID, pendingMessage.ID)
	}
	if requeuedExecID == nil || *requeuedExecID != execution.ID {
		t.Fatalf("requeued flow_node_execution_id = %v, want %s", requeuedExecID, execution.ID)
	}
}

func TestJobWorkerRequeuePendingTurnsWithoutJobsSkipsBlockedTasks(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-pending-turns-blocked-task",
		DisplayName: "Requeue Pending Turns Blocked Task",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Blocked Pending Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "Recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-pending-turns-blocked-task-project",
		DisplayName:    "Requeue Pending Turns Blocked Task Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-pending-turns-blocked-task-template",
		DisplayName:    "Requeue Pending Turns Blocked Task Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         project.ID,
		Title:             "Blocked pending turn",
		WorkStatus:        "blocked",
		BlocksScope:       "task",
		FlowTemplateID:    &template.ID,
		CurrentFlowNodeID: &flowNode.ID,
		CreatedByType:     "system",
		CreatedByID:       &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "resume blocked pending turn",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("update current turn: %v", err)
	}

	requeued, err := worker.RequeuePendingTurnsWithoutJobs(ctx)
	if err != nil {
		t.Fatalf("RequeuePendingTurnsWithoutJobs: %v", err)
	}
	if requeued != 0 {
		t.Fatalf("requeued turns = %d, want 0 for blocked task", requeued)
	}

	var pendingJobs int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status = 'pending'
		  AND (payload->>'session_id')::uuid = $1
	`, session.ID).Scan(&pendingJobs); err != nil {
		t.Fatalf("count pending jobs: %v", err)
	}
	if pendingJobs != 0 {
		t.Fatalf("pending jobs = %d, want 0", pendingJobs)
	}
}

func TestJobWorkerRequeueActiveExecutionSessionsWithoutTurns(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-execution-sessions-without-turns",
		DisplayName: "Requeue Active Execution Sessions Without Turns",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-execution-project",
		DisplayName:    "Requeue Active Execution Project",
		Description:    "Project for active execution task-session repair",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-active-execution-template",
		DisplayName:    "Requeue Active Execution Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover stranded active execution session",
		WorkStatus:      "draft",
		BlocksScope:     "task",
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create supervisor kickoff message: %v", err)
	}
	completedTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create completed recovery turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}
	if _, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	}); err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}
	if completedTurn.TriggerMessageID == nil || *completedTurn.TriggerMessageID != message.ID {
		t.Fatalf("completed recovery turn trigger_message_id = %v, want %s", completedTurn.TriggerMessageID, message.ID)
	}

	requeued, err := worker.RequeueActiveExecutionSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveExecutionSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued sessions = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
		retryCount     int
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID, &retryCount); err != nil {
		t.Fatalf("query requeued active execution job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID == message.ID {
		t.Fatalf("requeued message_id = %s, want refreshed bootstrap continuation message", requeuedMsgID)
	}
	if retryCount != 0 {
		t.Fatalf("requeued retry_count = %d, want 0 for fresh synthesized bootstrap continuation", retryCount)
	}
	var requeuedSource string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(metadata->>'source', '')
		FROM chat_message
		WHERE id = $1
	`, requeuedMsgID).Scan(&requeuedSource); err != nil {
		t.Fatalf("load requeued message source: %v", err)
	}
	if requeuedSource != "project_bootstrap" {
		t.Fatalf("requeued message source = %q, want project_bootstrap", requeuedSource)
	}
}

func TestJobWorkerRequeueActiveExecutionSessionsWithoutTurnsPreservesRateLimitBackoff(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-execution-preserves-rate-limit-backoff",
		DisplayName: "Requeue Active Execution Preserves Rate Limit Backoff",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-execution-preserves-rate-limit-backoff-project",
		DisplayName:    "Requeue Active Execution Preserves Rate Limit Backoff Project",
		Description:    "Project for no-turn rate-limit recovery coverage",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-active-execution-preserves-rate-limit-backoff-template",
		DisplayName:    "Requeue Active Execution Preserves Rate Limit Backoff Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover rate-limited no-turn execution session",
		WorkStatus:      "draft",
		BlocksScope:     "task",
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	messageMetadata := json.RawMessage(`{"source":"task_queue_processor","flow_event_type":"flow.rejected"}`)
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Start work on task: Validate pipeline output format and delivery",
		Status:    "pending",
		Metadata:  messageMetadata,
	})
	if err != nil {
		t.Fatalf("create task queue message: %v", err)
	}
	completedAt := time.Now().UTC().Add(-30 * time.Second).Truncate(time.Second)
	errorMessage := `model provider rate limited (retry_after=3h22m57s): provider http 429`
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "failed",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create failed turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET completed_at = $2,
		    error_message = $3
		WHERE id = $1
	`, turn.ID, completedAt, errorMessage); err != nil {
		t.Fatalf("update failed turn with rate limit error: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}
	messageMetadata = json.RawMessage(fmt.Sprintf(`{"source":"task_queue_processor","flow_event_type":"flow.rejected","flow_node_execution_id":"%s"}`, execution.ID))
	if _, err := repo.NewChatMessageRepo(pool).UpdateMetadata(ctx, message.ID, messageMetadata); err != nil {
		t.Fatalf("update task queue metadata: %v", err)
	}

	requeued, err := worker.RequeueActiveExecutionSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveExecutionSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued sessions = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
		retryCount     int
		runAfter       time.Time
		jitterApplied  bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       COALESCE((payload->>'retry_count')::int, 0),
		       run_after,
		       COALESCE((payload->>'rate_limit_jitter_applied')::boolean, false)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID, &retryCount, &runAfter, &jitterApplied); err != nil {
		t.Fatalf("query requeued rate-limited active execution job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued message_id = %s, want %s", requeuedMsgID, message.ID)
	}
	if retryCount != 0 {
		t.Fatalf("requeued retry_count = %d, want 0 for fresh synthesized bootstrap continuation", retryCount)
	}
	if !jitterApplied {
		t.Fatal("expected rate_limit_jitter_applied = true")
	}
	minExpected := completedAt.Add(agentTurnRateLimitMinBackoff)
	if !runAfter.After(minExpected) {
		t.Fatalf("run_after = %s, want > %s", runAfter, minExpected)
	}
	maxAllowed := worker.clock.Now().UTC().Add(agentTurnRateLimitBackoffCap + time.Minute)
	if runAfter.After(maxAllowed) {
		t.Fatalf("run_after = %s, want clamped near <= %s", runAfter, maxAllowed)
	}
}

func TestJobWorkerRequeueActiveExecutionSessionsWithoutTurnsCapsRecoveryBatch(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-execution-caps-batch",
		DisplayName: "Requeue Active Execution Caps Batch",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-execution-caps-batch-project",
		DisplayName:    "Requeue Active Execution Caps Batch Project",
		Description:    "Project for bounded recovery requeue coverage",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-active-execution-caps-batch-template",
		DisplayName:    "Requeue Active Execution Caps Batch Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}

	for i := 0; i < 3; i++ {
		task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
			OrganizationID:  org.ID,
			ProjectID:       project.ID,
			Title:           fmt.Sprintf("Recover stranded active execution session %d", i+1),
			WorkStatus:      "draft",
			BlocksScope:     "task",
			CreatedByType:   "system",
			CreatedByID:     &agent.ID,
			AssignedAgentID: &agent.ID,
			Metadata:        json.RawMessage(`{}`),
		})
		if err != nil {
			t.Fatalf("create project task %d: %v", i+1, err)
		}
		session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
			OrganizationID: org.ID,
			ScopeType:      "project_task",
			ScopeID:        task.ID,
			Mode:           "async",
			Status:         "active",
			CreatedByType:  "system",
			CreatedByID:    uuid.New(),
		})
		if err != nil {
			t.Fatalf("CreateSession %d: %v", i+1, err)
		}
		message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
			SessionID: session.ID,
			Role:      "user",
			Content:   "supervisor recovery: resume task",
			Status:    "pending",
			Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
		})
		if err != nil {
			t.Fatalf("create supervisor kickoff message %d: %v", i+1, err)
		}
		completedTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
			SessionID:        session.ID,
			TurnNumber:       1,
			RespondingType:   "agent",
			RespondingID:     agent.ID,
			Status:           "completed",
			TriggerMessageID: &message.ID,
		})
		if err != nil {
			t.Fatalf("create completed recovery turn %d: %v", i+1, err)
		}
		if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
			t.Fatalf("clear current turn %d: %v", i+1, err)
		}
		if _, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
			TaskID:      task.ID,
			FlowNodeID:  flowNode.ID,
			VisitNumber: 1,
			Status:      "active",
			SessionID:   &session.ID,
		}); err != nil {
			t.Fatalf("create active flow node execution %d: %v", i+1, err)
		}
		if completedTurn.TriggerMessageID == nil || *completedTurn.TriggerMessageID != message.ID {
			t.Fatalf("completed recovery turn %d trigger_message_id = %v, want %s", i+1, completedTurn.TriggerMessageID, message.ID)
		}
	}

	requeued, err := worker.RequeueActiveExecutionSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveExecutionSessionsWithoutTurns: %v", err)
	}
	if requeued != int64(worker.maxExecutionSessionRecoveryBatch()) {
		t.Fatalf("requeued sessions = %d, want %d", requeued, worker.maxExecutionSessionRecoveryBatch())
	}

	var pendingJobs int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status = 'pending'
	`).Scan(&pendingJobs); err != nil {
		t.Fatalf("count pending agent_turn jobs: %v", err)
	}
	if pendingJobs != worker.maxExecutionSessionRecoveryBatch() {
		t.Fatalf("pending agent_turn jobs = %d, want %d", pendingJobs, worker.maxExecutionSessionRecoveryBatch())
	}
}

func TestJobWorkerRequeueActiveExecutionSessionsWithoutTurnsRetiresStaleRunOwnership(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-execution-with-stale-run",
		DisplayName: "Requeue Active Execution With Stale Run",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-execution-stale-run-project",
		DisplayName:    "Requeue Active Execution Stale Run Project",
		Description:    "Project for no-turn stale run repair",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-active-execution-stale-run-template",
		DisplayName:    "Requeue Active Execution Stale Run Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover no-turn stale run ownership",
		WorkStatus:      "draft",
		BlocksScope:     "task",
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create supervisor kickoff message: %v", err)
	}
	if _, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	}); err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}
	runID := uuid.New()
	startedAt := time.Now().UTC().Add(-5 * time.Minute)
	if _, err := pool.Exec(ctx, `
		INSERT INTO run (
			id,
			organization_id,
			session_id,
			principal_type,
			principal_id,
			status,
			trigger_type,
			started_at,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, 'system', $4, 'in_progress', 'api', $5, $5, $5)
	`, runID, org.ID, session.ID, uuid.Nil, startedAt); err != nil {
		t.Fatalf("create stale run: %v", err)
	}

	requeued, err := worker.RequeueActiveExecutionSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveExecutionSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued sessions = %d, want 1", requeued)
	}

	var runStatus, failureReason string
	if err := pool.QueryRow(ctx, `
		SELECT status, COALESCE(failure_reason, '')
		FROM run
		WHERE id = $1
	`, runID).Scan(&runStatus, &failureReason); err != nil {
		t.Fatalf("query stale run after repair: %v", err)
	}
	if runStatus != "failed" {
		t.Fatalf("stale run status = %q, want failed", runStatus)
	}
	if !strings.Contains(strings.ToLower(strings.TrimSpace(failureReason)), "without live task turn ownership") {
		t.Fatalf("stale run failure_reason = %q, want no-turn ownership repair", failureReason)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
		retryCount     int
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID, &retryCount); err != nil {
		t.Fatalf("query requeued job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued message_id = %s, want %s", requeuedMsgID, message.ID)
	}
	if retryCount != 0 {
		t.Fatalf("requeued retry_count = %d, want 0 when no prior turn exists", retryCount)
	}
}

func TestJobWorkerRequeueActiveProjectSessionsWithoutTurns(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-project-sessions-without-turns",
		DisplayName: "Requeue Active Project Sessions Without Turns",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Continuation Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You continue active projects.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-project-session-project",
		DisplayName:    "Requeue Active Project Session Project",
		Description:    "Project for project-session repair coverage",
		DeliveryMode:   "gated",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"active"}}`),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the active project execution now.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","auto_continue":true}`),
	})
	if err != nil {
		t.Fatalf("create project continuation message: %v", err)
	}
	completedTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create completed project continuation turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}
	if completedTurn.TriggerMessageID == nil || *completedTurn.TriggerMessageID != message.ID {
		t.Fatalf("completed project turn trigger_message_id = %v, want %s", completedTurn.TriggerMessageID, message.ID)
	}

	requeued, err := worker.RequeueActiveProjectSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveProjectSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued sessions = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
		retryCount     int
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID, &retryCount); err != nil {
		t.Fatalf("query requeued active project job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if retryCount != 0 {
		t.Fatalf("requeued retry_count = %d, want 0 for fresh synthesized bootstrap continuation", retryCount)
	}
	if requeuedMsgID == message.ID {
		t.Fatalf("requeued message_id = %s, want refreshed bootstrap continuation message", requeuedMsgID)
	}
	var requeuedSource string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(metadata->>'source', '')
		FROM chat_message
		WHERE id = $1
	`, requeuedMsgID).Scan(&requeuedSource); err != nil {
		t.Fatalf("load requeued message source: %v", err)
	}
	if requeuedSource != "project_bootstrap" {
		t.Fatalf("requeued message source = %q, want project_bootstrap", requeuedSource)
	}
}

func TestJobWorkerRequeueActiveProjectSessionsWithoutTurnsSkipsFinalMessages(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-project-sessions-skip-final",
		DisplayName: "Requeue Active Project Sessions Skip Final",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-project-session-skip-final-project",
		DisplayName:    "Requeue Active Project Session Skip Final Project",
		Description:    "Project for final-message repair coverage",
		DeliveryMode:   "gated",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the active project execution now.",
		Status:    "final",
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","auto_continue":true}`),
	}); err != nil {
		t.Fatalf("create final project continuation message: %v", err)
	}

	requeued, err := worker.RequeueActiveProjectSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveProjectSessionsWithoutTurns: %v", err)
	}
	if requeued != 0 {
		t.Fatalf("requeued sessions = %d, want 0", requeued)
	}
}

func TestJobWorkerRequeueActiveProjectSessionsWithoutTurnsIgnoresStalePendingDispatch(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-project-session-ignore-stale-pending-dispatch",
		DisplayName: "Requeue Active Project Session Ignore Stale Pending Dispatch",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Continuation Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You continue active projects.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-project-session-ignore-stale-pending-dispatch-project",
		DisplayName:    "Requeue Active Project Session Ignore Stale Pending Dispatch Project",
		Description:    "Project for stale pending continuation dispatch coverage",
		DeliveryMode:   "gated",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the active project execution now.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","auto_continue":true}`),
	})
	if err != nil {
		t.Fatalf("create pending project continuation message: %v", err)
	}
	completedTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "failed",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create terminal project continuation turn: %v", err)
	}
	if _, err := worker.Enqueue(ctx, nil, agentTurnJobType, 100, agentTurnKeyPayload{
		SessionID:  session.ID,
		MessageID:  message.ID,
		RetryCount: 0,
	}, nil); err != nil {
		t.Fatalf("enqueue stale pending dispatch: %v", err)
	}
	if completedTurn.TriggerMessageID == nil || *completedTurn.TriggerMessageID != message.ID {
		t.Fatalf("completed turn trigger_message_id = %v, want %s", completedTurn.TriggerMessageID, message.ID)
	}

	requeued, err := worker.RequeueActiveProjectSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveProjectSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued sessions = %d, want 1", requeued)
	}

	var pendingJobs int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status = 'pending'
		  AND (payload->>'session_id')::uuid = $1
		  AND (payload->>'message_id')::uuid = $2
	`, session.ID, message.ID).Scan(&pendingJobs); err != nil {
		t.Fatalf("count pending agent_turn jobs: %v", err)
	}
	if pendingJobs != 1 {
		t.Fatalf("pending agent_turn jobs = %d, want 1 fresh requeue", pendingJobs)
	}
	var deadLetterJobs int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status = 'dead_letter'
		  AND (payload->>'session_id')::uuid = $1
		  AND (payload->>'message_id')::uuid = $2
	`, session.ID, message.ID).Scan(&deadLetterJobs); err != nil {
		t.Fatalf("count dead-letter agent_turn jobs: %v", err)
	}
	if deadLetterJobs != 1 {
		t.Fatalf("dead-letter agent_turn jobs = %d, want 1 stale dispatch retired", deadLetterJobs)
	}
}

func TestJobWorkerRequeueActiveProjectSessionsWithoutTurnsIgnoresCompletedBootstrapDispatch(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-project-session-ignore-completed-bootstrap-dispatch",
		DisplayName: "Requeue Active Project Session Ignore Completed Bootstrap Dispatch",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-project-session-ignore-completed-bootstrap-dispatch-project",
		DisplayName:    "Requeue Active Project Session Ignore Completed Bootstrap Dispatch Project",
		Description:    "Project for completed-bootstrap dispatch recovery coverage",
		DeliveryMode:   "gated",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	bootstrapMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue bootstrap.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_bootstrap","auto_continue":true}`),
	})
	if err != nil {
		t.Fatalf("create stale bootstrap message: %v", err)
	}
	creatorID := uuid.New()
	taskRepo := repo.NewProjectTaskRepo(pool)
	if _, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		TaskNumber:     1,
		Title:          "Later wave validation",
		WorkStatus:     "draft",
		CreatedByType:  "system",
		CreatedByID:    &creatorID,
	}); err != nil {
		t.Fatalf("create draft task: %v", err)
	}
	requeued, err := worker.RequeueActiveProjectSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveProjectSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued sessions = %d, want 1", requeued)
	}

	var (
		status     string
		messageID  uuid.UUID
		sessionID  uuid.UUID
		retryCount int
		source     string
	)
	if err := pool.QueryRow(ctx, `
		SELECT jq.status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       COALESCE((payload->>'retry_count')::int, 0),
		       COALESCE(cm.metadata->>'source', '')
		FROM job_queue jq
		JOIN chat_message cm
		  ON cm.id = (jq.payload->>'message_id')::uuid
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		  AND COALESCE(cm.metadata->>'source', '') = 'project_execution_continuation'
		ORDER BY jq.created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &messageID, &sessionID, &retryCount, &source); err != nil {
		t.Fatalf("query requeued continuation job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("continuation job status = %q, want pending", status)
	}
	if messageID == bootstrapMessage.ID {
		t.Fatalf("requeued message_id = %s, want synthesized continuation message", messageID)
	}
	if sessionID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", sessionID, session.ID)
	}
	if retryCount != 0 {
		t.Fatalf("retry_count = %d, want 0", retryCount)
	}
	if source != "project_execution_continuation" {
		t.Fatalf("continuation source = %q, want project_execution_continuation", source)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsDropsLegacyTaskDispatchWithoutExecutionOwnership(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-legacy-task-dispatch-without-execution-ownership",
		DisplayName: "Purge Legacy Task Dispatch Without Execution Ownership",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "legacy-task-dispatch-project",
		DisplayName:    "Legacy Task Dispatch Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Execution Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "Execute task work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "legacy-task-dispatch-template",
		DisplayName:    "Legacy Task Dispatch Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work Node",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:      org.ID,
		ProjectID:           project.ID,
		TaskNumber:          1,
		Title:               "Active task",
		WorkStatus:          "in_progress",
		FlowTemplateID:      &template.ID,
		CurrentFlowNodeID:   &flowNode.ID,
		CreatedByType:       "system",
		CreatedByID:         func() *uuid.UUID { id := uuid.New(); return &id }(),
		RequiresHumanReview: false,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
		Metadata:    json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}
	liveMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "current execution message",
		Status:    "pending",
		Metadata:  json.RawMessage(fmt.Sprintf(`{"source":"task_queue_processor","flow_node_execution_id":"%s"}`, execution.ID)),
	})
	if err != nil {
		t.Fatalf("create live message: %v", err)
	}
	liveTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &liveMessage.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create live turn: %v", err)
	}
	if _, err := repo.NewFlowNodeExecutionRepo(pool).UpdateMetadata(ctx, execution.ID, repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{
		TurnID: &liveTurn.ID,
	})); err != nil {
		t.Fatalf("update execution metadata: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &liveTurn.ID); err != nil {
		t.Fatalf("update current turn: %v", err)
	}

	legacyMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "legacy retry",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor"}`),
	})
	if err != nil {
		t.Fatalf("create legacy message: %v", err)
	}
	legacyPayload := map[string]any{
		"session_id":  session.ID.String(),
		"message_id":  legacyMessage.ID.String(),
		"retry_count": 1,
	}
	if _, err := worker.Enqueue(ctx, nil, agentTurnJobType, 70, legacyPayload, nil); err != nil {
		t.Fatalf("enqueue legacy agent_turn: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged < 1 {
		t.Fatalf("purged jobs = %d, want at least 1", purged)
	}

	var (
		status    string
		lastError string
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, COALESCE(last_error, '')
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		  AND (payload->>'message_id')::uuid = $2
	`, session.ID, legacyMessage.ID).Scan(&status, &lastError); err != nil {
		t.Fatalf("query purged legacy job: %v", err)
	}
	if status != "dead_letter" {
		t.Fatalf("legacy job status = %q, want dead_letter", status)
	}
	if !strings.Contains(lastError, "legacy task dispatch without execution ownership") {
		t.Fatalf("legacy job last_error = %q, want purge marker", lastError)
	}
}

func TestJobWorkerEnsureProjectContinuationMessageKeepsBootstrapContinuationWhileBootstrapActive(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-project-session-bootstrap-active",
		DisplayName: "Requeue Active Project Session Bootstrap Active",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-project-session-bootstrap-active-project",
		DisplayName:    "Requeue Active Project Session Bootstrap Active Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"active"}}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"active"}}`),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	bootstrapMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue bootstrap.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_bootstrap","auto_continue":true}`),
	})
	if err != nil {
		t.Fatalf("create stale bootstrap message: %v", err)
	}
	creatorID := uuid.New()
	taskRepo := repo.NewProjectTaskRepo(pool)
	if _, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		TaskNumber:     1,
		Title:          "Bootstrap task",
		WorkStatus:     "draft",
		CreatedByType:  "system",
		CreatedByID:    &creatorID,
	}); err != nil {
		t.Fatalf("create bootstrap draft task: %v", err)
	}
	messageID, err := worker.ensureProjectContinuationMessage(ctx, session.ID)
	if err != nil {
		t.Fatalf("ensureProjectContinuationMessage: %v", err)
	}
	if messageID == uuid.Nil {
		t.Fatal("messageID = nil, want synthesized bootstrap continuation")
	}

	var (
		sessionID  uuid.UUID
		source     string
		content    string
	)
	if err := pool.QueryRow(ctx, `
		SELECT cm.id,
		       cm.session_id,
		       COALESCE(cm.metadata->>'source', ''),
		       cm.content
		FROM chat_message cm
		WHERE cm.id = $1
		LIMIT 1
	`, messageID).Scan(&messageID, &sessionID, &source, &content); err != nil {
		t.Fatalf("query bootstrap continuation message: %v", err)
	}
	if messageID != bootstrapMessage.ID {
		t.Fatalf("message_id = %s, want existing bootstrap message %s", messageID, bootstrapMessage.ID)
	}
	if sessionID != session.ID {
		t.Fatalf("message session_id = %s, want %s", sessionID, session.ID)
	}
	if source != "project_bootstrap" {
		t.Fatalf("continuation source = %q, want project_bootstrap", source)
	}
	if !strings.Contains(content, "Continue bootstrap.") {
		t.Fatalf("continuation content = %q, want bootstrap continuation message", content)
	}
}

func TestJobWorkerEnsureProjectContinuationMessageSupersedesStalePendingContinuation(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        fmt.Sprintf("continuation-freshness-%s", strings.ToLower(uuid.NewString()[:8])),
		DisplayName: "Continuation Freshness Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           fmt.Sprintf("continuation-freshness-project-%s", strings.ToLower(uuid.NewString()[:8])),
		DisplayName:    "Continuation Freshness Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Continuation Worker",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You keep project continuations moving.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           fmt.Sprintf("continuation-freshness-template-%s", strings.ToLower(uuid.NewString()[:8])),
		DisplayName:    "Continuation Freshness Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(pool)
	olderDone, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		TaskNumber:     12,
		Title:          "Create and validate pipeline configuration files",
		WorkStatus:     "done",
		BlocksScope:    "task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create older done task: %v", err)
	}
	newerDone, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		TaskNumber:     18,
		Title:          "Validate pipeline output format and delivery",
		WorkStatus:     "done",
		BlocksScope:    "task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create newer done task: %v", err)
	}
	if _, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		TaskNumber:     19,
		Title:          "Run end-to-end pipeline integration test",
		WorkStatus:     "draft",
		BlocksScope:    "task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	}); err != nil {
		t.Fatalf("create draft task: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE project_task
		SET updated_at = CASE
			WHEN id = $1 THEN now() - interval '10 minutes'
			WHEN id = $2 THEN now()
			ELSE updated_at
		END
		WHERE id IN ($1, $2)
	`, olderDone.ID, newerDone.ID); err != nil {
		t.Fatalf("order completed tasks by updated_at: %v", err)
	}

	staleMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the active project execution now. The latest completed task was task 12.",
		Status:    "pending",
		Metadata:  json.RawMessage(fmt.Sprintf(`{"source":"project_execution_continuation","auto_continue":true,"synthetic_user_message":true,"completed_task_id":"%s"}`, olderDone.ID)),
	})
	if err != nil {
		t.Fatalf("create stale continuation message: %v", err)
	}

	messageID, err := worker.ensureProjectContinuationMessage(ctx, session.ID)
	if err != nil {
		t.Fatalf("ensureProjectContinuationMessage: %v", err)
	}
	if messageID == uuid.Nil {
		t.Fatal("messageID = nil, want fresh continuation message")
	}
	if messageID == staleMessage.ID {
		t.Fatalf("messageID = %s, want stale continuation to be superseded", messageID)
	}

	var (
		status          string
		errorMessage    string
		source          string
		completedTaskID string
		content         string
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, COALESCE(error_message, '')
		FROM chat_message
		WHERE id = $1
	`, staleMessage.ID).Scan(&status, &errorMessage); err != nil {
		t.Fatalf("query stale message: %v", err)
	}
	if status != "failed" {
		t.Fatalf("stale message status = %q, want failed", status)
	}
	if !strings.Contains(errorMessage, "superseded by newer completed project task") {
		t.Fatalf("stale message error = %q, want superseded reason", errorMessage)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(metadata->>'source', ''),
		       COALESCE(metadata->>'completed_task_id', ''),
		       content
		FROM chat_message
		WHERE id = $1
	`, messageID).Scan(&source, &completedTaskID, &content); err != nil {
		t.Fatalf("query fresh continuation message: %v", err)
	}
	if source != "project_execution_continuation" {
		t.Fatalf("fresh continuation source = %q, want project_execution_continuation", source)
	}
	if completedTaskID != newerDone.ID.String() {
		t.Fatalf("fresh continuation completed_task_id = %q, want %s", completedTaskID, newerDone.ID)
	}
	wantTaskLabel := fmt.Sprintf("latest completed task was task %d", newerDone.TaskNumber)
	if !strings.Contains(strings.ToLower(content), strings.ToLower(wantTaskLabel)) {
		t.Fatalf("fresh continuation content = %q, want %q context", content, wantTaskLabel)
	}
}

func TestJobWorkerResolveStaleTriggeredRetryMessageIDSwitchesProjectExecutionToBootstrapWhileBootstrapActive(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "resolve-stale-triggered-bootstrap-active",
		DisplayName: "Resolve Stale Triggered Bootstrap Active",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "resolve-stale-triggered-bootstrap-active-project",
		DisplayName:    "Resolve Stale Triggered Bootstrap Active Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"active"}}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"active"}}`),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	bootstrapMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue bootstrap.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_bootstrap","auto_continue":true}`),
	})
	if err != nil {
		t.Fatalf("create bootstrap message: %v", err)
	}
	badContinuationMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the active project execution now.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","auto_continue":true,"synthetic_user_message":true}`),
	})
	if err != nil {
		t.Fatalf("create bad continuation message: %v", err)
	}

	retryMessageID, err := worker.resolveStaleTriggeredRetryMessageID(ctx, session.ID, "project", badContinuationMessage.ID)
	if err != nil {
		t.Fatalf("resolveStaleTriggeredRetryMessageID: %v", err)
	}
	if retryMessageID != bootstrapMessage.ID {
		t.Fatalf("retryMessageID = %s, want bootstrap message %s", retryMessageID, bootstrapMessage.ID)
	}
}

func TestJobWorkerClearCompletedSessionCurrentTurnsEnablesRequeue(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "clear-completed-session-current-turns",
		DisplayName: "Clear Completed Session Current Turns",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Cleanup Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover stranded task sessions.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "clear-completed-current-turn-project",
		DisplayName:    "Clear Completed Current Turn Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "clear-completed-current-turn-template",
		DisplayName:    "Clear Completed Current Turn Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover failed current turn leak",
		WorkStatus:      "in_progress",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"current turn leaked after failure"}`),
	})
	if err != nil {
		t.Fatalf("create supervisor kickoff message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "failed",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create failed turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	}); err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}

	cleared, err := worker.ClearCompletedSessionCurrentTurns(ctx)
	if err != nil {
		t.Fatalf("ClearCompletedSessionCurrentTurns: %v", err)
	}
	if cleared != 1 {
		t.Fatalf("cleared sessions = %d, want 1", cleared)
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", *refreshedSession.CurrentTurnID)
	}

	requeued, err := worker.RequeueActiveExecutionSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveExecutionSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued sessions = %d, want 1", requeued)
	}
}

func TestJobWorkerRequeueActiveExecutionSessionsWithoutTurnsSkipsRecoveryHaltLoop(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-execution-skip-halted-recovery",
		DisplayName: "Requeue Active Execution Skip Halted Recovery",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-execution-skip-halted-recovery-project",
		DisplayName:    "Requeue Active Execution Skip Halted Recovery Project",
		Description:    "Project for halted recovery requeue suppression",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-active-execution-skip-halted-recovery-template",
		DisplayName:    "Requeue Active Execution Skip Halted Recovery Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         project.ID,
		Title:             "Skip halted recovery requeue",
		WorkStatus:        "review",
		BlocksScope:       "task",
		CurrentFlowNodeID: &flowNode.ID,
		FlowTemplateID:    &template.ID,
		CreatedByType:     "system",
		CreatedByID:       &agent.ID,
		AssignedAgentID:   &agent.ID,
		Metadata:          json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create supervisor kickoff message: %v", err)
	}
	stopReason := "recovery_content_required"
	completedTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		StopReason:       &stopReason,
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create completed halted recovery turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}
	if _, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	}); err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}
	if completedTurn.TriggerMessageID == nil || *completedTurn.TriggerMessageID != message.ID {
		t.Fatalf("completed halted turn trigger_message_id = %v, want %s", completedTurn.TriggerMessageID, message.ID)
	}

	requeued, err := worker.RequeueActiveExecutionSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveExecutionSessionsWithoutTurns: %v", err)
	}
	if requeued != 0 {
		t.Fatalf("requeued sessions = %d, want 0 for halted recovery loop", requeued)
	}
}

func TestJobWorkerRequeueActiveExecutionSessionsWithoutTurnsAllowsReviewRecoveryHaltRetry(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-execution-review-halt-retry",
		DisplayName: "Requeue Active Execution Review Halt Retry",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Review Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You review pending work.",
		AgentType:       "reviewer",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-execution-review-halt-retry-project",
		DisplayName:    "Requeue Active Execution Review Halt Retry Project",
		Description:    "Project for review recovery requeue exception",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-active-execution-review-halt-retry-template",
		DisplayName:    "Requeue Active Execution Review Halt Retry Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Internal Review",
		NodeType:       "review",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         project.ID,
		Title:             "Review lane should retry after halted recovery mutation",
		WorkStatus:        "review",
		BlocksScope:       "task",
		CurrentFlowNodeID: &flowNode.ID,
		FlowTemplateID:    &template.ID,
		CreatedByType:     "system",
		CreatedByID:       &agent.ID,
		AssignedAgentID:   &agent.ID,
		Metadata:          json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Review only. Use flow.review_decision.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"task_queue_processor","flow_node_execution_id":"00000000-0000-0000-0000-000000000000"}`),
	})
	if err != nil {
		t.Fatalf("create review kickoff message: %v", err)
	}
	stopReason := "recovery_content_required"
	if _, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		StopReason:       &stopReason,
		TriggerMessageID: &message.ID,
	}); err != nil {
		t.Fatalf("create completed halted review turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}
	messageMetadata := map[string]any{
		"source":                 "task_queue_processor",
		"flow_node_execution_id": execution.ID.String(),
	}
	encodedMessageMetadata, err := json.Marshal(messageMetadata)
	if err != nil {
		t.Fatalf("marshal review kickoff metadata: %v", err)
	}
	if _, err := repo.NewChatMessageRepo(pool).UpdateMetadata(ctx, message.ID, encodedMessageMetadata); err != nil {
		t.Fatalf("update review kickoff metadata: %v", err)
	}

	requeued, err := worker.RequeueActiveExecutionSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveExecutionSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued sessions = %d, want 1 for halted review recovery", requeued)
	}
}

func TestJobWorkerRequeueActiveExecutionSessionsWithoutTurnsAllowsSyntheticReviewRecoveryRetry(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-execution-synthetic-review-recovery",
		DisplayName: "Requeue Active Execution Synthetic Review Recovery",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Synthetic Review Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You review work.",
		AgentType:       "reviewer",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-execution-synthetic-review-recovery-project",
		DisplayName:    "Requeue Active Execution Synthetic Review Recovery Project",
		Description:    "Project for synthetic review recovery requeue coverage",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-active-execution-synthetic-review-recovery-template",
		DisplayName:    "Requeue Active Execution Synthetic Review Recovery Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Internal Review",
		NodeType:       "review",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         project.ID,
		Title:             "Synthetic review recovery should retry",
		WorkStatus:        "review",
		BlocksScope:       "task",
		CurrentFlowNodeID: &flowNode.ID,
		FlowTemplateID:    &template.ID,
		CreatedByType:     "system",
		CreatedByID:       &agent.ID,
		AssignedAgentID:   &agent.ID,
		Metadata:          json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}
	messageMetadata := json.RawMessage(fmt.Sprintf(`{"source":"task_recovery_resume","synthetic_user_message":true,"flow_node_execution_id":"%s"}`, execution.ID))
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the active task recovery now.",
		Status:    "pending",
		Metadata:  messageMetadata,
	})
	if err != nil {
		t.Fatalf("create synthetic recovery message: %v", err)
	}
	stopReason := "recovery_content_required"
	if _, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		StopReason:       &stopReason,
		TriggerMessageID: &message.ID,
	}); err != nil {
		t.Fatalf("create completed halted synthetic review turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}

	requeued, err := worker.RequeueActiveExecutionSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveExecutionSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued sessions = %d, want 1 for synthetic halted review recovery", requeued)
	}
}

func TestJobWorkerRequeueActiveExecutionSessionsWithoutTurnsAllowsTaskReviewActionRetry(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-execution-task-review-action",
		DisplayName: "Requeue Active Execution Task Review Action",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Review Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You review work.",
		AgentType:       "reviewer",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-execution-task-review-action-project",
		DisplayName:    "Requeue Active Execution Task Review Action Project",
		Description:    "Project for task_review_action requeue coverage",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-active-execution-task-review-action-template",
		DisplayName:    "Requeue Active Execution Task Review Action Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Internal Review",
		NodeType:       "review",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    org.ID,
		ProjectID:         project.ID,
		Title:             "Task review action should retry",
		WorkStatus:        "review",
		BlocksScope:       "task",
		CurrentFlowNodeID: &flowNode.ID,
		FlowTemplateID:    &template.ID,
		CreatedByType:     "system",
		CreatedByID:       &agent.ID,
		AssignedAgentID:   &agent.ID,
		Metadata:          json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}
	messageMetadata := json.RawMessage(fmt.Sprintf(`{"source":"task_review_action","synthetic_user_message":true,"flow_node_execution_id":"%s"}`, execution.ID))
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Review only. Use flow.review_decision.",
		Status:    "pending",
		Metadata:  messageMetadata,
	})
	if err != nil {
		t.Fatalf("create task review action message: %v", err)
	}
	stopReason := "recovery_content_required"
	if _, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		StopReason:       &stopReason,
		TriggerMessageID: &message.ID,
	}); err != nil {
		t.Fatalf("create completed halted task review action turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}

	requeued, err := worker.RequeueActiveExecutionSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveExecutionSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued sessions = %d, want 1 for task_review_action retry", requeued)
	}
}

func TestJobWorkerRequeueActiveExecutionSessionsWithoutTurnsSkipsLogicallyCancelledLatestMessage(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-execution-skip-cancelled-latest",
		DisplayName: "Requeue Active Execution Skip Cancelled Latest",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-execution-skip-cancelled-latest-project",
		DisplayName:    "Requeue Active Execution Skip Cancelled Latest Project",
		Description:    "Project for cancelled-latest message suppression",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-active-execution-skip-cancelled-latest-template",
		DisplayName:    "Requeue Active Execution Skip Cancelled Latest Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Skip logically cancelled latest message",
		WorkStatus:      "draft",
		BlocksScope:     "task",
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}
	taskQueueMetadata := json.RawMessage(fmt.Sprintf(`{"source":"task_queue_processor","flow_node_execution_id":"%s","flow_event_type":"flow.advanced"}`, execution.ID))
	taskQueueMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "task queue wakeup",
		Status:    "pending",
		Metadata:  taskQueueMetadata,
	})
	if err != nil {
		t.Fatalf("create task queue kickoff message: %v", err)
	}
	if _, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &taskQueueMessage.ID,
	}); err != nil {
		t.Fatalf("create completed task queue turn: %v", err)
	}
	cancelledMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(fmt.Sprintf(`{"source":"supervisor","reason":"active execution lost live task turn","flow_node_execution_id":"%s"}`, execution.ID)),
	})
	if err != nil {
		t.Fatalf("create cancelled supervisor message: %v", err)
	}
	if _, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       2,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "cancelled",
		TriggerMessageID: &cancelledMessage.ID,
	}); err != nil {
		t.Fatalf("create cancelled supervisor turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}

	requeued, err := worker.RequeueActiveExecutionSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveExecutionSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued sessions = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
		retryCount     int
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID, &retryCount); err != nil {
		t.Fatalf("query requeued active execution job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != taskQueueMessage.ID {
		t.Fatalf("requeued message_id = %s, want task queue message %s", requeuedMsgID, taskQueueMessage.ID)
	}
	if retryCount != 1 {
		t.Fatalf("requeued retry_count = %d, want 1", retryCount)
	}
}

func TestJobWorkerFailStaleModelInvocationsFailsOrphanedLiveTurnsWithoutClaimedJob(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "fail-stale-model-invocations-orphaned-live-turn",
		DisplayName: "Fail Stale Model Invocations Orphaned Live Turn",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "fail-stale-model-invocations-orphaned-live-turn-project",
		DisplayName:    "Fail Stale Model Invocations Orphaned Live Turn Project",
		Description:    "Project for stale in-flight invocation cleanup",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "fail-stale-model-invocations-template",
		DisplayName:    "Fail Stale Model Invocations Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Fail orphaned live in-flight invocation",
		WorkStatus:      "in_progress",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "task queue wakeup",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"task_queue_processor"}`),
	})
	if err != nil {
		t.Fatalf("create wakeup message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create in-progress turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '1 minute'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age live turn: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "fail-stale-model-invocations-provider",
		DisplayName: "Fail Stale Model Invocations Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
	})
	if err != nil {
		t.Fatalf("create in-flight invocation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE model_invocation
		SET created_at = now() - interval '3 minutes'
		WHERE id = $1
	`, invocation.ID); err != nil {
		t.Fatalf("age in-flight invocation: %v", err)
	}
	runID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO run (
			id, organization_id, project_id, task_id, flow_node_id, session_id, turn_id,
			principal_type, principal_id, status, trigger_type, version, metadata, started_at
		) VALUES (
			$1, $2, $3, $4, NULL, $5, NULL,
			'agent', $6, 'in_progress', 'scheduler', 1, '{}'::jsonb, now() - interval '1 minute'
		)
	`, runID, org.ID, project.ID, taskRecord.ID, session.ID, agent.ID); err != nil {
		t.Fatalf("create stale run: %v", err)
	}

	repaired, err := worker.FailStaleModelInvocations(ctx)
	if err != nil {
		t.Fatalf("FailStaleModelInvocations: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired invocations = %d, want 1", repaired)
	}

	storedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if storedInvocation.Status != "failed" {
		t.Fatalf("invocation status = %q, want failed", storedInvocation.Status)
	}
	if storedInvocation.ErrorCode == nil || strings.TrimSpace(*storedInvocation.ErrorCode) != "stale_model_invocation" {
		t.Fatalf("invocation error_code = %v, want stale_model_invocation", storedInvocation.ErrorCode)
	}
	if storedInvocation.CompletedAt == nil {
		t.Fatal("invocation completed_at = nil, want set")
	}
	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("turn status = %q, want failed", storedTurn.Status)
	}
	if storedTurn.CompletedAt == nil {
		t.Fatal("turn completed_at = nil, want set")
	}
	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", refreshedSession.CurrentTurnID)
	}
	var runStatus string
	var runFailureClass, runFailureReason *string
	var runCompletedAt *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT status, failure_class, failure_reason, completed_at
		FROM run
		WHERE id = $1
	`, runID).Scan(&runStatus, &runFailureClass, &runFailureReason, &runCompletedAt); err != nil {
		t.Fatalf("reload stale run: %v", err)
	}
	if runStatus != "failed" {
		t.Fatalf("run status = %q, want failed", runStatus)
	}
	if runFailureClass == nil || *runFailureClass != "permanent" {
		t.Fatalf("run failure_class = %v, want permanent", runFailureClass)
	}
	const wantFailureReason = "worker cleanup failed stale in_flight model invocation without live in-progress turn"
	if runFailureReason == nil || *runFailureReason != wantFailureReason {
		t.Fatalf("run failure_reason = %v, want %q", runFailureReason, wantFailureReason)
	}
	if runCompletedAt == nil {
		t.Fatal("run completed_at = nil, want set")
	}
}

func TestJobWorkerFailStaleModelInvocationsSkipsActiveExecutionTaskSession(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "skip-stale-model-invocations-active-execution",
		DisplayName: "Skip Stale Model Invocations Active Execution",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Execution Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You execute active task work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "skip-stale-model-invocations-active-execution-project",
		DisplayName:    "Skip Stale Model Invocations Active Execution Project",
		Description:    "Project for active execution stale invocation coverage",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "skip-stale-model-invocations-active-execution-template",
		DisplayName:    "Skip Stale Model Invocations Active Execution Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Skip active execution stale invocation cleanup",
		WorkStatus:      "in_progress",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
		Metadata:    json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow execution: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "task queue wakeup",
		Status:    "pending",
		Metadata:  json.RawMessage(fmt.Sprintf(`{"flow_node_execution_id":"%s","source":"task_queue_processor"}`, execution.ID)),
	})
	if err != nil {
		t.Fatalf("create wakeup message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create in-progress turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '1 minute'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age live turn: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "skip-stale-model-invocations-active-execution-provider",
		DisplayName: "Skip Stale Model Invocations Active Execution Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
	})
	if err != nil {
		t.Fatalf("create in-flight invocation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE model_invocation
		SET created_at = now() - interval '1 minute'
		WHERE id = $1
	`, invocation.ID); err != nil {
		t.Fatalf("age in-flight invocation: %v", err)
	}

	repaired, err := worker.FailStaleModelInvocations(ctx)
	if err != nil {
		t.Fatalf("FailStaleModelInvocations: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("repaired invocations = %d, want 0", repaired)
	}

	storedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if storedInvocation.Status != "in_flight" {
		t.Fatalf("invocation status = %q, want in_flight", storedInvocation.Status)
	}
	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload turn: %v", err)
	}
	if storedTurn.Status != "in_progress" {
		t.Fatalf("turn status = %q, want in_progress", storedTurn.Status)
	}
	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID == nil || *refreshedSession.CurrentTurnID != turn.ID {
		t.Fatalf("current_turn_id = %v, want %s", refreshedSession.CurrentTurnID, turn.ID)
	}
}

func TestJobWorkerFailStaleModelInvocationsSkipsActiveAsyncOrganizationSession(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})
	worker.startupAt = time.Now().UTC().Add(-10 * time.Minute)

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "skip-stale-model-invocations-active-async-org",
		DisplayName: "Skip Stale Model Invocations Active Async Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Org Continuation Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You continue async organization requests.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "create a new project now",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '1 minute'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age live turn: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "skip-stale-model-invocations-active-async-org-provider",
		DisplayName: "Skip Stale Model Invocations Active Async Org Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
	})
	if err != nil {
		t.Fatalf("create in-flight invocation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE model_invocation
		SET created_at = now() - interval '1 minute'
		WHERE id = $1
	`, invocation.ID); err != nil {
		t.Fatalf("age in-flight invocation: %v", err)
	}

	repaired, err := worker.FailStaleModelInvocations(ctx)
	if err != nil {
		t.Fatalf("FailStaleModelInvocations: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("repaired invocations = %d, want 0", repaired)
	}

	storedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if storedInvocation.Status != "in_flight" {
		t.Fatalf("invocation status = %q, want in_flight", storedInvocation.Status)
	}
	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload turn: %v", err)
	}
	if storedTurn.Status != "in_progress" {
		t.Fatalf("turn status = %q, want in_progress", storedTurn.Status)
	}
	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID == nil || *refreshedSession.CurrentTurnID != turn.ID {
		t.Fatalf("current_turn_id = %v, want %s", refreshedSession.CurrentTurnID, turn.ID)
	}
}

func TestJobWorkerFailStaleModelInvocationsFailsOrphanedAsyncProjectSessionInvocationWithoutTurn(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "fail-stale-project-session-orphaned-invocation",
		DisplayName: "Fail Stale Project Session Orphaned Invocation",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Continuation Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover project continuations.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "fail-stale-project-session-orphaned-invocation-project",
		DisplayName:    "Fail Stale Project Session Orphaned Invocation Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"active"}}`),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	invocationProvider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "fail-stale-project-session-orphaned-invocation-provider",
		DisplayName: "Fail Stale Project Session Orphaned Invocation Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   invocationProvider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		SessionID:         &session.ID,
	})
	if err != nil {
		t.Fatalf("create orphaned project invocation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE model_invocation
		SET created_at = now() - interval '20 minutes'
		WHERE id = $1
	`, invocation.ID); err != nil {
		t.Fatalf("age orphaned project invocation: %v", err)
	}

	repaired, err := worker.FailStaleModelInvocations(ctx)
	if err != nil {
		t.Fatalf("FailStaleModelInvocations: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired invocations = %d, want 1", repaired)
	}

	storedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if storedInvocation.Status != "failed" {
		t.Fatalf("invocation status = %q, want failed", storedInvocation.Status)
	}
	if storedInvocation.ErrorCode == nil || strings.TrimSpace(*storedInvocation.ErrorCode) != "stale_model_invocation" {
		t.Fatalf("invocation error_code = %v, want stale_model_invocation", storedInvocation.ErrorCode)
	}
}

func TestJobWorkerFailStaleModelInvocationsFailsDetachedAsyncProjectSessionInvocationWithStaleTurn(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "fail-stale-project-session-detached-turn-invocation",
		DisplayName: "Fail Stale Project Session Detached Turn Invocation",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Continuation Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover project continuations.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "fail-stale-project-session-detached-turn-invocation-project",
		DisplayName:    "Fail Stale Project Session Detached Turn Invocation Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue orchestrating the project",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","synthetic_user_message":true}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create stale detached turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '20 minutes'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age stale detached turn: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "fail-stale-project-session-detached-turn-invocation-provider",
		DisplayName: "Fail Stale Project Session Detached Turn Invocation Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
	})
	if err != nil {
		t.Fatalf("create detached project invocation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE model_invocation
		SET created_at = now() - interval '20 minutes'
		WHERE id = $1
	`, invocation.ID); err != nil {
		t.Fatalf("age detached project invocation: %v", err)
	}
	if _, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "pending",
	}); err != nil {
		t.Fatalf("create pending assistant: %v", err)
	}

	repaired, err := worker.FailStaleModelInvocations(ctx)
	if err != nil {
		t.Fatalf("FailStaleModelInvocations: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired invocations = %d, want 1", repaired)
	}

	storedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if storedInvocation.Status != "failed" {
		t.Fatalf("invocation status = %q, want failed", storedInvocation.Status)
	}
	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("turn status = %q, want failed", storedTurn.Status)
	}
}

func TestJobWorkerFailStaleModelInvocationsFailsOldActiveExecutionTaskSession(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "fail-old-stale-model-invocations-active-execution",
		DisplayName: "Fail Old Stale Model Invocations Active Execution",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Execution Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You execute active task work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "fail-old-stale-model-invocations-active-execution-project",
		DisplayName:    "Fail Old Stale Model Invocations Active Execution Project",
		Description:    "Project for old active execution stale invocation coverage",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "fail-old-stale-model-invocations-active-execution-template",
		DisplayName:    "Fail Old Stale Model Invocations Active Execution Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Fail old active execution stale invocation cleanup",
		WorkStatus:      "in_progress",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
		Metadata:    json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow execution: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "task queue wakeup",
		Status:    "pending",
		Metadata:  json.RawMessage(fmt.Sprintf(`{"flow_node_execution_id":"%s","source":"task_queue_processor"}`, execution.ID)),
	})
	if err != nil {
		t.Fatalf("create wakeup message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create in-progress turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '20 minutes'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age live turn: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "fail-old-stale-model-invocations-active-execution-provider",
		DisplayName: "Fail Old Stale Model Invocations Active Execution Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
	})
	if err != nil {
		t.Fatalf("create in-flight invocation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE model_invocation
		SET created_at = now() - interval '20 minutes'
		WHERE id = $1
	`, invocation.ID); err != nil {
		t.Fatalf("age in-flight invocation: %v", err)
	}

	repaired, err := worker.FailStaleModelInvocations(ctx)
	if err != nil {
		t.Fatalf("FailStaleModelInvocations: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired invocations = %d, want 1", repaired)
	}

	storedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if storedInvocation.Status != "failed" {
		t.Fatalf("invocation status = %q, want failed", storedInvocation.Status)
	}
	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("turn status = %q, want failed", storedTurn.Status)
	}
}

func TestJobWorkerFailStaleModelInvocationsKeepsSharedLiveRunForNewerTurn(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "keep-shared-live-run-for-newer-turn",
		DisplayName: "Keep Shared Live Run For Newer Turn",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Execution Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You execute active task work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "keep-shared-live-run-for-newer-turn-project",
		DisplayName:    "Keep Shared Live Run For Newer Turn Project",
		Description:    "Project for shared live run stale invocation coverage",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "keep-shared-live-run-for-newer-turn-template",
		DisplayName:    "Keep Shared Live Run For Newer Turn Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       1,
		MaxVisits:      2,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Keep shared live run for newer turn",
		WorkStatus:      "review",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
		Metadata:    json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow execution: %v", err)
	}
	oldMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "stale review retry",
		Status:    "pending",
		Metadata:  json.RawMessage(fmt.Sprintf(`{"flow_node_execution_id":"%s","source":"supervisor"}`, execution.ID)),
	})
	if err != nil {
		t.Fatalf("create old message: %v", err)
	}
	oldTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &oldMessage.ID,
	})
	if err != nil {
		t.Fatalf("create old turn: %v", err)
	}
	newMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "fresh review retry",
		Status:    "pending",
		Metadata:  json.RawMessage(fmt.Sprintf(`{"flow_node_execution_id":"%s","source":"supervisor"}`, execution.ID)),
	})
	if err != nil {
		t.Fatalf("create new message: %v", err)
	}
	newTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       2,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &newMessage.ID,
	})
	if err != nil {
		t.Fatalf("create new turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &newTurn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = CASE
			WHEN id = $1 THEN now() - interval '31 minutes'
			WHEN id = $2 THEN now() - interval '30 seconds'
			ELSE started_at
		END
		WHERE id IN ($1, $2)
	`, oldTurn.ID, newTurn.ID); err != nil {
		t.Fatalf("age turns: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "keep-shared-live-run-for-newer-turn-provider",
		DisplayName: "Keep Shared Live Run For Newer Turn Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	runID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO run (
			id, organization_id, project_id, task_id, flow_node_id, session_id, turn_id,
			principal_type, principal_id, status, trigger_type, version, metadata, started_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, NULL,
			'agent', $7, 'in_progress', 'scheduler', 1, '{}'::jsonb, now() - interval '5 minutes'
		)
	`, runID, org.ID, project.ID, taskRecord.ID, flowNode.ID, session.ID, agent.ID); err != nil {
		t.Fatalf("create shared run: %v", err)
	}
	oldInvocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &oldTurn.ID,
		RunID:             &runID,
	})
	if err != nil {
		t.Fatalf("create old invocation: %v", err)
	}
	newInvocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &newTurn.ID,
		RunID:             &runID,
	})
	if err != nil {
		t.Fatalf("create new invocation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE model_invocation
		SET created_at = CASE
			WHEN id = $1 THEN now() - interval '31 minutes'
			WHEN id = $2 THEN now() - interval '30 seconds'
			ELSE created_at
		END
		WHERE id IN ($1, $2)
	`, oldInvocation.ID, newInvocation.ID); err != nil {
		t.Fatalf("age invocations: %v", err)
	}

	repaired, err := worker.FailStaleModelInvocations(ctx)
	if err != nil {
		t.Fatalf("FailStaleModelInvocations: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired invocations = %d, want 1", repaired)
	}

	storedOldInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, oldInvocation.ID)
	if err != nil {
		t.Fatalf("reload old invocation: %v", err)
	}
	if storedOldInvocation.Status != "failed" {
		t.Fatalf("old invocation status = %q, want failed", storedOldInvocation.Status)
	}
	storedNewInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, newInvocation.ID)
	if err != nil {
		t.Fatalf("reload new invocation: %v", err)
	}
	if storedNewInvocation.Status != "in_flight" {
		t.Fatalf("new invocation status = %q, want in_flight", storedNewInvocation.Status)
	}

	storedOldTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, oldTurn.ID)
	if err != nil {
		t.Fatalf("reload old turn: %v", err)
	}
	if storedOldTurn.Status != "failed" {
		t.Fatalf("old turn status = %q, want failed", storedOldTurn.Status)
	}
	storedNewTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, newTurn.ID)
	if err != nil {
		t.Fatalf("reload new turn: %v", err)
	}
	if storedNewTurn.Status != "in_progress" {
		t.Fatalf("new turn status = %q, want in_progress", storedNewTurn.Status)
	}

	var runStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM run WHERE id = $1`, runID).Scan(&runStatus); err != nil {
		t.Fatalf("reload shared run: %v", err)
	}
	if runStatus != "in_progress" {
		t.Fatalf("shared run status = %q, want in_progress", runStatus)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsFailsPostModelOrphanedExecutionTurn(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-post-model-orphaned-triggered-turn",
		DisplayName: "Recover Post Model Orphaned Triggered Turn",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Execution Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You execute active task work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-post-model-orphaned-triggered-turn-project",
		DisplayName:    "Recover Post Model Orphaned Triggered Turn Project",
		Description:    "Project for post-model orphaned triggered turn recovery",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "recover-post-model-orphaned-triggered-turn-template",
		DisplayName:    "Recover Post Model Orphaned Triggered Turn Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover post-model orphaned triggered turn",
		WorkStatus:      "in_progress",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
		Metadata:    json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow execution: %v", err)
	}
	triggerMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "task queue wakeup",
		Status:    "pending",
		Metadata:  json.RawMessage(fmt.Sprintf(`{"flow_node_execution_id":"%s","source":"task_queue_processor"}`, execution.ID)),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &triggerMessage.ID,
	})
	if err != nil {
		t.Fatalf("create in-progress turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '1 minute'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age live turn: %v", err)
	}
	if _, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Content:   "stuck streaming output",
		Status:    "streaming",
	}); err != nil {
		t.Fatalf("create streaming assistant message: %v", err)
	}

	runID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO run (
			id, organization_id, project_id, task_id, flow_node_id, session_id, turn_id,
			principal_type, principal_id, status, trigger_type, version, metadata, started_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			'agent', $8, 'in_progress', 'scheduler', 1, $9::jsonb, $10
		)
	`, runID, org.ID, project.ID, taskRecord.ID, flowNode.ID, session.ID, turn.ID, agent.ID, fmt.Sprintf(`{"flow_node_execution_id":"%s"}`, execution.ID), time.Now().Add(-1*time.Minute)); err != nil {
		t.Fatalf("create run: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-post-model-orphaned-triggered-turn-provider",
		DisplayName: "Recover Post Model Orphaned Triggered Turn Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	completedAt := time.Now().Add(-1 * time.Minute)
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "completed",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
		CompletedAt:       &completedAt,
	})
	if err != nil {
		t.Fatalf("create completed invocation: %v", err)
	}
	if invocation.CompletedAt == nil {
		t.Fatal("completed invocation missing completed_at")
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired turns = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("turn status = %q, want failed", storedTurn.Status)
	}
	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", refreshedSession.CurrentTurnID)
	}
	var runStatus string
	var runCompletedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, completed_at FROM run WHERE id = $1`, runID).Scan(&runStatus, &runCompletedAt); err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if runStatus != "failed" {
		t.Fatalf("run status = %q, want failed", runStatus)
	}
	if runCompletedAt == nil {
		t.Fatal("run completed_at = nil, want set")
	}

	var retryJobCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status = 'pending'
		  AND (payload->>'session_id')::uuid = $1
		  AND (payload->>'message_id')::uuid = $2
	`, session.ID, triggerMessage.ID).Scan(&retryJobCount); err != nil {
		t.Fatalf("count retry jobs: %v", err)
	}
	if retryJobCount != 1 {
		t.Fatalf("retry jobs = %d, want 1", retryJobCount)
	}
}

func TestJobWorkerRecoverStaleInProgressContinuationTurns(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-continuation-turns",
		DisplayName: "Recover Stale Continuation Turns",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Continuation Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover leaked continuation turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-continuation-project",
		DisplayName:    "Recover Stale Continuation Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "recover-stale-continuation-template",
		DisplayName:    "Recover Stale Continuation Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover stale continuation turn",
		WorkStatus:      "draft",
		BlocksScope:     "task",
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	rootMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the task from the compressed context.",
		Status:    "final",
	})
	if err != nil {
		t.Fatalf("create root message: %v", err)
	}
	cycleID := uuid.New()
	rootTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		CycleID:          &cycleID,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &rootMessage.ID,
	})
	if err != nil {
		t.Fatalf("create root turn: %v", err)
	}
	continuationTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     2,
		CycleID:        &cycleID,
		RespondingType: "agent",
		RespondingID:   agent.ID,
		Status:         "in_progress",
	})
	if err != nil {
		t.Fatalf("create continuation turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &continuationTurn.ID); err != nil {
		t.Fatalf("set current continuation turn: %v", err)
	}
	if _, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	}); err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '6 minutes'
		WHERE id = $1
	`, continuationTurn.ID); err != nil {
		t.Fatalf("age continuation turn: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-stale-continuation-provider",
		DisplayName: "Recover Stale Continuation Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &continuationTurn.ID,
	})
	if err != nil {
		t.Fatalf("create in-flight invocation: %v", err)
	}
	assistantMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &continuationTurn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "streaming",
	})
	if err != nil {
		t.Fatalf("create streaming assistant message: %v", err)
	}
	if rootTurn.TriggerMessageID == nil || *rootTurn.TriggerMessageID != rootMessage.ID {
		t.Fatalf("root turn trigger_message_id = %v, want %s", rootTurn.TriggerMessageID, rootMessage.ID)
	}

	repaired, err := worker.RecoverStaleInProgressContinuationTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressContinuationTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired continuations = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, continuationTurn.ID)
	if err != nil {
		t.Fatalf("reload continuation turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("continuation turn status = %q, want failed", storedTurn.Status)
	}
	if storedTurn.CompletedAt == nil {
		t.Fatalf("continuation turn completed_at = nil, want set")
	}
	if storedTurn.ErrorMessage == nil || *storedTurn.ErrorMessage == "" {
		t.Fatalf("continuation turn error_message = %v, want non-empty", storedTurn.ErrorMessage)
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", *refreshedSession.CurrentTurnID)
	}

	refreshedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if refreshedInvocation.Status != "failed" {
		t.Fatalf("invocation status = %q, want failed", refreshedInvocation.Status)
	}
	if refreshedInvocation.ErrorCode == nil || *refreshedInvocation.ErrorCode != "stale_turn_recovered" {
		t.Fatalf("invocation error_code = %v, want stale_turn_recovered", refreshedInvocation.ErrorCode)
	}
	if refreshedInvocation.CompletedAt == nil {
		t.Fatalf("invocation completed_at = nil, want set")
	}

	refreshedAssistant, err := repo.NewChatMessageRepo(pool).GetByID(ctx, assistantMessage.ID)
	if err != nil {
		t.Fatalf("reload assistant message: %v", err)
	}
	if refreshedAssistant.Status != "failed" {
		t.Fatalf("assistant message status = %q, want failed", refreshedAssistant.Status)
	}
	if refreshedAssistant.ErrorMessage == nil || *refreshedAssistant.ErrorMessage == "" {
		t.Fatalf("assistant message error_message = %v, want non-empty", refreshedAssistant.ErrorMessage)
	}

	var (
		jobStatus     string
		requeuedMsgID uuid.UUID
		requeuedSess  uuid.UUID
		retryCount    int
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&jobStatus, &requeuedMsgID, &requeuedSess, &retryCount); err != nil {
		t.Fatalf("query requeued continuation retry job: %v", err)
	}
	if jobStatus != "pending" {
		t.Fatalf("requeued continuation job status = %q, want pending", jobStatus)
	}
	if requeuedSess != session.ID {
		t.Fatalf("requeued continuation session_id = %s, want %s", requeuedSess, session.ID)
	}
	if requeuedMsgID != rootMessage.ID {
		t.Fatalf("requeued continuation message_id = %s, want %s", requeuedMsgID, rootMessage.ID)
	}
	if retryCount != 1 {
		t.Fatalf("requeued continuation retry_count = %d, want 1", retryCount)
	}
}

func TestJobWorkerRecoverStaleInProgressContinuationTurnsUsesExecutionMetadataLiveTurn(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-continuation-live-owner",
		DisplayName: "Recover Stale Continuation Live Owner",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Continuation Live Owner Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover leaked continuation turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-continuation-live-owner-project",
		DisplayName:    "Recover Stale Continuation Live Owner Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "recover-stale-continuation-live-owner-template",
		DisplayName:    "Recover Stale Continuation Live Owner Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover stale continuation turn from execution owner metadata",
		WorkStatus:      "in_progress",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	rootMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the task from the compressed context.",
		Status:    "final",
	})
	if err != nil {
		t.Fatalf("create root message: %v", err)
	}
	cycleID := uuid.New()
	rootTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		CycleID:          &cycleID,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &rootMessage.ID,
	})
	if err != nil {
		t.Fatalf("create root turn: %v", err)
	}
	continuationTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     2,
		CycleID:        &cycleID,
		RespondingType: "agent",
		RespondingID:   agent.ID,
		Status:         "in_progress",
	})
	if err != nil {
		t.Fatalf("create continuation turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '6 minutes'
		WHERE id = $1
	`, continuationTurn.ID); err != nil {
		t.Fatalf("age continuation turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current continuation turn: %v", err)
	}

	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}
	metadata := repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{TurnID: &continuationTurn.ID})
	if _, err := repo.NewFlowNodeExecutionRepo(pool).UpdateMetadata(ctx, execution.ID, metadata); err != nil {
		t.Fatalf("set live turn metadata: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-stale-continuation-live-owner-provider",
		DisplayName: "Recover Stale Continuation Live Owner Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &continuationTurn.ID,
	})
	if err != nil {
		t.Fatalf("create in-flight invocation: %v", err)
	}
	assistantMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &continuationTurn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "streaming",
	})
	if err != nil {
		t.Fatalf("create streaming assistant message: %v", err)
	}
	if rootTurn.TriggerMessageID == nil || *rootTurn.TriggerMessageID != rootMessage.ID {
		t.Fatalf("root turn trigger_message_id = %v, want %s", rootTurn.TriggerMessageID, rootMessage.ID)
	}

	repaired, err := worker.RecoverStaleInProgressContinuationTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressContinuationTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired continuations = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, continuationTurn.ID)
	if err != nil {
		t.Fatalf("reload continuation turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("continuation turn status = %q, want failed", storedTurn.Status)
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", refreshedSession.CurrentTurnID)
	}

	refreshedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if refreshedInvocation.Status != "failed" {
		t.Fatalf("invocation status = %q, want failed", refreshedInvocation.Status)
	}

	refreshedAssistant, err := repo.NewChatMessageRepo(pool).GetByID(ctx, assistantMessage.ID)
	if err != nil {
		t.Fatalf("reload assistant message: %v", err)
	}
	if refreshedAssistant.Status != "failed" {
		t.Fatalf("assistant message status = %q, want failed", refreshedAssistant.Status)
	}
}

func TestJobWorkerRecoverStaleInProgressContinuationTurnsKeepsQueuedRetry(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-continuation-queued-retry-org",
		DisplayName: "Recover Stale Continuation Queued Retry Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Continuation Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover leaked continuation turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-continuation-queued-retry-project",
		DisplayName:    "Recover Stale Continuation Queued Retry Project",
		Description:    "Project for stale continuation retry reuse",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "recover-stale-continuation-queued-retry-template",
		DisplayName:    "Recover Stale Continuation Queued Retry Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover stale continuation turn with queued retry",
		WorkStatus:      "in_progress",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue task work from the last checkpoint.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	completedAt := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &message.ID,
		CompletedAt:      &completedAt,
	}); err != nil {
		t.Fatalf("create completed prior turn: %v", err)
	}
	startedAt := time.Now().UTC().Add(-1 * time.Hour)
	continuationTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     2,
		RespondingType: "agent",
		RespondingID:   agent.ID,
		Status:         "in_progress",
		StartedAt:      &startedAt,
	})
	if err != nil {
		t.Fatalf("create continuation turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &continuationTurn.ID); err != nil {
		t.Fatalf("set current continuation turn: %v", err)
	}
	if _, err := worker.Enqueue(ctx, nil, agentTurnJobType, 70, agentTurnKeyPayload{
		SessionID:  session.ID,
		MessageID:  message.ID,
		RetryCount: 1,
	}, nil); err != nil {
		t.Fatalf("enqueue queued retry job: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressContinuationTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressContinuationTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired continuation turns = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, continuationTurn.ID)
	if err != nil {
		t.Fatalf("reload continuation turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("continuation turn status = %q, want failed", storedTurn.Status)
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", *refreshedSession.CurrentTurnID)
	}

	var pendingJobs int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status = 'pending'
		  AND (payload->>'session_id')::uuid = $1
	`, session.ID).Scan(&pendingJobs); err != nil {
		t.Fatalf("count pending queued retry jobs: %v", err)
	}
	if pendingJobs != 1 {
		t.Fatalf("pending queued retry jobs = %d, want 1", pendingJobs)
	}
}

func TestJobWorkerRecoverStaleInProgressContinuationTurnsSuppressesCompletedProjectBootstrapRequeue(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-continuation-suppress-completed-bootstrap",
		DisplayName: "Recover Stale Continuation Suppress Completed Bootstrap",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover stale project continuations.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-continuation-suppress-completed-bootstrap-project",
		DisplayName:    "Recover Stale Continuation Suppress Completed Bootstrap Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	rootMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue bootstrap.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_bootstrap","auto_continue":true}`),
	})
	if err != nil {
		t.Fatalf("create root message: %v", err)
	}
	firstTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &rootMessage.ID,
	})
	if err != nil {
		t.Fatalf("create completed root turn: %v", err)
	}
	continuationTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     2,
		RespondingType: "agent",
		RespondingID:   agent.ID,
		Status:         "in_progress",
	})
	if err != nil {
		t.Fatalf("create continuation turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &continuationTurn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '20 minutes'
		WHERE id = $1
	`, continuationTurn.ID); err != nil {
		t.Fatalf("age continuation turn: %v", err)
	}
	if firstTurn.TriggerMessageID == nil || *firstTurn.TriggerMessageID != rootMessage.ID {
		t.Fatalf("first turn trigger_message_id = %v, want %s", firstTurn.TriggerMessageID, rootMessage.ID)
	}

	repaired, err := worker.RecoverStaleInProgressContinuationTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressContinuationTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired continuations = %d, want 1", repaired)
	}

	var queued int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status IN ('pending', 'claimed')
		  AND (payload->>'session_id')::uuid = $1
	`, session.ID).Scan(&queued); err != nil {
		t.Fatalf("count queued retries: %v", err)
	}
	if queued != 0 {
		t.Fatalf("queued retries = %d, want 0", queued)
	}
}

func TestJobWorkerRecoverStaleInProgressContinuationTurnsSuppressesProjectContinuationWithoutOpenTasks(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-continuation-suppress-project-continuation",
		DisplayName: "Recover Stale Continuation Suppress Project Continuation",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover stale project continuations.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-continuation-suppress-project-continuation-project",
		DisplayName:    "Recover Stale Continuation Suppress Project Continuation Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	rootMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue project execution.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","synthetic_user_message":true}`),
	})
	if err != nil {
		t.Fatalf("create root message: %v", err)
	}
	firstTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &rootMessage.ID,
	})
	if err != nil {
		t.Fatalf("create completed root turn: %v", err)
	}
	continuationTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     2,
		RespondingType: "agent",
		RespondingID:   agent.ID,
		Status:         "in_progress",
	})
	if err != nil {
		t.Fatalf("create continuation turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &continuationTurn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '20 minutes'
		WHERE id = $1
	`, continuationTurn.ID); err != nil {
		t.Fatalf("age continuation turn: %v", err)
	}
	if firstTurn.TriggerMessageID == nil || *firstTurn.TriggerMessageID != rootMessage.ID {
		t.Fatalf("first turn trigger_message_id = %v, want %s", firstTurn.TriggerMessageID, rootMessage.ID)
	}

	repaired, err := worker.RecoverStaleInProgressContinuationTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressContinuationTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired continuations = %d, want 1", repaired)
	}

	var queued int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status IN ('pending', 'claimed')
		  AND (payload->>'session_id')::uuid = $1
	`, session.ID).Scan(&queued); err != nil {
		t.Fatalf("count queued retries: %v", err)
	}
	if queued != 0 {
		t.Fatalf("queued retries = %d, want 0", queued)
	}
}

func TestJobWorkerRecoverStaleInProgressContinuationTurnsSynthesizesProjectContinuationWithoutPriorTrigger(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-continuation-synthesize-project-continuation",
		DisplayName: "Recover Stale Continuation Synthesize Project Continuation",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover stale project continuations.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-continuation-synthesize-project-continuation-project",
		DisplayName:    "Recover Stale Continuation Synthesize Project Continuation Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Remaining draft task",
		WorkStatus:     "draft",
		BlocksScope:    "task",
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	}); err != nil {
		t.Fatalf("create open project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	continuationTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   agent.ID,
		Status:         "in_progress",
	})
	if err != nil {
		t.Fatalf("create continuation turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &continuationTurn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '20 minutes'
		WHERE id = $1
	`, continuationTurn.ID); err != nil {
		t.Fatalf("age continuation turn: %v", err)
	}
	if _, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &continuationTurn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "streaming",
	}); err != nil {
		t.Fatalf("create streaming assistant message: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressContinuationTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressContinuationTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired continuations = %d, want 1", repaired)
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", refreshedSession.CurrentTurnID)
	}

	var (
		messageID uuid.UUID
		source    string
		autoCont  bool
		synthetic bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT id,
		       COALESCE(metadata->>'source', ''),
		       COALESCE(metadata->>'auto_continue', 'false') = 'true',
		       COALESCE(metadata->>'synthetic_user_message', 'false') = 'true'
		FROM chat_message
		WHERE session_id = $1
		  AND role = 'user'
		  AND status = 'pending'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, session.ID).Scan(&messageID, &source, &autoCont, &synthetic); err != nil {
		t.Fatalf("query synthesized continuation message: %v", err)
	}
	if source != "project_execution_continuation" {
		t.Fatalf("synthesized continuation source = %q, want project_execution_continuation", source)
	}
	if !autoCont || !synthetic {
		t.Fatalf("synthesized continuation flags = auto_continue:%t synthetic:%t, want both true", autoCont, synthetic)
	}

	var (
		jobStatus  string
		jobMessage uuid.UUID
		retryCount int
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, (payload->>'message_id')::uuid, COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status = 'pending'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, session.ID).Scan(&jobStatus, &jobMessage, &retryCount); err != nil {
		t.Fatalf("query synthesized continuation retry job: %v", err)
	}
	if jobStatus != "pending" {
		t.Fatalf("retry job status = %q, want pending", jobStatus)
	}
	if jobMessage != messageID {
		t.Fatalf("retry job message_id = %s, want synthesized %s", jobMessage, messageID)
	}
	if retryCount != 0 {
		t.Fatalf("retry count = %d, want 0 for synthesized continuation", retryCount)
	}
}

func TestJobWorkerRequeueActiveProjectSessionsWithoutTurnsSupersedesStalePendingContinuation(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-project-session-supersede-stale-continuation",
		DisplayName: "Requeue Active Project Session Supersede Stale Continuation",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-project-session-supersede-stale-continuation-project",
		DisplayName:    "Requeue Active Project Session Supersede Stale Continuation Project",
		Description:    "Project for stale continuation supersession coverage",
		DeliveryMode:   "gated",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Continuation Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You continue project execution.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-active-project-session-supersede-stale-continuation-template",
		DisplayName:    "Requeue Active Project Session Supersede Stale Continuation Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(pool)
	olderDone, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Create and validate pipeline configuration files",
		WorkStatus:     "done",
		BlocksScope:    "task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create older done task: %v", err)
	}
	newerDone, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Validate pipeline output format and delivery",
		WorkStatus:     "done",
		BlocksScope:    "task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create newer done task: %v", err)
	}
	if _, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Run end-to-end pipeline integration test",
		WorkStatus:     "draft",
		BlocksScope:    "task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	}); err != nil {
		t.Fatalf("create draft task: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE project_task
		SET updated_at = CASE
			WHEN id = $1 THEN now() - interval '10 minutes'
			WHEN id = $2 THEN now()
			ELSE updated_at
		END
		WHERE id IN ($1, $2)
	`, olderDone.ID, newerDone.ID); err != nil {
		t.Fatalf("order completed tasks by updated_at: %v", err)
	}

	staleMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the active project execution now. The latest completed task was older work.",
		Status:    "pending",
		Metadata:  json.RawMessage(fmt.Sprintf(`{"source":"project_execution_continuation","auto_continue":true,"synthetic_user_message":true,"completed_task_id":"%s"}`, olderDone.ID)),
	})
	if err != nil {
		t.Fatalf("create stale continuation message: %v", err)
	}
	completedTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &staleMessage.ID,
		RetryCount:       4,
	})
	if err != nil {
		t.Fatalf("create completed stale continuation turn: %v", err)
	}
	if completedTurn.TriggerMessageID == nil || *completedTurn.TriggerMessageID != staleMessage.ID {
		t.Fatalf("completed stale continuation turn trigger = %v, want %s", completedTurn.TriggerMessageID, staleMessage.ID)
	}

	requeued, err := worker.RequeueActiveProjectSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveProjectSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued sessions = %d, want 1", requeued)
	}

	var (
		staleStatus      string
		staleError       string
		jobStatus        string
		jobMessageID     uuid.UUID
		jobRetryCount    int
		newSource        string
		newCompletedTask string
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, COALESCE(error_message, '')
		FROM chat_message
		WHERE id = $1
	`, staleMessage.ID).Scan(&staleStatus, &staleError); err != nil {
		t.Fatalf("query stale continuation message: %v", err)
	}
	if staleStatus != "failed" {
		t.Fatalf("stale continuation status = %q, want failed", staleStatus)
	}
	if !strings.Contains(staleError, "superseded by newer completed project task") {
		t.Fatalf("stale continuation error = %q, want superseded reason", staleError)
	}
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, session.ID).Scan(&jobStatus, &jobMessageID, &jobRetryCount); err != nil {
		t.Fatalf("query requeued continuation job: %v", err)
	}
	if jobStatus != "pending" {
		t.Fatalf("requeued job status = %q, want pending", jobStatus)
	}
	if jobMessageID == staleMessage.ID {
		t.Fatalf("requeued message_id = %s, want fresh continuation message", jobMessageID)
	}
	if jobRetryCount != 0 {
		t.Fatalf("requeued retry_count = %d, want 0 after stale supersession", jobRetryCount)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(metadata->>'source', ''),
		       COALESCE(metadata->>'completed_task_id', '')
		FROM chat_message
		WHERE id = $1
	`, jobMessageID).Scan(&newSource, &newCompletedTask); err != nil {
		t.Fatalf("query fresh continuation message: %v", err)
	}
	if newSource != "project_execution_continuation" {
		t.Fatalf("fresh continuation source = %q, want project_execution_continuation", newSource)
	}
	if newCompletedTask != newerDone.ID.String() {
		t.Fatalf("fresh continuation completed_task_id = %q, want %s", newCompletedTask, newerDone.ID)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurns(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-turns",
		DisplayName: "Recover Stale Triggered Turns",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Triggered Turn Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover leaked triggered turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-triggered-project",
		DisplayName:    "Recover Stale Triggered Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "recover-stale-triggered-template",
		DisplayName:    "Recover Stale Triggered Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover stale triggered turn",
		WorkStatus:      "review",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '1 hour'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-stale-triggered-provider",
		DisplayName: "Recover Stale Triggered Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
	})
	if err != nil {
		t.Fatalf("create in-flight invocation: %v", err)
	}
	assistantMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create pending assistant message: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload triggered turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("triggered turn status = %q, want failed", storedTurn.Status)
	}
	if storedTurn.CompletedAt == nil {
		t.Fatalf("triggered turn completed_at = nil, want set")
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", *refreshedSession.CurrentTurnID)
	}

	refreshedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if refreshedInvocation.Status != "failed" {
		t.Fatalf("invocation status = %q, want failed", refreshedInvocation.Status)
	}
	if refreshedInvocation.ErrorCode == nil || *refreshedInvocation.ErrorCode != "stale_turn_recovered" {
		t.Fatalf("invocation error_code = %v, want stale_turn_recovered", refreshedInvocation.ErrorCode)
	}

	refreshedAssistant, err := repo.NewChatMessageRepo(pool).GetByID(ctx, assistantMessage.ID)
	if err != nil {
		t.Fatalf("reload assistant message: %v", err)
	}
	if refreshedAssistant.Status != "failed" {
		t.Fatalf("assistant message status = %q, want failed", refreshedAssistant.Status)
	}

	requeued, err := worker.RequeueActiveProjectSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveProjectSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued active project continuations = %d, want 1", requeued)
	}

	var (
		jobStatus     string
		requeuedMsgID uuid.UUID
		requeuedSess  uuid.UUID
		retryCount    int
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&jobStatus, &requeuedMsgID, &requeuedSess, &retryCount); err != nil {
		t.Fatalf("query requeued triggered retry job: %v", err)
	}
	if jobStatus != "pending" {
		t.Fatalf("requeued triggered job status = %q, want pending", jobStatus)
	}
	if requeuedSess != session.ID {
		t.Fatalf("requeued triggered session_id = %s, want %s", requeuedSess, session.ID)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued triggered message_id = %s, want %s", requeuedMsgID, message.ID)
	}
	if retryCount != 1 {
		t.Fatalf("requeued triggered retry_count = %d, want 1", retryCount)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsPreservesRateLimitBackoff(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-rate-limited-triggered-turn",
		DisplayName: "Recover Rate Limited Triggered Turn",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Rate Limited Triggered Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover rate-limited turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-rate-limited-triggered-project",
		DisplayName:    "Recover Rate Limited Triggered Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "recover-rate-limited-triggered-template",
		DisplayName:    "Recover Rate Limited Triggered Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover rate limited triggered turn",
		WorkStatus:      "in_progress",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue the task",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '1 hour'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-rate-limited-triggered-provider",
		DisplayName: "Recover Rate Limited Triggered Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	completedAt := time.Now().UTC().Add(-30 * time.Minute)
	errorMessage := `model provider rate limited (retry_after=3h22m57s): provider http 429`
	if _, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "failed",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
		ErrorMessage:      &errorMessage,
		CompletedAt:       &completedAt,
	}); err != nil {
		t.Fatalf("create failed invocation: %v", err)
	}

	before := time.Now().UTC()
	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}

	var (
		jobStatus string
		runAfter  time.Time
		retry     int
		jittered  bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       run_after,
		       COALESCE((payload->>'retry_count')::int, 0),
		       COALESCE((payload->>'rate_limit_jitter_applied')::boolean, false)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, session.ID).Scan(&jobStatus, &runAfter, &retry, &jittered); err != nil {
		t.Fatalf("query requeued rate-limited retry job: %v", err)
	}
	if jobStatus != "pending" {
		t.Fatalf("retry job status = %q, want pending", jobStatus)
	}
	if retry != 1 {
		t.Fatalf("retry count = %d, want 1", retry)
	}
	if !jittered {
		t.Fatalf("rate_limit_jitter_applied = false, want true")
	}
	wantRunAfter := before.Add(agentTurnRateLimitDelay(1, 3*time.Hour+22*time.Minute+57*time.Second))
	if runAfter.Before(wantRunAfter.Add(-5*time.Second)) || runAfter.After(wantRunAfter.Add(5*time.Second)) {
		t.Fatalf("run_after = %s, want about %s", runAfter, wantRunAfter)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsUsesExecutionMetadataLiveTurn(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-live-owner",
		DisplayName: "Recover Stale Triggered Live Owner",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Triggered Turn Live Owner Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover leaked triggered turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-triggered-live-owner-project",
		DisplayName:    "Recover Stale Triggered Live Owner Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "recover-stale-triggered-live-owner-template",
		DisplayName:    "Recover Stale Triggered Live Owner Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover stale triggered turn from execution owner metadata",
		WorkStatus:      "review",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create triggered turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '1 hour'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}

	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}
	metadata := repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{TurnID: &turn.ID})
	if _, err := repo.NewFlowNodeExecutionRepo(pool).UpdateMetadata(ctx, execution.ID, metadata); err != nil {
		t.Fatalf("set live turn metadata: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-stale-triggered-live-owner-provider",
		DisplayName: "Recover Stale Triggered Live Owner Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
	})
	if err != nil {
		t.Fatalf("create in-flight invocation: %v", err)
	}
	assistantMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create pending assistant message: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload triggered turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("triggered turn status = %q, want failed", storedTurn.Status)
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", refreshedSession.CurrentTurnID)
	}

	refreshedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if refreshedInvocation.Status != "failed" {
		t.Fatalf("invocation status = %q, want failed", refreshedInvocation.Status)
	}

	refreshedAssistant, err := repo.NewChatMessageRepo(pool).GetByID(ctx, assistantMessage.ID)
	if err != nil {
		t.Fatalf("reload assistant message: %v", err)
	}
	if refreshedAssistant.Status != "failed" {
		t.Fatalf("assistant message status = %q, want failed", refreshedAssistant.Status)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsIgnoresQueuedJobForDifferentExecution(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-different-execution-job",
		DisplayName: "Recover Stale Triggered Different Execution Job",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Triggered Turn Different Execution Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover leaked triggered turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-triggered-different-execution-job-project",
		DisplayName:    "Recover Stale Triggered Different Execution Job Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "recover-stale-triggered-different-execution-job-template",
		DisplayName:    "Recover Stale Triggered Different Execution Job Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover stale triggered turn with mismatched queued job",
		WorkStatus:      "review",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create triggered turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '1 hour'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}

	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}
	metadata := repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{TurnID: &turn.ID})
	if _, err := repo.NewFlowNodeExecutionRepo(pool).UpdateMetadata(ctx, execution.ID, metadata); err != nil {
		t.Fatalf("set live turn metadata: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-stale-triggered-different-execution-job-provider",
		DisplayName: "Recover Stale Triggered Different Execution Job Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	if _, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
	}); err != nil {
		t.Fatalf("create in-flight invocation: %v", err)
	}
	if _, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "pending",
	}); err != nil {
		t.Fatalf("create pending assistant message: %v", err)
	}

	staleExecutionID := uuid.New()
	if _, err := worker.Enqueue(ctx, nil, agentTurnJobType, 70, agentTurnKeyPayload{
		SessionID:           session.ID,
		MessageID:           uuid.New(),
		RetryCount:          1,
		FlowNodeExecutionID: &staleExecutionID,
	}, nil); err != nil {
		t.Fatalf("enqueue stale mismatched dispatch: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}

	var (
		jobStatus     string
		requeuedMsgID uuid.UUID
		requeuedExec  *uuid.UUID
		retryCount    int
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       CASE
		         WHEN COALESCE(payload->>'flow_node_execution_id', '') = '' THEN NULL
		         ELSE (payload->>'flow_node_execution_id')::uuid
		       END,
		       COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&jobStatus, &requeuedMsgID, &requeuedExec, &retryCount); err != nil {
		t.Fatalf("query requeued triggered retry job: %v", err)
	}
	if jobStatus != "pending" {
		t.Fatalf("requeued triggered job status = %q, want pending", jobStatus)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued triggered message_id = %s, want %s", requeuedMsgID, message.ID)
	}
	if requeuedExec == nil || *requeuedExec != execution.ID {
		t.Fatalf("requeued flow_node_execution_id = %v, want %s", requeuedExec, execution.ID)
	}
	if retryCount != 1 {
		t.Fatalf("requeued triggered retry_count = %d, want 1", retryCount)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsKeepsExistingPendingRetryJob(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-keep-existing-pending-retry",
		DisplayName: "Recover Stale Triggered Keep Existing Pending Retry",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Triggered Turn Pending Retry Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover leaked triggered turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-triggered-keep-existing-pending-retry-project",
		DisplayName:    "Recover Stale Triggered Keep Existing Pending Retry Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "recover-stale-triggered-keep-existing-pending-retry-template",
		DisplayName:    "Recover Stale Triggered Keep Existing Pending Retry Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover stale triggered turn while keeping existing pending retry",
		WorkStatus:      "review",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "review the task execution and decide",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"task_queue_processor"}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
		RetryCount:       1,
	})
	if err != nil {
		t.Fatalf("create triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '1 hour'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}

	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}
	metadata := repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{TurnID: &turn.ID})
	if _, err := repo.NewFlowNodeExecutionRepo(pool).UpdateMetadata(ctx, execution.ID, metadata); err != nil {
		t.Fatalf("set live turn metadata: %v", err)
	}
	messageMetadata := map[string]any{
		"source":                 "task_queue_processor",
		"flow_node_execution_id": execution.ID.String(),
	}
	encodedMessageMetadata, err := json.Marshal(messageMetadata)
	if err != nil {
		t.Fatalf("marshal message metadata: %v", err)
	}
	if _, err := repo.NewChatMessageRepo(pool).UpdateMetadata(ctx, message.ID, encodedMessageMetadata); err != nil {
		t.Fatalf("update trigger message metadata: %v", err)
	}

	if _, err := worker.Enqueue(ctx, nil, agentTurnJobType, 70, agentTurnKeyPayload{
		SessionID:           session.ID,
		MessageID:           message.ID,
		RetryCount:          2,
		FlowNodeExecutionID: &execution.ID,
	}, nil); err != nil {
		t.Fatalf("enqueue pending retry job: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload triggered turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("triggered turn status = %q, want failed", storedTurn.Status)
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", refreshedSession.CurrentTurnID)
	}

	var pendingJobs int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status = 'pending'
		  AND (payload->>'session_id')::uuid = $1
	`, session.ID).Scan(&pendingJobs); err != nil {
		t.Fatalf("count pending retry jobs: %v", err)
	}
	if pendingJobs != 1 {
		t.Fatalf("pending retry jobs = %d, want 1", pendingJobs)
	}

	var (
		jobStatus    string
		requeuedMsg  uuid.UUID
		requeuedExec *uuid.UUID
		retryCount   int
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       CASE
		         WHEN COALESCE(payload->>'flow_node_execution_id', '') = '' THEN NULL
		         ELSE (payload->>'flow_node_execution_id')::uuid
		       END,
		       COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&jobStatus, &requeuedMsg, &requeuedExec, &retryCount); err != nil {
		t.Fatalf("query pending retry job: %v", err)
	}
	if jobStatus != "pending" {
		t.Fatalf("pending retry job status = %q, want pending", jobStatus)
	}
	if requeuedMsg != message.ID {
		t.Fatalf("pending retry message_id = %s, want %s", requeuedMsg, message.ID)
	}
	if requeuedExec == nil || *requeuedExec != execution.ID {
		t.Fatalf("pending retry flow_node_execution_id = %v, want %s", requeuedExec, execution.ID)
	}
	if retryCount != 2 {
		t.Fatalf("pending retry retry_count = %d, want 2", retryCount)
	}
}

func TestJobWorkerRecoverClaimedAgentTurnsWithoutLiveOwnership(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-claimed-agent-turn-without-live-ownership",
		DisplayName: "Recover Claimed Agent Turn Without Live Ownership",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-claimed-agent-turn-without-live-ownership-project",
		DisplayName:    "Recover Claimed Agent Turn Without Live Ownership Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "recover-claimed-agent-turn-without-live-ownership-template",
		DisplayName:    "Recover Claimed Agent Turn Without Live Ownership Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover claimed agent_turn without live ownership",
		WorkStatus:      "in_progress",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the active task recovery now.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"task_recovery_resume"}`),
	})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
		RetryCount:       1,
	})
	if err != nil {
		t.Fatalf("create pending turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_message
		SET turn_id = $2
		WHERE id = $1
	`, message.ID, turn.ID); err != nil {
		t.Fatalf("bind message to pending turn: %v", err)
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, claimed_by, claimed_at, attempts, updated_at)
		VALUES ('agent_turn', 'claimed', $1::jsonb, now(), 70, 'test-worker', now() - interval '2 minutes', 1, now() - interval '2 minutes')
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1}`, session.ID, message.ID)).Scan(&jobID); err != nil {
		t.Fatalf("insert claimed agent_turn: %v", err)
	}

	recovered, err := worker.RecoverClaimedAgentTurnsWithoutLiveOwnership(ctx)
	if err != nil {
		t.Fatalf("RecoverClaimedAgentTurnsWithoutLiveOwnership: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered claimed jobs = %d, want 1", recovered)
	}

	var (
		status    string
		claimedBy *string
	)
	if err := pool.QueryRow(ctx, `SELECT status, claimed_by FROM job_queue WHERE id = $1`, jobID).Scan(&status, &claimedBy); err != nil {
		t.Fatalf("query recovered job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("recovered job status = %q, want pending", status)
	}
	if claimedBy != nil {
		t.Fatalf("recovered job claimed_by = %v, want nil", *claimedBy)
	}
}

func TestJobWorkerRecoverClaimedAgentTurnsWithoutLiveOwnershipKeepsCurrentPendingAttempt(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-claimed-agent-turn-keeps-current-pending-attempt",
		DisplayName: "Recover Claimed Agent Turn Keeps Current Pending Attempt",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Pending Attempt Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You continue pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue work.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create current pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, claimed_by, claimed_at, attempts, updated_at)
		VALUES ('agent_turn', 'claimed', $1::jsonb, now(), 70, 'test-worker', now() - interval '2 minutes', 1, now() - interval '2 minutes')
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, message.ID)).Scan(&jobID); err != nil {
		t.Fatalf("insert claimed agent_turn: %v", err)
	}

	recovered, err := worker.RecoverClaimedAgentTurnsWithoutLiveOwnership(ctx)
	if err != nil {
		t.Fatalf("RecoverClaimedAgentTurnsWithoutLiveOwnership: %v", err)
	}
	if recovered != 0 {
		t.Fatalf("recovered claimed jobs = %d, want 0", recovered)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("query claimed job: %v", err)
	}
	if status != "claimed" {
		t.Fatalf("claimed job status = %q, want claimed", status)
	}
}

func TestJobWorkerRecoverClaimedAgentTurnsWithoutLiveOwnershipRecoversCurrentInProgressAttemptWithoutModelOrRun(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-claimed-agent-turn-current-in-progress-no-model-or-run",
		DisplayName: "Recover Claimed Agent Turn Current In Progress No Model Or Run",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Continuation Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You continue organization work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the active organization request now from the continuation summary above.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"organization_continuation_resume","synthetic_user_message":true}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create current turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, claimed_by, claimed_at, attempts, updated_at)
		VALUES ('agent_turn', 'claimed', $1::jsonb, now(), 70, 'test-worker', now() - interval '2 minutes', 1, now() - interval '2 minutes')
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, message.ID)).Scan(&jobID); err != nil {
		t.Fatalf("insert claimed agent_turn: %v", err)
	}

	recovered, err := worker.RecoverClaimedAgentTurnsWithoutLiveOwnership(ctx)
	if err != nil {
		t.Fatalf("RecoverClaimedAgentTurnsWithoutLiveOwnership: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered claimed jobs = %d, want 1", recovered)
	}

	var (
		status    string
		claimedBy *string
	)
	if err := pool.QueryRow(ctx, `SELECT status, claimed_by FROM job_queue WHERE id = $1`, jobID).Scan(&status, &claimedBy); err != nil {
		t.Fatalf("query recovered job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("recovered job status = %q, want pending", status)
	}
	if claimedBy != nil {
		t.Fatalf("recovered job claimed_by = %v, want nil", *claimedBy)
	}
}

func TestJobWorkerRecoverClaimedAgentTurnsWithoutLiveOwnershipKeepsCurrentInProgressAttemptWithRecentCompletedInvocation(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-claimed-agent-turn-keep-recently-completed-invocation",
		DisplayName: "Recover Claimed Agent Turn Keep Recently Completed Invocation",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Continuation Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You continue organization work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the active organization request now from the continuation summary above.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"organization_continuation_resume","synthetic_user_message":true}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create current turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-claimed-agent-turn-keep-recent-completed-provider",
		DisplayName: "Recover Claimed Agent Turn Keep Recent Completed Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	completedAt := time.Now().UTC()
	if _, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "completed",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
		CompletedAt:       &completedAt,
	}); err != nil {
		t.Fatalf("create completed invocation: %v", err)
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, claimed_by, claimed_at, attempts, updated_at)
		VALUES ('agent_turn', 'claimed', $1::jsonb, now(), 70, 'test-worker', now() - interval '2 minutes', 1, now() - interval '2 minutes')
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, message.ID)).Scan(&jobID); err != nil {
		t.Fatalf("insert claimed agent_turn: %v", err)
	}

	recovered, err := worker.RecoverClaimedAgentTurnsWithoutLiveOwnership(ctx)
	if err != nil {
		t.Fatalf("RecoverClaimedAgentTurnsWithoutLiveOwnership: %v", err)
	}
	if recovered != 0 {
		t.Fatalf("recovered claimed jobs = %d, want 0", recovered)
	}

	var (
		status    string
		claimedBy *string
	)
	if err := pool.QueryRow(ctx, `SELECT status, claimed_by FROM job_queue WHERE id = $1`, jobID).Scan(&status, &claimedBy); err != nil {
		t.Fatalf("query claimed job: %v", err)
	}
	if status != "claimed" {
		t.Fatalf("claimed job status = %q, want claimed", status)
	}
	if claimedBy == nil || *claimedBy == "" {
		t.Fatalf("claimed job claimed_by = %v, want preserved owner", claimedBy)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsFailsNonHeartbeatingClaimedAttemptWithoutRun(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-non-heartbeating-claimed-attempt-without-run",
		DisplayName: "Recover Stale Triggered Non Heartbeating Claimed Attempt Without Run",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Review Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You review work.",
		AgentType:       "reviewer",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-triggered-non-heartbeating-claimed-attempt-project",
		DisplayName:    "Recover Stale Triggered Non Heartbeating Claimed Attempt Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "recover-stale-triggered-non-heartbeating-claimed-attempt-template",
		DisplayName:    "Recover Stale Triggered Non Heartbeating Claimed Attempt Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover stale in-progress claimed review attempt without run",
		WorkStatus:      "review",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '2 minutes'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}
	metadata := repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{TurnID: &turn.ID})
	if _, err := repo.NewFlowNodeExecutionRepo(pool).UpdateMetadata(ctx, execution.ID, metadata); err != nil {
		t.Fatalf("set live turn metadata: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, claimed_by, claimed_at, attempts, updated_at)
		VALUES ('agent_turn', 'claimed', $1::jsonb, now(), 70, 'test-worker', now() - interval '2 minutes', 1, now() - interval '2 minutes')
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0,"flow_node_execution_id":"%s"}`, session.ID, message.ID, execution.ID)); err != nil {
		t.Fatalf("insert stale claimed backing job: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload triggered turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("triggered turn status = %q, want failed", storedTurn.Status)
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", refreshedSession.CurrentTurnID)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsKeepsExistingPendingRetryJobForProjectSession(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-keep-existing-pending-retry-project-session",
		DisplayName: "Recover Stale Triggered Keep Existing Pending Retry Project Session",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Retry Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover leaked project bootstrap turns.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-triggered-keep-existing-pending-retry-project-session-project",
		DisplayName:    "Recover Stale Triggered Keep Existing Pending Retry Project Session Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue bootstrap",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
		RetryCount:       1,
	})
	if err != nil {
		t.Fatalf("create triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '2 minutes'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}

	completedAt := time.Now().Add(-2 * time.Minute)
	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-stale-triggered-keep-existing-pending-retry-project-session-provider",
		DisplayName: "Recover Stale Triggered Keep Existing Pending Retry Project Session Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if _, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "completed",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
		CompletedAt:       &completedAt,
	}); err != nil {
		t.Fatalf("create completed invocation: %v", err)
	}

	if _, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "streaming",
	}); err != nil {
		t.Fatalf("create streaming assistant message: %v", err)
	}

	if _, err := worker.Enqueue(ctx, nil, agentTurnJobType, 70, agentTurnKeyPayload{
		SessionID:  session.ID,
		MessageID:  message.ID,
		RetryCount: 2,
	}, nil); err != nil {
		t.Fatalf("enqueue pending retry job: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload triggered turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("triggered turn status = %q, want failed", storedTurn.Status)
	}
	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", refreshedSession.CurrentTurnID)
	}

	var pendingJobs int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status = 'pending'
		  AND (payload->>'session_id')::uuid = $1
		  AND (payload->>'message_id')::uuid = $2
		  AND COALESCE((payload->>'retry_count')::int, 0) = 2
	`, session.ID, message.ID).Scan(&pendingJobs); err != nil {
		t.Fatalf("count pending retry jobs: %v", err)
	}
	if pendingJobs != 1 {
		t.Fatalf("pending retry jobs = %d, want 1", pendingJobs)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsSuppressesCompletedProjectBootstrapRequeue(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-suppress-completed-project-bootstrap",
		DisplayName: "Recover Stale Triggered Suppress Completed Project Bootstrap",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Retry Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover leaked project bootstrap turns.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-triggered-suppress-completed-project-bootstrap-project",
		DisplayName:    "Recover Stale Triggered Suppress Completed Project Bootstrap Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue bootstrap",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_bootstrap","auto_continue":true}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '20 minutes'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}

	var queued int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status IN ('pending', 'claimed')
		  AND (payload->>'session_id')::uuid = $1
	`, session.ID).Scan(&queued); err != nil {
		t.Fatalf("count queued retries: %v", err)
	}
	if queued != 0 {
		t.Fatalf("queued retries = %d, want 0", queued)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsClearsPausedProjectWithoutRequeue(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-clear-paused-project",
		DisplayName: "Recover Stale Triggered Clear Paused Project",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Retry Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover leaked project bootstrap turns.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-triggered-clear-paused-project-project",
		DisplayName:    "Recover Stale Triggered Clear Paused Project Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       json.RawMessage(`{"pause":{"is_paused":true,"reason":"test pause"},"project_bootstrap":{"status":"failed"}}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"failed"}}`),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue bootstrap",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_bootstrap","auto_continue":true}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '20 minutes'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}
	if _, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "streaming",
	}); err != nil {
		t.Fatalf("create streaming assistant message: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload triggered turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("triggered turn status = %q, want failed", storedTurn.Status)
	}
	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", refreshedSession.CurrentTurnID)
	}

	var queued int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status IN ('pending', 'claimed')
		  AND (payload->>'session_id')::uuid = $1
	`, session.ID).Scan(&queued); err != nil {
		t.Fatalf("count queued retries: %v", err)
	}
	if queued != 0 {
		t.Fatalf("queued retries = %d, want 0", queued)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsSuppressesProjectContinuationWithoutOpenTasks(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-suppress-project-continuation-without-open-tasks",
		DisplayName: "Recover Stale Triggered Suppress Project Continuation Without Open Tasks",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Retry Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover project continuation turns.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-triggered-suppress-project-continuation-without-open-tasks-project",
		DisplayName:    "Recover Stale Triggered Suppress Project Continuation Without Open Tasks Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"completed"}}`),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue project after task completion",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","auto_continue":true}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '20 minutes'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}

	var queued int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status IN ('pending', 'claimed')
		  AND (payload->>'session_id')::uuid = $1
	`, session.ID).Scan(&queued); err != nil {
		t.Fatalf("count queued retries: %v", err)
	}
	if queued != 0 {
		t.Fatalf("queued retries = %d, want 0", queued)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsRequeuesOrganizationContinuationUsingPendingSyntheticUserMessage(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-org-continuation-user-retry",
		DisplayName: "Recover Stale Triggered Org Continuation User Retry",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Org Retry Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover async organization work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	requestMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Create rerun 30.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create request message: %v", err)
	}
	summaryMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "system",
		Content:   "[Continuation summary] Active organization request: Create rerun 30.",
		Status:    "final",
	})
	if err != nil {
		t.Fatalf("create summary message: %v", err)
	}
	resumeMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the active organization request now from the continuation summary above.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"organization_continuation_resume","synthetic_user_message":true}`),
	})
	if err != nil {
		t.Fatalf("create continuation resume message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &summaryMessage.ID,
	})
	if err != nil {
		t.Fatalf("create stale triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '20 minutes'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}
	completedAt := time.Now().Add(-20 * time.Minute)
	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-stale-triggered-org-continuation-user-retry-provider",
		DisplayName: "Recover Stale Triggered Org Continuation User Retry Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if _, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "completed",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
		CompletedAt:       &completedAt,
	}); err != nil {
		t.Fatalf("create completed invocation: %v", err)
	}
	if _, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Content:   "I'll",
		Status:    "streaming",
	}); err != nil {
		t.Fatalf("create streaming assistant message: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", refreshedSession.CurrentTurnID)
	}

	var (
		jobStatus    string
		jobMessageID uuid.UUID
		retryCount   int
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, (payload->>'message_id')::uuid, COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status = 'pending'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&jobStatus, &jobMessageID, &retryCount); err != nil {
		t.Fatalf("query pending retry job: %v", err)
	}
	if jobStatus != "pending" {
		t.Fatalf("pending retry job status = %q, want pending", jobStatus)
	}
	if jobMessageID != resumeMessage.ID {
		t.Fatalf("pending retry message_id = %s, want synthetic continuation %s (request=%s summary=%s)", jobMessageID, resumeMessage.ID, requestMessage.ID, summaryMessage.ID)
	}
	if retryCount != 1 {
		t.Fatalf("pending retry count = %d, want 1", retryCount)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsKeepsPendingRetryJobWithoutRetryCountForProjectSession(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-keep-pending-retry-no-count-project-session",
		DisplayName: "Recover Stale Triggered Keep Pending Retry No Count Project Session",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Retry Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover leaked project bootstrap turns.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-triggered-keep-pending-retry-no-count-project-session-project",
		DisplayName:    "Recover Stale Triggered Keep Pending Retry No Count Project Session Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue bootstrap",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '2 minutes'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}

	completedAt := time.Now().Add(-2 * time.Minute)
	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-stale-triggered-keep-pending-retry-no-count-project-session-provider",
		DisplayName: "Recover Stale Triggered Keep Pending Retry No Count Project Session Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if _, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "completed",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
		CompletedAt:       &completedAt,
	}); err != nil {
		t.Fatalf("create completed invocation: %v", err)
	}

	if _, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "streaming",
	}); err != nil {
		t.Fatalf("create streaming assistant message: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, attempts, updated_at)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, 0, now())
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s"}`, session.ID, message.ID)); err != nil {
		t.Fatalf("insert pending retry job without retry count: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsFailsClaimedProjectSessionAfterCompletedInvocation(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-claimed-project-session-post-model",
		DisplayName: "Recover Stale Triggered Claimed Project Session Post Model",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You continue project bootstrap.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-triggered-claimed-project-session-post-model-project",
		DisplayName:    "Recover Stale Triggered Claimed Project Session Post Model Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue bootstrap",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
		RetryCount:       1,
	})
	if err != nil {
		t.Fatalf("create triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '2 minutes'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-stale-triggered-claimed-project-session-post-model-provider",
		DisplayName: "Recover Stale Triggered Claimed Project Session Post Model Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	completedAt := time.Now().Add(-2 * time.Minute)
	if _, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "completed",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
		CompletedAt:       &completedAt,
	}); err != nil {
		t.Fatalf("create completed invocation: %v", err)
	}
	assistantMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "streaming",
	})
	if err != nil {
		t.Fatalf("create streaming assistant message: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, claimed_by, claimed_at, attempts, updated_at)
		VALUES ('agent_turn', 'claimed', $1::jsonb, now(), 70, 'test-worker', now() - interval '2 minutes', 1, now() - interval '2 minutes')
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1}`, session.ID, message.ID)); err != nil {
		t.Fatalf("insert stale claimed backing job: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload triggered turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("triggered turn status = %q, want failed", storedTurn.Status)
	}
	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", refreshedSession.CurrentTurnID)
	}
	refreshedAssistant, err := repo.NewChatMessageRepo(pool).GetByID(ctx, assistantMessage.ID)
	if err != nil {
		t.Fatalf("reload assistant message: %v", err)
	}
	if refreshedAssistant.Status != "failed" {
		t.Fatalf("assistant message status = %q, want failed", refreshedAssistant.Status)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsKeepsProjectBootstrapSessionWithLiveInvocation(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-project-bootstrap-live-invocation",
		DisplayName: "Recover Stale Triggered Project Bootstrap Live Invocation",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Bootstrap Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You continue project bootstrap.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-triggered-project-bootstrap-live-invocation-project",
		DisplayName:    "Recover Stale Triggered Project Bootstrap Live Invocation Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		Metadata:       json.RawMessage(`{"project_bootstrap":{"status":"active","current_phase":"staffing_persisted"}}`),
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue project bootstrap",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_bootstrap","auto_continue":true}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '20 minutes'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-stale-triggered-project-bootstrap-live-invocation-provider",
		DisplayName: "Recover Stale Triggered Project Bootstrap Live Invocation Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
	})
	if err != nil {
		t.Fatalf("create in-flight invocation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE model_invocation
		SET created_at = now() - interval '30 seconds'
		WHERE id = $1
	`, invocation.ID); err != nil {
		t.Fatalf("age invocation: %v", err)
	}
	assistantMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create assistant message: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("repaired triggered turns = %d, want 0", repaired)
	}

	refreshedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload turn: %v", err)
	}
	if refreshedTurn.Status != "in_progress" {
		t.Fatalf("turn status = %q, want in_progress", refreshedTurn.Status)
	}
	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID == nil || *refreshedSession.CurrentTurnID != turn.ID {
		t.Fatalf("current_turn_id = %v, want %s", refreshedSession.CurrentTurnID, turn.ID)
	}
	refreshedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if refreshedInvocation.Status != "in_flight" {
		t.Fatalf("invocation status = %q, want in_flight", refreshedInvocation.Status)
	}
	refreshedAssistant, err := repo.NewChatMessageRepo(pool).GetByID(ctx, assistantMessage.ID)
	if err != nil {
		t.Fatalf("reload assistant message: %v", err)
	}
	if refreshedAssistant.Status != "pending" {
		t.Fatalf("assistant message status = %q, want pending", refreshedAssistant.Status)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsFailsOrphanedAttemptWithoutJobRunOrInvocation(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-orphaned-attempt-without-job-run-or-invocation",
		DisplayName: "Recover Stale Triggered Orphaned Attempt Without Job Run Or Invocation",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Review Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You review work.",
		AgentType:       "reviewer",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-triggered-orphaned-attempt-project",
		DisplayName:    "Recover Stale Triggered Orphaned Attempt Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "recover-stale-triggered-orphaned-attempt-template",
		DisplayName:    "Recover Stale Triggered Orphaned Attempt Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover stale in-progress orphaned review attempt without job",
		WorkStatus:      "review",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '2 minutes'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}
	metadata := repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{TurnID: &turn.ID})
	if _, err := repo.NewFlowNodeExecutionRepo(pool).UpdateMetadata(ctx, execution.ID, metadata); err != nil {
		t.Fatalf("set live turn metadata: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload triggered turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("triggered turn status = %q, want failed", storedTurn.Status)
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", refreshedSession.CurrentTurnID)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurnsIgnoresStaleRunWithoutLiveInvocation(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-stale-run",
		DisplayName: "Recover Stale Triggered Stale Run",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Triggered Turn Stale Run Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover leaked triggered turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-triggered-stale-run-project",
		DisplayName:    "Recover Stale Triggered Stale Run Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "recover-stale-triggered-stale-run-template",
		DisplayName:    "Recover Stale Triggered Stale Run Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover stale triggered turn with stale run",
		WorkStatus:      "review",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create triggered turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '1 hour'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}

	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}
	metadata := repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{TurnID: &turn.ID})
	if _, err := repo.NewFlowNodeExecutionRepo(pool).UpdateMetadata(ctx, execution.ID, metadata); err != nil {
		t.Fatalf("set live turn metadata: %v", err)
	}

	runID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO run (
			id,
			organization_id,
			project_id,
			task_id,
			flow_node_id,
			session_id,
			turn_id,
			principal_type,
			principal_id,
			status,
			trigger_type,
			version,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'agent', $8, 'in_progress', 'scheduler', 1, '{}'::jsonb)
	`, runID, org.ID, project.ID, taskRecord.ID, flowNode.ID, session.ID, turn.ID, agent.ID); err != nil {
		t.Fatalf("create stale run: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE run
		SET started_at = now() - interval '1 hour',
		    updated_at = now() - interval '1 hour'
		WHERE id = $1
	`, runID); err != nil {
		t.Fatalf("age stale run: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-stale-triggered-stale-run-provider",
		DisplayName: "Recover Stale Triggered Stale Run Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	invocationCompletedAt := time.Now().UTC().Add(-30 * time.Minute)
	errorCode := "context_canceled"
	errorMessage := "context canceled"
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "failed",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
		ErrorCode:         &errorCode,
		ErrorMessage:      &errorMessage,
		CompletedAt:       &invocationCompletedAt,
	})
	if err != nil {
		t.Fatalf("create failed invocation: %v", err)
	}
	assistantMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create pending assistant message: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload triggered turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("triggered turn status = %q, want failed", storedTurn.Status)
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", refreshedSession.CurrentTurnID)
	}

	var refreshedRunStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM run WHERE id = $1`, runID).Scan(&refreshedRunStatus); err != nil {
		t.Fatalf("reload stale run: %v", err)
	}
	if refreshedRunStatus != "failed" {
		t.Fatalf("stale run status = %q, want failed for project_task turn recovery path", refreshedRunStatus)
	}

	refreshedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if refreshedInvocation.Status != "failed" {
		t.Fatalf("invocation status = %q, want failed", refreshedInvocation.Status)
	}

	refreshedAssistant, err := repo.NewChatMessageRepo(pool).GetByID(ctx, assistantMessage.ID)
	if err != nil {
		t.Fatalf("reload assistant message: %v", err)
	}
	if refreshedAssistant.Status != "failed" {
		t.Fatalf("assistant message status = %q, want failed", refreshedAssistant.Status)
	}
}

func TestJobWorkerFailStaleModelInvocationsRequeuesTriggeredProjectSession(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "fail-stale-model-invocations-project-session",
		DisplayName: "Fail Stale Model Invocations Project Session",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Project Session Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover stale project turns.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "fail-stale-model-invocations-project-session",
		DisplayName:    "Fail Stale Model Invocations Project Session",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue orchestrating the project",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "fail-stale-model-invocations-project-session-provider",
		DisplayName: "Fail Stale Model Invocations Project Session Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
	})
	if err != nil {
		t.Fatalf("create model invocation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE model_invocation
		SET created_at = now() - interval '20 minutes'
		WHERE id = $1
	`, invocation.ID); err != nil {
		t.Fatalf("age model invocation: %v", err)
	}
	assistantMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create pending assistant message: %v", err)
	}

	repaired, err := worker.FailStaleModelInvocations(ctx)
	if err != nil {
		t.Fatalf("FailStaleModelInvocations: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired stale invocations = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("turn status = %q, want failed", storedTurn.Status)
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", refreshedSession.CurrentTurnID)
	}

	refreshedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if refreshedInvocation.Status != "failed" {
		t.Fatalf("invocation status = %q, want failed", refreshedInvocation.Status)
	}
	if refreshedInvocation.ErrorCode == nil || *refreshedInvocation.ErrorCode != "stale_model_invocation" {
		t.Fatalf("invocation error_code = %v, want stale_model_invocation", refreshedInvocation.ErrorCode)
	}

	refreshedAssistant, err := repo.NewChatMessageRepo(pool).GetByID(ctx, assistantMessage.ID)
	if err != nil {
		t.Fatalf("reload assistant message: %v", err)
	}
	if refreshedAssistant.Status != "failed" {
		t.Fatalf("assistant message status = %q, want failed", refreshedAssistant.Status)
	}
}

func TestJobWorkerFailStaleModelInvocationsRecoversInheritedAsyncProjectInvocationAfterWorkerRestart(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-inherited-project-invocation-after-restart",
		DisplayName: "Recover Inherited Project Invocation After Restart",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Inherited Project Continuation Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover inherited project turns after restart.",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-inherited-project-invocation-after-restart",
		DisplayName:    "Recover Inherited Project Invocation After Restart",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue project bootstrap",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"project_bootstrap","auto_continue":true}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-inherited-project-invocation-after-restart-provider",
		DisplayName: "Recover Inherited Project Invocation After Restart Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
	})
	if err != nil {
		t.Fatalf("create model invocation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE model_invocation
		SET created_at = now() - interval '1 minute'
		WHERE id = $1
	`, invocation.ID); err != nil {
		t.Fatalf("age inherited model invocation: %v", err)
	}
	assistantMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create pending assistant message: %v", err)
	}

	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})
	worker.startupAt = time.Now().UTC()

	repaired, err := worker.FailStaleModelInvocations(ctx)
	if err != nil {
		t.Fatalf("FailStaleModelInvocations: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired stale invocations = %d, want 1", repaired)
	}

	refreshedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if refreshedInvocation.Status != "failed" {
		t.Fatalf("invocation status = %q, want failed", refreshedInvocation.Status)
	}
	refreshedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload turn: %v", err)
	}
	if refreshedTurn.Status != "failed" {
		t.Fatalf("turn status = %q, want failed", refreshedTurn.Status)
	}
	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", refreshedSession.CurrentTurnID)
	}
	refreshedAssistant, err := repo.NewChatMessageRepo(pool).GetByID(ctx, assistantMessage.ID)
	if err != nil {
		t.Fatalf("reload assistant message: %v", err)
	}
	if refreshedAssistant.Status != "failed" {
		t.Fatalf("assistant message status = %q, want failed", refreshedAssistant.Status)
	}
}

func TestJobWorkerRequeueActiveExecutionSessionsWithoutTurnsForTaskQueueKickoff(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-execution-task-queue-kickoff",
		DisplayName: "Requeue Active Execution Task Queue Kickoff",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Queue Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You resume queued task work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-execution-task-queue-project",
		DisplayName:    "Requeue Active Execution Task Queue Project",
		Description:    "Project for task-queue wake repair",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-active-execution-task-queue-template",
		DisplayName:    "Requeue Active Execution Task Queue Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover task-queue execution kickoff",
		WorkStatus:      "draft",
		BlocksScope:     "task",
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}
	metadata := json.RawMessage(fmt.Sprintf(`{"source":"task_queue_processor","flow_node_execution_id":"%s","flow_event_type":"flow.advanced"}`, execution.ID))
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "task queue wakeup",
		Status:    "pending",
		Metadata:  metadata,
	})
	if err != nil {
		t.Fatalf("create task queue kickoff message: %v", err)
	}
	completedTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create completed task queue turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}
	if completedTurn.TriggerMessageID == nil || *completedTurn.TriggerMessageID != message.ID {
		t.Fatalf("completed task queue turn trigger_message_id = %v, want %s", completedTurn.TriggerMessageID, message.ID)
	}

	requeued, err := worker.RequeueActiveExecutionSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveExecutionSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued sessions = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
		retryCount     int
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID, &retryCount); err != nil {
		t.Fatalf("query requeued task queue execution job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued message_id = %s, want %s", requeuedMsgID, message.ID)
	}
	if retryCount != 1 {
		t.Fatalf("requeued retry_count = %d, want 1", retryCount)
	}
}

func TestJobWorkerCancelsClaimedAgentTurnWhenSessionClosesMidExecution(t *testing.T) {
	pool := testdb.New(t)
	org := createOrgForJobQueue(t, pool, "agent-turn-close-mid-execution")
	session, err := repo.NewChatSessionRepo(pool).Create(context.Background(), repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "sync",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}

	worker := New(pool, nil, Config{
		WorkerID:             "close-mid-execution",
		PollInterval:         10 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		StaleClaimThreshold:  30 * time.Millisecond,
		CleanupEnqueuePeriod: time.Hour,
	})

	released := make(chan struct{})
	worker.Register(agentTurnJobType, func(ctx context.Context, job Job) error {
		select {
		case <-ctx.Done():
			close(released)
			return nil
		case <-time.After(5 * time.Second):
			return fmt.Errorf("timed out waiting for session-close cancellation")
		}
	})

	payload := map[string]any{
		"session_id": session.ID,
		"message_id": uuid.New(),
	}
	jobID, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 100, payload, nil)
	if err != nil {
		t.Fatalf("enqueue claimed-close agent_turn: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	defer func() {
		cancel()
		_ = worker.Stop()
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		if err := pool.QueryRow(context.Background(), `SELECT status FROM job_queue WHERE id = $1`, jobID).Scan(&status); err != nil {
			t.Fatalf("load claimed job status: %v", err)
		}
		if status == "claimed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := repo.NewChatSessionRepo(pool).Close(context.Background(), session.ID); err != nil {
		t.Fatalf("close claimed session: %v", err)
	}

	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for handler cancellation after session close")
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		if err := pool.QueryRow(context.Background(), `SELECT status FROM job_queue WHERE id = $1`, jobID).Scan(&status); err != nil {
			t.Fatalf("load finished job status: %v", err)
		}
		if status == "done" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	var status string
	_ = pool.QueryRow(context.Background(), `SELECT status FROM job_queue WHERE id = $1`, jobID).Scan(&status)
	t.Fatalf("job status after session close = %q, want done", status)
}

func TestIdempotencyCleanupJob(t *testing.T) {
	pool := testdb.New(t)
	org := createOrgForJobQueue(t, pool, "cleanup-org")

	insertedExpired := 3
	for i := 0; i < insertedExpired; i++ {
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO idempotency_key (
				organization_id, key_hash, request_hash, response_status, response_body, expires_at
			)
			VALUES ($1, $2, $3, 200, '{}'::jsonb, now() - interval '1 hour')
		`, org.ID, fmt.Sprintf("expired-%d", i), fmt.Sprintf("request-%d", i)); err != nil {
			t.Fatalf("insert expired idempotency key failed: %v", err)
		}
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO idempotency_key (
			organization_id, key_hash, request_hash, response_status, response_body, expires_at
		)
		VALUES ($1, 'fresh-key', 'fresh-request', 200, '{}'::jsonb, now() + interval '12 hours')
	`, org.ID); err != nil {
		t.Fatalf("insert non-expired idempotency key failed: %v", err)
	}

	worker := New(pool, nil, Config{
		PollInterval:         100 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	defer func() {
		cancel()
		_ = worker.Stop()
	}()

	if _, err := worker.Enqueue(context.Background(), nil, idempotencyCleanupJob, 200, map[string]any{"test": true}, nil); err != nil {
		t.Fatalf("enqueue idempotency cleanup job failed: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var expiredCount int
		if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM idempotency_key WHERE expires_at < now()`).Scan(&expiredCount); err != nil {
			t.Fatalf("count expired keys failed: %v", err)
		}
		if expiredCount == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	var (
		expiredCount int
		totalCount   int
	)
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM idempotency_key WHERE expires_at < now()`).Scan(&expiredCount); err != nil {
		t.Fatalf("count expired keys failed: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM idempotency_key`).Scan(&totalCount); err != nil {
		t.Fatalf("count all idempotency keys failed: %v", err)
	}

	if expiredCount != 0 {
		t.Fatalf("expired idempotency keys remaining = %d, want 0", expiredCount)
	}
	if totalCount != 1 {
		t.Fatalf("idempotency key total = %d, want 1", totalCount)
	}
}

func startWorker(worker *Worker, ctx context.Context) {
	go func() {
		_ = worker.Start(ctx)
	}()
}

func waitForDoneJobs(t *testing.T, pool *pgxpool.Pool, want int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var doneCount int
		if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM job_queue WHERE status = 'done'`).Scan(&doneCount); err != nil {
			t.Fatalf("count done jobs failed: %v", err)
		}
		if doneCount == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	var doneCount int
	_ = pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM job_queue WHERE status = 'done'`).Scan(&doneCount)
	t.Fatalf("timed out waiting for done jobs: got %d want %d", doneCount, want)
}

func createOrgForJobQueue(t *testing.T, pool *pgxpool.Pool, slug string) repo.Organization {
	t.Helper()

	orgRepo := repo.NewOrgRepo(pool)
	org, err := orgRepo.Create(context.Background(), repo.Organization{Slug: slug, DisplayName: slug})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}
	return org
}
