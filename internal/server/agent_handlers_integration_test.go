//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	agentsvc "github.com/samhotchkiss/otter-camp/internal/agent"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestAgentHTTPCreatePauseRetireTempAndListFilters(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, _, adminUser, _ := newAgentTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.ts.URL, adminUser.Email, "admin-password")

	createStaff := mustJSON(t, http.MethodPost, testServer.ts.URL+"/v1/agents", map[string]any{
		"display_name": "Build PM",
		"agent_class":  "staff",
		"agent_type":   "pm",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createStaff.StatusCode != http.StatusCreated {
		t.Fatalf("create staff status = %d, want %d body=%s", createStaff.StatusCode, http.StatusCreated, string(createStaff.Body))
	}
	staffID := jsonPathString(t, createStaff.Body, "data", "id")
	if staffID == "" {
		t.Fatal("expected staff id in create response")
	}

	unpauseResp := mustJSON(t, http.MethodPost, testServer.ts.URL+"/v1/agents/"+staffID+"/unpause", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if unpauseResp.StatusCode != http.StatusOK {
		t.Fatalf("unpause status = %d, want %d body=%s", unpauseResp.StatusCode, http.StatusOK, string(unpauseResp.Body))
	}

	pauseResp := mustJSON(t, http.MethodPost, testServer.ts.URL+"/v1/agents/"+staffID+"/pause", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if pauseResp.StatusCode != http.StatusOK {
		t.Fatalf("pause status = %d, want %d body=%s", pauseResp.StatusCode, http.StatusOK, string(pauseResp.Body))
	}

	getStaff := mustJSON(t, http.MethodGet, testServer.ts.URL+"/v1/agents/"+staffID, nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if getStaff.StatusCode != http.StatusOK {
		t.Fatalf("get staff status = %d, want %d body=%s", getStaff.StatusCode, http.StatusOK, string(getStaff.Body))
	}
	if got := jsonPathString(t, getStaff.Body, "data", "lifecycle_status"); got != "paused" {
		t.Fatalf("staff lifecycle_status = %q, want %q", got, "paused")
	}

	pauseAgain := mustJSON(t, http.MethodPost, testServer.ts.URL+"/v1/agents/"+staffID+"/pause", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if pauseAgain.StatusCode != http.StatusConflict {
		t.Fatalf("pause again status = %d, want %d body=%s", pauseAgain.StatusCode, http.StatusConflict, string(pauseAgain.Body))
	}
	if got := jsonPathString(t, pauseAgain.Body, "error", "code"); got != "invalid_transition" {
		t.Fatalf("pause again error code = %q, want %q", got, "invalid_transition")
	}

	createTemp := mustJSON(t, http.MethodPost, testServer.ts.URL+"/v1/agents", map[string]any{
		"display_name":    "Temp Worker",
		"agent_class":     "temp",
		"agent_type":      "worker",
		"temp_project_id": uuid.NewString(),
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createTemp.StatusCode != http.StatusCreated {
		t.Fatalf("create temp status = %d, want %d body=%s", createTemp.StatusCode, http.StatusCreated, string(createTemp.Body))
	}
	tempID := jsonPathString(t, createTemp.Body, "data", "id")
	if tempID == "" {
		t.Fatal("expected temp id in create response")
	}

	retireTemp := mustJSON(t, http.MethodPost, testServer.ts.URL+"/v1/agents/"+tempID+"/retire", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if retireTemp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("retire temp status = %d, want %d body=%s", retireTemp.StatusCode, http.StatusUnprocessableEntity, string(retireTemp.Body))
	}

	filtered := mustJSON(t, http.MethodGet, testServer.ts.URL+"/v1/agents?lifecycle_status=active&agent_class=temp", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if filtered.StatusCode != http.StatusOK {
		t.Fatalf("list filtered status = %d, want %d body=%s", filtered.StatusCode, http.StatusOK, string(filtered.Body))
	}

	items, ok := jsonPathValue(t, filtered.Body, "data").([]any)
	if !ok {
		t.Fatalf("filtered data type = %T, want []any", jsonPathValue(t, filtered.Body, "data"))
	}
	if len(items) != 1 {
		t.Fatalf("filtered list length = %d, want 1 body=%s", len(items), string(filtered.Body))
	}

	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("filtered item type = %T, want map[string]any", items[0])
	}
	if got := first["agent_class"]; got != "temp" {
		t.Fatalf("filtered item agent_class = %v, want temp", got)
	}
	if got := first["lifecycle_status"]; got != "active" {
		t.Fatalf("filtered item lifecycle_status = %v, want active", got)
	}

	totalValue := jsonPathValue(t, filtered.Body, "meta", "pagination", "total")
	total, ok := totalValue.(float64)
	if !ok || int(total) != 1 {
		t.Fatalf("filtered meta.pagination.total = %v, want 1", totalValue)
	}

}

func TestAgentHTTPAuthRBACAndTempLimit(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, org, adminUser, memberUser := newAgentTestServer(t)
	defer testServer.Close()

	unauthenticated := mustJSON(t, http.MethodGet, testServer.ts.URL+"/v1/agents", nil, nil)
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d body=%s", unauthenticated.StatusCode, http.StatusUnauthorized, string(unauthenticated.Body))
	}

	invalidToken := mustJSON(t, http.MethodGet, testServer.ts.URL+"/v1/agents", nil, map[string]string{
		"Authorization": "Bearer bad-token",
	})
	if invalidToken.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d, want %d body=%s", invalidToken.StatusCode, http.StatusUnauthorized, string(invalidToken.Body))
	}

	ctx := context.Background()
	orgRepo := repo.NewOrgRepo(testServer.pool)
	_, err := orgRepo.UpdateSettings(ctx, org.ID, repo.OrganizationSettings{
		Agents: repo.OrganizationAgentsSettings{MaxConcurrentTemps: 1},
	})
	if err != nil {
		t.Fatalf("update org settings: %v", err)
	}

	memberToken := loginToken(t, testServer.ts.URL, memberUser.Email, "member-password")
	memberCreateStaff := mustJSON(t, http.MethodPost, testServer.ts.URL+"/v1/agents", map[string]any{
		"display_name": "Member Staff",
		"agent_class":  "staff",
		"agent_type":   "worker",
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if memberCreateStaff.StatusCode != http.StatusForbidden {
		t.Fatalf("member create staff status = %d, want %d body=%s", memberCreateStaff.StatusCode, http.StatusForbidden, string(memberCreateStaff.Body))
	}

	memberCreateTemp := mustJSON(t, http.MethodPost, testServer.ts.URL+"/v1/agents", map[string]any{
		"display_name":    "Member Temp 1",
		"agent_class":     "temp",
		"agent_type":      "worker",
		"temp_project_id": uuid.NewString(),
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if memberCreateTemp.StatusCode != http.StatusCreated {
		t.Fatalf("member create temp status = %d, want %d body=%s", memberCreateTemp.StatusCode, http.StatusCreated, string(memberCreateTemp.Body))
	}

	memberCreateTemp2 := mustJSON(t, http.MethodPost, testServer.ts.URL+"/v1/agents", map[string]any{
		"display_name":    "Member Temp 2",
		"agent_class":     "temp",
		"agent_type":      "worker",
		"temp_project_id": uuid.NewString(),
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if memberCreateTemp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second member temp status = %d, want %d body=%s", memberCreateTemp2.StatusCode, http.StatusTooManyRequests, string(memberCreateTemp2.Body))
	}
	if got := jsonPathString(t, memberCreateTemp2.Body, "error", "code"); got != "temp_limit_reached" {
		t.Fatalf("second member temp error code = %q, want %q", got, "temp_limit_reached")
	}

	adminToken := loginToken(t, testServer.ts.URL, adminUser.Email, "admin-password")
	adminList := mustJSON(t, http.MethodGet, testServer.ts.URL+"/v1/agents", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if adminList.StatusCode != http.StatusOK {
		t.Fatalf("admin list status = %d, want %d body=%s", adminList.StatusCode, http.StatusOK, string(adminList.Body))
	}
}

type agentHTTPServer struct {
	ts   *httptest.Server
	pool *pgxpool.Pool
}

func (s *agentHTTPServer) Close() {
	if s == nil {
		return
	}
	s.ts.Close()
}

func newAgentTestServer(t *testing.T) (*agentHTTPServer, repo.Organization, repo.HumanUser, repo.HumanUser) {
	t.Helper()

	pool := testdb.New(t)
	org, adminUser := createOrgAndUser(t, pool, "agent-http-org", "agent-admin@example.com", "Agent Admin", "admin", "admin-password")
	_, memberUser := createOrgAndUser(t, pool, "agent-http-org", "agent-member@example.com", "Agent Member", "member", "member-password")

	authService, err := authsvc.NewService(authsvc.Options{
		Users:        repo.NewHumanUserRepo(pool),
		Sessions:     repo.NewAuthSessionRepo(pool),
		APIKeys:      repo.NewAPIKeyRepo(pool),
		AuthMode:     "standard",
		DefaultOrgID: org.ID,
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	eventBus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	agentService, err := agentsvc.NewService(agentsvc.Options{
		Pool:   pool,
		Agents: repo.NewAgentRepo(pool),
		Events: eventBus,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new agent service: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []RouteRegistrar{
			NewAgentRouteRegistrar(agentService, repo.NewAgentProfileTemplateRepo(pool)),
		},
	})

	ts := httptest.NewServer(handler)
	return &agentHTTPServer{ts: ts, pool: pool}, org, adminUser, memberUser
}
