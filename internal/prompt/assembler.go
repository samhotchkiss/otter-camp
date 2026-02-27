package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/policy"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/tools"
)

var ErrContextCompressed = errors.New("prompt context compressed")

const (
	defaultSystemPrompt          = "You are a helpful AI assistant operating within the OtterCamp system."
	defaultSkillsDir             = "./skills"
	defaultMemoryBudgetTokens    = 8000
	defaultLayer6BudgetTokens    = 12000
	defaultLayer7BudgetTokens    = 12000
	defaultLayer4BudgetTokens    = 2000
	defaultLayer3BudgetTokens    = 500
	defaultContextWindowTokens   = 16384
	defaultSummarizationJobPrio  = 80
	defaultPromptMessageOverhead = 4
	mcpPromptWordSample          = 30
	mcpPromptOverlapThreshold    = 3
	truncatedSuffix              = "[truncated]"
)

var coreTier1Tools = map[string]struct{}{
	"memory.query": {},
	"file.read":    {},
	"file.write":   {},
	"file.list":    {},
	"chat.search":  {},
}

// PromptToolCall carries a tool call from an assistant message so the
// conversation history is valid for providers that require a preceding
// assistant+tool_calls message before any tool result messages.
type PromptToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type PromptMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	ToolCallID *string         `json:"tool_call_id,omitempty"`
	ToolCalls  []PromptToolCall `json:"tool_calls,omitempty"`
}

type MemoryManifest struct {
	InjectedMemoryIDs []uuid.UUID `json:"injected_memory_ids"`
	TotalMemories     int         `json:"total_memories"`
	BudgetUsed        int         `json:"budget_used"`
}

type AssemblyInput struct {
	SessionID        uuid.UUID
	TurnID           uuid.UUID
	AgentID          uuid.UUID
	ModelProfileID   string
	ToolDescriptors  []tools.ToolDescriptor
	PreviousManifest *MemoryManifest
}

type AssembledPrompt struct {
	Messages        []PromptMessage        `json:"messages"`
	SystemPrompt    string                 `json:"system_prompt"`
	TotalTokens     int                    `json:"total_tokens"`
	LayerTokens     map[string]int         `json:"layer_tokens"`
	MemoryManifest  MemoryManifest         `json:"memory_manifest"`
	Errors          []string               `json:"errors"`
	ToolDescriptors []tools.ToolDescriptor `json:"tool_descriptors"`
}

type chatSessionRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ChatSession, error)
}

type chatMessageRepository interface {
	ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatMessage, error)
}

type chatSummaryRepository interface {
	ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatSummary, error)
}

type organizationRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Organization, error)
}

type projectRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

type taskRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
}

type flowTemplateRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowTemplate, error)
}

type flowNodeRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowNode, error)
}

type flowNodeExecutionRepository interface {
	GetActive(ctx context.Context, taskID, flowNodeID uuid.UUID) (repo.FlowNodeExecution, error)
}

type flowNodeSkillRepository interface {
	ListByNode(ctx context.Context, flowNodeID uuid.UUID) ([]repo.FlowNodeSkill, error)
}

type agentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

type agentSkillAttachmentRepository interface {
	ListByAgent(ctx context.Context, agentID uuid.UUID) ([]repo.AgentSkillAttachment, error)
}

type skillRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Skill, error)
}

type modelProfileRepository interface {
	GetCurrentByLogicalID(ctx context.Context, organizationID uuid.UUID, logicalProfileID string) (repo.ModelProfile, error)
}

type mcpConnectionRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.MCPConnection, error)
}

type memoryRetriever interface {
	Query(ctx context.Context, req memory.RetrievalRequest) (memory.RetrievalResult, error)
}

type summarizationChecker interface {
	ShouldSummarize(ctx context.Context, sessionID uuid.UUID, layerBudgetTokens int) (bool, error)
}

type jobEnqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error)
}

type policyEvaluator interface {
	Evaluate(ctx context.Context, req policy.EvaluationRequest) policy.PolicyDecision
}

type MCPPrompt struct {
	Name        string
	Description string
	Content     string
}

type MCPPromptLister interface {
	ListPrompts(ctx context.Context, connection repo.MCPConnection) ([]MCPPrompt, error)
}

type AssemblerOptions struct {
	Pool *pgxpool.Pool

	Sessions        chatSessionRepository
	Messages        chatMessageRepository
	Summaries       chatSummaryRepository
	Organizations   organizationRepository
	Projects        projectRepository
	Tasks           taskRepository
	FlowTemplates   flowTemplateRepository
	FlowNodes       flowNodeRepository
	FlowExecutions  flowNodeExecutionRepository
	FlowNodeSkills  flowNodeSkillRepository
	Agents          agentRepository
	AgentSkills     agentSkillAttachmentRepository
	Skills          skillRepository
	ModelProfiles   modelProfileRepository
	MCPConnections  mcpConnectionRepository
	MemoryRetriever memoryRetriever
	Summarization   summarizationChecker
	Enqueuer        jobEnqueuer
	Policies        policyEvaluator
	MCPPromptLister MCPPromptLister

	SkillsDir           string
	DefaultMemoryBudget int
	DefaultLayer6Budget int
	DefaultLayer7Budget int
	Logger              *slog.Logger
}

type mcpPromptCacheKey struct {
	sessionID    uuid.UUID
	connectionID uuid.UUID
}

type PromptAssembler struct {
	sessions       chatSessionRepository
	messages       chatMessageRepository
	summaries      chatSummaryRepository
	organizations  organizationRepository
	projects       projectRepository
	tasks          taskRepository
	flowTemplates  flowTemplateRepository
	flowNodes      flowNodeRepository
	flowExecutions flowNodeExecutionRepository
	flowNodeSkills flowNodeSkillRepository
	agents         agentRepository
	agentSkills    agentSkillAttachmentRepository
	skills         skillRepository
	modelProfiles  modelProfileRepository
	mcpConnections mcpConnectionRepository

	memoryRetriever memoryRetriever
	summarization   summarizationChecker
	enqueuer        jobEnqueuer
	policies        policyEvaluator
	mcpPromptLister MCPPromptLister

	skillsDir           string
	defaultMemoryBudget int
	defaultLayer6Budget int
	defaultLayer7Budget int

	logger *slog.Logger

	mcpPromptCacheMu sync.RWMutex
	mcpPromptCache   map[mcpPromptCacheKey][]MCPPrompt
}

type fallbackMemoryRetriever struct {
	memories *repo.MemoryRepo
}

func (r *fallbackMemoryRetriever) Query(ctx context.Context, req memory.RetrievalRequest) (memory.RetrievalResult, error) {
	if r == nil || r.memories == nil {
		return memory.RetrievalResult{}, nil
	}
	filter := repo.RetrievalFilter{
		OrganizationID: req.OrganizationID,
		Statuses:       []string{"active"},
		Scopes:         []string{"org", "project", "task"},
		Limit:          req.MaxResults,
	}
	normal := "normal"
	filter.Sensitivity = &normal
	rows, err := r.memories.ListForRetrieval(ctx, filter)
	if err != nil {
		return memory.RetrievalResult{}, err
	}
	ranked := make([]memory.RankedMemory, 0, len(rows))
	for _, item := range rows {
		score := (item.UtilityScore * 0.6) + (item.Confidence * 0.4)
		ranked = append(ranked, memory.RankedMemory{Memory: item, Score: score, CosineSim: score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].Memory.CreatedAt.After(ranked[j].Memory.CreatedAt)
		}
		return ranked[i].Score > ranked[j].Score
	})
	return memory.RetrievalResult{Memories: ranked}, nil
}

func NewPromptAssembler(opts AssemblerOptions) (*PromptAssembler, error) {
	requiresPool := opts.Sessions == nil ||
		opts.Messages == nil ||
		opts.Summaries == nil ||
		opts.Agents == nil ||
		opts.ModelProfiles == nil
	if requiresPool && opts.Pool == nil {
		return nil, fmt.Errorf("prompt assembler requires pool or explicit repositories")
	}

	a := &PromptAssembler{
		sessions:            opts.Sessions,
		messages:            opts.Messages,
		summaries:           opts.Summaries,
		organizations:       opts.Organizations,
		projects:            opts.Projects,
		tasks:               opts.Tasks,
		flowTemplates:       opts.FlowTemplates,
		flowNodes:           opts.FlowNodes,
		flowExecutions:      opts.FlowExecutions,
		flowNodeSkills:      opts.FlowNodeSkills,
		agents:              opts.Agents,
		agentSkills:         opts.AgentSkills,
		skills:              opts.Skills,
		modelProfiles:       opts.ModelProfiles,
		mcpConnections:      opts.MCPConnections,
		memoryRetriever:     opts.MemoryRetriever,
		summarization:       opts.Summarization,
		enqueuer:            opts.Enqueuer,
		policies:            opts.Policies,
		mcpPromptLister:     opts.MCPPromptLister,
		skillsDir:           strings.TrimSpace(opts.SkillsDir),
		defaultMemoryBudget: opts.DefaultMemoryBudget,
		defaultLayer6Budget: opts.DefaultLayer6Budget,
		defaultLayer7Budget: opts.DefaultLayer7Budget,
		logger:              opts.Logger,
		mcpPromptCache:      make(map[mcpPromptCacheKey][]MCPPrompt),
	}

	if a.logger == nil {
		a.logger = slog.Default()
	}
	if a.skillsDir == "" {
		a.skillsDir = strings.TrimSpace(os.Getenv("OTTERCAMP_SKILLS_DIR"))
	}
	if a.skillsDir == "" {
		a.skillsDir = defaultSkillsDir
	}
	if a.defaultMemoryBudget <= 0 {
		a.defaultMemoryBudget = defaultMemoryBudgetTokens
	}
	if a.defaultLayer6Budget <= 0 {
		a.defaultLayer6Budget = defaultLayer6BudgetTokens
	}
	if a.defaultLayer7Budget <= 0 {
		a.defaultLayer7Budget = defaultLayer7BudgetTokens
	}

	if a.sessions == nil {
		a.sessions = repo.NewChatSessionRepo(opts.Pool)
	}
	if a.messages == nil {
		a.messages = repo.NewChatMessageRepo(opts.Pool)
	}
	if a.summaries == nil {
		a.summaries = repo.NewChatSummaryRepo(opts.Pool)
	}
	if a.organizations == nil {
		a.organizations = repo.NewOrgRepo(opts.Pool)
	}
	if a.projects == nil {
		a.projects = repo.NewProjectRepo(opts.Pool)
	}
	if a.tasks == nil {
		a.tasks = repo.NewProjectTaskRepo(opts.Pool)
	}
	if a.flowTemplates == nil {
		a.flowTemplates = repo.NewFlowTemplateRepo(opts.Pool)
	}
	if a.flowNodes == nil {
		a.flowNodes = repo.NewFlowNodeRepo(opts.Pool)
	}
	if a.flowExecutions == nil {
		a.flowExecutions = repo.NewFlowNodeExecutionRepo(opts.Pool)
	}
	if a.flowNodeSkills == nil {
		a.flowNodeSkills = repo.NewFlowNodeSkillRepo(opts.Pool)
	}
	if a.agents == nil {
		a.agents = repo.NewAgentRepo(opts.Pool)
	}
	if a.agentSkills == nil {
		a.agentSkills = repo.NewAgentSkillAttachmentRepo(opts.Pool)
	}
	if a.skills == nil {
		a.skills = repo.NewSkillRepo(opts.Pool)
	}
	if a.modelProfiles == nil {
		a.modelProfiles = repo.NewModelProfileRepo(opts.Pool)
	}
	if a.mcpConnections == nil {
		a.mcpConnections = repo.NewMCPConnectionRepo(opts.Pool)
	}
	if a.memoryRetriever == nil && opts.Pool != nil {
		a.memoryRetriever = &fallbackMemoryRetriever{memories: repo.NewMemoryRepo(opts.Pool)}
	}
	if a.summarization == nil && opts.Pool != nil {
		checker, err := chat.NewSummarizationChecker(chat.SummarizationCheckerOptions{Pool: opts.Pool})
		if err != nil {
			return nil, err
		}
		a.summarization = checker
	}
	if a.enqueuer == nil && opts.Pool != nil {
		a.enqueuer = jobqueue.New(opts.Pool, a.logger, jobqueue.Config{})
	}

	return a, nil
}

func (a *PromptAssembler) Assemble(ctx context.Context, input AssemblyInput) (*AssembledPrompt, error) {
	if a == nil {
		return nil, fmt.Errorf("prompt assembler is not configured")
	}
	if input.SessionID == uuid.Nil {
		return nil, fmt.Errorf("session_id is required")
	}
	if input.AgentID == uuid.Nil {
		return nil, fmt.Errorf("agent_id is required")
	}

	session, err := a.sessions.GetByID(ctx, input.SessionID)
	if err != nil {
		return nil, err
	}
	agentRecord, err := a.agents.GetByID(ctx, input.AgentID)
	if err != nil {
		return nil, err
	}

	modelProfile, contextWindow := a.resolveModelProfile(ctx, session.OrganizationID, strings.TrimSpace(input.ModelProfileID))
	memoryBudget := a.resolveMemoryBudget(modelProfile, contextWindow)
	layer6Budget := a.resolveLayer6Budget(contextWindow)

	messages, err := a.messages.ListBySession(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	summaries, err := a.summaries.ListBySession(ctx, session.ID)
	if err != nil {
		return nil, err
	}

	assembled := &AssembledPrompt{
		LayerTokens: map[string]int{
			"layer1": 0,
			"layer2": 0,
			"layer3": 0,
			"layer4": 0,
			"layer5": 0,
			"layer6": 0,
			"layer7": 0,
		},
		Errors:          make([]string, 0),
		ToolDescriptors: append([]tools.ToolDescriptor(nil), input.ToolDescriptors...),
	}

	layer1 := buildLayer1(agentRecord)
	layer2 := a.buildLayer2(ctx, session, agentRecord, input.ToolDescriptors)
	layer3 := a.buildLayer3(ctx, session)
	layer4 := a.buildLayer4(ctx, session, agentRecord.ID, input.ToolDescriptors, &assembled.Errors)
	layer5, manifest := a.buildLayer5(ctx, session, agentRecord.ID, messages, input.PreviousManifest, memoryBudget)
	assembled.MemoryManifest = manifest

	historyMessages, layer6Compressed := a.buildLayer6(messages, summaries, layer6Budget)
	if shouldSummarize, summarizeErr := a.shouldSummarize(ctx, session.ID, layer6Budget); summarizeErr != nil {
		assembled.Errors = append(assembled.Errors, summarizeErr.Error())
	} else if shouldSummarize {
		if enqueueErr := a.enqueueSummarization(ctx, session.ID, layer6Budget); enqueueErr != nil {
			assembled.Errors = append(assembled.Errors, enqueueErr.Error())
		}
	}

	assembled.ToolDescriptors = reduceToolsToBudget(assembled.ToolDescriptors, a.defaultLayer7Budget)

	systemPrompt := joinSections(layer1, layer2, layer3, layer4, layer5)
	assembled.SystemPrompt = systemPrompt
	assembled.Messages = make([]PromptMessage, 0, len(historyMessages)+1)
	assembled.Messages = append(assembled.Messages, PromptMessage{Role: "system", Content: systemPrompt})
	assembled.Messages = append(assembled.Messages, historyMessages...)

	assembled.LayerTokens["layer1"] = estimateTokens(layer1)
	assembled.LayerTokens["layer2"] = estimateTokens(layer2)
	assembled.LayerTokens["layer3"] = estimateTokens(layer3)
	assembled.LayerTokens["layer4"] = estimateTokens(layer4)
	assembled.LayerTokens["layer5"] = estimateTokens(layer5)
	assembled.LayerTokens["layer6"] = estimatePromptMessagesTokens(historyMessages)
	assembled.LayerTokens["layer7"] = estimateToolsTokens(assembled.ToolDescriptors)

	assembled.TotalTokens = sumLayerTokens(assembled.LayerTokens)
	if contextWindow <= 0 {
		contextWindow = defaultContextWindowTokens
	}
	for assembled.TotalTokens > contextWindow {
		next := dropLowestPriorityTool(assembled.ToolDescriptors)
		if len(next) == len(assembled.ToolDescriptors) {
			break
		}
		assembled.ToolDescriptors = next
		assembled.LayerTokens["layer7"] = estimateToolsTokens(assembled.ToolDescriptors)
		assembled.TotalTokens = sumLayerTokens(assembled.LayerTokens)
	}

	if layer6Compressed {
		return assembled, ErrContextCompressed
	}
	return assembled, nil
}

func (a *PromptAssembler) resolveModelProfile(ctx context.Context, organizationID uuid.UUID, logicalProfileID string) (*repo.ModelProfile, int) {
	if strings.TrimSpace(logicalProfileID) == "" || a.modelProfiles == nil {
		return nil, defaultContextWindowTokens
	}
	item, err := a.modelProfiles.GetCurrentByLogicalID(ctx, organizationID, logicalProfileID)
	if err != nil {
		a.logger.Warn("prompt assembler could not load model profile", "logical_profile_id", logicalProfileID, "error", err)
		return nil, defaultContextWindowTokens
	}
	window := item.ContextWindowTokens
	if window <= 0 {
		window = defaultContextWindowTokens
	}
	return &item, window
}

func (a *PromptAssembler) resolveMemoryBudget(profile *repo.ModelProfile, contextWindow int) int {
	if a.defaultMemoryBudget > 0 {
		if profile == nil {
			return a.defaultMemoryBudget
		}
		adaptive := int(float64(contextWindow) * 0.35)
		if adaptive <= 0 {
			return a.defaultMemoryBudget
		}
		if adaptive < a.defaultMemoryBudget {
			return adaptive
		}
		return a.defaultMemoryBudget
	}
	return defaultMemoryBudgetTokens
}

func (a *PromptAssembler) resolveLayer6Budget(contextWindow int) int {
	if contextWindow <= 0 {
		contextWindow = defaultContextWindowTokens
	}
	adaptive := int(float64(contextWindow) * 0.65)
	if adaptive < 1024 {
		adaptive = 1024
	}
	if a.defaultLayer6Budget > 0 && a.defaultLayer6Budget < adaptive {
		return a.defaultLayer6Budget
	}
	return adaptive
}

func buildLayer1(agentRecord repo.Agent) string {
	systemPrompt := strings.TrimSpace(agentRecord.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = defaultSystemPrompt
	}
	operator := strings.TrimSpace(agentRecord.OperatorInstructions)
	if operator == "" {
		return systemPrompt
	}
	return systemPrompt + "\n\nOperator instructions:\n" + operator
}

func (a *PromptAssembler) buildLayer2(ctx context.Context, session repo.ChatSession, agentRecord repo.Agent, toolDescriptors []tools.ToolDescriptor) string {
	trustTier := "standard"
	if v := readTrustTier(session.Metadata); strings.TrimSpace(v) != "" {
		trustTier = v
	}
	lines := []string{
		"## Policies and Constraints",
		"Trust tier: " + trustTier + ".",
		"You have access to OtterCamp native tools. When the user asks you to perform an action — create a project, assign a task, manage workflows, send a message, etc. — USE your available tools to do it. Do not tell the user to do it themselves or say you cannot do it through this interface.",
	}

	if a.policies == nil {
		return strings.Join(lines, "\n")
	}

	projectID := projectIDFromScope(session)
	seen := map[string]struct{}{}
	denied := make([]string, 0)
	for _, toolDef := range toolDescriptors {
		capability := strings.TrimSpace(derefString(toolDef.Capability))
		if capability == "" {
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}

		decision := a.policies.Evaluate(ctx, policy.EvaluationRequest{
			OrganizationID: session.OrganizationID,
			ProjectID:      projectID,
			AgentID:        &agentRecord.ID,
			Capability:     capability,
			Context: map[string]any{
				"session_mode": session.Mode,
			},
		})
		if strings.EqualFold(decision.Effect, "deny") {
			reason := strings.TrimSpace(decision.Reason)
			if reason == "" {
				reason = "denied by policy"
			}
			if strings.Contains(strings.ToLower(capability), "shell") {
				denied = append(denied, "You are not permitted to execute shell commands.")
			} else {
				denied = append(denied, fmt.Sprintf("Capability '%s' denied: %s", capability, reason))
			}
		}
	}
	if len(denied) == 0 {
		return strings.Join(lines, "\n")
	}
	for _, item := range denied {
		lines = append(lines, "- "+item)
	}
	return strings.Join(lines, "\n")
}

func (a *PromptAssembler) buildLayer3(ctx context.Context, session repo.ChatSession) string {
	lines := []string{"## Current Context"}
	scopeType := strings.TrimSpace(session.ScopeType)

	switch scopeType {
	case "organization":
		if a.organizations != nil {
			org, err := a.organizations.GetByID(ctx, session.ScopeID)
			if err == nil {
				lines = append(lines, "Organization: "+strings.TrimSpace(org.DisplayName))
			} else {
				lines = append(lines, "Organization ID: "+session.ScopeID.String())
			}
		} else {
			lines = append(lines, "Organization ID: "+session.ScopeID.String())
		}
	case "project":
		projectName := ""
		projectDesc := ""
		flowTemplate := ""
		if a.projects != nil {
			projectRecord, err := a.projects.GetByID(ctx, session.ScopeID)
			if err == nil {
				projectName = strings.TrimSpace(projectRecord.DisplayName)
				projectDesc = strings.TrimSpace(projectRecord.Description)
				if projectRecord.DeployFlowTemplateID != nil && a.flowTemplates != nil {
					tpl, tplErr := a.flowTemplates.GetByID(ctx, *projectRecord.DeployFlowTemplateID)
					if tplErr == nil {
						flowTemplate = strings.TrimSpace(tpl.DisplayName)
					}
				}
			}
		}
		if projectName == "" {
			projectName = session.ScopeID.String()
		}
		lines = append(lines, "Project: "+projectName)
		if projectDesc != "" {
			lines = append(lines, "Project description: "+projectDesc)
		}
		if flowTemplate != "" {
			lines = append(lines, "Flow template: "+flowTemplate)
		}
	case "project_task":
		context, err := a.buildProjectTaskContext(ctx, session.ScopeID)
		if err != nil {
			lines = append(lines, "Project task ID: "+session.ScopeID.String())
			break
		}
		lines = append(lines, "Project: "+context.projectName)
		lines = append(lines, "Task: "+context.taskTitle)
		if context.taskDescription != "" {
			lines = append(lines, "Task description: "+context.taskDescription)
		}
		if context.nodeName != "" {
			lines = append(lines, "Flow node: "+context.nodeName)
		}
		if context.nodeDescription != "" {
			lines = append(lines, "Flow node description: "+context.nodeDescription)
		}
		if context.visitCount > 1 {
			lines = append(lines, fmt.Sprintf("Note: this node has been visited %d times previously.", context.visitCount))
		}
	default:
		lines = append(lines, "Scope: "+scopeType)
		lines = append(lines, "Scope ID: "+session.ScopeID.String())
	}

	layer3 := strings.Join(lines, "\n")
	if estimateTokens(layer3) <= defaultLayer3BudgetTokens || scopeType != "project_task" {
		return layer3
	}

	index := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "Task description: ") {
			index = i
			break
		}
	}
	if index < 0 {
		return layer3
	}

	prefix := "Task description: "
	full := strings.TrimPrefix(lines[index], prefix)
	others := append([]string(nil), lines...)
	others[index] = prefix
	baseTokens := estimateTokens(strings.Join(others, "\n"))
	allowTokens := defaultLayer3BudgetTokens - baseTokens - estimateTokens(truncatedSuffix)
	if allowTokens < 1 {
		lines[index] = prefix + truncatedSuffix
		return strings.Join(lines, "\n")
	}
	allowChars := allowTokens * 4
	if allowChars > len(full) {
		allowChars = len(full)
	}
	trimmed := strings.TrimSpace(full[:allowChars])
	if trimmed == "" {
		trimmed = truncatedSuffix
	} else if allowChars < len(full) {
		trimmed += " " + truncatedSuffix
	}
	lines[index] = prefix + trimmed
	return strings.Join(lines, "\n")
}

type projectTaskContext struct {
	projectName     string
	taskTitle       string
	taskDescription string
	nodeName        string
	nodeDescription string
	visitCount      int
	flowNodeID      *uuid.UUID
	flowTemplateID  *uuid.UUID
	organizationID  uuid.UUID
}

func (a *PromptAssembler) buildProjectTaskContext(ctx context.Context, taskID uuid.UUID) (projectTaskContext, error) {
	if a.tasks == nil {
		return projectTaskContext{}, fmt.Errorf("task repository is not configured")
	}
	taskRecord, err := a.tasks.GetByID(ctx, taskID)
	if err != nil {
		return projectTaskContext{}, err
	}
	out := projectTaskContext{
		taskTitle:       strings.TrimSpace(taskRecord.Title),
		taskDescription: strings.TrimSpace(derefString(taskRecord.Description)),
		flowNodeID:      taskRecord.CurrentFlowNodeID,
		flowTemplateID:  taskRecord.FlowTemplateID,
		organizationID:  taskRecord.OrganizationID,
	}
	if out.taskTitle == "" {
		out.taskTitle = taskRecord.ID.String()
	}

	if a.projects != nil {
		projectRecord, projectErr := a.projects.GetByID(ctx, taskRecord.ProjectID)
		if projectErr == nil {
			out.projectName = strings.TrimSpace(projectRecord.DisplayName)
		}
	}
	if out.projectName == "" {
		out.projectName = taskRecord.ProjectID.String()
	}

	if taskRecord.CurrentFlowNodeID != nil && a.flowNodes != nil {
		node, nodeErr := a.flowNodes.GetByID(ctx, *taskRecord.CurrentFlowNodeID)
		if nodeErr == nil {
			out.nodeName = strings.TrimSpace(node.DisplayName)
			out.nodeDescription = strings.TrimSpace(node.NodeType)
			if a.flowExecutions != nil {
				execRow, execErr := a.flowExecutions.GetActive(ctx, taskRecord.ID, node.ID)
				if execErr == nil {
					out.visitCount = execRow.VisitNumber
				}
			}
		}
	}
	return out, nil
}

func (a *PromptAssembler) buildLayer4(ctx context.Context, session repo.ChatSession, agentID uuid.UUID, toolDescriptors []tools.ToolDescriptor, errorsOut *[]string) string {
	skillsLoaded := a.loadSkills(ctx, session, agentID, errorsOut)
	promptsLoaded := a.loadMCPPrompts(ctx, session, toolDescriptors, errorsOut)
	promptsLoaded = suppressPromptConflicts(skillsLoaded, promptsLoaded, a.logger, errorsOut)

	skillSections := make([]string, 0, len(skillsLoaded))
	for _, skill := range skillsLoaded {
		content := strings.TrimSpace(skill.content)
		if content == "" {
			continue
		}
		skillSections = append(skillSections, "### Skill: "+skill.name+"\n"+content)
	}
	promptSections := make([]string, 0, len(promptsLoaded))
	for _, prompt := range promptsLoaded {
		parts := []string{"### MCP Prompt: " + prompt.Name}
		if strings.TrimSpace(prompt.Description) != "" {
			parts = append(parts, strings.TrimSpace(prompt.Description))
		}
		if strings.TrimSpace(prompt.Content) != "" {
			parts = append(parts, strings.TrimSpace(prompt.Content))
		}
		promptSections = append(promptSections, strings.Join(parts, "\n"))
	}

	combinedSkills := append([]string(nil), skillSections...)
	combinedPrompts := append([]string(nil), promptSections...)
	layer := buildLayer4Text(combinedSkills, combinedPrompts)
	for estimateTokens(layer) > defaultLayer4BudgetTokens {
		if len(combinedPrompts) > 0 {
			combinedPrompts = combinedPrompts[:len(combinedPrompts)-1]
			layer = buildLayer4Text(combinedSkills, combinedPrompts)
			continue
		}
		if len(combinedSkills) > 1 {
			combinedSkills = combinedSkills[:len(combinedSkills)-1]
			layer = buildLayer4Text(combinedSkills, combinedPrompts)
			continue
		}
		break
	}
	return layer
}

type loadedSkill struct {
	id          uuid.UUID
	name        string
	description string
	content     string
	priority    int
}

func (a *PromptAssembler) loadSkills(ctx context.Context, session repo.ChatSession, agentID uuid.UUID, errorsOut *[]string) []loadedSkill {
	if a.skills == nil {
		return nil
	}
	type attachment struct {
		skillID  uuid.UUID
		priority int
	}
	ordered := make([]attachment, 0)

	if strings.TrimSpace(session.ScopeType) == "project_task" && a.tasks != nil && a.flowNodeSkills != nil {
		taskRecord, err := a.tasks.GetByID(ctx, session.ScopeID)
		if err == nil && taskRecord.CurrentFlowNodeID != nil {
			rows, rowErr := a.flowNodeSkills.ListByNode(ctx, *taskRecord.CurrentFlowNodeID)
			if rowErr == nil {
				for _, row := range rows {
					ordered = append(ordered, attachment{skillID: row.SkillID, priority: row.Position})
				}
			}
		}
	}

	if a.agentSkills != nil {
		rows, err := a.agentSkills.ListByAgent(ctx, agentID)
		if err == nil {
			for _, row := range rows {
				ordered = append(ordered, attachment{skillID: row.SkillID, priority: row.Priority})
			}
		}
	}

	seen := map[uuid.UUID]struct{}{}
	loaded := make([]loadedSkill, 0, len(ordered))
	for _, item := range ordered {
		if _, ok := seen[item.skillID]; ok {
			continue
		}
		seen[item.skillID] = struct{}{}

		skillRecord, err := a.skills.GetByID(ctx, item.skillID)
		if err != nil || !skillRecord.IsActive {
			continue
		}
		content, readErr := a.readSkillFile(skillRecord.FilePath)
		if readErr != nil {
			a.logger.Warn("prompt assembler skipping missing skill file", "skill_id", skillRecord.ID, "file_path", skillRecord.FilePath, "error", readErr)
			if errorsOut != nil {
				*errorsOut = append(*errorsOut, fmt.Sprintf("skill %s skipped: %v", strings.TrimSpace(skillRecord.DisplayName), readErr))
			}
			continue
		}
		name := strings.TrimSpace(skillRecord.DisplayName)
		if name == "" {
			name = strings.TrimSpace(skillRecord.Slug)
		}
		loaded = append(loaded, loadedSkill{
			id:          skillRecord.ID,
			name:        name,
			description: strings.TrimSpace(skillRecord.Description),
			content:     strings.TrimSpace(string(content)),
			priority:    item.priority,
		})
	}

	sort.SliceStable(loaded, func(i, j int) bool {
		if loaded[i].priority == loaded[j].priority {
			return loaded[i].name < loaded[j].name
		}
		return loaded[i].priority < loaded[j].priority
	})
	return loaded
}

func (a *PromptAssembler) readSkillFile(path string) ([]byte, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, fmt.Errorf("empty skill file path")
	}
	resolved := trimmed
	if !filepath.IsAbs(trimmed) {
		resolved = filepath.Join(a.skillsDir, trimmed)
	}
	return os.ReadFile(filepath.Clean(resolved))
}

func (a *PromptAssembler) loadMCPPrompts(ctx context.Context, session repo.ChatSession, toolDescriptors []tools.ToolDescriptor, errorsOut *[]string) []MCPPrompt {
	if a.mcpPromptLister == nil || a.mcpConnections == nil {
		return nil
	}
	connectionIDs := uniqueMCPConnectionIDs(toolDescriptors)
	if len(connectionIDs) == 0 {
		return nil
	}

	type fetchResult struct {
		connectionID uuid.UUID
		prompts      []MCPPrompt
		err          error
	}
	results := make(chan fetchResult, len(connectionIDs))
	var wg sync.WaitGroup

	for _, connectionID := range connectionIDs {
		wg.Add(1)
		go func(connID uuid.UUID) {
			defer wg.Done()
			cacheKey := mcpPromptCacheKey{sessionID: session.ID, connectionID: connID}
			a.mcpPromptCacheMu.RLock()
			cached, ok := a.mcpPromptCache[cacheKey]
			a.mcpPromptCacheMu.RUnlock()
			if ok {
				results <- fetchResult{connectionID: connID, prompts: append([]MCPPrompt(nil), cached...)}
				return
			}

			connection, err := a.mcpConnections.GetByID(ctx, connID)
			if err != nil {
				results <- fetchResult{connectionID: connID, err: err}
				return
			}
			if connection.OrganizationID != session.OrganizationID {
				results <- fetchResult{connectionID: connID}
				return
			}
			if !connectionSupportsPrompts(connection.TransportConfig) {
				results <- fetchResult{connectionID: connID}
				return
			}
			prompts, err := a.mcpPromptLister.ListPrompts(ctx, connection)
			if err != nil {
				results <- fetchResult{connectionID: connID, err: err}
				return
			}
			a.mcpPromptCacheMu.Lock()
			a.mcpPromptCache[cacheKey] = append([]MCPPrompt(nil), prompts...)
			a.mcpPromptCacheMu.Unlock()
			results <- fetchResult{connectionID: connID, prompts: prompts}
		}(connectionID)
	}

	wg.Wait()
	close(results)

	merged := make([]MCPPrompt, 0)
	for item := range results {
		if item.err != nil {
			a.logger.Warn("prompt assembler MCP prompt fetch failed", "connection_id", item.connectionID, "error", item.err)
			if errorsOut != nil {
				*errorsOut = append(*errorsOut, fmt.Sprintf("mcp prompts unavailable for connection %s: %v", item.connectionID, item.err))
			}
			continue
		}
		merged = append(merged, item.prompts...)
	}
	return merged
}

func suppressPromptConflicts(skills []loadedSkill, prompts []MCPPrompt, logger *slog.Logger, errorsOut *[]string) []MCPPrompt {
	if len(skills) == 0 || len(prompts) == 0 {
		return prompts
	}
	kept := make([]MCPPrompt, 0, len(prompts))
	for _, prompt := range prompts {
		overriddenBy := ""
		for _, skill := range skills {
			if hasDomainConflict(skill.description, prompt.Description) {
				overriddenBy = skill.name
				break
			}
		}
		if overriddenBy == "" {
			kept = append(kept, prompt)
			continue
		}
		logger.Warn("skill prompt overrides mcp prompt", "skill", overriddenBy, "prompt", prompt.Name)
		if errorsOut != nil {
			*errorsOut = append(*errorsOut, fmt.Sprintf("skill '%s' overrides MCP prompt '%s' on domain conflict", overriddenBy, strings.TrimSpace(prompt.Name)))
		}
	}
	return kept
}

func hasDomainConflict(skillDescription, promptDescription string) bool {
	skillWords := normalizedWordSet(skillDescription, mcpPromptWordSample)
	promptWords := normalizedWordSet(promptDescription, mcpPromptWordSample)
	if len(skillWords) == 0 || len(promptWords) == 0 {
		return false
	}
	overlap := 0
	for word := range skillWords {
		if _, ok := promptWords[word]; ok {
			overlap++
			if overlap >= mcpPromptOverlapThreshold {
				return true
			}
		}
	}
	return false
}

func normalizedWordSet(text string, maxWords int) map[string]struct{} {
	words := strings.Fields(strings.ToLower(strings.TrimSpace(text)))
	if maxWords > 0 && len(words) > maxWords {
		words = words[:maxWords]
	}
	out := make(map[string]struct{}, len(words))
	for _, raw := range words {
		normalized := strings.Trim(strings.TrimSpace(raw), " .,;:!?\"'()[]{}")
		if len(normalized) < 3 {
			continue
		}
		if _, stop := stopWords[normalized]; stop {
			continue
		}
		out[normalized] = struct{}{}
	}
	return out
}

var stopWords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "with": {}, "that": {}, "this": {}, "from": {}, "are": {},
	"you": {}, "your": {}, "into": {}, "about": {}, "have": {}, "has": {}, "was": {}, "were": {},
	"will": {}, "would": {}, "can": {}, "could": {}, "should": {}, "not": {}, "but": {}, "use": {},
}

func buildLayer4Text(skills []string, prompts []string) string {
	sections := []string{"## Skills and Context"}
	if len(skills) == 0 && len(prompts) == 0 {
		sections = append(sections, "(none)")
		return strings.Join(sections, "\n")
	}
	sections = append(sections, skills...)
	sections = append(sections, prompts...)
	return strings.Join(sections, "\n\n")
}

func (a *PromptAssembler) buildLayer5(ctx context.Context, session repo.ChatSession, agentID uuid.UUID, messages []repo.ChatMessage, previous *MemoryManifest, budgetTokens int) (string, MemoryManifest) {
	manifest := MemoryManifest{InjectedMemoryIDs: []uuid.UUID{}}
	if a.memoryRetriever == nil {
		return "## Relevant Context\n(none)", manifest
	}

	query := buildMemoryQuery(messages)
	projectID, taskID := projectAndTaskIDsFromSession(session)
	req := memory.RetrievalRequest{
		OrganizationID:   session.OrganizationID,
		ProjectID:        projectID,
		TaskID:           taskID,
		AgentID:          &agentID,
		Query:            query,
		Mode:             memory.RetrievalModePassive,
		MaxResults:       20,
		SensitivityGate:  true,
		SessionID:        &session.ID,
		SessionTurnIndex: session.TurnCount + 1,
	}
	result, err := a.memoryRetriever.Query(ctx, req)
	if err != nil {
		a.logger.Warn("prompt assembler memory retrieval failed", "session_id", session.ID, "error", err)
		return "## Relevant Context\n(none)", manifest
	}

	ranked := append([]memory.RankedMemory(nil), result.Memories...)
	// The assembler performs attention-aware ordering: most relevant memory appears last.
	reverseRankedMemories(ranked)

	skipSet := map[uuid.UUID]struct{}{}
	if previous != nil && len(previous.InjectedMemoryIDs) > 0 && len(ranked) >= 3 {
		for _, id := range previous.InjectedMemoryIDs {
			skipSet[id] = struct{}{}
		}
	}

	manifest.TotalMemories = len(ranked)
	injectedLines := make([]string, 0)
	used := 0
	for _, rankedMemory := range ranked {
		if _, skip := skipSet[rankedMemory.Memory.ID]; skip {
			continue
		}
		content := strings.TrimSpace(rankedMemory.Memory.Content)
		if content == "" {
			continue
		}
		tokens := estimateTokens(content)
		if tokens > budgetTokens-used {
			continue
		}
		used += tokens
		manifest.InjectedMemoryIDs = append(manifest.InjectedMemoryIDs, rankedMemory.Memory.ID)
		injectedLines = append(injectedLines, "- "+content)
	}
	manifest.BudgetUsed = used

	if len(injectedLines) == 0 {
		return "## Relevant Context\n(none)", manifest
	}
	return "## Relevant Context\n" + strings.Join(injectedLines, "\n"), manifest
}

func reverseRankedMemories(items []memory.RankedMemory) {
	for i := 0; i < len(items)/2; i++ {
		j := len(items) - 1 - i
		items[i], items[j] = items[j], items[i]
	}
}

func buildMemoryQuery(messages []repo.ChatMessage) string {
	recent := make([]string, 0, 3)
	for i := len(messages) - 1; i >= 0 && len(recent) < 3; i-- {
		if strings.TrimSpace(messages[i].Role) != "user" {
			continue
		}
		content := strings.TrimSpace(messages[i].Content)
		if content == "" {
			continue
		}
		recent = append(recent, content)
	}
	if len(recent) == 0 {
		return "session context"
	}
	for i := 0; i < len(recent)/2; i++ {
		j := len(recent) - 1 - i
		recent[i], recent[j] = recent[j], recent[i]
	}
	return strings.Join(recent, "\n")
}

func (a *PromptAssembler) buildLayer6(messages []repo.ChatMessage, summaries []repo.ChatSummary, budgetTokens int) ([]PromptMessage, bool) {
	history := summarizeHistory(messages, summaries)
	total := estimatePromptMessagesTokens(history)
	if total <= budgetTokens {
		return history, false
	}

	compressed := false
	for total > budgetTokens {
		index := oldestRawMessageIndex(history)
		if index < 0 {
			compressed = true
			summaryIdx := oldestSummaryMessageIndex(history)
			if summaryIdx < 0 {
				break
			}
			history = append(history[:summaryIdx], history[summaryIdx+1:]...)
			total = estimatePromptMessagesTokens(history)
			continue
		}
		history = append(history[:index], history[index+1:]...)
		total = estimatePromptMessagesTokens(history)
	}

	return history, compressed
}

func (a *PromptAssembler) shouldSummarize(ctx context.Context, sessionID uuid.UUID, layerBudgetTokens int) (bool, error) {
	if a.summarization == nil {
		return false, nil
	}
	return a.summarization.ShouldSummarize(ctx, sessionID, layerBudgetTokens)
}

func (a *PromptAssembler) enqueueSummarization(ctx context.Context, sessionID uuid.UUID, layerBudgetTokens int) error {
	if a.enqueuer == nil {
		return nil
	}
	_, err := a.enqueuer.Enqueue(ctx, nil, chat.ChatSummarizeJobType, defaultSummarizationJobPrio, chat.ChatSummarizePayload{
		SessionID:         sessionID,
		LayerBudgetTokens: layerBudgetTokens,
	}, nil)
	if err != nil {
		return fmt.Errorf("enqueue summarization: %w", err)
	}
	return nil
}

func summarizeHistory(messages []repo.ChatMessage, summaries []repo.ChatSummary) []PromptMessage {
	if len(messages) == 0 && len(summaries) == 0 {
		return nil
	}
	summaryRanges := append([]repo.ChatSummary(nil), summaries...)
	sort.Slice(summaryRanges, func(i, j int) bool {
		if summaryRanges[i].FromSequence == summaryRanges[j].FromSequence {
			return summaryRanges[i].ToSequence < summaryRanges[j].ToSequence
		}
		return summaryRanges[i].FromSequence < summaryRanges[j].FromSequence
	})

	out := make([]PromptMessage, 0, len(messages)+len(summaryRanges))
	nextSummary := 0

	emitSummaryAtOrBefore := func(sequence int64) {
		for nextSummary < len(summaryRanges) && summaryRanges[nextSummary].FromSequence <= sequence {
			summary := summaryRanges[nextSummary]
			out = append(out, PromptMessage{
				Role:    "system",
				Content: "[Summary of earlier conversation]: " + strings.TrimSpace(summary.SummaryText),
			})
			nextSummary++
		}
	}

	for _, message := range messages {
		emitSummaryAtOrBefore(message.SequenceNumber)
		if coveredBySummary(message.SequenceNumber, summaryRanges) {
			continue
		}
		pm := PromptMessage{
			Role:       strings.TrimSpace(message.Role),
			Content:    strings.TrimSpace(message.Content),
			ToolCallID: message.ToolCallID,
		}
		// Read tool_calls persisted in message metadata (set by the turn engine
		// when the model returns tool_calls instead of text content).
		if len(message.Metadata) > 0 {
			var meta struct {
				ToolCalls []PromptToolCall `json:"tool_calls"`
			}
			if err := json.Unmarshal(message.Metadata, &meta); err == nil {
				pm.ToolCalls = meta.ToolCalls
			}
		}
		out = append(out, pm)
	}
	for nextSummary < len(summaryRanges) {
		summary := summaryRanges[nextSummary]
		out = append(out, PromptMessage{
			Role:    "system",
			Content: "[Summary of earlier conversation]: " + strings.TrimSpace(summary.SummaryText),
		})
		nextSummary++
	}
	return out
}

func coveredBySummary(sequence int64, summaries []repo.ChatSummary) bool {
	for _, summary := range summaries {
		if sequence >= summary.FromSequence && sequence <= summary.ToSequence {
			return true
		}
	}
	return false
}

func oldestRawMessageIndex(history []PromptMessage) int {
	for i, item := range history {
		if item.Role != "system" || !strings.HasPrefix(strings.TrimSpace(item.Content), "[Summary of earlier conversation]:") {
			return i
		}
	}
	return -1
}

func oldestSummaryMessageIndex(history []PromptMessage) int {
	for i, item := range history {
		if item.Role == "system" && strings.HasPrefix(strings.TrimSpace(item.Content), "[Summary of earlier conversation]:") {
			return i
		}
	}
	return -1
}

func joinSections(sections ...string) string {
	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		trimmed := strings.TrimSpace(section)
		if trimmed == "" {
			continue
		}
		parts = append(parts, trimmed)
	}
	return strings.Join(parts, "\n\n")
}

func reduceToolsToBudget(input []tools.ToolDescriptor, budgetTokens int) []tools.ToolDescriptor {
	if budgetTokens <= 0 {
		budgetTokens = defaultLayer7BudgetTokens
	}
	out := append([]tools.ToolDescriptor(nil), input...)
	if estimateToolsTokens(out) <= budgetTokens {
		return out
	}

	// Pass 1: remove lower-priority non-core tools first.
	for estimateToolsTokens(out) > budgetTokens {
		index := findDropCandidate(out, func(item tools.ToolDescriptor) bool {
			return !isCoreTier1Tool(item)
		})
		if index < 0 {
			break
		}
		out = append(out[:index], out[index+1:]...)
	}

	// Pass 2: remove MCP tools second.
	for estimateToolsTokens(out) > budgetTokens {
		index := findDropCandidate(out, func(item tools.ToolDescriptor) bool {
			return !isCoreTier1Tool(item) && strings.EqualFold(strings.TrimSpace(item.Source), "mcp")
		})
		if index < 0 {
			break
		}
		out = append(out[:index], out[index+1:]...)
	}
	return out
}

func dropLowestPriorityTool(input []tools.ToolDescriptor) []tools.ToolDescriptor {
	index := findDropCandidate(input, func(item tools.ToolDescriptor) bool {
		return !isCoreTier1Tool(item)
	})
	if index < 0 {
		return input
	}
	out := append([]tools.ToolDescriptor(nil), input...)
	return append(out[:index], out[index+1:]...)
}

func findDropCandidate(items []tools.ToolDescriptor, include func(item tools.ToolDescriptor) bool) int {
	bestIndex := -1
	bestPriority := -1
	for i, item := range items {
		if include != nil && !include(item) {
			continue
		}
		priority := item.Priority
		if priority <= 0 {
			priority = i + 1
		}
		if priority > bestPriority {
			bestPriority = priority
			bestIndex = i
		}
	}
	return bestIndex
}

func isCoreTier1Tool(item tools.ToolDescriptor) bool {
	if !strings.EqualFold(strings.TrimSpace(item.Tier), "tier1") {
		return false
	}
	_, ok := coreTier1Tools[strings.TrimSpace(item.Name)]
	return ok
}

func uniqueMCPConnectionIDs(toolDescriptors []tools.ToolDescriptor) []uuid.UUID {
	seen := map[uuid.UUID]struct{}{}
	out := make([]uuid.UUID, 0)
	for _, descriptor := range toolDescriptors {
		if !strings.EqualFold(strings.TrimSpace(descriptor.Source), "mcp") || descriptor.MCPConnectionID == nil {
			continue
		}
		if *descriptor.MCPConnectionID == uuid.Nil {
			continue
		}
		if _, ok := seen[*descriptor.MCPConnectionID]; ok {
			continue
		}
		seen[*descriptor.MCPConnectionID] = struct{}{}
		out = append(out, *descriptor.MCPConnectionID)
	}
	return out
}

func connectionSupportsPrompts(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false
	}
	if promptCap, ok := decoded["prompts"]; ok {
		if truthy(promptCap) {
			return true
		}
	}
	capsRaw, ok := decoded["capabilities"]
	if !ok {
		return false
	}
	caps, ok := capsRaw.(map[string]any)
	if !ok {
		return false
	}
	return truthy(caps["prompts"])
}

func truthy(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		trimmed := strings.TrimSpace(strings.ToLower(v))
		return trimmed == "1" || trimmed == "true" || trimmed == "yes"
	case float64:
		return v != 0
	case int:
		return v != 0
	default:
		return false
	}
}

func estimatePromptMessagesTokens(messages []PromptMessage) int {
	total := 0
	for _, message := range messages {
		total += estimateTokens(message.Role)
		total += estimateTokens(message.Content)
		total += defaultPromptMessageOverhead
	}
	return total
}

func estimateToolsTokens(items []tools.ToolDescriptor) int {
	total := 0
	for _, item := range items {
		encoded, _ := json.Marshal(item)
		total += estimateTokens(string(encoded))
	}
	return total
}

func estimateTokens(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	return len(text) / 4
}

func sumLayerTokens(layerTokens map[string]int) int {
	total := 0
	for _, key := range []string{"layer1", "layer2", "layer3", "layer4", "layer5", "layer6", "layer7"} {
		total += layerTokens[key]
	}
	return total
}

func readTrustTier(metadata json.RawMessage) string {
	if len(metadata) == 0 {
		return ""
	}
	var decoded map[string]any
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		return ""
	}
	raw, ok := decoded["trust_tier"]
	if !ok {
		return ""
	}
	if str, ok := raw.(string); ok {
		return str
	}
	return fmt.Sprint(raw)
}

func projectAndTaskIDsFromSession(session repo.ChatSession) (*uuid.UUID, *uuid.UUID) {
	switch strings.TrimSpace(session.ScopeType) {
	case "project":
		projectID := session.ScopeID
		return &projectID, nil
	case "project_task":
		taskID := session.ScopeID
		return nil, &taskID
	default:
		return nil, nil
	}
}

func projectIDFromScope(session repo.ChatSession) *uuid.UUID {
	if strings.TrimSpace(session.ScopeType) == "project" {
		projectID := session.ScopeID
		return &projectID
	}
	return nil
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
