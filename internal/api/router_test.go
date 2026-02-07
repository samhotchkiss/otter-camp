package api

import (
	"net/http"
	"net/http/httptest"
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
}
