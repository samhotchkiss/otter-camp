//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/agent"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestAgentHTTPCreatePauseAndGet(t *testing.T) {
	testServer, _, adminUser, _ := newAgentTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents", map[string]any{
		"display_name":          "Builder Worker",
		"agent_class":           "staff",
		"agent_type":            "worker",
		"system_prompt":         "help with tasks",
		"operator_instructions": "be precise",
		"tool_allow_list":       []string{"repo.*"},
		"tool_deny_list":        []string{"browser.*"},
		"memory_read_scopes":    []string{"org"},
		"private_memory":        false,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}

	agentID := jsonPathString(t, created.Body, "data", "id")
	if agentID == "" {
		t.Fatalf("missing created agent id in body=%s", string(created.Body))
	}

	activated := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents/"+agentID+"/unpause", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if activated.StatusCode != http.StatusOK {
		t.Fatalf("activate status = %d, want %d body=%s", activated.StatusCode, http.StatusOK, string(activated.Body))
	}

	paused := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents/"+agentID+"/pause", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if paused.StatusCode != http.StatusOK {
		t.Fatalf("pause status = %d, want %d body=%s", paused.StatusCode, http.StatusOK, string(paused.Body))
	}

	got := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents/"+agentID, nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if got.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want %d body=%s", got.StatusCode, http.StatusOK, string(got.Body))
	}
	if status := jsonPathString(t, got.Body, "data", "lifecycle_status"); status != "paused" {
		t.Fatalf("lifecycle_status = %q, want %q body=%s", status, "paused", string(got.Body))
	}
}

func TestAgentHTTPAPIKeyScopeEnforcement(t *testing.T) {
	testServer, _, adminUser, _ := newAgentTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	issued := mustJSON(t, http.MethodPost, testServer.URL+"/v1/api-keys", map[string]any{
		"display_name": "agents-read-only",
		"scopes":       []string{"read:agents"},
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if issued.StatusCode != http.StatusCreated {
		t.Fatalf("issue key status = %d, want %d body=%s", issued.StatusCode, http.StatusCreated, string(issued.Body))
	}
	rawKey := jsonPathString(t, issued.Body, "data", "key")
	if rawKey == "" {
		t.Fatalf("missing api key in response body=%s", string(issued.Body))
	}

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents", nil, map[string]string{
		"X-API-Key": rawKey,
	})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list agents status = %d, want %d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}

	createDenied := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents", map[string]any{}, map[string]string{
		"X-API-Key": rawKey,
	})
	if createDenied.StatusCode != http.StatusForbidden {
		t.Fatalf("create agent status = %d, want %d body=%s", createDenied.StatusCode, http.StatusForbidden, string(createDenied.Body))
	}
}

func TestAgentHTTPCreateTempLimitExceeded(t *testing.T) {
	testServer, org, adminUser, _ := newAgentTestServer(t)
	defer testServer.Close()

	if _, err := testServer.Pool.Exec(context.Background(), `
		UPDATE organization
		SET settings = jsonb_set(COALESCE(settings, '{}'::jsonb), '{agents,max_concurrent_temps}', '1'::jsonb, true)
		WHERE id = $1
	`, org.ID); err != nil {
		t.Fatalf("update org temp limit: %v", err)
	}

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	first := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents", map[string]any{
		"display_name":    "Temp One",
		"agent_class":     "temp",
		"agent_type":      "worker",
		"temp_project_id": uuid.NewString(),
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first temp create status = %d, want %d body=%s", first.StatusCode, http.StatusCreated, string(first.Body))
	}

	second := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents", map[string]any{
		"display_name":    "Temp Two",
		"agent_class":     "temp",
		"agent_type":      "worker",
		"temp_project_id": uuid.NewString(),
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second temp create status = %d, want %d body=%s", second.StatusCode, http.StatusTooManyRequests, string(second.Body))
	}
	if code := jsonPathString(t, second.Body, "error", "code"); code != "temp_limit_reached" {
		t.Fatalf("error.code = %q, want %q body=%s", code, "temp_limit_reached", string(second.Body))
	}
}

func TestAgentHTTPListFiltersAuthAndRBAC(t *testing.T) {
	testServer, _, adminUser, memberUser := newAgentTestServer(t)
	defer testServer.Close()

	unauth := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents", nil, nil)
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth list status = %d, want %d body=%s", unauth.StatusCode, http.StatusUnauthorized, string(unauth.Body))
	}

	invalidToken := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents", nil, map[string]string{
		"Authorization": "Bearer invalid-token",
	})
	if invalidToken.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid token list status = %d, want %d body=%s", invalidToken.StatusCode, http.StatusUnauthorized, string(invalidToken.Body))
	}

	memberToken := loginToken(t, testServer.URL, memberUser.Email, "member-password")
	memberCreateStaff := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents", map[string]any{
		"display_name": "Nope",
		"agent_class":  "staff",
		"agent_type":   "worker",
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if memberCreateStaff.StatusCode != http.StatusForbidden {
		t.Fatalf("member create staff status = %d, want %d body=%s", memberCreateStaff.StatusCode, http.StatusForbidden, string(memberCreateStaff.Body))
	}

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	staff := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents", map[string]any{
		"display_name": "Draft Staff",
		"agent_class":  "staff",
		"agent_type":   "worker",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if staff.StatusCode != http.StatusCreated {
		t.Fatalf("create staff status = %d, want %d body=%s", staff.StatusCode, http.StatusCreated, string(staff.Body))
	}

	temp := mustJSON(t, http.MethodPost, testServer.URL+"/v1/agents", map[string]any{
		"display_name":    "Active Temp",
		"agent_class":     "temp",
		"agent_type":      "worker",
		"temp_project_id": uuid.NewString(),
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if temp.StatusCode != http.StatusCreated {
		t.Fatalf("create temp status = %d, want %d body=%s", temp.StatusCode, http.StatusCreated, string(temp.Body))
	}
	tempID := jsonPathString(t, temp.Body, "data", "id")

	filtered := mustJSON(t, http.MethodGet, testServer.URL+"/v1/agents?lifecycle_status=active&agent_class=temp", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if filtered.StatusCode != http.StatusOK {
		t.Fatalf("list filtered status = %d, want %d body=%s", filtered.StatusCode, http.StatusOK, string(filtered.Body))
	}

	data, ok := jsonPathValue(t, filtered.Body, "data").([]any)
	if !ok {
		t.Fatalf("data is not an array body=%s", string(filtered.Body))
	}
	if len(data) != 1 {
		t.Fatalf("filtered length = %d, want 1 body=%s", len(data), string(filtered.Body))
	}
	if got := jsonPathString(t, filtered.Body, "data", "0", "id"); got != tempID {
		t.Fatalf("filtered id = %q, want %q body=%s", got, tempID, string(filtered.Body))
	}
	if !hasJSONPath(filtered.Body, "meta", "pagination", "total") {
		t.Fatalf("expected meta.pagination.total body=%s", string(filtered.Body))
	}
}

func newAgentTestServer(t *testing.T) (*authIntegrationServer, repo.Organization, repo.HumanUser, repo.HumanUser) {
	t.Helper()

	pool := testdb.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	slug := "agent-http-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	adminEmail := "admin+" + slug + "@example.com"
	memberEmail := "member+" + slug + "@example.com"

	org, adminUser := createOrgAndUser(t, pool, slug, adminEmail, "Admin", "admin", "admin-password")
	_, memberUser := createOrgAndUser(t, pool, slug, memberEmail, "Member", "member", "member-password")

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

	bus := eventbus.New(pool, logger, eventbus.Config{})
	agentService, err := agent.NewService(agent.Options{
		Pool:   pool,
		Agents: repo.NewAgentRepo(pool),
		Events: bus,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("new agent service: %v", err)
	}
	assignmentService, err := agent.NewAssignmentService(agent.AssignmentServiceOptions{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("new assignment service: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      logger,
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []RouteRegistrar{
			NewAgentRouteRegistrar(
				agentService,
				repo.NewAgentProfileTemplateRepo(pool),
				assignmentService,
				repo.NewAgentRepo(pool),
				repo.NewProjectRepo(pool),
				repo.NewSkillRepo(pool),
				repo.NewAgentProjectAssignmentRepo(pool),
				repo.NewAgentSkillAttachmentRepo(pool),
			),
		},
	})

	ts := httptest.NewServer(handler)
	return &authIntegrationServer{URL: ts.URL, Pool: pool, ts: ts}, org, adminUser, memberUser
}
