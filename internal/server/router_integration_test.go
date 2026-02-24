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
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/bootstrap"
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
	if logoutResp.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d, want %d body=%s", logoutResp.StatusCode, http.StatusOK, string(logoutResp.Body))
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
	if revoked.StatusCode != http.StatusOK {
		t.Fatalf("revoke status = %d, want %d body=%s", revoked.StatusCode, http.StatusOK, string(revoked.Body))
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

func TestTestResetRouteInTestMode(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	bootstrapper := bootstrap.New(bootstrap.Options{
		Pool:    pool,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version: "test-version",
		Config: bootstrap.Config{
			OrgSlug:       "server-test-reset",
			OrgName:       "Server Test Reset",
			AdminEmail:    "admin@test.local",
			AdminPassword: "admin-password",
			SkillsDir:     t.TempDir(),
		},
	})
	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("bootstrap run: %v", err)
	}

	orgRepo := repo.NewOrgRepo(pool)
	if _, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "extra-org-before-reset",
		DisplayName: "Extra Org Before Reset",
	}); err != nil {
		t.Fatalf("create extra org: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:      "test-version",
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		TestMode:     true,
		TestResetter: bootstrapper,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	resp := mustJSON(t, http.MethodPost, server.URL+"/test/reset", nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /test/reset status = %d, want %d body=%s", resp.StatusCode, http.StatusNoContent, string(resp.Body))
	}

	var orgCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM organization`).Scan(&orgCount); err != nil {
		t.Fatalf("count organizations: %v", err)
	}
	if orgCount != 1 {
		t.Fatalf("organization count after reset = %d, want 1", orgCount)
	}

	var orgSlug string
	if err := pool.QueryRow(ctx, `SELECT slug FROM organization LIMIT 1`).Scan(&orgSlug); err != nil {
		t.Fatalf("select organization slug: %v", err)
	}
	if orgSlug != "server-test-reset" {
		t.Fatalf("organization slug after reset = %q, want %q", orgSlug, "server-test-reset")
	}
}

func TestTestResetRouteNotRegisteredInProductionMode(t *testing.T) {
	handler := NewHandlerWithOptions(HandlerOptions{
		Version:      "test-version",
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		TestMode:     false,
		TestResetter: nil,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	resp := mustJSON(t, http.MethodPost, server.URL+"/test/reset", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST /test/reset status = %d, want %d", resp.StatusCode, http.StatusNotFound)
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

func sha256Hex(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
