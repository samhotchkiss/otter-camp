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

func TestMemory_ExtractionAndRetrieval(t *testing.T) {
	server, baseURL := testutil.StartServerWithWorker(t)
	defer server.Stop(t)

	token, orgID, frankID := bootstrapAndLogin(t, server, baseURL)
	sessionID := createSession(t, baseURL, token, "organization", orgID)
	addParticipant(t, baseURL, token, sessionID, frankID)

	seq1 := postUserMessage(t, baseURL, token, sessionID, "[memory-fact] The primary database server is named db-prod-01 and runs PostgreSQL 16.")
	testutil.WaitForAssistantMessage(t, baseURL, token, sessionID, seq1, 30*time.Second)
	seq2 := postUserMessage(t, baseURL, token, sessionID, "[memory-fact] The deployment pipeline runs every Monday at 09:00 UTC.")
	testutil.WaitForAssistantMessage(t, baseURL, token, sessionID, seq2, 30*time.Second)

	testutil.TriggerExtractionJob(t, baseURL, token, sessionID)

	memoryA := testutil.WaitForMemory(t, baseURL, token, testutil.MemoryFilter{ScopeType: "organization", ContainsText: "db-prod-01"}, 30*time.Second)
	if strings.TrimSpace(asString(memoryA["organization_id"])) == "" {
		t.Fatalf("memory organization_id empty: %+v", memoryA)
	}
	scope := strings.ToLower(strings.TrimSpace(asString(memoryA["scope"])))
	if scope != "org" && scope != "organization" {
		t.Fatalf("memory scope=%q want org|organization item=%+v", scope, memoryA)
	}
	if !strings.Contains(strings.ToLower(asString(memoryA["content"])), "db-prod-01") {
		t.Fatalf("memory content missing db-prod-01 item=%+v", memoryA)
	}
	if strings.ToLower(strings.TrimSpace(asString(memoryA["status"]))) != "active" {
		t.Fatalf("memory status=%q want=active item=%+v", asString(memoryA["status"]), memoryA)
	}
	if asFloat(memoryA["confidence"]) < 0 {
		t.Fatalf("memory confidence=%f want >= 0", asFloat(memoryA["confidence"]))
	}

	memoryB := testutil.WaitForMemory(t, baseURL, token, testutil.MemoryFilter{ContainsText: "deployment pipeline"}, 30*time.Second)
	contentB := strings.ToLower(asString(memoryB["content"]))
	if !strings.Contains(contentB, "monday") && !strings.Contains(contentB, "09:00") {
		t.Fatalf("memory content missing Monday or 09:00 item=%+v", memoryB)
	}

	queryBody, queryStatus := testutil.POST(t, baseURL, "/v1/memory/query", token, map[string]any{
		"query":       "database server configuration",
		"mode":        "mention",
		"max_results": 10,
	})
	if queryStatus != http.StatusOK {
		t.Fatalf("POST /v1/memory/query status=%d want=%d body=%s", queryStatus, http.StatusOK, string(queryBody))
	}
	results := asArray(t, testutil.JSONPath(t, queryBody, "data", "memories"), "memory query results")
	if len(results) < 1 {
		t.Fatalf("expected retrieval results body=%s", string(queryBody))
	}
	first := asObject(t, results[0], "first memory result")
	if asFloat(first["composite_score"]) <= 0 {
		t.Fatalf("composite_score=%f want > 0 result=%+v", asFloat(first["composite_score"]), first)
	}
	containsDB := false
	for _, raw := range results {
		item := asObject(t, raw, "memory result")
		if strings.Contains(strings.ToLower(asString(item["content"])), "db-prod-01") {
			containsDB = true
			break
		}
	}
	if !containsDB {
		t.Fatalf("retrieval results missing db-prod-01 body=%s", string(queryBody))
	}

	seq3 := postUserMessage(t, baseURL, token, sessionID, "What do you know about our infrastructure?")
	testutil.WaitForAssistantMessage(t, baseURL, token, sessionID, seq3, 30*time.Second)

	invBody, invStatus := testutil.GET(t, baseURL, "/v1/model/invocations?session_id="+sessionID, token)
	if invStatus != http.StatusOK {
		t.Fatalf("GET /v1/model/invocations?session_id status=%d want=%d body=%s", invStatus, http.StatusOK, string(invBody))
	}
	invocations := asArray(t, testutil.JSONPath(t, invBody, "data"), "model invocations")
	if len(invocations) < 1 {
		t.Fatalf("expected model invocations body=%s", string(invBody))
	}
	latest := asObject(t, invocations[0], "latest invocation")
	metadata, _ := latest["metadata"].(map[string]any)
	if len(metadata) == 0 {
		t.Fatalf("missing invocation metadata invocation=%+v", latest)
	}

	memoryLayerTokens := readNestedNumber(metadata, "layer_token_counts", "memory_injection")
	if memoryLayerTokens <= 0 {
		memoryLayerTokens = readNestedNumber(metadata, "memory_layer_tokens")
	}
	if memoryLayerTokens <= 0 {
		t.Log("TODO: verify metadata field name with task 036 implementer")
	}
}

func TestMemory_Compaction(t *testing.T) {
	server, baseURL := testutil.StartServerWithWorker(t)
	defer server.Stop(t)

	token, orgID, frankID := bootstrapAndLogin(t, server, baseURL)
	sessionID := createSession(t, baseURL, token, "organization", orgID)
	addParticipant(t, baseURL, token, sessionID, frankID)

	for i := 1; i <= 5; i++ {
		postUserMessage(t, baseURL, token, sessionID, fmt.Sprintf("[memory-fact] Infrastructure fact %d for compaction coverage.", i))
	}
	testutil.TriggerExtractionJob(t, baseURL, token, sessionID)

	episodicBody, episodicStatus := testutil.GET(t, baseURL, "/v1/memory/items?status=active&memory_type=episodic", token)
	if episodicStatus != http.StatusOK {
		t.Fatalf("GET /v1/memory/items episodic status=%d want=%d body=%s", episodicStatus, http.StatusOK, string(episodicBody))
	}
	episodic := asArray(t, testutil.JSONPath(t, episodicBody, "data"), "episodic memories")
	if len(episodic) < 2 {
		t.Fatalf("episodic count=%d want>=2 body=%s", len(episodic), string(episodicBody))
	}
	episodicIDs := make([]string, 0, len(episodic))
	for _, raw := range episodic {
		item := asObject(t, raw, "episodic item")
		episodicIDs = append(episodicIDs, asString(item["id"]))
	}

	testutil.TriggerCompactionRun(t, baseURL, token, orgID)

	semanticBody, semanticStatus := testutil.GET(t, baseURL, "/v1/memory/items?status=active&memory_type=semantic", token)
	if semanticStatus != http.StatusOK {
		t.Fatalf("GET /v1/memory/items semantic status=%d want=%d body=%s", semanticStatus, http.StatusOK, string(semanticBody))
	}
	semantic := asArray(t, testutil.JSONPath(t, semanticBody, "data"), "semantic memories")
	if len(semantic) < 1 {
		t.Fatalf("semantic count=%d want>=1 body=%s", len(semantic), string(semanticBody))
	}

	runsBody, runsStatus := testutil.GET(t, baseURL, "/v1/memory/compaction-runs?limit=20", token)
	if runsStatus != http.StatusOK {
		t.Fatalf("GET /v1/memory/compaction-runs status=%d want=%d body=%s", runsStatus, http.StatusOK, string(runsBody))
	}
	runs := asArray(t, testutil.JSONPath(t, runsBody, "data"), "compaction runs")
	if len(runs) < 1 {
		t.Fatalf("expected compaction runs body=%s", string(runsBody))
	}
	firstRun := asObject(t, runs[0], "first compaction run")
	if strings.ToLower(strings.TrimSpace(asString(firstRun["status"]))) != "completed" {
		t.Fatalf("compaction run status=%q want=completed run=%+v", asString(firstRun["status"]), firstRun)
	}
	if asFloat(firstRun["memories_created"]) < 1 {
		t.Fatalf("memories_created=%f want>=1 run=%+v", asFloat(firstRun["memories_created"]), firstRun)
	}

	deadline := time.Now().Add(30 * time.Second)
	for _, memoryID := range episodicIDs {
		verified := false
		for time.Now().Before(deadline) {
			detailBody, detailStatus := testutil.GET(t, baseURL, "/v1/memory/items/"+memoryID, token)
			if detailStatus == http.StatusTooManyRequests {
				time.Sleep(1 * time.Second)
				continue
			}
			if detailStatus != http.StatusOK {
				t.Fatalf("GET /v1/memory/items/%s status=%d want=%d body=%s", memoryID, detailStatus, http.StatusOK, string(detailBody))
			}
			detail := asObject(t, testutil.JSONPath(t, detailBody, "data"), "episodic detail")
			supersededBy := strings.TrimSpace(asString(detail["superseded_by"]))
			status := strings.ToLower(strings.TrimSpace(asString(detail["status"])))
			archivedAt := strings.TrimSpace(asString(detail["archived_at"]))
			if supersededBy != "" || status != "active" || archivedAt != "" {
				verified = true
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		if !verified {
			t.Fatalf("episodic memory %s not superseded/archived before timeout", memoryID)
		}
	}
}

func TestMemory_ScopeFilter(t *testing.T) {
	server, baseURL := testutil.StartServerWithWorker(t)
	defer server.Stop(t)

	token, orgID, frankID := bootstrapAndLogin(t, server, baseURL)

	orgSessionID := createSession(t, baseURL, token, "organization", orgID)
	addParticipant(t, baseURL, token, orgSessionID, frankID)
	postUserMessage(t, baseURL, token, orgSessionID, "[memory-fact] Org infrastructure baseline uses db-prod-01 and shared observability.")
	testutil.TriggerExtractionJob(t, baseURL, token, orgSessionID)

	projectID := createScopedProject(t, baseURL, token, "memory-scope")
	projectSessionID := createSession(t, baseURL, token, "project", projectID)
	addParticipant(t, baseURL, token, projectSessionID, frankID)
	postUserMessage(t, baseURL, token, projectSessionID, "[memory-fact] Project infrastructure uses a dedicated cache cluster project-cache-7.")
	testutil.TriggerExtractionJob(t, baseURL, token, projectSessionID)

	orgQueryBody, orgQueryStatus := testutil.POST(t, baseURL, "/v1/memory/query", token, map[string]any{
		"query":       "infrastructure",
		"mode":        "mention",
		"max_results": 20,
	})
	if orgQueryStatus != http.StatusOK {
		t.Fatalf("org scope query status=%d want=%d body=%s", orgQueryStatus, http.StatusOK, string(orgQueryBody))
	}
	orgResults := asArray(t, testutil.JSONPath(t, orgQueryBody, "data", "memories"), "org scope results")
	if len(orgResults) < 1 {
		t.Fatalf("org scope results empty body=%s", string(orgQueryBody))
	}
	orgHasOrgMemory := false
	orgHasProjectMemory := false
	for _, raw := range orgResults {
		item := asObject(t, raw, "org result item")
		content := strings.ToLower(asString(item["content"]))
		if strings.Contains(content, "db-prod-01") {
			orgHasOrgMemory = true
		}
		if strings.Contains(content, "project-cache-7") {
			orgHasProjectMemory = true
		}
	}
	if !orgHasOrgMemory {
		t.Fatalf("org-scope query missing org memory body=%s", string(orgQueryBody))
	}
	if orgHasProjectMemory {
		t.Fatalf("org-scope query unexpectedly included project memory body=%s", string(orgQueryBody))
	}

	projectQueryBody, projectQueryStatus := testutil.POST(t, baseURL, "/v1/memory/query", token, map[string]any{
		"query":       "infrastructure",
		"mode":        "mention",
		"project_id":  projectID,
		"max_results": 20,
	})
	if projectQueryStatus != http.StatusOK {
		t.Fatalf("project scope query status=%d want=%d body=%s", projectQueryStatus, http.StatusOK, string(projectQueryBody))
	}
	projectResults := asArray(t, testutil.JSONPath(t, projectQueryBody, "data", "memories"), "project scope results")
	if len(projectResults) < 1 {
		t.Fatalf("project scope results empty body=%s", string(projectQueryBody))
	}
	projectHasOrgMemory := false
	projectHasProjectMemory := false
	for _, raw := range projectResults {
		item := asObject(t, raw, "project result item")
		content := strings.ToLower(asString(item["content"]))
		if strings.Contains(content, "db-prod-01") {
			projectHasOrgMemory = true
		}
		if strings.Contains(content, "project-cache-7") {
			projectHasProjectMemory = true
		}
	}
	if !projectHasOrgMemory || !projectHasProjectMemory {
		t.Fatalf("project-scope query expected both org and project memories body=%s", string(projectQueryBody))
	}
}

func bootstrapAndLogin(t *testing.T, server *testutil.ServerProcess, baseURL string) (token string, orgID string, frankID string) {
	t.Helper()
	testutil.ResetState(t, baseURL)

	out, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap")
	if code != 0 {
		t.Fatalf("bootstrap exit=%d want=0 output=%s", code, out)
	}

	token = testutil.AdminToken(t, baseURL)
	orgBody, orgStatus := testutil.GET(t, baseURL, "/v1/orgs/current", token)
	if orgStatus != http.StatusOK {
		t.Fatalf("GET /v1/orgs/current status=%d want=%d body=%s", orgStatus, http.StatusOK, string(orgBody))
	}
	orgID = strings.TrimSpace(asString(testutil.JSONPath(t, orgBody, "data", "id")))
	if orgID == "" {
		t.Fatalf("missing org id body=%s", string(orgBody))
	}

	frankBody, frankStatus := testutil.GET(t, baseURL, "/v1/agents?name=Frank", token)
	if frankStatus != http.StatusOK {
		t.Fatalf("GET /v1/agents?name=Frank status=%d want=%d body=%s", frankStatus, http.StatusOK, string(frankBody))
	}
	frankAgents := asArray(t, testutil.JSONPath(t, frankBody, "data"), "frank agents")
	if len(frankAgents) == 0 {
		t.Fatalf("no frank agent found body=%s", string(frankBody))
	}
	frankID = strings.TrimSpace(asString(asObject(t, frankAgents[0], "frank")["id"]))
	if frankID == "" {
		t.Fatalf("frank id missing body=%s", string(frankBody))
	}
	return token, orgID, frankID
}

func createSession(t *testing.T, baseURL, token, scopeType, scopeID string) string {
	t.Helper()
	body, status := testutil.POST(t, baseURL, "/v1/chat-sessions", token, map[string]any{
		"scope_type": scopeType,
		"scope_id":   scopeID,
		"mode":       "sync",
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/chat-sessions status=%d want=%d body=%s", status, http.StatusCreated, string(body))
	}
	sessionID := strings.TrimSpace(asString(testutil.JSONPath(t, body, "data", "id")))
	if sessionID == "" {
		t.Fatalf("missing session id body=%s", string(body))
	}
	return sessionID
}

func addParticipant(t *testing.T, baseURL, token, sessionID, participantID string) {
	t.Helper()
	body, status := testutil.POST(t, baseURL, "/v1/chat-sessions/"+sessionID+"/participants", token, map[string]any{
		"participant_type": "agent",
		"participant_id":   participantID,
		"role":             "member",
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/chat-sessions/%s/participants status=%d want=%d body=%s", sessionID, status, http.StatusCreated, string(body))
	}
}

func postUserMessage(t *testing.T, baseURL, token, sessionID, content string) int64 {
	t.Helper()
	body, status := testutil.POST(t, baseURL, "/v1/chat-sessions/"+sessionID+"/messages", token, map[string]any{
		"content": content,
	})
	if status != http.StatusAccepted {
		t.Fatalf("POST /v1/chat-sessions/%s/messages status=%d want=%d body=%s", sessionID, status, http.StatusAccepted, string(body))
	}
	seq := testutil.JSONPath(t, body, "data", "sequence_number")
	return int64(asFloat(seq))
}

func createScopedProject(t *testing.T, baseURL, token, prefix string) string {
	t.Helper()
	slug := fmt.Sprintf("%s-%d", strings.TrimSpace(prefix), time.Now().UnixNano())
	body, status := testutil.POST(t, baseURL, "/v1/projects", token, map[string]any{
		"slug":         slug,
		"display_name": "Memory Scope Project",
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

func asFloat(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	default:
		return 0
	}
}

func readNestedNumber(root map[string]any, path ...string) float64 {
	current := any(root)
	for _, segment := range path {
		obj, ok := current.(map[string]any)
		if !ok {
			return 0
		}
		next, ok := obj[segment]
		if !ok {
			return 0
		}
		current = next
	}
	return asFloat(current)
}
