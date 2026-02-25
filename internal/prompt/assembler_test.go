package prompt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/policy"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/tools"
)

func TestPromptAssemblerLayer1AlwaysPresent(t *testing.T) {
	orgID := uuid.New()
	assembler := mustUnitAssembler(t, unitAssemblerConfig{
		session: repo.ChatSession{ID: uuid.New(), OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Mode: "sync", TurnCount: 5, Metadata: json.RawMessage(`{}`)},
		agent:   repo.Agent{ID: uuid.New(), OrganizationID: orgID, SystemPrompt: "", OperatorInstructions: ""},
		messages: []repo.ChatMessage{
			{SequenceNumber: 1, Role: "user", Content: strings.Repeat("x", 2000)},
			{SequenceNumber: 2, Role: "assistant", Content: strings.Repeat("y", 2000)},
		},
		summaries:    []repo.ChatSummary{{FromSequence: 1, ToSequence: 1, SummaryText: strings.Repeat("summary ", 200)}},
		modelProfile: repo.ModelProfile{LogicalProfileID: "main", ContextWindowTokens: 256},
	})

	assembled, err := assembler.Assemble(context.Background(), AssemblyInput{
		SessionID:       assembler.sessions.(*fakeSessionRepo).session.ID,
		AgentID:         assembler.agents.(*fakeAgentRepo).agent.ID,
		ModelProfileID:  "main",
		ToolDescriptors: []tools.ToolDescriptor{{Name: "tool.low", Priority: 99, Description: strings.Repeat("desc", 500)}},
	})
	if err != nil && !errors.Is(err, ErrContextCompressed) {
		t.Fatalf("Assemble error = %v", err)
	}
	if !strings.Contains(assembled.SystemPrompt, defaultSystemPrompt) {
		t.Fatalf("system prompt missing layer1 default prompt: %q", assembled.SystemPrompt)
	}
}

func TestPromptAssemblerLayer5OrderingMostRelevantLast(t *testing.T) {
	orgID := uuid.New()
	idA := uuid.New()
	idB := uuid.New()
	idC := uuid.New()

	assembler := mustUnitAssembler(t, unitAssemblerConfig{
		session:  repo.ChatSession{ID: uuid.New(), OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Mode: "sync", Metadata: json.RawMessage(`{}`)},
		agent:    repo.Agent{ID: uuid.New(), OrganizationID: orgID, SystemPrompt: "Agent"},
		messages: []repo.ChatMessage{{SequenceNumber: 1, Role: "user", Content: "context"}},
		retrieval: memory.RetrievalResult{Memories: []memory.RankedMemory{
			{Memory: repo.Memory{ID: idA, Content: "A", CreatedAt: time.Now().UTC()}, Score: 0.9},
			{Memory: repo.Memory{ID: idB, Content: "B", CreatedAt: time.Now().UTC()}, Score: 0.5},
			{Memory: repo.Memory{ID: idC, Content: "C", CreatedAt: time.Now().UTC()}, Score: 0.3},
		}},
	})

	assembled, err := assembler.Assemble(context.Background(), AssemblyInput{SessionID: assembler.sessions.(*fakeSessionRepo).session.ID, AgentID: assembler.agents.(*fakeAgentRepo).agent.ID})
	if err != nil {
		t.Fatalf("Assemble error = %v", err)
	}
	got := assembled.MemoryManifest.InjectedMemoryIDs
	want := []uuid.UUID{idC, idB, idA}
	if len(got) != len(want) {
		t.Fatalf("injected ids len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("injected id[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestPromptAssemblerLayer5CooldownSkipsPreviousTurnMemories(t *testing.T) {
	orgID := uuid.New()
	idA := uuid.New()
	idB := uuid.New()
	idC := uuid.New()

	assembler := mustUnitAssembler(t, unitAssemblerConfig{
		session:  repo.ChatSession{ID: uuid.New(), OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Mode: "sync"},
		agent:    repo.Agent{ID: uuid.New(), OrganizationID: orgID, SystemPrompt: "Agent"},
		messages: []repo.ChatMessage{{SequenceNumber: 1, Role: "user", Content: "context"}},
		retrieval: memory.RetrievalResult{Memories: []memory.RankedMemory{
			{Memory: repo.Memory{ID: idA, Content: "A"}, Score: 0.9},
			{Memory: repo.Memory{ID: idB, Content: "B"}, Score: 0.5},
			{Memory: repo.Memory{ID: idC, Content: "C"}, Score: 0.3},
		}},
	})

	assembled, err := assembler.Assemble(context.Background(), AssemblyInput{
		SessionID: assembler.sessions.(*fakeSessionRepo).session.ID,
		AgentID:   assembler.agents.(*fakeAgentRepo).agent.ID,
		PreviousManifest: &MemoryManifest{
			InjectedMemoryIDs: []uuid.UUID{idA},
		},
	})
	if err != nil {
		t.Fatalf("Assemble error = %v", err)
	}
	got := assembled.MemoryManifest.InjectedMemoryIDs
	want := []uuid.UUID{idC, idB}
	if len(got) != len(want) {
		t.Fatalf("injected ids len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("injected id[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestPromptAssemblerLayer5BudgetSkipsOverflowMemories(t *testing.T) {
	orgID := uuid.New()
	memories := []memory.RankedMemory{}
	for i := 0; i < 4; i++ {
		memories = append(memories, memory.RankedMemory{Memory: repo.Memory{ID: uuid.New(), Content: strings.Repeat("m", 40)}, Score: float64(10 - i)})
	}
	assembler := mustUnitAssembler(t, unitAssemblerConfig{
		session:             repo.ChatSession{ID: uuid.New(), OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Mode: "sync"},
		agent:               repo.Agent{ID: uuid.New(), OrganizationID: orgID, SystemPrompt: "Agent"},
		messages:            []repo.ChatMessage{{SequenceNumber: 1, Role: "user", Content: "context"}},
		retrieval:           memory.RetrievalResult{Memories: memories},
		defaultMemoryBudget: 30,
	})

	assembled, err := assembler.Assemble(context.Background(), AssemblyInput{SessionID: assembler.sessions.(*fakeSessionRepo).session.ID, AgentID: assembler.agents.(*fakeAgentRepo).agent.ID})
	if err != nil {
		t.Fatalf("Assemble error = %v", err)
	}
	if got := len(assembled.MemoryManifest.InjectedMemoryIDs); got != 3 {
		t.Fatalf("injected memory count = %d, want 3", got)
	}
}

func TestPromptAssemblerLayer6SummarySubstitution(t *testing.T) {
	orgID := uuid.New()
	messages := make([]repo.ChatMessage, 0, 25)
	for i := 1; i <= 25; i++ {
		messages = append(messages, repo.ChatMessage{SequenceNumber: int64(i), Role: "user", Content: "m" + strings.Repeat("x", i)})
	}
	assembler := mustUnitAssembler(t, unitAssemblerConfig{
		session:   repo.ChatSession{ID: uuid.New(), OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Mode: "sync"},
		agent:     repo.Agent{ID: uuid.New(), OrganizationID: orgID, SystemPrompt: "Agent"},
		messages:  messages,
		summaries: []repo.ChatSummary{{FromSequence: 1, ToSequence: 20, SummaryText: "condensed"}},
	})

	assembled, err := assembler.Assemble(context.Background(), AssemblyInput{SessionID: assembler.sessions.(*fakeSessionRepo).session.ID, AgentID: assembler.agents.(*fakeAgentRepo).agent.ID})
	if err != nil {
		t.Fatalf("Assemble error = %v", err)
	}
	history := assembled.Messages[1:]
	summaryCount := 0
	rawCount := 0
	for _, msg := range history {
		if strings.HasPrefix(msg.Content, "[Summary of earlier conversation]:") {
			summaryCount++
			continue
		}
		rawCount++
	}
	if summaryCount != 1 {
		t.Fatalf("summary count = %d, want 1", summaryCount)
	}
	if rawCount != 5 {
		t.Fatalf("raw message count = %d, want 5", rawCount)
	}
	if strings.Contains(assembled.SystemPrompt, "mxxxxxxxxxxxxxxxxxxxx") {
		t.Fatalf("system prompt unexpectedly contains raw summarized content")
	}
}

func TestReduceToolsToBudgetDropsDeprioritizedBeforePrioritizedAndKeepsCore(t *testing.T) {
	items := []tools.ToolDescriptor{
		{Name: "memory.query", Tier: "tier1", Priority: 1, Description: strings.Repeat("a", 400)},
		{Name: "git.status", Tier: "tier2", Priority: 2, Description: strings.Repeat("b", 400)},
		{Name: "mcp.foo", Source: "mcp", Tier: "tier2", Priority: 20, Description: strings.Repeat("c", 400)},
		{Name: "file.search", Tier: "tier2", Priority: 30, Description: strings.Repeat("d", 400)},
	}
	trimmed := reduceToolsToBudget(items, 380)
	if !containsTool(trimmed, "memory.query") {
		t.Fatalf("core tier1 tool removed unexpectedly")
	}
	if !containsTool(trimmed, "git.status") {
		t.Fatalf("prioritized tool removed before deprioritized tools")
	}
	if containsTool(trimmed, "file.search") {
		t.Fatalf("deprioritized tool should be removed first")
	}
}

func TestPromptAssemblerReturnsErrContextCompressedWhenOnlySummariesOverflow(t *testing.T) {
	orgID := uuid.New()
	assembler := mustUnitAssembler(t, unitAssemblerConfig{
		session:             repo.ChatSession{ID: uuid.New(), OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Mode: "sync"},
		agent:               repo.Agent{ID: uuid.New(), OrganizationID: orgID, SystemPrompt: "Agent"},
		messages:            []repo.ChatMessage{},
		summaries:           []repo.ChatSummary{{FromSequence: 1, ToSequence: 100, SummaryText: strings.Repeat("summary ", 200)}},
		defaultLayer6Budget: 20,
	})

	_, err := assembler.Assemble(context.Background(), AssemblyInput{SessionID: assembler.sessions.(*fakeSessionRepo).session.ID, AgentID: assembler.agents.(*fakeAgentRepo).agent.ID})
	if !errors.Is(err, ErrContextCompressed) {
		t.Fatalf("Assemble error = %v, want ErrContextCompressed", err)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens("hello world"); got != 2 {
		t.Fatalf("estimateTokens(hello world) = %d, want 2", got)
	}
}

func TestPromptAssemblerMCPPromptConflictSkillWins(t *testing.T) {
	orgID := uuid.New()
	agentID := uuid.New()
	skillID := uuid.New()
	connID := uuid.New()
	skillsDir := t.TempDir()
	if err := osWriteFile(skillsDir+"/git.md", []byte("Skill content")); err != nil {
		t.Fatalf("write skill file: %v", err)
	}

	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	assembler := mustUnitAssembler(t, unitAssemblerConfig{
		session:       repo.ChatSession{ID: uuid.New(), OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Mode: "sync"},
		agent:         repo.Agent{ID: agentID, OrganizationID: orgID, SystemPrompt: "Agent"},
		messages:      []repo.ChatMessage{{SequenceNumber: 1, Role: "user", Content: "context"}},
		skillsDir:     skillsDir,
		logger:        logger,
		agentSkills:   []repo.AgentSkillAttachment{{AgentID: agentID, SkillID: skillID, Priority: 1, IsActive: true}},
		skillRecords:  map[uuid.UUID]repo.Skill{skillID: {ID: skillID, OrganizationID: orgID, DisplayName: "Git Skill", Description: "git workflow guide", FilePath: "git.md", IsActive: true}},
		mcpConnection: repo.MCPConnection{ID: connID, OrganizationID: orgID, TransportConfig: json.RawMessage(`{"capabilities":{"prompts":true}}`)},
		mcpPrompts:    []MCPPrompt{{Name: "Git Prompt", Description: "git workflow guide", Content: "MCP content"}},
	})

	assembled, err := assembler.Assemble(context.Background(), AssemblyInput{
		SessionID: assembler.sessions.(*fakeSessionRepo).session.ID,
		AgentID:   assembler.agents.(*fakeAgentRepo).agent.ID,
		ToolDescriptors: []tools.ToolDescriptor{{
			Name:            "mcp.git.run",
			Source:          "mcp",
			MCPConnectionID: &connID,
		}},
	})
	if err != nil {
		t.Fatalf("Assemble error = %v", err)
	}
	if !strings.Contains(assembled.SystemPrompt, "Skill content") {
		t.Fatalf("assembled prompt missing skill content")
	}
	if strings.Contains(assembled.SystemPrompt, "MCP content") {
		t.Fatalf("assembled prompt still contains conflicting MCP prompt content")
	}
	if !strings.Contains(strings.Join(assembled.Errors, "\n"), "overrides MCP prompt") {
		t.Fatalf("missing conflict warning in errors: %+v", assembled.Errors)
	}
}

func containsTool(items []tools.ToolDescriptor, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}

type unitAssemblerConfig struct {
	session             repo.ChatSession
	agent               repo.Agent
	messages            []repo.ChatMessage
	summaries           []repo.ChatSummary
	retrieval           memory.RetrievalResult
	defaultMemoryBudget int
	defaultLayer6Budget int
	skillsDir           string
	logger              *slog.Logger
	modelProfile        repo.ModelProfile
	agentSkills         []repo.AgentSkillAttachment
	skillRecords        map[uuid.UUID]repo.Skill
	mcpConnection       repo.MCPConnection
	mcpPrompts          []MCPPrompt
}

func mustUnitAssembler(t *testing.T, cfg unitAssemblerConfig) *PromptAssembler {
	t.Helper()
	if cfg.session.ID == uuid.Nil {
		cfg.session.ID = uuid.New()
	}
	if cfg.agent.ID == uuid.Nil {
		cfg.agent.ID = uuid.New()
	}
	if cfg.modelProfile.LogicalProfileID == "" {
		cfg.modelProfile = repo.ModelProfile{LogicalProfileID: "default", ContextWindowTokens: defaultContextWindowTokens}
	}

	assembler, err := NewPromptAssembler(AssemblerOptions{
		Sessions:            &fakeSessionRepo{session: cfg.session},
		Messages:            &fakeMessageRepo{messages: cfg.messages},
		Summaries:           &fakeSummaryRepo{summaries: cfg.summaries},
		Organizations:       &fakeOrganizationRepo{org: repo.Organization{ID: cfg.session.ScopeID, DisplayName: "Test Org"}},
		Projects:            &fakeProjectRepo{},
		Tasks:               &fakeTaskRepo{},
		FlowTemplates:       &fakeFlowTemplateRepo{},
		FlowNodes:           &fakeFlowNodeRepo{},
		FlowExecutions:      &fakeFlowExecutionRepo{},
		FlowNodeSkills:      &fakeFlowNodeSkillRepo{},
		Agents:              &fakeAgentRepo{agent: cfg.agent},
		ModelProfiles:       &fakeModelProfileRepo{profile: cfg.modelProfile},
		MemoryRetriever:     &fakeMemoryRetriever{result: cfg.retrieval},
		Summarization:       &fakeSummarizationChecker{},
		Enqueuer:            &fakeEnqueuer{},
		Policies:            &fakePolicyEvaluator{},
		AgentSkills:         &fakeAgentSkillAttachmentRepo{rows: cfg.agentSkills},
		Skills:              &fakeSkillRepo{records: cfg.skillRecords},
		MCPConnections:      &fakeMCPConnectionRepo{connection: cfg.mcpConnection},
		MCPPromptLister:     &fakeMCPPromptLister{prompts: cfg.mcpPrompts},
		DefaultMemoryBudget: cfg.defaultMemoryBudget,
		DefaultLayer6Budget: cfg.defaultLayer6Budget,
		SkillsDir:           cfg.skillsDir,
		Logger:              cfg.logger,
	})
	if err != nil {
		t.Fatalf("NewPromptAssembler: %v", err)
	}
	return assembler
}

type fakeSessionRepo struct{ session repo.ChatSession }

func (f *fakeSessionRepo) GetByID(context.Context, uuid.UUID) (repo.ChatSession, error) {
	return f.session, nil
}

type fakeAgentRepo struct{ agent repo.Agent }

func (f *fakeAgentRepo) GetByID(context.Context, uuid.UUID) (repo.Agent, error) {
	return f.agent, nil
}

type fakeMessageRepo struct{ messages []repo.ChatMessage }

func (f *fakeMessageRepo) ListBySession(context.Context, uuid.UUID) ([]repo.ChatMessage, error) {
	return append([]repo.ChatMessage(nil), f.messages...), nil
}

type fakeSummaryRepo struct{ summaries []repo.ChatSummary }

func (f *fakeSummaryRepo) ListBySession(context.Context, uuid.UUID) ([]repo.ChatSummary, error) {
	return append([]repo.ChatSummary(nil), f.summaries...), nil
}

type fakeOrganizationRepo struct{ org repo.Organization }

func (f *fakeOrganizationRepo) GetByID(context.Context, uuid.UUID) (repo.Organization, error) {
	if f.org.ID == uuid.Nil {
		return repo.Organization{}, repo.ErrNotFound
	}
	return f.org, nil
}

type fakeProjectRepo struct{}

func (f *fakeProjectRepo) GetByID(context.Context, uuid.UUID) (repo.Project, error) {
	return repo.Project{}, repo.ErrNotFound
}

type fakeTaskRepo struct{}

func (f *fakeTaskRepo) GetByID(context.Context, uuid.UUID) (repo.ProjectTask, error) {
	return repo.ProjectTask{}, repo.ErrNotFound
}

type fakeFlowTemplateRepo struct{}

func (f *fakeFlowTemplateRepo) GetByID(context.Context, uuid.UUID) (repo.FlowTemplate, error) {
	return repo.FlowTemplate{}, repo.ErrNotFound
}

type fakeFlowNodeRepo struct{}

func (f *fakeFlowNodeRepo) GetByID(context.Context, uuid.UUID) (repo.FlowNode, error) {
	return repo.FlowNode{}, repo.ErrNotFound
}

type fakeFlowExecutionRepo struct{}

func (f *fakeFlowExecutionRepo) GetActive(context.Context, uuid.UUID, uuid.UUID) (repo.FlowNodeExecution, error) {
	return repo.FlowNodeExecution{}, repo.ErrNotFound
}

type fakeFlowNodeSkillRepo struct{}

func (f *fakeFlowNodeSkillRepo) ListByNode(context.Context, uuid.UUID) ([]repo.FlowNodeSkill, error) {
	return nil, nil
}

type fakeModelProfileRepo struct{ profile repo.ModelProfile }

func (f *fakeModelProfileRepo) GetCurrentByLogicalID(context.Context, uuid.UUID, string) (repo.ModelProfile, error) {
	if f.profile.LogicalProfileID == "" {
		return repo.ModelProfile{}, repo.ErrNotFound
	}
	return f.profile, nil
}

type fakeMemoryRetriever struct {
	result memory.RetrievalResult
}

func (f *fakeMemoryRetriever) Query(context.Context, memory.RetrievalRequest) (memory.RetrievalResult, error) {
	return f.result, nil
}

type fakeSummarizationChecker struct{}

func (f *fakeSummarizationChecker) ShouldSummarize(context.Context, uuid.UUID, int) (bool, error) {
	return false, nil
}

type fakeEnqueuer struct{}

func (f *fakeEnqueuer) Enqueue(context.Context, pgx.Tx, string, int, any, *time.Time) (uuid.UUID, error) {
	return uuid.New(), nil
}

type fakePolicyEvaluator struct{}

func (f *fakePolicyEvaluator) Evaluate(context.Context, policy.EvaluationRequest) policy.PolicyDecision {
	return policy.PolicyDecision{Effect: "allow", Layer: "none", Reason: "test"}
}

type fakeAgentSkillAttachmentRepo struct {
	rows []repo.AgentSkillAttachment
}

func (f *fakeAgentSkillAttachmentRepo) ListByAgent(context.Context, uuid.UUID) ([]repo.AgentSkillAttachment, error) {
	return append([]repo.AgentSkillAttachment(nil), f.rows...), nil
}

type fakeSkillRepo struct {
	records map[uuid.UUID]repo.Skill
}

func (f *fakeSkillRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Skill, error) {
	item, ok := f.records[id]
	if !ok {
		return repo.Skill{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeMCPConnectionRepo struct {
	connection repo.MCPConnection
}

func (f *fakeMCPConnectionRepo) GetByID(context.Context, uuid.UUID) (repo.MCPConnection, error) {
	if f.connection.ID == uuid.Nil {
		return repo.MCPConnection{}, repo.ErrNotFound
	}
	return f.connection, nil
}

type fakeMCPPromptLister struct {
	prompts []MCPPrompt
}

func (f *fakeMCPPromptLister) ListPrompts(context.Context, repo.MCPConnection) ([]MCPPrompt, error) {
	return append([]MCPPrompt(nil), f.prompts...), nil
}

func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
