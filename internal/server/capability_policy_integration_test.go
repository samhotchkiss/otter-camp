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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/audit"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/policy"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestCapabilityPolicyHTTPEvaluatePrecedenceAndTrace(t *testing.T) {
	server := newCapabilityPolicyTestServer(t)
	defer server.Close()

	adminToken := loginToken(t, server.URL, server.AdminA.Email, "admin-password")

	agent := seedPolicyAgent(t, server.Pool, server.OrgA.ID, "policy-agent-a")
	project := seedPolicyProject(t, server.Pool, server.OrgA.ID, "policy-project-a")

	orgDeny := mustJSON(t, http.MethodPost, server.URL+"/v1/control/policies", map[string]any{
		"policy_layer": "org",
		"capability":   "system.file.write",
		"effect":       "deny",
		"priority":     100,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if orgDeny.StatusCode != http.StatusCreated {
		t.Fatalf("create org deny status = %d, want %d body=%s", orgDeny.StatusCode, http.StatusCreated, string(orgDeny.Body))
	}

	agentAllow := mustJSON(t, http.MethodPost, server.URL+"/v1/control/policies", map[string]any{
		"policy_layer": "agent_profile",
		"agent_id":     agent.ID.String(),
		"capability":   "system.file.write",
		"effect":       "allow",
		"priority":     100,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if agentAllow.StatusCode != http.StatusCreated {
		t.Fatalf("create agent allow status = %d, want %d body=%s", agentAllow.StatusCode, http.StatusCreated, string(agentAllow.Body))
	}

	layerOrdering := mustJSON(t, http.MethodPost, server.URL+"/v1/control/policies/evaluate", map[string]any{
		"organization_id": server.OrgA.ID.String(),
		"agent_id":        agent.ID.String(),
		"capability":      "system.file.write",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if layerOrdering.StatusCode != http.StatusOK {
		t.Fatalf("evaluate layer ordering status = %d, want %d body=%s", layerOrdering.StatusCode, http.StatusOK, string(layerOrdering.Body))
	}
	if got := jsonPathString(t, layerOrdering.Body, "data", "effect"); got != "deny" {
		t.Fatalf("evaluate effect = %q, want %q body=%s", got, "deny", string(layerOrdering.Body))
	}
	if got := jsonPathString(t, layerOrdering.Body, "data", "layer"); got != "org" {
		t.Fatalf("evaluate layer = %q, want %q body=%s", got, "org", string(layerOrdering.Body))
	}
	assertTraceHasAllLayers(t, layerOrdering.Body)

	instancePolicy, err := server.PolicyRepo.Create(context.Background(), repo.CapabilityPolicy{
		PolicyLayer:   "instance",
		Capability:    "browser.navigate",
		Effect:        "deny",
		Priority:      100,
		CreatedByType: "system",
	})
	if err != nil {
		t.Fatalf("create instance policy: %v", err)
	}
	if err := server.PolicyEvaluator.LoadInstancePolicies(context.Background()); err != nil {
		t.Fatalf("reload instance policies: %v", err)
	}

	projectAllow := mustJSON(t, http.MethodPost, server.URL+"/v1/control/policies", map[string]any{
		"policy_layer": "project",
		"project_id":   project.ID.String(),
		"capability":   "browser.navigate",
		"effect":       "allow",
		"priority":     100,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if projectAllow.StatusCode != http.StatusCreated {
		t.Fatalf("create project allow status = %d, want %d body=%s", projectAllow.StatusCode, http.StatusCreated, string(projectAllow.Body))
	}

	agentAllowBrowser := mustJSON(t, http.MethodPost, server.URL+"/v1/control/policies", map[string]any{
		"policy_layer": "agent_profile",
		"agent_id":     agent.ID.String(),
		"capability":   "browser.navigate",
		"effect":       "allow",
		"priority":     100,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if agentAllowBrowser.StatusCode != http.StatusCreated {
		t.Fatalf("create agent allow browser status = %d, want %d body=%s", agentAllowBrowser.StatusCode, http.StatusCreated, string(agentAllowBrowser.Body))
	}

	absoluteDeny := mustJSON(t, http.MethodPost, server.URL+"/v1/control/policies/evaluate", map[string]any{
		"organization_id": server.OrgA.ID.String(),
		"project_id":      project.ID.String(),
		"agent_id":        agent.ID.String(),
		"capability":      "browser.navigate",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if absoluteDeny.StatusCode != http.StatusOK {
		t.Fatalf("evaluate absolute deny status = %d, want %d body=%s", absoluteDeny.StatusCode, http.StatusOK, string(absoluteDeny.Body))
	}
	if got := jsonPathString(t, absoluteDeny.Body, "data", "effect"); got != "deny" {
		t.Fatalf("absolute deny effect = %q, want %q body=%s", got, "deny", string(absoluteDeny.Body))
	}
	if got := jsonPathString(t, absoluteDeny.Body, "data", "layer"); got != "instance" {
		t.Fatalf("absolute deny layer = %q, want %q body=%s", got, "instance", string(absoluteDeny.Body))
	}
	assertTraceHasAllLayers(t, absoluteDeny.Body)

	silencePasses := mustJSON(t, http.MethodPost, server.URL+"/v1/control/policies/evaluate", map[string]any{
		"organization_id": server.OrgA.ID.String(),
		"capability":      "unknown.capability",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if silencePasses.StatusCode != http.StatusOK {
		t.Fatalf("silence passes status = %d, want %d body=%s", silencePasses.StatusCode, http.StatusOK, string(silencePasses.Body))
	}
	if got := jsonPathString(t, silencePasses.Body, "data", "effect"); got != "allow" {
		t.Fatalf("silence effect = %q, want %q body=%s", got, "allow", string(silencePasses.Body))
	}
	if got := jsonPathString(t, silencePasses.Body, "data", "layer"); got != "none" {
		t.Fatalf("silence layer = %q, want %q body=%s", got, "none", string(silencePasses.Body))
	}

	_ = instancePolicy
}

func TestCapabilityPolicyHTTPCRUDInstanceProtectionAndOrgIsolation(t *testing.T) {
	server := newCapabilityPolicyTestServer(t)
	defer server.Close()

	adminAToken := loginToken(t, server.URL, server.AdminA.Email, "admin-password")

	create := mustJSON(t, http.MethodPost, server.URL+"/v1/control/policies", map[string]any{
		"policy_layer": "org",
		"capability":   "system.file.write",
		"effect":       "allow",
		"priority":     100,
	}, map[string]string{"Authorization": "Bearer " + adminAToken})
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", create.StatusCode, http.StatusCreated, string(create.Body))
	}
	policyID := jsonPathString(t, create.Body, "data", "id")
	if policyID == "" {
		t.Fatalf("missing policy id body=%s", string(create.Body))
	}

	getCreated := mustJSON(t, http.MethodGet, server.URL+"/v1/control/policies?capability=system.file.write", nil, map[string]string{
		"Authorization": "Bearer " + adminAToken,
	})
	if getCreated.StatusCode != http.StatusOK {
		t.Fatalf("get after create status = %d, want %d body=%s", getCreated.StatusCode, http.StatusOK, string(getCreated.Body))
	}
	items, ok := jsonPathValue(t, getCreated.Body, "data").([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("get after create len=%d want=1 body=%s", len(items), string(getCreated.Body))
	}

	update := mustJSON(t, http.MethodPut, server.URL+"/v1/control/policies/"+policyID, map[string]any{
		"policy_layer": "org",
		"capability":   "system.file.write",
		"effect":       "deny",
		"priority":     100,
	}, map[string]string{"Authorization": "Bearer " + adminAToken})
	if update.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, want %d body=%s", update.StatusCode, http.StatusOK, string(update.Body))
	}
	if got := jsonPathString(t, update.Body, "data", "effect"); got != "deny" {
		t.Fatalf("updated effect = %q, want %q body=%s", got, "deny", string(update.Body))
	}

	instancePolicy, err := server.PolicyRepo.Create(context.Background(), repo.CapabilityPolicy{
		PolicyLayer:   "instance",
		Capability:    "system.browser.interact",
		Effect:        "deny",
		Priority:      100,
		CreatedByType: "system",
	})
	if err != nil {
		t.Fatalf("create instance policy: %v", err)
	}
	if err := server.PolicyEvaluator.LoadInstancePolicies(context.Background()); err != nil {
		t.Fatalf("reload instance policies: %v", err)
	}

	getInstance := mustJSON(t, http.MethodGet, server.URL+"/v1/control/policies?policy_layer=instance", nil, map[string]string{
		"Authorization": "Bearer " + adminAToken,
	})
	if getInstance.StatusCode != http.StatusOK {
		t.Fatalf("get instance status = %d, want %d body=%s", getInstance.StatusCode, http.StatusOK, string(getInstance.Body))
	}
	instanceItems, ok := jsonPathValue(t, getInstance.Body, "data").([]any)
	if !ok || len(instanceItems) == 0 {
		t.Fatalf("instance list empty body=%s", string(getInstance.Body))
	}

	deleteInstance := mustJSON(t, http.MethodDelete, server.URL+"/v1/control/policies/"+instancePolicy.ID.String(), nil, map[string]string{
		"Authorization": "Bearer " + adminAToken,
	})
	if deleteInstance.StatusCode != http.StatusForbidden {
		t.Fatalf("delete instance status = %d, want %d body=%s", deleteInstance.StatusCode, http.StatusForbidden, string(deleteInstance.Body))
	}

	deletePolicy := mustJSON(t, http.MethodDelete, server.URL+"/v1/control/policies/"+policyID, nil, map[string]string{
		"Authorization": "Bearer " + adminAToken,
	})
	if deletePolicy.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d body=%s", deletePolicy.StatusCode, http.StatusNoContent, string(deletePolicy.Body))
	}

	getAfterDelete := mustJSON(t, http.MethodGet, server.URL+"/v1/control/policies?capability=system.file.write", nil, map[string]string{
		"Authorization": "Bearer " + adminAToken,
	})
	if getAfterDelete.StatusCode != http.StatusOK {
		t.Fatalf("get after delete status = %d, want %d body=%s", getAfterDelete.StatusCode, http.StatusOK, string(getAfterDelete.Body))
	}
	itemsAfterDelete, ok := jsonPathValue(t, getAfterDelete.Body, "data").([]any)
	if !ok || len(itemsAfterDelete) != 0 {
		t.Fatalf("get after delete len=%d want=0 body=%s", len(itemsAfterDelete), string(getAfterDelete.Body))
	}

	putDeleted := mustJSON(t, http.MethodPut, server.URL+"/v1/control/policies/"+policyID, map[string]any{
		"policy_layer": "org",
		"capability":   "system.file.write",
		"effect":       "allow",
		"priority":     100,
	}, map[string]string{"Authorization": "Bearer " + adminAToken})
	if putDeleted.StatusCode != http.StatusNotFound {
		t.Fatalf("put deleted status = %d, want %d body=%s", putDeleted.StatusCode, http.StatusNotFound, string(putDeleted.Body))
	}

	waitForPolicyAuditEvent(t, server.Pool, server.OrgA.ID, "policy.created")
	waitForPolicyAuditEvent(t, server.Pool, server.OrgA.ID, "policy.updated")
	waitForPolicyAuditEvent(t, server.Pool, server.OrgA.ID, "policy.deleted")
	waitForPolicyAuditEvent(t, server.Pool, server.OrgA.ID, "policy.instance_write_attempt")

	orgBSession := seedSessionToken(t, server.Pool, server.AdminB.ID)
	orgBView := mustJSON(t, http.MethodGet, server.URL+"/v1/control/policies?capability=system.file.write", nil, map[string]string{
		"Authorization": "Bearer " + orgBSession,
	})
	if orgBView.StatusCode != http.StatusOK {
		t.Fatalf("org B list status = %d, want %d body=%s", orgBView.StatusCode, http.StatusOK, string(orgBView.Body))
	}
	orgBItems, ok := jsonPathValue(t, orgBView.Body, "data").([]any)
	if !ok {
		t.Fatalf("org B data shape invalid body=%s", string(orgBView.Body))
	}
	if len(orgBItems) != 0 {
		t.Fatalf("org B list len = %d, want 0 body=%s", len(orgBItems), string(orgBView.Body))
	}
}

type capabilityPolicyIntegrationServer struct {
	*authIntegrationServer
	PolicyRepo      *repo.CapabilityPolicyRepo
	PolicyEvaluator *policy.PolicyEvaluator
	OrgA            repo.Organization
	AdminA          repo.HumanUser
	OrgB            repo.Organization
	AdminB          repo.HumanUser
}

func newCapabilityPolicyTestServer(t *testing.T) *capabilityPolicyIntegrationServer {
	t.Helper()

	pool := testdb.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	slugA := "policy-http-a-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	adminEmailA := "admin+" + slugA + "@example.com"
	orgA, adminA := createOrgAndUser(t, pool, slugA, adminEmailA, "Admin A", "admin", "admin-password")

	slugB := "policy-http-b-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	adminEmailB := "admin+" + slugB + "@example.com"
	orgB, adminB := createOrgAndUser(t, pool, slugB, adminEmailB, "Admin B", "admin", "admin-password")

	authService, err := authsvc.NewService(authsvc.Options{
		Users:        repo.NewHumanUserRepo(pool),
		Sessions:     repo.NewAuthSessionRepo(pool),
		APIKeys:      repo.NewAPIKeyRepo(pool),
		DefaultOrgID: orgA.ID,
		AuthMode:     "standard",
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	policyRepo := repo.NewCapabilityPolicyRepo(pool)
	evaluator, err := policy.NewPolicyEvaluator(policy.EvaluatorOptions{
		Policies: policyRepo,
	})
	if err != nil {
		t.Fatalf("new policy evaluator: %v", err)
	}
	if err := evaluator.LoadInstancePolicies(context.Background()); err != nil {
		t.Fatalf("load instance policies: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      logger,
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []RouteRegistrar{
			NewCapabilityPolicyRouteRegistrar(CapabilityPolicyRouteOptions{
				Policies:      policyRepo,
				Projects:      repo.NewProjectRepo(pool),
				Agents:        repo.NewAgentRepo(pool),
				Evaluator:     evaluator,
				AuditRecorder: audit.NewService(repo.NewAuditEventRepo(pool), logger),
			}),
		},
	})

	ts := httptest.NewServer(handler)
	return &capabilityPolicyIntegrationServer{
		authIntegrationServer: &authIntegrationServer{URL: ts.URL, Pool: pool, ts: ts},
		PolicyRepo:            policyRepo,
		PolicyEvaluator:       evaluator,
		OrgA:                  orgA,
		AdminA:                adminA,
		OrgB:                  orgB,
		AdminB:                adminB,
	}
}

func seedPolicyProject(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, slugPrefix string) repo.Project {
	t.Helper()
	project, err := repo.NewProjectRepo(pool).Create(context.Background(), repo.Project{
		OrganizationID: orgID,
		Slug:           slugPrefix + "-" + uuid.NewString()[:8],
		DisplayName:    "Policy Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project
}

func seedPolicyAgent(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, name string) repo.Agent {
	t.Helper()
	agent, err := repo.NewAgentRepo(pool).Create(context.Background(), repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          name,
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent
}

func seedSessionToken(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) string {
	t.Helper()
	raw := "session-" + uuid.NewString()
	_, err := repo.NewAuthSessionRepo(pool).Create(context.Background(), repo.AuthSession{
		UserID:    userID,
		TokenHash: sha256Hex(raw),
		ExpiresAt: time.Now().Add(6 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create auth session: %v", err)
	}
	return raw
}

func waitForPolicyAuditEvent(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, eventType string) {
	t.Helper()
	auditRepo := repo.NewAuditEventRepo(pool)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		filter := eventType
		found, err := auditRepo.ListByOrg(context.Background(), orgID, repo.AuditEventFilters{
			EventType: &filter,
		}, repo.Pagination{Limit: 10})
		if err != nil {
			t.Fatalf("list audit events (%s): %v", eventType, err)
		}
		if len(found) > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("expected audit event %q not found", eventType)
}

func assertTraceHasAllLayers(t *testing.T, body []byte) {
	t.Helper()
	trace, ok := jsonPathValue(t, body, "data", "trace").([]any)
	if !ok {
		t.Fatalf("trace shape invalid body=%s", string(body))
	}
	if len(trace) != 5 {
		t.Fatalf("trace len = %d, want 5 body=%s", len(trace), string(body))
	}
	layers := map[string]bool{}
	for _, item := range trace {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("trace item shape invalid body=%s", string(body))
		}
		layer, _ := entry["layer"].(string)
		layers[layer] = true
	}
	for _, required := range []string{"instance", "org", "project", "agent_profile", "request"} {
		if !layers[required] {
			t.Fatalf("missing trace layer %q body=%s", required, string(body))
		}
	}
}
