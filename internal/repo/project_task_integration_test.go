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

func TestProjectTaskRepoUpdateRejectsStaleSnapshot(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskRepoOrgProject(t, ctx, pool)
	taskRepo := NewProjectTaskRepo(pool)

	created, err := taskRepo.Create(ctx, ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Stale write task",
		WorkStatus:     "draft",
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	fresh, err := taskRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID fresh: %v", err)
	}
	stale, err := taskRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID stale: %v", err)
	}

	fresh.WorkStatus = "blocked"
	if _, err := taskRepo.Update(ctx, fresh); err != nil {
		t.Fatalf("Update fresh: %v", err)
	}

	stale.WorkStatus = "in_progress"
	if _, err := taskRepo.Update(ctx, stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update stale err = %v, want ErrConflict", err)
	}

	stored, err := taskRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID stored: %v", err)
	}
	if stored.WorkStatus != "blocked" {
		t.Fatalf("stored work_status = %q, want blocked", stored.WorkStatus)
	}
}

func TestProjectTaskRepoUpdateMetadataPreservesWorkStatus(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskRepoOrgProject(t, ctx, pool)
	taskRepo := NewProjectTaskRepo(pool)

	created, err := taskRepo.Create(ctx, ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Metadata-only update",
		WorkStatus:     "blocked",
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{"before":true}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := taskRepo.UpdateMetadata(ctx, created.ID, json.RawMessage(`{"after":true}`))
	if err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}
	if updated.WorkStatus != "blocked" {
		t.Fatalf("work_status = %q, want blocked", updated.WorkStatus)
	}
	var decoded map[string]bool
	if err := json.Unmarshal(updated.Metadata, &decoded); err != nil {
		t.Fatalf("Unmarshal metadata: %v", err)
	}
	if !decoded["after"] {
		t.Fatalf("metadata = %s, want after=true", string(updated.Metadata))
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated_at = %s, want after %s", updated.UpdatedAt, created.UpdatedAt)
	}
}

func TestProjectTaskRepoUpdateAssignedAgentPreservesWorkStatus(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskRepoOrgProject(t, ctx, pool)
	taskRepo := NewProjectTaskRepo(pool)
	agentRepo := NewAgentRepo(pool)

	created, err := taskRepo.Create(ctx, ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Assignment-only update",
		WorkStatus:     "blocked",
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{"before":true}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	assignedAgent, err := agentRepo.Create(ctx, Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Assigned worker",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "worker prompt",
		OperatorInstructions: "",
		AgentType:            "worker",
		IsStarterTrio:        false,
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("Create agent: %v", err)
	}

	updated, err := taskRepo.UpdateAssignedAgent(ctx, created.ID, &assignedAgent.ID)
	if err != nil {
		t.Fatalf("UpdateAssignedAgent: %v", err)
	}
	if updated.WorkStatus != "blocked" {
		t.Fatalf("work_status = %q, want blocked", updated.WorkStatus)
	}
	if updated.AssignedAgentID == nil || *updated.AssignedAgentID != assignedAgent.ID {
		t.Fatalf("assigned_agent_id = %v, want %s", updated.AssignedAgentID, assignedAgent.ID)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated_at = %s, want after %s", updated.UpdatedAt, created.UpdatedAt)
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

func TestProjectTaskEventRepoLatestBlockedReasonsByTask(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskRepoOrgProject(t, ctx, pool)
	taskRepo := NewProjectTaskRepo(pool)
	eventRepo := NewProjectTaskEventRepo(pool)

	blockedTask, err := taskRepo.Create(ctx, ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Blocked task",
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Create blocked task: %v", err)
	}
	rejectedTask, err := taskRepo.Create(ctx, ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Rejected task",
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Create rejected task: %v", err)
	}
	emptyTask, err := taskRepo.Create(ctx, ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Plain task",
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Create empty task: %v", err)
	}

	if _, err := eventRepo.Record(ctx, ProjectTaskEvent{
		TaskID:    blockedTask.ID,
		ProjectID: project.ID,
		EventType: "status.changed",
		ActorType: "system",
		Payload:   json.RawMessage(`{"to_status":"blocked","blocker_reason":"review turn repeatedly hit file.read not_found"}`),
	}); err != nil {
		t.Fatalf("Record blocked event: %v", err)
	}
	if _, err := eventRepo.Record(ctx, ProjectTaskEvent{
		TaskID:    rejectedTask.ID,
		ProjectID: project.ID,
		EventType: "status.changed",
		ActorType: "system",
		Payload:   json.RawMessage(`{"to_status":"blocked","blocker_reason":""}`),
	}); err != nil {
		t.Fatalf("Record initial rejected blocked event: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := eventRepo.Record(ctx, ProjectTaskEvent{
		TaskID:    rejectedTask.ID,
		ProjectID: project.ID,
		EventType: "flow.rejected",
		ActorType: "system",
		Payload:   json.RawMessage(`{"blocked_reason":"flow rejection max visits exceeded"}`),
	}); err != nil {
		t.Fatalf("Record flow.rejected event: %v", err)
	}

	reasons, err := eventRepo.LatestBlockedReasonsByTask(ctx, []uuid.UUID{blockedTask.ID, rejectedTask.ID, emptyTask.ID})
	if err != nil {
		t.Fatalf("LatestBlockedReasonsByTask: %v", err)
	}
	if got := reasons[blockedTask.ID]; got != "review turn repeatedly hit file.read not_found" {
		t.Fatalf("blockedTask reason = %q, want blocked reason", got)
	}
	if got := reasons[rejectedTask.ID]; got != "flow rejection max visits exceeded" {
		t.Fatalf("rejectedTask reason = %q, want flow rejection reason", got)
	}
	if _, ok := reasons[emptyTask.ID]; ok {
		t.Fatalf("emptyTask reason unexpectedly present: %q", reasons[emptyTask.ID])
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
