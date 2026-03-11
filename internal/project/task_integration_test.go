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
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestTask_StateMachine_FullPath(t *testing.T) {
	ctx := context.Background()
	fx := newTaskFixture073(t, false)

	taskRecord, err := fx.taskService.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:      fx.project.ID,
		Title:          "state-machine-full-path",
		FlowTemplateID: &fx.flowTemplate.ID,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	current := taskRecord
	for _, nextStatus := range []string{"queued", "in_progress"} {
		actor := tasksvc.Actor{Type: "system"}
		if nextStatus == "in_progress" {
			actor.AllowNoActiveFlow = true
		}
		current, err = fx.taskService.TransitionStatus(ctx, current.ID, nextStatus, actor)
		if err != nil {
			t.Fatalf("TransitionStatus %s: %v", nextStatus, err)
		}
	}
	current, err = moveTaskToReview073(ctx, fx, current.ID)
	if err != nil {
		t.Fatalf("TransitionStatus review: %v", err)
	}

	nodes, err := repo.NewFlowNodeRepo(fx.pool).GetByTemplateOrdered(ctx, fx.flowTemplate.ID)
	if err != nil {
		t.Fatalf("GetByTemplateOrdered: %v", err)
	}
	if len(nodes) < 3 {
		t.Fatalf("flow node count = %d, want at least 3", len(nodes))
	}
	current.CurrentFlowNodeID = &nodes[2].ID
	updatedTask, err := fx.taskRepo.Update(ctx, *current)
	if err != nil {
		t.Fatalf("Update current flow node: %v", err)
	}
	current = &updatedTask

	executionRepo := repo.NewFlowNodeExecutionRepo(fx.pool)
	workExecution, err := executionRepo.Create(ctx, repo.FlowNodeExecution{
		TaskID:      current.ID,
		FlowNodeID:  nodes[0].ID,
		VisitNumber: 1,
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("Create work execution: %v", err)
	}
	if _, err := executionRepo.Complete(ctx, workExecution.ID); err != nil {
		t.Fatalf("Complete work execution: %v", err)
	}
	reviewExecution, err := executionRepo.Create(ctx, repo.FlowNodeExecution{
		TaskID:      current.ID,
		FlowNodeID:  nodes[1].ID,
		VisitNumber: 1,
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("Create review execution: %v", err)
	}
	if _, err := executionRepo.Complete(ctx, reviewExecution.ID); err != nil {
		t.Fatalf("Complete review execution: %v", err)
	}
	mergeExecution, err := executionRepo.Create(ctx, repo.FlowNodeExecution{
		TaskID:      current.ID,
		FlowNodeID:  nodes[2].ID,
		VisitNumber: 1,
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("Create merge execution: %v", err)
	}
	if _, err := executionRepo.Complete(ctx, mergeExecution.ID); err != nil {
		t.Fatalf("Complete merge execution: %v", err)
	}

	current, err = fx.taskService.TransitionStatus(ctx, current.ID, "done", tasksvc.Actor{Type: "system"})
	if err != nil {
		t.Fatalf("TransitionStatus done: %v", err)
	}
	if current.WorkStatus != "done" {
		t.Fatalf("final work_status = %q, want %q", current.WorkStatus, "done")
	}

	rows, err := fx.pool.Query(ctx, `
		SELECT
			event_type,
			actor_type,
			COALESCE(payload->>'from_status', ''),
			COALESCE(payload->>'to_status', '')
		FROM project_task_event
		WHERE task_id = $1
		ORDER BY created_at ASC, id ASC
	`, taskRecord.ID)
	if err != nil {
		t.Fatalf("query task events: %v", err)
	}
	defer rows.Close()

	type transition struct {
		from string
		to   string
	}
	var transitions []transition
	for rows.Next() {
		var eventType string
		var actorType string
		var fromStatus string
		var toStatus string
		if err := rows.Scan(&eventType, &actorType, &fromStatus, &toStatus); err != nil {
			t.Fatalf("scan task event: %v", err)
		}
		if eventType != "status.changed" {
			continue
		}
		if actorType != "system" {
			t.Fatalf("actor_type = %q, want %q", actorType, "system")
		}
		transitions = append(transitions, transition{from: fromStatus, to: toStatus})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate task events: %v", err)
	}

	expected := []transition{
		{from: "draft", to: "queued"},
		{from: "queued", to: "in_progress"},
		{from: "in_progress", to: "review"},
		{from: "review", to: "done"},
	}
	if len(transitions) != len(expected) {
		t.Fatalf("status transition count = %d, want %d", len(transitions), len(expected))
	}
	for i := range expected {
		if transitions[i] != expected[i] {
			t.Fatalf("transition[%d] = %+v, want %+v", i, transitions[i], expected[i])
		}
	}

	if _, err := fx.queueRepo.GetByTask(ctx, taskRecord.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("merge queue entry should not exist before enqueue, err=%v", err)
	}
}

func TestTask_StateMachine_Blocked(t *testing.T) {
	ctx := context.Background()
	fx := newTaskFixture073(t, false)

	taskRecord := createTask073(t, ctx, fx, "blocked-state-machine")
	if _, err := fx.taskService.TransitionStatus(ctx, taskRecord.ID, "queued", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus queued: %v", err)
	}
	if _, err := fx.taskService.TransitionStatus(ctx, taskRecord.ID, "in_progress", tasksvc.Actor{Type: "system", AllowNoActiveFlow: true}); err != nil {
		t.Fatalf("TransitionStatus in_progress: %v", err)
	}

	blocked, err := fx.taskService.MarkBlocked(ctx, taskRecord.ID, "dependency unavailable", tasksvc.Actor{Type: "system"})
	if err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}
	if blocked.WorkStatus != "blocked" {
		t.Fatalf("blocked status = %q, want %q", blocked.WorkStatus, "blocked")
	}

	var blockedEventCount int
	if err := fx.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task_event
		WHERE task_id = $1
		  AND event_type = 'status.changed'
		  AND payload->>'to_status' = 'blocked'
	`, taskRecord.ID).Scan(&blockedEventCount); err != nil {
		t.Fatalf("count blocked events: %v", err)
	}
	if blockedEventCount == 0 {
		t.Fatal("expected status.changed event with to_status=blocked")
	}

	items, err := fx.inboxRepo.ListForUser(ctx, fx.org.ID, fx.pmUser.ID, repo.InboxListOptions{
		ItemType:     "blocker_filed",
		IncludeActed: true,
		Limit:        20,
	})
	if err != nil {
		t.Fatalf("ListForUser blocker_filed: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected blocker_filed inbox item for PM")
	}
}

func TestTask_StateMachine_OnHold(t *testing.T) {
	ctx := context.Background()
	fx := newTaskFixture073(t, false)

	taskRecord := createTask073(t, ctx, fx, "on-hold-state-machine")
	if _, err := fx.taskService.TransitionStatus(ctx, taskRecord.ID, "queued", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus queued: %v", err)
	}
	if _, err := fx.taskService.TransitionStatus(ctx, taskRecord.ID, "in_progress", tasksvc.Actor{Type: "system", AllowNoActiveFlow: true}); err != nil {
		t.Fatalf("TransitionStatus in_progress: %v", err)
	}
	if _, err := fx.taskService.TransitionStatus(ctx, taskRecord.ID, "on_hold", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus on_hold: %v", err)
	}
	if _, err := fx.taskService.TransitionStatus(ctx, taskRecord.ID, "queued", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus on_hold->queued: %v", err)
	}

	rows, err := fx.pool.Query(ctx, `
		SELECT payload->>'from_status', payload->>'to_status'
		FROM project_task_event
		WHERE task_id = $1
		  AND event_type = 'status.changed'
		ORDER BY created_at ASC, id ASC
	`, taskRecord.ID)
	if err != nil {
		t.Fatalf("query status events: %v", err)
	}
	defer rows.Close()

	var foundOnHold bool
	var foundResume bool
	for rows.Next() {
		var fromStatus string
		var toStatus string
		if err := rows.Scan(&fromStatus, &toStatus); err != nil {
			t.Fatalf("scan status event: %v", err)
		}
		if fromStatus == "in_progress" && toStatus == "on_hold" {
			foundOnHold = true
		}
		if fromStatus == "on_hold" && toStatus == "queued" {
			foundResume = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate status events: %v", err)
	}
	if !foundOnHold || !foundResume {
		t.Fatalf("expected on_hold and resume transitions, got on_hold=%v resume=%v", foundOnHold, foundResume)
	}
}

func TestTask_RequiresHumanReview_Gate(t *testing.T) {
	ctx := context.Background()
	fx := newTaskFixture073(t, true)

	taskRecord := createTask073(t, ctx, fx, "requires-human-review")
	if !taskRecord.RequiresHumanReview {
		t.Fatal("requires_human_review = false, want true")
	}

	if _, err := fx.taskService.TransitionStatus(ctx, taskRecord.ID, "queued", tasksvc.Actor{Type: "system"}); !errors.Is(err, tasksvc.ErrRequiresHumanApproval) {
		t.Fatalf("TransitionStatus queued err = %v, want ErrRequiresHumanApproval", err)
	}

	if _, err := fx.taskService.ApproveTask(ctx, taskRecord.ID, fx.pmUser.ID); !errors.Is(err, tasksvc.ErrInboxItemNotFound) {
		t.Fatalf("ApproveTask before review request err = %v, want ErrInboxItemNotFound", err)
	}

	inboxItem, err := fx.taskService.RequestHumanApproval(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("RequestHumanApproval: %v", err)
	}
	if inboxItem.ItemType != "human_approval_required" {
		t.Fatalf("inbox item_type = %q, want %q", inboxItem.ItemType, "human_approval_required")
	}

	approved, err := fx.taskService.ApproveTask(ctx, taskRecord.ID, fx.pmUser.ID)
	if err != nil {
		t.Fatalf("ApproveTask: %v", err)
	}
	if approved.WorkStatus != "queued" {
		t.Fatalf("approved work_status = %q, want %q", approved.WorkStatus, "queued")
	}
}

func TestTask_StateMachine_Cancelled(t *testing.T) {
	ctx := context.Background()
	fx := newTaskFixture073(t, false)

	taskRecord := createTask073(t, ctx, fx, "cancelled-state-machine")
	if _, err := fx.taskService.TransitionStatus(ctx, taskRecord.ID, "queued", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus queued: %v", err)
	}
	if _, err := fx.taskService.TransitionStatus(ctx, taskRecord.ID, "in_progress", tasksvc.Actor{Type: "system", AllowNoActiveFlow: true}); err != nil {
		t.Fatalf("TransitionStatus in_progress: %v", err)
	}
	if _, err := moveTaskToReview073(ctx, fx, taskRecord.ID); err != nil {
		t.Fatalf("TransitionStatus review: %v", err)
	}

	branch := "feature/" + uuid.NewString()[:8]
	if _, err := fx.taskRepo.SetBranch(ctx, taskRecord.ID, &branch); err != nil {
		t.Fatalf("SetBranch: %v", err)
	}
	enqueued, err := fx.taskService.EnqueueForMerge(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("EnqueueForMerge: %v", err)
	}
	if enqueued.ArchivedAt != nil {
		t.Fatalf("archived_at before cancel = %v, want nil", enqueued.ArchivedAt)
	}

	cancelled, err := fx.taskService.TransitionStatus(ctx, taskRecord.ID, "cancelled", tasksvc.Actor{Type: "system"})
	if err != nil {
		t.Fatalf("TransitionStatus cancelled: %v", err)
	}
	if cancelled.WorkStatus != "cancelled" {
		t.Fatalf("cancelled work_status = %q, want %q", cancelled.WorkStatus, "cancelled")
	}
	if cancelled.CompletedAt == nil {
		t.Fatal("cancelled completed_at = nil, want non-nil")
	}

	stored, err := fx.queueRepo.GetByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByTask: %v", err)
	}
	if stored.ArchivedAt == nil {
		t.Fatal("merge queue archived_at = nil after cancellation, want non-nil")
	}
}

func TestMergeQueue_Lifecycle(t *testing.T) {
	ctx := context.Background()
	fx := newTaskFixture073(t, false)

	taskRecord := createTask073(t, ctx, fx, "merge-queue-lifecycle")
	if _, err := fx.taskService.TransitionStatus(ctx, taskRecord.ID, "queued", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus queued: %v", err)
	}
	if _, err := fx.taskService.TransitionStatus(ctx, taskRecord.ID, "in_progress", tasksvc.Actor{Type: "system", AllowNoActiveFlow: true}); err != nil {
		t.Fatalf("TransitionStatus in_progress: %v", err)
	}
	if _, err := moveTaskToReview073(ctx, fx, taskRecord.ID); err != nil {
		t.Fatalf("TransitionStatus review: %v", err)
	}

	branch := "feature/" + uuid.NewString()[:8]
	if _, err := fx.taskRepo.SetBranch(ctx, taskRecord.ID, &branch); err != nil {
		t.Fatalf("SetBranch: %v", err)
	}

	enqueued, err := fx.taskService.EnqueueForMerge(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("EnqueueForMerge: %v", err)
	}
	if enqueued.ArchivedAt != nil {
		t.Fatalf("archived_at = %v, want nil for active queue entry", enqueued.ArchivedAt)
	}

	mergedAt := time.Now().UTC()
	if _, err := fx.queueRepo.UpdateStatus(ctx, enqueued.ID, "merged", nil, &mergedAt); err != nil {
		t.Fatalf("UpdateStatus merged: %v", err)
	}
	archived, err := fx.queueRepo.Archive(ctx, enqueued.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if archived.ArchivedAt == nil {
		t.Fatal("archived_at = nil, want non-nil")
	}

	stored, err := fx.queueRepo.GetByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByTask: %v", err)
	}
	if stored.ID != enqueued.ID {
		t.Fatalf("stored id = %s, want %s", stored.ID, enqueued.ID)
	}
	if stored.ArchivedAt == nil {
		t.Fatal("stored archived_at = nil, want non-nil")
	}
}

func TestInboxItem_Creation(t *testing.T) {
	ctx := context.Background()
	fx := newTaskFixture073(t, true)

	taskRecord := createTask073(t, ctx, fx, "inbox-item-lifecycle")
	inboxItem, err := fx.taskService.RequestHumanApproval(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("RequestHumanApproval: %v", err)
	}
	if inboxItem.ItemType != "human_approval_required" {
		t.Fatalf("item_type = %q, want %q", inboxItem.ItemType, "human_approval_required")
	}

	if err := fx.taskService.ActOnInboxItem(ctx, inboxItem.ID, fx.pmUser.ID, "approve", nil); err != nil {
		t.Fatalf("ActOnInboxItem approve: %v", err)
	}

	stored, err := fx.inboxRepo.GetByID(ctx, inboxItem.ID)
	if err != nil {
		t.Fatalf("GetByID inbox: %v", err)
	}
	if !stored.IsActed || stored.ActedAt == nil {
		t.Fatalf("acted flags after approve = is_acted:%v acted_at:%v, want true/non-nil", stored.IsActed, stored.ActedAt)
	}
}

type taskFixture073 struct {
	pool         *pgxpool.Pool
	taskService  tasksvc.TaskService
	org          repo.Organization
	project      repo.Project
	flowTemplate repo.FlowTemplate
	pmUser       repo.HumanUser
	pmAgent      repo.Agent
	taskRepo     *repo.ProjectTaskRepo
	queueRepo    *repo.MergeQueueEntryRepo
	inboxRepo    *repo.InboxItemRepo
}

func newTaskFixture073(t *testing.T, requiresHumanReview bool) taskFixture073 {
	t.Helper()

	ctx := context.Background()
	pool := testdb.New(t)
	org, projectRecord, flowTemplate, pmUser, pmAgent := seedTaskFixtureData073(t, ctx, pool, requiresHumanReview)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := eventbus.New(pool, logger, eventbus.Config{})
	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:     pool,
		EventBus: bus,
	})
	if err != nil {
		t.Fatalf("new task service: %v", err)
	}

	return taskFixture073{
		pool:         pool,
		taskService:  taskService,
		org:          org,
		project:      projectRecord,
		flowTemplate: flowTemplate,
		pmUser:       pmUser,
		pmAgent:      pmAgent,
		taskRepo:     repo.NewProjectTaskRepo(pool),
		queueRepo:    repo.NewMergeQueueEntryRepo(pool),
		inboxRepo:    repo.NewInboxItemRepo(pool),
	}
}

func seedTaskFixtureData073(t *testing.T, ctx context.Context, pool *pgxpool.Pool, requiresHumanReview bool) (repo.Organization, repo.Project, repo.FlowTemplate, repo.HumanUser, repo.Agent) {
	t.Helper()

	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "task-073-org-" + uuid.NewString()[:8],
		DisplayName: "Task 073 Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	settings := json.RawMessage(`{}`)
	if requiresHumanReview {
		settings = json.RawMessage(`{"requires_human_review":true}`)
	}
	projectRecord, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "task-073-project-" + uuid.NewString()[:8],
		DisplayName:    "Task 073 Project",
		DeliveryMode:   "gated",
		Settings:       settings,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	flowTemplate, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &projectRecord.ID,
		Slug:           "task-073-flow-" + uuid.NewString()[:8],
		DisplayName:    "Task 073 Flow",
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
		FlowTemplateID: flowTemplate.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create work node: %v", err)
	}
	reviewNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: flowTemplate.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       2,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create review node: %v", err)
	}
	mergeNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: flowTemplate.ID,
		DisplayName:    "Merge",
		NodeType:       "merge",
		Position:       3,
		MaxVisits:      1,
	})
	if err != nil {
		t.Fatalf("create merge node: %v", err)
	}
	workNode.NextNodeID = &reviewNode.ID
	if _, err := repo.NewFlowNodeRepo(pool).Update(ctx, workNode); err != nil {
		t.Fatalf("link work node: %v", err)
	}
	reviewNode.NextNodeID = &mergeNode.ID
	if _, err := repo.NewFlowNodeRepo(pool).Update(ctx, reviewNode); err != nil {
		t.Fatalf("link review node: %v", err)
	}
	flowTemplate.StartNodeID = &workNode.ID
	flowTemplate, err = repo.NewFlowTemplateRepo(pool).Update(ctx, flowTemplate)
	if err != nil {
		t.Fatalf("update flow template start node: %v", err)
	}

	pmUser, err := repo.NewHumanUserRepo(pool).Create(ctx, repo.HumanUser{
		OrganizationID: org.ID,
		Email:          "pm+" + uuid.NewString()[:8] + "@example.com",
		DisplayName:    "PM User",
		Role:           "admin",
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create pm user: %v", err)
	}

	pmAgent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       org.ID,
		DisplayName:          "PM Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		AgentType:            "pm",
		CreatedByType:        "human_user",
		CreatedByID:          pmUser.ID,
		MemoryReadScopes:     []string{},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		SystemPrompt:         "",
		OperatorInstructions: "",
	})
	if err != nil {
		t.Fatalf("create pm agent: %v", err)
	}

	if _, err := repo.NewAgentProjectAssignmentRepo(pool).Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        pmAgent.ID,
		ProjectID:      projectRecord.ID,
		Role:           "pm",
		AssignedByType: "system",
		AssignedByID:   nil,
		IsActive:       true,
	}); err != nil {
		t.Fatalf("assign pm agent: %v", err)
	}

	return org, projectRecord, flowTemplate, pmUser, pmAgent
}

func createTask073(t *testing.T, ctx context.Context, fx taskFixture073, title string) *tasksvc.ProjectTask {
	t.Helper()

	created, err := fx.taskService.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:      fx.project.ID,
		Title:          title,
		FlowTemplateID: &fx.flowTemplate.ID,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("CreateTask %q: %v", title, err)
	}
	return created
}

func moveTaskToReview073(ctx context.Context, fx taskFixture073, taskID uuid.UUID) (*tasksvc.ProjectTask, error) {
	nodes, err := repo.NewFlowNodeRepo(fx.pool).GetByTemplateOrdered(ctx, fx.flowTemplate.ID)
	if err != nil {
		return nil, err
	}
	if len(nodes) < 2 {
		return nil, errors.New("review node missing from test flow template")
	}
	taskRecord, err := fx.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	taskRecord.CurrentFlowNodeID = &nodes[1].ID
	updated, err := fx.taskRepo.Update(ctx, taskRecord)
	if err != nil {
		return nil, err
	}
	return fx.taskService.TransitionStatus(ctx, updated.ID, "review", tasksvc.Actor{Type: "system"})
}
