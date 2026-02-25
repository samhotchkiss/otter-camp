//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/e2e/testutil"
)

func TestControlPlane_RunLifecycle(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	token, _, _ := resetBootstrapAndLogin(t, server, baseURL)
	projectID := createControlPlaneProject(t, baseURL, token, "cp-lifecycle")
	taskID := createProjectTask(t, baseURL, token, projectID, "Run lifecycle test task", "Control plane lifecycle task", "")
	runID := createControlRun(t, baseURL, token, taskID)

	run := testutil.WaitForRunStatus(t, baseURL, token, runID, "in_progress", 30*time.Second)
	if strings.TrimSpace(asString(run["id"])) != runID {
		t.Fatalf("run id=%q want=%q run=%+v", asString(run["id"]), runID, run)
	}
	if strings.TrimSpace(asString(run["task_id"])) != taskID {
		t.Fatalf("run task_id=%q want=%q run=%+v", asString(run["task_id"]), taskID, run)
	}

	stepsBody, stepsStatus := testutil.GET(t, baseURL, "/v1/control/runs/"+runID+"/steps", token)
	if stepsStatus != http.StatusOK {
		t.Fatalf("GET /v1/control/runs/%s/steps status=%d want=%d body=%s", runID, stepsStatus, http.StatusOK, string(stepsBody))
	}
	steps := asArray(t, testutil.JSONPath(t, stepsBody, "data"), "run steps")
	if len(steps) < 1 {
		t.Fatalf("expected at least one run step body=%s", string(stepsBody))
	}
}

func TestControlPlane_PolicyAllow(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	token, orgID, frankID := resetBootstrapAndLogin(t, server, baseURL)
	projectID := createControlPlaneProject(t, baseURL, token, "cp-allow")
	taskID := createProjectTask(
		t,
		baseURL,
		token,
		projectID,
		"[tool-call:file.write] Write a test file",
		"Triggers deterministic tier-2 file.write in test mode",
		"",
	)

	evalBody, evalStatus := testutil.POST(t, baseURL, "/v1/control/policies/evaluate", token, map[string]any{
		"organization_id": orgID,
		"project_id":      projectID,
		"agent_id":        frankID,
		"capability":      "system.file.write",
	})
	if evalStatus != http.StatusOK {
		t.Fatalf("POST /v1/control/policies/evaluate status=%d want=%d body=%s", evalStatus, http.StatusOK, string(evalBody))
	}
	effect := strings.ToLower(strings.TrimSpace(asString(testutil.JSONPath(t, evalBody, "data", "effect"))))
	if effect != "allow" {
		t.Fatalf("policy evaluation effect=%q want=allow body=%s", effect, string(evalBody))
	}

	runID := createControlRun(t, baseURL, token, taskID)
	_ = testutil.WaitForRunStatus(t, baseURL, token, runID, "in_progress", 30*time.Second)
	exec := testutil.WaitForToolExecution(t, baseURL, token, testutil.ToolExecutionFilter{
		RunID:    runID,
		ToolName: "file.write",
	}, 30*time.Second)
	if got := strings.ToLower(strings.TrimSpace(asString(exec["policy_decision"]))); got != "allowed" {
		t.Fatalf("tool_execution policy_decision=%q want=allowed exec=%+v", got, exec)
	}
	if got := strings.ToLower(strings.TrimSpace(asString(exec["tool_tier"]))); got != "tier2" {
		t.Fatalf("tool_execution tool_tier=%q want=tier2 exec=%+v", got, exec)
	}
	if got := strings.ToLower(strings.TrimSpace(asString(exec["status"]))); got != "completed" {
		t.Fatalf("tool_execution status=%q want=completed exec=%+v", got, exec)
	}

	auditBody, auditStatus := testutil.GET(t, baseURL, "/v1/audit?action=capability_allowed", token)
	if auditStatus != http.StatusOK {
		t.Fatalf("GET /v1/audit?action=capability_allowed status=%d want=%d body=%s", auditStatus, http.StatusOK, string(auditBody))
	}
	audits := asArray(t, testutil.JSONPath(t, auditBody, "data"), "allow audit events")
	if len(audits) < 1 {
		t.Fatalf("expected capability_allowed audit event body=%s", string(auditBody))
	}
}

func TestControlPlane_PolicyDeny_RunContinues(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	token, _, _ := resetBootstrapAndLogin(t, server, baseURL)
	projectID := createControlPlaneProject(t, baseURL, token, "cp-deny")

	policyBody, policyStatus := testutil.POST(t, baseURL, "/v1/control/policies", token, map[string]any{
		"policy_layer": "project",
		"project_id":   projectID,
		"capability":   "system.network.outbound",
		"effect":       "deny",
	})
	if policyStatus != http.StatusCreated {
		t.Fatalf("POST /v1/control/policies status=%d want=%d body=%s", policyStatus, http.StatusCreated, string(policyBody))
	}

	taskID := createProjectTask(
		t,
		baseURL,
		token,
		projectID,
		"[tool-call:network.fetch] Fetch external URL",
		"Triggers deterministic tier-2 network.fetch in test mode",
		"",
	)
	runID := createControlRun(t, baseURL, token, taskID)

	exec := testutil.WaitForToolExecution(t, baseURL, token, testutil.ToolExecutionFilter{
		RunID:          runID,
		PolicyDecision: "denied",
	}, 30*time.Second)
	if got := strings.ToLower(strings.TrimSpace(asString(exec["tool_name"]))); !strings.Contains(got, "network") {
		t.Fatalf("tool_execution tool_name=%q want contains network exec=%+v", got, exec)
	}
	if got := strings.ToLower(strings.TrimSpace(asString(exec["status"]))); got != "policy_denied" {
		t.Fatalf("tool_execution status=%q want=policy_denied exec=%+v", got, exec)
	}

	runBody, runStatus := testutil.GET(t, baseURL, "/v1/control/runs/"+runID, token)
	if runStatus != http.StatusOK {
		t.Fatalf("GET /v1/control/runs/%s status=%d want=%d body=%s", runID, runStatus, http.StatusOK, string(runBody))
	}
	run := asObject(t, testutil.JSONPath(t, runBody, "data"), "run")
	if got := strings.ToLower(strings.TrimSpace(asString(run["status"]))); got == "failed" {
		t.Fatalf("run status=%q want != failed body=%s", got, string(runBody))
	}

	auditBody, auditStatus := testutil.GET(t, baseURL, "/v1/audit?action=capability_denied", token)
	if auditStatus != http.StatusOK {
		t.Fatalf("GET /v1/audit?action=capability_denied status=%d want=%d body=%s", auditStatus, http.StatusOK, string(auditBody))
	}
	audits := asArray(t, testutil.JSONPath(t, auditBody, "data"), "deny audit events")
	if len(audits) < 1 {
		t.Fatalf("expected capability_denied audit event body=%s", string(auditBody))
	}
}

func TestControlPlane_RunCancellation(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	token, _, _ := resetBootstrapAndLogin(t, server, baseURL)
	projectID := createControlPlaneProject(t, baseURL, token, "cp-cancel")
	taskID := createProjectTask(t, baseURL, token, projectID, "[slow-run] Long running task", "Deterministic slow run for cancel test", "")
	runID := createControlRun(t, baseURL, token, taskID)

	_ = testutil.WaitForRunStatus(t, baseURL, token, runID, "in_progress", 30*time.Second)

	cancelBody, cancelStatus := testutil.POST(t, baseURL, "/v1/control/runs/"+runID+"/cancel", token, map[string]any{})
	if cancelStatus != http.StatusAccepted {
		t.Fatalf("POST /v1/control/runs/%s/cancel status=%d want=%d body=%s", runID, cancelStatus, http.StatusAccepted, string(cancelBody))
	}
	cancelState := strings.ToLower(strings.TrimSpace(asString(testutil.JSONPath(t, cancelBody, "data", "status"))))
	if cancelState != "cancelling" && cancelState != "cancelled" {
		t.Fatalf("cancel response status=%q want=cancelling|cancelled body=%s", cancelState, string(cancelBody))
	}

	_ = testutil.WaitForRunStatus(t, baseURL, token, runID, "cancelled", 30*time.Second)

	eventsBody, eventsStatus := testutil.GET(t, baseURL, "/v1/control/runs/"+runID+"/events", token)
	if eventsStatus != http.StatusOK {
		t.Fatalf("GET /v1/control/runs/%s/events status=%d want=%d body=%s", runID, eventsStatus, http.StatusOK, string(eventsBody))
	}
	events := asArray(t, testutil.JSONPath(t, eventsBody, "data"), "run events")
	if !containsAnyRunEventType(events, "run_cancelled", "cancelled") {
		t.Fatalf("expected run_cancelled event body=%s", string(eventsBody))
	}
}

func resetBootstrapAndLogin(t *testing.T, server *testutil.ServerProcess, baseURL string) (token, orgID, frankID string) {
	t.Helper()
	testutil.ResetState(t, baseURL)
	if output, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap"); code != 0 {
		t.Fatalf("bootstrap exit=%d want=0 output=%s", code, output)
	}

	token = testutil.AdminToken(t, baseURL)
	orgBody, orgStatus := testutil.GET(t, baseURL, "/v1/orgs/current", token)
	if orgStatus != http.StatusOK {
		t.Fatalf("GET /v1/orgs/current status=%d want=%d body=%s", orgStatus, http.StatusOK, string(orgBody))
	}
	orgID = strings.TrimSpace(asString(testutil.JSONPath(t, orgBody, "data", "id")))
	if orgID == "" {
		t.Fatalf("missing organization id body=%s", string(orgBody))
	}

	frankBody, frankStatus := testutil.GET(t, baseURL, "/v1/agents?name=Frank", token)
	if frankStatus != http.StatusOK {
		t.Fatalf("GET /v1/agents?name=Frank status=%d want=%d body=%s", frankStatus, http.StatusOK, string(frankBody))
	}
	frankAgents := asArray(t, testutil.JSONPath(t, frankBody, "data"), "frank agents")
	if len(frankAgents) < 1 {
		t.Fatalf("expected Frank agent body=%s", string(frankBody))
	}
	frankID = strings.TrimSpace(asString(asObject(t, frankAgents[0], "frank")["id"]))
	if frankID == "" {
		t.Fatalf("missing Frank id body=%s", string(frankBody))
	}
	return token, orgID, frankID
}

func createControlPlaneProject(t *testing.T, baseURL, token, prefix string) string {
	t.Helper()
	suffix := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(prefix)), "_", "-")
	slug := fmt.Sprintf("%s-%d", suffix, time.Now().UnixNano())
	body, status := testutil.POST(t, baseURL, "/v1/projects", token, map[string]any{
		"slug":         slug,
		"display_name": "Control Plane Test " + prefix,
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/projects status=%d want=%d body=%s", status, http.StatusCreated, string(body))
	}
	projectID := strings.TrimSpace(asString(testutil.JSONPath(t, body, "data", "id")))
	if projectID == "" {
		t.Fatalf("missing project id body=%s", string(body))
	}
	return projectID
}

func createProjectTask(t *testing.T, baseURL, token, projectID, title, description, assignedAgentID string) string {
	t.Helper()
	payload := map[string]any{
		"title":       title,
		"description": description,
	}
	if strings.TrimSpace(assignedAgentID) != "" {
		payload["assigned_agent_id"] = assignedAgentID
	}
	body, status := testutil.POST(t, baseURL, "/v1/projects/"+projectID+"/tasks", token, payload)
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/projects/%s/tasks status=%d want=%d body=%s", projectID, status, http.StatusCreated, string(body))
	}
	taskID := strings.TrimSpace(asString(testutil.JSONPath(t, body, "data", "id")))
	if taskID == "" {
		t.Fatalf("missing task id body=%s", string(body))
	}
	return taskID
}

func createControlRun(t *testing.T, baseURL, token, taskID string) string {
	t.Helper()
	body, status := testutil.POST(t, baseURL, "/v1/control/runs", token, map[string]any{
		"task_id":      taskID,
		"trigger_type": "agent_tool",
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /v1/control/runs status=%d want=%d|%d body=%s", status, http.StatusCreated, http.StatusOK, string(body))
	}
	runID := strings.TrimSpace(asString(testutil.JSONPath(t, body, "data", "id")))
	if runID == "" {
		t.Fatalf("missing run id body=%s", string(body))
	}
	return runID
}

func containsAnyRunEventType(events []any, candidates ...string) bool {
	if len(events) == 0 {
		return false
	}
	want := map[string]struct{}{}
	for _, item := range candidates {
		trimmed := strings.ToLower(strings.TrimSpace(item))
		if trimmed != "" {
			want[trimmed] = struct{}{}
		}
	}
	for _, raw := range events {
		event, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		eventType := strings.ToLower(strings.TrimSpace(asString(event["event_type"])))
		if _, ok := want[eventType]; ok {
			return true
		}
	}
	return false
}
