//go:build integration

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/testutil"
)

func TestCircuitBreaker_Closed_NormalOperation(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	agent := testutil.MakeAgent(t, pool, orgID)
	server := testutil.MockMCPServer(t, testutil.MockMCPServerOptions{
		CallResult: json.RawMessage(`{"ok":true}`),
	})

	service := mustNewMCPService(t, pool, ServiceOptions{
		Resolver:         &integrationResolver{},
		TransportFactory: NewDefaultTransportFactory(&http.Client{Timeout: time.Second}),
	})
	logRepo := repo.NewMCPExecutionLogRepo(pool)
	executor := mustNewExecutor(t, pool, service, logRepo)

	connID := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		Status:    "active",
		Transport: "http",
		TransportConfig: json.RawMessage(`{
			"base_url":"` + server.URL + `",
			"failure_threshold": 3
		}`),
	})
	mustSeedCatalogTool(t, pool, connID, "tool.echo", json.RawMessage(`{}`))

	execCtx := WithExecutionContext(ctx, ExecutionContext{AgentID: &agent.ID})
	if _, err := executor.Execute(execCtx, "tool.echo", map[string]any{"input": "ok"}, connID); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if state := service.CircuitState(connID); state != CBClosed {
		t.Fatalf("CircuitState = %q, want closed", state)
	}

	logs, err := logRepo.ListByConnection(ctx, connID)
	if err != nil {
		t.Fatalf("ListByConnection logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("log count = %d, want 1", len(logs))
	}
	if logs[0].Status != "success" {
		t.Fatalf("log status = %q, want success", logs[0].Status)
	}
}

func TestCircuitBreaker_Trips_OnConsecutiveFailures(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	server := testutil.MockMCPServer(t, testutil.MockMCPServerOptions{
		CallStatusCode: http.StatusInternalServerError,
		CallErrorBody:  "forced failure",
	})

	service := mustNewMCPService(t, pool, ServiceOptions{
		Resolver:         &integrationResolver{},
		TransportFactory: NewDefaultTransportFactory(&http.Client{Timeout: time.Second}),
	})
	connID := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		Status:    "active",
		Transport: "http",
		TransportConfig: json.RawMessage(`{
			"base_url":"` + server.URL + `",
			"failure_threshold": 2,
			"recovery_timeout_ms": 5000
		}`),
	})

	for i := 0; i < 2; i++ {
		_, err := service.CallTool(ctx, CallToolRequest{
			ConnectionID: connID,
			Tool:         "tool.echo",
			Input:        json.RawMessage(`{"x":1}`),
			Intent:       ToolIntentWrite,
			SafeToRetry:  ptrBool(false),
		})
		if err == nil {
			t.Fatalf("call %d error = nil, want failure", i+1)
		}
	}
	if state := service.CircuitState(connID); state != CBOpen {
		t.Fatalf("CircuitState after threshold failures = %q, want open", state)
	}

	before := server.CallsForPath("/tools/call")
	_, err := service.CallTool(ctx, CallToolRequest{
		ConnectionID: connID,
		Tool:         "tool.echo",
		Input:        json.RawMessage(`{"x":2}`),
		Intent:       ToolIntentWrite,
		SafeToRetry:  ptrBool(false),
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("call after open error = %v, want ErrCircuitOpen", err)
	}
	if got := server.CallsForPath("/tools/call"); got != before {
		t.Fatalf("tools/call count = %d, want %d (no extra call after open)", got, before)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM mcp_connection WHERE id = $1`, connID).Scan(&status); err != nil {
		t.Fatalf("load connection status: %v", err)
	}
	if status != "degraded" {
		t.Fatalf("connection status = %q, want degraded", status)
	}
}

func TestCircuitBreaker_Open_RejectsAllCalls(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	server := testutil.MockMCPServer(t, testutil.MockMCPServerOptions{
		CallStatusCode: http.StatusInternalServerError,
		CallErrorBody:  "forced failure",
	})

	service := mustNewMCPService(t, pool, ServiceOptions{
		Resolver:         &integrationResolver{},
		TransportFactory: NewDefaultTransportFactory(&http.Client{Timeout: time.Second}),
	})
	connID := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		Status:    "active",
		Transport: "http",
		TransportConfig: json.RawMessage(`{
			"base_url":"` + server.URL + `",
			"failure_threshold": 1,
			"recovery_timeout_ms": 5000
		}`),
	})

	_, _ = service.CallTool(ctx, CallToolRequest{
		ConnectionID: connID,
		Tool:         "tool.echo",
		Intent:       ToolIntentWrite,
		SafeToRetry:  ptrBool(false),
	})
	if state := service.CircuitState(connID); state != CBOpen {
		t.Fatalf("CircuitState = %q, want open", state)
	}

	before := server.CallsForPath("/tools/call")
	_, err := service.CallTool(ctx, CallToolRequest{
		ConnectionID: connID,
		Tool:         "tool.echo",
		Intent:       ToolIntentWrite,
		SafeToRetry:  ptrBool(false),
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("open-state call error = %v, want ErrCircuitOpen", err)
	}
	if got := server.CallsForPath("/tools/call"); got != before {
		t.Fatalf("server calls after open = %d, want %d", got, before)
	}
}

func TestCircuitBreaker_HalfOpen_RecoveryAttempt(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	clock := newTestClock(time.Now().UTC())
	server := testutil.ScriptedMCPServer(t, []testutil.ScriptedMCPHandler{
		mcpCallFailureHandler(),
		mcpCallSuccessHandler(),
		mcpCallSuccessHandler(),
	})

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
			"failure_threshold": 1,
			"recovery_timeout_ms": 1000
		}`),
	})

	_, _ = service.CallTool(ctx, CallToolRequest{
		ConnectionID: connID,
		Tool:         "tool.echo",
		Intent:       ToolIntentWrite,
		SafeToRetry:  ptrBool(false),
	})
	if state := service.CircuitState(connID); state != CBOpen {
		t.Fatalf("CircuitState after first failure = %q, want open", state)
	}

	clock.Advance(1100 * time.Millisecond)
	if _, err := service.CallTool(ctx, CallToolRequest{
		ConnectionID: connID,
		Tool:         "tool.echo",
		Intent:       ToolIntentWrite,
		SafeToRetry:  ptrBool(false),
	}); err != nil {
		t.Fatalf("half-open probe call: %v", err)
	}
	if _, err := service.CallTool(ctx, CallToolRequest{
		ConnectionID: connID,
		Tool:         "tool.echo",
		Intent:       ToolIntentWrite,
		SafeToRetry:  ptrBool(false),
	}); err != nil {
		t.Fatalf("post-recovery normal call: %v", err)
	}
	if state := service.CircuitState(connID); state != CBClosed {
		t.Fatalf("CircuitState = %q, want closed", state)
	}
}

func TestCircuitBreaker_HalfOpen_FailsBack(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	clock := newTestClock(time.Now().UTC())
	server := testutil.ScriptedMCPServer(t, []testutil.ScriptedMCPHandler{
		mcpCallFailureHandler(),
		mcpCallFailureHandler(),
		mcpCallSuccessHandler(),
	})

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
			"failure_threshold": 1,
			"recovery_timeout_ms": 1000
		}`),
	})

	_, _ = service.CallTool(ctx, CallToolRequest{
		ConnectionID: connID,
		Tool:         "tool.echo",
		Intent:       ToolIntentWrite,
		SafeToRetry:  ptrBool(false),
	})
	clock.Advance(1100 * time.Millisecond)
	_, _ = service.CallTool(ctx, CallToolRequest{
		ConnectionID: connID,
		Tool:         "tool.echo",
		Intent:       ToolIntentWrite,
		SafeToRetry:  ptrBool(false),
	})
	if state := service.CircuitState(connID); state != CBOpen {
		t.Fatalf("CircuitState after failed probe = %q, want open", state)
	}

	before := server.CallsForPath("/tools/call")
	clock.Advance(500 * time.Millisecond)
	_, err := service.CallTool(ctx, CallToolRequest{
		ConnectionID: connID,
		Tool:         "tool.echo",
		Intent:       ToolIntentWrite,
		SafeToRetry:  ptrBool(false),
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("call before full recovery window error = %v, want ErrCircuitOpen", err)
	}
	if got := server.CallsForPath("/tools/call"); got != before {
		t.Fatalf("server calls after early retry = %d, want %d", got, before)
	}

	clock.Advance(600 * time.Millisecond)
	if _, err := service.CallTool(ctx, CallToolRequest{
		ConnectionID: connID,
		Tool:         "tool.echo",
		Intent:       ToolIntentWrite,
		SafeToRetry:  ptrBool(false),
	}); err != nil {
		t.Fatalf("call after reset window should probe/succeed: %v", err)
	}
}

func TestCircuitBreaker_PerCallTimeout(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	server := testutil.MockMCPServer(t, testutil.MockMCPServerOptions{
		PerRequestDelay: 200 * time.Millisecond,
	})

	service := mustNewMCPService(t, pool, ServiceOptions{
		Resolver:         &integrationResolver{},
		TransportFactory: NewDefaultTransportFactory(&http.Client{Timeout: time.Second}),
	})
	connID := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		Status:    "active",
		Transport: "http",
		TransportConfig: json.RawMessage(`{
			"base_url":"` + server.URL + `",
			"per_call_timeout_ms": 25,
			"failure_threshold": 1
		}`),
	})

	_, err := service.CallTool(ctx, CallToolRequest{
		ConnectionID: connID,
		Tool:         "tool.echo",
		Intent:       ToolIntentWrite,
		SafeToRetry:  ptrBool(false),
	})
	if err == nil {
		t.Fatal("timeout call error = nil, want timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout call error = %v, want context deadline exceeded", err)
	}
	if state := service.CircuitState(connID); state != CBOpen {
		t.Fatalf("CircuitState = %q, want open after timeout failure", state)
	}
}

func TestCircuitBreaker_WriteTools_NoAutoRetry(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	transport := &MockTransport{
		CallFn: func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
			return nil, context.DeadlineExceeded
		},
	}

	service := mustNewMCPService(t, pool, ServiceOptions{
		Resolver:         &integrationResolver{},
		TransportFactory: StaticTransportFactory{Transport: transport},
	})
	connID := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		Status:    "active",
		Transport: "http",
		TransportConfig: json.RawMessage(`{
			"base_url":"https://mcp.example.test",
			"failure_threshold": 1
		}`),
	})

	_, err := service.CallTool(ctx, CallToolRequest{
		ConnectionID: connID,
		Tool:         "tool.write",
		Intent:       ToolIntentWrite,
		SafeToRetry:  nil,
	})
	if err == nil {
		t.Fatal("write failure error = nil, want error")
	}
	if transport.CallCount != 1 {
		t.Fatalf("transport call count = %d, want 1 (no auto-retry)", transport.CallCount)
	}
	if state := service.CircuitState(connID); state != CBOpen {
		t.Fatalf("CircuitState = %q, want open", state)
	}
}

func mustNewExecutor(t *testing.T, pool *pgxpool.Pool, caller MCPCaller, logs *repo.MCPExecutionLogRepo) *Executor {
	t.Helper()

	executor, err := NewExecutor(ExecutorOptions{
		Connections:      repo.NewMCPConnectionRepo(pool),
		ConnectionStatus: repo.NewMCPConnectionRepo(pool),
		Catalog:          repo.NewMCPToolCatalogRepo(pool),
		Assignments:      repo.NewAgentProjectAssignmentRepo(pool),
		Caller:           caller,
		Logs:             logs,
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	return executor
}

func mustSeedCatalogTool(t *testing.T, pool *pgxpool.Pool, connID uuid.UUID, toolName string, metadata json.RawMessage) {
	t.Helper()

	_, err := repo.NewMCPToolCatalogRepo(pool).BulkUpsert(context.Background(), connID, []repo.MCPToolCatalogEntry{{
		ConnectionID: connID,
		ToolName:     toolName,
		Description:  toolName + " description",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		Metadata:     metadata,
	}})
	if err != nil {
		t.Fatalf("seed catalog tool: %v", err)
	}
}

func mcpCallFailureHandler() testutil.ScriptedMCPHandler {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/tools/call" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("forced failure"))
	}
}

func mcpCallSuccessHandler() testutil.ScriptedMCPHandler {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/tools/call" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{"ok": true},
		})
	}
}

func ptrBool(v bool) *bool {
	return &v
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(start time.Time) *testClock {
	return &testClock{now: start.UTC()}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(delta time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(delta)
}
