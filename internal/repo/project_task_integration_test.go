//go:build integration

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestProjectTaskRepoCreateUpdateStatusAndTaskNumber(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskRepoOrgProject(t, ctx, pool)
	taskRepo := NewProjectTaskRepo(pool)
	template := seedTaskRepoFlowTemplate(t, ctx, pool, org.ID, project.ID)

	created, err := taskRepo.Create(ctx, ProjectTask{
		OrganizationID:      org.ID,
		ProjectID:           project.ID,
		Title:               "First task",
		WorkStatus:          "draft",
		FlowTemplateID:      &template.ID,
		Priority:            2,
		CreatedByType:       "system",
		CreatedByID:         nil,
		RequiresHumanReview: false,
		Metadata:            json.RawMessage(`{"source":"integration"}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.TaskNumber != 1 {
		t.Fatalf("task_number = %d, want 1", created.TaskNumber)
	}
	if created.Priority != 2 {
		t.Fatalf("priority = %d, want 2", created.Priority)
	}

	time.Sleep(10 * time.Millisecond)
	updated, err := taskRepo.UpdateStatus(ctx, created.ID, "in_progress")
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if updated.WorkStatus != "in_progress" {
		t.Fatalf("work_status = %q, want %q", updated.WorkStatus, "in_progress")
	}
	if updated.Priority != 2 {
		t.Fatalf("priority after status update = %d, want 2", updated.Priority)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated_at = %s, want after %s", updated.UpdatedAt, created.UpdatedAt)
	}
}

func TestProjectTaskRepoRejectsExecutionStatusWithoutFlowTemplate(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskRepoOrgProject(t, ctx, pool)
	taskRepo := NewProjectTaskRepo(pool)

	created, err := taskRepo.Create(ctx, ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Missing flow template",
		WorkStatus:     "draft",
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Create task: %v", err)
	}

	if _, err := taskRepo.UpdateStatus(ctx, created.ID, "queued"); !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateStatus queued err = %v, want ErrConflict", err)
	}

	var invalidCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task
		WHERE work_status IN ('queued', 'in_progress', 'review', 'done')
		  AND flow_template_id IS NULL
	`).Scan(&invalidCount); err != nil {
		t.Fatalf("count invalid execution tasks: %v", err)
	}
	if invalidCount != 0 {
		t.Fatalf("invalid execution task count = %d, want 0", invalidCount)
	}
}

func TestProjectTaskRepoCreateConcurrentTaskNumbersUniquePerProject(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskRepoOrgProject(t, ctx, pool)
	taskRepo := NewProjectTaskRepo(pool)

	const workers = 10
	type result struct {
		number int
		err    error
	}
	results := make(chan result, workers)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			created, err := taskRepo.Create(ctx, ProjectTask{
				OrganizationID: org.ID,
				ProjectID:      project.ID,
				Title:          "Task " + uuid.NewString()[:8],
				CreatedByType:  "system",
				Metadata:       json.RawMessage(`{}`),
			})
			if err != nil {
				results <- result{err: err}
				return
			}
			results <- result{number: created.TaskNumber}
		}()
	}
	wg.Wait()
	close(results)

	seen := make(map[int]struct{}, workers)
	for item := range results {
		if item.err != nil {
			t.Fatalf("Create concurrent task: %v", item.err)
		}
		if _, exists := seen[item.number]; exists {
			t.Fatalf("duplicate task_number generated: %d", item.number)
		}
		seen[item.number] = struct{}{}
	}
	if len(seen) != workers {
		t.Fatalf("unique task_number count = %d, want %d", len(seen), workers)
	}
}

func TestProjectTaskEventRepoRecordAndListByTaskOrder(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskRepoOrgProject(t, ctx, pool)
	taskRepo := NewProjectTaskRepo(pool)
	eventRepo := NewProjectTaskEventRepo(pool)

	task, err := taskRepo.Create(ctx, ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Event task",
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Create task: %v", err)
	}

	if _, err := eventRepo.Record(ctx, ProjectTaskEvent{
		TaskID:    task.ID,
		ProjectID: project.ID,
		EventType: "task.created",
		ActorType: "system",
		Payload:   json.RawMessage(`{"step":1}`),
	}); err != nil {
		t.Fatalf("Record first event: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := eventRepo.Record(ctx, ProjectTaskEvent{
		TaskID:    task.ID,
		ProjectID: project.ID,
		EventType: "status.changed",
		ActorType: "supervisor",
		Payload:   json.RawMessage(`{"from_status":"draft","to_status":"queued"}`),
	}); err != nil {
		t.Fatalf("Record second event: %v", err)
	}

	events, err := eventRepo.ListByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}
	if events[0].EventType != "status.changed" {
		t.Fatalf("first event type = %q, want %q", events[0].EventType, "status.changed")
	}
}

func TestInboxItemRepoExpireDueAndListForUserExcludesActedByDefault(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, _ := seedTaskRepoOrgProject(t, ctx, pool)
	user := seedTaskRepoUser(t, ctx, pool, org.ID, "inbox-user")
	inboxRepo := NewInboxItemRepo(pool)

	expiresAt := time.Now().UTC().Add(-1 * time.Minute)
	item, err := inboxRepo.Create(ctx, InboxItem{
		OrganizationID: org.ID,
		TargetUserID:   &user.ID,
		ItemType:       "human_approval_required",
		CreatedByType:  "system",
		Title:          "Approval needed",
		ActionPayload:  json.RawMessage(`{}`),
		ExpiresAt:      &expiresAt,
	})
	if err != nil {
		t.Fatalf("Create inbox item: %v", err)
	}

	itemsBefore, err := inboxRepo.ListForUser(ctx, org.ID, user.ID, InboxListOptions{})
	if err != nil {
		t.Fatalf("ListForUser before expire: %v", err)
	}
	if len(itemsBefore) != 1 || itemsBefore[0].ID != item.ID {
		t.Fatalf("ListForUser before expire len/id mismatch: len=%d", len(itemsBefore))
	}

	affected, err := inboxRepo.ExpireDue(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("ExpireDue: %v", err)
	}
	if affected != 1 {
		t.Fatalf("ExpireDue affected = %d, want 1", affected)
	}

	itemsAfter, err := inboxRepo.ListForUser(ctx, org.ID, user.ID, InboxListOptions{})
	if err != nil {
		t.Fatalf("ListForUser after expire: %v", err)
	}
	if len(itemsAfter) != 0 {
		t.Fatalf("ListForUser after expire len = %d, want 0", len(itemsAfter))
	}
}

func TestMergeQueueEntryRepoEnqueueListArchiveLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskRepoOrgProject(t, ctx, pool)
	taskRepo := NewProjectTaskRepo(pool)
	queueRepo := NewMergeQueueEntryRepo(pool)

	task, err := taskRepo.Create(ctx, ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Merge me",
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Create task: %v", err)
	}

	enqueued, err := queueRepo.Enqueue(ctx, MergeQueueEntry{
		ProjectID:  project.ID,
		TaskID:     task.ID,
		BranchName: "feature/task-" + uuid.NewString()[:8],
		Metadata:   json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if enqueued.Position != 1 {
		t.Fatalf("position = %d, want 1", enqueued.Position)
	}

	active, err := queueRepo.ListActive(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListActive before archive: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("active count before archive = %d, want 1", len(active))
	}

	mergedAt := time.Now().UTC()
	if _, err := queueRepo.UpdateStatus(ctx, enqueued.ID, "merged", nil, &mergedAt); err != nil {
		t.Fatalf("UpdateStatus merged: %v", err)
	}
	if _, err := queueRepo.Archive(ctx, enqueued.ID, time.Now().UTC()); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	activeAfterArchive, err := queueRepo.ListActive(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListActive after archive: %v", err)
	}
	if len(activeAfterArchive) != 0 {
		t.Fatalf("active count after archive = %d, want 0", len(activeAfterArchive))
	}
}

func seedTaskRepoOrgProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (Organization, Project) {
	t.Helper()

	orgRepo := NewOrgRepo(pool)
	projectRepo := NewProjectRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{
		Slug:        "task-repo-org-" + uuid.NewString()[:8],
		DisplayName: "Task Repo Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := projectRepo.Create(ctx, Project{
		OrganizationID: org.ID,
		Slug:           "task-repo-project-" + uuid.NewString()[:8],
		DisplayName:    "Task Repo Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return org, project
}

func seedTaskRepoFlowTemplate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID uuid.UUID) FlowTemplate {
	t.Helper()
	template, err := NewFlowTemplateRepo(pool).Create(ctx, FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "task-repo-flow-" + uuid.NewString()[:8],
		DisplayName:    "Task Repo Flow",
		Description:    "Task repo integration flow template",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	return template
}

func seedTaskRepoUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, prefix string) HumanUser {
	t.Helper()
	userRepo := NewHumanUserRepo(pool)
	user, err := userRepo.Create(ctx, HumanUser{
		OrganizationID: orgID,
		Email:          prefix + "+" + uuid.NewString()[:8] + "@example.com",
		DisplayName:    prefix,
		Role:           "member",
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}
