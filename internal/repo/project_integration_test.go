//go:build integration

package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestProjectRepoCreateGetBySlugAndOrgScopedUniqueness(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	projectRepo := NewProjectRepo(pool)

	orgA, err := orgRepo.Create(ctx, Organization{Slug: "proj-org-a", DisplayName: "Project Org A"})
	if err != nil {
		t.Fatalf("create org A: %v", err)
	}
	orgB, err := orgRepo.Create(ctx, Organization{Slug: "proj-org-b", DisplayName: "Project Org B"})
	if err != nil {
		t.Fatalf("create org B: %v", err)
	}

	created, err := projectRepo.Create(ctx, Project{
		OrganizationID: orgA.ID,
		Slug:           "alpha",
		DisplayName:    "Alpha",
		Description:    "Alpha project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	loaded, err := projectRepo.GetBySlug(ctx, orgA.ID, "alpha")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if loaded.ID != created.ID {
		t.Fatalf("GetBySlug id = %s, want %s", loaded.ID, created.ID)
	}

	_, err = projectRepo.Create(ctx, Project{
		OrganizationID: orgA.ID,
		Slug:           "alpha",
		DisplayName:    "Duplicate",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate slug in same org err = %v, want ErrConflict", err)
	}

	if _, err := projectRepo.Create(ctx, Project{
		OrganizationID: orgB.ID,
		Slug:           "alpha",
		DisplayName:    "Alpha Org B",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	}); err != nil {
		t.Fatalf("same slug in different org should succeed, got: %v", err)
	}
}

func TestFlowTemplateRepoDeprecateAndBulkUpsertBySlugIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	projectRepo := NewProjectRepo(pool)
	templateRepo := NewFlowTemplateRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "flow-org", DisplayName: "Flow Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := projectRepo.Create(ctx, Project{
		OrganizationID: org.ID,
		Slug:           "flow-project",
		DisplayName:    "Flow Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	created, err := templateRepo.Create(ctx, FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "default-flow",
		DisplayName:    "Default Flow",
		Description:    "v1",
		IsCurrent:      true,
		Version:        1,
		IsSystem:       false,
		CreatedByType:  "human_user",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	next, err := templateRepo.Deprecate(ctx, created.ID, FlowTemplate{
		DisplayName:   "Default Flow v2",
		Description:   "v2",
		CreatedByType: "human_user",
		CreatedByID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("Deprecate first call: %v", err)
	}
	if !next.IsCurrent || next.Version != 2 {
		t.Fatalf("new template current=%v version=%d, want current=true version=2", next.IsCurrent, next.Version)
	}

	nextAgain, err := templateRepo.Deprecate(ctx, created.ID, FlowTemplate{
		DisplayName:   "Default Flow v2",
		Description:   "v2",
		CreatedByType: "human_user",
		CreatedByID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("Deprecate second call: %v", err)
	}
	if nextAgain.ID != next.ID {
		t.Fatalf("Deprecate second call id = %s, want %s", nextAgain.ID, next.ID)
	}

	var versionedCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM flow_template
		WHERE organization_id = $1
		  AND project_id = $2
		  AND slug = 'default-flow'
	`, org.ID, project.ID).Scan(&versionedCount); err != nil {
		t.Fatalf("count versioned templates: %v", err)
	}
	if versionedCount != 2 {
		t.Fatalf("versioned template count = %d, want 2", versionedCount)
	}

	systemSeeds := []FlowTemplate{
		{
			Slug:          "default-single-agent",
			DisplayName:   "Default Single Agent",
			Description:   "system",
			IsCurrent:     true,
			Version:       1,
			IsSystem:      true,
			CreatedByType: "system",
			CreatedByID:   uuid.Nil,
		},
		{
			Slug:          "default-review",
			DisplayName:   "Default Review",
			Description:   "system",
			IsCurrent:     true,
			Version:       1,
			IsSystem:      true,
			CreatedByType: "system",
			CreatedByID:   uuid.Nil,
		},
		{
			Slug:          "default-review-refinement",
			DisplayName:   "Default Review Refinement",
			Description:   "system",
			IsCurrent:     true,
			Version:       1,
			IsSystem:      true,
			CreatedByType: "system",
			CreatedByID:   uuid.Nil,
		},
	}
	if _, err := templateRepo.BulkUpsertBySlug(ctx, systemSeeds); err != nil {
		t.Fatalf("BulkUpsertBySlug first call: %v", err)
	}
	if _, err := templateRepo.BulkUpsertBySlug(ctx, systemSeeds); err != nil {
		t.Fatalf("BulkUpsertBySlug second call: %v", err)
	}

	var systemCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM flow_template
		WHERE organization_id IS NULL
		  AND project_id IS NULL
		  AND slug IN ('default-single-agent', 'default-review', 'default-review-refinement')
	`).Scan(&systemCount); err != nil {
		t.Fatalf("count system templates: %v", err)
	}
	if systemCount != 3 {
		t.Fatalf("system template count = %d, want 3", systemCount)
	}
}

func TestTaskScheduleRepoUpdateLastFiredListDueAndOverlapPolicyConstraint(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	projectRepo := NewProjectRepo(pool)
	templateRepo := NewFlowTemplateRepo(pool)
	scheduleRepo := NewTaskScheduleRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "sched-org", DisplayName: "Sched Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := projectRepo.Create(ctx, Project{
		OrganizationID: org.ID,
		Slug:           "sched-project",
		DisplayName:    "Sched Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := templateRepo.Create(ctx, FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "sched-flow",
		DisplayName:    "Sched Flow",
		Description:    "sched",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	now := time.Now().UTC()
	past := now.Add(-1 * time.Minute)
	future := now.Add(5 * time.Minute)

	due, err := scheduleRepo.Create(ctx, TaskSchedule{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		FlowTemplateID: template.ID,
		DisplayName:    "due",
		CronExpression: "* * * * *",
		OverlapPolicy:  "skip",
		IsEnabled:      true,
		NextFireAt:     &past,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create due schedule: %v", err)
	}

	_, err = scheduleRepo.Create(ctx, TaskSchedule{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		FlowTemplateID: template.ID,
		DisplayName:    "future",
		CronExpression: "* * * * *",
		OverlapPolicy:  "skip",
		IsEnabled:      true,
		NextFireAt:     &future,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create future schedule: %v", err)
	}

	disabled, err := scheduleRepo.Create(ctx, TaskSchedule{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		FlowTemplateID: template.ID,
		DisplayName:    "disabled due",
		CronExpression: "* * * * *",
		OverlapPolicy:  "skip",
		IsEnabled:      false,
		NextFireAt:     &past,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create disabled due schedule: %v", err)
	}
	if _, err := scheduleRepo.Disable(ctx, disabled.ID); err != nil {
		t.Fatalf("disable schedule: %v", err)
	}

	dueItems, err := scheduleRepo.ListDue(ctx, now, 50)
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(dueItems) != 1 || dueItems[0].ID != due.ID {
		t.Fatalf("ListDue result = %+v, want only %s", dueItems, due.ID)
	}

	next := now.Add(10 * time.Minute)
	updated, err := scheduleRepo.UpdateLastFired(ctx, due.ID, now, &next)
	if err != nil {
		t.Fatalf("UpdateLastFired: %v", err)
	}
	if updated.LastFiredAt == nil || updated.NextFireAt == nil {
		t.Fatalf("UpdateLastFired did not set timestamps: %+v", updated)
	}

	afterUpdateDue, err := scheduleRepo.ListDue(ctx, now, 50)
	if err != nil {
		t.Fatalf("ListDue after update: %v", err)
	}
	if len(afterUpdateDue) != 0 {
		t.Fatalf("ListDue after update length = %d, want 0", len(afterUpdateDue))
	}

	_, err = scheduleRepo.Create(ctx, TaskSchedule{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		FlowTemplateID: template.ID,
		DisplayName:    "invalid overlap",
		CronExpression: "* * * * *",
		OverlapPolicy:  "bad-policy",
		IsEnabled:      true,
		NextFireAt:     &future,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid overlap policy error = %v, want ErrConflict", err)
	}
}
