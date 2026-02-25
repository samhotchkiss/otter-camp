//go:build integration

package tools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestToolResolverIntegrationFullPipelineAndCacheHit(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, projectA, _, agent := seedToolResolverBase(t, ctx, pool, []string{"git.*"})
	session := seedProjectScopedSession(t, ctx, pool, org.ID, projectA.ID)

	seedNativeTool(t, ctx, pool, "memory.lookup", "tier1", "memory")
	seedNativeTool(t, ctx, pool, "git.test_push", "tier2", "git")

	conn := seedMCPConnection(t, ctx, pool, org.ID, nil)
	seedMCPToolCatalogEntry(t, ctx, pool, conn.ID, "mcp.github.create_issue")

	resolver, err := NewToolResolver(ToolResolverOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewToolResolver: %v", err)
	}

	first, err := resolver.GetSessionToolSet(ctx, session.ID, agent.ID)
	if err != nil {
		t.Fatalf("GetSessionToolSet first: %v", err)
	}
	if !containsTool(first, "memory.lookup") {
		t.Fatalf("resolved set missing memory.lookup: %+v", first)
	}
	if !containsTool(first, "mcp.github.create_issue") {
		t.Fatalf("resolved set missing org-scoped mcp tool: %+v", first)
	}
	if containsTool(first, "git.test_push") {
		t.Fatalf("resolved set unexpectedly contains git.test_push despite deny list")
	}

	second, err := resolver.GetSessionToolSet(ctx, session.ID, agent.ID)
	if err != nil {
		t.Fatalf("GetSessionToolSet second: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("cache hit returned different set length: first=%d second=%d", len(first), len(second))
	}

	var rowCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM session_tool_set
		WHERE session_id = $1
		  AND agent_id = $2
	`, session.ID, agent.ID).Scan(&rowCount); err != nil {
		t.Fatalf("count session_tool_set rows: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("session_tool_set row count = %d, want 1", rowCount)
	}
}

func TestToolResolverIntegrationPolicyUpdateInvalidatesAndRebuildsCache(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := eventbus.New(pool, logger, eventbus.Config{
		PollInterval:         20 * time.Millisecond,
		ListenReconnectDelay: 20 * time.Millisecond,
		BatchSize:            20,
	})

	org, projectA, _, agent := seedToolResolverBase(t, ctx, pool, nil)
	session := seedProjectScopedSession(t, ctx, pool, org.ID, projectA.ID)
	seedNativeTool(t, ctx, pool, "memory.lookup", "tier1", "memory")

	resolver, err := NewToolResolver(ToolResolverOptions{
		Pool:   pool,
		Events: bus,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("NewToolResolver: %v", err)
	}

	sub := resolver.SubscribePolicyUpdates(nil)
	defer bus.Unsubscribe(sub)

	if _, err := resolver.GetSessionToolSet(ctx, session.ID, agent.ID); err != nil {
		t.Fatalf("GetSessionToolSet first: %v", err)
	}

	if err := bus.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: org.ID,
		EventType:      "capability.policy.updated",
		ActorType:      "system",
		Payload:        json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("publish policy update event: %v", err)
	}

	waitFor(t, 3*time.Second, func() bool {
		var invalidatedCount int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM session_tool_set sts
			JOIN chat_session s ON s.id = sts.session_id
			WHERE s.organization_id = $1
			  AND sts.invalidated_at IS NOT NULL
		`, org.ID).Scan(&invalidatedCount); err != nil {
			return false
		}
		return invalidatedCount > 0
	})

	if _, err := resolver.GetSessionToolSet(ctx, session.ID, agent.ID); err != nil {
		t.Fatalf("GetSessionToolSet after invalidation: %v", err)
	}

	var totalRows int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM session_tool_set
		WHERE session_id = $1
		  AND agent_id = $2
	`, session.ID, agent.ID).Scan(&totalRows); err != nil {
		t.Fatalf("count total session_tool_set rows: %v", err)
	}
	if totalRows != 2 {
		t.Fatalf("session_tool_set total rows = %d, want 2", totalRows)
	}

	var activeRows int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM session_tool_set
		WHERE session_id = $1
		  AND agent_id = $2
		  AND invalidated_at IS NULL
	`, session.ID, agent.ID).Scan(&activeRows); err != nil {
		t.Fatalf("count active session_tool_set rows: %v", err)
	}
	if activeRows != 1 {
		t.Fatalf("session_tool_set active rows = %d, want 1", activeRows)
	}
}

func TestToolResolverIntegrationMCPScopeFilter(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, projectA, projectB, agent := seedToolResolverBase(t, ctx, pool, nil)
	session := seedProjectScopedSession(t, ctx, pool, org.ID, projectA.ID)

	orgConn := seedMCPConnection(t, ctx, pool, org.ID, nil)
	projectBConn := seedMCPConnection(t, ctx, pool, org.ID, &projectB.ID)
	seedMCPToolCatalogEntry(t, ctx, pool, orgConn.ID, "mcp.github.list_issues")
	seedMCPToolCatalogEntry(t, ctx, pool, projectBConn.ID, "mcp.jira.list_issues")

	resolver, err := NewToolResolver(ToolResolverOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewToolResolver: %v", err)
	}

	got, err := resolver.GetSessionToolSet(ctx, session.ID, agent.ID)
	if err != nil {
		t.Fatalf("GetSessionToolSet: %v", err)
	}
	if !containsTool(got, "mcp.github.list_issues") {
		t.Fatalf("missing org-scoped MCP tool")
	}
	if containsTool(got, "mcp.jira.list_issues") {
		t.Fatalf("unexpected project-B MCP tool in project-A session")
	}
}

func seedToolResolverBase(t *testing.T, ctx context.Context, pool *pgxpool.Pool, denyList []string) (repo.Organization, repo.Project, repo.Project, repo.Agent) {
	t.Helper()
	orgRepo := repo.NewOrgRepo(pool)
	projectRepo := repo.NewProjectRepo(pool)
	agentRepo := repo.NewAgentRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "tools-org-" + uuid.NewString()[:8],
		DisplayName: "Tools Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	projectA, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "project-a-" + uuid.NewString()[:8],
		DisplayName:    "Project A",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project A: %v", err)
	}
	projectB, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "project-b-" + uuid.NewString()[:8],
		DisplayName:    "Project B",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project B: %v", err)
	}

	agent, err := agentRepo.Create(ctx, repo.Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Resolver Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		AgentType:            "general",
		MemoryReadScopes:     []string{},
		ToolAllowList:        []string{},
		ToolDenyList:         denyList,
		OperatorInstructions: "",
		SystemPrompt:         "",
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	return org, projectA, projectB, agent
}

func seedProjectScopedSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID uuid.UUID) repo.ChatSession {
	t.Helper()
	sessionRepo := repo.NewChatSessionRepo(pool)
	session, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      "project",
		ScopeID:        projectID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	return session
}

func seedNativeTool(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name, tier, domain string) repo.ToolDefinition {
	t.Helper()
	toolRepo := repo.NewToolDefinitionRepo(pool)
	created, err := toolRepo.Create(ctx, repo.ToolDefinition{
		Name:        name,
		DisplayName: name,
		Description: name + " description",
		ToolTier:    tier,
		ToolDomain:  domain,
		IsEnabled:   true,
		InputSchema: json.RawMessage(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("create tool %s: %v", name, err)
	}
	return created
}

func seedMCPConnection(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, projectID *uuid.UUID) repo.MCPConnection {
	t.Helper()
	connRepo := repo.NewMCPConnectionRepo(pool)
	conn, err := connRepo.Create(ctx, repo.MCPConnection{
		OrganizationID:  orgID,
		ProjectID:       projectID,
		DisplayName:     "MCP " + uuid.NewString()[:6],
		Slug:            "mcp-" + uuid.NewString()[:6],
		Transport:       "sse",
		TransportConfig: json.RawMessage(`{"url":"https://mcp.example"}`),
		Status:          "active",
		IsEnabled:       true,
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create mcp connection: %v", err)
	}
	return conn
}

func seedMCPToolCatalogEntry(t *testing.T, ctx context.Context, pool *pgxpool.Pool, connectionID uuid.UUID, toolName string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO mcp_tool_catalog (
			connection_id,
			tool_name,
			description,
			input_schema,
			is_enabled,
			metadata
		)
		VALUES ($1, $2, $3, $4::jsonb, true, '{}'::jsonb)
	`, connectionID, toolName, toolName+" description", json.RawMessage(`{"type":"object"}`)); err != nil {
		t.Fatalf("insert mcp_tool_catalog entry: %v", err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("waitFor timeout")
}
