//go:build integration

package auth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/audit"
	authpkg "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/ratelimit"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/server"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/testutil"
	"golang.org/x/crypto/bcrypt"
)

func TestLogin_Success(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	user := testutil.MakeUser(t, pool, orgID, "admin")
	srv := newAuthHarness(t, pool, orgID, authHarnessOptions{})

	resp := doJSON(t, http.MethodPost, srv.URL+"/v1/auth/login", map[string]any{
		"email":    user.Email,
		"password": testutil.DefaultUserPassword,
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d want=%d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}

	token := jsonPathString(t, resp.Body, "data", "session_token")
	if strings.TrimSpace(token) == "" {
		t.Fatal("expected session_token in response")
	}

	var (
		userID    uuid.UUID
		expiresAt time.Time
	)
	err := pool.QueryRow(context.Background(), `
		SELECT user_id, expires_at
		FROM auth_session
		WHERE token_hash = $1
	`, sha256Hex(token)).Scan(&userID, &expiresAt)
	if err != nil {
		t.Fatalf("query auth_session: %v", err)
	}
	if userID != user.ID {
		t.Fatalf("session user_id=%s want=%s", userID, user.ID)
	}

	wantMin := srv.clock.Now().Add(30*24*time.Hour - time.Minute)
	wantMax := srv.clock.Now().Add(30*24*time.Hour + time.Minute)
	if expiresAt.Before(wantMin) || expiresAt.After(wantMax) {
		t.Fatalf("expires_at=%s outside expected window [%s,%s]", expiresAt, wantMin, wantMax)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	user := testutil.MakeUser(t, pool, orgID, "member")
	srv := newAuthHarness(t, pool, orgID, authHarnessOptions{})

	resp := doJSON(t, http.MethodPost, srv.URL+"/v1/auth/login", map[string]any{
		"email":    user.Email,
		"password": "wrong-password",
	}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login wrong password status=%d want=%d body=%s", resp.StatusCode, http.StatusUnauthorized, string(resp.Body))
	}

	var sessionCount int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM auth_session WHERE user_id = $1`, user.ID).Scan(&sessionCount); err != nil {
		t.Fatalf("count auth_session: %v", err)
	}
	if sessionCount != 0 {
		t.Fatalf("session rows=%d want=0", sessionCount)
	}

	var attempts int
	if err := pool.QueryRow(context.Background(), `SELECT failed_login_attempts FROM human_user WHERE id = $1`, user.ID).Scan(&attempts); err != nil {
		t.Fatalf("query failed_login_attempts: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("failed_login_attempts=%d want=1", attempts)
	}
}

func TestLogin_AccountLockout(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	user := testutil.MakeUser(t, pool, orgID, "member")
	srv := newAuthHarness(t, pool, orgID, authHarnessOptions{maxFailedAttempts: 5})

	for i := 0; i < 5; i++ {
		resp := doJSON(t, http.MethodPost, srv.URL+"/v1/auth/login", map[string]any{
			"email":    user.Email,
			"password": "wrong-password",
		}, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("failed login attempt %d status=%d want=%d body=%s", i+1, resp.StatusCode, http.StatusUnauthorized, string(resp.Body))
		}
	}

	sixth := doJSON(t, http.MethodPost, srv.URL+"/v1/auth/login", map[string]any{
		"email":    user.Email,
		"password": testutil.DefaultUserPassword,
	}, nil)
	if sixth.StatusCode != http.StatusLocked {
		t.Fatalf("6th login status=%d want=%d body=%s", sixth.StatusCode, http.StatusLocked, string(sixth.Body))
	}

	var lockedUntil *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT locked_until FROM human_user WHERE id = $1`, user.ID).Scan(&lockedUntil); err != nil {
		t.Fatalf("query locked_until: %v", err)
	}
	if lockedUntil == nil {
		t.Fatal("expected locked_until to be set")
	}

	waitFor(t, 2*time.Second, func() bool {
		var count int
		err := pool.QueryRow(context.Background(), `
			SELECT COUNT(*)
			FROM audit_event
			WHERE organization_id = $1
			  AND event_type = 'login_failed_lockout'
			  AND principal_type = 'human'
			  AND principal_id = $2
		`, orgID, user.ID).Scan(&count)
		return err == nil && count >= 1
	}, "expected login_failed_lockout audit_event row")
}

func TestSession_SlidingExpiry(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	user := testutil.MakeUser(t, pool, orgID, "member")
	srv := newAuthHarness(t, pool, orgID, authHarnessOptions{})

	token := testutil.LoginUser(t, srv.server, user.Email, testutil.DefaultUserPassword)

	initial := mustSessionExpiry(t, pool, token)
	srv.clock.Advance(6 * time.Hour)

	me := doJSON(t, http.MethodGet, srv.URL+"/v1/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if me.StatusCode != http.StatusOK {
		t.Fatalf("me status=%d want=%d body=%s", me.StatusCode, http.StatusOK, string(me.Body))
	}

	var refreshed time.Time
	waitFor(t, 2*time.Second, func() bool {
		refreshed = mustSessionExpiry(t, pool, token)
		return refreshed.After(initial)
	}, "expected session expiry to refresh")

	second := doJSON(t, http.MethodGet, srv.URL+"/v1/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second me status=%d want=%d body=%s", second.StatusCode, http.StatusOK, string(second.Body))
	}

	time.Sleep(100 * time.Millisecond)
	afterSecond := mustSessionExpiry(t, pool, token)
	if !afterSecond.Equal(refreshed) {
		t.Fatalf("second me unexpectedly re-extended expiry: got=%s want=%s", afterSecond, refreshed)
	}
}

func TestSession_Revocation(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	user := testutil.MakeUser(t, pool, orgID, "member")
	srv := newAuthHarness(t, pool, orgID, authHarnessOptions{})

	token := testutil.LoginUser(t, srv.server, user.Email, testutil.DefaultUserPassword)
	logout := doJSON(t, http.MethodPost, srv.URL+"/v1/auth/logout", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if logout.StatusCode != http.StatusOK {
		t.Fatalf("logout status=%d want=%d body=%s", logout.StatusCode, http.StatusOK, string(logout.Body))
	}

	me := doJSON(t, http.MethodGet, srv.URL+"/v1/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if me.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after logout status=%d want=%d body=%s", me.StatusCode, http.StatusUnauthorized, string(me.Body))
	}

	var revokedAt *time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT revoked_at
		FROM auth_session
		WHERE token_hash = $1
	`, sha256Hex(token)).Scan(&revokedAt); err != nil {
		t.Fatalf("query revoked_at: %v", err)
	}
	if revokedAt == nil {
		t.Fatal("expected revoked_at to be set")
	}
}

func TestSession_ExpiredToken(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	user := testutil.MakeUser(t, pool, orgID, "member")
	srv := newAuthHarness(t, pool, orgID, authHarnessOptions{})

	rawToken := "expired-session-token"
	_, err := repo.NewAuthSessionRepo(pool).Create(context.Background(), repo.AuthSession{
		UserID:    user.ID,
		TokenHash: sha256Hex(rawToken),
		ExpiresAt: srv.clock.Now().Add(-1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create expired session: %v", err)
	}

	resp := doJSON(t, http.MethodGet, srv.URL+"/v1/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + rawToken,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me with expired token status=%d want=%d body=%s", resp.StatusCode, http.StatusUnauthorized, string(resp.Body))
	}

	var expiresAt time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT expires_at
		FROM auth_session
		WHERE token_hash = $1
	`, sha256Hex(rawToken)).Scan(&expiresAt); err != nil {
		t.Fatalf("query expired session row: %v", err)
	}
	if expiresAt.After(srv.clock.Now()) {
		t.Fatalf("expired session unexpectedly refreshed: expires_at=%s now=%s", expiresAt, srv.clock.Now())
	}
}

func TestAPIKey_Issuance(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	user := testutil.MakeUser(t, pool, orgID, "member")
	srv := newAuthHarness(t, pool, orgID, authHarnessOptions{})

	token := testutil.LoginUser(t, srv.server, user.Email, testutil.DefaultUserPassword)
	resp := doJSON(t, http.MethodPost, srv.URL+"/v1/api-keys", map[string]any{
		"display_name": "integration-key",
		"scopes":       []string{"chat:read"},
	}, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("issue api key status=%d want=%d body=%s", resp.StatusCode, http.StatusCreated, string(resp.Body))
	}

	keyID := mustUUID(t, jsonPathString(t, resp.Body, "data", "id"))
	rawKey := jsonPathString(t, resp.Body, "data", "key")
	if strings.TrimSpace(rawKey) == "" {
		t.Fatal("expected raw key in response")
	}

	var storedHash string
	if err := pool.QueryRow(context.Background(), `SELECT key_hash FROM api_key WHERE id = $1`, keyID).Scan(&storedHash); err != nil {
		t.Fatalf("query api_key hash: %v", err)
	}
	if storedHash == rawKey {
		t.Fatal("api_key.key_hash should not store the raw key")
	}
	if storedHash != sha256Hex(rawKey) {
		t.Fatalf("stored hash mismatch: got=%q want=%q", storedHash, sha256Hex(rawKey))
	}
}

func TestAPIKey_Validation(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	user := testutil.MakeUser(t, pool, orgID, "member")
	srv := newAuthHarness(t, pool, orgID, authHarnessOptions{})

	token := testutil.LoginUser(t, srv.server, user.Email, testutil.DefaultUserPassword)
	issued := doJSON(t, http.MethodPost, srv.URL+"/v1/api-keys", map[string]any{
		"display_name": "bearer-key",
		"scopes":       []string{"chat:read"},
	}, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if issued.StatusCode != http.StatusCreated {
		t.Fatalf("issue key status=%d want=%d body=%s", issued.StatusCode, http.StatusCreated, string(issued.Body))
	}

	keyID := mustUUID(t, jsonPathString(t, issued.Body, "data", "id"))
	rawKey := jsonPathString(t, issued.Body, "data", "key")
	me := doJSON(t, http.MethodGet, srv.URL+"/v1/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + rawKey,
	})
	if me.StatusCode != http.StatusOK {
		t.Fatalf("me via bearer api key status=%d want=%d body=%s", me.StatusCode, http.StatusOK, string(me.Body))
	}
	if got := jsonPathString(t, me.Body, "data", "email"); got != user.Email {
		t.Fatalf("me email=%q want=%q", got, user.Email)
	}

	var lastUsed *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT last_used_at FROM api_key WHERE id = $1`, keyID).Scan(&lastUsed); err != nil {
		t.Fatalf("query api_key.last_used_at: %v", err)
	}
	if lastUsed == nil {
		t.Fatal("expected api_key.last_used_at to be updated")
	}
}

func TestAPIKey_Revocation(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	user := testutil.MakeUser(t, pool, orgID, "member")
	srv := newAuthHarness(t, pool, orgID, authHarnessOptions{})

	token := testutil.LoginUser(t, srv.server, user.Email, testutil.DefaultUserPassword)
	issued := doJSON(t, http.MethodPost, srv.URL+"/v1/api-keys", map[string]any{
		"display_name": "revocation-key",
		"scopes":       []string{"chat:read"},
	}, map[string]string{"Authorization": "Bearer " + token})
	if issued.StatusCode != http.StatusCreated {
		t.Fatalf("issue key status=%d want=%d body=%s", issued.StatusCode, http.StatusCreated, string(issued.Body))
	}

	keyID := jsonPathString(t, issued.Body, "data", "id")
	rawKey := jsonPathString(t, issued.Body, "data", "key")
	revoked := doJSON(t, http.MethodDelete, srv.URL+"/v1/api-keys/"+keyID, nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if revoked.StatusCode != http.StatusOK {
		t.Fatalf("revoke key status=%d want=%d body=%s", revoked.StatusCode, http.StatusOK, string(revoked.Body))
	}

	after := doJSON(t, http.MethodGet, srv.URL+"/v1/auth/me", nil, map[string]string{
		"X-API-Key": rawKey,
	})
	if after.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after key revoke status=%d want=%d body=%s", after.StatusCode, http.StatusUnauthorized, string(after.Body))
	}

	var revokedAt *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT revoked_at FROM api_key WHERE id = $1`, keyID).Scan(&revokedAt); err != nil {
		t.Fatalf("query api_key.revoked_at: %v", err)
	}
	if revokedAt == nil {
		t.Fatal("expected api_key.revoked_at to be set")
	}
}

func TestRBAC_AdminOnly(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	admin := testutil.MakeUser(t, pool, orgID, "admin")
	member := testutil.MakeUser(t, pool, orgID, "member")
	srv := newAuthHarness(t, pool, orgID, authHarnessOptions{})

	memberToken := testutil.LoginUser(t, srv.server, member.Email, testutil.DefaultUserPassword)
	memberResp := doJSON(t, http.MethodPost, srv.URL+"/v1/agents", map[string]any{"name": "agent-member"}, map[string]string{
		"Authorization": "Bearer " + memberToken,
	})
	if memberResp.StatusCode != http.StatusForbidden {
		t.Fatalf("member admin-only status=%d want=%d body=%s", memberResp.StatusCode, http.StatusForbidden, string(memberResp.Body))
	}

	adminToken := testutil.LoginUser(t, srv.server, admin.Email, testutil.DefaultUserPassword)
	adminResp := doJSON(t, http.MethodPost, srv.URL+"/v1/agents", map[string]any{"name": "agent-admin"}, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if adminResp.StatusCode < 200 || adminResp.StatusCode >= 300 {
		t.Fatalf("admin admin-only status=%d want 2xx body=%s", adminResp.StatusCode, string(adminResp.Body))
	}
}

func TestOrgIsolation_CrossOrgData(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	orgA := testutil.MakeOrg(t, pool)
	orgB := testutil.MakeOrg(t, pool)
	userA := testutil.MakeUser(t, pool, orgA, "admin")
	userB := testutil.MakeUser(t, pool, orgB, "admin")

	srvA := newAuthHarness(t, pool, orgA, authHarnessOptions{})
	srvB := newAuthHarness(t, pool, orgB, authHarnessOptions{})

	tokenA := testutil.LoginUser(t, srvA.server, userA.Email, testutil.DefaultUserPassword)
	tokenB := testutil.LoginUser(t, srvB.server, userB.Email, testutil.DefaultUserPassword)

	createdB := doJSON(t, http.MethodPost, srvB.URL+"/v1/projects", map[string]any{"name": "project-b"}, map[string]string{
		"Authorization": "Bearer " + tokenB,
	})
	if createdB.StatusCode != http.StatusCreated {
		t.Fatalf("create project B status=%d want=%d body=%s", createdB.StatusCode, http.StatusCreated, string(createdB.Body))
	}
	projectBID := jsonPathString(t, createdB.Body, "data", "id")

	cross := doJSON(t, http.MethodGet, srvA.URL+"/v1/projects/"+projectBID, nil, map[string]string{
		"Authorization": "Bearer " + tokenA,
	})
	if cross.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-org get status=%d want=%d body=%s", cross.StatusCode, http.StatusNotFound, string(cross.Body))
	}

	listA := doJSON(t, http.MethodGet, srvA.URL+"/v1/projects", nil, map[string]string{
		"Authorization": "Bearer " + tokenA,
	})
	if listA.StatusCode != http.StatusOK {
		t.Fatalf("list projects A status=%d want=%d body=%s", listA.StatusCode, http.StatusOK, string(listA.Body))
	}
	if listContainsID(t, listA.Body, projectBID) {
		t.Fatalf("org A list unexpectedly contains org B project id %s", projectBID)
	}
}

func TestOrgIsolation_AuditEvents(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	orgA := testutil.MakeOrg(t, pool)
	orgB := testutil.MakeOrg(t, pool)
	userA := testutil.MakeUser(t, pool, orgA, "admin")
	userB := testutil.MakeUser(t, pool, orgB, "admin")

	srvA := newAuthHarness(t, pool, orgA, authHarnessOptions{})
	srvB := newAuthHarness(t, pool, orgB, authHarnessOptions{})

	tokenA := testutil.LoginUser(t, srvA.server, userA.Email, testutil.DefaultUserPassword)
	tokenB := testutil.LoginUser(t, srvB.server, userB.Email, testutil.DefaultUserPassword)

	created := doJSON(t, http.MethodPost, srvA.URL+"/v1/projects", map[string]any{"name": "audit-visible-only-in-org-a"}, map[string]string{
		"Authorization": "Bearer " + tokenA,
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create project in org A status=%d want=%d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}

	auditListB := doJSON(t, http.MethodGet, srvB.URL+"/v1/audit-events", nil, map[string]string{
		"Authorization": "Bearer " + tokenB,
	})
	if auditListB.StatusCode != http.StatusOK {
		t.Fatalf("audit list org B status=%d want=%d body=%s", auditListB.StatusCode, http.StatusOK, string(auditListB.Body))
	}

	if got := jsonArrayLength(t, auditListB.Body, "data"); got != 0 {
		t.Fatalf("org B audit event count=%d want=0 body=%s", got, string(auditListB.Body))
	}
}

func TestRateLimit_PerIP(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	_ = testutil.MakeUser(t, pool, orgID, "member")
	srv := newAuthHarness(t, pool, orgID, authHarnessOptions{ipLimit: 10})

	for i := 1; i <= 11; i++ {
		resp := doJSON(t, http.MethodPost, srv.URL+"/v1/auth/login", map[string]any{
			"email":    "missing@example.com",
			"password": "wrong-password",
		}, map[string]string{"X-Forwarded-For": "203.0.113.50"})

		if i <= 10 && resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status=%d want=%d body=%s", i, resp.StatusCode, http.StatusUnauthorized, string(resp.Body))
		}
		if i == 11 {
			if resp.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("attempt %d status=%d want=%d body=%s", i, resp.StatusCode, http.StatusTooManyRequests, string(resp.Body))
			}
			if strings.TrimSpace(resp.Headers.Get("Retry-After")) == "" {
				t.Fatal("expected Retry-After header on rate-limited login")
			}
		}
	}
}

func TestLocalAuth_AutoLogin(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "local")

	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	admin := testutil.MakeUser(t, pool, orgID, "admin")
	srv := newAuthHarness(t, pool, orgID, authHarnessOptions{authMode: "local"})

	me := doJSON(t, http.MethodGet, srv.URL+"/v1/auth/me", nil, nil)
	if me.StatusCode != http.StatusOK {
		t.Fatalf("local auth me status=%d want=%d body=%s", me.StatusCode, http.StatusOK, string(me.Body))
	}
	if got := jsonPathString(t, me.Body, "data", "email"); got != admin.Email {
		t.Fatalf("local auth me email=%q want=%q", got, admin.Email)
	}
}

func TestAuditEvent_DelegationPrincipal(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	user := testutil.MakeUser(t, pool, orgID, "admin")
	srv := newAuthHarness(t, pool, orgID, authHarnessOptions{})

	token := testutil.LoginUser(t, srv.server, user.Email, testutil.DefaultUserPassword)
	resp := doJSON(t, http.MethodPost, srv.URL+"/v1/delegated-actions", map[string]any{"action": "simulate-agent-delegation"}, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("delegated action status=%d want=%d body=%s", resp.StatusCode, http.StatusCreated, string(resp.Body))
	}
	agentID := mustUUID(t, jsonPathString(t, resp.Body, "data", "principal_id"))

	var (
		principalType   string
		principalID     uuid.UUID
		delegatedByType *string
		delegatedByID   *uuid.UUID
	)
	err := pool.QueryRow(context.Background(), `
		SELECT principal_type, principal_id, delegated_by_type, delegated_by_id
		FROM audit_event
		WHERE organization_id = $1
		  AND event_type = 'delegated.action'
		ORDER BY created_at DESC
		LIMIT 1
	`, orgID).Scan(&principalType, &principalID, &delegatedByType, &delegatedByID)
	if err != nil {
		t.Fatalf("query delegated audit_event: %v", err)
	}
	if principalType != "agent" {
		t.Fatalf("principal_type=%q want=%q", principalType, "agent")
	}
	if principalID != agentID {
		t.Fatalf("principal_id=%s want=%s", principalID, agentID)
	}
	if delegatedByType == nil || *delegatedByType != "human" {
		t.Fatalf("delegated_by_type=%v want=%q", delegatedByType, "human")
	}
	if delegatedByID == nil || *delegatedByID != user.ID {
		t.Fatalf("delegated_by_id=%v want=%s", delegatedByID, user.ID)
	}
}

type authHarness struct {
	URL    string
	server *httptest.Server
	clock  *clock.Fake
}

type authHarnessOptions struct {
	authMode          string
	maxFailedAttempts int
	ipLimit           int
}

func newAuthHarness(t *testing.T, pool *pgxpool.Pool, defaultOrgID uuid.UUID, opts authHarnessOptions) authHarness {
	t.Helper()
	ensureIntegrationTables(t, pool)

	now := time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)

	authMode := strings.TrimSpace(opts.authMode)
	if authMode == "" {
		authMode = "standard"
	}

	ipLimit := opts.ipLimit
	if ipLimit <= 0 {
		ipLimit = 20
	}

	service, err := authpkg.NewService(authpkg.Options{
		Users:                  repo.NewHumanUserRepo(pool),
		Sessions:               repo.NewAuthSessionRepo(pool),
		APIKeys:                repo.NewAPIKeyRepo(pool),
		Clock:                  fakeClock,
		IPLimiter:              ratelimit.New(ipLimit, 15*time.Minute, fakeClock),
		BcryptCost:             bcrypt.MinCost,
		AuthMode:               authMode,
		DefaultOrgID:           defaultOrgID,
		MaxFailedLoginAttempts: opts.maxFailedAttempts,
		AuditRecorder:          audit.NewService(repo.NewAuditEventRepo(pool), slog.New(slog.NewTextHandler(io.Discard, nil))),
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	handler := server.NewHandlerWithOptions(server.HandlerOptions{
		Version:     "test-version",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthService: service,
		Pool:        pool,
		RouteRegistrars: []server.RouteRegistrar{
			authIntegrationRouteRegistrar{pool: pool},
		},
	})

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return authHarness{
		URL:    ts.URL,
		server: ts,
		clock:  fakeClock,
	}
}

type authIntegrationRouteRegistrar struct {
	pool *pgxpool.Pool
}

func (r authIntegrationRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.With(middleware.RequireRole("admin")).Post("/agents", func(w http.ResponseWriter, req *http.Request) {
		api.JSON(w, http.StatusCreated, map[string]any{"ok": true})
	})

	router.Post("/projects", r.createProject)
	router.Get("/projects", r.listProjects)
	router.Get("/projects/{id}", r.getProject)
	router.Get("/audit-events", r.listAuditEvents)
	router.Post("/delegated-actions", r.delegatedAction)
}

func (r authIntegrationRouteRegistrar) createProject(w http.ResponseWriter, req *http.Request) {
	principal, ok := middleware.PrincipalFromContext(req.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	var payload struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		api.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "name is required")
		return
	}

	var projectID uuid.UUID
	err := r.pool.QueryRow(req.Context(), `
		INSERT INTO integration_project (organization_id, name)
		VALUES ($1, $2)
		RETURNING id
	`, principal.OrganizationID, name).Scan(&projectID)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to create project")
		return
	}

	targetType := "project"
	targetID := projectID
	_ = repo.NewAuditEventRepo(r.pool).Insert(req.Context(), repo.AuditEvent{
		OrganizationID: principal.OrganizationID,
		EventType:      "project.created",
		PrincipalType:  "human",
		PrincipalID:    principal.UserID,
		TargetType:     &targetType,
		TargetID:       &targetID,
		Metadata: map[string]any{
			"name": name,
		},
	})

	api.JSON(w, http.StatusCreated, map[string]any{
		"id":   projectID,
		"name": name,
	})
}

func (r authIntegrationRouteRegistrar) listProjects(w http.ResponseWriter, req *http.Request) {
	principal, ok := middleware.PrincipalFromContext(req.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	rows, err := r.pool.Query(req.Context(), `
		SELECT id, name
		FROM integration_project
		WHERE organization_id = $1
		ORDER BY created_at ASC
	`, principal.OrganizationID)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list projects")
		return
	}
	defer rows.Close()

	type projectListItem struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	}
	out := make([]projectListItem, 0)
	for rows.Next() {
		var item projectListItem
		if scanErr := rows.Scan(&item.ID, &item.Name); scanErr != nil {
			api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list projects")
			return
		}
		out = append(out, item)
	}
	if rows.Err() != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list projects")
		return
	}

	api.JSON(w, http.StatusOK, out)
}

func (r authIntegrationRouteRegistrar) getProject(w http.ResponseWriter, req *http.Request) {
	principal, ok := middleware.PrincipalFromContext(req.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	projectID, err := uuid.Parse(chi.URLParam(req, "id"))
	if err != nil {
		api.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "project not found")
		return
	}

	var (
		organizationID uuid.UUID
		name           string
	)
	err = r.pool.QueryRow(req.Context(), `
		SELECT organization_id, name
		FROM integration_project
		WHERE id = $1
	`, projectID).Scan(&organizationID, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "project not found")
		return
	}
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to load project")
		return
	}
	if organizationID != principal.OrganizationID {
		api.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "project not found")
		return
	}

	api.JSON(w, http.StatusOK, map[string]any{
		"id":   projectID,
		"name": name,
	})
}

func (r authIntegrationRouteRegistrar) listAuditEvents(w http.ResponseWriter, req *http.Request) {
	principal, ok := middleware.PrincipalFromContext(req.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	events, err := repo.NewAuditEventRepo(r.pool).ListByOrg(req.Context(), principal.OrganizationID, repo.AuditEventFilters{}, repo.Pagination{Limit: 100})
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list audit events")
		return
	}

	type auditItem struct {
		ID        uuid.UUID `json:"id"`
		EventType string    `json:"event_type"`
	}
	out := make([]auditItem, 0, len(events))
	for _, event := range events {
		out = append(out, auditItem{
			ID:        event.ID,
			EventType: event.EventType,
		})
	}
	api.JSON(w, http.StatusOK, out)
}

func (r authIntegrationRouteRegistrar) delegatedAction(w http.ResponseWriter, req *http.Request) {
	principal, ok := middleware.PrincipalFromContext(req.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	agentID := uuid.New()
	delegatedByType := "human"
	delegatedByID := principal.UserID
	if err := repo.NewAuditEventRepo(r.pool).Insert(req.Context(), repo.AuditEvent{
		OrganizationID:  principal.OrganizationID,
		EventType:       "delegated.action",
		PrincipalType:   "agent",
		PrincipalID:     agentID,
		DelegatedByType: &delegatedByType,
		DelegatedByID:   &delegatedByID,
		Metadata:        map[string]any{"source": "integration_test"},
	}); err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to write audit event")
		return
	}

	api.JSON(w, http.StatusCreated, map[string]any{
		"principal_id": agentID,
	})
}

func ensureIntegrationTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS integration_project (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
			name text NOT NULL,
			created_at timestamptz NOT NULL DEFAULT now()
		)
	`)
	if err != nil {
		t.Fatalf("create integration_project table: %v", err)
	}
}

type httpResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

func doJSON(t *testing.T, method, url string, payload any, headers map[string]string) httpResponse {
	t.Helper()

	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(encoded)
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
		t.Fatalf("execute request: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	return httpResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header.Clone(),
		Body:       respBody,
	}
}

func jsonPathString(t *testing.T, body []byte, path ...string) string {
	t.Helper()
	value, _ := jsonPathValue(t, body, path...).(string)
	return value
}

func jsonArrayLength(t *testing.T, body []byte, path ...string) int {
	t.Helper()
	value := jsonPathValue(t, body, path...)
	array, ok := value.([]any)
	if !ok {
		t.Fatalf("json path %v is %T, want []any in body=%s", path, value, string(body))
	}
	return len(array)
}

func listContainsID(t *testing.T, body []byte, id string) bool {
	t.Helper()
	items, ok := jsonPathValue(t, body, "data").([]any)
	if !ok {
		t.Fatalf("data is not a list in body=%s", string(body))
	}
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		value, _ := object["id"].(string)
		if value == id {
			return true
		}
	}
	return false
}

func jsonPathValue(t *testing.T, body []byte, path ...string) any {
	t.Helper()
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("unmarshal body: %v body=%s", err, string(body))
	}

	current := root
	for _, segment := range path {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("segment %q expected map, got %T (path=%v body=%s)", segment, current, path, string(body))
		}
		next, ok := object[segment]
		if !ok {
			t.Fatalf("missing segment %q (path=%v body=%s)", segment, path, string(body))
		}
		current = next
	}
	return current
}

func mustSessionExpiry(t *testing.T, pool *pgxpool.Pool, rawToken string) time.Time {
	t.Helper()
	var expiresAt time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT expires_at
		FROM auth_session
		WHERE token_hash = $1
	`, sha256Hex(rawToken)).Scan(&expiresAt); err != nil {
		t.Fatalf("query session expiry: %v", err)
	}
	return expiresAt
}

func sha256Hex(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:])
}

func mustUUID(t *testing.T, raw string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("parse uuid %q: %v", raw, err)
	}
	return id
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(message)
}
