//go:build integration

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestRefreshCatalogIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgID := seedMCPOrgAndProject(t, ctx, pool)
	connRepo := repo.NewMCPConnectionRepo(pool)
	catalogRepo := repo.NewMCPToolCatalogRepo(pool)
	bindingRepo := repo.NewMCPSecretBindingRepo(pool)
	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})

	service, err := NewService(ServiceOptions{
		Connections: connRepo,
		Catalog:     catalogRepo,
		Bindings:    bindingRepo,
		Resolver: &integrationResolver{values: map[string]string{
			"ref:catalog-token": "token-value",
		}},
		TransportFactory: StaticTransportFactory{Transport: &MockTransport{Tools: []ToolManifest{
			{ToolName: "tool.alpha", Description: "alpha", InputSchema: []byte(`{"type":"object"}`)},
			{ToolName: "tool.beta", Description: "beta", InputSchema: []byte(`{"type":"object"}`)},
		}}},
		EventBus: bus,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	created, err := service.CreateConnection(ctx, CreateConnectionRequest{
		OrganizationID:  orgID,
		DisplayName:     "Catalog Connection",
		Slug:            "catalog-connection",
		Transport:       "http",
		TransportConfig: []byte(`{"api_key":"ref:catalog-token"}`),
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	if err := service.RefreshCatalog(ctx, created.ID); err != nil {
		t.Fatalf("RefreshCatalog: %v", err)
	}

	var (
		catalogCount int
		enabledCount int
		status       string
		eventCount   int
	)
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM mcp_tool_catalog WHERE connection_id = $1`, created.ID).Scan(&catalogCount); err != nil {
		t.Fatalf("count catalog rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM mcp_tool_catalog WHERE connection_id = $1 AND is_enabled = true`, created.ID).Scan(&enabledCount); err != nil {
		t.Fatalf("count enabled rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM mcp_connection WHERE id = $1`, created.ID).Scan(&status); err != nil {
		t.Fatalf("load connection status: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM domain_event WHERE organization_id = $1 AND event_type = 'mcp.catalog.changed'`, orgID).Scan(&eventCount); err != nil {
		t.Fatalf("count domain events: %v", err)
	}

	if catalogCount != 2 {
		t.Fatalf("catalog row count = %d, want 2", catalogCount)
	}
	if enabledCount != 0 {
		t.Fatalf("enabled row count = %d, want 0 (default deny)", enabledCount)
	}
	if status != "active" {
		t.Fatalf("connection status = %q, want active", status)
	}
	if eventCount != 1 {
		t.Fatalf("domain event count = %d, want 1", eventCount)
	}
}

func TestRegisterNativeToolDefinitionsIntegration(t *testing.T) {
	pool := testdb.New(t)
	toolRepo := repo.NewToolDefinitionRepo(pool)

	if err := RegisterNativeToolDefinitions(context.Background(), toolRepo); err != nil {
		t.Fatalf("RegisterNativeToolDefinitions: %v", err)
	}

	tool, err := toolRepo.GetByName(context.Background(), DiscoverToolName)
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if tool.Name != DiscoverToolName {
		t.Fatalf("tool name = %q, want %q", tool.Name, DiscoverToolName)
	}
	if tool.ToolTier != "tier1" {
		t.Fatalf("tool tier = %q, want tier1", tool.ToolTier)
	}
}

func TestResolveSecretBindingsIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgID := seedMCPOrgAndProject(t, ctx, pool)
	connRepo := repo.NewMCPConnectionRepo(pool)
	catalogRepo := repo.NewMCPToolCatalogRepo(pool)
	bindingRepo := repo.NewMCPSecretBindingRepo(pool)

	service, err := NewService(ServiceOptions{
		Connections: connRepo,
		Catalog:     catalogRepo,
		Bindings:    bindingRepo,
		Resolver: &integrationResolver{values: map[string]string{
			"ref:mcp-key":   "secret-key",
			"ref:mcp-token": "secret-token",
		}},
		TransportFactory: StaticTransportFactory{Transport: &MockTransport{}},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	created, err := service.CreateConnection(ctx, CreateConnectionRequest{
		OrganizationID: orgID,
		DisplayName:    "Resolve Connection",
		Slug:           "resolve-connection",
		Transport:      "http",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	if _, err := bindingRepo.Create(ctx, repo.MCPSecretBinding{ConnectionID: created.ID, SecretRef: "ref:mcp-key", EnvVarName: "MCP_KEY"}); err != nil {
		t.Fatalf("create binding MCP_KEY: %v", err)
	}
	if _, err := bindingRepo.Create(ctx, repo.MCPSecretBinding{ConnectionID: created.ID, SecretRef: "ref:mcp-token", EnvVarName: "MCP_TOKEN"}); err != nil {
		t.Fatalf("create binding MCP_TOKEN: %v", err)
	}

	resolved, err := service.ResolveSecretBindings(ctx, created.ID)
	if err != nil {
		t.Fatalf("ResolveSecretBindings: %v", err)
	}
	if resolved["MCP_KEY"] != "secret-key" || resolved["MCP_TOKEN"] != "secret-token" {
		t.Fatalf("resolved map = %#v", resolved)
	}
}

func TestHealthSchedulerTransitionsToDegraded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool := testdb.New(t)
	orgID := seedMCPOrgAndProject(t, ctx, pool)
	connRepo := repo.NewMCPConnectionRepo(pool)
	catalogRepo := repo.NewMCPToolCatalogRepo(pool)
	bindingRepo := repo.NewMCPSecretBindingRepo(pool)

	connection, err := connRepo.Create(ctx, repo.MCPConnection{
		OrganizationID:  orgID,
		DisplayName:     "Scheduler Connection",
		Slug:            "scheduler-connection",
		Transport:       "http",
		TransportConfig: []byte(`{"failure_threshold":2,"health_active_interval_ms":1,"health_scheduler_tick_ms":1,"health_check_timeout_ms":10}`),
		IsEnabled:       true,
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if _, err := connRepo.SetStatus(ctx, connection.ID, "active"); err != nil {
		t.Fatalf("set active status: %v", err)
	}

	factory := &switchingFactory{transport: &MockTransport{HealthCheckErr: errors.New("down")}}
	svc, err := NewService(ServiceOptions{
		Connections:          connRepo,
		Catalog:              catalogRepo,
		Bindings:             bindingRepo,
		Resolver:             &integrationResolver{},
		TransportFactory:     factory,
		DefaultSchedulerTick: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	svc.StartHealthScheduler(ctx)
	for i := 0; i < 4; i++ {
		if err := svc.RunHealthChecks(ctx); err != nil {
			t.Fatalf("RunHealthChecks: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitForStatus(t, ctx, pool, connection.ID, "degraded", 2*time.Second)
}

func TestCircuitBreakerStatusRecoveryViaHealthProbe(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := seedMCPOrgAndProject(t, ctx, pool)
	connRepo := repo.NewMCPConnectionRepo(pool)

	connection, err := connRepo.Create(ctx, repo.MCPConnection{
		OrganizationID:  orgID,
		DisplayName:     "Breaker Recovery",
		Slug:            "breaker-recovery",
		Transport:       "http",
		TransportConfig: []byte(`{"failure_threshold":5,"recovery_timeout_ms":20,"health_active_interval_ms":1,"health_degraded_interval_ms":1}`),
		IsEnabled:       true,
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if _, err := connRepo.SetStatus(ctx, connection.ID, "active"); err != nil {
		t.Fatalf("set active status: %v", err)
	}

	factory := &switchingFactory{transport: &MockTransport{CallErr: errors.New("network failure"), HealthCheckErr: errors.New("network failure")}}
	service, err := NewService(ServiceOptions{
		Connections:      connRepo,
		Catalog:          repo.NewMCPToolCatalogRepo(pool),
		Bindings:         repo.NewMCPSecretBindingRepo(pool),
		Resolver:         &integrationResolver{},
		TransportFactory: factory,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	for i := 0; i < 5; i++ {
		_, callErr := service.CallTool(ctx, CallToolRequest{
			ConnectionID: connection.ID,
			Tool:         "tool.write",
			Intent:       ToolIntentWrite,
			SafeToRetry:  boolPtr(false),
		})
		if callErr == nil {
			t.Fatalf("call %d should fail", i+1)
		}
	}

	waitForStatus(t, ctx, pool, connection.ID, "degraded", 2*time.Second)

	time.Sleep(50 * time.Millisecond)
	factory.setTransport(&MockTransport{HealthCheckErr: nil})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := service.RunHealthChecks(ctx); err != nil {
			t.Fatalf("RunHealthChecks: %v", err)
		}
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM mcp_connection WHERE id = $1`, connection.ID).Scan(&status); err != nil {
			t.Fatalf("load connection status: %v", err)
		}
		if status == "active" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM mcp_connection WHERE id = $1`, connection.ID).Scan(&status); err != nil {
		t.Fatalf("load connection status: %v", err)
	}
	t.Fatalf("status = %q, want active", status)
}

func TestCircuitBreakerStatusTransitionsToFailedAfterMaxConsecutiveOpens(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := seedMCPOrgAndProject(t, ctx, pool)
	connRepo := repo.NewMCPConnectionRepo(pool)

	connection, err := connRepo.Create(ctx, repo.MCPConnection{
		OrganizationID:  orgID,
		DisplayName:     "Breaker Permanent Open",
		Slug:            "breaker-permanent-open",
		Transport:       "http",
		TransportConfig: []byte(`{"failure_threshold":1,"max_consecutive_opens":3,"recovery_timeout_ms":5,"health_active_interval_ms":1,"health_degraded_interval_ms":1}`),
		IsEnabled:       true,
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if _, err := connRepo.SetStatus(ctx, connection.ID, "active"); err != nil {
		t.Fatalf("set active status: %v", err)
	}

	service, err := NewService(ServiceOptions{
		Connections:      connRepo,
		Catalog:          repo.NewMCPToolCatalogRepo(pool),
		Bindings:         repo.NewMCPSecretBindingRepo(pool),
		Resolver:         &integrationResolver{},
		TransportFactory: &switchingFactory{transport: &MockTransport{HealthCheckErr: errors.New("network failure")}},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := service.RunHealthChecks(ctx); err != nil {
			t.Fatalf("RunHealthChecks: %v", err)
		}
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM mcp_connection WHERE id = $1`, connection.ID).Scan(&status); err != nil {
			t.Fatalf("load connection status: %v", err)
		}
		if status == "failed" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM mcp_connection WHERE id = $1`, connection.ID).Scan(&status); err != nil {
		t.Fatalf("load connection status: %v", err)
	}
	t.Fatalf("status = %q, want failed", status)
}

func TestStdioTransportCallAndHealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stdio transport integration in short mode")
	}

	transport, err := NewStdioTransport(
		os.Args[0],
		[]string{"-test.run=TestMCPHelperProcess", "--"},
		"",
		map[string]string{"OTTERCAMP_MCP_HELPER": "1"},
	)
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}
	defer transport.Close()

	if err := transport.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck running process: %v", err)
	}

	tools, err := transport.DiscoverTools(context.Background())
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 || tools[0].ToolName != "echo" {
		t.Fatalf("tools = %#v, want echo tool", tools)
	}

	result, err := transport.Call(context.Background(), "echo", json.RawMessage(`{"msg":"hello"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !strings.Contains(string(result), "hello") {
		t.Fatalf("call result = %s, expected echoed payload", result)
	}

	if err := transport.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper process: %v", err)
	}
	<-transport.waitDone
	if err := transport.HealthCheck(context.Background()); err == nil {
		t.Fatal("HealthCheck should fail after process exit")
	}
}

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("OTTERCAMP_MCP_HELPER") != "1" {
		return
	}

	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var req map[string]any
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			writeRPCError(writer, req, -32700, "invalid json")
			continue
		}

		method, _ := req["method"].(string)
		switch method {
		case "ping":
			writeRPCResult(writer, req, map[string]any{"ok": true})
		case "tools/list":
			writeRPCResult(writer, req, map[string]any{"tools": []map[string]any{{
				"name":         "echo",
				"description":  "Echo input",
				"input_schema": map[string]any{"type": "object"},
				"metadata":     map[string]any{"safe_to_retry": true},
			}}})
		case "tools/call":
			params, _ := req["params"].(map[string]any)
			input := map[string]any{}
			if rawInput, ok := params["input"].(map[string]any); ok {
				input = rawInput
			}
			writeRPCResult(writer, req, map[string]any{"result": map[string]any{"echo": input}})
		default:
			writeRPCError(writer, req, -32601, fmt.Sprintf("unknown method: %s", method))
		}
	}
}

func writeRPCResult(w *bufio.Writer, req map[string]any, result any) {
	id, _ := req["id"]
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	_, _ = w.Write(append(payload, '\n'))
	_ = w.Flush()
}

func writeRPCError(w *bufio.Writer, req map[string]any, code int, message string) {
	id, _ := req["id"]
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
	_, _ = w.Write(append(payload, '\n'))
	_ = w.Flush()
}

func waitForStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, connectionID uuid.UUID, expected string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM mcp_connection WHERE id = $1`, connectionID).Scan(&status); err != nil {
			t.Fatalf("load connection status: %v", err)
		}
		if status == expected {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM mcp_connection WHERE id = $1`, connectionID).Scan(&status); err != nil {
		t.Fatalf("load connection status: %v", err)
	}
	t.Fatalf("status = %q, want %q", status, expected)
}

func seedMCPOrgAndProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()

	orgRepo := repo.NewOrgRepo(pool)
	projectRepo := repo.NewProjectRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{Slug: "mcp-service-org", DisplayName: "MCP Service Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "mcp-service-project",
		DisplayName:    "MCP Service Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return org.ID
}

type integrationResolver struct {
	values map[string]string
}

func (r *integrationResolver) ResolveRef(_ context.Context, _ uuid.UUID, ref string) (string, error) {
	if value, ok := r.values[ref]; ok {
		return value, nil
	}
	return ref, nil
}

type switchingFactory struct {
	mu        sync.RWMutex
	transport MCPTransport
	err       error
}

func (f *switchingFactory) New(_ context.Context, _ repo.MCPConnection, _ map[string]any, _ map[string]string) (MCPTransport, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.transport, nil
}

func (f *switchingFactory) setTransport(transport MCPTransport) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transport = transport
}

func boolPtr(v bool) *bool {
	return &v
}
