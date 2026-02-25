//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/e2e/testutil"
)

func TestBootstrap_FreshInstall(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	testutil.ResetState(t, baseURL)

	bootstrapOut, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap")
	if code != 0 {
		t.Fatalf("bootstrap exit=%d want=0 output=%s", code, bootstrapOut)
	}
	if !strings.Contains(bootstrapOut, "Bootstrap complete") {
		t.Fatalf("bootstrap output missing completion marker: %s", bootstrapOut)
	}

	healthOut, code := server.RunCLI(t, "health", "--server-url", baseURL, "--output", "json")
	if code != 0 {
		t.Fatalf("health exit=%d want=0 output=%s", code, healthOut)
	}
	healthLower := strings.ToLower(healthOut)
	if !strings.Contains(healthLower, "healthy") && !strings.Contains(healthLower, "\"status\":\"ok\"") {
		t.Fatalf("health output missing healthy marker: %s", healthOut)
	}

	healthBody, healthStatus := testutil.GET(t, baseURL, "/health/live", "")
	if healthStatus != http.StatusOK {
		t.Fatalf("GET /health/live status=%d want=%d body=%s", healthStatus, http.StatusOK, string(healthBody))
	}
	statusText := strings.ToLower(strings.TrimSpace(asString(testutil.JSONPath(t, healthBody, "data", "status"))))
	if statusText != "healthy" && statusText != "ok" {
		t.Fatalf("/health/live data.status=%q want=healthy|ok body=%s", statusText, string(healthBody))
	}

	token := testutil.AdminToken(t, baseURL)

	orgBody, orgStatus := testutil.GET(t, baseURL, "/v1/orgs/current", token)
	if orgStatus != http.StatusOK {
		t.Fatalf("GET /v1/orgs/current status=%d want=%d body=%s", orgStatus, http.StatusOK, string(orgBody))
	}
	if strings.TrimSpace(asString(testutil.JSONPath(t, orgBody, "data", "id"))) == "" {
		t.Fatalf("organization id is empty body=%s", string(orgBody))
	}
	if strings.TrimSpace(asString(testutil.JSONPath(t, orgBody, "data", "name"))) == "" {
		t.Fatalf("organization name is empty body=%s", string(orgBody))
	}

	frankBody, frankStatus := testutil.GET(t, baseURL, "/v1/agents?name=Frank", token)
	if frankStatus != http.StatusOK {
		t.Fatalf("GET /v1/agents?name=Frank status=%d want=%d body=%s", frankStatus, http.StatusOK, string(frankBody))
	}
	frankAgents := asArray(t, testutil.JSONPath(t, frankBody, "data"), "agents Frank")
	if len(frankAgents) == 0 {
		t.Fatalf("expected Frank agent body=%s", string(frankBody))
	}
	frank := asObject(t, frankAgents[0], "Frank agent")
	if got := strings.ToLower(asString(frank["lifecycle_status"])); got != "active" {
		t.Fatalf("Frank lifecycle_status=%q want=active body=%s", got, string(frankBody))
	}
	if got := strings.ToLower(asString(frank["agent_class"])); got != "staff" {
		t.Fatalf("Frank agent_class=%q want=staff body=%s", got, string(frankBody))
	}
	if strings.TrimSpace(asString(frank["agent_type"])) == "" {
		t.Fatalf("Frank agent_type is empty body=%s", string(frankBody))
	}

	pmBody, pmStatus := testutil.GET(t, baseURL, "/v1/agents?role=pm", token)
	if pmStatus != http.StatusOK {
		t.Fatalf("GET /v1/agents?role=pm status=%d want=%d body=%s", pmStatus, http.StatusOK, string(pmBody))
	}
	pmAgents := asArray(t, testutil.JSONPath(t, pmBody, "data"), "PM agents")
	if len(pmAgents) < 1 {
		t.Fatalf("expected at least one PM agent body=%s", string(pmBody))
	}
	pm := asObject(t, pmAgents[0], "PM agent")
	if got := strings.ToLower(asString(pm["lifecycle_status"])); got != "active" {
		t.Fatalf("PM lifecycle_status=%q want=active body=%s", got, string(pmBody))
	}

	instanceBody, instanceStatus := testutil.GET(t, baseURL, "/v1/control/policies?policy_layer=instance", token)
	if instanceStatus != http.StatusOK {
		t.Fatalf("GET /v1/control/policies?policy_layer=instance status=%d want=%d body=%s", instanceStatus, http.StatusOK, string(instanceBody))
	}
	instancePolicies := asArray(t, testutil.JSONPath(t, instanceBody, "data"), "instance policies")
	if len(instancePolicies) < 1 {
		t.Fatalf("expected instance policies body=%s", string(instanceBody))
	}
	firstInstance := asObject(t, instancePolicies[0], "instance policy")
	if got := strings.ToLower(asString(firstInstance["policy_layer"])); got != "instance" {
		t.Fatalf("instance policy_layer=%q want=instance body=%s", got, string(instanceBody))
	}

	orgPoliciesBody, orgPoliciesStatus := testutil.GET(t, baseURL, "/v1/control/policies?policy_layer=org", token)
	if orgPoliciesStatus != http.StatusOK {
		t.Fatalf("GET /v1/control/policies?policy_layer=org status=%d want=%d body=%s", orgPoliciesStatus, http.StatusOK, string(orgPoliciesBody))
	}
	orgPolicies := asArray(t, testutil.JSONPath(t, orgPoliciesBody, "data"), "org policies")
	if len(orgPolicies) < 1 {
		t.Fatalf("expected org policies body=%s", string(orgPoliciesBody))
	}

	assignBody, assignStatus := testutil.GET(t, baseURL, "/v1/model/assignments?scope_type=organization", token)
	if assignStatus != http.StatusOK {
		t.Fatalf("GET /v1/model/assignments?scope_type=organization status=%d want=%d body=%s", assignStatus, http.StatusOK, string(assignBody))
	}
	assignments := asArray(t, testutil.JSONPath(t, assignBody, "data"), "model assignments")
	if len(assignments) < 1 {
		t.Fatalf("expected org model assignment body=%s", string(assignBody))
	}
	firstAssign := asObject(t, assignments[0], "model assignment")
	if got := strings.ToLower(asString(firstAssign["scope_type"])); got != "organization" {
		t.Fatalf("scope_type=%q want=organization body=%s", got, string(assignBody))
	}
	if strings.TrimSpace(asString(firstAssign["logical_profile_id"])) == "" {
		t.Fatalf("logical_profile_id empty body=%s", string(assignBody))
	}

	auditBody, auditStatus := testutil.GET(t, baseURL, "/v1/audit?action=bootstrap_complete", token)
	if auditStatus != http.StatusOK {
		t.Fatalf("GET /v1/audit?action=bootstrap_complete status=%d want=%d body=%s", auditStatus, http.StatusOK, string(auditBody))
	}
	auditEvents := asArray(t, testutil.JSONPath(t, auditBody, "data"), "audit events")
	if len(auditEvents) < 1 {
		t.Fatalf("expected bootstrap audit events body=%s", string(auditBody))
	}
	firstAudit := asObject(t, auditEvents[0], "audit event")
	if got := strings.ToLower(asString(firstAudit["action"])); got != "bootstrap_complete" {
		t.Fatalf("audit action=%q want=bootstrap_complete body=%s", got, string(auditBody))
	}
}

func TestBootstrap_Idempotent(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	testutil.ResetState(t, baseURL)
	if out, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap"); code != 0 {
		t.Fatalf("first bootstrap exit=%d want=0 output=%s", code, out)
	}

	token := testutil.AdminToken(t, baseURL)
	agentsBefore := countList(t, baseURL, "/v1/agents", token)
	policiesBefore := countList(t, baseURL, "/v1/control/policies?policy_layer=instance", token)
	assignBefore := countList(t, baseURL, "/v1/model/assignments?scope_type=organization", token)

	secondOut, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap")
	if code != 0 {
		t.Fatalf("second bootstrap exit=%d want=0 output=%s", code, secondOut)
	}
	secondOutLower := strings.ToLower(secondOut)
	if !strings.Contains(secondOutLower, "bootstrap complete") && !strings.Contains(secondOutLower, "already bootstrapped") {
		t.Fatalf("second bootstrap output missing expected marker: %s", secondOut)
	}

	agentsAfter := countList(t, baseURL, "/v1/agents", token)
	policiesAfter := countList(t, baseURL, "/v1/control/policies?policy_layer=instance", token)
	assignAfter := countList(t, baseURL, "/v1/model/assignments?scope_type=organization", token)

	if agentsAfter != agentsBefore {
		t.Fatalf("agent count changed after rerun: before=%d after=%d", agentsBefore, agentsAfter)
	}
	if policiesAfter != policiesBefore {
		t.Fatalf("instance policy count changed after rerun: before=%d after=%d", policiesBefore, policiesAfter)
	}
	if assignAfter != assignBefore {
		t.Fatalf("model assignment count changed after rerun: before=%d after=%d", assignBefore, assignAfter)
	}
}

func TestBootstrap_TestReset_ProducesCleanState(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	testutil.ResetState(t, baseURL)
	if out, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap"); code != 0 {
		t.Fatalf("bootstrap exit=%d want=0 output=%s", code, out)
	}

	token := testutil.AdminToken(t, baseURL)
	createBody, createStatus := testutil.POST(t, baseURL, "/v1/agents", token, map[string]any{
		"display_name": "ephemeral-test-agent",
		"agent_class":  "staff",
		"agent_type":   "general",
	})
	if createStatus != http.StatusCreated {
		t.Fatalf("POST /v1/agents status=%d want=%d body=%s", createStatus, http.StatusCreated, string(createBody))
	}

	testutil.ResetState(t, baseURL)
	if out, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap"); code != 0 {
		t.Fatalf("post-reset bootstrap exit=%d want=0 output=%s", code, out)
	}

	token = testutil.AdminToken(t, baseURL)
	agentsBody, agentsStatus := testutil.GET(t, baseURL, "/v1/agents?name=ephemeral-test-agent", token)
	if agentsStatus != http.StatusOK {
		t.Fatalf("GET /v1/agents?name=ephemeral-test-agent status=%d want=%d body=%s", agentsStatus, http.StatusOK, string(agentsBody))
	}
	agents := asArray(t, testutil.JSONPath(t, agentsBody, "data"), "ephemeral agents")
	if len(agents) != 0 {
		t.Fatalf("ephemeral agent still present after reset+bootstrap body=%s", string(agentsBody))
	}
}

func countList(t *testing.T, baseURL, path, token string) int {
	t.Helper()
	body, status := testutil.GET(t, baseURL, path, token)
	if status != http.StatusOK {
		t.Fatalf("GET %s status=%d want=%d body=%s", path, status, http.StatusOK, string(body))
	}
	return len(asArray(t, testutil.JSONPath(t, body, "data"), path))
}

func asArray(t *testing.T, value any, label string) []any {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("%s type=%T want=[]any", label, value)
	}
	return items
}

func asObject(t *testing.T, value any, label string) map[string]any {
	t.Helper()
	obj, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s type=%T want=map[string]any", label, value)
	}
	return obj
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}
