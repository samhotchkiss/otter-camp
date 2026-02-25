package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
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
	items       []*repo.SessionToolSet
	active      map[string]*repo.SessionToolSet
	activeByOrg map[uuid.UUID][]repo.SessionToolSet
	createCalls int
}

func newFakeSessionToolSetRepo() *fakeSessionToolSetRepo {
	return &fakeSessionToolSetRepo{
		items:       make([]*repo.SessionToolSet, 0),
		active:      make(map[string]*repo.SessionToolSet),
		activeByOrg: make(map[uuid.UUID][]repo.SessionToolSet),
	}
}

func (f *fakeSessionToolSetRepo) Create(_ context.Context, sessionID, agentID uuid.UUID, toolSet json.RawMessage) (*repo.SessionToolSet, error) {
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
	key := sessionID.String() + "|" + agentID.String()
	item, ok := f.active[key]
	if !ok || item.InvalidatedAt != nil {
		return nil, repo.ErrNotFound
	}
	return item, nil
}

func (f *fakeSessionToolSetRepo) Invalidate(_ context.Context, sessionID, agentID uuid.UUID) error {
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
	items := f.activeByOrg[organizationID]
	out := make([]repo.SessionToolSet, len(items))
	copy(out, items)
	return out, nil
}

type fakeChatSessionRepo struct {
	items map[uuid.UUID]repo.ChatSession
}

func (f *fakeChatSessionRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
	item, ok := f.items[id]
	if !ok {
		return repo.ChatSession{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeAgentRepo struct {
	items map[uuid.UUID]repo.Agent
}

func (f *fakeAgentRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Agent, error) {
	item, ok := f.items[id]
	if !ok {
		return repo.Agent{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeTaskRepo struct {
	items map[uuid.UUID]repo.ProjectTask
}

func (f *fakeTaskRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectTask, error) {
	item, ok := f.items[id]
	if !ok {
		return repo.ProjectTask{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeFlowExecutionRepo struct {
	active map[string]repo.FlowNodeExecution
}

func (f *fakeFlowExecutionRepo) GetActive(_ context.Context, taskID, flowNodeID uuid.UUID) (repo.FlowNodeExecution, error) {
	key := taskID.String() + "|" + flowNodeID.String()
	item, ok := f.active[key]
	if !ok {
		return repo.FlowNodeExecution{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeFlowNodeRepo struct {
	items map[uuid.UUID]repo.FlowNode
}

func (f *fakeFlowNodeRepo) GetByID(_ context.Context, id uuid.UUID) (repo.FlowNode, error) {
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
