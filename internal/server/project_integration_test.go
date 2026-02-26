//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestProjectHTTPCRUDAndRBAC(t *testing.T) {
	testServer, org, adminUser, memberUser := newProjectTestServer(t)
	defer testServer.Close()

	unauth := mustJSON(t, http.MethodGet, testServer.URL+"/v1/projects", nil, nil)
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth list status = %d, want %d body=%s", unauth.StatusCode, http.StatusUnauthorized, string(unauth.Body))
	}

	memberToken := loginToken(t, testServer.URL, memberUser.Email, "member-password")
	memberCreate := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects", map[string]any{
		"slug":          "member-project",
		"display_name":  "Member Project",
		"delivery_mode": "gated",
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if memberCreate.StatusCode != http.StatusForbidden {
		t.Fatalf("member create status = %d, want %d body=%s", memberCreate.StatusCode, http.StatusForbidden, string(memberCreate.Body))
	}

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects", map[string]any{
		"slug":          "api-project",
		"display_name":  "API Project",
		"description":   "project from api",
		"delivery_mode": "gated",
		"settings": map[string]any{
			"tier": "test",
		},
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create project status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
	projectID := jsonPathString(t, created.Body, "data", "id")
	if projectID == "" {
		t.Fatalf("missing project id in create response: %s", string(created.Body))
	}

	got := mustJSON(t, http.MethodGet, testServer.URL+"/v1/projects/"+projectID, nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if got.StatusCode != http.StatusOK {
		t.Fatalf("get project status = %d, want %d body=%s", got.StatusCode, http.StatusOK, string(got.Body))
	}
	if slug := jsonPathString(t, got.Body, "data", "slug"); slug != "api-project" {
		t.Fatalf("slug = %q, want %q body=%s", slug, "api-project", string(got.Body))
	}

	dup := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects", map[string]any{
		"slug":          "api-project",
		"display_name":  "API Project Dup",
		"delivery_mode": "gated",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if dup.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate slug status = %d, want %d body=%s", dup.StatusCode, http.StatusConflict, string(dup.Body))
	}

	memberDelete := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/projects/"+projectID, nil, map[string]string{
		"Authorization": "Bearer " + memberToken,
	})
	if memberDelete.StatusCode != http.StatusForbidden {
		t.Fatalf("member delete status = %d, want %d body=%s", memberDelete.StatusCode, http.StatusForbidden, string(memberDelete.Body))
	}

	ensureProjectTaskTableForHTTP(t, testServer.Pool)
	if _, err := testServer.Pool.Exec(context.Background(), `
		INSERT INTO project_task (
			organization_id,
			project_id,
			task_number,
			title,
			work_status,
			created_by_type,
			metadata
		)
		VALUES ($1, $2, $3, $4, 'in_progress', 'system', '{}'::jsonb)
	`, org.ID, projectID, 1, "active task"); err != nil {
		t.Fatalf("insert active project_task row: %v", err)
	}

	deleteWithActiveTasks := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/projects/"+projectID, nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if deleteWithActiveTasks.StatusCode != http.StatusConflict {
		t.Fatalf("delete with active tasks status = %d, want %d body=%s", deleteWithActiveTasks.StatusCode, http.StatusConflict, string(deleteWithActiveTasks.Body))
	}
	if code := jsonPathString(t, deleteWithActiveTasks.Body, "error", "code"); code != "project_has_active_tasks" {
		t.Fatalf("error.code = %q, want %q body=%s", code, "project_has_active_tasks", string(deleteWithActiveTasks.Body))
	}

	if _, err := testServer.Pool.Exec(context.Background(), `
		DELETE FROM project_task
		WHERE project_id = $1
	`, projectID); err != nil {
		t.Fatalf("clear project_task rows: %v", err)
	}

	deleted := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/projects/"+projectID, nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete project status = %d, want %d body=%s", deleted.StatusCode, http.StatusNoContent, string(deleted.Body))
	}
}

func TestProjectHTTPFlowTemplateScheduleAndNodes(t *testing.T) {
	testServer, org, adminUser, memberUser := newProjectTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	memberToken := loginToken(t, testServer.URL, memberUser.Email, "member-password")

	createdProject := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects", map[string]any{
		"slug":          "template-proj",
		"display_name":  "Template Project",
		"delivery_mode": "gated",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createdProject.StatusCode != http.StatusCreated {
		t.Fatalf("create project status = %d, want %d body=%s", createdProject.StatusCode, http.StatusCreated, string(createdProject.Body))
	}
	projectID := jsonPathString(t, createdProject.Body, "data", "id")

	projectTemplate := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+projectID+"/flow-templates", map[string]any{
		"slug":         "build-flow",
		"display_name": "Build Flow",
		"description":  "v1",
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if projectTemplate.StatusCode != http.StatusCreated {
		t.Fatalf("create flow template status = %d, want %d body=%s", projectTemplate.StatusCode, http.StatusCreated, string(projectTemplate.Body))
	}
	templateID := jsonPathString(t, projectTemplate.Body, "data", "id")

	// System templates are immutable through the API.
	systemTemplate, err := repo.NewFlowTemplateRepo(testServer.Pool).Create(context.Background(), repo.FlowTemplate{
		OrganizationID: nil,
		ProjectID:      nil,
		Slug:           "system-protected-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		DisplayName:    "System Protected",
		Description:    "",
		IsSystem:       true,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create system template: %v", err)
	}
	patchSystemMember := mustJSON(t, http.MethodPatch, testServer.URL+"/v1/flow-templates/"+systemTemplate.ID.String(), map[string]any{
		"display_name": "Nope",
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if patchSystemMember.StatusCode != http.StatusForbidden {
		t.Fatalf("member patch system template status = %d, want %d body=%s", patchSystemMember.StatusCode, http.StatusForbidden, string(patchSystemMember.Body))
	}

	patchSystemAdmin := mustJSON(t, http.MethodPatch, testServer.URL+"/v1/flow-templates/"+systemTemplate.ID.String(), map[string]any{
		"display_name": "System Updated",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if patchSystemAdmin.StatusCode != http.StatusOK {
		t.Fatalf("admin patch system template status = %d, want %d body=%s", patchSystemAdmin.StatusCode, http.StatusOK, string(patchSystemAdmin.Body))
	}
	if got := jsonPathString(t, patchSystemAdmin.Body, "data", "display_name"); got != "System Updated" {
		t.Fatalf("system template display_name = %q, want %q body=%s", got, "System Updated", string(patchSystemAdmin.Body))
	}

	memberSystemNodeCreate := mustJSON(t, http.MethodPost, testServer.URL+"/v1/flow-templates/"+systemTemplate.ID.String()+"/nodes", map[string]any{
		"display_name": "System Node",
		"node_type":    "work",
		"position":     0,
		"max_visits":   10,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if memberSystemNodeCreate.StatusCode != http.StatusForbidden {
		t.Fatalf("member create system node status = %d, want %d body=%s", memberSystemNodeCreate.StatusCode, http.StatusForbidden, string(memberSystemNodeCreate.Body))
	}

	ensureProjectTaskTableForHTTP(t, testServer.Pool)
	if _, err := testServer.Pool.Exec(context.Background(), `
		INSERT INTO project_task (
			organization_id,
			project_id,
			task_number,
			title,
			work_status,
			flow_template_id,
			created_by_type,
			metadata
		)
		VALUES ($1, $2, $3, $4, 'in_progress', $5, 'system', '{}'::jsonb)
	`, org.ID, projectID, 1, "active task", templateID); err != nil {
		t.Fatalf("insert template in-use row: %v", err)
	}

	updatedTemplate := mustJSON(t, http.MethodPatch, testServer.URL+"/v1/flow-templates/"+templateID, map[string]any{
		"display_name": "Build Flow v2",
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if updatedTemplate.StatusCode != http.StatusOK {
		t.Fatalf("update flow template status = %d, want %d body=%s", updatedTemplate.StatusCode, http.StatusOK, string(updatedTemplate.Body))
	}
	newTemplateID := jsonPathString(t, updatedTemplate.Body, "data", "id")
	if newTemplateID == templateID {
		t.Fatalf("template id did not change for in-use update body=%s", string(updatedTemplate.Body))
	}
	if version := jsonPathValue(t, updatedTemplate.Body, "data", "version"); version.(float64) != 2 {
		t.Fatalf("version = %v, want 2 body=%s", version, string(updatedTemplate.Body))
	}

	invalidCron := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+projectID+"/schedules", map[string]any{
		"display_name":     "Bad Cron",
		"flow_template_id": newTemplateID,
		"cron_expression":  "invalid cron",
		"overlap_policy":   "skip",
		"is_enabled":       true,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if invalidCron.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid cron status = %d, want %d body=%s", invalidCron.StatusCode, http.StatusUnprocessableEntity, string(invalidCron.Body))
	}
	if code := jsonPathString(t, invalidCron.Body, "error", "code"); code != "invalid_cron_expression" {
		t.Fatalf("invalid cron error.code = %q, want %q body=%s", code, "invalid_cron_expression", string(invalidCron.Body))
	}

	validSchedule := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+projectID+"/schedules", map[string]any{
		"display_name":     "Daily Standup",
		"flow_template_id": newTemplateID,
		"cron_expression":  "0 9 * * 1-5",
		"overlap_policy":   "skip",
		"is_enabled":       true,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if validSchedule.StatusCode != http.StatusCreated {
		t.Fatalf("create schedule status = %d, want %d body=%s", validSchedule.StatusCode, http.StatusCreated, string(validSchedule.Body))
	}
	scheduleID := jsonPathString(t, validSchedule.Body, "data", "id")
	if scheduleID == "" {
		t.Fatalf("missing schedule id body=%s", string(validSchedule.Body))
	}
	if !hasJSONPath(validSchedule.Body, "data", "next_fire_at") {
		t.Fatalf("missing next_fire_at body=%s", string(validSchedule.Body))
	}

	disabledSchedule := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+projectID+"/schedules/"+scheduleID+"/disable", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + memberToken,
	})
	if disabledSchedule.StatusCode != http.StatusOK {
		t.Fatalf("disable schedule status = %d, want %d body=%s", disabledSchedule.StatusCode, http.StatusOK, string(disabledSchedule.Body))
	}
	if enabled := jsonPathValue(t, disabledSchedule.Body, "data", "enabled"); enabled.(bool) {
		t.Fatalf("disable response enabled = %v, want false body=%s", enabled, string(disabledSchedule.Body))
	}

	enabledSchedule := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+projectID+"/schedules/"+scheduleID+"/enable", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + memberToken,
	})
	if enabledSchedule.StatusCode != http.StatusOK {
		t.Fatalf("enable schedule status = %d, want %d body=%s", enabledSchedule.StatusCode, http.StatusOK, string(enabledSchedule.Body))
	}
	if !hasJSONPath(enabledSchedule.Body, "data", "next_run_at") {
		t.Fatalf("enable response missing next_run_at body=%s", string(enabledSchedule.Body))
	}

	nodeA := mustJSON(t, http.MethodPost, testServer.URL+"/v1/flow-templates/"+newTemplateID+"/nodes", map[string]any{
		"display_name":          "Node A",
		"node_type":             "review",
		"actor_type":            "human",
		"requires_human_review": true,
		"position":              2,
		"mcp_tools":             []any{},
		"tool_domains":          []any{},
		"max_visits":            10,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if nodeA.StatusCode != http.StatusCreated {
		t.Fatalf("create node A status = %d, want %d body=%s", nodeA.StatusCode, http.StatusCreated, string(nodeA.Body))
	}
	nodeAID := jsonPathString(t, nodeA.Body, "data", "id")

	nodeB := mustJSON(t, http.MethodPost, testServer.URL+"/v1/flow-templates/"+newTemplateID+"/nodes", map[string]any{
		"display_name":          "Node B",
		"node_type":             "work",
		"requires_human_review": false,
		"position":              1,
		"mcp_tools":             []any{},
		"tool_domains":          []any{},
		"max_visits":            10,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if nodeB.StatusCode != http.StatusCreated {
		t.Fatalf("create node B status = %d, want %d body=%s", nodeB.StatusCode, http.StatusCreated, string(nodeB.Body))
	}
	nodeBID := jsonPathString(t, nodeB.Body, "data", "id")

	nodesInitial := mustJSON(t, http.MethodGet, testServer.URL+"/v1/flow-templates/"+newTemplateID+"/nodes", nil, map[string]string{
		"Authorization": "Bearer " + memberToken,
	})
	if nodesInitial.StatusCode != http.StatusOK {
		t.Fatalf("list nodes status = %d, want %d body=%s", nodesInitial.StatusCode, http.StatusOK, string(nodesInitial.Body))
	}
	if got := jsonPathString(t, nodesInitial.Body, "data", "0", "id"); got != nodeBID {
		t.Fatalf("first node id = %q, want %q body=%s", got, nodeBID, string(nodesInitial.Body))
	}

	updateNodeA := mustJSON(t, http.MethodPatch, testServer.URL+"/v1/flow-templates/"+newTemplateID+"/nodes/"+nodeAID, map[string]any{
		"position": 0,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if updateNodeA.StatusCode != http.StatusOK {
		t.Fatalf("update node A status = %d, want %d body=%s", updateNodeA.StatusCode, http.StatusOK, string(updateNodeA.Body))
	}

	nodesAfterReorder := mustJSON(t, http.MethodGet, testServer.URL+"/v1/flow-templates/"+newTemplateID+"/nodes", nil, map[string]string{
		"Authorization": "Bearer " + memberToken,
	})
	if nodesAfterReorder.StatusCode != http.StatusOK {
		t.Fatalf("list nodes after reorder status = %d, want %d body=%s", nodesAfterReorder.StatusCode, http.StatusOK, string(nodesAfterReorder.Body))
	}
	if got := jsonPathString(t, nodesAfterReorder.Body, "data", "0", "id"); got != nodeAID {
		t.Fatalf("first node after reorder id = %q, want %q body=%s", got, nodeAID, string(nodesAfterReorder.Body))
	}

	deleteNodeB := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/flow-templates/"+newTemplateID+"/nodes/"+nodeBID, nil, map[string]string{
		"Authorization": "Bearer " + memberToken,
	})
	if deleteNodeB.StatusCode != http.StatusNoContent {
		t.Fatalf("delete node B status = %d, want %d body=%s", deleteNodeB.StatusCode, http.StatusNoContent, string(deleteNodeB.Body))
	}

	nodesAfterDelete := mustJSON(t, http.MethodGet, testServer.URL+"/v1/flow-templates/"+newTemplateID+"/nodes", nil, map[string]string{
		"Authorization": "Bearer " + memberToken,
	})
	if nodesAfterDelete.StatusCode != http.StatusOK {
		t.Fatalf("list nodes after delete status = %d, want %d body=%s", nodesAfterDelete.StatusCode, http.StatusOK, string(nodesAfterDelete.Body))
	}
	data, ok := jsonPathValue(t, nodesAfterDelete.Body, "data").([]any)
	if !ok {
		t.Fatalf("data is not an array body=%s", string(nodesAfterDelete.Body))
	}
	if len(data) != 1 {
		t.Fatalf("node count = %d, want 1 body=%s", len(data), string(nodesAfterDelete.Body))
	}
}

func TestProjectHTTPFlowNodeCreatePersistsNextNodeID(t *testing.T) {
	testServer, _, adminUser, memberUser := newProjectTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	memberToken := loginToken(t, testServer.URL, memberUser.Email, "member-password")

	createdProject := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects", map[string]any{
		"slug":          "template-next-" + strings.ToLower(uuid.NewString()[:8]),
		"display_name":  "Template Next",
		"delivery_mode": "gated",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createdProject.StatusCode != http.StatusCreated {
		t.Fatalf("create project status = %d, want %d body=%s", createdProject.StatusCode, http.StatusCreated, string(createdProject.Body))
	}
	projectID := jsonPathString(t, createdProject.Body, "data", "id")

	projectTemplate := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+projectID+"/flow-templates", map[string]any{
		"slug":         "build-flow-next",
		"display_name": "Build Flow Next",
		"description":  "v1",
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if projectTemplate.StatusCode != http.StatusCreated {
		t.Fatalf("create flow template status = %d, want %d body=%s", projectTemplate.StatusCode, http.StatusCreated, string(projectTemplate.Body))
	}
	templateID := jsonPathString(t, projectTemplate.Body, "data", "id")

	nodeA := mustJSON(t, http.MethodPost, testServer.URL+"/v1/flow-templates/"+templateID+"/nodes", map[string]any{
		"display_name":          "Node A",
		"node_type":             "work",
		"requires_human_review": false,
		"position":              0,
		"max_visits":            10,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if nodeA.StatusCode != http.StatusCreated {
		t.Fatalf("create node A status = %d, want %d body=%s", nodeA.StatusCode, http.StatusCreated, string(nodeA.Body))
	}
	nodeAID := jsonPathString(t, nodeA.Body, "data", "id")

	nodeB := mustJSON(t, http.MethodPost, testServer.URL+"/v1/flow-templates/"+templateID+"/nodes", map[string]any{
		"display_name":          "Node B",
		"node_type":             "review",
		"requires_human_review": true,
		"position":              1,
		"next_node_id":          nodeAID,
		"max_visits":            10,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if nodeB.StatusCode != http.StatusCreated {
		t.Fatalf("create node B status = %d, want %d body=%s", nodeB.StatusCode, http.StatusCreated, string(nodeB.Body))
	}
	if got := jsonPathString(t, nodeB.Body, "data", "next_node_id"); got != nodeAID {
		t.Fatalf("created node next_node_id = %q, want %q body=%s", got, nodeAID, string(nodeB.Body))
	}
}

func newProjectTestServer(t *testing.T) (*authIntegrationServer, repo.Organization, repo.HumanUser, repo.HumanUser) {
	t.Helper()

	pool := testdb.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	slug := "project-http-" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	projectService, err := projectsvc.NewService(projectsvc.Options{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("new project service: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      logger,
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []RouteRegistrar{
			NewProjectRouteRegistrar(projectService, nil),
		},
	})

	ts := httptest.NewServer(handler)
	return &authIntegrationServer{URL: ts.URL, Pool: pool, ts: ts}, org, adminUser, memberUser
}

func ensureProjectTaskTableForHTTP(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS project_task (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			organization_id uuid NOT NULL,
			project_id uuid NOT NULL,
			task_number integer NOT NULL DEFAULT 1,
			title text NOT NULL DEFAULT '',
			flow_template_id uuid,
			work_status text NOT NULL,
			created_by_type text NOT NULL DEFAULT 'system',
			metadata jsonb NOT NULL DEFAULT '{}'::jsonb
		)
	`); err != nil {
		t.Fatalf("create project_task table: %v", err)
	}
}
