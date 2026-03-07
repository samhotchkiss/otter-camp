//go:build integration

package task

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestTaskServiceIntegrationStatusLifecycleAndEvents(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	svc := newTaskIntegrationService(t, pool)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)

	created, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Lifecycle task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	steps := []string{"queued", "in_progress", "review", "done"}
	current := created
	for _, step := range steps {
		actor := Actor{Type: "system"}
		if step == "in_progress" {
			actor.AllowNoActiveFlow = true
		}
		if step == "done" {
			actor.AllowDoneBypass = true
		}
		next, stepErr := svc.TransitionStatus(ctx, current.ID, step, actor)
		if stepErr != nil {
			t.Fatalf("TransitionStatus %s: %v", step, stepErr)
		}
		current = next
	}
	if current.WorkStatus != "done" {
		t.Fatalf("work_status = %q, want %q", current.WorkStatus, "done")
	}
	if current.CompletedAt == nil {
		t.Fatal("completed_at is nil after done transition")
	}

	taskEvents, err := repo.NewProjectTaskEventRepo(pool).ListByTask(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListByTask events: %v", err)
	}
	var createdCount, statusCount int
	for _, event := range taskEvents {
		switch event.EventType {
		case "task.created":
			createdCount++
		case "status.changed":
			statusCount++
		}
	}
	if createdCount < 1 {
		t.Fatalf("task.created events = %d, want >= 1", createdCount)
	}
	if statusCount != 4 {
		t.Fatalf("status.changed events = %d, want 4", statusCount)
	}

	var domainCreated, domainStatusChanged int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'task.created'
	`, org.ID).Scan(&domainCreated); err != nil {
		t.Fatalf("count domain task.created: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'task.status_changed'
	`, org.ID).Scan(&domainStatusChanged); err != nil {
		t.Fatalf("count domain task.status_changed: %v", err)
	}
	if domainCreated < 1 {
		t.Fatalf("domain task.created count = %d, want >= 1", domainCreated)
	}
	if domainStatusChanged != 4 {
		t.Fatalf("domain task.status_changed count = %d, want 4", domainStatusChanged)
	}
}

func TestTaskServiceIntegrationRejectsQueueWhenOutstandingProjectGateExistsEX256(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	svc := newTaskIntegrationService(t, pool)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)

	taskRepo := repo.NewProjectTaskRepo(pool)
	gateTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Bootstrap governance gate",
		WorkStatus:     "draft",
		BlocksScope:    "all",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{"bootstrap_gate":true}`),
	})
	if err != nil {
		t.Fatalf("create gate task: %v", err)
	}

	regularTask, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Regular queued task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask regular: %v", err)
	}

	_, err = svc.TransitionStatus(ctx, regularTask.ID, "queued", Actor{Type: "system"})
	if !errors.Is(err, ErrProjectGateBlockingQueue) {
		t.Fatalf("TransitionStatus queued err = %v, want ErrProjectGateBlockingQueue", err)
	}
	if !strings.Contains(err.Error(), gateTask.Title) {
		t.Fatalf("error = %q, want gate title context", err.Error())
	}

	stored, err := taskRepo.GetByID(ctx, regularTask.ID)
	if err != nil {
		t.Fatalf("GetByID regular task: %v", err)
	}
	if stored.WorkStatus != "draft" {
		t.Fatalf("regular task work_status = %q, want draft after blocked queue attempt", stored.WorkStatus)
	}
}

func TestTaskServiceIntegrationHumanApprovalGate(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{"requires_human_review":true}`))
	pmUser := seedTaskServiceUser(t, ctx, pool, org.ID, "pm-user", "admin")
	pmAgent := seedTaskServiceAgent(t, ctx, pool, org.ID, "PM Agent", "staff", "pm", "human_user", pmUser.ID)
	assignPMToProject(t, ctx, pool, pmAgent.ID, project.ID)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)

	svc := newTaskIntegrationService(t, pool)

	taskRecord, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Needs approval",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if !taskRecord.RequiresHumanReview {
		t.Fatal("requires_human_review = false, want true")
	}
	if taskRecord.WorkStatus != "draft" {
		t.Fatalf("initial work_status = %q, want %q", taskRecord.WorkStatus, "draft")
	}

	inboxItem, err := svc.RequestHumanApproval(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("RequestHumanApproval: %v", err)
	}
	if inboxItem.ItemType != "human_approval_required" {
		t.Fatalf("inbox item_type = %q, want %q", inboxItem.ItemType, "human_approval_required")
	}

	approved, err := svc.ApproveTask(ctx, taskRecord.ID, pmUser.ID)
	if err != nil {
		t.Fatalf("ApproveTask: %v", err)
	}
	if approved.WorkStatus != "queued" {
		t.Fatalf("work_status after approve = %q, want %q", approved.WorkStatus, "queued")
	}

	storedItem, err := repo.NewInboxItemRepo(pool).GetByID(ctx, inboxItem.ID)
	if err != nil {
		t.Fatalf("GetByID inbox item: %v", err)
	}
	if !storedItem.IsActed {
		t.Fatal("inbox item is_acted = false, want true")
	}
}

func TestTaskServiceIntegrationRejectsFlowTemplateWithoutReviewNode(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "task-svc-invalid-flow-" + uuid.NewString()[:8],
		DisplayName:    "Invalid Flow Without Review",
		Description:    "integration test invalid flow",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create invalid flow template: %v", err)
	}

	flowNodeRepo := repo.NewFlowNodeRepo(pool)
	if _, err := flowNodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      10,
	}); err != nil {
		t.Fatalf("create non-review node: %v", err)
	}

	svc := newTaskIntegrationService(t, pool)
	if _, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Invalid review-less template",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
	}); !errors.Is(err, ErrFlowTemplateReviewRequired) {
		t.Fatalf("CreateTask err = %v, want ErrFlowTemplateReviewRequired", err)
	}

	taskRepo := repo.NewProjectTaskRepo(pool)
	direct, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Direct draft for transition check",
		WorkStatus:     "draft",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create direct draft task: %v", err)
	}

	if _, err := svc.TransitionStatus(ctx, direct.ID, "queued", Actor{Type: "system"}); !errors.Is(err, ErrFlowTemplateReviewRequired) {
		t.Fatalf("TransitionStatus queued err = %v, want ErrFlowTemplateReviewRequired", err)
	}
}

func TestTaskServiceIntegrationMarkBlockedDoesNotCreateResolutionTaskByDefault(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	pmUser := seedTaskServiceUser(t, ctx, pool, org.ID, "pm-user", "admin")
	pmAgent := seedTaskServiceAgent(t, ctx, pool, org.ID, "PM Agent", "staff", "pm", "human_user", pmUser.ID)
	assignPMToProject(t, ctx, pool, pmAgent.ID, project.ID)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)

	svc := newTaskIntegrationService(t, pool)
	taskRepo := repo.NewProjectTaskRepo(pool)
	inboxRepo := repo.NewInboxItemRepo(pool)

	created, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Work item",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.TransitionStatus(ctx, created.ID, "queued", Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus queued: %v", err)
	}
	if _, err := svc.TransitionStatus(ctx, created.ID, "in_progress", Actor{Type: "system", AllowNoActiveFlow: true}); err != nil {
		t.Fatalf("TransitionStatus in_progress: %v", err)
	}

	blocked, err := svc.MarkBlocked(ctx, created.ID, "dependency missing", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}
	if blocked.WorkStatus != "blocked" {
		t.Fatalf("blocked work_status = %q, want %q", blocked.WorkStatus, "blocked")
	}

	tasks, err := taskRepo.ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("project task count = %d, want 1", len(tasks))
	}

	inboxItems, err := inboxRepo.ListForUser(ctx, org.ID, pmUser.ID, repo.InboxListOptions{
		ItemType:     "blocker_filed",
		IncludeActed: true,
		Limit:        50,
	})
	if err != nil {
		t.Fatalf("ListForUser blocker inbox: %v", err)
	}
	if len(inboxItems) == 0 {
		t.Fatal("expected blocker_filed inbox item")
	}

	if _, err := svc.TransitionStatus(ctx, blocked.ID, "queued", Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus blocked->queued: %v", err)
	}
}

func TestTaskServiceIntegrationMarkBlockedCreatesResolutionTaskWhenPolicyRequiresIt(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	pmUser := seedTaskServiceUser(t, ctx, pool, org.ID, "pm-user", "admin")
	pmAgent := seedTaskServiceAgent(t, ctx, pool, org.ID, "PM Agent", "staff", "pm", "human_user", pmUser.ID)
	assignPMToProject(t, ctx, pool, pmAgent.ID, project.ID)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)

	svc := newTaskIntegrationService(t, pool)
	taskRepo := repo.NewProjectTaskRepo(pool)

	created, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Work item",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		Metadata:       ApplyBlockerAutoResolutionTask(json.RawMessage(`{}`), true),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.TransitionStatus(ctx, created.ID, "queued", Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus queued: %v", err)
	}
	if _, err := svc.TransitionStatus(ctx, created.ID, "in_progress", Actor{Type: "system", AllowNoActiveFlow: true}); err != nil {
		t.Fatalf("TransitionStatus in_progress: %v", err)
	}

	blocked, err := svc.MarkBlocked(ctx, created.ID, "dependency missing", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}
	if blocked.WorkStatus != "blocked" {
		t.Fatalf("blocked work_status = %q, want %q", blocked.WorkStatus, "blocked")
	}

	tasks, err := taskRepo.ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject tasks: %v", err)
	}
	var resolution *repo.ProjectTask
	for i := range tasks {
		item := tasks[i]
		if strings.HasPrefix(item.Title, "Resolve blocker for task "+project.Slug+"-") {
			resolution = &item
			break
		}
	}
	if resolution == nil {
		t.Fatalf("resolution task not found in project tasks")
	}
	if resolution.AssignedAgentID == nil || *resolution.AssignedAgentID != pmAgent.ID {
		t.Fatalf("resolution assigned_agent_id = %v, want %s", resolution.AssignedAgentID, pmAgent.ID)
	}
	if resolution.WorkStatus != "queued" {
		t.Fatalf("resolution work_status = %q, want %q", resolution.WorkStatus, "queued")
	}
}

func TestTaskServiceIntegrationMergeQueueOrderingAndDequeue(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	_, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	svc := newTaskIntegrationService(t, pool)
	taskRepo := repo.NewProjectTaskRepo(pool)

	taskIDs := make([]uuid.UUID, 0, 3)
	for i := 0; i < 3; i++ {
		created, err := svc.CreateTask(ctx, CreateTaskRequest{
			ProjectID:     project.ID,
			Title:         "Merge task " + uuid.NewString()[:8],
			CreatedByType: "system",
		})
		if err != nil {
			t.Fatalf("CreateTask %d: %v", i, err)
		}

		branch := "feature/" + uuid.NewString()[:8]
		if _, err := taskRepo.SetBranch(ctx, created.ID, &branch); err != nil {
			t.Fatalf("SetBranch %d: %v", i, err)
		}
		taskIDs = append(taskIDs, created.ID)
	}

	first, err := svc.EnqueueForMerge(ctx, taskIDs[0])
	if err != nil {
		t.Fatalf("EnqueueForMerge first: %v", err)
	}
	second, err := svc.EnqueueForMerge(ctx, taskIDs[1])
	if err != nil {
		t.Fatalf("EnqueueForMerge second: %v", err)
	}
	third, err := svc.EnqueueForMerge(ctx, taskIDs[2])
	if err != nil {
		t.Fatalf("EnqueueForMerge third: %v", err)
	}
	if first.Position != 1 || second.Position != 2 || third.Position != 3 {
		t.Fatalf("positions = [%d,%d,%d], want [1,2,3]", first.Position, second.Position, third.Position)
	}

	if _, err := svc.DequeueFromMerge(ctx, second.ID, "skipped for test"); err != nil {
		t.Fatalf("DequeueFromMerge: %v", err)
	}

	active, err := svc.GetMergeQueueStatus(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetMergeQueueStatus: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("active queue length = %d, want 2", len(active))
	}
	if active[0].Position != 1 || active[1].Position != 3 {
		t.Fatalf("active positions = [%d,%d], want [1,3]", active[0].Position, active[1].Position)
	}
}

func TestTaskServiceIntegrationQueueRequiresPMWhenProjectConfigured(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{"requires_pm_assignment_before_queue":true}`))
	svc := newTaskIntegrationService(t, pool)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)

	created, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "PM-gated queue",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if _, err := svc.TransitionStatus(ctx, created.ID, "queued", Actor{Type: "system"}); !errors.Is(err, ErrPMNotAssigned) {
		t.Fatalf("TransitionStatus queued err = %v, want ErrPMNotAssigned", err)
	}

	pmUser := seedTaskServiceUser(t, ctx, pool, org.ID, "pm-gated-user", "admin")
	pmAgent := seedTaskServiceAgent(t, ctx, pool, org.ID, "PM Agent", "staff", "pm", "human_user", pmUser.ID)
	assignPMToProject(t, ctx, pool, pmAgent.ID, project.ID)

	queued, err := svc.TransitionStatus(ctx, created.ID, "queued", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("TransitionStatus queued with PM: %v", err)
	}
	if queued.WorkStatus != "queued" {
		t.Fatalf("queued work_status = %q, want queued", queued.WorkStatus)
	}
}

func TestTaskServiceIntegrationQueueDecomposesOversizedTaskIntoChildWorkUnits(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	svc := newTaskIntegrationService(t, pool)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)

	description := strings.Join([]string{
		"- Migrate all legacy markdown posts into the new CMS schema with canonical slug preservation and author mapping.",
		"- Rewrite and validate all media URLs while uploading assets into object storage with stable redirect coverage.",
		"- Rebuild taxonomy/tag mappings and verify inbound URL parity against production analytics snapshots.",
	}, "\n")

	created, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Blog migration epic",
		Description:    &description,
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		Metadata:       taskdecomp.ApplyQueueDecompositionMode(json.RawMessage(`{}`), taskdecomp.QueueDecompositionModeParallelChildren),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	queued, err := svc.TransitionStatus(ctx, created.ID, "queued", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("TransitionStatus queued: %v", err)
	}
	if queued.WorkStatus != "queued" {
		t.Fatalf("queued work_status = %q, want queued", queued.WorkStatus)
	}
	if queued.Description == nil || !strings.Contains(*queued.Description, "Migrate all legacy markdown posts") {
		t.Fatalf("queued description = %v, want focused primary deliverable", queued.Description)
	}

	var childCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task
		WHERE project_id = $1
		  AND metadata->>'decomposition_parent_task_id' = $2
	`, project.ID, created.ID.String()).Scan(&childCount); err != nil {
		t.Fatalf("count decomposed child tasks: %v", err)
	}
	if childCount < 1 {
		t.Fatalf("decomposed child task count = %d, want >= 1", childCount)
	}

	var primaryDeliverable string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(metadata #>> '{decomposition,primary_deliverable}', '')
		FROM project_task
		WHERE id = $1
	`, created.ID).Scan(&primaryDeliverable); err != nil {
		t.Fatalf("read decomposition metadata: %v", err)
	}
	if !strings.Contains(primaryDeliverable, "Migrate all legacy markdown posts") {
		t.Fatalf("primary_deliverable = %q, want focused deliverable", primaryDeliverable)
	}
}

func TestTaskServiceIntegrationQueueSkipsDecompositionWithoutExplicitMode(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	svc := newTaskIntegrationService(t, pool)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)

	description := strings.Join([]string{
		"- Migrate all legacy markdown posts into the new CMS schema with canonical slug preservation and author mapping.",
		"- Rewrite and validate all media URLs while uploading assets into object storage with stable redirect coverage.",
		"- Rebuild taxonomy/tag mappings and verify inbound URL parity against production analytics snapshots.",
	}, "\n")

	created, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Blog migration epic",
		Description:    &description,
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	queued, err := svc.TransitionStatus(ctx, created.ID, "queued", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("TransitionStatus queued: %v", err)
	}
	if queued.WorkStatus != "queued" {
		t.Fatalf("queued work_status = %q, want queued", queued.WorkStatus)
	}
	if queued.Description == nil || *queued.Description != description {
		t.Fatalf("queued description = %v, want original description preserved", queued.Description)
	}

	var childCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task
		WHERE project_id = $1
		  AND metadata->>'decomposition_parent_task_id' = $2
	`, project.ID, created.ID.String()).Scan(&childCount); err != nil {
		t.Fatalf("count decomposed child tasks: %v", err)
	}
	if childCount != 0 {
		t.Fatalf("decomposed child task count = %d, want 0", childCount)
	}
}

func newTaskIntegrationService(t *testing.T, pool *pgxpool.Pool) TaskService {
	t.Helper()
	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	svc, err := NewService(Options{
		Pool:     pool,
		EventBus: bus,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func seedTaskServiceOrgProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, settings json.RawMessage) (repo.Organization, repo.Project) {
	t.Helper()
	orgRepo := repo.NewOrgRepo(pool)
	projectRepo := repo.NewProjectRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "task-svc-org-" + uuid.NewString()[:8],
		DisplayName: "Task Service Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "task-svc-project-" + uuid.NewString()[:8],
		DisplayName:    "Task Service Project",
		DeliveryMode:   "gated",
		Settings:       settings,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return org, project
}

func seedTaskServiceFlowTemplate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID uuid.UUID) repo.FlowTemplate {
	t.Helper()

	templateRepo := repo.NewFlowTemplateRepo(pool)
	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "task-svc-flow-" + uuid.NewString()[:8],
		DisplayName:    "Task Service Flow",
		Description:    "integration test flow",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	workNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	reviewNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       2,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create review flow node: %v", err)
	}
	mergeNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Complete",
		NodeType:       "merge",
		Position:       3,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create merge flow node: %v", err)
	}
	workNode.NextNodeID = &reviewNode.ID
	if _, err := repo.NewFlowNodeRepo(pool).Update(ctx, workNode); err != nil {
		t.Fatalf("link flow nodes: %v", err)
	}
	reviewNode.NextNodeID = &mergeNode.ID
	if _, err := repo.NewFlowNodeRepo(pool).Update(ctx, reviewNode); err != nil {
		t.Fatalf("link review to merge node: %v", err)
	}
	template.StartNodeID = &workNode.ID
	updated, err := templateRepo.Update(ctx, template)
	if err != nil {
		t.Fatalf("update flow template start node: %v", err)
	}
	return updated
}

func seedTaskServiceUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, prefix, role string) repo.HumanUser {
	t.Helper()
	userRepo := repo.NewHumanUserRepo(pool)
	user, err := userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: orgID,
		Email:          prefix + "+" + uuid.NewString()[:8] + "@example.com",
		DisplayName:    prefix,
		Role:           role,
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func seedTaskServiceAgent(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	orgID uuid.UUID,
	displayName string,
	agentClass string,
	agentType string,
	createdByType string,
	createdByID uuid.UUID,
) repo.Agent {
	t.Helper()
	agentRepo := repo.NewAgentRepo(pool)
	agentRecord, err := agentRepo.Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          displayName,
		AgentClass:           agentClass,
		LifecycleStatus:      "active",
		AgentType:            agentType,
		CreatedByType:        createdByType,
		CreatedByID:          createdByID,
		MemoryReadScopes:     []string{},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		OperatorInstructions: "",
		SystemPrompt:         "",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agentRecord
}

func assignPMToProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, agentID, projectID uuid.UUID) {
	t.Helper()
	assignmentRepo := repo.NewAgentProjectAssignmentRepo(pool)
	if _, err := assignmentRepo.Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        agentID,
		ProjectID:      projectID,
		Role:           "pm",
		AssignedByType: "system",
	}); err != nil {
		t.Fatalf("assign PM: %v", err)
	}
}
