//go:build integration

package server

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestAgentAssignmentHTTPPMFlow(t *testing.T) {
	testServer, org, adminUser, _ := newAgentTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	project := seedAssignmentProject(t, testServer.Pool, org.ID)
	agentRecord := seedActiveAssignmentAgent(t, testServer.Pool, org.ID, "pm-flow-agent")

	assignResp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents/"+agentRecord.ID.String()+"/project-assignments", map[string]any{
		"project_id": project.ID.String(),
		"role":       "pm",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if assignResp.StatusCode != http.StatusOK {
		t.Fatalf("assign status = %d, want %d body=%s", assignResp.StatusCode, http.StatusOK, string(assignResp.Body))
	}

	listBeforeDelete := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents/"+agentRecord.ID.String()+"/project-assignments", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if listBeforeDelete.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want %d body=%s", listBeforeDelete.StatusCode, http.StatusOK, string(listBeforeDelete.Body))
	}
	items, ok := jsonPathValue(t, listBeforeDelete.Body, "data").([]any)
	if !ok {
		t.Fatalf("list data not array body=%s", string(listBeforeDelete.Body))
	}
	if len(items) != 1 {
		t.Fatalf("list item count = %d, want 1 body=%s", len(items), string(listBeforeDelete.Body))
	}
	if got := jsonPathString(t, listBeforeDelete.Body, "data", "0", "project_id"); got != project.ID.String() {
		t.Fatalf("project_id = %q, want %q body=%s", got, project.ID.String(), string(listBeforeDelete.Body))
	}

	deleteResp := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/agents/"+agentRecord.ID.String()+"/project-assignments/"+project.ID.String(), nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d, want %d body=%s", deleteResp.StatusCode, http.StatusOK, string(deleteResp.Body))
	}

	listAfterDelete := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents/"+agentRecord.ID.String()+"/project-assignments", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if listAfterDelete.StatusCode != http.StatusOK {
		t.Fatalf("list after delete status = %d, want %d body=%s", listAfterDelete.StatusCode, http.StatusOK, string(listAfterDelete.Body))
	}
	itemsAfter, ok := jsonPathValue(t, listAfterDelete.Body, "data").([]any)
	if !ok {
		t.Fatalf("list after delete data not array body=%s", string(listAfterDelete.Body))
	}
	if len(itemsAfter) != 0 {
		t.Fatalf("list after delete item count = %d, want 0 body=%s", len(itemsAfter), string(listAfterDelete.Body))
	}
}

func TestProjectAgentsHTTPAssignmentLifecycle(t *testing.T) {
	testServer, org, adminUser, _ := newAgentTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	project := seedAssignmentProject(t, testServer.Pool, org.ID)
	agentRecord := seedActiveAssignmentAgent(t, testServer.Pool, org.ID, "project-route-agent")

	assignResp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+project.ID.String()+"/agents", map[string]any{
		"agent_id": agentRecord.ID.String(),
		"role":     "worker",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if assignResp.StatusCode != http.StatusOK {
		t.Fatalf("assign status = %d, want %d body=%s", assignResp.StatusCode, http.StatusOK, string(assignResp.Body))
	}

	listResp := mustJSON(t, http.MethodGet, testServer.URL+"/v1/projects/"+project.ID.String()+"/agents", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want %d body=%s", listResp.StatusCode, http.StatusOK, string(listResp.Body))
	}
	items, ok := jsonPathValue(t, listResp.Body, "data").([]any)
	if !ok {
		t.Fatalf("list data not array body=%s", string(listResp.Body))
	}
	if len(items) != 1 {
		t.Fatalf("list item count = %d, want 1 body=%s", len(items), string(listResp.Body))
	}
	if got := jsonPathString(t, listResp.Body, "data", "0", "agent_id"); got != agentRecord.ID.String() {
		t.Fatalf("agent_id = %q, want %q body=%s", got, agentRecord.ID.String(), string(listResp.Body))
	}

	deleteResp := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/projects/"+project.ID.String()+"/agents/"+agentRecord.ID.String(), nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d, want %d body=%s", deleteResp.StatusCode, http.StatusOK, string(deleteResp.Body))
	}

	listAfterDelete := mustJSON(t, http.MethodGet, testServer.URL+"/v1/projects/"+project.ID.String()+"/agents", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if listAfterDelete.StatusCode != http.StatusOK {
		t.Fatalf("list after delete status = %d, want %d body=%s", listAfterDelete.StatusCode, http.StatusOK, string(listAfterDelete.Body))
	}
	itemsAfter, ok := jsonPathValue(t, listAfterDelete.Body, "data").([]any)
	if !ok {
		t.Fatalf("list after delete data not array body=%s", string(listAfterDelete.Body))
	}
	if len(itemsAfter) != 0 {
		t.Fatalf("list after delete item count = %d, want 0 body=%s", len(itemsAfter), string(listAfterDelete.Body))
	}
}

func TestAgentSkillsHTTPAttachmentLifecycle(t *testing.T) {
	testServer, org, adminUser, _ := newAgentTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	agentRecord := seedActiveAssignmentAgent(t, testServer.Pool, org.ID, "skill-flow-agent")
	skillA := seedAssignmentSkillRecord(t, testServer.Pool, org.ID, "skill-a")
	skillB := seedAssignmentSkillRecord(t, testServer.Pool, org.ID, "skill-b")

	attachA := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents/"+agentRecord.ID.String()+"/skills", map[string]any{
		"skill_id": skillA.ID.String(),
		"priority": 200,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if attachA.StatusCode != http.StatusCreated {
		t.Fatalf("attachA status = %d, want %d body=%s", attachA.StatusCode, http.StatusCreated, string(attachA.Body))
	}

	attachB := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents/"+agentRecord.ID.String()+"/skills", map[string]any{
		"skill_id": skillB.ID.String(),
		"priority": 50,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if attachB.StatusCode != http.StatusCreated {
		t.Fatalf("attachB status = %d, want %d body=%s", attachB.StatusCode, http.StatusCreated, string(attachB.Body))
	}

	listBeforePatch := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents/"+agentRecord.ID.String()+"/skills", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if listBeforePatch.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want %d body=%s", listBeforePatch.StatusCode, http.StatusOK, string(listBeforePatch.Body))
	}
	if got := jsonPathString(t, listBeforePatch.Body, "data", "0", "skill_id"); got != skillB.ID.String() {
		t.Fatalf("first skill before patch = %q, want %q body=%s", got, skillB.ID.String(), string(listBeforePatch.Body))
	}

	patchResp := mustJSON(t, http.MethodPatch, testServer.URL+"/v1/agents/"+agentRecord.ID.String()+"/skills/"+skillA.ID.String(), map[string]any{
		"priority": 10,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d, want %d body=%s", patchResp.StatusCode, http.StatusOK, string(patchResp.Body))
	}

	listAfterPatch := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents/"+agentRecord.ID.String()+"/skills", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if listAfterPatch.StatusCode != http.StatusOK {
		t.Fatalf("list after patch status = %d, want %d body=%s", listAfterPatch.StatusCode, http.StatusOK, string(listAfterPatch.Body))
	}
	if got := jsonPathString(t, listAfterPatch.Body, "data", "0", "skill_id"); got != skillA.ID.String() {
		t.Fatalf("first skill after patch = %q, want %q body=%s", got, skillA.ID.String(), string(listAfterPatch.Body))
	}

	deleteA := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/agents/"+agentRecord.ID.String()+"/skills/"+skillA.ID.String(), nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if deleteA.StatusCode != http.StatusOK {
		t.Fatalf("deleteA status = %d, want %d body=%s", deleteA.StatusCode, http.StatusOK, string(deleteA.Body))
	}
	deleteB := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/agents/"+agentRecord.ID.String()+"/skills/"+skillB.ID.String(), nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if deleteB.StatusCode != http.StatusOK {
		t.Fatalf("deleteB status = %d, want %d body=%s", deleteB.StatusCode, http.StatusOK, string(deleteB.Body))
	}

	listAfterDelete := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents/"+agentRecord.ID.String()+"/skills", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if listAfterDelete.StatusCode != http.StatusOK {
		t.Fatalf("list after delete status = %d, want %d body=%s", listAfterDelete.StatusCode, http.StatusOK, string(listAfterDelete.Body))
	}
	itemsAfterDelete, ok := jsonPathValue(t, listAfterDelete.Body, "data").([]any)
	if !ok {
		t.Fatalf("list after delete data not array body=%s", string(listAfterDelete.Body))
	}
	if len(itemsAfterDelete) != 0 {
		t.Fatalf("list after delete item count = %d, want 0 body=%s", len(itemsAfterDelete), string(listAfterDelete.Body))
	}
}

func TestAgentAssignmentHTTPConcurrentPMRequests(t *testing.T) {
	testServer, org, adminUser, _ := newAgentTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	project := seedAssignmentProject(t, testServer.Pool, org.ID)
	agentA := seedActiveAssignmentAgent(t, testServer.Pool, org.ID, "concurrent-a")
	agentB := seedActiveAssignmentAgent(t, testServer.Pool, org.ID, "concurrent-b")

	statuses := make([]int, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		resp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents/"+agentA.ID.String()+"/project-assignments", map[string]any{
			"project_id": project.ID.String(),
			"role":       "pm",
		}, map[string]string{"Authorization": "Bearer " + adminToken})
		statuses[0] = resp.StatusCode
	}()
	go func() {
		defer wg.Done()
		resp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents/"+agentB.ID.String()+"/project-assignments", map[string]any{
			"project_id": project.ID.String(),
			"role":       "pm",
		}, map[string]string{"Authorization": "Bearer " + adminToken})
		statuses[1] = resp.StatusCode
	}()
	wg.Wait()

	if statuses[0] != http.StatusOK || statuses[1] != http.StatusOK {
		t.Fatalf("concurrent statuses = [%d %d], want [200 200]", statuses[0], statuses[1])
	}

	var activePMCount int
	if err := testServer.Pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM agent_project_assignment
		WHERE project_id = $1
		  AND role = 'pm'
		  AND is_active = true
	`, project.ID).Scan(&activePMCount); err != nil {
		t.Fatalf("count active pm rows: %v", err)
	}
	if activePMCount != 1 {
		t.Fatalf("active pm rows = %d, want 1", activePMCount)
	}
}

func TestAgentAssignmentHTTPOrgIsolationAndRBAC(t *testing.T) {
	testServer, org, adminUser, memberUser := newAgentTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	memberToken := loginToken(t, testServer.URL, memberUser.Email, "member-password")

	otherOrg, otherAgent := seedAssignmentForeignOrgAgent(t, testServer.Pool)
	_ = otherOrg
	project := seedAssignmentProject(t, testServer.Pool, org.ID)
	localAgent := seedActiveAssignmentAgent(t, testServer.Pool, org.ID, "rbac-agent")
	localSkill := seedAssignmentSkillRecord(t, testServer.Pool, org.ID, "rbac-skill")

	unauth := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents/"+localAgent.ID.String()+"/project-assignments", nil, nil)
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d, want %d body=%s", unauth.StatusCode, http.StatusUnauthorized, string(unauth.Body))
	}

	memberWrite := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents/"+localAgent.ID.String()+"/skills", map[string]any{
		"skill_id": localSkill.ID.String(),
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if memberWrite.StatusCode != http.StatusForbidden {
		t.Fatalf("member write status = %d, want %d body=%s", memberWrite.StatusCode, http.StatusForbidden, string(memberWrite.Body))
	}

	crossOrgAssign := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents/"+otherAgent.ID.String()+"/project-assignments", map[string]any{
		"project_id": project.ID.String(),
		"role":       "pm",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if crossOrgAssign.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-org assign status = %d, want %d body=%s", crossOrgAssign.StatusCode, http.StatusNotFound, string(crossOrgAssign.Body))
	}

	crossOrgList := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents/"+otherAgent.ID.String()+"/project-assignments", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if crossOrgList.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-org list status = %d, want %d body=%s", crossOrgList.StatusCode, http.StatusNotFound, string(crossOrgList.Body))
	}
}

func seedAssignmentProject(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) repo.Project {
	t.Helper()

	projectRepo := repo.NewProjectRepo(pool)
	project, err := projectRepo.Create(context.Background(), repo.Project{
		OrganizationID: orgID,
		Slug:           "proj-" + uuid.NewString()[:8],
		DisplayName:    "Project " + uuid.NewString()[:8],
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project
}

func seedActiveAssignmentAgent(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, name string) repo.Agent {
	t.Helper()

	agentRepo := repo.NewAgentRepo(pool)
	created, err := agentRepo.Create(context.Background(), repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          name + "-" + uuid.NewString()[:8],
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return created
}

func seedAssignmentSkillRecord(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, slug string) repo.Skill {
	t.Helper()

	skillRepo := repo.NewSkillRepo(pool)
	created, err := skillRepo.Create(context.Background(), repo.Skill{
		OrganizationID: orgID,
		Slug:           slug + "-" + uuid.NewString()[:8],
		DisplayName:    slug,
		Description:    "desc",
		FilePath:       "skills/" + slug + ".md",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	return created
}

func seedAssignmentForeignOrgAgent(t *testing.T, pool *pgxpool.Pool) (repo.Organization, repo.Agent) {
	t.Helper()

	orgRepo := repo.NewOrgRepo(pool)
	agentRepo := repo.NewAgentRepo(pool)
	org, err := orgRepo.Create(context.Background(), repo.Organization{
		Slug:        "foreign-org-" + uuid.NewString()[:8],
		DisplayName: "Foreign Org",
	})
	if err != nil {
		t.Fatalf("create foreign org: %v", err)
	}
	agentRecord, err := agentRepo.Create(context.Background(), repo.Agent{
		OrganizationID:       org.ID,
		DisplayName:          "foreign-agent-" + uuid.NewString()[:8],
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create foreign agent: %v", err)
	}
	return org, agentRecord
}
