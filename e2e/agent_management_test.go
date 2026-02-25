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

func TestAgent_StaffLifecycle(t *testing.T) {
	server, baseURL, token := setupAgentScenario(t)
	defer server.Stop(t)

	createBody, createStatus := testutil.POST(t, baseURL, "/v1/agents", token, map[string]any{
		"display_name":  "Ada",
		"agent_class":   "staff",
		"agent_type":    "worker",
		"system_prompt": "You are Ada, a senior software engineer.",
	})
	if createStatus != http.StatusCreated {
		t.Fatalf("POST /v1/agents status=%d want=%d body=%s", createStatus, http.StatusCreated, string(createBody))
	}

	staffAgentID := asString(testutil.JSONPath(t, createBody, "data", "id"))
	if strings.TrimSpace(staffAgentID) == "" {
		t.Fatalf("missing staff agent id body=%s", string(createBody))
	}
	if got := strings.ToLower(strings.TrimSpace(asString(testutil.JSONPath(t, createBody, "data", "lifecycle_status")))); got != "draft" {
		t.Fatalf("create lifecycle_status=%q want=draft body=%s", got, string(createBody))
	}
	if got := strings.ToLower(strings.TrimSpace(asString(testutil.JSONPath(t, createBody, "data", "agent_class")))); got != "staff" {
		t.Fatalf("create agent_class=%q want=staff body=%s", got, string(createBody))
	}

	getBody, getStatus := testutil.GET(t, baseURL, "/v1/agents/"+staffAgentID, token)
	if getStatus != http.StatusOK {
		t.Fatalf("GET /v1/agents/%s status=%d want=%d body=%s", staffAgentID, getStatus, http.StatusOK, string(getBody))
	}
	if got := strings.ToLower(strings.TrimSpace(asString(testutil.JSONPath(t, getBody, "data", "lifecycle_status")))); got != "draft" {
		t.Fatalf("draft check lifecycle_status=%q want=draft body=%s", got, string(getBody))
	}

	activateAgent(t, baseURL, token, staffAgentID)
	testutil.WaitForAgentStatus(t, baseURL, token, staffAgentID, "active", 10*time.Second)

	projectID := createProject(t, baseURL, token, "Agent Test Project")

	assignBody, assignStatus := testutil.POST(t, baseURL, "/v1/agents/"+staffAgentID+"/project-assignments", token, map[string]any{
		"project_id": projectID,
		"role":       "worker",
	})
	if assignStatus != http.StatusOK && assignStatus != http.StatusCreated {
		t.Fatalf("POST /v1/agents/%s/project-assignments status=%d want=200|201 body=%s", staffAgentID, assignStatus, string(assignBody))
	}
	if got := strings.TrimSpace(asString(testutil.JSONPath(t, assignBody, "data", "agent_id"))); got != staffAgentID {
		t.Fatalf("assignment agent_id=%q want=%q body=%s", got, staffAgentID, string(assignBody))
	}
	if got := strings.TrimSpace(asString(testutil.JSONPath(t, assignBody, "data", "project_id"))); got != projectID {
		t.Fatalf("assignment project_id=%q want=%q body=%s", got, projectID, string(assignBody))
	}
	if active, ok := testutil.JSONPath(t, assignBody, "data", "is_active").(bool); !ok || !active {
		t.Fatalf("assignment is_active=%v want=true body=%s", testutil.JSONPath(t, assignBody, "data", "is_active"), string(assignBody))
	}

	listBody, listStatus := testutil.GET(t, baseURL, "/v1/agents/"+staffAgentID+"/project-assignments", token)
	if listStatus != http.StatusOK {
		t.Fatalf("GET /v1/agents/%s/project-assignments status=%d want=%d body=%s", staffAgentID, listStatus, http.StatusOK, string(listBody))
	}
	items := asArray(t, testutil.JSONPath(t, listBody, "data"), "project assignments")
	found := false
	for _, item := range items {
		entry := asObject(t, item, "assignment entry")
		if strings.TrimSpace(asString(entry["project_id"])) != projectID {
			continue
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("project assignment for %s not found body=%s", projectID, string(listBody))
	}
}

func TestAgent_TempLifecycle_ProjectScoped(t *testing.T) {
	server, baseURL, token := setupAgentScenario(t)
	defer server.Stop(t)

	projectID := createProject(t, baseURL, token, "Temp Lifecycle Project")

	createTempBody, createTempStatus := testutil.POST(t, baseURL, "/v1/agents", token, map[string]any{
		"display_name":    "temp-worker-1",
		"agent_class":     "temp",
		"agent_type":      "worker",
		"temp_project_id": projectID,
	})
	if createTempStatus != http.StatusCreated {
		t.Fatalf("POST /v1/agents temp status=%d want=%d body=%s", createTempStatus, http.StatusCreated, string(createTempBody))
	}
	tempAgentID := strings.TrimSpace(asString(testutil.JSONPath(t, createTempBody, "data", "id")))
	if tempAgentID == "" {
		t.Fatalf("missing temp agent id body=%s", string(createTempBody))
	}
	if got := strings.ToLower(strings.TrimSpace(asString(testutil.JSONPath(t, createTempBody, "data", "lifecycle_status")))); got != "active" {
		t.Fatalf("temp lifecycle_status=%q want=active body=%s", got, string(createTempBody))
	}
	if got := strings.ToLower(strings.TrimSpace(asString(testutil.JSONPath(t, createTempBody, "data", "agent_class")))); got != "temp" {
		t.Fatalf("temp agent_class=%q want=temp body=%s", got, string(createTempBody))
	}
	if got := strings.TrimSpace(asString(testutil.JSONPath(t, createTempBody, "data", "temp_project_id"))); got != projectID {
		t.Fatalf("temp temp_project_id=%q want=%q body=%s", got, projectID, string(createTempBody))
	}

	testutil.WaitForAgentStatus(t, baseURL, token, tempAgentID, "active", 10*time.Second)

	taskBody, taskStatus := testutil.POST(t, baseURL, "/v1/projects/"+projectID+"/tasks", token, map[string]any{
		"title": "Temp agent test task",
	})
	if taskStatus != http.StatusCreated {
		t.Fatalf("POST /v1/projects/%s/tasks status=%d want=%d body=%s", projectID, taskStatus, http.StatusCreated, string(taskBody))
	}
	taskID := strings.TrimSpace(asString(testutil.JSONPath(t, taskBody, "data", "id")))
	if taskID == "" {
		t.Fatalf("missing task id body=%s", string(taskBody))
	}

	testutil.CompleteTask(t, baseURL, token, taskID)

	postCompleteAgentBody, postCompleteAgentStatus := testutil.GET(t, baseURL, "/v1/agents/"+tempAgentID, token)
	if postCompleteAgentStatus != http.StatusOK {
		t.Fatalf("GET /v1/agents/%s status=%d want=%d body=%s", tempAgentID, postCompleteAgentStatus, http.StatusOK, string(postCompleteAgentBody))
	}
	if got := strings.ToLower(strings.TrimSpace(asString(testutil.JSONPath(t, postCompleteAgentBody, "data", "lifecycle_status")))); got != "active" {
		t.Fatalf("temp lifecycle after task=%q want=active body=%s", got, string(postCompleteAgentBody))
	}

	retireBody, retireStatus := testutil.POST(t, baseURL, "/v1/agents/"+tempAgentID+"/retire", token, map[string]any{})
	if retireStatus != http.StatusOK {
		t.Fatalf("POST /v1/agents/%s/retire status=%d want=%d body=%s", tempAgentID, retireStatus, http.StatusOK, string(retireBody))
	}
	testutil.WaitForAgentStatus(t, baseURL, token, tempAgentID, "retired", 10*time.Second)

	assignRetiredBody, assignRetiredStatus := testutil.POST(t, baseURL, "/v1/agents/"+tempAgentID+"/project-assignments", token, map[string]any{
		"project_id": projectID,
		"role":       "worker",
	})
	if assignRetiredStatus != http.StatusUnprocessableEntity && assignRetiredStatus != http.StatusConflict {
		t.Fatalf("retired assignment status=%d want=422|409 body=%s", assignRetiredStatus, string(assignRetiredBody))
	}
}

func TestAgent_TempLifecycle_TTL_Expires(t *testing.T) {
	server, baseURL, token := setupAgentScenario(t)
	defer server.Stop(t)

	projectID := createProject(t, baseURL, token, "Temp TTL Project")

	createBody, createStatus := testutil.POST(t, baseURL, "/v1/agents", token, map[string]any{
		"display_name":     "ttl-temp-worker",
		"agent_class":      "temp",
		"agent_type":       "worker",
		"temp_project_id":  projectID,
		"temp_ttl_seconds": 3600,
	})
	if createStatus != http.StatusCreated {
		t.Fatalf("POST /v1/agents temp ttl status=%d want=%d body=%s", createStatus, http.StatusCreated, string(createBody))
	}
	tempAgentID := strings.TrimSpace(asString(testutil.JSONPath(t, createBody, "data", "id")))
	if tempAgentID == "" {
		t.Fatalf("missing temp agent id body=%s", string(createBody))
	}
	if got := strings.ToLower(strings.TrimSpace(asString(testutil.JSONPath(t, createBody, "data", "lifecycle_status")))); got != "active" {
		t.Fatalf("ttl temp lifecycle_status=%q want=active body=%s", got, string(createBody))
	}
	expiresAt := strings.TrimSpace(asString(testutil.JSONPath(t, createBody, "data", "temp_expires_at")))
	if expiresAt == "" {
		t.Fatalf("temp_expires_at is empty body=%s", string(createBody))
	}

	taskBody, taskStatus := testutil.POST(t, baseURL, "/v1/projects/"+projectID+"/tasks", token, map[string]any{
		"title": "TTL temp task",
	})
	if taskStatus != http.StatusCreated {
		t.Fatalf("POST /v1/projects/%s/tasks status=%d want=%d body=%s", projectID, taskStatus, http.StatusCreated, string(taskBody))
	}
	taskID := strings.TrimSpace(asString(testutil.JSONPath(t, taskBody, "data", "id")))
	if taskID == "" {
		t.Fatalf("missing task id body=%s", string(taskBody))
	}

	testutil.CompleteTask(t, baseURL, token, taskID)
	testutil.WaitForAgentStatus(t, baseURL, token, tempAgentID, "active", 10*time.Second)

	agentBody, agentStatus := testutil.GET(t, baseURL, "/v1/agents/"+tempAgentID, token)
	if agentStatus != http.StatusOK {
		t.Fatalf("GET /v1/agents/%s status=%d want=%d body=%s", tempAgentID, agentStatus, http.StatusOK, string(agentBody))
	}
	expiresAt = strings.TrimSpace(asString(testutil.JSONPath(t, agentBody, "data", "temp_expires_at")))
	if expiresAt == "" {
		t.Fatalf("temp_expires_at is empty after completion body=%s", string(agentBody))
	}
	parsedExpiresAt := parseAPITime(t, expiresAt)
	if !parsedExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("temp_expires_at=%s must be in the future now=%s", parsedExpiresAt.Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	}
}

func TestAgent_StarterTrio_AlwaysPresent(t *testing.T) {
	server, baseURL, token := setupAgentScenario(t)
	defer server.Stop(t)

	agentsBody, agentsStatus := testutil.GET(t, baseURL, "/v1/agents", token)
	if agentsStatus != http.StatusOK {
		t.Fatalf("GET /v1/agents status=%d want=%d body=%s", agentsStatus, http.StatusOK, string(agentsBody))
	}
	agents := asArray(t, testutil.JSONPath(t, agentsBody, "data"), "agents list")

	required := map[string]bool{
		"frank": false,
		"lori":  false,
		"ellie": false,
	}

	for _, entry := range agents {
		agentObj := asObject(t, entry, "agent")
		name := strings.ToLower(strings.TrimSpace(asString(agentObj["display_name"])))
		if _, ok := required[name]; !ok {
			continue
		}
		if got := strings.ToLower(strings.TrimSpace(asString(agentObj["lifecycle_status"]))); got != "active" {
			t.Fatalf("%s lifecycle_status=%q want=active body=%s", name, got, string(agentsBody))
		}
		if got := strings.ToLower(strings.TrimSpace(asString(agentObj["agent_class"]))); got != "staff" {
			t.Fatalf("%s agent_class=%q want=staff body=%s", name, got, string(agentsBody))
		}
		required[name] = true
	}

	for name, found := range required {
		if !found {
			t.Fatalf("starter trio agent %s missing from /v1/agents body=%s", name, string(agentsBody))
		}
	}
}

func setupAgentScenario(t *testing.T) (*testutil.ServerProcess, string, string) {
	t.Helper()

	server, baseURL := testutil.StartServer(t)
	testutil.ResetState(t, baseURL)

	bootstrapOut, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap")
	if code != 0 {
		server.Stop(t)
		t.Fatalf("bootstrap exit=%d want=0 output=%s", code, bootstrapOut)
	}
	if !strings.Contains(strings.ToLower(bootstrapOut), "bootstrap complete") {
		server.Stop(t)
		t.Fatalf("bootstrap output missing completion marker: %s", bootstrapOut)
	}

	token := testutil.AdminToken(t, baseURL)
	return server, baseURL, token
}

func activateAgent(t *testing.T, baseURL, token, agentID string) {
	t.Helper()

	activateBody, activateStatus := testutil.POST(t, baseURL, "/v1/agents/"+agentID+"/activate", token, map[string]any{})
	if activateStatus == http.StatusOK {
		return
	}
	if activateStatus != http.StatusNotFound {
		t.Fatalf("POST /v1/agents/%s/activate status=%d body=%s", agentID, activateStatus, string(activateBody))
	}

	unpauseBody, unpauseStatus := testutil.POST(t, baseURL, "/v1/agents/"+agentID+"/unpause", token, map[string]any{})
	if unpauseStatus != http.StatusOK {
		t.Fatalf("POST /v1/agents/%s/unpause status=%d want=%d body=%s", agentID, unpauseStatus, http.StatusOK, string(unpauseBody))
	}
}

func createProject(t *testing.T, baseURL, token, name string) string {
	t.Helper()

	slug := strings.ToLower(strings.ReplaceAll(name, " ", "-")) + "-" + fmt.Sprintf("%d", time.Now().UnixNano())
	projectBody, projectStatus := testutil.POST(t, baseURL, "/v1/projects", token, map[string]any{
		"slug":          slug,
		"display_name":  name,
		"delivery_mode": "gated",
	})
	if projectStatus != http.StatusCreated {
		t.Fatalf("POST /v1/projects status=%d want=%d body=%s", projectStatus, http.StatusCreated, string(projectBody))
	}
	projectID := strings.TrimSpace(asString(testutil.JSONPath(t, projectBody, "data", "id")))
	if projectID == "" {
		t.Fatalf("missing project id body=%s", string(projectBody))
	}
	return projectID
}

func parseAPITime(t *testing.T, raw string) time.Time {
	t.Helper()
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, strings.TrimSpace(raw))
		if err == nil {
			return parsed.UTC()
		}
	}
	t.Fatalf("unable to parse time %q", raw)
	return time.Time{}
}
