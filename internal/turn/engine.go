package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
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
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/metrics"
	"github.com/samhotchkiss/otter-camp/internal/model"
	"github.com/samhotchkiss/otter-camp/internal/projectpause"
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
	"github.com/samhotchkiss/otter-camp/internal/toolargs"
	"github.com/samhotchkiss/otter-camp/internal/tools"
	"github.com/samhotchkiss/otter-camp/internal/workspace"
)

const (
	AgentTurnJobType                = "agent_turn"
	defaultAgentTurnJobPriority     = 70
	defaultMaxToolCalls             = 75
	defaultSyncMaxDuration          = 5 * time.Minute
	defaultAsyncMaxDuration         = 30 * time.Minute
	defaultListeningEvalDelay       = 500 * time.Millisecond
	defaultAutoContinueDelay        = 2 * time.Second
	defaultModelRetryBudget         = 3
	defaultRateLimitBackoff         = 30 * time.Second
	maxRateLimitBackoff             = 30 * time.Minute
	maxRateLimitRetries             = 5
	maxConsecutiveAutoTurns         = 10
	defaultSummarizeLayerBudget     = 0
	chunkPollSteerEveryNChunks      = 10
	maxContinuationTurnDepth        = 3
	defaultTurnConsumerName         = "turn-engine.user-message"
	defaultReactionConsumerName     = "turn-engine.reactions"
	defaultTurnCompletedName        = "turn-engine.turn-completed"
	defaultCancelConsumerPrefix     = "turn-engine.cancel"
	stopReasonMaxToolCalls          = "max_tool_calls"
	stopReasonMaxDuration           = "max_duration"
	stopReasonRecoveryCLIRejected   = "model_error"
	stopReasonRecoveryContinuation  = stopReasonRecoveryCLIRejected
	stopReasonRecoveryFileRejected  = "recovery_content_required"
	stopReasonRecoveryFileFallback  = stopReasonRecoveryCLIRejected
	stopReasonValidationBlocked     = "validation_loop_blocked"
	recoveryActionValidationResume  = "resume_validation_blocked_task"
	workerPromptTokenGuardrail      = 32000
	defaultPromptTokenGuardrail     = 64000
	validationLoopBlockThreshold    = 3
	validationLoopSuppressionReason = "validation_loop_blocked"
	recoveryCLIRepairBudget         = 1
	recoveryFileWriteRepairBudget   = 1
	recoveryArtifactDir             = ".ottercamp/recovery"
	recoveryResumeExcerptChars      = 3000
)

var (
	ErrModelTransient = errors.New("transient model failure")
	errTurnDeferred   = errors.New("turn deferred")
	errTurnCancelled  = errors.New("turn cancelled")
	errTurnPaused     = errors.New("turn paused")
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
	RunID           *uuid.UUID
	RunStepID       *uuid.UUID
	RunAttemptID    *uuid.UUID
	InvocationID    *uuid.UUID
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
	CreateForMessageAttempt(ctx context.Context, sessionID, agentID, messageID uuid.UUID, retryCount int) (repo.ChatTurn, bool, error)
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
	Update(ctx context.Context, task repo.ProjectTask) (repo.ProjectTask, error)
}

type taskTransitionService interface {
	MarkBlocked(ctx context.Context, taskID uuid.UUID, reason string, actor tasksvc.Actor) (*tasksvc.ProjectTask, error)
}

type flowNodeRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowNode, error)
}

type flowAdvancer interface {
	AdvanceFlow(ctx context.Context, taskID uuid.UUID, actor flowsvc.Actor) (*repo.FlowNodeExecution, error)
	RecordNodeCommit(ctx context.Context, taskID uuid.UUID, commitSHA, branchName string) (*repo.FlowNodeExecution, error)
}

type assignmentRepository interface {
	GetPM(ctx context.Context, projectID uuid.UUID) (repo.AgentProjectAssignment, error)
}

type projectRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

type organizationRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Organization, error)
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
	Pool    *pgxpool.Pool
	DataDir string

	Chat          ChatService
	ToolResolver  ToolResolver
	Assembler     PromptAssembler
	Summarization SummarizationChecker
	ModelGateway  ModelGateway
	Dispatcher    ToolDispatcher
	RunCanceler   RunCanceler
	Events        EventBus
	Enqueuer      JobEnqueuer

	Invocations     modelInvocationRepo
	ModelProfiles   modelProfileLookup
	Profiles        ProfileResolver
	Messages        messageRepository
	Turns           turnRepository
	Sessions        sessionRepository
	Agents          agentRepository
	Tasks           taskRepository
	TaskTransitions taskTransitionService
	FlowNodes       flowNodeRepository
	FlowAdvancer    flowAdvancer
	Assignments     assignmentRepository
	Projects        projectRepository
	Organizations   organizationRepository
	MemorySources   memorySourceRepository
	Memories        memoryRepository

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
	pool          *pgxpool.Pool
	dataDir       string
	chat          ChatService
	toolResolver  ToolResolver
	assembler     PromptAssembler
	summarization SummarizationChecker
	models        ModelGateway
	dispatcher    ToolDispatcher
	runCanceler   RunCanceler
	events        EventBus
	enqueuer      JobEnqueuer

	invocations     modelInvocationRepo
	profiles        modelProfileLookup
	resolver        ProfileResolver
	messages        messageRepository
	turns           turnRepository
	sessions        sessionRepository
	agents          agentRepository
	tasks           taskRepository
	taskTransitions taskTransitionService
	flowNodes       flowNodeRepository
	flowAdvancer    flowAdvancer
	assignments     assignmentRepository
	projects        projectRepository
	organizations   organizationRepository
	sources         memorySourceRepository
	memories        memoryRepository

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
	rollupUpdater         *model.RollupUpdater
}

type turnRuntime struct {
	session             *chat.ChatSession
	agent               repo.Agent
	turn                *chat.ChatTurn
	initialMessageID    uuid.UUID
	currentJobID        *uuid.UUID
	runID               *uuid.UUID
	runStepID           *uuid.UUID
	runAttemptID        *uuid.UUID
	startedAt           time.Time
	toolCallsUsed       int
	activeTier2RunID    *uuid.UUID
	activeTier2RunMu    sync.RWMutex
	modelRetryUsed      int
	invocationAttempt   int
	toolSet             []tools.ToolDescriptor
	stopReason          string
	projectIdentity     *projectIdentity
	historyStartID      *uuid.UUID
	disableMemory       bool
	freshKickoff        bool
	recoveryTurn        bool
	recoveryCLIFixes    int
	recoveryFileFixes   int
	recoveryFileWrites  map[string]recoveryPopulatedFileWriteState
	recoveryBlockReason string
	recoveryQueuedTurn  bool
}

type projectIdentity struct {
	id   uuid.UUID
	slug string
}

type recoveryPopulatedFileWriteState struct {
	TargetPath string
	Draft      string
}

type toolValidationFailure struct {
	ToolName           string
	FailureClass       string
	FailureCode        string
	FailureReason      string
	Fingerprint        string
	AttemptFingerprint string
}

type taskValidationGuardState = tasksvc.ValidationGuardState

func NewEngine(opts Options) (*TurnEngine, error) {
	needsPool := opts.Messages == nil || opts.Turns == nil || opts.Sessions == nil ||
		opts.Agents == nil || opts.Tasks == nil || opts.Projects == nil || opts.MemorySources == nil || opts.Memories == nil
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
	if opts.Projects == nil {
		opts.Projects = repo.NewProjectRepo(opts.Pool)
	}
	if opts.Organizations == nil && opts.Pool != nil {
		opts.Organizations = repo.NewOrgRepo(opts.Pool)
	}
	if opts.FlowNodes == nil && opts.Pool != nil {
		opts.FlowNodes = repo.NewFlowNodeRepo(opts.Pool)
	}
	if opts.FlowAdvancer == nil && opts.Pool != nil {
		flowAdvancer, err := flowsvc.NewService(flowsvc.Options{
			Pool:   opts.Pool,
			Events: opts.Events,
		})
		if err != nil {
			return nil, fmt.Errorf("turn engine flow advancement service: %w", err)
		}
		opts.FlowAdvancer = flowAdvancer
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
	if opts.TaskTransitions == nil && opts.Pool != nil {
		taskTransitions, err := tasksvc.NewService(tasksvc.Options{
			Pool:     opts.Pool,
			EventBus: opts.Events,
		})
		if err != nil {
			return nil, fmt.Errorf("turn engine task transition service: %w", err)
		}
		opts.TaskTransitions = taskTransitions
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

	var rollupUpdater *model.RollupUpdater
	if opts.Pool != nil {
		rollupUpdater = model.NewRollupUpdater(opts.Pool)
	}

	return &TurnEngine{
		pool:                  opts.Pool,
		dataDir:               strings.TrimSpace(opts.DataDir),
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
		taskTransitions:       opts.TaskTransitions,
		flowNodes:             opts.FlowNodes,
		flowAdvancer:          opts.FlowAdvancer,
		assignments:           opts.Assignments,
		projects:              opts.Projects,
		organizations:         opts.Organizations,
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
		rollupUpdater:         rollupUpdater,
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
	if session, err := e.chat.GetSession(ctx, payload.SessionID); err == nil {
		if paused, reason, pauseErr := e.projectPausedForSession(ctx, session); pauseErr != nil {
			return pauseErr
		} else if paused {
			e.logPausedTurnSkip("skipping queued agent turn for paused project", session, reason, payload.MessageID)
			return nil
		}
	}
	return e.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &job.ID)
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
		if paused, reason, pauseErr := e.projectPausedForSession(ctx, session); pauseErr != nil {
			return pauseErr
		} else if paused {
			e.logPausedTurnSkip("skipping agent turn enqueue for paused project", session, reason, payload.MessageID)
			return nil
		}
		if responderID, resolveErr := e.resolveSessionAgentForSession(ctx, session); resolveErr == nil && responderID != uuid.Nil {
			responderID = e.resolveNonSelfLoopResponder(ctx, session, event, responderID)
			payload.AgentID = &responderID
		}
		enqueued, enqueueErr := e.enqueueAgentTurnIfActive(ctx, session, payload, nil)
		if enqueueErr != nil {
			return enqueueErr
		}
		if !enqueued {
			return nil
		}
		return nil
	}
	_, err = e.enqueuer.Enqueue(ctx, nil, AgentTurnJobType, e.jobPriority, payload, nil)
	return err
}

func (e *TurnEngine) resolveNonSelfLoopResponder(ctx context.Context, session *chat.ChatSession, event eventbus.DomainEvent, responderID uuid.UUID) uuid.UUID {
	if session == nil || responderID == uuid.Nil {
		return responderID
	}
	if !strings.EqualFold(strings.TrimSpace(event.ActorType), "agent") || event.ActorID == nil || *event.ActorID == uuid.Nil {
		return responderID
	}
	actorID := *event.ActorID
	if responderID != actorID {
		return responderID
	}
	if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project") {
		return responderID
	}

	if participantID, err := e.resolveFirstAgentParticipantExcluding(ctx, session.ID, actorID); err == nil && participantID != uuid.Nil {
		return participantID
	}

	frankID, err := e.resolveFrankStarterID(ctx, session.OrganizationID)
	if err != nil || frankID != actorID {
		return responderID
	}
	loriID, err := e.resolveLoriStarterID(ctx, session.OrganizationID)
	if err != nil || loriID == uuid.Nil || loriID == actorID {
		return responderID
	}
	if err := e.ensureAgentParticipant(ctx, session.ID, loriID); err != nil {
		return responderID
	}
	return loriID
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
	if paused, reason, pauseErr := e.projectPausedForSession(ctx, session); pauseErr != nil {
		return pauseErr
	} else if paused {
		e.logPausedTurnSkip("skipping auto continuation for paused project", session, reason, payload.TurnID)
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
	if shouldSuppressAutoContinuationForStopReason(latestCompleted.StopReason) {
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
	if handled, handleErr := e.handleCompletedWorkTurn(ctx, taskRecord, latestCompleted.RespondingID, messages, payload.TurnID); handleErr != nil {
		return handleErr
	} else if handled {
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
	enqueued, err := e.enqueueAgentTurnIfActive(ctx, session, nextPayload, &runAfter)
	if err != nil {
		return err
	}
	if !enqueued {
		return nil
	}
	return nil
}

type workCompletionSignal struct {
	commitSHA      string
	filesCommitted int
}

func (e *TurnEngine) handleCompletedWorkTurn(ctx context.Context, taskRecord repo.ProjectTask, agentID uuid.UUID, messages []repo.ChatMessage, turnID uuid.UUID) (bool, error) {
	if e.flowAdvancer == nil || taskRecord.ID == uuid.Nil || agentID == uuid.Nil {
		return false, nil
	}
	signal, ok := completedWorkSignalFromMessages(messages, turnID)
	if !ok {
		return false, nil
	}
	if signal.commitSHA != "" {
		if _, err := e.flowAdvancer.RecordNodeCommit(ctx, taskRecord.ID, signal.commitSHA, ""); err != nil {
			return false, err
		}
	}
	if _, err := e.flowAdvancer.AdvanceFlow(ctx, taskRecord.ID, flowsvc.Actor{Type: "agent", ID: agentID}); err != nil {
		return false, err
	}
	return true, nil
}

func (e *TurnEngine) HandleUserMessage(ctx context.Context, sessionID, messageID uuid.UUID) error {
	return e.handleUserMessage(ctx, sessionID, messageID, nil, 0, nil)
}

func (e *TurnEngine) startInboundMessageTurn(ctx context.Context, turn repo.ChatTurn) (*chat.ChatTurn, bool, error) {
	if turn.ID == uuid.Nil {
		return nil, false, fmt.Errorf("turn_id is required")
	}
	if err := e.chat.StartTurn(ctx, turn.ID); err != nil {
		if !errors.Is(err, chat.ErrInvalidStatusTransition) {
			return nil, false, e.describeTurnTransitionError(ctx, turn.ID, "handleUserMessage StartTurn", "pending->in_progress", err)
		}
		current, getErr := e.chat.GetTurn(ctx, turn.ID)
		if getErr != nil {
			return nil, false, getErr
		}
		switch strings.ToLower(strings.TrimSpace(current.Status)) {
		case "in_progress", "completed", "cancelled", "failed":
			return current, false, nil
		default:
			return nil, false, e.describeTurnTransitionError(ctx, turn.ID, "handleUserMessage StartTurn", "pending->in_progress", err)
		}
	}

	startedTurn, err := e.chat.GetTurn(ctx, turn.ID)
	if err != nil {
		return nil, false, err
	}
	return startedTurn, true, nil
}

func (e *TurnEngine) handleUserMessage(ctx context.Context, sessionID, messageID uuid.UUID, routedAgentID *uuid.UUID, retryCount int, currentJobID *uuid.UUID) error {
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
	if paused, reason, pauseErr := e.projectPausedForSession(ctx, session); pauseErr != nil {
		return pauseErr
	} else if paused {
		e.logPausedTurnSkip("skipping agent turn for paused project", session, reason, messageID)
		return nil
	}
	if blocked, guard, blockErr := e.validationLoopBlockerForSession(ctx, session); blockErr != nil {
		return blockErr
	} else if blocked {
		e.logValidationLoopSuppressed("skipping agent turn for blocked validation loop", session, messageID, guard)
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
	if chat.AgentTurnDispatchCancelled(message.Metadata) {
		e.logger.Info("skipping cancelled agent turn dispatch", "session_id", sessionID, "message_id", messageID)
		return nil
	}
	cancelled, err := e.logicalMessageCancelled(ctx, sessionID, messageID)
	if err != nil {
		return err
	}
	if cancelled {
		e.logger.Info("skipping agent turn after logical message cancellation", "session_id", sessionID, "message_id", messageID)
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

	turnRecord, _, err := e.turns.CreateForMessageAttempt(ctx, sessionID, agentID, messageID, retryCount)
	if err != nil {
		return err
	}
	turn, shouldRun, err := e.startInboundMessageTurn(ctx, turnRecord)
	if err != nil {
		return err
	}
	if !shouldRun {
		return nil
	}
	if retryCount > 0 {
		if _, err := e.appendSystemMessage(ctx, turn.ID, sessionID, fmt.Sprintf("[Retry attempt %d started.]", retryCount)); err != nil {
			return err
		}
	}

	runtime := &turnRuntime{
		session:          session,
		agent:            agent,
		turn:             turn,
		initialMessageID: messageID,
		currentJobID:     cloneUUIDPointer(currentJobID),
		runID:            runIDFromMetadata(message.Metadata),
		runStepID:        runStepIDFromMetadata(message.Metadata),
		runAttemptID:     runAttemptIDFromMetadata(message.Metadata),
		startedAt:        e.turnStartTime(turn),
		recoveryTurn:     isRecoveryResumeMessage(message),
	}
	runtime.projectIdentity = e.loadProjectIdentityForMessage(ctx, sessionID, messageID)
	if isFreshKickoffRequest(session, message) {
		runtime.historyStartID = &message.ID
		runtime.disableMemory = true
		runtime.freshKickoff = true
	}

	cancelCtx, stopCancelWatch := e.watchTurnCancellation(ctx, runtime)
	defer stopCancelWatch()

	err = e.runTurn(cancelCtx, runtime)
	if err == nil || errors.Is(err, errTurnDeferred) || errors.Is(err, errTurnCancelled) || errors.Is(err, errTurnPaused) {
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
	enqueued, err := e.enqueueAgentTurnIfActive(ctx, runtime.session, nextPayload, &runAfter)
	if err != nil {
		return false, fmt.Errorf("enqueue rate-limited turn retry: %w", err)
	}

	_ = e.chat.FailTurn(ctx, runtime.turn.ID, summarizeFailure(cause))
	message := fmt.Sprintf("[Rate limited, retrying in %s...]", formatRetryDelay(retryDelay))
	if !enqueued {
		message = "[Project paused - retry deferred until resume.]"
	}
	_, _ = e.appendSystemMessage(ctx, runtime.turn.ID, runtime.session.ID, message)
	return true, nil
}

func (e *TurnEngine) runTurn(ctx context.Context, rt *turnRuntime) error {
	if err := e.requireTurnInProgress(ctx, rt); err != nil {
		return err
	}
	if rt != nil && rt.recoveryTurn && rt.historyStartID == nil {
		if _, err := e.appendRecoveryResumeState(ctx, rt, true); err != nil {
			return err
		}
	}

	continuations := 0
	listeningChecked := false
	var previousManifest *prompt.MemoryManifest

	for {
		if err := ctx.Err(); err != nil {
			return e.handleCancellation(ctx, rt)
		}

		taskComplexity := e.isComplexAgentTurnTask(ctx, rt.session)
		profile, err := e.resolveModelProfile(ctx, rt.session, rt.agent, "agent_turn", rt.modelRetryUsed, taskComplexity)
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
			HistoryStartID:   cloneUUIDPointer(rt.historyStartID),
			DisableMemory:    rt.disableMemory,
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
				if handled, handleErr := e.handleFreshKickoffBlocker(ctx, rt, "prompt context remained too large before the kickoff could reach initial task creation"); handled {
					return handleErr
				}
				if handled, handleErr := e.handleRecoveryContinuationDepthBlocker(ctx, rt, buildRecoveryContinuationDepthReason("prompt context remained too large")); handled {
					return handleErr
				}
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
		guardrail := agentTurnPromptGuardrailTokens(rt.agent, taskComplexity)
		if guardrail > 0 && assembled != nil && assembled.TotalTokens > guardrail {
			continuations++
			if continuations > maxContinuationTurnDepth {
				if handled, handleErr := e.handleFreshKickoffBlocker(ctx, rt, fmt.Sprintf("prompt input kept exceeding the %d-token guardrail before the kickoff could reach initial task creation", guardrail)); handled {
					return handleErr
				}
				if handled, handleErr := e.handleRecoveryContinuationDepthBlocker(ctx, rt, buildRecoveryContinuationDepthReason(fmt.Sprintf("prompt input kept exceeding the %d-token guardrail", guardrail))); handled {
					return handleErr
				}
				return fmt.Errorf("agent turn prompt exceeded guardrail continuation depth")
			}
			rt.stopReason = ""
			if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, fmt.Sprintf("[Prompt input exceeded %d-token guardrail - continuing in a new turn.]", guardrail)); err != nil {
				return err
			}
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
				enqueued, err := e.enqueueAgentTurnIfActive(ctx, rt.session, AgentTurnPayload{SessionID: rt.session.ID, MessageID: rt.initialMessageID}, &runAfter)
				if err != nil {
					return err
				}
				_ = e.chat.CompleteTurn(ctx, rt.turn.ID)
				if !enqueued {
					return errTurnPaused
				}
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
			if handled, handleErr := e.handleTaskScopedProviderAuthFailure(ctx, rt, err); handled {
				return handleErr
			}
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
			if _, err := e.handleToolValidationResults(ctx, rt, nil, nil); err != nil {
				return fmt.Errorf("clear validation guard: %w", err)
			}
			if err := e.completeTurn(ctx, rt); err != nil {
				return fmt.Errorf("no-tool completeTurn: %w", err)
			}
			return nil
		}
		response.ToolCalls = normalizeModelToolCalls(response.ToolCalls)

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
				return e.ensureRecoveryTurnDurableTaskState(ctx, rt)
			}
		}
		return e.describeTurnTransitionError(ctx, rt.turn.ID, "completeTurn CompleteTurn", "in_progress->completed", err)
	}
	return e.ensureRecoveryTurnDurableTaskState(ctx, rt)
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
	paused, reason, err := e.projectPausedForSession(ctx, rt.session)
	if err != nil {
		return err
	}
	if paused {
		notice := "[Project paused - continuation deferred until resume.]"
		if trimmed := strings.TrimSpace(reason); trimmed != "" {
			notice = fmt.Sprintf("[Project paused - continuation deferred until resume: %s]", trimmed)
		}
		if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, notice); err != nil {
			return err
		}
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
	if paused {
		return errTurnPaused
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
	if checkpointed, err := e.appendContentMigrationCheckpoint(ctx, rt); err != nil {
		return err
	} else if checkpointed {
		return nil
	}
	if resumed, err := e.appendRecoveryResumeState(ctx, rt, false); err != nil {
		return err
	} else if resumed {
		return nil
	}

	profile, err := e.resolveModelProfile(ctx, rt.session, rt.agent, "continuation_summary", 0, false)
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
		RunID:           cloneUUIDPointer(rt.runID),
		RunStepID:       cloneUUIDPointer(rt.runStepID),
		RunAttemptID:    cloneUUIDPointer(rt.runAttemptID),
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

func (e *TurnEngine) appendContentMigrationCheckpoint(ctx context.Context, rt *turnRuntime) (bool, error) {
	if rt == nil || rt.session == nil || rt.turn == nil || e.tasks == nil || e.projects == nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project_task") {
		return false, nil
	}
	taskRecord, err := e.tasks.GetByID(ctx, rt.session.ScopeID)
	if errors.Is(err, repo.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !taskcheckpoint.IsContentMigrationTask(taskRecord.Title, taskRecord.Description) {
		return false, nil
	}

	projectRecord, err := e.projects.GetByID(ctx, taskRecord.ProjectID)
	if err != nil {
		return false, err
	}
	workspaceRoot, err := workspace.ProjectRoot(e.dataDir, projectRecord.Slug)
	if err != nil {
		return false, err
	}
	snapshot, err := taskcheckpoint.ScanWorkspace(workspaceRoot)
	if err != nil {
		return false, err
	}

	checkpointPath := taskcheckpoint.CheckpointRelativePath(taskRecord.TaskNumber, taskRecord.ID)
	checkpointAbs := filepath.Join(workspaceRoot, filepath.FromSlash(checkpointPath))
	if err := os.MkdirAll(filepath.Dir(checkpointAbs), 0o755); err != nil {
		return false, err
	}

	checkpoint := taskcheckpoint.ContentMigrationCheckpoint{
		Version:        1,
		CheckpointPath: checkpointPath,
		UpdatedAt:      e.now().UTC().Format(time.RFC3339Nano),
		Artifacts:      snapshot.Artifacts,
		Scripts:        snapshot.Scripts,
		Outputs:        snapshot.Outputs,
	}
	if err := os.WriteFile(checkpointAbs, []byte(taskcheckpoint.BuildCheckpointDocument(buildTaskLabel(taskRecord), checkpoint)), 0o644); err != nil {
		return false, err
	}

	message, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, taskcheckpoint.BuildSystemMessage(checkpoint))
	if err != nil {
		return false, err
	}
	checkpoint.HistoryStartMessageID = message.ID.String()
	mergedMetadata, err := taskcheckpoint.MergeContentMigrationCheckpoint(taskRecord.Metadata, checkpoint)
	if err != nil {
		return false, err
	}
	taskRecord.Metadata = mergedMetadata
	if _, err := e.tasks.Update(ctx, taskRecord); err != nil {
		return false, err
	}
	rt.historyStartID = &message.ID
	return true, nil
}

func buildTaskLabel(task repo.ProjectTask) string {
	title := strings.TrimSpace(task.Title)
	if task.TaskNumber > 0 && title != "" {
		return fmt.Sprintf("OC-%d: %s", task.TaskNumber, title)
	}
	if task.TaskNumber > 0 {
		return fmt.Sprintf("OC-%d", task.TaskNumber)
	}
	if title != "" {
		return title
	}
	return task.ID.String()
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
		fallbackReason, fallbackOK := compatibleStopReasonFallback(reason)
		if !fallbackOK || !isChatTurnStopReasonConstraintError(err) {
			return err
		}
		updated, fallbackErr := e.turns.SetStopReason(ctx, rt.turn.ID, &fallbackReason)
		if fallbackErr != nil {
			return fmt.Errorf("persist stop_reason %q failed and fallback %q also failed: %w", reason, fallbackReason, fallbackErr)
		}
		sessionID := uuid.Nil
		if rt.session != nil {
			sessionID = rt.session.ID
		}
		e.logger.Warn("turn stop_reason fell back to legacy-compatible value",
			"session_id", sessionID,
			"turn_id", rt.turn.ID,
			"preferred_stop_reason", reason,
			"fallback_stop_reason", fallbackReason,
		)
		rt.stopReason = fallbackReason
		rt.turn.StopReason = updated.StopReason
		return nil
	}
	rt.turn.StopReason = updated.StopReason
	return nil
}

func compatibleStopReasonFallback(reason string) (string, bool) {
	switch strings.TrimSpace(reason) {
	case stopReasonRecoveryFileRejected:
		return stopReasonRecoveryFileFallback, true
	default:
		return "", false
	}
}

func isChatTurnStopReasonConstraintError(err error) bool {
	if err == nil || !errors.Is(err, repo.ErrConflict) {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "chat_turn_stop_reason_check")
}

func (e *TurnEngine) runListeningEval(ctx context.Context, rt *turnRuntime, assembled *prompt.AssembledPrompt) (bool, error) {
	pending, err := e.pendingHumanMessages(ctx, rt.session.ID)
	if err != nil {
		return false, err
	}
	if rt.session.Mode != "async" && pending <= 1 {
		return false, nil
	}

	profile, err := e.resolveModelProfile(ctx, rt.session, rt.agent, "listening_eval", 0, false)
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
		RunID:           cloneUUIDPointer(rt.runID),
		RunStepID:       cloneUUIDPointer(rt.runStepID),
		RunAttemptID:    cloneUUIDPointer(rt.runAttemptID),
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

	binding, err := e.resolveToolExecutionBinding(ctx, rt.session)
	if err != nil {
		return false, err
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
	blockedCalls := make([]ToolResult, 0)
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
		if binding.projectID != nil {
			if _, exists := arguments["project_id"]; !exists {
				arguments["project_id"] = binding.projectID.String()
			}
		}
		if binding.taskID != nil {
			if _, exists := arguments["task_id"]; !exists {
				arguments["task_id"] = binding.taskID.String()
			}
		}
		if strings.EqualFold(name, "project.create") && rt.projectIdentity != nil {
			blockedCalls = append(blockedCalls, ToolResult{
				ToolCallID: id,
				Name:       name,
				Error:      buildProjectCreateConflictGuardError(rt.projectIdentity),
			})
			continue
		}
		toolCall := ToolCall{
			ID:              id,
			Name:            name,
			Tier:            tier,
			Arguments:       arguments,
			MCPConnectionID: call.MCPConnectionID,
		}
		toolCalls = append(toolCalls, toolCall)
	}

	maxDuration := e.syncMaxDuration
	if strings.EqualFold(rt.session.Mode, "async") {
		maxDuration = e.asyncMaxDuration
	}

	toolBudget := e.maxToolCalls
	if toolBudget < 1 {
		toolBudget = 1
	}

	if len(blockedCalls) > 0 {
		if err := e.appendToolResults(ctx, rt, blockedCalls); err != nil {
			return false, err
		}
		rt.toolCallsUsed += len(blockedCalls)
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
		blocked, err := e.handleToolValidationResults(ctx, rt, runCalls, results)
		if err != nil {
			return false, err
		}
		rt.toolCallsUsed += len(runCalls)
		if blocked {
			return true, nil
		}
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
		handled, stop, err := e.handleRecoveryCLIExecuteWithoutCommand(ctx, rt, call)
		if err != nil {
			return false, err
		}
		if handled {
			if stop {
				return true, nil
			}
			return false, nil
		}
		handled, stop, err = e.handleRecoveryRejectedFileWriteContent(ctx, rt, &call)
		if err != nil {
			return false, err
		}
		if handled {
			if stop {
				return true, nil
			}
			return false, nil
		}
		handled, stop, err = e.handleRecoveryFileWriteWithoutContent(ctx, rt, &call)
		if err != nil {
			return false, err
		}
		if handled {
			if stop {
				return true, nil
			}
			return false, nil
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
		recoveryBlocked, recoveryErr := e.handleRecoveryPopulatedFileWriteOutcome(ctx, rt, call, result)
		if recoveryErr != nil {
			return false, recoveryErr
		}
		if recoveryBlocked {
			rt.toolCallsUsed++
			return true, nil
		}
		blocked, err := e.handleToolValidationResults(ctx, rt, []ToolCall{call}, []ToolResult{result})
		if err != nil {
			return false, err
		}
		rt.toolCallsUsed++
		if blocked {
			return true, nil
		}
		if rt.toolCallsUsed >= toolBudget {
			rt.stopReason = stopReasonMaxToolCalls
			_, _ = e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, "[Max tool calls reached. Turn ended.]")
			return true, nil
		}
	}

	return false, nil
}

func (e *TurnEngine) handleRecoveryCLIExecuteWithoutCommand(ctx context.Context, rt *turnRuntime, call ToolCall) (bool, bool, error) {
	if rt == nil || !rt.recoveryTurn || rt.turn == nil || rt.session == nil {
		return false, false, nil
	}
	if !isRecoveryCLIExecuteWithoutCommand(call) {
		return false, false, nil
	}
	if rt.recoveryCLIFixes >= recoveryCLIRepairBudget {
		if halted, err := e.haltRecoveryCLIExecuteWithoutCommand(ctx, rt); halted {
			return true, true, err
		}
		rt.stopReason = stopReasonRecoveryCLIRejected
		rt.recoveryBlockReason = buildRecoveryCLIExecuteBlockedTaskReason()
		if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildRecoveryCLIExecuteRejectedMessage()); err != nil {
			return true, true, err
		}
		return true, true, nil
	}
	rt.recoveryCLIFixes++
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildRecoveryCLIExecuteRetryMessage()); err != nil {
		return true, false, err
	}
	return true, false, nil
}

func isRecoveryCLIExecuteWithoutCommand(call ToolCall) bool {
	if !strings.EqualFold(strings.TrimSpace(call.Name), "cli.execute") {
		return false
	}
	if hasRawToolArguments(call) {
		return false
	}
	return stringValue(call.Arguments["command"]) == ""
}

func buildRecoveryCLIExecuteRetryMessage() string {
	return "[Recovery correction: cli.execute was emitted without `command`. Retry only with a non-empty `cli.execute.command` string. For file output, use one full shell command such as:\ncat > docs/target.md <<'EOF'\n...full file contents...\nEOF\nReplace `docs/target.md` with the exact workspace-relative task path. If you cannot provide the full command yet, draft the content first or use `file.write` with populated `path` and `content`.]"
}

func buildRecoveryCLIExecuteRejectedMessage() string {
	return "[Recovery turn halted: cli.execute was retried without `command` after one correction. The task is now blocked. Resume only after providing a full `cli.execute.command` string or a populated `file.write` call.]"
}

func (e *TurnEngine) haltRecoveryCLIExecuteWithoutCommand(ctx context.Context, rt *turnRuntime) (bool, error) {
	targetPath, historyDraft, ok := e.recoveryFileOutputContext(ctx, rt)
	if !ok {
		return false, nil
	}

	artifactDraft := strings.TrimSpace(historyDraft)
	failureReason := buildRecoveryCLIExecuteFileOutputFailureReason(targetPath)
	if currentDraft, draftOK := e.latestRecoveryAssistantDraftContent(ctx, rt); draftOK {
		switch rejectReason := recoveryFileWriteDraftRejectReason(currentDraft, targetPath); {
		case strings.TrimSpace(rejectReason) != "":
			failureReason = buildRecoveryCLIExecuteRejectedDraftFailureReason(targetPath, rejectReason)
			if artifactDraft == "" {
				artifactDraft = strings.TrimSpace(currentDraft)
			}
		case looksLikeRecoveryFileDraft(currentDraft):
			artifactDraft = strings.TrimSpace(currentDraft)
		case artifactDraft == "":
			artifactDraft = strings.TrimSpace(currentDraft)
		}
	}

	rt.stopReason = stopReasonRecoveryFileRejected
	artifactPath, artifactErr := e.persistRecoveryFileWriteArtifact(ctx, rt, targetPath, artifactDraft, failureReason)
	if artifactErr != nil {
		e.logger.Warn("recovery: failed to persist cli.execute file-output artifact",
			"session_id", rt.session.ID,
			"turn_id", rt.turn.ID,
			"path", targetPath,
			"error", artifactErr,
		)
	}
	message, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildRecoveryCLIExecuteFileOutputRejectedMessage(targetPath, artifactPath, failureReason))
	if err != nil {
		return true, err
	}
	if checkpointErr := e.persistRecoveryFileWriteCheckpoint(ctx, rt, targetPath, artifactPath, failureReason, message.ID); checkpointErr != nil {
		return true, checkpointErr
	}
	rt.recoveryBlockReason = buildRecoveryCLIExecuteFileOutputBlockedTaskReason(targetPath, artifactPath, failureReason)
	return true, nil
}

func buildRecoveryCLIExecuteFileOutputFailureReason(targetPath string) string {
	path := strings.TrimSpace(targetPath)
	if path == "" {
		path = "the requested workspace file"
	}
	return fmt.Sprintf("cli.execute for %s was retried without `command` after one bounded correction; persist the full file body before retrying the final workspace mutation", path)
}

func buildRecoveryCLIExecuteRejectedDraftFailureReason(targetPath, rejectReason string) string {
	path := strings.TrimSpace(targetPath)
	if path == "" {
		path = "the requested workspace file"
	}
	return fmt.Sprintf("cli.execute for %s was retried without `command` after one bounded correction; %s", path, strings.TrimSpace(rejectReason))
}

func buildRecoveryCLIExecuteFileOutputRejectedMessage(targetPath, artifactPath, failureReason string) string {
	path := strings.TrimSpace(targetPath)
	if path == "" {
		path = "the requested workspace file"
	}
	reason := strings.TrimSpace(failureReason)
	if strings.TrimSpace(artifactPath) == "" {
		return fmt.Sprintf("[Recovery turn halted: cli.execute for `%s` was retried without `command` after one correction. The task is now blocked. Last recovery failure: %s. Produce the full file body and a concrete `cli.execute.command` string or populated `file.write` call before retrying.]", path, reason)
	}
	return fmt.Sprintf("[Recovery turn halted: cli.execute for `%s` was retried without `command` after one correction. The task is now blocked. Last recovery failure: %s. Resume from `%s` and only retry file mutation after the full file body and a concrete `cli.execute.command` string or populated `file.write` call exist.]", path, reason, strings.TrimSpace(artifactPath))
}

func (e *TurnEngine) handleRecoveryRejectedFileWriteContent(ctx context.Context, rt *turnRuntime, call *ToolCall) (bool, bool, error) {
	if rt == nil || call == nil || !rt.recoveryTurn || rt.turn == nil || rt.session == nil {
		return false, false, nil
	}

	normalized, targetPath, draft, ok := recoveryFileWriteWithContent(*call)
	if !ok {
		return false, false, nil
	}
	call.Arguments = normalized

	rejectReason := recoveryFileWriteDraftRejectReason(draft, targetPath)
	if strings.TrimSpace(rejectReason) == "" {
		return false, false, nil
	}
	return e.haltRejectedRecoveryFileWrite(ctx, rt, targetPath, draft, rejectReason)
}

func (e *TurnEngine) handleRecoveryFileWriteWithoutContent(ctx context.Context, rt *turnRuntime, call *ToolCall) (bool, bool, error) {
	if rt == nil || call == nil || !rt.recoveryTurn || rt.turn == nil || rt.session == nil {
		return false, false, nil
	}

	normalized, targetPath, ok := recoveryFileWriteMissingContent(*call)
	if !ok {
		return false, false, nil
	}

	if draft, rejectReason, ok := e.recoveryFileWriteDraftContent(ctx, rt, targetPath); ok {
		normalized["content"] = draft
		if _, exists := normalized["create_dirs"]; !exists {
			normalized["create_dirs"] = true
		}
		call.Arguments = normalized
		if rt.recoveryFileWrites == nil {
			rt.recoveryFileWrites = make(map[string]recoveryPopulatedFileWriteState)
		}
		rt.recoveryFileWrites[strings.TrimSpace(call.ID)] = recoveryPopulatedFileWriteState{
			TargetPath: strings.TrimSpace(targetPath),
			Draft:      draft,
		}
		e.logger.Info("recovery: populated file.write from assistant draft",
			"session_id", rt.session.ID,
			"turn_id", rt.turn.ID,
			"path", targetPath,
		)
		return false, false, nil
	} else if strings.TrimSpace(rejectReason) != "" {
		return e.haltRejectedRecoveryFileWrite(ctx, rt, targetPath, draft, rejectReason)
	}

	if rt.recoveryFileFixes >= recoveryFileWriteRepairBudget {
		rt.stopReason = stopReasonRecoveryFileRejected
		artifactPath, artifactErr := e.persistRecoveryFileWriteArtifact(ctx, rt, targetPath, "", "")
		if artifactErr != nil {
			e.logger.Warn("recovery: failed to persist file.write artifact",
				"session_id", rt.session.ID,
				"turn_id", rt.turn.ID,
				"path", targetPath,
				"error", artifactErr,
			)
		}
		message, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildRecoveryFileWriteRejectedMessage(targetPath, artifactPath, ""))
		if err != nil {
			return true, true, err
		}
		if checkpointErr := e.persistRecoveryFileWriteCheckpoint(ctx, rt, targetPath, artifactPath, "", message.ID); checkpointErr != nil {
			return true, true, checkpointErr
		}
		rt.recoveryBlockReason = buildRecoveryFileWriteBlockedTaskReason(targetPath, artifactPath, "")
		return true, true, nil
	}

	rt.recoveryFileFixes++
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildRecoveryFileWriteRetryMessage(targetPath)); err != nil {
		return true, false, err
	}
	return true, false, nil
}

func (e *TurnEngine) haltRejectedRecoveryFileWrite(ctx context.Context, rt *turnRuntime, targetPath, draft, failureReason string) (bool, bool, error) {
	rt.stopReason = stopReasonRecoveryFileRejected
	artifactPath, artifactErr := e.persistRecoveryFileWriteArtifact(ctx, rt, targetPath, draft, failureReason)
	if artifactErr != nil {
		e.logger.Warn("recovery: failed to persist rejected file.write artifact",
			"session_id", rt.session.ID,
			"turn_id", rt.turn.ID,
			"path", targetPath,
			"error", artifactErr,
		)
	}
	message, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildRecoveryFileWriteRejectedMessage(targetPath, artifactPath, failureReason))
	if err != nil {
		return true, true, err
	}
	if checkpointErr := e.persistRecoveryFileWriteCheckpoint(ctx, rt, targetPath, artifactPath, failureReason, message.ID); checkpointErr != nil {
		return true, true, checkpointErr
	}
	rt.recoveryBlockReason = buildRecoveryFileWriteBlockedTaskReason(targetPath, artifactPath, failureReason)
	return true, true, nil
}

func recoveryFileWriteMissingContent(call ToolCall) (map[string]any, string, bool) {
	if !strings.EqualFold(strings.TrimSpace(call.Name), "file.write") {
		return nil, "", false
	}
	normalized := toolargs.Normalize("file.write", call.Arguments)
	targetPath := strings.TrimSpace(stringValue(normalized["path"]))
	if targetPath == "" {
		return nil, "", false
	}
	if strings.TrimSpace(stringValue(normalized["content"])) != "" {
		return nil, "", false
	}
	return normalized, targetPath, true
}

func recoveryFileWriteWithContent(call ToolCall) (map[string]any, string, string, bool) {
	if !strings.EqualFold(strings.TrimSpace(call.Name), "file.write") {
		return nil, "", "", false
	}
	normalized := toolargs.Normalize("file.write", call.Arguments)
	targetPath := strings.TrimSpace(stringValue(normalized["path"]))
	if targetPath == "" {
		return nil, "", "", false
	}
	content := stringValue(normalized["content"])
	if strings.TrimSpace(content) == "" {
		return nil, "", "", false
	}
	return normalized, targetPath, content, true
}

func buildRecoveryFileWriteRetryMessage(targetPath string) string {
	path := strings.TrimSpace(targetPath)
	if path == "" {
		path = "the requested workspace file"
	}
	return fmt.Sprintf("[Recovery correction: file.write for `%s` was emitted without `content`. Before retrying file mutation tools, draft the full file body in the assistant response or resend `file.write` with both `path` and `content` populated. If you already have the draft text, carry that exact text into the next write instead of emitting another empty-content call.]", path)
}

func buildRecoveryFileWriteRejectedMessage(targetPath, artifactPath, failureReason string) string {
	path := strings.TrimSpace(targetPath)
	if path == "" {
		path = "the requested workspace file"
	}
	if reason := strings.TrimSpace(failureReason); reason != "" {
		if isRecoveryDraftRejectFailureReason(reason) {
			if strings.TrimSpace(artifactPath) == "" {
				return fmt.Sprintf("[Recovery turn halted: recovered file.write for `%s` rejected a non-substantive draft. The task is now blocked. Last failure: %s. Produce the concrete file body before retrying.]", path, reason)
			}
			return fmt.Sprintf("[Recovery turn halted: recovered file.write for `%s` rejected a non-substantive draft. The task is now blocked. Last failure: %s. Resume from `%s` and only retry the final write after the full file body exists.]", path, reason, strings.TrimSpace(artifactPath))
		}
		if strings.TrimSpace(artifactPath) == "" {
			return fmt.Sprintf("[Recovery turn halted: recovered file.write for `%s` did not produce a durable file. The task is now blocked. Last failure: %s. Resolve that write failure before retrying.]", path, reason)
		}
		return fmt.Sprintf("[Recovery turn halted: recovered file.write for `%s` did not produce a durable file. The task is now blocked. Last failure: %s. Resume from `%s` and only retry the final write after resolving that failure.]", path, reason, strings.TrimSpace(artifactPath))
	}
	if strings.TrimSpace(artifactPath) == "" {
		return fmt.Sprintf("[Recovery turn halted: file.write for `%s` was retried without `content` after one correction. The task is now blocked. Resume only after producing the full file body.]", path)
	}
	return fmt.Sprintf("[Recovery turn halted: file.write for `%s` was retried without `content` after one correction. The task is now blocked. Resume from `%s` and only retry the final write after the file body exists.]", path, strings.TrimSpace(artifactPath))
}

func buildRecoveryCLIExecuteBlockedTaskReason() string {
	return "recovery halted after cli.execute was retried without command; re-queue only after providing a concrete cli.execute.command string or a populated file.write call"
}

func buildRecoveryCLIExecuteFileOutputBlockedTaskReason(targetPath, artifactPath, failureReason string) string {
	path := strings.TrimSpace(targetPath)
	if path == "" {
		path = "the requested workspace file"
	}
	reason := strings.TrimSpace(failureReason)
	if artifact := strings.TrimSpace(artifactPath); artifact != "" {
		return fmt.Sprintf("recovery halted after cli.execute for %s was retried without command: %s; resume from %s and re-queue only after the full file body and a concrete cli.execute.command string or populated file.write call exist", path, reason, artifact)
	}
	return fmt.Sprintf("recovery halted after cli.execute for %s was retried without command: %s; re-queue only after the full file body and a concrete cli.execute.command string or populated file.write call exist", path, reason)
}

func buildRecoveryFileWriteBlockedTaskReason(targetPath, artifactPath, failureReason string) string {
	path := strings.TrimSpace(targetPath)
	if path == "" {
		path = "the requested workspace file"
	}
	if reason := strings.TrimSpace(failureReason); reason != "" {
		if isRecoveryDraftRejectFailureReason(reason) {
			if artifact := strings.TrimSpace(artifactPath); artifact != "" {
				return fmt.Sprintf("recovery halted after %s; resume from %s and re-queue only after concrete content exists", reason, artifact)
			}
			return fmt.Sprintf("recovery halted after %s; re-queue only after concrete content exists", reason)
		}
		if artifact := strings.TrimSpace(artifactPath); artifact != "" {
			return fmt.Sprintf("recovery halted after recovered file.write for %s failed: %s; resume from %s and re-queue only after resolving that failure", path, reason, artifact)
		}
		return fmt.Sprintf("recovery halted after recovered file.write for %s failed: %s; resolve that write failure before re-queueing", path, reason)
	}
	if artifact := strings.TrimSpace(artifactPath); artifact != "" {
		return fmt.Sprintf("recovery halted after file.write for %s was retried without content; resume from %s and re-queue only after the file body exists", path, artifact)
	}
	return fmt.Sprintf("recovery halted after file.write for %s was retried without content; re-queue only after producing the full file body", path)
}

func (e *TurnEngine) currentRecoveryFileWriteCheckpoint(ctx context.Context, rt *turnRuntime) (*taskcheckpoint.RecoveryFileWriteCheckpoint, bool) {
	if e == nil || e.tasks == nil || rt == nil || rt.session == nil {
		return nil, false
	}
	taskID := resolveTaskID(rt.session)
	if taskID == nil || *taskID == uuid.Nil {
		return nil, false
	}
	taskRecord, err := e.tasks.GetByID(ctx, *taskID)
	if err != nil {
		return nil, false
	}
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata)
	if !ok {
		return nil, false
	}
	return &checkpoint, true
}

type recoveryResumeState struct {
	targetPath    string
	targetDraft   string
	artifactPath  string
	artifactDraft string
	failureReason string
}

func (e *TurnEngine) appendRecoveryResumeState(ctx context.Context, rt *turnRuntime, preserveInitialMessage bool) (bool, error) {
	if rt == nil || !rt.recoveryTurn || rt.turn == nil || rt.session == nil {
		return false, nil
	}
	state, ok := e.loadRecoveryResumeState(ctx, rt)
	if !ok {
		return false, nil
	}
	message, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildRecoveryResumeStateMessage(state))
	if err != nil {
		return false, err
	}
	if preserveInitialMessage && rt.initialMessageID != uuid.Nil {
		initial := rt.initialMessageID
		rt.historyStartID = &initial
		return true, nil
	}
	rt.historyStartID = &message.ID
	return true, nil
}

func (e *TurnEngine) loadRecoveryResumeState(ctx context.Context, rt *turnRuntime) (recoveryResumeState, bool) {
	checkpoint, ok := e.currentRecoveryFileWriteCheckpoint(ctx, rt)
	if !ok {
		return recoveryResumeState{}, false
	}

	state := recoveryResumeState{
		targetPath:    strings.TrimSpace(checkpoint.TargetPath),
		artifactPath:  strings.TrimSpace(checkpoint.ArtifactPath),
		failureReason: strings.TrimSpace(checkpoint.FailureReason),
	}
	if draft, found := e.readRecoveryWorkspaceText(ctx, rt, state.targetPath); found {
		state.targetDraft = strings.TrimSpace(draft)
	}
	if artifactBody, found := e.readRecoveryWorkspaceText(ctx, rt, state.artifactPath); found {
		state.artifactDraft = strings.TrimSpace(recoveryArtifactDraftContent(artifactBody))
	}
	if strings.TrimSpace(state.targetPath) == "" &&
		strings.TrimSpace(state.targetDraft) == "" &&
		strings.TrimSpace(state.artifactPath) == "" &&
		strings.TrimSpace(state.artifactDraft) == "" &&
		strings.TrimSpace(state.failureReason) == "" {
		return recoveryResumeState{}, false
	}
	return state, true
}

func (e *TurnEngine) readRecoveryWorkspaceText(ctx context.Context, rt *turnRuntime, relPath string) (string, bool) {
	trimmed := strings.TrimSpace(relPath)
	if trimmed == "" {
		return "", false
	}
	roots, err := e.recoveryWorkspaceRoots(ctx, rt)
	if err != nil || len(roots) == 0 {
		return "", false
	}

	var firstExisting string
	for _, root := range roots {
		absPath, _, resolveErr := resolveRecoveryWorkspacePath(root, trimmed)
		if resolveErr != nil {
			continue
		}
		body, readErr := os.ReadFile(absPath)
		if readErr != nil {
			continue
		}
		text := string(body)
		if strings.TrimSpace(text) != "" {
			return text, true
		}
		if firstExisting == "" {
			firstExisting = text
		}
	}
	if firstExisting != "" {
		return firstExisting, true
	}
	return "", false
}

func (e *TurnEngine) recoveryWorkspaceRoots(ctx context.Context, rt *turnRuntime) ([]string, error) {
	if e == nil || e.tasks == nil || e.projects == nil || rt == nil || rt.session == nil {
		return nil, fmt.Errorf("recovery workspace roots require task scope repositories")
	}
	taskID := resolveTaskID(rt.session)
	if taskID == nil || *taskID == uuid.Nil {
		return nil, fmt.Errorf("recovery workspace roots require task scope")
	}
	taskRecord, err := e.tasks.GetByID(ctx, *taskID)
	if err != nil {
		return nil, err
	}
	projectRecord, err := e.projects.GetByID(ctx, taskRecord.ProjectID)
	if err != nil {
		return nil, err
	}
	return e.projectWorkspaceRoots(ctx, taskRecord.OrganizationID, projectRecord)
}

func buildRecoveryResumeStateMessage(state recoveryResumeState) string {
	lines := []string{
		"[Recovery resume state]",
		"Resume order: target file draft, then recovery artifact draft, then checkpoint metadata/failure reason.",
		"Continue from the durable drafts below instead of asking which task to resume.",
	}

	if target := strings.TrimSpace(state.targetPath); target != "" {
		lines = append(lines, "Target file: "+target)
		if draft := strings.TrimSpace(state.targetDraft); draft != "" {
			excerpt, truncated := truncateRecoveryResumeExcerpt(draft, recoveryResumeExcerptChars)
			lines = append(lines,
				"Existing target file draft:",
				"```text",
				excerpt,
				"```",
			)
			if truncated {
				lines = append(lines, "_Target file excerpt truncated for prompt budget._")
			}
		} else {
			lines = append(lines, "Existing target file draft: (not found on disk)")
		}
	}

	if artifact := strings.TrimSpace(state.artifactPath); artifact != "" {
		lines = append(lines, "Recovery artifact: "+artifact)
		if draft := strings.TrimSpace(state.artifactDraft); draft != "" {
			excerpt, truncated := truncateRecoveryResumeExcerpt(draft, recoveryResumeExcerptChars)
			lines = append(lines,
				"Recovery artifact draft:",
				"```text",
				excerpt,
				"```",
			)
			if truncated {
				lines = append(lines, "_Recovery artifact excerpt truncated for prompt budget._")
			}
		} else {
			lines = append(lines, "Recovery artifact draft: (not found on disk)")
		}
	}

	if reason := strings.TrimSpace(state.failureReason); reason != "" {
		lines = append(lines, "Checkpoint failure reason: "+reason)
	}
	if strings.TrimSpace(state.targetDraft) != "" && strings.TrimSpace(state.artifactDraft) != "" {
		lines = append(lines, "If the target file is only a stub but the recovery artifact is fuller, merge the fuller artifact content into the target before retrying the final write.")
	}
	return strings.Join(lines, "\n")
}

func recoveryArtifactDraftContent(document string) string {
	trimmed := strings.TrimSpace(document)
	if trimmed == "" {
		return ""
	}
	parts := strings.SplitN(trimmed, "\n## Draft Content\n", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[1])
	}
	return trimmed
}

func truncateRecoveryResumeExcerpt(text string, limit int) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || limit <= 0 || len(trimmed) <= limit {
		return trimmed, false
	}
	cut := strings.TrimSpace(trimmed[:limit])
	if idx := strings.LastIndex(cut, "\n"); idx >= limit/2 {
		cut = strings.TrimSpace(cut[:idx])
	}
	if cut == "" {
		cut = strings.TrimSpace(trimmed[:limit])
	}
	return cut + "\n[truncated]", true
}

func (e *TurnEngine) recoveryFileOutputContext(ctx context.Context, rt *turnRuntime) (string, string, bool) {
	if checkpoint, ok := e.currentRecoveryFileWriteCheckpoint(ctx, rt); ok {
		if targetPath := strings.TrimSpace(checkpoint.TargetPath); targetPath != "" {
			return targetPath, "", true
		}
	}
	if e == nil || e.messages == nil || rt == nil || rt.session == nil || rt.turn == nil {
		return "", "", false
	}

	messages, err := e.messages.ListBySession(ctx, rt.session.ID)
	if err != nil {
		return "", "", false
	}
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.TurnID != nil && *message.TurnID == rt.turn.ID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(message.Role), "tool_result") {
			continue
		}
		toolName, output, errText, ok := parseToolResultMessage(message.Content)
		if !ok || strings.TrimSpace(errText) != "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(toolName)) {
		case "file.read", "file.write", "cli.execute":
		default:
			continue
		}
		targetPath, usedArtifactPath := recoveryTargetPathFromToolOutput(output)
		if targetPath == "" {
			continue
		}
		draft := ""
		if strings.EqualFold(strings.TrimSpace(toolName), "file.read") && !usedArtifactPath {
			draft = strings.TrimSpace(anyString(output["content"]))
		}
		return targetPath, draft, true
	}
	return "", "", false
}

func recoveryTargetPathFromToolOutput(output map[string]any) (string, bool) {
	if output == nil {
		return "", false
	}
	rawPath := strings.TrimSpace(anyString(output["path"]))
	if rawPath == "" {
		return "", false
	}
	normalized := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rawPath)))
	prefix := recoveryArtifactDir + "/"
	if strings.HasPrefix(normalized, prefix) && len(normalized) > len(prefix) {
		return strings.TrimPrefix(normalized, prefix), true
	}
	return normalized, false
}

func (e *TurnEngine) hasQueuedAgentTurnForSession(ctx context.Context, sessionID uuid.UUID, excludeJobID *uuid.UUID) (bool, error) {
	if e == nil || e.pool == nil || sessionID == uuid.Nil {
		return false, nil
	}
	var queued bool
	if excludeJobID != nil && *excludeJobID != uuid.Nil {
		if err := e.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM job_queue
				WHERE job_type = $1
				  AND status IN ('pending', 'claimed')
				  AND payload->>'session_id' = $2
				  AND id <> $3
			)
		`, AgentTurnJobType, sessionID.String(), *excludeJobID).Scan(&queued); err != nil {
			return false, err
		}
		return queued, nil
	}
	if err := e.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM job_queue
			WHERE job_type = $1
			  AND status IN ('pending', 'claimed')
			  AND payload->>'session_id' = $2
		)
	`, AgentTurnJobType, sessionID.String()).Scan(&queued); err != nil {
		return false, err
	}
	return queued, nil
}

func (e *TurnEngine) handleTaskScopedProviderAuthFailure(ctx context.Context, rt *turnRuntime, cause error) (bool, error) {
	if rt == nil || rt.turn == nil || rt.session == nil || !errors.Is(cause, ErrAuthFailed) {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project_task") || rt.session.ScopeID == uuid.Nil {
		return false, nil
	}

	rt.stopReason = stopReasonRecoveryCLIRejected
	rt.recoveryBlockReason = buildProviderAuthBlockedTaskReason(rt.recoveryTurn)
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildProviderAuthRejectedMessage(rt.recoveryTurn)); err != nil {
		return true, err
	}
	if err := e.ensureRecoveryTurnDurableTaskState(ctx, rt); err != nil {
		return true, err
	}
	if err := e.completeTurn(ctx, rt); err != nil {
		return true, err
	}
	return true, nil
}

func buildProviderAuthRejectedMessage(recoveryTurn bool) string {
	prefix := "Task turn halted"
	if recoveryTurn {
		prefix = "Recovery turn halted"
	}
	return fmt.Sprintf("[%s: provider authentication failed and no healthy enabled fallback connection could continue the work. The task is now blocked. Fix or disable the failing provider credential, then resume the task.]", prefix)
}

func buildProviderAuthBlockedTaskReason(recoveryTurn bool) string {
	if recoveryTurn {
		return "recovery halted after provider authentication failed on every eligible model connection; fix or disable the failing credential before resuming the task"
	}
	return "task blocked after provider authentication failed on every eligible model connection; fix or disable the failing credential before re-queueing the task"
}

func (e *TurnEngine) recoveryFileWriteDraftContent(ctx context.Context, rt *turnRuntime, targetPath string) (string, string, bool) {
	if e == nil || e.messages == nil || rt == nil || rt.session == nil || rt.turn == nil {
		return "", "", false
	}
	draft, ok := e.latestRecoveryAssistantDraftContent(ctx, rt)
	if !ok {
		return "", "", false
	}
	if reason := recoveryFileWriteDraftRejectReason(draft, targetPath); reason != "" {
		return draft, reason, false
	}
	if !looksLikeRecoveryFileDraft(draft) {
		return "", "", false
	}
	return draft, "", true
}

func (e *TurnEngine) latestRecoveryAssistantDraftContent(ctx context.Context, rt *turnRuntime) (string, bool) {
	if e == nil || e.messages == nil || rt == nil || rt.session == nil || rt.turn == nil {
		return "", false
	}
	messages, err := e.messages.ListBySession(ctx, rt.session.ID)
	if err != nil {
		return "", false
	}
	draft := latestNonEmptyAssistantFinalForTurn(messages, rt.turn.ID)
	if draft == nil {
		return "", false
	}
	return draft.Content, true
}

func latestNonEmptyAssistantFinalForTurn(messages []repo.ChatMessage, turnID uuid.UUID) *repo.ChatMessage {
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
		if strings.TrimSpace(message.Content) == "" {
			continue
		}
		if latest == nil || message.SequenceNumber > latest.SequenceNumber {
			copyMessage := message
			latest = &copyMessage
		}
	}
	return latest
}

func looksLikeRecoveryFileDraft(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{
		"i'll ",
		"i will ",
		"i am going to ",
		"attempt ",
		"trying ",
		"using ",
		"let me ",
	} {
		if strings.HasPrefix(lower, prefix) && len(trimmed) < 240 {
			return false
		}
	}
	if len(trimmed) >= 180 {
		return true
	}
	return hasStructuredRecoveryFileDraftMarkers(trimmed)
}

func recoveryFileWriteDraftRejectReason(content, targetPath string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	path := strings.TrimSpace(targetPath)
	if path == "" {
		path = "the requested workspace file"
	}
	if containsAny(lower,
		"i understand the problem",
		"i can see the problem",
		"let me ",
		"i need to ",
		"my `file_write`",
		"my `file.write`",
		"my file_write",
		"my file.write",
	) && containsAny(lower,
		"file_write",
		"file.write",
		"cli_execute",
		"cli.execute",
		"conversation history",
		"content parameter",
		"path parameter",
		"tool call",
		"tool calls",
		"invocation",
		"invocations",
		"fallback",
	) {
		return fmt.Sprintf("assistant draft for %s described tool-recovery troubleshooting instead of the file body", path)
	}
	if looksLikeRecoveryIntentNarrationPlaceholder(trimmed) {
		return fmt.Sprintf("assistant draft for %s described intent to write the deliverable instead of the file body", path)
	}
	return ""
}

func hasStructuredRecoveryFileDraftMarkers(trimmed string) bool {
	if strings.Count(trimmed, "\n") >= 3 {
		return true
	}
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "---") || strings.HasPrefix(trimmed, "<") || strings.HasPrefix(trimmed, "{") {
		return len(trimmed) >= 60
	}
	if strings.Contains(trimmed, "\n- ") || strings.Contains(trimmed, "\n## ") || strings.Contains(trimmed, "\n1. ") {
		return len(trimmed) >= 60
	}
	return false
}

func looksLikeRecoveryIntentNarrationPlaceholder(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	if hasStructuredRecoveryFileDraftMarkers(trimmed) || strings.Count(trimmed, "\n") >= 3 || len(trimmed) > 600 {
		return false
	}

	lower := strings.ToLower(trimmed)
	if containsAny(lower, "the single deliverable", "final deliverable") {
		return true
	}
	hasWriteIntent := containsAny(lower,
		"let me write",
		"let me draft",
		"i'm going to write",
		"i am going to write",
		"write the full",
		"write the comprehensive",
		"draft the full",
		"draft the comprehensive",
	)
	hasSetupCue := containsAny(lower,
		"now i have everything i need",
		"i now have everything i need",
		"i have everything i need",
		"i have enough context",
		"i have what i need",
		"now that i have",
	)
	if hasWriteIntent && hasSetupCue {
		return true
	}
	if containsAny(lower, "this needs to be") && containsAny(lower,
		"deliverable",
		"unblock",
		"task",
		"strategic foundation",
		"foundation for",
	) {
		return true
	}
	return false
}

func isRecoveryDraftRejectFailureReason(reason string) bool {
	lower := strings.ToLower(strings.TrimSpace(reason))
	return strings.Contains(lower, "assistant draft for ") && strings.Contains(lower, "instead of the file body")
}

func containsAny(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if needle == "" {
			continue
		}
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func (e *TurnEngine) persistRecoveryFileWriteArtifact(ctx context.Context, rt *turnRuntime, targetPath, draft, failureReason string) (string, error) {
	if e == nil || e.tasks == nil || e.projects == nil || rt == nil || rt.session == nil {
		return "", fmt.Errorf("recovery artifact persistence requires task and project repositories")
	}
	taskID := resolveTaskID(rt.session)
	if taskID == nil || *taskID == uuid.Nil {
		return "", fmt.Errorf("recovery artifact persistence requires task scope")
	}

	taskRecord, err := e.tasks.GetByID(ctx, *taskID)
	if err != nil {
		return "", err
	}
	projectRecord, err := e.projects.GetByID(ctx, taskRecord.ProjectID)
	if err != nil {
		return "", err
	}
	workspaceRoots, err := e.projectWorkspaceRoots(ctx, taskRecord.OrganizationID, projectRecord)
	if err != nil {
		return "", err
	}
	workspaceRoot := workspaceRoots[0]
	_, targetRel, err := resolveRecoveryWorkspacePath(workspaceRoot, targetPath)
	if err != nil {
		return "", err
	}

	artifactRel := filepath.ToSlash(filepath.Join(recoveryArtifactDir, filepath.FromSlash(targetRel)))
	if strings.TrimSpace(draft) == "" {
		draft, _, _ = e.recoveryFileWriteDraftContent(ctx, rt, targetPath)
	}
	document := buildRecoveryFileWriteArtifactDocument(buildTaskLabel(taskRecord), targetRel, draft, failureReason, e.now())
	for _, root := range workspaceRoots {
		artifactAbs := filepath.Join(root, filepath.FromSlash(artifactRel))
		if err := os.MkdirAll(filepath.Dir(artifactAbs), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(artifactAbs, []byte(document), 0o644); err != nil {
			return "", err
		}
		if _, err := os.Stat(artifactAbs); err != nil {
			return "", err
		}
	}
	return artifactRel, nil
}

func (e *TurnEngine) projectWorkspaceRoots(ctx context.Context, organizationID uuid.UUID, projectRecord repo.Project) ([]string, error) {
	projectRoot, err := workspace.ProjectRoot(e.dataDir, projectRecord.Slug)
	if err != nil {
		return nil, err
	}
	if e == nil || e.organizations == nil || organizationID == uuid.Nil {
		return []string{projectRoot}, nil
	}
	orgRecord, err := e.organizations.GetByID(ctx, organizationID)
	if err != nil {
		return []string{projectRoot}, nil
	}
	roots, err := workspace.ProjectCompatibilityRoots(e.dataDir, orgRecord.Slug, projectRecord.Slug)
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		return []string{projectRoot}, nil
	}
	return roots, nil
}

func (e *TurnEngine) persistRecoveryFileWriteCheckpoint(ctx context.Context, rt *turnRuntime, targetPath, artifactPath, failureReason string, messageID uuid.UUID) error {
	if e == nil || e.tasks == nil || rt == nil || rt.session == nil || rt.turn == nil {
		return nil
	}
	taskID := resolveTaskID(rt.session)
	if taskID == nil || *taskID == uuid.Nil {
		return nil
	}

	taskRecord, err := e.tasks.GetByID(ctx, *taskID)
	if err != nil {
		return err
	}
	checkpoint := taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:            strings.TrimSpace(targetPath),
		ArtifactPath:          strings.TrimSpace(artifactPath),
		FailureReason:         strings.TrimSpace(failureReason),
		HistoryStartMessageID: messageID.String(),
		HaltTurnID:            rt.turn.ID.String(),
		UpdatedAt:             e.now().UTC().Format(time.RFC3339Nano),
	}
	merged, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(taskRecord.Metadata, checkpoint)
	if err != nil {
		return err
	}
	taskRecord.Metadata = merged
	if _, err := e.tasks.Update(ctx, taskRecord); err != nil {
		return err
	}
	rt.historyStartID = &messageID
	return nil
}

func (e *TurnEngine) maybeClearRecoveryFileWriteCheckpoint(ctx context.Context, rt *turnRuntime, result ToolResult) (bool, error) {
	if e == nil || e.tasks == nil || rt == nil || rt.session == nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(result.Name), "file.write") || strings.TrimSpace(result.Error) != "" {
		return false, nil
	}
	writtenPath := strings.TrimSpace(stringValue(result.Output["path"]))
	if writtenPath == "" {
		return false, nil
	}

	taskID := resolveTaskID(rt.session)
	if taskID == nil || *taskID == uuid.Nil {
		return false, nil
	}
	taskRecord, err := e.tasks.GetByID(ctx, *taskID)
	if err != nil {
		return false, err
	}
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata)
	if !ok {
		return false, nil
	}
	if !sameWorkspaceRelativePath(checkpoint.TargetPath, writtenPath) {
		return false, nil
	}
	cleared, err := taskcheckpoint.ClearRecoveryFileWriteCheckpoint(taskRecord.Metadata)
	if err != nil {
		return false, err
	}
	taskRecord.Metadata = cleared
	if _, err := e.tasks.Update(ctx, taskRecord); err != nil {
		return false, err
	}
	return true, nil
}

func (e *TurnEngine) handleRecoveryPopulatedFileWriteOutcome(ctx context.Context, rt *turnRuntime, call ToolCall, result ToolResult) (bool, error) {
	if rt == nil || !rt.recoveryTurn || rt.turn == nil || rt.session == nil {
		return false, nil
	}
	if len(rt.recoveryFileWrites) == 0 {
		return false, nil
	}
	state, ok := rt.recoveryFileWrites[strings.TrimSpace(call.ID)]
	if !ok {
		return false, nil
	}
	delete(rt.recoveryFileWrites, strings.TrimSpace(call.ID))

	targetPath := strings.TrimSpace(state.TargetPath)
	if targetPath == "" {
		targetPath = strings.TrimSpace(stringValue(call.Arguments["path"]))
	}
	if targetPath == "" {
		targetPath = strings.TrimSpace(stringValue(result.Output["path"]))
	}

	durable, failureReason := e.recoveryPopulatedFileWriteDurableOutcome(ctx, rt, targetPath, result)
	if durable {
		return false, nil
	}

	rt.stopReason = stopReasonRecoveryFileRejected
	artifactPath, artifactErr := e.persistRecoveryFileWriteArtifact(ctx, rt, targetPath, state.Draft, failureReason)
	if artifactErr != nil {
		e.logger.Warn("recovery: failed to persist populated file.write artifact",
			"session_id", rt.session.ID,
			"turn_id", rt.turn.ID,
			"path", targetPath,
			"error", artifactErr,
		)
	}
	message, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildRecoveryFileWriteRejectedMessage(targetPath, artifactPath, failureReason))
	if err != nil {
		return true, err
	}
	if checkpointErr := e.persistRecoveryFileWriteCheckpoint(ctx, rt, targetPath, artifactPath, failureReason, message.ID); checkpointErr != nil {
		return true, checkpointErr
	}
	rt.recoveryBlockReason = buildRecoveryFileWriteBlockedTaskReason(targetPath, artifactPath, failureReason)
	return true, nil
}

func (e *TurnEngine) recoveryPopulatedFileWriteDurableOutcome(ctx context.Context, rt *turnRuntime, targetPath string, result ToolResult) (bool, string) {
	if failureReason := recoveryFileWriteFailureReason(result); failureReason != "" {
		return false, failureReason
	}
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		targetPath = strings.TrimSpace(stringValue(result.Output["path"]))
	}
	if targetPath == "" {
		return false, "recovered file.write returned without a target path to verify"
	}
	exists, err := e.recoveryFileWriteTargetExists(ctx, rt, targetPath)
	if err != nil {
		return false, fmt.Sprintf("could not verify %s on disk after recovered file.write: %v", targetPath, err)
	}
	if !exists {
		return false, fmt.Sprintf("recovered file.write reported success but %s was not found on disk", targetPath)
	}
	return true, ""
}

func recoveryFileWriteFailureReason(result ToolResult) string {
	if reason := strings.TrimSpace(stripToolFailurePrefix(result.Error, result.Name)); reason != "" {
		return reason
	}
	if code := strings.TrimSpace(toolResultErrorCode(result)); code != "" {
		if message := strings.TrimSpace(stringValue(result.Output["message"])); message != "" {
			return message
		}
		return code
	}
	return ""
}

func (e *TurnEngine) recoveryFileWriteTargetExists(ctx context.Context, rt *turnRuntime, targetPath string) (bool, error) {
	if e == nil || e.tasks == nil || e.projects == nil || rt == nil || rt.session == nil {
		return false, fmt.Errorf("task and project repositories are required")
	}
	taskID := resolveTaskID(rt.session)
	if taskID == nil || *taskID == uuid.Nil {
		return false, fmt.Errorf("task scope is required")
	}
	taskRecord, err := e.tasks.GetByID(ctx, *taskID)
	if err != nil {
		return false, err
	}
	projectRecord, err := e.projects.GetByID(ctx, taskRecord.ProjectID)
	if err != nil {
		return false, err
	}
	workspaceRoots, err := e.projectWorkspaceRoots(ctx, taskRecord.OrganizationID, projectRecord)
	if err != nil {
		return false, err
	}
	var resolutionErr error
	for _, root := range workspaceRoots {
		targetAbs, _, err := resolveRecoveryWorkspacePath(root, targetPath)
		if err != nil {
			if resolutionErr == nil {
				resolutionErr = err
			}
			continue
		}

		info, err := os.Stat(targetAbs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if resolutionErr == nil {
				resolutionErr = err
			}
			continue
		}
		if info.IsDir() {
			if resolutionErr == nil {
				resolutionErr = fmt.Errorf("%s resolved to a directory", targetPath)
			}
			continue
		}
		return true, nil
	}
	if resolutionErr != nil {
		return false, resolutionErr
	}
	return false, nil
}

func sameWorkspaceRelativePath(left, right string) bool {
	return filepath.Clean(filepath.FromSlash(strings.TrimSpace(left))) == filepath.Clean(filepath.FromSlash(strings.TrimSpace(right)))
}

func resolveRecoveryWorkspacePath(root, relOrAbsPath string) (string, string, error) {
	trimmedRoot := strings.TrimSpace(root)
	trimmedPath := strings.TrimSpace(relOrAbsPath)
	if trimmedRoot == "" {
		return "", "", fmt.Errorf("workspace root is required")
	}
	if trimmedPath == "" {
		return "", "", fmt.Errorf("target path is required")
	}

	rootAbs, err := filepath.Abs(trimmedRoot)
	if err != nil {
		return "", "", err
	}

	candidate := filepath.Clean(filepath.FromSlash(trimmedPath))
	targetAbs := candidate
	if !filepath.IsAbs(targetAbs) {
		targetAbs = filepath.Join(rootAbs, candidate)
	}
	targetAbs = filepath.Clean(targetAbs)

	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", "", err
	}
	if rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path traversal is not allowed")
	}
	return targetAbs, filepath.ToSlash(rel), nil
}

func buildRecoveryFileWriteArtifactDocument(taskLabel, targetPath, draft, failureReason string, now time.Time) string {
	reasonLine := "Reason: Recovery turn retried file.write without concrete content after one bounded correction."
	if reason := strings.TrimSpace(failureReason); reason != "" {
		reasonLine = "Reason: Recovery turn halted with a durable file-output checkpoint instead of retrying without a concrete final write."
		if strings.Contains(reason, "was not found on disk") || strings.Contains(reason, "could not verify") {
			reasonLine = "Reason: Recovery turn carried assistant draft content into file.write, but the write did not produce a durable target file."
		}
	}
	lines := []string{
		"# Recovery file.write artifact",
		"",
		"Task: " + strings.TrimSpace(taskLabel),
		"Target Path: " + strings.TrimSpace(targetPath),
		"Generated: " + now.UTC().Format(time.RFC3339Nano),
		reasonLine,
		"",
		"## Resume Instructions",
		fmt.Sprintf("- Produce the full file body for `%s` before retrying the final workspace file mutation.", strings.TrimSpace(targetPath)),
		"- Reuse any draft content captured below instead of starting from scratch.",
		"",
	}
	if reason := strings.TrimSpace(failureReason); reason != "" {
		lines = append(lines,
			"## Last Write Failure",
			"",
			reason,
			"",
		)
	}
	lines = append(lines,
		"## Draft Content",
		"",
	)
	if strings.TrimSpace(draft) == "" {
		lines = append(lines, "_No concrete draft content was available in the recovery turn. Replace this section with the intended file body before retrying._")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, strings.TrimRight(draft, "\n"))
	return strings.Join(lines, "\n")
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
		if identity, ok := parseProjectIdentityFromToolResult(result); ok {
			rt.projectIdentity = identity
			if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildProjectIdentityLockMessage(identity)); err != nil {
				return err
			}
		}
		if instruction, ok := e.buildProjectKickoffHandoffInstruction(ctx, rt, result); ok {
			if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, instruction); err != nil {
				return err
			}
		}
		if cleared, clearErr := e.maybeClearRecoveryFileWriteCheckpoint(ctx, rt, result); clearErr != nil {
			return clearErr
		} else if cleared {
			rt.historyStartID = nil
		}
	}
	return nil
}

func (e *TurnEngine) handleToolValidationResults(ctx context.Context, rt *turnRuntime, calls []ToolCall, results []ToolResult) (bool, error) {
	if rt == nil || rt.session == nil || rt.turn == nil || e.tasks == nil {
		return false, nil
	}
	taskID := resolveTaskID(rt.session)
	if taskID == nil || *taskID == uuid.Nil {
		return false, nil
	}

	taskRecord, err := e.tasks.GetByID(ctx, *taskID)
	if errors.Is(err, repo.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	failures := collectToolValidationFailures(calls, results)
	current, ok := parseTaskValidationGuard(taskRecord.Metadata)
	if len(failures) == 0 {
		if !ok || current.Blocked || current.Count == 0 || current.InitialMessageID != rt.initialMessageID.String() {
			return false, nil
		}
		if len(calls) > 0 && current.AttemptFingerprint != "" && !toolCallsContainAttemptFingerprint(calls, current.AttemptFingerprint) {
			return false, nil
		}
		cleared, clearErr := clearTaskValidationGuardMetadata(taskRecord.Metadata)
		if clearErr != nil {
			return false, clearErr
		}
		taskRecord.Metadata = cleared
		if _, err := e.tasks.Update(ctx, taskRecord); err != nil {
			return false, err
		}
		return false, nil
	}

	next := current
	blockedNow := false
	for _, failure := range failures {
		candidate, candidateBlocked := nextTaskValidationGuardState(next, rt.initialMessageID, rt.turn.ID, failure, e.now())
		next = candidate
		if candidateBlocked {
			blockedNow = true
			break
		}
	}

	merged, mergeErr := mergeTaskValidationGuardMetadata(taskRecord.Metadata, next)
	if mergeErr != nil {
		return false, mergeErr
	}
	taskRecord.Metadata = merged
	updatedTask, err := e.tasks.Update(ctx, taskRecord)
	if err != nil {
		return false, err
	}

	if !blockedNow {
		return false, nil
	}

	if !strings.EqualFold(strings.TrimSpace(updatedTask.WorkStatus), "blocked") {
		blockReason := buildValidationLoopBlockReason(next)
		if e.taskTransitions != nil {
			if _, err := e.taskTransitions.MarkBlocked(ctx, updatedTask.ID, blockReason, tasksvc.Actor{Type: "system"}); err != nil {
				return false, err
			}
		} else {
			updatedTask.WorkStatus = "blocked"
			if _, err := e.tasks.Update(ctx, updatedTask); err != nil {
				return false, err
			}
		}
	}

	metrics.RecordAgentTurnValidationLoopBlock(next.ToolName, next.FailureCode)
	rt.stopReason = stopReasonValidationBlocked
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildValidationLoopSystemMessage(next)); err != nil {
		return false, err
	}
	return true, nil
}

func collectToolValidationFailures(calls []ToolCall, results []ToolResult) []toolValidationFailure {
	if len(results) == 0 {
		return nil
	}
	callByID := make(map[string]ToolCall, len(calls))
	for _, call := range calls {
		callByID[strings.TrimSpace(call.ID)] = call
	}

	failures := make([]toolValidationFailure, 0, len(results))
	for _, result := range results {
		call, ok := callByID[strings.TrimSpace(result.ToolCallID)]
		if !ok {
			call = ToolCall{ID: result.ToolCallID, Name: result.Name}
		}
		failure, matched := classifyToolValidationFailure(call, result)
		if matched {
			failures = append(failures, failure)
		}
	}
	return failures
}

func toolCallsContainAttemptFingerprint(calls []ToolCall, attemptFingerprint string) bool {
	if strings.TrimSpace(attemptFingerprint) == "" {
		return true
	}
	for _, call := range calls {
		if toolargs.AttemptFingerprint(call.Name, call.Arguments) == attemptFingerprint {
			return true
		}
	}
	return false
}

func classifyToolValidationFailure(call ToolCall, result ToolResult) (toolValidationFailure, bool) {
	toolName := strings.TrimSpace(call.Name)
	if toolName == "" {
		toolName = strings.TrimSpace(result.Name)
	}
	if toolName == "" {
		return toolValidationFailure{}, false
	}
	attemptFingerprint := toolargs.AttemptFingerprint(toolName, call.Arguments)

	if hasRawToolArguments(call) {
		reason := "malformed _raw arguments"
		if code := strings.TrimSpace(toolResultErrorCode(result)); code != "" {
			reason = fmt.Sprintf("malformed _raw arguments (%s)", code)
		}
		return buildToolValidationFailure(toolName, "malformed_arguments_raw", reason, attemptFingerprint), true
	}

	if code := normalizeValidationFailureCode(toolResultErrorCode(result)); isToolValidationCode(code) {
		return buildToolValidationFailure(toolName, code, strings.TrimSpace(toolResultErrorCode(result)), attemptFingerprint), true
	}

	if reason := strings.TrimSpace(stripToolFailurePrefix(result.Error, toolName)); reason != "" {
		if code := normalizeValidationFailureCode(reason); isToolValidationCode(code) {
			return buildToolValidationFailure(toolName, code, reason, attemptFingerprint), true
		}
	}

	return toolValidationFailure{}, false
}

func buildToolValidationFailure(toolName, failureCode, failureReason, attemptFingerprint string) toolValidationFailure {
	code := normalizeValidationFailureCode(failureCode)
	if code == "" {
		code = "validation_failure"
	}
	reason := strings.TrimSpace(failureReason)
	if reason == "" {
		reason = code
	}
	toolName = strings.TrimSpace(toolName)
	return toolValidationFailure{
		ToolName:           toolName,
		FailureClass:       "tool_validation",
		FailureCode:        code,
		FailureReason:      reason,
		Fingerprint:        strings.ToLower(strings.TrimSpace(toolName)) + ":" + code,
		AttemptFingerprint: strings.TrimSpace(attemptFingerprint),
	}
}

func hasRawToolArguments(call ToolCall) bool {
	if call.Arguments == nil {
		return false
	}
	raw, ok := call.Arguments["_raw"]
	if !ok || raw == nil {
		return false
	}
	return strings.TrimSpace(fmt.Sprintf("%v", raw)) != ""
}

func toolResultErrorCode(result ToolResult) string {
	if strings.TrimSpace(result.Error) != "" {
		return strings.TrimSpace(result.Error)
	}
	if len(result.Output) == 0 {
		return ""
	}
	raw, ok := result.Output["error"]
	if !ok || raw == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", raw))
}

func stripToolFailurePrefix(message, toolName string) string {
	trimmed := strings.TrimSpace(message)
	prefix := strings.ToLower(strings.TrimSpace(toolName) + " failed:")
	if prefix == "failed:" {
		return trimmed
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, prefix) {
		return strings.TrimSpace(trimmed[len(prefix):])
	}
	return trimmed
}

func normalizeValidationFailureCode(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return ""
	}
	builder := strings.Builder{}
	builder.Grow(len(normalized))
	lastUnderscore := false
	for _, r := range normalized {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if lastUnderscore {
			continue
		}
		builder.WriteByte('_')
		lastUnderscore = true
	}
	return strings.Trim(builder.String(), "_")
}

func isToolValidationCode(code string) bool {
	if strings.TrimSpace(code) == "" {
		return false
	}
	switch code {
	case "not_found",
		"path_traversal",
		"not_a_git_repo",
		"cannot_commit_to_main",
		"service_unavailable",
		"cli_executor_unavailable",
		"memory_service_unavailable",
		"project_repository_unavailable",
		"task_repository_unavailable",
		"dependency_repository_unavailable",
		"subtask_repository_unavailable",
		"schedule_repository_unavailable",
		"assignment_repository_unavailable",
		"agent_repository_unavailable",
		"chat_repository_unavailable",
		"flow_repository_unavailable",
		"profile_catalog_unavailable",
		"inbox_repository_unavailable",
		"project_manager_requires_staff_agent",
		"pm_conflict",
		"cycle_detected",
		"cross_level_dependency",
		"self_dependency":
		return false
	}
	if strings.Contains(code, "unavailable") || strings.Contains(code, "timeout") || strings.Contains(code, "denied") || strings.Contains(code, "policy") {
		return false
	}
	return strings.HasSuffix(code, "_required") ||
		strings.HasPrefix(code, "invalid_") ||
		strings.Contains(code, "validation") ||
		strings.Contains(code, "schema") ||
		strings.Contains(code, "malformed") ||
		strings.Contains(code, "must_be") ||
		strings.Contains(code, "can_only_be") ||
		strings.Contains(code, "requires_")
}

func parseTaskValidationGuard(metadata json.RawMessage) (taskValidationGuardState, bool) {
	state, ok := tasksvc.ParseValidationGuard(metadata)
	if !ok {
		return taskValidationGuardState{}, false
	}
	if state.BlockThreshold <= 0 {
		state.BlockThreshold = validationLoopBlockThreshold
	}
	return state, true
}

func mergeTaskValidationGuardMetadata(metadata json.RawMessage, state taskValidationGuardState) (json.RawMessage, error) {
	return tasksvc.MergeValidationGuardMetadata(metadata, tasksvc.ValidationGuardState(state))
}

func clearTaskValidationGuardMetadata(metadata json.RawMessage) (json.RawMessage, error) {
	return tasksvc.ClearValidationGuardMetadata(metadata)
}

func nextTaskValidationGuardState(current taskValidationGuardState, initialMessageID, turnID uuid.UUID, failure toolValidationFailure, now time.Time) (taskValidationGuardState, bool) {
	nowValue := now.UTC().Format(time.RFC3339Nano)
	next := taskValidationGuardState{
		InitialMessageID:   initialMessageID.String(),
		Fingerprint:        failure.Fingerprint,
		AttemptFingerprint: failure.AttemptFingerprint,
		ToolName:           failure.ToolName,
		FailureClass:       failure.FailureClass,
		FailureCode:        failure.FailureCode,
		FailureReason:      failure.FailureReason,
		Count:              1,
		BlockThreshold:     validationLoopBlockThreshold,
		Blocked:            false,
		FirstSeenAt:        nowValue,
		LastSeenAt:         nowValue,
		LastTurnID:         turnID.String(),
	}
	if current.InitialMessageID == next.InitialMessageID && current.Fingerprint == next.Fingerprint && current.Count > 0 {
		next.Count = current.Count + 1
		next.FirstSeenAt = current.FirstSeenAt
		if next.FirstSeenAt == "" {
			next.FirstSeenAt = nowValue
		}
	}
	next.Blocked = next.Count >= validationLoopBlockThreshold
	return next, next.Blocked && !current.Blocked
}

func buildValidationLoopBlockReason(state taskValidationGuardState) string {
	reason := strings.TrimSpace(state.FailureReason)
	if reason == "" {
		reason = strings.TrimSpace(state.FailureCode)
	}
	toolName := strings.TrimSpace(state.ToolName)
	if toolName == "" {
		return fmt.Sprintf("deterministic tool validation loop blocked after %d identical failures: %s", state.Count, reason)
	}
	return fmt.Sprintf("deterministic tool validation loop blocked after %d identical failures: %s (%s)", state.Count, toolName, reason)
}

func buildValidationLoopSystemMessage(state taskValidationGuardState) string {
	reason := strings.TrimSpace(state.FailureReason)
	if reason == "" {
		reason = strings.TrimSpace(state.FailureCode)
	}
	toolName := strings.TrimSpace(state.ToolName)
	if toolName == "" {
		return fmt.Sprintf("[Deterministic tool validation loop blocked after %d identical failures: %s]", state.Count, reason)
	}
	return fmt.Sprintf("[Deterministic tool validation loop blocked after %d identical failures: %s (%s)]", state.Count, toolName, reason)
}

func (e *TurnEngine) loadProjectIdentityForMessage(ctx context.Context, sessionID, messageID uuid.UUID) *projectIdentity {
	if e == nil || e.turns == nil || e.messages == nil || sessionID == uuid.Nil || messageID == uuid.Nil {
		return nil
	}

	turns, err := e.turns.ListBySession(ctx, sessionID)
	if err != nil {
		return nil
	}
	turnIDs := make(map[uuid.UUID]struct{}, len(turns))
	for _, turn := range turns {
		if turn.TriggerMessageID == nil || *turn.TriggerMessageID != messageID {
			continue
		}
		turnIDs[turn.ID] = struct{}{}
	}
	if len(turnIDs) == 0 {
		return nil
	}

	messages, err := e.messages.ListBySession(ctx, sessionID)
	if err != nil {
		return nil
	}
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.TurnID == nil {
			continue
		}
		if _, ok := turnIDs[*message.TurnID]; !ok {
			continue
		}
		if identity, ok := parseProjectIdentityFromMessage(message); ok {
			return identity
		}
	}
	return nil
}

func parseProjectIdentityFromMessage(message repo.ChatMessage) (*projectIdentity, bool) {
	if !strings.EqualFold(strings.TrimSpace(message.Role), "tool_result") {
		return nil, false
	}
	if strings.TrimSpace(message.Content) == "" {
		return nil, false
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(message.Content), &payload); err != nil {
		return nil, false
	}
	name := stringValue(payload["tool_name"])
	if name == "" {
		return nil, false
	}
	result := ToolResult{
		Name:  name,
		Error: stringValue(payload["error"]),
	}
	if message.ToolCallID != nil {
		result.ToolCallID = strings.TrimSpace(*message.ToolCallID)
	}
	if output, ok := payload["output"].(map[string]any); ok {
		result.Output = output
	}
	return parseProjectIdentityFromToolResult(result)
}

func isFreshKickoffRequest(session *chat.ChatSession, message repo.ChatMessage) bool {
	if session == nil || message.ID == uuid.Nil {
		return false
	}
	scopeType := strings.ToLower(strings.TrimSpace(session.ScopeType))
	if scopeType != "organization" && scopeType != "project" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
		return false
	}
	text := strings.ToLower(normalizeInstructionText(message.Content))
	if text == "" {
		return false
	}
	explicitFresh := strings.Contains(text, "from scratch") || strings.Contains(text, "start over") || strings.Contains(text, "fresh start") || strings.Contains(text, "fresh kickoff") || strings.Contains(text, "clean slate") || strings.Contains(text, "new run") || strings.Contains(text, "do over")
	if !explicitFresh && (strings.Contains(text, "resume") || strings.Contains(text, "recover") || strings.Contains(text, "continue where") || strings.Contains(text, "pick up where") || strings.Contains(text, "reopen")) {
		return false
	}
	if explicitFresh {
		return true
	}
	return strings.Contains(text, "restart") && (strings.Contains(text, "fresh") || strings.Contains(text, "from scratch") || strings.Contains(text, "clean slate") || strings.Contains(text, "start over") || strings.Contains(text, "new run"))
}

func isRecoveryResumeMessage(message repo.ChatMessage) bool {
	if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
		return false
	}
	metadata := messageMetadataMap(message.Metadata)
	if tasksvc.IsRecoveryResumeAction(stringValue(metadata["recovery_action"])) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(message.Content), "supervisor recovery: resume task") {
		return true
	}
	if strings.ToLower(stringValue(metadata["source"])) != "supervisor" {
		return false
	}
	text := strings.ToLower(normalizeInstructionText(message.Content))
	return strings.Contains(text, "resume") && strings.Contains(text, "task")
}

func messageMetadataMap(metadata json.RawMessage) map[string]any {
	if len(metadata) == 0 {
		return map[string]any{}
	}
	var decoded map[string]any
	if err := json.Unmarshal(metadata, &decoded); err != nil || decoded == nil {
		return map[string]any{}
	}
	return decoded
}

func (e *TurnEngine) handleFreshKickoffBlocker(ctx context.Context, rt *turnRuntime, reason string) (bool, error) {
	if rt == nil || !rt.freshKickoff {
		return false, nil
	}
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildFreshKickoffBlockerMessage(rt.projectIdentity, reason)); err != nil {
		return true, err
	}
	if err := e.completeTurn(ctx, rt); err != nil {
		return true, err
	}
	return true, nil
}

func buildRecoveryContinuationDepthReason(detail string) string {
	trimmed := strings.TrimSpace(detail)
	if trimmed == "" {
		trimmed = "prompt recovery could not progress within the continuation guardrails"
	}
	return fmt.Sprintf("%s across %d continuation turns", trimmed, maxContinuationTurnDepth)
}

func (e *TurnEngine) handleRecoveryContinuationDepthBlocker(ctx context.Context, rt *turnRuntime, reason string) (bool, error) {
	if rt == nil || !rt.recoveryTurn || rt.turn == nil || rt.session == nil {
		return false, nil
	}

	checkpoint, _ := e.currentRecoveryFileWriteCheckpoint(ctx, rt)
	queued, err := e.hasQueuedAgentTurnForSession(ctx, rt.session.ID, rt.currentJobID)
	if err != nil {
		return true, err
	}

	rt.stopReason = stopReasonRecoveryContinuation
	if queued {
		rt.recoveryQueuedTurn = true
		rt.recoveryBlockReason = ""
	} else {
		rt.recoveryQueuedTurn = false
		rt.recoveryBlockReason = buildRecoveryContinuationBlockedTaskReason(reason, checkpoint)
	}

	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildRecoveryContinuationBlockedMessage(reason, checkpoint, queued)); err != nil {
		return true, err
	}
	if err := e.completeTurn(ctx, rt); err != nil {
		return true, err
	}
	return true, nil
}

func buildRecoveryContinuationBlockedMessage(reason string, checkpoint *taskcheckpoint.RecoveryFileWriteCheckpoint, queued bool) string {
	trimmedReason := strings.TrimSpace(reason)
	if trimmedReason == "" {
		trimmedReason = "prompt recovery could not progress within the continuation guardrails"
	}
	if queued {
		if checkpoint != nil && strings.TrimSpace(checkpoint.ArtifactPath) != "" {
			return fmt.Sprintf("[Recovery turn halted: %s. A follow-on wakeup is already queued, so the task remains in progress. Resume from `%s` and narrow the next recovery attempt before retrying the final write.]", trimmedReason, strings.TrimSpace(checkpoint.ArtifactPath))
		}
		return fmt.Sprintf("[Recovery turn halted: %s. A follow-on wakeup is already queued, so the task remains in progress. Narrow the next recovery attempt before retrying.]", trimmedReason)
	}
	if checkpoint != nil && strings.TrimSpace(checkpoint.ArtifactPath) != "" {
		return fmt.Sprintf("[Recovery turn halted: %s. The task is now blocked. Resume from `%s` and narrow the next recovery attempt before re-queueing.]", trimmedReason, strings.TrimSpace(checkpoint.ArtifactPath))
	}
	if checkpoint != nil && strings.TrimSpace(checkpoint.TargetPath) != "" {
		return fmt.Sprintf("[Recovery turn halted: %s. The task is now blocked. Continue from `%s` only after narrowing the next recovery attempt and producing the concrete file body before re-queueing.]", trimmedReason, strings.TrimSpace(checkpoint.TargetPath))
	}
	return fmt.Sprintf("[Recovery turn halted: %s. The task is now blocked. Narrow the next recovery attempt or split the work before re-queueing.]", trimmedReason)
}

func buildRecoveryContinuationBlockedTaskReason(reason string, checkpoint *taskcheckpoint.RecoveryFileWriteCheckpoint) string {
	trimmedReason := strings.TrimSpace(reason)
	if trimmedReason == "" {
		trimmedReason = "prompt recovery could not progress within the continuation guardrails"
	}
	if checkpoint != nil && strings.TrimSpace(checkpoint.ArtifactPath) != "" {
		return fmt.Sprintf("recovery halted after %s; resume from %s and narrow the next recovery attempt before re-queueing", trimmedReason, strings.TrimSpace(checkpoint.ArtifactPath))
	}
	if checkpoint != nil && strings.TrimSpace(checkpoint.TargetPath) != "" {
		return fmt.Sprintf("recovery halted after %s; continue from %s only after narrowing the next recovery attempt and producing the concrete file body before re-queueing", trimmedReason, strings.TrimSpace(checkpoint.TargetPath))
	}
	return fmt.Sprintf("recovery halted after %s; narrow the next recovery attempt or split the work before re-queueing", trimmedReason)
}

func parseProjectIdentityFromToolResult(result ToolResult) (*projectIdentity, bool) {
	if !strings.EqualFold(strings.TrimSpace(result.Name), "project.create") {
		return nil, false
	}
	if strings.TrimSpace(result.Error) != "" || len(result.Output) == 0 {
		return nil, false
	}
	projectValue, ok := result.Output["project"]
	if !ok || projectValue == nil {
		return nil, false
	}
	projectMap, ok := projectValue.(map[string]any)
	if !ok || len(projectMap) == 0 {
		return nil, false
	}
	projectID, ok := parseUUIDAny(projectMap["id"])
	if !ok || projectID == uuid.Nil {
		return nil, false
	}
	slug := strings.TrimSpace(fmt.Sprintf("%v", projectMap["slug"]))
	if slug == "" {
		return nil, false
	}
	return &projectIdentity{id: projectID, slug: slug}, true
}

func (e *TurnEngine) buildProjectKickoffHandoffInstruction(ctx context.Context, rt *turnRuntime, result ToolResult) (string, bool) {
	if rt == nil || rt.session == nil {
		return "", false
	}
	if !strings.EqualFold(strings.TrimSpace(result.Name), "project.create") {
		return "", false
	}
	if strings.TrimSpace(result.Error) != "" {
		return "", false
	}
	if !strings.EqualFold(strings.TrimSpace(rt.agent.DisplayName), "frank") && !strings.EqualFold(strings.TrimSpace(rt.agent.AgentType), "general") {
		return "", false
	}
	scope := strings.TrimSpace(rt.session.ScopeType)
	if !strings.EqualFold(scope, "organization") && !strings.EqualFold(scope, "project") {
		return "", false
	}

	projectID, projectSlug, ok := parseProjectCreateIdentity(result)
	if !ok {
		return "", false
	}

	originatingRequest := ""
	if e.messages != nil && rt.initialMessageID != uuid.Nil {
		if source, err := e.messages.GetByID(ctx, rt.initialMessageID); err == nil && strings.EqualFold(strings.TrimSpace(source.Role), "user") {
			originatingRequest = normalizeInstructionText(source.Content)
		}
	}
	if originatingRequest == "" {
		return fmt.Sprintf("[Kickoff handoff requirement: provide Lori a complete handoff summary that includes all major requested workstreams for the created project (slug=%s, project_id=%s).]", projectSlug, projectID), true
	}
	return fmt.Sprintf("[Kickoff handoff requirement: provide Lori a complete handoff summary that includes all major requested workstreams from the originating user request. Created project: slug=%s project_id=%s. Originating user request: %s]", projectSlug, projectID, originatingRequest), true
}

func parseProjectCreateIdentity(result ToolResult) (uuid.UUID, string, bool) {
	if len(result.Output) == 0 {
		return uuid.Nil, "", false
	}
	rawProject, ok := result.Output["project"]
	if !ok || rawProject == nil {
		return uuid.Nil, "", false
	}
	project, ok := rawProject.(map[string]any)
	if !ok {
		return uuid.Nil, "", false
	}
	projectID, ok := parseUUIDAny(project["id"])
	if !ok || projectID == uuid.Nil {
		return uuid.Nil, "", false
	}
	projectSlug := strings.TrimSpace(fmt.Sprintf("%v", project["slug"]))
	if projectSlug == "" {
		return uuid.Nil, "", false
	}
	return projectID, projectSlug, true
}

func parseUUIDAny(value any) (uuid.UUID, bool) {
	switch typed := value.(type) {
	case uuid.UUID:
		if typed == uuid.Nil {
			return uuid.Nil, false
		}
		return typed, true
	case string:
		parsed, err := uuid.Parse(strings.TrimSpace(typed))
		if err != nil || parsed == uuid.Nil {
			return uuid.Nil, false
		}
		return parsed, true
	default:
		return uuid.Nil, false
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed))
	}
}

func (e *TurnEngine) maybeEscalateWorkerRetryProfile(ctx context.Context, rt *turnRuntime, current repo.ModelProfile) (repo.ModelProfile, bool) {
	if rt == nil || rt.session == nil {
		return repo.ModelProfile{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(rt.agent.AgentType), "worker") {
		return repo.ModelProfile{}, false
	}
	if strings.EqualFold(strings.TrimSpace(current.LogicalProfileID), "high-capability") {
		return repo.ModelProfile{}, false
	}
	escalated, err := e.profiles.GetCurrentByLogicalID(ctx, rt.session.OrganizationID, "high-capability")
	if err != nil {
		return repo.ModelProfile{}, false
	}
	return escalated, true
}

func buildProjectIdentityLockMessage(identity *projectIdentity) string {
	if identity == nil {
		return "[Project identity locked.]"
	}
	return fmt.Sprintf("[Project identity locked: slug=%s project_id=%s. Continue using this project. Do not reopen slug-conflict or archive-vs-reuse reasoning unless the user explicitly asks to create another project.]", strings.TrimSpace(identity.slug), identity.id)
}

func buildProjectCreateConflictGuardError(identity *projectIdentity) string {
	if identity == nil {
		return "project already created in this flow"
	}
	return fmt.Sprintf("project already created in this flow as slug=%s project_id=%s; continue with that project unless the user explicitly starts a new create attempt", strings.TrimSpace(identity.slug), identity.id)
}

func buildFreshKickoffBlockerMessage(identity *projectIdentity, reason string) string {
	reason = strings.TrimSpace(reason)
	if identity == nil {
		if reason == "" {
			return "[Fresh kickoff blocked: unable to reach initial task creation within the current prompt guardrails. Review the kickoff request and retry only after narrowing scope or choosing explicit resume/recovery mode.]"
		}
		return fmt.Sprintf("[Fresh kickoff blocked: %s. Review the kickoff request and retry only after narrowing scope or choosing explicit resume/recovery mode.]", reason)
	}
	if reason == "" {
		return fmt.Sprintf("[Fresh kickoff blocked: unable to reach initial task creation within the current prompt guardrails. Continue only with the canonical live project slug=%s project_id=%s unless you explicitly choose resume/recovery mode.]", strings.TrimSpace(identity.slug), identity.id)
	}
	return fmt.Sprintf("[Fresh kickoff blocked: %s. Continue only with the canonical live project slug=%s project_id=%s unless you explicitly choose resume/recovery mode.]", reason, strings.TrimSpace(identity.slug), identity.id)
}

func normalizeInstructionText(value string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	const maxLen = 800
	if len(normalized) <= maxLen {
		return normalized
	}
	return normalized[:maxLen] + "..."
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
		invocationInput := repo.ModelInvocation{
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
			RunID:                    cloneUUIDPointer(rt.runID),
			RunStepID:                cloneUUIDPointer(rt.runStepID),
			RunAttemptID:             cloneUUIDPointer(rt.runAttemptID),
			Metadata:                 invocationMetadata,
		}
		invocation, err := e.invocations.Create(ctx, invocationInput)
		if err != nil && isRunAttributionConstraintError(err) && (invocationInput.RunID != nil || invocationInput.RunStepID != nil || invocationInput.RunAttemptID != nil) {
			e.logger.Warn(
				"dropping invalid run attribution on model invocation create",
				"session_id", rt.session.ID,
				"turn_id", rt.turn.ID,
				"run_id", invocationInput.RunID,
				"run_step_id", invocationInput.RunStepID,
				"run_attempt_id", invocationInput.RunAttemptID,
				"error", err,
			)
			invocationInput.RunID = nil
			invocationInput.RunStepID = nil
			invocationInput.RunAttemptID = nil
			invocation, err = e.invocations.Create(ctx, invocationInput)
		}
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
			RunID:          cloneUUIDPointer(rt.runID),
			RunStepID:      cloneUUIDPointer(rt.runStepID),
			RunAttemptID:   cloneUUIDPointer(rt.runAttemptID),
			InvocationID:   cloneUUIDPointer(&invocation.ID),
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
				if escalated, ok := e.maybeEscalateWorkerRetryProfile(ctx, rt, profile); ok {
					profile = escalated
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
		rollupInvocation := invocation
		if rollupInvocation.RunID == nil {
			rollupInvocation.RunID = cloneUUIDPointer(rt.runID)
		}
		if rollupInvocation.RunStepID == nil {
			rollupInvocation.RunStepID = cloneUUIDPointer(rt.runStepID)
		}
		if rollupInvocation.RunAttemptID == nil {
			rollupInvocation.RunAttemptID = cloneUUIDPointer(rt.runAttemptID)
		}
		e.updateRunTokenRollup(ctx, rollupInvocation, usage)
		return response, nil
	}
}

func (e *TurnEngine) updateRunTokenRollup(ctx context.Context, invocation repo.ModelInvocation, usage *ModelUsage) {
	if e == nil || e.rollupUpdater == nil || usage == nil {
		return
	}
	inputTokens := usage.InputTokens
	outputTokens := usage.OutputTokens
	invocation.InputTokens = &inputTokens
	invocation.OutputTokens = &outputTokens
	if err := e.rollupUpdater.UpdateRunTokenCounts(ctx, invocation); err != nil {
		e.logger.Warn(
			"failed to update run token rollup",
			"invocation_id", invocation.ID,
			"run_id", invocation.RunID,
			"run_step_id", invocation.RunStepID,
			"run_attempt_id", invocation.RunAttemptID,
			"error", err,
		)
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

func (e *TurnEngine) resolveSessionAgentForSession(ctx context.Context, session *chat.ChatSession) (uuid.UUID, error) {
	if session == nil {
		return uuid.Nil, repo.ErrNotFound
	}
	scopeType := strings.TrimSpace(session.ScopeType)
	if strings.EqualFold(scopeType, "project_task") {
		agentID, err := e.resolveTaskScopeAgent(ctx, session.OrganizationID, session.ScopeID)
		if err != nil {
			return uuid.Nil, err
		}
		if agentID != uuid.Nil {
			if err := e.ensureAgentParticipant(ctx, session.ID, agentID); err != nil {
				return uuid.Nil, err
			}
			return agentID, nil
		}
	}
	if strings.EqualFold(scopeType, "project") {
		// For project-scoped sessions, prefer the project PM.
		if e.assignments != nil && session.ScopeID != uuid.Nil {
			pm, pmErr := e.assignments.GetPM(ctx, session.ScopeID)
			if pmErr == nil && pm.IsActive && pm.AgentID != uuid.Nil {
				if err := e.ensureAgentParticipant(ctx, session.ID, pm.AgentID); err != nil {
					return uuid.Nil, err
				}
				return pm.AgentID, nil
			}
		}

		// During early kickoff (before a PM is assigned), route the first turn to
		// Frank and then hand off to Lori for staffing/scoping follow-up.
		frankID, err := e.resolveFrankStarterID(ctx, session.OrganizationID)
		if err != nil {
			return uuid.Nil, err
		}
		if loriID, loriErr := e.resolveLoriStarterID(ctx, session.OrganizationID); loriErr == nil && loriID != uuid.Nil {
			if e.shouldRouteProjectKickoffToLori(ctx, session.ID, frankID) {
				if err := e.ensureAgentParticipant(ctx, session.ID, loriID); err != nil {
					return uuid.Nil, err
				}
				return loriID, nil
			}
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
	if e.tasks == nil {
		return uuid.Nil, fmt.Errorf("internal invariant: task-scoped session is missing task repository")
	}
	if taskID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("internal invariant: task-scoped session is missing task_id")
	}
	taskRecord, err := e.tasks.GetByID(ctx, taskID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return uuid.Nil, fmt.Errorf("internal invariant: task-scoped session task binding was not found")
		}
		return uuid.Nil, err
	}
	if taskRecord.OrganizationID != uuid.Nil && taskRecord.OrganizationID != organizationID {
		return uuid.Nil, repo.ErrNotFound
	}
	if taskRecord.AssignedAgentID == nil || *taskRecord.AssignedAgentID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("internal invariant: task-scoped session is missing assigned agent")
	}
	return *taskRecord.AssignedAgentID, nil
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

func (e *TurnEngine) resolveLoriStarterID(ctx context.Context, organizationID uuid.UUID) (uuid.UUID, error) {
	if e.agents == nil {
		return uuid.Nil, repo.ErrNotFound
	}
	starterAgents, err := e.agents.GetStarterTrio(ctx, organizationID)
	if err != nil {
		return uuid.Nil, err
	}
	for _, item := range starterAgents {
		if strings.EqualFold(strings.TrimSpace(item.DisplayName), "lori") && item.ID != uuid.Nil {
			return item.ID, nil
		}
	}
	for _, item := range starterAgents {
		if strings.EqualFold(strings.TrimSpace(item.AgentType), "pm") && item.ID != uuid.Nil {
			return item.ID, nil
		}
	}
	for _, item := range starterAgents {
		if item.ID == uuid.Nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.DisplayName), "frank") {
			continue
		}
		return item.ID, nil
	}
	return uuid.Nil, repo.ErrNotFound
}

func (e *TurnEngine) shouldRouteProjectKickoffToLori(ctx context.Context, sessionID, frankID uuid.UUID) bool {
	if e.turns == nil || sessionID == uuid.Nil || frankID == uuid.Nil {
		return false
	}
	turns, err := e.turns.ListBySession(ctx, sessionID)
	if err != nil {
		return false
	}
	for _, turn := range turns {
		if !strings.EqualFold(strings.TrimSpace(turn.RespondingType), "agent") {
			continue
		}
		if turn.RespondingID != frankID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(turn.Status), "completed") {
			continue
		}
		return true
	}
	return false
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

func (e *TurnEngine) resolveFirstAgentParticipantExcluding(ctx context.Context, sessionID, excludedAgentID uuid.UUID) (uuid.UUID, error) {
	participants, err := e.chat.ListParticipants(ctx, sessionID)
	if err != nil {
		return uuid.Nil, err
	}
	for _, participant := range participants {
		if participant == nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(participant.ParticipantType), "agent") {
			continue
		}
		if participant.ParticipantID == uuid.Nil || participant.ParticipantID == excludedAgentID {
			continue
		}
		return participant.ParticipantID, nil
	}
	return uuid.Nil, repo.ErrNotFound
}

func (e *TurnEngine) resolveModelProfile(ctx context.Context, session *chat.ChatSession, agent repo.Agent, purpose string, retryHint int, taskComplex bool) (repo.ModelProfile, error) {
	scopes := make([]model.Scope, 0, 3)
	scopes = append(scopes, model.Scope{Type: "agent", ID: agent.ID})
	if projectID := resolveProjectID(ctx, session, e.tasks); projectID != nil {
		scopes = append(scopes, model.Scope{Type: "project", ID: *projectID})
	}

	if e.resolver != nil {
		profile, err := e.resolver.Resolve(ctx, session.OrganizationID, strings.TrimSpace(purpose), scopes...)
		if err == nil && profile != nil {
			if downgraded, ok := e.maybeDowngradeWorkerProfile(ctx, session.OrganizationID, agent, strings.TrimSpace(purpose), *profile, taskComplex, retryHint); ok {
				return downgraded, nil
			}
			return *profile, nil
		}
	}

	candidates := make([]string, 0, 6)
	if strings.TrimSpace(purpose) == "listening_eval" || strings.TrimSpace(purpose) == "continuation_summary" {
		candidates = append(candidates, "haiku")
	}
	if agent.DefaultModelProfileID != nil && strings.TrimSpace(*agent.DefaultModelProfileID) != "" {
		candidates = append(candidates, strings.TrimSpace(*agent.DefaultModelProfileID))
	}
	if e.defaultModelProfileID != nil && strings.TrimSpace(*e.defaultModelProfileID) != "" {
		candidates = append(candidates, strings.TrimSpace(*e.defaultModelProfileID))
	}
	candidates = append(candidates, roleAwareFallbackCandidates(agent, strings.TrimSpace(purpose), taskComplex, retryHint)...)
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

func (e *TurnEngine) isComplexAgentTurnTask(ctx context.Context, session *chat.ChatSession) bool {
	if session == nil || strings.TrimSpace(session.ScopeType) != "project_task" || e.tasks == nil {
		return false
	}
	taskID := session.ScopeID
	if taskID == uuid.Nil {
		return false
	}
	taskRecord, err := e.tasks.GetByID(ctx, taskID)
	if err != nil {
		return false
	}
	return taskLooksComplex(taskRecord.Title, taskRecord.Description)
}

func taskLooksComplex(title string, description *string) bool {
	text := strings.TrimSpace(title)
	if description != nil {
		if desc := strings.TrimSpace(*description); desc != "" {
			if text != "" {
				text += "\n"
			}
			text += desc
		}
	}
	if text == "" {
		return false
	}

	lower := strings.ToLower(text)
	// Keep this heuristic intentionally conservative: role-based worker downgrades
	// should only be bypassed when the task likely needs deeper reasoning.
	complexSignals := []string{
		"multi-step",
		"multi step",
		"decompose",
		"decomposition",
		"architecture",
		"refactor",
		"migration",
		"cross-service",
		"cross service",
		"end-to-end",
		"integration",
		"investigate",
	}
	for _, signal := range complexSignals {
		if strings.Contains(lower, signal) {
			return true
		}
	}

	if strings.Count(lower, "\n- ") >= 3 || strings.Count(lower, "\n* ") >= 3 {
		return true
	}
	if len(strings.Fields(lower)) >= 120 {
		return true
	}
	return false
}

func roleAwareFallbackCandidates(agent repo.Agent, purpose string, taskComplex bool, retryHint int) []string {
	p := strings.TrimSpace(strings.ToLower(purpose))
	if p != "agent_turn" {
		return []string{"high-capability", "standard", "haiku"}
	}
	if retryHint > 0 {
		return []string{"high-capability", "standard", "haiku"}
	}

	role := strings.TrimSpace(strings.ToLower(agent.AgentType))
	switch role {
	case "worker":
		if taskComplex {
			return []string{"standard", "high-capability", "haiku"}
		}
		return []string{"standard", "haiku", "high-capability"}
	case "reviewer":
		if taskComplex {
			return []string{"high-capability", "standard", "haiku"}
		}
		return []string{"standard", "high-capability", "haiku"}
	case "project_manager", "pm", "general":
		return []string{"high-capability", "standard", "haiku"}
	default:
		return []string{"standard", "high-capability", "haiku"}
	}
}

func (e *TurnEngine) maybeDowngradeWorkerProfile(
	ctx context.Context,
	orgID uuid.UUID,
	agent repo.Agent,
	purpose string,
	resolved repo.ModelProfile,
	taskComplex bool,
	retryHint int,
) (repo.ModelProfile, bool) {
	if !strings.EqualFold(strings.TrimSpace(purpose), "agent_turn") {
		return repo.ModelProfile{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(agent.AgentType), "worker") {
		return repo.ModelProfile{}, false
	}
	if retryHint > 0 || taskComplex {
		return repo.ModelProfile{}, false
	}
	if agent.DefaultModelProfileID != nil && strings.TrimSpace(*agent.DefaultModelProfileID) != "" {
		return repo.ModelProfile{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(resolved.LogicalProfileID), "high-capability") {
		return repo.ModelProfile{}, false
	}
	standard, err := e.profiles.GetCurrentByLogicalID(ctx, orgID, "standard")
	if err != nil {
		return repo.ModelProfile{}, false
	}
	return standard, true
}

func agentTurnPromptGuardrailTokens(agent repo.Agent, taskComplex bool) int {
	if strings.EqualFold(strings.TrimSpace(agent.AgentType), "worker") {
		if taskComplex {
			return defaultPromptTokenGuardrail
		}
		return workerPromptTokenGuardrail
	}
	return defaultPromptTokenGuardrail
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

func shouldSuppressAutoContinuationForStopReason(stopReason *string) bool {
	if stopReason == nil {
		return false
	}
	switch strings.TrimSpace(*stopReason) {
	case stopReasonRecoveryCLIRejected, stopReasonRecoveryFileRejected:
		return true
	default:
		return false
	}
}

func (e *TurnEngine) ensureRecoveryTurnDurableTaskState(ctx context.Context, rt *turnRuntime) error {
	if e == nil || e.tasks == nil || rt == nil || rt.session == nil || rt.turn == nil {
		return nil
	}
	if !rt.recoveryTurn && strings.TrimSpace(rt.recoveryBlockReason) == "" {
		return nil
	}
	if rt.recoveryQueuedTurn {
		return nil
	}

	reason := strings.TrimSpace(rt.recoveryBlockReason)
	taskID := resolveTaskID(rt.session)
	if taskID == nil || *taskID == uuid.Nil {
		return nil
	}

	taskRecord, err := e.tasks.GetByID(ctx, *taskID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	if reason == "" {
		if !rt.recoveryTurn {
			return nil
		}
		if !shouldSuppressAutoContinuationForStopReason(rt.turn.StopReason) {
			return nil
		}
		reason = buildRecoveryTaskBlockedReason(taskRecord.Metadata, rt.turn.ID, rt.turn.StopReason)
	}
	if reason == "" {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "in_progress") {
		return nil
	}

	if e.taskTransitions != nil {
		if _, err := e.taskTransitions.MarkBlocked(ctx, taskRecord.ID, reason, tasksvc.Actor{Type: "system"}); err != nil {
			var transitionErr tasksvc.ErrInvalidStatusTransition
			if errors.As(err, &transitionErr) {
				refreshed, refreshErr := e.tasks.GetByID(ctx, taskRecord.ID)
				if refreshErr == nil {
					nextStatus := strings.ToLower(strings.TrimSpace(refreshed.WorkStatus))
					if nextStatus == "blocked" || nextStatus == "done" || nextStatus == "cancelled" {
						return nil
					}
				}
			}
			return err
		}
		return nil
	}

	taskRecord.WorkStatus = "blocked"
	taskRecord.CompletedAt = nil
	_, err = e.tasks.Update(ctx, taskRecord)
	return err
}

func buildRecoveryTaskBlockedReason(metadata json.RawMessage, turnID uuid.UUID, stopReason *string) string {
	if checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(metadata); ok {
		if strings.TrimSpace(checkpoint.HaltTurnID) == turnID.String() {
			return buildRecoveryFileWriteBlockedTaskReason(checkpoint.TargetPath, checkpoint.ArtifactPath, checkpoint.FailureReason)
		}
	}
	if stopReason == nil {
		return ""
	}
	return fmt.Sprintf("recovery halted without a queued continuation (stop_reason=%s); inspect the last recovery turn before re-queueing the task", strings.TrimSpace(*stopReason))
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

func completedWorkSignalFromMessages(messages []repo.ChatMessage, turnID uuid.UUID) (workCompletionSignal, bool) {
	var latest workCompletionSignal
	found := false
	for _, message := range messages {
		if message.TurnID == nil || *message.TurnID != turnID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(message.Role), "tool_result") {
			continue
		}
		toolName, output, errText, ok := parseToolResultMessage(message.Content)
		if !ok || strings.TrimSpace(errText) != "" {
			continue
		}
		if !strings.EqualFold(toolName, "git.commit") {
			continue
		}
		commitSHA := strings.TrimSpace(anyString(output["sha"]))
		if commitSHA == "" {
			commitSHA = strings.TrimSpace(anyString(output["short_sha"]))
		}
		filesCommitted := anyInt(output["files_committed"])
		if commitSHA == "" || filesCommitted < 1 {
			continue
		}
		latest = workCompletionSignal{
			commitSHA:      commitSHA,
			filesCommitted: filesCommitted,
		}
		found = true
	}
	return latest, found
}

func parseToolResultMessage(content string) (string, map[string]any, string, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", nil, "", false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return "", nil, "", false
	}
	if payload == nil {
		return "", nil, "", false
	}
	toolName := strings.TrimSpace(anyString(payload["tool_name"]))
	if toolName == "" {
		return "", nil, "", false
	}
	output, _ := payload["output"].(map[string]any)
	errText := strings.TrimSpace(anyString(payload["error"]))
	return toolName, output, errText, true
}

func anyString(raw any) string {
	if raw == nil {
		return ""
	}
	switch typed := raw.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprintf("%v", raw)
	}
}

func anyInt(raw any) int {
	switch typed := raw.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
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

func runIDFromMetadata(metadata json.RawMessage) *uuid.UUID {
	return runAttributionIDFromMetadata(metadata, "run_id")
}

func runStepIDFromMetadata(metadata json.RawMessage) *uuid.UUID {
	return runAttributionIDFromMetadata(metadata, "run_step_id")
}

func runAttemptIDFromMetadata(metadata json.RawMessage) *uuid.UUID {
	return runAttributionIDFromMetadata(metadata, "run_attempt_id")
}

func runAttributionIDFromMetadata(metadata json.RawMessage, key string) *uuid.UUID {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return nil
	}
	id, ok := payloadUUID(payload, key)
	if !ok {
		return nil
	}
	return cloneUUIDPointer(&id)
}

func isRunAttributionConstraintError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "model_invocation_run_id_fk") ||
		strings.Contains(text, "model_invocation_run_step_id_fk") ||
		strings.Contains(text, "model_invocation_run_attempt_id_fk")
}

func (e *TurnEngine) logicalMessageCancelled(ctx context.Context, sessionID, messageID uuid.UUID) (bool, error) {
	if e.turns == nil {
		return false, nil
	}
	turns, err := e.turns.ListBySession(ctx, sessionID)
	if err != nil {
		return false, err
	}
	for _, turn := range turns {
		if turn.TriggerMessageID == nil || *turn.TriggerMessageID != messageID {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(turn.Status), "cancelled") {
			return true, nil
		}
	}
	return false, nil
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

func (e *TurnEngine) projectPausedForSession(ctx context.Context, session *chat.ChatSession) (bool, string, error) {
	if session == nil || e.projects == nil {
		return false, "", nil
	}
	projectID := resolveProjectID(ctx, session, e.tasks)
	if projectID == nil || *projectID == uuid.Nil {
		return false, "", nil
	}
	projectRecord, err := e.projects.GetByID(ctx, *projectID)
	if errors.Is(err, repo.ErrNotFound) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	pauseState := projectpause.Parse(projectRecord.Settings)
	return pauseState.IsPaused, pauseState.Reason, nil
}

func (e *TurnEngine) validationLoopBlockerForSession(ctx context.Context, session *chat.ChatSession) (bool, taskValidationGuardState, error) {
	if session == nil || e.tasks == nil {
		return false, taskValidationGuardState{}, nil
	}
	taskID := resolveTaskID(session)
	if taskID == nil || *taskID == uuid.Nil {
		return false, taskValidationGuardState{}, nil
	}
	taskRecord, err := e.tasks.GetByID(ctx, *taskID)
	if errors.Is(err, repo.ErrNotFound) {
		return false, taskValidationGuardState{}, nil
	}
	if err != nil {
		return false, taskValidationGuardState{}, err
	}
	guard, ok := parseTaskValidationGuard(taskRecord.Metadata)
	if !ok || !guard.Blocked {
		return false, taskValidationGuardState{}, nil
	}
	if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "blocked") {
		return false, taskValidationGuardState{}, nil
	}
	return true, guard, nil
}

func (e *TurnEngine) enqueueAgentTurnIfActive(ctx context.Context, session *chat.ChatSession, payload AgentTurnPayload, runAfter *time.Time) (bool, error) {
	if paused, reason, err := e.projectPausedForSession(ctx, session); err != nil {
		return false, err
	} else if paused {
		e.logPausedTurnSkip("skipping agent turn enqueue for paused project", session, reason, payload.MessageID)
		return false, nil
	}
	if blocked, guard, err := e.validationLoopBlockerForSession(ctx, session); err != nil {
		return false, err
	} else if blocked {
		e.logValidationLoopSuppressed("skipping agent turn enqueue for blocked validation loop", session, payload.MessageID, guard)
		return false, nil
	}
	message, err := e.messages.GetByID(ctx, payload.MessageID)
	if err != nil {
		return false, err
	}
	if chat.AgentTurnDispatchCancelled(message.Metadata) {
		e.logger.Info("skipping enqueue for cancelled agent turn dispatch", "session_id", payload.SessionID, "message_id", payload.MessageID)
		return false, nil
	}
	cancelled, err := e.logicalMessageCancelled(ctx, payload.SessionID, payload.MessageID)
	if err != nil {
		return false, err
	}
	if cancelled {
		e.logger.Info("skipping enqueue after logical message cancellation", "session_id", payload.SessionID, "message_id", payload.MessageID)
		return false, nil
	}
	if _, err := e.enqueuer.Enqueue(ctx, nil, AgentTurnJobType, e.jobPriority, payload, runAfter); err != nil {
		return false, err
	}
	return true, nil
}

func (e *TurnEngine) logValidationLoopSuppressed(message string, session *chat.ChatSession, messageID uuid.UUID, guard taskValidationGuardState) {
	metrics.RecordAgentTurnDispatchSuppressed(validationLoopSuppressionReason)
	if e == nil || e.logger == nil || session == nil {
		return
	}
	attrs := []any{
		"session_id", session.ID,
		"scope_type", strings.TrimSpace(session.ScopeType),
		"tool_name", strings.TrimSpace(guard.ToolName),
		"failure_class", strings.TrimSpace(guard.FailureClass),
		"failure_code", strings.TrimSpace(guard.FailureCode),
		"failure_count", guard.Count,
	}
	if messageID != uuid.Nil {
		attrs = append(attrs, "message_id", messageID)
	}
	if taskID := resolveTaskID(session); taskID != nil && *taskID != uuid.Nil {
		attrs = append(attrs, "task_id", *taskID)
	}
	e.logger.Info(message, attrs...)
}

func (e *TurnEngine) logPausedTurnSkip(message string, session *chat.ChatSession, reason string, messageID uuid.UUID) {
	if e == nil || e.logger == nil || session == nil {
		return
	}
	attrs := []any{
		"session_id", session.ID,
		"scope_type", strings.TrimSpace(session.ScopeType),
	}
	if messageID != uuid.Nil {
		attrs = append(attrs, "message_id", messageID)
	}
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		attrs = append(attrs, "pause_reason", trimmed)
	}
	if projectID := resolveProjectID(context.Background(), session, e.tasks); projectID != nil && *projectID != uuid.Nil {
		attrs = append(attrs, "project_id", *projectID)
	}
	e.logger.Info(message, attrs...)
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

type toolExecutionBinding struct {
	projectID *uuid.UUID
	taskID    *uuid.UUID
}

func (e *TurnEngine) resolveToolExecutionBinding(ctx context.Context, session *chat.ChatSession) (toolExecutionBinding, error) {
	if session == nil {
		return toolExecutionBinding{}, nil
	}
	scopeType := strings.ToLower(strings.TrimSpace(session.ScopeType))
	switch scopeType {
	case "project":
		if session.ScopeID == uuid.Nil {
			return toolExecutionBinding{}, fmt.Errorf("internal invariant: project-scoped session is missing project_id")
		}
		projectID := session.ScopeID
		return toolExecutionBinding{projectID: &projectID}, nil
	case "project_task":
		if session.ScopeID == uuid.Nil {
			return toolExecutionBinding{}, fmt.Errorf("internal invariant: task-scoped tool execution is missing bound task context: session is missing task_id")
		}
		if e.tasks == nil {
			return toolExecutionBinding{}, fmt.Errorf("internal invariant: task-scoped tool execution is missing bound task context: task repository unavailable")
		}
		taskRecord, err := e.tasks.GetByID(ctx, session.ScopeID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return toolExecutionBinding{}, fmt.Errorf("internal invariant: task-scoped tool execution is missing bound task context: task record not found")
			}
			return toolExecutionBinding{}, err
		}
		if taskRecord.OrganizationID != uuid.Nil && taskRecord.OrganizationID != session.OrganizationID {
			return toolExecutionBinding{}, fmt.Errorf("internal invariant: task-scoped tool execution is missing bound task context: task organization mismatch")
		}
		if taskRecord.ProjectID == uuid.Nil {
			return toolExecutionBinding{}, fmt.Errorf("internal invariant: task-scoped tool execution is missing bound task context: project_id is missing for task")
		}
		taskID := session.ScopeID
		projectID := taskRecord.ProjectID
		return toolExecutionBinding{projectID: &projectID, taskID: &taskID}, nil
	default:
		return toolExecutionBinding{}, nil
	}
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

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
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

func normalizeModelToolCalls(calls []ModelToolCall) []ModelToolCall {
	if len(calls) == 0 {
		return nil
	}
	normalized := make([]ModelToolCall, 0, len(calls))
	for _, call := range calls {
		callCopy := call
		callCopy.Arguments = toolargs.Normalize(call.Name, cloneMap(call.Arguments))
		normalized = append(normalized, callCopy)
	}
	return normalized
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
