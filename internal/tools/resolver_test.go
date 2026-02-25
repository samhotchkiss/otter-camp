package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/policy"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestStage1UniverseAppliesMCPProjectScope(t *testing.T) {
	orgID := uuid.New()
	projectA := uuid.New()
	projectB := uuid.New()
	sessionID := uuid.New()
	agentID := uuid.New()

	resolver := newResolverFixture()
	resolver.sessions.items[sessionID] = repo.ChatSession{
		ID:             sessionID,
		OrganizationID: orgID,
		ScopeType:      "project",
		ScopeID:        projectA,
	}
	resolver.agents.items[agentID] = repo.Agent{ID: agentID}
	resolver.toolDefs.items = []repo.ToolDefinition{
		{Name: "memory.query", ToolTier: "tier1", ToolDomain: "memory", IsEnabled: true},
		{Name: "file.read", ToolTier: "tier1", ToolDomain: "file", IsEnabled: true},
		{Name: "file.list", ToolTier: "tier1", ToolDomain: "file", IsEnabled: true},
		{Name: "git.status", ToolTier: "tier1", ToolDomain: "git", IsEnabled: true},
		{Name: "task.list", ToolTier: "tier1", ToolDomain: "task", IsEnabled: true},
	}

	orgScopedConn := repo.MCPConnection{ID: uuid.New(), OrganizationID: orgID, ProjectID: nil, IsEnabled: true}
	projectScopedConn := repo.MCPConnection{ID: uuid.New(), OrganizationID: orgID, ProjectID: &projectB, IsEnabled: true}
	resolver.connections.items = []repo.MCPConnection{orgScopedConn, projectScopedConn}
	resolver.catalog.entries[orgScopedConn.ID] = []repo.MCPToolCatalogEntry{
		{ConnectionID: orgScopedConn.ID, ToolName: "mcp.github.create_issue", IsEnabled: true},
	}
	resolver.catalog.entries[projectScopedConn.ID] = []repo.MCPToolCatalogEntry{
		{ConnectionID: projectScopedConn.ID, ToolName: "mcp.jira.create_ticket", IsEnabled: true},
	}

	got, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID)
	if err != nil {
		t.Fatalf("GetSessionToolSet: %v", err)
	}
	if len(got) != 6 {
		t.Fatalf("tool count = %d, want 6", len(got))
	}
	if containsTool(got, "mcp.jira.create_ticket") {
		t.Fatalf("unexpected project-B MCP tool in project-A session")
	}
}

func TestBuildUniverseErrorsAndDomainFallback(t *testing.T) {
	orgID := uuid.New()
	session := repo.ChatSession{
		ID:             uuid.New(),
		OrganizationID: orgID,
		ScopeType:      "organization",
		ScopeID:        orgID,
	}

	t.Run("mcp connection list error", func(t *testing.T) {
		resolver := newResolverFixture()
		resolver.connections.err = errors.New("connections failed")
		_, err := resolver.resolver.buildUniverse(context.Background(), session, nil)
		if err == nil || err.Error() != "connections failed" {
			t.Fatalf("buildUniverse error = %v, want connections failed", err)
		}
	})

	t.Run("catalog error", func(t *testing.T) {
		resolver := newResolverFixture()
		connID := uuid.New()
		resolver.connections.items = []repo.MCPConnection{{ID: connID, OrganizationID: orgID, IsEnabled: true}}
		resolver.catalog.err = errors.New("catalog failed")
		_, err := resolver.resolver.buildUniverse(context.Background(), session, nil)
		if err == nil || err.Error() != "catalog failed" {
			t.Fatalf("buildUniverse error = %v, want catalog failed", err)
		}
	})

	t.Run("tool domain fallback", func(t *testing.T) {
		resolver := newResolverFixture()
		resolver.toolDefs.items = []repo.ToolDefinition{
			{Name: "memory.query", ToolTier: "tier1", ToolDomain: "", IsEnabled: true},
		}
		got, err := resolver.resolver.buildUniverse(context.Background(), session, nil)
		if err != nil {
			t.Fatalf("buildUniverse: %v", err)
		}
		if len(got) != 1 || got[0].Domain != "memory" {
			t.Fatalf("domain fallback failed: got=%+v", got)
		}
	})
}

func TestStage2DenyGlobRemovesTools(t *testing.T) {
	input := []ToolDescriptor{
		{Name: "memory.query"},
		{Name: "mcp.github.create_issue"},
		{Name: "git.commit"},
	}
	got := applyAgentFilters(input, []string{"mcp.*"}, nil)
	if len(got) != 2 {
		t.Fatalf("filtered count = %d, want 2", len(got))
	}
	if containsTool(got, "mcp.github.create_issue") {
		t.Fatalf("mcp tool was not removed by deny glob")
	}
}

func TestStage2AllowGlobKeepsOnlyMatchedTools(t *testing.T) {
	input := []ToolDescriptor{
		{Name: "memory.query"},
		{Name: "git.status"},
		{Name: "mcp.github.list_issues"},
	}
	got := applyAgentFilters(input, nil, []string{"memory.*", "git.*"})
	if len(got) != 2 {
		t.Fatalf("filtered count = %d, want 2", len(got))
	}
	if !containsTool(got, "memory.query") || !containsTool(got, "git.status") {
		t.Fatalf("allow filter dropped expected tools: %+v", got)
	}
	if containsTool(got, "mcp.github.list_issues") {
		t.Fatalf("allow filter retained unexpected tool")
	}
}

func TestMatchToolGlobRules(t *testing.T) {
	if !matchToolGlob("file.*", "file.read") {
		t.Fatal("expected file.* to match file.read")
	}
	if !matchToolGlob("file.*", "file.write") {
		t.Fatal("expected file.* to match file.write")
	}
	if matchToolGlob("file.*", "memory.query") {
		t.Fatal("did not expect file.* to match memory.query")
	}
	if !matchToolGlob("file.**", "file.read") {
		t.Fatal("expected file.** to match file.read")
	}
	if !matchToolGlob("file.**", "file.subfolder.tool") {
		t.Fatal("expected file.** to match file.subfolder.tool")
	}
	if !matchToolGlob("**.create", "mcp.github.create") {
		t.Fatal("expected **.create to match mcp.github.create")
	}
	if !matchToolGlob("mcp.**.tool", "mcp.tool") {
		t.Fatal("expected mcp.**.tool to match mcp.tool with empty ** segment")
	}
	if !matchToolGlob("mcp.*.create", "mcp.github.create") {
		t.Fatal("expected mcp.*.create to match mcp.github.create")
	}
	if matchToolGlob("mcp.*.create", "mcp.github.issue.create") {
		t.Fatal("did not expect mcp.*.create to match multi-segment intermediate name")
	}
}

func TestStage3NoOpWhenFlowNodeDomainsEmpty(t *testing.T) {
	taskID := uuid.New()
	nodeID := uuid.New()
	session := repo.ChatSession{ScopeType: "project_task", ScopeID: taskID}

	resolver := newResolverFixture()
	resolver.tasks.items[taskID] = repo.ProjectTask{
		ID:                taskID,
		CurrentFlowNodeID: &nodeID,
	}
	resolver.flowExecutions.active[taskID.String()+"|"+nodeID.String()] = repo.FlowNodeExecution{TaskID: taskID, FlowNodeID: nodeID}
	resolver.flowNodes.items[nodeID] = repo.FlowNode{
		ID:          nodeID,
		ToolDomains: nil,
	}

	input := []ToolDescriptor{
		{Name: "memory.query", Domain: "memory"},
		{Name: "file.read", Domain: "file"},
	}

	got, err := resolver.resolver.applyFlowNodeOrdering(context.Background(), session, input)
	if err != nil {
		t.Fatalf("applyFlowNodeOrdering: %v", err)
	}
	if got[0].Name != input[0].Name || got[1].Name != input[1].Name {
		t.Fatalf("stage3 changed ordering during no-op: got=%v", got)
	}
}

func TestStage3DeprioritizesOutOfDomainTools(t *testing.T) {
	taskID := uuid.New()
	nodeID := uuid.New()
	session := repo.ChatSession{ScopeType: "project_task", ScopeID: taskID}

	resolver := newResolverFixture()
	resolver.tasks.items[taskID] = repo.ProjectTask{
		ID:                taskID,
		CurrentFlowNodeID: &nodeID,
	}
	resolver.flowExecutions.active[taskID.String()+"|"+nodeID.String()] = repo.FlowNodeExecution{TaskID: taskID, FlowNodeID: nodeID}
	resolver.flowNodes.items[nodeID] = repo.FlowNode{
		ID:          nodeID,
		ToolDomains: []string{"memory"},
	}

	input := []ToolDescriptor{
		{Name: "file.read", Domain: "file"},
		{Name: "memory.query", Domain: "memory"},
		{Name: "git.status", Domain: "git"},
	}
	got, err := resolver.resolver.applyFlowNodeOrdering(context.Background(), session, input)
	if err != nil {
		t.Fatalf("applyFlowNodeOrdering: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("tool count = %d, want 3", len(got))
	}
	if got[0].Name != "memory.query" {
		t.Fatalf("first tool = %q, want memory.query", got[0].Name)
	}
	if !containsTool(got, "file.read") || !containsTool(got, "git.status") {
		t.Fatalf("deprioritized tools removed: got=%v", got)
	}
}

func TestStage4Tier1BypassAndTier2Exclusion(t *testing.T) {
	orgID := uuid.New()
	agentID := uuid.New()
	capability := "git.write"
	sessionProjectID := uuid.New()

	resolver := newResolverFixture()
	resolver.policies.decisions[capability] = policy.PolicyDecision{Effect: "deny", Layer: "org", Reason: "test deny"}

	stage4, err := resolver.resolver.applyCapabilityGate(context.Background(), orgID, &sessionProjectID, agentID, []ToolDescriptor{
		{Name: "git.status", Tier: "tier1", Capability: &capability},
		{Name: "git.push", Tier: "tier2", Capability: &capability},
	})
	if err != nil {
		t.Fatalf("applyCapabilityGate: %v", err)
	}
	if !containsTool(stage4, "git.status") {
		t.Fatalf("tier1 tool should be retained on deny")
	}
	if containsTool(stage4, "git.push") {
		t.Fatalf("tier2 tool should be excluded on deny")
	}
}

func TestGetSessionToolSetCacheHitSkipsPipelineRebuild(t *testing.T) {
	orgID := uuid.New()
	sessionID := uuid.New()
	agentID := uuid.New()

	resolver := newResolverFixture()
	resolver.sessions.items[sessionID] = repo.ChatSession{
		ID:             sessionID,
		OrganizationID: orgID,
		ScopeType:      "organization",
		ScopeID:        orgID,
	}
	resolver.agents.items[agentID] = repo.Agent{ID: agentID}
	resolver.toolDefs.items = []repo.ToolDefinition{
		{Name: "memory.query", ToolTier: "tier1", ToolDomain: "memory", IsEnabled: true},
	}

	first, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID)
	if err != nil {
		t.Fatalf("GetSessionToolSet first: %v", err)
	}
	second, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID)
	if err != nil {
		t.Fatalf("GetSessionToolSet second: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("cache hit returned different length: first=%d second=%d", len(first), len(second))
	}
	if resolver.sessionSets.createCalls != 1 {
		t.Fatalf("Create calls = %d, want 1", resolver.sessionSets.createCalls)
	}
}

func TestGetSessionToolSetAfterInvalidateRebuildsCache(t *testing.T) {
	orgID := uuid.New()
	sessionID := uuid.New()
	agentID := uuid.New()

	resolver := newResolverFixture()
	resolver.sessions.items[sessionID] = repo.ChatSession{
		ID:             sessionID,
		OrganizationID: orgID,
		ScopeType:      "organization",
		ScopeID:        orgID,
	}
	resolver.agents.items[agentID] = repo.Agent{ID: agentID}
	resolver.toolDefs.items = []repo.ToolDefinition{
		{Name: "memory.query", ToolTier: "tier1", ToolDomain: "memory", IsEnabled: true},
	}

	if _, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID); err != nil {
		t.Fatalf("GetSessionToolSet first: %v", err)
	}
	if err := resolver.sessionSets.Invalidate(context.Background(), sessionID, agentID); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID); err != nil {
		t.Fatalf("GetSessionToolSet second: %v", err)
	}
	if resolver.sessionSets.createCalls != 2 {
		t.Fatalf("Create calls = %d, want 2", resolver.sessionSets.createCalls)
	}
}

func TestGetSessionToolSetPropagatesActiveCacheError(t *testing.T) {
	sessionID := uuid.New()
	agentID := uuid.New()
	orgID := uuid.New()

	resolver := newResolverFixture()
	resolver.sessionSets.getActiveErr = errors.New("cache unavailable")
	resolver.sessions.items[sessionID] = repo.ChatSession{
		ID:             sessionID,
		OrganizationID: orgID,
		ScopeType:      "organization",
		ScopeID:        orgID,
	}
	resolver.agents.items[agentID] = repo.Agent{ID: agentID}

	_, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID)
	if err == nil || err.Error() != "cache unavailable" {
		t.Fatalf("GetSessionToolSet error = %v, want propagated cache error", err)
	}
}

func TestHandlePolicyUpdateInvalidatesOrgCache(t *testing.T) {
	orgID := uuid.New()
	sessionID := uuid.New()
	agentID := uuid.New()

	resolver := newResolverFixture()
	set, err := resolver.sessionSets.Create(context.Background(), sessionID, agentID, json.RawMessage(`[]`))
	if err != nil {
		t.Fatalf("Create tool set: %v", err)
	}
	resolver.sessionSets.activeByOrg[orgID] = []repo.SessionToolSet{*set}

	if err := resolver.resolver.HandlePolicyUpdate(context.Background(), orgID); err != nil {
		t.Fatalf("HandlePolicyUpdate: %v", err)
	}
	if _, err := resolver.sessionSets.GetActive(context.Background(), sessionID, agentID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("GetActive after invalidate err = %v, want ErrNotFound", err)
	}
}

func TestSubscribePolicyUpdatesHandlesOrgFallback(t *testing.T) {
	orgID := uuid.New()
	sessionID := uuid.New()
	agentID := uuid.New()

	resolver := newResolverFixture()
	set, err := resolver.sessionSets.Create(context.Background(), sessionID, agentID, json.RawMessage(`[]`))
	if err != nil {
		t.Fatalf("Create tool set: %v", err)
	}
	resolver.sessionSets.activeByOrg[orgID] = []repo.SessionToolSet{*set}

	subscriber := &fakeEventSubscriber{}
	resolver.resolver.events = subscriber

	_ = resolver.resolver.SubscribePolicyUpdates(&orgID)
	if subscriber.handler == nil {
		t.Fatal("expected subscription handler")
	}

	// ignored event type
	if err := subscriber.handler(context.Background(), eventbus.DomainEvent{
		EventType:      "other.event",
		OrganizationID: orgID,
	}); err != nil {
		t.Fatalf("handler other.event error: %v", err)
	}
	if _, err := resolver.sessionSets.GetActive(context.Background(), sessionID, agentID); err != nil {
		t.Fatalf("GetActive after ignored event: %v", err)
	}

	// policy update with nil event org falls back to subscription org id
	if err := subscriber.handler(context.Background(), eventbus.DomainEvent{
		EventType:      "capability.policy.updated",
		OrganizationID: uuid.Nil,
	}); err != nil {
		t.Fatalf("handler policy update error: %v", err)
	}
	if _, err := resolver.sessionSets.GetActive(context.Background(), sessionID, agentID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("GetActive after policy update err = %v, want ErrNotFound", err)
	}
}

func TestResolveSessionProjectIDProjectTaskScope(t *testing.T) {
	taskID := uuid.New()
	projectID := uuid.New()
	resolver := newResolverFixture()
	resolver.tasks.items[taskID] = repo.ProjectTask{
		ID:        taskID,
		ProjectID: projectID,
	}

	got, err := resolver.resolver.resolveSessionProjectID(context.Background(), repo.ChatSession{
		ScopeType: "project_task",
		ScopeID:   taskID,
	})
	if err != nil {
		t.Fatalf("resolveSessionProjectID: %v", err)
	}
	if got == nil || *got != projectID {
		t.Fatalf("project id = %v, want %s", got, projectID)
	}
}

func TestStage3MCPToolsMovedToFront(t *testing.T) {
	taskID := uuid.New()
	nodeID := uuid.New()
	connID := uuid.New()
	session := repo.ChatSession{ScopeType: "project_task", ScopeID: taskID}

	resolver := newResolverFixture()
	resolver.tasks.items[taskID] = repo.ProjectTask{
		ID:                taskID,
		CurrentFlowNodeID: &nodeID,
	}
	resolver.flowExecutions.active[taskID.String()+"|"+nodeID.String()] = repo.FlowNodeExecution{TaskID: taskID, FlowNodeID: nodeID}
	resolver.flowNodes.items[nodeID] = repo.FlowNode{
		ID: nodeID,
		MCPTools: []repo.FlowNodeMCPTool{
			{ConnectionID: connID, ToolName: "mcp.github.create_issue"},
		},
	}

	input := []ToolDescriptor{
		{Name: "native.a", Source: "native", Domain: "native"},
		{Name: "mcp.github.create_issue", Source: "mcp", Domain: "mcp", MCPConnectionID: &connID},
		{Name: "native.b", Source: "native", Domain: "native"},
	}
	got, err := resolver.resolver.applyFlowNodeOrdering(context.Background(), session, input)
	if err != nil {
		t.Fatalf("applyFlowNodeOrdering: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("tool count = %d, want 3", len(got))
	}
	if got[0].Name != "mcp.github.create_issue" {
		t.Fatalf("first tool = %q, want mcp.github.create_issue", got[0].Name)
	}
	if got[1].Name != "native.a" || got[2].Name != "native.b" {
		t.Fatalf("native relative order changed: got=%v", []string{got[1].Name, got[2].Name})
	}
}

func TestPriorityAndCapabilityHelpers(t *testing.T) {
	connID := uuid.New()
	if key := mcpPriorityKey(connID, " mcp.github.create_issue "); key != connID.String()+"|mcp.github.create_issue" {
		t.Fatalf("mcpPriorityKey = %q", key)
	}

	prioritized := map[string]struct{}{
		mcpPriorityKey(connID, "mcp.github.create_issue"): {},
	}
	if !isPrioritizedMCPTool(ToolDescriptor{
		Name:            "mcp.github.create_issue",
		Source:          "mcp",
		MCPConnectionID: &connID,
	}, prioritized) {
		t.Fatal("expected prioritized mcp tool")
	}
	if isPrioritizedMCPTool(ToolDescriptor{
		Name:            "mcp.github.create_issue",
		Source:          "native",
		MCPConnectionID: &connID,
	}, prioritized) {
		t.Fatal("did not expect non-mcp tool to be prioritized")
	}

	if trimCapability(nil) != nil {
		t.Fatal("trimCapability(nil) should be nil")
	}
	blank := "   "
	if trimCapability(&blank) != nil {
		t.Fatal("trimCapability(blank) should be nil")
	}
	value := " system.file.read "
	trimmed := trimCapability(&value)
	if trimmed == nil || *trimmed != "system.file.read" {
		t.Fatalf("trimCapability = %v, want system.file.read", trimmed)
	}
}

func TestToolDomainFromName(t *testing.T) {
	if got := toolDomainFromName("memory.query"); got != "memory" {
		t.Fatalf("toolDomainFromName(memory.query) = %q, want memory", got)
	}
	if got := toolDomainFromName("single"); got != "single" {
		t.Fatalf("toolDomainFromName(single) = %q, want single", got)
	}
	if got := toolDomainFromName("   "); got != "" {
		t.Fatalf("toolDomainFromName(blank) = %q, want empty", got)
	}
}

func TestDecodeToolSetRoundTripAndInvalidInput(t *testing.T) {
	descriptors, err := decodeToolSet(nil)
	if err != nil {
		t.Fatalf("decodeToolSet(nil): %v", err)
	}
	if len(descriptors) != 0 {
		t.Fatalf("decodeToolSet(nil) len = %d, want 0", len(descriptors))
	}

	if _, err := decodeToolSet(json.RawMessage(`{"invalid":"shape"}`)); err == nil {
		t.Fatal("expected decodeToolSet invalid shape error")
	}

	encoded := json.RawMessage(`[{"name":"memory.query","tier":"tier1","domain":"memory","input_schema":{"type":"object"}}]`)
	decoded, err := decodeToolSet(encoded)
	if err != nil {
		t.Fatalf("decodeToolSet(round-trip): %v", err)
	}
	if len(decoded) != 1 || decoded[0].Name != "memory.query" {
		t.Fatalf("decoded = %+v, want one memory.query descriptor", decoded)
	}
}

func TestConnectionVisibleInSessionMatrix(t *testing.T) {
	projectA := uuid.New()
	projectB := uuid.New()
	tests := []struct {
		name       string
		connection *uuid.UUID
		session    *uuid.UUID
		want       bool
	}{
		{name: "nil connection and nil session", connection: nil, session: nil, want: true},
		{name: "connection project but nil session", connection: &projectA, session: nil, want: false},
		{name: "mismatched projects", connection: &projectA, session: &projectB, want: false},
		{name: "matched projects", connection: &projectA, session: &projectA, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := connectionVisibleInSession(tc.connection, tc.session); got != tc.want {
				t.Fatalf("connectionVisibleInSession = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewToolResolverRequiresPoolWhenRepositoriesMissing(t *testing.T) {
	if _, err := NewToolResolver(ToolResolverOptions{}); err == nil {
		t.Fatal("expected error when pool and repositories are missing")
	}
}

func TestNewToolResolverWiresDefaultReposFromPool(t *testing.T) {
	resolver, err := NewToolResolver(ToolResolverOptions{
		Pool: &pgxpool.Pool{},
	})
	if err != nil {
		t.Fatalf("NewToolResolver: %v", err)
	}
	if resolver.sessionToolSets == nil || resolver.sessions == nil || resolver.agents == nil ||
		resolver.tasks == nil || resolver.flowExecutions == nil || resolver.flowNodes == nil ||
		resolver.toolDefinitions == nil || resolver.mcpConnections == nil || resolver.mcpCatalog == nil {
		t.Fatal("expected default repositories to be wired")
	}
}

func TestGetSessionToolSetCachedDecodeError(t *testing.T) {
	sessionID := uuid.New()
	agentID := uuid.New()
	resolver := newResolverFixture()
	resolver.sessionSets.active[sessionID.String()+"|"+agentID.String()] = &repo.SessionToolSet{
		SessionID: sessionID,
		AgentID:   agentID,
		ToolSet:   json.RawMessage(`{"invalid":"shape"}`),
	}

	_, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID)
	if err == nil {
		t.Fatal("expected decode error from cached invalid tool_set")
	}
}

func TestGetSessionToolSetCacheMissErrorPaths(t *testing.T) {
	sessionID := uuid.New()
	agentID := uuid.New()
	orgID := uuid.New()

	t.Run("session lookup failure", func(t *testing.T) {
		resolver := newResolverFixture()
		resolver.sessions.err = errors.New("session lookup failed")
		_, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID)
		if err == nil || err.Error() != "session lookup failed" {
			t.Fatalf("GetSessionToolSet error = %v, want session lookup failed", err)
		}
	})

	t.Run("create failure", func(t *testing.T) {
		resolver := newResolverFixture()
		resolver.sessions.items[sessionID] = repo.ChatSession{
			ID:             sessionID,
			OrganizationID: orgID,
			ScopeType:      "organization",
			ScopeID:        orgID,
		}
		resolver.agents.items[agentID] = repo.Agent{ID: agentID}
		resolver.toolDefs.items = []repo.ToolDefinition{{Name: "memory.query", ToolTier: "tier1", ToolDomain: "memory", IsEnabled: true}}
		resolver.sessionSets.createErr = errors.New("create failed")

		_, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID)
		if err == nil || err.Error() != "create failed" {
			t.Fatalf("GetSessionToolSet error = %v, want create failed", err)
		}
	})

	t.Run("created row decode failure", func(t *testing.T) {
		resolver := newResolverFixture()
		resolver.sessions.items[sessionID] = repo.ChatSession{
			ID:             sessionID,
			OrganizationID: orgID,
			ScopeType:      "organization",
			ScopeID:        orgID,
		}
		resolver.agents.items[agentID] = repo.Agent{ID: agentID}
		resolver.toolDefs.items = []repo.ToolDefinition{{Name: "memory.query", ToolTier: "tier1", ToolDomain: "memory", IsEnabled: true}}
		resolver.sessionSets.createToolSet = json.RawMessage(`{"invalid":"shape"}`)

		_, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID)
		if err == nil {
			t.Fatal("expected decode error from created row tool_set")
		}
	})
}

func TestHandlePolicyUpdateValidationAndRepositoryErrors(t *testing.T) {
	resolver := newResolverFixture()
	if err := resolver.resolver.HandlePolicyUpdate(context.Background(), uuid.Nil); err == nil {
		t.Fatal("expected validation error for nil org id")
	}

	resolver.sessionSets.listActiveErr = errors.New("list failed")
	if err := resolver.resolver.HandlePolicyUpdate(context.Background(), uuid.New()); err == nil || err.Error() != "list failed" {
		t.Fatalf("HandlePolicyUpdate list error = %v, want list failed", err)
	}

	orgID := uuid.New()
	sessionID := uuid.New()
	agentID := uuid.New()
	set, err := resolver.sessionSets.Create(context.Background(), sessionID, agentID, json.RawMessage(`[]`))
	if err != nil {
		t.Fatalf("Create tool set: %v", err)
	}
	resolver.sessionSets.listActiveErr = nil
	resolver.sessionSets.activeByOrg[orgID] = []repo.SessionToolSet{*set}
	resolver.sessionSets.invalidateErr = errors.New("invalidate failed")
	if err := resolver.resolver.HandlePolicyUpdate(context.Background(), orgID); err == nil || err.Error() != "invalidate failed" {
		t.Fatalf("HandlePolicyUpdate invalidate error = %v, want invalidate failed", err)
	}
}

func TestSubscribePolicyUpdatesNoSubscriber(t *testing.T) {
	resolver := newResolverFixture()
	resolver.resolver.events = nil
	sub := resolver.resolver.SubscribePolicyUpdates(nil)
	if sub != (eventbus.Subscription{}) {
		t.Fatalf("subscription = %#v, want zero value", sub)
	}
}

func TestSubscribePolicyUpdatesNilOrgWithoutFallback(t *testing.T) {
	resolver := newResolverFixture()
	subscriber := &fakeEventSubscriber{}
	resolver.resolver.events = subscriber
	_ = resolver.resolver.SubscribePolicyUpdates(nil)
	if subscriber.handler == nil {
		t.Fatal("expected subscription handler")
	}

	if err := subscriber.handler(context.Background(), eventbus.DomainEvent{
		EventType:      "capability.policy.updated",
		OrganizationID: uuid.Nil,
	}); err != nil {
		t.Fatalf("handler nil-org update: %v", err)
	}
}

func TestResolveToolSetErrorPaths(t *testing.T) {
	orgID := uuid.New()
	session := repo.ChatSession{
		ID:             uuid.New(),
		OrganizationID: orgID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
	}
	agentID := uuid.New()

	t.Run("task lookup error", func(t *testing.T) {
		resolver := newResolverFixture()
		resolver.tasks.err = errors.New("task lookup failed")
		_, err := resolver.resolver.resolveToolSet(context.Background(), session, agentID)
		if err == nil || err.Error() != "task lookup failed" {
			t.Fatalf("resolveToolSet error = %v, want task lookup failed", err)
		}
	})

	t.Run("tool definition error", func(t *testing.T) {
		resolver := newResolverFixture()
		resolver.tasks.items[session.ScopeID] = repo.ProjectTask{ID: session.ScopeID, ProjectID: uuid.New()}
		resolver.toolDefs.err = errors.New("tool defs failed")
		_, err := resolver.resolver.resolveToolSet(context.Background(), session, agentID)
		if err == nil || err.Error() != "tool defs failed" {
			t.Fatalf("resolveToolSet error = %v, want tool defs failed", err)
		}
	})

	t.Run("agent lookup error", func(t *testing.T) {
		resolver := newResolverFixture()
		resolver.tasks.items[session.ScopeID] = repo.ProjectTask{ID: session.ScopeID, ProjectID: uuid.New()}
		resolver.toolDefs.items = []repo.ToolDefinition{{Name: "memory.query", ToolTier: "tier1", ToolDomain: "memory", IsEnabled: true}}
		resolver.agents.err = errors.New("agent lookup failed")
		_, err := resolver.resolver.resolveToolSet(context.Background(), session, agentID)
		if err == nil || err.Error() != "agent lookup failed" {
			t.Fatalf("resolveToolSet error = %v, want agent lookup failed", err)
		}
	})

	t.Run("flow ordering error", func(t *testing.T) {
		resolver := newResolverFixture()
		nodeID := uuid.New()
		resolver.tasks.items[session.ScopeID] = repo.ProjectTask{ID: session.ScopeID, ProjectID: uuid.New(), CurrentFlowNodeID: &nodeID}
		resolver.toolDefs.items = []repo.ToolDefinition{{Name: "memory.query", ToolTier: "tier1", ToolDomain: "memory", IsEnabled: true}}
		resolver.agents.items[agentID] = repo.Agent{ID: agentID}
		resolver.flowExecutions.err = errors.New("flow execution failed")
		_, err := resolver.resolver.resolveToolSet(context.Background(), session, agentID)
		if err == nil || err.Error() != "flow execution failed" {
			t.Fatalf("resolveToolSet error = %v, want flow execution failed", err)
		}
	})
}

func TestApplyFlowNodeOrderingBranchCoverage(t *testing.T) {
	resolver := newResolverFixture()

	tools := []ToolDescriptor{{Name: "memory.query", Domain: "memory"}}
	out, err := resolver.resolver.applyFlowNodeOrdering(context.Background(), repo.ChatSession{ScopeType: "organization"}, tools)
	if err != nil {
		t.Fatalf("non-task scope error: %v", err)
	}
	if len(out) != 1 || out[0].Name != "memory.query" {
		t.Fatalf("unexpected non-task scope output: %+v", out)
	}

	taskID := uuid.New()
	session := repo.ChatSession{ScopeType: "project_task", ScopeID: taskID}
	resolver.tasks.items[taskID] = repo.ProjectTask{ID: taskID, CurrentFlowNodeID: nil}
	out, err = resolver.resolver.applyFlowNodeOrdering(context.Background(), session, tools)
	if err != nil {
		t.Fatalf("nil flow node branch error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected unchanged output for nil flow node")
	}

	nodeID := uuid.New()
	resolver.tasks.items[taskID] = repo.ProjectTask{ID: taskID, CurrentFlowNodeID: &nodeID}
	resolver.flowExecutions.active = map[string]repo.FlowNodeExecution{}
	out, err = resolver.resolver.applyFlowNodeOrdering(context.Background(), session, tools)
	if err != nil {
		t.Fatalf("flow execution not found should be no-op, err=%v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected unchanged output for missing flow execution")
	}

	resolver.flowExecutions.err = errors.New("flow execution failed")
	if _, err := resolver.resolver.applyFlowNodeOrdering(context.Background(), session, tools); err == nil || err.Error() != "flow execution failed" {
		t.Fatalf("flow execution error = %v, want flow execution failed", err)
	}

	resolver.flowExecutions.err = nil
	resolver.flowExecutions.active[taskID.String()+"|"+nodeID.String()] = repo.FlowNodeExecution{TaskID: taskID, FlowNodeID: nodeID}
	resolver.flowNodes.err = errors.New("flow node failed")
	if _, err := resolver.resolver.applyFlowNodeOrdering(context.Background(), session, tools); err == nil || err.Error() != "flow node failed" {
		t.Fatalf("flow node error = %v, want flow node failed", err)
	}
}

type resolverFixture struct {
	resolver       *ToolResolver
	sessionSets    *fakeSessionToolSetRepo
	sessions       *fakeChatSessionRepo
	agents         *fakeAgentRepo
	tasks          *fakeTaskRepo
	flowExecutions *fakeFlowExecutionRepo
	flowNodes      *fakeFlowNodeRepo
	toolDefs       *fakeToolDefinitionRepo
	connections    *fakeMCPConnectionRepo
	catalog        *fakeMCPCatalogRepo
	policies       *fakePolicyEvaluator
}

func newResolverFixture() resolverFixture {
	sessionSets := newFakeSessionToolSetRepo()
	sessions := &fakeChatSessionRepo{items: make(map[uuid.UUID]repo.ChatSession)}
	agents := &fakeAgentRepo{items: make(map[uuid.UUID]repo.Agent)}
	tasks := &fakeTaskRepo{items: make(map[uuid.UUID]repo.ProjectTask)}
	flowExecutions := &fakeFlowExecutionRepo{active: make(map[string]repo.FlowNodeExecution)}
	flowNodes := &fakeFlowNodeRepo{items: make(map[uuid.UUID]repo.FlowNode)}
	toolDefs := &fakeToolDefinitionRepo{}
	connections := &fakeMCPConnectionRepo{}
	catalog := &fakeMCPCatalogRepo{entries: make(map[uuid.UUID][]repo.MCPToolCatalogEntry)}
	policies := &fakePolicyEvaluator{decisions: make(map[string]policy.PolicyDecision)}

	resolver, err := NewToolResolver(ToolResolverOptions{
		SessionToolSets: sessionSets,
		Sessions:        sessions,
		Agents:          agents,
		Tasks:           tasks,
		FlowExecutions:  flowExecutions,
		FlowNodes:       flowNodes,
		ToolDefinitions: toolDefs,
		MCPConnections:  connections,
		MCPCatalog:      catalog,
		Policies:        policies,
	})
	if err != nil {
		panic(err)
	}
	return resolverFixture{
		resolver:       resolver,
		sessionSets:    sessionSets,
		sessions:       sessions,
		agents:         agents,
		tasks:          tasks,
		flowExecutions: flowExecutions,
		flowNodes:      flowNodes,
		toolDefs:       toolDefs,
		connections:    connections,
		catalog:        catalog,
		policies:       policies,
	}
}

type fakeSessionToolSetRepo struct {
	items         []*repo.SessionToolSet
	active        map[string]*repo.SessionToolSet
	activeByOrg   map[uuid.UUID][]repo.SessionToolSet
	createCalls   int
	getActiveErr  error
	invalidateErr error
	listActiveErr error
	createErr     error
	createToolSet json.RawMessage
}

func newFakeSessionToolSetRepo() *fakeSessionToolSetRepo {
	return &fakeSessionToolSetRepo{
		items:       make([]*repo.SessionToolSet, 0),
		active:      make(map[string]*repo.SessionToolSet),
		activeByOrg: make(map[uuid.UUID][]repo.SessionToolSet),
	}
}

func (f *fakeSessionToolSetRepo) Create(_ context.Context, sessionID, agentID uuid.UUID, toolSet json.RawMessage) (*repo.SessionToolSet, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.createCalls++
	now := time.Now().UTC()
	if len(f.createToolSet) > 0 {
		toolSet = f.createToolSet
	}
	item := &repo.SessionToolSet{
		ID:         uuid.New(),
		SessionID:  sessionID,
		AgentID:    agentID,
		ResolvedAt: now,
		ToolSet:    toolSet,
	}
	key := sessionID.String() + "|" + agentID.String()
	f.active[key] = item
	f.items = append(f.items, item)
	return item, nil
}

func (f *fakeSessionToolSetRepo) GetActive(_ context.Context, sessionID, agentID uuid.UUID) (*repo.SessionToolSet, error) {
	if f.getActiveErr != nil {
		return nil, f.getActiveErr
	}
	key := sessionID.String() + "|" + agentID.String()
	item, ok := f.active[key]
	if !ok || item.InvalidatedAt != nil {
		return nil, repo.ErrNotFound
	}
	return item, nil
}

func (f *fakeSessionToolSetRepo) Invalidate(_ context.Context, sessionID, agentID uuid.UUID) error {
	if f.invalidateErr != nil {
		return f.invalidateErr
	}
	key := sessionID.String() + "|" + agentID.String()
	item, ok := f.active[key]
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	item.InvalidatedAt = &now
	delete(f.active, key)
	return nil
}

func (f *fakeSessionToolSetRepo) ReplaceToolSet(ctx context.Context, sessionID, agentID uuid.UUID, newToolSet json.RawMessage) (*repo.SessionToolSet, error) {
	if err := f.Invalidate(ctx, sessionID, agentID); err != nil {
		return nil, err
	}
	return f.Create(ctx, sessionID, agentID, newToolSet)
}

func (f *fakeSessionToolSetRepo) ListActiveByOrganization(_ context.Context, organizationID uuid.UUID) ([]repo.SessionToolSet, error) {
	if f.listActiveErr != nil {
		return nil, f.listActiveErr
	}
	items := f.activeByOrg[organizationID]
	out := make([]repo.SessionToolSet, len(items))
	copy(out, items)
	return out, nil
}

type fakeChatSessionRepo struct {
	items map[uuid.UUID]repo.ChatSession
	err   error
}

func (f *fakeChatSessionRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
	if f.err != nil {
		return repo.ChatSession{}, f.err
	}
	item, ok := f.items[id]
	if !ok {
		return repo.ChatSession{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeAgentRepo struct {
	items map[uuid.UUID]repo.Agent
	err   error
}

func (f *fakeAgentRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Agent, error) {
	if f.err != nil {
		return repo.Agent{}, f.err
	}
	item, ok := f.items[id]
	if !ok {
		return repo.Agent{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeTaskRepo struct {
	items map[uuid.UUID]repo.ProjectTask
	err   error
}

func (f *fakeTaskRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectTask, error) {
	if f.err != nil {
		return repo.ProjectTask{}, f.err
	}
	item, ok := f.items[id]
	if !ok {
		return repo.ProjectTask{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeFlowExecutionRepo struct {
	active map[string]repo.FlowNodeExecution
	err    error
}

func (f *fakeFlowExecutionRepo) GetActive(_ context.Context, taskID, flowNodeID uuid.UUID) (repo.FlowNodeExecution, error) {
	if f.err != nil {
		return repo.FlowNodeExecution{}, f.err
	}
	key := taskID.String() + "|" + flowNodeID.String()
	item, ok := f.active[key]
	if !ok {
		return repo.FlowNodeExecution{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeFlowNodeRepo struct {
	items map[uuid.UUID]repo.FlowNode
	err   error
}

func (f *fakeFlowNodeRepo) GetByID(_ context.Context, id uuid.UUID) (repo.FlowNode, error) {
	if f.err != nil {
		return repo.FlowNode{}, f.err
	}
	item, ok := f.items[id]
	if !ok {
		return repo.FlowNode{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeToolDefinitionRepo struct {
	items []repo.ToolDefinition
	err   error
}

func (f *fakeToolDefinitionRepo) ListEnabled(context.Context) ([]repo.ToolDefinition, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

type fakeMCPConnectionRepo struct {
	items []repo.MCPConnection
	err   error
}

func (f *fakeMCPConnectionRepo) ListEnabled(context.Context) ([]repo.MCPConnection, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

type fakeMCPCatalogRepo struct {
	entries map[uuid.UUID][]repo.MCPToolCatalogEntry
	err     error
}

func (f *fakeMCPCatalogRepo) GetEnabled(_ context.Context, connectionID uuid.UUID) ([]repo.MCPToolCatalogEntry, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.entries[connectionID], nil
}

type fakePolicyEvaluator struct {
	decisions map[string]policy.PolicyDecision
}

func (f *fakePolicyEvaluator) Evaluate(_ context.Context, req policy.EvaluationRequest) policy.PolicyDecision {
	capability := req.Capability
	if decision, ok := f.decisions[capability]; ok {
		return decision
	}
	return policy.PolicyDecision{Effect: "allow", Layer: "none", Reason: "silence passes"}
}

type fakeEventSubscriber struct {
	handler eventbus.EventHandler
}

func (f *fakeEventSubscriber) Subscribe(consumerName string, orgID *uuid.UUID, handler eventbus.EventHandler) eventbus.Subscription {
	f.handler = handler
	return eventbus.Subscription{}
}

func containsTool(items []ToolDescriptor, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}

var _ sessionToolSetRepository = (*fakeSessionToolSetRepo)(nil)
var _ chatSessionRepository = (*fakeChatSessionRepo)(nil)
var _ agentRepository = (*fakeAgentRepo)(nil)
var _ projectTaskRepository = (*fakeTaskRepo)(nil)
var _ flowNodeExecutionRepository = (*fakeFlowExecutionRepo)(nil)
var _ flowNodeRepository = (*fakeFlowNodeRepo)(nil)
var _ toolDefinitionRepository = (*fakeToolDefinitionRepo)(nil)
var _ mcpConnectionRepository = (*fakeMCPConnectionRepo)(nil)
var _ mcpToolCatalogRepository = (*fakeMCPCatalogRepo)(nil)
var _ policyEvaluator = (*fakePolicyEvaluator)(nil)
var _ eventSubscriber = (*fakeEventSubscriber)(nil)
