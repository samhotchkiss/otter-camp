//go:build integration

package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	secretsvc "github.com/samhotchkiss/otter-camp/internal/secret"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/testutil"
)

func TestConnection_Lifecycle_ConfiguringToActive(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	server := testutil.MockMCPServer(t, testutil.MockMCPServerOptions{
		Tools: []testutil.MCPTool{
			{Name: "tool.alpha", Description: "alpha", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "tool.beta", Description: "beta", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	})

	service := mustNewMCPService(t, pool, ServiceOptions{
		Resolver:         &integrationResolver{},
		EventBus:         eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{}),
		TransportFactory: NewDefaultTransportFactory(&http.Client{Timeout: time.Second}),
	})

	connID := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		DisplayName: "Lifecycle Connection",
		Slug:        "lifecycle-connection",
		Status:      "configuring",
		Transport:   "http",
		TransportConfig: json.RawMessage(`{
			"base_url":"` + server.URL + `"
		}`),
	})

	if err := service.RefreshCatalog(ctx, connID); err != nil {
		t.Fatalf("RefreshCatalog: %v", err)
	}

	var (
		status      string
		totalTools  int
		enabled     int
		eventCount  int
		changedType = "mcp.catalog.changed"
	)
	if err := pool.QueryRow(ctx, `SELECT status FROM mcp_connection WHERE id = $1`, connID).Scan(&status); err != nil {
		t.Fatalf("load status: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM mcp_tool_catalog WHERE connection_id = $1`, connID).Scan(&totalTools); err != nil {
		t.Fatalf("count tool rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM mcp_tool_catalog WHERE connection_id = $1 AND is_enabled = true`, connID).Scan(&enabled); err != nil {
		t.Fatalf("count enabled rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM domain_event WHERE organization_id = $1 AND event_type = $2`, orgID, changedType).Scan(&eventCount); err != nil {
		t.Fatalf("count domain events: %v", err)
	}

	if status != "active" {
		t.Fatalf("status = %q, want active", status)
	}
	if totalTools != 2 {
		t.Fatalf("tool count = %d, want 2", totalTools)
	}
	if enabled != 0 {
		t.Fatalf("enabled tools = %d, want 0", enabled)
	}
	if eventCount != 1 {
		t.Fatalf("domain event count = %d, want 1", eventCount)
	}
}

func TestConnection_CatalogRefresh_DefaultDeny(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	server := testutil.MockMCPServer(t, testutil.MockMCPServerOptions{
		Tools: []testutil.MCPTool{
			{Name: "tool.alpha", Description: "alpha"},
			{Name: "tool.beta", Description: "beta"},
			{Name: "tool.gamma", Description: "gamma"},
		},
	})

	service := mustNewMCPService(t, pool, ServiceOptions{
		Resolver:         &integrationResolver{},
		TransportFactory: NewDefaultTransportFactory(&http.Client{Timeout: time.Second}),
	})
	catalogRepo := repo.NewMCPToolCatalogRepo(pool)

	connID := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		Status:    "configuring",
		Transport: "http",
		TransportConfig: json.RawMessage(`{
			"base_url":"` + server.URL + `"
		}`),
	})
	if err := service.RefreshCatalog(ctx, connID); err != nil {
		t.Fatalf("RefreshCatalog: %v", err)
	}

	entries, err := catalogRepo.ListByConnection(ctx, connID)
	if err != nil {
		t.Fatalf("ListByConnection: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("catalog entry count = %d, want 3", len(entries))
	}
	for _, entry := range entries {
		if entry.IsEnabled {
			t.Fatalf("entry %q is_enabled = true, want false", entry.ToolName)
		}
	}

	if err := service.EnableTool(ctx, connID, entries[0].ID); err != nil {
		t.Fatalf("EnableTool: %v", err)
	}

	entries, err = catalogRepo.ListByConnection(ctx, connID)
	if err != nil {
		t.Fatalf("ListByConnection after enable: %v", err)
	}
	for idx, entry := range entries {
		wantEnabled := idx == 0
		if entry.IsEnabled != wantEnabled {
			t.Fatalf("entry %q is_enabled = %v, want %v", entry.ToolName, entry.IsEnabled, wantEnabled)
		}
	}
}

func TestConnection_CatalogRefresh_NewTool(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	server := testutil.ScriptedMCPServer(t, []testutil.ScriptedMCPHandler{
		mcpToolsListHandler([]string{"tool.alpha", "tool.beta", "tool.gamma"}),
		mcpToolsListHandler([]string{"tool.alpha", "tool.beta", "tool.gamma", "tool.delta"}),
	})

	service := mustNewMCPService(t, pool, ServiceOptions{
		Resolver:         &integrationResolver{},
		EventBus:         eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{}),
		TransportFactory: NewDefaultTransportFactory(&http.Client{Timeout: time.Second}),
	})
	catalogRepo := repo.NewMCPToolCatalogRepo(pool)

	connID := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		Status:    "configuring",
		Transport: "http",
		TransportConfig: json.RawMessage(`{
			"base_url":"` + server.URL + `"
		}`),
	})
	if err := service.RefreshCatalog(ctx, connID); err != nil {
		t.Fatalf("first RefreshCatalog: %v", err)
	}
	before, err := catalogRepo.ListByConnection(ctx, connID)
	if err != nil {
		t.Fatalf("ListByConnection before: %v", err)
	}
	if len(before) != 3 {
		t.Fatalf("catalog size before = %d, want 3", len(before))
	}

	if err := service.EnableTool(ctx, connID, before[0].ID); err != nil {
		t.Fatalf("EnableTool existing: %v", err)
	}
	if err := service.RefreshCatalog(ctx, connID); err != nil {
		t.Fatalf("second RefreshCatalog: %v", err)
	}

	after, err := catalogRepo.ListByConnection(ctx, connID)
	if err != nil {
		t.Fatalf("ListByConnection after: %v", err)
	}
	if len(after) != 4 {
		t.Fatalf("catalog size after = %d, want 4", len(after))
	}

	foundNew := false
	for _, entry := range after {
		if entry.ToolName == "tool.delta" {
			foundNew = true
			if entry.IsEnabled {
				t.Fatalf("new tool %q is_enabled = true, want false", entry.ToolName)
			}
		}
		if entry.ToolName == before[0].ToolName && !entry.IsEnabled {
			t.Fatalf("existing enabled tool %q became disabled", entry.ToolName)
		}
	}
	if !foundNew {
		t.Fatal("expected tool.delta after refresh")
	}

	var events int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM domain_event WHERE organization_id = $1 AND event_type = 'mcp.catalog.changed'`, orgID).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 2 {
		t.Fatalf("domain event count = %d, want 2", events)
	}
}

func TestConnection_CatalogRefresh_RemovedTool(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	server := testutil.ScriptedMCPServer(t, []testutil.ScriptedMCPHandler{
		mcpToolsListHandler([]string{"tool.alpha", "tool.beta", "tool.gamma"}),
		mcpToolsListHandler([]string{"tool.alpha", "tool.gamma"}),
	})

	service := mustNewMCPService(t, pool, ServiceOptions{
		Resolver:         &integrationResolver{},
		EventBus:         eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{}),
		TransportFactory: NewDefaultTransportFactory(&http.Client{Timeout: time.Second}),
	})
	catalogRepo := repo.NewMCPToolCatalogRepo(pool)

	connID := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		Status:    "configuring",
		Transport: "http",
		TransportConfig: json.RawMessage(`{
			"base_url":"` + server.URL + `"
		}`),
	})
	if err := service.RefreshCatalog(ctx, connID); err != nil {
		t.Fatalf("first RefreshCatalog: %v", err)
	}

	entries, err := catalogRepo.ListByConnection(ctx, connID)
	if err != nil {
		t.Fatalf("ListByConnection before remove: %v", err)
	}
	var removedID uuid.UUID
	var removedBefore time.Time
	for _, entry := range entries {
		if entry.ToolName == "tool.beta" {
			removedID = entry.ID
			removedBefore = entry.UpdatedAt
			if err := service.EnableTool(ctx, connID, entry.ID); err != nil {
				t.Fatalf("EnableTool removed candidate: %v", err)
			}
		}
	}
	if removedID == uuid.Nil {
		t.Fatal("missing tool.beta row before refresh")
	}

	if err := service.RefreshCatalog(ctx, connID); err != nil {
		t.Fatalf("second RefreshCatalog: %v", err)
	}

	removedRow, err := catalogRepo.GetByID(ctx, removedID)
	if err != nil {
		t.Fatalf("GetByID removed row: %v", err)
	}
	if removedRow.IsEnabled {
		t.Fatalf("removed tool row is_enabled = true, want false")
	}
	if !removedRow.UpdatedAt.After(removedBefore) {
		t.Fatalf("removed row updated_at = %s, want > %s", removedRow.UpdatedAt, removedBefore)
	}

	var events int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM domain_event WHERE organization_id = $1 AND event_type = 'mcp.catalog.changed'`, orgID).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 2 {
		t.Fatalf("domain event count = %d, want 2", events)
	}
}

func TestConnection_SecretBinding_Resolution(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)

	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(makeMCPTestKey()))
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	secretService := secretsvc.NewService(repo.NewSecretRepo(pool))
	secretValue := "sk-abc123def456ghi789jklmnopqrst"
	if err := secretService.Set(ctx, orgID, "db-password", "DB Password", "", secretValue, secretsvc.Principal{Type: "system", ID: uuid.Nil}); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	captureFactory := &capturingTransportFactory{
		transport: &MockTransport{Tools: []ToolManifest{{ToolName: "tool.echo", Description: "echo"}}},
	}
	service := mustNewMCPService(t, pool, ServiceOptions{
		Resolver:         secretService,
		EventBus:         eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{}),
		TransportFactory: captureFactory,
	})

	created, err := service.CreateConnection(ctx, CreateConnectionRequest{
		OrganizationID: orgID,
		DisplayName:    "Secret Ref Connection",
		Slug:           "secret-ref-connection",
		Transport:      "http",
		TransportConfig: json.RawMessage(`{
			"base_url":"https://mcp.example.test",
			"api_key":"ref:db-password"
		}`),
		SecretBindings: []SecretBindingInput{{SecretRef: "ref:db-password", EnvVarName: "DB_PASSWORD"}},
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	if err := service.RefreshCatalog(ctx, created.ID); err != nil {
		t.Fatalf("RefreshCatalog: %v", err)
	}

	captureFactory.mu.Lock()
	config := captureFactory.lastConfig
	env := captureFactory.lastEnv
	captureFactory.mu.Unlock()
	if got, _ := config["api_key"].(string); got != secretValue {
		t.Fatalf("resolved transport_config api_key = %q, want %q", got, secretValue)
	}
	if got := env["DB_PASSWORD"]; got != secretValue {
		t.Fatalf("resolved secret binding env DB_PASSWORD = %q, want %q", got, secretValue)
	}

	var storedConfig string
	if err := pool.QueryRow(ctx, `SELECT transport_config::text FROM mcp_connection WHERE id = $1`, created.ID).Scan(&storedConfig); err != nil {
		t.Fatalf("load stored transport config: %v", err)
	}
	if containsSecret(storedConfig, secretValue) {
		t.Fatalf("stored transport_config leaked secret value")
	}

	var payloadText string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(payload::text, '')
		FROM domain_event
		WHERE organization_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, orgID).Scan(&payloadText); err != nil {
		t.Fatalf("load latest domain event payload: %v", err)
	}
	if containsSecret(payloadText, secretValue) {
		t.Fatalf("domain_event payload leaked secret value")
	}
}

func TestConnection_SecretBinding_MissingSecret(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)

	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(makeMCPTestKey()))
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	service := mustNewMCPService(t, pool, ServiceOptions{
		Resolver:         secretsvc.NewService(repo.NewSecretRepo(pool)),
		TransportFactory: StaticTransportFactory{Transport: &MockTransport{}},
	})

	connID := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		Status:    "configuring",
		Transport: "http",
		TransportConfig: json.RawMessage(`{
			"base_url":"https://mcp.example.test",
			"api_key":"ref:missing-secret"
		}`),
	})
	if _, err := repo.NewMCPSecretBindingRepo(pool).Create(ctx, repo.MCPSecretBinding{
		ConnectionID: connID,
		SecretRef:    "ref:missing-secret",
		EnvVarName:   "DB_PASSWORD",
	}); err != nil {
		t.Fatalf("Create binding: %v", err)
	}

	err := service.RefreshCatalog(ctx, connID)
	if err == nil {
		t.Fatal("RefreshCatalog error = nil, want missing secret error")
	}
	if !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("RefreshCatalog error = %v, want ErrSecretNotFound", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM mcp_connection WHERE id = $1`, connID).Scan(&status); err != nil {
		t.Fatalf("load status: %v", err)
	}
	if status != "failed" {
		t.Fatalf("status = %q, want failed", status)
	}
}

func TestConnection_PeriodicHealthCheck(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	server := testutil.MockMCPServer(t, testutil.MockMCPServerOptions{})
	clock := newTestClock(time.Now().UTC())

	service := mustNewMCPService(t, pool, ServiceOptions{
		Resolver:         &integrationResolver{},
		TransportFactory: NewDefaultTransportFactory(&http.Client{Timeout: time.Second}),
		Now:              clock.Now,
	})

	connID := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		Status:    "active",
		Transport: "http",
		TransportConfig: json.RawMessage(`{
			"base_url":"` + server.URL + `",
			"health_active_interval_ms": 1000,
			"health_check_timeout_ms": 200
		}`),
	})

	if err := service.RunHealthChecks(ctx); err != nil {
		t.Fatalf("RunHealthChecks first: %v", err)
	}
	var firstHealthy time.Time
	if err := pool.QueryRow(ctx, `SELECT COALESCE(last_healthy_at, '-infinity'::timestamptz) FROM mcp_connection WHERE id = $1`, connID).Scan(&firstHealthy); err != nil {
		t.Fatalf("load first last_healthy_at: %v", err)
	}

	clock.Advance(1500 * time.Millisecond)
	if err := service.RunHealthChecks(ctx); err != nil {
		t.Fatalf("RunHealthChecks second: %v", err)
	}
	var secondHealthy time.Time
	if err := pool.QueryRow(ctx, `SELECT COALESCE(last_healthy_at, '-infinity'::timestamptz) FROM mcp_connection WHERE id = $1`, connID).Scan(&secondHealthy); err != nil {
		t.Fatalf("load second last_healthy_at: %v", err)
	}
	if !secondHealthy.After(firstHealthy) {
		t.Fatalf("last_healthy_at did not advance: first=%s second=%s", firstHealthy, secondHealthy)
	}
}

func TestConnection_ProjectScope_vs_OrgScope(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	projectA := testutil.MakeProject(t, pool, orgID)
	projectB := testutil.MakeProject(t, pool, orgID)

	service := mustNewMCPService(t, pool, ServiceOptions{
		Resolver:         &integrationResolver{},
		TransportFactory: StaticTransportFactory{Transport: &MockTransport{}},
	})

	orgScoped := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		DisplayName: "Org Scoped",
		Slug:        "org-scoped",
		Status:      "active",
	})
	projectAScoped := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		ProjectID:   &projectA.ID,
		DisplayName: "Project A Scoped",
		Slug:        "project-a-scoped",
		Status:      "active",
	})
	projectBScoped := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		ProjectID:   &projectB.ID,
		DisplayName: "Project B Scoped",
		Slug:        "project-b-scoped",
		Status:      "active",
	})

	results, err := service.ListConnections(ctx, orgID, MCPConnectionFilter{ProjectID: &projectA.ID})
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("filtered connection count = %d, want 2", len(results))
	}

	seen := map[uuid.UUID]bool{}
	for _, item := range results {
		seen[item.ID] = true
	}
	if !seen[orgScoped] {
		t.Fatalf("expected org-scoped connection in project filter results")
	}
	if !seen[projectAScoped] {
		t.Fatalf("expected matching project-scoped connection in results")
	}
	if seen[projectBScoped] {
		t.Fatalf("unexpected other-project connection in results")
	}
}

func mustNewMCPService(t *testing.T, pool *pgxpool.Pool, opts ServiceOptions) MCPService {
	t.Helper()

	if opts.Connections == nil {
		opts.Connections = repo.NewMCPConnectionRepo(pool)
	}
	if opts.Catalog == nil {
		opts.Catalog = repo.NewMCPToolCatalogRepo(pool)
	}
	if opts.Bindings == nil {
		opts.Bindings = repo.NewMCPSecretBindingRepo(pool)
	}

	service, err := NewService(opts)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}

func mcpToolsListHandler(names []string) testutil.ScriptedMCPHandler {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/tools/list" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		tools := make([]map[string]any, 0, len(names))
		for _, name := range names {
			tools = append(tools, map[string]any{
				"name":         name,
				"description":  name + " description",
				"input_schema": map[string]any{"type": "object"},
				"metadata":     map[string]any{},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tools": tools})
	}
}

type capturingTransportFactory struct {
	mu sync.Mutex

	transport  MCPTransport
	lastConfig map[string]any
	lastEnv    map[string]string
}

func (f *capturingTransportFactory) New(_ context.Context, _ repo.MCPConnection, resolvedConfig map[string]any, env map[string]string) (MCPTransport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.lastConfig = copyAnyMap(resolvedConfig)
	f.lastEnv = copyStringMap(env)
	return f.transport, nil
}

func copyAnyMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}

func copyStringMap(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}

func containsSecret(value, secret string) bool {
	return secret != "" && value != "" && strings.Contains(value, secret)
}

func makeMCPTestKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}
