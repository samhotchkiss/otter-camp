//go:build integration

package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestProjectHTTPAPIKeyScopeEnforcement(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, _, adminUser, _ := newProjectTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	readKey := issueAPIKeyRaw(t, testServer.URL, adminToken, []string{"read:projects"})

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/projects", nil, map[string]string{"X-API-Key": readKey})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list via read scope status = %d, want %d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}

	readOnlyCreate := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects", map[string]any{
		"slug":          "scope-read-only-project",
		"display_name":  "Scope Read Only Project",
		"delivery_mode": "gated",
	}, map[string]string{"X-API-Key": readKey})
	if readOnlyCreate.StatusCode != http.StatusForbidden {
		t.Fatalf("create via read scope status = %d, want %d body=%s", readOnlyCreate.StatusCode, http.StatusForbidden, string(readOnlyCreate.Body))
	}

	writeKey := issueAPIKeyRaw(t, testServer.URL, adminToken, []string{"write:projects"})
	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/projects", map[string]any{
		"slug":          "scope-write-project",
		"display_name":  "Scope Write Project",
		"delivery_mode": "gated",
	}, map[string]string{"X-API-Key": writeKey})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create via write scope status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
}

func TestChatHTTPAPIKeyScopeEnforcement(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, org, adminUser, _ := newChatTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	readKey := issueAPIKeyRaw(t, testServer.URL, adminToken, []string{"read:chat"})

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/chat-sessions", nil, map[string]string{"X-API-Key": readKey})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list via read scope status = %d, want %d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}

	readOnlyCreate := mustJSON(t, http.MethodPost, testServer.URL+"/v1/chat-sessions", map[string]any{
		"scope_type": "organization",
		"scope_id":   org.ID.String(),
		"mode":       "sync",
		"title":      "scope test",
	}, map[string]string{"X-API-Key": readKey})
	if readOnlyCreate.StatusCode != http.StatusForbidden {
		t.Fatalf("create via read scope status = %d, want %d body=%s", readOnlyCreate.StatusCode, http.StatusForbidden, string(readOnlyCreate.Body))
	}

	writeKey := issueAPIKeyRaw(t, testServer.URL, adminToken, []string{"write:chat"})
	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/chat-sessions", map[string]any{
		"scope_type": "organization",
		"scope_id":   org.ID.String(),
		"mode":       "sync",
		"title":      "scope write",
	}, map[string]string{"X-API-Key": writeKey})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create via write scope status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
}

func TestMemoryHTTPAPIKeyScopeEnforcement(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	fixture := newMemoryAPITestServer(t)
	defer fixture.Close()

	adminToken := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	readKey := issueAPIKeyRaw(t, fixture.URL, adminToken, []string{"read:memory"})

	listed := mustJSON(t, http.MethodGet, fixture.URL+"/v1/memory/items", nil, map[string]string{"X-API-Key": readKey})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list via read scope status = %d, want %d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}

	readOnlyQuery := mustJSON(t, http.MethodPost, fixture.URL+"/v1/memory/query", map[string]any{
		"query": "scope check",
	}, map[string]string{"X-API-Key": readKey})
	if readOnlyQuery.StatusCode != http.StatusOK {
		t.Fatalf("query via read scope status = %d, want %d body=%s", readOnlyQuery.StatusCode, http.StatusOK, string(readOnlyQuery.Body))
	}

	writeKey := issueAPIKeyRaw(t, fixture.URL, adminToken, []string{"write:memory"})
	queried := mustJSON(t, http.MethodPost, fixture.URL+"/v1/memory/query", map[string]any{
		"query": "scope check",
	}, map[string]string{"X-API-Key": writeKey})
	if queried.StatusCode != http.StatusOK {
		t.Fatalf("query via write scope status = %d, want %d body=%s", queried.StatusCode, http.StatusOK, string(queried.Body))
	}
}

func TestAgentHTTPAPIKeyScopeEnforcement(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, _, adminUser, _ := newAgentTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	readKey := issueAPIKeyRaw(t, testServer.URL, adminToken, []string{"read:agents"})

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents", nil, map[string]string{"X-API-Key": readKey})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list via read scope status = %d, want %d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}

	readOnlyCreate := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents", map[string]any{
		"display_name": "Scope Read Agent",
		"agent_class":  "staff",
		"agent_type":   "worker",
	}, map[string]string{"X-API-Key": readKey})
	if readOnlyCreate.StatusCode != http.StatusForbidden {
		t.Fatalf("create via read scope status = %d, want %d body=%s", readOnlyCreate.StatusCode, http.StatusForbidden, string(readOnlyCreate.Body))
	}

	writeKey := issueAPIKeyRaw(t, testServer.URL, adminToken, []string{"write:agents"})
	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents", map[string]any{
		"display_name": "Scope Write Agent",
		"agent_class":  "staff",
		"agent_type":   "worker",
	}, map[string]string{"X-API-Key": writeKey})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create via write scope status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
}

func TestAuthHTTPAdminRoutesRequireAdminScopeForAPIKeys(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, adminUser, _ := newAuthTestServer(t, "standard")
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	readKey := issueAPIKeyRaw(t, testServer.URL, adminToken, []string{"read:chat"})

	forbidden := mustJSON(t, http.MethodGet, testServer.URL+"/v1/admin/users?email="+adminUser.Email, nil, map[string]string{
		"X-API-Key": readKey,
	})
	if forbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("admin users via non-admin scope status = %d, want %d body=%s", forbidden.StatusCode, http.StatusForbidden, string(forbidden.Body))
	}

	adminKey := issueAPIKeyRaw(t, testServer.URL, adminToken, []string{"admin:*"})
	allowed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/admin/users?email="+adminUser.Email, nil, map[string]string{
		"X-API-Key": adminKey,
	})
	if allowed.StatusCode != http.StatusOK {
		t.Fatalf("admin users via admin scope status = %d, want %d body=%s", allowed.StatusCode, http.StatusOK, string(allowed.Body))
	}
}

func TestOrgAuditHTTPAPIKeyScopeEnforcement(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	pool := testdb.New(t)
	org, adminUser := createOrgAndUser(t, pool, "audit-http-org", "audit-admin@example.com", "Audit Admin", "admin", "admin-password")
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

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []RouteRegistrar{
			NewOrgAuditRouteRegistrar(pool),
		},
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	adminToken := loginToken(t, ts.URL, adminUser.Email, "admin-password")
	nonAuditKey := issueAPIKeyRaw(t, ts.URL, adminToken, []string{"read:chat"})

	forbidden := mustJSON(t, http.MethodGet, ts.URL+"/v1/audit-events", nil, map[string]string{
		"X-API-Key": nonAuditKey,
	})
	if forbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("audit-events via non-audit scope status = %d, want %d body=%s", forbidden.StatusCode, http.StatusForbidden, string(forbidden.Body))
	}

	auditKey := issueAPIKeyRaw(t, ts.URL, adminToken, []string{"read:audit"})
	allowed := mustJSON(t, http.MethodGet, ts.URL+"/v1/audit-events", nil, map[string]string{
		"X-API-Key": auditKey,
	})
	if allowed.StatusCode != http.StatusOK {
		t.Fatalf("audit-events via read:audit scope status = %d, want %d body=%s", allowed.StatusCode, http.StatusOK, string(allowed.Body))
	}
}

func issueAPIKeyRaw(t *testing.T, baseURL, bearerToken string, scopes []string) string {
	t.Helper()
	created := mustJSON(t, http.MethodPost, baseURL+"/v1/api-keys", map[string]any{
		"display_name": "scope-key",
		"scopes":       scopes,
	}, map[string]string{
		"Authorization": "Bearer " + bearerToken,
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("issue api key status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
	key := jsonPathString(t, created.Body, "data", "key")
	if key == "" {
		t.Fatalf("issued api key payload missing key body=%s", string(created.Body))
	}
	return key
}
