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
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/policy"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
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

func TestPromptAssemblerIncludesTaskContextForTaskScopedSession(t *testing.T) {
	assembled := assembleTaskContextPrompt(t)
	if !strings.Contains(assembled.SystemPrompt, "[Task Context]") {
		t.Fatalf("system prompt missing [Task Context] block")
	}
	if !strings.Contains(assembled.SystemPrompt, "Task: OC-5: Build OtterCamp sales landing page") {
		t.Fatalf("system prompt missing task title")
	}
	if !strings.Contains(assembled.SystemPrompt, "Status: in_progress") {
		t.Fatalf("system prompt missing task status")
	}
	if !strings.Contains(assembled.SystemPrompt, "Current Flow Step: Implement (active)") {
		t.Fatalf("system prompt missing current flow step")
	}
	if !strings.Contains(assembled.SystemPrompt, "Subtasks:\n- [done] Set up Next.js project") {
		t.Fatalf("system prompt missing subtasks block")
	}
}

func TestPromptAssemblerOmitsTaskContextForOrgScope(t *testing.T) {
	orgID := uuid.New()
	assembler := mustUnitAssembler(t, unitAssemblerConfig{
		session:  repo.ChatSession{ID: uuid.New(), OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Mode: "sync"},
		agent:    repo.Agent{ID: uuid.New(), OrganizationID: orgID, SystemPrompt: "Agent"},
		messages: []repo.ChatMessage{{SequenceNumber: 1, Role: "user", Content: "context"}},
	})

	assembled, err := assembler.Assemble(context.Background(), AssemblyInput{
		SessionID: assembler.sessions.(*fakeSessionRepo).session.ID,
		AgentID:   assembler.agents.(*fakeAgentRepo).agent.ID,
	})
	if err != nil {
		t.Fatalf("Assemble error = %v", err)
	}
	if strings.Contains(assembled.SystemPrompt, "[Task Context]") {
		t.Fatalf("system prompt unexpectedly contains [Task Context] block")
	}
}

func TestPromptAssemblerLayer2RequiresDirectContextInspection(t *testing.T) {
	orgID := uuid.New()
	assembler := mustUnitAssembler(t, unitAssemblerConfig{
		session:  repo.ChatSession{ID: uuid.New(), OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Mode: "sync"},
		agent:    repo.Agent{ID: uuid.New(), OrganizationID: orgID, SystemPrompt: "Agent"},
		messages: []repo.ChatMessage{{SequenceNumber: 1, Role: "user", Content: "Please continue the project."}},
	})

	assembled, err := assembler.Assemble(context.Background(), AssemblyInput{
		SessionID: assembler.sessions.(*fakeSessionRepo).session.ID,
		AgentID:   assembler.agents.(*fakeAgentRepo).agent.ID,
	})
	if err != nil {
		t.Fatalf("Assemble error = %v", err)
	}
	if !strings.Contains(assembled.SystemPrompt, "inspect those materials yourself") {
		t.Fatalf("system prompt missing direct-inspection guidance:\n%s", assembled.SystemPrompt)
	}
	if !strings.Contains(assembled.SystemPrompt, "Do not ask the operator to go read existing docs") {
		t.Fatalf("system prompt missing anti-doc-bounce guidance:\n%s", assembled.SystemPrompt)
	}
	if !strings.Contains(assembled.SystemPrompt, "Ask the operator only for information that is truly unavailable") {
		t.Fatalf("system prompt missing unavailable-info guard:\n%s", assembled.SystemPrompt)
	}
}

func TestPromptAssemblerTaskContextBlockFormat(t *testing.T) {
	assembled := assembleTaskContextPrompt(t)
	expectedLines := []string{
		"[Task Context]",
		"Task: OC-5: Build OtterCamp sales landing page",
		"Project: Landing Site",
		"Status: in_progress",
		"Description: Create a responsive landing page for OtterCamp.",
		"Acceptance Criteria:",
		"- Mobile responsive design",
		"- Page load < 2s",
		"Current Flow Step: Implement (active)",
		"Completed Flow Steps:",
		"- Plan",
		"Pending Flow Steps:",
		"- Review",
		"Subtasks:",
		"- [done] Set up Next.js project",
		"- [active] Build hero section",
		"- [pending] Add pricing table",
		"Dependencies:",
		"- OC-4: Finalize copy deck",
	}
	offset := 0
	for _, want := range expectedLines {
		next := strings.Index(assembled.SystemPrompt[offset:], want)
		if next < 0 {
			t.Fatalf("task context block missing %q after offset %d.\nprompt:\n%s", want, offset, assembled.SystemPrompt)
		}
		offset += next + len(want)
	}
}

func TestPromptAssemblerProjectContextIncludesLinkedPlanningArtifacts(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	assembler := mustUnitAssembler(t, unitAssemblerConfig{
		session:  repo.ChatSession{ID: uuid.New(), OrganizationID: orgID, ScopeType: "project", ScopeID: projectID, Mode: "sync"},
		agent:    repo.Agent{ID: uuid.New(), OrganizationID: orgID, SystemPrompt: "Agent"},
		messages: []repo.ChatMessage{{SequenceNumber: 1, Role: "user", Content: "context"}},
	})
	assembler.projects = &fakeProjectRepo{
		projects: map[uuid.UUID]repo.Project{
			projectID: {ID: projectID, OrganizationID: orgID, DisplayName: "Landing Site", Description: "Marketing website refresh"},
		},
	}
	assembler.planningAssets = &fakePlanningArtifactRepo{
		byProject: map[uuid.UUID][]repo.PlanningArtifact{
			projectID: {
				{
					ID:             uuid.New(),
					OrganizationID: orgID,
					ProjectID:      projectID,
					ArtifactKind:   taskplan.ArtifactKindPRDSpec,
					ArtifactSlug:   "prd",
					Title:          "PRD / requirements spec",
					RepoPath:       "planning/prd-spec/oc-7-prd.md",
					CurrentVersion: 2,
				},
			},
		},
	}

	assembled, err := assembler.Assemble(context.Background(), AssemblyInput{
		SessionID: assembler.sessions.(*fakeSessionRepo).session.ID,
		AgentID:   assembler.agents.(*fakeAgentRepo).agent.ID,
	})
	if err != nil {
		t.Fatalf("Assemble error = %v", err)
	}
	if !strings.Contains(assembled.SystemPrompt, "Planning Artifacts:\n- PRD / requirements spec [prd_spec] v2 @ planning/prd-spec/oc-7-prd.md") {
		t.Fatalf("system prompt missing linked planning artifact:\n%s", assembled.SystemPrompt)
	}
}

func TestPromptAssemblerTaskContextIncludesLinkedPlanningArtifacts(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	assembler := mustUnitAssembler(t, unitAssemblerConfig{
		session:  repo.ChatSession{ID: uuid.New(), OrganizationID: orgID, ScopeType: "project_task", ScopeID: taskID, Mode: "sync"},
		agent:    repo.Agent{ID: uuid.New(), OrganizationID: orgID, SystemPrompt: "Agent"},
		messages: []repo.ChatMessage{{SequenceNumber: 1, Role: "user", Content: "context"}},
	})
	assembler.projects = &fakeProjectRepo{
		projects: map[uuid.UUID]repo.Project{
			projectID: {ID: projectID, OrganizationID: orgID, DisplayName: "Billing Platform"},
		},
	}
	assembler.tasks = &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				TaskNumber:     12,
				Title:          "Implement billing migration",
				Description:    strPtr("Ship the approved billing migration safely."),
				WorkStatus:     "draft",
				Metadata:       json.RawMessage(`{}`),
			},
		},
	}
	assembler.planningAssets = &fakePlanningArtifactRepo{
		byTask: map[uuid.UUID][]repo.PlanningArtifact{
			taskID: {
				{
					ID:             uuid.New(),
					OrganizationID: orgID,
					ProjectID:      projectID,
					SourceTaskID:   taskID,
					ArtifactKind:   taskplan.ArtifactKindPRDSpec,
					ArtifactSlug:   "prd",
					Title:          "PRD / requirements spec",
					RepoPath:       "planning/prd-spec/oc-11-prd.md",
					CurrentVersion: 3,
				},
			},
		},
	}

	assembled, err := assembler.Assemble(context.Background(), AssemblyInput{
		SessionID: assembler.sessions.(*fakeSessionRepo).session.ID,
		AgentID:   assembler.agents.(*fakeAgentRepo).agent.ID,
	})
	if err != nil {
		t.Fatalf("Assemble error = %v", err)
	}
	if !strings.Contains(assembled.SystemPrompt, "Relevant Planning Artifacts:\n- PRD / requirements spec [prd_spec] v3 @ planning/prd-spec/oc-11-prd.md") {
		t.Fatalf("system prompt missing task-linked planning artifact:\n%s", assembled.SystemPrompt)
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
		session:      repo.ChatSession{ID: uuid.New(), OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Mode: "sync"},
		agent:        repo.Agent{ID: uuid.New(), OrganizationID: orgID, SystemPrompt: "Agent"},
		messages:     []repo.ChatMessage{},
		summaries:    []repo.ChatSummary{{FromSequence: 1, ToSequence: 100, SummaryText: strings.Repeat("summary ", 1400)}},
		modelProfile: repo.ModelProfile{LogicalProfileID: "main", ContextWindowTokens: 256},
	})

	_, err := assembler.Assemble(context.Background(), AssemblyInput{
		SessionID:      assembler.sessions.(*fakeSessionRepo).session.ID,
		AgentID:        assembler.agents.(*fakeAgentRepo).agent.ID,
		ModelProfileID: "main",
	})
	if !errors.Is(err, ErrContextCompressed) {
		t.Fatalf("Assemble error = %v, want ErrContextCompressed", err)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens("hello world"); got != 2 {
		t.Fatalf("estimateTokens(hello world) = %d, want 2", got)
	}
}

func TestPromptAssemblerEnqueueSummarizationUsesBackgroundPriority(t *testing.T) {
	assembler := mustUnitAssembler(t, unitAssemblerConfig{
		session: repo.ChatSession{ID: uuid.New(), ScopeID: uuid.New()},
		agent:   repo.Agent{ID: uuid.New()},
	})
	enqueuer, ok := assembler.enqueuer.(*fakeEnqueuer)
	if !ok {
		t.Fatalf("enqueuer = %T, want *fakeEnqueuer", assembler.enqueuer)
	}

	if err := assembler.enqueueSummarization(context.Background(), assembler.sessions.(*fakeSessionRepo).session.ID, 130000); err != nil {
		t.Fatalf("enqueueSummarization: %v", err)
	}
	if enqueuer.jobType != chat.ChatSummarizeJobType {
		t.Fatalf("job type = %q, want %q", enqueuer.jobType, chat.ChatSummarizeJobType)
	}
	if enqueuer.priority != defaultSummarizationJobPrio {
		t.Fatalf("priority = %d, want %d", enqueuer.priority, defaultSummarizationJobPrio)
	}
	if enqueuer.priority >= 70 {
		t.Fatalf("priority = %d, want below agent_turn priority", enqueuer.priority)
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

func assembleTaskContextPrompt(t *testing.T) *AssembledPrompt {
	t.Helper()

	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	depTaskID := uuid.New()
	flowTemplateID := uuid.New()
	nodePlanID := uuid.New()
	nodeImplementID := uuid.New()
	nodeReviewID := uuid.New()
	activeExecutionID := uuid.New()

	taskMetadata := json.RawMessage(`{"acceptance_criteria":["Mobile responsive design","Page load < 2s"]}`)
	currentFlowNodeID := nodeImplementID
	taskFlowTemplateID := flowTemplateID

	assembler := mustUnitAssembler(t, unitAssemblerConfig{
		session: repo.ChatSession{
			ID:             uuid.New(),
			OrganizationID: orgID,
			ScopeType:      "project_task",
			ScopeID:        taskID,
			Mode:           "async",
		},
		agent: repo.Agent{
			ID:             uuid.New(),
			OrganizationID: orgID,
			SystemPrompt:   "Agent",
		},
		messages: []repo.ChatMessage{
			{SequenceNumber: 1, Role: "user", Content: "start"},
		},
	})

	assembler.projects = &fakeProjectRepo{
		projects: map[uuid.UUID]repo.Project{
			projectID: {ID: projectID, OrganizationID: orgID, DisplayName: "Landing Site"},
		},
	}
	assembler.tasks = &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    orgID,
				ProjectID:         projectID,
				TaskNumber:        5,
				Title:             "Build OtterCamp sales landing page",
				Description:       strPtr("Create a responsive landing page for OtterCamp."),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &currentFlowNodeID,
				FlowTemplateID:    &taskFlowTemplateID,
				Metadata:          taskMetadata,
			},
			depTaskID: {
				ID:             depTaskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				TaskNumber:     4,
				Title:          "Finalize copy deck",
			},
		},
	}
	assembler.flowNodes = &fakeFlowNodeRepo{
		nodesByID: map[uuid.UUID]repo.FlowNode{
			nodePlanID:      {ID: nodePlanID, FlowTemplateID: flowTemplateID, DisplayName: "Plan"},
			nodeImplementID: {ID: nodeImplementID, FlowTemplateID: flowTemplateID, DisplayName: "Implement"},
			nodeReviewID:    {ID: nodeReviewID, FlowTemplateID: flowTemplateID, DisplayName: "Review"},
		},
		nodesByTemplate: map[uuid.UUID][]repo.FlowNode{
			flowTemplateID: {
				{ID: nodePlanID, FlowTemplateID: flowTemplateID, DisplayName: "Plan", Position: 1},
				{ID: nodeImplementID, FlowTemplateID: flowTemplateID, DisplayName: "Implement", Position: 2},
				{ID: nodeReviewID, FlowTemplateID: flowTemplateID, DisplayName: "Review", Position: 3},
			},
		},
	}
	assembler.flowExecutions = &fakeFlowExecutionRepo{
		activeByTaskAndNode: map[string]repo.FlowNodeExecution{
			taskID.String() + "|" + nodeImplementID.String(): {
				ID:         activeExecutionID,
				TaskID:     taskID,
				FlowNodeID: nodeImplementID,
				Status:     "active",
			},
		},
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{ID: uuid.New(), TaskID: taskID, FlowNodeID: nodePlanID, Status: "completed"},
				{ID: activeExecutionID, TaskID: taskID, FlowNodeID: nodeImplementID, Status: "active"},
			},
		},
	}
	assembler.subtasks = &fakeSubtaskRepo{
		byExecution: map[uuid.UUID][]repo.ProjectSubtask{
			activeExecutionID: {
				{TaskID: taskID, FlowNodeExecutionID: activeExecutionID, Title: "Set up Next.js project", WorkStatus: "done", SequenceNumber: 1},
				{TaskID: taskID, FlowNodeExecutionID: activeExecutionID, Title: "Build hero section", WorkStatus: "active", SequenceNumber: 2},
				{TaskID: taskID, FlowNodeExecutionID: activeExecutionID, Title: "Add pricing table", WorkStatus: "pending", SequenceNumber: 3},
			},
		},
	}
	assembler.dependencies = &fakeDependencyRepo{
		outboundBySource: map[uuid.UUID][]repo.ProjectTaskDependency{
			taskID: {
				{
					ID:            uuid.New(),
					SourceType:    "project_task",
					SourceID:      taskID,
					DependsOnType: "project_task",
					DependsOnID:   depTaskID,
				},
			},
		},
	}

	assembled, err := assembler.Assemble(context.Background(), AssemblyInput{
		SessionID: assembler.sessions.(*fakeSessionRepo).session.ID,
		AgentID:   assembler.agents.(*fakeAgentRepo).agent.ID,
	})
	if err != nil {
		t.Fatalf("Assemble error = %v", err)
	}
	return assembled
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
		Subtasks:            &fakeSubtaskRepo{},
		Dependencies:        &fakeDependencyRepo{},
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

type fakeProjectRepo struct {
	projects map[uuid.UUID]repo.Project
}

func (f *fakeProjectRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Project, error) {
	if item, ok := f.projects[id]; ok {
		return item, nil
	}
	return repo.Project{}, repo.ErrNotFound
}

type fakeTaskRepo struct {
	tasks map[uuid.UUID]repo.ProjectTask
}

func (f *fakeTaskRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectTask, error) {
	if item, ok := f.tasks[id]; ok {
		return item, nil
	}
	return repo.ProjectTask{}, repo.ErrNotFound
}

type fakeFlowTemplateRepo struct{}

func (f *fakeFlowTemplateRepo) GetByID(context.Context, uuid.UUID) (repo.FlowTemplate, error) {
	return repo.FlowTemplate{}, repo.ErrNotFound
}

type fakeFlowNodeRepo struct {
	nodesByID       map[uuid.UUID]repo.FlowNode
	nodesByTemplate map[uuid.UUID][]repo.FlowNode
}

func (f *fakeFlowNodeRepo) GetByID(_ context.Context, id uuid.UUID) (repo.FlowNode, error) {
	if item, ok := f.nodesByID[id]; ok {
		return item, nil
	}
	return repo.FlowNode{}, repo.ErrNotFound
}

func (f *fakeFlowNodeRepo) GetByTemplateOrdered(_ context.Context, flowTemplateID uuid.UUID) ([]repo.FlowNode, error) {
	if rows, ok := f.nodesByTemplate[flowTemplateID]; ok {
		return append([]repo.FlowNode(nil), rows...), nil
	}
	return nil, repo.ErrNotFound
}

type fakeFlowExecutionRepo struct {
	activeByTaskAndNode map[string]repo.FlowNodeExecution
	byTask              map[uuid.UUID][]repo.FlowNodeExecution
}

func (f *fakeFlowExecutionRepo) GetActive(_ context.Context, taskID, flowNodeID uuid.UUID) (repo.FlowNodeExecution, error) {
	key := taskID.String() + "|" + flowNodeID.String()
	if item, ok := f.activeByTaskAndNode[key]; ok {
		return item, nil
	}
	return repo.FlowNodeExecution{}, repo.ErrNotFound
}

func (f *fakeFlowExecutionRepo) ListByTask(_ context.Context, taskID uuid.UUID) ([]repo.FlowNodeExecution, error) {
	if rows, ok := f.byTask[taskID]; ok {
		return append([]repo.FlowNodeExecution(nil), rows...), nil
	}
	return nil, repo.ErrNotFound
}

type fakeFlowNodeSkillRepo struct{}

func (f *fakeFlowNodeSkillRepo) ListByNode(context.Context, uuid.UUID) ([]repo.FlowNodeSkill, error) {
	return nil, nil
}

type fakeSubtaskRepo struct {
	byExecution map[uuid.UUID][]repo.ProjectSubtask
}

func (f *fakeSubtaskRepo) ListByExecution(_ context.Context, flowNodeExecutionID uuid.UUID) ([]repo.ProjectSubtask, error) {
	if rows, ok := f.byExecution[flowNodeExecutionID]; ok {
		return append([]repo.ProjectSubtask(nil), rows...), nil
	}
	return []repo.ProjectSubtask{}, nil
}

type fakeDependencyRepo struct {
	outboundBySource map[uuid.UUID][]repo.ProjectTaskDependency
}

func (f *fakeDependencyRepo) ListOutbound(_ context.Context, sourceType string, sourceID uuid.UUID) ([]repo.ProjectTaskDependency, error) {
	if strings.TrimSpace(sourceType) != "project_task" {
		return []repo.ProjectTaskDependency{}, nil
	}
	if rows, ok := f.outboundBySource[sourceID]; ok {
		return append([]repo.ProjectTaskDependency(nil), rows...), nil
	}
	return []repo.ProjectTaskDependency{}, nil
}

type fakePlanningArtifactRepo struct {
	byProject map[uuid.UUID][]repo.PlanningArtifact
	byTask    map[uuid.UUID][]repo.PlanningArtifact
}

func (f *fakePlanningArtifactRepo) ListByProject(_ context.Context, projectID uuid.UUID) ([]repo.PlanningArtifact, error) {
	if rows, ok := f.byProject[projectID]; ok {
		return append([]repo.PlanningArtifact(nil), rows...), nil
	}
	return nil, nil
}

func (f *fakePlanningArtifactRepo) ListBySourceTask(_ context.Context, taskID uuid.UUID) ([]repo.PlanningArtifact, error) {
	if rows, ok := f.byTask[taskID]; ok {
		return append([]repo.PlanningArtifact(nil), rows...), nil
	}
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

type fakeEnqueuer struct {
	jobType  string
	priority int
}

func (f *fakeEnqueuer) Enqueue(_ context.Context, _ pgx.Tx, jobType string, priority int, _ any, _ *time.Time) (uuid.UUID, error) {
	f.jobType = jobType
	f.priority = priority
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

func strPtr(value string) *string {
	copyValue := value
	return &copyValue
}

func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func TestSummarizeHistorySkipsFailedMessages(t *testing.T) {
	summary := summarizeHistory([]repo.ChatMessage{
		{
			SequenceNumber: 1,
			Role:           "user",
			Content:        "Keep going.",
			Status:         "final",
		},
		{
			SequenceNumber: 2,
			Role:           "assistant",
			Content:        "",
			Status:         "failed",
		},
		{
			SequenceNumber: 3,
			Role:           "system",
			Content:        "[Continuation summary] Continuation summary unavailable.",
			Status:         "final",
		},
	}, nil)

	if len(summary) != 2 {
		t.Fatalf("summary len = %d, want 2", len(summary))
	}
	if summary[0].Role != "user" || summary[0].Content != "Keep going." {
		t.Fatalf("summary[0] = %#v, want user message", summary[0])
	}
	if summary[1].Role != "system" || !strings.Contains(summary[1].Content, "Continuation summary unavailable") {
		t.Fatalf("summary[1] = %#v, want continuation system message", summary[1])
	}
}

func TestSummarizeHistoryIncludesPendingMessages(t *testing.T) {
	summary := summarizeHistory([]repo.ChatMessage{
		{
			SequenceNumber: 1,
			Role:           "user",
			Content:        "Keep going.",
			Status:         "final",
		},
		{
			SequenceNumber: 2,
			Role:           "system",
			Content:        "[Continuation summary] stale placeholder",
			Status:         "pending",
		},
		{
			SequenceNumber: 3,
			Role:           "assistant",
			Content:        "placeholder",
			Status:         "failed",
		},
		{
			SequenceNumber: 4,
			Role:           "tool_result",
			Content:        "{\"ok\":true}",
			Status:         "final",
		},
	}, nil)

	if len(summary) != 3 {
		t.Fatalf("summary len = %d, want 3", len(summary))
	}
	if summary[0].Role != "user" || summary[0].Content != "Keep going." {
		t.Fatalf("summary[0] = %#v, want user message", summary[0])
	}
	if summary[1].Role != "system" || summary[1].Content != "[Continuation summary] stale placeholder" {
		t.Fatalf("summary[1] = %#v, want pending system message", summary[1])
	}
	if summary[2].Role != "tool_result" || summary[2].Content != "{\"ok\":true}" {
		t.Fatalf("summary[2] = %#v, want durable tool_result message", summary[2])
	}
}
