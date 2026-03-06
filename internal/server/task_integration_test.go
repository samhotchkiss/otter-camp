//go:build integration

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	deliverysvc "github.com/samhotchkiss/otter-camp/internal/delivery"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestTaskHTTPCreateQueueReviewDecisionLifecycle(t *testing.T) {
	testServer, org, adminUser, _ := newTaskTestServer(t)
	defer testServer.Close()

	project := seedTaskProject(t, testServer.Pool, org.ID, adminUser.ID, "task-lifecycle", true)
	seedPMAssignment(t, testServer.Pool, org.ID, project.ID, adminUser.ID)

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+project.ID.String()+"/tasks", map[string]any{
		"title":       "Lifecycle Task",
		"description": "verify queue + review decision",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create task status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
	taskID := jsonPathString(t, created.Body, "data", "id")

	queued := mustJSON(t, http.MethodPost, testServer.URL+"/v1/tasks/"+taskID+"/queue", map[string]any{}, map[string]string{"Authorization": "Bearer " + adminToken})
	if queued.StatusCode != http.StatusOK {
		t.Fatalf("queue task status = %d, want %d body=%s", queued.StatusCode, http.StatusOK, string(queued.Body))
	}
	if got := jsonPathString(t, queued.Body, "data", "work_status"); got != "draft" {
		t.Fatalf("queue work_status = %q, want %q body=%s", got, "draft", string(queued.Body))
	}

	var inboxCount int
	if err := testServer.Pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM inbox_item
		WHERE source_task_id = $1
		  AND item_type = 'human_approval_required'
	`, taskID).Scan(&inboxCount); err != nil {
		t.Fatalf("count queue inbox items: %v", err)
	}
	if inboxCount != 1 {
		t.Fatalf("human_approval_required inbox count = %d, want 1", inboxCount)
	}

	taskRepo := repo.NewProjectTaskRepo(testServer.Pool)
	taskUUID := uuid.MustParse(taskID)
	seedReviewedTerminalFlowState(t, testServer.Pool, org.ID, project.ID, taskUUID)
	taskRecord, err := taskRepo.GetByID(context.Background(), taskUUID)
	if err != nil {
		t.Fatalf("get task after queue: %v", err)
	}
	taskRecord.WorkStatus = "review"
	if _, err := taskRepo.Update(context.Background(), taskRecord); err != nil {
		t.Fatalf("set task to review: %v", err)
	}

	body := "Review this task"
	_, err = repo.NewInboxItemRepo(testServer.Pool).Create(context.Background(), repo.InboxItem{
		OrganizationID:  org.ID,
		TargetUserID:    &adminUser.ID,
		ItemType:        "task_review",
		SourceProjectID: &project.ID,
		SourceTaskID:    &taskUUID,
		CreatedByType:   "system",
		Title:           "Task review required",
		Body:            &body,
		ActionPayload:   json.RawMessage(`{"action":"review"}`),
	})
	if err != nil {
		t.Fatalf("create task_review inbox: %v", err)
	}

	approved := mustJSON(t, http.MethodPost, testServer.URL+"/v1/tasks/"+taskID+"/review-decision", map[string]any{
		"decision": "approve",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if approved.StatusCode != http.StatusOK {
		t.Fatalf("review decision status = %d, want %d body=%s", approved.StatusCode, http.StatusOK, string(approved.Body))
	}

	got := mustJSON(t, http.MethodGet, testServer.URL+"/v1/tasks/"+taskID, nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if got.StatusCode != http.StatusOK {
		t.Fatalf("get task status = %d, want %d body=%s", got.StatusCode, http.StatusOK, string(got.Body))
	}
	if status := jsonPathString(t, got.Body, "data", "work_status"); status != "done" {
		t.Fatalf("work_status = %q, want %q body=%s", status, "done", string(got.Body))
	}
}

func TestTaskHTTPQueueRequiresFlowTemplate(t *testing.T) {
	testServer, org, adminUser, _ := newTaskTestServer(t)
	defer testServer.Close()

	project := seedTaskProject(t, testServer.Pool, org.ID, adminUser.ID, "task-queue-flow-template", false)
	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+project.ID.String()+"/tasks", map[string]any{
		"title":       "Queue needs flow",
		"description": "should fail without flow template",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create task status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
	taskID := jsonPathString(t, created.Body, "data", "id")

	queued := mustJSON(t, http.MethodPost, testServer.URL+"/v1/tasks/"+taskID+"/queue", map[string]any{}, map[string]string{"Authorization": "Bearer " + adminToken})
	if queued.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("queue task status = %d, want %d body=%s", queued.StatusCode, http.StatusUnprocessableEntity, string(queued.Body))
	}
	if got := jsonPathString(t, queued.Body, "error", "message"); got != "task requires a flow template before it can be queued" {
		t.Fatalf("queue error message = %q, want %q body=%s", got, "task requires a flow template before it can be queued", string(queued.Body))
	}
}

func TestTaskHTTPQueueRejectsOutstandingProjectGateEX256(t *testing.T) {
	testServer, org, adminUser, _ := newTaskTestServer(t)
	defer testServer.Close()

	project := seedTaskProject(t, testServer.Pool, org.ID, adminUser.ID, "task-gate-queue", false)
	seedPMAssignment(t, testServer.Pool, org.ID, project.ID, adminUser.ID)
	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	gateTask, templateID, _, _ := seedFlowTask(t, testServer.Pool, org.ID, project.ID)
	taskRepo := repo.NewProjectTaskRepo(testServer.Pool)
	gateTask.Title = "Bootstrap governance gate"
	gateTask.BlocksScope = "all"
	if _, err := taskRepo.Update(context.Background(), gateTask); err != nil {
		t.Fatalf("update gate task: %v", err)
	}

	regularTask, err := taskRepo.Create(context.Background(), repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Regular queued task",
		WorkStatus:     "draft",
		FlowTemplateID: &templateID,
		CreatedByType:  "human_user",
		CreatedByID:    &adminUser.ID,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create regular task: %v", err)
	}

	queued := mustJSON(t, http.MethodPost, testServer.URL+"/v1/tasks/"+regularTask.ID.String()+"/queue", map[string]any{}, map[string]string{"Authorization": "Bearer " + adminToken})
	if queued.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("queue task status = %d, want %d body=%s", queued.StatusCode, http.StatusUnprocessableEntity, string(queued.Body))
	}
	if got := jsonPathString(t, queued.Body, "error", "message"); !strings.Contains(got, "Bootstrap governance gate") {
		t.Fatalf("queue error message = %q, want gate context body=%s", got, string(queued.Body))
	}
}

func TestTaskHTTPPatchPriorityRoundTrip(t *testing.T) {
	testServer, org, adminUser, _ := newTaskTestServer(t)
	defer testServer.Close()

	project := seedTaskProject(t, testServer.Pool, org.ID, adminUser.ID, "task-priority", false)
	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+project.ID.String()+"/tasks", map[string]any{
		"title": "Priority Task",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create task status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
	taskID := jsonPathString(t, created.Body, "data", "id")
	if got := jsonPathValue(t, created.Body, "data", "priority"); got != float64(0) {
		t.Fatalf("create priority = %v, want 0 body=%s", got, string(created.Body))
	}

	patched := mustJSON(t, http.MethodPatch, testServer.URL+"/v1/tasks/"+taskID, map[string]any{
		"priority": 4,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if patched.StatusCode != http.StatusOK {
		t.Fatalf("patch task status = %d, want %d body=%s", patched.StatusCode, http.StatusOK, string(patched.Body))
	}
	if got := jsonPathValue(t, patched.Body, "data", "priority"); got != float64(4) {
		t.Fatalf("patch priority = %v, want 4 body=%s", got, string(patched.Body))
	}

	got := mustJSON(t, http.MethodGet, testServer.URL+"/v1/tasks/"+taskID, nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if got.StatusCode != http.StatusOK {
		t.Fatalf("get task status = %d, want %d body=%s", got.StatusCode, http.StatusOK, string(got.Body))
	}
	if value := jsonPathValue(t, got.Body, "data", "priority"); value != float64(4) {
		t.Fatalf("get priority = %v, want 4 body=%s", value, string(got.Body))
	}

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/projects/"+project.ID.String()+"/tasks", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list tasks status = %d, want %d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}
	if value := jsonPathValue(t, listed.Body, "data", "0", "priority"); value != float64(4) {
		t.Fatalf("list priority = %v, want 4 body=%s", value, string(listed.Body))
	}
}

func TestTaskHTTPAdvanceFlowAndMissingActiveExecution(t *testing.T) {
	testServer, org, adminUser, _ := newTaskTestServer(t)
	defer testServer.Close()

	project := seedTaskProject(t, testServer.Pool, org.ID, adminUser.ID, "flow-advance", false)
	taskRecord, templateID, nodeA, nodeB := seedFlowTask(t, testServer.Pool, org.ID, project.ID)
	_ = templateID

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	advanced := mustJSON(t, http.MethodPost, testServer.URL+"/v1/tasks/"+taskRecord.ID.String()+"/advance-flow", map[string]any{}, map[string]string{"Authorization": "Bearer " + adminToken})
	if advanced.StatusCode != http.StatusOK {
		t.Fatalf("advance status = %d, want %d body=%s", advanced.StatusCode, http.StatusOK, string(advanced.Body))
	}

	execRepo := repo.NewFlowNodeExecutionRepo(testServer.Pool)
	taskAfter, err := repo.NewProjectTaskRepo(testServer.Pool).GetByID(context.Background(), taskRecord.ID)
	if err != nil {
		t.Fatalf("get task after advance: %v", err)
	}
	if taskAfter.CurrentFlowNodeID == nil || *taskAfter.CurrentFlowNodeID != nodeB.ID {
		t.Fatalf("current_flow_node_id = %v, want %v", taskAfter.CurrentFlowNodeID, nodeB.ID)
	}

	executions, err := execRepo.ListByTask(context.Background(), taskRecord.ID)
	if err != nil {
		t.Fatalf("list executions: %v", err)
	}
	if len(executions) != 2 {
		t.Fatalf("execution count = %d, want 2", len(executions))
	}
	if executions[0].FlowNodeID != nodeA.ID || executions[0].Status != "completed" {
		t.Fatalf("first execution = %+v, want node=%s status=completed", executions[0], nodeA.ID)
	}
	if executions[1].FlowNodeID != nodeB.ID || executions[1].Status != "active" {
		t.Fatalf("second execution = %+v, want node=%s status=active", executions[1], nodeB.ID)
	}
}

func TestTaskHTTPGetFlowIncludesTopologyCurrentStateAndActorsEX258(t *testing.T) {
	testServer, org, adminUser, _ := newTaskTestServer(t)
	defer testServer.Close()

	project := seedTaskProject(t, testServer.Pool, org.ID, adminUser.ID, "flow-visualization-linear", false)
	pmAgent := seedPMAssignment(t, testServer.Pool, org.ID, project.ID, adminUser.ID)
	graph := seedTaskFlowGraph(t, testServer.Pool, org.ID, project.ID, pmAgent.ID, adminUser.ID, false)

	taskRecord := seedTaskForFlowTemplate(t, testServer.Pool, org.ID, project.ID, graph.Template.ID, graph.Review.ID, "in_progress")
	sessionRepo := repo.NewChatSessionRepo(testServer.Pool)
	execRepo := repo.NewFlowNodeExecutionRepo(testServer.Pool)
	subtaskRepo := repo.NewProjectSubtaskRepo(testServer.Pool)

	base := time.Date(2026, time.March, 1, 10, 0, 0, 0, time.UTC)
	workSession := createTaskFlowSession(t, sessionRepo, org.ID, taskRecord.ID, "work")
	reviewSession := createTaskFlowSession(t, sessionRepo, org.ID, taskRecord.ID, "review")
	workCompletedAt := base.Add(5 * time.Minute)

	workExec, err := execRepo.Create(context.Background(), repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  graph.Work.ID,
		VisitNumber: 1,
		Status:      "completed",
		SessionID:   &workSession.ID,
		StartedAt:   base,
		CompletedAt: &workCompletedAt,
		Metadata:    json.RawMessage(`{"summary":"implemented"}`),
	})
	if err != nil {
		t.Fatalf("create work execution: %v", err)
	}
	reviewExec, err := execRepo.Create(context.Background(), repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  graph.Review.ID,
		VisitNumber: 2,
		Status:      "active",
		SessionID:   &reviewSession.ID,
		StartedAt:   base.Add(10 * time.Minute),
		Metadata:    json.RawMessage(`{"summary":"awaiting review"}`),
	})
	if err != nil {
		t.Fatalf("create review execution: %v", err)
	}

	if _, err := subtaskRepo.Create(context.Background(), repo.ProjectSubtask{
		TaskID:              taskRecord.ID,
		FlowNodeExecutionID: workExec.ID,
		Title:               "Implement endpoint payload",
		WorkStatus:          "done",
		SequenceNumber:      1,
		CreatedByType:       "system",
	}); err != nil {
		t.Fatalf("create work subtask: %v", err)
	}
	if _, err := subtaskRepo.Create(context.Background(), repo.ProjectSubtask{
		TaskID:              taskRecord.ID,
		FlowNodeExecutionID: reviewExec.ID,
		Title:               "Review topology output",
		WorkStatus:          "in_progress",
		SequenceNumber:      1,
		CreatedByType:       "system",
	}); err != nil {
		t.Fatalf("create review subtask: %v", err)
	}

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	resp := mustJSON(t, http.MethodGet, testServer.URL+"/v1/tasks/"+taskRecord.ID.String()+"/flow", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get flow status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}

	flow := decodeTaskFlowResponse(t, resp.Body)
	if flow.TaskID != taskRecord.ID {
		t.Fatalf("task_id = %s, want %s", flow.TaskID, taskRecord.ID)
	}
	if flow.CurrentNode == nil || flow.CurrentNode.ID != graph.Review.ID {
		t.Fatalf("current_node = %+v, want review node %s", flow.CurrentNode, graph.Review.ID)
	}
	if flow.CurrentExecution == nil || flow.CurrentExecution.ID != reviewExec.ID {
		t.Fatalf("current_execution = %+v, want %s", flow.CurrentExecution, reviewExec.ID)
	}
	if len(flow.Nodes) != 3 {
		t.Fatalf("node count = %d, want 3", len(flow.Nodes))
	}
	if len(flow.Edges) != 2 {
		t.Fatalf("edge count = %d, want 2", len(flow.Edges))
	}
	if !hasTaskFlowEdge(flow.Edges, graph.Work.ID, graph.Review.ID, "next", false) {
		t.Fatalf("missing work -> review edge: %+v", flow.Edges)
	}
	if !hasTaskFlowEdge(flow.Edges, graph.Review.ID, graph.Done.ID, "next", false) {
		t.Fatalf("missing review -> done edge: %+v", flow.Edges)
	}

	workNode := mustTaskFlowNode(t, flow.Nodes, graph.Work.ID)
	if workNode.State != "completed" {
		t.Fatalf("work state = %q, want completed", workNode.State)
	}
	if workNode.ActorLabel != pmAgent.DisplayName {
		t.Fatalf("work actor_label = %q, want %q", workNode.ActorLabel, pmAgent.DisplayName)
	}
	if workNode.VisitCount != 1 || len(workNode.Executions) != 1 {
		t.Fatalf("work visits = %d/%d, want 1/1", workNode.VisitCount, len(workNode.Executions))
	}
	if workNode.LatestSessionID == nil || *workNode.LatestSessionID != workSession.ID {
		t.Fatalf("work latest_session_id = %v, want %s", workNode.LatestSessionID, workSession.ID)
	}
	if workNode.SubtaskCounts.Done != 1 || workNode.SubtaskCounts.Total != 1 {
		t.Fatalf("work subtask_counts = %+v, want total=1 done=1", workNode.SubtaskCounts)
	}

	reviewNode := mustTaskFlowNode(t, flow.Nodes, graph.Review.ID)
	if !reviewNode.IsCurrent || reviewNode.State != "active" {
		t.Fatalf("review node current/state = %v/%q, want true/active", reviewNode.IsCurrent, reviewNode.State)
	}
	if reviewNode.ActorLabel != adminUser.DisplayName {
		t.Fatalf("review actor_label = %q, want %q", reviewNode.ActorLabel, adminUser.DisplayName)
	}
	if reviewNode.LatestSessionID == nil || *reviewNode.LatestSessionID != reviewSession.ID {
		t.Fatalf("review latest_session_id = %v, want %s", reviewNode.LatestSessionID, reviewSession.ID)
	}
	if reviewNode.SubtaskCounts.InProgress != 1 || reviewNode.SubtaskCounts.Total != 1 {
		t.Fatalf("review subtask_counts = %+v, want total=1 in_progress=1", reviewNode.SubtaskCounts)
	}

	doneNode := mustTaskFlowNode(t, flow.Nodes, graph.Done.ID)
	if doneNode.State != "pending" {
		t.Fatalf("done state = %q, want pending", doneNode.State)
	}
	if doneNode.ActorLabel != "Release Manager" {
		t.Fatalf("done actor_label = %q, want %q", doneNode.ActorLabel, "Release Manager")
	}
}

func TestTaskHTTPGetFlowIncludesRejectLoopAndHistoricalNodeExecutionsEX258(t *testing.T) {
	testServer, org, adminUser, _ := newTaskTestServer(t)
	defer testServer.Close()

	project := seedTaskProject(t, testServer.Pool, org.ID, adminUser.ID, "flow-visualization-loop", false)
	pmAgent := seedPMAssignment(t, testServer.Pool, org.ID, project.ID, adminUser.ID)
	graph := seedTaskFlowGraph(t, testServer.Pool, org.ID, project.ID, pmAgent.ID, adminUser.ID, true)

	taskRecord := seedTaskForFlowTemplate(t, testServer.Pool, org.ID, project.ID, graph.Template.ID, graph.Work.ID, "in_progress")
	sessionRepo := repo.NewChatSessionRepo(testServer.Pool)
	execRepo := repo.NewFlowNodeExecutionRepo(testServer.Pool)
	subtaskRepo := repo.NewProjectSubtaskRepo(testServer.Pool)

	base := time.Date(2026, time.March, 2, 14, 0, 0, 0, time.UTC)
	workSession1 := createTaskFlowSession(t, sessionRepo, org.ID, taskRecord.ID, "work-1")
	reviewSession := createTaskFlowSession(t, sessionRepo, org.ID, taskRecord.ID, "review")
	workSession2 := createTaskFlowSession(t, sessionRepo, org.ID, taskRecord.ID, "work-2")

	workCompletedAt := base.Add(5 * time.Minute)
	reviewCompletedAt := base.Add(15 * time.Minute)
	workExec1, err := execRepo.Create(context.Background(), repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  graph.Work.ID,
		VisitNumber: 1,
		Status:      "completed",
		SessionID:   &workSession1.ID,
		StartedAt:   base,
		CompletedAt: &workCompletedAt,
		Metadata:    json.RawMessage(`{"summary":"first pass"}`),
	})
	if err != nil {
		t.Fatalf("create work execution 1: %v", err)
	}
	reviewExec, err := execRepo.Create(context.Background(), repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  graph.Review.ID,
		VisitNumber: 2,
		Status:      "rejected",
		SessionID:   &reviewSession.ID,
		StartedAt:   base.Add(10 * time.Minute),
		CompletedAt: &reviewCompletedAt,
		Metadata:    json.RawMessage(`{"reason":"needs changes"}`),
	})
	if err != nil {
		t.Fatalf("create review execution: %v", err)
	}
	workExec2, err := execRepo.Create(context.Background(), repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  graph.Work.ID,
		VisitNumber: 3,
		Status:      "active",
		SessionID:   &workSession2.ID,
		StartedAt:   base.Add(20 * time.Minute),
		Metadata:    json.RawMessage(`{"summary":"rework"}`),
	})
	if err != nil {
		t.Fatalf("create work execution 2: %v", err)
	}

	if _, err := subtaskRepo.Create(context.Background(), repo.ProjectSubtask{
		TaskID:              taskRecord.ID,
		FlowNodeExecutionID: workExec1.ID,
		Title:               "Initial implementation",
		WorkStatus:          "done",
		SequenceNumber:      1,
		CreatedByType:       "system",
	}); err != nil {
		t.Fatalf("create work subtask 1: %v", err)
	}
	if _, err := subtaskRepo.Create(context.Background(), repo.ProjectSubtask{
		TaskID:              taskRecord.ID,
		FlowNodeExecutionID: reviewExec.ID,
		Title:               "Address reviewer feedback",
		WorkStatus:          "cancelled",
		SequenceNumber:      1,
		CreatedByType:       "system",
	}); err != nil {
		t.Fatalf("create review subtask: %v", err)
	}
	if _, err := subtaskRepo.Create(context.Background(), repo.ProjectSubtask{
		TaskID:              taskRecord.ID,
		FlowNodeExecutionID: workExec2.ID,
		Title:               "Rework endpoint payload",
		WorkStatus:          "in_progress",
		SequenceNumber:      1,
		CreatedByType:       "system",
	}); err != nil {
		t.Fatalf("create work subtask 2: %v", err)
	}

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	resp := mustJSON(t, http.MethodGet, testServer.URL+"/v1/tasks/"+taskRecord.ID.String()+"/flow", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get flow status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}

	flow := decodeTaskFlowResponse(t, resp.Body)
	if flow.CurrentNode == nil || flow.CurrentNode.ID != graph.Work.ID {
		t.Fatalf("current_node = %+v, want work node %s", flow.CurrentNode, graph.Work.ID)
	}
	if flow.CurrentExecution == nil || flow.CurrentExecution.ID != workExec2.ID {
		t.Fatalf("current_execution = %+v, want %s", flow.CurrentExecution, workExec2.ID)
	}
	if !hasTaskFlowEdge(flow.Edges, graph.Review.ID, graph.Work.ID, "reject", true) {
		t.Fatalf("missing reject back-edge in %+v", flow.Edges)
	}

	workNode := mustTaskFlowNode(t, flow.Nodes, graph.Work.ID)
	if !workNode.IsCurrent || workNode.State != "active" {
		t.Fatalf("work node current/state = %v/%q, want true/active", workNode.IsCurrent, workNode.State)
	}
	if workNode.VisitCount != 2 || workNode.CompletedVisits != 1 {
		t.Fatalf("work visit counts = %+v, want visit_count=2 completed_visits=1", workNode)
	}
	if len(workNode.Executions) != 2 {
		t.Fatalf("work execution count = %d, want 2", len(workNode.Executions))
	}
	if workNode.Executions[0].SessionID == nil || *workNode.Executions[0].SessionID != workSession1.ID {
		t.Fatalf("work execution[0] session = %v, want %s", workNode.Executions[0].SessionID, workSession1.ID)
	}
	if workNode.Executions[1].SessionID == nil || *workNode.Executions[1].SessionID != workSession2.ID {
		t.Fatalf("work execution[1] session = %v, want %s", workNode.Executions[1].SessionID, workSession2.ID)
	}
	if workNode.SubtaskCounts.InProgress != 1 || workNode.SubtaskCounts.Total != 1 {
		t.Fatalf("work latest subtask_counts = %+v, want total=1 in_progress=1", workNode.SubtaskCounts)
	}

	reviewNode := mustTaskFlowNode(t, flow.Nodes, graph.Review.ID)
	if reviewNode.State != "rejected" {
		t.Fatalf("review state = %q, want rejected", reviewNode.State)
	}
	if reviewNode.RejectedVisits != 1 || len(reviewNode.Executions) != 1 {
		t.Fatalf("review rejected_visits/executions = %d/%d, want 1/1", reviewNode.RejectedVisits, len(reviewNode.Executions))
	}
	if reviewNode.Executions[0].SessionID == nil || *reviewNode.Executions[0].SessionID != reviewSession.ID {
		t.Fatalf("review execution session = %v, want %s", reviewNode.Executions[0].SessionID, reviewSession.ID)
	}
	if reviewNode.SubtaskCounts.Cancelled != 1 || reviewNode.SubtaskCounts.Total != 1 {
		t.Fatalf("review subtask_counts = %+v, want total=1 cancelled=1", reviewNode.SubtaskCounts)
	}
}

func TestTaskHTTPInboxListIncludesBroadcastAndFiltersActed(t *testing.T) {
	testServer, org, adminUser, _ := newTaskTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	inboxRepo := repo.NewInboxItemRepo(testServer.Pool)

	for i := 0; i < 3; i++ {
		title := "direct-" + strconv.Itoa(i)
		if _, err := inboxRepo.Create(context.Background(), repo.InboxItem{
			OrganizationID: org.ID,
			TargetUserID:   &adminUser.ID,
			ItemType:       "system_alert",
			CreatedByType:  "system",
			Title:          title,
			ActionPayload:  json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("create direct inbox item %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		title := "broadcast-" + strconv.Itoa(i)
		if _, err := inboxRepo.Create(context.Background(), repo.InboxItem{
			OrganizationID: org.ID,
			ItemType:       "system_alert",
			CreatedByType:  "system",
			Title:          title,
			ActionPayload:  json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("create broadcast inbox item %d: %v", i, err)
		}
	}
	actedTitle := "acted-item"
	actedItem, err := inboxRepo.Create(context.Background(), repo.InboxItem{
		OrganizationID: org.ID,
		TargetUserID:   &adminUser.ID,
		ItemType:       "system_alert",
		CreatedByType:  "system",
		Title:          actedTitle,
		ActionPayload:  json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create acted inbox item: %v", err)
	}
	if _, err := inboxRepo.MarkActed(context.Background(), actedItem.ID, adminUser.ID); err != nil {
		t.Fatalf("mark acted inbox item: %v", err)
	}

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/inbox", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list inbox status = %d, want %d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}
	items, ok := jsonPathValue(t, listed.Body, "data").([]any)
	if !ok {
		t.Fatalf("inbox list data type = %T, want []any body=%s", jsonPathValue(t, listed.Body, "data"), string(listed.Body))
	}
	if len(items) != 5 {
		t.Fatalf("inbox item count = %d, want 5 body=%s", len(items), string(listed.Body))
	}

	unacted := mustJSON(t, http.MethodGet, testServer.URL+"/v1/inbox?is_acted=false", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if unacted.StatusCode != http.StatusOK {
		t.Fatalf("inbox unacted status = %d, want %d body=%s", unacted.StatusCode, http.StatusOK, string(unacted.Body))
	}
	unactedItems, ok := jsonPathValue(t, unacted.Body, "data").([]any)
	if !ok {
		t.Fatalf("unacted data type = %T, want []any body=%s", jsonPathValue(t, unacted.Body, "data"), string(unacted.Body))
	}
	if len(unactedItems) != 5 {
		t.Fatalf("unacted item count = %d, want 5 body=%s", len(unactedItems), string(unacted.Body))
	}
}

func TestTaskHTTPMergeQueuePositionOrder(t *testing.T) {
	testServer, org, adminUser, _ := newTaskTestServer(t)
	defer testServer.Close()

	project := seedTaskProject(t, testServer.Pool, org.ID, adminUser.ID, "merge-queue", false)
	queueRepo := repo.NewMergeQueueEntryRepo(testServer.Pool)
	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	taskA := seedTaskRecord(t, testServer.Pool, org.ID, project.ID, "queue-a", "done", "feature/a")
	taskB := seedTaskRecord(t, testServer.Pool, org.ID, project.ID, "queue-b", "done", "feature/b")
	taskC := seedTaskRecord(t, testServer.Pool, org.ID, project.ID, "queue-c", "done", "feature/c")

	if _, err := queueRepo.Enqueue(context.Background(), repo.MergeQueueEntry{ProjectID: project.ID, TaskID: taskA.ID, BranchName: "feature/a"}); err != nil {
		t.Fatalf("enqueue A: %v", err)
	}
	if _, err := queueRepo.Enqueue(context.Background(), repo.MergeQueueEntry{ProjectID: project.ID, TaskID: taskB.ID, BranchName: "feature/b"}); err != nil {
		t.Fatalf("enqueue B: %v", err)
	}
	if _, err := queueRepo.Enqueue(context.Background(), repo.MergeQueueEntry{ProjectID: project.ID, TaskID: taskC.ID, BranchName: "feature/c"}); err != nil {
		t.Fatalf("enqueue C: %v", err)
	}

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/projects/"+project.ID.String()+"/merge-queue", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("merge queue list status = %d, want %d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}
	items, ok := jsonPathValue(t, listed.Body, "data").([]any)
	if !ok {
		t.Fatalf("merge queue data type = %T, want []any body=%s", jsonPathValue(t, listed.Body, "data"), string(listed.Body))
	}
	if len(items) != 3 {
		t.Fatalf("merge queue item count = %d, want 3 body=%s", len(items), string(listed.Body))
	}
	queueTaskID := func(index int) string {
		t.Helper()
		item, ok := items[index].(map[string]any)
		if !ok {
			t.Fatalf("merge queue item[%d] type = %T, want map[string]any body=%s", index, items[index], string(listed.Body))
		}
		value, _ := item["task_id"].(string)
		return value
	}
	if got := queueTaskID(0); got != taskA.ID.String() {
		t.Fatalf("queue[0].task_id = %q, want %q body=%s", got, taskA.ID.String(), string(listed.Body))
	}
	if got := queueTaskID(1); got != taskB.ID.String() {
		t.Fatalf("queue[1].task_id = %q, want %q body=%s", got, taskB.ID.String(), string(listed.Body))
	}
	if got := queueTaskID(2); got != taskC.ID.String() {
		t.Fatalf("queue[2].task_id = %q, want %q body=%s", got, taskC.ID.String(), string(listed.Body))
	}
}

func TestTaskHTTPRemoteDeleteProtectionWithActiveEnvironment(t *testing.T) {
	testServer, org, adminUser, _ := newTaskTestServer(t)
	defer testServer.Close()

	project := seedTaskProject(t, testServer.Pool, org.ID, adminUser.ID, "delivery-remotes", false)
	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	createdRemote := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+project.ID.String()+"/remotes", map[string]any{
		"name":      "origin",
		"url":       "https://example.com/repo.git",
		"transport": "https",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createdRemote.StatusCode != http.StatusCreated {
		t.Fatalf("create remote status = %d, want %d body=%s", createdRemote.StatusCode, http.StatusCreated, string(createdRemote.Body))
	}
	remoteID := jsonPathString(t, createdRemote.Body, "data", "id")

	createdEnv := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+project.ID.String()+"/environments", map[string]any{
		"name":          "production",
		"delivery_mode": "gated",
		"remote_id":     remoteID,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createdEnv.StatusCode != http.StatusCreated {
		t.Fatalf("create environment status = %d, want %d body=%s", createdEnv.StatusCode, http.StatusCreated, string(createdEnv.Body))
	}
	environmentID := jsonPathString(t, createdEnv.Body, "data", "id")

	blockedDelete := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/projects/"+project.ID.String()+"/remotes/"+remoteID, nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if blockedDelete.StatusCode != http.StatusConflict {
		t.Fatalf("delete remote while active status = %d, want %d body=%s", blockedDelete.StatusCode, http.StatusConflict, string(blockedDelete.Body))
	}

	deactivateEnv := mustJSON(t, http.MethodPatch, testServer.URL+"/v1/projects/"+project.ID.String()+"/environments/"+environmentID, map[string]any{
		"is_active": false,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if deactivateEnv.StatusCode != http.StatusOK {
		t.Fatalf("deactivate environment status = %d, want %d body=%s", deactivateEnv.StatusCode, http.StatusOK, string(deactivateEnv.Body))
	}

	deleted := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/projects/"+project.ID.String()+"/remotes/"+remoteID, nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete remote status = %d, want %d body=%s", deleted.StatusCode, http.StatusNoContent, string(deleted.Body))
	}
}

func TestTaskHTTPDeliveryEndpointsListAndCreate(t *testing.T) {
	testServer, org, adminUser, _ := newTaskTestServer(t)
	defer testServer.Close()

	project := seedTaskProject(t, testServer.Pool, org.ID, adminUser.ID, "delivery-list", false)
	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	remotes := mustJSON(t, http.MethodGet, testServer.URL+"/v1/projects/"+project.ID.String()+"/remotes", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if remotes.StatusCode != http.StatusOK {
		t.Fatalf("list remotes status = %d, want %d body=%s", remotes.StatusCode, http.StatusOK, string(remotes.Body))
	}
	remoteItems, ok := jsonPathValue(t, remotes.Body, "data").([]any)
	if !ok {
		t.Fatalf("list remotes data type = %T, want []any body=%s", jsonPathValue(t, remotes.Body, "data"), string(remotes.Body))
	}
	if len(remoteItems) != 0 {
		t.Fatalf("list remotes count = %d, want 0 body=%s", len(remoteItems), string(remotes.Body))
	}

	createdRemote := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+project.ID.String()+"/remotes", map[string]any{
		"name":      "origin",
		"url":       "https://example.com/repo.git",
		"transport": "https",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createdRemote.StatusCode != http.StatusCreated {
		t.Fatalf("create remote status = %d, want %d body=%s", createdRemote.StatusCode, http.StatusCreated, string(createdRemote.Body))
	}

	environments := mustJSON(t, http.MethodGet, testServer.URL+"/v1/projects/"+project.ID.String()+"/environments", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if environments.StatusCode != http.StatusOK {
		t.Fatalf("list environments status = %d, want %d body=%s", environments.StatusCode, http.StatusOK, string(environments.Body))
	}
	environmentItems, ok := jsonPathValue(t, environments.Body, "data").([]any)
	if !ok {
		t.Fatalf("list environments data type = %T, want []any body=%s", jsonPathValue(t, environments.Body, "data"), string(environments.Body))
	}
	if len(environmentItems) != 0 {
		t.Fatalf("list environments count = %d, want 0 body=%s", len(environmentItems), string(environments.Body))
	}
}

func TestTaskHTTPRollbackCreatesQueuedDeployTask(t *testing.T) {
	testServer, org, adminUser, _ := newTaskTestServer(t)
	defer testServer.Close()

	project := seedTaskProject(t, testServer.Pool, org.ID, adminUser.ID, "rollback", false)
	seedPMAssignment(t, testServer.Pool, org.ID, project.ID, adminUser.ID)
	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	resp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+project.ID.String()+"/rollback", map[string]any{
		"target_commit_sha": "deadbeefcafefeed",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("rollback status = %d, want %d body=%s", resp.StatusCode, http.StatusAccepted, string(resp.Body))
	}

	rollbackTaskID := uuid.MustParse(jsonPathString(t, resp.Body, "data", "rollback_task_id"))
	taskRecord, err := repo.NewProjectTaskRepo(testServer.Pool).GetByID(context.Background(), rollbackTaskID)
	if err != nil {
		t.Fatalf("get rollback task: %v", err)
	}
	if taskRecord.ProjectID != project.ID {
		t.Fatalf("rollback task project_id = %s, want %s", taskRecord.ProjectID, project.ID)
	}
	if taskRecord.WorkStatus != "queued" {
		t.Fatalf("rollback task work_status = %q, want queued", taskRecord.WorkStatus)
	}

	var metadata map[string]any
	if err := json.Unmarshal(taskRecord.Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal rollback metadata: %v", err)
	}
	if rollback, _ := metadata["rollback"].(bool); !rollback {
		t.Fatalf("rollback metadata flag = %v, want true", metadata["rollback"])
	}
	if got, _ := metadata["target_commit_sha"].(string); got != "deadbeefcafefeed" {
		t.Fatalf("target_commit_sha = %q, want deadbeefcafefeed", got)
	}
}

func newTaskTestServer(t *testing.T) (*authIntegrationServer, repo.Organization, repo.HumanUser, repo.HumanUser) {
	t.Helper()

	pool := testdb.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	slug := "task-http-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	adminEmail := "admin+" + slug + "@example.com"
	memberEmail := "member+" + slug + "@example.com"
	org, adminUser := createOrgAndUser(t, pool, slug, adminEmail, "Admin", "admin", "admin-password")
	_, memberUser := createOrgAndUser(t, pool, slug, memberEmail, "Member", "member", "member-password")

	authService, err := authsvc.NewService(authsvc.Options{
		Users:        repo.NewHumanUserRepo(pool),
		Sessions:     repo.NewAuthSessionRepo(pool),
		APIKeys:      repo.NewAPIKeyRepo(pool),
		DefaultOrgID: org.ID,
		AuthMode:     "standard",
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	bus := eventbus.New(pool, logger, eventbus.Config{})
	taskService, err := tasksvc.NewService(tasksvc.Options{Pool: pool, EventBus: bus})
	if err != nil {
		t.Fatalf("new task service: %v", err)
	}
	flowService, err := flowsvc.NewService(flowsvc.Options{Pool: pool, TasksService: taskService, Events: bus})
	if err != nil {
		t.Fatalf("new flow service: %v", err)
	}
	deliveryService, err := deliverysvc.NewService(deliverysvc.Options{Pool: pool})
	if err != nil {
		t.Fatalf("new delivery service: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      logger,
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []RouteRegistrar{
			NewTaskRouteRegistrar(taskService, flowService, deliveryService, pool),
		},
	})

	ts := httptest.NewServer(handler)
	return &authIntegrationServer{URL: ts.URL, Pool: pool, ts: ts}, org, adminUser, memberUser
}

func seedTaskProject(t *testing.T, pool *pgxpool.Pool, orgID, createdByID uuid.UUID, slug string, requiresHumanReview bool) repo.Project {
	t.Helper()

	settings := json.RawMessage(`{}`)
	if requiresHumanReview {
		settings = json.RawMessage(`{"requires_human_review":true}`)
	}

	project, err := repo.NewProjectRepo(pool).Create(context.Background(), repo.Project{
		OrganizationID: orgID,
		Slug:           slug + "-" + uuid.NewString()[:8],
		DisplayName:    slug,
		Description:    "",
		DeliveryMode:   "gated",
		Settings:       settings,
		CreatedByType:  "human_user",
		CreatedByID:    createdByID,
	})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return project
}

func seedPMAssignment(t *testing.T, pool *pgxpool.Pool, orgID, projectID, userID uuid.UUID) repo.Agent {
	t.Helper()

	agent, err := repo.NewAgentRepo(pool).Create(context.Background(), repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          "pm-" + uuid.NewString()[:8],
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "",
		OperatorInstructions: "",
		AgentType:            "pm",
		CreatedByType:        "human_user",
		CreatedByID:          userID,
	})
	if err != nil {
		t.Fatalf("seed PM agent: %v", err)
	}
	if _, err := repo.NewAgentProjectAssignmentRepo(pool).Assign(context.Background(), repo.AgentProjectAssignment{
		AgentID:        agent.ID,
		ProjectID:      projectID,
		Role:           "pm",
		AssignedByType: "human_user",
		AssignedByID:   &userID,
		IsActive:       true,
	}); err != nil {
		t.Fatalf("seed PM assignment: %v", err)
	}
	return agent
}

func seedTaskRecord(t *testing.T, pool *pgxpool.Pool, orgID, projectID uuid.UUID, title, status, branch string) repo.ProjectTask {
	t.Helper()

	branchPtr := branch
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(context.Background(), repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      projectID,
		Title:          title,
		WorkStatus:     status,
		BranchName:     &branchPtr,
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	return taskRecord
}

func seedFlowTask(t *testing.T, pool *pgxpool.Pool, orgID, projectID uuid.UUID) (repo.ProjectTask, uuid.UUID, repo.FlowNode, repo.FlowNode) {
	t.Helper()

	template, err := repo.NewFlowTemplateRepo(pool).Create(context.Background(), repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "flow-" + uuid.NewString()[:8],
		DisplayName:    "Flow",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	nodeRepo := repo.NewFlowNodeRepo(pool)
	nodeA, err := nodeRepo.Create(context.Background(), repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Node A",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create node A: %v", err)
	}
	nodeB, err := nodeRepo.Create(context.Background(), repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Node B",
		NodeType:       "work",
		Position:       2,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create node B: %v", err)
	}
	nodeA.NextNodeID = &nodeB.ID
	if _, err := nodeRepo.Update(context.Background(), nodeA); err != nil {
		t.Fatalf("update node A next_node_id: %v", err)
	}
	template.StartNodeID = &nodeA.ID
	if _, err := repo.NewFlowTemplateRepo(pool).Update(context.Background(), template); err != nil {
		t.Fatalf("update template start node: %v", err)
	}

	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(context.Background(), repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      projectID,
		Title:          "flow-task",
		WorkStatus:     "in_progress",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow task: %v", err)
	}
	if _, err := repo.NewProjectTaskRepo(pool).SetFlowNode(context.Background(), taskRecord.ID, &nodeA.ID); err != nil {
		t.Fatalf("set task current node: %v", err)
	}
	taskRecord.CurrentFlowNodeID = &nodeA.ID

	return taskRecord, template.ID, nodeA, nodeB
}

func seedReviewedTerminalFlowState(t *testing.T, pool *pgxpool.Pool, orgID, projectID, taskID uuid.UUID) {
	t.Helper()

	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)
	execRepo := repo.NewFlowNodeExecutionRepo(pool)
	taskRepo := repo.NewProjectTaskRepo(pool)

	template, err := templateRepo.Create(context.Background(), repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "review-terminal-" + uuid.NewString()[:8],
		DisplayName:    "Review Terminal",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create review terminal template: %v", err)
	}

	workNode, err := nodeRepo.Create(context.Background(), repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create work node: %v", err)
	}
	reviewNode, err := nodeRepo.Create(context.Background(), repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       2,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create review node: %v", err)
	}
	doneNode, err := nodeRepo.Create(context.Background(), repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Merge",
		NodeType:       "merge",
		Position:       3,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create done node: %v", err)
	}

	workNode.NextNodeID = &reviewNode.ID
	if _, err := nodeRepo.Update(context.Background(), workNode); err != nil {
		t.Fatalf("update work node next_node_id: %v", err)
	}
	reviewNode.NextNodeID = &doneNode.ID
	reviewNode.RejectNodeID = &workNode.ID
	if _, err := nodeRepo.Update(context.Background(), reviewNode); err != nil {
		t.Fatalf("update review node edges: %v", err)
	}
	template.StartNodeID = &workNode.ID
	if _, err := templateRepo.Update(context.Background(), template); err != nil {
		t.Fatalf("update template start node: %v", err)
	}

	taskRecord, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task for terminal flow seed: %v", err)
	}
	taskRecord.FlowTemplateID = &template.ID
	taskRecord.CurrentFlowNodeID = &doneNode.ID
	if _, err := taskRepo.Update(context.Background(), taskRecord); err != nil {
		t.Fatalf("update task terminal flow state: %v", err)
	}

	for index, nodeID := range []uuid.UUID{workNode.ID, reviewNode.ID, doneNode.ID} {
		if _, err := execRepo.Create(context.Background(), repo.FlowNodeExecution{
			TaskID:      taskID,
			FlowNodeID:  nodeID,
			VisitNumber: index + 1,
			Status:      "completed",
			Metadata:    json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("create completed execution %d: %v", index+1, err)
		}
	}
}

type seededTaskFlowGraph struct {
	Template repo.FlowTemplate
	Work     repo.FlowNode
	Review   repo.FlowNode
	Done     repo.FlowNode
}

func seedTaskFlowGraph(t *testing.T, pool *pgxpool.Pool, orgID, projectID, agentID, reviewerID uuid.UUID, includeRejectLoop bool) seededTaskFlowGraph {
	t.Helper()

	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)

	template, err := templateRepo.Create(context.Background(), repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "flow-visualization-" + uuid.NewString()[:8],
		DisplayName:    "Flow Visualization",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create visualization template: %v", err)
	}

	agentType := "agent"
	humanType := "human"
	roleType := "role"

	workNode, err := nodeRepo.Create(context.Background(), repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Implement",
		NodeType:       "work",
		Position:       1,
		ActorType:      &agentType,
		ActorID:        &agentID,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create work node: %v", err)
	}
	reviewNode, err := nodeRepo.Create(context.Background(), repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       2,
		ActorType:      &humanType,
		ActorID:        &reviewerID,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create review node: %v", err)
	}
	doneNode, err := nodeRepo.Create(context.Background(), repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Merge",
		NodeType:       "merge",
		Position:       3,
		ActorType:      &roleType,
		MaxVisits:      3,
		Metadata:       json.RawMessage(`{"actor_role":"release_manager"}`),
	})
	if err != nil {
		t.Fatalf("create done node: %v", err)
	}

	workNode.NextNodeID = &reviewNode.ID
	if _, err := nodeRepo.Update(context.Background(), workNode); err != nil {
		t.Fatalf("update work node next edge: %v", err)
	}
	reviewNode.NextNodeID = &doneNode.ID
	if includeRejectLoop {
		reviewNode.RejectNodeID = &workNode.ID
	}
	if _, err := nodeRepo.Update(context.Background(), reviewNode); err != nil {
		t.Fatalf("update review node edges: %v", err)
	}
	template.StartNodeID = &workNode.ID
	if _, err := templateRepo.Update(context.Background(), template); err != nil {
		t.Fatalf("update visualization template start node: %v", err)
	}

	return seededTaskFlowGraph{
		Template: template,
		Work:     workNode,
		Review:   reviewNode,
		Done:     doneNode,
	}
}

func seedTaskForFlowTemplate(t *testing.T, pool *pgxpool.Pool, orgID, projectID, templateID, currentNodeID uuid.UUID, workStatus string) repo.ProjectTask {
	t.Helper()

	taskRepo := repo.NewProjectTaskRepo(pool)
	taskRecord, err := taskRepo.Create(context.Background(), repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      projectID,
		Title:          "flow-visualization-task",
		WorkStatus:     workStatus,
		FlowTemplateID: &templateID,
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create visualization task: %v", err)
	}
	taskRecord, err = taskRepo.SetFlowNode(context.Background(), taskRecord.ID, &currentNodeID)
	if err != nil {
		t.Fatalf("set visualization task current node: %v", err)
	}
	return taskRecord
}

func createTaskFlowSession(t *testing.T, sessionRepo *repo.ChatSessionRepo, orgID, taskID uuid.UUID, mode string) repo.ChatSession {
	t.Helper()

	title := mode
	session, err := sessionRepo.Create(context.Background(), repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      "project_task",
		ScopeID:        taskID,
		Mode:           "sync",
		Status:         "active",
		Title:          &title,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create task flow session: %v", err)
	}
	return session
}

func decodeTaskFlowResponse(t *testing.T, body []byte) taskFlowResponse {
	t.Helper()

	var payload struct {
		Data taskFlowResponse `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode task flow response: %v body=%s", err, string(body))
	}
	return payload.Data
}

func mustTaskFlowNode(t *testing.T, nodes []taskFlowNodeViewResponse, nodeID uuid.UUID) taskFlowNodeViewResponse {
	t.Helper()

	for _, node := range nodes {
		if node.ID == nodeID {
			return node
		}
	}
	t.Fatalf("node %s not found in %+v", nodeID, nodes)
	return taskFlowNodeViewResponse{}
}

func hasTaskFlowEdge(edges []taskFlowEdgeResponse, fromNodeID, toNodeID uuid.UUID, kind string, isBackEdge bool) bool {
	for _, edge := range edges {
		if edge.FromNodeID == fromNodeID && edge.ToNodeID == toNodeID && edge.Kind == kind && edge.IsBackEdge == isBackEdge {
			return true
		}
	}
	return false
}
