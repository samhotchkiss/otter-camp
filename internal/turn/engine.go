package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/model"
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/tools"
)

const (
	AgentTurnJobType            = "agent_turn"
	defaultAgentTurnJobPriority = 70
	defaultMaxToolCalls         = 75
	defaultSyncMaxDuration      = 5 * time.Minute
	defaultAsyncMaxDuration     = 30 * time.Minute
	defaultListeningEvalDelay   = 500 * time.Millisecond
	defaultAutoContinueDelay    = 2 * time.Second
	defaultModelRetryBudget     = 3
	defaultRateLimitBackoff     = 30 * time.Second
	maxRateLimitBackoff         = 30 * time.Minute
	maxRateLimitRetries         = 5
	maxConsecutiveAutoTurns     = 10
	defaultSummarizeLayerBudget = 0
	chunkPollSteerEveryNChunks  = 10
	maxContinuationTurnDepth    = 3
	defaultTurnConsumerName     = "turn-engine.user-message"
	defaultReactionConsumerName = "turn-engine.reactions"
	defaultTurnCompletedName    = "turn-engine.turn-completed"
	defaultCancelConsumerPrefix = "turn-engine.cancel"
	stopReasonMaxToolCalls      = "max_tool_calls"
	stopReasonMaxDuration       = "max_duration"
)

var (
	ErrModelTransient = errors.New("transient model failure")
	errTurnDeferred   = errors.New("turn deferred")
	errTurnCancelled  = errors.New("turn cancelled")
)

type AgentTurnPayload struct {
	SessionID  uuid.UUID  `json:"session_id"`
	MessageID  uuid.UUID  `json:"message_id"`
	AgentID    *uuid.UUID `json:"agent_id,omitempty"`
	RetryCount int        `json:"retry_count,omitempty"`
}

type ModelRequest struct {
	OrganizationID  uuid.UUID
	SessionID       uuid.UUID
	TurnID          uuid.UUID
	AgentID         uuid.UUID
	Purpose         string
	Profile         repo.ModelProfile
	Prompt          *prompt.AssembledPrompt
	HumanMessages   []string
	InstructionHint string
}

type ModelUsage struct {
	InputTokens     int
	OutputTokens    int
	CacheReadTokens int
}

type ModelToolCall struct {
	ID              string
	Name            string
	Tier            string
	Arguments       map[string]any
	MCPConnectionID *uuid.UUID
}

type ModelResponse struct {
	Content   string
	ToolCalls []ModelToolCall
	Usage     *ModelUsage
}

type ToolCall struct {
	ID              string
	Name            string
	Tier            string
	Arguments       map[string]any
	MCPConnectionID *uuid.UUID
}

type ToolResult struct {
	ToolCallID string
	Name       string
	Output     map[string]any
	Error      string
	RunID      *uuid.UUID
}

type ChatService interface {
	GetSession(ctx context.Context, id uuid.UUID) (*chat.ChatSession, error)
	CreateTurn(ctx context.Context, sessionID, agentID uuid.UUID) (*chat.ChatTurn, error)
	StartTurn(ctx context.Context, turnID uuid.UUID) error
	CompleteTurn(ctx context.Context, turnID uuid.UUID) error
	CancelTurn(ctx context.Context, turnID uuid.UUID, reason string) error
	FailTurn(ctx context.Context, turnID uuid.UUID, errorMsg string) error
	GetTurn(ctx context.Context, turnID uuid.UUID) (*chat.ChatTurn, error)
	ListParticipants(ctx context.Context, sessionID uuid.UUID) ([]*chat.ChatParticipant, error)
	AddParticipant(ctx context.Context, sessionID uuid.UUID, participantType string, participantID uuid.UUID, role string) (*chat.ChatParticipant, error)
	AppendMessage(ctx context.Context, input chat.AppendMessageInput) (*chat.ChatMessage, error)
	UpdateMessageStatus(ctx context.Context, messageID uuid.UUID, newStatus, errorMsg string) error
}

type ToolResolver interface {
	GetSessionToolSet(ctx context.Context, sessionID, agentID uuid.UUID) ([]tools.ToolDescriptor, error)
}

type PromptAssembler interface {
	Assemble(ctx context.Context, input prompt.AssemblyInput) (*prompt.AssembledPrompt, error)
}

type SummarizationChecker interface {
	ShouldSummarize(ctx context.Context, sessionID uuid.UUID, layerBudgetTokens int) (bool, error)
}

type ModelGateway interface {
	StreamComplete(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error)
	Complete(ctx context.Context, req ModelRequest) (ModelResponse, error)
}

type ToolDispatcher interface {
	DispatchTier1(ctx context.Context, call ToolCall) (ToolResult, error)
	DispatchTier2(ctx context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error)
}

type UnavailableModelGateway struct{}

func (UnavailableModelGateway) StreamComplete(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
	return ModelResponse{}, fmt.Errorf("model gateway is not configured")
}

func (UnavailableModelGateway) Complete(_ context.Context, _ ModelRequest) (ModelResponse, error) {
	return ModelResponse{}, fmt.Errorf("model gateway is not configured")
}

type UnavailableToolDispatcher struct{}

func (UnavailableToolDispatcher) DispatchTier1(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Error:      fmt.Sprintf("%s failed: tool dispatcher is not configured", call.Name),
	}, nil
}

func (UnavailableToolDispatcher) DispatchTier2(_ context.Context, call ToolCall, _ func(runID uuid.UUID)) (ToolResult, error) {
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Error:      fmt.Sprintf("%s failed: tool dispatcher is not configured", call.Name),
	}, nil
}

type RunCanceler interface {
	RequestCancel(ctx context.Context, runID uuid.UUID, requestedBy controlplane.CancelRequestActor) error
}

type EventBus interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
	Subscribe(consumerName string, orgID *uuid.UUID, handler eventbus.EventHandler) eventbus.Subscription
	Unsubscribe(sub eventbus.Subscription)
}

type JobEnqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error)
}

type modelInvocationRepo interface {
	Create(ctx context.Context, invocation repo.ModelInvocation) (repo.ModelInvocation, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, errorCode, errorMessage *string) (repo.ModelInvocation, error)
	UpdateCompletion(ctx context.Context, id uuid.UUID, inputTokens, outputTokens, cacheTokens, latencyMS, totalDurationMS int, promptKey, responseKey *string) error
}

type modelProfileLookup interface {
	GetCurrentByLogicalID(ctx context.Context, organizationID uuid.UUID, logicalProfileID string) (repo.ModelProfile, error)
}

type messageRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ChatMessage, error)
	ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatMessage, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, errorMessage string) (repo.ChatMessage, error)
	UpdateContent(ctx context.Context, id uuid.UUID, content string) (repo.ChatMessage, error)
	UpdateMetadata(ctx context.Context, id uuid.UUID, metadata json.RawMessage) (repo.ChatMessage, error)
}

type turnRepository interface {
	Create(ctx context.Context, turn repo.ChatTurn) (repo.ChatTurn, error)
	ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatTurn, error)
	SetStopReason(ctx context.Context, id uuid.UUID, stopReason *string) (repo.ChatTurn, error)
}

type sessionRepository interface {
	UpdateCurrentTurn(ctx context.Context, id uuid.UUID, currentTurnID *uuid.UUID) (repo.ChatSession, error)
	IncrementCounts(ctx context.Context, id uuid.UUID, turnDelta, messageDelta int) (repo.ChatSession, error)
}

type agentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
	GetStarterTrio(ctx context.Context, organizationID uuid.UUID) ([]repo.Agent, error)
}

type taskRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
}

type flowNodeRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowNode, error)
}

type assignmentRepository interface {
	GetPM(ctx context.Context, projectID uuid.UUID) (repo.AgentProjectAssignment, error)
}

type memorySourceRepository interface {
	ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.MemorySource, error)
}

type memoryRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Memory, error)
	UpdateConfidence(ctx context.Context, id uuid.UUID, confidence float64) (repo.Memory, error)
}

type ProfileResolver interface {
	Resolve(ctx context.Context, orgID uuid.UUID, purpose string, scopes ...model.Scope) (*repo.ModelProfile, error)
}

type Options struct {
	Pool *pgxpool.Pool

	Chat          ChatService
	ToolResolver  ToolResolver
	Assembler     PromptAssembler
	Summarization SummarizationChecker
	ModelGateway  ModelGateway
	Dispatcher    ToolDispatcher
	RunCanceler   RunCanceler
	Events        EventBus
	Enqueuer      JobEnqueuer

	Invocations   modelInvocationRepo
	ModelProfiles modelProfileLookup
	Profiles      ProfileResolver
	Messages      messageRepository
	Turns         turnRepository
	Sessions      sessionRepository
	Agents        agentRepository
	Tasks         taskRepository
	FlowNodes     flowNodeRepository
	Assignments   assignmentRepository
	MemorySources memorySourceRepository
	Memories      memoryRepository

	DefaultModelProfileID *string
	MaxToolCalls          int
	SyncMaxDuration       time.Duration
	AsyncMaxDuration      time.Duration
	ListeningEvalDelay    time.Duration
	ModelRetryBudget      int
	JobPriority           int
	Now                   func() time.Time
	Sleep                 func(context.Context, time.Duration) error
	Logger                *slog.Logger
}

type TurnEngine struct {
	chat          ChatService
	toolResolver  ToolResolver
	assembler     PromptAssembler
	summarization SummarizationChecker
	models        ModelGateway
	dispatcher    ToolDispatcher
	runCanceler   RunCanceler
	events        EventBus
	enqueuer      JobEnqueuer

	invocations modelInvocationRepo
	profiles    modelProfileLookup
	resolver    ProfileResolver
	messages    messageRepository
	turns       turnRepository
	sessions    sessionRepository
	agents      agentRepository
	tasks       taskRepository
	flowNodes   flowNodeRepository
	assignments assignmentRepository
	sources     memorySourceRepository
	memories    memoryRepository

	defaultModelProfileID *string
	maxToolCalls          int
	syncMaxDuration       time.Duration
	asyncMaxDuration      time.Duration
	listeningEvalDelay    time.Duration
	modelRetryBudget      int
	jobPriority           int
	now                   func() time.Time
	sleep                 func(context.Context, time.Duration) error
	logger                *slog.Logger
	cancelConsumerName    string
}

type turnRuntime struct {
	session           *chat.ChatSession
	agent             repo.Agent
	turn              *chat.ChatTurn
	initialMessageID  uuid.UUID
	startedAt         time.Time
	toolCallsUsed     int
	activeTier2RunID  *uuid.UUID
	activeTier2RunMu  sync.RWMutex
	modelRetryUsed    int
	invocationAttempt int
	toolSet           []tools.ToolDescriptor
	stopReason        string
}

func NewEngine(opts Options) (*TurnEngine, error) {
	needsPool := opts.Messages == nil || opts.Turns == nil || opts.Sessions == nil ||
		opts.Agents == nil || opts.Tasks == nil || opts.MemorySources == nil || opts.Memories == nil
	if needsPool && opts.Pool == nil {
		return nil, fmt.Errorf("turn engine requires database pool")
	}
	if opts.Chat == nil {
		return nil, fmt.Errorf("turn engine requires chat service")
	}
	if opts.ToolResolver == nil {
		return nil, fmt.Errorf("turn engine requires tool resolver")
	}
	if opts.Assembler == nil {
		return nil, fmt.Errorf("turn engine requires prompt assembler")
	}
	if opts.ModelGateway == nil {
		return nil, fmt.Errorf("turn engine requires model gateway")
	}
	if opts.Dispatcher == nil {
		return nil, fmt.Errorf("turn engine requires tool dispatcher")
	}
	if opts.Events == nil {
		return nil, fmt.Errorf("turn engine requires event bus")
	}
	if opts.Enqueuer == nil {
		return nil, fmt.Errorf("turn engine requires job enqueuer")
	}
	if opts.Invocations == nil {
		return nil, fmt.Errorf("turn engine requires model invocation repository")
	}
	if opts.ModelProfiles == nil {
		return nil, fmt.Errorf("turn engine requires model profile repository")
	}
	if opts.Messages == nil {
		opts.Messages = repo.NewChatMessageRepo(opts.Pool)
	}
	if opts.Turns == nil {
		opts.Turns = repo.NewChatTurnRepo(opts.Pool)
	}
	if opts.Sessions == nil {
		opts.Sessions = repo.NewChatSessionRepo(opts.Pool)
	}
	if opts.Agents == nil {
		opts.Agents = repo.NewAgentRepo(opts.Pool)
	}
	if opts.Tasks == nil {
		opts.Tasks = repo.NewProjectTaskRepo(opts.Pool)
	}
	if opts.FlowNodes == nil && opts.Pool != nil {
		opts.FlowNodes = repo.NewFlowNodeRepo(opts.Pool)
	}
	if opts.Assignments == nil && opts.Pool != nil {
		opts.Assignments = repo.NewAgentProjectAssignmentRepo(opts.Pool)
	}
	if opts.MemorySources == nil {
		opts.MemorySources = repo.NewMemorySourceRepo(opts.Pool)
	}
	if opts.Memories == nil {
		opts.Memories = repo.NewMemoryRepo(opts.Pool)
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.MaxToolCalls <= 0 {
		opts.MaxToolCalls = defaultMaxToolCalls
	}
	if opts.SyncMaxDuration <= 0 {
		opts.SyncMaxDuration = defaultSyncMaxDuration
	}
	if opts.AsyncMaxDuration <= 0 {
		opts.AsyncMaxDuration = defaultAsyncMaxDuration
	}
	if opts.ListeningEvalDelay <= 0 {
		opts.ListeningEvalDelay = defaultListeningEvalDelay
	}
	if opts.ModelRetryBudget <= 0 {
		opts.ModelRetryBudget = defaultModelRetryBudget
	}
	if opts.JobPriority <= 0 {
		opts.JobPriority = defaultAgentTurnJobPriority
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Sleep == nil {
		opts.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}

	return &TurnEngine{
		chat:                  opts.Chat,
		toolResolver:          opts.ToolResolver,
		assembler:             opts.Assembler,
		summarization:         opts.Summarization,
		models:                opts.ModelGateway,
		dispatcher:            opts.Dispatcher,
		runCanceler:           opts.RunCanceler,
		events:                opts.Events,
		enqueuer:              opts.Enqueuer,
		invocations:           opts.Invocations,
		profiles:              opts.ModelProfiles,
		resolver:              opts.Profiles,
		messages:              opts.Messages,
		turns:                 opts.Turns,
		sessions:              opts.Sessions,
		agents:                opts.Agents,
		tasks:                 opts.Tasks,
		flowNodes:             opts.FlowNodes,
		assignments:           opts.Assignments,
		sources:               opts.MemorySources,
		memories:              opts.Memories,
		defaultModelProfileID: opts.DefaultModelProfileID,
		maxToolCalls:          opts.MaxToolCalls,
		syncMaxDuration:       opts.SyncMaxDuration,
		asyncMaxDuration:      opts.AsyncMaxDuration,
		listeningEvalDelay:    opts.ListeningEvalDelay,
		modelRetryBudget:      opts.ModelRetryBudget,
		jobPriority:           opts.JobPriority,
		now:                   opts.Now,
		sleep:                 opts.Sleep,
		logger:                opts.Logger,
		cancelConsumerName:    defaultCancelConsumerPrefix + "." + uuid.NewString(),
	}, nil
}

func (e *TurnEngine) RegisterJobHandler(worker interface {
	Register(jobType string, handler jobqueue.JobHandler)
}) {
	if e == nil || worker == nil {
		return
	}
	worker.Register(AgentTurnJobType, e.HandleTurnJob)
}

func (e *TurnEngine) SubscribeUserMessageEnqueue(orgID *uuid.UUID) eventbus.Subscription {
	return e.events.Subscribe(defaultTurnConsumerName, orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		return e.HandleUserMessageEvent(ctx, event)
	})
}

func (e *TurnEngine) SubscribeReactionFeedback(orgID *uuid.UUID) eventbus.Subscription {
	return e.events.Subscribe(defaultReactionConsumerName, orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		return e.HandleReactionEvent(ctx, event)
	})
}

func (e *TurnEngine) SubscribeTurnCompletedAutoContinuation(orgID *uuid.UUID) eventbus.Subscription {
	return e.events.Subscribe(defaultTurnCompletedName, orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		return e.HandleTurnCompletedEvent(ctx, event)
	})
}

func (e *TurnEngine) HandleTurnJob(ctx context.Context, job jobqueue.Job) error {
	if strings.TrimSpace(job.JobType) != AgentTurnJobType {
		return nil
	}
	var payload AgentTurnPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("decode %s payload: %w", AgentTurnJobType, err)
	}
	if payload.SessionID == uuid.Nil || payload.MessageID == uuid.Nil {
		return fmt.Errorf("%s payload missing session_id or message_id", AgentTurnJobType)
	}
	return e.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount)
}

func (e *TurnEngine) HandleUserMessageEvent(ctx context.Context, event eventbus.DomainEvent) error {
	// Only enqueue on user_sent, not on the generic created event, to avoid
	// duplicate jobs (both events fire for the same user message).
	if event.EventType != "chat.message.user_sent" {
		return nil
	}
	payload, err := parseAgentTurnPayload(event.Payload)
	if err != nil {
		return nil
	}
	if session, getErr := e.chat.GetSession(ctx, payload.SessionID); getErr == nil {
		if responderID, resolveErr := e.resolveSessionAgentForSession(ctx, session); resolveErr == nil && responderID != uuid.Nil {
			payload.AgentID = &responderID
		}
	}
	_, err = e.enqueuer.Enqueue(ctx, nil, AgentTurnJobType, e.jobPriority, payload, nil)
	return err
}

func (e *TurnEngine) HandleReactionEvent(ctx context.Context, event eventbus.DomainEvent) error {
	if event.EventType != "chat.reaction.added" {
		return nil
	}
	payload := map[string]any{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil
	}
	messageID, ok := payloadUUID(payload, "message_id")
	if !ok {
		return nil
	}
	emoji, _ := payloadString(payload, "emoji")
	delta, applies := reactionDelta(emoji)
	if !applies {
		return nil
	}

	message, err := e.messages.GetByID(ctx, messageID)
	if err != nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") || message.TurnID == nil {
		return nil
	}

	sources, err := e.sources.ListBySession(ctx, message.SessionID)
	if err != nil {
		return nil
	}
	for _, source := range sources {
		if strings.TrimSpace(source.SourceType) != "chat_message" || source.SourceID == nil {
			continue
		}
		sourceMessage, getErr := e.messages.GetByID(ctx, *source.SourceID)
		if getErr != nil || sourceMessage.TurnID == nil || *sourceMessage.TurnID != *message.TurnID {
			continue
		}
		memoryRow, getMemErr := e.memories.GetByID(ctx, source.MemoryID)
		if getMemErr != nil {
			continue
		}
		next := memoryRow.Confidence + delta
		if next < 0 {
			next = 0
		}
		if next > 1 {
			next = 1
		}
		_, _ = e.memories.UpdateConfidence(ctx, source.MemoryID, next)
	}
	return nil
}

func (e *TurnEngine) HandleTurnCompletedEvent(ctx context.Context, event eventbus.DomainEvent) error {
	if event.EventType != "chat.turn.completed" {
		return nil
	}

	var payload struct {
		SessionID uuid.UUID `json:"session_id"`
		TurnID    uuid.UUID `json:"turn_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil
	}
	if payload.SessionID == uuid.Nil || payload.TurnID == uuid.Nil {
		return nil
	}

	session, err := e.chat.GetSession(ctx, payload.SessionID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project_task") {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(session.Status), "active") {
		return nil
	}
	if e.tasks == nil || e.flowNodes == nil {
		return nil
	}

	taskRecord, err := e.tasks.GetByID(ctx, session.ScopeID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "in_progress") {
		return nil
	}
	if taskRecord.CurrentFlowNodeID == nil {
		return nil
	}

	currentNode, err := e.flowNodes.GetByID(ctx, *taskRecord.CurrentFlowNodeID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(currentNode.NodeType), "work") {
		return nil
	}

	turns, err := e.turns.ListBySession(ctx, session.ID)
	if err != nil {
		return err
	}
	latestCompleted := latestCompletedTurn(turns)
	if latestCompleted == nil || latestCompleted.ID != payload.TurnID {
		return nil
	}

	messages, err := e.messages.ListBySession(ctx, session.ID)
	if err != nil {
		return err
	}
	latestUser := latestUserMessage(messages)
	if latestUser == nil {
		return nil
	}
	assistant := latestAssistantFinalForTurn(messages, payload.TurnID)
	if assistant == nil {
		return nil
	}
	if latestUser.SequenceNumber > assistant.SequenceNumber {
		return nil
	}
	if indicatesTaskCompletion(assistant.Content) {
		return nil
	}

	if consecutive := consecutiveAutoTurnsSinceLatestUser(turns, messages, latestUser.SequenceNumber); consecutive >= maxConsecutiveAutoTurns {
		e.logger.Warn("auto continuation cap reached",
			"session_id", session.ID,
			"turn_id", payload.TurnID,
			"task_id", session.ScopeID,
			"consecutive_auto_turns", consecutive,
		)
		return nil
	}

	nextPayload := AgentTurnPayload{
		SessionID: session.ID,
		MessageID: latestUser.ID,
	}
	if latestCompleted.RespondingID != uuid.Nil {
		agentID := latestCompleted.RespondingID
		nextPayload.AgentID = &agentID
	}
	runAfter := e.now().Add(defaultAutoContinueDelay).UTC()
	_, err = e.enqueuer.Enqueue(ctx, nil, AgentTurnJobType, e.jobPriority, nextPayload, &runAfter)
	return err
}

func (e *TurnEngine) HandleUserMessage(ctx context.Context, sessionID, messageID uuid.UUID) error {
	return e.handleUserMessage(ctx, sessionID, messageID, nil, 0)
}

func (e *TurnEngine) handleUserMessage(ctx context.Context, sessionID, messageID uuid.UUID, routedAgentID *uuid.UUID, retryCount int) error {
	if sessionID == uuid.Nil || messageID == uuid.Nil {
		return fmt.Errorf("session_id and message_id are required")
	}

	session, err := e.chat.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if strings.EqualFold(session.Status, "closed") || strings.EqualFold(session.Status, "archived") {
		e.logger.Info("skipping agent turn for closed session", "session_id", sessionID)
		return nil
	}
	message, err := e.messages.GetByID(ctx, messageID)
	if err != nil {
		return err
	}
	if message.SessionID != sessionID {
		return repo.ErrNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
		return nil
	}

	agentID := uuid.Nil
	if routedAgentID != nil && *routedAgentID != uuid.Nil {
		agentID = *routedAgentID
	}
	if agentID == uuid.Nil {
		var resolveErr error
		agentID, resolveErr = e.resolveSessionAgentForSession(ctx, session)
		if resolveErr != nil {
			return resolveErr
		}
	}
	agent, err := e.agents.GetByID(ctx, agentID)
	if err != nil {
		return err
	}

	turn, err := e.chat.CreateTurn(ctx, sessionID, agentID)
	if err != nil {
		return err
	}
	if err := e.chat.StartTurn(ctx, turn.ID); err != nil {
		return e.describeTurnTransitionError(ctx, turn.ID, "handleUserMessage StartTurn", "pending->in_progress", err)
	}
	refreshedTurn, err := e.chat.GetTurn(ctx, turn.ID)
	if err == nil {
		turn = refreshedTurn
	}

	runtime := &turnRuntime{
		session:          session,
		agent:            agent,
		turn:             turn,
		initialMessageID: messageID,
		startedAt:        e.turnStartTime(turn),
	}

	cancelCtx, stopCancelWatch := e.watchTurnCancellation(ctx, runtime)
	defer stopCancelWatch()

	err = e.runTurn(cancelCtx, runtime)
	if err == nil || errors.Is(err, errTurnDeferred) || errors.Is(err, errTurnCancelled) {
		return nil
	}
	if errors.Is(err, ErrRateLimited) {
		handled, handleErr := e.handleRateLimitedTurnFailure(ctx, runtime, messageID, routedAgentID, retryCount, err)
		if handleErr != nil {
			return handleErr
		}
		if handled {
			return nil
		}
	}

	e.logger.Error("turn failed", "error", err, "session_id", sessionID, "turn_id", runtime.turn.ID, "agent_id", agentID)
	_ = e.chat.FailTurn(ctx, runtime.turn.ID, summarizeFailure(err))
	_, _ = e.appendSystemMessage(ctx, runtime.turn.ID, runtime.session.ID, fmt.Sprintf("[Turn failed: %s]", summarizeFailure(err)))
	return err
}

func (e *TurnEngine) handleRateLimitedTurnFailure(
	ctx context.Context,
	runtime *turnRuntime,
	messageID uuid.UUID,
	routedAgentID *uuid.UUID,
	retryCount int,
	cause error,
) (bool, error) {
	if runtime == nil || runtime.turn == nil || runtime.session == nil {
		return false, nil
	}
	if retryCount < 0 {
		retryCount = 0
	}
	if retryCount >= maxRateLimitRetries {
		_ = e.chat.FailTurn(ctx, runtime.turn.ID, summarizeFailure(cause))
		_, _ = e.appendSystemMessage(ctx, runtime.turn.ID, runtime.session.ID, fmt.Sprintf("[Turn failed: model retries exhausted after %d attempts.]", maxRateLimitRetries))
		return true, nil
	}

	nextPayload := AgentTurnPayload{
		SessionID:  runtime.session.ID,
		MessageID:  messageID,
		RetryCount: retryCount + 1,
	}
	if routedAgentID != nil && *routedAgentID != uuid.Nil {
		agentID := *routedAgentID
		nextPayload.AgentID = &agentID
	}

	retryDelay := rateLimitRetryDelay(retryCount, rateLimitRetryAfterHint(cause))
	runAfter := e.now().Add(retryDelay).UTC()
	if _, err := e.enqueuer.Enqueue(ctx, nil, AgentTurnJobType, e.jobPriority, nextPayload, &runAfter); err != nil {
		return false, fmt.Errorf("enqueue rate-limited turn retry: %w", err)
	}

	_ = e.chat.FailTurn(ctx, runtime.turn.ID, summarizeFailure(cause))
	_, _ = e.appendSystemMessage(ctx, runtime.turn.ID, runtime.session.ID, fmt.Sprintf("[Rate limited, retrying in %s...]", formatRetryDelay(retryDelay)))
	return true, nil
}

func (e *TurnEngine) runTurn(ctx context.Context, rt *turnRuntime) error {
	if err := e.requireTurnInProgress(ctx, rt); err != nil {
		return err
	}

	continuations := 0
	listeningChecked := false
	var previousManifest *prompt.MemoryManifest

	for {
		if err := ctx.Err(); err != nil {
			return e.handleCancellation(ctx, rt)
		}

		profile, err := e.resolveModelProfile(ctx, rt.session, rt.agent, "agent_turn")
		if err != nil {
			return fmt.Errorf("resolveModelProfile: %w", err)
		}

		toolSet, err := e.toolResolver.GetSessionToolSet(ctx, rt.session.ID, rt.agent.ID)
		if err != nil {
			return fmt.Errorf("getSessionToolSet: %w", err)
		}
		rt.toolSet = toolSet

		assembled, assembleErr := e.assembler.Assemble(ctx, prompt.AssemblyInput{
			SessionID:        rt.session.ID,
			TurnID:           rt.turn.ID,
			AgentID:          rt.agent.ID,
			ModelProfileID:   profile.LogicalProfileID,
			ToolDescriptors:  toolSet,
			PreviousManifest: previousManifest,
		})
		if assembleErr != nil && !errors.Is(assembleErr, prompt.ErrContextCompressed) {
			return fmt.Errorf("assemble: %w", assembleErr)
		}
		if assembled != nil {
			manifestCopy := assembled.MemoryManifest
			previousManifest = &manifestCopy
		}
		if errors.Is(assembleErr, prompt.ErrContextCompressed) {
			continuations++
			if continuations > maxContinuationTurnDepth {
				return fmt.Errorf("context compression continuation depth exceeded")
			}
			rt.stopReason = ""
			if err := e.continueTurn(ctx, rt); err != nil {
				return err
			}
			listeningChecked = false
			previousManifest = nil
			continue
		}

		if e.summarization != nil {
			if summarize, summarizeErr := e.summarization.ShouldSummarize(ctx, rt.session.ID, defaultSummarizeLayerBudget); summarizeErr == nil && summarize {
				_, _ = e.enqueuer.Enqueue(ctx, nil, chat.ChatSummarizeJobType, e.jobPriority, chat.ChatSummarizePayload{SessionID: rt.session.ID, LayerBudgetTokens: defaultSummarizeLayerBudget}, nil)
			}
		}

		if !listeningChecked {
			wait, waitErr := e.runListeningEval(ctx, rt, assembled)
			if waitErr != nil {
				return waitErr
			}
			listeningChecked = true
			if wait {
				runAfter := e.now().Add(e.listeningEvalDelay)
				_, _ = e.enqueuer.Enqueue(ctx, nil, AgentTurnJobType, e.jobPriority, AgentTurnPayload{SessionID: rt.session.ID, MessageID: rt.initialMessageID}, &runAfter)
				_ = e.chat.CompleteTurn(ctx, rt.turn.ID)
				return errTurnDeferred
			}
		}

		assistantMessage, err := e.appendAssistantPlaceholder(ctx, rt.turn.ID, rt.session.ID, rt.agent.ID)
		if err != nil {
			return err
		}

		chunkSeq := int64(0)
		response, err := e.callMainModel(ctx, rt, profile, assembled, assistantMessage, &chunkSeq)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return e.handleCancellation(ctx, rt)
			}
			return err
		}

		if err := e.publishEvent(ctx, rt.session.OrganizationID, "chat.turn.model_call_done", "agent", &rt.agent.ID, map[string]any{
			"session_id": rt.session.ID,
			"turn_id":    rt.turn.ID,
			"message_id": assistantMessage.ID,
		}); err != nil {
			return err
		}

		if len(response.ToolCalls) == 0 {
			currentMessage, _ := e.messages.GetByID(ctx, assistantMessage.ID)
			if strings.EqualFold(strings.TrimSpace(currentMessage.Status), "pending") {
				if _, err := e.messages.UpdateStatus(ctx, assistantMessage.ID, "streaming", ""); err != nil {
					return fmt.Errorf("no-tool pending→streaming: %w", err)
				}
			}
			if _, err := e.messages.UpdateStatus(ctx, assistantMessage.ID, "final", ""); err != nil {
				return fmt.Errorf("no-tool →final (msg status=%s): %w", currentMessage.Status, err)
			}
			if err := e.completeTurn(ctx, rt); err != nil {
				return fmt.Errorf("no-tool completeTurn: %w", err)
			}
			return nil
		}

		// Persist tool_calls in the assistant message metadata so the prompt
		// assembler can include them in the conversation history on the next
		// model call (required by OpenAI/Anthropic for tool result messages).
		if toolCallMeta := buildToolCallMetadata(response.ToolCalls); toolCallMeta != nil {
			_, _ = e.messages.UpdateMetadata(ctx, assistantMessage.ID, toolCallMeta)
		}
		// Mark the assistant message final now — it was fully streamed even
		// though it contained tool calls. The next iteration creates a new
		// assistant placeholder for the follow-up model response.
		_, _ = e.messages.UpdateStatus(ctx, assistantMessage.ID, "final", "")

		stop, dispatchErr := e.dispatchTools(ctx, rt, response.ToolCalls)
		if dispatchErr != nil {
			if errors.Is(dispatchErr, context.Canceled) {
				return e.handleCancellation(ctx, rt)
			}
			return fmt.Errorf("dispatchTools: %w", dispatchErr)
		}
		if stop {
			shouldContinue, err := e.shouldContinueMaxToolCalls(ctx, rt)
			if err != nil {
				return fmt.Errorf("shouldContinueMaxToolCalls: %w", err)
			}
			if shouldContinue {
				if err := e.continueTurn(ctx, rt); err != nil {
					return fmt.Errorf("continueTurn: %w", err)
				}
				listeningChecked = false
				previousManifest = nil
				continue
			}
			if err := e.completeTurn(ctx, rt); err != nil {
				return fmt.Errorf("stop completeTurn: %w", err)
			}
			return nil
		}
	}
}

func (e *TurnEngine) completeTurn(ctx context.Context, rt *turnRuntime) error {
	if err := e.recordStopReason(ctx, rt); err != nil {
		return err
	}
	if err := e.chat.CompleteTurn(ctx, rt.turn.ID); err != nil {
		if errors.Is(err, chat.ErrInvalidStatusTransition) {
			if current, getErr := e.chat.GetTurn(ctx, rt.turn.ID); getErr == nil && strings.EqualFold(strings.TrimSpace(current.Status), "completed") {
				e.logger.Warn("completeTurn no-op for already completed turn",
					"session_id", rt.session.ID,
					"turn_id", rt.turn.ID,
				)
				rt.turn = current
				return nil
			}
		}
		return e.describeTurnTransitionError(ctx, rt.turn.ID, "completeTurn CompleteTurn", "in_progress->completed", err)
	}
	return nil
}

func (e *TurnEngine) continueTurn(ctx context.Context, rt *turnRuntime) error {
	// Check for cancellation before attempting to complete the current turn.
	// This handles the race where a supervisor/API cancel changed the turn
	// status in the DB and published a cancel event, but the context
	// cancellation wasn't detected before we reached this point.
	if err := ctx.Err(); err != nil {
		return e.handleCancellation(ctx, rt)
	}

	notice := "[Context compressed - continuing in a new turn.]"
	if strings.EqualFold(strings.TrimSpace(rt.stopReason), stopReasonMaxToolCalls) {
		notice = "[Max tool calls reached - continuing in a new turn.]"
	}
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, notice); err != nil {
		return err
	}
	if err := e.recordStopReason(ctx, rt); err != nil {
		return err
	}
	if err := e.chat.CompleteTurn(ctx, rt.turn.ID); err != nil {
		// If the turn was cancelled externally (supervisor/API), treat as cancellation.
		if errors.Is(err, chat.ErrInvalidStatusTransition) {
			current, getErr := e.chat.GetTurn(ctx, rt.turn.ID)
			if getErr == nil && isTerminalTurnStatus(current.Status) {
				e.logger.Warn("continuation skip complete for terminal turn",
					"session_id", rt.session.ID,
					"turn_id", rt.turn.ID,
					"turn_status", strings.ToLower(strings.TrimSpace(current.Status)),
				)
				rt.turn = current
			} else {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return e.handleCancellation(ctx, rt)
				}
				return e.describeTurnTransitionError(ctx, rt.turn.ID, "continueTurn CompleteTurn", "in_progress->completed", err)
			}
		} else {
			return e.describeTurnTransitionError(ctx, rt.turn.ID, "continueTurn CompleteTurn", "in_progress->completed", err)
		}
	}

	currentTurn, err := e.chat.GetTurn(ctx, rt.turn.ID)
	if err != nil {
		return err
	}
	cycleID := currentTurn.CycleID
	if cycleID == nil {
		created := uuid.New()
		cycleID = &created
	}

	turns, err := e.turns.ListBySession(ctx, rt.session.ID)
	if err != nil {
		return err
	}
	nextTurnNumber := 1
	for _, item := range turns {
		if item.TurnNumber >= nextTurnNumber {
			nextTurnNumber = item.TurnNumber + 1
		}
	}

	created, err := e.turns.Create(ctx, repo.ChatTurn{
		SessionID:      rt.session.ID,
		TurnNumber:     nextTurnNumber,
		CycleID:        cycleID,
		RespondingType: "agent",
		RespondingID:   rt.agent.ID,
		Status:         "pending",
	})
	if err != nil {
		return err
	}
	if _, err := e.sessions.UpdateCurrentTurn(ctx, rt.session.ID, &created.ID); err != nil {
		return err
	}
	if _, err := e.sessions.IncrementCounts(ctx, rt.session.ID, 1, 0); err != nil {
		return err
	}
	if err := e.chat.StartTurn(ctx, created.ID); err != nil {
		return fmt.Errorf("continueTurn StartTurn: %w", err)
	}

	rt.turn, err = e.chat.GetTurn(ctx, created.ID)
	if err != nil {
		return err
	}
	rt.startedAt = e.turnStartTime(rt.turn)
	rt.toolCallsUsed = 0
	rt.modelRetryUsed = 0
	rt.invocationAttempt = 0
	rt.stopReason = ""

	profile, err := e.resolveModelProfile(ctx, rt.session, rt.agent, "continuation_summary")
	if err != nil {
		return err
	}
	messages, _ := e.messages.ListBySession(ctx, rt.session.ID)
	recent := lastNUserMessages(messages, 3)
	resp, err := e.models.Complete(ctx, ModelRequest{
		OrganizationID:  rt.session.OrganizationID,
		SessionID:       rt.session.ID,
		TurnID:          rt.turn.ID,
		AgentID:         rt.agent.ID,
		Purpose:         "continuation_summary",
		Profile:         profile,
		HumanMessages:   recent,
		InstructionHint: "Summarize the work completed so far and what remains.",
	})
	if err != nil {
		return err
	}
	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		summary = "Continuation summary unavailable."
	}
	_, err = e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, "[Continuation summary] "+summary)
	return err
}

func (e *TurnEngine) requireTurnInProgress(ctx context.Context, rt *turnRuntime) error {
	if rt == nil || rt.turn == nil {
		return fmt.Errorf("runTurn requires an initialized turn runtime")
	}
	current, err := e.chat.GetTurn(ctx, rt.turn.ID)
	if err != nil {
		return fmt.Errorf("runTurn GetTurn: %w", err)
	}
	rt.turn = current
	if strings.EqualFold(strings.TrimSpace(current.Status), "in_progress") {
		return nil
	}
	return fmt.Errorf(
		"runTurn preflight invalid turn state (operation=execute_turn expected_status=in_progress turn_status=%s turn_id=%s): %w",
		strings.ToLower(strings.TrimSpace(current.Status)),
		current.ID,
		chat.ErrInvalidStatusTransition,
	)
}

func (e *TurnEngine) describeTurnTransitionError(ctx context.Context, turnID uuid.UUID, operation, transition string, err error) error {
	if !errors.Is(err, chat.ErrInvalidStatusTransition) {
		return err
	}
	status := "unknown"
	if current, getErr := e.chat.GetTurn(ctx, turnID); getErr == nil {
		status = strings.ToLower(strings.TrimSpace(current.Status))
	}
	return fmt.Errorf(
		"%s invalid turn transition (transition=%s turn_id=%s turn_status=%s): %w",
		operation,
		transition,
		turnID,
		status,
		err,
	)
}

func isTerminalTurnStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "cancelled", "failed":
		return true
	default:
		return false
	}
}

func (e *TurnEngine) shouldContinueMaxToolCalls(ctx context.Context, rt *turnRuntime) (bool, error) {
	if !strings.EqualFold(strings.TrimSpace(rt.session.Mode), "async") {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.stopReason), stopReasonMaxToolCalls) {
		return false, nil
	}
	continuations, err := e.cycleContinuationCount(ctx, rt)
	if err != nil {
		return false, err
	}
	return continuations < maxContinuationTurnDepth, nil
}

func (e *TurnEngine) cycleContinuationCount(ctx context.Context, rt *turnRuntime) (int, error) {
	if rt == nil || rt.turn == nil || rt.turn.CycleID == nil {
		return 0, nil
	}
	turns, err := e.turns.ListBySession(ctx, rt.session.ID)
	if err != nil {
		return 0, err
	}
	matches := 0
	for _, turn := range turns {
		if turn.CycleID != nil && *turn.CycleID == *rt.turn.CycleID {
			matches++
		}
	}
	if matches <= 1 {
		return 0, nil
	}
	return matches - 1, nil
}

func (e *TurnEngine) recordStopReason(ctx context.Context, rt *turnRuntime) error {
	reason := strings.TrimSpace(rt.stopReason)
	if reason == "" {
		return nil
	}
	updated, err := e.turns.SetStopReason(ctx, rt.turn.ID, &reason)
	if err != nil {
		return err
	}
	rt.turn.StopReason = updated.StopReason
	return nil
}

func (e *TurnEngine) runListeningEval(ctx context.Context, rt *turnRuntime, assembled *prompt.AssembledPrompt) (bool, error) {
	pending, err := e.pendingHumanMessages(ctx, rt.session.ID)
	if err != nil {
		return false, err
	}
	if rt.session.Mode != "async" && pending <= 1 {
		return false, nil
	}

	profile, err := e.resolveModelProfile(ctx, rt.session, rt.agent, "listening_eval")
	if err != nil {
		return false, err
	}

	messages, _ := e.messages.ListBySession(ctx, rt.session.ID)
	last3 := lastNUserMessages(messages, 3)
	resp, err := e.models.Complete(ctx, ModelRequest{
		OrganizationID:  rt.session.OrganizationID,
		SessionID:       rt.session.ID,
		TurnID:          rt.turn.ID,
		AgentID:         rt.agent.ID,
		Purpose:         "listening_eval",
		Profile:         profile,
		Prompt:          assembled,
		HumanMessages:   last3,
		InstructionHint: "Is there more context incoming, or should I respond now?",
	})
	if err != nil {
		return false, err
	}
	decision := strings.ToLower(strings.TrimSpace(resp.Content))
	return strings.HasPrefix(decision, "wait"), nil
}

func (e *TurnEngine) dispatchTools(ctx context.Context, rt *turnRuntime, calls []ModelToolCall) (bool, error) {
	rt.stopReason = ""
	if len(calls) == 0 {
		return false, nil
	}

	// Build reverse map: sanitized API name → original tool name, and tier lookup.
	apiNameToOriginal := make(map[string]string, len(rt.toolSet))
	apiNameToTier := make(map[string]string, len(rt.toolSet))
	for _, td := range rt.toolSet {
		apiName := td.APIName
		if apiName == "" {
			apiName = tools.SanitizeToolNameForAPI(td.Name)
		}
		apiNameToOriginal[apiName] = td.Name
		apiNameToTier[apiName] = td.Tier
	}

	toolCalls := make([]ToolCall, 0, len(calls))
	for i, call := range calls {
		id := strings.TrimSpace(call.ID)
		if id == "" {
			id = fmt.Sprintf("tool-%d", i+1)
		}
		// Resolve the model-returned name (may be sanitized) back to the original.
		name := strings.TrimSpace(call.Name)
		if orig, ok := apiNameToOriginal[name]; ok {
			name = orig
		}
		// Resolve tier from tool set if not provided by model.
		tier := strings.TrimSpace(strings.ToLower(call.Tier))
		if tier == "" {
			if t, ok := apiNameToTier[strings.TrimSpace(call.Name)]; ok {
				tier = strings.ToLower(t)
			}
		}
		arguments := cloneMap(call.Arguments)
		arguments["organization_id"] = rt.session.OrganizationID.String()
		if _, exists := arguments["session_id"]; !exists {
			arguments["session_id"] = rt.session.ID.String()
		}
		arguments["turn_id"] = rt.turn.ID.String()
		if _, exists := arguments["agent_id"]; !exists {
			arguments["agent_id"] = rt.agent.ID.String()
		}
		if projectID := resolveProjectID(ctx, rt.session, e.tasks); projectID != nil {
			if _, exists := arguments["project_id"]; !exists {
				arguments["project_id"] = projectID.String()
			}
		}
		if taskID := resolveTaskID(rt.session); taskID != nil {
			if _, exists := arguments["task_id"]; !exists {
				arguments["task_id"] = taskID.String()
			}
		}
		toolCalls = append(toolCalls, ToolCall{
			ID:              id,
			Name:            name,
			Tier:            tier,
			Arguments:       arguments,
			MCPConnectionID: call.MCPConnectionID,
		})
	}

	maxDuration := e.syncMaxDuration
	if strings.EqualFold(rt.session.Mode, "async") {
		maxDuration = e.asyncMaxDuration
	}

	toolBudget := e.maxToolCalls
	if toolBudget < 1 {
		toolBudget = 1
	}
	if rt.toolCallsUsed >= toolBudget {
		rt.stopReason = stopReasonMaxToolCalls
		_, _ = e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, "[Max tool calls reached. Turn ended.]")
		return true, nil
	}

	tier1 := make([]ToolCall, 0, len(toolCalls))
	tier2 := make([]ToolCall, 0, len(toolCalls))
	for _, call := range toolCalls {
		if strings.EqualFold(call.Tier, "tier2") {
			tier2 = append(tier2, call)
		} else {
			tier1 = append(tier1, call)
		}
	}

	if len(tier1) > 0 {
		if e.now().After(rt.startedAt.Add(maxDuration)) {
			rt.stopReason = stopReasonMaxDuration
			_, _ = e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, "[Turn duration limit reached. Turn ended.]")
			return true, nil
		}
		if rt.toolCallsUsed >= toolBudget {
			rt.stopReason = stopReasonMaxToolCalls
			_, _ = e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, "[Max tool calls reached. Turn ended.]")
			return true, nil
		}
		runCalls := tier1
		budgetRemaining := toolBudget - rt.toolCallsUsed
		if len(runCalls) > budgetRemaining {
			runCalls = runCalls[:budgetRemaining]
		}
		results, err := e.dispatchTier1Concurrent(ctx, runCalls)
		if err != nil {
			return false, err
		}
		if err := e.appendToolResults(ctx, rt, results); err != nil {
			return false, err
		}
		rt.toolCallsUsed += len(runCalls)
		if rt.toolCallsUsed >= toolBudget {
			rt.stopReason = stopReasonMaxToolCalls
			_, _ = e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, "[Max tool calls reached. Turn ended.]")
			return true, nil
		}
	}

	for _, call := range tier2 {
		if e.now().After(rt.startedAt.Add(maxDuration)) {
			rt.stopReason = stopReasonMaxDuration
			_, _ = e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, "[Turn duration limit reached. Turn ended.]")
			return true, nil
		}
		if rt.toolCallsUsed >= toolBudget {
			rt.stopReason = stopReasonMaxToolCalls
			_, _ = e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, "[Max tool calls reached. Turn ended.]")
			return true, nil
		}

		var runID *uuid.UUID
		result, err := e.dispatcher.DispatchTier2(ctx, call, func(id uuid.UUID) {
			runID = &id
			rt.setActiveTier2Run(id)
		})
		rt.clearActiveTier2Run()
		if err != nil {
			result = ToolResult{ToolCallID: call.ID, Name: call.Name, Error: fmt.Sprintf("%s failed: %s", call.Name, err.Error()), RunID: runID}
		}
		if err := e.appendToolResults(ctx, rt, []ToolResult{result}); err != nil {
			return false, err
		}
		rt.toolCallsUsed++
		if rt.toolCallsUsed >= toolBudget {
			rt.stopReason = stopReasonMaxToolCalls
			_, _ = e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, "[Max tool calls reached. Turn ended.]")
			return true, nil
		}
	}

	return false, nil
}

func (e *TurnEngine) dispatchTier1Concurrent(ctx context.Context, calls []ToolCall) ([]ToolResult, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	results := make([]ToolResult, len(calls))
	errCh := make(chan error, len(calls))
	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			result, err := e.dispatcher.DispatchTier1(ctx, calls[index])
			if err != nil {
				results[index] = ToolResult{ToolCallID: calls[index].ID, Name: calls[index].Name, Error: fmt.Sprintf("%s failed: %s", calls[index].Name, err.Error())}
				return
			}
			results[index] = result
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

func (e *TurnEngine) appendToolResults(ctx context.Context, rt *turnRuntime, results []ToolResult) error {
	for _, result := range results {
		payload := map[string]any{
			"tool_name": result.Name,
		}
		if len(result.Output) > 0 {
			payload["output"] = result.Output
		}
		if strings.TrimSpace(result.Error) != "" {
			payload["error"] = strings.TrimSpace(result.Error)
		}
		if result.RunID != nil {
			payload["run_id"] = result.RunID.String()
		}
		raw, _ := json.Marshal(payload)
		toolCallID := result.ToolCallID
		message, err := e.chat.AppendMessage(ctx, chat.AppendMessageInput{
			SessionID:  rt.session.ID,
			TurnID:     &rt.turn.ID,
			Role:       "tool_result",
			Content:    string(raw),
			ToolCallID: &toolCallID,
		})
		if err != nil {
			return err
		}
		if _, err := e.messages.UpdateStatus(ctx, message.ID, "final", ""); err != nil {
			return err
		}
	}
	return nil
}

func (e *TurnEngine) callMainModel(
	ctx context.Context,
	rt *turnRuntime,
	profile repo.ModelProfile,
	assembled *prompt.AssembledPrompt,
	assistant *chat.ChatMessage,
	chunkSeq *int64,
) (ModelResponse, error) {
	var lastInvocationID *uuid.UUID

	for {
		if rt.modelRetryUsed >= e.modelRetryBudget {
			return ModelResponse{}, fmt.Errorf("model retry budget exhausted")
		}
		rt.invocationAttempt++
		invocationMetadata := buildInvocationMetadata(assembled)
		invocation, err := e.invocations.Create(ctx, repo.ModelInvocation{
			OrganizationID:           rt.session.OrganizationID,
			ModelProviderID:          profile.ProviderID,
			ModelProfileID:           stringPtr(profile.LogicalProfileID),
			InvocationPurpose:        "agent_turn",
			Status:                   "in_flight",
			ModelName:                profile.ModelName,
			IsStreaming:              true,
			AttemptNumber:            rt.invocationAttempt,
			FallbackFromInvocationID: lastInvocationID,
			AgentID:                  &rt.agent.ID,
			ProjectID:                resolveProjectID(ctx, rt.session, e.tasks),
			ProjectTaskID:            resolveTaskID(rt.session),
			SessionID:                &rt.session.ID,
			TurnID:                   &rt.turn.ID,
			RunID:                    nil,
			RunStepID:                nil,
			RunAttemptID:             nil,
			Metadata:                 invocationMetadata,
		})
		if err != nil {
			return ModelResponse{}, err
		}
		lastInvocationID = &invocation.ID

		started := e.now()
		tokensSeen := 0
		streamingMarked := false
		builder := strings.Builder{}
		lastSteerPollChunks := 0

		response, callErr := e.models.StreamComplete(ctx, ModelRequest{
			OrganizationID: rt.session.OrganizationID,
			SessionID:      rt.session.ID,
			TurnID:         rt.turn.ID,
			AgentID:        rt.agent.ID,
			Purpose:        "agent_turn",
			Profile:        profile,
			Prompt:         assembled,
		}, func(token string) error {
			if !streamingMarked {
				if err := e.chat.UpdateMessageStatus(ctx, assistant.ID, "streaming", ""); err != nil {
					return err
				}
				streamingMarked = true
			}
			tokensSeen++
			builder.WriteString(token)
			if _, err := e.messages.UpdateContent(ctx, assistant.ID, builder.String()); err != nil {
				return err
			}
			*chunkSeq++
			if err := e.publishEvent(ctx, rt.session.OrganizationID, "chat.message.chunk", "agent", &rt.agent.ID, map[string]any{
				"session_id": rt.session.ID,
				"turn_id":    rt.turn.ID,
				"message_id": assistant.ID,
				"delta":      token,
				"sequence":   *chunkSeq,
			}); err != nil {
				return err
			}

			lastSteerPollChunks++
			if lastSteerPollChunks >= chunkPollSteerEveryNChunks {
				lastSteerPollChunks = 0
				_, _ = e.findSteerMessages(ctx, rt.session.ID, rt.startedAt)
			}
			return nil
		})

		if callErr != nil {
			errorCode := stringPtr("model_error")
			errorText := stringPtr(callErr.Error())
			_, _ = e.invocations.UpdateStatus(ctx, invocation.ID, "failed", errorCode, errorText)

			if tokensSeen > 0 {
				_ = e.chat.UpdateMessageStatus(ctx, assistant.ID, "failed", callErr.Error())
				return ModelResponse{}, callErr
			}
			if isTransientModelError(callErr) {
				rt.modelRetryUsed++
				if rt.modelRetryUsed >= e.modelRetryBudget {
					_ = e.chat.UpdateMessageStatus(ctx, assistant.ID, "failed", callErr.Error())
					return ModelResponse{}, callErr
				}
				if sleepErr := e.sleep(ctx, retryBackoff(rt.modelRetryUsed)); sleepErr != nil {
					return ModelResponse{}, sleepErr
				}
				continue
			}
			_ = e.chat.UpdateMessageStatus(ctx, assistant.ID, "failed", callErr.Error())
			return ModelResponse{}, callErr
		}

		content := builder.String()
		if strings.TrimSpace(content) == "" {
			content = strings.TrimSpace(response.Content)
		}
		if _, err := e.messages.UpdateContent(ctx, assistant.ID, content); err != nil {
			return ModelResponse{}, err
		}
		_ = e.publishEvent(ctx, rt.session.OrganizationID, "chat.message.finalized", "agent", &rt.agent.ID, map[string]any{
			"session_id": rt.session.ID,
			"message_id": assistant.ID,
			"role":       "assistant",
			"content":    content,
		})

		usage := response.Usage
		if usage == nil {
			usage = &ModelUsage{InputTokens: estimateTokensFromPrompt(assembled), OutputTokens: maxInt(tokensSeen, estimateTokens(content))}
		}
		_ = e.invocations.UpdateCompletion(ctx, invocation.ID,
			usage.InputTokens,
			usage.OutputTokens,
			usage.CacheReadTokens,
			maxInt(0, int(e.now().Sub(started).Milliseconds())),
			maxInt(0, int(e.now().Sub(started).Milliseconds())),
			nil,
			nil,
		)
		return response, nil
	}
}

func (e *TurnEngine) handleCancellation(ctx context.Context, rt *turnRuntime) error {
	if runID := rt.getActiveTier2Run(); runID != nil && e.runCanceler != nil {
		_ = e.runCanceler.RequestCancel(context.Background(), *runID, controlplane.CancelRequestActor{Type: "system"})
	}

	messages, _ := e.messages.ListBySession(ctx, rt.session.ID)
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.TurnID == nil || *m.TurnID != rt.turn.ID {
			continue
		}
		if strings.EqualFold(m.Role, "assistant") && strings.EqualFold(m.Status, "streaming") {
			_, _ = e.messages.UpdateStatus(ctx, m.ID, "failed", "cancelled")
			break
		}
	}
	_, _ = e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, "[Turn cancelled by user.]")
	_ = e.chat.CancelTurn(context.Background(), rt.turn.ID, "user_cancelled")
	return errTurnCancelled
}

func (e *TurnEngine) watchTurnCancellation(ctx context.Context, rt *turnRuntime) (context.Context, func()) {
	cancelCtx, cancel := context.WithCancel(ctx)
	consumer := e.cancelConsumerName
	sub := e.events.Subscribe(consumer, &rt.session.OrganizationID, func(_ context.Context, event eventbus.DomainEvent) error {
		if event.EventType != "chat.turn.cancelled" {
			return nil
		}
		payload := map[string]any{}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil
		}
		turnID, ok := payloadUUID(payload, "turn_id")
		if !ok || turnID != rt.turn.ID {
			return nil
		}
		if runID := rt.getActiveTier2Run(); runID != nil && e.runCanceler != nil {
			_ = e.runCanceler.RequestCancel(context.Background(), *runID, controlplane.CancelRequestActor{Type: "system"})
		}
		cancel()
		return nil
	})

	cleanup := func() {
		cancel()
		e.events.Unsubscribe(sub)
	}
	return cancelCtx, cleanup
}

func (e *TurnEngine) resolveSessionAgent(ctx context.Context, sessionID uuid.UUID) (uuid.UUID, error) {
	session, err := e.chat.GetSession(ctx, sessionID)
	if err != nil {
		return uuid.Nil, err
	}
	return e.resolveSessionAgentForSession(ctx, session)
}

func (e *TurnEngine) resolveSessionAgentForSession(ctx context.Context, session *chat.ChatSession) (uuid.UUID, error) {
	if session == nil {
		return uuid.Nil, repo.ErrNotFound
	}
	scopeType := strings.TrimSpace(session.ScopeType)
	if strings.EqualFold(scopeType, "project_task") {
		if agentID, err := e.resolveTaskScopeAgent(ctx, session.OrganizationID, session.ScopeID); err == nil && agentID != uuid.Nil {
			return agentID, nil
		}
	}
	if strings.EqualFold(scopeType, "project") {
		// For project-scoped sessions, prefer the project PM, then fall back to Frank.
		if e.assignments != nil && session.ScopeID != uuid.Nil {
			pm, pmErr := e.assignments.GetPM(ctx, session.ScopeID)
			if pmErr == nil && pm.IsActive && pm.AgentID != uuid.Nil {
				if err := e.ensureAgentParticipant(ctx, session.ID, pm.AgentID); err != nil {
					return uuid.Nil, err
				}
				return pm.AgentID, nil
			}
		}
		frankID, err := e.resolveFrankStarterID(ctx, session.OrganizationID)
		if err != nil {
			return uuid.Nil, err
		}
		if err := e.ensureAgentParticipant(ctx, session.ID, frankID); err != nil {
			return uuid.Nil, err
		}
		return frankID, nil
	}
	if strings.EqualFold(scopeType, "organization") {
		// For org-scoped sessions, always route to Frank (the receptionist).
		return e.resolveFrankStarterID(ctx, session.OrganizationID)
	}
	return e.resolveFirstAgentParticipant(ctx, session.ID)
}

func (e *TurnEngine) ensureAgentParticipant(ctx context.Context, sessionID, agentID uuid.UUID) error {
	if sessionID == uuid.Nil || agentID == uuid.Nil {
		return nil
	}
	if _, err := e.chat.AddParticipant(ctx, sessionID, "agent", agentID, "member"); err != nil && !errors.Is(err, chat.ErrAlreadyParticipant) {
		return err
	}
	return nil
}

func (e *TurnEngine) resolveTaskScopeAgent(ctx context.Context, organizationID, taskID uuid.UUID) (uuid.UUID, error) {
	if e.tasks != nil && taskID != uuid.Nil {
		if taskRecord, err := e.tasks.GetByID(ctx, taskID); err == nil {
			if taskRecord.OrganizationID != uuid.Nil && taskRecord.OrganizationID != organizationID {
				return uuid.Nil, repo.ErrNotFound
			}
			if taskRecord.AssignedAgentID != nil && *taskRecord.AssignedAgentID != uuid.Nil {
				return *taskRecord.AssignedAgentID, nil
			}
			if e.assignments != nil {
				if pm, pmErr := e.assignments.GetPM(ctx, taskRecord.ProjectID); pmErr == nil && pm.IsActive && pm.AgentID != uuid.Nil {
					return pm.AgentID, nil
				}
			}
		}
	}
	return e.resolveFrankStarterID(ctx, organizationID)
}

func (e *TurnEngine) resolveFrankStarterID(ctx context.Context, organizationID uuid.UUID) (uuid.UUID, error) {
	if e.agents == nil {
		return uuid.Nil, repo.ErrNotFound
	}
	starterAgents, err := e.agents.GetStarterTrio(ctx, organizationID)
	if err != nil {
		return uuid.Nil, err
	}
	for _, item := range starterAgents {
		if strings.EqualFold(strings.TrimSpace(item.DisplayName), "frank") && item.ID != uuid.Nil {
			return item.ID, nil
		}
	}
	for _, item := range starterAgents {
		if strings.EqualFold(strings.TrimSpace(item.AgentType), "general") && item.ID != uuid.Nil {
			return item.ID, nil
		}
	}
	for _, item := range starterAgents {
		if item.ID != uuid.Nil {
			return item.ID, nil
		}
	}
	return uuid.Nil, repo.ErrNotFound
}

func (e *TurnEngine) resolveFirstAgentParticipant(ctx context.Context, sessionID uuid.UUID) (uuid.UUID, error) {
	participants, err := e.chat.ListParticipants(ctx, sessionID)
	if err != nil {
		return uuid.Nil, err
	}
	for _, participant := range participants {
		if participant == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(participant.ParticipantType), "agent") {
			return participant.ParticipantID, nil
		}
	}
	return uuid.Nil, repo.ErrNotFound
}

func (e *TurnEngine) resolveModelProfile(ctx context.Context, session *chat.ChatSession, agent repo.Agent, purpose string) (repo.ModelProfile, error) {
	scopes := make([]model.Scope, 0, 3)
	scopes = append(scopes, model.Scope{Type: "agent", ID: agent.ID})
	if projectID := resolveProjectID(ctx, session, e.tasks); projectID != nil {
		scopes = append(scopes, model.Scope{Type: "project", ID: *projectID})
	}

	if e.resolver != nil {
		profile, err := e.resolver.Resolve(ctx, session.OrganizationID, strings.TrimSpace(purpose), scopes...)
		if err == nil && profile != nil {
			return *profile, nil
		}
	}

	candidates := make([]string, 0, 4)
	if strings.TrimSpace(purpose) == "listening_eval" || strings.TrimSpace(purpose) == "continuation_summary" {
		candidates = append(candidates, "haiku")
	}
	if agent.DefaultModelProfileID != nil && strings.TrimSpace(*agent.DefaultModelProfileID) != "" {
		candidates = append(candidates, strings.TrimSpace(*agent.DefaultModelProfileID))
	}
	if e.defaultModelProfileID != nil && strings.TrimSpace(*e.defaultModelProfileID) != "" {
		candidates = append(candidates, strings.TrimSpace(*e.defaultModelProfileID))
	}
	candidates = append(candidates, "high-capability", "haiku")

	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		profile, err := e.profiles.GetCurrentByLogicalID(ctx, session.OrganizationID, candidate)
		if err == nil {
			return profile, nil
		}
	}

	return repo.ModelProfile{}, model.ErrNoProfileAssigned
}

func (e *TurnEngine) appendAssistantPlaceholder(ctx context.Context, turnID, sessionID, agentID uuid.UUID) (*chat.ChatMessage, error) {
	authorType := "agent"
	return e.chat.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  sessionID,
		TurnID:     &turnID,
		AuthorType: &authorType,
		AuthorID:   &agentID,
		Role:       "assistant",
		Content:    "",
	})
}

func (e *TurnEngine) appendSystemMessage(ctx context.Context, turnID, sessionID uuid.UUID, content string) (*chat.ChatMessage, error) {
	msg, err := e.chat.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: sessionID,
		TurnID:    &turnID,
		Role:      "system",
		Content:   strings.TrimSpace(content),
	})
	if err != nil {
		return nil, err
	}
	if err := e.chat.UpdateMessageStatus(ctx, msg.ID, "streaming", ""); err != nil {
		return nil, fmt.Errorf("appendSystemMessage pending->streaming: %w", err)
	}
	if err := e.chat.UpdateMessageStatus(ctx, msg.ID, "final", ""); err != nil {
		return nil, fmt.Errorf("appendSystemMessage streaming->final: %w", err)
	}
	return msg, nil
}

func (e *TurnEngine) pendingHumanMessages(ctx context.Context, sessionID uuid.UUID) (int, error) {
	items, err := e.messages.ListBySession(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, item := range items {
		if !strings.EqualFold(strings.TrimSpace(item.Role), "user") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(item.Status), "pending") {
			continue
		}
		count++
	}
	return count, nil
}

func (e *TurnEngine) findSteerMessages(ctx context.Context, sessionID uuid.UUID, startedAt time.Time) ([]repo.ChatMessage, error) {
	messages, err := e.messages.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	items := make([]repo.ChatMessage, 0)
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			continue
		}
		if message.CreatedAt.UTC().After(startedAt.UTC()) {
			items = append(items, message)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].SequenceNumber < items[j].SequenceNumber
	})
	return items, nil
}

func (e *TurnEngine) publishEvent(ctx context.Context, orgID uuid.UUID, eventType, actorType string, actorID *uuid.UUID, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return e.events.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: orgID,
		EventType:      strings.TrimSpace(eventType),
		ActorType:      strings.TrimSpace(actorType),
		ActorID:        actorID,
		Payload:        raw,
	})
}

func (rt *turnRuntime) setActiveTier2Run(runID uuid.UUID) {
	rt.activeTier2RunMu.Lock()
	defer rt.activeTier2RunMu.Unlock()
	copied := runID
	rt.activeTier2RunID = &copied
}

func (rt *turnRuntime) clearActiveTier2Run() {
	rt.activeTier2RunMu.Lock()
	defer rt.activeTier2RunMu.Unlock()
	rt.activeTier2RunID = nil
}

func (rt *turnRuntime) getActiveTier2Run() *uuid.UUID {
	rt.activeTier2RunMu.RLock()
	defer rt.activeTier2RunMu.RUnlock()
	if rt.activeTier2RunID == nil {
		return nil
	}
	copyID := *rt.activeTier2RunID
	return &copyID
}

func (e *TurnEngine) turnStartTime(turn *chat.ChatTurn) time.Time {
	if turn != nil && turn.StartedAt != nil {
		return turn.StartedAt.UTC()
	}
	return e.now()
}

func parseAgentTurnPayload(raw json.RawMessage) (AgentTurnPayload, error) {
	var payload AgentTurnPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return AgentTurnPayload{}, err
	}
	if payload.SessionID == uuid.Nil || payload.MessageID == uuid.Nil {
		return AgentTurnPayload{}, fmt.Errorf("missing session_id or message_id")
	}
	if payload.RetryCount < 0 {
		payload.RetryCount = 0
	}
	return payload, nil
}

func latestCompletedTurn(turns []repo.ChatTurn) *repo.ChatTurn {
	var latest *repo.ChatTurn
	for i := range turns {
		turn := turns[i]
		if !strings.EqualFold(strings.TrimSpace(turn.Status), "completed") {
			continue
		}
		if latest == nil || turn.TurnNumber > latest.TurnNumber {
			copyTurn := turn
			latest = &copyTurn
		}
	}
	return latest
}

func latestUserMessage(messages []repo.ChatMessage) *repo.ChatMessage {
	var latest *repo.ChatMessage
	for i := range messages {
		message := messages[i]
		if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			continue
		}
		if latest == nil || message.SequenceNumber > latest.SequenceNumber {
			copyMessage := message
			latest = &copyMessage
		}
	}
	return latest
}

func latestAssistantFinalForTurn(messages []repo.ChatMessage, turnID uuid.UUID) *repo.ChatMessage {
	var latest *repo.ChatMessage
	for i := range messages {
		message := messages[i]
		if message.TurnID == nil || *message.TurnID != turnID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(message.Status), "final") {
			continue
		}
		if latest == nil || message.SequenceNumber > latest.SequenceNumber {
			copyMessage := message
			latest = &copyMessage
		}
	}
	return latest
}

func consecutiveAutoTurnsSinceLatestUser(turns []repo.ChatTurn, messages []repo.ChatMessage, latestUserSequence int64) int {
	if latestUserSequence <= 0 {
		return 0
	}
	maxMessageSeqByTurn := make(map[uuid.UUID]int64)
	for _, message := range messages {
		if message.TurnID == nil || *message.TurnID == uuid.Nil {
			continue
		}
		current := maxMessageSeqByTurn[*message.TurnID]
		if message.SequenceNumber > current {
			maxMessageSeqByTurn[*message.TurnID] = message.SequenceNumber
		}
	}

	completedSinceUser := 0
	for _, turn := range turns {
		if !strings.EqualFold(strings.TrimSpace(turn.Status), "completed") {
			continue
		}
		if maxMessageSeqByTurn[turn.ID] <= latestUserSequence {
			continue
		}
		completedSinceUser++
	}
	if completedSinceUser <= 1 {
		return 0
	}
	return completedSinceUser - 1
}

func indicatesTaskCompletion(content string) bool {
	normalized := strings.ToLower(strings.TrimSpace(content))
	if normalized == "" {
		return false
	}
	if normalized == "done" || normalized == "complete" || normalized == "completed" {
		return true
	}
	phrases := []string{
		"task complete",
		"task completed",
		"work complete",
		"work completed",
		"completed the task",
		"all done",
		"i'm done",
		"i am done",
		"ready for review",
		"awaiting review",
		"please review",
		"mark this done",
		"marking this done",
	}
	for _, phrase := range phrases {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

type rateLimitRetryAfterProvider interface {
	RateLimitRetryAfter() time.Duration
}

func rateLimitRetryAfterHint(err error) time.Duration {
	if err == nil {
		return 0
	}
	var provider rateLimitRetryAfterProvider
	if errors.As(err, &provider) {
		hint := provider.RateLimitRetryAfter()
		if hint > 0 {
			return hint
		}
	}
	return 0
}

func rateLimitRetryDelay(retryCount int, retryAfterHint time.Duration) time.Duration {
	if retryAfterHint > 0 {
		return retryAfterHint
	}
	if retryCount < 0 {
		retryCount = 0
	}

	delay := defaultRateLimitBackoff
	for i := 0; i < retryCount; i++ {
		if delay >= (maxRateLimitBackoff / 2) {
			return maxRateLimitBackoff
		}
		delay *= 2
	}
	if delay > maxRateLimitBackoff {
		return maxRateLimitBackoff
	}
	return delay
}

func formatRetryDelay(delay time.Duration) string {
	if delay <= 0 {
		delay = time.Second
	}
	return delay.Round(time.Second).String()
}

func payloadUUID(payload map[string]any, key string) (uuid.UUID, bool) {
	value, ok := payload[key]
	if !ok {
		return uuid.Nil, false
	}
	text, ok := value.(string)
	if !ok {
		return uuid.Nil, false
	}
	parsed, err := uuid.Parse(strings.TrimSpace(text))
	if err != nil {
		return uuid.Nil, false
	}
	return parsed, true
}

func payloadString(payload map[string]any, key string) (string, bool) {
	value, ok := payload[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(text), true
}

func reactionDelta(emoji string) (float64, bool) {
	normalized := strings.TrimSpace(strings.ToLower(emoji))
	switch normalized {
	case "👍", "✅", "❤️", ":thumbsup:", "thumbs_up", "heart":
		return 0.05, true
	case "👎", "❌", ":thumbsdown:", "thumbs_down":
		return -0.10, true
	default:
		return 0, false
	}
}

func resolveProjectID(ctx context.Context, session *chat.ChatSession, tasks taskRepository) *uuid.UUID {
	if session == nil {
		return nil
	}
	switch strings.TrimSpace(session.ScopeType) {
	case "project":
		projectID := session.ScopeID
		return &projectID
	case "project_task":
		if tasks == nil {
			return nil
		}
		taskRecord, err := tasks.GetByID(ctx, session.ScopeID)
		if err != nil {
			return nil
		}
		projectID := taskRecord.ProjectID
		return &projectID
	default:
		return nil
	}
}

func resolveTaskID(session *chat.ChatSession) *uuid.UUID {
	if session == nil {
		return nil
	}
	if strings.TrimSpace(session.ScopeType) != "project_task" {
		return nil
	}
	id := session.ScopeID
	return &id
}

func isTransientModelError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrModelTransient) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, ErrRateLimited) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "timeout") || strings.Contains(text, "rate limit") || strings.Contains(text, "temporar") {
		return true
	}
	return false
}

func retryBackoff(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 1 * time.Second
	case 2:
		return 2 * time.Second
	default:
		return 4 * time.Second
	}
}

func estimateTokensFromPrompt(assembled *prompt.AssembledPrompt) int {
	if assembled == nil {
		return 0
	}
	if assembled.TotalTokens > 0 {
		return assembled.TotalTokens
	}
	total := 0
	for _, message := range assembled.Messages {
		total += estimateTokens(message.Content)
	}
	if total == 0 {
		total = estimateTokens(assembled.SystemPrompt)
	}
	return total
}

func buildInvocationMetadata(assembled *prompt.AssembledPrompt) json.RawMessage {
	if assembled == nil {
		return json.RawMessage(`{}`)
	}

	layerTokens := make(map[string]any, len(assembled.LayerTokens)+1)
	for key, value := range assembled.LayerTokens {
		layerTokens[key] = value
	}
	memoryTokens := 0
	if value, ok := assembled.LayerTokens["layer5"]; ok && value > 0 {
		memoryTokens = value
	}
	layerTokens["memory_injection"] = memoryTokens

	payload := map[string]any{
		"layer_token_counts":  layerTokens,
		"memory_layer_tokens": memoryTokens,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func estimateTokens(content string) int {
	if strings.TrimSpace(content) == "" {
		return 0
	}
	return len([]rune(content)) / 4
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func stringPtr(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	copyValue := trimmed
	return &copyValue
}

func lastNUserMessages(messages []repo.ChatMessage, n int) []string {
	if n <= 0 {
		return nil
	}
	items := make([]string, 0, n)
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			continue
		}
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}
		items = append(items, text)
		if len(items) == n {
			break
		}
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items
}

func summarizeFailure(err error) string {
	if err == nil {
		return "unknown error"
	}
	text := strings.TrimSpace(err.Error())
	if text == "" {
		return "unknown error"
	}
	if len(text) > 240 {
		text = text[:240]
	}
	return text
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	copied := make(map[string]any, len(input))
	for key, value := range input {
		copied[key] = value
	}
	return copied
}

// buildToolCallMetadata serializes tool calls into JSONB metadata for the
// assistant chat_message. The prompt assembler reads this metadata so that
// the assistant message can carry its tool_calls in the conversation history.
func buildToolCallMetadata(calls []ModelToolCall) json.RawMessage {
	if len(calls) == 0 {
		return nil
	}
	type storedToolCall struct {
		ID        string         `json:"id"`
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments,omitempty"`
	}
	stored := make([]storedToolCall, 0, len(calls))
	for _, c := range calls {
		stored = append(stored, storedToolCall{
			ID:        c.ID,
			Name:      c.Name,
			Arguments: c.Arguments,
		})
	}
	meta := map[string]any{"tool_calls": stored}
	raw, err := json.Marshal(meta)
	if err != nil {
		return nil
	}
	return raw
}
