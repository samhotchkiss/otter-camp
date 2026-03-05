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
