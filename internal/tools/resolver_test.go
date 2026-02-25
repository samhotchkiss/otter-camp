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

func TestNewToolResolverRequiresPoolWhenDependenciesMissing(t *testing.T) {
	if _, err := NewToolResolver(ToolResolverOptions{}); err == nil {
		t.Fatal("expected error when pool is missing and default repositories are requested")
	}
}

func TestNewToolResolverAllowsPoolBackedDefaults(t *testing.T) {
	resolver, err := NewToolResolver(ToolResolverOptions{Pool: &pgxpool.Pool{}})
	if err != nil {
		t.Fatalf("NewToolResolver with pool: %v", err)
	}
	if resolver == nil {
		t.Fatal("resolver is nil")
	}
}

func TestGetSessionToolSetCacheLookupError(t *testing.T) {
	sessionID := uuid.New()
	agentID := uuid.New()
	resolver := newResolverFixture()
	resolver.sessionSets.getErr = errors.New("cache lookup failed")

	_, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID)
	if err == nil || err.Error() != "cache lookup failed" {
		t.Fatalf("GetSessionToolSet error = %v, want cache lookup failed", err)
	}
}

func TestGetSessionToolSetRejectsInvalidCachedJSON(t *testing.T) {
	sessionID := uuid.New()
	agentID := uuid.New()
	resolver := newResolverFixture()
	resolver.sessionSets.active[sessionID.String()+"|"+agentID.String()] = &repo.SessionToolSet{
		ID:        uuid.New(),
		SessionID: sessionID,
		AgentID:   agentID,
		ToolSet:   json.RawMessage(`{"invalid":"shape"}`),
	}

	_, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID)
	if err == nil {
		t.Fatal("expected decode error for invalid cached tool_set json")
	}
}

func TestGetSessionToolSetPropagatesCreateError(t *testing.T) {
	orgID := uuid.New()
	sessionID := uuid.New()
	agentID := uuid.New()

	resolver := newResolverFixture()
	resolver.sessionSets.createErr = errors.New("create failed")
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

	_, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID)
	if err == nil || err.Error() != "create failed" {
		t.Fatalf("GetSessionToolSet error = %v, want create failed", err)
	}
}

func TestGetSessionToolSetPropagatesSessionLookupError(t *testing.T) {
	sessionID := uuid.New()
	agentID := uuid.New()
	resolver := newResolverFixture()
	resolver.sessions.err = errors.New("session load failed")

	_, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID)
	if err == nil || err.Error() != "session load failed" {
		t.Fatalf("GetSessionToolSet error = %v, want session load failed", err)
	}
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

func TestHandlePolicyUpdateInvalidatesAllActiveSets(t *testing.T) {
	orgID := uuid.New()
	sessionA := uuid.New()
	sessionB := uuid.New()
	agentA := uuid.New()
	agentB := uuid.New()

	resolver := newResolverFixture()
	rowA, err := resolver.sessionSets.Create(context.Background(), sessionA, agentA, json.RawMessage(`[]`))
	if err != nil {
		t.Fatalf("Create rowA: %v", err)
	}
	rowB, err := resolver.sessionSets.Create(context.Background(), sessionB, agentB, json.RawMessage(`[]`))
	if err != nil {
		t.Fatalf("Create rowB: %v", err)
	}
	resolver.sessionSets.activeByOrg[orgID] = []repo.SessionToolSet{*rowA, *rowB}

	if err := resolver.resolver.HandlePolicyUpdate(context.Background(), orgID); err != nil {
		t.Fatalf("HandlePolicyUpdate: %v", err)
	}

	if _, err := resolver.sessionSets.GetActive(context.Background(), sessionA, agentA); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("sessionA active row error = %v, want ErrNotFound", err)
	}
	if _, err := resolver.sessionSets.GetActive(context.Background(), sessionB, agentB); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("sessionB active row error = %v, want ErrNotFound", err)
	}
}

func TestHandlePolicyUpdateValidatesOrgID(t *testing.T) {
	resolver := newResolverFixture()
	if err := resolver.resolver.HandlePolicyUpdate(context.Background(), uuid.Nil); err == nil {
		t.Fatal("expected error for nil organization id")
	}
}

func TestHandlePolicyUpdatePropagatesListError(t *testing.T) {
	resolver := newResolverFixture()
	resolver.sessionSets.listErr = errors.New("list failed")
	err := resolver.resolver.HandlePolicyUpdate(context.Background(), uuid.New())
	if err == nil || err.Error() != "list failed" {
		t.Fatalf("HandlePolicyUpdate error = %v, want list failed", err)
	}
}

func TestHandlePolicyUpdatePropagatesInvalidateError(t *testing.T) {
	orgID := uuid.New()
	sessionID := uuid.New()
	agentID := uuid.New()
	resolver := newResolverFixture()

	row, err := resolver.sessionSets.Create(context.Background(), sessionID, agentID, json.RawMessage(`[]`))
	if err != nil {
		t.Fatalf("Create row: %v", err)
	}
	resolver.sessionSets.activeByOrg[orgID] = []repo.SessionToolSet{*row}
	resolver.sessionSets.invalidateErr = errors.New("invalidate failed")

	err = resolver.resolver.HandlePolicyUpdate(context.Background(), orgID)
	if err == nil || err.Error() != "invalidate failed" {
		t.Fatalf("HandlePolicyUpdate error = %v, want invalidate failed", err)
	}
}

func TestSubscribePolicyUpdatesHandlesCapabilityEvents(t *testing.T) {
	orgID := uuid.New()
	sessionID := uuid.New()
	agentID := uuid.New()

	resolver := newResolverFixture()
	row, err := resolver.sessionSets.Create(context.Background(), sessionID, agentID, json.RawMessage(`[]`))
	if err != nil {
		t.Fatalf("Create row: %v", err)
	}
	resolver.sessionSets.activeByOrg[orgID] = []repo.SessionToolSet{*row}

	_ = resolver.resolver.SubscribePolicyUpdates(nil)
	if resolver.events.handler == nil {
		t.Fatal("expected policy update handler to be registered")
	}

	if err := resolver.events.handler(context.Background(), eventbus.DomainEvent{
		OrganizationID: orgID,
		EventType:      "capability.policy.updated",
	}); err != nil {
		t.Fatalf("handler(policy.updated): %v", err)
	}
	if _, err := resolver.sessionSets.GetActive(context.Background(), sessionID, agentID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("active row error = %v, want ErrNotFound after policy update", err)
	}
}

func TestSubscribePolicyUpdatesFallsBackToSubscribedOrg(t *testing.T) {
	orgID := uuid.New()
	sessionID := uuid.New()
	agentID := uuid.New()

	resolver := newResolverFixture()
	row, err := resolver.sessionSets.Create(context.Background(), sessionID, agentID, json.RawMessage(`[]`))
	if err != nil {
		t.Fatalf("Create row: %v", err)
	}
	resolver.sessionSets.activeByOrg[orgID] = []repo.SessionToolSet{*row}

	_ = resolver.resolver.SubscribePolicyUpdates(&orgID)
	if resolver.events.handler == nil {
		t.Fatal("expected policy update handler to be registered")
	}

	if err := resolver.events.handler(context.Background(), eventbus.DomainEvent{
		EventType: "capability.policy.updated",
	}); err != nil {
		t.Fatalf("handler(policy.updated fallback org): %v", err)
	}
	if _, err := resolver.sessionSets.GetActive(context.Background(), sessionID, agentID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("active row error = %v, want ErrNotFound after policy update fallback", err)
	}
}

func TestSubscribePolicyUpdatesIgnoresNonPolicyEvents(t *testing.T) {
	resolver := newResolverFixture()
	_ = resolver.resolver.SubscribePolicyUpdates(nil)
	if resolver.events.handler == nil {
		t.Fatal("expected policy update handler to be registered")
	}
	if err := resolver.events.handler(context.Background(), eventbus.DomainEvent{
		OrganizationID: uuid.New(),
		EventType:      "something.else",
	}); err != nil {
		t.Fatalf("handler(non-policy): %v", err)
	}
}

func TestSubscribePolicyUpdatesNoEventBus(t *testing.T) {
	resolver := newResolverFixture()
	resolver.resolver.events = nil
	if sub := resolver.resolver.SubscribePolicyUpdates(nil); sub != (eventbus.Subscription{}) {
		t.Fatalf("expected zero subscription when event bus is nil, got %#v", sub)
	}
}

func TestResolveSessionProjectIDUsesProjectTaskScope(t *testing.T) {
	orgID := uuid.New()
	projectA := uuid.New()
	projectB := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	agentID := uuid.New()

	resolver := newResolverFixture()
	resolver.sessions.items[sessionID] = repo.ChatSession{
		ID:             sessionID,
		OrganizationID: orgID,
		ScopeType:      "project_task",
		ScopeID:        taskID,
	}
	resolver.tasks.items[taskID] = repo.ProjectTask{
		ID:        taskID,
		ProjectID: projectA,
	}
	resolver.agents.items[agentID] = repo.Agent{ID: agentID}
	resolver.toolDefs.items = []repo.ToolDefinition{
		{Name: "memory.query", ToolTier: "tier1", ToolDomain: "memory", IsEnabled: true},
	}

	connA := repo.MCPConnection{ID: uuid.New(), OrganizationID: orgID, ProjectID: &projectA, IsEnabled: true}
	connB := repo.MCPConnection{ID: uuid.New(), OrganizationID: orgID, ProjectID: &projectB, IsEnabled: true}
	resolver.connections.items = []repo.MCPConnection{connA, connB}
	resolver.catalog.entries[connA.ID] = []repo.MCPToolCatalogEntry{{ConnectionID: connA.ID, ToolName: "mcp.projectA.allowed", IsEnabled: true}}
	resolver.catalog.entries[connB.ID] = []repo.MCPToolCatalogEntry{{ConnectionID: connB.ID, ToolName: "mcp.projectB.denied", IsEnabled: true}}

	got, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID)
	if err != nil {
		t.Fatalf("GetSessionToolSet: %v", err)
	}
	if !containsTool(got, "mcp.projectA.allowed") {
		t.Fatalf("missing project A MCP tool: %+v", got)
	}
	if containsTool(got, "mcp.projectB.denied") {
		t.Fatalf("unexpected project B MCP tool in project-task scoped session: %+v", got)
	}
}

func TestResolveToolSetPropagatesProjectTaskLookupError(t *testing.T) {
	orgID := uuid.New()
	taskID := uuid.New()
	agentID := uuid.New()
	resolver := newResolverFixture()
	resolver.tasks.err = errors.New("task lookup failed")

	_, err := resolver.resolver.resolveToolSet(context.Background(), repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      "project_task",
		ScopeID:        taskID,
	}, agentID)
	if err == nil || err.Error() != "task lookup failed" {
		t.Fatalf("resolveToolSet error = %v, want task lookup failed", err)
	}
}

func TestBuildUniverseInfersDomainFromToolNamePrefix(t *testing.T) {
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
		{Name: "git.status", ToolTier: "tier1", ToolDomain: "", IsEnabled: true},
	}

	got, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID)
	if err != nil {
		t.Fatalf("GetSessionToolSet: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("tool count = %d, want 1", len(got))
	}
	if got[0].Domain != "git" {
		t.Fatalf("tool domain = %q, want git", got[0].Domain)
	}
}

func TestTrimCapabilityNilAndEmptyString(t *testing.T) {
	if got := trimCapability(nil); got != nil {
		t.Fatalf("trimCapability(nil) = %v, want nil", got)
	}

	blank := "  "
	if got := trimCapability(&blank); got != nil {
		t.Fatalf("trimCapability(blank) = %v, want nil", got)
	}
}

func TestDecodeToolSetEmptyAndInvalidJSON(t *testing.T) {
	got, err := decodeToolSet(nil)
	if err != nil {
		t.Fatalf("decodeToolSet(nil): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("decodeToolSet(nil) length = %d, want 0", len(got))
	}

	if _, err := decodeToolSet(json.RawMessage(`{"not":"an array"}`)); err == nil {
		t.Fatal("expected decodeToolSet to fail on non-array JSON")
	}
}

func TestConnectionVisibleInSessionCombinations(t *testing.T) {
	projectID := uuid.New()
	otherProjectID := uuid.New()

	if !connectionVisibleInSession(nil, nil) {
		t.Fatal("org-scoped connection should be visible without session project")
	}
	if !connectionVisibleInSession(nil, &projectID) {
		t.Fatal("org-scoped connection should be visible with session project")
	}
	if connectionVisibleInSession(&projectID, nil) {
		t.Fatal("project-scoped connection should be hidden without session project")
	}
	if !connectionVisibleInSession(&projectID, &projectID) {
		t.Fatal("project-scoped connection should be visible in matching project session")
	}
	if connectionVisibleInSession(&projectID, &otherProjectID) {
		t.Fatal("project-scoped connection should be hidden in different project session")
	}
}

func TestMatchToolGlobEdgeCases(t *testing.T) {
	if matchToolGlob("", "file.read") {
		t.Fatal("empty pattern must not match")
	}
	if matchToolGlob("file.*", "") {
		t.Fatal("empty name must not match")
	}
	if !matchToolGlob("mcp.**.create_issue", "mcp.github.repo.create_issue") {
		t.Fatal("expected ** mid-pattern to match nested name")
	}
	if !matchToolGlob("file.*.*", "file.alpha.beta") {
		t.Fatal("expected two-segment wildcard to match")
	}
	if matchToolGlob("file.*.read", "file.alpha.beta.read") {
		t.Fatal("single segment wildcard should not match extra segments")
	}
}

func TestStage3PrioritizesMCPTools(t *testing.T) {
	taskID := uuid.New()
	nodeID := uuid.New()
	connectionID := uuid.New()
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
			{ConnectionID: connectionID, ToolName: "mcp.github.create_issue"},
		},
	}

	input := []ToolDescriptor{
		{Name: "memory.query", Domain: "memory", Source: "native"},
		{Name: "mcp.github.create_issue", Domain: "mcp", Source: "mcp", MCPConnectionID: &connectionID},
		{Name: "file.read", Domain: "file", Source: "native"},
	}
	got, err := resolver.resolver.applyFlowNodeOrdering(context.Background(), session, input)
	if err != nil {
		t.Fatalf("applyFlowNodeOrdering: %v", err)
	}
	if len(got) != len(input) {
		t.Fatalf("tool count = %d, want %d", len(got), len(input))
	}
	if got[0].Name != "mcp.github.create_issue" {
		t.Fatalf("first tool = %q, want mcp.github.create_issue", got[0].Name)
	}
	if !containsTool(got, "memory.query") || !containsTool(got, "file.read") {
		t.Fatalf("stage3 removed non-prioritized tools: %+v", got)
	}
}

func TestBuildUniversePropagatesMCPConnectionListError(t *testing.T) {
	orgID := uuid.New()
	resolver := newResolverFixture()
	resolver.toolDefs.items = []repo.ToolDefinition{{Name: "memory.query", ToolTier: "tier1", ToolDomain: "memory"}}
	resolver.connections.err = errors.New("mcp connection list failed")

	_, err := resolver.resolver.buildUniverse(context.Background(), repo.ChatSession{OrganizationID: orgID}, nil)
	if err == nil || err.Error() != "mcp connection list failed" {
		t.Fatalf("buildUniverse error = %v, want mcp connection list failed", err)
	}
}

func TestBuildUniversePropagatesMCPCatalogError(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	resolver := newResolverFixture()
	resolver.toolDefs.items = []repo.ToolDefinition{{Name: "memory.query", ToolTier: "tier1", ToolDomain: "memory"}}
	resolver.connections.items = []repo.MCPConnection{
		{ID: uuid.New(), OrganizationID: orgID, ProjectID: &projectID, IsEnabled: true},
	}
	resolver.catalog.err = errors.New("mcp catalog failed")

	_, err := resolver.resolver.buildUniverse(context.Background(), repo.ChatSession{OrganizationID: orgID}, &projectID)
	if err == nil || err.Error() != "mcp catalog failed" {
		t.Fatalf("buildUniverse error = %v, want mcp catalog failed", err)
	}
}

func TestResolveToolSetPropagatesAgentError(t *testing.T) {
	orgID := uuid.New()
	agentID := uuid.New()
	resolver := newResolverFixture()
	resolver.toolDefs.items = []repo.ToolDefinition{{Name: "memory.query", ToolTier: "tier1", ToolDomain: "memory"}}
	resolver.agents.err = errors.New("agent load failed")

	_, err := resolver.resolver.resolveToolSet(context.Background(), repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      "organization",
		ScopeID:        orgID,
	}, agentID)
	if err == nil || err.Error() != "agent load failed" {
		t.Fatalf("resolveToolSet error = %v, want agent load failed", err)
	}
}

func TestResolveToolSetPropagatesFlowExecutionError(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	nodeID := uuid.New()
	agentID := uuid.New()
	resolver := newResolverFixture()
	resolver.toolDefs.items = []repo.ToolDefinition{{Name: "memory.query", ToolTier: "tier1", ToolDomain: "memory"}}
	resolver.agents.items[agentID] = repo.Agent{ID: agentID}
	resolver.tasks.items[taskID] = repo.ProjectTask{ID: taskID, ProjectID: projectID, CurrentFlowNodeID: &nodeID}
	resolver.flowExecutions.err = errors.New("flow execution failed")

	_, err := resolver.resolver.resolveToolSet(context.Background(), repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      "project_task",
		ScopeID:        taskID,
	}, agentID)
	if err == nil || err.Error() != "flow execution failed" {
		t.Fatalf("resolveToolSet error = %v, want flow execution failed", err)
	}
}

func TestApplyFlowNodeOrderingHandlesCurrentNodeNil(t *testing.T) {
	taskID := uuid.New()
	session := repo.ChatSession{ScopeType: "project_task", ScopeID: taskID}
	input := []ToolDescriptor{{Name: "memory.query", Domain: "memory"}}

	resolver := newResolverFixture()
	resolver.tasks.items[taskID] = repo.ProjectTask{ID: taskID}

	got, err := resolver.resolver.applyFlowNodeOrdering(context.Background(), session, input)
	if err != nil {
		t.Fatalf("applyFlowNodeOrdering: %v", err)
	}
	if len(got) != 1 || got[0].Name != "memory.query" {
		t.Fatalf("unexpected ordering output: %+v", got)
	}
}

func TestApplyFlowNodeOrderingHandlesMissingActiveExecution(t *testing.T) {
	taskID := uuid.New()
	nodeID := uuid.New()
	session := repo.ChatSession{ScopeType: "project_task", ScopeID: taskID}
	input := []ToolDescriptor{{Name: "memory.query", Domain: "memory"}}

	resolver := newResolverFixture()
	resolver.tasks.items[taskID] = repo.ProjectTask{ID: taskID, CurrentFlowNodeID: &nodeID}

	got, err := resolver.resolver.applyFlowNodeOrdering(context.Background(), session, input)
	if err != nil {
		t.Fatalf("applyFlowNodeOrdering: %v", err)
	}
	if len(got) != 1 || got[0].Name != "memory.query" {
		t.Fatalf("unexpected ordering output: %+v", got)
	}
}

func TestApplyFlowNodeOrderingPropagatesFlowNodeError(t *testing.T) {
	taskID := uuid.New()
	nodeID := uuid.New()
	session := repo.ChatSession{ScopeType: "project_task", ScopeID: taskID}
	input := []ToolDescriptor{{Name: "memory.query", Domain: "memory"}}

	resolver := newResolverFixture()
	resolver.tasks.items[taskID] = repo.ProjectTask{ID: taskID, CurrentFlowNodeID: &nodeID}
	resolver.flowExecutions.active[taskID.String()+"|"+nodeID.String()] = repo.FlowNodeExecution{TaskID: taskID, FlowNodeID: nodeID}
	resolver.flowNodes.err = errors.New("flow node load failed")

	_, err := resolver.resolver.applyFlowNodeOrdering(context.Background(), session, input)
	if err == nil || err.Error() != "flow node load failed" {
		t.Fatalf("applyFlowNodeOrdering error = %v, want flow node load failed", err)
	}
}

func TestTrimCapabilityReturnsTrimmedValue(t *testing.T) {
	value := "  memory.read  "
	got := trimCapability(&value)
	if got == nil || *got != "memory.read" {
		t.Fatalf("trimCapability(trimmed) = %v, want memory.read", got)
	}
}

func TestToolDomainFromNameEdgeCases(t *testing.T) {
	if got := toolDomainFromName("single"); got != "single" {
		t.Fatalf("toolDomainFromName(single) = %q, want single", got)
	}
	if got := toolDomainFromName(""); got != "" {
		t.Fatalf("toolDomainFromName(empty) = %q, want empty", got)
	}
	if got := toolDomainFromName(" git.status "); got != "git" {
		t.Fatalf("toolDomainFromName(trimmed) = %q, want git", got)
	}
}

func TestSanitizeToolNameForAPI(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"file.read", "file_read"},
		{"git.status", "git_status"},
		{"memory.query", "memory_query"},
		{"task.create", "task_create"},
		{"already_valid", "already_valid"},
		{"with-dashes", "with-dashes"},
		{"MixedCase.Action", "MixedCase_Action"},
		{"a.b.c", "a_b_c"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := SanitizeToolNameForAPI(tc.input)
			if got != tc.want {
				t.Fatalf("SanitizeToolNameForAPI(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestBuildUniversePopulatesAPIName(t *testing.T) {
	orgID := uuid.New()
	sessionID := uuid.New()
	agentID := uuid.New()

	resolver := newResolverFixture()
	resolver.sessions.items[sessionID] = repo.ChatSession{
		ID:             sessionID,
		OrganizationID: orgID,
	}
	resolver.agents.items[agentID] = repo.Agent{ID: agentID}
	resolver.toolDefs.items = []repo.ToolDefinition{
		{Name: "file.read", ToolTier: "tier1", IsEnabled: true},
		{Name: "git.status", ToolTier: "tier1", IsEnabled: true},
	}

	got, err := resolver.resolver.GetSessionToolSet(context.Background(), sessionID, agentID)
	if err != nil {
		t.Fatalf("GetSessionToolSet: %v", err)
	}
	byName := make(map[string]ToolDescriptor, len(got))
	for _, td := range got {
		byName[td.Name] = td
	}
	if byName["file.read"].APIName != "file_read" {
		t.Fatalf("file.read APIName = %q, want file_read", byName["file.read"].APIName)
	}
	if byName["git.status"].APIName != "git_status" {
		t.Fatalf("git.status APIName = %q, want git_status", byName["git.status"].APIName)
	}
}

func TestApplyCapabilityGateWithoutPolicyEvaluatorReturnsInput(t *testing.T) {
	resolver := newResolverFixture()
	resolver.resolver.policies = nil
	input := []ToolDescriptor{{Name: "memory.query", Tier: "tier1"}}

	got, err := resolver.resolver.applyCapabilityGate(context.Background(), uuid.New(), nil, uuid.New(), input)
	if err != nil {
		t.Fatalf("applyCapabilityGate: %v", err)
	}
	if len(got) != 1 || got[0].Name != "memory.query" {
		t.Fatalf("applyCapabilityGate output = %+v, want unchanged input", got)
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
	events         *fakeEventSubscriber
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
	events := &fakeEventSubscriber{}

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
		Events:          events,
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
		events:         events,
	}
}

type fakeSessionToolSetRepo struct {
	items         []*repo.SessionToolSet
	active        map[string]*repo.SessionToolSet
	activeByOrg   map[uuid.UUID][]repo.SessionToolSet
	createCalls   int
	createErr     error
	getErr        error
	invalidateErr error
	listErr       error
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
	if f.getErr != nil {
		return nil, f.getErr
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
	if f.listErr != nil {
		return nil, f.listErr
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

func containsTool(items []ToolDescriptor, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}

type fakeEventSubscriber struct {
	consumerName string
	orgID        *uuid.UUID
	handler      eventbus.EventHandler
}

func (f *fakeEventSubscriber) Subscribe(consumerName string, orgID *uuid.UUID, handler eventbus.EventHandler) eventbus.Subscription {
	f.consumerName = consumerName
	f.orgID = orgID
	f.handler = handler
	return eventbus.Subscription{}
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
