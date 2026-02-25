//go:build integration

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/testutil"
)

func TestExecutionLog_RecordOnSuccess(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	agent := testutil.MakeAgent(t, pool, orgID)
	server := testutil.MockMCPServer(t, testutil.MockMCPServerOptions{
		CallResult: json.RawMessage(`{"message":"ok"}`),
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
			"base_url":"` + server.URL + `"
		}`),
	})
	mustSeedCatalogTool(t, pool, connID, "tool.echo", json.RawMessage(`{"safe_to_retry":false}`))

	execCtx := WithExecutionContext(ctx, ExecutionContext{AgentID: &agent.ID})
	if _, err := executor.Execute(execCtx, "tool.echo", map[string]any{"request": "ok"}, connID); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	logs, err := logRepo.ListByConnection(ctx, connID)
	if err != nil {
		t.Fatalf("ListByConnection: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("log count = %d, want 1", len(logs))
	}
	entry := logs[0]
	if entry.Status != "success" {
		t.Fatalf("status = %q, want success", entry.Status)
	}
	if entry.ToolName == nil || *entry.ToolName != "tool.echo" {
		t.Fatalf("tool_name = %v, want tool.echo", entry.ToolName)
	}
	if entry.AgentID == nil || *entry.AgentID != agent.ID {
		t.Fatalf("agent_id = %v, want %s", entry.AgentID, agent.ID)
	}
	if entry.LatencyMS == nil || *entry.LatencyMS < 0 {
		t.Fatalf("latency_ms = %v, want non-negative", entry.LatencyMS)
	}
}

func TestExecutionLog_RecordOnFailure(t *testing.T) {
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
	logRepo := repo.NewMCPExecutionLogRepo(pool)
	executor := mustNewExecutor(t, pool, service, logRepo)

	connID := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		Status:    "active",
		Transport: "http",
		TransportConfig: json.RawMessage(`{
			"base_url":"` + server.URL + `"
		}`),
	})
	mustSeedCatalogTool(t, pool, connID, "tool.echo", json.RawMessage(`{}`))

	if _, err := executor.Execute(ctx, "tool.echo", map[string]any{"request": "fail"}, connID); err == nil {
		t.Fatal("Execute error = nil, want failure")
	}

	logs, err := logRepo.ListByConnection(ctx, connID)
	if err != nil {
		t.Fatalf("ListByConnection: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("log count = %d, want 1", len(logs))
	}
	entry := logs[0]
	if entry.Status != "error" {
		t.Fatalf("status = %q, want error", entry.Status)
	}
	if entry.ErrorMessage == nil || strings.TrimSpace(*entry.ErrorMessage) == "" {
		t.Fatalf("error_message = %v, want populated", entry.ErrorMessage)
	}
}

func TestExecutionLog_RunLinkage(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	agent := testutil.MakeAgent(t, pool, orgID)
	server := testutil.MockMCPServer(t, testutil.MockMCPServerOptions{
		CallResult: json.RawMessage(`{"message":"linked"}`),
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
			"base_url":"` + server.URL + `"
		}`),
	})
	mustSeedCatalogTool(t, pool, connID, "tool.echo", json.RawMessage(`{}`))

	runID, toolExecutionID := mustInsertRunAndToolExecution(t, pool, orgID)
	execCtx := WithExecutionContext(ctx, ExecutionContext{
		RunID:           &runID,
		ToolExecutionID: &toolExecutionID,
		AgentID:         &agent.ID,
	})
	if _, err := executor.Execute(execCtx, "tool.echo", map[string]any{"request": "linked"}, connID); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	logs, err := logRepo.ListByConnection(ctx, connID)
	if err != nil {
		t.Fatalf("ListByConnection: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("log count = %d, want 1", len(logs))
	}
	entry := logs[0]
	if entry.RunID == nil || *entry.RunID != runID {
		t.Fatalf("run_id = %v, want %s", entry.RunID, runID)
	}
	if entry.ToolExecutionID == nil || *entry.ToolExecutionID != toolExecutionID {
		t.Fatalf("tool_execution_id = %v, want %s", entry.ToolExecutionID, toolExecutionID)
	}

	var (
		runCount      int
		toolExecCount int
	)
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM run WHERE id = $1`, runID).Scan(&runCount); err != nil {
		t.Fatalf("count run row: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM tool_execution WHERE id = $1`, toolExecutionID).Scan(&toolExecCount); err != nil {
		t.Fatalf("count tool_execution row: %v", err)
	}
	if runCount != 1 || toolExecCount != 1 {
		t.Fatalf("fk rows missing run_count=%d tool_execution_count=%d", runCount, toolExecCount)
	}
}

func TestExecutionLog_SecretScrubber(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	secret := "sk-abc123def456ghi789jklmnopqrst"
	server := testutil.MockMCPServer(t, testutil.MockMCPServerOptions{
		CallResult: json.RawMessage(`{"token":"` + secret + `"}`),
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
			"base_url":"` + server.URL + `"
		}`),
	})
	mustSeedCatalogTool(t, pool, connID, "tool.echo", json.RawMessage(`{}`))

	if _, err := executor.Execute(ctx, "tool.echo", map[string]any{"request": "scrub"}, connID); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var responseText string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(response_payload::text, '')
		FROM mcp_execution_log
		WHERE mcp_connection_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, connID).Scan(&responseText); err != nil {
		t.Fatalf("load response payload text: %v", err)
	}
	if strings.Contains(responseText, secret) {
		t.Fatalf("response payload leaked raw secret: %s", responseText)
	}
	if !strings.Contains(responseText, "[REDACTED]") {
		t.Fatalf("response payload missing [REDACTED]: %s", responseText)
	}
}

func TestMCPResource_ScopeCheck(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)

	projectScopedConnID := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		ProjectID: &project.ID,
		Status:    "active",
	})
	orgScopedConnID := testutil.MakeMCPConnection(t, pool, orgID, testutil.MCPConnectionOptions{
		Status: "active",
	})

	resourceReader := &recordingResourceReader{
		payload: json.RawMessage(`{"resource":"ok"}`),
	}
	executor, err := NewExecutor(ExecutorOptions{
		Connections:      repo.NewMCPConnectionRepo(pool),
		ConnectionStatus: repo.NewMCPConnectionRepo(pool),
		Assignments:      repo.NewAgentProjectAssignmentRepo(pool),
		Caller:           noopCaller{},
		Resources:        resourceReader,
		Logs:             repo.NewMCPExecutionLogRepo(pool),
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	projectCtx := WithExecutionContext(ctx, ExecutionContext{AgentID: &agent.ID})
	if _, err := executor.ReadResource(projectCtx, "resource://project/data", projectScopedConnID); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("project-scoped read without assignment error = %v, want ErrAccessDenied", err)
	}
	if resourceReader.CallCount() != 0 {
		t.Fatalf("resource reader calls after denied read = %d, want 0", resourceReader.CallCount())
	}

	_, assignErr := repo.NewAgentProjectAssignmentRepo(pool).Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        agent.ID,
		ProjectID:      project.ID,
		Role:           "worker",
		AssignedByType: "system",
		AssignedByID:   nil,
	})
	if assignErr != nil {
		t.Fatalf("assign agent to project: %v", assignErr)
	}

	if _, err := executor.ReadResource(projectCtx, "resource://project/data", projectScopedConnID); err != nil {
		t.Fatalf("project-scoped read with assignment: %v", err)
	}

	if _, err := executor.ReadResource(context.Background(), "resource://org/data", orgScopedConnID); err != nil {
		t.Fatalf("org-scoped read: %v", err)
	}
	if resourceReader.CallCount() != 2 {
		t.Fatalf("resource reader calls = %d, want 2", resourceReader.CallCount())
	}
}

func mustInsertRunAndToolExecution(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()

	var runID uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO run (organization_id, principal_type, principal_id, trigger_type, status)
		VALUES ($1, 'system', $2, 'api', 'in_progress')
		RETURNING id
	`, orgID, uuid.Nil).Scan(&runID); err != nil {
		t.Fatalf("insert run: %v", err)
	}

	var toolExecutionID uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO tool_execution (run_id, tool_name, tool_tier, tool_domain, policy_decision, input, status)
		VALUES ($1, 'mcp.tool.echo', 'tier2', 'mcp', 'allowed', '{}'::jsonb, 'pending')
		RETURNING id
	`, runID).Scan(&toolExecutionID); err != nil {
		t.Fatalf("insert tool_execution: %v", err)
	}
	return runID, toolExecutionID
}

type noopCaller struct{}

func (noopCaller) CallTool(context.Context, CallToolRequest) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (noopCaller) CircuitState(uuid.UUID) CBState {
	return CBClosed
}

type recordingResourceReader struct {
	mu      sync.Mutex
	payload json.RawMessage
	err     error
	calls   int
}

func (r *recordingResourceReader) ReadResource(_ context.Context, _ uuid.UUID, _ string) (json.RawMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	return r.payload, nil
}

func (r *recordingResourceReader) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}
