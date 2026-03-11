//go:build integration

package scheduling

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestScheduleTickWorkerExecuteCallsMaybeFireForEnabledSchedules(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project, template := seedScheduleFixtures(t, ctx, pool)
	_ = org

	enabledA := createScheduleRecord(t, ctx, pool, org.ID, project.ID, template.ID, "enabled-a", true, "skip")
	enabledB := createScheduleRecord(t, ctx, pool, org.ID, project.ID, template.ID, "enabled-b", true, "skip")
	enabledC := createScheduleRecord(t, ctx, pool, org.ID, project.ID, template.ID, "enabled-c", true, "queue")
	disabled := createScheduleRecord(t, ctx, pool, org.ID, project.ID, template.ID, "disabled", false, "skip")

	engine := &recordingEngine{}
	worker := NewScheduleTickWorker(pool, engine, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := worker.Execute(ctx, jobqueue.Job{JobType: ScheduleTickJobType}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(engine.calls) != 3 {
		t.Fatalf("MaybeFire calls = %d, want 3", len(engine.calls))
	}

	seen := map[uuid.UUID]struct{}{}
	for _, id := range engine.calls {
		seen[id] = struct{}{}
	}
	for _, id := range []uuid.UUID{enabledA.ID, enabledB.ID, enabledC.ID} {
		if _, ok := seen[id]; !ok {
			t.Fatalf("enabled schedule %s was not fired", id)
		}
	}
	if _, ok := seen[disabled.ID]; ok {
		t.Fatalf("disabled schedule %s should not be fired", disabled.ID)
	}
}

func TestTaskScheduleServiceEnableDisableRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := eventbus.New(pool, logger, eventbus.Config{})
	org, project, template := seedScheduleFixtures(t, ctx, pool)

	schedule := createScheduleRecord(t, ctx, pool, org.ID, project.ID, template.ID, "toggle", false, "skip")
	service, err := NewTaskScheduleService(TaskScheduleServiceOptions{
		Pool:       pool,
		Events:     bus,
		Clock:      clock.NewFake(time.Date(2026, time.February, 24, 8, 0, 0, 0, time.UTC)),
		CronParser: NewCronParser(),
	})
	if err != nil {
		t.Fatalf("NewTaskScheduleService: %v", err)
	}

	enabled, err := service.Enable(ctx, schedule.ID)
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !enabled.IsEnabled {
		t.Fatalf("enabled flag = false, want true")
	}
	if enabled.NextFireAt == nil {
		t.Fatal("next_fire_at = nil, want value")
	}

	disabled, err := service.Disable(ctx, schedule.ID)
	if err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if disabled.IsEnabled {
		t.Fatalf("enabled flag = true, want false")
	}
	if disabled.NextFireAt != nil {
		t.Fatalf("next_fire_at = %v, want nil", *disabled.NextFireAt)
	}
}

func TestScheduleTickWorkerOverlapSkipEmitsSkippedEvent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := eventbus.New(pool, logger, eventbus.Config{})
	org, project, template := seedScheduleFixtures(t, ctx, pool)

	schedule := createScheduleRecord(t, ctx, pool, org.ID, project.ID, template.ID, "skip-overlap", true, "skip")
	taskService, err := tasksvc.NewService(tasksvc.Options{Pool: pool, EventBus: bus})
	if err != nil {
		t.Fatalf("New task service: %v", err)
	}

	scheduleID := schedule.ID
	if _, err := taskService.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:      schedule.ProjectID,
		Title:          "existing pending",
		FlowTemplateID: &schedule.FlowTemplateID,
		ScheduleID:     &scheduleID,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	}); err != nil {
		t.Fatalf("CreateTask existing pending: %v", err)
	}

	engine, err := NewScheduleEngine(ScheduleEngineOptions{
		Pool:       pool,
		Tasks:      taskService,
		Events:     bus,
		Clock:      clock.NewFake(time.Now().UTC().Add(5 * time.Minute)),
		Logger:     logger,
		CronParser: NewCronParser(),
	})
	if err != nil {
		t.Fatalf("NewScheduleEngine: %v", err)
	}

	worker := NewScheduleTickWorker(pool, engine, logger)
	if err := worker.Execute(ctx, jobqueue.Job{JobType: ScheduleTickJobType}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var taskCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM project_task WHERE schedule_id = $1`, schedule.ID).Scan(&taskCount); err != nil {
		t.Fatalf("count schedule tasks: %v", err)
	}
	if taskCount != 1 {
		t.Fatalf("schedule task count = %d, want 1", taskCount)
	}

	var skippedCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE event_type = 'system.schedule.skipped'
		  AND organization_id = $1
		  AND payload->>'schedule_id' = $2
	`, org.ID, schedule.ID.String()).Scan(&skippedCount); err != nil {
		t.Fatalf("count skipped events: %v", err)
	}
	if skippedCount == 0 {
		t.Fatal("expected at least one system.schedule.skipped event")
	}
}

type recordingEngine struct {
	calls []uuid.UUID
}

func (r *recordingEngine) MaybeFire(_ context.Context, schedule repo.TaskSchedule) error {
	r.calls = append(r.calls, schedule.ID)
	return nil
}

func seedScheduleFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (repo.Organization, repo.Project, repo.FlowTemplate) {
	t.Helper()

	orgRepo := repo.NewOrgRepo(pool)
	projectRepo := repo.NewProjectRepo(pool)
	templateRepo := repo.NewFlowTemplateRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "sched-org-" + uuid.NewString()[:8],
		DisplayName: "Scheduling Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "sched-proj-" + uuid.NewString()[:8],
		DisplayName:    "Scheduling Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "sched-flow-" + uuid.NewString()[:8],
		DisplayName:    "Scheduling Flow",
		Description:    "schedule",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	nodeRepo := repo.NewFlowNodeRepo(pool)
	workNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Scheduled Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create schedule work node: %v", err)
	}
	reviewNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Scheduled Review",
		NodeType:       "review",
		Position:       2,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create schedule review node: %v", err)
	}
	mergeNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Scheduled Merge",
		NodeType:       "merge",
		Position:       3,
		MaxVisits:      1,
	})
	if err != nil {
		t.Fatalf("create schedule merge node: %v", err)
	}
	workNode.NextNodeID = &reviewNode.ID
	if _, err := nodeRepo.Update(ctx, workNode); err != nil {
		t.Fatalf("link schedule work node: %v", err)
	}
	reviewNode.NextNodeID = &mergeNode.ID
	if _, err := nodeRepo.Update(ctx, reviewNode); err != nil {
		t.Fatalf("link schedule review node: %v", err)
	}
	template.StartNodeID = &workNode.ID
	template, err = templateRepo.Update(ctx, template)
	if err != nil {
		t.Fatalf("update schedule flow template start node: %v", err)
	}

	return org, project, template
}

func createScheduleRecord(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID, templateID uuid.UUID, name string, enabled bool, overlap string) repo.TaskSchedule {
	t.Helper()

	repository := repo.NewTaskScheduleRepo(pool)
	next := time.Now().UTC().Add(2 * time.Minute)
	schedule, err := repository.Create(ctx, repo.TaskSchedule{
		OrganizationID: orgID,
		ProjectID:      projectID,
		FlowTemplateID: templateID,
		DisplayName:    name,
		CronExpression: "* * * * *",
		OverlapPolicy:  overlap,
		IsEnabled:      enabled,
		NextFireAt:     &next,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create schedule %q: %v", name, err)
	}
	return schedule
}
