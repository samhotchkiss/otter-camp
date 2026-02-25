//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/e2e/testutil"
)

func TestProjectTaskFlow_ThreeNodeFlow(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	testutil.ResetState(t, baseURL)
	if out, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap"); code != 0 {
		t.Fatalf("bootstrap exit=%d want=0 output=%s", code, out)
	}
	token := testutil.AdminToken(t, baseURL)

	projectBody, projectStatus := testutil.POST(t, baseURL, "/v1/projects", token, map[string]any{
		"name":          "Flow Test Project",
		"slug":          "flow-test",
		"delivery_mode": "gated",
	})
	if projectStatus != http.StatusCreated {
		t.Fatalf("POST /v1/projects status=%d want=%d body=%s", projectStatus, http.StatusCreated, string(projectBody))
	}
	projectID := asString(testutil.JSONPath(t, projectBody, "data", "id"))
	if asString(testutil.JSONPath(t, projectBody, "data", "slug")) != "flow-test" {
		t.Fatalf("project slug mismatch body=%s", string(projectBody))
	}

	pmAgentsBody, pmAgentsStatus := testutil.GET(t, baseURL, "/v1/agents?role=pm", token)
	if pmAgentsStatus != http.StatusOK {
		t.Fatalf("GET /v1/agents?role=pm status=%d want=%d body=%s", pmAgentsStatus, http.StatusOK, string(pmAgentsBody))
	}
	pmAgents := asArray(t, testutil.JSONPath(t, pmAgentsBody, "data"), "pm agents")
	if len(pmAgents) < 1 {
		t.Fatalf("expected at least one PM agent body=%s", string(pmAgentsBody))
	}
	pmAgentID := asString(asObject(t, pmAgents[0], "pm agent")["id"])
	if pmAgentID == "" {
		t.Fatalf("pm agent id empty body=%s", string(pmAgentsBody))
	}
	assignPMBody, assignPMStatus := testutil.POST(t, baseURL, "/v1/agents/"+pmAgentID+"/project-assignments", token, map[string]any{
		"project_id": projectID,
		"role":       "pm",
	})
	if assignPMStatus != http.StatusOK {
		t.Fatalf("POST /v1/agents/%s/project-assignments status=%d want=%d body=%s", pmAgentID, assignPMStatus, http.StatusOK, string(assignPMBody))
	}

	remoteBody, remoteStatus := testutil.POST(t, baseURL, "/v1/projects/"+projectID+"/remotes", token, map[string]any{
		"name":       "origin",
		"url":        "https://example.com/repo.git",
		"transport":  "https",
		"is_default": true,
	})
	if remoteStatus != http.StatusCreated {
		t.Fatalf("POST /v1/projects/%s/remotes status=%d want=%d body=%s", projectID, remoteStatus, http.StatusCreated, string(remoteBody))
	}
	remoteID := asString(testutil.JSONPath(t, remoteBody, "data", "id"))

	environmentBody, environmentStatus := testutil.POST(t, baseURL, "/v1/projects/"+projectID+"/environments", token, map[string]any{
		"name":          "production",
		"delivery_mode": "gated",
		"remote_id":     remoteID,
	})
	if environmentStatus != http.StatusCreated {
		t.Fatalf("POST /v1/projects/%s/environments status=%d want=%d body=%s", projectID, environmentStatus, http.StatusCreated, string(environmentBody))
	}
	environmentID := asString(testutil.JSONPath(t, environmentBody, "data", "id"))

	templateBody, templateStatus := testutil.POST(t, baseURL, "/v1/projects/"+projectID+"/flow-templates", token, map[string]any{
		"slug": "three-node-flow",
		"name": "Three Node Flow",
	})
	if templateStatus != http.StatusCreated {
		t.Fatalf("POST /v1/projects/%s/flow-templates status=%d want=%d body=%s", projectID, templateStatus, http.StatusCreated, string(templateBody))
	}
	templateID := asString(testutil.JSONPath(t, templateBody, "data", "id"))

	node1 := createFlowNode(t, baseURL, token, templateID, "Implementation", "agent_work", 1, false)
	node2 := createFlowNode(t, baseURL, token, templateID, "Human Review", "human_review", 2, true)
	node3 := createFlowNode(t, baseURL, token, templateID, "Finalization", "agent_work", 3, false)

	updateNode(t, baseURL, token, templateID, node1, map[string]any{"next_node_id": node2})
	updateNode(t, baseURL, token, templateID, node2, map[string]any{"next_node_id": node3, "requires_human_review": true})
	updateTemplate(t, baseURL, token, templateID, map[string]any{"start_node_id": node1})

	taskBody, taskStatus := testutil.POST(t, baseURL, "/v1/projects/"+projectID+"/tasks", token, map[string]any{
		"title":            "Build the feature",
		"flow_template_id": templateID,
		"description":      "Implement and review the feature",
	})
	if taskStatus != http.StatusCreated {
		t.Fatalf("POST /v1/projects/%s/tasks status=%d want=%d body=%s", projectID, taskStatus, http.StatusCreated, string(taskBody))
	}
	taskID := asString(testutil.JSONPath(t, taskBody, "data", "id"))
	workStatus := asString(testutil.JSONPath(t, taskBody, "data", "work_status"))
	if workStatus != "queued" && workStatus != "in_progress" {
		t.Fatalf("task work_status=%q want queued|in_progress body=%s", workStatus, string(taskBody))
	}
	if asString(testutil.JSONPath(t, taskBody, "data", "current_flow_node_id")) != node1 {
		t.Fatalf("task current_flow_node_id mismatch body=%s", string(taskBody))
	}

	advance1Body, advance1Status := testutil.POST(t, baseURL, "/v1/tasks/"+taskID+"/advance-flow", token, map[string]any{
		"decision":   "complete",
		"commit_sha": "abc1234def5678",
	})
	if advance1Status != http.StatusOK {
		t.Fatalf("POST /v1/tasks/%s/advance-flow node1 status=%d want=%d body=%s", taskID, advance1Status, http.StatusOK, string(advance1Body))
	}
	if asString(testutil.JSONPath(t, advance1Body, "data", "flow_node_id")) != node2 {
		t.Fatalf("advance node1 target node mismatch body=%s", string(advance1Body))
	}

	inboxItem := testutil.WaitForInboxItem(t, baseURL, token, map[string]string{
		"item_type":      "task_review",
		"source_task_id": taskID,
	}, 30*time.Second)
	inboxItemID := asString(inboxItem["id"])
	if inboxItemID == "" {
		t.Fatal("review inbox item id is empty")
	}

	actBody, actStatus := testutil.POST(t, baseURL, "/v1/inbox/"+inboxItemID+"/act", token, map[string]any{
		"action": "approve",
	})
	if actStatus != http.StatusOK {
		t.Fatalf("POST /v1/inbox/%s/act status=%d want=%d body=%s", inboxItemID, actStatus, http.StatusOK, string(actBody))
	}
	if asString(testutil.JSONPath(t, actBody, "data", "status")) != "resolved" {
		t.Fatalf("inbox action status mismatch body=%s", string(actBody))
	}

	taskAfterReviewBody, taskAfterReviewStatus := testutil.GET(t, baseURL, "/v1/tasks/"+taskID, token)
	if taskAfterReviewStatus != http.StatusOK {
		t.Fatalf("GET /v1/tasks/%s status=%d want=%d body=%s", taskID, taskAfterReviewStatus, http.StatusOK, string(taskAfterReviewBody))
	}
	if asString(testutil.JSONPath(t, taskAfterReviewBody, "data", "current_flow_node_id")) != node3 {
		t.Fatalf("task current_flow_node_id after review mismatch body=%s", string(taskAfterReviewBody))
	}

	advance3Body, advance3Status := testutil.POST(t, baseURL, "/v1/tasks/"+taskID+"/advance-flow", token, map[string]any{
		"decision":   "complete",
		"commit_sha": "def5678ghi9012",
	})
	if advance3Status != http.StatusOK {
		t.Fatalf("POST /v1/tasks/%s/advance-flow node3 status=%d want=%d body=%s", taskID, advance3Status, http.StatusOK, string(advance3Body))
	}

	finalTask := testutil.WaitForTaskStatus(t, baseURL, token, taskID, "done", 30*time.Second)
	if asString(finalTask["current_flow_node_id"]) != "" {
		t.Fatalf("expected null current_flow_node_id after completion task=%v", finalTask)
	}

	mergeEntry := testutil.WaitForMergeQueueEntry(t, baseURL, token, projectID, taskID, 30*time.Second)
	mergeEntryID := asString(mergeEntry["id"])
	if mergeEntryID == "" {
		t.Fatal("merge queue entry id is empty")
	}
	if asString(mergeEntry["task_id"]) != taskID {
		t.Fatalf("merge queue task_id mismatch entry=%v", mergeEntry)
	}
	if asString(mergeEntry["archived_at"]) != "" {
		t.Fatalf("merge queue archived_at=%v want nil before deploy", mergeEntry["archived_at"])
	}

	preDeployEnvsBody, preDeployEnvsStatus := testutil.GET(t, baseURL, "/v1/projects/"+projectID+"/environments", token)
	if preDeployEnvsStatus != http.StatusOK {
		t.Fatalf("GET /v1/projects/%s/environments before deploy status=%d want=%d body=%s", projectID, preDeployEnvsStatus, http.StatusOK, string(preDeployEnvsBody))
	}

	deployBody, deployStatus := testutil.POST(t, baseURL, "/v1/projects/"+projectID+"/environments/"+environmentID+"/deploy", token, map[string]any{
		"commit_sha": "def5678ghi9012",
	})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /v1/projects/%s/environments/%s/deploy status=%d want=%d body=%s", projectID, environmentID, deployStatus, http.StatusOK, string(deployBody))
	}
	deployTaskID := asString(testutil.JSONPath(t, deployBody, "data", "id"))
	if deployTaskID == "" {
		t.Fatalf("missing deploy task id body=%s", string(deployBody))
	}

	testutil.WaitForTaskStatus(t, baseURL, token, deployTaskID, "done", 60*time.Second)

	mergeQueueBody, mergeQueueStatus := testutil.GET(t, baseURL, "/v1/projects/"+projectID+"/merge-queue", token)
	if mergeQueueStatus != http.StatusOK {
		t.Fatalf("GET /v1/projects/%s/merge-queue status=%d want=%d body=%s", projectID, mergeQueueStatus, http.StatusOK, string(mergeQueueBody))
	}
	mergeItems := asArray(t, testutil.JSONPath(t, mergeQueueBody, "data"), "merge queue list")
	archivedSeen := false
	for _, itemValue := range mergeItems {
		item := asObject(t, itemValue, "merge queue item")
		if asString(item["id"]) != mergeEntryID {
			continue
		}
		if asString(item["archived_at"]) == "" {
			t.Fatalf("merge queue archived_at missing after deploy item=%v", item)
		}
		archivedSeen = true
	}
	if !archivedSeen {
		t.Fatalf("merge queue entry %s not found after deploy body=%s", mergeEntryID, string(mergeQueueBody))
	}

	environmentsBody, environmentsStatus := testutil.GET(t, baseURL, "/v1/projects/"+projectID+"/environments", token)
	if environmentsStatus != http.StatusOK {
		t.Fatalf("GET /v1/projects/%s/environments status=%d want=%d body=%s", projectID, environmentsStatus, http.StatusOK, string(environmentsBody))
	}
	environments := asArray(t, testutil.JSONPath(t, environmentsBody, "data"), "environments")
	if len(environments) < 1 {
		t.Fatalf("expected at least one environment body=%s", string(environmentsBody))
	}
	var targetEnvironment map[string]any
	for _, itemValue := range environments {
		item := asObject(t, itemValue, "environment item")
		if asString(item["id"]) == environmentID {
			targetEnvironment = item
			break
		}
	}
	if targetEnvironment == nil {
		t.Fatalf("environment %s not found body=%s", environmentID, string(environmentsBody))
	}
	if asString(targetEnvironment["deployed_commit_sha"]) != "def5678ghi9012" {
		t.Fatalf("deployed_commit_sha=%q want=%q item=%v", asString(targetEnvironment["deployed_commit_sha"]), "def5678ghi9012", targetEnvironment)
	}
}

func TestProjectTaskFlow_FlowNodeRejectionLoop(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	testutil.ResetState(t, baseURL)
	if out, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap"); code != 0 {
		t.Fatalf("bootstrap exit=%d want=0 output=%s", code, out)
	}
	token := testutil.AdminToken(t, baseURL)

	projectBody, projectStatus := testutil.POST(t, baseURL, "/v1/projects", token, map[string]any{
		"name":          "Reject Loop Project",
		"slug":          "reject-loop",
		"delivery_mode": "gated",
	})
	if projectStatus != http.StatusCreated {
		t.Fatalf("POST /v1/projects status=%d want=%d body=%s", projectStatus, http.StatusCreated, string(projectBody))
	}
	projectID := asString(testutil.JSONPath(t, projectBody, "data", "id"))

	templateBody, templateStatus := testutil.POST(t, baseURL, "/v1/projects/"+projectID+"/flow-templates", token, map[string]any{
		"slug": "reject-loop-flow",
		"name": "Reject Loop Flow",
	})
	if templateStatus != http.StatusCreated {
		t.Fatalf("POST /v1/projects/%s/flow-templates status=%d want=%d body=%s", projectID, templateStatus, http.StatusCreated, string(templateBody))
	}
	templateID := asString(testutil.JSONPath(t, templateBody, "data", "id"))

	node1 := createFlowNode(t, baseURL, token, templateID, "Review", "human_review", 1, false)
	node2 := createFlowNode(t, baseURL, token, templateID, "Done", "agent_work", 2, false)
	updateNode(t, baseURL, token, templateID, node1, map[string]any{
		"next_node_id":   node2,
		"reject_node_id": node1,
		"max_visits":     5,
	})
	updateTemplate(t, baseURL, token, templateID, map[string]any{"start_node_id": node1})

	taskBody, taskStatus := testutil.POST(t, baseURL, "/v1/projects/"+projectID+"/tasks", token, map[string]any{
		"title":            "Reject flow task",
		"flow_template_id": templateID,
	})
	if taskStatus != http.StatusCreated {
		t.Fatalf("POST /v1/projects/%s/tasks status=%d want=%d body=%s", projectID, taskStatus, http.StatusCreated, string(taskBody))
	}
	taskID := asString(testutil.JSONPath(t, taskBody, "data", "id"))
	if asString(testutil.JSONPath(t, taskBody, "data", "current_flow_node_id")) != node1 {
		t.Fatalf("task should start on node1 body=%s", string(taskBody))
	}

	rejectBody, rejectStatus := testutil.POST(t, baseURL, "/v1/tasks/"+taskID+"/advance-flow", token, map[string]any{
		"decision": "reject",
		"reason":   "Needs rework",
	})
	if rejectStatus != http.StatusOK {
		t.Fatalf("POST /v1/tasks/%s/advance-flow reject status=%d want=%d body=%s", taskID, rejectStatus, http.StatusOK, string(rejectBody))
	}
	if asString(testutil.JSONPath(t, rejectBody, "data", "flow_node_id")) != node1 {
		t.Fatalf("rejected flow should stay on node1 body=%s", string(rejectBody))
	}

	flowBody, flowStatus := testutil.GET(t, baseURL, "/v1/tasks/"+taskID+"/flow", token)
	if flowStatus != http.StatusOK {
		t.Fatalf("GET /v1/tasks/%s/flow status=%d want=%d body=%s", taskID, flowStatus, http.StatusOK, string(flowBody))
	}
	if got := asNumber(testutil.JSONPath(t, flowBody, "data", "current_execution", "visit_count")); got != 2 {
		t.Fatalf("current_execution.visit_count=%d want=2 body=%s", got, string(flowBody))
	}
	if asString(testutil.JSONPath(t, flowBody, "data", "current_execution", "flow_node_id")) != node1 {
		t.Fatalf("current execution node mismatch body=%s", string(flowBody))
	}
}

func createFlowNode(t *testing.T, baseURL, token, templateID, name, nodeType string, position int, requiresHumanReview bool) string {
	t.Helper()
	body, status := testutil.POST(t, baseURL, "/v1/flow-templates/"+templateID+"/nodes", token, map[string]any{
		"name":                  name,
		"node_type":             nodeType,
		"position":              position,
		"requires_human_review": requiresHumanReview,
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/flow-templates/%s/nodes status=%d want=%d body=%s", templateID, status, http.StatusCreated, string(body))
	}
	return asString(testutil.JSONPath(t, body, "data", "id"))
}

func updateNode(t *testing.T, baseURL, token, templateID, nodeID string, payload map[string]any) {
	t.Helper()
	body, status := patch(t, baseURL, "/v1/flow-templates/"+templateID+"/nodes/"+nodeID, token, payload)
	if status != http.StatusOK {
		t.Fatalf("PATCH /v1/flow-templates/%s/nodes/%s status=%d want=%d body=%s", templateID, nodeID, status, http.StatusOK, string(body))
	}
}

func updateTemplate(t *testing.T, baseURL, token, templateID string, payload map[string]any) {
	t.Helper()
	body, status := patch(t, baseURL, "/v1/flow-templates/"+templateID, token, payload)
	if status != http.StatusOK {
		t.Fatalf("PATCH /v1/flow-templates/%s status=%d want=%d body=%s", templateID, status, http.StatusOK, string(body))
	}
}

func patch(t *testing.T, baseURL, path, token string, payload map[string]any) ([]byte, int) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal PATCH %s: %v", path, err)
	}
	req, err := http.NewRequest(http.MethodPatch, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("new PATCH %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("PATCH %s failed: %v", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read PATCH %s body: %v", path, err)
	}
	return body, resp.StatusCode
}

func asNumber(value any) int {
	number, _ := value.(float64)
	return int(number)
}
