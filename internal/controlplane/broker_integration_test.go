//go:build integration

package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/tools/native"
)

func TestToolBrokerDispatchTier2PipelineIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := seedControlPlaneOrg(t, ctx, pool)
	agentRecord := seedBrokerAgent(t, ctx, pool, org.ID)
	seedToolDefinition(t, ctx, pool, repo.ToolDefinition{
		Name:               "tooltest.file.write",
		DisplayName:        "File Write",
		Description:        "Write a file",
		ToolTier:           "tier2",
		ToolDomain:         "native",
		RequiredCapability: strPtr("system.file.write"),
		IsEnabled:          true,
	})

	runRecord, step, attempt := seedBrokerRunContext(t, ctx, pool, org.ID)
	broker, err := NewToolBroker(ToolBrokerOptions{
		Pool:   pool,
		Policy: &integrationPolicyService{decision: CapabilityDecision{Allowed: true}},
		Native: &integrationNativeExecutor{output: map[string]any{"ok": true}},
	})
	if err != nil {
		t.Fatalf("NewToolBroker: %v", err)
	}

	execRecord, dispatchErr := broker.Dispatch(ctx, DispatchInput{
		RunID:        &runRecord.ID,
		RunStepID:    &step.ID,
		RunAttemptID: &attempt.ID,
		AgentID:      agentRecord.ID,
		ToolName:     "tooltest.file.write",
		ToolTier:     "tier2",
		Input:        map[string]any{"path": "README.md"},
	})
	if dispatchErr != nil {
		t.Fatalf("Dispatch: %v", dispatchErr)
	}

	stored, err := NewToolExecutionRepository(pool).Get(ctx, execRecord.ID)
	if err != nil {
		t.Fatalf("Get tool_execution: %v", err)
	}
	if stored.PolicyDecision != "allowed" {
		t.Fatalf("policy_decision = %q, want allowed", stored.PolicyDecision)
	}
	if stored.Status != "completed" {
		t.Fatalf("status = %q, want completed", stored.Status)
	}
}

func TestToolBrokerDispatchFileReadPersistsOffsetWindowIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "window.txt"), []byte("0123456789abcdef"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	org := seedControlPlaneOrg(t, ctx, pool)
	agentRecord := seedBrokerAgent(t, ctx, pool, org.ID)
	runRecord, step, attempt := seedBrokerRunContext(t, ctx, pool, org.ID)

	broker, err := NewToolBroker(ToolBrokerOptions{
		Pool:   pool,
		Policy: &integrationPolicyService{decision: CapabilityDecision{Allowed: true}},
		Native: native.NewExecutor(native.ExecutorOptions{WorkspaceRoot: root}),
	})
	if err != nil {
		t.Fatalf("NewToolBroker: %v", err)
	}

	execRecord, dispatchErr := broker.Dispatch(ctx, DispatchInput{
		RunID:        &runRecord.ID,
		RunStepID:    &step.ID,
		RunAttemptID: &attempt.ID,
		AgentID:      agentRecord.ID,
		ToolName:     "file.read",
		ToolTier:     "tier1",
		Input: map[string]any{
			"path":         "window.txt",
			"offset_bytes": 4,
			"max_bytes":    6,
		},
	})
	if dispatchErr != nil {
		t.Fatalf("Dispatch: %v", dispatchErr)
	}
	if got := execRecord.Output; got == nil {
		t.Fatal("execution output = nil, want populated")
	}

	stored, err := NewToolExecutionRepository(pool).Get(ctx, execRecord.ID)
	if err != nil {
		t.Fatalf("Get tool_execution: %v", err)
	}
	var output map[string]any
	if err := json.Unmarshal(stored.Output, &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got := output["content"]; got != "456789" {
		t.Fatalf("content = %v, want 456789", got)
	}
	if got := output["offset_bytes"]; got != float64(4) {
		t.Fatalf("offset_bytes = %v, want 4", got)
	}
	if got := output["bytes_read"]; got != float64(6) {
		t.Fatalf("bytes_read = %v, want 6", got)
	}
	if got := output["truncated"]; got != true {
		t.Fatalf("truncated = %v, want true", got)
	}
}

func TestToolBrokerDispatchMCPWritesExecutionLogIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := seedControlPlaneOrg(t, ctx, pool)
	agentRecord := seedBrokerAgent(t, ctx, pool, org.ID)
	seedToolDefinition(t, ctx, pool, repo.ToolDefinition{
		Name:               "github.issue.create",
		DisplayName:        "GitHub Create Issue",
		Description:        "Create issue via MCP",
		ToolTier:           "tier2",
		ToolDomain:         "mcp",
		RequiredCapability: strPtr("mcp.connection.use"),
		IsEnabled:          true,
	})
	connection := seedBrokerMCPConnection(t, ctx, pool, org.ID)
	runRecord, step, attempt := seedBrokerRunContext(t, ctx, pool, org.ID)

	mcpLogs := repo.NewMCPExecutionLogRepo(pool)
	mcpExecutor, err := mcp.NewExecutor(mcp.ExecutorOptions{
		Connections:      repo.NewMCPConnectionRepo(pool),
		ConnectionStatus: repo.NewMCPConnectionRepo(pool),
		Caller: &integrationMCPCaller{
			state:  mcp.CBClosed,
			result: json.RawMessage(`{"id":"123"}`),
		},
		Logs: mcpLogs,
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	broker, err := NewToolBroker(ToolBrokerOptions{
		Pool:   pool,
		Policy: &integrationPolicyService{decision: CapabilityDecision{Allowed: true}},
		MCP:    mcpExecutor,
	})
	if err != nil {
		t.Fatalf("NewToolBroker: %v", err)
	}

	execRecord, dispatchErr := broker.Dispatch(ctx, DispatchInput{
		RunID:        &runRecord.ID,
		RunStepID:    &step.ID,
		RunAttemptID: &attempt.ID,
		AgentID:      agentRecord.ID,
		ToolName:     "github.issue.create",
		ToolTier:     "tier2",
		Input: map[string]any{
			"mcp_connection_id": connection.ID.String(),
			"title":             "integration",
		},
	})
	if dispatchErr != nil {
		t.Fatalf("Dispatch: %v", dispatchErr)
	}

	entries, err := mcpLogs.ListByConnection(ctx, connection.ID)
	if err != nil {
		t.Fatalf("ListByConnection: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected mcp_execution_log rows")
	}
	if entries[0].ToolExecutionID == nil || *entries[0].ToolExecutionID != execRecord.ID {
		t.Fatalf("tool_execution_id = %v, want %s", entries[0].ToolExecutionID, execRecord.ID)
	}
}

func TestToolBrokerDispatchPolicyDeniedIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := seedControlPlaneOrg(t, ctx, pool)
	agentRecord := seedBrokerAgent(t, ctx, pool, org.ID)
	seedToolDefinition(t, ctx, pool, repo.ToolDefinition{
		Name:               "tooltest.browser.navigate",
		DisplayName:        "Browser Navigate",
		Description:        "Navigate browser",
		ToolTier:           "tier2",
		ToolDomain:         "browser",
		RequiredCapability: strPtr("system.browser.interact"),
		IsEnabled:          true,
	})
	runRecord, step, attempt := seedBrokerRunContext(t, ctx, pool, org.ID)

	broker, err := NewToolBroker(ToolBrokerOptions{
		Pool:     pool,
		EventBus: eventbus.New(pool, nil, eventbus.Config{}),
		Policy:   &integrationPolicyService{decision: CapabilityDecision{Allowed: false, Reason: "blocked"}},
		Browser:  &integrationBrowserExecutor{output: map[string]any{"ok": true}},
	})
	if err != nil {
		t.Fatalf("NewToolBroker: %v", err)
	}

	execRecord, dispatchErr := broker.Dispatch(ctx, DispatchInput{
		RunID:        &runRecord.ID,
		RunStepID:    &step.ID,
		RunAttemptID: &attempt.ID,
		AgentID:      agentRecord.ID,
		ToolName:     "tooltest.browser.navigate",
		ToolTier:     "tier2",
		Input:        map[string]any{"url": "https://example.com"},
	})
	if !errors.Is(dispatchErr, ErrCapabilityDenied) {
		t.Fatalf("Dispatch error = %v, want ErrCapabilityDenied", dispatchErr)
	}
	if execRecord.PolicyDecision != "denied" || execRecord.Status != "policy_denied" {
		t.Fatalf("execution = %+v, want denied/policy_denied", execRecord)
	}

	stored, err := NewToolExecutionRepository(pool).Get(ctx, execRecord.ID)
	if err != nil {
		t.Fatalf("Get tool_execution: %v", err)
	}
	if stored.PolicyDecision != "denied" || stored.Status != "policy_denied" {
		t.Fatalf("stored execution = %+v, want denied/policy_denied", stored)
	}

	var deniedCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'tool.capability_denied'
		  AND payload->>'run_id' = $2
	`, org.ID, runRecord.ID.String()).Scan(&deniedCount); err != nil {
		t.Fatalf("count tool.capability_denied events: %v", err)
	}
	if deniedCount == 0 {
		t.Fatal("expected tool.capability_denied domain_event")
	}
}

func seedBrokerAgent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) repo.Agent {
	t.Helper()
	agentRecord, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          "Broker Agent",
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
	return agentRecord
}

func seedToolDefinition(t *testing.T, ctx context.Context, pool *pgxpool.Pool, definition repo.ToolDefinition) {
	t.Helper()
	if _, err := repo.NewToolDefinitionRepo(pool).Create(ctx, definition); err != nil {
		t.Fatalf("create tool definition %s: %v", definition.Name, err)
	}
}

func seedBrokerRunContext(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) (Run, RunStep, RunAttempt) {
	t.Helper()
	runRepo := NewRunRepository(pool)
	stepRepo := NewRunStepRepository(pool)
	attemptRepo := NewRunAttemptRepository(pool)

	runRecord, err := runRepo.Create(ctx, Run{
		OrganizationID: orgID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Status:         "created",
		Metadata:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	step, err := stepRepo.Create(ctx, RunStep{RunID: runRecord.ID, StepNumber: 1, Status: "pending", Metadata: []byte(`{}`)})
	if err != nil {
		t.Fatalf("create run step: %v", err)
	}
	attempt, err := attemptRepo.Create(ctx, RunAttempt{RunStepID: step.ID, AttemptNumber: 1, Trigger: "initial", Status: "pending", Metadata: []byte(`{}`)})
	if err != nil {
		t.Fatalf("create run attempt: %v", err)
	}
	return runRecord, step, attempt
}

func seedBrokerMCPConnection(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) repo.MCPConnection {
	t.Helper()
	connection, err := repo.NewMCPConnectionRepo(pool).Create(ctx, repo.MCPConnection{
		OrganizationID:  orgID,
		DisplayName:     "Broker MCP",
		Slug:            "broker-mcp-" + uuid.NewString()[:8],
		Transport:       "http",
		TransportConfig: []byte(`{"base_url":"http://example.local"}`),
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create mcp connection: %v", err)
	}
	return connection
}

type integrationPolicyService struct {
	decision CapabilityDecision
	err      error
}

func (s *integrationPolicyService) EvaluateCapability(context.Context, uuid.UUID, *uuid.UUID, uuid.UUID, string) (CapabilityDecision, error) {
	if s.err != nil {
		return CapabilityDecision{}, s.err
	}
	return s.decision, nil
}

type integrationNativeExecutor struct {
	output map[string]any
}

func (e *integrationNativeExecutor) Execute(context.Context, string, map[string]any) (map[string]any, error) {
	if e.output == nil {
		return map[string]any{}, nil
	}
	return e.output, nil
}

type integrationBrowserExecutor struct {
	output map[string]any
}

func (e *integrationBrowserExecutor) Execute(context.Context, map[string]any) (map[string]any, error) {
	if e.output == nil {
		return map[string]any{}, nil
	}
	return e.output, nil
}

type integrationMCPCaller struct {
	state  mcp.CBState
	result json.RawMessage
	err    error
}

func (c *integrationMCPCaller) CallTool(context.Context, mcp.CallToolRequest) (json.RawMessage, error) {
	if c.err != nil {
		return nil, c.err
	}
	if len(c.result) == 0 {
		return json.RawMessage(`{"ok":true}`), nil
	}
	return c.result, nil
}

func (c *integrationMCPCaller) CircuitState(uuid.UUID) mcp.CBState {
	if c.state == "" {
		return mcp.CBClosed
	}
	return c.state
}
