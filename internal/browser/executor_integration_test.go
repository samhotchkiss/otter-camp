//go:build integration

package browser

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/testutil"
)

type integrationFixture struct {
	executor    *Executor
	taskService tasksvc.TaskService
	runRepo     *controlplane.RunRepository
	handoffRepo *BrowserHandoffRepository

	pool      *pgxpool.Pool
	orgID     uuid.UUID
	projectID uuid.UUID
	taskID    uuid.UUID
	agentID   uuid.UUID
	userID    uuid.UUID
	runID     uuid.UUID
	runStepID uuid.UUID
}

func TestBrowserSessionPartialUniqueIndexIntegration(t *testing.T) {
	fx := newIntegrationFixture(t)
	ctx := context.Background()

	if _, err := fx.pool.Exec(ctx, `
		INSERT INTO browser_session (task_id, project_id, agent_id, organization_id, status)
		VALUES ($1, $2, $3, $4, 'active')
	`, fx.taskID, fx.projectID, fx.agentID, fx.orgID); err != nil {
		t.Fatalf("insert first browser_session: %v", err)
	}

	_, err := fx.pool.Exec(ctx, `
		INSERT INTO browser_session (task_id, project_id, agent_id, organization_id, status)
		VALUES ($1, $2, $3, $4, 'active')
	`, fx.taskID, fx.projectID, fx.agentID, fx.orgID)
	if err == nil {
		t.Fatal("expected unique index conflict on second active browser_session")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("second insert error = %v, want pg code 23505", err)
	}
}

func TestBrowserExecutorDomainPolicyBlockIntegration(t *testing.T) {
	fx := newIntegrationFixture(t)
	ctx := context.Background()

	session, err := fx.executor.GetOrCreateSession(ctx, fx.taskID, fx.agentID, fx.projectID)
	if err != nil {
		t.Fatalf("GetOrCreateSession: %v", err)
	}

	_, execErr := fx.executor.ExecuteAction(ctx, session.ID, fx.runID, fx.runStepID, ExecuteActionInput{
		ActionType: "navigate",
		Input:      map[string]any{"url": "https://dashboard.stripe.com"},
	})
	if !errors.Is(execErr, ErrDomainPolicyDenied) {
		t.Fatalf("ExecuteAction error = %v, want ErrDomainPolicyDenied", execErr)
	}

	var status string
	if err := fx.pool.QueryRow(ctx, `
		SELECT status
		FROM browser_action
		WHERE browser_session_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, session.ID).Scan(&status); err != nil {
		t.Fatalf("query browser_action status: %v", err)
	}
	if status != StatusBlockedByDomainPolicy {
		t.Fatalf("browser_action status = %q, want %q", status, StatusBlockedByDomainPolicy)
	}
}

func TestBrowserExecutorStoresScreenshotsForNavigateClickTypeIntegration(t *testing.T) {
	fx := newIntegrationFixture(t)
	ctx := context.Background()

	session, err := fx.executor.GetOrCreateSession(ctx, fx.taskID, fx.agentID, fx.projectID)
	if err != nil {
		t.Fatalf("GetOrCreateSession: %v", err)
	}

	actions := []ExecuteActionInput{
		{ActionType: "navigate", Input: map[string]any{"url": "https://en.wikipedia.org/wiki/Go_(programming_language)"}},
		{ActionType: "click", Input: map[string]any{"selector": "#firstHeading"}},
		{ActionType: "type", Input: map[string]any{"selector": "input[name=q]", "text": "ottercamp"}},
	}
	for _, action := range actions {
		if _, err := fx.executor.ExecuteAction(ctx, session.ID, fx.runID, fx.runStepID, action); err != nil {
			t.Fatalf("ExecuteAction(%s): %v", action.ActionType, err)
		}
	}

	var count int
	if err := fx.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run_artifact
		WHERE run_id = $1
		  AND artifact_type = 'screenshot'
		  AND inline_content IS NULL
	`, fx.runID).Scan(&count); err != nil {
		t.Fatalf("count screenshot run_artifact: %v", err)
	}
	if count != 3 {
		t.Fatalf("screenshot artifact count = %d, want 3", count)
	}
}

func TestBrowserHandoffRoundTripViaInboxActIntegration(t *testing.T) {
	fx := newIntegrationFixture(t)
	ctx := context.Background()

	session, err := fx.executor.GetOrCreateSession(ctx, fx.taskID, fx.agentID, fx.projectID)
	if err != nil {
		t.Fatalf("GetOrCreateSession: %v", err)
	}

	handoff, err := fx.executor.RequestHandoff(ctx, session.ID, fx.runID, fx.agentID, fx.userID, "human verification needed")
	if err != nil {
		t.Fatalf("RequestHandoff: %v", err)
	}

	runPaused, err := fx.runRepo.Get(ctx, fx.runID)
	if err != nil {
		t.Fatalf("Get run after RequestHandoff: %v", err)
	}
	if runPaused.Status != "paused" {
		t.Fatalf("run status after RequestHandoff = %q, want paused", runPaused.Status)
	}

	if err := fx.taskService.ActOnInboxItem(ctx, handoff.InboxItemID, fx.userID, "completed", nil); err != nil {
		t.Fatalf("ActOnInboxItem completed: %v", err)
	}

	runResumed, err := fx.runRepo.Get(ctx, fx.runID)
	if err != nil {
		t.Fatalf("Get run after handoff completion: %v", err)
	}
	if runResumed.Status != "in_progress" {
		t.Fatalf("run status after handoff completion = %q, want in_progress", runResumed.Status)
	}

	stored, err := fx.handoffRepo.Get(ctx, handoff.ID)
	if err != nil {
		t.Fatalf("Get browser_handoff: %v", err)
	}
	if stored.Status != "completed" {
		t.Fatalf("browser_handoff status = %q, want completed", stored.Status)
	}
}

func newIntegrationFixture(t *testing.T) integrationFixture {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)

	orgID := testutil.MakeOrg(t, pool)
	user := testutil.MakeUser(t, pool, orgID, "admin")
	project := testutil.MakeProject(t, pool, orgID)
	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{CreatedByType: "system", Metadata: []byte(`{}`)})
	agent := testutil.MakeAgent(t, pool, orgID)

	runRepo := controlplane.NewRunRepository(pool)
	runRecord, err := runRepo.Create(ctx, controlplane.Run{
		OrganizationID: orgID,
		ProjectID:      &project.ID,
		TaskID:         &task.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		Status:         "in_progress",
		TriggerType:    "api",
		Metadata:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	runStepRepo := controlplane.NewRunStepRepository(pool)
	runStep, err := runStepRepo.Create(ctx, controlplane.RunStep{
		RunID:      runRecord.ID,
		StepNumber: 1,
		Status:     "pending",
	})
	if err != nil {
		t.Fatalf("create run_step: %v", err)
	}

	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	runService, err := controlplane.NewRunService(controlplane.RunServiceOptions{
		Pool:     pool,
		EventBus: bus,
	})
	if err != nil {
		t.Fatalf("NewRunService: %v", err)
	}

	store, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS store: %v", err)
	}

	executor, err := NewExecutor(ExecutorOptions{
		Pool:      pool,
		Runs:      runService,
		Artifacts: controlplane.NewRunArtifactRepository(pool),
		Store:     store,
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	t.Cleanup(executor.Shutdown)

	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:            pool,
		EventBus:        bus,
		BrowserHandoffs: executor,
	})
	if err != nil {
		t.Fatalf("task.NewService: %v", err)
	}

	return integrationFixture{
		executor:    executor,
		taskService: taskService,
		runRepo:     runRepo,
		handoffRepo: NewBrowserHandoffRepository(pool),
		pool:        pool,
		orgID:       orgID,
		projectID:   project.ID,
		taskID:      task.ID,
		agentID:     agent.ID,
		userID:      user.ID,
		runID:       runRecord.ID,
		runStepID:   runStep.ID,
	}
}
