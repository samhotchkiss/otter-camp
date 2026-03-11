//go:build integration

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
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

	seedHTTPActiveProjectTask(t, testServer.Pool, org.ID, uuid.MustParse(projectID))

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

func TestProjectHTTPListStatusFiltersAfterArchive(t *testing.T) {
	testServer, _, adminUser, _ := newProjectTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	createArchivedTarget := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects", map[string]any{
		"slug":          "archive-target",
		"display_name":  "Archive Target",
		"delivery_mode": "gated",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createArchivedTarget.StatusCode != http.StatusCreated {
		t.Fatalf("create archive target status = %d, want %d body=%s", createArchivedTarget.StatusCode, http.StatusCreated, string(createArchivedTarget.Body))
	}
	archivedTargetID := jsonPathString(t, createArchivedTarget.Body, "data", "id")

	createStillActive := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects", map[string]any{
		"slug":          "still-active",
		"display_name":  "Still Active",
		"delivery_mode": "gated",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createStillActive.StatusCode != http.StatusCreated {
		t.Fatalf("create still-active status = %d, want %d body=%s", createStillActive.StatusCode, http.StatusCreated, string(createStillActive.Body))
	}
	stillActiveID := jsonPathString(t, createStillActive.Body, "data", "id")

	archivedResp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+archivedTargetID+"/archive", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if archivedResp.StatusCode != http.StatusOK {
		t.Fatalf("archive status = %d, want %d body=%s", archivedResp.StatusCode, http.StatusOK, string(archivedResp.Body))
	}
	if got := jsonPathString(t, archivedResp.Body, "data", "status"); got != "archived" {
		t.Fatalf("archive response status = %q, want %q body=%s", got, "archived", string(archivedResp.Body))
	}

	defaultList := mustJSON(t, http.MethodGet, testServer.URL+"/v1/projects", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if defaultList.StatusCode != http.StatusOK {
		t.Fatalf("default list status = %d, want %d body=%s", defaultList.StatusCode, http.StatusOK, string(defaultList.Body))
	}
	defaultItems, ok := jsonPathValue(t, defaultList.Body, "data").([]any)
	if !ok {
		t.Fatalf("default list data is not an array body=%s", string(defaultList.Body))
	}
	if len(defaultItems) != 1 {
		t.Fatalf("default list count = %d, want 1 body=%s", len(defaultItems), string(defaultList.Body))
	}
	if got := jsonPathString(t, defaultList.Body, "data", "0", "id"); got != stillActiveID {
		t.Fatalf("default list first id = %q, want %q body=%s", got, stillActiveID, string(defaultList.Body))
	}

	archivedList := mustJSON(t, http.MethodGet, testServer.URL+"/v1/projects?status=archived", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if archivedList.StatusCode != http.StatusOK {
		t.Fatalf("archived list status = %d, want %d body=%s", archivedList.StatusCode, http.StatusOK, string(archivedList.Body))
	}
	archivedItems, ok := jsonPathValue(t, archivedList.Body, "data").([]any)
	if !ok {
		t.Fatalf("archived list data is not an array body=%s", string(archivedList.Body))
	}
	if len(archivedItems) != 1 {
		t.Fatalf("archived list count = %d, want 1 body=%s", len(archivedItems), string(archivedList.Body))
	}
	if got := jsonPathString(t, archivedList.Body, "data", "0", "id"); got != archivedTargetID {
		t.Fatalf("archived list first id = %q, want %q body=%s", got, archivedTargetID, string(archivedList.Body))
	}
	if got := jsonPathString(t, archivedList.Body, "data", "0", "status"); got != "archived" {
		t.Fatalf("archived list first status = %q, want %q body=%s", got, "archived", string(archivedList.Body))
	}

	allList := mustJSON(t, http.MethodGet, testServer.URL+"/v1/projects?status=all", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if allList.StatusCode != http.StatusOK {
		t.Fatalf("all list status = %d, want %d body=%s", allList.StatusCode, http.StatusOK, string(allList.Body))
	}
	allItems, ok := jsonPathValue(t, allList.Body, "data").([]any)
	if !ok {
		t.Fatalf("all list data is not an array body=%s", string(allList.Body))
	}
	if len(allItems) != 2 {
		t.Fatalf("all list count = %d, want 2 body=%s", len(allItems), string(allList.Body))
	}
	ids := make(map[string]bool, len(allItems))
	for i := range allItems {
		item, ok := allItems[i].(map[string]any)
		if !ok {
			t.Fatalf("all list item[%d] not an object body=%s", i, string(allList.Body))
		}
		id, _ := item["id"].(string)
		if id != "" {
			ids[id] = true
		}
	}
	if !ids[archivedTargetID] || !ids[stillActiveID] {
		t.Fatalf("all list ids = %+v, want both %s and %s body=%s", ids, archivedTargetID, stillActiveID, string(allList.Body))
	}

	invalidStatus := mustJSON(t, http.MethodGet, testServer.URL+"/v1/projects?status=unknown", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if invalidStatus.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid status code = %d, want %d body=%s", invalidStatus.StatusCode, http.StatusUnprocessableEntity, string(invalidStatus.Body))
	}
	if msg := jsonPathString(t, invalidStatus.Body, "error", "message"); msg != "status must be one of active, archived, all" {
		t.Fatalf("invalid status message = %q, want %q body=%s", msg, "status must be one of active, archived, all", string(invalidStatus.Body))
	}
}

func TestProjectHTTPPauseResumeExposesPausedState(t *testing.T) {
	testServer, _, adminUser, _ := newProjectTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects", map[string]any{
		"slug":          "paused-project",
		"display_name":  "Paused Project",
		"delivery_mode": "gated",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create project status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
	projectID := jsonPathString(t, created.Body, "data", "id")

	paused := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+projectID+"/pause", map[string]any{
		"reason": "operator pause",
		"metadata": map[string]any{
			"source": "integration-test",
		},
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if paused.StatusCode != http.StatusOK {
		t.Fatalf("pause project status = %d, want %d body=%s", paused.StatusCode, http.StatusOK, string(paused.Body))
	}
	if !jsonPathBoolValue(t, paused.Body, "data", "is_paused") {
		t.Fatalf("pause response is_paused = false, want true body=%s", string(paused.Body))
	}
	if got := jsonPathString(t, paused.Body, "data", "pause_reason"); got != "operator pause" {
		t.Fatalf("pause_reason = %q, want %q body=%s", got, "operator pause", string(paused.Body))
	}
	if got := jsonPathString(t, paused.Body, "data", "pause_metadata", "source"); got != "integration-test" {
		t.Fatalf("pause_metadata.source = %q, want %q body=%s", got, "integration-test", string(paused.Body))
	}

	got := mustJSON(t, http.MethodGet, testServer.URL+"/v1/projects/"+projectID, nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if got.StatusCode != http.StatusOK {
		t.Fatalf("get paused project status = %d, want %d body=%s", got.StatusCode, http.StatusOK, string(got.Body))
	}
	if !jsonPathBoolValue(t, got.Body, "data", "is_paused") {
		t.Fatalf("get response is_paused = false, want true body=%s", string(got.Body))
	}
	if gotReason := jsonPathString(t, got.Body, "data", "pause_reason"); gotReason != "operator pause" {
		t.Fatalf("get pause_reason = %q, want %q body=%s", gotReason, "operator pause", string(got.Body))
	}

	list := mustJSON(t, http.MethodGet, testServer.URL+"/v1/projects", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list projects status = %d, want %d body=%s", list.StatusCode, http.StatusOK, string(list.Body))
	}
	if !jsonPathBoolValue(t, list.Body, "data", "0", "is_paused") {
		t.Fatalf("list response is_paused = false, want true body=%s", string(list.Body))
	}
	if got := jsonPathString(t, list.Body, "data", "0", "pause_metadata", "source"); got != "integration-test" {
		t.Fatalf("list pause_metadata.source = %q, want %q body=%s", got, "integration-test", string(list.Body))
	}

	resumed := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+projectID+"/resume", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if resumed.StatusCode != http.StatusOK {
		t.Fatalf("resume project status = %d, want %d body=%s", resumed.StatusCode, http.StatusOK, string(resumed.Body))
	}
	if jsonPathBoolValue(t, resumed.Body, "data", "is_paused") {
		t.Fatalf("resume response is_paused = true, want false body=%s", string(resumed.Body))
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

	seedHTTPTemplateUsageTask(t, testServer.Pool, org.ID, uuid.MustParse(projectID), uuid.MustParse(templateID))

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

func TestProjectHTTPFlowNodeCreateAcceptsLabelOrdinalAndListsLabel(t *testing.T) {
	testServer, _, adminUser, memberUser := newProjectTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	memberToken := loginToken(t, testServer.URL, memberUser.Email, "member-password")

	createdProject := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects", map[string]any{
		"slug":          "node-label-" + strings.ToLower(uuid.NewString()[:8]),
		"display_name":  "Node Label Project",
		"delivery_mode": "gated",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createdProject.StatusCode != http.StatusCreated {
		t.Fatalf("create project status = %d, want %d body=%s", createdProject.StatusCode, http.StatusCreated, string(createdProject.Body))
	}
	projectID := jsonPathString(t, createdProject.Body, "data", "id")

	templateResp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+projectID+"/flow-templates", map[string]any{
		"slug":         "label-flow",
		"display_name": "Label Flow",
		"description":  "label test",
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if templateResp.StatusCode != http.StatusCreated {
		t.Fatalf("create flow template status = %d, want %d body=%s", templateResp.StatusCode, http.StatusCreated, string(templateResp.Body))
	}
	templateID := jsonPathString(t, templateResp.Body, "data", "id")

	workNode := mustJSON(t, http.MethodPost, testServer.URL+"/v1/flow-templates/"+templateID+"/nodes", map[string]any{
		"label":     "Code",
		"node_type": "work",
		"ordinal":   1,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if workNode.StatusCode != http.StatusCreated {
		t.Fatalf("create work node status = %d, want %d body=%s", workNode.StatusCode, http.StatusCreated, string(workNode.Body))
	}
	if got := jsonPathString(t, workNode.Body, "data", "label"); got != "Code" {
		t.Fatalf("work node label = %q, want %q body=%s", got, "Code", string(workNode.Body))
	}
	if got := jsonPathString(t, workNode.Body, "data", "display_name"); got != "Code" {
		t.Fatalf("work node display_name = %q, want %q body=%s", got, "Code", string(workNode.Body))
	}

	reviewNode := mustJSON(t, http.MethodPost, testServer.URL+"/v1/flow-templates/"+templateID+"/nodes", map[string]any{
		"label":     "Review",
		"node_type": "review",
		"ordinal":   2,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if reviewNode.StatusCode != http.StatusCreated {
		t.Fatalf("create review node status = %d, want %d body=%s", reviewNode.StatusCode, http.StatusCreated, string(reviewNode.Body))
	}

	nodes := mustJSON(t, http.MethodGet, testServer.URL+"/v1/flow-templates/"+templateID+"/nodes", nil, map[string]string{
		"Authorization": "Bearer " + memberToken,
	})
	if nodes.StatusCode != http.StatusOK {
		t.Fatalf("list nodes status = %d, want %d body=%s", nodes.StatusCode, http.StatusOK, string(nodes.Body))
	}
	if got := jsonPathString(t, nodes.Body, "data", "0", "label"); got != "Code" {
		t.Fatalf("node[0].label = %q, want %q body=%s", got, "Code", string(nodes.Body))
	}
	if got := jsonPathString(t, nodes.Body, "data", "0", "node_type"); got != "work" {
		t.Fatalf("node[0].node_type = %q, want %q body=%s", got, "work", string(nodes.Body))
	}

	listData, ok := jsonPathValue(t, nodes.Body, "data").([]any)
	if !ok || len(listData) < 2 {
		t.Fatalf("list nodes data malformed body=%s", string(nodes.Body))
	}
	second, ok := listData[1].(map[string]any)
	if !ok {
		t.Fatalf("node[1] is not object body=%s", string(nodes.Body))
	}
	if got, _ := second["label"].(string); got != "Review" {
		t.Fatalf("node[1].label = %q, want %q body=%s", got, "Review", string(nodes.Body))
	}
	if got, _ := second["node_type"].(string); got != "review" {
		t.Fatalf("node[1].node_type = %q, want %q body=%s", got, "review", string(nodes.Body))
	}
}

func TestProjectHTTPFlowNodeCreateNormalizesHumanReviewAndCompletionAliases(t *testing.T) {
	testServer, _, adminUser, memberUser := newProjectTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	memberToken := loginToken(t, testServer.URL, memberUser.Email, "member-password")

	createdProject := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects", map[string]any{
		"slug":          "node-alias-" + strings.ToLower(uuid.NewString()[:8]),
		"display_name":  "Node Alias Project",
		"delivery_mode": "gated",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createdProject.StatusCode != http.StatusCreated {
		t.Fatalf("create project status = %d, want %d body=%s", createdProject.StatusCode, http.StatusCreated, string(createdProject.Body))
	}
	projectID := jsonPathString(t, createdProject.Body, "data", "id")

	templateResp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+projectID+"/flow-templates", map[string]any{
		"slug":         "alias-flow",
		"display_name": "Alias Flow",
		"description":  "alias test",
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if templateResp.StatusCode != http.StatusCreated {
		t.Fatalf("create flow template status = %d, want %d body=%s", templateResp.StatusCode, http.StatusCreated, string(templateResp.Body))
	}
	templateID := jsonPathString(t, templateResp.Body, "data", "id")

	completionNode := mustJSON(t, http.MethodPost, testServer.URL+"/v1/flow-templates/"+templateID+"/nodes", map[string]any{
		"display_name": "Completion",
		"node_type":    "completion",
		"position":     3,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if completionNode.StatusCode != http.StatusCreated {
		t.Fatalf("create completion node status = %d, want %d body=%s", completionNode.StatusCode, http.StatusCreated, string(completionNode.Body))
	}
	completionNodeID := jsonPathString(t, completionNode.Body, "data", "id")
	if got := jsonPathString(t, completionNode.Body, "data", "node_type"); got != "merge" {
		t.Fatalf("completion node_type = %q, want merge body=%s", got, string(completionNode.Body))
	}

	humanReviewNode := mustJSON(t, http.MethodPost, testServer.URL+"/v1/flow-templates/"+templateID+"/nodes", map[string]any{
		"display_name": "Human Review",
		"node_type":    "human_review",
		"position":     2,
		"next_node_id": completionNodeID,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if humanReviewNode.StatusCode != http.StatusCreated {
		t.Fatalf("create human review node status = %d, want %d body=%s", humanReviewNode.StatusCode, http.StatusCreated, string(humanReviewNode.Body))
	}
	humanReviewNodeID := jsonPathString(t, humanReviewNode.Body, "data", "id")
	if got := jsonPathString(t, humanReviewNode.Body, "data", "node_type"); got != "review" {
		t.Fatalf("human review node_type = %q, want review body=%s", got, string(humanReviewNode.Body))
	}
	if got := jsonPathValue(t, humanReviewNode.Body, "data", "requires_human_review"); got != true {
		t.Fatalf("requires_human_review = %v, want true body=%s", got, string(humanReviewNode.Body))
	}

	workNode := mustJSON(t, http.MethodPost, testServer.URL+"/v1/flow-templates/"+templateID+"/nodes", map[string]any{
		"display_name": "Work",
		"node_type":    "work",
		"position":     1,
		"next_node_id": humanReviewNodeID,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if workNode.StatusCode != http.StatusCreated {
		t.Fatalf("create work node status = %d, want %d body=%s", workNode.StatusCode, http.StatusCreated, string(workNode.Body))
	}
	workNodeID := jsonPathString(t, workNode.Body, "data", "id")

	updateTemplate := mustJSON(t, http.MethodPatch, testServer.URL+"/v1/flow-templates/"+templateID, map[string]any{
		"start_node_id": workNodeID,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if updateTemplate.StatusCode != http.StatusOK {
		t.Fatalf("update flow template status = %d, want %d body=%s", updateTemplate.StatusCode, http.StatusOK, string(updateTemplate.Body))
	}
}

func TestProjectHTTPFlowNodeCreateRejectsInvalidNodeType(t *testing.T) {
	testServer, _, adminUser, memberUser := newProjectTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	memberToken := loginToken(t, testServer.URL, memberUser.Email, "member-password")

	createdProject := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects", map[string]any{
		"slug":          "invalid-node-type-" + strings.ToLower(uuid.NewString()[:8]),
		"display_name":  "Invalid Node Type Project",
		"delivery_mode": "gated",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createdProject.StatusCode != http.StatusCreated {
		t.Fatalf("create project status = %d, want %d body=%s", createdProject.StatusCode, http.StatusCreated, string(createdProject.Body))
	}
	projectID := jsonPathString(t, createdProject.Body, "data", "id")

	templateResp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+projectID+"/flow-templates", map[string]any{
		"slug":         "invalid-node-type-flow",
		"display_name": "Invalid Node Type Flow",
		"description":  "invalid node type test",
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if templateResp.StatusCode != http.StatusCreated {
		t.Fatalf("create flow template status = %d, want %d body=%s", templateResp.StatusCode, http.StatusCreated, string(templateResp.Body))
	}
	templateID := jsonPathString(t, templateResp.Body, "data", "id")

	invalidNode := mustJSON(t, http.MethodPost, testServer.URL+"/v1/flow-templates/"+templateID+"/nodes", map[string]any{
		"display_name": "QA Gate",
		"node_type":    "qa_gate",
		"position":     1,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if invalidNode.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("create invalid node status = %d, want %d body=%s", invalidNode.StatusCode, http.StatusUnprocessableEntity, string(invalidNode.Body))
	}
	if got := jsonPathString(t, invalidNode.Body, "error", "message"); got != "invalid flow node type \"qa_gate\"" {
		t.Fatalf("error.message = %q, want invalid flow node type guidance body=%s", got, string(invalidNode.Body))
	}
}

func TestProjectHTTPFlowTemplateRejectsPathsWithoutReview(t *testing.T) {
	testServer, _, adminUser, memberUser := newProjectTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	memberToken := loginToken(t, testServer.URL, memberUser.Email, "member-password")

	createdProject := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects", map[string]any{
		"slug":          "template-review-gate-" + strings.ToLower(uuid.NewString()[:8]),
		"display_name":  "Template Review Gate",
		"delivery_mode": "gated",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createdProject.StatusCode != http.StatusCreated {
		t.Fatalf("create project status = %d, want %d body=%s", createdProject.StatusCode, http.StatusCreated, string(createdProject.Body))
	}
	projectID := jsonPathString(t, createdProject.Body, "data", "id")

	projectTemplate := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects/"+projectID+"/flow-templates", map[string]any{
		"slug":         "review-gate-flow",
		"display_name": "Review Gate Flow",
		"description":  "validation",
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if projectTemplate.StatusCode != http.StatusCreated {
		t.Fatalf("create flow template status = %d, want %d body=%s", projectTemplate.StatusCode, http.StatusCreated, string(projectTemplate.Body))
	}
	templateID := jsonPathString(t, projectTemplate.Body, "data", "id")

	terminalNode := mustJSON(t, http.MethodPost, testServer.URL+"/v1/flow-templates/"+templateID+"/nodes", map[string]any{
		"display_name": "Merge",
		"node_type":    "merge",
		"position":     2,
		"max_visits":   10,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if terminalNode.StatusCode != http.StatusCreated {
		t.Fatalf("create terminal node status = %d, want %d body=%s", terminalNode.StatusCode, http.StatusCreated, string(terminalNode.Body))
	}
	terminalNodeID := jsonPathString(t, terminalNode.Body, "data", "id")

	workNode := mustJSON(t, http.MethodPost, testServer.URL+"/v1/flow-templates/"+templateID+"/nodes", map[string]any{
		"display_name": "Work",
		"node_type":    "work",
		"position":     1,
		"next_node_id": terminalNodeID,
		"max_visits":   10,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if workNode.StatusCode != http.StatusCreated {
		t.Fatalf("create work node status = %d, want %d body=%s", workNode.StatusCode, http.StatusCreated, string(workNode.Body))
	}
	workNodeID := jsonPathString(t, workNode.Body, "data", "id")

	invalidStart := mustJSON(t, http.MethodPatch, testServer.URL+"/v1/flow-templates/"+templateID, map[string]any{
		"start_node_id": workNodeID,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if invalidStart.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid start status = %d, want %d body=%s", invalidStart.StatusCode, http.StatusUnprocessableEntity, string(invalidStart.Body))
	}
	if got := jsonPathString(t, invalidStart.Body, "error", "message"); got != "flow template must define a work -> review -> completion path" {
		t.Fatalf("error.message = %q body=%s", got, string(invalidStart.Body))
	}

	reviewNode := mustJSON(t, http.MethodPost, testServer.URL+"/v1/flow-templates/"+templateID+"/nodes", map[string]any{
		"display_name":   "Review",
		"node_type":      "review",
		"position":       3,
		"next_node_id":   terminalNodeID,
		"reject_node_id": workNodeID,
		"max_visits":     10,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if reviewNode.StatusCode != http.StatusCreated {
		t.Fatalf("create review node status = %d, want %d body=%s", reviewNode.StatusCode, http.StatusCreated, string(reviewNode.Body))
	}
	reviewNodeID := jsonPathString(t, reviewNode.Body, "data", "id")

	updateWork := mustJSON(t, http.MethodPatch, testServer.URL+"/v1/flow-templates/"+templateID+"/nodes/"+workNodeID, map[string]any{
		"next_node_id": reviewNodeID,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if updateWork.StatusCode != http.StatusOK {
		t.Fatalf("update work node status = %d, want %d body=%s", updateWork.StatusCode, http.StatusOK, string(updateWork.Body))
	}

	validStart := mustJSON(t, http.MethodPatch, testServer.URL+"/v1/flow-templates/"+templateID, map[string]any{
		"start_node_id": workNodeID,
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if validStart.StatusCode != http.StatusOK {
		t.Fatalf("valid start status = %d, want %d body=%s", validStart.StatusCode, http.StatusOK, string(validStart.Body))
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

func seedHTTPActiveProjectTask(t *testing.T, pool *pgxpool.Pool, orgID, projectID uuid.UUID) repo.ProjectTask {
	t.Helper()

	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)
	taskRepo := repo.NewProjectTaskRepo(pool)

	template, err := templateRepo.Create(context.Background(), repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "project-http-active-" + uuid.NewString()[:8],
		DisplayName:    "Project Active Flow",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create active flow template: %v", err)
	}
	workNode, err := nodeRepo.Create(context.Background(), repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create active work node: %v", err)
	}
	reviewNode, err := nodeRepo.Create(context.Background(), repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       2,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create active review node: %v", err)
	}
	mergeNode, err := nodeRepo.Create(context.Background(), repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Merge",
		NodeType:       "merge",
		Position:       3,
		MaxVisits:      1,
	})
	if err != nil {
		t.Fatalf("create active merge node: %v", err)
	}
	workNode.NextNodeID = &reviewNode.ID
	if _, err := nodeRepo.Update(context.Background(), workNode); err != nil {
		t.Fatalf("link active work node: %v", err)
	}
	reviewNode.NextNodeID = &mergeNode.ID
	if _, err := nodeRepo.Update(context.Background(), reviewNode); err != nil {
		t.Fatalf("link active review node: %v", err)
	}
	template.StartNodeID = &workNode.ID
	if _, err := templateRepo.Update(context.Background(), template); err != nil {
		t.Fatalf("update active template start node: %v", err)
	}

	taskRecord, err := taskRepo.Create(context.Background(), repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      projectID,
		Title:          "active task",
		WorkStatus:     "in_progress",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create active project task: %v", err)
	}
	if _, err := taskRepo.SetFlowNode(context.Background(), taskRecord.ID, &workNode.ID); err != nil {
		t.Fatalf("set active project task flow node: %v", err)
	}
	taskRecord.CurrentFlowNodeID = &workNode.ID
	return taskRecord
}

func seedHTTPTemplateUsageTask(t *testing.T, pool *pgxpool.Pool, orgID, projectID, templateID uuid.UUID) repo.ProjectTask {
	t.Helper()

	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(context.Background(), repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      projectID,
		Title:          "template in use",
		WorkStatus:     "draft",
		FlowTemplateID: &templateID,
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create template usage task: %v", err)
	}
	return taskRecord
}
