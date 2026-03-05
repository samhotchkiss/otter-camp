//go:build integration

package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestRunServiceIntegrationLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)

	svc := newRunServiceIntegration(t, pool)

	runRecord, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       []byte(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := svc.StartRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	step, err := svc.CreateStep(ctx, runRecord.ID, "system.file.read", "tier2")
	if err != nil {
		t.Fatalf("CreateStep: %v", err)
	}
	if err := svc.StartStep(ctx, step.ID); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	if err := svc.CompleteStep(ctx, step.ID); err != nil {
		t.Fatalf("CompleteStep: %v", err)
	}
	if err := svc.CompleteRun(ctx, runRecord.ID, []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("CompleteRun: %v", err)
	}

	storedRun, err := NewRunRepository(pool).Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if storedRun.Status != "completed" {
		t.Fatalf("run status = %s, want completed", storedRun.Status)
	}

	storedStep, err := NewRunStepRepository(pool).Get(ctx, step.ID)
	if err != nil {
		t.Fatalf("Get step: %v", err)
	}
	if storedStep.Status != "completed" {
		t.Fatalf("step status = %s, want completed", storedStep.Status)
	}

	events, err := NewRunEventRepository(pool).ListByRun(ctx, runRecord.ID, 0)
	if err != nil {
		t.Fatalf("List run events: %v", err)
	}
	eventTypes := map[string]bool{}
	for _, event := range events {
		eventTypes[event.EventType] = true
	}
	for _, required := range []string{"run_started", "step_started", "step_completed", "run_completed"} {
		if !eventTypes[required] {
			t.Fatalf("missing run_event type %q", required)
		}
	}

	assertDomainEventCount(t, ctx, pool, org.ID, "run.created", 1)
	assertDomainEventCount(t, ctx, pool, org.ID, "run.completed", 1)
}

func TestRunServiceIntegrationRetryEnvelopeAndDeadLetter(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord := seedRunProject(t, ctx, pool, org.ID)
	taskRecord := seedRunTask(t, ctx, pool, org.ID, projectRecord.ID)

	svc := newRunServiceIntegration(t, pool)

	runRecord, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		ProjectID:      &projectRecord.ID,
		TaskID:         &taskRecord.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       []byte(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := svc.StartRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := svc.FailRun(ctx, runRecord.ID, "initial failure", "transient"); err != nil {
		t.Fatalf("FailRun: %v", err)
	}

	step, err := svc.CreateStep(ctx, runRecord.ID, "mcp.execute", "tier2")
	if err != nil {
		t.Fatalf("CreateStep: %v", err)
	}

	workerType := "mcp"
	attemptRepo := NewRunAttemptRepository(pool)
	_, err = attemptRepo.Create(ctx, RunAttempt{RunStepID: step.ID, AttemptNumber: 1, Trigger: "initial", Status: "failed", WorkerType: &workerType})
	if err != nil {
		t.Fatalf("seed attempt 1: %v", err)
	}

	if _, err := svc.CreateRetryAttempt(ctx, step.ID, "retry_transient"); err != nil {
		t.Fatalf("CreateRetryAttempt #2: %v", err)
	}
	if _, err := svc.CreateRetryAttempt(ctx, step.ID, "retry_transient"); err != nil {
		t.Fatalf("CreateRetryAttempt #3: %v", err)
	}
	if _, err := svc.CreateRetryAttempt(ctx, step.ID, "retry_transient"); !errors.Is(err, ErrMaxAttemptsExceeded) {
		t.Fatalf("CreateRetryAttempt #4 error = %v, want ErrMaxAttemptsExceeded", err)
	}

	updatedRun, err := NewRunRepository(pool).Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if updatedRun.Status != "dead_letter" {
		t.Fatalf("run status = %s, want dead_letter", updatedRun.Status)
	}

	var itemType, requestedType string
	if err := pool.QueryRow(ctx, `
		SELECT item_type, COALESCE(action_payload->>'requested_type', '')
		FROM inbox_item
		WHERE source_task_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, taskRecord.ID).Scan(&itemType, &requestedType); err != nil {
		t.Fatalf("load dead-letter inbox item: %v", err)
	}
	if itemType != "escalation" && !(itemType == "system_alert" && requestedType == "escalation") {
		t.Fatalf("dead-letter inbox item = (%s,%s), want escalation or system_alert/escalation", itemType, requestedType)
	}

	var deadLetteredCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'run.dead_lettered'
	`, org.ID).Scan(&deadLetteredCount); err != nil {
		t.Fatalf("count run.dead_lettered domain events: %v", err)
	}
	if deadLetteredCount == 0 {
		t.Fatal("expected run.dead_lettered domain_event")
	}

	var taskEventCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task_event
		WHERE task_id = $1
		  AND event_type = 'run_dead_lettered'
	`, taskRecord.ID).Scan(&taskEventCount); err != nil {
		t.Fatalf("count run_dead_lettered project_task_event: %v", err)
	}
	if taskEventCount == 0 {
		t.Fatal("expected project_task_event run_dead_lettered row")
	}
}

func TestRunServiceIntegrationIdempotencyConcurrentCreateRun(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	svc := newRunServiceIntegration(t, pool)

	key := "integration-idempotency-key"
	const workers = 12

	var (
		wg    sync.WaitGroup
		errCh = make(chan error, workers)
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.CreateRun(ctx, CreateRunInput{
				OrganizationID: org.ID,
				PrincipalType:  "system",
				PrincipalID:    uuid.Nil,
				TriggerType:    "api",
				IdempotencyKey: &key,
				Metadata:       []byte(`{"run_mode":"sync"}`),
			})
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent CreateRun error: %v", err)
		}
	}

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run
		WHERE organization_id = $1
		  AND idempotency_key = $2
	`, org.ID, key).Scan(&count); err != nil {
		t.Fatalf("count idempotent runs: %v", err)
	}
	if count != 1 {
		t.Fatalf("run rows for idempotency key = %d, want 1", count)
	}
}

func TestSupervisorIntegrationDetectOrphanedRunsInitiatesRecovery(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	svc := newRunServiceIntegration(t, pool)

	runRecord, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       []byte(`{"run_mode":"sync"}`),
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

	storedRun, err := NewRunRepository(pool).Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if storedRun.Status != "failed" {
		t.Fatalf("orphaned run status = %s, want failed", storedRun.Status)
	}

	var recoveryCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run
		WHERE organization_id = $1
		  AND trigger_type = 'supervisor'
	`, org.ID).Scan(&recoveryCount); err != nil {
		t.Fatalf("count supervisor recovery runs: %v", err)
	}
	if recoveryCount == 0 {
		t.Fatal("expected supervisor recovery run")
	}
}

func newRunServiceIntegration(t *testing.T, pool *pgxpool.Pool) RunService {
	t.Helper()
	bus := eventbus.New(pool, newDiscardLogger(), eventbus.Config{})
	svc, err := NewRunService(RunServiceOptions{
		Pool:     pool,
		EventBus: bus,
		Policy:   allowRunCreationPolicy{},
		Logger:   newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewRunService: %v", err)
	}
	return svc
}

type allowRunCreationPolicy struct{}

func (allowRunCreationPolicy) EvaluateRunCreation(context.Context, uuid.UUID, Principal) (RunCreationPolicyDecision, error) {
	return RunCreationPolicyDecision{Allowed: true}, nil
}

func seedRunProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) repo.Project {
	t.Helper()
	projectRecord, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: orgID,
		Slug:           "cp-proj-" + uuid.NewString()[:8],
		DisplayName:    "Control Plane Project",
		Description:    "",
		DeliveryMode:   "gated",
		Settings:       json.RawMessage(`{}`),
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return projectRecord
}

func seedRunTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID uuid.UUID) repo.ProjectTask {
	t.Helper()
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "cp-run-flow-" + uuid.NewString()[:8],
		DisplayName:    "Control Plane Run Flow",
		Description:    "Flow template for queued control-plane test tasks",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      projectID,
		Title:          "Control Plane Task",
		WorkStatus:     "queued",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID:    nil,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return taskRecord
}

func assertDomainEventCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, eventType string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = $2
	`, orgID, eventType).Scan(&count); err != nil {
		t.Fatalf("count domain_event %s: %v", eventType, err)
	}
	if count != want {
		t.Fatalf("domain_event %s count = %d, want %d", eventType, count, want)
	}
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
