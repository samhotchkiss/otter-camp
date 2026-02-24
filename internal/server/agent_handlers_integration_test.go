//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	agentdomain "github.com/samhotchkiss/otter-camp/internal/agent"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestAgentHTTPCreatePauseAndInvalidSecondPause(t *testing.T) {
	testServer := newAgentTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, testServer.AdminUser.Email, "admin-password")

	create := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents", map[string]any{
		"display_name": "Ops Reviewer",
		"agent_class":  "staff",
		"agent_type":   "reviewer",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", create.StatusCode, http.StatusCreated, string(create.Body))
	}

	agentID := jsonPathString(t, create.Body, "data", "id")
	if agentID == "" {
		t.Fatal("expected created agent id")
	}

	activate := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents/"+agentID+"/unpause", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if activate.StatusCode != http.StatusOK {
		t.Fatalf("activate status = %d, want %d body=%s", activate.StatusCode, http.StatusOK, string(activate.Body))
	}
	if got := jsonPathString(t, activate.Body, "data", "lifecycle_status"); got != "active" {
		t.Fatalf("activated lifecycle_status = %q, want %q", got, "active")
	}

	pause := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents/"+agentID+"/pause", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if pause.StatusCode != http.StatusOK {
		t.Fatalf("pause status = %d, want %d body=%s", pause.StatusCode, http.StatusOK, string(pause.Body))
	}
	if got := jsonPathString(t, pause.Body, "data", "lifecycle_status"); got != "paused" {
		t.Fatalf("lifecycle_status = %q, want %q", got, "paused")
	}

	pauseAgain := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents/"+agentID+"/pause", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if pauseAgain.StatusCode != http.StatusConflict {
		t.Fatalf("second pause status = %d, want %d body=%s", pauseAgain.StatusCode, http.StatusConflict, string(pauseAgain.Body))
	}
	if got := jsonPathString(t, pauseAgain.Body, "error", "code"); got != "invalid_transition" {
		t.Fatalf("error.code = %q, want %q", got, "invalid_transition")
	}

	getResp := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents/"+agentID, nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want %d body=%s", getResp.StatusCode, http.StatusOK, string(getResp.Body))
	}
	if got := jsonPathString(t, getResp.Body, "data", "lifecycle_status"); got != "paused" {
		t.Fatalf("GET lifecycle_status = %q, want %q", got, "paused")
	}
}

func TestAgentHTTPTempLimitAndRetireTempRejected(t *testing.T) {
	testServer := newAgentTestServer(t)
	defer testServer.Close()

	orgRepo := repo.NewOrgRepo(testServer.Pool)
	if _, err := orgRepo.UpdateSettings(context.Background(), testServer.Org.ID, repo.OrganizationSettings{
		Agents: repo.OrganizationAgentsSettings{MaxConcurrentTemps: 1},
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	adminToken := loginToken(t, testServer.URL, testServer.AdminUser.Email, "admin-password")

	createTemp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents", map[string]any{
		"display_name":    "Temp Worker 1",
		"agent_class":     "temp",
		"agent_type":      "worker",
		"temp_project_id": testServer.Org.ID.String(),
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createTemp.StatusCode != http.StatusCreated {
		t.Fatalf("create temp status = %d, want %d body=%s", createTemp.StatusCode, http.StatusCreated, string(createTemp.Body))
	}

	tempID := jsonPathString(t, createTemp.Body, "data", "id")
	if tempID == "" {
		t.Fatal("expected temp id")
	}

	createOverflow := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents", map[string]any{
		"display_name":    "Temp Worker 2",
		"agent_class":     "temp",
		"agent_type":      "worker",
		"temp_project_id": testServer.Org.ID.String(),
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if createOverflow.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("overflow status = %d, want %d body=%s", createOverflow.StatusCode, http.StatusTooManyRequests, string(createOverflow.Body))
	}
	if got := jsonPathString(t, createOverflow.Body, "error", "code"); got != "temp_limit_reached" {
		t.Fatalf("error.code = %q, want %q", got, "temp_limit_reached")
	}

	retireTemp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents/"+tempID+"/retire", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if retireTemp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("retire temp status = %d, want %d body=%s", retireTemp.StatusCode, http.StatusUnprocessableEntity, string(retireTemp.Body))
	}
}

func TestAgentHTTPListFilterAuthAndRBAC(t *testing.T) {
	testServer := newAgentTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, testServer.AdminUser.Email, "admin-password")
	memberToken := loginToken(t, testServer.URL, testServer.MemberUser.Email, "member-password")

	// staff starts in draft
	staff := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents", map[string]any{
		"display_name": "Staff PM",
		"agent_class":  "staff",
		"agent_type":   "pm",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if staff.StatusCode != http.StatusCreated {
		t.Fatalf("create staff status = %d, want %d body=%s", staff.StatusCode, http.StatusCreated, string(staff.Body))
	}

	temp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents", map[string]any{
		"display_name":    "Temp Active",
		"agent_class":     "temp",
		"agent_type":      "worker",
		"temp_project_id": testServer.Org.ID.String(),
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if temp.StatusCode != http.StatusCreated {
		t.Fatalf("create temp status = %d, want %d body=%s", temp.StatusCode, http.StatusCreated, string(temp.Body))
	}

	filtered := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents?lifecycle_status=active&agent_class=temp", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if filtered.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want %d body=%s", filtered.StatusCode, http.StatusOK, string(filtered.Body))
	}
	if got := jsonPathString(t, filtered.Body, "data", "0", "agent_class"); got != "temp" {
		t.Fatalf("first result agent_class = %q, want %q", got, "temp")
	}
	if got := jsonPathString(t, filtered.Body, "data", "0", "lifecycle_status"); got != "active" {
		t.Fatalf("first result lifecycle_status = %q, want %q", got, "active")
	}

	noAuth := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents", nil, nil)
	if noAuth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-auth status = %d, want %d body=%s", noAuth.StatusCode, http.StatusUnauthorized, string(noAuth.Body))
	}

	wrongToken := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents", nil, map[string]string{
		"Authorization": "Bearer invalid-token",
	})
	if wrongToken.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-token status = %d, want %d body=%s", wrongToken.StatusCode, http.StatusUnauthorized, string(wrongToken.Body))
	}

	memberCreateStaff := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents", map[string]any{
		"display_name": "Unauthorized Staff",
		"agent_class":  "staff",
		"agent_type":   "worker",
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if memberCreateStaff.StatusCode != http.StatusForbidden {
		t.Fatalf("member staff create status = %d, want %d body=%s", memberCreateStaff.StatusCode, http.StatusForbidden, string(memberCreateStaff.Body))
	}
}

type agentIntegrationServer struct {
	URL        string
	Pool       *pgxpool.Pool
	Org        repo.Organization
	AdminUser  repo.HumanUser
	MemberUser repo.HumanUser
	ts         *httptest.Server
}

func (s *agentIntegrationServer) Close() {
	if s == nil {
		return
	}
	s.ts.Close()
}

func newAgentTestServer(t *testing.T) *agentIntegrationServer {
	t.Helper()

	pool := testdb.New(t)
	org, adminUser := createOrgAndUser(t, pool, "agent-http-org", "agent-admin@example.com", "Agent Admin", "admin", "admin-password")
	_, memberUser := createOrgAndUser(t, pool, "agent-http-org", "agent-member@example.com", "Agent Member", "member", "member-password")

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

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	agentService, err := agentdomain.NewService(agentdomain.Options{
		Pool:   pool,
		Agents: repo.NewAgentRepo(pool),
		Events: eventbus.New(pool, logger, eventbus.Config{}),
		Clock:  clock.Real{},
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("new agent service: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      logger,
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []RouteRegistrar{
			NewAgentRouteRegistrar(agentService, repo.NewAgentProfileTemplateRepo(pool)),
		},
	})

	ts := httptest.NewServer(handler)
	return &agentIntegrationServer{
		URL:        ts.URL,
		Pool:       pool,
		Org:        org,
		AdminUser:  adminUser,
		MemberUser: memberUser,
		ts:         ts,
	}
}
