//go:build integration

package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/taskorchestration"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/workspace"
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
		if step == "review" {
			currentFlowNodeID := template.StartNodeID
			if currentFlowNodeID == nil {
				t.Fatal("flow template start_node_id is nil")
			}
			workNode, err := repo.NewFlowNodeRepo(pool).GetByID(ctx, *currentFlowNodeID)
			if err != nil {
				t.Fatalf("GetByID work flow node: %v", err)
			}
			if workNode.NextNodeID == nil {
				t.Fatal("work flow node next_node_id is nil")
			}
			taskRepo := repo.NewProjectTaskRepo(pool)
			taskRecord, err := taskRepo.GetByID(ctx, current.ID)
			if err != nil {
				t.Fatalf("GetByID task before review: %v", err)
			}
			taskRecord.CurrentFlowNodeID = workNode.NextNodeID
			if _, err := taskRepo.Update(ctx, taskRecord); err != nil {
				t.Fatalf("Update task current_flow_node_id for review: %v", err)
			}
		}
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

func TestTaskServiceIntegrationAllowsReviewToBlockedWithFlowContext(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	pmUser := seedTaskServiceUser(t, ctx, pool, org.ID, "review-blocked-pm", "admin")
	pmAgent := seedTaskServiceAgent(t, ctx, pool, org.ID, "Review Blocked PM", "staff", "pm", "human_user", pmUser.ID)
	assignPMToProject(t, ctx, pool, pmAgent.ID, project.ID)
	svc := newTaskIntegrationService(t, pool)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)

	created, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Review blocker task",
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

	workNode, err := repo.NewFlowNodeRepo(pool).GetByID(ctx, *template.StartNodeID)
	if err != nil {
		t.Fatalf("GetByID work node: %v", err)
	}
	if workNode.NextNodeID == nil {
		t.Fatal("work node next_node_id is nil")
	}
	taskRepo := repo.NewProjectTaskRepo(pool)
	taskRecord, err := taskRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID task before review: %v", err)
	}
	taskRecord.CurrentFlowNodeID = workNode.NextNodeID
	if _, err := taskRepo.Update(ctx, taskRecord); err != nil {
		t.Fatalf("Update task current_flow_node_id for review: %v", err)
	}
	if _, err := svc.TransitionStatus(ctx, created.ID, "review", Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus review: %v", err)
	}

	blocked, err := svc.MarkBlocked(ctx, created.ID, "external dependency discovered during review", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("MarkBlocked review->blocked: %v", err)
	}
	if blocked.WorkStatus != "blocked" {
		t.Fatalf("blocked work_status = %q, want blocked", blocked.WorkStatus)
	}
	if blocked.CurrentFlowNodeID == nil || *blocked.CurrentFlowNodeID != *workNode.NextNodeID {
		t.Fatalf("current_flow_node_id = %v, want review node %s", blocked.CurrentFlowNodeID, *workNode.NextNodeID)
	}

	var blockedEvents int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'task.status_changed'
		  AND payload->>'task_id' = $2
		  AND payload->>'from_status' = 'review'
		  AND payload->>'to_status' = 'blocked'
	`, org.ID, created.ID.String()).Scan(&blockedEvents); err != nil {
		t.Fatalf("count review->blocked domain events: %v", err)
	}
	if blockedEvents != 1 {
		t.Fatalf("review->blocked domain events = %d, want 1", blockedEvents)
	}
}

func TestTaskServiceIntegrationFlowBackedOnHoldResumesThroughQueued(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	svc := newTaskIntegrationService(t, pool)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)

	created, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Flow-backed hold task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.TransitionStatus(ctx, created.ID, "queued", Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus queued: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(pool)
	taskRecord, err := taskRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	taskRecord.CurrentFlowNodeID = template.StartNodeID
	if _, err := taskRepo.Update(ctx, taskRecord); err != nil {
		t.Fatalf("Update task current_flow_node_id: %v", err)
	}

	execRepo := repo.NewFlowNodeExecutionRepo(pool)
	activeExecution, err := execRepo.Create(ctx, repo.FlowNodeExecution{
		TaskID:      created.ID,
		FlowNodeID:  *template.StartNodeID,
		VisitNumber: 1,
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("Create active flow execution: %v", err)
	}

	inProgress, err := svc.TransitionStatus(ctx, created.ID, "in_progress", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("TransitionStatus in_progress: %v", err)
	}
	if inProgress.CurrentFlowNodeID == nil || *inProgress.CurrentFlowNodeID != *template.StartNodeID {
		t.Fatalf("current_flow_node_id in_progress = %v, want %s", inProgress.CurrentFlowNodeID, *template.StartNodeID)
	}

	onHold, err := svc.TransitionStatus(ctx, created.ID, "on_hold", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("TransitionStatus on_hold: %v", err)
	}
	if onHold.WorkStatus != "on_hold" {
		t.Fatalf("on_hold work_status = %q, want on_hold", onHold.WorkStatus)
	}
	if onHold.CurrentFlowNodeID == nil || *onHold.CurrentFlowNodeID != *template.StartNodeID {
		t.Fatalf("current_flow_node_id on_hold = %v, want %s", onHold.CurrentFlowNodeID, *template.StartNodeID)
	}

	resumed, err := svc.TransitionStatus(ctx, created.ID, "queued", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("TransitionStatus queued from on_hold: %v", err)
	}
	if resumed.WorkStatus != "queued" {
		t.Fatalf("resumed work_status = %q, want queued", resumed.WorkStatus)
	}
	if resumed.CurrentFlowNodeID == nil || *resumed.CurrentFlowNodeID != *template.StartNodeID {
		t.Fatalf("current_flow_node_id resumed = %v, want %s", resumed.CurrentFlowNodeID, *template.StartNodeID)
	}

	refreshedExecution, err := execRepo.GetByID(ctx, activeExecution.ID)
	if err != nil {
		t.Fatalf("GetByID active execution: %v", err)
	}
	if refreshedExecution.Status != "active" {
		t.Fatalf("flow execution status after on_hold->queued = %q, want active", refreshedExecution.Status)
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

	regularTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Regular queued task",
		WorkStatus:     "draft",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("create regular task: %v", err)
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

func TestTaskServiceIntegrationRejectsCreateWhenOutstandingProjectGateExists(t *testing.T) {
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

	_, err = svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Regular workstream task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
	})
	if !errors.Is(err, ErrProjectGateBlockingCreate) {
		t.Fatalf("CreateTask err = %v, want ErrProjectGateBlockingCreate", err)
	}
	if !strings.Contains(err.Error(), gateTask.Title) {
		t.Fatalf("error = %q, want gate title context", err.Error())
	}

	allTasks, err := taskRepo.ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(allTasks) != 1 {
		t.Fatalf("task count after blocked create = %d, want 1 gate task only", len(allTasks))
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

func TestTaskServiceIntegrationCreateTaskAllowsExplicitHumanReviewOverride(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{"requires_human_review":false}`))
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)

	svc := newTaskIntegrationService(t, pool)
	override := true
	taskRecord, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:           project.ID,
		Title:               "Explicit review override",
		FlowTemplateID:      &template.ID,
		CreatedByType:       "system",
		RequiresHumanReview: &override,
		Priority:            4,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if !taskRecord.RequiresHumanReview {
		t.Fatal("requires_human_review = false, want true")
	}
	if taskRecord.Priority != 4 {
		t.Fatalf("priority = %d, want 4", taskRecord.Priority)
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

	if _, err := svc.TransitionStatus(ctx, blocked.ID, "in_progress", Actor{Type: "human_user", ID: pmUser.ID}); !errors.Is(err, ErrActiveFlowRequired) {
		t.Fatalf("TransitionStatus blocked->in_progress err = %v, want ErrActiveFlowRequired", err)
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

func TestTaskServiceIntegrationResumeValidationBlockedTaskClearsGuardAndQueuesRecovery(t *testing.T) {
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
		Title:          "Validation blocked task",
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

	taskRecord, err := taskRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	guardedMetadata, err := MergeValidationGuardMetadata(taskRecord.Metadata, ValidationGuardState{
		InitialMessageID:   uuid.NewString(),
		Fingerprint:        "cli.execute:command_required",
		AttemptFingerprint: "cli.execute:command_required:attempt",
		ToolName:           "cli.execute",
		FailureClass:       "tool_validation",
		FailureCode:        "command_required",
		FailureReason:      "command is required",
		Count:              3,
		BlockThreshold:     3,
		Blocked:            true,
	})
	if err != nil {
		t.Fatalf("MergeValidationGuardMetadata: %v", err)
	}
	taskRecord.Metadata = guardedMetadata
	if _, err := taskRepo.Update(ctx, taskRecord); err != nil {
		t.Fatalf("Update guarded task: %v", err)
	}
	if _, err := svc.MarkBlocked(ctx, created.ID, "deterministic tool validation loop blocked after 3 identical failures: cli.execute (command is required)", Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}

	resumed, err := svc.ResumeValidationBlockedTask(ctx, created.ID, Actor{Type: "human_user", ID: pmUser.ID})
	if err != nil {
		t.Fatalf("ResumeValidationBlockedTask: %v", err)
	}
	if resumed.WorkStatus != "queued" {
		t.Fatalf("resumed work_status = %q, want queued", resumed.WorkStatus)
	}
	if _, ok := ParseValidationGuard(resumed.Metadata); ok {
		t.Fatalf("expected validation guard to be cleared, metadata=%s", string(resumed.Metadata))
	}

	var (
		eventType string
		payload   []byte
	)
	if err := pool.QueryRow(ctx, `
		SELECT event_type, payload
		FROM project_task_event
		WHERE task_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, created.ID).Scan(&eventType, &payload); err != nil {
		t.Fatalf("load latest task event: %v", err)
	}
	if eventType != "status.changed" {
		t.Fatalf("latest event_type = %q, want status.changed", eventType)
	}
	var eventPayload map[string]any
	if err := json.Unmarshal(payload, &eventPayload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", eventPayload["to_status"])); got != "queued" {
		t.Fatalf("to_status = %q, want queued", got)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", eventPayload["recovery_action"])); got != "resume_validation_blocked_task" {
		t.Fatalf("recovery_action = %q, want resume_validation_blocked_task", got)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", eventPayload["validation_failure_code"])); got != "command_required" {
		t.Fatalf("validation_failure_code = %q, want command_required", got)
	}
}

func TestTaskServiceIntegrationResumeDurableRecoveryCheckpointQueuesRecoveryEX324(t *testing.T) {
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
		Title:          "Recovery checkpoint blocked task",
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

	taskRecord, err := taskRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	targetPath := "docs/content-strategy.md"
	artifactPath := ".ottercamp/recovery/docs/content-strategy.md"
	failureReason := "assistant draft for docs/content-strategy.md described tool-recovery troubleshooting instead of the file body"
	checkpointMetadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(taskRecord.Metadata, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:    targetPath,
		ArtifactPath:  artifactPath,
		FailureReason: failureReason,
		HaltTurnID:    uuid.NewString(),
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	taskRecord.Metadata = checkpointMetadata
	if _, err := taskRepo.Update(ctx, taskRecord); err != nil {
		t.Fatalf("Update checkpointed task: %v", err)
	}
	blockerReason := "recovery halted after assistant draft for docs/content-strategy.md described tool-recovery troubleshooting instead of the file body; resume from .ottercamp/recovery/docs/content-strategy.md and re-queue only after concrete content exists"
	if _, err := svc.MarkBlocked(ctx, created.ID, blockerReason, Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}

	resumed, err := svc.ResumeValidationBlockedTask(ctx, created.ID, Actor{Type: "human_user", ID: pmUser.ID})
	if err != nil {
		t.Fatalf("ResumeValidationBlockedTask: %v", err)
	}
	if resumed.WorkStatus != "queued" {
		t.Fatalf("resumed work_status = %q, want queued", resumed.WorkStatus)
	}
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(resumed.Metadata)
	if !ok {
		t.Fatalf("expected durable recovery checkpoint to remain after resume, metadata=%s", string(resumed.Metadata))
	}
	if checkpoint.TargetPath != targetPath {
		t.Fatalf("checkpoint target_path = %q, want %q", checkpoint.TargetPath, targetPath)
	}
	if checkpoint.ArtifactPath != artifactPath {
		t.Fatalf("checkpoint artifact_path = %q, want %q", checkpoint.ArtifactPath, artifactPath)
	}

	var (
		eventType string
		payload   []byte
	)
	if err := pool.QueryRow(ctx, `
		SELECT event_type, payload
		FROM project_task_event
		WHERE task_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, created.ID).Scan(&eventType, &payload); err != nil {
		t.Fatalf("load latest task event: %v", err)
	}
	if eventType != "status.changed" {
		t.Fatalf("latest event_type = %q, want status.changed", eventType)
	}
	var eventPayload map[string]any
	if err := json.Unmarshal(payload, &eventPayload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", eventPayload["recovery_action"])); got != RecoveryActionResumeBlockedTask {
		t.Fatalf("recovery_action = %q, want %q", got, RecoveryActionResumeBlockedTask)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", eventPayload["recovery_blocker_class"])); got != RecoveryBlockerClassDurableRecoveryCheckpoint {
		t.Fatalf("recovery_blocker_class = %q, want %q", got, RecoveryBlockerClassDurableRecoveryCheckpoint)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", eventPayload["recovery_checkpoint_target_path"])); got != targetPath {
		t.Fatalf("recovery_checkpoint_target_path = %q, want %q", got, targetPath)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", eventPayload["recovery_checkpoint_artifact_path"])); got != artifactPath {
		t.Fatalf("recovery_checkpoint_artifact_path = %q, want %q", got, artifactPath)
	}
}

func TestTaskServiceIntegrationResumeValidationBlockedReviewTaskRestoresReviewStatus(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	pmUser := seedTaskServiceUser(t, ctx, pool, org.ID, "pm-user", "admin")
	pmAgent := seedTaskServiceAgent(t, ctx, pool, org.ID, "PM Agent", "staff", "pm", "human_user", pmUser.ID)
	assignPMToProject(t, ctx, pool, pmAgent.ID, project.ID)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)

	svc := newTaskIntegrationService(t, pool)
	taskRepo := repo.NewProjectTaskRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)

	created, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Validation blocked review task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	nodes, err := nodeRepo.GetByTemplateOrdered(ctx, template.ID)
	if err != nil {
		t.Fatalf("GetByTemplateOrdered: %v", err)
	}
	if len(nodes) < 2 {
		t.Fatalf("nodes len = %d, want >= 2", len(nodes))
	}
	reviewNode := nodes[1]

	taskRecord, err := taskRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	taskRecord.CurrentFlowNodeID = &reviewNode.ID
	taskRecord.WorkStatus = "review"
	guardedMetadata, err := MergeValidationGuardMetadata(taskRecord.Metadata, ValidationGuardState{
		InitialMessageID:   uuid.NewString(),
		Fingerprint:        "file.write:content_required",
		AttemptFingerprint: "file.write:content_required:attempt",
		ToolName:           "file.write",
		FailureClass:       "tool_validation",
		FailureCode:        "content_required",
		FailureReason:      "content_required",
		Count:              3,
		BlockThreshold:     3,
		Blocked:            true,
	})
	if err != nil {
		t.Fatalf("MergeValidationGuardMetadata: %v", err)
	}
	taskRecord.Metadata = guardedMetadata
	if _, err := taskRepo.Update(ctx, taskRecord); err != nil {
		t.Fatalf("Update review task: %v", err)
	}
	if _, err := svc.MarkBlocked(ctx, created.ID, "deterministic tool validation loop blocked after 3 identical failures: file.write (content_required)", Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}

	resumed, err := svc.ResumeValidationBlockedTask(ctx, created.ID, Actor{Type: "human_user", ID: pmUser.ID})
	if err != nil {
		t.Fatalf("ResumeValidationBlockedTask: %v", err)
	}
	if resumed.WorkStatus != "review" {
		t.Fatalf("resumed work_status = %q, want review", resumed.WorkStatus)
	}
	if _, ok := ParseValidationGuard(resumed.Metadata); ok {
		t.Fatalf("expected validation guard to be cleared, metadata=%s", string(resumed.Metadata))
	}

	var payload []byte
	if err := pool.QueryRow(ctx, `
		SELECT payload
		FROM project_task_event
		WHERE task_id = $1
		  AND event_type = 'status.changed'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, created.ID).Scan(&payload); err != nil {
		t.Fatalf("load latest task event: %v", err)
	}
	var eventPayload map[string]any
	if err := json.Unmarshal(payload, &eventPayload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", eventPayload["to_status"])); got != "review" {
		t.Fatalf("to_status = %q, want review", got)
	}
}

func TestTaskServiceIntegrationResumeMissingDurableRecoveryCheckpointRepairsFromWorkspaceEX325(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	t.Setenv("OTTERCAMP_DATA_DIR", t.TempDir())

	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	pmUser := seedTaskServiceUser(t, ctx, pool, org.ID, "resume-repair-pm", "admin")
	pmAgent := seedTaskServiceAgent(t, ctx, pool, org.ID, "Resume Repair PM", "staff", "pm", "human_user", pmUser.ID)
	assignPMToProject(t, ctx, pool, pmAgent.ID, project.ID)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)

	svc := newTaskIntegrationService(t, pool)
	taskRepo := repo.NewProjectTaskRepo(pool)

	created, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Repair missing durable checkpoint",
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

	const (
		targetPath    = "docs/content-strategy.md"
		artifactPath  = ".ottercamp/recovery/docs/content-strategy.md"
		failureReason = "assistant draft for docs/content-strategy.md described tool-recovery troubleshooting instead of the file body"
	)
	targetBody := "# Content Strategy\n\n- Focus the first launch on migration-safe editorial workflows.\n"
	writeTaskRecoveryWorkspaceFiles(t, project.Slug, targetPath, artifactPath, targetBody, failureReason)

	blockerReason := "recovery halted after assistant draft for docs/content-strategy.md described tool-recovery troubleshooting instead of the file body; resume from .ottercamp/recovery/docs/content-strategy.md and re-queue only after concrete content exists"
	if _, err := svc.MarkBlocked(ctx, created.ID, blockerReason, Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}

	blocked, err := taskRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID blocked task: %v", err)
	}
	if _, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(blocked.Metadata); ok {
		t.Fatalf("expected blocked task to start without checkpoint metadata, metadata=%s", string(blocked.Metadata))
	}

	resumed, err := svc.ResumeValidationBlockedTask(ctx, created.ID, Actor{Type: "human_user", ID: pmUser.ID})
	if err != nil {
		t.Fatalf("ResumeValidationBlockedTask: %v", err)
	}
	if resumed.WorkStatus != "queued" {
		t.Fatalf("resumed work_status = %q, want queued", resumed.WorkStatus)
	}
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(resumed.Metadata)
	if !ok {
		t.Fatalf("expected repaired durable recovery checkpoint, metadata=%s", string(resumed.Metadata))
	}
	if checkpoint.TargetPath != targetPath {
		t.Fatalf("checkpoint target_path = %q, want %q", checkpoint.TargetPath, targetPath)
	}
	if checkpoint.ArtifactPath != artifactPath {
		t.Fatalf("checkpoint artifact_path = %q, want %q", checkpoint.ArtifactPath, artifactPath)
	}
	if checkpoint.FailureReason != failureReason {
		t.Fatalf("checkpoint failure_reason = %q, want %q", checkpoint.FailureReason, failureReason)
	}

	var payload []byte
	if err := pool.QueryRow(ctx, `
		SELECT payload
		FROM project_task_event
		WHERE task_id = $1
		  AND event_type = 'status.changed'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, created.ID).Scan(&payload); err != nil {
		t.Fatalf("load latest task event: %v", err)
	}
	var eventPayload map[string]any
	if err := json.Unmarshal(payload, &eventPayload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", eventPayload["recovery_blocker_class"])); got != RecoveryBlockerClassDurableRecoveryCheckpoint {
		t.Fatalf("recovery_blocker_class = %q, want %q", got, RecoveryBlockerClassDurableRecoveryCheckpoint)
	}
	if got, ok := eventPayload["recovery_checkpoint_rebuilt"].(bool); !ok || !got {
		t.Fatalf("recovery_checkpoint_rebuilt = %v, want true", eventPayload["recovery_checkpoint_rebuilt"])
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

func TestTaskServiceIntegrationQueueKeepsParentDraftAndQueuesChildWorkUnits(t *testing.T) {
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
	if queued.WorkStatus != "draft" {
		t.Fatalf("queued work_status = %q, want draft parent orchestration-only state", queued.WorkStatus)
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

	var queuedChildCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task
		WHERE project_id = $1
		  AND metadata->>'decomposition_parent_task_id' = $2
		  AND work_status = 'queued'
	`, project.ID, created.ID.String()).Scan(&queuedChildCount); err != nil {
		t.Fatalf("count queued decomposed child tasks: %v", err)
	}
	if queuedChildCount != childCount {
		t.Fatalf("queued decomposed child task count = %d, want %d", queuedChildCount, childCount)
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

func TestTaskServiceIntegrationParentDoneRequiresVerificationAndIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	svc := newTaskIntegrationService(t, pool)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)
	taskRepo := repo.NewProjectTaskRepo(pool)

	parent, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Launch integration gate",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	parentRecord, children := seedOrchestrationChildrenForParent(t, ctx, svc, taskRepo, parent.ID, project.ID, template.ID, []string{
		"Finalize landing checklist",
		"Verify billing handoff",
		"Validate analytics coverage",
	})

	for _, child := range children {
		child.WorkStatus = "done"
		if _, updateErr := taskRepo.Update(ctx, child); updateErr != nil {
			t.Fatalf("Update child %s: %v", child.ID, updateErr)
		}
	}

	markTaskReadyForDone(t, ctx, pool, parentRecord.ID, template.ID)
	if _, err := svc.TransitionStatus(ctx, parentRecord.ID, "done", Actor{Type: "agent", ID: uuid.New()}); !errors.Is(err, taskorchestration.ErrParentCompletionRequirements) {
		t.Fatalf("TransitionStatus done err = %v, want ErrParentCompletionRequirements", err)
	}

	parentRecord, err = taskRepo.GetByID(ctx, parentRecord.ID)
	if err != nil {
		t.Fatalf("GetByID parent after failed done: %v", err)
	}
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	verifications := make([]taskorchestration.ChildVerification, 0, len(children))
	for _, child := range children {
		verifications = append(verifications, taskorchestration.NewChildVerification(child.ID, "Verified "+child.Title+" against the parent outcome.", now))
	}
	parentRecord.Metadata, err = taskorchestration.Apply(parentRecord.Metadata, taskorchestration.Update{
		ChildVerifications: verifications,
		IntegrationCheck:   taskorchestration.NewIntegrationCheck("passed", "Ran the combined launch smoke test across landing, billing, and analytics.", now),
		OutcomeAssessment:  taskorchestration.NewOutcomeAssessment(true, "The launch integration gate outcome is satisfied.", now),
	})
	if err != nil {
		t.Fatalf("Apply parent orchestration metadata: %v", err)
	}
	if _, err := taskRepo.Update(ctx, parentRecord); err != nil {
		t.Fatalf("Update parent metadata: %v", err)
	}

	completed, err := svc.TransitionStatus(ctx, parentRecord.ID, "done", Actor{Type: "agent", ID: uuid.New()})
	if err != nil {
		t.Fatalf("TransitionStatus done after verification: %v", err)
	}
	if completed.WorkStatus != "done" {
		t.Fatalf("work_status = %q, want done", completed.WorkStatus)
	}
}

func TestTaskServiceIntegrationOrchestrationOnlyParentAutoCompletesWithSynthesizedMetadata(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	svc := newTaskIntegrationService(t, pool)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)
	taskRepo := repo.NewProjectTaskRepo(pool)

	parent, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Relaunch planning integration",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	parentRecord, children := seedOrchestrationChildrenForParent(t, ctx, svc, taskRepo, parent.ID, project.ID, template.ID, []string{
		"Define site structure",
		"Draft content pillars",
		"Confirm delivery plan",
	})
	for _, child := range children {
		child.WorkStatus = "done"
		if _, updateErr := taskRepo.Update(ctx, child); updateErr != nil {
			t.Fatalf("Update child %s: %v", child.ID, updateErr)
		}
	}

	completed, err := svc.TransitionStatus(ctx, parentRecord.ID, "done", Actor{
		Type:                           "system",
		AllowOrchestrationAutoComplete: true,
	})
	if err != nil {
		t.Fatalf("TransitionStatus done: %v", err)
	}
	if completed.WorkStatus != "done" {
		t.Fatalf("work_status = %q, want done", completed.WorkStatus)
	}

	stored, err := taskRepo.GetByID(ctx, parentRecord.ID)
	if err != nil {
		t.Fatalf("GetByID completed parent: %v", err)
	}
	state, ok := taskorchestration.Parse(stored.Metadata)
	if !ok {
		t.Fatal("expected parent orchestration metadata")
	}
	if len(state.ChildVerifications) != len(children) {
		t.Fatalf("child verifications len = %d, want %d", len(state.ChildVerifications), len(children))
	}
	if state.IntegrationCheck == nil || state.IntegrationCheck.Status != "passed" {
		t.Fatalf("integration check = %+v, want passed", state.IntegrationCheck)
	}
	if state.OutcomeAssessment == nil || !state.OutcomeAssessment.Satisfied {
		t.Fatalf("outcome assessment = %+v, want satisfied", state.OutcomeAssessment)
	}
}

func seedOrchestrationChildrenForParent(t *testing.T, ctx context.Context, svc TaskService, taskRepo *repo.ProjectTaskRepo, parentID, projectID, flowTemplateID uuid.UUID, titles []string) (repo.ProjectTask, []repo.ProjectTask) {
	t.Helper()

	children := make([]repo.ProjectTask, 0, len(titles))
	childIDs := make([]uuid.UUID, 0, len(titles))
	for idx, title := range titles {
		child, err := svc.CreateTask(ctx, CreateTaskRequest{
			ProjectID:      projectID,
			Title:          title,
			FlowTemplateID: &flowTemplateID,
			CreatedByType:  "system",
			Metadata:       taskdecomp.ApplyChildMetadata(json.RawMessage(`{}`), parentID, idx+1),
		})
		if err != nil {
			t.Fatalf("CreateTask child %d: %v", idx+1, err)
		}
		children = append(children, *child)
		childIDs = append(childIDs, child.ID)
	}

	parentRecord, err := taskRepo.GetByID(ctx, parentID)
	if err != nil {
		t.Fatalf("GetByID parent: %v", err)
	}
	parentRecord.Metadata = taskdecomp.ApplyMetadata(parentRecord.Metadata, taskdecomp.Plan{
		RequiresDecomposition: true,
		PrimaryDeliverable:    strings.TrimSpace(parentRecord.Title),
	}, "Seeded orchestration children for integration coverage.", childIDs)
	if _, err := taskRepo.Update(ctx, parentRecord); err != nil {
		t.Fatalf("Update parent decomposition metadata: %v", err)
	}
	parentRecord, err = taskRepo.GetByID(ctx, parentID)
	if err != nil {
		t.Fatalf("GetByID parent after decomposition: %v", err)
	}
	return parentRecord, children
}

func TestTaskServiceIntegrationCompletedChildReopenPersistsParentFeedback(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	svc := newTaskIntegrationService(t, pool)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)
	taskRepo := repo.NewProjectTaskRepo(pool)

	parentID := uuid.New()
	child, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Completed child",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		Metadata:       taskdecomp.ApplyChildMetadata(json.RawMessage(`{}`), parentID, 2),
	})
	if err != nil {
		t.Fatalf("CreateTask child: %v", err)
	}

	child.WorkStatus = "done"
	now := time.Date(2026, 3, 11, 9, 30, 0, 0, time.UTC)
	child.CompletedAt = &now
	if _, err := taskRepo.Update(ctx, *child); err != nil {
		t.Fatalf("Update child done: %v", err)
	}

	feedback := "Fix the checkout redirect mismatch discovered during parent integration."
	queued, err := svc.TransitionStatusWithPayload(ctx, child.ID, "queued", Actor{
		Type:                      "agent",
		ID:                        uuid.New(),
		AllowCompletedChildReopen: true,
	}, map[string]any{"parent_integration_feedback": feedback})
	if err != nil {
		t.Fatalf("TransitionStatusWithPayload queued: %v", err)
	}
	if queued.WorkStatus != "queued" {
		t.Fatalf("work_status = %q, want queued", queued.WorkStatus)
	}
	if queued.CompletedAt != nil {
		t.Fatalf("completed_at = %v, want nil after reopen", queued.CompletedAt)
	}

	stored, err := taskRepo.GetByID(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetByID reopened child: %v", err)
	}
	metadata := taskMetadataMap(stored.Metadata)
	if got := strings.TrimSpace(fmt.Sprintf("%v", metadata["parent_integration_feedback"])); got != feedback {
		t.Fatalf("parent_integration_feedback = %q, want %q", got, feedback)
	}
	if strings.TrimSpace(fmt.Sprintf("%v", metadata["parent_integration_feedback_recorded_at"])) == "" {
		t.Fatal("expected parent_integration_feedback_recorded_at")
	}
}

func TestTaskServiceIntegrationCompletedChildReopenRequiresParentFeedback(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	svc := newTaskIntegrationService(t, pool)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)
	taskRepo := repo.NewProjectTaskRepo(pool)

	parentID := uuid.New()
	child, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Completed child",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		Metadata:       taskdecomp.ApplyChildMetadata(json.RawMessage(`{}`), parentID, 2),
	})
	if err != nil {
		t.Fatalf("CreateTask child: %v", err)
	}

	child.WorkStatus = "done"
	now := time.Date(2026, 3, 11, 9, 31, 0, 0, time.UTC)
	child.CompletedAt = &now
	if _, err := taskRepo.Update(ctx, *child); err != nil {
		t.Fatalf("Update child done: %v", err)
	}

	_, err = svc.TransitionStatusWithPayload(ctx, child.ID, "queued", Actor{
		Type:                      "agent",
		ID:                        uuid.New(),
		AllowCompletedChildReopen: true,
	}, map[string]any{})
	if !errors.Is(err, ErrParentIntegrationFeedbackRequired) {
		t.Fatalf("TransitionStatusWithPayload err = %v, want ErrParentIntegrationFeedbackRequired", err)
	}
}

func TestTaskServiceIntegrationQueueRejectsOversizedUnsplittableWork(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskServiceOrgProject(t, ctx, pool, json.RawMessage(`{}`))
	svc := newTaskIntegrationService(t, pool)
	template := seedTaskServiceFlowTemplate(t, ctx, pool, org.ID, project.ID)

	description := "Create the full launch strategy packet that covers customer research synthesis and messaging framework and positioning rationale and editorial pillar selection and rollout sequencing and stakeholder communication in one end-to-end document for the Sam.blog relaunch without breaking it into separate reviewable work units."

	created, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID:      project.ID,
		Title:          "Launch strategy packet",
		Description:    &description,
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	_, err = svc.TransitionStatus(ctx, created.ID, "queued", Actor{Type: "system"})
	if !errors.Is(err, taskdecomp.ErrBoundedTaskTooLarge) {
		t.Fatalf("TransitionStatus queued err = %v, want ErrBoundedTaskTooLarge", err)
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

func markTaskReadyForDone(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskID, flowTemplateID uuid.UUID) {
	t.Helper()

	taskRepo := repo.NewProjectTaskRepo(pool)
	taskRecord, err := taskRepo.GetByID(ctx, taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}

	nodes, err := repo.NewFlowNodeRepo(pool).GetByTemplateOrdered(ctx, flowTemplateID)
	if err != nil {
		t.Fatalf("GetByTemplateOrdered: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("flow nodes len = 0, want > 0")
	}

	terminalNode := nodes[len(nodes)-1]
	taskRecord.FlowTemplateID = &flowTemplateID
	taskRecord.CurrentFlowNodeID = &terminalNode.ID
	taskRecord.WorkStatus = "review"
	if _, err := taskRepo.Update(ctx, taskRecord); err != nil {
		t.Fatalf("Update task terminal state: %v", err)
	}

	execRepo := repo.NewFlowNodeExecutionRepo(pool)
	for _, node := range nodes {
		if _, err := execRepo.Create(ctx, repo.FlowNodeExecution{
			TaskID:      taskID,
			FlowNodeID:  node.ID,
			VisitNumber: 1,
			Status:      "completed",
		}); err != nil {
			t.Fatalf("Create flow execution for node %s: %v", node.ID, err)
		}
	}
}

func writeTaskRecoveryWorkspaceFiles(t *testing.T, projectSlug, targetPath, artifactPath, targetBody, failureReason string) {
	t.Helper()

	root, err := workspace.ProjectRoot("", projectSlug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}
	targetAbs := filepath.Join(root, filepath.FromSlash(targetPath))
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(targetAbs, []byte(targetBody), 0o644); err != nil {
		t.Fatalf("write target file: %v", err)
	}

	artifactAbs := filepath.Join(root, filepath.FromSlash(artifactPath))
	if err := os.MkdirAll(filepath.Dir(artifactAbs), 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	artifactBody := strings.Join([]string{
		"# Recovery file.write artifact",
		"",
		"Task: WS3",
		"Target Path: " + targetPath,
		"Generated: " + time.Now().UTC().Format(time.RFC3339Nano),
		"Reason: Recovery turn halted with a durable file-output checkpoint instead of retrying without a concrete final write.",
		"",
		"## Last Write Failure",
		"",
		failureReason,
		"",
		"## Draft Content",
		"",
		strings.TrimRight(targetBody, "\n"),
	}, "\n")
	if err := os.WriteFile(artifactAbs, []byte(artifactBody), 0o644); err != nil {
		t.Fatalf("write artifact file: %v", err)
	}
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
