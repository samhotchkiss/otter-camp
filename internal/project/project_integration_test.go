//go:build integration

package project

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/samhotchkiss/otter-camp/internal/scheduling"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestProject_CRUD(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgRepo := repo.NewOrgRepo(pool)

	orgA, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "project-crud-a-" + uuid.NewString()[:8],
		DisplayName: "Project CRUD Org A",
	})
	if err != nil {
		t.Fatalf("create org A: %v", err)
	}
	orgB, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "project-crud-b-" + uuid.NewString()[:8],
		DisplayName: "Project CRUD Org B",
	})
	if err != nil {
		t.Fatalf("create org B: %v", err)
	}

	created, err := svc.Create(ctx, CreateProjectRequest{
		OrganizationID: orgA.ID,
		Slug:           "alpha",
		DisplayName:    "Alpha",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	loaded, err := svc.Get(ctx, orgA.ID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.ID != created.ID {
		t.Fatalf("loaded id = %s, want %s", loaded.ID, created.ID)
	}

	deliveryMode := "continuous"
	updated, err := svc.Update(ctx, orgA.ID, created.ID, UpdateProjectRequest{
		DeliveryMode:  &deliveryMode,
		UpdatedByType: "system",
		UpdatedByID:   uuid.Nil,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.DeliveryMode != deliveryMode {
		t.Fatalf("delivery_mode = %q, want %q", updated.DeliveryMode, deliveryMode)
	}

	_, err = svc.Create(ctx, CreateProjectRequest{
		OrganizationID: orgA.ID,
		Slug:           "alpha",
		DisplayName:    "Alpha Duplicate",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("duplicate slug err = %v, want ErrSlugTaken", err)
	}

	if _, err := svc.Create(ctx, CreateProjectRequest{
		OrganizationID: orgB.ID,
		Slug:           "alpha",
		DisplayName:    "Alpha Org B",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	}); err != nil {
		t.Fatalf("same slug across orgs should succeed: %v", err)
	}
}

func TestFlowTemplate_Immutability(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)

	org, projectRecord := seedProjectTestData073(t, ctx, pool)
	template, err := svc.CreateFlowTemplate(ctx, CreateFlowTemplateRequest{
		OrganizationID: &org.ID,
		ProjectID:      &projectRecord.ID,
		Slug:           "immutability-flow",
		DisplayName:    "Immutability Flow",
		Description:    "v1",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("CreateFlowTemplate: %v", err)
	}

	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      projectRecord.ID,
		Title:          "in-flight",
		WorkStatus:     "in_progress",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID:    nil,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create in-flight task: %v", err)
	}

	newName := "Immutability Flow v2"
	updated, err := svc.UpdateFlowTemplate(ctx, org.ID, template.ID, UpdateFlowTemplateRequest{
		DisplayName:   &newName,
		UpdatedByType: "system",
		UpdatedByID:   uuid.Nil,
	})
	if err != nil {
		t.Fatalf("UpdateFlowTemplate: %v", err)
	}
	if updated.ID == template.ID {
		t.Fatalf("updated template id should differ when template is in use")
	}
	if !updated.IsCurrent {
		t.Fatalf("updated template is_current = false, want true")
	}

	oldTemplate, err := repo.NewFlowTemplateRepo(pool).GetByID(ctx, template.ID)
	if err != nil {
		t.Fatalf("GetByID old template: %v", err)
	}
	if oldTemplate.IsCurrent {
		t.Fatalf("old template is_current = true, want false")
	}

	unchangedTask, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if unchangedTask.FlowTemplateID == nil || *unchangedTask.FlowTemplateID != template.ID {
		t.Fatalf("task flow_template_id changed = %v, want %s", unchangedTask.FlowTemplateID, template.ID)
	}
}

func TestTaskSchedule_CronValidation(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)

	org, projectRecord := seedProjectTestData073(t, ctx, pool)
	template, err := svc.CreateFlowTemplate(ctx, CreateFlowTemplateRequest{
		OrganizationID: &org.ID,
		ProjectID:      &projectRecord.ID,
		Slug:           "schedule-validation-flow",
		DisplayName:    "Schedule Validation",
		Description:    "cron",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("CreateFlowTemplate: %v", err)
	}

	_, err = svc.CreateSchedule(ctx, CreateScheduleRequest{
		OrganizationID: org.ID,
		ProjectID:      projectRecord.ID,
		FlowTemplateID: template.ID,
		DisplayName:    "Invalid Cron",
		CronExpression: "invalid cron",
		OverlapPolicy:  "skip",
		IsEnabled:      true,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if !errors.Is(err, ErrInvalidCronExpression) {
		t.Fatalf("invalid cron err = %v, want ErrInvalidCronExpression", err)
	}

	created, err := svc.CreateSchedule(ctx, CreateScheduleRequest{
		OrganizationID: org.ID,
		ProjectID:      projectRecord.ID,
		FlowTemplateID: template.ID,
		DisplayName:    "Valid Cron",
		CronExpression: "0 9 * * 1-5",
		OverlapPolicy:  "skip",
		IsEnabled:      true,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("CreateSchedule valid: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("created schedule id is nil")
	}
}

func TestTaskSchedule_OverlapPolicy(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := eventbus.New(pool, logger, eventbus.Config{})
	projectService, err := NewService(Options{Pool: pool, Events: bus})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	taskService, err := tasksvc.NewService(tasksvc.Options{Pool: pool, EventBus: bus})
	if err != nil {
		t.Fatalf("new task service: %v", err)
	}

	org, projectRecord := seedProjectTestData073(t, ctx, pool)
	template, err := projectService.CreateFlowTemplate(ctx, CreateFlowTemplateRequest{
		OrganizationID: &org.ID,
		ProjectID:      &projectRecord.ID,
		Slug:           "schedule-overlap-flow",
		DisplayName:    "Schedule Overlap",
		Description:    "skip overlap",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("CreateFlowTemplate: %v", err)
	}
	template = makeFlowTemplateExecutable073(t, ctx, pool, template)

	schedule, err := projectService.CreateSchedule(ctx, CreateScheduleRequest{
		OrganizationID: org.ID,
		ProjectID:      projectRecord.ID,
		FlowTemplateID: template.ID,
		DisplayName:    "Overlap Policy",
		CronExpression: "* * * * *",
		OverlapPolicy:  "skip",
		IsEnabled:      true,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	scheduleID := schedule.ID
	if _, err := taskService.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:      projectRecord.ID,
		Title:          "already in progress",
		FlowTemplateID: &template.ID,
		ScheduleID:     &scheduleID,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	}); err != nil {
		t.Fatalf("CreateTask existing task: %v", err)
	}

	engine, err := scheduling.NewScheduleEngine(scheduling.ScheduleEngineOptions{
		Pool:       pool,
		Tasks:      taskService,
		Events:     bus,
		Clock:      clock.NewFake(time.Now().UTC().Add(5 * time.Minute)),
		Logger:     logger,
		CronParser: scheduling.NewCronParser(),
	})
	if err != nil {
		t.Fatalf("NewScheduleEngine: %v", err)
	}
	worker := scheduling.NewScheduleTickWorker(pool, engine, logger)
	if err := worker.Execute(ctx, jobqueue.Job{JobType: scheduling.ScheduleTickJobType}); err != nil {
		t.Fatalf("Execute schedule tick: %v", err)
	}

	var taskCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM project_task WHERE schedule_id = $1`, schedule.ID).Scan(&taskCount); err != nil {
		t.Fatalf("count schedule tasks: %v", err)
	}
	if taskCount != 1 {
		t.Fatalf("task count for schedule = %d, want 1", taskCount)
	}

	var skippedEvents int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'system.schedule.skipped'
		  AND payload->>'schedule_id' = $2
	`, org.ID, schedule.ID.String()).Scan(&skippedEvents); err != nil {
		t.Fatalf("count skipped events: %v", err)
	}
	if skippedEvents == 0 {
		t.Fatal("expected system.schedule.skipped event")
	}
}

func seedProjectTestData073(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (repo.Organization, repo.Project) {
	t.Helper()

	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "proj-073-org-" + uuid.NewString()[:8],
		DisplayName: "Project 073 Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	projectRecord, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "proj-073-" + uuid.NewString()[:8],
		DisplayName:    "Project 073",
		DeliveryMode:   "gated",
		Settings:       json.RawMessage(`{}`),
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return org, projectRecord
}

func makeFlowTemplateExecutable073(t *testing.T, ctx context.Context, pool *pgxpool.Pool, template *FlowTemplate) *FlowTemplate {
	t.Helper()

	nodeRepo := repo.NewFlowNodeRepo(pool)
	workNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create work node: %v", err)
	}
	reviewNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       2,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create review node: %v", err)
	}
	workNode.NextNodeID = &reviewNode.ID
	if _, err := nodeRepo.Update(ctx, workNode); err != nil {
		t.Fatalf("link work node: %v", err)
	}

	updated, err := repo.NewFlowTemplateRepo(pool).Update(ctx, repo.FlowTemplate{
		ID:             template.ID,
		OrganizationID: template.OrganizationID,
		ProjectID:      template.ProjectID,
		Slug:           template.Slug,
		DisplayName:    template.DisplayName,
		Description:    template.Description,
		StartNodeID:    &workNode.ID,
		IsCurrent:      template.IsCurrent,
		Version:        template.Version,
		CreatedByType:  template.CreatedByType,
		CreatedByID:    template.CreatedByID,
	})
	if err != nil {
		t.Fatalf("update template start node: %v", err)
	}
	result := FlowTemplate(updated)
	return &result
}
