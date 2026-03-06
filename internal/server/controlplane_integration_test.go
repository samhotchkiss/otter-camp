//go:build integration

package server

import (
	"bufio"
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
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

type controlPlaneIntegrationServer struct {
	URL        string
	Pool       *pgxpool.Pool
	ts         *httptest.Server
	runService controlplane.RunService
}

func (s *controlPlaneIntegrationServer) Close() {
	if s == nil {
		return
	}
	s.ts.Close()
}

func TestControlPlaneAPIRoundTripCreateListCancel(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, _, adminA, _, _ := newControlPlaneTestServer(t)
	defer testServer.Close()
	token := loginToken(t, testServer.URL, adminA.Email, "admin-password")

	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/control/runs", map[string]any{
		"trigger_type": "api",
	}, map[string]string{"Authorization": "Bearer " + token})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create run status=%d want=%d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
	runID := jsonPathString(t, created.Body, "data", "id")
	runUUID, err := uuid.Parse(runID)
	if err != nil {
		t.Fatalf("parse run id: %v", err)
	}

	if err := testServer.runService.StartRun(context.Background(), runUUID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/control/runs?status=in_progress", nil, map[string]string{"Authorization": "Bearer " + token})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list runs status=%d want=%d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}
	rows, ok := jsonPathValue(t, listed.Body, "data").([]any)
	if !ok {
		t.Fatalf("list data type=%T want=[]any body=%s", jsonPathValue(t, listed.Body, "data"), string(listed.Body))
	}
	if len(rows) == 0 {
		t.Fatalf("expected at least one in-progress run body=%s", string(listed.Body))
	}

	cancel := mustJSON(t, http.MethodPost, testServer.URL+"/v1/control/runs/"+runID+"/cancel", map[string]any{}, map[string]string{"Authorization": "Bearer " + token})
	if cancel.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel status=%d want=%d body=%s", cancel.StatusCode, http.StatusAccepted, string(cancel.Body))
	}
	if got := jsonPathString(t, cancel.Body, "data", "status"); got != "cancelling" {
		t.Fatalf("cancelled status=%q want=%q body=%s", got, "cancelling", string(cancel.Body))
	}
}

func TestControlPlaneAPIRunEventStreamReplayWithLastEventID(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, orgA, adminA, _, _ := newControlPlaneTestServer(t)
	defer testServer.Close()
	token := loginToken(t, testServer.URL, adminA.Email, "admin-password")

	runRepo := controlplane.NewRunRepository(testServer.Pool)
	eventRepo := controlplane.NewRunEventRepository(testServer.Pool)

	now := time.Now().UTC()
	runRecord, err := runRepo.Create(context.Background(), controlplane.Run{
		OrganizationID: orgA.ID,
		PrincipalType:  "human_user",
		PrincipalID:    adminA.ID,
		Status:         "in_progress",
		TriggerType:    "api",
		StartedAt:      &now,
	})
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}

	streamReq, err := http.NewRequest(http.MethodGet, testServer.URL+"/v1/control/runs/"+runRecord.ID.String()+"/events/stream", nil)
	if err != nil {
		t.Fatalf("new stream request: %v", err)
	}
	streamReq.Header.Set("Authorization", "Bearer "+token)
	streamResp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer streamResp.Body.Close()
	if streamResp.StatusCode != http.StatusOK {
		t.Fatalf("stream status=%d want=%d", streamResp.StatusCode, http.StatusOK)
	}

	for i := 1; i <= 10; i++ {
		if _, err := eventRepo.Append(context.Background(), controlplane.RunEvent{
			RunID:     runRecord.ID,
			EventType: "heartbeat",
			ActorType: "system",
			Payload:   []byte(`{"idx":` + strconv.Itoa(i) + `}`),
		}); err != nil {
			t.Fatalf("append run event %d: %v", i, err)
		}
	}

	reader := bufio.NewReader(streamResp.Body)
	received := make([]int, 0, 10)
	deadline := time.Now().Add(5 * time.Second)
	for len(received) < 10 {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for stream events, got=%v", received)
		}
		eventName, id, _, err := readSSEFrame(reader)
		if err != nil {
			if err == io.EOF {
				break
			}
			continue
		}
		if eventName != "run_event" {
			continue
		}
		seq, err := strconv.Atoi(id)
		if err != nil {
			t.Fatalf("parse event id %q: %v", id, err)
		}
		received = append(received, seq)
	}
	if len(received) != 10 {
		t.Fatalf("received events=%v, want 10 events", received)
	}
	for i := 1; i <= 10; i++ {
		if received[i-1] != i {
			t.Fatalf("received events=%v, want [1..10]", received)
		}
	}

	if _, err := runRepo.UpdateStatus(context.Background(), runRecord.ID, runRecord.Version, "completed", nil, nil); err != nil {
		t.Fatalf("mark run completed: %v", err)
	}

	replayReq, err := http.NewRequest(http.MethodGet, testServer.URL+"/v1/control/runs/"+runRecord.ID.String()+"/events/stream", nil)
	if err != nil {
		t.Fatalf("new replay request: %v", err)
	}
	replayReq.Header.Set("Authorization", "Bearer "+token)
	replayReq.Header.Set("Last-Event-ID", "5")
	replayResp, err := http.DefaultClient.Do(replayReq)
	if err != nil {
		t.Fatalf("open replay stream: %v", err)
	}
	defer replayResp.Body.Close()
	if replayResp.StatusCode != http.StatusOK {
		t.Fatalf("replay status=%d want=%d", replayResp.StatusCode, http.StatusOK)
	}

	replayReader := bufio.NewReader(replayResp.Body)
	replayed := make([]int, 0, 5)
	for {
		eventName, id, _, err := readSSEFrame(replayReader)
		if err != nil {
			if err == io.EOF {
				break
			}
			continue
		}
		if eventName != "run_event" {
			continue
		}
		seq, err := strconv.Atoi(id)
		if err != nil {
			t.Fatalf("parse replay event id %q: %v", id, err)
		}
		replayed = append(replayed, seq)
	}
	if len(replayed) != 5 {
		t.Fatalf("replayed len=%d, want 5 replayed events (6..10), got=%v", len(replayed), replayed)
	}
	for idx, seq := range replayed {
		want := idx + 6
		if seq != want {
			t.Fatalf("replayed sequence[%d]=%d, want %d; replayed=%v", idx, seq, want, replayed)
		}
	}
}

func TestControlPlaneAPICostSummaryTotals(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, orgA, adminA, _, _ := newControlPlaneTestServer(t)
	defer testServer.Close()
	token := loginToken(t, testServer.URL, adminA.Email, "admin-password")

	runRepo := controlplane.NewRunRepository(testServer.Pool)
	stepRepo := controlplane.NewRunStepRepository(testServer.Pool)
	attemptRepo := controlplane.NewRunAttemptRepository(testServer.Pool)

	runRecord, err := runRepo.Create(context.Background(), controlplane.Run{
		OrganizationID: orgA.ID,
		PrincipalType:  "human_user",
		PrincipalID:    adminA.ID,
		Status:         "completed",
		TriggerType:    "api",
	})
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	step1, err := stepRepo.Create(context.Background(), controlplane.RunStep{RunID: runRecord.ID, StepNumber: 1, Status: "completed"})
	if err != nil {
		t.Fatalf("seed step1: %v", err)
	}
	step2, err := stepRepo.Create(context.Background(), controlplane.RunStep{RunID: runRecord.ID, StepNumber: 2, Status: "completed"})
	if err != nil {
		t.Fatalf("seed step2: %v", err)
	}

	for _, attempt := range []controlplane.RunAttempt{
		{RunStepID: step1.ID, AttemptNumber: 1, Trigger: "initial", Status: "completed", InputTokens: 10, OutputTokens: 5},
		{RunStepID: step1.ID, AttemptNumber: 2, Trigger: "retry_transient", Status: "completed", InputTokens: 3, OutputTokens: 2},
		{RunStepID: step2.ID, AttemptNumber: 1, Trigger: "initial", Status: "completed", InputTokens: 20, OutputTokens: 10},
	} {
		if _, err := attemptRepo.Create(context.Background(), attempt); err != nil {
			t.Fatalf("seed attempt %+v: %v", attempt, err)
		}
	}

	summary := mustJSON(t, http.MethodGet, testServer.URL+"/v1/control/cost/summary?period=30d&group_by=project", nil, map[string]string{"Authorization": "Bearer " + token})
	if summary.StatusCode != http.StatusOK {
		t.Fatalf("cost summary status=%d want=%d body=%s", summary.StatusCode, http.StatusOK, string(summary.Body))
	}
	if got := int(jsonPathFloatValue(t, summary.Body, "data", "total_tokens")); got != 50 {
		t.Fatalf("total_tokens=%d want=%d body=%s", got, 50, string(summary.Body))
	}
}

func TestControlPlaneAPIToolExecutionsDeniedFilter(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, orgA, adminA, orgB, _ := newControlPlaneTestServer(t)
	defer testServer.Close()
	tokenA := loginToken(t, testServer.URL, adminA.Email, "admin-password")

	runRepo := controlplane.NewRunRepository(testServer.Pool)
	toolRepo := controlplane.NewToolExecutionRepository(testServer.Pool)

	runA, err := runRepo.Create(context.Background(), controlplane.Run{OrganizationID: orgA.ID, PrincipalType: "human_user", PrincipalID: adminA.ID, Status: "in_progress", TriggerType: "api"})
	if err != nil {
		t.Fatalf("seed run A: %v", err)
	}
	runB, err := runRepo.Create(context.Background(), controlplane.Run{OrganizationID: orgB.ID, PrincipalType: "human_user", PrincipalID: uuid.New(), Status: "in_progress", TriggerType: "api"})
	if err != nil {
		t.Fatalf("seed run B: %v", err)
	}

	for _, exec := range []controlplane.ToolExecution{
		{RunID: &runA.ID, ToolName: "file.read", ToolTier: "tier1", ToolDomain: "native", PolicyDecision: "denied", Status: "policy_denied", Input: json.RawMessage(`{"a":1}`)},
		{RunID: &runA.ID, ToolName: "file.list", ToolTier: "tier1", ToolDomain: "native", PolicyDecision: "allowed", Status: "completed", Input: json.RawMessage(`{"a":2}`)},
		{RunID: &runB.ID, ToolName: "file.read", ToolTier: "tier1", ToolDomain: "native", PolicyDecision: "denied", Status: "policy_denied", Input: json.RawMessage(`{"a":3}`)},
	} {
		if _, err := toolRepo.Create(context.Background(), exec); err != nil {
			t.Fatalf("seed tool execution: %v", err)
		}
	}

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/control/tool-executions?policy_decision=denied", nil, map[string]string{"Authorization": "Bearer " + tokenA})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list tool executions status=%d want=%d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}
	rows, ok := jsonPathValue(t, listed.Body, "data").([]any)
	if !ok {
		t.Fatalf("tool executions data type=%T want=[]any body=%s", jsonPathValue(t, listed.Body, "data"), string(listed.Body))
	}
	if len(rows) != 1 {
		t.Fatalf("denied tool executions len=%d want=1 body=%s", len(rows), string(listed.Body))
	}
	if got := jsonPathString(t, listed.Body, "data", "0", "policy_decision"); got != "denied" {
		t.Fatalf("policy_decision=%q want=denied body=%s", got, string(listed.Body))
	}
}

func TestOperatorDashboardSummaryIncludesStaleTaskAndExecution(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, orgA, adminA, _, _ := newControlPlaneTestServer(t)
	defer testServer.Close()
	token := loginToken(t, testServer.URL, adminA.Email, "admin-password")

	ctx := context.Background()
	projectRepo := repo.NewProjectRepo(testServer.Pool)
	taskRepo := repo.NewProjectTaskRepo(testServer.Pool)
	templateRepo := repo.NewFlowTemplateRepo(testServer.Pool)
	runRepo := controlplane.NewRunRepository(testServer.Pool)
	stateRepo := controlplane.NewRuntimeStateRepository(testServer.Pool)

	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: orgA.ID,
		Slug:           "ops-dashboard-stale",
		DisplayName:    "Ops Dashboard Stale",
		DeliveryMode:   "gated",
		CreatedByType:  "human_user",
		CreatedByID:    adminA.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgA.ID,
		Slug:           "ops-dashboard-stale-template",
		DisplayName:    "Ops Dashboard Stale Template",
		Description:    "template for operator dashboard integration tests",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "human_user",
		CreatedByID:    adminA.ID,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	createdByID := adminA.ID
	staleExecTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgA.ID,
		ProjectID:      project.ID,
		Title:          "Investigate stalled runtime",
		WorkStatus:     "in_progress",
		FlowTemplateID: &template.ID,
		CreatedByType:  "human_user",
		CreatedByID:    &createdByID,
	})
	if err != nil {
		t.Fatalf("create stale execution task: %v", err)
	}
	staleExecutionAt := time.Now().UTC().Add(-12 * time.Minute)
	staleRun, err := runRepo.Create(ctx, controlplane.Run{
		OrganizationID: orgA.ID,
		ProjectID:      &project.ID,
		TaskID:         &staleExecTask.ID,
		PrincipalType:  "human_user",
		PrincipalID:    adminA.ID,
		Status:         "in_progress",
		TriggerType:    "api",
		StartedAt:      &staleExecutionAt,
	})
	if err != nil {
		t.Fatalf("create stale run: %v", err)
	}
	if _, err := testServer.Pool.Exec(ctx, `UPDATE run SET updated_at = $2 WHERE id = $1`, staleRun.ID, staleExecutionAt); err != nil {
		t.Fatalf("backdate stale run: %v", err)
	}
	state, err := stateRepo.Ensure(ctx, orgA.ID, "task", staleExecTask.ID)
	if err != nil {
		t.Fatalf("ensure runtime state: %v", err)
	}
	if _, err := stateRepo.SetActive(ctx, state.ID, staleRun.ID, "human_user", &adminA.ID, staleExecutionAt, staleExecutionAt); err != nil {
		t.Fatalf("set active runtime state: %v", err)
	}

	staleTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgA.ID,
		ProjectID:      project.ID,
		Title:          "Review queue is wedged",
		WorkStatus:     "queued",
		FlowTemplateID: &template.ID,
		CreatedByType:  "human_user",
		CreatedByID:    &createdByID,
	})
	if err != nil {
		t.Fatalf("create stale task: %v", err)
	}
	if _, err := testServer.Pool.Exec(ctx, `UPDATE project_task SET updated_at = $2 WHERE id = $1`, staleTask.ID, time.Now().UTC().Add(-40*time.Minute)); err != nil {
		t.Fatalf("backdate stale task: %v", err)
	}

	resp := mustJSON(t, http.MethodGet, testServer.URL+"/v1/control/dashboard?limit=6", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard status=%d want=%d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}

	payload := decodeOperatorDashboardResponse(t, resp.Body)
	if payload.Summary.StaleTasks != 1 {
		t.Fatalf("stale_tasks=%d want=1 body=%s", payload.Summary.StaleTasks, string(resp.Body))
	}
	if payload.Summary.StaleExecutions != 1 {
		t.Fatalf("stale_executions=%d want=1 body=%s", payload.Summary.StaleExecutions, string(resp.Body))
	}

	staleExecutionItem := findOperatorDashboardItem(payload.Stale.Items, "stale_execution", staleRun.ID)
	if staleExecutionItem == nil {
		t.Fatalf("stale execution item missing body=%s", string(resp.Body))
	}
	if staleExecutionItem.Task == nil || staleExecutionItem.Task.ID != staleExecTask.ID {
		t.Fatalf("stale execution task ref=%+v want task=%s body=%s", staleExecutionItem.Task, staleExecTask.ID, string(resp.Body))
	}

	staleTaskItem := findOperatorDashboardItem(payload.Stale.Items, "stale_task", staleTask.ID)
	if staleTaskItem == nil {
		t.Fatalf("stale task item missing body=%s", string(resp.Body))
	}
	if staleTaskItem.Links.Task != "/v1/tasks/"+staleTask.ID.String() {
		t.Fatalf("stale task link=%q want=%q body=%s", staleTaskItem.Links.Task, "/v1/tasks/"+staleTask.ID.String(), string(resp.Body))
	}
}

func TestOperatorDashboardSectionTotalCountExceedsReturnedCountWhenLimited(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, orgA, adminA, _, _ := newControlPlaneTestServer(t)
	defer testServer.Close()
	token := loginToken(t, testServer.URL, adminA.Email, "admin-password")

	ctx := context.Background()
	projectRepo := repo.NewProjectRepo(testServer.Pool)
	taskRepo := repo.NewProjectTaskRepo(testServer.Pool)
	templateRepo := repo.NewFlowTemplateRepo(testServer.Pool)
	runRepo := controlplane.NewRunRepository(testServer.Pool)
	stateRepo := controlplane.NewRuntimeStateRepository(testServer.Pool)

	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: orgA.ID,
		Slug:           "ops-dashboard-limited-stale",
		DisplayName:    "Ops Dashboard Limited Stale",
		DeliveryMode:   "gated",
		CreatedByType:  "human_user",
		CreatedByID:    adminA.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgA.ID,
		Slug:           "ops-dashboard-limited-stale-template",
		DisplayName:    "Ops Dashboard Limited Stale Template",
		Description:    "template for operator dashboard limit integration tests",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "human_user",
		CreatedByID:    adminA.ID,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	createdByID := adminA.ID
	staleExecTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgA.ID,
		ProjectID:      project.ID,
		Title:          "Investigate stale execution limit",
		WorkStatus:     "in_progress",
		FlowTemplateID: &template.ID,
		CreatedByType:  "human_user",
		CreatedByID:    &createdByID,
	})
	if err != nil {
		t.Fatalf("create stale execution task: %v", err)
	}
	staleExecutionAt := time.Now().UTC().Add(-12 * time.Minute)
	staleRun, err := runRepo.Create(ctx, controlplane.Run{
		OrganizationID: orgA.ID,
		ProjectID:      &project.ID,
		TaskID:         &staleExecTask.ID,
		PrincipalType:  "human_user",
		PrincipalID:    adminA.ID,
		Status:         "in_progress",
		TriggerType:    "api",
		StartedAt:      &staleExecutionAt,
	})
	if err != nil {
		t.Fatalf("create stale run: %v", err)
	}
	if _, err := testServer.Pool.Exec(ctx, `UPDATE run SET updated_at = $2 WHERE id = $1`, staleRun.ID, staleExecutionAt); err != nil {
		t.Fatalf("backdate stale run: %v", err)
	}
	state, err := stateRepo.Ensure(ctx, orgA.ID, "task", staleExecTask.ID)
	if err != nil {
		t.Fatalf("ensure runtime state: %v", err)
	}
	if _, err := stateRepo.SetActive(ctx, state.ID, staleRun.ID, "human_user", &adminA.ID, staleExecutionAt, staleExecutionAt); err != nil {
		t.Fatalf("set active runtime state: %v", err)
	}

	for _, title := range []string{
		"Review backlog is idle",
		"Approval queue has not moved",
	} {
		taskRecord, createErr := taskRepo.Create(ctx, repo.ProjectTask{
			OrganizationID: orgA.ID,
			ProjectID:      project.ID,
			Title:          title,
			WorkStatus:     "queued",
			FlowTemplateID: &template.ID,
			CreatedByType:  "human_user",
			CreatedByID:    &createdByID,
		})
		if createErr != nil {
			t.Fatalf("create stale task %q: %v", title, createErr)
		}
		if _, createErr := testServer.Pool.Exec(ctx, `UPDATE project_task SET updated_at = $2 WHERE id = $1`, taskRecord.ID, time.Now().UTC().Add(-40*time.Minute)); createErr != nil {
			t.Fatalf("backdate stale task %q: %v", title, createErr)
		}
	}

	resp := mustJSON(t, http.MethodGet, testServer.URL+"/v1/control/dashboard?limit=1", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard status=%d want=%d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}

	payload := decodeOperatorDashboardResponse(t, resp.Body)
	if payload.Stale.Count != 1 {
		t.Fatalf("stale.count=%d want=1 body=%s", payload.Stale.Count, string(resp.Body))
	}
	if payload.Stale.TotalCount != 3 {
		t.Fatalf("stale.total_count=%d want=3 body=%s", payload.Stale.TotalCount, string(resp.Body))
	}
	if payload.Stale.TotalCount <= payload.Stale.Count {
		t.Fatalf("stale.total_count=%d want greater than stale.count=%d body=%s", payload.Stale.TotalCount, payload.Stale.Count, string(resp.Body))
	}
}

func TestOperatorDashboardRecentActivityIncludesFailuresRetriesAndNavigationTargets(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, orgA, adminA, _, _ := newControlPlaneTestServer(t)
	defer testServer.Close()
	token := loginToken(t, testServer.URL, adminA.Email, "admin-password")

	ctx := context.Background()
	projectRepo := repo.NewProjectRepo(testServer.Pool)
	taskRepo := repo.NewProjectTaskRepo(testServer.Pool)
	templateRepo := repo.NewFlowTemplateRepo(testServer.Pool)
	runRepo := controlplane.NewRunRepository(testServer.Pool)
	eventRepo := controlplane.NewRunEventRepository(testServer.Pool)

	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: orgA.ID,
		Slug:           "ops-dashboard-activity",
		DisplayName:    "Ops Dashboard Activity",
		DeliveryMode:   "gated",
		CreatedByType:  "human_user",
		CreatedByID:    adminA.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgA.ID,
		Slug:           "ops-dashboard-activity-template",
		DisplayName:    "Ops Dashboard Activity Template",
		Description:    "template for operator dashboard activity integration tests",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "human_user",
		CreatedByID:    adminA.ID,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	createdByID := adminA.ID
	task, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgA.ID,
		ProjectID:      project.ID,
		Title:          "Retry deployment reconciliation",
		WorkStatus:     "in_progress",
		FlowTemplateID: &template.ID,
		CreatedByType:  "human_user",
		CreatedByID:    &createdByID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	failureReason := "worker timed out"
	runRecord, err := runRepo.Create(ctx, controlplane.Run{
		OrganizationID: orgA.ID,
		ProjectID:      &project.ID,
		TaskID:         &task.ID,
		PrincipalType:  "human_user",
		PrincipalID:    adminA.ID,
		Status:         "failed",
		TriggerType:    "api",
		FailureReason:  &failureReason,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := eventRepo.Append(ctx, controlplane.RunEvent{
		RunID:     runRecord.ID,
		EventType: "run_failed",
		ActorType: "system",
		Payload:   []byte(`{"reason":"worker timed out"}`),
	}); err != nil {
		t.Fatalf("append run_failed: %v", err)
	}
	if _, err := eventRepo.Append(ctx, controlplane.RunEvent{
		RunID:     runRecord.ID,
		EventType: "wakeup_promoted",
		ActorType: "system",
		Payload:   []byte(`{"reason":"stale_owner_timeout"}`),
	}); err != nil {
		t.Fatalf("append wakeup_promoted: %v", err)
	}

	resp := mustJSON(t, http.MethodGet, testServer.URL+"/v1/control/dashboard?limit=6", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard status=%d want=%d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}

	payload := decodeOperatorDashboardResponse(t, resp.Body)
	if payload.Summary.RecentFailures < 2 {
		t.Fatalf("recent_failures=%d want>=2 body=%s", payload.Summary.RecentFailures, string(resp.Body))
	}

	retryItem := findOperatorDashboardItem(payload.RecentActivity.Items, "wakeup_promoted", runRecord.ID)
	if retryItem == nil {
		t.Fatalf("recent activity missing wakeup_promoted body=%s", string(resp.Body))
	}
	if retryItem.Project == nil || retryItem.Project.ID != project.ID {
		t.Fatalf("retry project ref=%+v want=%s body=%s", retryItem.Project, project.ID, string(resp.Body))
	}
	if retryItem.Task == nil || retryItem.Task.ID != task.ID {
		t.Fatalf("retry task ref=%+v want=%s body=%s", retryItem.Task, task.ID, string(resp.Body))
	}
	if retryItem.Run == nil || retryItem.Run.ID != runRecord.ID {
		t.Fatalf("retry run ref=%+v want=%s body=%s", retryItem.Run, runRecord.ID, string(resp.Body))
	}
	if retryItem.Links.Project != "/v1/projects/"+project.ID.String() {
		t.Fatalf("retry project link=%q want=%q body=%s", retryItem.Links.Project, "/v1/projects/"+project.ID.String(), string(resp.Body))
	}
	if retryItem.Links.Task != "/v1/tasks/"+task.ID.String() {
		t.Fatalf("retry task link=%q want=%q body=%s", retryItem.Links.Task, "/v1/tasks/"+task.ID.String(), string(resp.Body))
	}
	if retryItem.Links.Run != "/v1/control/runs/"+runRecord.ID.String() {
		t.Fatalf("retry run link=%q want=%q body=%s", retryItem.Links.Run, "/v1/control/runs/"+runRecord.ID.String(), string(resp.Body))
	}

	failureItem := findOperatorDashboardItem(payload.RecentFailures.Items, "run_failed", runRecord.ID)
	if failureItem == nil {
		t.Fatalf("recent failures missing run_failed body=%s", string(resp.Body))
	}
}

func newControlPlaneTestServer(t *testing.T) (*controlPlaneIntegrationServer, repo.Organization, repo.HumanUser, repo.Organization, repo.HumanUser) {
	t.Helper()

	pool := testdb.New(t)
	orgA, adminA := createOrgAndUser(t, pool, "control-http-org-a", "control-admin-a@example.com", "Control Admin A", "admin", "admin-password")
	orgB, adminB := createOrgAndUser(t, pool, "control-http-org-b", "control-admin-b@example.com", "Control Admin B", "admin", "admin-password")

	authService, err := authsvc.NewService(authsvc.Options{
		Users:        repo.NewHumanUserRepo(pool),
		Sessions:     repo.NewAuthSessionRepo(pool),
		APIKeys:      repo.NewAPIKeyRepo(pool),
		DefaultOrgID: orgA.ID,
		AuthMode:     "standard",
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	runService, err := controlplane.NewRunService(controlplane.RunServiceOptions{
		Pool:     pool,
		EventBus: bus,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new run service: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []RouteRegistrar{
			NewControlPlaneRouteRegistrar(ControlPlaneRouteOptions{
				Pool:       pool,
				RunService: runService,
			}),
		},
	})
	ts := httptest.NewServer(handler)
	return &controlPlaneIntegrationServer{URL: ts.URL, Pool: pool, ts: ts, runService: runService}, orgA, adminA, orgB, adminB
}

func decodeOperatorDashboardResponse(t *testing.T, body []byte) operatorDashboardResponse {
	t.Helper()

	var payload struct {
		Data operatorDashboardResponse `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode operator dashboard: %v body=%s", err, string(body))
	}
	return payload.Data
}

func findOperatorDashboardItem(items []operatorDashboardItemResponse, kind string, id uuid.UUID) *operatorDashboardItemResponse {
	for i := range items {
		item := &items[i]
		if strings.TrimSpace(item.Kind) != strings.TrimSpace(kind) {
			continue
		}
		switch {
		case item.Run != nil && item.Run.ID == id:
			return item
		case item.Task != nil && item.Task.ID == id:
			return item
		case item.Project != nil && item.Project.ID == id:
			return item
		}
	}
	return nil
}
