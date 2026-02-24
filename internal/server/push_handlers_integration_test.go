//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/push"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestPushPreferenceAPIGetPatchRoundTrip(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	server, admin := newPushTestServer(t)
	defer server.Close()

	token := loginToken(t, server.URL, admin.Email, "admin-password")

	initial := mustJSON(t, http.MethodGet, server.URL+"/v1/me/push-preferences", nil, map[string]string{"Authorization": "Bearer " + token})
	if initial.StatusCode != http.StatusOK {
		t.Fatalf("initial GET status = %d, want %d body=%s", initial.StatusCode, http.StatusOK, string(initial.Body))
	}
	if got := jsonPathValue(t, initial.Body, "data", "tier_enabled", "urgent"); got != true {
		t.Fatalf("tier_enabled.urgent = %v, want true", got)
	}

	invalid := mustJSON(t, http.MethodPatch, server.URL+"/v1/me/push-preferences", map[string]any{
		"quiet_hours_start": "25:00",
	}, map[string]string{"Authorization": "Bearer " + token})
	if invalid.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid PATCH status = %d, want %d body=%s", invalid.StatusCode, http.StatusUnprocessableEntity, string(invalid.Body))
	}

	patched := mustJSON(t, http.MethodPatch, server.URL+"/v1/me/push-preferences", map[string]any{
		"tier_enabled":         map[string]any{"normal": false},
		"quiet_hours_enabled":  true,
		"quiet_hours_start":    "22:00",
		"quiet_hours_end":      "06:00",
		"quiet_hours_timezone": "UTC",
	}, map[string]string{"Authorization": "Bearer " + token})
	if patched.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status = %d, want %d body=%s", patched.StatusCode, http.StatusOK, string(patched.Body))
	}

	again := mustJSON(t, http.MethodGet, server.URL+"/v1/me/push-preferences", nil, map[string]string{"Authorization": "Bearer " + token})
	if again.StatusCode != http.StatusOK {
		t.Fatalf("second GET status = %d, want %d body=%s", again.StatusCode, http.StatusOK, string(again.Body))
	}
	if got := jsonPathValue(t, again.Body, "data", "tier_enabled", "normal"); got != false {
		t.Fatalf("tier_enabled.normal = %v, want false", got)
	}
	if got := jsonPathValue(t, again.Body, "data", "quiet_hours_enabled"); got != true {
		t.Fatalf("quiet_hours_enabled = %v, want true", got)
	}
}

func newPushTestServer(t *testing.T) (*authIntegrationServer, repo.HumanUser) {
	t.Helper()

	pool := testdb.New(t)
	org, adminUser := createOrgAndUser(t, pool, "push-http-org", "push-admin@example.com", "Push Admin", "admin", "admin-password")

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

	repository := push.NewPreferenceRepository(pool)
	service, err := push.NewPreferenceService(push.PreferenceServiceOptions{Repository: repository})
	if err != nil {
		t.Fatalf("new push preference service: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []RouteRegistrar{
			NewPushRouteRegistrar(service, repository),
		},
	})

	ts := httptest.NewServer(handler)
	return &authIntegrationServer{URL: ts.URL, Pool: pool, ts: ts}, adminUser
}

func TestPushTokenRegisterAndRevokeEndpoints(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	server, admin := newPushTestServer(t)
	defer server.Close()

	token := loginToken(t, server.URL, admin.Email, "admin-password")

	registered := mustJSON(t, http.MethodPost, server.URL+"/v1/me/push-token", map[string]any{
		"token":     "token-a",
		"platform":  "apns",
		"device_id": "device-a",
	}, map[string]string{"Authorization": "Bearer " + token})
	if registered.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d, want %d body=%s", registered.StatusCode, http.StatusOK, string(registered.Body))
	}

	updated := mustJSON(t, http.MethodPost, server.URL+"/v1/me/push-token", map[string]any{
		"token":     "token-a-updated",
		"platform":  "apns",
		"device_id": "device-a",
	}, map[string]string{"Authorization": "Bearer " + token})
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("register update status = %d, want %d body=%s", updated.StatusCode, http.StatusOK, string(updated.Body))
	}

	repoImpl := push.NewPreferenceRepository(server.Pool)
	tokens, err := repoImpl.GetTokens(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("GetTokens: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("token count = %d, want 1", len(tokens))
	}
	if tokens[0].Token != "token-a-updated" {
		t.Fatalf("device-a token = %q, want token-a-updated", tokens[0].Token)
	}

	revoked := mustJSON(t, http.MethodDelete, server.URL+"/v1/me/push-token/device-a", nil, map[string]string{"Authorization": "Bearer " + token})
	if revoked.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want %d body=%s", revoked.StatusCode, http.StatusNoContent, string(revoked.Body))
	}

	tokens, err = repoImpl.GetTokens(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("GetTokens after revoke: %v", err)
	}
	if len(tokens) != 0 {
		t.Fatalf("token count after revoke = %d, want 0", len(tokens))
	}
}
