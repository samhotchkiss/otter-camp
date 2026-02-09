package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRouterSetup(t *testing.T) {
	t.Parallel()

	router := NewRouter()

	for _, tc := range []struct {
		name   string
		target string
	}{
		{name: "health", target: "/health"},
		{name: "root", target: "/"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.target, nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected status %d, got %d", tc.name, http.StatusOK, rec.Code)
		}
	}
}

func TestCORSMiddleware(t *testing.T) {
	t.Parallel()

	router := NewRouter()
	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, X-Org-ID")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 200 or 204, got %d", rec.Code)
	}

	if allowOrigin := rec.Header().Get("Access-Control-Allow-Origin"); allowOrigin == "" {
		t.Fatalf("expected Access-Control-Allow-Origin to be set")
	}

	if allowMethods := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(allowMethods, http.MethodGet) {
		t.Fatalf("expected Access-Control-Allow-Methods to include GET, got %q", allowMethods)
	}

	if allowHeaders := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(allowHeaders), "x-org-id") {
		t.Fatalf("expected Access-Control-Allow-Headers to include X-Org-ID, got %q", allowHeaders)
	}
}

func TestJSONContentType(t *testing.T) {
	t.Parallel()

	router := NewRouter()

	for _, target := range []string{"/health", "/"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("%s: expected content-type application/json, got %q", target, ct)
		}
	}
}

func TestNotFoundHandler(t *testing.T) {
	t.Parallel()

	router := NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestMessagesRouteIsRegistered(t *testing.T) {
	t.Parallel()

	router := NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/messages?thread_id=dm_test", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("expected /api/messages route to be registered, got status %d", rec.Code)
	}
}

func TestProjectsAndInboxRoutesAreRegistered(t *testing.T) {
	t.Parallel()

	router := NewRouter()
	orgID := "00000000-0000-0000-0000-000000000001"

	reqProjects := httptest.NewRequest(http.MethodGet, "/api/projects?org_id="+orgID, nil)
	recProjects := httptest.NewRecorder()
	router.ServeHTTP(recProjects, reqProjects)
	if recProjects.Code == http.StatusNotFound {
		t.Fatalf("expected /api/projects route to be registered, got status %d", recProjects.Code)
	}

	reqInbox := httptest.NewRequest(http.MethodGet, "/api/inbox?org_id="+orgID, nil)
	recInbox := httptest.NewRecorder()
	router.ServeHTTP(recInbox, reqInbox)
	if recInbox.Code == http.StatusNotFound {
		t.Fatalf("expected /api/inbox route to be registered, got status %d", recInbox.Code)
	}

	reqCreateIssue := httptest.NewRequest(http.MethodPost, "/api/projects/"+orgID+"/issues?org_id="+orgID, strings.NewReader(`{"title":"hello"}`))
	recCreateIssue := httptest.NewRecorder()
	router.ServeHTTP(recCreateIssue, reqCreateIssue)
	if recCreateIssue.Code == http.StatusNotFound {
		t.Fatalf("expected /api/projects/{id}/issues route to be registered, got status %d", recCreateIssue.Code)
	}

	reqIssueDetail := httptest.NewRequest(http.MethodGet, "/api/issues/"+orgID+"?org_id="+orgID, nil)
	recIssueDetail := httptest.NewRecorder()
	router.ServeHTTP(recIssueDetail, reqIssueDetail)
	if recIssueDetail.Code == http.StatusNotFound {
		t.Fatalf("expected /api/issues/{id} route to be registered, got status %d", recIssueDetail.Code)
	}

	reqPatchIssue := httptest.NewRequest(http.MethodPatch, "/api/issues/"+orgID+"?org_id="+orgID, strings.NewReader(`{"priority":"P1"}`))
	recPatchIssue := httptest.NewRecorder()
	router.ServeHTTP(recPatchIssue, reqPatchIssue)
	if recPatchIssue.Code == http.StatusNotFound {
		t.Fatalf("expected /api/issues/{id} PATCH route to be registered, got status %d", recPatchIssue.Code)
	}

	reqAdminConnections := httptest.NewRequest(http.MethodGet, "/api/admin/connections?org_id="+orgID, nil)
	recAdminConnections := httptest.NewRecorder()
	router.ServeHTTP(recAdminConnections, reqAdminConnections)
	if recAdminConnections.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/connections route to be registered, got status %d", recAdminConnections.Code)
	}

	reqAdminEvents := httptest.NewRequest(http.MethodGet, "/api/admin/events?org_id="+orgID, nil)
	recAdminEvents := httptest.NewRecorder()
	router.ServeHTTP(recAdminEvents, reqAdminEvents)
	if recAdminEvents.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/events route to be registered, got status %d", recAdminEvents.Code)
	}

	reqGatewayRestart := httptest.NewRequest(http.MethodPost, "/api/admin/gateway/restart?org_id="+orgID, nil)
	recGatewayRestart := httptest.NewRecorder()
	router.ServeHTTP(recGatewayRestart, reqGatewayRestart)
	if recGatewayRestart.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/gateway/restart route to be registered, got status %d", recGatewayRestart.Code)
	}

	reqAgentPing := httptest.NewRequest(http.MethodPost, "/api/admin/agents/main/ping?org_id="+orgID, nil)
	recAgentPing := httptest.NewRecorder()
	router.ServeHTTP(recAgentPing, reqAgentPing)
	if recAgentPing.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/agents/{id}/ping route to be registered, got status %d", recAgentPing.Code)
	}

	reqAgentReset := httptest.NewRequest(http.MethodPost, "/api/admin/agents/main/reset?org_id="+orgID, nil)
	recAgentReset := httptest.NewRecorder()
	router.ServeHTTP(recAgentReset, reqAgentReset)
	if recAgentReset.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/agents/{id}/reset route to be registered, got status %d", recAgentReset.Code)
	}

	reqAdminAgentsList := httptest.NewRequest(http.MethodGet, "/api/admin/agents?org_id="+orgID, nil)
	recAdminAgentsList := httptest.NewRecorder()
	router.ServeHTTP(recAdminAgentsList, reqAdminAgentsList)
	if recAdminAgentsList.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/agents route to be registered, got status %d", recAdminAgentsList.Code)
	}

	reqAdminAgentsCreate := httptest.NewRequest(http.MethodPost, "/api/admin/agents?org_id="+orgID, strings.NewReader(`{"slot":"research","display_name":"Riley","model":"gpt-5.2-codex"}`))
	recAdminAgentsCreate := httptest.NewRecorder()
	router.ServeHTTP(recAdminAgentsCreate, reqAdminAgentsCreate)
	if recAdminAgentsCreate.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/agents POST route to be registered, got status %d", recAdminAgentsCreate.Code)
	}

	reqAdminAgentsGet := httptest.NewRequest(http.MethodGet, "/api/admin/agents/main?org_id="+orgID, nil)
	recAdminAgentsGet := httptest.NewRecorder()
	router.ServeHTTP(recAdminAgentsGet, reqAdminAgentsGet)
	if recAdminAgentsGet.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/agents/{id} route to be registered, got status %d", recAdminAgentsGet.Code)
	}

	reqAdminAgentFiles := httptest.NewRequest(http.MethodGet, "/api/admin/agents/main/files?org_id="+orgID, nil)
	recAdminAgentFiles := httptest.NewRecorder()
	router.ServeHTTP(recAdminAgentFiles, reqAdminAgentFiles)
	if recAdminAgentFiles.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/agents/{id}/files route to be registered, got status %d", recAdminAgentFiles.Code)
	}

	reqAdminAgentFile := httptest.NewRequest(http.MethodGet, "/api/admin/agents/main/files/SOUL.md?org_id="+orgID, nil)
	recAdminAgentFile := httptest.NewRecorder()
	router.ServeHTTP(recAdminAgentFile, reqAdminAgentFile)
	if recAdminAgentFile.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/agents/{id}/files/{path} route to be registered, got status %d", recAdminAgentFile.Code)
	}

	reqAdminAgentMemory := httptest.NewRequest(http.MethodGet, "/api/admin/agents/main/memory?org_id="+orgID, nil)
	recAdminAgentMemory := httptest.NewRecorder()
	router.ServeHTTP(recAdminAgentMemory, reqAdminAgentMemory)
	if recAdminAgentMemory.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/agents/{id}/memory route to be registered, got status %d", recAdminAgentMemory.Code)
	}

	reqAdminAgentMemoryDate := httptest.NewRequest(http.MethodGet, "/api/admin/agents/main/memory/2026-02-07?org_id="+orgID, nil)
	recAdminAgentMemoryDate := httptest.NewRecorder()
	router.ServeHTTP(recAdminAgentMemoryDate, reqAdminAgentMemoryDate)
	if recAdminAgentMemoryDate.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/agents/{id}/memory/{date} route to be registered, got status %d", recAdminAgentMemoryDate.Code)
	}

	reqAdminAgentRetire := httptest.NewRequest(http.MethodPost, "/api/admin/agents/main/retire?org_id="+orgID, nil)
	recAdminAgentRetire := httptest.NewRecorder()
	router.ServeHTTP(recAdminAgentRetire, reqAdminAgentRetire)
	if recAdminAgentRetire.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/agents/{id}/retire route to be registered, got status %d", recAdminAgentRetire.Code)
	}

	reqAdminAgentReactivate := httptest.NewRequest(http.MethodPost, "/api/admin/agents/main/reactivate?org_id="+orgID, nil)
	recAdminAgentReactivate := httptest.NewRecorder()
	router.ServeHTTP(recAdminAgentReactivate, reqAdminAgentReactivate)
	if recAdminAgentReactivate.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/agents/{id}/reactivate route to be registered, got status %d", recAdminAgentReactivate.Code)
	}

	// NOTE: /api/admin/config routes are from an unmerged spec — skip until merged

	reqDiagnostics := httptest.NewRequest(http.MethodPost, "/api/admin/diagnostics?org_id="+orgID, nil)
	recDiagnostics := httptest.NewRecorder()
	router.ServeHTTP(recDiagnostics, reqDiagnostics)
	if recDiagnostics.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/diagnostics route to be registered, got status %d", recDiagnostics.Code)
	}

	reqLogs := httptest.NewRequest(http.MethodGet, "/api/admin/logs?org_id="+orgID, nil)
	recLogs := httptest.NewRecorder()
	router.ServeHTTP(recLogs, reqLogs)
	if recLogs.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/logs route to be registered, got status %d", recLogs.Code)
	}

	reqCronJobs := httptest.NewRequest(http.MethodGet, "/api/admin/cron/jobs?org_id="+orgID, nil)
	recCronJobs := httptest.NewRecorder()
	router.ServeHTTP(recCronJobs, reqCronJobs)
	if recCronJobs.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/cron/jobs route to be registered, got status %d", recCronJobs.Code)
	}

	reqCronRun := httptest.NewRequest(http.MethodPost, "/api/admin/cron/jobs/job-1/run?org_id="+orgID, nil)
	recCronRun := httptest.NewRecorder()
	router.ServeHTTP(recCronRun, reqCronRun)
	if recCronRun.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/cron/jobs/{id}/run route to be registered, got status %d", recCronRun.Code)
	}

	reqCronToggle := httptest.NewRequest(http.MethodPatch, "/api/admin/cron/jobs/job-1?org_id="+orgID, strings.NewReader(`{"enabled":true}`))
	recCronToggle := httptest.NewRecorder()
	router.ServeHTTP(recCronToggle, reqCronToggle)
	if recCronToggle.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/cron/jobs/{id} PATCH route to be registered, got status %d", recCronToggle.Code)
	}

	reqProcesses := httptest.NewRequest(http.MethodGet, "/api/admin/processes?org_id="+orgID, nil)
	recProcesses := httptest.NewRecorder()
	router.ServeHTTP(recProcesses, reqProcesses)
	if recProcesses.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/processes route to be registered, got status %d", recProcesses.Code)
	}

	reqKillProcess := httptest.NewRequest(http.MethodPost, "/api/admin/processes/proc-1/kill?org_id="+orgID, nil)
	recKillProcess := httptest.NewRecorder()
	router.ServeHTTP(recKillProcess, reqKillProcess)
	if recKillProcess.Code == http.StatusNotFound {
		t.Fatalf("expected /api/admin/processes/{id}/kill route to be registered, got status %d", recKillProcess.Code)
	}
}

func TestWorkflowRoutesAreRegistered(t *testing.T) {
	t.Parallel()

	router := NewRouter()
	orgID := "00000000-0000-0000-0000-000000000001"

	reqList := httptest.NewRequest(http.MethodGet, "/api/workflows?org_id="+orgID, nil)
	recList := httptest.NewRecorder()
	router.ServeHTTP(recList, reqList)
	if recList.Code == http.StatusNotFound {
		t.Fatalf("expected /api/workflows route to be registered, got status %d", recList.Code)
	}

	reqToggle := httptest.NewRequest(http.MethodPatch, "/api/workflows/"+orgID+"?org_id="+orgID, strings.NewReader(`{"enabled":false}`))
	recToggle := httptest.NewRecorder()
	router.ServeHTTP(recToggle, reqToggle)
	if recToggle.Code == http.StatusNotFound {
		t.Fatalf("expected /api/workflows/{id} PATCH route to be registered, got status %d", recToggle.Code)
	}

	reqRun := httptest.NewRequest(http.MethodPost, "/api/workflows/"+orgID+"/run?org_id="+orgID, nil)
	recRun := httptest.NewRecorder()
	router.ServeHTTP(recRun, reqRun)
	if recRun.Code == http.StatusNotFound {
		t.Fatalf("expected /api/workflows/{id}/run route to be registered, got status %d", recRun.Code)
	}
}

func TestSettingsRoutesAreRegistered(t *testing.T) {
	t.Parallel()

	router := NewRouter()
	orgID := "00000000-0000-0000-0000-000000000001"
	paths := []string{
		"/api/settings/profile",
		"/api/settings/notifications",
		"/api/settings/workspace",
		"/api/settings/integrations",
	}

	for _, path := range paths {
		reqGet := httptest.NewRequest(http.MethodGet, path+"?org_id="+orgID, nil)
		recGet := httptest.NewRecorder()
		router.ServeHTTP(recGet, reqGet)
		if recGet.Code == http.StatusNotFound {
			t.Fatalf("expected %s GET route to be registered, got status %d", path, recGet.Code)
		}

		reqPut := httptest.NewRequest(http.MethodPut, path+"?org_id="+orgID, strings.NewReader(`{}`))
		recPut := httptest.NewRecorder()
		router.ServeHTTP(recPut, reqPut)
		if recPut.Code == http.StatusNotFound {
			t.Fatalf("expected %s PUT route to be registered, got status %d", path, recPut.Code)
		}
		if recPut.Code == http.StatusMethodNotAllowed {
			t.Fatalf("expected %s PUT route handler to be registered, got status %d", path, recPut.Code)
		}
	}
}

func TestUploadsRouteServesStoredFile(t *testing.T) {
	t.Parallel()

	router := NewRouter()
	orgDir := "router-test-org"
	fileName := "hello.txt"
	content := "hello from uploads route"
	fullDir := filepath.Join(getUploadsStorageDir(), orgDir)
	fullPath := filepath.Join(fullDir, fileName)

	requireNoErr := func(err error) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	requireNoErr(os.MkdirAll(fullDir, 0o755))
	requireNoErr(os.WriteFile(fullPath, []byte(content), 0o644))
	t.Cleanup(func() {
		_ = os.Remove(fullPath)
		_ = os.Remove(fullDir)
	})

	req := httptest.NewRequest(http.MethodGet, "/uploads/"+orgDir+"/"+fileName, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
	if body := rec.Body.String(); body != content {
		t.Fatalf("expected uploads body %q, got %q", content, body)
	}
}

func TestUploadsRouteMissingFileReturnsNotFound(t *testing.T) {
	t.Parallel()

	router := NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/uploads/missing-org/missing-file.txt", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "<!doctype html") {
		t.Fatalf("expected non-HTML 404 for missing upload path, got SPA HTML fallback")
	}
}
