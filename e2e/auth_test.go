//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/e2e/testutil"
)

func TestAuth_RegisterLoginRefreshLogout(t *testing.T) {
	start := time.Now()

	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	runBootstrap(t, server, baseURL)
	adminToken := testutil.AdminToken(t, baseURL)

	createUserBody, createUserStatus := testutil.POST(t, baseURL, "/v1/users", adminToken, map[string]any{
		"email":    "alice@example.com",
		"password": "securepassword123",
		"name":     "Alice",
	})
	if createUserStatus != http.StatusCreated {
		t.Fatalf("POST /v1/users status=%d want=%d body=%s", createUserStatus, http.StatusCreated, string(createUserBody))
	}
	aliceID := strings.TrimSpace(asString(testutil.JSONPath(t, createUserBody, "data", "id")))
	if _, err := uuid.Parse(aliceID); err != nil {
		t.Fatalf("invalid alice id=%q err=%v body=%s", aliceID, err, string(createUserBody))
	}
	if got := asString(testutil.JSONPath(t, createUserBody, "data", "email")); got != "alice@example.com" {
		t.Fatalf("created user email=%q want=%q body=%s", got, "alice@example.com", string(createUserBody))
	}

	loginBody, loginStatus := testutil.POST(t, baseURL, "/v1/auth/login", "", map[string]any{
		"email":    "alice@example.com",
		"password": "securepassword123",
	})
	if loginStatus != http.StatusOK {
		t.Fatalf("POST /v1/auth/login status=%d want=%d body=%s", loginStatus, http.StatusOK, string(loginBody))
	}
	sessionToken := sessionTokenFromAuthBody(t, loginBody)
	if len(sessionToken) <= 20 {
		t.Fatalf("session token length=%d want>20 body=%s", len(sessionToken), string(loginBody))
	}
	assertFutureTimestamp(t, loginBody, "login", "data", "expires_at")

	meBody, meStatus := testutil.GET(t, baseURL, "/v1/auth/me", sessionToken)
	if meStatus != http.StatusOK {
		t.Fatalf("GET /v1/auth/me status=%d want=%d body=%s", meStatus, http.StatusOK, string(meBody))
	}
	if got := asString(testutil.JSONPath(t, meBody, "data", "email")); got != "alice@example.com" {
		t.Fatalf("/v1/auth/me email=%q want=%q body=%s", got, "alice@example.com", string(meBody))
	}
	if idText := strings.TrimSpace(asString(testutil.JSONPath(t, meBody, "data", "id"))); idText == "" {
		t.Fatalf("/v1/auth/me id empty body=%s", string(meBody))
	}

	refreshBody, refreshStatus := testutil.POST(t, baseURL, "/v1/auth/refresh", sessionToken, map[string]any{})
	if refreshStatus != http.StatusOK {
		t.Fatalf("POST /v1/auth/refresh status=%d want=%d body=%s", refreshStatus, http.StatusOK, string(refreshBody))
	}
	newSessionToken := sessionTokenFromAuthBody(t, refreshBody)
	if strings.TrimSpace(newSessionToken) == "" {
		t.Fatalf("refresh returned empty session token body=%s", string(refreshBody))
	}
	if newSessionToken == sessionToken {
		t.Fatalf("refresh did not rotate token body=%s", string(refreshBody))
	}
	assertFutureTimestamp(t, refreshBody, "refresh", "data", "expires_at")

	newMeBody, newMeStatus := testutil.GET(t, baseURL, "/v1/auth/me", newSessionToken)
	if newMeStatus != http.StatusOK {
		t.Fatalf("GET /v1/auth/me with refreshed token status=%d want=%d body=%s", newMeStatus, http.StatusOK, string(newMeBody))
	}
	if got := asString(testutil.JSONPath(t, newMeBody, "data", "email")); got != "alice@example.com" {
		t.Fatalf("/v1/auth/me refreshed token email=%q want=%q body=%s", got, "alice@example.com", string(newMeBody))
	}

	oldMeBody, oldMeStatus := testutil.GET(t, baseURL, "/v1/auth/me", sessionToken)
	if oldMeStatus != http.StatusOK {
		t.Fatalf("original token no longer valid immediately after refresh status=%d want=%d body=%s", oldMeStatus, http.StatusOK, string(oldMeBody))
	}

	logoutBody, logoutStatus := testutil.POST(t, baseURL, "/v1/auth/logout", newSessionToken, map[string]any{})
	if logoutStatus != http.StatusNoContent {
		t.Fatalf("POST /v1/auth/logout status=%d want=%d body=%s", logoutStatus, http.StatusNoContent, string(logoutBody))
	}

	postLogoutBody, postLogoutStatus := testutil.GET(t, baseURL, "/v1/auth/me", newSessionToken)
	if postLogoutStatus != http.StatusUnauthorized {
		t.Fatalf("GET /v1/auth/me after logout status=%d want=%d body=%s", postLogoutStatus, http.StatusUnauthorized, string(postLogoutBody))
	}
	errorCode := strings.TrimSpace(asString(testutil.JSONPath(t, postLogoutBody, "error", "code")))
	if errorCode != "session_invalid" && errorCode != "unauthorized" && errorCode != "session_revoked" {
		t.Fatalf("logout invalidation error.code=%q want=session_invalid|unauthorized|session_revoked body=%s", errorCode, string(postLogoutBody))
	}

	if elapsed := time.Since(start); elapsed > 60*time.Second {
		t.Fatalf("scenario runtime=%s want<60s", elapsed)
	}
}

func TestAuth_APIKey_IssueAndAuthenticate(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	runBootstrap(t, server, baseURL)
	adminToken := testutil.AdminToken(t, baseURL)

	issueBody, issueStatus := testutil.POST(t, baseURL, "/v1/api-keys", adminToken, map[string]any{
		"name":   "ci-test-key",
		"scopes": []string{"read", "write"},
	})
	if issueStatus != http.StatusCreated {
		t.Fatalf("POST /v1/api-keys status=%d want=%d body=%s", issueStatus, http.StatusCreated, string(issueBody))
	}
	apiKeyID := strings.TrimSpace(asString(testutil.JSONPath(t, issueBody, "data", "id")))
	if _, err := uuid.Parse(apiKeyID); err != nil {
		t.Fatalf("api key id=%q parse err=%v body=%s", apiKeyID, err, string(issueBody))
	}
	apiKeyRaw := strings.TrimSpace(asString(testutil.JSONPath(t, issueBody, "data", "key")))
	if apiKeyRaw == "" {
		t.Fatalf("api key raw value missing body=%s", string(issueBody))
	}
	issuedName := strings.TrimSpace(asString(testutil.JSONPath(t, issueBody, "data", "name")))
	if issuedName == "" {
		issuedName = strings.TrimSpace(asString(testutil.JSONPath(t, issueBody, "data", "display_name")))
	}
	if issuedName != "ci-test-key" {
		t.Fatalf("issued key name=%q want=%q body=%s", issuedName, "ci-test-key", string(issueBody))
	}

	apiKeyMeBody, apiKeyMeStatus := testutil.GET(t, baseURL, "/v1/auth/me", apiKeyRaw)
	if apiKeyMeStatus != http.StatusOK {
		t.Fatalf("GET /v1/auth/me with api key status=%d want=%d body=%s", apiKeyMeStatus, http.StatusOK, string(apiKeyMeBody))
	}
	if strings.TrimSpace(asString(testutil.JSONPath(t, apiKeyMeBody, "data", "email"))) == "" {
		t.Fatalf("api-key /v1/auth/me email empty body=%s", string(apiKeyMeBody))
	}

	listBody, listStatus := testutil.GET(t, baseURL, "/v1/api-keys", adminToken)
	if listStatus != http.StatusOK {
		t.Fatalf("GET /v1/api-keys status=%d want=%d body=%s", listStatus, http.StatusOK, string(listBody))
	}
	items := asArray(t, testutil.JSONPath(t, listBody, "data"), "api key list")
	found := false
	for _, item := range items {
		obj := asObject(t, item, "api key item")
		if strings.TrimSpace(asString(obj["id"])) == apiKeyID {
			name := strings.TrimSpace(asString(obj["name"]))
			if name == "" {
				name = strings.TrimSpace(asString(obj["display_name"]))
			}
			if name != "ci-test-key" {
				t.Fatalf("listed key name=%q want=%q body=%s", name, "ci-test-key", string(listBody))
			}
			found = true
		}
		if keyValue := strings.TrimSpace(asString(obj["key"])); keyValue != "" {
			t.Fatalf("listed api key leaked raw key=%q body=%s", keyValue, string(listBody))
		}
	}
	if !found {
		t.Fatalf("issued key id=%s not present in list body=%s", apiKeyID, string(listBody))
	}

	deleteBody, deleteStatus := testutil.DELETE(t, baseURL, "/v1/api-keys/"+apiKeyID, adminToken)
	if deleteStatus != http.StatusNoContent {
		t.Fatalf("DELETE /v1/api-keys/%s status=%d want=%d body=%s", apiKeyID, deleteStatus, http.StatusNoContent, string(deleteBody))
	}

	afterDeleteBody, afterDeleteStatus := testutil.GET(t, baseURL, "/v1/auth/me", apiKeyRaw)
	if afterDeleteStatus != http.StatusUnauthorized {
		t.Fatalf("GET /v1/auth/me with deleted api key status=%d want=%d body=%s", afterDeleteStatus, http.StatusUnauthorized, string(afterDeleteBody))
	}
}

func TestAuth_OrgIsolation(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	runBootstrap(t, server, baseURL)
	orgAToken := testutil.AdminToken(t, baseURL)

	projectBody, projectStatus := testutil.POST(t, baseURL, "/v1/projects", orgAToken, map[string]any{
		"display_name": "org-a-project",
		"slug":         "org-a-project",
	})
	if projectStatus != http.StatusCreated {
		t.Fatalf("POST /v1/projects status=%d want=%d body=%s", projectStatus, http.StatusCreated, string(projectBody))
	}
	projectIDA := strings.TrimSpace(asString(testutil.JSONPath(t, projectBody, "data", "id")))
	if _, err := uuid.Parse(projectIDA); err != nil {
		t.Fatalf("project id=%q parse err=%v body=%s", projectIDA, err, string(projectBody))
	}

	orgBody, orgStatus := testutil.POST(t, baseURL, "/v1/orgs", orgAToken, map[string]any{
		"name":           "Org B",
		"slug":           "org-b",
		"admin_email":    "org-b-admin@example.com",
		"admin_password": "securepassword123",
		"admin_name":     "Org B Admin",
	})
	if orgStatus != http.StatusCreated {
		t.Fatalf("POST /v1/orgs status=%d want=%d body=%s", orgStatus, http.StatusCreated, string(orgBody))
	}
	if orgBID := strings.TrimSpace(asString(testutil.JSONPath(t, orgBody, "data", "id"))); orgBID == "" {
		t.Fatalf("org B id empty body=%s", string(orgBody))
	}

	orgBLoginBody, orgBLoginStatus := testutil.POST(t, baseURL, "/v1/auth/login", "", map[string]any{
		"email":    "org-b-admin@example.com",
		"password": "securepassword123",
	})
	if orgBLoginStatus != http.StatusOK {
		t.Fatalf("POST /v1/auth/login for org B status=%d want=%d body=%s", orgBLoginStatus, http.StatusOK, string(orgBLoginBody))
	}
	orgBToken := sessionTokenFromAuthBody(t, orgBLoginBody)
	if strings.TrimSpace(orgBToken) == "" {
		t.Fatalf("org B token empty body=%s", string(orgBLoginBody))
	}

	getCrossBody, getCrossStatus := testutil.GET(t, baseURL, "/v1/projects/"+projectIDA, orgBToken)
	if getCrossStatus != http.StatusNotFound && getCrossStatus != http.StatusForbidden {
		t.Fatalf("GET /v1/projects/%s as org B status=%d want=404|403 body=%s", projectIDA, getCrossStatus, string(getCrossBody))
	}

	listBody, listStatus := testutil.GET(t, baseURL, "/v1/projects", orgBToken)
	if listStatus != http.StatusOK {
		t.Fatalf("GET /v1/projects as org B status=%d want=%d body=%s", listStatus, http.StatusOK, string(listBody))
	}
	projects := asArray(t, testutil.JSONPath(t, listBody, "data"), "org B project list")
	for _, project := range projects {
		entry := asObject(t, project, "project list entry")
		if strings.TrimSpace(asString(entry["id"])) == projectIDA {
			t.Fatalf("org B project list contains org A project id=%s body=%s", projectIDA, string(listBody))
		}
	}
}

func TestAuth_AuthenticatedEndpointsRejectInvalidToken(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	runBootstrap(t, server, baseURL)
	invalid := "not-a-valid-token"

	endpoints := []string{"/v1/auth/me", "/v1/projects", "/v1/api-keys"}
	for _, path := range endpoints {
		body, status := testutil.GET(t, baseURL, path, invalid)
		if status != http.StatusUnauthorized {
			t.Fatalf("GET %s with invalid token status=%d want=%d body=%s", path, status, http.StatusUnauthorized, string(body))
		}
	}
}

func runBootstrap(t *testing.T, server *testutil.ServerProcess, baseURL string) {
	t.Helper()
	testutil.ResetState(t, baseURL)
	if out, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap"); code != 0 {
		t.Fatalf("bootstrap exit=%d want=0 output=%s", code, out)
	}
}

func sessionTokenFromAuthBody(t *testing.T, body []byte) string {
	t.Helper()
	token := strings.TrimSpace(asString(testutil.JSONPath(t, body, "data", "session_token")))
	if token != "" {
		return token
	}
	return strings.TrimSpace(asString(testutil.JSONPath(t, body, "data", "token")))
}

func assertFutureTimestamp(t *testing.T, body []byte, label string, path ...string) {
	t.Helper()
	raw := strings.TrimSpace(asString(testutil.JSONPath(t, body, path...)))
	if raw == "" {
		t.Fatalf("%s timestamp missing at path=%v body=%s", label, path, string(body))
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("%s timestamp parse failed raw=%q err=%v body=%s", label, raw, err, string(body))
	}
	if !parsed.After(time.Now()) {
		t.Fatalf("%s timestamp=%s not in future body=%s", label, parsed, string(body))
	}
}
