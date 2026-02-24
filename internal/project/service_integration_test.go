//go:build integration

package project

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestProjectServiceCreateGetBySlugAndUniqueness(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgRepo := repo.NewOrgRepo(pool)

	orgA, err := orgRepo.Create(ctx, repo.Organization{Slug: "proj-svc-org-a", DisplayName: "Project Service Org A"})
	if err != nil {
		t.Fatalf("create org A: %v", err)
	}
	orgB, err := orgRepo.Create(ctx, repo.Organization{Slug: "proj-svc-org-b", DisplayName: "Project Service Org B"})
	if err != nil {
		t.Fatalf("create org B: %v", err)
	}

	created, err := svc.Create(ctx, CreateProjectRequest{
		OrganizationID: orgA.ID,
		Slug:           "alpha",
		DisplayName:    "Alpha",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	loaded, err := svc.GetBySlug(ctx, orgA.ID, "alpha")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if loaded.ID != created.ID {
		t.Fatalf("GetBySlug id = %s, want %s", loaded.ID, created.ID)
	}

	_, err = svc.Create(ctx, CreateProjectRequest{
		OrganizationID: orgA.ID,
		Slug:           "alpha",
		DisplayName:    "Alpha Duplicate",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
	})
	if !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("duplicate slug in same org err = %v, want ErrSlugTaken", err)
	}

	if _, err := svc.Create(ctx, CreateProjectRequest{
		OrganizationID: orgB.ID,
		Slug:           "alpha",
		DisplayName:    "Alpha Org B",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
	}); err != nil {
		t.Fatalf("same slug in different org should succeed, got: %v", err)
	}
}

func TestProjectServiceDeleteActiveTasksBlocked(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgID, projectID := seedProject(t, ctx, pool)
	ensureProjectTaskTable(t, ctx, pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO project_task (project_id, work_status)
		VALUES ($1, 'in_progress')
	`, projectID); err != nil {
		t.Fatalf("insert active project_task row: %v", err)
	}

	err := svc.Delete(ctx, orgID, projectID)
	if !errors.Is(err, ErrProjectHasActiveTasks) {
		t.Fatalf("Delete err = %v, want ErrProjectHasActiveTasks", err)
	}
}

func TestProjectServiceUpdateFlowTemplateInUseCreatesNewVersion(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgID, projectID := seedProject(t, ctx, pool)
	templateRepo := repo.NewFlowTemplateRepo(pool)
	ensureProjectTaskTable(t, ctx, pool)

	template, err := svc.CreateFlowTemplate(ctx, CreateFlowTemplateRequest{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "deploy-flow",
		DisplayName:    "Deploy Flow",
		Description:    "v1",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateFlowTemplate: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO project_task (project_id, flow_template_id, work_status)
		VALUES ($1, $2, 'in_progress')
	`, projectID, template.ID); err != nil {
		t.Fatalf("insert in-use project_task row: %v", err)
	}

	newName := "Deploy Flow v2"
	newSlug := "deploy-flow-v2"
	updated, err := svc.UpdateFlowTemplate(ctx, orgID, template.ID, UpdateFlowTemplateRequest{
		DisplayName:   &newName,
		Slug:          &newSlug,
		UpdatedByType: "system",
	})
	if err != nil {
		t.Fatalf("UpdateFlowTemplate: %v", err)
	}
	if updated.ID == template.ID {
		t.Fatalf("updated template id should change when in use")
	}
	if updated.Version != template.Version+1 {
		t.Fatalf("updated version = %d, want %d", updated.Version, template.Version+1)
	}
	if updated.Slug != newSlug {
		t.Fatalf("updated slug = %q, want %q", updated.Slug, newSlug)
	}

	oldRow, err := templateRepo.GetByID(ctx, template.ID)
	if err != nil {
		t.Fatalf("GetByID old template: %v", err)
	}
	if oldRow.IsCurrent {
		t.Fatalf("old template is_current = true, want false")
	}
}

func TestProjectServiceUpdateFlowTemplateNotInUseUpdatesInPlace(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgID, projectID := seedProject(t, ctx, pool)
	templateRepo := repo.NewFlowTemplateRepo(pool)

	template, err := svc.CreateFlowTemplate(ctx, CreateFlowTemplateRequest{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "review-flow",
		DisplayName:    "Review Flow",
		Description:    "v1",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateFlowTemplate: %v", err)
	}

	newName := "Review Flow Updated"
	updated, err := svc.UpdateFlowTemplate(ctx, orgID, template.ID, UpdateFlowTemplateRequest{
		DisplayName:   &newName,
		UpdatedByType: "system",
	})
	if err != nil {
		t.Fatalf("UpdateFlowTemplate: %v", err)
	}
	if updated.ID != template.ID {
		t.Fatalf("updated template id = %s, want %s", updated.ID, template.ID)
	}

	all, err := templateRepo.ListCurrent(ctx, &orgID, &projectID)
	if err != nil {
		t.Fatalf("ListCurrent: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("current template count = %d, want 1", len(all))
	}
	if all[0].DisplayName != newName {
		t.Fatalf("updated display_name = %q, want %q", all[0].DisplayName, newName)
	}
}

func TestProjectServiceCreateListAndDeleteSchedule(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgID, projectA := seedProject(t, ctx, pool)
	_, projectB := seedProjectWithSlug(t, ctx, pool, orgID, "sched-project-b")

	templateA, err := svc.CreateFlowTemplate(ctx, CreateFlowTemplateRequest{
		OrganizationID: &orgID,
		ProjectID:      &projectA,
		Slug:           "schedule-flow-a",
		DisplayName:    "Schedule Flow A",
		Description:    "A",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateFlowTemplate A: %v", err)
	}

	schedule, err := svc.CreateSchedule(ctx, CreateScheduleRequest{
		OrganizationID: orgID,
		ProjectID:      projectA,
		FlowTemplateID: templateA.ID,
		DisplayName:    "Daily Trigger",
		CronExpression: "0 9 * * 1-5",
		OverlapPolicy:  "skip",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if schedule.NextFireAt == nil {
		t.Fatal("next_fire_at is nil, want computed value")
	}

	schedulesA, err := svc.ListSchedules(ctx, projectA)
	if err != nil {
		t.Fatalf("ListSchedules project A: %v", err)
	}
	if len(schedulesA) != 1 {
		t.Fatalf("project A schedule count = %d, want 1", len(schedulesA))
	}

	schedulesB, err := svc.ListSchedules(ctx, projectB)
	if err != nil {
		t.Fatalf("ListSchedules project B: %v", err)
	}
	if len(schedulesB) != 0 {
		t.Fatalf("project B schedule count = %d, want 0", len(schedulesB))
	}

	if err := svc.DeleteSchedule(ctx, schedule.ID); err != nil {
		t.Fatalf("DeleteSchedule: %v", err)
	}
	if _, err := svc.GetSchedule(ctx, schedule.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("GetSchedule after delete err = %v, want repo.ErrNotFound", err)
	}
}

func newIntegrationService(t *testing.T, pool *pgxpool.Pool) ProjectService {
	t.Helper()
	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	svc, err := NewService(Options{Pool: pool, Events: bus})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func seedProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	orgRepo := repo.NewOrgRepo(pool)
	projectRepo := repo.NewProjectRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{Slug: "proj-svc-org-" + uuid.NewString()[:8], DisplayName: "Project Service Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "proj-svc-" + uuid.NewString()[:8],
		DisplayName:    "Project Service Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return org.ID, project.ID
}

func seedProjectWithSlug(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, slug string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	projectRepo := repo.NewProjectRepo(pool)

	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: orgID,
		Slug:           slug,
		DisplayName:    "Project " + slug,
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project (%s): %v", slug, err)
	}
	return orgID, project.ID
}

func ensureProjectTaskTable(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS project_task (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			project_id uuid NOT NULL,
			flow_template_id uuid,
			work_status text NOT NULL
		)
	`); err != nil {
		t.Fatalf("create test project_task table: %v", err)
	}
}
