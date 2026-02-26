//go:build integration

package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestAuthHTTPLoginMeLogoutAndExpiredSession(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, adminUser, _ := newAuthTestServer(t, "standard")
	defer testServer.Close()

	loginResp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/auth/login", map[string]any{
		"email":    adminUser.Email,
		"password": "admin-password",
	}, nil)
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want %d body=%s", loginResp.StatusCode, http.StatusOK, string(loginResp.Body))
	}

	if loginResp.Headers.Get("X-Request-ID") == "" {
		t.Fatal("expected X-Request-ID on login response")
	}

	token := jsonPathString(t, loginResp.Body, "data", "token")
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	meResp := mustJSON(t, http.MethodGet, testServer.URL+"/v1/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d, want %d body=%s", meResp.StatusCode, http.StatusOK, string(meResp.Body))
	}
	if got := jsonPathString(t, meResp.Body, "data", "email"); got != adminUser.Email {
		t.Fatalf("me email = %q, want %q", got, adminUser.Email)
	}

	logoutResp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/auth/logout", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if logoutResp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d body=%s", logoutResp.StatusCode, http.StatusNoContent, string(logoutResp.Body))
	}

	meAfterLogout := mustJSON(t, http.MethodGet, testServer.URL+"/v1/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if meAfterLogout.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after logout status = %d, want %d body=%s", meAfterLogout.StatusCode, http.StatusUnauthorized, string(meAfterLogout.Body))
	}

	expiredToken := "expired-session-token"
	sessionRepo := repo.NewAuthSessionRepo(testServer.Pool)
	if _, err := sessionRepo.Create(context.Background(), repo.AuthSession{
		UserID:    adminUser.ID,
		TokenHash: sha256Hex(expiredToken),
		ExpiresAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create expired session: %v", err)
	}

	expiredResp := mustJSON(t, http.MethodGet, testServer.URL+"/v1/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + expiredToken,
	})
	if expiredResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired me status = %d, want %d body=%s", expiredResp.StatusCode, http.StatusUnauthorized, string(expiredResp.Body))
	}
	if got := jsonPathString(t, expiredResp.Body, "error", "code"); got != "session_expired" {
		t.Fatalf("error.code = %q, want %q", got, "session_expired")
	}
}

func TestAuthHTTPAPIKeyLifecycleAndAdminRevoke(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, adminUser, memberUser := newAuthTestServer(t, "standard")
	defer testServer.Close()

	memberToken := loginToken(t, testServer.URL, memberUser.Email, "member-password")
	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/api-keys", map[string]any{
		"display_name": "member-cli",
		"scopes":       []string{"chat:read"},
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create key status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}

	apiKeyID := jsonPathString(t, created.Body, "data", "id")
	rawKey := jsonPathString(t, created.Body, "data", "key")
	if apiKeyID == "" || rawKey == "" {
		t.Fatalf("expected key id and raw key, got id=%q key=%q", apiKeyID, rawKey)
	}

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/api-keys", nil, map[string]string{"Authorization": "Bearer " + memberToken})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list keys status = %d, want %d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}
	if hasJSONPath(listed.Body, "data", "0", "key") {
		t.Fatalf("list response unexpectedly included raw key: %s", string(listed.Body))
	}

	byAPIKey := mustJSON(t, http.MethodGet, testServer.URL+"/v1/auth/me", nil, map[string]string{
		"X-API-Key": rawKey,
	})
	if byAPIKey.StatusCode != http.StatusOK {
		t.Fatalf("me via api key status = %d, want %d body=%s", byAPIKey.StatusCode, http.StatusOK, string(byAPIKey.Body))
	}
	if got := jsonPathString(t, byAPIKey.Body, "data", "email"); got != memberUser.Email {
		t.Fatalf("api key me email = %q, want %q", got, memberUser.Email)
	}

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	revoked := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/api-keys/"+apiKeyID, nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if revoked.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want %d body=%s", revoked.StatusCode, http.StatusNoContent, string(revoked.Body))
	}

	afterRevoke := mustJSON(t, http.MethodGet, testServer.URL+"/v1/auth/me", nil, map[string]string{
		"X-API-Key": rawKey,
	})
	if afterRevoke.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after revoke status = %d, want %d body=%s", afterRevoke.StatusCode, http.StatusUnauthorized, string(afterRevoke.Body))
	}
}

func TestAuthHTTPLoginRateLimit(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, _, _ := newAuthTestServer(t, "standard")
	defer testServer.Close()

	for i := 1; i <= 21; i++ {
		resp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/auth/login", map[string]any{
			"email":    "missing@example.com",
			"password": "wrong",
		}, map[string]string{"X-Forwarded-For": "203.0.113.123"})

		if i <= 20 && resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want %d body=%s", i, resp.StatusCode, http.StatusUnauthorized, string(resp.Body))
		}
		if i == 21 {
			if resp.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("attempt %d status = %d, want %d body=%s", i, resp.StatusCode, http.StatusTooManyRequests, string(resp.Body))
			}
			if got := jsonPathString(t, resp.Body, "error", "code"); got != "rate_limited" {
				t.Fatalf("error.code = %q, want %q", got, "rate_limited")
			}
		}
	}
}

func TestAuthHTTPLocalModeAutoLogin(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "local")

	testServer, _, _ := newAuthTestServer(t, "local")
	defer testServer.Close()

	meResp := mustJSON(t, http.MethodGet, testServer.URL+"/v1/auth/me", nil, nil)
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("local mode me status = %d, want %d body=%s", meResp.StatusCode, http.StatusOK, string(meResp.Body))
	}
}

func TestVersionAndPrefixEnforcement(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, _, _ := newAuthTestServer(t, "standard")
	defer testServer.Close()

	versionResp := mustJSON(t, http.MethodGet, testServer.URL+"/v1/version", nil, nil)
	if versionResp.StatusCode != http.StatusOK {
		t.Fatalf("version status = %d, want %d body=%s", versionResp.StatusCode, http.StatusOK, string(versionResp.Body))
	}
	if got := jsonPathString(t, versionResp.Body, "data", "version"); got != "test-version" {
		t.Fatalf("version = %q, want %q", got, "test-version")
	}

	prefixResp := mustJSON(t, http.MethodPost, testServer.URL+"/api/projects", map[string]any{}, nil)
	if prefixResp.StatusCode != http.StatusNotFound {
		t.Fatalf("prefix status = %d, want %d body=%s", prefixResp.StatusCode, http.StatusNotFound, string(prefixResp.Body))
	}
	if got := jsonPathString(t, prefixResp.Body, "error", "message"); got != "This API uses the /v1/ prefix. See docs." {
		t.Fatalf("prefix message = %q, want %q", got, "This API uses the /v1/ prefix. See docs.")
	}
}

func TestMetricsEndpointSecretAccessControl(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	t.Setenv("OTTERCAMP_METRICS_SECRET", "metrics-secret")

	testServer, _, _ := newAuthTestServer(t, "standard")
	defer testServer.Close()

	noSecret := mustJSON(t, http.MethodGet, testServer.URL+"/metrics", nil, nil)
	if noSecret.StatusCode != http.StatusUnauthorized {
		t.Fatalf("metrics without secret status = %d, want %d body=%s", noSecret.StatusCode, http.StatusUnauthorized, string(noSecret.Body))
	}

	wrongSecret := mustJSON(t, http.MethodGet, testServer.URL+"/metrics", nil, map[string]string{
		"X-Metrics-Token": "wrong-secret",
	})
	if wrongSecret.StatusCode != http.StatusUnauthorized {
		t.Fatalf("metrics with wrong secret status = %d, want %d body=%s", wrongSecret.StatusCode, http.StatusUnauthorized, string(wrongSecret.Body))
	}

	withHeader := mustJSON(t, http.MethodGet, testServer.URL+"/metrics", nil, map[string]string{
		"X-Metrics-Token": "metrics-secret",
	})
	if withHeader.StatusCode != http.StatusOK {
		t.Fatalf("metrics with X-Metrics-Token status = %d, want %d body=%s", withHeader.StatusCode, http.StatusOK, string(withHeader.Body))
	}

	withBearer := mustJSON(t, http.MethodGet, testServer.URL+"/metrics", nil, map[string]string{
		"Authorization": "Bearer metrics-secret",
	})
	if withBearer.StatusCode != http.StatusOK {
		t.Fatalf("metrics with bearer secret status = %d, want %d body=%s", withBearer.StatusCode, http.StatusOK, string(withBearer.Body))
	}
}

func TestMetricsEndpointOpenWhenSecretUnset(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	t.Setenv("OTTERCAMP_METRICS_SECRET", "")

	testServer, _, _ := newAuthTestServer(t, "standard")
	defer testServer.Close()

	resp := mustJSON(t, http.MethodGet, testServer.URL+"/metrics", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}
}

func TestStaticServingAndAPIRoutePrecedence(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	t.Setenv("OTTERCAMP_WEB_MODE", "directory")
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html>spa index</html>"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(webDir, "assets"), 0o700); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "assets", "main.abc123.js"), []byte("console.log('ok')"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	t.Setenv("OTTERCAMP_WEB_DIR", webDir)

	testServer, _, _ := newAuthTestServer(t, "standard")
	defer testServer.Close()

	spaRoute := mustJSON(t, http.MethodGet, testServer.URL+"/some/spa/route", nil, nil)
	if spaRoute.StatusCode != http.StatusOK {
		t.Fatalf("spa route status = %d, want %d body=%s", spaRoute.StatusCode, http.StatusOK, string(spaRoute.Body))
	}
	if !strings.Contains(string(spaRoute.Body), "spa index") {
		t.Fatalf("spa route body = %q", string(spaRoute.Body))
	}

	if got := spaRoute.Headers.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
	if got := spaRoute.Headers.Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Fatalf("X-Frame-Options = %q, want %q", got, "SAMEORIGIN")
	}

	missingAPI := mustJSON(t, http.MethodGet, testServer.URL+"/v1/nonexistent", nil, nil)
	if missingAPI.StatusCode != http.StatusNotFound {
		t.Fatalf("missing API status = %d, want %d body=%s", missingAPI.StatusCode, http.StatusNotFound, string(missingAPI.Body))
	}
	if got := jsonPathString(t, missingAPI.Body, "error", "code"); got != "not_found" {
		t.Fatalf("missing API error code = %q, want %q body=%s", got, "not_found", string(missingAPI.Body))
	}

	asset := mustJSON(t, http.MethodGet, testServer.URL+"/assets/main.abc123.js", nil, nil)
	if asset.StatusCode != http.StatusOK {
		t.Fatalf("asset status = %d, want %d body=%s", asset.StatusCode, http.StatusOK, string(asset.Body))
	}
	if got := asset.Headers.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("asset cache-control = %q, want %q", got, "no-store")
	}
}

func TestAuthHTTPSessionListAndRevokeEndpoints(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, adminUser, _ := newAuthTestServer(t, "standard")
	defer testServer.Close()

	loginWithHeaders := func(userAgent, ipAddress string) string {
		resp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/auth/login", map[string]any{
			"email":    adminUser.Email,
			"password": "admin-password",
		}, map[string]string{
			"User-Agent":      userAgent,
			"X-Forwarded-For": ipAddress,
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("login status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
		}
		return jsonPathString(t, resp.Body, "data", "token")
	}

	currentToken := loginWithHeaders("OtterCamp-iOS/1.0", "203.0.113.11")
	revokeByIDToken := loginWithHeaders("Mozilla/5.0", "203.0.113.22")
	otherToken := loginWithHeaders("OtterCamp-TUI/1.0", "203.0.113.33")

	listResp := mustJSON(t, http.MethodGet, testServer.URL+"/v1/auth/sessions", nil, map[string]string{
		"Authorization": "Bearer " + currentToken,
	})
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list sessions status = %d, want %d body=%s", listResp.StatusCode, http.StatusOK, string(listResp.Body))
	}
	sessionItems, ok := jsonPathValue(t, listResp.Body, "data", "sessions").([]any)
	if !ok {
		t.Fatalf("sessions payload type = %T, want []any body=%s", jsonPathValue(t, listResp.Body, "data", "sessions"), string(listResp.Body))
	}
	if len(sessionItems) != 3 {
		t.Fatalf("session count = %d, want 3 body=%s", len(sessionItems), string(listResp.Body))
	}

	var revokeSessionID string
	for _, item := range sessionItems {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(stringValue(record["ip_address"])) == "203.0.113.22" {
			revokeSessionID = strings.TrimSpace(stringValue(record["session_id"]))
			break
		}
	}
	if revokeSessionID == "" {
		t.Fatalf("expected to find session for ip 203.0.113.22 in body=%s", string(listResp.Body))
	}

	deleteByID := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/auth/sessions/"+revokeSessionID, nil, map[string]string{
		"Authorization": "Bearer " + currentToken,
	})
	if deleteByID.StatusCode != http.StatusOK {
		t.Fatalf("delete by id status = %d, want %d body=%s", deleteByID.StatusCode, http.StatusOK, string(deleteByID.Body))
	}

	revokedByIDMe := mustJSON(t, http.MethodGet, testServer.URL+"/v1/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + revokeByIDToken,
	})
	if revokedByIDMe.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked-by-id token status = %d, want %d body=%s", revokedByIDMe.StatusCode, http.StatusUnauthorized, string(revokedByIDMe.Body))
	}

	deleteOthers := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/auth/sessions", nil, map[string]string{
		"Authorization": "Bearer " + currentToken,
	})
	if deleteOthers.StatusCode != http.StatusOK {
		t.Fatalf("delete others status = %d, want %d body=%s", deleteOthers.StatusCode, http.StatusOK, string(deleteOthers.Body))
	}
	if got := intValue(jsonPathValue(t, deleteOthers.Body, "data", "revoked_count")); got != 1 {
		t.Fatalf("revoked_count = %d, want 1 body=%s", got, string(deleteOthers.Body))
	}

	currentMe := mustJSON(t, http.MethodGet, testServer.URL+"/v1/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + currentToken,
	})
	if currentMe.StatusCode != http.StatusOK {
		t.Fatalf("current session me status = %d, want %d body=%s", currentMe.StatusCode, http.StatusOK, string(currentMe.Body))
	}

	otherMe := mustJSON(t, http.MethodGet, testServer.URL+"/v1/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + otherToken,
	})
	if otherMe.StatusCode != http.StatusUnauthorized {
		t.Fatalf("other session me status = %d, want %d body=%s", otherMe.StatusCode, http.StatusUnauthorized, string(otherMe.Body))
	}
}

func TestMobileDashboardAggregation(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, adminUser, _ := newAuthTestServer(t, "standard")
	defer testServer.Close()

	token := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	ctx := context.Background()

	projectRepo := repo.NewProjectRepo(testServer.Pool)
	taskRepo := repo.NewProjectTaskRepo(testServer.Pool)
	environmentRepo := repo.NewProjectEnvironmentRepo(testServer.Pool)
	inboxRepo := repo.NewInboxItemRepo(testServer.Pool)
	sessionRepo := repo.NewChatSessionRepo(testServer.Pool)
	messageRepo := repo.NewChatMessageRepo(testServer.Pool)
	readCursorRepo := repo.NewChatReadCursorRepo(testServer.Pool)

	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: adminUser.OrganizationID,
		Slug:           "mobile-dashboard",
		DisplayName:    "Mobile Dashboard",
		DeliveryMode:   "gated",
		CreatedByType:  "human_user",
		CreatedByID:    adminUser.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	createdByID := adminUser.ID
	if _, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: adminUser.OrganizationID,
		ProjectID:      project.ID,
		Title:          "Active task",
		WorkStatus:     "in_progress",
		CreatedByType:  "human_user",
		CreatedByID:    &createdByID,
	}); err != nil {
		t.Fatalf("create active task: %v", err)
	}
	if _, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: adminUser.OrganizationID,
		ProjectID:      project.ID,
		Title:          "Blocked task",
		WorkStatus:     "blocked",
		CreatedByType:  "human_user",
		CreatedByID:    &createdByID,
	}); err != nil {
		t.Fatalf("create blocked task: %v", err)
	}

	deployedAt := time.Now().UTC().Add(-10 * time.Minute)
	commitSHA := "0123456789abcdef"
	if _, err := environmentRepo.Create(ctx, repo.ProjectEnvironment{
		ProjectID:          project.ID,
		Name:               "prod",
		DeliveryMode:       "continuous",
		LastDeployedCommit: &commitSHA,
		LastDeployedAt:     &deployedAt,
		IsActive:           true,
	}); err != nil {
		t.Fatalf("create project environment: %v", err)
	}

	body := "Please review"
	if _, err := inboxRepo.Create(ctx, repo.InboxItem{
		OrganizationID: adminUser.OrganizationID,
		TargetUserID:   &adminUser.ID,
		ItemType:       "task_review",
		CreatedByType:  "human_user",
		CreatedByID:    &createdByID,
		Title:          "Inbox one",
		Body:           &body,
	}); err != nil {
		t.Fatalf("create inbox one: %v", err)
	}
	if _, err := inboxRepo.Create(ctx, repo.InboxItem{
		OrganizationID: adminUser.OrganizationID,
		TargetUserID:   &adminUser.ID,
		ItemType:       "comment_mention",
		CreatedByType:  "human_user",
		CreatedByID:    &createdByID,
		Title:          "Inbox two",
	}); err != nil {
		t.Fatalf("create inbox two: %v", err)
	}

	lastMessageAt := time.Now().UTC().Add(-2 * time.Minute)
	session, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: adminUser.OrganizationID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "sync",
		Status:         "active",
		CreatedByType:  "human_user",
		CreatedByID:    adminUser.ID,
		LastMessageAt:  &lastMessageAt,
	})
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}

	for i := 0; i < 3; i++ {
		role := "assistant"
		if i == 0 {
			role = "user"
		}
		if _, err := messageRepo.Create(ctx, repo.ChatMessage{
			SessionID: session.ID,
			Role:      role,
			Content:   "message",
			Status:    "final",
		}); err != nil {
			t.Fatalf("create chat message %d: %v", i, err)
		}
	}

	if _, err := readCursorRepo.Upsert(ctx, repo.ChatReadCursor{
		SessionID:        session.ID,
		UserID:           adminUser.ID,
		LastReadSequence: 2,
	}); err != nil {
		t.Fatalf("upsert read cursor: %v", err)
	}

	resp := mustJSON(t, http.MethodGet, testServer.URL+"/v1/mobile/dashboard?project_ids="+project.ID.String()+"&inbox_limit=10", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}

	if got := intValue(jsonPathValue(t, resp.Body, "data", "inbox", "unread_count")); got != 2 {
		t.Fatalf("inbox unread_count = %d, want 2 body=%s", got, string(resp.Body))
	}
	if got := intValue(jsonPathValue(t, resp.Body, "data", "projects", "0", "active_task_count")); got != 1 {
		t.Fatalf("active_task_count = %d, want 1 body=%s", got, string(resp.Body))
	}
	if got := intValue(jsonPathValue(t, resp.Body, "data", "projects", "0", "blocked_task_count")); got != 1 {
		t.Fatalf("blocked_task_count = %d, want 1 body=%s", got, string(resp.Body))
	}
	if got := jsonPathString(t, resp.Body, "data", "projects", "0", "latest_deploy", "status"); got != "deployed" {
		t.Fatalf("latest_deploy.status = %q, want %q body=%s", got, "deployed", string(resp.Body))
	}
	if got := intValue(jsonPathValue(t, resp.Body, "data", "recent_sessions", "0", "unread_message_count")); got != 1 {
		t.Fatalf("recent_sessions unread_message_count = %d, want 1 body=%s", got, string(resp.Body))
	}
}

type authIntegrationServer struct {
	URL  string
	Pool *pgxpool.Pool
	ts   *httptest.Server
}

func (s *authIntegrationServer) Close() {
	if s == nil {
		return
	}
	s.ts.Close()
}

func newAuthTestServer(t *testing.T, authMode string) (*authIntegrationServer, repo.HumanUser, repo.HumanUser) {
	t.Helper()

	pool := testdb.New(t)
	org, adminUser := createOrgAndUser(t, pool, "auth-http-org", "admin@example.com", "Admin", "admin", "admin-password")
	_, memberUser := createOrgAndUser(t, pool, "auth-http-org", "member@example.com", "Member", "member", "member-password")

	service, err := authsvc.NewService(authsvc.Options{
		Users:        repo.NewHumanUserRepo(pool),
		Sessions:     repo.NewAuthSessionRepo(pool),
		APIKeys:      repo.NewAPIKeyRepo(pool),
		DefaultOrgID: org.ID,
		AuthMode:     authMode,
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthService: service,
		Pool:        pool,
	})

	ts := httptest.NewServer(handler)
	return &authIntegrationServer{URL: ts.URL, Pool: pool, ts: ts}, adminUser, memberUser
}

func createOrgAndUser(t *testing.T, pool *pgxpool.Pool, slug, email, displayName, role, password string) (repo.Organization, repo.HumanUser) {
	t.Helper()

	ctx := context.Background()
	orgRepo := repo.NewOrgRepo(pool)
	userRepo := repo.NewHumanUserRepo(pool)

	org, err := orgRepo.GetBySlug(ctx, slug)
	if err != nil {
		org, err = orgRepo.Create(ctx, repo.Organization{Slug: slug, DisplayName: slug})
		if err != nil {
			t.Fatalf("create org: %v", err)
		}
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	passwordHash := string(hashed)

	user, err := userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: org.ID,
		Email:          email,
		DisplayName:    displayName,
		PasswordHash:   &passwordHash,
		Role:           role,
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	return org, user
}

type jsonResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

func mustJSON(t *testing.T, method, url string, payload any, headers map[string]string) jsonResponse {
	t.Helper()

	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return jsonResponse{StatusCode: resp.StatusCode, Headers: resp.Header.Clone(), Body: respBody}
}

func loginToken(t *testing.T, baseURL, email, password string) string {
	t.Helper()
	resp := mustJSON(t, http.MethodPost, baseURL+"/v1/auth/login", map[string]any{
		"email":    email,
		"password": password,
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}
	return jsonPathString(t, resp.Body, "data", "token")
}

func jsonPathString(t *testing.T, body []byte, path ...string) string {
	t.Helper()
	current := jsonPathValue(t, body, path...)
	value, _ := current.(string)
	return value
}

func hasJSONPath(body []byte, path ...string) bool {
	current := any(nil)
	if err := json.Unmarshal(body, &current); err != nil {
		return false
	}
	for _, segment := range path {
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[segment]
			if !ok {
				return false
			}
			current = next
		case []any:
			if segment == "0" && len(typed) > 0 {
				current = typed[0]
				continue
			}
			return false
		default:
			return false
		}
	}
	return true
}

func jsonPathValue(t *testing.T, body []byte, path ...string) any {
	t.Helper()
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}

	for _, segment := range path {
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[segment]
			if !ok {
				t.Fatalf("json path %v missing at %q in body: %s", path, segment, string(body))
			}
			current = next
		case []any:
			if segment != "0" || len(typed) == 0 {
				t.Fatalf("json path %v invalid array segment %q in body: %s", path, segment, string(body))
			}
			current = typed[0]
		default:
			t.Fatalf("json path %v invalid segment %q (type %T) in body: %s", path, segment, current, string(body))
		}
	}

	return current
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func stringValue(value any) string {
	str, _ := value.(string)
	return str
}

func sha256Hex(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
