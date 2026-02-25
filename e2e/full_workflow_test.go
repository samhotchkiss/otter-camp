//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/e2e/testutil"
)

func TestFullWorkflow_EndToEnd(t *testing.T) {
	startedAt := time.Now()

	server, baseURL := testutil.StartServerWithWorker(t)
	defer server.Stop(t)

	testutil.ResetState(t, baseURL)
	if out, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap"); code != 0 {
		t.Fatalf("bootstrap exit=%d want=0 output=%s", code, out)
	}
	token := testutil.AdminToken(t, baseURL)

	orgBody, orgStatus := testutil.GET(t, baseURL, "/v1/orgs/current", token)
	if orgStatus != http.StatusOK {
		t.Fatalf("GET /v1/orgs/current status=%d want=%d body=%s", orgStatus, http.StatusOK, string(orgBody))
	}
	orgID := strings.TrimSpace(asString(testutil.JSONPath(t, orgBody, "data", "id")))
	if orgID == "" {
		t.Fatalf("organization id missing body=%s", string(orgBody))
	}

	frankBody, frankStatus := testutil.GET(t, baseURL, "/v1/agents?name=Frank", token)
	if frankStatus != http.StatusOK {
		t.Fatalf("GET /v1/agents?name=Frank status=%d want=%d body=%s", frankStatus, http.StatusOK, string(frankBody))
	}
	frankAgents := asArray(t, testutil.JSONPath(t, frankBody, "data"), "Frank agents")
	if len(frankAgents) < 1 {
		t.Fatalf("expected Frank agent body=%s", string(frankBody))
	}
	frank := asObject(t, frankAgents[0], "Frank agent")
	if got := strings.ToLower(strings.TrimSpace(asString(frank["lifecycle_status"]))); got != "active" {
		t.Fatalf("Frank lifecycle_status=%q want=active body=%s", got, string(frankBody))
	}

	pmBody, pmStatus := testutil.GET(t, baseURL, "/v1/agents?role=pm", token)
	if pmStatus != http.StatusOK {
		t.Fatalf("GET /v1/agents?role=pm status=%d want=%d body=%s", pmStatus, http.StatusOK, string(pmBody))
	}
	pmAgents := asArray(t, testutil.JSONPath(t, pmBody, "data"), "PM agents")
	if len(pmAgents) < 1 {
		t.Fatalf("expected PM agent body=%s", string(pmBody))
	}
	pmAgentID := strings.TrimSpace(asString(asObject(t, pmAgents[0], "PM agent")["id"]))
	if pmAgentID == "" {
		t.Fatalf("pm agent id missing body=%s", string(pmBody))
	}
	if got := strings.ToLower(strings.TrimSpace(asString(asObject(t, pmAgents[0], "PM agent")["lifecycle_status"]))); got != "active" {
		t.Fatalf("PM lifecycle_status=%q want=active body=%s", got, string(pmBody))
	}

	projectSlug := "smoke-test-" + strings.ToLower(strings.ReplaceAll(time.Now().UTC().Format("150405"), ":", ""))
	projectBody, projectStatus := testutil.POST(t, baseURL, "/v1/projects", token, map[string]any{
		"display_name":  "Smoke Test Project",
		"slug":          projectSlug,
		"delivery_mode": "gated",
	})
	if projectStatus != http.StatusCreated {
		t.Fatalf("POST /v1/projects status=%d want=%d body=%s", projectStatus, http.StatusCreated, string(projectBody))
	}
	projectID := strings.TrimSpace(asString(testutil.JSONPath(t, projectBody, "data", "id")))
	if projectID == "" {
		t.Fatalf("project id missing body=%s", string(projectBody))
	}

	templateSlug := "basic-review-flow-" + strings.ToLower(strings.ReplaceAll(time.Now().UTC().Format("150405"), ":", ""))
	templateBody, templateStatus := testutil.POST(t, baseURL, "/v1/projects/"+projectID+"/flow-templates", token, map[string]any{
		"display_name": "Basic Review Flow",
		"slug":         templateSlug,
	})
	if templateStatus != http.StatusCreated {
		t.Fatalf("POST /v1/projects/%s/flow-templates status=%d want=%d body=%s", projectID, templateStatus, http.StatusCreated, string(templateBody))
	}
	templateID := strings.TrimSpace(asString(testutil.JSONPath(t, templateBody, "data", "id")))
	if templateID == "" {
		t.Fatalf("template id missing body=%s", string(templateBody))
	}

	var node1ID string
	var node2ID string
	existingNodesBody, existingNodesStatus := testutil.GET(t, baseURL, "/v1/flow-templates/"+templateID+"/nodes", token)
	if existingNodesStatus != http.StatusOK {
		t.Fatalf("GET /v1/flow-templates/%s/nodes status=%d want=%d body=%s", templateID, existingNodesStatus, http.StatusOK, string(existingNodesBody))
	}
	existingNodes := asArray(t, testutil.JSONPath(t, existingNodesBody, "data"), "existing flow nodes")
	if len(existingNodes) < 2 {
		node1ID = createFlowNode(t, baseURL, token, templateID, "Implementation", "work", 10, false)
		node2ID = createFlowNode(t, baseURL, token, templateID, "Review", "review", 20, true)
	} else {
		node1ID, node2ID = selectTwoFlowNodes(existingNodes)
	}
	if node1ID == "" || node2ID == "" || node1ID == node2ID {
		t.Fatalf("failed to resolve two distinct flow nodes template=%s nodes=%s", templateID, string(existingNodesBody))
	}

	nodePatchBody, nodePatchStatus := patchJSON(t, baseURL, "/v1/flow-templates/"+templateID+"/nodes/"+node1ID, token, map[string]any{
		"next_node_id": node2ID,
	})
	if nodePatchStatus != http.StatusOK {
		t.Fatalf("PATCH node status=%d want=%d body=%s", nodePatchStatus, http.StatusOK, string(nodePatchBody))
	}

	templatePatchBody, templatePatchStatus := patchJSON(t, baseURL, "/v1/flow-templates/"+templateID, token, map[string]any{
		"start_node_id": node1ID,
	})
	if templatePatchStatus != http.StatusOK {
		t.Fatalf("PATCH template status=%d want=%d body=%s", templatePatchStatus, http.StatusOK, string(templatePatchBody))
	}

	assignBody, assignStatus := testutil.POST(t, baseURL, "/v1/agents/"+pmAgentID+"/project-assignments", token, map[string]any{
		"project_id": projectID,
		"role":       "pm",
	})
	if assignStatus != http.StatusOK && assignStatus != http.StatusCreated {
		t.Fatalf("POST /v1/agents/%s/project-assignments status=%d want=%d|%d body=%s", pmAgentID, assignStatus, http.StatusOK, http.StatusCreated, string(assignBody))
	}

	sessionBody, sessionStatus := testutil.POST(t, baseURL, "/v1/chat-sessions", token, map[string]any{
		"scope_type": "project",
		"scope_id":   projectID,
		"mode":       "sync",
	})
	if sessionStatus != http.StatusCreated {
		t.Fatalf("POST /v1/chat-sessions status=%d want=%d body=%s", sessionStatus, http.StatusCreated, string(sessionBody))
	}
	sessionID := strings.TrimSpace(asString(testutil.JSONPath(t, sessionBody, "data", "id")))
	if sessionID == "" {
		t.Fatalf("session id missing body=%s", string(sessionBody))
	}

	participantBody, participantStatus := testutil.POST(t, baseURL, "/v1/chat-sessions/"+sessionID+"/participants", token, map[string]any{
		"participant_type": "agent",
		"participant_id":   pmAgentID,
		"role":             "member",
	})
	if participantStatus != http.StatusCreated {
		t.Fatalf("POST /v1/chat-sessions/%s/participants status=%d want=%d body=%s", sessionID, participantStatus, http.StatusCreated, string(participantBody))
	}

	sseEvents, closeSSE := testutil.SSEClient(t, baseURL, "/v1/events/stream?scopes=session:"+sessionID, token)
	defer closeSSE()
	_ = testutil.WaitForSSEEvent(t, sseEvents, "connected", 10*time.Second)

	messageBody, messageStatus := testutil.POST(t, baseURL, "/v1/chat-sessions/"+sessionID+"/messages", token, map[string]any{
		"content": "[create-task] Please create a task to implement the login feature using the Basic Review Flow template.",
	})
	if messageStatus != http.StatusAccepted && messageStatus != http.StatusCreated {
		t.Fatalf("POST /v1/chat-sessions/%s/messages status=%d want=%d|%d body=%s", sessionID, messageStatus, http.StatusAccepted, http.StatusCreated, string(messageBody))
	}
	if !waitForTurnCompletionEvent(sseEvents, 60*time.Second) {
		t.Fatalf("missing turn completion event after create-task prompt")
	}

	taskID, taskTitle, taskWorkStatus, taskFound := waitForTaskForTemplate(t, baseURL, token, projectID, templateID, 60*time.Second)
	if !taskFound {
		t.Fatalf("agent did not create task for template=%s in project=%s after create-task message", templateID, projectID)
	}
	if !strings.Contains(strings.ToLower(taskTitle), "login") {
		t.Logf("created task title does not include login: %q", taskTitle)
	}
	if taskWorkStatus == "draft" {
		queueBody, queueStatus := testutil.POST(t, baseURL, "/v1/tasks/"+taskID+"/queue", token, map[string]any{})
		if queueStatus != http.StatusOK {
			t.Fatalf("POST /v1/tasks/%s/queue status=%d want=%d body=%s", taskID, queueStatus, http.StatusOK, string(queueBody))
		}
		taskWorkStatus = strings.ToLower(strings.TrimSpace(asString(testutil.JSONPath(t, queueBody, "data", "work_status"))))
	}
	if taskWorkStatus != "queued" && taskWorkStatus != "in_progress" {
		t.Fatalf("task work_status=%q want=queued|in_progress", taskWorkStatus)
	}

	runRecord, runFound := findRunForSession(t, baseURL, token, sessionID, 60*time.Second)
	if !runFound {
		t.Fatalf("no completed control run found for session_id=%s", sessionID)
	}
	runID := strings.TrimSpace(asString(runRecord["id"]))
	if runID == "" {
		t.Fatalf("run id missing from run record=%v", runRecord)
	}
	_ = testutil.WaitForRunStatus(t, baseURL, token, runID, "completed", 30*time.Second)

	currentTask, inProgress := waitForTaskInProgress(t, baseURL, token, taskID, 90*time.Second)
	if !inProgress {
		t.Fatalf("task never reached in_progress: flow cannot be tested")
	}
	if got := strings.TrimSpace(asString(currentTask["current_flow_node_id"])); got != node1ID {
		t.Fatalf("task current_flow_node_id=%q want=%q task=%v", got, node1ID, currentTask)
	}

	advanceBody, advanceStatus := testutil.POST(t, baseURL, "/v1/tasks/"+taskID+"/advance-flow", token, map[string]any{
		"decision":   "complete",
		"commit_sha": "smoketest-commit-abc123",
	})
	if advanceStatus != http.StatusOK {
		t.Fatalf("POST /v1/tasks/%s/advance-flow status=%d want=%d body=%s", taskID, advanceStatus, http.StatusOK, string(advanceBody))
	}
	advanceNode := strings.TrimSpace(asString(testutil.JSONPath(t, advanceBody, "data", "flow_node_id")))
	if advanceNode == "" {
		advanceNode = strings.TrimSpace(asString(testutil.JSONPath(t, advanceBody, "data", "current_flow_node_id")))
	}
	if advanceNode != node2ID {
		t.Fatalf("advance target node=%q want=%q body=%s", advanceNode, node2ID, string(advanceBody))
	}

	inboxItem := testutil.WaitForInboxItem(t, baseURL, token, map[string]string{
		"item_type":      "task_review",
		"source_task_id": taskID,
	}, 30*time.Second)
	inboxItemID := strings.TrimSpace(asString(inboxItem["id"]))
	if inboxItemID == "" {
		t.Fatalf("inbox item id missing item=%v", inboxItem)
	}

	actBody, actStatus := testutil.POST(t, baseURL, "/v1/inbox/"+inboxItemID+"/act", token, map[string]any{
		"action": "approve",
	})
	if actStatus != http.StatusOK {
		t.Fatalf("POST /v1/inbox/%s/act status=%d want=%d body=%s", inboxItemID, actStatus, http.StatusOK, string(actBody))
	}
	if got := strings.TrimSpace(asString(testutil.JSONPath(t, actBody, "data", "status"))); got != "resolved" {
		t.Fatalf("inbox status=%q want=resolved body=%s", got, string(actBody))
	}

	doneTask := testutil.WaitForTaskStatus(t, baseURL, token, taskID, "done", 30*time.Second)
	if got := strings.TrimSpace(asString(doneTask["work_status"])); got != "done" {
		t.Fatalf("task work_status=%q want=done task=%v", got, doneTask)
	}

	testutil.TriggerExtractionJob(t, baseURL, token, sessionID)

	memory := testutil.WaitForMemory(t, baseURL, token, testutil.MemoryFilter{
		ScopeType:    "project",
		ProjectID:    projectID,
		ContainsText: "login",
	}, 30*time.Second)
	if got := strings.ToLower(strings.TrimSpace(asString(memory["status"]))); got != "" && got != "active" {
		t.Fatalf("memory status=%q want=active memory=%v", got, memory)
	}
	if isActive, ok := memory["is_active"].(bool); ok && !isActive {
		t.Fatalf("memory is_active=%v want=true memory=%v", isActive, memory)
	}

	followUpBody, followUpStatus := testutil.POST(t, baseURL, "/v1/chat-sessions/"+sessionID+"/messages", token, map[string]any{
		"content": "What tasks have we discussed so far?",
	})
	if followUpStatus != http.StatusAccepted && followUpStatus != http.StatusCreated {
		t.Fatalf("follow-up POST /v1/chat-sessions/%s/messages status=%d want=%d|%d body=%s", sessionID, followUpStatus, http.StatusAccepted, http.StatusCreated, string(followUpBody))
	}
	if !waitForTurnCompletionEvent(sseEvents, 60*time.Second) {
		t.Fatalf("missing turn completion event for follow-up memory-retrieval prompt")
	}

	invocationsPath := "/v1/model/invocations?session_id=" + url.QueryEscape(sessionID)
	invocationsBody, invocationsStatus := testutil.GET(t, baseURL, invocationsPath, token)
	switch invocationsStatus {
	case http.StatusOK:
		invocations := asArray(t, testutil.JSONPath(t, invocationsBody, "data"), "model invocations")
		if len(invocations) < 1 {
			t.Fatalf("expected at least one model invocation for session_id=%s body=%s", sessionID, string(invocationsBody))
		}
		first := asObject(t, invocations[0], "model invocation")
		metadata, _ := first["metadata"].(map[string]any)
		if len(metadata) == 0 {
			t.Errorf("model invocation metadata missing; cannot verify memory injection body=%s", string(invocationsBody))
		} else {
			memoryTokens := readNestedNumber(metadata, "layer_token_counts", "memory_injection")
			if memoryTokens <= 0 {
				memoryTokens = readNestedNumber(metadata, "memory_layer_tokens")
			}
			memoryInjected, _ := metadata["memory_injected"].(bool)
			if memoryTokens <= 0 && !memoryInjected {
				t.Errorf("memory injection assertion failed: expected memory tokens > 0 or memory_injected=true metadata=%v", metadata)
			}
		}
	case http.StatusNotFound:
		t.Errorf("model invocations endpoint unavailable; cannot verify memory injection path=%s", invocationsPath)
	default:
		t.Fatalf("GET %s status=%d want=%d|%d body=%s", invocationsPath, invocationsStatus, http.StatusOK, http.StatusNotFound, string(invocationsBody))
	}

	auditBody, auditStatus := testutil.GET(t, baseURL, "/v1/audit?limit=200", token)
	if auditStatus != http.StatusOK {
		t.Fatalf("GET /v1/audit status=%d want=%d body=%s", auditStatus, http.StatusOK, string(auditBody))
	}
	auditEvents := asArray(t, testutil.JSONPath(t, auditBody, "data"), "audit events")
	if len(auditEvents) < 1 {
		t.Fatalf("expected audit events body=%s", string(auditBody))
	}
	if !auditContains(auditEvents, "bootstrap_complete") {
		t.Fatalf("audit missing bootstrap_complete body=%s", string(auditBody))
	}
	if !auditContainsAny(auditEvents, "session", "session_created") {
		t.Fatalf("audit missing session event body=%s", string(auditBody))
	}
	if !auditContainsAny(auditEvents, "message", "message_sent") {
		t.Fatalf("audit missing message event body=%s", string(auditBody))
	}
	if !auditContainsAny(auditEvents, "task", "task_created") {
		t.Fatalf("audit missing task event body=%s", string(auditBody))
	}
	if !auditContainsAny(auditEvents, "run", "run_created") {
		t.Fatalf("audit missing run event body=%s", string(auditBody))
	}

	healthBody, healthStatus := testutil.GET(t, baseURL, "/health/live", "")
	if healthStatus != http.StatusOK {
		t.Fatalf("GET /health/live status=%d want=%d body=%s", healthStatus, http.StatusOK, string(healthBody))
	}
	healthState := strings.ToLower(strings.TrimSpace(asString(testutil.JSONPath(t, healthBody, "data", "status"))))
	if healthState != "healthy" && healthState != "ok" {
		t.Fatalf("/health/live data.status=%q want=healthy|ok body=%s", healthState, string(healthBody))
	}

	if elapsed := time.Since(startedAt); elapsed > 5*time.Minute {
		t.Fatalf("full workflow scenario exceeded 5 minute budget: elapsed=%s", elapsed)
	}
}

func createFlowNode(t *testing.T, baseURL, token, templateID, displayName, nodeType string, position int, requiresHumanReview bool) string {
	t.Helper()
	body, status := testutil.POST(t, baseURL, "/v1/flow-templates/"+templateID+"/nodes", token, map[string]any{
		"display_name":          displayName,
		"node_type":             nodeType,
		"position":              position,
		"requires_human_review": requiresHumanReview,
	})
	if status == http.StatusConflict {
		nodesBody, nodesStatus := testutil.GET(t, baseURL, "/v1/flow-templates/"+templateID+"/nodes", token)
		if nodesStatus == http.StatusOK {
			nodes := asArray(t, testutil.JSONPath(t, nodesBody, "data"), "flow nodes")
			for _, raw := range nodes {
				node := asObject(t, raw, "flow node")
				gotType := strings.ToLower(strings.TrimSpace(asString(node["node_type"])))
				wantType := strings.ToLower(strings.TrimSpace(nodeType))
				gotReview, _ := node["requires_human_review"].(bool)
				if gotType == wantType && gotReview == requiresHumanReview {
					nodeID := strings.TrimSpace(asString(node["id"]))
					if nodeID != "" {
						return nodeID
					}
				}
			}
			for _, raw := range nodes {
				node := asObject(t, raw, "flow node")
				if strings.EqualFold(strings.TrimSpace(asString(node["display_name"])), strings.TrimSpace(displayName)) {
					nodeID := strings.TrimSpace(asString(node["id"]))
					if nodeID != "" {
						return nodeID
					}
				}
			}
			for _, raw := range nodes {
				node := asObject(t, raw, "flow node")
				nodeID := strings.TrimSpace(asString(node["id"]))
				if nodeID != "" {
					return nodeID
				}
			}
		}
	}
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/flow-templates/%s/nodes status=%d want=%d body=%s", templateID, status, http.StatusCreated, string(body))
	}
	nodeID := strings.TrimSpace(asString(testutil.JSONPath(t, body, "data", "id")))
	if nodeID == "" {
		t.Fatalf("flow node id missing body=%s", string(body))
	}
	return nodeID
}

func patchJSON(t *testing.T, baseURL, path, token string, payload map[string]any) ([]byte, int) {
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

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read PATCH %s body: %v", path, err)
	}
	return rawBody, resp.StatusCode
}

func findTaskForTemplate(t *testing.T, body []byte, templateID string) (taskID string, title string, workStatus string) {
	t.Helper()
	items := asArray(t, testutil.JSONPath(t, body, "data"), "project tasks")
	if len(items) == 0 {
		return "", "", ""
	}

	targetTemplate := strings.TrimSpace(templateID)
	for _, raw := range items {
		item := asObject(t, raw, "task item")
		if strings.TrimSpace(asString(item["flow_template_id"])) != targetTemplate {
			continue
		}
		return strings.TrimSpace(asString(item["id"])), asString(item["title"]), strings.ToLower(strings.TrimSpace(asString(item["work_status"])))
	}

	return "", "", ""
}

func waitForTaskForTemplate(t *testing.T, baseURL, token, projectID, templateID string, timeout time.Duration) (string, string, string, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	path := "/v1/projects/" + strings.TrimSpace(projectID) + "/tasks"
	for time.Now().Before(deadline) {
		tasksBody, tasksStatus := testutil.GET(t, baseURL, path, token)
		if tasksStatus == http.StatusOK {
			taskID, title, workStatus := findTaskForTemplate(t, tasksBody, templateID)
			if strings.TrimSpace(taskID) != "" {
				return taskID, title, workStatus, true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", "", "", false
}

func findRunForSession(t *testing.T, baseURL, token, sessionID string, timeout time.Duration) (map[string]any, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		body, status := testutil.GET(t, baseURL, "/v1/control/runs?status=completed&limit=100", token)
		if status == http.StatusOK {
			items := asArray(t, testutil.JSONPath(t, body, "data"), "control runs")
			for _, raw := range items {
				run := asObject(t, raw, "run item")
				if strings.TrimSpace(asString(run["session_id"])) != strings.TrimSpace(sessionID) {
					continue
				}
				if strings.ToLower(strings.TrimSpace(asString(run["status"]))) != "completed" {
					continue
				}
				return run, true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil, false
}

func waitForTaskInProgress(t *testing.T, baseURL, token, taskID string, timeout time.Duration) (map[string]any, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	path := "/v1/tasks/" + strings.TrimSpace(taskID)
	for time.Now().Before(deadline) {
		body, status := testutil.GET(t, baseURL, path, token)
		if status == http.StatusOK {
			task, ok := testutil.JSONPath(t, body, "data").(map[string]any)
			if ok {
				workStatus := strings.ToLower(strings.TrimSpace(asString(task["work_status"])))
				if workStatus == "in_progress" {
					return task, true
				}
			}
		}
		time.Sleep(750 * time.Millisecond)
	}
	return nil, false
}

func selectTwoFlowNodes(nodes []any) (node1ID string, node2ID string) {
	var fallbackIDs []string
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := strings.TrimSpace(asString(node["id"]))
		if id == "" {
			continue
		}
		fallbackIDs = append(fallbackIDs, id)
		nodeType := strings.ToLower(strings.TrimSpace(asString(node["node_type"])))
		if node1ID == "" && nodeType == "work" {
			node1ID = id
			continue
		}
		if node2ID == "" && nodeType == "review" {
			node2ID = id
			continue
		}
	}
	if node1ID == "" && len(fallbackIDs) > 0 {
		node1ID = fallbackIDs[0]
	}
	if node2ID == "" && len(fallbackIDs) > 1 {
		if fallbackIDs[1] != node1ID {
			node2ID = fallbackIDs[1]
		}
	}
	return node1ID, node2ID
}

func auditContains(items []any, want string) bool {
	needle := strings.ToLower(strings.TrimSpace(want))
	for _, raw := range items {
		event, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		action := strings.ToLower(strings.TrimSpace(asString(event["action"])))
		eventType := strings.ToLower(strings.TrimSpace(asString(event["event_type"])))
		if action == needle || eventType == needle {
			return true
		}
	}
	return false
}

func auditContainsAny(items []any, options ...string) bool {
	for _, raw := range items {
		event, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		action := strings.ToLower(strings.TrimSpace(asString(event["action"])))
		eventType := strings.ToLower(strings.TrimSpace(asString(event["event_type"])))
		for _, option := range options {
			needle := strings.ToLower(strings.TrimSpace(option))
			if strings.Contains(action, needle) || strings.Contains(eventType, needle) {
				return true
			}
		}
	}
	return false
}

func waitForTurnCompletionEvent(ch <-chan testutil.SSEEvent, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case evt, ok := <-ch:
			if !ok {
				return false
			}
			name := strings.TrimSpace(evt.Event)
			if name == "chat.turn.completed" || name == "turn.completed" {
				return true
			}
		case <-time.After(250 * time.Millisecond):
		}
	}
	return false
}
