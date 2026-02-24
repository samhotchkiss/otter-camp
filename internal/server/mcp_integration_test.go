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
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	secretsvc "github.com/samhotchkiss/otter-camp/internal/secret"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestMCPHTTPRoundTripCreateRefreshEnableTestDelete(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, adminUser, memberUser := newMCPTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	memberToken := loginToken(t, testServer.URL, memberUser.Email, "member-password")

	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/mcp/connections", map[string]any{
		"display_name": "My MCP Server",
		"slug":         "my-mcp-server",
		"project_id":   nil,
		"transport":    "http",
		"transport_config": map[string]any{
			"base_url": "https://mcp.example.test",
		},
		"is_enabled": true,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
	connectionID := jsonPathString(t, created.Body, "data", "id")
	if connectionID == "" {
		t.Fatalf("expected connection id in response: %s", string(created.Body))
	}
	if got := jsonPathString(t, created.Body, "data", "status"); got != "configuring" {
		t.Fatalf("create status field = %q, want %q", got, "configuring")
	}

	refreshed := mustJSON(t, http.MethodPost, testServer.URL+"/v1/mcp/connections/"+connectionID+"/refresh", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if refreshed.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d, want %d body=%s", refreshed.StatusCode, http.StatusOK, string(refreshed.Body))
	}

	catalog := mustJSON(t, http.MethodGet, testServer.URL+"/v1/mcp/connections/"+connectionID+"/catalog", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if catalog.StatusCode != http.StatusOK {
		t.Fatalf("catalog status = %d, want %d body=%s", catalog.StatusCode, http.StatusOK, string(catalog.Body))
	}
	entryID := jsonPathString(t, catalog.Body, "data", "0", "id")
	if entryID == "" {
		t.Fatalf("expected catalog entry id: %s", string(catalog.Body))
	}
	if got := jsonPathBoolValue(t, catalog.Body, "data", "0", "is_enabled"); got {
		t.Fatalf("catalog entry is_enabled = true, want false")
	}

	enabled := mustJSON(t, http.MethodPatch, testServer.URL+"/v1/mcp/connections/"+connectionID+"/catalog/"+entryID, map[string]any{
		"is_enabled": true,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if enabled.StatusCode != http.StatusOK {
		t.Fatalf("enable tool status = %d, want %d body=%s", enabled.StatusCode, http.StatusOK, string(enabled.Body))
	}

	catalogAfterEnable := mustJSON(t, http.MethodGet, testServer.URL+"/v1/mcp/connections/"+connectionID+"/catalog", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if catalogAfterEnable.StatusCode != http.StatusOK {
		t.Fatalf("catalog after enable status = %d, want %d body=%s", catalogAfterEnable.StatusCode, http.StatusOK, string(catalogAfterEnable.Body))
	}
	if got := jsonPathBoolValue(t, catalogAfterEnable.Body, "data", "0", "is_enabled"); !got {
		t.Fatalf("catalog entry is_enabled = false, want true")
	}

	tested := mustJSON(t, http.MethodPost, testServer.URL+"/v1/mcp/connections/"+connectionID+"/test", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + memberToken,
	})
	if tested.StatusCode != http.StatusOK {
		t.Fatalf("test status = %d, want %d body=%s", tested.StatusCode, http.StatusOK, string(tested.Body))
	}
	if got := jsonPathString(t, tested.Body, "data", "status"); got != "ok" {
		t.Fatalf("test payload status = %q, want %q", got, "ok")
	}
	if latency := jsonPathFloatValue(t, tested.Body, "data", "latency_ms"); latency <= 0 {
		t.Fatalf("latency_ms = %v, want > 0", latency)
	}

	executions := mustJSON(t, http.MethodGet, testServer.URL+"/v1/mcp/connections/"+connectionID+"/executions", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if executions.StatusCode != http.StatusOK {
		t.Fatalf("executions status = %d, want %d body=%s", executions.StatusCode, http.StatusOK, string(executions.Body))
	}
	if executions.Headers.Get("X-Stub") != "true" {
		t.Fatalf("X-Stub header = %q, want %q", executions.Headers.Get("X-Stub"), "true")
	}
	executionsData, ok := jsonPathValue(t, executions.Body, "data").([]any)
	if !ok {
		t.Fatalf("executions data shape = %T, want []any body=%s", jsonPathValue(t, executions.Body, "data"), string(executions.Body))
	}
	if len(executionsData) != 0 {
		t.Fatalf("executions data len = %d, want 0 body=%s", len(executionsData), string(executions.Body))
	}

	deleted := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/mcp/connections/"+connectionID, nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d body=%s", deleted.StatusCode, http.StatusNoContent, string(deleted.Body))
	}

	notFound := mustJSON(t, http.MethodGet, testServer.URL+"/v1/mcp/connections/"+connectionID, nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if notFound.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want %d body=%s", notFound.StatusCode, http.StatusNotFound, string(notFound.Body))
	}

	var catalogRows int
	if err := testServer.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM mcp_tool_catalog`).Scan(&catalogRows); err != nil {
		t.Fatalf("count catalog rows: %v", err)
	}
	if catalogRows != 0 {
		t.Fatalf("catalog row count after delete = %d, want 0", catalogRows)
	}
}

func TestMCPHTTPCreateConnectionHonorsIsEnabledAndSecretBindings(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, adminUser, _ := newMCPTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/mcp/connections", map[string]any{
		"display_name": "Disabled MCP",
		"slug":         "disabled-mcp",
		"transport":    "http",
		"transport_config": map[string]any{
			"base_url": "https://mcp.example.test",
		},
		"is_enabled": false,
		"secret_bindings": []map[string]any{
			{
				"secret_ref":   "ref:mcp-api-key",
				"env_var_name": "MCP_API_KEY",
			},
		},
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
	connectionID := jsonPathString(t, created.Body, "data", "id")
	if connectionID == "" {
		t.Fatalf("expected connection id in response body=%s", string(created.Body))
	}
	connectionUUID, err := uuid.Parse(connectionID)
	if err != nil {
		t.Fatalf("parse connection id: %v", err)
	}

	got := mustJSON(t, http.MethodGet, testServer.URL+"/v1/mcp/connections/"+connectionID, nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if got.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want %d body=%s", got.StatusCode, http.StatusOK, string(got.Body))
	}
	if enabled := jsonPathBoolValue(t, got.Body, "data", "is_enabled"); enabled {
		t.Fatalf("is_enabled = true, want false body=%s", string(got.Body))
	}

	var bindingCount int
	if err := testServer.Pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM mcp_secret_binding
		WHERE connection_id = $1
	`, connectionUUID).Scan(&bindingCount); err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	if bindingCount != 1 {
		t.Fatalf("binding count = %d, want 1", bindingCount)
	}
}

func TestMCPHTTPRBACAndUnauth(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, _, memberUser := newMCPTestServer(t)
	defer testServer.Close()

	memberToken := loginToken(t, testServer.URL, memberUser.Email, "member-password")

	forbidden := mustJSON(t, http.MethodPost, testServer.URL+"/v1/mcp/connections", map[string]any{
		"display_name": "Forbidden",
		"slug":         "forbidden",
		"transport":    "http",
		"transport_config": map[string]any{
			"base_url": "https://mcp.example.test",
		},
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if forbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("member create status = %d, want %d body=%s", forbidden.StatusCode, http.StatusForbidden, string(forbidden.Body))
	}

	unauth := mustJSON(t, http.MethodGet, testServer.URL+"/v1/mcp/connections", nil, nil)
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth list status = %d, want %d body=%s", unauth.StatusCode, http.StatusUnauthorized, string(unauth.Body))
	}
}

func newMCPTestServer(t *testing.T) (*authIntegrationServer, repo.HumanUser, repo.HumanUser) {
	t.Helper()

	pool := testdb.New(t)
	org, adminUser := createOrgAndUser(t, pool, "mcp-http-org", "mcp-admin@example.com", "MCP Admin", "admin", "admin-password")
	_, memberUser := createOrgAndUser(t, pool, "mcp-http-org", "mcp-member@example.com", "MCP Member", "member", "member-password")

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

	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	secretService := secretsvc.NewService(repo.NewSecretRepo(pool))
	mcpService, err := mcp.NewService(mcp.ServiceOptions{
		Connections:      repo.NewMCPConnectionRepo(pool),
		Catalog:          repo.NewMCPToolCatalogRepo(pool),
		Bindings:         repo.NewMCPSecretBindingRepo(pool),
		Resolver:         secretService,
		EventBus:         bus,
		TransportFactory: mcp.StaticTransportFactory{Transport: &mcp.MockTransport{Tools: []mcp.ToolManifest{{ToolName: "tool.echo", Description: "Echo", InputSchema: []byte(`{"type":"object"}`)}}}},
	})
	if err != nil {
		t.Fatalf("new mcp service: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []RouteRegistrar{
			NewMCPRouteRegistrar(mcpService, repo.NewMCPToolCatalogRepo(pool)),
		},
	})

	ts := httptest.NewServer(handler)
	return &authIntegrationServer{URL: ts.URL, Pool: pool, ts: ts}, adminUser, memberUser
}

func jsonPathBoolValue(t *testing.T, body []byte, path ...string) bool {
	t.Helper()
	value := jsonPathValue(t, body, path...)
	typed, ok := value.(bool)
	if !ok {
		t.Fatalf("jsonPath %v = %#v (%T), want bool", path, value, value)
	}
	return typed
}

func jsonPathFloatValue(t *testing.T, body []byte, path ...string) float64 {
	t.Helper()
	value := jsonPathValue(t, body, path...)
	typed, ok := value.(float64)
	if !ok {
		t.Fatalf("jsonPath %v = %#v (%T), want float64", path, value, value)
	}
	return typed
}
