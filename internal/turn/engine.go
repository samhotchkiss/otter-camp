package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/assignmentrole"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/flowpolicy"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/metrics"
	"github.com/samhotchkiss/otter-camp/internal/model"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/projectfailure"
	"github.com/samhotchkiss/otter-camp/internal/projectpause"
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/taskorchestration"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
	"github.com/samhotchkiss/otter-camp/internal/toolargs"
	"github.com/samhotchkiss/otter-camp/internal/tools"
	"github.com/samhotchkiss/otter-camp/internal/workspace"
)

const (
	AgentTurnJobType                          = "agent_turn"
	defaultAgentTurnJobPriority               = 70
	backgroundSummarizeJobPriority            = 60
	defaultMaxToolCalls                       = 75
	defaultSyncMaxDuration                    = 5 * time.Minute
	defaultAsyncMaxDuration                   = 30 * time.Minute
	defaultProjectBootstrapTurnTimeout        = 90 * time.Second
	defaultProjectBootstrapPromotionTimeout   = 2 * time.Second
	defaultProjectBootstrapPromotionPollDelay = 25 * time.Millisecond
	defaultProjectBootstrapRestartRetryBudget = 2
	defaultListeningEvalDelay                 = 500 * time.Millisecond
	defaultAutoContinueDelay                  = 2 * time.Second
	defaultModelRetryBudget                   = 3
	defaultRateLimitBackoff                   = 30 * time.Second
	maxRateLimitBackoff                       = 30 * time.Minute
	maxRateLimitRetries                       = 5
	rateLimitRetryJitterThreshold             = 5 * time.Minute
	maxRateLimitRetryJitter                   = 30 * time.Second
	defaultTransientInfraBackoff              = 15 * time.Second
	maxTransientInfraBackoff                  = 5 * time.Minute
	maxTransientInfraRetries                  = 5
	maxConsecutiveAutoTurns                   = 10
	maxGenericRecoveryReplyRetries            = defaultModelRetryBudget - 1
	maxProjectBootstrapAutoTurns              = 3
	defaultSummarizeLayerBudget               = 0
	chunkPollSteerEveryNChunks                = 10
	maxContinuationTurnDepth                  = 3
	defaultTurnConsumerName                   = "turn-engine.user-message"
	defaultReactionConsumerName               = "turn-engine.reactions"
	defaultTurnCompletedName                  = "turn-engine.turn-completed"
	defaultTurnCancelledName                  = "turn-engine.turn-cancelled"
	defaultTaskStatusName                     = "turn-engine.task-status"
	defaultProjectResumedName                 = "turn-engine.project-resumed"
	defaultCancelConsumerPrefix               = "turn-engine.cancel"
	stopReasonMaxToolCalls                    = "max_tool_calls"
	stopReasonMaxDuration                     = "max_duration"
	stopReasonRecoveryCLIRejected             = "model_error"
	stopReasonRecoveryContinuation            = stopReasonRecoveryCLIRejected
	stopReasonRecoveryFileRejected            = "recovery_content_required"
	stopReasonRecoveryFileFallback            = stopReasonRecoveryCLIRejected
	stopReasonValidationBlocked               = "validation_loop_blocked"
	recoveryActionValidationResume            = "resume_validation_blocked_task"
	workerPromptTokenGuardrail                = 32000
	defaultPromptTokenGuardrail               = 64000
	validationLoopBlockThreshold              = 3
	validationLoopSuppressionReason           = "validation_loop_blocked"
	recoveryCLIRepairBudget                   = 1
	recoveryFileWriteRepairBudget             = 1
	recoveryArtifactDir                       = ".ottercamp/recovery"
	recoveryResumeExcerptChars                = 3000
	maxContinuationSummaryChars               = 4000
	projectBootstrapStaffingDiscoveryBudget   = 4
	projectBootstrapMetadataKey               = "project_bootstrap"
	projectBootstrapStatusActive              = "active"
	projectBootstrapStatusCompleted           = "completed"
	projectBootstrapStatusFailed              = "failed"
	projectBootstrapFailureStalled            = "stalled"
	projectBootstrapFailureGuardrail          = "guardrail_loop"
	projectBootstrapFailureMissingAssignments = "missing_assignments"
	projectBootstrapFailureMissingPM          = "pm_assignment_missing"
	projectBootstrapFailureMissingReviewer    = "reviewer_assignment_missing"
	projectBootstrapFailureRepoBinding        = "project_repo_binding_missing"
	projectBootstrapFailureCompoundParent     = "compound_parent_missing_children"
	projectBootstrapFailureSetupTaskScope     = "bootstrap_setup_task_unbounded"
	projectBootstrapFailureSetupTaskChildren  = "bootstrap_setup_task_hidden_children"
	projectBootstrapFailureFirstWaveFlow      = "first_wave_flow_invalid"
	projectBootstrapFailureFirstWaveExecution = "first_wave_execution_missing"
	projectBootstrapFailureFirstWaveSize      = "first_wave_task_unbounded"
	projectBootstrapValidationPending         = "pending"
	projectBootstrapValidationPassed          = "passed"
	projectBootstrapValidationFailed          = "failed"
	projectBootstrapCheckpointStaffing        = projectBootstrapCheckpointStaffingPersisted
	projectBootstrapCheckpointTaskTree        = projectBootstrapCheckpointTaskTreePersisted
	projectBootstrapCheckpointFlowTemplates   = projectBootstrapCheckpointFlowTemplatesPersisted
	projectBootstrapCheckpointFirstWave       = projectBootstrapCheckpointFirstWaveSelected
	projectBootstrapCheckpointExecutions      = projectBootstrapCheckpointFirstWaveExecutions
	projectBootstrapCheckpointJobsClaimed     = projectBootstrapCheckpointFirstWaveJobsClaimed
	projectFailureActionArchive               = "archive"
	projectFailureActionPause                 = "pause"
	projectFailureCategoryBootstrap           = "bootstrap_product"
	projectFailureCategoryExecution           = "execution_runtime"
	projectFailureCategoryProvider            = "provider_api"
	projectFailureClassExecutionRuntime       = "task_runtime_failed"
	projectFailureClassProviderAuth           = projectBootstrapFailureProviderAuth
	projectFailureClassProviderRateLimit      = projectBootstrapFailureProviderRateLimit
	projectFailureClassProviderTransient      = projectBootstrapFailureProviderTransient
	projectBootstrapSource                    = "project_bootstrap"
	projectBootstrapTemplateSlug              = "bootstrap-governance-gate"
	bootstrapFrankSignOffStepSlug             = "record-frank-sign-off"
	bootstrapChildTaskBoundednessError        = "parent integration follow-on tasks must already be bounded before they are created"
	taskContinuationResumeMessageSource       = "task_continuation_resume"
)

var (
	errContextCompressionContinuationDepthExceeded = errors.New("context compression continuation depth exceeded")
	errAgentTurnPromptGuardrailDepthExceeded       = errors.New("agent turn prompt exceeded guardrail continuation depth")
	explicitDeliverablePathPattern                 = regexp.MustCompile(`(?i)\bdeliverable:\s*([^\s,;]+)`)
)

const (
	errMissingTaskTransitionServiceForValidationBlock = "turn engine requires task transition service to block validation-loop tasks"
	errMissingTaskTransitionServiceForRecoveryBlock   = "turn engine requires task transition service to block recovery tasks"
)

var (
	ErrModelTransient           = errors.New("transient model failure")
	errTurnDeferred             = errors.New("turn deferred")
	errTurnCancelled            = errors.New("turn cancelled")
	errTurnPaused               = errors.New("turn paused")
	errTurnSessionClosed        = errors.New("turn cancelled because session or project closed")
	errProjectBootstrapWatchdog = errors.New("project bootstrap watchdog timeout")
	errAsyncTurnWatchdog        = errors.New("async turn watchdog timeout")
)

type AgentTurnPayload struct {
	SessionID              uuid.UUID  `json:"session_id"`
	MessageID              uuid.UUID  `json:"message_id"`
	AgentID                *uuid.UUID `json:"agent_id,omitempty"`
	FlowNodeExecutionID    *uuid.UUID `json:"flow_node_execution_id,omitempty"`
	RetryCount             int        `json:"retry_count,omitempty"`
	RateLimitJitterApplied bool       `json:"rate_limit_jitter_applied,omitempty"`
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
	CreateSession(ctx context.Context, input chat.CreateSessionInput) (*chat.ChatSession, error)
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
	SetTriggerMessageID(ctx context.Context, id uuid.UUID, triggerMessageID *uuid.UUID) (repo.ChatTurn, error)
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
	GetByProjectAndNumber(ctx context.Context, projectID uuid.UUID, taskNumber int) (repo.ProjectTask, error)
	ListByProject(ctx context.Context, projectID uuid.UUID, statuses ...string) ([]repo.ProjectTask, error)
	UpdateMetadata(ctx context.Context, id uuid.UUID, metadata json.RawMessage) (repo.ProjectTask, error)
	Update(ctx context.Context, task repo.ProjectTask) (repo.ProjectTask, error)
}

func updateTurnTaskMetadata(ctx context.Context, tasks taskRepository, taskRecord repo.ProjectTask) (repo.ProjectTask, error) {
	return tasks.UpdateMetadata(ctx, taskRecord.ID, taskRecord.Metadata)
}

type taskTransitionService interface {
	TransitionStatus(ctx context.Context, taskID uuid.UUID, toStatus string, actor tasksvc.Actor) (*tasksvc.ProjectTask, error)
	TransitionStatusWithPayload(ctx context.Context, taskID uuid.UUID, toStatus string, actor tasksvc.Actor, extraPayload map[string]any) (*tasksvc.ProjectTask, error)
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
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.AgentProjectAssignment, error)
}

type projectRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

type projectEnvironmentLister interface {
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.ProjectEnvironment, error)
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
	Environments    projectEnvironmentLister
	Organizations   organizationRepository
	MemorySources   memorySourceRepository
	Memories        memoryRepository

	DefaultModelProfileID       *string
	MaxToolCalls                int
	SyncMaxDuration             time.Duration
	AsyncMaxDuration            time.Duration
	ProjectBootstrapTurnTimeout time.Duration
	ListeningEvalDelay          time.Duration
	ModelRetryBudget            int
	JobPriority                 int
	Now                         func() time.Time
	Sleep                       func(context.Context, time.Duration) error
	Logger                      *slog.Logger
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
	environments    projectEnvironmentLister
	organizations   organizationRepository
	sources         memorySourceRepository
	memories        memoryRepository

	defaultModelProfileID       *string
	maxToolCalls                int
	syncMaxDuration             time.Duration
	asyncMaxDuration            time.Duration
	projectBootstrapTurnTimeout time.Duration
	listeningEvalDelay          time.Duration
	modelRetryBudget            int
	jobPriority                 int
	now                         func() time.Time
	sleep                       func(context.Context, time.Duration) error
	logger                      *slog.Logger
	cancelConsumerName          string
	rollupUpdater               *model.RollupUpdater
}

type turnRuntime struct {
	session             *chat.ChatSession
	agent               repo.Agent
	turn                *chat.ChatTurn
	initialMessageID    uuid.UUID
	initialMessageText  string
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
	recoveryWriteDone   bool
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
	if opts.Environments == nil && opts.Pool != nil {
		opts.Environments = repo.NewProjectEnvironmentRepo(opts.Pool)
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
	if opts.ProjectBootstrapTurnTimeout <= 0 {
		opts.ProjectBootstrapTurnTimeout = defaultProjectBootstrapTurnTimeout
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
		pool:                        opts.Pool,
		dataDir:                     strings.TrimSpace(opts.DataDir),
		chat:                        opts.Chat,
		toolResolver:                opts.ToolResolver,
		assembler:                   opts.Assembler,
		summarization:               opts.Summarization,
		models:                      opts.ModelGateway,
		dispatcher:                  opts.Dispatcher,
		runCanceler:                 opts.RunCanceler,
		events:                      opts.Events,
		enqueuer:                    opts.Enqueuer,
		invocations:                 opts.Invocations,
		profiles:                    opts.ModelProfiles,
		resolver:                    opts.Profiles,
		messages:                    opts.Messages,
		turns:                       opts.Turns,
		sessions:                    opts.Sessions,
		agents:                      opts.Agents,
		tasks:                       opts.Tasks,
		taskTransitions:             opts.TaskTransitions,
		flowNodes:                   opts.FlowNodes,
		flowAdvancer:                opts.FlowAdvancer,
		assignments:                 opts.Assignments,
		projects:                    opts.Projects,
		environments:                opts.Environments,
		organizations:               opts.Organizations,
		sources:                     opts.MemorySources,
		memories:                    opts.Memories,
		defaultModelProfileID:       opts.DefaultModelProfileID,
		maxToolCalls:                opts.MaxToolCalls,
		syncMaxDuration:             opts.SyncMaxDuration,
		asyncMaxDuration:            opts.AsyncMaxDuration,
		projectBootstrapTurnTimeout: opts.ProjectBootstrapTurnTimeout,
		listeningEvalDelay:          opts.ListeningEvalDelay,
		modelRetryBudget:            opts.ModelRetryBudget,
		jobPriority:                 opts.JobPriority,
		now:                         opts.Now,
		sleep:                       opts.Sleep,
		logger:                      opts.Logger,
		cancelConsumerName:          defaultCancelConsumerPrefix,
		rollupUpdater:               rollupUpdater,
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

func (e *TurnEngine) SubscribeTurnCancelledBootstrapRecovery(orgID *uuid.UUID) eventbus.Subscription {
	return e.events.Subscribe(defaultTurnCancelledName, orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		return e.HandleTurnCancelledEvent(ctx, event)
	})
}

func (e *TurnEngine) SubscribeTaskStatusBootstrap(orgID *uuid.UUID) eventbus.Subscription {
	return e.events.Subscribe(defaultTaskStatusName, orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		return e.HandleTaskStatusChangedEvent(ctx, event)
	})
}

func (e *TurnEngine) SubscribeProjectResumedPendingTurns(orgID *uuid.UUID) eventbus.Subscription {
	return e.events.Subscribe(defaultProjectResumedName, orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		return e.HandleProjectResumedEvent(ctx, event)
	})
}

func (e *TurnEngine) RecoverCancelledBootstrapSessions(ctx context.Context) (int, error) {
	if e == nil || e.pool == nil || e.chat == nil {
		return 0, nil
	}

	rows, err := e.pool.Query(ctx, `
		WITH latest_turn AS (
			SELECT DISTINCT ON (session_id)
				session_id,
				id,
				status
			FROM chat_turn
			ORDER BY session_id, turn_number DESC, created_at DESC, id DESC
		)
		SELECT s.id, lt.id
		FROM chat_session s
		JOIN latest_turn lt ON lt.session_id = s.id
		WHERE s.scope_type = 'project'
		  AND s.mode = 'async'
		  AND s.status = 'active'
		  AND s.current_turn_id IS NULL
		  AND COALESCE(NULLIF(s.metadata->'project_bootstrap'->>'status', ''), '') = 'active'
		  AND lt.status = 'cancelled'
		  AND NOT EXISTS (
			SELECT 1
			FROM job_queue jq
			WHERE jq.job_type = $1
			  AND jq.status IN ('pending', 'claimed')
			  AND jq.payload->>'session_id' = s.id::text
		  )
	`, AgentTurnJobType)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	recovered := 0
	for rows.Next() {
		var sessionID, turnID uuid.UUID
		if err := rows.Scan(&sessionID, &turnID); err != nil {
			return recovered, err
		}
		session, err := e.chat.GetSession(ctx, sessionID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				continue
			}
			return recovered, err
		}
		before, err := e.countActiveAgentTurnJobsForSession(ctx, sessionID)
		if err != nil {
			return recovered, err
		}
		if err := e.handleProjectBootstrapCancelledTurn(ctx, session, turnID); err != nil {
			return recovered, err
		}
		after, err := e.countActiveAgentTurnJobsForSession(ctx, sessionID)
		if err != nil {
			return recovered, err
		}
		if after > before {
			recovered++
		}
	}
	if rows.Err() != nil {
		return recovered, rows.Err()
	}
	return recovered, nil
}

func (e *TurnEngine) CleanupLegacyCancelConsumerCursors(ctx context.Context) (int, error) {
	if e == nil || e.pool == nil {
		return 0, nil
	}

	commandTag, err := e.pool.Exec(ctx, `
		DELETE FROM consumer_cursor
		WHERE consumer_name LIKE $1
		  AND consumer_name <> $2
	`, defaultCancelConsumerPrefix+".%", defaultCancelConsumerPrefix)
	if err != nil {
		return 0, err
	}
	return int(commandTag.RowsAffected()), nil
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
	if e.logger != nil {
		e.logger.Debug("agent_turn job: begin",
			"job_id", job.ID,
			"session_id", payload.SessionID,
			"message_id", payload.MessageID,
			"retry_count", payload.RetryCount,
		)
	}
	if session, err := e.chat.GetSession(ctx, payload.SessionID); err == nil {
		if paused, reason, pauseErr := e.projectPausedForSession(ctx, session); pauseErr != nil {
			return pauseErr
		} else if paused {
			e.logPausedTurnSkip("skipping queued agent turn for paused project", session, reason, payload.MessageID)
			return nil
		}
	}
	err := e.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &job.ID)
	if err == nil {
		if e.logger != nil {
			e.logger.Debug("agent_turn job: completed", "job_id", job.ID, "session_id", payload.SessionID, "message_id", payload.MessageID)
		}
		return nil
	}
	if handled, recoverErr := e.handleRecoverableBootstrapTurnJobFailure(ctx, payload, &job.ID, err); recoverErr != nil {
		return recoverErr
	} else if handled {
		return nil
	}
	return err
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
		if !strings.EqualFold(strings.TrimSpace(session.Status), "active") {
			return nil
		}
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
	if payload.FlowNodeExecutionID != nil && *payload.FlowNodeExecutionID != uuid.Nil {
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
	if strings.EqualFold(strings.TrimSpace(session.ScopeType), "project") {
		return e.handleProjectBootstrapCompletedTurn(ctx, session, payload.TurnID)
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
	if shouldSuppressAutoContinuationForRecoveryHalt(messages, payload.TurnID, taskRecord.Metadata) {
		return nil
	}
	latestUser := latestUserMessage(messages)
	if latestUser == nil {
		return nil
	}
	assistant := latestAssistantFinalForTurn(messages, payload.TurnID)
	if assistant == nil {
		if handled, handleErr := e.handleCompletedTaskResumeWithoutUsableAssistant(ctx, session, taskRecord, latestCompleted, latestUser, ""); handleErr != nil {
			return handleErr
		} else if handled {
			return nil
		}
		return nil
	}
	if handled, handleErr := e.handleCompletedTaskResumeWithoutUsableAssistant(ctx, session, taskRecord, latestCompleted, latestUser, assistant.Content); handleErr != nil {
		return handleErr
	} else if handled {
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

func (e *TurnEngine) handleCompletedTaskResumeWithoutUsableAssistant(
	ctx context.Context,
	session *chat.ChatSession,
	taskRecord repo.ProjectTask,
	latestCompleted *repo.ChatTurn,
	latestUser *repo.ChatMessage,
	assistantContent string,
) (bool, error) {
	if session == nil || latestCompleted == nil || latestUser == nil {
		return false, nil
	}
	if !isRecoveryResumeMessage(*latestUser) && !taskContinuationResumeMessageRootsHistory(*latestUser) {
		return false, nil
	}
	sessionMessages := messagesFromTaskSession(ctx, e, session)
	if recoveryTurnProducedDurableWrite(messagesForTurn(sessionMessages, latestCompleted.ID), taskRecord.Metadata) {
		if e.flowAdvancer != nil && latestCompleted.RespondingID != uuid.Nil {
			if _, err := e.flowAdvancer.AdvanceFlow(ctx, taskRecord.ID, flowsvc.Actor{Type: "agent", ID: latestCompleted.RespondingID}); err != nil {
				return true, err
			}
			return true, nil
		}
		return true, nil
	}
	if strings.TrimSpace(assistantContent) != "" && !looksLikeGenericTaskRecoveryReply(assistantContent) {
		return false, nil
	}

	if latestCompleted.RetryCount >= maxGenericRecoveryReplyRetries {
		reason := buildGenericRecoveryReplyBlockedReason(assistantContent, latestCompleted.RetryCount+1)
		if _, err := e.appendSystemMessage(ctx, latestCompleted.ID, session.ID, "[Recovery turn halted: "+reason+"]"); err != nil {
			return true, err
		}
		if e.taskTransitions == nil {
			return true, fmt.Errorf(errMissingTaskTransitionServiceForRecoveryBlock)
		}
		if _, err := e.taskTransitions.MarkBlocked(ctx, taskRecord.ID, reason, tasksvc.Actor{Type: "system"}); err != nil {
			return true, err
		}
		return true, nil
	}

	nextMessageID := latestUser.ID
	if e.chat != nil {
		retryMessage, appendErr := e.chat.AppendMessage(ctx, chat.AppendMessageInput{
			SessionID: session.ID,
			TurnID:    &latestCompleted.ID,
			Role:      "user",
			Content:   latestUser.Content,
			Metadata:  latestUser.Metadata,
		})
		if appendErr != nil {
			return true, appendErr
		}
		if retryMessage != nil && retryMessage.ID != uuid.Nil {
			nextMessageID = retryMessage.ID
		}
	}

	nextPayload := AgentTurnPayload{
		SessionID:  session.ID,
		MessageID:  nextMessageID,
		RetryCount: latestCompleted.RetryCount + 1,
	}
	if latestCompleted.RespondingID != uuid.Nil {
		agentID := latestCompleted.RespondingID
		nextPayload.AgentID = &agentID
	}
	runAfter := e.now().Add(defaultAutoContinueDelay).UTC()
	enqueued, err := e.enqueueAgentTurnIfActive(ctx, session, nextPayload, &runAfter)
	if err != nil {
		return true, err
	}
	return enqueued, nil
}

func messagesFromTaskSession(ctx context.Context, e *TurnEngine, session *chat.ChatSession) []repo.ChatMessage {
	if e == nil || e.messages == nil || session == nil {
		return nil
	}
	messages, err := e.messages.ListBySession(ctx, session.ID)
	if err != nil {
		return nil
	}
	return messages
}

func messagesForTurn(messages []repo.ChatMessage, turnID uuid.UUID) []repo.ChatMessage {
	if turnID == uuid.Nil || len(messages) == 0 {
		return nil
	}
	filtered := make([]repo.ChatMessage, 0, len(messages))
	for _, message := range messages {
		if message.TurnID == nil || *message.TurnID != turnID {
			continue
		}
		filtered = append(filtered, message)
	}
	return filtered
}

func recoveryTurnProducedDurableWrite(messages []repo.ChatMessage, metadata json.RawMessage) bool {
	if len(messages) == 0 {
		return false
	}
	if _, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(metadata); ok {
		return false
	}
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "tool_result") {
			continue
		}
		toolName, _, errText, ok := parseToolResultMessage(message.Content)
		if !ok || strings.TrimSpace(errText) != "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(toolName), "file.write") {
			return true
		}
	}
	return false
}

func (e *TurnEngine) HandleTurnCancelledEvent(ctx context.Context, event eventbus.DomainEvent) error {
	if event.EventType != "chat.turn.cancelled" {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(event.ActorType), "human") {
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
	if strings.EqualFold(strings.TrimSpace(session.ScopeType), "project") {
		return e.handleProjectBootstrapCancelledTurn(ctx, session, payload.TurnID)
	}
	return nil
}

func (e *TurnEngine) HandleTaskStatusChangedEvent(ctx context.Context, event eventbus.DomainEvent) error {
	if event.EventType != "task.status_changed" || e == nil || e.pool == nil {
		return nil
	}

	var payload struct {
		TaskID    string `json:"task_id"`
		ProjectID string `json:"project_id"`
		ToStatus  string `json:"to_status"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(payload.ToStatus), "done") {
		return nil
	}

	projectID, err := uuid.Parse(strings.TrimSpace(payload.ProjectID))
	if err != nil || projectID == uuid.Nil {
		return nil
	}
	taskID, err := uuid.Parse(strings.TrimSpace(payload.TaskID))
	if err != nil || taskID == uuid.Nil {
		return nil
	}

	taskRecord, err := repo.NewProjectTaskRepo(e.pool).GetByID(ctx, taskID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		return err
	}
	metadata := messageMetadataMap(taskRecord.Metadata)
	if bootstrapGate, _ := metadata["bootstrap_gate"].(bool); !bootstrapGate {
		if setupTask, _ := metadata["bootstrap_setup_task"].(bool); !setupTask {
			return nil
		}
	}

	session, err := repo.NewChatSessionRepo(e.pool).GetByScopeAndMode(ctx, "project", projectID, "async")
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		return err
	}

	progress, err := e.loadProjectBootstrapProgress(ctx, projectID)
	if err != nil {
		return err
	}
	if progress.BootstrapTaskID == uuid.Nil {
		return nil
	}
	if progress.ValidationFailed() {
		return e.refreshProjectBootstrapSessionState(ctx, session, progress)
	}

	progress, err = e.ensureProjectBootstrapFirstWaveExecution(ctx, progress)
	if err != nil {
		return err
	}
	return e.refreshProjectBootstrapSessionState(ctx, session, progress)
}

func (e *TurnEngine) HandleProjectResumedEvent(ctx context.Context, event eventbus.DomainEvent) error {
	if event.EventType != "project.resumed" || e == nil || e.pool == nil || e.enqueuer == nil || e.chat == nil {
		return nil
	}

	var payload struct {
		ProjectID uuid.UUID `json:"project_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil
	}
	if payload.ProjectID == uuid.Nil {
		return nil
	}

	rows, err := e.pool.Query(ctx, `
		SELECT DISTINCT cs.id, ct.trigger_message_id
		FROM chat_session cs
		LEFT JOIN project_task pt
		  ON cs.scope_type = 'project_task'
		 AND pt.id = cs.scope_id
		JOIN chat_turn ct ON ct.id = cs.current_turn_id
		WHERE cs.mode = 'async'
		  AND cs.status = 'active'
		  AND ct.status = 'pending'
		  AND ct.trigger_message_id IS NOT NULL
		  AND (
		    (cs.scope_type = 'project' AND cs.scope_id = $1)
		    OR
		    (cs.scope_type = 'project_task' AND pt.project_id = $1)
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM job_queue jq
		    WHERE jq.job_type = $2
		      AND jq.status IN ('pending', 'claimed')
		      AND (jq.payload->>'session_id')::uuid = cs.id
		  )
	`, payload.ProjectID, AgentTurnJobType)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var sessionID uuid.UUID
		var messageID uuid.UUID
		if err := rows.Scan(&sessionID, &messageID); err != nil {
			return err
		}
		session, err := e.chat.GetSession(ctx, sessionID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				continue
			}
			return err
		}
		if _, err := e.enqueueAgentTurnIfActive(ctx, session, AgentTurnPayload{
			SessionID: sessionID,
			MessageID: messageID,
		}, nil); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	pendingMessageRows, err := e.pool.Query(ctx, `
		SELECT DISTINCT cs.id, cm.id
		FROM chat_session cs
		LEFT JOIN project_task pt
		  ON cs.scope_type = 'project_task'
		 AND pt.id = cs.scope_id
		JOIN chat_message cm
		  ON cm.session_id = cs.id
		 AND cm.role = 'user'
		 AND cm.status = 'pending'
		LEFT JOIN chat_turn current_ct
		  ON current_ct.id = cs.current_turn_id
		WHERE cs.mode = 'async'
		  AND cs.status = 'active'
		  AND (
		    (cs.scope_type = 'project' AND cs.scope_id = $1)
		    OR
		    (cs.scope_type = 'project_task' AND pt.project_id = $1)
		  )
		  AND (
		    cs.current_turn_id IS NULL
		    OR current_ct.id IS NULL
		    OR current_ct.status IN ('completed', 'cancelled', 'failed')
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_turn ct
		    WHERE ct.session_id = cs.id
		      AND ct.trigger_message_id = cm.id
		      AND ct.status IN ('pending', 'in_progress')
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM job_queue jq
		    WHERE jq.job_type = $2
		      AND jq.status IN ('pending', 'claimed')
		      AND (jq.payload->>'session_id')::uuid = cs.id
		      AND (jq.payload->>'message_id')::uuid = cm.id
		  )
	`, payload.ProjectID, AgentTurnJobType)
	if err != nil {
		return err
	}
	defer pendingMessageRows.Close()

	for pendingMessageRows.Next() {
		var sessionID uuid.UUID
		var messageID uuid.UUID
		if err := pendingMessageRows.Scan(&sessionID, &messageID); err != nil {
			return err
		}
		session, err := e.chat.GetSession(ctx, sessionID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				continue
			}
			return err
		}
		if _, err := e.enqueueAgentTurnIfActive(ctx, session, AgentTurnPayload{
			SessionID: sessionID,
			MessageID: messageID,
		}, nil); err != nil {
			return err
		}
	}
	return pendingMessageRows.Err()
}

func (e *TurnEngine) handleProjectBootstrapCompletedTurn(ctx context.Context, session *chat.ChatSession, turnID uuid.UUID) error {
	if e == nil || session == nil || turnID == uuid.Nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(session.Status), "active") {
		return nil
	}
	if session.ScopeID == uuid.Nil || !strings.EqualFold(strings.TrimSpace(session.Mode), "async") {
		return nil
	}
	if paused, reason, pauseErr := e.projectPausedForSession(ctx, session); pauseErr != nil {
		return pauseErr
	} else if paused {
		e.logPausedTurnSkip("skipping bootstrap auto continuation for paused project", session, reason, turnID)
		return nil
	}

	progress, err := e.loadProjectBootstrapProgress(ctx, session.ScopeID)
	if err != nil {
		return err
	}

	turns, err := e.turns.ListBySession(ctx, session.ID)
	if err != nil {
		return err
	}
	latestCompleted := latestCompletedTurn(turns)
	if latestCompleted == nil || latestCompleted.ID != turnID {
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
	assistant := latestAssistantFinalForTurn(messages, turnID)
	if assistant == nil || latestUser.SequenceNumber > assistant.SequenceNumber {
		return nil
	}

	state := projectBootstrapStateFromMetadata(session.Metadata)
	if !projectBootstrapCompletedTurnManaged(messages, latestUser, state, progress) {
		return nil
	}
	if strings.TrimSpace(state.LastTurnID) == turnID.String() {
		return nil
	}
	now := e.now().UTC()
	initialMessageID := projectBootstrapWorkflowMessageID(latestUser)
	if strings.TrimSpace(state.InitialMessageID) != initialMessageID.String() {
		state = projectBootstrapState{
			InitialMessageID: initialMessageID.String(),
			StartedAt:        &now,
		}
	}
	if progress.BootstrapTaskOutstanding && progress.BootstrapSetupTaskCount > 0 && !projectBootstrapStateActive(state) {
		state.Status = projectBootstrapStatusActive
	}
	if persisted, persistErr := e.syncProjectBootstrapSetupFromWorkspace(ctx, session, turnID); persistErr != nil {
		return persistErr
	} else if persisted {
		progress, err = e.loadProjectBootstrapProgress(ctx, session.ScopeID)
		if err != nil {
			return err
		}
	}
	progress, err = e.ensureProjectBootstrapFirstWaveExecution(ctx, progress)
	if err != nil {
		return err
	}
	narrativeClaimedCompletion := projectBootstrapNarrativeClaimsCompletion(assistant)
	normalizeProjectBootstrapValidationFailure(&progress, narrativeClaimedCompletion)
	blockedReason, blockedClass := projectBootstrapBlockedRecoveryFailure(messages, state)
	if !progress.ValidationFailed() && latestCompleted.StopReason != nil && strings.TrimSpace(*latestCompleted.StopReason) == stopReasonValidationBlocked {
		if strings.TrimSpace(blockedReason) != "" {
			progress.ValidationStatus = projectBootstrapValidationFailed
			progress.ValidationFailureReason = blockedReason
			progress.ValidationFailureClass = blockedClass
		}
	}
	if strings.TrimSpace(blockedReason) != "" && projectBootstrapNarrativeOnlyReply(messages, assistant) {
		progress.ValidationStatus = projectBootstrapValidationFailed
		progress.ValidationFailureReason = buildProjectBootstrapNarrativeOnlyRecoveryFailureReason(blockedReason, assistant)
		progress.ValidationFailureClass = projectBootstrapFailureStalled
	}
	if !progress.ValidationFailed() && projectBootstrapRestartSession(session) && projectBootstrapRestartScaffoldOnly(progress) && projectBootstrapNarrativeOnlyReply(messages, assistant) {
		progress.ValidationStatus = projectBootstrapValidationFailed
		progress.ValidationFailureReason = buildProjectBootstrapNarrativeOnlyRestartFailureReason(assistant)
		progress.ValidationFailureClass = projectBootstrapFailureStalled
	}
	if narrativeClaimedCompletion && !progress.Materialized() {
		rt := &turnRuntime{session: session, turn: &chat.ChatTurn{ID: turnID}}
		if latestCompleted.RespondingID != uuid.Nil {
			rt.agent.ID = latestCompleted.RespondingID
		}
		if progress.ValidationFailed() {
			if handled, recoverErr := e.continueRecoverableProjectBootstrapValidation(ctx, rt, state, progress, now, false); recoverErr != nil {
				return recoverErr
			} else if handled {
				return nil
			}
		}
		return e.failProjectBootstrapValidation(ctx, rt, progress, now)
	}

	madeProgress := progress.AssignmentCount > state.AssignmentCount ||
		progress.StaffingDraftCount > state.StaffingDraftCount ||
		progress.PlannedTaskCount > state.PlannedTaskCount ||
		progress.PlannedFlowTemplateCount > state.PlannedFlowTemplateCount ||
		progress.FirstWaveTaskCount > state.FirstWaveTaskCount ||
		progress.FirstWavePromotedCount > state.FirstWavePromotedCount ||
		progress.FirstWaveExecutionCount > state.FirstWaveExecutionCount ||
		progress.FirstWaveJobCount > state.FirstWaveJobCount
	if !progress.ValidationFailed() &&
		projectBootstrapRestartSession(session) &&
		!madeProgress &&
		latestCompleted.StopReason != nil &&
		strings.TrimSpace(*latestCompleted.StopReason) == stopReasonValidationBlocked {
		progress.ValidationStatus = projectBootstrapValidationFailed
		if strings.TrimSpace(blockedReason) != "" {
			progress.ValidationFailureReason = buildProjectBootstrapNarrativeOnlyRecoveryFailureReason(blockedReason, assistant)
		} else {
			progress.ValidationFailureReason = buildProjectBootstrapNarrativeOnlyRestartFailureReason(assistant)
		}
		progress.ValidationFailureClass = projectBootstrapFailureStalled
	}
	if !progress.ValidationFailed() && projectBootstrapRestartSession(session) && !madeProgress && projectBootstrapNarrativeOnlyReply(messages, assistant) {
		progress.ValidationStatus = projectBootstrapValidationFailed
		progress.ValidationFailureReason = buildProjectBootstrapNarrativeOnlyRestartFailureReason(assistant)
		progress.ValidationFailureClass = projectBootstrapFailureStalled
	}
	state.Status = projectBootstrapStatusActive
	state.LastTurnID = turnID.String()
	state.BootstrapTaskID = progress.BootstrapTaskID.String()
	state.BootstrapTaskOutstanding = progress.BootstrapTaskOutstanding
	applyProjectBootstrapProgressState(&state, progress)
	state.UpdatedAt = &now
	state.CompletedAt = nil
	state.FailedAt = nil
	state.FailureCategory = ""
	state.FailureClass = ""
	state.FailurePhase = ""
	state.FailureReason = ""
	state.ProviderFailureClass = ""
	state.ProviderFailureReason = ""
	if latestCompleted.RespondingID != uuid.Nil {
		state.LastResponderID = latestCompleted.RespondingID.String()
	}
	if madeProgress {
		state.LastProgressAt = &now
		state.AutoTurnCount = 0
	} else {
		state.AutoTurnCount++
	}

	if progress.Materialized() {
		state.Status = projectBootstrapStatusCompleted
		state.AutoTurnCount = 0
		state.CompletedAt = &now
		state.BootstrapTaskOutstanding = progress.BootstrapTaskOutstanding
		return e.updateProjectBootstrapState(ctx, session, state)
	}

	if !progress.ValidationFailed() && projectBootstrapRestartSession(session) && projectBootstrapRestartScaffoldOnly(progress) {
		progress.ValidationStatus = projectBootstrapValidationFailed
		progress.ValidationFailureClass = projectBootstrapFailureRuntime
		progress.ValidationFailureReason = buildProjectBootstrapRestartScaffoldFailureReason()
	}

	if progress.ValidationFailed() {
		if projectBootstrapRecoverableMaxToolCallFailure(progress) && projectBootstrapHasNewerLiveContinuationTurn(turns, *latestCompleted) {
			state.ValidationStatus = ""
			state.ValidationFailureClass = ""
			state.ValidationFailureReason = ""
			return e.updateProjectBootstrapState(ctx, session, state)
		}
		rt := &turnRuntime{session: session, turn: &chat.ChatTurn{ID: turnID}}
		if latestCompleted.RespondingID != uuid.Nil {
			rt.agent.ID = latestCompleted.RespondingID
		}
		recoveryState := state
		if !madeProgress && recoveryState.AutoTurnCount > 0 {
			// Consecutive recoverable validation retries should consume one
			// continuation budget increment, not both the generic no-progress
			// increment and the recovery-continuation increment.
			recoveryState.AutoTurnCount--
		}
		if handled, recoverErr := e.continueRecoverableProjectBootstrapValidation(ctx, rt, recoveryState, progress, now, false); recoverErr != nil {
			return recoverErr
		} else if handled {
			return nil
		}
		return e.failProjectBootstrapValidation(ctx, rt, progress, now)
	}

	if progress.WaitingForBootstrapGate() {
		state.AutoTurnCount = 0
	}

	if state.AutoTurnCount >= maxProjectBootstrapAutoTurns {
		record := buildProjectBootstrapAutomaticFailureRecord(
			progress,
			projectFailureCategoryBootstrap,
			projectBootstrapFailureStalled,
			buildProjectBootstrapFailureReason(state.AutoTurnCount),
			now,
		)
		state.Status = projectBootstrapStatusFailed
		state.FailedAt = &now
		state.FailureCategory = record.FailureCategory
		state.FailureClass = record.FailureClass
		state.FailurePhase = record.FailurePhase
		state.FailureReason = record.FailureReason
		if err := e.updateProjectBootstrapState(ctx, session, state); err != nil {
			return err
		}
		_, _ = e.appendSystemMessage(ctx, turnID, session.ID, buildProjectBootstrapAutomaticFailureMessage(record))
		if err := e.applyProjectAutomaticFailure(ctx, session.ScopeID, record); err != nil {
			return err
		}
		return nil
	}

	if err := e.updateProjectBootstrapState(ctx, session, state); err != nil {
		return err
	}
	if queued, err := e.hasQueuedAgentTurnForSession(ctx, session.ID, nil); err != nil {
		return err
	} else if queued {
		return nil
	}

	continuationAgentID := e.projectBootstrapContinuationAgent(ctx, session, latestCompleted.RespondingID)
	continuationMessage, err := e.appendProjectBootstrapContinuationMessage(ctx, session.ID, continuationAgentID, state.InitialMessageID, state.AutoTurnCount)
	if err != nil {
		return err
	}

	nextPayload := AgentTurnPayload{
		SessionID: session.ID,
		MessageID: continuationMessage.ID,
	}
	if continuationAgentID != uuid.Nil {
		nextAgentID := continuationAgentID
		nextPayload.AgentID = &nextAgentID
	}
	runAfter := now.Add(defaultAutoContinueDelay)
	_, err = e.enqueueAgentTurnIfActive(ctx, session, nextPayload, &runAfter)
	return err
}

func (e *TurnEngine) handleProjectBootstrapCancelledTurn(ctx context.Context, session *chat.ChatSession, turnID uuid.UUID) error {
	if e == nil || session == nil || turnID == uuid.Nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(session.Status), "active") {
		return nil
	}
	if session.ScopeID == uuid.Nil || !strings.EqualFold(strings.TrimSpace(session.Mode), "async") {
		return nil
	}
	if session.CurrentTurnID != nil {
		return nil
	}
	if paused, reason, pauseErr := e.projectPausedForSession(ctx, session); pauseErr != nil {
		return pauseErr
	} else if paused {
		e.logPausedTurnSkip("skipping bootstrap cancellation recovery for paused project", session, reason, turnID)
		return nil
	}

	state := projectBootstrapStateFromMetadata(session.Metadata)
	if !strings.EqualFold(strings.TrimSpace(state.Status), projectBootstrapStatusActive) {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(state.Status), projectBootstrapStatusFailed) {
		return nil
	}

	progress, err := e.loadProjectBootstrapProgress(ctx, session.ScopeID)
	if err != nil {
		return err
	}
	normalizeProjectBootstrapValidationFailure(&progress, false)
	if progress.Materialized() {
		return nil
	}
	recoveryProgress, recoverableValidation := projectBootstrapCancelledRecoveryProgress(state, progress)
	if recoveryProgress.ValidationFailed() && !recoverableValidation {
		return nil
	}

	turns, err := e.turns.ListBySession(ctx, session.ID)
	if err != nil {
		return err
	}
	var cancelled *repo.ChatTurn
	for i := range turns {
		turn := turns[i]
		if turn.ID != turnID {
			continue
		}
		copyTurn := turn
		cancelled = &copyTurn
		break
	}
	if cancelled == nil || !strings.EqualFold(strings.TrimSpace(cancelled.Status), "cancelled") {
		return nil
	}
	for i := range turns {
		turn := turns[i]
		if turn.TurnNumber <= cancelled.TurnNumber {
			continue
		}
		return nil
	}

	messages, err := e.messages.ListBySession(ctx, session.ID)
	if err != nil {
		return err
	}
	if latestUser := latestUserMessage(messages); latestUser != nil && latestUser.SequenceNumber > latestMessageSequenceForTurn(messages, turnID) {
		metadata := messageMetadataMap(latestUser.Metadata)
		if strings.EqualFold(strings.TrimSpace(fmt.Sprintf("%v", metadata["source"])), projectBootstrapSource) {
			if e.pool == nil {
				return nil
			}
			queued, err := e.hasQueuedAgentTurnForSession(ctx, session.ID, nil)
			if err != nil {
				return err
			}
			if queued {
				return nil
			}
		}
	}

	initialMessageID := strings.TrimSpace(state.InitialMessageID)
	if initialMessageID == "" {
		if latestUser := latestUserMessage(messages); latestUser != nil {
			initialMessageID = projectBootstrapWorkflowMessageID(latestUser).String()
		}
	}
	if initialMessageID == "" {
		return nil
	}

	now := e.now().UTC()
	state.Status = projectBootstrapStatusActive
	state.LastTurnID = turnID.String()
	state.BootstrapTaskID = progress.BootstrapTaskID.String()
	state.BootstrapTaskOutstanding = progress.BootstrapTaskOutstanding
	applyProjectBootstrapProgressState(&state, progress)
	if recoverableValidation {
		state.ValidationStatus = ""
		state.ValidationFailureClass = ""
		state.ValidationFailureReason = ""
	}
	state.UpdatedAt = &now
	if cancelled.RespondingID != uuid.Nil {
		state.LastResponderID = cancelled.RespondingID.String()
	}
	if err := e.updateProjectBootstrapState(ctx, session, state); err != nil {
		return err
	}

	_, _ = e.appendSystemMessage(ctx, turnID, session.ID, "[Recovered cancelled bootstrap turn - retrying in a fresh turn.]")
	continuationAgentID := e.projectBootstrapContinuationAgent(ctx, session, cancelled.RespondingID)
	var continuationMessage *chat.ChatMessage
	if recoverableValidation {
		continuationMessage, err = e.appendProjectBootstrapRecoveryContinuationMessage(ctx, session.ID, continuationAgentID, initialMessageID, state.AutoTurnCount, recoveryProgress)
	} else {
		continuationMessage, err = e.appendProjectBootstrapContinuationMessage(ctx, session.ID, continuationAgentID, initialMessageID, state.AutoTurnCount)
	}
	if err != nil {
		return err
	}

	nextPayload := AgentTurnPayload{
		SessionID: session.ID,
		MessageID: continuationMessage.ID,
	}
	if continuationAgentID != uuid.Nil {
		nextAgentID := continuationAgentID
		nextPayload.AgentID = &nextAgentID
	}
	runAfter := now.Add(defaultAutoContinueDelay)
	_, err = e.enqueueAgentTurnIfActive(ctx, session, nextPayload, &runAfter)
	return err
}

func (e *TurnEngine) continueRecoverableProjectBootstrapValidation(
	ctx context.Context,
	rt *turnRuntime,
	state projectBootstrapState,
	progress projectBootstrapProgress,
	now time.Time,
	failCurrentTurn bool,
) (bool, error) {
	if e == nil || rt == nil || rt.session == nil || rt.turn == nil {
		return false, nil
	}
	normalizeProjectBootstrapValidationFailure(&progress, false)
	if !projectBootstrapRecoverableMaxToolCallFailure(progress) {
		return false, nil
	}

	if state.StartedAt == nil {
		state.StartedAt = &now
	}
	if projectBootstrapProgressAdvancedBeyondState(state, progress) {
		state.AutoTurnCount = 0
	}
	state.Status = projectBootstrapStatusActive
	state.BootstrapTaskID = progress.BootstrapTaskID.String()
	state.BootstrapTaskOutstanding = progress.BootstrapTaskOutstanding
	state.LastTurnID = rt.turn.ID.String()
	if rt.agent.ID != uuid.Nil {
		state.LastResponderID = rt.agent.ID.String()
	}
	state.AutoTurnCount++
	applyProjectBootstrapProgressState(&state, progress)
	state.ValidationStatus = ""
	state.ValidationFailureClass = ""
	state.ValidationFailureReason = ""
	state.UpdatedAt = &now
	state.CompletedAt = nil
	state.FailedAt = nil
	state.FailureCategory = ""
	state.FailureClass = ""
	state.FailurePhase = ""
	state.FailureReason = ""
	state.ProviderFailureClass = ""
	state.ProviderFailureReason = ""
	if state.AutoTurnCount > maxProjectBootstrapAutoTurns {
		return false, nil
	}

	if err := e.updateProjectBootstrapState(ctx, rt.session, state); err != nil {
		return true, err
	}
	if failCurrentTurn {
		if failErr := e.chat.FailTurn(ctx, rt.turn.ID, progress.ValidationFailureReason); failErr != nil && !errors.Is(failErr, chat.ErrInvalidStatusTransition) {
			return true, failErr
		}
	}

	if failCurrentTurn {
		queued, err := e.hasQueuedAgentTurnForSession(ctx, rt.session.ID, rt.currentJobID)
		if err != nil {
			return true, err
		}
		if queued {
			return true, nil
		}
	}

	continuationAgentID := e.projectBootstrapContinuationAgent(ctx, rt.session, rt.agent.ID)
	continuationMessage, err := e.appendProjectBootstrapRecoveryContinuationMessage(ctx, rt.session.ID, continuationAgentID, state.InitialMessageID, state.AutoTurnCount, progress)
	if err != nil {
		return true, err
	}

	nextPayload := AgentTurnPayload{
		SessionID: rt.session.ID,
		MessageID: continuationMessage.ID,
	}
	if continuationAgentID != uuid.Nil {
		nextAgentID := continuationAgentID
		nextPayload.AgentID = &nextAgentID
	}
	runAfter := now.Add(defaultAutoContinueDelay)
	if _, err := e.enqueueAgentTurnIfActive(ctx, rt.session, nextPayload, &runAfter); err != nil {
		return true, err
	}
	return true, nil
}

func (e *TurnEngine) continueRecoverableProjectBootstrapValidationForSession(
	ctx context.Context,
	session *chat.ChatSession,
	state projectBootstrapState,
	progress projectBootstrapProgress,
	now time.Time,
) (bool, error) {
	if e == nil || session == nil {
		return false, nil
	}
	normalizeProjectBootstrapValidationFailure(&progress, false)
	if !projectBootstrapRecoverableMaxToolCallFailure(progress) {
		return false, nil
	}
	if session.CurrentTurnID != nil {
		return false, nil
	}

	if state.StartedAt == nil {
		state.StartedAt = &now
	}
	if projectBootstrapProgressAdvancedBeyondState(state, progress) {
		state.AutoTurnCount = 0
	}
	state.Status = projectBootstrapStatusActive
	state.BootstrapTaskID = progress.BootstrapTaskID.String()
	state.BootstrapTaskOutstanding = progress.BootstrapTaskOutstanding
	state.AutoTurnCount++
	applyProjectBootstrapProgressState(&state, progress)
	state.ValidationStatus = ""
	state.ValidationFailureClass = ""
	state.ValidationFailureReason = ""
	state.UpdatedAt = &now
	state.CompletedAt = nil
	state.FailedAt = nil
	state.FailureCategory = ""
	state.FailureClass = ""
	state.FailurePhase = ""
	state.FailureReason = ""
	state.ProviderFailureClass = ""
	state.ProviderFailureReason = ""
	if state.AutoTurnCount > maxProjectBootstrapAutoTurns {
		return false, nil
	}

	if err := e.updateProjectBootstrapState(ctx, session, state); err != nil {
		return true, err
	}
	queued, err := e.hasQueuedAgentTurnForSession(ctx, session.ID, nil)
	if err != nil {
		return true, err
	}
	if queued {
		return true, nil
	}

	initialMessageID := strings.TrimSpace(state.InitialMessageID)
	if initialMessageID == "" && e.messages != nil {
		messages, err := e.messages.ListBySession(ctx, session.ID)
		if err != nil {
			return true, err
		}
		if latestUser := latestUserMessage(messages); latestUser != nil {
			initialMessageID = projectBootstrapWorkflowMessageID(latestUser).String()
		}
	}
	if initialMessageID == "" {
		return false, nil
	}

	latestResponderID, _ := uuid.Parse(strings.TrimSpace(state.LastResponderID))
	continuationAgentID := e.projectBootstrapContinuationAgent(ctx, session, latestResponderID)
	if continuationAgentID == uuid.Nil {
		return false, nil
	}
	continuationMessage, err := e.appendProjectBootstrapRecoveryContinuationMessage(ctx, session.ID, continuationAgentID, initialMessageID, state.AutoTurnCount, progress)
	if err != nil {
		return true, err
	}

	nextPayload := AgentTurnPayload{
		SessionID: session.ID,
		MessageID: continuationMessage.ID,
	}
	if continuationAgentID != uuid.Nil {
		nextAgentID := continuationAgentID
		nextPayload.AgentID = &nextAgentID
	}
	runAfter := now.Add(defaultAutoContinueDelay)
	if _, err := e.enqueueAgentTurnIfActive(ctx, session, nextPayload, &runAfter); err != nil {
		return true, err
	}
	return true, nil
}

func (e *TurnEngine) failProjectBootstrapValidation(ctx context.Context, rt *turnRuntime, progress projectBootstrapProgress, now time.Time) error {
	if rt == nil || rt.session == nil || rt.turn == nil {
		return nil
	}
	normalizeProjectBootstrapValidationFailure(&progress, false)

	state := projectBootstrapStateFromMetadata(rt.session.Metadata)
	if state.StartedAt == nil {
		state.StartedAt = &now
	}
	state.Status = projectBootstrapStatusFailed
	state.BootstrapTaskID = progress.BootstrapTaskID.String()
	state.BootstrapTaskOutstanding = progress.BootstrapTaskOutstanding
	state.LastTurnID = rt.turn.ID.String()
	if rt.agent.ID != uuid.Nil {
		state.LastResponderID = rt.agent.ID.String()
	}
	applyProjectBootstrapProgressState(&state, progress)

	record := buildProjectBootstrapAutomaticFailureRecord(
		progress,
		projectFailureCategoryBootstrap,
		progress.ValidationFailureClass,
		progress.ValidationFailureReason,
		now,
	)
	state.FailedAt = &now
	state.UpdatedAt = &now
	state.CompletedAt = nil
	state.FailureCategory = record.FailureCategory
	state.FailureClass = record.FailureClass
	state.FailurePhase = record.FailurePhase
	state.FailureReason = record.FailureReason
	if err := e.updateProjectBootstrapState(ctx, rt.session, state); err != nil {
		return err
	}
	if failErr := e.chat.FailTurn(ctx, rt.turn.ID, state.FailureReason); failErr != nil && !errors.Is(failErr, chat.ErrInvalidStatusTransition) {
		return failErr
	}
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildProjectBootstrapAutomaticFailureMessage(record)); err != nil {
		return err
	}
	return e.applyProjectAutomaticFailure(ctx, rt.session.ScopeID, record)
}

func (e *TurnEngine) ensureProjectBootstrapFirstWaveExecution(ctx context.Context, progress projectBootstrapProgress) (projectBootstrapProgress, error) {
	if e == nil || e.taskTransitions == nil || progress.ValidationStatus != projectBootstrapValidationPassed || progress.FirstWaveMaterialized() {
		return progress, nil
	}
	if progress.FirstWaveTaskCount == 0 || len(progress.FirstWaveTasks) == 0 {
		return progress, nil
	}

	projectID := progress.FirstWaveTasks[0].ProjectID
	if progress.BootstrapTaskOutstanding && progress.BootstrapTaskID != uuid.Nil {
		if !progress.BootstrapGateReady() {
			return progress, nil
		}
		if err := e.completeProjectBootstrapGateTask(ctx, progress.BootstrapTaskID); err != nil {
			return progress, err
		}
		updated, err := e.loadProjectBootstrapProgress(ctx, projectID)
		if err != nil {
			return progress, err
		}
		progress = updated
	}

	queuedAny := false
	for _, task := range progress.FirstWaveTasks {
		if !strings.EqualFold(strings.TrimSpace(task.WorkStatus), "draft") {
			continue
		}
		if _, err := e.taskTransitions.TransitionStatus(ctx, task.ID, "queued", tasksvc.Actor{Type: "system", AllowGateBypass: true}); err != nil {
			if isAlreadyQueuedTaskTransition(err) {
				queuedAny = true
				continue
			}
			if errors.Is(err, taskdecomp.ErrBoundedTaskTooLarge) {
				progress.ValidationStatus = projectBootstrapValidationFailed
				progress.ValidationFailureClass = projectBootstrapFailureFirstWaveExecution
				progress.ValidationFailureReason = err.Error()
				return progress, nil
			}
			return progress, err
		}
		queuedAny = true
	}
	if !queuedAny && progress.FirstWavePromotedCount == 0 && progress.FirstWaveExecutionCount == 0 && progress.FirstWaveJobCount == 0 {
		progress.ValidationStatus = projectBootstrapValidationFailed
		progress.ValidationFailureClass = projectBootstrapFailureFirstWaveExecution
		progress.ValidationFailureReason = buildProjectBootstrapFirstWaveExecutionFailureReason(progress)
		return progress, nil
	}

	updated, err := e.loadProjectBootstrapProgress(ctx, projectID)
	if err != nil {
		return progress, err
	}
	if updated.FirstWavePromotedCount == 0 && updated.FirstWaveExecutionCount == 0 && updated.FirstWaveJobCount == 0 {
		updated.ValidationStatus = projectBootstrapValidationFailed
		updated.ValidationFailureClass = projectBootstrapFailureFirstWaveExecution
		updated.ValidationFailureReason = buildProjectBootstrapFirstWaveExecutionFailureReason(updated)
		return updated, nil
	}
	if !updated.BootstrapGateReady() {
		return updated, nil
	}

	deadline := e.now().Add(defaultProjectBootstrapPromotionTimeout)
	for {
		updated, err = e.loadProjectBootstrapProgress(ctx, projectID)
		if err != nil {
			return progress, err
		}
		if updated.FirstWaveMaterialized() || updated.ValidationFailed() {
			return updated, nil
		}
		if e.now().After(deadline) {
			updated.ValidationStatus = projectBootstrapValidationFailed
			updated.ValidationFailureClass = projectBootstrapFailureFirstWaveExecution
			updated.ValidationFailureReason = buildProjectBootstrapFirstWaveExecutionFailureReason(updated)
			return updated, nil
		}
		if ctx.Err() != nil {
			return progress, ctx.Err()
		}
		if err := e.sleep(ctx, defaultProjectBootstrapPromotionPollDelay); err != nil {
			return progress, err
		}
	}
}

func isAlreadyQueuedTaskTransition(err error) bool {
	var transitionErr tasksvc.ErrInvalidStatusTransition
	if !errors.As(err, &transitionErr) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(transitionErr.From), "queued") &&
		strings.EqualFold(strings.TrimSpace(transitionErr.To), "queued")
}

func (e *TurnEngine) completeProjectBootstrapGateTask(ctx context.Context, taskID uuid.UUID) error {
	if e == nil || e.tasks == nil || taskID == uuid.Nil {
		return nil
	}

	taskRecord, err := e.tasks.GetByID(ctx, taskID)
	if err != nil {
		return err
	}
	if projectBootstrapTaskStatusTerminal(taskRecord.WorkStatus) {
		return nil
	}

	projectTasks, err := repo.NewProjectTaskRepo(e.pool).ListByProject(ctx, taskRecord.ProjectID)
	if err != nil {
		return err
	}
	childTasks := make([]repo.ProjectTask, 0)
	verifiedAt := e.now().UTC()
	verifications := make([]taskorchestration.ChildVerification, 0)
	childLabels := make([]string, 0)
	for _, candidate := range projectTasks {
		metadata := messageMetadataMap(candidate.Metadata)
		if setupTask, _ := metadata["bootstrap_setup_task"].(bool); !setupTask {
			continue
		}
		if parentID, ok := parseUUIDAny(metadata["decomposition_parent_task_id"]); !ok || parentID != taskID {
			continue
		}
		childTasks = append(childTasks, candidate)
		if !strings.EqualFold(strings.TrimSpace(candidate.WorkStatus), "done") {
			return nil
		}
		verifications = append(verifications, taskorchestration.NewChildVerification(candidate.ID, "Verified bootstrap setup output for "+strings.TrimSpace(candidate.Title)+".", verifiedAt))
		childLabels = append(childLabels, strings.TrimSpace(candidate.Title))
	}
	if len(childTasks) == 0 {
		return nil
	}

	integrationSummary := "Validated the bootstrap setup outputs together and confirmed the first-wave execution plan is coherent."
	if len(childLabels) > 0 {
		integrationSummary = fmt.Sprintf("Validated the bootstrap setup outputs together across %s and confirmed the first-wave execution plan is coherent.", strings.Join(childLabels, ", "))
	}
	taskRecord.Metadata, err = taskorchestration.Apply(taskRecord.Metadata, taskorchestration.Update{
		ChildVerifications: verifications,
		IntegrationCheck:   taskorchestration.NewIntegrationCheck("passed", integrationSummary, verifiedAt),
		OutcomeAssessment:  taskorchestration.NewOutcomeAssessment(true, "The bootstrap task tree is complete and Frank sign-off is recorded.", verifiedAt),
	})
	if err != nil {
		return err
	}

	if _, err := updateTurnTaskMetadata(ctx, e.tasks, taskRecord); err != nil {
		return err
	}
	if e.taskTransitions == nil {
		return fmt.Errorf("turn engine requires task transition service to auto-complete bootstrap gate tasks")
	}
	_, err = e.taskTransitions.TransitionStatusWithPayload(ctx, taskRecord.ID, "done", tasksvc.Actor{
		Type:                           "system",
		AllowBootstrapGateAutoComplete: true,
	}, map[string]any{
		"bootstrap_gate_auto_complete": true,
	})
	return err
}

func (e *TurnEngine) refreshProjectBootstrapSessionState(ctx context.Context, session *chat.ChatSession, progress projectBootstrapProgress) error {
	if e == nil || session == nil {
		return nil
	}
	normalizeProjectBootstrapValidationFailure(&progress, false)

	state := projectBootstrapStateFromMetadata(session.Metadata)
	now := e.now().UTC()
	state.Status = projectBootstrapStatusActive
	state.BootstrapTaskID = progress.BootstrapTaskID.String()
	state.BootstrapTaskOutstanding = progress.BootstrapTaskOutstanding
	applyProjectBootstrapProgressState(&state, progress)
	state.UpdatedAt = &now
	state.CompletedAt = nil
	state.FailedAt = nil
	state.FailureCategory = ""
	state.FailureClass = ""
	state.FailurePhase = ""
	state.FailureReason = ""
	state.ProviderFailureClass = ""
	state.ProviderFailureReason = ""

	if progress.Materialized() {
		state.Status = projectBootstrapStatusCompleted
		state.AutoTurnCount = 0
		state.CompletedAt = &now
		return e.updateProjectBootstrapState(ctx, session, state)
	}
	if progress.ValidationFailed() {
		// Task-status events can fire while the current bootstrap turn is still
		// dispatching tool calls. Do not fail-close the project/session from this
		// async observer path until the active turn has unwound.
		if session.CurrentTurnID != nil && *session.CurrentTurnID != uuid.Nil {
			return e.updateProjectBootstrapState(ctx, session, state)
		}
		if e != nil && e.pool != nil {
			if queued, err := e.hasQueuedAgentTurnForSession(ctx, session.ID, nil); err != nil {
				return err
			} else if queued && projectBootstrapRecoverableMaxToolCallFailure(progress) {
				state.ValidationStatus = ""
				state.ValidationFailureClass = ""
				state.ValidationFailureReason = ""
				return e.updateProjectBootstrapState(ctx, session, state)
			}
		}
		if handled, err := e.continueRecoverableProjectBootstrapValidationForSession(ctx, session, state, progress, now); err != nil {
			return err
		} else if handled {
			return nil
		}
		record := buildProjectBootstrapAutomaticFailureRecord(
			progress,
			projectFailureCategoryBootstrap,
			progress.ValidationFailureClass,
			progress.ValidationFailureReason,
			now,
		)
		state.Status = projectBootstrapStatusFailed
		state.FailedAt = &now
		state.FailureCategory = record.FailureCategory
		state.FailureClass = record.FailureClass
		state.FailurePhase = record.FailurePhase
		state.FailureReason = record.FailureReason
		if err := e.updateProjectBootstrapState(ctx, session, state); err != nil {
			return err
		}
		return e.applyProjectAutomaticFailure(ctx, session.ScopeID, record)
	}
	return e.updateProjectBootstrapState(ctx, session, state)
}

type projectBootstrapProgress struct {
	BootstrapTaskID          uuid.UUID
	BootstrapTaskOutstanding bool
	BootstrapSetupTaskCount  int
	BootstrapSetupDoneCount  int
	FrankSignOffRecorded     bool
	AssignmentCount          int
	StaffingDraftCount       int
	PlannedTaskCount         int
	PlannedFlowTemplateCount int
	FirstWaveTaskCount       int
	FirstWavePromotedCount   int
	FirstWaveExecutionCount  int
	FirstWaveJobCount        int
	ValidationStatus         string
	ValidationFailureClass   string
	ValidationFailureReason  string
	FirstWaveTasks           []repo.ProjectTask
}

func (p projectBootstrapProgress) Materialized() bool {
	return p.FirstWaveMaterialized() &&
		(p.BootstrapTaskID == uuid.Nil || !p.BootstrapTaskOutstanding)
}

func (p projectBootstrapProgress) FirstWaveMaterialized() bool {
	return p.ValidationStatus == projectBootstrapValidationPassed &&
		p.AssignmentCount > 0 &&
		p.PlannedTaskCount > 0 &&
		p.PlannedFlowTemplateCount > 0 &&
		p.FirstWaveTaskCount > 0 &&
		p.FirstWavePromotedCount >= p.FirstWaveTaskCount &&
		p.FirstWaveExecutionCount >= p.FirstWaveTaskCount &&
		p.FirstWaveJobCount >= p.FirstWaveTaskCount
}

func (p projectBootstrapProgress) ValidationFailed() bool {
	return p.ValidationStatus == projectBootstrapValidationFailed
}

func (p projectBootstrapProgress) BootstrapSetupComplete() bool {
	return p.BootstrapSetupTaskCount > 0 && p.BootstrapSetupDoneCount == p.BootstrapSetupTaskCount
}

func (p projectBootstrapProgress) BootstrapGateReady() bool {
	if p.BootstrapTaskID == uuid.Nil || !p.BootstrapTaskOutstanding {
		return true
	}
	return p.ValidationStatus == projectBootstrapValidationPassed &&
		p.BootstrapSetupComplete() &&
		p.FrankSignOffRecorded
}

func (p projectBootstrapProgress) WaitingForBootstrapGate() bool {
	return p.ValidationStatus == projectBootstrapValidationPassed &&
		p.BootstrapTaskID != uuid.Nil &&
		p.BootstrapTaskOutstanding &&
		!p.BootstrapGateReady()
}

func canonicalProjectBootstrapSetupTasks(tasks []repo.ProjectTask) ([]repo.ProjectTask, map[uuid.UUID]repo.ProjectTask) {
	if len(tasks) == 0 {
		return nil, map[uuid.UUID]repo.ProjectTask{}
	}
	bySlug := make(map[string]repo.ProjectTask)
	anonymous := make([]repo.ProjectTask, 0)
	for _, task := range tasks {
		metadata := messageMetadataMap(task.Metadata)
		slug := strings.TrimSpace(stringValue(metadata["bootstrap_step_slug"]))
		if slug == "" {
			anonymous = append(anonymous, task)
			continue
		}
		existing, ok := bySlug[slug]
		if !ok || shouldPreferCanonicalBootstrapScaffoldTask(existing, task) {
			bySlug[slug] = task
		}
	}
	canonical := make([]repo.ProjectTask, 0, len(bySlug)+len(anonymous))
	for _, task := range bySlug {
		canonical = append(canonical, task)
	}
	canonical = append(canonical, anonymous...)
	sort.Slice(canonical, func(i, j int) bool {
		return bootstrapScaffoldTaskLess(canonical[i], canonical[j])
	})
	byID := make(map[uuid.UUID]repo.ProjectTask, len(canonical))
	for _, task := range canonical {
		if task.ID != uuid.Nil {
			byID[task.ID] = task
		}
	}
	return canonical, byID
}

func canonicalProjectBootstrapGateTask(gates, setupTasks []repo.ProjectTask) (repo.ProjectTask, bool) {
	if len(gates) == 0 {
		return repo.ProjectTask{}, false
	}
	earliestCompletedSetup := 0
	for _, task := range setupTasks {
		if !strings.EqualFold(strings.TrimSpace(task.WorkStatus), "done") || task.TaskNumber <= 0 {
			continue
		}
		if earliestCompletedSetup == 0 || task.TaskNumber < earliestCompletedSetup {
			earliestCompletedSetup = task.TaskNumber
		}
	}
	if earliestCompletedSetup > 0 {
		var chosen repo.ProjectTask
		found := false
		for _, gate := range gates {
			if gate.TaskNumber <= 0 || gate.TaskNumber >= earliestCompletedSetup {
				continue
			}
			if !found || gate.TaskNumber > chosen.TaskNumber {
				chosen = gate
				found = true
			}
		}
		if found {
			return chosen, true
		}
	}
	chosen := gates[0]
	for _, gate := range gates[1:] {
		if shouldPreferCanonicalBootstrapScaffoldTask(chosen, gate) {
			chosen = gate
		}
	}
	return chosen, true
}

func shouldPreferCanonicalBootstrapScaffoldTask(existing, candidate repo.ProjectTask) bool {
	existingDone := strings.EqualFold(strings.TrimSpace(existing.WorkStatus), "done")
	candidateDone := strings.EqualFold(strings.TrimSpace(candidate.WorkStatus), "done")
	if existingDone != candidateDone {
		return candidateDone
	}
	return bootstrapScaffoldTaskLess(candidate, existing)
}

func bootstrapScaffoldTaskLess(left, right repo.ProjectTask) bool {
	switch {
	case left.TaskNumber > 0 && right.TaskNumber > 0:
		if left.TaskNumber != right.TaskNumber {
			return left.TaskNumber < right.TaskNumber
		}
	case left.TaskNumber > 0:
		return true
	case right.TaskNumber > 0:
		return false
	}
	if left.ID != uuid.Nil && right.ID != uuid.Nil {
		return strings.Compare(left.ID.String(), right.ID.String()) < 0
	}
	return left.ID != uuid.Nil
}

func projectBootstrapProgressAdvancedBeyondState(state projectBootstrapState, progress projectBootstrapProgress) bool {
	return progress.AssignmentCount > state.AssignmentCount ||
		progress.StaffingDraftCount > state.StaffingDraftCount ||
		progress.PlannedTaskCount > state.PlannedTaskCount ||
		progress.PlannedFlowTemplateCount > state.PlannedFlowTemplateCount ||
		progress.FirstWaveTaskCount > state.FirstWaveTaskCount ||
		progress.FirstWavePromotedCount > state.FirstWavePromotedCount ||
		progress.FirstWaveExecutionCount > state.FirstWaveExecutionCount ||
		progress.FirstWaveJobCount > state.FirstWaveJobCount
}

func projectBootstrapRestartScaffoldOnly(progress projectBootstrapProgress) bool {
	return progress.BootstrapTaskID != uuid.Nil &&
		progress.BootstrapSetupTaskCount > 0 &&
		progress.AssignmentCount == 0 &&
		progress.PlannedTaskCount == 0 &&
		progress.FirstWaveTaskCount == 0 &&
		progress.FirstWavePromotedCount == 0 &&
		progress.FirstWaveExecutionCount == 0 &&
		progress.FirstWaveJobCount == 0
}

func buildProjectBootstrapRestartScaffoldFailureReason() string {
	return "kickoff validation failed: automatic bootstrap restart recreated only the canonical bootstrap scaffold and never persisted staffed executable work, so the restart was archived instead of remaining active"
}

func buildProjectBootstrapScaffoldOnlyFailureReason() string {
	return "kickoff validation failed: bootstrap setup persisted staffing but did not yet materialize any executable non-bootstrap project tasks"
}

func projectBootstrapRestartScaffoldFailureReason(reason string) bool {
	normalized := strings.ToLower(strings.TrimSpace(reason))
	return strings.Contains(normalized, "automatic bootstrap restart recreated only the canonical bootstrap scaffold") ||
		strings.Contains(normalized, "did not yet materialize any executable non-bootstrap project tasks") ||
		strings.Contains(normalized, "did not emit any executable non-bootstrap project tasks for the first wave")
}

func projectBootstrapLastCheckpoint(progress projectBootstrapProgress) string {
	checkpoint := projectBootstrapCheckpointProjectCreated
	if progress.AssignmentCount > 0 {
		checkpoint = projectBootstrapCheckpointStaffing
	}
	if progress.PlannedTaskCount > 0 {
		checkpoint = projectBootstrapCheckpointTaskTree
	}
	if progress.PlannedFlowTemplateCount > 0 {
		checkpoint = projectBootstrapCheckpointFlowTemplates
	}
	if progress.FirstWaveTaskCount > 0 {
		checkpoint = projectBootstrapCheckpointFirstWave
	}
	if progress.FirstWaveExecutionCount > 0 {
		checkpoint = projectBootstrapCheckpointExecutions
	}
	if progress.FirstWaveJobCount > 0 {
		checkpoint = projectBootstrapCheckpointJobsClaimed
	}
	return checkpoint
}

func projectBootstrapReachedFirstWaveClaim(progress projectBootstrapProgress) bool {
	return projectBootstrapLastCheckpoint(progress) == projectBootstrapCheckpointJobsClaimed
}

type projectBootstrapState struct {
	Status                   string                              `json:"status,omitempty"`
	CurrentPhase             string                              `json:"current_phase,omitempty"`
	LastSuccessfulCheckpoint string                              `json:"last_successful_checkpoint,omitempty"`
	InitialMessageID         string                              `json:"initial_message_id,omitempty"`
	BootstrapTaskID          string                              `json:"bootstrap_task_id,omitempty"`
	BootstrapTaskOutstanding bool                                `json:"bootstrap_task_outstanding,omitempty"`
	LastTurnID               string                              `json:"last_turn_id,omitempty"`
	LastResponderID          string                              `json:"last_responder_id,omitempty"`
	AutoTurnCount            int                                 `json:"auto_turn_count,omitempty"`
	AssignmentCount          int                                 `json:"assignment_count,omitempty"`
	StaffingDraftCount       int                                 `json:"staffing_draft_count,omitempty"`
	PlannedTaskCount         int                                 `json:"planned_task_count,omitempty"`
	PlannedFlowTemplateCount int                                 `json:"planned_flow_template_count,omitempty"`
	FirstWaveTaskCount       int                                 `json:"first_wave_task_count,omitempty"`
	FirstWavePromotedCount   int                                 `json:"first_wave_promoted_count,omitempty"`
	FirstWaveExecutionCount  int                                 `json:"first_wave_execution_count,omitempty"`
	FirstWaveJobCount        int                                 `json:"first_wave_job_count,omitempty"`
	FirstWaveKickoffCount    int                                 `json:"first_wave_kickoff_count,omitempty"`
	ValidationStatus         string                              `json:"validation_status,omitempty"`
	ValidationFailureClass   string                              `json:"validation_failure_class,omitempty"`
	ValidationFailureReason  string                              `json:"validation_failure_reason,omitempty"`
	Checkpoints              []projectBootstrapCheckpoint        `json:"checkpoints,omitempty"`
	ValidationFindings       []projectBootstrapValidationFinding `json:"validation_findings,omitempty"`
	FailureCategory          string                              `json:"failure_category,omitempty"`
	FailureClass             string                              `json:"failure_class,omitempty"`
	FailurePhase             string                              `json:"failure_phase,omitempty"`
	FailureReason            string                              `json:"failure_reason,omitempty"`
	ProviderFailureClass     string                              `json:"provider_failure_class,omitempty"`
	ProviderFailureReason    string                              `json:"provider_failure_reason,omitempty"`
	Phase                    string                              `json:"phase,omitempty"`
	LastCheckpoint           string                              `json:"last_checkpoint,omitempty"`
	StartedAt                *time.Time                          `json:"started_at,omitempty"`
	LastProgressAt           *time.Time                          `json:"last_progress_at,omitempty"`
	UpdatedAt                *time.Time                          `json:"updated_at,omitempty"`
	CompletedAt              *time.Time                          `json:"completed_at,omitempty"`
	FailedAt                 *time.Time                          `json:"failed_at,omitempty"`
}

type projectAutomaticFailureRecord struct {
	Action                   string
	Source                   string
	FailureCategory          string
	FailureClass             string
	FailurePhase             string
	LastCheckpoint           string
	LastSuccessfulCheckpoint string
	FailureReason            string
	SetupPersisted           bool
	RecordedAt               time.Time
}

type projectBootstrapWatchdog struct {
	Timeout   time.Duration
	Remaining time.Duration
}

type projectBootstrapWatchdogStream struct {
	ctx    context.Context
	reset  func()
	stop   func()
	cause  func() error
	active bool
}

func normalizeProjectBootstrapStateCounts(state projectBootstrapState) projectBootstrapState {
	switch {
	case state.FirstWaveJobCount == 0 && state.FirstWaveKickoffCount > 0:
		state.FirstWaveJobCount = state.FirstWaveKickoffCount
	case state.FirstWaveKickoffCount == 0 && state.FirstWaveJobCount > 0:
		state.FirstWaveKickoffCount = state.FirstWaveJobCount
	}
	return state
}

func (s *projectBootstrapState) setFirstWaveJobCount(count int) {
	if s == nil {
		return
	}
	s.FirstWaveJobCount = count
	s.FirstWaveKickoffCount = count
}

type projectBootstrapTimeoutError struct {
	InvocationID uuid.UUID
	Timeout      time.Duration
	Progress     projectBootstrapProgress
}

func (e *projectBootstrapTimeoutError) Error() string {
	if e == nil {
		return errProjectBootstrapWatchdog.Error()
	}
	if e.Timeout <= 0 {
		return "project bootstrap watchdog timed out while waiting for setup materialization"
	}
	return fmt.Sprintf("project bootstrap watchdog timed out after %s while waiting for setup materialization", e.Timeout.String())
}

func (e *projectBootstrapTimeoutError) Is(target error) bool {
	return target == errProjectBootstrapWatchdog
}

type asyncTurnTimeoutError struct {
	InvocationID uuid.UUID
	Timeout      time.Duration
}

func (e *asyncTurnTimeoutError) Error() string {
	if e == nil || e.Timeout <= 0 {
		return "turn duration limit reached while waiting for model response"
	}
	return fmt.Sprintf("turn duration limit reached after %s while waiting for model response", e.Timeout.String())
}

func (e *asyncTurnTimeoutError) Is(target error) bool {
	return target == errAsyncTurnWatchdog
}

func (e *TurnEngine) loadProjectBootstrapProgress(ctx context.Context, projectID uuid.UUID) (projectBootstrapProgress, error) {
	progress := projectBootstrapProgress{ValidationStatus: projectBootstrapValidationPending}
	if e == nil || e.pool == nil || projectID == uuid.Nil {
		return progress, nil
	}

	assignments, err := repo.NewAgentProjectAssignmentRepo(e.pool).ListByProject(ctx, projectID)
	if err != nil {
		return projectBootstrapProgress{}, err
	}
	progress.AssignmentCount = e.countBootstrapMaterializedAssignments(ctx, assignments)
	progress.StaffingDraftCount, err = e.countProjectBootstrapStaffingDrafts(ctx, projectID)
	if err != nil {
		return projectBootstrapProgress{}, err
	}

	tasks, err := repo.NewProjectTaskRepo(e.pool).ListByProject(ctx, projectID)
	if err != nil {
		return projectBootstrapProgress{}, err
	}

	plannedTasks := make([]repo.ProjectTask, 0, len(tasks))
	setupTaskCandidates := make([]repo.ProjectTask, 0)
	gateCandidates := make([]repo.ProjectTask, 0)
	bootstrapSetupTaskByID := make(map[uuid.UUID]repo.ProjectTask)
	childCounts := make(map[uuid.UUID]int)
	for _, task := range tasks {
		metadata := messageMetadataMap(task.Metadata)
		if bootstrapGate, _ := metadata["bootstrap_gate"].(bool); bootstrapGate {
			gateCandidates = append(gateCandidates, task)
			continue
		}
		if bootstrapSetupTask, _ := metadata["bootstrap_setup_task"].(bool); bootstrapSetupTask {
			setupTaskCandidates = append(setupTaskCandidates, task)
			continue
		}
		plannedTasks = append(plannedTasks, task)
		if parentID, ok := parseUUIDAny(metadata["decomposition_parent_task_id"]); ok && parentID != uuid.Nil {
			childCounts[parentID]++
		}
	}
	bootstrapSetupTasks, canonicalSetupTaskByID := canonicalProjectBootstrapSetupTasks(setupTaskCandidates)
	bootstrapSetupTaskByID = canonicalSetupTaskByID
	progress.BootstrapSetupTaskCount = len(bootstrapSetupTasks)
	for _, task := range bootstrapSetupTasks {
		metadata := messageMetadataMap(task.Metadata)
		if strings.EqualFold(strings.TrimSpace(task.WorkStatus), "done") {
			progress.BootstrapSetupDoneCount++
			if strings.EqualFold(strings.TrimSpace(stringValue(metadata["bootstrap_step_slug"])), bootstrapFrankSignOffStepSlug) {
				progress.FrankSignOffRecorded = true
			}
		}
	}
	if bootstrapTask, ok := canonicalProjectBootstrapGateTask(gateCandidates, bootstrapSetupTasks); ok {
		progress.BootstrapTaskID = bootstrapTask.ID
		progress.BootstrapTaskOutstanding = !projectBootstrapTaskStatusTerminal(bootstrapTask.WorkStatus)
	}

	progress.PlannedTaskCount = len(plannedTasks)
	if len(plannedTasks) == 0 {
		progress.PlannedFlowTemplateCount, err = e.countProjectBootstrapCurrentFlowTemplates(ctx, projectID)
		if err != nil {
			return projectBootstrapProgress{}, err
		}
		if progress.AssignmentCount > 0 || progress.PlannedFlowTemplateCount > 0 {
			progress.ValidationStatus = projectBootstrapValidationFailed
			progress.ValidationFailureClass = projectBootstrapFailureRuntime
			progress.ValidationFailureReason = buildProjectBootstrapScaffoldOnlyFailureReason()
		}
		return progress, nil
	}
	for _, task := range bootstrapSetupTasks {
		metadata := messageMetadataMap(task.Metadata)
		if persisted, _ := metadata["bootstrap_setup_persisted"].(bool); persisted &&
			strings.EqualFold(strings.TrimSpace(task.WorkStatus), "done") {
			continue
		}
		prepared, decompErr := taskdecomp.PrepareQueueDecomposition(taskdecomp.QueueDecompositionInput{
			ParentTaskID: task.ID,
			Title:        task.Title,
			Description:  task.Description,
			Metadata:     task.Metadata,
		})
		if decompErr != nil && errors.Is(decompErr, taskdecomp.ErrBoundedTaskTooLarge) {
			progress.ValidationStatus = projectBootstrapValidationFailed
			progress.ValidationFailureClass = projectBootstrapFailureSetupTaskScope
			progress.ValidationFailureReason = buildProjectBootstrapSetupTaskSizeFailureReason(task, decompErr.Error())
			return progress, nil
		}
		if decompErr != nil {
			return projectBootstrapProgress{}, decompErr
		}
		if prepared.Applied {
			progress.ValidationStatus = projectBootstrapValidationFailed
			progress.ValidationFailureClass = projectBootstrapFailureSetupTaskScope
			progress.ValidationFailureReason = buildProjectBootstrapSetupTaskSizeFailureReason(task, "split the setup/orchestration outcome into a bounded checklist and delegate deliverable work into normal project tasks")
			return progress, nil
		}
	}

	repoBindingKnown := e.environments != nil
	repoBindingPresent := false
	if repoBindingKnown {
		environments, err := e.environments.ListByProject(ctx, projectID)
		if err != nil {
			return projectBootstrapProgress{}, err
		}
		repoBindingPresent = projectsvc.HasProjectRepoBinding(environments)
	}
	blockedTaskIDs, err := e.loadProjectBootstrapBlockedTaskIDs(ctx, projectID)
	if err != nil {
		return projectBootstrapProgress{}, err
	}

	firstWaveTasks := make([]repo.ProjectTask, 0, len(plannedTasks))
	assignedFirstWaveTasks := make([]repo.ProjectTask, 0, len(plannedTasks))
	firstWaveTemplateIDs := make(map[uuid.UUID]struct{})
	structuralFailureClass := ""
	structuralFailureReason := ""
	unassignedLeafFailureClass := ""
	unassignedLeafFailureReason := ""
	for _, task := range plannedTasks {
		if parentID := taskdecomp.ParseParentTaskID(task.Metadata); parentID != uuid.Nil {
			if setupTask, ok := bootstrapSetupTaskByID[parentID]; ok {
				progress.ValidationStatus = projectBootstrapValidationFailed
				progress.ValidationFailureClass = projectBootstrapFailureSetupTaskChildren
				progress.ValidationFailureReason = buildProjectBootstrapSetupTaskChildrenFailureReason(setupTask, task)
				return progress, nil
			}
		}
		childCount := childCounts[task.ID]
		prepared, decompErr := taskdecomp.PrepareQueueDecomposition(taskdecomp.QueueDecompositionInput{
			ParentTaskID: task.ID,
			Title:        task.Title,
			Description:  task.Description,
			Metadata:     task.Metadata,
		})
		if decompErr != nil && errors.Is(decompErr, taskdecomp.ErrBoundedTaskTooLarge) {
			if childCount == 0 {
				if task.AssignedAgentID != nil && *task.AssignedAgentID != uuid.Nil {
					progress.ValidationStatus = projectBootstrapValidationFailed
					progress.ValidationFailureClass = projectBootstrapFailureFirstWaveSize
					progress.ValidationFailureReason = buildProjectBootstrapFirstWaveSizeFailureReason(task, decompErr.Error())
					return progress, nil
				}
				if unassignedLeafFailureClass == "" {
					unassignedLeafFailureClass = projectBootstrapFailureFirstWaveSize
					unassignedLeafFailureReason = buildProjectBootstrapFirstWaveSizeFailureReason(task, decompErr.Error())
				}
				continue
			}
			if projectBootstrapTaskEnteredExecution(task.WorkStatus) && structuralFailureClass == "" {
				structuralFailureClass = projectBootstrapFailureCompoundParent
				structuralFailureReason = buildProjectBootstrapParentExecutionFailureReason(task, childCount)
			}
			continue
		}
		if decompErr != nil {
			return projectBootstrapProgress{}, decompErr
		}
		if prepared.Applied && childCount == 0 {
			if task.AssignedAgentID != nil && *task.AssignedAgentID != uuid.Nil {
				progress.ValidationStatus = projectBootstrapValidationFailed
				progress.ValidationFailureClass = projectBootstrapFailureCompoundParent
				progress.ValidationFailureReason = buildProjectBootstrapCompoundParentFailureReason(task)
				return progress, nil
			}
			if unassignedLeafFailureClass == "" {
				unassignedLeafFailureClass = projectBootstrapFailureCompoundParent
				unassignedLeafFailureReason = buildProjectBootstrapCompoundParentFailureReason(task)
			}
			continue
		}
		if childCount > 0 && projectBootstrapTaskEnteredExecution(task.WorkStatus) && structuralFailureClass == "" {
			structuralFailureClass = projectBootstrapFailureCompoundParent
			structuralFailureReason = buildProjectBootstrapParentExecutionFailureReason(task, childCount)
		}
		if childCount > 0 {
			continue
		}
		if _, blocked := blockedTaskIDs[task.ID]; blocked {
			continue
		}
		firstWaveTasks = append(firstWaveTasks, task)
		if task.AssignedAgentID != nil && *task.AssignedAgentID != uuid.Nil {
			assignedFirstWaveTasks = append(assignedFirstWaveTasks, task)
		}
		if projectBootstrapTaskEnteredExecution(task.WorkStatus) {
			progress.FirstWavePromotedCount++
		}
		if task.FlowTemplateID != nil && *task.FlowTemplateID != uuid.Nil {
			firstWaveTemplateIDs[*task.FlowTemplateID] = struct{}{}
		}
	}
	if len(firstWaveTasks) > 0 && len(assignedFirstWaveTasks) == 0 {
		progress.ValidationStatus = projectBootstrapValidationFailed
		progress.ValidationFailureClass = projectBootstrapFailureFirstWaveExecution
		progress.ValidationFailureReason = buildProjectBootstrapFirstWaveAssignmentFailureReason(firstWaveTasks[0])
		return progress, nil
	}
	if len(assignedFirstWaveTasks) > 0 {
		firstWaveTasks = assignedFirstWaveTasks
	} else if unassignedLeafFailureClass != "" {
		progress.ValidationStatus = projectBootstrapValidationFailed
		progress.ValidationFailureClass = unassignedLeafFailureClass
		progress.ValidationFailureReason = unassignedLeafFailureReason
		return progress, nil
	}
	progress.FirstWaveTaskCount = len(firstWaveTasks)
	if len(firstWaveTemplateIDs) > progress.PlannedFlowTemplateCount {
		progress.PlannedFlowTemplateCount = len(firstWaveTemplateIDs)
	}
	progress.FirstWaveTasks = append(progress.FirstWaveTasks[:0], firstWaveTasks...)

	switch {
	case progress.AssignmentCount == 0:
		progress.ValidationStatus = projectBootstrapValidationFailed
		progress.ValidationFailureClass = projectBootstrapFailureMissingAssignments
		progress.ValidationFailureReason = "kickoff validation failed: planned tasks were created before any active project assignments were persisted"
		return progress, nil
	case e.pool != nil:
		pmAssignment, pmErr := repo.NewAgentProjectAssignmentRepo(e.pool).GetPM(ctx, projectID)
		if errors.Is(pmErr, repo.ErrNotFound) || pmAssignment.AgentID == uuid.Nil || !pmAssignment.IsActive {
			progress.ValidationStatus = projectBootstrapValidationFailed
			progress.ValidationFailureClass = projectBootstrapFailureMissingPM
			progress.ValidationFailureReason = "kickoff validation failed: staffed project persisted work but did not assign an active project manager"
			return progress, nil
		}
		if pmErr != nil {
			return projectBootstrapProgress{}, pmErr
		}
		if !e.bootstrapHasMaterializedProjectRole(ctx, assignments, "reviewer") {
			progress.ValidationStatus = projectBootstrapValidationFailed
			progress.ValidationFailureClass = projectBootstrapFailureMissingReviewer
			progress.ValidationFailureReason = "kickoff validation failed: staffed project persisted executable work but did not assign an active reviewer"
			return progress, nil
		}
	case !repoBindingKnown:
		return progress, nil
	case !repoBindingPresent:
		progress.ValidationStatus = projectBootstrapValidationFailed
		progress.ValidationFailureClass = projectBootstrapFailureRepoBinding
		progress.ValidationFailureReason = buildProjectBootstrapRepoBindingFailureReason()
		return progress, nil
	case structuralFailureClass != "":
		progress.ValidationStatus = projectBootstrapValidationFailed
		progress.ValidationFailureClass = structuralFailureClass
		progress.ValidationFailureReason = structuralFailureReason
		return progress, nil
	}

	if len(firstWaveTasks) == 0 {
		progress.ValidationStatus = projectBootstrapValidationFailed
		progress.ValidationFailureClass = projectBootstrapFailureCompoundParent
		progress.ValidationFailureReason = "kickoff validation failed: no bounded first-wave tasks remain after excluding orchestration-only parent workstreams"
		return progress, nil
	}

	failureClass, failureReason, err := e.validateProjectBootstrapFirstWaveTasks(ctx, firstWaveTasks)
	if err != nil {
		return projectBootstrapProgress{}, err
	}
	if failureClass != "" {
		progress.ValidationStatus = projectBootstrapValidationFailed
		progress.ValidationFailureClass = failureClass
		progress.ValidationFailureReason = failureReason
		return progress, nil
	}

	firstWaveTaskIDs := make([]uuid.UUID, 0, len(firstWaveTasks))
	for _, task := range firstWaveTasks {
		if task.ID != uuid.Nil {
			firstWaveTaskIDs = append(firstWaveTaskIDs, task.ID)
		}
	}
	progress.FirstWaveExecutionCount, err = e.countProjectBootstrapFirstWaveExecutions(ctx, firstWaveTaskIDs)
	if err != nil {
		return projectBootstrapProgress{}, err
	}
	progress.FirstWaveJobCount, err = e.countProjectBootstrapFirstWaveJobs(ctx, firstWaveTaskIDs)
	if err != nil {
		return projectBootstrapProgress{}, err
	}
	progress.ValidationStatus = projectBootstrapValidationPassed
	return progress, nil
}

func (e *TurnEngine) loadProjectBootstrapBlockedTaskIDs(ctx context.Context, projectID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	blocked := make(map[uuid.UUID]struct{})
	if e == nil || e.pool == nil || projectID == uuid.Nil {
		return blocked, nil
	}
	rows, err := e.pool.Query(ctx, `
		SELECT DISTINCT d.source_id
		FROM project_task_dependency d
		JOIN project_task source_task ON source_task.id = d.source_id
		JOIN project_task depends_on_task ON depends_on_task.id = d.depends_on_id
		WHERE source_task.project_id = $1
		  AND d.source_type = 'project_task'
		  AND d.depends_on_type = 'project_task'
		  AND lower(trim(depends_on_task.work_status)) NOT IN ('done', 'cancelled')
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID uuid.UUID
		if scanErr := rows.Scan(&taskID); scanErr != nil {
			return nil, scanErr
		}
		if taskID != uuid.Nil {
			blocked[taskID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return blocked, nil
}

func projectBootstrapTaskStatusTerminal(status string) bool {
	normalized := strings.ToLower(strings.TrimSpace(status))
	return normalized == "done" || normalized == "cancelled"
}

func projectBootstrapStateActive(state projectBootstrapState) bool {
	return strings.EqualFold(strings.TrimSpace(state.Status), projectBootstrapStatusActive)
}

func projectBootstrapCompletedTurnManaged(messages []repo.ChatMessage, latestUser *repo.ChatMessage, state projectBootstrapState, progress projectBootstrapProgress) bool {
	if progress.Materialized() || latestUser == nil || latestUser.ID == uuid.Nil {
		return false
	}
	if progress.BootstrapTaskOutstanding && progress.BootstrapSetupTaskCount > 0 {
		return true
	}
	if projectBootstrapStateActive(state) {
		return true
	}
	metadata := messageMetadataMap(latestUser.Metadata)
	if strings.EqualFold(strings.TrimSpace(stringValue(metadata["source"])), projectBootstrapSource) {
		return true
	}
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			continue
		}
		return message.ID == latestUser.ID
	}
	return false
}

func projectBootstrapRestartSession(session *chat.ChatSession) bool {
	if session == nil || len(session.Metadata) == 0 {
		return false
	}
	metadata := messageMetadataMap(session.Metadata)
	if raw, ok := metadata["bootstrap_restart"].(bool); ok {
		return raw
	}
	_, ok := metadata["bootstrap_restart"]
	return ok
}

func (e *TurnEngine) projectBootstrapRuntimeManaged(ctx context.Context, session *chat.ChatSession, initialMessageID uuid.UUID) bool {
	if session == nil || session.ID == uuid.Nil {
		return false
	}
	if projectBootstrapStateActive(projectBootstrapStateFromMetadata(session.Metadata)) {
		return true
	}
	if e == nil || e.messages == nil || initialMessageID == uuid.Nil {
		return false
	}

	message, err := e.messages.GetByID(ctx, initialMessageID)
	if err != nil {
		return false
	}
	workflowMessageID := projectBootstrapWorkflowMessageID(&message)
	if workflowMessageID == uuid.Nil {
		return false
	}

	messages, err := e.messages.ListBySession(ctx, session.ID)
	if err != nil {
		return false
	}
	for _, item := range messages {
		if !strings.EqualFold(strings.TrimSpace(item.Role), "user") {
			continue
		}
		return item.ID == workflowMessageID
	}
	return false
}

func projectBootstrapTaskEnteredExecution(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "in_progress", "review", "done":
		return true
	default:
		return false
	}
}

func (e *TurnEngine) validateProjectBootstrapFirstWaveTasks(ctx context.Context, tasks []repo.ProjectTask) (string, string, error) {
	if e == nil || e.pool == nil {
		return "", "", nil
	}

	templateRepo := repo.NewFlowTemplateRepo(e.pool)
	nodeRepo := repo.NewFlowNodeRepo(e.pool)
	templateCache := make(map[uuid.UUID]repo.FlowTemplate)
	nodeCache := make(map[uuid.UUID][]repo.FlowNode)

	for _, task := range tasks {
		if task.AssignedAgentID == nil || *task.AssignedAgentID == uuid.Nil {
			return projectBootstrapFailureFirstWaveExecution, buildProjectBootstrapFirstWaveAssignmentFailureReason(task), nil
		}
		if task.RequiresHumanReview {
			return projectBootstrapFailureFirstWaveExecution, buildProjectBootstrapFirstWaveApprovalFailureReason(task), nil
		}
		if task.FlowTemplateID == nil || *task.FlowTemplateID == uuid.Nil {
			return projectBootstrapFailureFirstWaveFlow, buildProjectBootstrapFirstWaveFlowFailureReason(task, "no flow template is attached"), nil
		}

		templateID := *task.FlowTemplateID
		template, ok := templateCache[templateID]
		if !ok {
			loaded, err := templateRepo.GetByID(ctx, templateID)
			if err != nil {
				if errors.Is(err, repo.ErrNotFound) {
					return projectBootstrapFailureFirstWaveFlow, buildProjectBootstrapFirstWaveFlowFailureReason(task, "the attached flow template no longer exists"), nil
				}
				return "", "", err
			}
			template = loaded
			templateCache[templateID] = template
		}

		nodes, ok := nodeCache[templateID]
		if !ok {
			loaded, err := nodeRepo.GetByTemplateOrdered(ctx, templateID)
			if err != nil {
				return "", "", err
			}
			nodes = loaded
			nodeCache[templateID] = nodes
		}

		if err := flowpolicy.ValidateExecutableFlowTemplate(template.StartNodeID, nodes); err != nil {
			return projectBootstrapFailureFirstWaveFlow, buildProjectBootstrapFirstWaveFlowFailureReason(task, err.Error()), nil
		}
	}

	return "", "", nil
}

func (e *TurnEngine) countProjectBootstrapFirstWaveExecutions(ctx context.Context, taskIDs []uuid.UUID) (int, error) {
	if e == nil || e.pool == nil || len(taskIDs) == 0 {
		return 0, nil
	}

	var count int
	if err := e.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT task_id)
		FROM flow_node_execution
		WHERE task_id = ANY($1::uuid[])
	`, taskIDs).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (e *TurnEngine) countProjectBootstrapCurrentFlowTemplates(ctx context.Context, projectID uuid.UUID) (int, error) {
	if e == nil || e.pool == nil || projectID == uuid.Nil {
		return 0, nil
	}

	var count int
	if err := e.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM flow_template
		WHERE project_id = $1
		  AND is_current = true
		  AND slug <> $2
	`, projectID, projectBootstrapTemplateSlug).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (e *TurnEngine) countBootstrapMaterializedAssignments(ctx context.Context, assignments []repo.AgentProjectAssignment) int {
	if len(assignments) == 0 {
		return 0
	}
	if e == nil || e.agents == nil {
		return len(assignments)
	}

	count := 0
	for _, assignment := range assignments {
		agentRecord, err := e.agents.GetByID(ctx, assignment.AgentID)
		if err != nil {
			count++
			continue
		}
		if agentRecord.IsStarterTrio {
			continue
		}
		count++
	}
	return count
}

func (e *TurnEngine) bootstrapHasMaterializedProjectRole(ctx context.Context, assignments []repo.AgentProjectAssignment, role string) bool {
	role = strings.TrimSpace(strings.ToLower(role))
	if role == "" {
		return false
	}
	for _, assignment := range assignments {
		if !assignment.IsActive || assignment.AgentID == uuid.Nil {
			continue
		}
		if strings.TrimSpace(strings.ToLower(assignment.Role)) != role {
			continue
		}
		if e == nil || e.agents == nil {
			return true
		}
		agentRecord, err := e.agents.GetByID(ctx, assignment.AgentID)
		if err != nil {
			return true
		}
		if agentRecord.IsStarterTrio {
			continue
		}
		return true
	}
	return false
}

func (e *TurnEngine) countProjectBootstrapFirstWaveJobs(ctx context.Context, taskIDs []uuid.UUID) (int, error) {
	if e == nil || e.pool == nil || len(taskIDs) == 0 {
		return 0, nil
	}

	var count int
	if err := e.pool.QueryRow(ctx, `
		WITH runnable_jobs AS (
			SELECT DISTINCT s.scope_id
			FROM job_queue jq
			JOIN chat_session s ON s.id::text = jq.payload->>'session_id'
			WHERE jq.job_type = 'agent_turn'
			  AND jq.status IN ('pending', 'claimed')
			  AND s.scope_type = 'project_task'
			  AND s.mode = 'async'
			  AND s.scope_id = ANY($1::uuid[])
		),
		started_sessions AS (
			SELECT DISTINCT scope_id
			FROM chat_session
			WHERE scope_type = 'project_task'
			  AND mode = 'async'
			  AND scope_id = ANY($1::uuid[])
			  AND (
				turn_count > 0 OR
				current_turn_id IS NOT NULL
			  )
		)
		SELECT COUNT(DISTINCT task_id)
		FROM (
			SELECT scope_id AS task_id FROM runnable_jobs
			UNION
			SELECT scope_id AS task_id FROM started_sessions
		) materialized
	`, taskIDs).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func buildProjectBootstrapCompoundParentFailureReason(task repo.ProjectTask) string {
	return fmt.Sprintf("kickoff validation failed: %s is still a broad parent workstream and must be split into bounded executable child tasks before bootstrap can complete", projectBootstrapTaskLabel(task))
}

func buildProjectBootstrapSetupTaskSizeFailureReason(task repo.ProjectTask, detail string) string {
	trimmed := strings.TrimSpace(detail)
	if trimmed == "" {
		trimmed = "split the setup/orchestration outcome into a smaller checklist and delegate production work into normal project tasks"
	}
	return fmt.Sprintf("kickoff validation failed: bootstrap setup %s violates the bounded task-size policy: %s", projectBootstrapTaskLabel(task), trimmed)
}

func buildProjectBootstrapSetupTaskChildrenFailureReason(setupTask, childTask repo.ProjectTask) string {
	return fmt.Sprintf("kickoff validation failed: bootstrap setup %s must stay orchestration-only, so executable %s cannot be hidden beneath it; delegate deliverable work into normal project tasks instead", projectBootstrapTaskLabel(setupTask), projectBootstrapTaskLabel(childTask))
}

func buildProjectBootstrapRepoBindingFailureReason() string {
	return "kickoff validation failed: planned tasks were created before the project repo/workspace binding was persisted, so no first-wave task has a repo-bound workspace to run in"
}

func buildProjectBootstrapParentExecutionFailureReason(task repo.ProjectTask, childCount int) string {
	return fmt.Sprintf("kickoff validation failed: %s entered execution even though %d executable child task(s) already exist, so the parent must remain orchestration-only", projectBootstrapTaskLabel(task), childCount)
}

func buildProjectBootstrapFirstWaveSizeFailureReason(task repo.ProjectTask, detail string) string {
	trimmed := strings.TrimSpace(detail)
	if trimmed == "" {
		trimmed = "split the work into smaller reviewable tasks before queueing"
	}
	return fmt.Sprintf("kickoff validation failed: first-wave %s violates the bounded task-size policy: %s", projectBootstrapTaskLabel(task), trimmed)
}

func buildProjectBootstrapFirstWaveFlowFailureReason(task repo.ProjectTask, detail string) string {
	trimmed := strings.TrimSpace(detail)
	if trimmed == "" {
		trimmed = "the attached flow template cannot run"
	}
	return fmt.Sprintf("kickoff validation failed: first-wave %s cannot run because %s", projectBootstrapTaskLabel(task), trimmed)
}

func buildProjectBootstrapFirstWaveAssignmentFailureReason(task repo.ProjectTask) string {
	return fmt.Sprintf("kickoff validation failed: first-wave %s has no assigned agent, so bootstrap cannot queue runnable execution", projectBootstrapTaskLabel(task))
}

func buildProjectBootstrapFirstWaveApprovalFailureReason(task repo.ProjectTask) string {
	return fmt.Sprintf("kickoff validation failed: first-wave %s requires human approval before queueing, so bootstrap cannot materialize autonomous runnable execution", projectBootstrapTaskLabel(task))
}

func buildProjectBootstrapFirstWaveExecutionFailureReason(progress projectBootstrapProgress) string {
	switch {
	case progress.FirstWavePromotedCount == 0:
		return "kickoff validation failed: persisted setup created assignments, scoped child tasks, and runnable flow templates, but no first-wave child task left draft or entered queued execution"
	case progress.FirstWavePromotedCount < progress.FirstWaveTaskCount:
		return fmt.Sprintf("kickoff validation failed: only %d of %d selected first-wave child tasks left draft or entered queued execution, so bootstrap never promoted the full runnable child wave", progress.FirstWavePromotedCount, progress.FirstWaveTaskCount)
	case progress.FirstWaveExecutionCount == 0:
		return "kickoff validation failed: first-wave child tasks left draft, but no flow_node_execution rows were created, so bootstrap never entered runnable execution"
	case progress.FirstWaveExecutionCount < progress.FirstWaveTaskCount:
		return fmt.Sprintf("kickoff validation failed: only %d of %d selected first-wave child tasks created flow_node_execution rows, so bootstrap never materialized the full runnable child wave", progress.FirstWaveExecutionCount, progress.FirstWaveTaskCount)
	case progress.FirstWaveJobCount == 0:
		return "kickoff validation failed: first-wave child tasks entered flow execution, but no runnable agent_turn jobs were created for their execution sessions"
	case progress.FirstWaveJobCount < progress.FirstWaveTaskCount:
		return fmt.Sprintf("kickoff validation failed: only %d of %d selected first-wave child tasks produced runnable agent_turn jobs, so bootstrap never claimed the full runnable child wave", progress.FirstWaveJobCount, progress.FirstWaveTaskCount)
	default:
		return "kickoff validation failed: first-wave execution never materialized after persisted setup was created"
	}
}

func buildProjectBootstrapPrematureCompletionFailure(progress projectBootstrapProgress) (string, string) {
	switch {
	case progress.ValidationFailed():
		return progress.ValidationFailureClass, progress.ValidationFailureReason
	case progress.FirstWaveTaskCount == 0:
		return projectBootstrapFailureCompoundParent, "kickoff validation failed: project-session narrative claimed bootstrap complete before any executable first-wave child tasks were selected"
	default:
		reason := buildProjectBootstrapFirstWaveExecutionFailureReason(progress)
		reason = strings.TrimPrefix(reason, "kickoff validation failed: ")
		return projectBootstrapFailureFirstWaveExecution, "kickoff validation failed: project-session narrative claimed bootstrap complete, but " + reason
	}
}

func normalizeProjectBootstrapValidationFailure(progress *projectBootstrapProgress, completionClaimed bool) {
	if progress == nil {
		return
	}
	if completionClaimed && !progress.Materialized() {
		progress.ValidationStatus = projectBootstrapValidationFailed
		progress.ValidationFailureClass, progress.ValidationFailureReason = buildProjectBootstrapPrematureCompletionFailure(*progress)
		return
	}
	if !progress.ValidationFailed() || (progress.ValidationFailureClass != "" && progress.ValidationFailureReason != "") {
		return
	}
	switch {
	case progress.FirstWaveTaskCount > 0:
		progress.ValidationFailureClass = projectBootstrapFailureFirstWaveExecution
		progress.ValidationFailureReason = buildProjectBootstrapFirstWaveExecutionFailureReason(*progress)
	case progress.PlannedTaskCount == 0 && progress.AssignmentCount > 0:
		progress.ValidationFailureClass = projectBootstrapFailureRuntime
		progress.ValidationFailureReason = buildProjectBootstrapScaffoldOnlyFailureReason()
	default:
		progress.ValidationFailureClass = projectBootstrapFailureRuntime
		progress.ValidationFailureReason = "kickoff validation failed: bootstrap transitioned to a failed state without recording a machine-readable reason"
	}
}

func projectBootstrapNarrativeClaimsCompletion(message *repo.ChatMessage) bool {
	if message == nil {
		return false
	}
	content := strings.ToLower(strings.TrimSpace(message.Content))
	if content == "" {
		return false
	}
	return strings.Contains(content, "bootstrap complete") ||
		strings.Contains(content, "bootstrap completed") ||
		strings.Contains(content, "bootstrap is complete") ||
		strings.Contains(content, "bootstrap setup is complete")
}

func buildProjectBootstrapFirstWaveStatusFailureReason(status string) string {
	trimmed := strings.ToLower(strings.TrimSpace(status))
	if trimmed == "" {
		trimmed = "draft"
	}
	return fmt.Sprintf("it is still %q instead of queued or already executing", trimmed)
}

func projectBootstrapTaskLabel(task repo.ProjectTask) string {
	title := strings.TrimSpace(task.Title)
	if task.TaskNumber > 0 && title != "" {
		return fmt.Sprintf("task %d (%s)", task.TaskNumber, title)
	}
	if task.TaskNumber > 0 {
		return fmt.Sprintf("task %d", task.TaskNumber)
	}
	if title != "" {
		return fmt.Sprintf("task %s", title)
	}
	return "bootstrap task"
}

func (e *TurnEngine) projectBootstrapContinuationAgent(ctx context.Context, session *chat.ChatSession, latestResponderID uuid.UUID) uuid.UUID {
	if latestResponderID == uuid.Nil {
		return uuid.Nil
	}
	frankID, err := e.resolveFrankStarterID(ctx, session.OrganizationID)
	if err == nil && latestResponderID == frankID {
		return uuid.Nil
	}
	return latestResponderID
}

func (e *TurnEngine) updateProjectBootstrapState(ctx context.Context, session *chat.ChatSession, state projectBootstrapState) error {
	if e == nil || e.pool == nil || session == nil || session.ID == uuid.Nil {
		return nil
	}
	previous := projectBootstrapStateFromMetadata(session.Metadata)
	state = normalizeProjectBootstrapStateCounts(state)
	state = projectBootstrapStateWithDerived(previous, state)
	metadata, err := projectBootstrapMetadataJSON(session.Metadata, state)
	if err != nil {
		return err
	}
	if _, err := e.pool.Exec(ctx, `
		UPDATE chat_session
		SET metadata = $2::jsonb
		WHERE id = $1
	`, session.ID, metadata); err != nil {
		return err
	}
	session.Metadata = metadata
	if strings.EqualFold(strings.TrimSpace(session.ScopeType), "project") && session.ScopeID != uuid.Nil {
		if err := e.updateProjectBootstrapProjectState(ctx, session.ScopeID, state); err != nil {
			return err
		}
	}
	return nil
}

func projectBootstrapStateFromMetadata(metadata json.RawMessage) projectBootstrapState {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return projectBootstrapState{}
	}
	var decoded map[string]any
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		return projectBootstrapState{}
	}
	raw, ok := decoded[projectBootstrapMetadataKey]
	if !ok || raw == nil {
		return projectBootstrapState{}
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return projectBootstrapState{}
	}
	var state projectBootstrapState
	if err := json.Unmarshal(payload, &state); err != nil {
		return projectBootstrapState{}
	}
	state = normalizeProjectBootstrapStateCounts(state)
	return projectBootstrapStateWithDerived(state, state)
}

func projectBootstrapMetadataJSON(metadata json.RawMessage, state projectBootstrapState) (json.RawMessage, error) {
	payload := map[string]any{}
	if len(metadata) > 0 && json.Valid(metadata) {
		if err := json.Unmarshal(metadata, &payload); err != nil {
			return nil, err
		}
	}
	payload[projectBootstrapMetadataKey] = state
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func projectBootstrapWorkflowMessageID(message *repo.ChatMessage) uuid.UUID {
	if message == nil {
		return uuid.Nil
	}
	metadata := messageMetadataMap(message.Metadata)
	if parsed, ok := parseUUIDAny(metadata["bootstrap_initial_message_id"]); ok && parsed != uuid.Nil {
		return parsed
	}
	return message.ID
}

func (e *TurnEngine) appendProjectBootstrapContinuationMessage(ctx context.Context, sessionID, authorAgentID uuid.UUID, initialMessageID string, autoTurnCount int) (*chat.ChatMessage, error) {
	return e.appendProjectBootstrapContinuationMessageWithContent(ctx, sessionID, authorAgentID, initialMessageID, autoTurnCount, buildProjectBootstrapContinuationPrompt(autoTurnCount))
}

func (e *TurnEngine) appendProjectBootstrapRecoveryContinuationMessage(ctx context.Context, sessionID, authorAgentID uuid.UUID, initialMessageID string, autoTurnCount int, progress projectBootstrapProgress) (*chat.ChatMessage, error) {
	content := buildProjectBootstrapValidationRecoveryPrompt(autoTurnCount, progress)
	if e != nil && e.chat != nil {
		if session, err := e.chat.GetSession(ctx, sessionID); err == nil && session != nil && strings.EqualFold(strings.TrimSpace(session.ScopeType), "project") {
			state := projectBootstrapStateFromMetadata(session.Metadata)
			if strings.TrimSpace(progress.ValidationFailureReason) != "" {
				state.ValidationStatus = projectBootstrapValidationFailed
				state.ValidationFailureReason = strings.TrimSpace(progress.ValidationFailureReason)
				state.ValidationFailureClass = projectBootstrapFailureClassForReason(progress.ValidationFailureClass, progress.ValidationFailureReason)
			}
			snapshot, snapshotErr := e.loadProjectBootstrapResumeSnapshot(ctx, session.ScopeID, state)
			if snapshotErr == nil {
				if repairLine := buildProjectBootstrapAdditionalRepairTaskLine(progress); repairLine != "" {
					snapshot.RepairTaskLine = repairLine
				}
				if snippet := buildProjectBootstrapRecoveryContinuationContext(snapshot); snippet != "" {
					content = strings.TrimSpace(content + " " + snippet)
				}
			}
		}
	}
	return e.appendProjectBootstrapContinuationMessageWithContent(ctx, sessionID, authorAgentID, initialMessageID, autoTurnCount, content)
}

func (e *TurnEngine) appendProjectBootstrapContinuationMessageWithContent(ctx context.Context, sessionID, authorAgentID uuid.UUID, initialMessageID string, autoTurnCount int, content string) (*chat.ChatMessage, error) {
	if e == nil || sessionID == uuid.Nil {
		return nil, repo.ErrNotFound
	}
	metadataMap := map[string]any{
		"source":                       projectBootstrapSource,
		"auto_continue":                true,
		"bootstrap_initial_message_id": strings.TrimSpace(initialMessageID),
		"bootstrap_auto_turn_count":    autoTurnCount,
	}
	if freshKickoff, err := e.bootstrapInitialMessageRequestsFreshKickoff(ctx, sessionID, initialMessageID); err != nil {
		return nil, err
	} else if freshKickoff {
		metadataMap["fresh_kickoff"] = true
	}
	metadata, err := json.Marshal(metadataMap)
	if err != nil {
		return nil, err
	}
	var authorType *string
	var authorID *uuid.UUID
	if authorAgentID != uuid.Nil {
		agentType := "agent"
		authorType = &agentType
		authorID = &authorAgentID
	}
	return e.chat.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  sessionID,
		AuthorType: authorType,
		AuthorID:   authorID,
		Role:       "user",
		Content:    content,
		Metadata:   metadata,
	})
}

func buildProjectBootstrapContinuationPrompt(autoTurnCount int) string {
	return fmt.Sprintf(
		"Continue the bounded project bootstrap setup workflow now. This is automatic follow-on bootstrap turn %d. Do not stop at acknowledgement. Persist project assignments, scoped tasks, and flow templates if the handoff already contains enough information. Every task.create or subtask.create call must include a concrete non-empty title. The bootstrap governance gate task is system-managed: do not edit it, do not try to assign it, and do not try to queue or complete it manually. Keep first-wave execution tasks in draft until the gate auto-completes after validation passes. Frank, Lori, and Ellie are starter-trio governance agents, not project staff, so do not assign them to project roles. The project manager must be a staff PM agent, not a temp agent. Assign every executable non-bootstrap task to an existing active project assignee before first-wave selection or promotion. Treat bind-repo-environment as confirming the canonical repo/workspace binding and environment records already present for the project; do not use git.commit or ad hoc cli.execute commands just to satisfy the bootstrap checklist. If setup truly cannot continue, explain the concrete blocker so the session can mark bootstrap failure instead of idling.",
		autoTurnCount,
	)
}

func buildProjectBootstrapValidationRecoveryPrompt(autoTurnCount int, progress projectBootstrapProgress) string {
	reason := strings.TrimSpace(progress.ValidationFailureReason)
	if reason == "" {
		reason = "bootstrap validation found recoverable bounded work that still needs correction"
	}
	recoveryHint := "Correct the persisted task tree by splitting the offending broad parent or first-wave task into narrower executable child tasks, assign every executable child to an existing active project assignee, then continue first-wave selection from those bounded children."
	nextActionHint := "Do not return to bootstrap.setup.persist until that structural repair is complete and the first-wave selection points at the corrected bounded child tasks."
	switch strings.TrimSpace(progress.ValidationFailureClass) {
	case projectBootstrapFailureFirstWaveExecution:
		nextActionHint = "Do not call bootstrap.setup.persist until every selected first-wave task has an assigned active project agent and the corrected first-wave set is ready to validate."
	}
	lowerReason := strings.ToLower(reason)
	if strings.Contains(lowerReason, "has no assigned agent") {
		recoveryHint = "Repair the named persisted first-wave task directly. Do not begin with project.get, task.list, task.children, flow.list_templates, agent.list, or other broad rereads. Assign that exact task to one of the already-created active project assignees, then continue bootstrap from the corrected first-wave task set."
		nextActionHint = "Do not call bootstrap.setup.persist until every selected first-wave task has an assigned active project agent and the corrected first-wave set is ready to validate. Your first repair step should directly fix the named unassigned first-wave task instead of gathering more context. Your next assistant action should be a tool call, not a narrative reply. Do not call task.get on the named blocked task first when the exact task id and active assignee roster are already present in the bootstrap resume system message; go straight to task.update unless a concrete blocker makes that impossible. Do not call task.get with the bare task number from the validation error; use the exact task id and active assignee roster from the bootstrap resume system message already in this turn, and only inspect that one specific task if its persisted assignment target is still unclear."
		if strings.Contains(lowerReason, "wave ") || strings.Contains(lowerReason, "workstream") || strings.Contains(lowerReason, "parent") {
			recoveryHint += " If that named task is still a broad wave/workstream parent, keep the parent orchestration-only and immediately create bounded executable child tasks beneath it instead of trying to execute the parent directly."
			nextActionHint += " Do not call file.read on planning artifacts just to decide the child split. Use the persisted wave/workstream title and existing task tree to create the bounded children directly."
		}
		nextActionHint += " After you fix the assignment, do not call project.list, project.get, task.list, or flow.list_templates to rediscover the broader scaffold. If that same named task still looks broad, keep it orchestration-only and split it directly into bounded executable child tasks beneath the exact task id already provided."
		if progress.PlannedFlowTemplateCount > 0 || progress.BootstrapSetupDoneCount > 0 || progress.BootstrapTaskOutstanding {
			nextActionHint += " Once that direct repair is complete, do not go back to flow.list_templates or other broad rereads just to finish the checklist. The persisted bootstrap state already contains staffed tasks, flow attachments, and setup progress; return straight to bootstrap.setup.persist with canonical step slugs such as attach-validate-flow-templates, select-first-wave, and record-frank-sign-off if those steps are already satisfied."
		}
	}
	if strings.Contains(lowerReason, "requires human approval before queueing") {
		recoveryHint = "Repair the named persisted first-wave task directly so it can run autonomously. Do not ask for manual approval and do not keep that task in the first wave while it still requires human approval. Remove the approval gate from that exact task or replace it in the selected first wave with an already-created autonomous child task that can queue immediately."
		nextActionHint = "Do not call bootstrap.setup.persist until every selected first-wave task can queue without human approval. Your next assistant action should be a direct task mutation tool call on the named task or the selected first-wave set, not a narrative reply. Do not begin with project.list, project.get, task.list, flow.list_templates, agent.list, inbox reads, or staffing discovery. Work directly from the persisted task ids already present in the bootstrap resume state."
	}
	if strings.Contains(lowerReason, "bounded task-size policy") || strings.Contains(lowerReason, "bounded size policy") {
		recoveryHint += " Your next assistant action should be a tool call, not a narrative reply. Do not call task.get on the named oversized task first when the blocked task id is already present in the bootstrap resume message; keep that task orchestration-only and go straight to task.update plus bounded child-task creation."
		nextActionHint += " Do not start by rereading the oversized parent task; use the exact blocked task id from the bootstrap resume message and repair it directly."
	}
	if projectBootstrapRestartScaffoldFailureReason(reason) {
		recoveryHint = "This bootstrap state already has staffing but did not materialize staffed executable project work. Do not begin with project.get, task.list, flow.list_templates, file.list, git.log, or other broad rereads. Reuse the dedicated project staff already created in this session, assign them to the project if that assignment step is still incomplete, then immediately create bounded executable workstream tasks and child tasks so bootstrap moves past scaffold-only state."
		nextActionHint = "Do not spend this turn rediscovering repo state or profile catalogs. Your first repair step should be to persist staffed executable work using the existing project staff and then continue bootstrap from that materialized task tree. Do not answer with a standalone acknowledgement or status note. This turn should contain the concrete staffing/task mutation tool calls needed to move past scaffold-only state; if you cannot make those tool calls, explain the concrete blocker instead."
	}
	if strings.Contains(lowerReason, "only ") &&
		(strings.Contains(lowerReason, "selected first-wave child tasks created flow_node_execution rows") ||
			strings.Contains(lowerReason, "selected first-wave child tasks produced runnable agent_turn jobs") ||
			strings.Contains(lowerReason, "selected first-wave child tasks left draft or entered queued execution")) {
		recoveryHint = "Shrink the selected first wave to a smaller bounded subset of the already-created child tasks so every selected task can leave draft and fully materialize execution. Leave later-wave tasks in draft, keep the staffed task tree intact, and persist the corrected first-wave selection instead of broadening the task tree further."
		nextActionHint = "Do not call bootstrap.setup.persist again until the selected first wave is reduced to that smaller runnable subset. Do not begin with project.list, project.get, task.list, flow.list_templates, flow.get_execution, file.read, file.write, agent.list, or staffing discovery. Do not rewrite planning artifacts or regenerate the broader scaffold. Work directly from the already-created child tasks and persisted first-wave set, keep later-wave tasks in draft, and repair the selected runnable subset with direct task and flow mutations only."
	}
	return fmt.Sprintf(
		"Continue the bounded project bootstrap setup workflow now. This is automatic follow-on bootstrap turn %d. Recovery target: %s. Do not repeat the same oversized task definitions or re-run the same rejected task.create calls. Every task.create or subtask.create call must include a concrete non-empty title. The bootstrap governance gate task is system-managed: do not edit it, do not try to assign it, and do not try to queue or complete it manually. Keep first-wave execution tasks in draft until the gate auto-completes after validation passes. Frank, Lori, and Ellie are starter-trio governance agents, not project staff, so do not assign them to project roles. The project manager must be a staff PM agent, not a temp agent. %s %s Treat bind-repo-environment as confirming the canonical repo/workspace binding and environment records already present for the project; do not use git.commit or ad hoc cli.execute commands just to satisfy the bootstrap checklist. If setup truly cannot continue, explain the concrete blocker so the session can mark bootstrap failure instead of idling.",
		autoTurnCount,
		reason,
		recoveryHint,
		nextActionHint,
	)
}

func buildProjectBootstrapRecoveryContinuationContext(snapshot projectBootstrapResumeSnapshot) string {
	parts := []string{}
	if blockedTask := strings.TrimSpace(snapshot.FailedTaskLine); blockedTask != "" {
		parts = append(parts, blockedTask)
	}
	if repairLine := strings.TrimSpace(snapshot.RepairTaskLine); repairLine != "" {
		parts = append(parts, repairLine)
	}
	if assignments := strings.TrimSpace(snapshot.AssignmentLine); assignments != "" {
		parts = append(parts, "Existing active assignments: "+assignments+". Reuse one of those assigned agent ids directly; do not call agent.list unless the persisted roster itself is inconsistent.")
	}
	return strings.Join(parts, " ")
}

func buildProjectBootstrapFailureReason(autoTurnCount int) string {
	return fmt.Sprintf("bootstrap setup stalled after %d consecutive follow-on turns without creating project assignments, scoped tasks, and flow templates", autoTurnCount)
}

func buildProjectBootstrapWatchdogFailureReason(timeoutErr *projectBootstrapTimeoutError) string {
	timeout := defaultProjectBootstrapTurnTimeout
	invocationID := uuid.Nil
	progress := projectBootstrapProgress{}
	if timeoutErr != nil {
		if timeoutErr.Timeout > 0 {
			timeout = timeoutErr.Timeout
		}
		invocationID = timeoutErr.InvocationID
		progress = timeoutErr.Progress
	}
	var reason string
	if progress.AssignmentCount == 0 && progress.StaffingDraftCount == 0 && progress.PlannedTaskCount == 0 && progress.PlannedFlowTemplateCount == 0 {
		reason = fmt.Sprintf(
			"bootstrap setup watchdog timed out after %s with zero persisted staffing drafts, project assignments, scoped tasks, or flow templates",
			timeout.String(),
		)
	} else {
		reason = fmt.Sprintf(
			"bootstrap setup watchdog timed out after %s after partial persisted setup (staffing_drafts=%d, assignments=%d, scoped_tasks=%d, flow_templates=%d, first_wave_tasks=%d, first_wave_promoted=%d, first_wave_execution=%d, first_wave_jobs=%d)",
			timeout.String(),
			progress.StaffingDraftCount,
			progress.AssignmentCount,
			progress.PlannedTaskCount,
			progress.PlannedFlowTemplateCount,
			progress.FirstWaveTaskCount,
			progress.FirstWavePromotedCount,
			progress.FirstWaveExecutionCount,
			progress.FirstWaveJobCount,
		)
	}
	if invocationID != uuid.Nil {
		reason += fmt.Sprintf("; model invocation %s remained in_flight", invocationID)
	}
	return reason
}

func (e *TurnEngine) countProjectBootstrapStaffingDrafts(ctx context.Context, projectID uuid.UUID) (int, error) {
	if e == nil || e.pool == nil || projectID == uuid.Nil {
		return 0, nil
	}
	var count int
	err := e.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM chat_message m
		JOIN chat_session s ON s.id = m.session_id
		WHERE s.scope_type = 'project'
		  AND s.scope_id = $1
		  AND m.role = 'tool_result'
		  AND COALESCE(NULLIF(m.status, ''), 'pending') = 'final'
		  AND (
			m.content LIKE '%"tool_name":"agent.create_staff"%'
			OR m.content LIKE '%"tool_name":"agent.create_temp"%'
		  )
	`, projectID).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func buildProjectBootstrapGuardrailFailureReason(detail string) string {
	trimmed := strings.TrimSpace(detail)
	if trimmed == "" {
		trimmed = "bounded prompt/continuation guardrails exhausted before persisted setup was created"
	}
	return fmt.Sprintf("bootstrap setup hit continuation-depth guardrails before creating project assignments, scoped tasks, and flow templates: %s", trimmed)
}

func buildProjectBootstrapTerminalFailureReason(nextCheckpoint string, cause error) string {
	target := strings.TrimSpace(nextCheckpoint)
	if target == "" {
		target = "the next bootstrap checkpoint"
	}
	switch {
	case errors.Is(cause, ErrAuthFailed):
		return fmt.Sprintf("bootstrap setup could not reach %s because model provider authentication failed: %s", target, summarizeFailure(cause))
	case errors.Is(cause, ErrRateLimited):
		return fmt.Sprintf("bootstrap setup could not reach %s because model provider rate limits were exhausted: %s", target, summarizeFailure(cause))
	case isTransientModelError(cause):
		return fmt.Sprintf("bootstrap setup could not reach %s because provider/API connectivity remained transiently unavailable: %s", target, summarizeFailure(cause))
	default:
		return fmt.Sprintf("bootstrap setup failed before reaching %s: %s", target, summarizeFailure(cause))
	}
}

func buildProjectBootstrapFailureMessage(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		trimmed = "bootstrap setup stalled before persisted staffing and task records were created"
	}
	return fmt.Sprintf("[Project bootstrap failed: %s. The project session metadata now carries a machine-visible bootstrap failure state instead of silently idling.]", trimmed)
}

func classifyProjectProviderFailure(err error) (string, string, bool) {
	switch {
	case errors.Is(err, ErrAuthFailed):
		return projectFailureClassProviderAuth, strings.TrimSpace(err.Error()), true
	case errors.Is(err, ErrRateLimited):
		return projectFailureClassProviderRateLimit, strings.TrimSpace(err.Error()), true
	case isTransientModelError(err):
		return projectFailureClassProviderTransient, strings.TrimSpace(err.Error()), true
	default:
		return "", "", false
	}
}

func projectFailureActionForProgress(progress projectBootstrapProgress, failureCategory, failureClass string) string {
	if failureCategory == projectFailureCategoryProvider {
		switch strings.TrimSpace(failureClass) {
		case projectFailureClassProviderAuth:
			return projectFailureActionPause
		case projectFailureClassProviderRateLimit, projectFailureClassProviderTransient:
			if projectBootstrapSetupPersisted(progress) || projectBootstrapReachedFirstWaveClaim(progress) {
				return projectFailureActionPause
			}
			return projectFailureActionArchive
		default:
			return projectFailureActionPause
		}
	}
	if projectBootstrapReachedFirstWaveClaim(progress) {
		return projectFailureActionPause
	}
	return projectFailureActionArchive
}

func formatBootstrapCheckpoint(checkpoint string) string {
	trimmed := strings.TrimSpace(checkpoint)
	if trimmed == "" {
		return projectBootstrapCheckpointProjectCreated
	}
	return trimmed
}

func projectBootstrapFailureCheckpoint(progress projectBootstrapProgress, failureClass string) string {
	switch strings.TrimSpace(failureClass) {
	case projectBootstrapFailureMissingAssignments, projectBootstrapFailureMissingPM, projectBootstrapFailureMissingReviewer:
		if progress.PlannedTaskCount > 0 {
			return projectBootstrapCheckpointTaskTree
		}
		return projectBootstrapCheckpointProjectCreated
	case projectBootstrapFailureCompoundParent, projectBootstrapFailureFirstWaveSize, projectBootstrapFailureSetupTaskScope, projectBootstrapFailureSetupTaskChildren:
		if progress.PlannedTaskCount > 0 {
			return projectBootstrapCheckpointTaskTree
		}
		if progress.PlannedFlowTemplateCount > 0 || progress.AssignmentCount > 0 {
			return projectBootstrapCheckpointFirstWave
		}
		return projectBootstrapCheckpointProjectCreated
	case projectBootstrapFailureRepoBinding, projectBootstrapFailureFirstWaveFlow:
		if progress.FirstWaveTaskCount > 0 {
			return projectBootstrapCheckpointFirstWave
		}
		if progress.PlannedFlowTemplateCount > 0 {
			return projectBootstrapCheckpointFlowTemplates
		}
		if progress.PlannedTaskCount > 0 {
			return projectBootstrapCheckpointTaskTree
		}
		return projectBootstrapCheckpointProjectCreated
	case projectBootstrapFailureFirstWaveExecution:
		if progress.FirstWaveTaskCount > 0 {
			return projectBootstrapCheckpointExecutions
		}
		if progress.PlannedFlowTemplateCount > 0 {
			return projectBootstrapCheckpointFlowTemplates
		}
		if progress.PlannedTaskCount > 0 {
			return projectBootstrapCheckpointTaskTree
		}
		return projectBootstrapCheckpointProjectCreated
	default:
		return projectBootstrapLastCheckpoint(progress)
	}
}

func projectBootstrapLastSuccessfulCheckpoint(progress projectBootstrapProgress) string {
	checkpoints := []struct {
		name    string
		reached bool
	}{
		{name: projectBootstrapCheckpointProjectCreated, reached: true},
		{name: projectBootstrapCheckpointStaffingPersisted, reached: progress.AssignmentCount > 0 || progress.StaffingDraftCount > 0},
		{name: projectBootstrapCheckpointTaskTreePersisted, reached: progress.PlannedTaskCount > 0},
		{name: projectBootstrapCheckpointFlowTemplatesPersisted, reached: progress.PlannedFlowTemplateCount > 0},
		{name: projectBootstrapCheckpointFirstWaveSelected, reached: progress.FirstWaveTaskCount > 0},
		{name: projectBootstrapCheckpointFirstWaveExecutions, reached: progress.FirstWaveExecutionCount > 0},
		{name: projectBootstrapCheckpointFirstWaveJobsClaimed, reached: progress.FirstWaveJobCount > 0},
	}
	last := ""
	for _, checkpoint := range checkpoints {
		if !checkpoint.reached {
			break
		}
		last = checkpoint.name
	}
	return last
}

func projectBootstrapSetupPersisted(progress projectBootstrapProgress) bool {
	return progress.AssignmentCount > 0 ||
		progress.StaffingDraftCount > 0 ||
		progress.PlannedTaskCount > 0 ||
		progress.PlannedFlowTemplateCount > 0 ||
		progress.FirstWaveTaskCount > 0 ||
		progress.FirstWaveExecutionCount > 0 ||
		progress.FirstWaveJobCount > 0
}

func buildProjectBootstrapAutomaticFailureRecord(progress projectBootstrapProgress, failureCategory, failureClass, failureReason string, now time.Time) projectAutomaticFailureRecord {
	checkpoint := projectBootstrapFailureCheckpoint(progress, failureClass)
	return projectAutomaticFailureRecord{
		Action:                   projectFailureActionForProgress(progress, failureCategory, failureClass),
		Source:                   projectBootstrapSource,
		FailureCategory:          strings.TrimSpace(failureCategory),
		FailureClass:             strings.TrimSpace(failureClass),
		FailurePhase:             checkpoint,
		LastCheckpoint:           checkpoint,
		LastSuccessfulCheckpoint: projectBootstrapLastSuccessfulCheckpoint(progress),
		FailureReason:            strings.TrimSpace(failureReason),
		SetupPersisted:           projectBootstrapSetupPersisted(progress),
		RecordedAt:               now.UTC(),
	}
}

func buildProjectExecutionFailureRecord(progress projectBootstrapProgress, failureCategory, failureClass, failureReason string, now time.Time) projectAutomaticFailureRecord {
	checkpoint := projectBootstrapLastCheckpoint(progress)
	return projectAutomaticFailureRecord{
		Action:                   projectFailureActionPause,
		Source:                   "execution_runtime",
		FailureCategory:          strings.TrimSpace(failureCategory),
		FailureClass:             strings.TrimSpace(failureClass),
		FailurePhase:             checkpoint,
		LastCheckpoint:           checkpoint,
		LastSuccessfulCheckpoint: projectBootstrapLastSuccessfulCheckpoint(progress),
		FailureReason:            strings.TrimSpace(failureReason),
		SetupPersisted:           projectBootstrapSetupPersisted(progress),
		RecordedAt:               now.UTC(),
	}
}

func buildProjectBootstrapAutomaticFailureMessage(record projectAutomaticFailureRecord) string {
	reason := strings.TrimSpace(record.FailureReason)
	if reason == "" {
		reason = "bootstrap setup failed"
	}
	checkpoint := formatBootstrapCheckpoint(record.LastCheckpoint)
	switch {
	case record.FailureCategory == projectFailureCategoryProvider:
		return fmt.Sprintf("[Project bootstrap paused: %s. The project was paused instead of archived because the failure was classified as a provider/API outage at phase %s.]", reason, checkpoint)
	case record.Action == projectFailureActionPause:
		return fmt.Sprintf("[Project bootstrap failed: %s. The project was paused automatically because execution had already reached %s and existing work needed to be preserved.]", reason, checkpoint)
	default:
		return fmt.Sprintf("[Project bootstrap failed: %s. The project was archived automatically because bootstrap never reached %s.]", reason, projectBootstrapCheckpointJobsClaimed)
	}
}

func buildProjectExecutionPauseMessage(record projectAutomaticFailureRecord) string {
	reason := strings.TrimSpace(record.FailureReason)
	if reason == "" {
		reason = "execution failed"
	}
	checkpoint := formatBootstrapCheckpoint(record.LastCheckpoint)
	if record.FailureCategory == projectFailureCategoryProvider {
		return fmt.Sprintf("[Project paused automatically: %s. Execution had already reached %s, and the failure was classified as a provider/API outage rather than a bootstrap defect.]", reason, checkpoint)
	}
	return fmt.Sprintf("[Project paused automatically: %s. Execution had already reached %s, so the project was paused to preserve existing work.]", reason, checkpoint)
}

func (e *TurnEngine) applyProjectAutomaticFailure(ctx context.Context, projectID uuid.UUID, record projectAutomaticFailureRecord) error {
	if e == nil || e.pool == nil || e.events == nil || projectID == uuid.Nil {
		return nil
	}

	projectRecord, err := e.projects.GetByID(ctx, projectID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	projectRepo := repo.NewProjectRepo(e.pool)
	updated := projectRecord
	updated.Settings, err = projectfailure.Apply(updated.Settings, projectfailure.State{
		Action:                   record.Action,
		Source:                   record.Source,
		FailureCategory:          record.FailureCategory,
		FailureClass:             record.FailureClass,
		FailurePhase:             record.FailurePhase,
		LastCheckpoint:           record.LastCheckpoint,
		LastSuccessfulCheckpoint: record.LastSuccessfulCheckpoint,
		FailureReason:            record.FailureReason,
		SetupPersisted:           record.SetupPersisted,
		RecordedAt: func() *time.Time {
			recordedAt := record.RecordedAt.UTC()
			return &recordedAt
		}(),
	})
	if err != nil {
		return err
	}
	if _, err := projectRepo.Update(ctx, updated); err != nil {
		return err
	}

	projectService, err := projectsvc.NewService(projectsvc.Options{
		Pool:   e.pool,
		Events: e.events,
	})
	if err != nil {
		return err
	}

	switch strings.TrimSpace(record.Action) {
	case projectFailureActionArchive:
		if strings.EqualFold(strings.TrimSpace(projectRecord.Status), "archived") {
			return nil
		}
		archived, err := projectService.Archive(ctx, projectRecord.OrganizationID, projectRecord.ID)
		if err != nil {
			return err
		}
		if archived != nil {
			updated = *archived
		}
		return e.maybeRestartArchivedBootstrapProject(ctx, updated, record, false)
	case projectFailureActionPause:
		if strings.EqualFold(strings.TrimSpace(projectRecord.Status), "archived") {
			return nil
		}
		_, err = projectService.Pause(ctx, projectRecord.OrganizationID, projectRecord.ID, projectsvc.PauseProjectRequest{
			Reason:       record.FailureReason,
			Metadata:     projectfailure.State{Action: record.Action, Source: record.Source, FailureCategory: record.FailureCategory, FailureClass: record.FailureClass, FailurePhase: record.FailurePhase, LastCheckpoint: record.LastCheckpoint, LastSuccessfulCheckpoint: record.LastSuccessfulCheckpoint, FailureReason: record.FailureReason, SetupPersisted: record.SetupPersisted, RecordedAt: &record.RecordedAt}.JSON(),
			PausedByType: "system",
		})
		return err
	default:
		return nil
	}
}

func applyProjectBootstrapProgressState(state *projectBootstrapState, progress projectBootstrapProgress) {
	if state == nil {
		return
	}
	checkpoint := projectBootstrapLastCheckpoint(progress)
	state.Phase = checkpoint
	state.LastCheckpoint = checkpoint
	state.AssignmentCount = progress.AssignmentCount
	state.StaffingDraftCount = progress.StaffingDraftCount
	state.PlannedTaskCount = progress.PlannedTaskCount
	state.PlannedFlowTemplateCount = progress.PlannedFlowTemplateCount
	state.FirstWaveTaskCount = progress.FirstWaveTaskCount
	state.FirstWavePromotedCount = progress.FirstWavePromotedCount
	state.FirstWaveExecutionCount = progress.FirstWaveExecutionCount
	state.setFirstWaveJobCount(progress.FirstWaveJobCount)
	state.ValidationStatus = progress.ValidationStatus
	state.ValidationFailureClass = progress.ValidationFailureClass
	state.ValidationFailureReason = progress.ValidationFailureReason
}

type workCompletionSignal struct {
	commitSHA      string
	filesCommitted int
}

func (e *TurnEngine) handleCompletedWorkTurn(ctx context.Context, taskRecord repo.ProjectTask, agentID uuid.UUID, messages []repo.ChatMessage, turnID uuid.UUID) (bool, error) {
	if e.flowAdvancer == nil || taskRecord.ID == uuid.Nil || agentID == uuid.Nil {
		return false, nil
	}
	signal, ok := completedWorkSignalFromMessages(taskRecord, messages, turnID)
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
	if e.logger != nil {
		e.logger.Debug("agent_turn dispatch: loaded session",
			"session_id", sessionID,
			"message_id", messageID,
			"scope_type", strings.TrimSpace(session.ScopeType),
			"mode", strings.TrimSpace(session.Mode),
			"status", strings.TrimSpace(session.Status),
			"current_turn_id", session.CurrentTurnID,
		)
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
	effectiveRoutedAgentID := normalizeRoutedAgentForSession(session, routedAgentID)
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
	if e.logger != nil {
		e.logger.Debug("agent_turn dispatch: loaded message",
			"session_id", sessionID,
			"message_id", messageID,
			"role", strings.TrimSpace(message.Role),
			"status", strings.TrimSpace(message.Status),
		)
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
	if effectiveRoutedAgentID != nil && *effectiveRoutedAgentID != uuid.Nil {
		agentID = *effectiveRoutedAgentID
	}
	if agentID == uuid.Nil {
		var resolveErr error
		agentID, resolveErr = e.resolveSessionAgentForSession(ctx, session)
		if resolveErr != nil {
			return resolveErr
		}
	}
	if e.logger != nil {
		e.logger.Debug("agent_turn dispatch: resolved agent",
			"session_id", sessionID,
			"message_id", messageID,
			"agent_id", agentID,
		)
	}
	agent, err := e.agents.GetByID(ctx, agentID)
	if err != nil {
		return err
	}

	turnRecord, _, err := e.turns.CreateForMessageAttempt(ctx, sessionID, agentID, messageID, retryCount)
	if err != nil {
		return err
	}
	if e.logger != nil {
		e.logger.Debug("agent_turn dispatch: created turn record",
			"session_id", sessionID,
			"message_id", messageID,
			"turn_id", turnRecord.ID,
			"turn_status", strings.TrimSpace(turnRecord.Status),
			"retry_count", retryCount,
		)
	}
	turn, shouldRun, err := e.startInboundMessageTurn(ctx, turnRecord)
	if err != nil {
		return err
	}
	if e.logger != nil {
		e.logger.Debug("agent_turn dispatch: start inbound turn",
			"session_id", sessionID,
			"message_id", messageID,
			"turn_id", turn.ID,
			"turn_status", strings.TrimSpace(turn.Status),
			"should_run", shouldRun,
		)
	}
	if !shouldRun {
		if recovered, recoverErr := e.recoverProjectTaskStaleInboundTurnWithoutRun(ctx, session, messageID, agentID, retryCount, currentJobID, turn); recoverErr != nil {
			return recoverErr
		} else if recovered {
			return nil
		}
		if recovered, recoverErr := e.recoverRetriedSessionCurrentTurnLeak(ctx, session, messageID, agentID, retryCount, currentJobID, turn); recoverErr != nil {
			return recoverErr
		} else if recovered {
			return nil
		}
		if recovered, recoverErr := e.recoverRetriedAgentTurnLeak(ctx, session, messageID, agentID, retryCount, currentJobID, turn); recoverErr != nil {
			return recoverErr
		} else if recovered {
			return nil
		}
		return nil
	}
	if err := e.syncBoundFlowExecutionTurnOwnership(ctx, session, &turn.ID); err != nil {
		return err
	}
	defer e.reconcileBoundFlowExecutionTurnOwnership(context.Background(), session, turn.ID)
	if retryCount > 0 {
		if _, err := e.appendSystemMessage(ctx, turn.ID, sessionID, fmt.Sprintf("[Retry attempt %d started.]", retryCount)); err != nil {
			return err
		}
	}

	runtime := &turnRuntime{
		session:            session,
		agent:              agent,
		turn:               turn,
		initialMessageID:   messageID,
		initialMessageText: strings.TrimSpace(message.Content),
		currentJobID:       cloneUUIDPointer(currentJobID),
		runID:              runIDFromMetadata(message.Metadata),
		runStepID:          runStepIDFromMetadata(message.Metadata),
		runAttemptID:       runAttemptIDFromMetadata(message.Metadata),
		startedAt:          e.turnStartTime(turn),
		recoveryTurn:       isRecoveryResumeMessage(message),
	}
	runtime.projectIdentity = e.loadProjectIdentityForMessage(ctx, sessionID, messageID)
	if messageRequestsFreshKickoff(session, message) {
		runtime.historyStartID = &message.ID
		runtime.disableMemory = true
		runtime.freshKickoff = true
	} else if taskContinuationResumeMessageRootsHistory(message) {
		runtime.historyStartID = &message.ID
	}
	if handled, handleErr := e.handleProjectBootstrapPreflight(ctx, runtime); handleErr != nil {
		return handleErr
	} else if handled {
		return nil
	}

	cancelCtx, stopCancelWatch := e.watchTurnCancellation(ctx, runtime)
	defer stopCancelWatch()

	err = e.runTurn(cancelCtx, runtime)
	if err == nil || errors.Is(err, errTurnDeferred) || errors.Is(err, errTurnCancelled) || errors.Is(err, errTurnPaused) {
		return e.ensureTurnRunExitInvariant(ctx, runtime)
	}
	if errors.Is(err, chat.ErrSessionClosed) {
		closedCtx, cancelClosed := context.WithCancelCause(ctx)
		cancelClosed(errTurnSessionClosed)
		return e.handleCancellation(closedCtx, runtime)
	}
	if errors.Is(err, context.Canceled) {
		if ctx.Err() != nil {
			return e.handleCancellation(ctx, runtime)
		}
		if closed, closeErr := e.sessionClosedOrArchived(context.Background(), sessionID); closeErr == nil && closed {
			return e.handleCancellation(ctx, runtime)
		}
	}
	var bootstrapTimeoutErr *projectBootstrapTimeoutError
	if errors.As(err, &bootstrapTimeoutErr) {
		handled, handleErr := e.handleProjectBootstrapWatchdogTimeout(ctx, runtime, bootstrapTimeoutErr)
		if handleErr != nil {
			return handleErr
		}
		if handled {
			return nil
		}
	}
	var asyncTimeoutErr *asyncTurnTimeoutError
	if errors.As(err, &asyncTimeoutErr) {
		handled, handleErr := e.handleAsyncTurnWatchdogTimeout(ctx, runtime, asyncTimeoutErr)
		if handleErr != nil {
			return handleErr
		}
		if handled {
			return err
		}
	}
	if errors.Is(err, ErrRateLimited) {
		handled, handleErr := e.handleRateLimitedTurnFailure(ctx, runtime, messageID, effectiveRoutedAgentID, retryCount, err)
		if handleErr != nil {
			return handleErr
		}
		if handled {
			return nil
		}
	}
	if isTransientInfrastructureError(err) {
		handled, handleErr := e.handleTransientInfrastructureTurnFailure(ctx, runtime, messageID, effectiveRoutedAgentID, retryCount, err)
		if handleErr != nil {
			return handleErr
		}
		if handled {
			return nil
		}
	}
	if handled, handleErr := e.handleTaskContinuationDepthTurnFailure(ctx, runtime, messageID, effectiveRoutedAgentID, err); handleErr != nil {
		return handleErr
	} else if handled {
		return nil
	}
	if handled, handleErr := e.handleProjectBootstrapUnhandledFailure(ctx, runtime, err); handleErr != nil {
		return handleErr
	} else if handled {
		return nil
	}

	e.logger.Error("turn failed", "error", err, "session_id", sessionID, "turn_id", runtime.turn.ID, "agent_id", agentID)
	_ = e.chat.FailTurn(ctx, runtime.turn.ID, summarizeFailure(err))
	_, _ = e.appendSystemMessage(ctx, runtime.turn.ID, runtime.session.ID, fmt.Sprintf("[Turn failed: %s]", summarizeFailure(err)))
	if pauseErr := e.pauseProjectAfterExecutionFailure(ctx, runtime, err); pauseErr != nil {
		return pauseErr
	}
	return err
}

func (e *TurnEngine) handleRecoverableBootstrapTurnJobFailure(
	ctx context.Context,
	payload AgentTurnPayload,
	currentJobID *uuid.UUID,
	cause error,
) (bool, error) {
	if e == nil || e.chat == nil || cause == nil || payload.SessionID == uuid.Nil || payload.MessageID == uuid.Nil {
		return false, nil
	}

	session, err := e.chat.GetSession(ctx, payload.SessionID)
	if err != nil {
		return false, err
	}
	if session.ScopeID == uuid.Nil ||
		!strings.EqualFold(strings.TrimSpace(session.ScopeType), "project") ||
		!strings.EqualFold(strings.TrimSpace(session.Mode), "async") {
		return false, nil
	}

	progress, err := e.loadProjectBootstrapProgress(ctx, session.ScopeID)
	if err != nil {
		return false, err
	}
	normalizeProjectBootstrapValidationFailure(&progress, false)
	recoverableValidation := progress.ValidationFailed() && projectBootstrapRecoverableMaxToolCallFailure(progress)
	recoverableBoundedSize := strings.Contains(strings.ToLower(strings.TrimSpace(summarizeFailure(cause))), "bounded size policy")
	if recoverableBoundedSize && !recoverableValidation {
		progress.ValidationFailureClass = projectBootstrapFailureFirstWaveExecution
		progress.ValidationFailureReason = summarizeFailure(cause)
		recoverableValidation = projectBootstrapRecoverableMaxToolCallFailure(progress)
	}
	if !e.projectBootstrapRuntimeManaged(ctx, session, payload.MessageID) ||
		(!recoverableValidation && !recoverableBoundedSize) ||
		!projectBootstrapSetupPersisted(progress) ||
		session.CurrentTurnID == nil ||
		*session.CurrentTurnID == uuid.Nil {
		return false, nil
	}

	turn, err := e.chat.GetTurn(ctx, *session.CurrentTurnID)
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(strings.TrimSpace(turn.Status), "in_progress") {
		return false, nil
	}

	rt := &turnRuntime{
		session:          session,
		turn:             turn,
		initialMessageID: payload.MessageID,
		currentJobID:     cloneUUIDPointer(currentJobID),
		startedAt:        e.turnStartTime(turn),
	}
	if payload.AgentID != nil && *payload.AgentID != uuid.Nil {
		rt.agent.ID = *payload.AgentID
	} else if turn.RespondingID != uuid.Nil {
		rt.agent.ID = turn.RespondingID
	}

	handled, recoverErr := e.continueRecoverableProjectBootstrapValidation(
		ctx,
		rt,
		projectBootstrapStateFromMetadata(session.Metadata),
		progress,
		e.now().UTC(),
		true,
	)
	if recoverErr != nil {
		return true, recoverErr
	}
	if handled {
		_, _ = e.appendSystemMessage(
			ctx,
			turn.ID,
			session.ID,
			fmt.Sprintf("[Recovered bootstrap validation job failure into a fresh continuation: %s]", summarizeFailure(cause)),
		)
		return true, nil
	}
	return false, nil
}

func (e *TurnEngine) recoverRetriedAgentTurnLeak(
	ctx context.Context,
	session *chat.ChatSession,
	messageID, agentID uuid.UUID,
	retryCount int,
	currentJobID *uuid.UUID,
	turn *chat.ChatTurn,
) (bool, error) {
	if e == nil || e.pool == nil || e.chat == nil || e.enqueuer == nil || session == nil || turn == nil {
		return false, nil
	}
	if strings.EqualFold(strings.TrimSpace(session.ScopeType), "project_task") {
		return false, nil
	}
	if currentJobID == nil || *currentJobID == uuid.Nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(turn.Status), "in_progress") {
		return false, nil
	}

	attempts, err := e.agentTurnJobAttempts(ctx, *currentJobID)
	if err != nil {
		return false, err
	}
	if attempts <= 1 {
		return false, nil
	}

	failureReason := "recovered claimed agent_turn found prior turn still in_progress; marking stale turn failed and scheduling a fresh retry"
	if err := e.failRetriedLeakedTurn(ctx, turn.ID, failureReason); err != nil {
		return false, err
	}
	_, _ = e.appendSystemMessage(ctx, turn.ID, session.ID, "[Recovered stale in-progress turn after claimed job recovery - retrying in a fresh turn.]")

	if handled, recoverErr := e.enqueueRecoveredBootstrapValidationContinuation(ctx, session, turn, messageID, agentID, currentJobID); recoverErr != nil {
		return false, recoverErr
	} else if handled {
		return true, nil
	}

	nextPayload := AgentTurnPayload{
		SessionID:  session.ID,
		MessageID:  messageID,
		RetryCount: retryCount + 1,
	}
	if agentID != uuid.Nil {
		nextAgentID := agentID
		nextPayload.AgentID = &nextAgentID
	}
	if _, err := e.enqueueAgentTurnIfActive(ctx, session, nextPayload, nil); err != nil {
		return false, fmt.Errorf("enqueue recovered stale-turn retry: %w", err)
	}
	return true, nil
}

func (e *TurnEngine) recoverProjectTaskStaleInboundTurnWithoutRun(
	ctx context.Context,
	session *chat.ChatSession,
	messageID, agentID uuid.UUID,
	retryCount int,
	currentJobID *uuid.UUID,
	turn *chat.ChatTurn,
) (bool, error) {
	if e == nil || e.pool == nil || e.chat == nil || e.enqueuer == nil || session == nil || turn == nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project_task") {
		return false, nil
	}
	currentSession := session
	if refreshed, err := e.chat.GetSession(ctx, session.ID); err == nil && refreshed != nil {
		currentSession = refreshed
	}
	if currentSession.CurrentTurnID == nil || *currentSession.CurrentTurnID == uuid.Nil || *currentSession.CurrentTurnID != turn.ID {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(turn.Status), "in_progress") {
		return false, nil
	}

	var activeRuns int
	if err := e.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run
		WHERE session_id = $1
		  AND turn_id IS NOT NULL
		  AND status IN ('created', 'queued', 'in_progress')
	`, session.ID).Scan(&activeRuns); err != nil {
		return false, fmt.Errorf("count active runs for project_task stale inbound turn recovery: %w", err)
	}
	if activeRuns > 0 {
		return false, nil
	}

	failureReason := "recovered stale project_task inbound turn without active run ownership; scheduling a fresh retry"
	if err := e.failRetriedLeakedTurn(ctx, turn.ID, failureReason); err != nil {
		return false, err
	}
	_, _ = e.appendSystemMessage(ctx, turn.ID, currentSession.ID, "[Recovered stale in-progress task turn without active run ownership - retrying in a fresh turn.]")

	nextPayload := AgentTurnPayload{
		SessionID:  currentSession.ID,
		MessageID:  messageID,
		RetryCount: retryCount + 1,
	}
	if agentID != uuid.Nil {
		nextAgentID := agentID
		nextPayload.AgentID = &nextAgentID
	}
	if _, err := e.enqueueAgentTurnIfActive(ctx, currentSession, nextPayload, nil); err != nil {
		return false, fmt.Errorf("enqueue recovered stale project_task retry: %w", err)
	}
	return true, nil
}

func (e *TurnEngine) recoverRetriedSessionCurrentTurnLeak(
	ctx context.Context,
	session *chat.ChatSession,
	messageID, agentID uuid.UUID,
	retryCount int,
	currentJobID *uuid.UUID,
	turn *chat.ChatTurn,
) (bool, error) {
	if e == nil || e.chat == nil || session == nil || turn == nil || currentJobID == nil || *currentJobID == uuid.Nil {
		return false, nil
	}
	if strings.EqualFold(strings.TrimSpace(session.ScopeType), "project_task") {
		return false, nil
	}
	if session.CurrentTurnID == nil || *session.CurrentTurnID == uuid.Nil || *session.CurrentTurnID == turn.ID {
		return false, nil
	}

	attempts, err := e.agentTurnJobAttempts(ctx, *currentJobID)
	if err != nil {
		return false, err
	}
	if attempts <= 1 {
		return false, nil
	}

	currentTurn, err := e.chat.GetTurn(ctx, *session.CurrentTurnID)
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(strings.TrimSpace(currentTurn.Status), "in_progress") {
		return false, nil
	}

	failureReason := "recovered claimed agent_turn retry found later session continuation still in_progress; marking stale continuation failed and scheduling a fresh retry"
	if err := e.failRetriedLeakedTurn(ctx, currentTurn.ID, failureReason); err != nil {
		return false, err
	}
	_, _ = e.appendSystemMessage(ctx, currentTurn.ID, session.ID, "[Recovered stale in-progress continuation turn after claimed job recovery - retrying in a fresh turn.]")

	if handled, recoverErr := e.enqueueRecoveredBootstrapValidationContinuation(ctx, session, currentTurn, messageID, agentID, currentJobID); recoverErr != nil {
		return false, recoverErr
	} else if handled {
		return true, nil
	}

	nextPayload := AgentTurnPayload{
		SessionID:  session.ID,
		MessageID:  messageID,
		RetryCount: retryCount + 1,
	}
	if agentID != uuid.Nil {
		nextAgentID := agentID
		nextPayload.AgentID = &nextAgentID
	}
	if _, err := e.enqueueAgentTurnIfActive(ctx, session, nextPayload, nil); err != nil {
		return false, fmt.Errorf("enqueue recovered stale continuation retry: %w", err)
	}
	return true, nil
}

func (e *TurnEngine) enqueueRecoveredBootstrapValidationContinuation(
	ctx context.Context,
	session *chat.ChatSession,
	turn *chat.ChatTurn,
	messageID, agentID uuid.UUID,
	currentJobID *uuid.UUID,
) (bool, error) {
	if e == nil || session == nil || turn == nil || session.ScopeID == uuid.Nil || currentJobID == nil || *currentJobID == uuid.Nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project") || !strings.EqualFold(strings.TrimSpace(session.Mode), "async") {
		return false, nil
	}

	lastError, err := e.agentTurnJobLastError(ctx, *currentJobID)
	if err != nil {
		return false, err
	}
	if !strings.Contains(strings.ToLower(strings.TrimSpace(lastError)), "bounded size policy") {
		return false, nil
	}

	progress, err := e.loadProjectBootstrapProgress(ctx, session.ScopeID)
	if err != nil {
		return false, err
	}
	if !projectBootstrapSetupPersisted(progress) || !e.projectBootstrapRuntimeManaged(ctx, session, messageID) {
		return false, nil
	}
	normalizeProjectBootstrapValidationFailure(&progress, false)

	state := projectBootstrapStateFromMetadata(session.Metadata)
	now := e.now().UTC()
	if state.StartedAt == nil {
		state.StartedAt = &now
	}
	state.Status = projectBootstrapStatusActive
	state.BootstrapTaskID = progress.BootstrapTaskID.String()
	state.BootstrapTaskOutstanding = progress.BootstrapTaskOutstanding
	state.LastTurnID = turn.ID.String()
	if agentID != uuid.Nil {
		state.LastResponderID = agentID.String()
	}
	state.AutoTurnCount++
	applyProjectBootstrapProgressState(&state, progress)
	state.ValidationStatus = ""
	state.ValidationFailureClass = ""
	state.ValidationFailureReason = ""
	state.UpdatedAt = &now
	state.CompletedAt = nil
	state.FailedAt = nil
	state.FailureCategory = ""
	state.FailureClass = ""
	state.FailurePhase = ""
	state.FailureReason = ""
	state.ProviderFailureClass = ""
	state.ProviderFailureReason = ""
	if state.AutoTurnCount >= maxProjectBootstrapAutoTurns {
		return false, nil
	}

	if err := e.updateProjectBootstrapState(ctx, session, state); err != nil {
		return false, err
	}

	continuationAgentID := e.projectBootstrapContinuationAgent(ctx, session, agentID)
	continuationMessage, err := e.appendProjectBootstrapRecoveryContinuationMessage(ctx, session.ID, continuationAgentID, state.InitialMessageID, state.AutoTurnCount, progress)
	if err != nil {
		return false, err
	}
	nextPayload := AgentTurnPayload{
		SessionID: session.ID,
		MessageID: continuationMessage.ID,
	}
	if continuationAgentID != uuid.Nil {
		nextPayload.AgentID = &continuationAgentID
	}
	runAfter := now.Add(defaultAutoContinueDelay)
	enqueued, err := e.enqueueAgentTurnIfActive(ctx, session, nextPayload, &runAfter)
	if err != nil {
		return false, err
	}
	if !enqueued {
		return false, nil
	}
	_, _ = e.appendSystemMessage(ctx, turn.ID, session.ID, "[Recovered bounded bootstrap retry into a validation continuation turn.]")
	return true, nil
}

func (e *TurnEngine) failRetriedLeakedTurn(ctx context.Context, turnID uuid.UUID, failureReason string) error {
	if e == nil || e.chat == nil || e.pool == nil || turnID == uuid.Nil {
		return nil
	}
	failed, err := e.chat.GetTurn(ctx, turnID)
	if err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(failed.Status), "in_progress") {
		if err := e.chat.FailTurn(ctx, turnID, failureReason); err != nil && !errors.Is(err, chat.ErrInvalidStatusTransition) {
			return err
		}
	}
	var cleanupErr error
	if e.invocations != nil {
		session, sessionErr := e.chat.GetSession(ctx, failed.SessionID)
		if sessionErr != nil {
			cleanupErr = errors.Join(cleanupErr, sessionErr)
		} else {
			invocations, listErr := repo.NewModelInvocationRepo(e.pool).ListBySession(ctx, session.OrganizationID, failed.SessionID)
			if listErr != nil {
				cleanupErr = errors.Join(cleanupErr, listErr)
			} else {
				errorCode := stringPtr("stale_turn_recovered")
				errorText := stringPtr(failureReason)
				for _, invocation := range invocations {
					if invocation.TurnID == nil || *invocation.TurnID != turnID {
						continue
					}
					if !strings.EqualFold(strings.TrimSpace(invocation.Status), "in_flight") {
						continue
					}
					if _, updateErr := e.invocations.UpdateStatus(ctx, invocation.ID, "failed", errorCode, errorText); updateErr != nil {
						cleanupErr = errors.Join(cleanupErr, updateErr)
					}
				}
			}
		}
	}
	if e.messages != nil {
		messages, listErr := repo.NewChatMessageRepo(e.pool).ListBySession(ctx, failed.SessionID)
		if listErr != nil {
			cleanupErr = errors.Join(cleanupErr, listErr)
		} else {
			for _, message := range messages {
				if message.TurnID == nil || *message.TurnID != turnID {
					continue
				}
				if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") {
					continue
				}
				status := strings.ToLower(strings.TrimSpace(message.Status))
				if status != "pending" && status != "streaming" {
					continue
				}
				if _, updateErr := e.messages.UpdateStatus(ctx, message.ID, "failed", failureReason); updateErr != nil {
					cleanupErr = errors.Join(cleanupErr, updateErr)
				}
			}
		}
	}
	updated, err := e.chat.GetTurn(ctx, turnID)
	if err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(updated.Status), "in_progress") {
		return fmt.Errorf("stale retry turn remained in_progress after recovery fail request (turn_id=%s)", turnID)
	}
	if cleanupErr != nil && e.logger != nil {
		e.logger.Warn("stale turn recovery cleanup was only partially successful",
			"turn_id", turnID,
			"session_id", failed.SessionID,
			"error", cleanupErr,
		)
	}
	return nil
}

func (e *TurnEngine) agentTurnJobAttempts(ctx context.Context, jobID uuid.UUID) (int, error) {
	if e == nil || e.pool == nil || jobID == uuid.Nil {
		return 0, nil
	}
	var attempts int
	if err := e.pool.QueryRow(ctx, `
		SELECT attempts
		FROM job_queue
		WHERE id = $1
		  AND job_type = $2
	`, jobID, AgentTurnJobType).Scan(&attempts); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return attempts, nil
}

func (e *TurnEngine) agentTurnJobLastError(ctx context.Context, jobID uuid.UUID) (string, error) {
	if e == nil || e.pool == nil || jobID == uuid.Nil {
		return "", nil
	}
	var lastError string
	if err := e.pool.QueryRow(ctx, `
		SELECT COALESCE(last_error, '')
		FROM job_queue
		WHERE id = $1
		  AND job_type = $2
	`, jobID, AgentTurnJobType).Scan(&lastError); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(lastError), nil
}

func (e *TurnEngine) ensureTurnRunExitInvariant(ctx context.Context, rt *turnRuntime) error {
	if e == nil || e.chat == nil || rt == nil || rt.turn == nil {
		return nil
	}
	currentTurn, err := e.chat.GetTurn(ctx, rt.turn.ID)
	if err != nil {
		return err
	}
	rt.turn = currentTurn
	if !strings.EqualFold(strings.TrimSpace(currentTurn.Status), "in_progress") {
		return nil
	}
	stopReason := ""
	if currentTurn.StopReason != nil {
		stopReason = strings.TrimSpace(*currentTurn.StopReason)
	}
	if recovered, recoverErr := e.recoverLeakedAsyncContinuationTurn(ctx, rt, currentTurn, stopReason); recoverErr != nil {
		return recoverErr
	} else if recovered {
		return nil
	}
	return fmt.Errorf("turn leaked in_progress after run exit (session_id=%s turn_id=%s stop_reason=%s)", rt.session.ID, currentTurn.ID, stopReason)
}

func (e *TurnEngine) recoverLeakedAsyncContinuationTurn(ctx context.Context, rt *turnRuntime, currentTurn *chat.ChatTurn, stopReason string) (bool, error) {
	if e == nil || e.chat == nil || rt == nil || rt.session == nil || currentTurn == nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.Mode), "async") {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(stopReason), stopReasonMaxToolCalls) {
		return false, nil
	}
	failureReason := "recovered leaked in-progress turn after max-tool-calls continuation handoff"
	if err := e.chat.FailTurn(ctx, currentTurn.ID, failureReason); err != nil && !errors.Is(err, chat.ErrInvalidStatusTransition) {
		return false, err
	}
	if _, err := e.appendSystemMessage(ctx, currentTurn.ID, rt.session.ID, "[Recovered leaked in-progress turn after max-tool-calls handoff - allowing queued continuation to proceed.]"); err != nil {
		return false, err
	}
	return true, nil
}

func (e *TurnEngine) handleAsyncTurnWatchdogTimeout(ctx context.Context, rt *turnRuntime, timeoutErr *asyncTurnTimeoutError) (bool, error) {
	if rt == nil || rt.turn == nil || rt.session == nil {
		return false, nil
	}
	rt.stopReason = stopReasonMaxDuration
	if err := e.recordStopReason(ctx, rt); err != nil {
		return true, err
	}
	if failErr := e.chat.FailTurn(ctx, rt.turn.ID, timeoutErr.Error()); failErr != nil && !errors.Is(failErr, chat.ErrInvalidStatusTransition) {
		return true, failErr
	}
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, fmt.Sprintf("[Turn failed: %s]", timeoutErr.Error())); err != nil {
		return true, err
	}
	if err := e.pauseProjectAfterExecutionFailure(ctx, rt, timeoutErr); err != nil {
		return true, err
	}
	return true, nil
}

func (e *TurnEngine) handleProjectBootstrapPreflight(ctx context.Context, rt *turnRuntime) (bool, error) {
	if rt == nil || rt.turn == nil || rt.session == nil || rt.session.ScopeID == uuid.Nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") || !strings.EqualFold(strings.TrimSpace(rt.session.Mode), "async") {
		return false, nil
	}

	progress, err := e.loadProjectBootstrapProgress(ctx, rt.session.ScopeID)
	if err != nil {
		return false, err
	}
	if !e.projectBootstrapRuntimeManaged(ctx, rt.session, rt.initialMessageID) || progress.Materialized() {
		return false, nil
	}

	progress, err = e.ensureProjectBootstrapFirstWaveExecution(ctx, progress)
	if err != nil {
		return false, err
	}
	if !progress.ValidationFailed() {
		return false, nil
	}
	if e.projectBootstrapAutoContinueMessage(ctx, rt.initialMessageID) && projectBootstrapRecoverableMaxToolCallFailure(progress) {
		return false, nil
	}
	deferFailure, err := e.shouldDeferRecoverableProjectBootstrapValidation(ctx, rt.session, rt.turn, progress)
	if err != nil {
		return false, err
	}
	if deferFailure {
		return false, nil
	}
	if handled, recoverErr := e.continueRecoverableProjectBootstrapValidation(ctx, rt, projectBootstrapStateFromMetadata(rt.session.Metadata), progress, e.now().UTC(), true); recoverErr != nil {
		return true, recoverErr
	} else if handled {
		return true, nil
	}
	if err := e.failProjectBootstrapValidation(ctx, rt, progress, e.now().UTC()); err != nil {
		return true, err
	}
	return true, nil
}

func (e *TurnEngine) projectBootstrapAutoContinueMessage(ctx context.Context, messageID uuid.UUID) bool {
	if e == nil || e.messages == nil || messageID == uuid.Nil {
		return false
	}
	message, err := e.messages.GetByID(ctx, messageID)
	if err != nil {
		return false
	}
	metadata := messageMetadataMap(message.Metadata)
	if !strings.EqualFold(strings.TrimSpace(stringValue(metadata["source"])), projectBootstrapSource) {
		return false
	}
	raw, ok := metadata["auto_continue"].(bool)
	return ok && raw
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
		if handled, handleErr := e.handleProjectBootstrapUnhandledFailure(ctx, runtime, cause); handleErr != nil {
			return true, handleErr
		} else if handled {
			return true, nil
		}
		_, _ = e.appendSystemMessage(ctx, runtime.turn.ID, runtime.session.ID, fmt.Sprintf("[Turn failed: model retries exhausted after %d attempts.]", maxRateLimitRetries))
		if err := e.pauseProjectAfterExecutionFailure(ctx, runtime, cause); err != nil {
			return true, err
		}
		return true, nil
	}

	nextPayload := AgentTurnPayload{
		SessionID:              runtime.session.ID,
		MessageID:              messageID,
		RetryCount:             retryCount + 1,
		RateLimitJitterApplied: true,
	}
	if routedAgentID != nil && *routedAgentID != uuid.Nil {
		agentID := *routedAgentID
		nextPayload.AgentID = &agentID
	}

	retryDelay := jitteredRateLimitRetryDelay(
		rateLimitRetryDelay(retryCount, rateLimitRetryAfterHint(cause)),
		runtime.session.ID,
		messageID,
		retryCount,
	)
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

func (e *TurnEngine) handleTransientInfrastructureTurnFailure(
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
	if retryCount >= maxTransientInfraRetries {
		_ = e.chat.FailTurn(ctx, runtime.turn.ID, summarizeFailure(cause))
		_, _ = e.appendSystemMessage(ctx, runtime.turn.ID, runtime.session.ID, fmt.Sprintf("[Turn failed: temporary infrastructure retries exhausted after %d attempts.]", maxTransientInfraRetries))
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

	retryDelay := transientInfrastructureRetryDelay(retryCount)
	runAfter := e.now().Add(retryDelay).UTC()
	enqueued, err := e.enqueueAgentTurnIfActive(ctx, runtime.session, nextPayload, &runAfter)
	if err != nil {
		return false, fmt.Errorf("enqueue transient infrastructure retry: %w", err)
	}

	_ = e.chat.FailTurn(ctx, runtime.turn.ID, summarizeFailure(cause))
	message := fmt.Sprintf("[Infrastructure temporarily unavailable, retrying in %s...]", formatRetryDelay(retryDelay))
	if !enqueued {
		message = "[Project paused - retry deferred until resume.]"
	}
	_, _ = e.appendSystemMessage(ctx, runtime.turn.ID, runtime.session.ID, message)
	return true, nil
}

func (e *TurnEngine) handleTaskContinuationDepthTurnFailure(
	ctx context.Context,
	runtime *turnRuntime,
	messageID uuid.UUID,
	routedAgentID *uuid.UUID,
	cause error,
) (bool, error) {
	if runtime == nil || runtime.turn == nil || runtime.session == nil {
		return false, nil
	}
	if !isRecoverableExecutionContinuationDepthError(cause) {
		return false, nil
	}
	if runtime.recoveryTurn || !shouldAppendTaskContinuationActionPrompt(runtime.session) {
		return false, nil
	}

	retryAttempt := e.taskContinuationResumeAttempt(ctx, messageID) + 1
	actionMessage, err := e.chat.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: runtime.session.ID,
		TurnID:    &runtime.turn.ID,
		Role:      "user",
		Content:   buildTaskContinuationActionPrompt(""),
		Metadata:  taskContinuationResumeMessageMetadata(runtime.session, retryAttempt),
	})
	if err != nil {
		return true, err
	}

	nextPayload := AgentTurnPayload{
		SessionID: runtime.session.ID,
		MessageID: actionMessage.ID,
	}
	if routedAgentID != nil && *routedAgentID != uuid.Nil {
		agentID := *routedAgentID
		nextPayload.AgentID = &agentID
	}

	retryDelay := defaultAutoContinueDelay
	runAfter := e.now().Add(retryDelay).UTC()
	enqueued, enqueueErr := e.enqueueAgentTurnIfActive(ctx, runtime.session, nextPayload, &runAfter)
	if enqueueErr != nil {
		return true, fmt.Errorf("enqueue task continuation depth retry: %w", enqueueErr)
	}

	_ = e.chat.FailTurn(ctx, runtime.turn.ID, summarizeFailure(cause))
	message := fmt.Sprintf("[Task continuation remained too large after %d continuation turns - retrying from a narrowed continuation root in %s.]", maxContinuationTurnDepth, formatRetryDelay(retryDelay))
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
	if rt != nil && rt.historyStartID == nil && e.projectBootstrapAutoContinueMessage(ctx, rt.initialMessageID) {
		if _, err := e.appendProjectBootstrapResumeState(ctx, rt); err != nil {
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
				if handled, handleErr := e.handleProjectBootstrapGuardrailFailure(ctx, rt, buildRecoveryContinuationDepthReason("prompt context remained too large")); handled {
					return handleErr
				}
				if handled, handleErr := e.handleRecoveryContinuationDepthBlocker(ctx, rt, buildRecoveryContinuationDepthReason("prompt context remained too large")); handled {
					return handleErr
				}
				return errContextCompressionContinuationDepthExceeded
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
				if handled, handleErr := e.handleProjectBootstrapGuardrailFailure(ctx, rt, buildRecoveryContinuationDepthReason(fmt.Sprintf("prompt input kept exceeding the %d-token guardrail", guardrail))); handled {
					return handleErr
				}
				if handled, handleErr := e.handleRecoveryContinuationDepthBlocker(ctx, rt, buildRecoveryContinuationDepthReason(fmt.Sprintf("prompt input kept exceeding the %d-token guardrail", guardrail))); handled {
					return handleErr
				}
				return errAgentTurnPromptGuardrailDepthExceeded
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
				_, _ = e.enqueuer.Enqueue(ctx, nil, chat.ChatSummarizeJobType, backgroundSummarizeJobPriority, chat.ChatSummarizePayload{SessionID: rt.session.ID, LayerBudgetTokens: defaultSummarizeLayerBudget}, nil)
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
			if errors.Is(err, context.Canceled) && ctx.Err() != nil {
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
			if _, err := e.autoPersistBootstrapSetupFromWorkspace(ctx, rt); err != nil {
				return fmt.Errorf("auto persist bootstrap setup: %w", err)
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
			if errors.Is(dispatchErr, context.Canceled) && ctx.Err() != nil {
				return e.handleCancellation(ctx, rt)
			}
			return fmt.Errorf("dispatchTools: %w", dispatchErr)
		}
		if stop {
			if currentTurn, getErr := e.chat.GetTurn(ctx, rt.turn.ID); getErr == nil {
				rt.turn = currentTurn
				if !strings.EqualFold(strings.TrimSpace(currentTurn.Status), "in_progress") {
					return e.ensureRecoveryTurnDurableTaskState(ctx, rt)
				}
			}
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
			if current, getErr := e.chat.GetTurn(ctx, rt.turn.ID); getErr == nil &&
				isTerminalTurnStatus(current.Status) {
				e.logger.Warn("completeTurn no-op for stale terminal turn",
					"session_id", rt.session.ID,
					"turn_id", rt.turn.ID,
					"turn_status", current.Status,
				)
				rt.turn = current
				return e.ensureRecoveryTurnDurableTaskState(ctx, rt)
			}
		}
		return e.describeTurnTransitionError(ctx, rt.turn.ID, "completeTurn CompleteTurn", "in_progress->completed", err)
	}
	if err := e.ensureProjectKickoffHandoff(ctx, rt); err != nil {
		return err
	}
	return e.ensureRecoveryTurnDurableTaskState(ctx, rt)
}

func (e *TurnEngine) ensureProjectKickoffHandoff(ctx context.Context, rt *turnRuntime) error {
	if e == nil || e.chat == nil || e.messages == nil || rt == nil || rt.session == nil || rt.projectIdentity == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "organization") {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.agent.DisplayName), "frank") && !strings.EqualFold(strings.TrimSpace(rt.agent.AgentType), "general") {
		return nil
	}

	projectSession, err := e.chat.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: rt.session.OrganizationID,
		ScopeType:      "project",
		ScopeID:        rt.projectIdentity.id,
		Mode:           "async",
	})
	if err != nil {
		return err
	}
	if _, err := e.chat.GetSession(ctx, projectSession.ID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		return err
	}
	if err := e.ensureAgentParticipant(ctx, projectSession.ID, rt.agent.ID); err != nil {
		return err
	}
	loriID, err := e.resolveLoriStarterID(ctx, rt.session.OrganizationID)
	if err == nil && loriID != uuid.Nil {
		if ensureErr := e.ensureAgentParticipant(ctx, projectSession.ID, loriID); ensureErr != nil {
			return ensureErr
		}
	}

	projectMessages, err := e.messages.ListBySession(ctx, projectSession.ID)
	if err != nil {
		return err
	}
	originatingRequest := ""
	if rt.initialMessageID != uuid.Nil {
		if source, err := e.messages.GetByID(ctx, rt.initialMessageID); err == nil && strings.EqualFold(strings.TrimSpace(source.Role), "user") {
			originatingRequest = normalizeInstructionText(source.Content)
		}
	}
	for _, message := range projectMessages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "user") && projectKickoffHandoffCarriesOriginatingContext(message.Content, originatingRequest) {
			return nil
		}
	}

	handoff, err := e.chat.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  projectSession.ID,
		AuthorType: stringPtr("agent"),
		AuthorID:   &rt.agent.ID,
		Role:       "user",
		Content:    e.buildSyntheticProjectKickoffHandoff(ctx, rt),
	})
	if err != nil {
		return err
	}
	now := e.now().UTC()
	state := projectBootstrapStateFromMetadata(projectSession.Metadata)
	state.Status = projectBootstrapStatusActive
	state.CurrentPhase = "kickoff_handoff"
	state.InitialMessageID = projectBootstrapWorkflowMessageID(handoff).String()
	state.LastTurnID = ""
	state.AutoTurnCount = 0
	state.StartedAt = &now
	state.UpdatedAt = &now
	state.CompletedAt = nil
	state.FailedAt = nil
	state.FailureCategory = ""
	state.FailureClass = ""
	state.FailurePhase = ""
	state.FailureReason = ""
	state.ProviderFailureClass = ""
	state.ProviderFailureReason = ""
	if err := e.updateProjectBootstrapState(ctx, projectSession, state); err != nil {
		return err
	}
	if loriID == uuid.Nil {
		return nil
	}
	_, err = e.enqueueAgentTurnIfActive(ctx, projectSession, AgentTurnPayload{
		SessionID: projectSession.ID,
		MessageID: handoff.ID,
		AgentID:   &loriID,
	}, nil)
	return err
}

func projectKickoffHandoffCarriesOriginatingContext(content, originatingRequest string) bool {
	trimmedContent := strings.TrimSpace(content)
	if trimmedContent == "" {
		return false
	}
	trimmedRequest := strings.TrimSpace(originatingRequest)
	if trimmedRequest == "" {
		return true
	}
	lowerContent := strings.ToLower(normalizeInstructionText(trimmedContent))
	lowerRequest := strings.ToLower(normalizeInstructionText(trimmedRequest))
	if strings.Contains(lowerContent, "originating user request:") {
		return true
	}
	return strings.Contains(lowerContent, lowerRequest)
}

func (e *TurnEngine) buildSyntheticProjectKickoffHandoff(ctx context.Context, rt *turnRuntime) string {
	if rt == nil || rt.projectIdentity == nil {
		return "Frank handoff: create the initial staffed work plan for this project."
	}
	originatingRequest := ""
	if e != nil && e.messages != nil && rt.initialMessageID != uuid.Nil {
		if source, err := e.messages.GetByID(ctx, rt.initialMessageID); err == nil && strings.EqualFold(strings.TrimSpace(source.Role), "user") {
			originatingRequest = normalizeInstructionText(source.Content)
		}
	}
	lines := []string{
		"Frank handoff: create the initial staffed bootstrap for this project.",
		fmt.Sprintf("Created project: slug=%s project_id=%s.", strings.TrimSpace(rt.projectIdentity.slug), rt.projectIdentity.id),
	}
	if originatingRequest != "" {
		lines = append(lines, fmt.Sprintf("Originating user request: %s", originatingRequest))
	}
	lines = append(lines, "Treat this as a fresh project bootstrap. Do not assume architecture, CMS choice, or workflow from archived/restart chains or org memory unless the originating user request explicitly asks for reuse. Prefer the current project description and live tool results over prior-project memory.")
	lines = append(lines, "Do not call memory.query, memory.list, or other memory tools during this bootstrap handoff unless the originating user request explicitly asks to reuse prior project work.")
	lines = append(lines, "Frank, Lori, and Ellie are starter-trio governance agents for setup/review only, not project staff. Do not assign them to project roles; create or assign dedicated project staff instead.")
	lines = append(lines, "Keep staffing discovery bounded. Use at most one staffing.browse_profiles pass per needed category and at most one staffing.get_profile call per candidate you actually intend to staff. Once you can name one staff PM, the concrete workers, and the needed reviewers, stop browsing profiles and persist staffing. Do not spend multiple rounds re-browsing similar profiles when the current candidates are already sufficient to act.")
	lines = append(lines, "Do not spend a turn writing a staffing plan, rationale memo, or markdown table before you materialize staff. As soon as you have enough candidates, create the PM/workers/reviewers, assign them to the project, and continue bootstrap.")
	lines = append(lines, "Once enough candidates are known, do not emit another assistant planning summary about staffing. Your next step should be the concrete agent.create_staff and assignment tool calls needed to materialize the roster.")
	lines = append(lines, "Fresh bootstrap staffing must materially advance in this turn. Do not spend the turn narrating profile selection or re-listing role constraints. After the first viable PM, worker, and reviewer candidates are identified, the same turn must create and assign them before any further bootstrap narration.")
	lines = append(lines, "Do not interleave extra assistant summaries between staffing lookups. Use the tool results to choose candidates, then go straight to agent.create_staff plus project assignment mutations in the same turn.")
	lines = append(lines, "Use the canonical bootstrap workflow: staff the project, create bounded tasks/subtasks, attach runnable flows, and move the first executable wave into execution.")
	lines = append(lines, "If you need project docs, files, planning artifacts, or current task state during bootstrap, inspect them directly with tools. Do not ask the operator to go read docs, restate accessible context, or manually restart the same bootstrap step for you.")
	lines = append(lines, "When you start task decomposition, do not stop after creating broad parent workstreams or after decomposing only one of them. Before you pause to read scaffold artifacts or persist setup, every persisted broad workstream parent must either have bounded executable child tasks under it or be replaced by those bounded children directly. Do not leave other broad parents untouched while you deepen only one workstream.")
	return strings.Join(lines, "\n\n")
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
	if strings.EqualFold(strings.TrimSpace(currentTurn.Status), "in_progress") {
		if recovered, recoverErr := e.recoverLeakedAsyncContinuationTurn(ctx, rt, currentTurn, rt.stopReason); recoverErr != nil {
			return recoverErr
		} else if !recovered {
			return fmt.Errorf("continueTurn left prior turn in_progress after completion handoff (session_id=%s turn_id=%s stop_reason=%s)", rt.session.ID, currentTurn.ID, strings.TrimSpace(rt.stopReason))
		}
		currentTurn, err = e.chat.GetTurn(ctx, rt.turn.ID)
		if err != nil {
			return err
		}
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
	refreshedSession, err := e.chat.GetSession(ctx, rt.session.ID)
	if err != nil {
		return err
	}
	rt.session = refreshedSession
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
	if resumed, err := e.appendProjectBootstrapResumeState(ctx, rt); err != nil {
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
	summary = compactContinuationSummary(summary)
	if shouldAppendTaskContinuationActionPrompt(rt.session) && continuationSummaryLooksUnavailable(summary) {
		summary = taskExecutionContinuationFallbackSummary()
	}
	if shouldAppendProjectContinuationActionPrompt(rt.session) && continuationSummaryLooksUnavailable(summary) {
		summary = projectExecutionContinuationFallbackSummary()
	}
	message, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, "[Continuation summary] "+summary)
	if err != nil {
		return err
	}
	if shouldAppendTaskContinuationActionPrompt(rt.session) {
		shouldAppend, appendErr := e.shouldAppendSyntheticUserPrompt(ctx, rt.session.ID, taskContinuationResumeMessageSource)
		if appendErr != nil {
			return appendErr
		}
		if shouldAppend {
			if _, err := e.chat.AppendMessage(ctx, chat.AppendMessageInput{
				SessionID: rt.session.ID,
				TurnID:    &rt.turn.ID,
				Role:      "user",
				Content:   buildTaskContinuationActionPrompt(summary),
				Metadata:  syntheticContinuationActionMessageMetadata(rt.session, taskContinuationResumeMessageSource),
			}); err != nil {
				return err
			}
		}
	}
	if shouldAppendProjectContinuationActionPrompt(rt.session) {
		shouldAppend, appendErr := e.shouldAppendSyntheticUserPrompt(ctx, rt.session.ID, "project_continuation_resume")
		if appendErr != nil {
			return appendErr
		}
		if shouldAppend {
			if _, err := e.chat.AppendMessage(ctx, chat.AppendMessageInput{
				SessionID: rt.session.ID,
				TurnID:    &rt.turn.ID,
				Role:      "user",
				Content:   buildProjectContinuationActionPrompt(summary),
				Metadata:  syntheticContinuationActionMessageMetadata(rt.session, "project_continuation_resume"),
			}); err != nil {
				return err
			}
		}
	}
	rt.historyStartID = &message.ID
	if err := e.persistTurnHistoryStart(ctx, rt, message.ID); err != nil {
		return err
	}
	return nil
}

func (e *TurnEngine) persistTurnHistoryStart(ctx context.Context, rt *turnRuntime, messageID uuid.UUID) error {
	if e == nil || e.turns == nil || rt == nil || rt.turn == nil || messageID == uuid.Nil {
		return nil
	}
	updated, err := e.turns.SetTriggerMessageID(ctx, rt.turn.ID, &messageID)
	if err != nil {
		return err
	}
	rt.turn = &updated
	return nil
}

func compactContinuationSummary(summary string) string {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return "Continuation summary unavailable."
	}
	if continuationSummaryLooksUnavailable(trimmed) {
		return "Continuation summary unavailable."
	}
	if len(trimmed) <= maxContinuationSummaryChars {
		return trimmed
	}
	cut := trimmed[:maxContinuationSummaryChars]
	if idx := strings.LastIndex(cut, "\n"); idx >= maxContinuationSummaryChars/2 {
		cut = cut[:idx]
	}
	cut = strings.TrimSpace(cut)
	if cut == "" {
		return "Continuation summary unavailable."
	}
	return cut + "\n[Summary truncated]"
}

func continuationSummaryLooksUnavailable(summary string) bool {
	normalized := strings.ToLower(strings.TrimSpace(summary))
	if normalized == "" {
		return true
	}
	patterns := []string{
		"continuation summary unavailable",
		"i don't have a continuation summary",
		"i do not have a continuation summary",
		"i don't have context",
		"i do not have context",
		"i don't have prior context",
		"i do not have prior context",
		"please provide the task details",
		"please provide: 1.",
		"what task were we working on",
		"what specific task was previously in progress",
	}
	for _, pattern := range patterns {
		if strings.Contains(normalized, pattern) {
			return true
		}
	}
	return false
}

func (e *TurnEngine) appendProjectBootstrapResumeState(ctx context.Context, rt *turnRuntime) (bool, error) {
	if rt == nil || rt.turn == nil || rt.session == nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") {
		return false, nil
	}
	state := projectBootstrapStateFromMetadata(rt.session.Metadata)
	if progress, err := e.loadProjectBootstrapProgress(ctx, rt.session.ScopeID); err != nil {
		return false, err
	} else {
		if !projectBootstrapStateActive(state) {
			if strings.EqualFold(strings.TrimSpace(state.Status), projectBootstrapStatusFailed) || progress.Materialized() || !e.projectBootstrapRuntimeManaged(ctx, rt.session, rt.initialMessageID) {
				return false, nil
			}
			state.Status = projectBootstrapStatusActive
			state.InitialMessageID = strings.TrimSpace(state.InitialMessageID)
			if state.InitialMessageID == "" && rt.initialMessageID != uuid.Nil {
				state.InitialMessageID = rt.initialMessageID.String()
			}
		}
		applyProjectBootstrapProgressState(&state, progress)
	}
	snapshot, err := e.loadProjectBootstrapResumeSnapshot(ctx, rt.session.ScopeID, state)
	if err != nil {
		return false, err
	}
	message, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildProjectBootstrapResumeStateMessage(state, snapshot))
	if err != nil {
		return false, err
	}
	if compactAction, ok, err := e.appendProjectBootstrapResumeActionPrompt(ctx, rt, state); err != nil {
		return false, err
	} else if ok {
		rt.historyStartID = &message.ID
		if err := e.persistTurnHistoryStart(ctx, rt, message.ID); err != nil {
			return false, err
		}
		if compactAction != nil && compactAction.ID != uuid.Nil {
			rt.initialMessageID = compactAction.ID
		}
		return true, nil
	}
	if projectBootstrapResumeShouldRootAtResumeMessage(state) {
		rt.historyStartID = &message.ID
		if err := e.persistTurnHistoryStart(ctx, rt, message.ID); err != nil {
			return false, err
		}
		return true, nil
	}
	if rt.initialMessageID != uuid.Nil {
		initial := rt.initialMessageID
		rt.historyStartID = &initial
		if err := e.persistTurnHistoryStart(ctx, rt, initial); err != nil {
			return false, err
		}
		return true, nil
	}
	rt.historyStartID = &message.ID
	if err := e.persistTurnHistoryStart(ctx, rt, message.ID); err != nil {
		return false, err
	}
	return true, nil
}

func (e *TurnEngine) appendProjectBootstrapResumeActionPrompt(ctx context.Context, rt *turnRuntime, state projectBootstrapState) (*chat.ChatMessage, bool, error) {
	if e == nil || e.messages == nil || rt == nil || rt.initialMessageID == uuid.Nil {
		return nil, false, nil
	}
	message, err := e.messages.GetByID(ctx, rt.initialMessageID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
		return nil, false, nil
	}
	metadata := messageMetadataMap(message.Metadata)
	if !strings.EqualFold(strings.TrimSpace(stringValue(metadata["source"])), projectBootstrapSource) {
		return nil, false, nil
	}
	raw, ok := metadata["auto_continue"].(bool)
	if !ok || !raw || !projectBootstrapResumeShouldRootAtResumeMessage(state) {
		return nil, false, nil
	}
	initialMessageID := stringValue(metadata["bootstrap_initial_message_id"])
	if strings.TrimSpace(initialMessageID) == "" {
		initialMessageID = projectBootstrapWorkflowMessageID(&repo.ChatMessage{ID: message.ID, Metadata: message.Metadata}).String()
	}
	compacted, err := e.appendProjectBootstrapContinuationMessageWithContent(
		ctx,
		rt.session.ID,
		rt.agent.ID,
		initialMessageID,
		state.AutoTurnCount,
		buildProjectBootstrapResumeActionPrompt(state),
	)
	if err != nil {
		return nil, false, err
	}
	return compacted, true, nil
}

type projectBootstrapResumeSnapshot struct {
	ProjectID      string
	ProjectSlug    string
	ExistingPM     string
	AssignmentLine string
	FailedTaskLine string
	RepairTaskLine string
}

var projectBootstrapFailureTaskNumberPattern = regexp.MustCompile(`(?:first-wave )?task ([0-9]+)`)
var projectBootstrapFailureTaskTitlePattern = regexp.MustCompile(`(?:first-wave )?task [0-9]+ \(([^)]+)\)`)

func formatBootstrapResumeAgentLabel(agentRecord repo.Agent) string {
	name := strings.TrimSpace(agentRecord.DisplayName)
	if name == "" {
		return ""
	}
	parts := []string{}
	if agentID := strings.TrimSpace(agentRecord.ID.String()); agentID != "" {
		parts = append(parts, "id="+agentID)
	}
	if class := strings.TrimSpace(agentRecord.AgentClass); class != "" {
		parts = append(parts, "class="+class)
	}
	if agentType := strings.TrimSpace(agentRecord.AgentType); agentType != "" {
		parts = append(parts, "type="+agentType)
	}
	if len(parts) == 0 {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, strings.Join(parts, ", "))
}

func projectBootstrapFailureTaskNumber(reason string) int {
	matches := projectBootstrapFailureTaskNumberPattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(reason)))
	if len(matches) < 2 {
		return 0
	}
	number, err := strconv.Atoi(strings.TrimSpace(matches[1]))
	if err != nil || number <= 0 {
		return 0
	}
	return number
}

func projectBootstrapFailureTaskTitle(reason string) string {
	matches := projectBootstrapFailureTaskTitlePattern.FindStringSubmatch(strings.TrimSpace(reason))
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func projectBootstrapBlockedRecoveryFailure(messages []repo.ChatMessage, state projectBootstrapState) (string, string) {
	reason := strings.TrimSpace(state.ValidationFailureReason)
	if reason == "" {
		for i := len(messages) - 1; i >= 0; i-- {
			message := messages[i]
			if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
				continue
			}
			reason = projectBootstrapRecoveryTargetFromMessage(message.Content)
			if reason != "" {
				break
			}
		}
	}
	if reason == "" {
		return "", ""
	}
	return reason, projectBootstrapFailureClassForReason(state.ValidationFailureClass, reason)
}

func projectBootstrapAckOnlyReply(assistant *repo.ChatMessage) bool {
	if assistant == nil {
		return false
	}
	content := strings.ToLower(strings.TrimSpace(assistant.Content))
	switch content {
	case "acknowledged.", "acknowledged", "understood.", "understood":
		return true
	}
	return false
}

func projectBootstrapAckOnlyRecoveryReply(messages []repo.ChatMessage, assistant *repo.ChatMessage) bool {
	if !projectBootstrapAckOnlyReply(assistant) {
		return false
	}
	return projectBootstrapRecoveryTurn(messages)
}

func projectBootstrapNarrativeOnlyReply(messages []repo.ChatMessage, assistant *repo.ChatMessage) bool {
	if assistant == nil {
		return false
	}
	if projectBootstrapAckOnlyReply(assistant) {
		return true
	}
	if projectBootstrapReplyUsedTools(messages) {
		return false
	}
	content := strings.ToLower(strings.TrimSpace(assistant.Content))
	if content == "" {
		return true
	}
	for _, marker := range []string{"cannot", "can't", "unable", "blocker", "blocked", "failed", "failure", "error"} {
		if strings.Contains(content, marker) {
			return false
		}
	}
	return strings.Contains(content, "from memory") ||
		(strings.Contains(content, "i recall") && strings.Contains(content, "memory")) ||
		(strings.Contains(content, "recall") && strings.Contains(content, "prior")) ||
		(strings.Contains(content, "review") && strings.Contains(content, "memory"))
}

func projectBootstrapNarrativeOnlyRecoveryReply(messages []repo.ChatMessage, assistant *repo.ChatMessage) bool {
	if !projectBootstrapRecoveryTurn(messages) {
		return false
	}
	return projectBootstrapNarrativeOnlyReply(messages, assistant)
}

func projectBootstrapRecoveryTurn(messages []repo.ChatMessage) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			continue
		}
		return strings.TrimSpace(projectBootstrapRecoveryTargetFromMessage(message.Content)) != ""
	}
	return false
}

func projectBootstrapReplyUsedTools(messages []repo.ChatMessage) bool {
	for _, message := range messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "tool") {
			return true
		}
		if message.ToolCallID != nil && strings.TrimSpace(*message.ToolCallID) != "" {
			return true
		}
	}
	return false
}

func buildProjectBootstrapAckOnlyRecoveryFailureReason(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return "kickoff validation failed: automatic bootstrap recovery replied with an acknowledgement only and did not perform the required repair work"
	}
	return fmt.Sprintf(
		"kickoff validation failed: automatic bootstrap recovery replied with an acknowledgement only instead of repairing the requested bootstrap target (%s)",
		target,
	)
}

func buildProjectBootstrapAckOnlyRestartFailureReason() string {
	return "kickoff validation failed: automatic bootstrap restart replied with an acknowledgement only and never persisted staffed executable work"
}

func buildProjectBootstrapNarrativeOnlyRecoveryFailureReason(target string, assistant *repo.ChatMessage) string {
	if projectBootstrapAckOnlyReply(assistant) {
		return buildProjectBootstrapAckOnlyRecoveryFailureReason(target)
	}
	target = strings.TrimSpace(target)
	reply := ""
	if assistant != nil {
		reply = strings.TrimSpace(assistant.Content)
	}
	if target == "" {
		return fmt.Sprintf("kickoff validation failed: automatic bootstrap recovery replied with narrative only instead of performing the required repair work (%s)", reply)
	}
	return fmt.Sprintf("kickoff validation failed: automatic bootstrap recovery replied with narrative only instead of repairing the requested bootstrap target (%s); reply=%q", target, reply)
}

func buildProjectBootstrapNarrativeOnlyRestartFailureReason(assistant *repo.ChatMessage) string {
	if projectBootstrapAckOnlyReply(assistant) {
		return buildProjectBootstrapAckOnlyRestartFailureReason()
	}
	reply := ""
	if assistant != nil {
		reply = strings.TrimSpace(assistant.Content)
	}
	return fmt.Sprintf("kickoff validation failed: automatic bootstrap restart replied with narrative only and never persisted staffed executable work; reply=%q", reply)
}

func projectBootstrapRecoveryTargetFromMessage(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	marker := "recovery target:"
	idx := strings.Index(lower, marker)
	if idx < 0 {
		return ""
	}
	target := strings.TrimSpace(trimmed[idx+len(marker):])
	for _, stop := range []string{"\n", " Do not ", " Repair the named ", " Repair target:", " Current validation failure:"} {
		if cut := strings.Index(target, stop); cut >= 0 {
			target = strings.TrimSpace(target[:cut])
		}
	}
	return strings.TrimSpace(target)
}

func projectBootstrapFailureClassForReason(currentClass, reason string) string {
	if strings.TrimSpace(currentClass) != "" {
		return strings.TrimSpace(currentClass)
	}
	lower := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(lower, "bounded size policy"), strings.Contains(lower, "bounded task-size policy"):
		return projectBootstrapFailureFirstWaveSize
	case strings.Contains(lower, "flow"), strings.Contains(lower, "template"):
		return projectBootstrapFailureFirstWaveFlow
	case strings.Contains(lower, "has no assigned agent"),
		strings.Contains(lower, "queue runnable execution"),
		strings.Contains(lower, "materialized execution"),
		projectBootstrapFailureTaskNumber(reason) > 0:
		return projectBootstrapFailureFirstWaveExecution
	default:
		return ""
	}
}

func formatBootstrapResumeTaskLine(taskRecord repo.ProjectTask) string {
	if taskRecord.ID == uuid.Nil {
		return ""
	}
	assigned := "unassigned"
	if taskRecord.AssignedAgentID != nil && *taskRecord.AssignedAgentID != uuid.Nil {
		assigned = taskRecord.AssignedAgentID.String()
	}
	return fmt.Sprintf(
		"Named blocked task: task %d id=%s title=%q work_status=%s assigned_agent_id=%s. Use task.update directly on this task id instead of task.get with the bare task number.",
		taskRecord.TaskNumber,
		taskRecord.ID.String(),
		strings.TrimSpace(taskRecord.Title),
		strings.TrimSpace(taskRecord.WorkStatus),
		assigned,
	)
}

func buildProjectBootstrapAdditionalRepairTaskLine(progress projectBootstrapProgress) string {
	if strings.TrimSpace(progress.ValidationFailureClass) != projectBootstrapFailureFirstWaveExecution {
		return ""
	}
	blockedTaskNumber := projectBootstrapFailureTaskNumber(progress.ValidationFailureReason)
	if blockedTaskNumber <= 0 {
		return ""
	}
	parts := make([]string, 0, len(progress.FirstWaveTasks))
	for _, task := range progress.FirstWaveTasks {
		if task.TaskNumber == blockedTaskNumber {
			continue
		}
		if task.AssignedAgentID != nil && *task.AssignedAgentID != uuid.Nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("task %d id=%s title=%q", task.TaskNumber, task.ID.String(), strings.TrimSpace(task.Title)))
	}
	if len(parts) == 0 {
		return ""
	}
	line := "Other still-unassigned first-wave tasks you can repair in this same turn without rereading the task tree: "
	return line + strings.Join(parts, "; ") + "."
}

func buildProjectBootstrapCompoundParentRepairTaskLine(tasks []repo.ProjectTask, blockedTask repo.ProjectTask) string {
	if blockedTask.ID == uuid.Nil {
		return ""
	}
	parts := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if task.ID == uuid.Nil || task.ID == blockedTask.ID || task.TaskNumber <= 0 {
			continue
		}
		if taskdecomp.ParseParentTaskID(task.Metadata) != uuid.Nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(task.WorkStatus), "draft") {
			continue
		}
		parts = append(parts, fmt.Sprintf("task %d id=%s title=%q", task.TaskNumber, task.ID.String(), strings.TrimSpace(task.Title)))
	}
	if len(parts) == 0 {
		return ""
	}
	const maxListed = 4
	line := "Other existing top-level draft tasks you can fold under this blocked parent without rereading the task tree: "
	if len(parts) > maxListed {
		return line + strings.Join(parts[:maxListed], "; ") + fmt.Sprintf("; plus %d more top-level draft tasks.", len(parts)-maxListed)
	}
	return line + strings.Join(parts, "; ") + "."
}

func buildProjectBootstrapBoundedSizeRepairTaskLine(blockedTask repo.ProjectTask) string {
	if blockedTask.ID == uuid.Nil {
		return ""
	}
	return fmt.Sprintf(
		"Direct repair for the named oversized task: keep task %d orchestration-only, do not call task.get on task id=%s first, create 2-4 bounded executable child tasks directly beneath task id=%s, keep each child to a single concrete deliverable under 60 minutes, and assign each child to an existing active project assignee before resuming bootstrap.setup.persist.",
		blockedTask.TaskNumber,
		blockedTask.ID.String(),
		blockedTask.ID.String(),
	)
}

func buildProjectBootstrapUnresolvedFailureRepairTaskLine(taskNumber int, title string) string {
	if taskNumber <= 0 {
		return ""
	}
	parts := []string{
		fmt.Sprintf("The current validation failure still names first-wave task %d", taskNumber),
	}
	if trimmedTitle := strings.TrimSpace(title); trimmedTitle != "" {
		parts[len(parts)-1] += fmt.Sprintf(" (%q)", trimmedTitle)
	}
	parts[len(parts)-1] += ", but the exact persisted task id is no longer resolvable from that stale task number/title."
	parts = append(parts, "Do not fabricate a UUID from the bare task number.")
	parts = append(parts, "If prior repair turns already reassigned, renamed, or split that task, call bootstrap.setup.persist with canonical completed_step_slugs from the current persisted state instead of retrying raw task.update against an invented id.")
	parts = append(parts, "Only if bootstrap.setup.persist returns a concrete blocker naming a single current task should you inspect that one task directly.")
	return strings.Join(parts, " ")
}

func (e *TurnEngine) loadProjectBootstrapResumeSnapshot(ctx context.Context, projectID uuid.UUID, state projectBootstrapState) (projectBootstrapResumeSnapshot, error) {
	if e == nil || e.assignments == nil || e.agents == nil || projectID == uuid.Nil {
		return projectBootstrapResumeSnapshot{}, nil
	}
	assignments, err := e.assignments.ListByProject(ctx, projectID)
	if err != nil {
		return projectBootstrapResumeSnapshot{}, err
	}
	if len(assignments) == 0 {
		return projectBootstrapResumeSnapshot{}, nil
	}

	grouped := map[string][]string{}
	for _, assignment := range assignments {
		agentRecord, agentErr := e.agents.GetByID(ctx, assignment.AgentID)
		if agentErr != nil {
			continue
		}
		name := strings.TrimSpace(agentRecord.DisplayName)
		if name == "" {
			continue
		}
		role := assignmentrole.Normalize(assignment.Role)
		if role == "" {
			role = strings.ToLower(strings.TrimSpace(assignment.Role))
		}
		if role == "" {
			role = "worker"
		}
		label := formatBootstrapResumeAgentLabel(agentRecord)
		grouped[role] = append(grouped[role], label)
	}

	for role := range grouped {
		sort.Strings(grouped[role])
	}

	snapshot := projectBootstrapResumeSnapshot{}
	if projectID != uuid.Nil {
		snapshot.ProjectID = projectID.String()
	}
	if e.projects != nil {
		if projectRecord, projectErr := e.projects.GetByID(ctx, projectID); projectErr == nil {
			snapshot.ProjectSlug = strings.TrimSpace(projectRecord.Slug)
		}
	}
	if names := grouped["project_manager"]; len(names) > 0 {
		snapshot.ExistingPM = names[0]
		delete(grouped, "project_manager")
	}

	roleOrder := []string{"worker", "reviewer", "observer"}
	parts := make([]string, 0, len(grouped))
	for _, role := range roleOrder {
		names := grouped[role]
		if len(names) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%ss=%s", role, strings.Join(names, ", ")))
		delete(grouped, role)
	}
	for role, names := range grouped {
		if len(names) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", role, strings.Join(names, ", ")))
	}
	sort.Strings(parts)
	if len(parts) > 0 {
		snapshot.AssignmentLine = strings.Join(parts, "; ")
	}
	if taskNumber := projectBootstrapFailureTaskNumber(state.ValidationFailureReason); taskNumber > 0 && e.tasks != nil {
		failureTitle := projectBootstrapFailureTaskTitle(state.ValidationFailureReason)
		taskRecord, taskErr := e.tasks.GetByProjectAndNumber(ctx, projectID, taskNumber)
		if taskErr == nil && failureTitle != "" && !strings.EqualFold(strings.TrimSpace(taskRecord.Title), failureTitle) {
			taskErr = repo.ErrNotFound
		}
		var allTasks []repo.ProjectTask
		if taskErr != nil || failureTitle != "" {
			if listed, listErr := e.tasks.ListByProject(ctx, projectID); listErr == nil {
				allTasks = listed
				if failureTitle != "" {
					for _, candidate := range allTasks {
						if strings.EqualFold(strings.TrimSpace(candidate.Title), failureTitle) {
							taskRecord = candidate
							taskErr = nil
							break
						}
					}
				}
			}
		}
		if taskErr == nil {
			snapshot.FailedTaskLine = formatBootstrapResumeTaskLine(taskRecord)
			switch strings.TrimSpace(projectBootstrapFailureClassForReason(state.ValidationFailureClass, state.ValidationFailureReason)) {
			case projectBootstrapFailureCompoundParent:
				if len(allTasks) == 0 {
					if tasks, listErr := e.tasks.ListByProject(ctx, projectID); listErr == nil {
						allTasks = tasks
					}
				}
				if len(allTasks) > 0 {
					snapshot.RepairTaskLine = buildProjectBootstrapCompoundParentRepairTaskLine(allTasks, taskRecord)
				}
			case projectBootstrapFailureFirstWaveSize:
				snapshot.RepairTaskLine = buildProjectBootstrapBoundedSizeRepairTaskLine(taskRecord)
			}
		} else {
			snapshot.RepairTaskLine = buildProjectBootstrapUnresolvedFailureRepairTaskLine(taskNumber, failureTitle)
		}
	}
	return snapshot, nil
}

func buildProjectBootstrapResumeStateMessage(state projectBootstrapState, snapshot projectBootstrapResumeSnapshot) string {
	lines := []string{
		"[Project bootstrap resume]",
		"Resume the active project bootstrap workflow from the persisted state below.",
	}
	if projectID := strings.TrimSpace(snapshot.ProjectID); projectID != "" {
		projectLine := "Active project id: " + projectID
		if slug := strings.TrimSpace(snapshot.ProjectSlug); slug != "" {
			projectLine += " (slug " + slug + ")"
		}
		lines = append(lines, projectLine)
	}
	if phase := strings.TrimSpace(state.CurrentPhase); phase != "" {
		lines = append(lines, "Current phase: "+phase)
	}
	lines = append(lines, fmt.Sprintf(
		"Persisted counts: assignments=%d planned_tasks=%d flow_templates=%d first_wave_tasks=%d first_wave_promoted=%d first_wave_jobs=%d",
		state.AssignmentCount,
		state.PlannedTaskCount,
		state.PlannedFlowTemplateCount,
		state.FirstWaveTaskCount,
		state.FirstWavePromotedCount,
		state.FirstWaveJobCount,
	))
	if checkpoint := strings.TrimSpace(state.LastSuccessfulCheckpoint); checkpoint != "" {
		lines = append(lines, "Last successful checkpoint: "+checkpoint)
	}
	if strings.EqualFold(strings.TrimSpace(state.ValidationStatus), projectBootstrapValidationFailed) {
		if reason := strings.TrimSpace(state.ValidationFailureReason); reason != "" {
			lines = append(lines, "Current validation failure: "+reason)
		}
	}
	if blockedTask := strings.TrimSpace(snapshot.FailedTaskLine); blockedTask != "" {
		lines = append(lines, blockedTask)
	}
	if repairLine := strings.TrimSpace(snapshot.RepairTaskLine); repairLine != "" {
		lines = append(lines, repairLine)
	}
	if pm := strings.TrimSpace(snapshot.ExistingPM); pm != "" {
		lines = append(lines, "Existing PM: "+pm)
	}
	compactRoster := projectBootstrapResumeUsesCompactRoster(state)
	if assignments := strings.TrimSpace(snapshot.AssignmentLine); assignments != "" && !compactRoster {
		lines = append(lines, "Existing active assignments: "+assignments)
	} else if compactRoster && state.AssignmentCount > 0 {
		lines = append(lines, fmt.Sprintf("Existing staffing is already persisted for %d active project assignments. Reuse that roster; do not create duplicate agents or replace the PM unless a required role is still missing.", state.AssignmentCount))
		if assignments != "" {
			lines = append(lines, "Existing active assignments: "+assignments)
		}
	}
	if shouldRequireDirectBootstrapRepairAction(state, snapshot) {
		switch strings.TrimSpace(projectBootstrapFailureClassForReason(state.ValidationFailureClass, state.ValidationFailureReason)) {
		case projectBootstrapFailureFirstWaveSize, projectBootstrapFailureCompoundParent:
			lines = append(lines, "The next acceptable bootstrap action is direct bounded child-task creation beneath the named blocked task id above. Do not answer with narrative, recollection, or a state summary. If you do not take that direct repair action or report a concrete blocker, bootstrap will be treated as failed.")
		default:
			lines = append(lines, "The next acceptable bootstrap action is a direct task.update on the named blocked task id above using one of the active assignee ids above. Do not answer with narrative, recollection, or a state summary. If you do not take that direct repair action or report a concrete blocker, bootstrap will be treated as failed.")
		}
	}
	scaffoldOnlyRecovery := (state.AssignmentCount > 0 && state.PlannedTaskCount == 0 &&
		(strings.TrimSpace(state.CurrentPhase) == projectBootstrapCheckpointTaskTreePersisted ||
			strings.TrimSpace(state.LastSuccessfulCheckpoint) == projectBootstrapCheckpointStaffingPersisted)) ||
		projectBootstrapRestartScaffoldFailureReason(state.ValidationFailureReason)
	if scaffoldOnlyRecovery {
		lines = append(lines, "The next acceptable bootstrap action is direct task.create or subtask.create calls that materialize bounded non-bootstrap project work using the existing staffed roster above. Do not answer with narrative, recollection, or a project summary. Do not call project.get, project.list, task.list, flow.list_templates, or scaffold file reads before those task-creation mutations.")
	}
	flowTemplatesReady := state.PlannedFlowTemplateCount > 0 ||
		strings.TrimSpace(state.CurrentPhase) == projectBootstrapCheckpointFlowTemplatesPersisted ||
		strings.TrimSpace(state.LastSuccessfulCheckpoint) == projectBootstrapCheckpointFlowTemplatesPersisted
	if flowTemplatesReady && state.FirstWaveTaskCount == 0 && state.FirstWavePromotedCount == 0 && state.FirstWaveJobCount == 0 {
		lines = append(lines, "The persisted task tree already has runnable flow templates. Do not create more agents, parent tasks, or child tasks unless a concrete task is still unassigned or fails a boundedness/validation rule. Reuse the existing staffed task tree and move a small first executable wave into execution now.")
	}
	lines = append(lines, projectBootstrapResumePhaseGuidance(state))
	return strings.Join(lines, "\n")
}

func projectBootstrapResumeUsesCompactRoster(state projectBootstrapState) bool {
	if state.FirstWaveTaskCount > 0 || state.FirstWavePromotedCount > 0 || state.FirstWaveJobCount > 0 {
		return true
	}
	switch strings.TrimSpace(state.CurrentPhase) {
	case projectBootstrapCheckpointFirstWaveSelected,
		projectBootstrapCheckpointFirstWaveExecutions,
		projectBootstrapCheckpointFirstWaveJobsClaimed:
		return true
	}
	switch strings.TrimSpace(state.LastSuccessfulCheckpoint) {
	case projectBootstrapCheckpointFirstWaveSelected,
		projectBootstrapCheckpointFirstWaveExecutions,
		projectBootstrapCheckpointFirstWaveJobsClaimed:
		return true
	}
	return false
}

func shouldRequireDirectBootstrapRepairAction(state projectBootstrapState, snapshot projectBootstrapResumeSnapshot) bool {
	if !strings.EqualFold(strings.TrimSpace(state.ValidationStatus), projectBootstrapValidationFailed) {
		return false
	}
	if strings.TrimSpace(snapshot.FailedTaskLine) == "" {
		return false
	}
	failureClass := strings.TrimSpace(projectBootstrapFailureClassForReason(state.ValidationFailureClass, state.ValidationFailureReason))
	switch failureClass {
	case projectBootstrapFailureFirstWaveSize, projectBootstrapFailureCompoundParent:
		return true
	}
	reason := strings.ToLower(strings.TrimSpace(state.ValidationFailureReason))
	if !strings.Contains(reason, "has no assigned agent") {
		return false
	}
	return strings.TrimSpace(snapshot.AssignmentLine) != ""
}

func projectBootstrapResumeShouldRootAtResumeMessage(state projectBootstrapState) bool {
	if state.AssignmentCount > 0 ||
		state.PlannedTaskCount > 0 ||
		state.PlannedFlowTemplateCount > 0 ||
		state.FirstWaveTaskCount > 0 ||
		state.FirstWavePromotedCount > 0 ||
		state.FirstWaveJobCount > 0 {
		return true
	}
	if checkpoint := strings.TrimSpace(state.LastSuccessfulCheckpoint); checkpoint != "" && checkpoint != projectBootstrapCheckpointProjectCreated {
		return true
	}
	if phase := strings.TrimSpace(state.CurrentPhase); phase != "" && phase != projectBootstrapCheckpointProjectCreated {
		return true
	}
	return false
}

func projectBootstrapResumePhaseGuidance(state projectBootstrapState) string {
	if strings.EqualFold(strings.TrimSpace(state.ValidationStatus), projectBootstrapValidationFailed) {
		return "Bootstrap is currently blocked on a concrete validation failure. Do not re-run bootstrap.setup.persist as the first step unless the named blocker is already repaired. Fix the specific persisted task, assignment, flow attachment, or bounded-size problem named above first, then resume bootstrap from that corrected persisted state. Do not restart the project or ask the user to restate the request."
	}
	if projectBootstrapResumeUsesCompactRoster(state) {
		guidance := "Continue bootstrap only from the persisted first-wave state. Do not create more agents, parent tasks, or broad child-task batches unless a concrete persisted task is still invalid or unassigned. Reuse the existing staffed task tree, keep the bootstrap governance gate task untouched, keep first-wave execution tasks in draft until the gate auto-completes after validation passes, and finish any remaining task assignment, flow attachment, or first-wave selection using the persisted tasks already on the project. Reuse the existing active project assignees, including temp agents, whenever they already cover the needed role; only create a new agent if a required role is truly missing from the persisted assignment roster. Unless the persisted snapshot is clearly inconsistent, do not spend tools re-reading scaffold planning artifacts or re-listing the full task tree and template catalog before acting. Prefer direct task assignment, first-wave selection, and bootstrap.setup.persist using the persisted counts and roster above."
		if projectBootstrapResumeShouldStartWithPersist(state) {
			guidance += " In this resume turn, start with bootstrap.setup.persist from the persisted setup progress above before re-reading tasks, templates, or scaffold artifacts. Record already-complete staffing and decomposition steps first, and only inspect a specifically named blocker if that tool asks for it."
		}
		if projectBootstrapResumeNeedsSetupPersist(state) {
			guidance += " Bootstrap checklist steps are persisted through the bootstrap.setup.persist tool, not raw task.update status changes. While the bootstrap governance gate is still open, do not manually queue or start first-wave execution tasks. In this phase, do not call task.list or flow.list_templates before trying bootstrap.setup.persist from the persisted counts above; only inspect a specific task or template if bootstrap.setup.persist returns a concrete blocker naming it. Treat bind-repo-environment as confirming the canonical repo/workspace binding and environment records already present for the project; do not use git.commit or ad hoc cli.execute commands just to satisfy the bootstrap checklist. If the persisted setup work is already complete, call bootstrap.setup.persist with completed_step_slugs for bind-repo-environment, staff-project, decompose-workstreams, validate-task-shape, attach-validate-flow-templates, select-first-wave, and record-frank-sign-off; include sign_off_summary when recording Frank approval."
		}
		return guidance + " Do not restart the project or ask the user to restate the request."
	}
	return "Continue bootstrap only. Reuse the existing persisted PM and assigned agents unless a required role is still missing. Do not create duplicate agents or another PM. The bootstrap governance gate task is system-managed: do not edit it, do not try to assign it, and do not try to queue or complete it manually. Keep first-wave execution tasks in draft until the gate auto-completes after validation passes. The project manager must be a staff PM agent, not a temp agent. Finish staffing, bounded task decomposition, task assignment, flow attachment, and first-wave selection/promotion. Every executable non-bootstrap task must have an assigned active project agent before you promote or queue it. Do not restart the project or ask the user to restate the request."
}

func projectBootstrapResumeNeedsSetupPersist(state projectBootstrapState) bool {
	return state.ValidationStatus == projectBootstrapValidationPassed &&
		state.BootstrapTaskOutstanding &&
		strings.TrimSpace(state.BootstrapTaskID) != "" &&
		state.FirstWaveTaskCount > 0 &&
		state.FirstWavePromotedCount == 0
}

func projectBootstrapResumeShouldStartWithPersist(state projectBootstrapState) bool {
	if !state.BootstrapTaskOutstanding || strings.TrimSpace(state.BootstrapTaskID) == "" {
		return false
	}
	if state.AssignmentCount > 0 ||
		state.PlannedTaskCount > 0 ||
		state.PlannedFlowTemplateCount > 0 ||
		state.FirstWaveTaskCount > 0 ||
		state.FirstWavePromotedCount > 0 ||
		state.FirstWaveJobCount > 0 {
		return true
	}
	switch strings.TrimSpace(state.CurrentPhase) {
	case projectBootstrapCheckpointStaffingPersisted,
		projectBootstrapCheckpointTaskTreePersisted,
		projectBootstrapCheckpointFlowTemplatesPersisted,
		projectBootstrapCheckpointFirstWaveSelected,
		projectBootstrapCheckpointFirstWaveExecutions,
		projectBootstrapCheckpointFirstWaveJobsClaimed:
		return true
	}
	switch strings.TrimSpace(state.LastSuccessfulCheckpoint) {
	case projectBootstrapCheckpointStaffingPersisted,
		projectBootstrapCheckpointTaskTreePersisted,
		projectBootstrapCheckpointFlowTemplatesPersisted,
		projectBootstrapCheckpointFirstWaveSelected,
		projectBootstrapCheckpointFirstWaveExecutions,
		projectBootstrapCheckpointFirstWaveJobsClaimed:
		return true
	}
	return false
}

func buildProjectBootstrapResumeActionPrompt(state projectBootstrapState) string {
	lines := []string{
		"Continue the active project bootstrap from the persisted state above.",
		"Do not restate the project state or re-read scaffold artifacts, template catalogs, or the full task tree unless the persisted counts are clearly inconsistent with tool results.",
		"Do not call task.list, flow.list_templates, or file.read on scaffold planning artifacts unless a specific persisted task, template, or count is actually unclear.",
	}
	if strings.EqualFold(strings.TrimSpace(state.ValidationStatus), projectBootstrapValidationFailed) {
		reason := strings.TrimSpace(state.ValidationFailureReason)
		if reason == "" {
			reason = "repair the concrete bootstrap validation blocker named above"
		}
		lowerReason := strings.ToLower(reason)
		lines = append(lines, "Bootstrap is currently blocked on this validation failure: "+reason)
		lines = append(lines, "Do not start with bootstrap.setup.persist on this turn unless you have already repaired the named blocker. First fix the specific persisted task, assignment, flow attachment, or bounded-size issue named above.")
		lines = append(lines, "If the failure names an oversized first-wave or parent task, split that exact persisted task into narrower executable child tasks and keep each child bounded. If the failure names an unassigned or flowless first-wave task, fix that exact task directly.")
		if strings.Contains(lowerReason, "only ") &&
			(strings.Contains(lowerReason, "selected first-wave child tasks created flow_node_execution rows") ||
				strings.Contains(lowerReason, "selected first-wave child tasks produced runnable agent_turn jobs") ||
				strings.Contains(lowerReason, "selected first-wave child tasks left draft or entered queued execution")) {
			lines = append(lines, "This validation failure means the selected first wave is too large or too broad to materialize cleanly in one pass. Reduce the first wave to a smaller bounded subset of the already-created child tasks, leave later-wave tasks in draft, and then persist the corrected first-wave selection.")
			lines = append(lines, "Do not start that repair with project.list, project.get, task.list, flow.list_templates, flow.get_execution, file.read, file.write, agent.list, or staffing discovery. Do not rewrite planning artifacts or restaff the project. Work directly from the persisted first-wave child tasks already named by the bootstrap state, repair the runnable subset with direct task and flow mutations, and only then return to bootstrap.setup.persist.")
		}
		if strings.Contains(lowerReason, "has no assigned agent") {
			lines = append(lines, "This validation failure already names the exact unassigned first-wave task. Repair that persisted task directly instead of gathering more context. Do not start with project.get, task.list, task.children, flow.list_templates, or agent.list unless a single task-specific lookup is strictly necessary to complete that one assignment.")
			lines = append(lines, "Do not call task.get with the bare task number from the validation error. Use the exact task id and active assignee ids from the bootstrap resume state above, then call task.update directly on that task.")
			lines = append(lines, "Your next assistant action should be a tool call, not a narrative reply. Do not say that you recall prior details from memory or summarize the state. If the exact task id and active assignee ids are already present above, call task.update on that task now.")
			if strings.Contains(lowerReason, "wave ") || strings.Contains(lowerReason, "workstream") || strings.Contains(lowerReason, "parent") {
				lines = append(lines, "If the named task is a broad wave/workstream parent, do not read planning artifacts first. Keep the parent orchestration-only and create bounded executable child tasks directly beneath it using the persisted task title and current task tree.")
			}
		}
		lines = append(lines, "When the named blocker is fixed, resume with bootstrap.setup.persist using only canonical bootstrap setup step slugs such as bind-repo-environment, staff-project, decompose-workstreams, validate-task-shape, attach-validate-flow-templates, select-first-wave, and record-frank-sign-off. Current phase names like first_wave_executions_created are not valid completed_step_slugs.")
		lines = append(lines, "If first-wave selection is already persisted, do not use raw task.update to force draft first-wave tasks into queued or in_progress. Leave those tasks in draft and let bootstrap.setup.persist plus the bootstrap governance gate handle promotion after validation passes.")
		lines = append(lines, "Only after the named blocker is repaired should you call bootstrap.setup.persist to record the corrected setup state.")
		lines = append(lines, "Do not ask the user what they want. Continue the bootstrap workflow now.")
		return strings.Join(lines, " ")
	}
	if projectBootstrapResumeShouldStartWithPersist(state) {
		lines = append(lines, "Your first tool call in this resume turn should be bootstrap.setup.persist using the persisted setup progress above. Record any already-complete checklist steps before reading more tasks, templates, or scaffold artifacts.")
		lines = append(lines, "If staffing and bounded task decomposition are already complete, call bootstrap.setup.persist immediately with completed_step_slugs for staff-project, decompose-workstreams, and validate-task-shape as applicable. Only inspect a specific task or template if that tool returns a concrete blocker naming it.")
	}
	if projectBootstrapResumeNeedsSetupPersist(state) {
		lines = append(lines, "Act directly on the remaining setup work using the persisted roster and task ids: assign any unassigned executable tasks, attach any missing flow to a specific persisted task, finish first-wave selection, and then call bootstrap.setup.persist with canonical step slugs once the persisted setup checklist is complete.")
		lines = append(lines, "Before any task.list or flow.list_templates call, try bootstrap.setup.persist first from the persisted state above. Only inspect a specific task or template if that tool returns a concrete blocker naming it.")
		lines = append(lines, "Treat bind-repo-environment as confirming the canonical repo/workspace binding and environment records already present for the project; do not use git.commit or ad hoc cli.execute commands just to satisfy the bootstrap checklist.")
		lines = append(lines, "While the bootstrap governance gate is still open, do not use raw task.update to queue or start first-wave execution tasks, and do not edit, assign, queue, or complete the gate task manually.")
	} else {
		lines = append(lines, "Act directly on the remaining bootstrap work: assign any unassigned executable tasks, finish first-wave selection or promotion, and only inspect a specific persisted task if its current state is unclear.")
	}
	lines = append(lines, "Do not ask the user what they want. Continue the bootstrap workflow now.")
	return strings.Join(lines, " ")
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
	if _, err := updateTurnTaskMetadata(ctx, e.tasks, taskRecord); err != nil {
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
	if isTerminalTurnStatus(current.Status) {
		if !strings.EqualFold(strings.TrimSpace(current.Status), "cancelled") {
			e.logger.Warn("runTurn preflight skipping terminal turn",
				"session_id", rt.session.ID,
				"turn_id", rt.turn.ID,
				"turn_status", strings.ToLower(strings.TrimSpace(current.Status)),
			)
		}
		return errTurnCancelled
	}
	return fmt.Errorf(
		"runTurn preflight invalid turn state (operation=execute_turn expected_status=in_progress turn_status=%s turn_id=%s): %w",
		strings.ToLower(strings.TrimSpace(current.Status)),
		current.ID,
		chat.ErrInvalidStatusTransition,
	)
}

func (e *TurnEngine) immutableMessageWriteForTerminalTurn(ctx context.Context, rt *turnRuntime, err error) bool {
	if e == nil || rt == nil || rt.turn == nil || !errors.Is(err, repo.ErrMessageContentImmutable) {
		return false
	}
	current, getErr := e.chat.GetTurn(ctx, rt.turn.ID)
	if getErr != nil || current == nil || !isTerminalTurnStatus(current.Status) {
		return false
	}
	e.logger.Warn("ignoring immutable message write for terminal turn",
		"session_id", rt.session.ID,
		"turn_id", rt.turn.ID,
		"turn_status", strings.ToLower(strings.TrimSpace(current.Status)),
	)
	rt.turn = current
	return true
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
	if strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") {
		bootstrapState := projectBootstrapStateFromMetadata(rt.session.Metadata)
		if projectBootstrapStateActive(bootstrapState) {
			progress, err := e.loadProjectBootstrapProgress(ctx, rt.session.ScopeID)
			if err != nil {
				return false, err
			}
			if progress.ValidationFailed() {
				if !projectBootstrapRecoverableMaxToolCallFailure(progress) {
					return false, nil
				}
				if !projectBootstrapSetupPersisted(progress) {
					return false, nil
				}
			}
		}
	}
	continuations, err := e.cycleContinuationCount(ctx, rt)
	if err != nil {
		return false, err
	}
	return continuations < maxContinuationTurnDepth, nil
}

func projectBootstrapRecoverableMaxToolCallFailure(progress projectBootstrapProgress) bool {
	switch strings.TrimSpace(progress.ValidationFailureClass) {
	case projectBootstrapFailureCompoundParent, projectBootstrapFailureFirstWaveSize:
		return true
	case projectBootstrapFailureFirstWaveExecution:
		reason := strings.ToLower(strings.TrimSpace(progress.ValidationFailureReason))
		return strings.Contains(reason, "bounded size policy") ||
			strings.Contains(reason, "has no assigned agent") ||
			strings.Contains(reason, "requires human approval before queueing") ||
			(strings.Contains(reason, "only ") &&
				(strings.Contains(reason, "selected first-wave child tasks created flow_node_execution rows") ||
					strings.Contains(reason, "selected first-wave child tasks produced runnable agent_turn jobs") ||
					strings.Contains(reason, "selected first-wave child tasks left draft or entered queued execution")))
	case projectBootstrapFailureRuntime:
		return projectBootstrapRestartScaffoldFailureReason(progress.ValidationFailureReason)
	default:
		return false
	}
}

func projectBootstrapCancelledRecoveryProgress(state projectBootstrapState, progress projectBootstrapProgress) (projectBootstrapProgress, bool) {
	normalizeProjectBootstrapValidationFailure(&progress, false)
	if progress.ValidationFailed() {
		return progress, projectBootstrapRecoverableMaxToolCallFailure(progress)
	}

	stateValidation := projectBootstrapProgress{
		ValidationStatus:        strings.TrimSpace(state.ValidationStatus),
		ValidationFailureClass:  strings.TrimSpace(state.ValidationFailureClass),
		ValidationFailureReason: strings.TrimSpace(state.ValidationFailureReason),
	}
	normalizeProjectBootstrapValidationFailure(&stateValidation, false)
	if stateValidation.ValidationFailed() {
		return stateValidation, projectBootstrapRecoverableMaxToolCallFailure(stateValidation)
	}
	return progress, false
}

func (e *TurnEngine) shouldDeferRecoverableProjectBootstrapValidation(ctx context.Context, session *chat.ChatSession, currentTurn *chat.ChatTurn, progress projectBootstrapProgress) (bool, error) {
	if e == nil || e.turns == nil || session == nil || currentTurn == nil || !progress.ValidationFailed() {
		return false, nil
	}
	if !projectBootstrapRecoverableMaxToolCallFailure(progress) {
		return false, nil
	}
	turns, err := e.turns.ListBySession(ctx, session.ID)
	if err != nil {
		return false, err
	}
	return projectBootstrapHasPriorMaxToolCallsContinuation(turns, *currentTurn), nil
}

func projectBootstrapHasPriorMaxToolCallsContinuation(turns []repo.ChatTurn, currentTurn chat.ChatTurn) bool {
	if currentTurn.TurnNumber <= 1 {
		return false
	}
	for _, turn := range turns {
		if turn.TurnNumber >= currentTurn.TurnNumber {
			continue
		}
		if currentTurn.CycleID != nil {
			if turn.CycleID == nil || *turn.CycleID != *currentTurn.CycleID {
				continue
			}
		}
		if !strings.EqualFold(strings.TrimSpace(turn.Status), "completed") || turn.StopReason == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(*turn.StopReason), stopReasonMaxToolCalls) {
			return true
		}
	}
	return false
}

func projectBootstrapHasNewerLiveContinuationTurn(turns []repo.ChatTurn, completedTurn repo.ChatTurn) bool {
	for _, turn := range turns {
		if turn.TurnNumber <= completedTurn.TurnNumber {
			continue
		}
		if completedTurn.CycleID != nil {
			if turn.CycleID == nil || *turn.CycleID != *completedTurn.CycleID {
				continue
			}
		}
		switch strings.ToLower(strings.TrimSpace(turn.Status)) {
		case "pending", "in_progress":
			return true
		}
	}
	return false
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
	if rt != nil && rt.session != nil && strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project_task") {
		return false, nil
	}
	if rt != nil && rt.session != nil &&
		strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") &&
		projectBootstrapStateActive(projectBootstrapStateFromMetadata(rt.session.Metadata)) {
		return false, nil
	}
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

func toolPreservesExplicitTargetSessionID(name string) bool {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "message.send", "session.get", "session.history", "session.invite_agent":
		return true
	default:
		return false
	}
}

func toolPreservesExplicitTargetAgentID(name string) bool {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "agent.assign_project", "agent.update", "agent.get", "session.invite_agent":
		return true
	default:
		return false
	}
}

func toolPreservesExplicitTargetTaskID(name string) bool {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "task.get", "task.update":
		return true
	default:
		return false
	}
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
	blockedBootstrapRecoveryReread := false
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
		if !toolPreservesExplicitTargetSessionID(name) {
			arguments["session_id"] = rt.session.ID.String()
		}
		arguments["turn_id"] = rt.turn.ID.String()
		if !toolPreservesExplicitTargetAgentID(name) {
			arguments["agent_id"] = rt.agent.ID.String()
		}
		if binding.projectID != nil {
			arguments["project_id"] = binding.projectID.String()
		}
		if binding.taskID != nil {
			arguments["task_id"] = binding.taskID.String()
		} else if strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") && !toolPreservesExplicitTargetTaskID(name) {
			delete(arguments, "task_id")
		}
		if strings.EqualFold(name, "project.create") && rt.projectIdentity != nil {
			blockedCalls = append(blockedCalls, ToolResult{
				ToolCallID: id,
				Name:       name,
				Error:      buildProjectCreateConflictGuardError(rt.projectIdentity),
			})
			continue
		}
		if shouldBlockFreshKickoffPreCreateTool(rt, name) {
			blockedCalls = append(blockedCalls, ToolResult{
				ToolCallID: id,
				Name:       name,
				Error:      buildFreshKickoffPreCreateToolGuardError(),
			})
			continue
		}
		if shouldBlockFreshKickoffMemoryTool(rt, name) {
			blockedCalls = append(blockedCalls, ToolResult{
				ToolCallID: id,
				Name:       name,
				Error:      buildFreshKickoffMemoryToolGuardError(),
			})
			continue
		}
		if shouldBlockFreshKickoffAgentBrowseTool(rt, name) {
			blockedCalls = append(blockedCalls, ToolResult{
				ToolCallID: id,
				Name:       name,
				Error:      buildFreshKickoffAgentBrowseToolGuardError(),
			})
			continue
		}
		if shouldBlockProjectBootstrapRestaffingTool(rt, name) {
			blockedCalls = append(blockedCalls, ToolResult{
				ToolCallID: id,
				Name:       name,
				Error:      buildProjectBootstrapRestaffingToolGuardError(rt),
			})
			continue
		}
		if shouldBlockProjectBootstrapExcessStaffingDiscoveryTool(rt, name) {
			blockedCalls = append(blockedCalls, ToolResult{
				ToolCallID: id,
				Name:       name,
				Error:      buildProjectBootstrapExcessStaffingDiscoveryGuardError(),
			})
			continue
		}
		if shouldBlockProjectBootstrapRecoveryRereadTool(rt, name, arguments) {
			blockedBootstrapRecoveryReread = true
			blockedCalls = append(blockedCalls, ToolResult{
				ToolCallID: id,
				Name:       name,
				Error:      buildProjectBootstrapRecoveryRereadToolGuardError(rt, name),
			})
			continue
		}
		if shouldBlockTaskRecoveryStatusPathTool(rt, name, arguments) {
			blockedCalls = append(blockedCalls, ToolResult{
				ToolCallID: id,
				Name:       name,
				Error:      buildTaskRecoveryStatusPathToolGuardError(name),
			})
			continue
		}
		if shouldBlockTaskStatusMessageTool(rt, name, arguments) {
			blockedCalls = append(blockedCalls, ToolResult{
				ToolCallID: id,
				Name:       name,
				Error:      buildTaskStatusMessageToolGuardError(),
			})
			continue
		}
		if shouldBlockProjectKickoffFollowOnTool(rt, name) {
			blockedCalls = append(blockedCalls, ToolResult{
				ToolCallID: id,
				Name:       name,
				Error:      buildProjectKickoffFollowOnToolGuardError(rt.projectIdentity),
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
		if shouldStopAfterBlockedProjectBootstrapRecoveryReread(rt, blockedBootstrapRecoveryReread) {
			rt.stopReason = stopReasonValidationBlocked
			_, _ = e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, "[Bootstrap validation recovery reread blocked - ending this turn so the next continuation can repair the named blocker directly.]")
			return true, nil
		}
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
		failedBootstrap, err := e.handleProjectBootstrapChildTaskFailure(ctx, rt, results)
		if err != nil {
			return false, err
		}
		if failedBootstrap {
			return true, nil
		}
		blocked, err := e.handleToolValidationResults(ctx, rt, runCalls, results)
		if err != nil {
			return false, err
		}
		rt.toolCallsUsed += len(runCalls)
		if blocked {
			return true, nil
		}
		stopAfterBootstrapPersist, err := e.shouldStopAfterBootstrapPersist(ctx, rt, results)
		if err != nil {
			return false, err
		}
		if stopAfterBootstrapPersist {
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
		handled, stop, err = e.handleRecoveryFileWriteWithoutPath(ctx, rt, &call)
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
		handled, stop, err = e.handleRecoveryMalformedFileEditWithoutPath(ctx, rt, &call)
		if err != nil {
			return false, err
		}
		if handled {
			if stop {
				return true, nil
			}
			return false, nil
		}
		handled, stop, err = e.handleTaskMalformedFileEditWithoutPath(ctx, rt, &call)
		if err != nil {
			return false, err
		}
		if handled {
			if stop {
				return true, nil
			}
			return false, nil
		}
		handled, stop, err = e.handleTaskRejectedFileWriteContent(ctx, rt, &call)
		if err != nil {
			return false, err
		}
		if handled {
			if stop {
				return true, nil
			}
			return false, nil
		}
		handled, stop, err = e.handleTaskFileWriteWithoutContent(ctx, rt, &call)
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
		if rt.recoveryTurn && rt.recoveryWriteDone {
			rt.toolCallsUsed++
			return true, nil
		}
		failedBootstrap, err := e.handleProjectBootstrapChildTaskFailure(ctx, rt, []ToolResult{result})
		if err != nil {
			return false, err
		}
		if failedBootstrap {
			rt.toolCallsUsed++
			return true, nil
		}
		recoveryBlocked, recoveryErr := e.handleRecoveryPopulatedFileWriteOutcome(ctx, rt, call, result)
		if recoveryErr != nil {
			return false, recoveryErr
		}
		if recoveryBlocked {
			rt.toolCallsUsed++
			return true, nil
		}
		stopAfterDeliverableWrite, err := e.shouldStopAfterExecutionDeliverableWrite(ctx, rt, result)
		if err != nil {
			return false, err
		}
		if stopAfterDeliverableWrite {
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
		stopAfterBootstrapPersist, err := e.shouldStopAfterBootstrapPersist(ctx, rt, []ToolResult{result})
		if err != nil {
			return false, err
		}
		if stopAfterBootstrapPersist {
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

func (e *TurnEngine) shouldStopAfterBootstrapPersist(ctx context.Context, rt *turnRuntime, results []ToolResult) (bool, error) {
	if e == nil || e.projects == nil || rt == nil || rt.session == nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") || rt.session.ScopeID == uuid.Nil {
		return false, nil
	}
	if !projectBootstrapStateActive(projectBootstrapStateFromMetadata(rt.session.Metadata)) {
		return false, nil
	}
	if !toolResultsContainNamedTool(results, "bootstrap.setup.persist") {
		return false, nil
	}
	if bootstrapPersistChecklistComplete(results) {
		return true, nil
	}

	projectRecord, err := e.projects.GetByID(ctx, rt.session.ScopeID)
	if errors.Is(err, repo.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	state := projectBootstrapProjectStateFromSettings(projectRecord.Settings)
	return strings.EqualFold(strings.TrimSpace(state.Status), projectBootstrapStatusCompleted), nil
}

func (e *TurnEngine) shouldStopAfterExecutionDeliverableWrite(ctx context.Context, rt *turnRuntime, result ToolResult) (bool, error) {
	if e == nil || e.tasks == nil || rt == nil || rt.session == nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project_task") || rt.session.ScopeID == uuid.Nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(result.Name), "file.write") || strings.TrimSpace(result.Error) != "" {
		return false, nil
	}

	taskRecord, err := e.tasks.GetByID(ctx, rt.session.ScopeID)
	if err != nil {
		return false, err
	}
	return shouldStopAfterExecutionArtifactWrite(taskRecord, result), nil
}

func bootstrapPersistChecklistComplete(results []ToolResult) bool {
	for _, result := range results {
		if !strings.EqualFold(strings.TrimSpace(result.Name), "bootstrap.setup.persist") {
			continue
		}
		if len(result.Output) == 0 {
			continue
		}
		raw, ok := result.Output["setup_checklist_complete"]
		if !ok {
			continue
		}
		complete, ok := raw.(bool)
		if ok && complete {
			return true
		}
	}
	return false
}

func (e *TurnEngine) autoPersistBootstrapSetupFromWorkspace(ctx context.Context, rt *turnRuntime) (bool, error) {
	if e == nil || rt == nil || rt.session == nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") || rt.session.ScopeID == uuid.Nil {
		return false, nil
	}
	progress, err := e.loadProjectBootstrapProgress(ctx, rt.session.ScopeID)
	if err != nil {
		return false, err
	}
	if !progress.BootstrapTaskOutstanding || progress.BootstrapSetupTaskCount == 0 {
		return false, nil
	}
	stepSlugs, signOffSummary, err := e.inferBootstrapSetupPersistFromWorkspace(ctx, rt.session.ScopeID, progress)
	if err != nil {
		return false, err
	}
	if len(stepSlugs) == 0 {
		return false, nil
	}
	output, err := e.persistBootstrapSetupSteps(ctx, rt.session.ScopeID, stepSlugs, signOffSummary)
	if err != nil {
		return false, err
	}
	call := ToolCall{ID: "bootstrap-setup-persist-auto", Name: "bootstrap.setup.persist", Tier: "tier1"}
	results := []ToolResult{{
		ToolCallID: call.ID,
		Name:       call.Name,
		Output:     output,
	}}
	if err := e.appendToolResults(ctx, rt, results); err != nil {
		return false, err
	}
	rt.toolCallsUsed++
	if _, err := e.handleToolValidationResults(ctx, rt, []ToolCall{call}, results); err != nil {
		return false, err
	}
	if _, err := e.shouldStopAfterBootstrapPersist(ctx, rt, results); err != nil {
		return false, err
	}
	return true, nil
}

func (e *TurnEngine) syncProjectBootstrapSetupFromWorkspace(ctx context.Context, session *chat.ChatSession, turnID uuid.UUID) (bool, error) {
	if e == nil || session == nil || turnID == uuid.Nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project") || session.ScopeID == uuid.Nil {
		return false, nil
	}
	progress, err := e.loadProjectBootstrapProgress(ctx, session.ScopeID)
	if err != nil {
		return false, err
	}
	if !progress.BootstrapTaskOutstanding || progress.BootstrapSetupTaskCount == 0 {
		return false, nil
	}
	stepSlugs, signOffSummary, err := e.inferBootstrapSetupPersistFromWorkspace(ctx, session.ScopeID, progress)
	if err != nil {
		return false, err
	}
	if len(stepSlugs) == 0 {
		return false, nil
	}
	output, err := e.persistBootstrapSetupSteps(ctx, session.ScopeID, stepSlugs, signOffSummary)
	if err != nil {
		return false, err
	}
	payload, _ := json.Marshal(map[string]any{
		"tool_name": "bootstrap.setup.persist",
		"output":    output,
	})
	if _, err := e.chat.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: session.ID,
		TurnID:    &turnID,
		Role:      "tool_result",
		Content:   string(payload),
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (e *TurnEngine) persistBootstrapSetupSteps(ctx context.Context, projectID uuid.UUID, stepSlugs []string, signOffSummary string) (map[string]any, error) {
	if e == nil || e.tasks == nil || e.taskTransitions == nil || projectID == uuid.Nil {
		return nil, nil
	}
	projectTasks, err := e.tasks.ListByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	setupTasks := make([]repo.ProjectTask, 0, len(projectTasks))
	for _, task := range projectTasks {
		metadata := messageMetadataMap(task.Metadata)
		if setupTask, _ := metadata["bootstrap_setup_task"].(bool); !setupTask {
			continue
		}
		setupTasks = append(setupTasks, task)
	}
	canonicalSetupTasks, _ := canonicalProjectBootstrapSetupTasks(setupTasks)
	tasksBySlug := make(map[string]repo.ProjectTask, len(canonicalSetupTasks))
	for _, task := range canonicalSetupTasks {
		metadata := messageMetadataMap(task.Metadata)
		slug := strings.TrimSpace(stringValue(metadata["bootstrap_step_slug"]))
		if slug == "" {
			continue
		}
		tasksBySlug[slug] = task
	}

	completed := make([]map[string]any, 0, len(stepSlugs))
	for _, slug := range stepSlugs {
		taskRecord, ok := tasksBySlug[slug]
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "done") {
			payload := map[string]any{
				"bootstrap_setup_persisted": true,
				"bootstrap_step_slug":       slug,
			}
			if strings.TrimSpace(signOffSummary) != "" {
				payload["sign_off_summary"] = strings.TrimSpace(signOffSummary)
			}
			updated, err := e.taskTransitions.TransitionStatusWithPayload(ctx, taskRecord.ID, "done", tasksvc.Actor{
				Type:                        "system",
				AllowFlowRuntimeBypass:      true,
				AllowDoneBypass:             true,
				AllowBootstrapSetupComplete: true,
			}, payload)
			if err != nil {
				return nil, err
			}
			taskRecord = repo.ProjectTask(*updated)
			tasksBySlug[slug] = taskRecord
		}
		completed = append(completed, map[string]any{
			"task_id":     taskRecord.ID,
			"task_number": taskRecord.TaskNumber,
			"title":       taskRecord.Title,
			"step_slug":   slug,
			"work_status": taskRecord.WorkStatus,
		})
	}

	remaining := make([]string, 0)
	for _, slug := range []string{
		"bind-repo-environment",
		"staff-project",
		"decompose-workstreams",
		"validate-task-shape",
		"attach-validate-flow-templates",
		"select-first-wave",
		bootstrapFrankSignOffStepSlug,
	} {
		taskRecord, ok := tasksBySlug[slug]
		if !ok || !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "done") {
			remaining = append(remaining, slug)
		}
	}

	return map[string]any{
		"project_id":               projectID,
		"status":                   "persisted",
		"completed_steps":          completed,
		"setup_checklist_complete": len(remaining) == 0,
		"remaining_step_slugs":     remaining,
	}, nil
}

func (e *TurnEngine) inferBootstrapSetupPersistFromWorkspace(ctx context.Context, projectID uuid.UUID, progress projectBootstrapProgress) ([]string, string, error) {
	if e == nil || e.projects == nil || e.tasks == nil || projectID == uuid.Nil {
		return nil, "", nil
	}
	projectRecord, err := e.projects.GetByID(ctx, projectID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	workspaceRoot, err := workspace.ProjectRoot(e.dataDir, projectRecord.Slug)
	if err != nil {
		return nil, "", err
	}

	projectTasks, err := e.tasks.ListByProject(ctx, projectID)
	if err != nil {
		return nil, "", err
	}
	setupTasks := make([]repo.ProjectTask, 0, len(projectTasks))
	for _, task := range projectTasks {
		metadata := messageMetadataMap(task.Metadata)
		if setupTask, _ := metadata["bootstrap_setup_task"].(bool); !setupTask {
			continue
		}
		setupTasks = append(setupTasks, task)
	}
	canonicalSetupTasks, _ := canonicalProjectBootstrapSetupTasks(setupTasks)
	statusBySlug := make(map[string]string, len(canonicalSetupTasks))
	for _, task := range canonicalSetupTasks {
		metadata := messageMetadataMap(task.Metadata)
		slug := strings.TrimSpace(stringValue(metadata["bootstrap_step_slug"]))
		if slug == "" {
			continue
		}
		statusBySlug[slug] = strings.TrimSpace(task.WorkStatus)
	}
	filePresent := func(rel string) bool {
		abs := filepath.Join(workspaceRoot, filepath.FromSlash(rel))
		info, statErr := os.Stat(abs)
		return statErr == nil && !info.IsDir()
	}
	stepReady := map[string]bool{
		"bind-repo-environment":          filePresent("bootstrap/02-repo-and-environment.md"),
		"staff-project":                  filePresent("bootstrap/03-staffing-plan.md") && progress.AssignmentCount > 0,
		"decompose-workstreams":          filePresent("bootstrap/04-workstream-decomposition.md") && progress.PlannedTaskCount > 0,
		"validate-task-shape":            filePresent("bootstrap/05-task-validation.md") && progress.PlannedTaskCount > 0,
		"attach-validate-flow-templates": filePresent("bootstrap/06-flow-templates.md") && progress.PlannedTaskCount > 0 && progress.PlannedFlowTemplateCount > 0,
		"select-first-wave":              filePresent("bootstrap/07-first-wave-tasks.md") && progress.FirstWaveTaskCount > 0,
	}
	if filePresent("bootstrap/08-frank-sign-off-request.md") &&
		stepReady["bind-repo-environment"] &&
		stepReady["staff-project"] &&
		stepReady["decompose-workstreams"] &&
		stepReady["validate-task-shape"] &&
		stepReady["attach-validate-flow-templates"] &&
		stepReady["select-first-wave"] {
		stepReady[bootstrapFrankSignOffStepSlug] = true
	}

	canonicalOrder := []string{
		"bind-repo-environment",
		"staff-project",
		"decompose-workstreams",
		"validate-task-shape",
		"attach-validate-flow-templates",
		"select-first-wave",
		bootstrapFrankSignOffStepSlug,
	}
	completed := make([]string, 0, len(canonicalOrder))
	for _, slug := range canonicalOrder {
		if !stepReady[slug] {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(statusBySlug[slug]), "done") {
			continue
		}
		completed = append(completed, slug)
	}
	signOffSummary := ""
	for _, slug := range completed {
		if slug == bootstrapFrankSignOffStepSlug {
			signOffSummary = "Frank sign-off request artifact exists and bootstrap setup progress was synchronized automatically from the persisted workspace state."
			break
		}
	}
	return completed, signOffSummary, nil
}

func toolResultsContainNamedTool(results []ToolResult, toolName string) bool {
	normalized := strings.TrimSpace(toolName)
	if normalized == "" {
		return false
	}
	for _, result := range results {
		if strings.EqualFold(strings.TrimSpace(result.Name), normalized) {
			return true
		}
	}
	return false
}

func (e *TurnEngine) handleProjectBootstrapChildTaskFailure(ctx context.Context, rt *turnRuntime, results []ToolResult) (bool, error) {
	if e == nil || e.messages == nil || rt == nil || rt.session == nil || rt.turn == nil || rt.session.ScopeID == uuid.Nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.Mode), "async") {
		return false, nil
	}
	if !projectBootstrapToolResultsContainChildBoundednessFailure(results) {
		return false, nil
	}

	initialMessage, err := e.messages.GetByID(ctx, rt.initialMessageID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	messageSource := strings.TrimSpace(stringValue(messageMetadataMap(initialMessage.Metadata)["source"]))
	bootstrapState := projectBootstrapStateFromMetadata(rt.session.Metadata)
	if !strings.EqualFold(messageSource, projectBootstrapSource) && bootstrapState.Status != projectBootstrapStatusActive {
		return false, nil
	}

	// Do not fail-close the entire bootstrap turn immediately here.
	// This tool result often means the planner tried a broad child creation
	// first, but can still recover within the same turn by emitting bounded
	// children on a follow-up tool call. The hard gate remains the normal
	// end-of-turn bootstrap validation, which will archive only if the
	// persisted task tree is still structurally invalid after the turn ends.
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildProjectBootstrapBoundedChildRetryMessage()); err != nil {
		return false, err
	}
	return false, nil
}

func buildProjectBootstrapBoundedChildRetryMessage() string {
	return "[Bootstrap recovery: a parent follow-on task creation attempt was rejected because the child work was still too broad. Recover within this same turn by creating bounded executable child tasks directly under the parent. Do not queue or execute the broad parent task. Do not pivot to standalone tasks or workaround subtasks. Split the requested work into smaller reviewable child tasks with narrow titles and create those children under the parent.]"
}

func projectBootstrapToolResultsContainChildBoundednessFailure(results []ToolResult) bool {
	for _, result := range results {
		if projectBootstrapToolResultHasChildBoundednessFailure(result) {
			return true
		}
	}
	return false
}

func projectBootstrapToolResultHasChildBoundednessFailure(result ToolResult) bool {
	if strings.Contains(strings.ToLower(strings.TrimSpace(result.Error)), bootstrapChildTaskBoundednessError) {
		return true
	}
	if len(result.Output) == 0 {
		return false
	}
	raw, ok := result.Output["error"]
	if !ok || raw == nil {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", raw))), bootstrapChildTaskBoundednessError)
}

func (e *TurnEngine) handleRecoveryCLIExecuteWithoutCommand(ctx context.Context, rt *turnRuntime, call ToolCall) (bool, bool, error) {
	if rt == nil || !rt.recoveryTurn || rt.turn == nil || rt.session == nil {
		return false, false, nil
	}
	if !isRecoveryCLIExecuteWithoutCommand(call) {
		return false, false, nil
	}
	if rewritten, err := e.rewriteRecoveryCLIExecuteWithoutCommandToFileWrite(ctx, rt, &call); rewritten || err != nil {
		return rewritten, false, err
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

func (e *TurnEngine) rewriteRecoveryCLIExecuteWithoutCommandToFileWrite(ctx context.Context, rt *turnRuntime, call *ToolCall) (bool, error) {
	if rt == nil || call == nil || rt.turn == nil || rt.session == nil {
		return false, nil
	}
	targetPath, _, ok := e.recoveryFileOutputContext(ctx, rt)
	if !ok || strings.TrimSpace(targetPath) == "" {
		return false, nil
	}
	draft, rejectReason, draftOK := e.recoveryFileWriteDraftContent(ctx, rt, targetPath)
	if !draftOK || strings.TrimSpace(rejectReason) != "" || strings.TrimSpace(draft) == "" {
		return false, nil
	}

	call.Name = "file.write"
	call.Arguments = mergeRewrittenFileWriteArguments(call.Arguments, map[string]any{
		"path":        strings.TrimSpace(targetPath),
		"content":     draft,
		"create_dirs": true,
	})
	if rt.recoveryFileWrites == nil {
		rt.recoveryFileWrites = make(map[string]recoveryPopulatedFileWriteState)
	}
	rt.recoveryFileWrites[strings.TrimSpace(call.ID)] = recoveryPopulatedFileWriteState{
		TargetPath: strings.TrimSpace(targetPath),
		Draft:      draft,
	}
	e.logger.Info("recovery: rewrote empty cli.execute to file.write from persisted draft",
		"session_id", rt.session.ID,
		"turn_id", rt.turn.ID,
		"path", targetPath,
	)
	return false, nil
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
	_ = e.cancelRecoveryResumeDispatch(ctx, rt, rt.recoveryBlockReason)
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

	rejectReason := recoveryFileWriteDraftRejectReason(draft, targetPath)
	if strings.TrimSpace(rejectReason) == "" {
		call.Arguments = normalized
		return false, false, nil
	}
	if persistedDraft, persistedRejectReason, persistedOK := e.recoveryPersistedDraftContent(ctx, rt, targetPath); persistedOK && strings.TrimSpace(persistedRejectReason) == "" {
		normalized["content"] = persistedDraft
		if _, exists := normalized["create_dirs"]; !exists {
			normalized["create_dirs"] = true
		}
		call.Arguments = normalized
		if rt.recoveryFileWrites == nil {
			rt.recoveryFileWrites = make(map[string]recoveryPopulatedFileWriteState)
		}
		rt.recoveryFileWrites[strings.TrimSpace(call.ID)] = recoveryPopulatedFileWriteState{
			TargetPath: strings.TrimSpace(targetPath),
			Draft:      persistedDraft,
		}
		e.logger.Info("recovery: replaced rejected file.write content from persisted draft",
			"session_id", rt.session.ID,
			"turn_id", rt.turn.ID,
			"path", targetPath,
		)
		return false, false, nil
	}
	call.Arguments = normalized
	return e.haltRejectedRecoveryFileWrite(ctx, rt, targetPath, draft, rejectReason)
}

func (e *TurnEngine) handleRecoveryFileWriteWithoutPath(ctx context.Context, rt *turnRuntime, call *ToolCall) (bool, bool, error) {
	if rt == nil || call == nil || !rt.recoveryTurn || rt.turn == nil || rt.session == nil {
		return false, false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(call.Name), "file.write") {
		return false, false, nil
	}

	normalized := toolargs.Normalize("file.write", call.Arguments)
	if strings.TrimSpace(stringValue(normalized["path"])) != "" {
		return false, false, nil
	}

	targetPath, _, ok := e.recoveryFileOutputContext(ctx, rt)
	if !ok || strings.TrimSpace(targetPath) == "" {
		return false, false, nil
	}

	normalized["path"] = strings.TrimSpace(targetPath)
	if _, exists := normalized["create_dirs"]; !exists {
		normalized["create_dirs"] = true
	}
	call.Arguments = normalized

	if strings.TrimSpace(stringValue(normalized["content"])) != "" {
		e.logger.Info("recovery: populated file.write path from recovery context",
			"session_id", rt.session.ID,
			"turn_id", rt.turn.ID,
			"path", targetPath,
		)
		return false, false, nil
	}

	if draft, rejectReason, ok := e.recoveryFileWriteDraftContent(ctx, rt, targetPath); ok {
		normalized["content"] = draft
		call.Arguments = normalized
		if rt.recoveryFileWrites == nil {
			rt.recoveryFileWrites = make(map[string]recoveryPopulatedFileWriteState)
		}
		rt.recoveryFileWrites[strings.TrimSpace(call.ID)] = recoveryPopulatedFileWriteState{
			TargetPath: strings.TrimSpace(targetPath),
			Draft:      draft,
		}
		e.logger.Info("recovery: populated pathless file.write from recovery context",
			"session_id", rt.session.ID,
			"turn_id", rt.turn.ID,
			"path", targetPath,
		)
		return false, false, nil
	} else if strings.TrimSpace(rejectReason) != "" {
		return e.haltRejectedRecoveryFileWrite(ctx, rt, targetPath, draft, rejectReason)
	}

	e.logger.Info("recovery: injected missing file.write path from recovery context",
		"session_id", rt.session.ID,
		"turn_id", rt.turn.ID,
		"path", targetPath,
	)
	return false, false, nil
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

func (e *TurnEngine) handleRecoveryMalformedFileEditWithoutPath(ctx context.Context, rt *turnRuntime, call *ToolCall) (bool, bool, error) {
	if rt == nil || call == nil || !rt.recoveryTurn || rt.turn == nil || rt.session == nil {
		return false, false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(call.Name), "file.edit") {
		return false, false, nil
	}

	normalized := toolargs.Normalize("file.edit", call.Arguments)
	if strings.TrimSpace(stringValue(normalized["path"])) != "" {
		return false, false, nil
	}

	targetPath, _, ok := e.recoveryFileOutputContext(ctx, rt)
	if !ok {
		return false, false, nil
	}
	draft, rejectReason, ok := e.recoveryFileWriteDraftContent(ctx, rt, targetPath)
	if ok {
		call.Name = "file.write"
		call.Arguments = mergeRewrittenFileWriteArguments(call.Arguments, map[string]any{
			"path":        targetPath,
			"content":     draft,
			"create_dirs": true,
		})
		if rt.recoveryFileWrites == nil {
			rt.recoveryFileWrites = make(map[string]recoveryPopulatedFileWriteState)
		}
		rt.recoveryFileWrites[strings.TrimSpace(call.ID)] = recoveryPopulatedFileWriteState{
			TargetPath: strings.TrimSpace(targetPath),
			Draft:      draft,
		}
		e.logger.Info("recovery: rewrote malformed file.edit to persisted draft write",
			"session_id", rt.session.ID,
			"turn_id", rt.turn.ID,
			"path", targetPath,
		)
		return false, false, nil
	}
	if strings.TrimSpace(rejectReason) != "" {
		return e.haltRejectedRecoveryFileWrite(ctx, rt, targetPath, draft, rejectReason)
	}
	return false, false, nil
}

func (e *TurnEngine) handleTaskFileWriteWithoutContent(ctx context.Context, rt *turnRuntime, call *ToolCall) (bool, bool, error) {
	if rt == nil || call == nil || rt.turn == nil || rt.session == nil || rt.recoveryTurn {
		return false, false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project_task") {
		return false, false, nil
	}

	normalized, targetPath, ok := recoveryFileWriteMissingContent(*call)
	if !ok {
		return false, false, nil
	}

	draft, ok := e.taskContinuationDraftContent(ctx, rt, targetPath)
	if !ok {
		return false, false, nil
	}

	normalized["content"] = draft
	if _, exists := normalized["create_dirs"]; !exists {
		normalized["create_dirs"] = true
	}
	call.Arguments = normalized
	e.logger.Info("task continuation: populated file.write from assistant draft",
		"session_id", rt.session.ID,
		"turn_id", rt.turn.ID,
		"path", targetPath,
	)
	return false, false, nil
}

func (e *TurnEngine) handleTaskRejectedFileWriteContent(ctx context.Context, rt *turnRuntime, call *ToolCall) (bool, bool, error) {
	if rt == nil || call == nil || rt.turn == nil || rt.session == nil || rt.recoveryTurn {
		return false, false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project_task") {
		return false, false, nil
	}

	normalized, targetPath, draft, ok := recoveryFileWriteWithContent(*call)
	if !ok {
		return false, false, nil
	}
	rejectReason := recoveryFileWriteDraftRejectReason(draft, targetPath)
	if strings.TrimSpace(rejectReason) == "" {
		return false, false, nil
	}
	if replacement, replacementOK := e.taskContinuationDraftContent(ctx, rt, targetPath); replacementOK && strings.TrimSpace(replacement) != strings.TrimSpace(draft) {
		normalized["content"] = replacement
		if _, exists := normalized["create_dirs"]; !exists {
			normalized["create_dirs"] = true
		}
		call.Arguments = normalized
		e.logger.Info("task continuation: replaced non-substantive file.write content from substantive draft",
			"session_id", rt.session.ID,
			"turn_id", rt.turn.ID,
			"path", targetPath,
		)
		return false, false, nil
	}
	return e.haltRejectedRecoveryFileWrite(ctx, rt, targetPath, draft, rejectReason)
}

func (e *TurnEngine) handleTaskMalformedFileEditWithoutPath(ctx context.Context, rt *turnRuntime, call *ToolCall) (bool, bool, error) {
	if rt == nil || call == nil || rt.turn == nil || rt.session == nil || rt.recoveryTurn {
		return false, false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project_task") {
		return false, false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(call.Name), "file.edit") {
		return false, false, nil
	}

	normalized := toolargs.Normalize("file.edit", call.Arguments)
	if strings.TrimSpace(stringValue(normalized["path"])) != "" {
		return false, false, nil
	}

	targetPath, _, ok := e.recoveryFileOutputContext(ctx, rt)
	if !ok || strings.TrimSpace(targetPath) == "" {
		return false, false, nil
	}
	draft, ok := e.taskContinuationDraftContent(ctx, rt, targetPath)
	if !ok {
		return false, false, nil
	}

	call.Name = "file.write"
	call.Arguments = mergeRewrittenFileWriteArguments(call.Arguments, map[string]any{
		"path":        strings.TrimSpace(targetPath),
		"content":     draft,
		"create_dirs": true,
	})
	e.logger.Info("task continuation: rewrote pathless file.edit to file.write",
		"session_id", rt.session.ID,
		"turn_id", rt.turn.ID,
		"path", targetPath,
	)
	return false, false, nil
}

func (e *TurnEngine) taskContinuationDraftContent(ctx context.Context, rt *turnRuntime, targetPath string) (string, bool) {
	if draft, ok := e.latestSubstantiveAssistantDraftContent(ctx, rt, targetPath); ok {
		return draft, true
	}
	if draft, ok := e.latestRecoveryArtifactDraftContent(ctx, rt, targetPath); ok {
		if reason := recoveryFileWriteDraftRejectReason(draft, targetPath); reason == "" && looksLikeRecoveryFileDraft(draft) {
			return draft, true
		}
	}
	if draft, ok := e.latestPriorSubstantiveAssistantDraftContent(ctx, rt, targetPath); ok {
		return draft, true
	}
	if draft, ok := e.latestContinuationSummaryDraftContent(ctx, rt, targetPath); ok {
		return draft, true
	}
	if draft, ok := e.latestTaskHistoricalSubstantiveDraftContent(ctx, rt, targetPath); ok {
		return draft, true
	}
	if draft, rejectReason, ok := e.recoveryPersistedDraftContent(ctx, rt, targetPath); ok && strings.TrimSpace(rejectReason) == "" && looksLikeRecoveryFileDraft(draft) {
		return draft, true
	}
	if draft, ok := e.latestRecoveryAssistantDraftContent(ctx, rt); ok {
		if reason := recoveryFileWriteDraftRejectReason(draft, targetPath); reason == "" && looksLikeRecoveryFileDraft(draft) {
			return draft, true
		}
	}
	return "", false
}

func mergeRewrittenFileWriteArguments(existing, overrides map[string]any) map[string]any {
	merged := cloneMap(existing)
	if merged == nil {
		merged = map[string]any{}
	}
	for _, key := range []string{"organization_id", "session_id", "turn_id", "agent_id", "project_id", "task_id"} {
		if _, ok := merged[key]; !ok {
			continue
		}
	}
	for key, value := range overrides {
		merged[key] = value
	}
	return merged
}

func (e *TurnEngine) haltRejectedRecoveryFileWrite(ctx context.Context, rt *turnRuntime, targetPath, draft, failureReason string) (bool, bool, error) {
	failureReason = e.strengthenRecoveryDraftRejectFailureReason(ctx, rt, targetPath, failureReason)
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
	_ = e.cancelRecoveryResumeDispatch(ctx, rt, rt.recoveryBlockReason)
	return true, true, nil
}

func (e *TurnEngine) cancelRecoveryResumeDispatch(ctx context.Context, rt *turnRuntime, reason string) error {
	if e == nil || e.messages == nil || rt == nil || rt.initialMessageID == uuid.Nil {
		return nil
	}
	message, err := e.messages.GetByID(ctx, rt.initialMessageID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		return err
	}
	merged, err := chat.MergeAgentTurnDispatchCancelledMetadata(message.Metadata, strings.TrimSpace(reason), e.now().UTC())
	if err != nil {
		return err
	}
	_, err = e.messages.UpdateMetadata(ctx, message.ID, merged)
	return err
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
	return fmt.Sprintf("[Recovery correction: file.write for `%s` was emitted without `content`. Before retrying file mutation tools, draft the full file body in the assistant response or resend `file.write` with both `path` and `content` populated. The first non-whitespace character of your next assistant message must be the first character of the deliverable itself, not a sentence like 'I will write' or 'Now I'll draft'. If you already have the draft text, carry that exact text into the next write instead of emitting another empty-content call.]", path)
}

func buildRecoveryFileWriteRejectedMessage(targetPath, artifactPath, failureReason string) string {
	path := strings.TrimSpace(targetPath)
	if path == "" {
		path = "the requested workspace file"
	}
	if reason := strings.TrimSpace(failureReason); reason != "" {
		if taskcheckpoint.RecoveryFileWriteFailureRejectsDraft(reason) {
			if taskcheckpoint.RecoveryFileWriteFailureIsRepeatedDraftReject(reason) {
				if strings.TrimSpace(artifactPath) == "" {
					return fmt.Sprintf("[Recovery turn halted: recovered file.write for `%s` produced another non-substantive draft after the prior checkpoint already rejected placeholder narration. The task is now blocked with a hardened recovery checkpoint. Last failure: %s. Retry only when the next attempt can write the concrete file body instead of another plan to write it.]", path, reason)
				}
				return fmt.Sprintf("[Recovery turn halted: recovered file.write for `%s` produced another non-substantive draft after the prior checkpoint already rejected placeholder narration. The task is now blocked with a hardened recovery checkpoint. Last failure: %s. Resume from `%s` only when the next attempt can write the concrete file body instead of another placeholder.]", path, reason, strings.TrimSpace(artifactPath))
			}
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
		if taskcheckpoint.RecoveryFileWriteFailureRejectsDraft(reason) {
			if taskcheckpoint.RecoveryFileWriteFailureIsRepeatedDraftReject(reason) {
				if artifact := strings.TrimSpace(artifactPath); artifact != "" {
					return fmt.Sprintf("recovery halted after repeated non-substantive drafts for %s; resume from %s and re-queue only when the next attempt can write the concrete file body instead of another placeholder", path, artifact)
				}
				return fmt.Sprintf("recovery halted after repeated non-substantive drafts for %s; re-queue only when the next attempt can write the concrete file body instead of another placeholder", path)
			}
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

func hasRecoveryFileWriteCheckpointState(checkpoint taskcheckpoint.RecoveryFileWriteCheckpoint) bool {
	checkpoint = taskcheckpoint.NormalizeRecoveryFileWriteCheckpoint(checkpoint)
	return taskcheckpoint.RecoveryFileWriteBlockerClass(&checkpoint) != "" ||
		strings.TrimSpace(checkpoint.TargetPath) != "" ||
		strings.TrimSpace(checkpoint.ArtifactPath) != "" ||
		strings.TrimSpace(checkpoint.FailureReason) != "" ||
		len(checkpoint.PriorFailureReasons) != 0 ||
		strings.TrimSpace(checkpoint.HistoryStartMessageID) != "" ||
		strings.TrimSpace(checkpoint.HaltTurnID) != ""
}

func recoveryCheckpointFromMessageMetadata(metadata json.RawMessage, fallbackReason string) (taskcheckpoint.RecoveryFileWriteCheckpoint, bool) {
	decoded := messageMetadataMap(metadata)
	if !tasksvc.IsRecoveryResumeAction(stringValue(decoded["recovery_action"])) {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false
	}

	checkpoint := taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:          strings.TrimSpace(stringValue(decoded["recovery_checkpoint_target_path"])),
		ArtifactPath:        strings.TrimSpace(stringValue(decoded["recovery_checkpoint_artifact_path"])),
		BlockerClass:        strings.TrimSpace(stringValue(decoded["recovery_blocker_class"])),
		FailureReason:       strings.TrimSpace(stringValue(decoded["recovery_checkpoint_failure_reason"])),
		PriorFailureReasons: anyStrings(decoded["recovery_checkpoint_prior_failure_reasons"]),
	}
	if checkpoint.TargetPath == "" {
		if targetPath, ok := recoveryTargetPathFromArtifact(checkpoint.ArtifactPath); ok {
			checkpoint.TargetPath = targetPath
		}
	}
	if strings.TrimSpace(checkpoint.TargetPath) == "" && strings.TrimSpace(checkpoint.ArtifactPath) == "" {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false
	}
	if checkpoint.FailureReason == "" {
		checkpoint.FailureReason = strings.TrimSpace(fallbackReason)
	}
	checkpoint = taskcheckpoint.NormalizeRecoveryFileWriteCheckpoint(checkpoint)
	if !hasRecoveryFileWriteCheckpointState(checkpoint) {
		return taskcheckpoint.RecoveryFileWriteCheckpoint{}, false
	}
	return checkpoint, true
}

func (e *TurnEngine) recoveryCheckpointFromInitialMessageMetadata(ctx context.Context, rt *turnRuntime, fallbackReason string) (*taskcheckpoint.RecoveryFileWriteCheckpoint, bool) {
	if e == nil || e.messages == nil || rt == nil || rt.initialMessageID == uuid.Nil {
		return nil, false
	}
	message, err := e.messages.GetByID(ctx, rt.initialMessageID)
	if err != nil {
		return nil, false
	}
	checkpoint, ok := recoveryCheckpointFromMessageMetadata(message.Metadata, fallbackReason)
	if !ok {
		return nil, false
	}
	return &checkpoint, true
}

func (e *TurnEngine) reconcileRecoveryCheckpointCandidate(ctx context.Context, rt *turnRuntime, checkpoint taskcheckpoint.RecoveryFileWriteCheckpoint) taskcheckpoint.RecoveryFileWriteCheckpoint {
	checkpoint = taskcheckpoint.NormalizeRecoveryFileWriteCheckpoint(checkpoint)
	if e == nil || rt == nil {
		return checkpoint
	}
	historicalTarget, _, ok := e.recoveryHistoricalSubstantiveOutputContext(ctx, rt)
	historicalTarget = strings.TrimSpace(historicalTarget)
	if !ok || historicalTarget == "" {
		return checkpoint
	}
	candidateTarget := strings.TrimSpace(checkpoint.TargetPath)
	if candidateTarget == "" {
		checkpoint.TargetPath = historicalTarget
		if checkpoint.ArtifactPath != "" {
			if recoveredTarget, targetOK := recoveryTargetPathFromArtifact(checkpoint.ArtifactPath); !targetOK || !sameWorkspaceRelativePath(recoveredTarget, historicalTarget) {
				checkpoint.ArtifactPath = ""
			}
		}
		return checkpoint
	}
	if sameWorkspaceRelativePath(candidateTarget, historicalTarget) {
		return checkpoint
	}
	if candidateDraft, found := e.readRecoveryWorkspaceText(ctx, rt, candidateTarget); found {
		if reason := recoveryFileWriteDraftRejectReason(candidateDraft, candidateTarget); reason == "" && looksLikeRecoveryFileDraft(candidateDraft) {
			return checkpoint
		}
	}
	checkpoint.TargetPath = historicalTarget
	if checkpoint.ArtifactPath != "" {
		if recoveredTarget, targetOK := recoveryTargetPathFromArtifact(checkpoint.ArtifactPath); !targetOK || !sameWorkspaceRelativePath(recoveredTarget, historicalTarget) {
			checkpoint.ArtifactPath = ""
		}
	}
	return checkpoint
}

func (e *TurnEngine) recoveryFileWriteCheckpointCandidate(ctx context.Context, rt *turnRuntime, fallbackReason string) (*taskcheckpoint.RecoveryFileWriteCheckpoint, bool) {
	if checkpoint, ok := e.currentRecoveryFileWriteCheckpoint(ctx, rt); ok && hasRecoveryFileWriteCheckpointState(*checkpoint) {
		candidate := *checkpoint
		if strings.TrimSpace(candidate.TargetPath) == "" {
			if targetPath, targetOK := recoveryTargetPathFromArtifact(candidate.ArtifactPath); targetOK {
				candidate.TargetPath = targetPath
			}
		}
		if strings.TrimSpace(candidate.FailureReason) == "" {
			candidate.FailureReason = strings.TrimSpace(fallbackReason)
		}
		candidate = e.reconcileRecoveryCheckpointCandidate(ctx, rt, candidate)
		return &candidate, true
	}
	if checkpoint, ok := e.recoveryCheckpointFromInitialMessageMetadata(ctx, rt, fallbackReason); ok {
		reconciled := e.reconcileRecoveryCheckpointCandidate(ctx, rt, *checkpoint)
		return &reconciled, true
	}
	if targetPath, _, ok := e.recoveryHistoricalSubstantiveOutputContext(ctx, rt); ok && strings.TrimSpace(targetPath) != "" {
		checkpoint := taskcheckpoint.RecoveryFileWriteCheckpoint{
			TargetPath:    strings.TrimSpace(targetPath),
			FailureReason: strings.TrimSpace(fallbackReason),
		}
		return &checkpoint, true
	}
	targetPath, _, ok := e.recoveryFileOutputContext(ctx, rt)
	if !ok || strings.TrimSpace(targetPath) == "" {
		return nil, false
	}
	checkpoint := taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:    strings.TrimSpace(targetPath),
		FailureReason: strings.TrimSpace(fallbackReason),
	}
	return &checkpoint, true
}

type recoveryResumeState struct {
	targetPath                  string
	targetDraft                 string
	targetDraftRejectedReason   string
	artifactPath                string
	artifactDraft               string
	artifactDraftRejectedReason string
	summaryDraft                string
	summaryDraftRejectedReason  string
	blockerClass                string
	failureReason               string
	priorFailureReasons         []string
}

func (e *TurnEngine) appendRecoveryResumeState(ctx context.Context, rt *turnRuntime, preserveInitialMessage bool) (bool, error) {
	if rt == nil || !rt.recoveryTurn || rt.turn == nil || rt.session == nil {
		return false, nil
	}
	state, ok := e.loadRecoveryResumeState(ctx, rt)
	if !ok {
		if !shouldAppendTaskRecoveryActionPrompt(rt.session) {
			return false, nil
		}
		shouldAppend, appendErr := e.shouldAppendSyntheticUserPrompt(ctx, rt.session.ID, "task_recovery_action")
		if appendErr != nil {
			return false, appendErr
		}
		var actionMessage *chat.ChatMessage
		var err error
		if shouldAppend {
			actionMessage, err = e.chat.AppendMessage(ctx, chat.AppendMessageInput{
				SessionID: rt.session.ID,
				TurnID:    &rt.turn.ID,
				Role:      "user",
				Content:   buildTaskRecoveryActionPrompt(),
				Metadata:  syntheticContinuationActionMessageMetadata(rt.session, "task_recovery_action"),
			})
			if err != nil {
				return false, err
			}
		}
		if preserveInitialMessage && rt.initialMessageID != uuid.Nil {
			initial := rt.initialMessageID
			rt.historyStartID = &initial
			if err := e.persistTurnHistoryStart(ctx, rt, initial); err != nil {
				return false, err
			}
			return true, nil
		}
		if actionMessage != nil {
			rt.historyStartID = &actionMessage.ID
			if err := e.persistTurnHistoryStart(ctx, rt, actionMessage.ID); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	message, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildRecoveryResumeStateMessage(state))
	if err != nil {
		return false, err
	}
	shouldAppend, appendErr := e.shouldAppendSyntheticUserPrompt(ctx, rt.session.ID, "task_recovery_resume")
	if appendErr != nil {
		return false, appendErr
	}
	var actionMessage *chat.ChatMessage
	var actionErr error
	if shouldAppend {
		actionMessage, actionErr = e.chat.AppendMessage(ctx, chat.AppendMessageInput{
			SessionID: rt.session.ID,
			TurnID:    &rt.turn.ID,
			Role:      "user",
			Content:   buildRecoveryResumeActionPrompt(state),
			Metadata:  syntheticContinuationActionMessageMetadata(rt.session, "task_recovery_resume"),
		})
		if actionErr != nil {
			return false, actionErr
		}
	}
	if preserveInitialMessage && rt.initialMessageID != uuid.Nil {
		initial := rt.initialMessageID
		rt.historyStartID = &initial
		if err := e.persistTurnHistoryStart(ctx, rt, initial); err != nil {
			return false, err
		}
		return true, nil
	}
	if actionMessage != nil {
		rt.historyStartID = &actionMessage.ID
		if err := e.persistTurnHistoryStart(ctx, rt, actionMessage.ID); err != nil {
			return false, err
		}
		return true, nil
	}
	rt.historyStartID = &message.ID
	if err := e.persistTurnHistoryStart(ctx, rt, message.ID); err != nil {
		return false, err
	}
	return true, nil
}

func (e *TurnEngine) loadRecoveryResumeState(ctx context.Context, rt *turnRuntime) (recoveryResumeState, bool) {
	checkpoint, ok := e.recoveryFileWriteCheckpointCandidate(ctx, rt, "")
	if !ok {
		return recoveryResumeState{}, false
	}

	state := recoveryResumeState{
		targetPath:          strings.TrimSpace(checkpoint.TargetPath),
		artifactPath:        strings.TrimSpace(checkpoint.ArtifactPath),
		blockerClass:        taskcheckpoint.RecoveryFileWriteBlockerClass(checkpoint),
		failureReason:       strings.TrimSpace(checkpoint.FailureReason),
		priorFailureReasons: append([]string(nil), checkpoint.PriorFailureReasons...),
	}
	if draft, found := e.readRecoveryWorkspaceText(ctx, rt, state.targetPath); found {
		state.targetDraft, state.targetDraftRejectedReason = recoveryResumeDraftForPrompt(state.failureReason, state.targetPath, draft)
	}
	if artifactBody, found := e.readRecoveryWorkspaceText(ctx, rt, state.artifactPath); found {
		state.artifactDraft, state.artifactDraftRejectedReason = recoveryResumeDraftForPrompt(
			state.failureReason,
			state.targetPath,
			recoveryArtifactDraftContent(artifactBody),
		)
	}
	if strings.TrimSpace(state.targetDraft) == "" && strings.TrimSpace(state.artifactDraft) == "" {
		if summaryDraft, ok := e.latestContinuationSummaryDraftContent(ctx, rt, state.targetPath); ok {
			state.summaryDraft = summaryDraft
		}
	}
	if strings.TrimSpace(state.targetPath) == "" &&
		strings.TrimSpace(state.targetDraft) == "" &&
		strings.TrimSpace(state.targetDraftRejectedReason) == "" &&
		strings.TrimSpace(state.artifactPath) == "" &&
		strings.TrimSpace(state.artifactDraft) == "" &&
		strings.TrimSpace(state.artifactDraftRejectedReason) == "" &&
		strings.TrimSpace(state.summaryDraft) == "" &&
		strings.TrimSpace(state.summaryDraftRejectedReason) == "" &&
		strings.TrimSpace(state.failureReason) == "" &&
		strings.TrimSpace(state.blockerClass) == "" &&
		len(state.priorFailureReasons) == 0 {
		return recoveryResumeState{}, false
	}
	return state, true
}

func recoveryResumeDraftForPrompt(failureReason, targetPath, draft string) (string, string) {
	trimmed := strings.TrimSpace(draft)
	if trimmed == "" {
		return "", ""
	}
	rejectReason := strings.TrimSpace(recoveryFileWriteDraftRejectReason(trimmed, targetPath))
	if rejectReason == "" {
		return trimmed, ""
	}
	if !taskcheckpoint.RecoveryFileWriteFailureRejectsDraft(failureReason) {
		return "", rejectReason
	}
	return "", rejectReason
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
	if blockerClass := strings.TrimSpace(state.blockerClass); blockerClass != "" {
		lines = append(lines, "Checkpoint blocker class: "+blockerClass)
	}
	if taskcheckpoint.RecoveryFileWriteFailureRejectsDraft(state.failureReason) {
		lines = append(lines,
			"Prior recovery failure rejected a non-substantive draft. Treat rejected placeholder text as invalid context, not as the draft to continue.",
			"Next attempt rule: begin with the substantive file body for the target path, not progress narration or intent-to-write filler.",
		)
		if taskcheckpoint.RecoveryFileWriteFailureIsRepeatedDraftReject(state.failureReason) {
			lines = append(lines, "Repeated non-substantive drafts are already a hardened blocker state for this checkpoint.")
		}
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
		} else if strings.TrimSpace(state.targetDraftRejectedReason) != "" {
			lines = append(lines, "Existing target file draft: omitted because it matches the previously rejected non-substantive pattern.")
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
		} else if strings.TrimSpace(state.artifactDraftRejectedReason) != "" {
			lines = append(lines, "Recovery artifact draft: omitted because it only preserved the rejected non-substantive placeholder.")
		} else {
			lines = append(lines, "Recovery artifact draft: (not found on disk)")
		}
	}
	if draft := strings.TrimSpace(state.summaryDraft); draft != "" {
		excerpt, truncated := truncateRecoveryResumeExcerpt(draft, recoveryResumeExcerptChars)
		lines = append(lines,
			"Continuation summary draft:",
			"```text",
			excerpt,
			"```",
		)
		if truncated {
			lines = append(lines, "_Continuation summary excerpt truncated for prompt budget._")
		}
	} else if strings.TrimSpace(state.summaryDraftRejectedReason) != "" {
		lines = append(lines, "Continuation summary draft: omitted because it matches the previously rejected non-substantive pattern.")
	}

	if reason := strings.TrimSpace(state.failureReason); reason != "" {
		lines = append(lines, "Checkpoint failure reason: "+reason)
	}
	if len(state.priorFailureReasons) != 0 {
		lines = append(lines, "Prior recovery failure history: "+strings.Join(state.priorFailureReasons, " | "))
	}
	if taskcheckpoint.RecoveryFileWriteFailureRejectsDraft(state.failureReason) &&
		strings.TrimSpace(state.targetDraft) == "" &&
		strings.TrimSpace(state.artifactDraft) == "" &&
		strings.TrimSpace(state.summaryDraft) == "" {
		lines = append(lines, "No substantive durable draft is currently available on disk. The next attempt must write the real file body from scratch rather than restating the plan to do so.")
	}
	if strings.TrimSpace(state.targetDraft) != "" &&
		(strings.TrimSpace(state.artifactDraft) != "" || strings.TrimSpace(state.summaryDraft) != "") {
		lines = append(lines, "If the target file is only a stub but the recovery artifact is fuller, merge the fuller artifact content into the target before retrying the final write.")
	}
	return strings.Join(lines, "\n")
}

func buildRecoveryResumeActionPrompt(state recoveryResumeState) string {
	lines := []string{
		"Continue the active task recovery now.",
		"Your next response must take direct recovery action from the durable drafts above.",
		"Do not answer with generic chat, acknowledgements, or a question to the user.",
		"Do not say that you are ready, ask what to do next, or summarize the state instead of acting.",
		"Your entire next assistant message must be either the concrete file body for the target deliverable or one concrete blocker sentence.",
		"Do not ask 'What do you need?', 'What would you like me to do?', or any equivalent recovery question.",
	}
	if target := strings.TrimSpace(state.targetPath); target != "" {
		lines = append(lines, "Treat "+target+" as the target file for this recovery turn.")
	}
	lines = append(lines,
		"Do not browse .ottercamp/recovery broadly or read recovery artifacts for other tasks during this recovery turn.",
		"If you need grounding, limit reads to the target file, the named recovery artifact for this task, and same-task planning artifacts only.",
		"Ignore unrelated OC-* artifacts even if a broad search returns them; they are not valid recovery context for this task.",
	)
	if strings.TrimSpace(state.targetDraft) != "" || strings.TrimSpace(state.artifactDraft) != "" || strings.TrimSpace(state.summaryDraft) != "" {
		lines = append(lines,
			"A substantive durable draft is already available above. Reuse that draft body directly instead of introducing yourself, summarizing the task, or describing what you are about to do.",
			"If you need to repair the target file, your next assistant message should begin with the first line of the best available draft rather than a sentence about context or readiness.",
			"Do not reread strategy artifacts, planning files, or workspace listings before writing when the substantive draft is already present above.",
			"Your next tool action should be the concrete file mutation for the target file, not another discovery read.",
		)
	}
	if taskcheckpoint.RecoveryFileWriteFailureRejectsDraft(state.failureReason) {
		target := strings.TrimSpace(state.targetPath)
		if target == "" {
			target = "the target file"
		}
		lines = append(lines,
			"Your next assistant message must begin with the concrete file body for "+target+" itself, not narration about planning to write it.",
			"The first non-whitespace character of your next assistant message must be the first character of the deliverable body itself.",
			"Do not preface the file body with readiness text, explanations, or intent-to-write filler.",
			"Do not start with phrases like 'I', 'I'll', 'I will', 'Now I'll', 'Let me', 'Here is', or 'Below is' before the file body.",
		)
		if strings.TrimSpace(state.targetDraft) == "" && strings.TrimSpace(state.artifactDraft) == "" && strings.TrimSpace(state.summaryDraft) == "" {
			lines = append(lines, "No substantive durable draft is available. Draft the file body from scratch in the assistant message before any file.write. If the target is Markdown, start immediately with a heading and real section content.")
		}
		if taskcheckpoint.RecoveryFileWriteFailureIsRepeatedDraftReject(state.failureReason) {
			lines = append(lines, "This checkpoint is already hardened after repeated non-substantive drafts. Another intent-to-write sentence will fail; either write the file body now or report one concrete blocker sentence.")
		}
	}
	switch strings.TrimSpace(state.blockerClass) {
	case taskcheckpoint.RecoveryFileWriteBlockerClassDurableCheckpoint,
		taskcheckpoint.RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint:
		lines = append(lines, "Use the recovered target draft, artifact draft, or continuation summary draft above to write the real file body now. Do not emit placeholder text about intending to write the file.")
	default:
		lines = append(lines, "Act directly from the durable drafts above. Prefer the concrete repair tool call or file output needed to resume execution.")
	}
	lines = append(lines, "If a draft is already substantive enough, use it directly instead of re-reading workspace artifacts first.")
	lines = append(lines, "If you truly cannot continue, report the concrete blocker in one sentence instead of switching into generic conversation.")
	return strings.Join(lines, " ")
}

func shouldAppendTaskContinuationActionPrompt(session *chat.ChatSession) bool {
	if session == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project_task") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(session.Mode), "async")
}

func shouldAppendProjectContinuationActionPrompt(session *chat.ChatSession) bool {
	if session == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(session.Mode), "async")
}

func shouldAppendTaskRecoveryActionPrompt(session *chat.ChatSession) bool {
	return shouldAppendTaskContinuationActionPrompt(session)
}

func buildTaskRecoveryActionPrompt() string {
	lines := []string{
		"Continue the active task recovery now.",
		"Your next response must take direct recovery action on the task instead of generic chat.",
		"Do not say that you are ready, ask what to do next, or ask the user what they need.",
		"Do not say that you lack context or ask the user to restate the task when this recovery turn already includes the task session history and recovery kickoff.",
		"Do not restate the task state or reread broad context before acting.",
		"Do not start with project.list, project.get, task.list, task.get, task_get, flow.list_templates, flow.get_execution, file.read, file_list, or agent.list unless a specific blocker names that exact record.",
		"Use the existing workspace, task state, recent tool results, and supervisor metadata to continue the task directly.",
		"If you truly cannot continue, report the concrete blocker in one sentence instead of switching into generic conversation.",
	}
	return strings.Join(lines, " ")
}

func buildTaskContinuationActionPrompt(summary string) string {
	lines := []string{
		"Continue the active task now from the continuation summary above.",
		"Your next response must take direct action on the task instead of generic chat.",
		"Do not say that you are ready, ask what to do next, or ask the user what they need.",
		"Do not say that you lack context or ask the user to restate the task when this continuation turn already includes the task session history and continuation summary.",
		"Do not restate the task state or reread broad context before acting.",
		"Do not start with project.list, project.get, task.list, task.get, task_get, flow.list_templates, flow.get_execution, file.read, file_list, or agent.list unless a specific blocker names that exact record.",
		"Use the existing workspace, task state, and recent tool results to continue the task directly.",
		"If you truly cannot continue, report the concrete blocker in one sentence instead of switching into generic conversation.",
	}
	if continuationSummaryLooksLikeDraft(summary) {
		lines = append(lines,
			"The continuation summary above already contains draft deliverable content. Treat it as the working artifact draft for this turn.",
			"Do not reopen broad workspace context or search for more source material before using that draft.",
			"If a target file is in scope, revise the draft directly and write the file with concrete content instead of re-deriving the document from scratch.",
		)
	}
	return strings.Join(lines, " ")
}

func buildProjectContinuationActionPrompt(summary string) string {
	lines := []string{
		"Continue the active project execution now from the continuation summary above.",
		"Your next response must take direct project action instead of generic chat.",
		"Do not say that you are ready, ask what to do next, or ask the user what they need.",
		"Do not say that you lack context or ask the user to restate the project when this continuation turn already includes the project session history and continuation summary.",
		"Do not restate the project state or reread broad project context before acting.",
		"Use the existing task tree, workspace state, planning artifacts, and recent tool results to continue execution directly.",
		"Prefer direct task.create, task.update, bootstrap.setup.persist, flow, assignment, or file actions over more narration whenever the next step is already clear.",
		"If you truly cannot continue, report the concrete blocker in one sentence instead of switching into generic conversation.",
	}
	if continuationSummaryLooksLikeDraft(summary) {
		lines = append(lines,
			"The continuation summary above already contains draft project deliverable content. Treat it as the working draft for this turn.",
			"Do not reopen broad workspace context or re-derive the same planning document before using that draft.",
		)
	}
	return strings.Join(lines, " ")
}

func projectExecutionContinuationFallbackSummary() string {
	return "Project execution is already underway. Reuse the existing project task tree, workspace artifacts, planning files, and recent tool results from this session to keep the active work moving forward."
}

func taskExecutionContinuationFallbackSummary() string {
	return "Task execution is already underway. Reuse the existing workspace files, task state, prior tool results, and recent artifacts from this session to continue the active task directly."
}

func continuationSummaryLooksLikeDraft(summary string) bool {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return false
	}
	if !strings.HasPrefix(trimmed, "#") {
		return false
	}
	return strings.Contains(trimmed, "\n## ") || strings.Contains(trimmed, "\n- Kind:")
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
			if draft, found := e.readRecoveryWorkspaceText(ctx, rt, targetPath); found {
				return targetPath, draft, true
			}
			if artifactPath := strings.TrimSpace(checkpoint.ArtifactPath); artifactPath != "" {
				if draft, found := e.readRecoveryWorkspaceText(ctx, rt, artifactPath); found {
					return targetPath, recoveryArtifactDraftContent(draft), true
				}
			}
		}
	}
	if targetPath, draft, ok := e.recoveryHistoricalSubstantiveOutputContext(ctx, rt); ok {
		return targetPath, draft, true
	}
	if e == nil || e.messages == nil || rt == nil || rt.session == nil || rt.turn == nil {
		return "", "", false
	}

	messages, err := e.messages.ListBySession(ctx, rt.session.ID)
	if err != nil {
		return "", "", false
	}
	fallbackPath := ""
	fallbackDraft := ""
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
			if reason := recoveryFileWriteDraftRejectReason(draft, targetPath); reason == "" && looksLikeRecoveryFileDraft(draft) {
				return targetPath, draft, true
			}
		}
		if draft == "" {
			if workspaceDraft, found := e.readRecoveryWorkspaceText(ctx, rt, targetPath); found {
				if reason := recoveryFileWriteDraftRejectReason(workspaceDraft, targetPath); reason == "" && looksLikeRecoveryFileDraft(workspaceDraft) {
					return targetPath, workspaceDraft, true
				}
				if fallbackPath == "" {
					fallbackPath = targetPath
					fallbackDraft = workspaceDraft
				}
				continue
			}
		}
		if fallbackPath == "" {
			fallbackPath = targetPath
			fallbackDraft = draft
		}
	}
	if fallbackPath != "" {
		return fallbackPath, fallbackDraft, true
	}
	return "", "", false
}

func (e *TurnEngine) recoveryHistoricalSubstantiveOutputContext(ctx context.Context, rt *turnRuntime) (string, string, bool) {
	if e == nil || e.messages == nil || rt == nil || rt.session == nil || rt.turn == nil {
		return "", "", false
	}
	sessionIDs := []uuid.UUID{rt.session.ID}
	bestPath := ""
	bestDraft := ""
	bestScore := -1
	if e.pool != nil && strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project_task") && rt.session.ScopeID != uuid.Nil {
		rows, err := e.pool.Query(ctx, `
			SELECT id
			FROM chat_session
			WHERE scope_type = 'project_task'
			  AND scope_id = $1
			ORDER BY created_at DESC
		`, rt.session.ScopeID)
		if err == nil {
			defer rows.Close()
			sessionIDs = sessionIDs[:0]
			for rows.Next() {
				var sessionID uuid.UUID
				if scanErr := rows.Scan(&sessionID); scanErr != nil {
					sessionIDs = []uuid.UUID{rt.session.ID}
					break
				}
				sessionIDs = append(sessionIDs, sessionID)
			}
			if rows.Err() != nil || len(sessionIDs) == 0 {
				sessionIDs = []uuid.UUID{rt.session.ID}
			}
		}
	}
	for _, sessionID := range sessionIDs {
		messages, err := e.messages.ListBySession(ctx, sessionID)
		if err != nil {
			continue
		}
		for i := len(messages) - 1; i >= 0; i-- {
			message := messages[i]
			if sessionID == rt.session.ID && message.TurnID != nil && *message.TurnID == rt.turn.ID {
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
			if strings.EqualFold(strings.TrimSpace(toolName), "file.read") && !usedArtifactPath {
				draft := strings.TrimSpace(anyString(output["content"]))
				if score, ok := recoveryHistoricalDraftCandidateScore(strings.TrimSpace(toolName), targetPath, draft); ok && score > bestScore {
					bestPath = targetPath
					bestDraft = draft
					bestScore = score
				}
			}
			if workspaceDraft, found := e.readRecoveryWorkspaceText(ctx, rt, targetPath); found {
				if score, ok := recoveryHistoricalDraftCandidateScore(strings.TrimSpace(toolName), targetPath, workspaceDraft); ok && score > bestScore {
					bestPath = targetPath
					bestDraft = workspaceDraft
					bestScore = score
				}
			}
		}
	}
	if bestScore >= 0 {
		return bestPath, bestDraft, true
	}
	return "", "", false
}

func recoveryHistoricalDraftPathLooksLikeDeliverable(targetPath string) bool {
	trimmed := strings.TrimSpace(strings.ReplaceAll(targetPath, "\\", "/"))
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, ".ottercamp/"):
		return false
	case strings.HasPrefix(lower, "planning/checkpoint/"):
		return false
	case strings.HasPrefix(lower, "planning/recovery-state/"):
		return false
	case strings.HasPrefix(lower, "planning/strategy-artifact/"):
		return false
	}
	return true
}

func recoveryHistoricalReadPathShouldFallback(targetPath string) bool {
	trimmed := strings.TrimSpace(strings.ReplaceAll(targetPath, "\\", "/"))
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "planning/")
}

func recoveryHistoricalDraftCandidateScore(toolName, targetPath, draft string) (int, bool) {
	trimmedDraft := strings.TrimSpace(draft)
	if trimmedDraft == "" {
		return 0, false
	}
	if reason := recoveryFileWriteDraftRejectReason(trimmedDraft, targetPath); reason != "" {
		return 0, false
	}
	if !looksLikeRecoveryFileDraft(trimmedDraft) && !recoveryHistoricalSourceDraftLooksSubstantive(targetPath, trimmedDraft) {
		return 0, false
	}
	if !recoveryHistoricalDraftPathLooksLikeDeliverable(targetPath) {
		return 1, true
	}
	if strings.EqualFold(strings.TrimSpace(toolName), "file.read") && recoveryHistoricalReadPathShouldFallback(targetPath) {
		return 2, true
	}
	return 3, true
}

func recoveryHistoricalSourceDraftLooksSubstantive(targetPath, draft string) bool {
	trimmedDraft := strings.TrimSpace(draft)
	if trimmedDraft == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(filepath.Ext(targetPath))) {
	case ".py", ".go", ".js", ".ts", ".tsx", ".jsx", ".rb", ".sh", ".bash", ".zsh", ".sql", ".json", ".yaml", ".yml", ".toml", ".html", ".css":
		return len(trimmedDraft) >= 40
	default:
		return false
	}
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

func recoveryTargetPathFromArtifact(artifactPath string) (string, bool) {
	normalized := filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(artifactPath))))
	prefix := recoveryArtifactDir + "/"
	if !strings.HasPrefix(normalized, prefix) || len(normalized) <= len(prefix) {
		return "", false
	}
	targetPath := strings.TrimPrefix(normalized, prefix)
	if targetPath == "" || targetPath == "." || targetPath == ".." || strings.HasPrefix(targetPath, "../") {
		return "", false
	}
	return targetPath, true
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

func (e *TurnEngine) handleProjectBootstrapUnhandledFailure(ctx context.Context, rt *turnRuntime, cause error) (bool, error) {
	if rt == nil || rt.turn == nil || rt.session == nil || rt.session.ScopeID == uuid.Nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") || !strings.EqualFold(strings.TrimSpace(rt.session.Mode), "async") {
		return false, nil
	}

	progress, err := e.loadProjectBootstrapProgress(ctx, rt.session.ScopeID)
	if err != nil {
		return true, err
	}
	state := projectBootstrapStateFromMetadata(rt.session.Metadata)
	if !e.projectBootstrapRuntimeManaged(ctx, rt.session, rt.initialMessageID) || progress.Materialized() {
		return false, nil
	}
	now := e.now().UTC()
	if state.StartedAt == nil {
		startedAt := rt.startedAt.UTC()
		state.StartedAt = &startedAt
	}
	if strings.TrimSpace(state.InitialMessageID) == "" && rt.initialMessageID != uuid.Nil {
		if message, getErr := e.messages.GetByID(ctx, rt.initialMessageID); getErr == nil {
			state.InitialMessageID = projectBootstrapWorkflowMessageID(&message).String()
		}
	}

	failureCategory := projectFailureCategoryBootstrap
	failureClass := projectBootstrapFailureRuntime
	failureReason := summarizeFailure(cause)
	if providerClass, providerReason, ok := classifyProjectProviderFailure(cause); ok {
		failureCategory = projectFailureCategoryProvider
		failureClass = providerClass
		if strings.TrimSpace(providerReason) != "" {
			failureReason = providerReason
		}
	}
	if failureCategory == projectFailureCategoryProvider && projectBootstrapSetupPersisted(progress) {
		return e.handleDeferredBootstrapProviderFailure(ctx, rt, progress, failureClass, failureReason, now)
	}
	record := buildProjectBootstrapAutomaticFailureRecord(progress, failureCategory, failureClass, failureReason, now)

	state.Status = projectBootstrapStatusFailed
	state.BootstrapTaskID = progress.BootstrapTaskID.String()
	state.BootstrapTaskOutstanding = progress.BootstrapTaskOutstanding
	state.LastTurnID = rt.turn.ID.String()
	if rt.agent.ID != uuid.Nil {
		state.LastResponderID = rt.agent.ID.String()
	}
	applyProjectBootstrapProgressState(&state, progress)
	state.ValidationStatus = projectBootstrapValidationFailed
	state.ValidationFailureClass = failureClass
	state.ValidationFailureReason = failureReason
	state.UpdatedAt = &now
	state.CompletedAt = nil
	state.FailedAt = &now
	state.FailureCategory = record.FailureCategory
	state.FailureClass = record.FailureClass
	state.FailurePhase = record.FailurePhase
	state.FailureReason = record.FailureReason
	if failureCategory == projectFailureCategoryProvider {
		state.ProviderFailureClass = record.FailureClass
		state.ProviderFailureReason = record.FailureReason
	} else {
		state.ProviderFailureClass = ""
		state.ProviderFailureReason = ""
	}

	if err := e.updateProjectBootstrapState(ctx, rt.session, state); err != nil {
		return true, err
	}
	if failErr := e.chat.FailTurn(ctx, rt.turn.ID, record.FailureReason); failErr != nil && !errors.Is(failErr, chat.ErrInvalidStatusTransition) {
		return true, failErr
	}
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildProjectBootstrapAutomaticFailureMessage(record)); err != nil {
		return true, err
	}
	if err := e.applyProjectAutomaticFailure(ctx, rt.session.ScopeID, record); err != nil {
		return true, err
	}
	return true, nil
}

func (e *TurnEngine) handleDeferredBootstrapProviderFailure(ctx context.Context, rt *turnRuntime, progress projectBootstrapProgress, failureClass, failureReason string, now time.Time) (bool, error) {
	if e == nil || rt == nil || rt.turn == nil || rt.session == nil {
		return false, nil
	}

	state := projectBootstrapStateFromMetadata(rt.session.Metadata)
	if state.StartedAt == nil {
		startedAt := rt.startedAt.UTC()
		state.StartedAt = &startedAt
	}
	state.Status = projectBootstrapStatusActive
	state.BootstrapTaskID = progress.BootstrapTaskID.String()
	state.BootstrapTaskOutstanding = progress.BootstrapTaskOutstanding
	state.LastTurnID = rt.turn.ID.String()
	if rt.agent.ID != uuid.Nil {
		state.LastResponderID = rt.agent.ID.String()
	}
	applyProjectBootstrapProgressState(&state, progress)
	state.ValidationStatus = projectBootstrapValidationFailed
	state.ValidationFailureClass = failureClass
	state.ValidationFailureReason = failureReason
	state.UpdatedAt = &now
	state.CompletedAt = nil
	state.FailedAt = nil
	state.FailureCategory = projectFailureCategoryProvider
	state.FailureClass = failureClass
	state.FailurePhase = projectBootstrapLastCheckpoint(progress)
	state.FailureReason = failureReason
	state.ProviderFailureClass = failureClass
	state.ProviderFailureReason = failureReason
	if err := e.updateProjectBootstrapState(ctx, rt.session, state); err != nil {
		return true, err
	}

	// Provider-backed bootstrap retries are deferred work, not a distinct turn stop class.
	rt.stopReason = stopReasonMaxDuration
	if err := e.recordStopReason(ctx, rt); err != nil {
		return true, err
	}
	if failErr := e.chat.FailTurn(ctx, rt.turn.ID, failureReason); failErr != nil && !errors.Is(failErr, chat.ErrInvalidStatusTransition) {
		return true, failErr
	}
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildProjectBootstrapProviderRetryMessage(failureReason)); err != nil {
		return true, err
	}

	nextPayload := AgentTurnPayload{
		SessionID: rt.session.ID,
		MessageID: rt.initialMessageID,
	}
	if rt.agent.ID != uuid.Nil {
		nextAgentID := rt.agent.ID
		nextPayload.AgentID = &nextAgentID
	}
	runAfter := now.Add(retryBackoff(rt.modelRetryUsed + 1))
	_, err := e.enqueueAgentTurnIfActive(ctx, rt.session, nextPayload, &runAfter)
	return true, err
}

func buildProjectBootstrapProviderRetryMessage(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		trimmed = "provider outage"
	}
	return fmt.Sprintf("[Project bootstrap delayed: %s. Bootstrap setup has already persisted, so the project session remains active and will retry automatically.]", trimmed)
}

func (e *TurnEngine) pauseProjectAfterExecutionFailure(ctx context.Context, rt *turnRuntime, cause error) error {
	if rt == nil || rt.turn == nil || rt.session == nil {
		return nil
	}
	if isRecoverableProjectExecutionFailure(cause) {
		return nil
	}

	projectID := resolveProjectID(ctx, rt.session, e.tasks)
	if projectID == nil || *projectID == uuid.Nil {
		return nil
	}

	progress, err := e.loadProjectBootstrapProgress(ctx, *projectID)
	if err != nil {
		return err
	}
	if !projectBootstrapReachedFirstWaveClaim(progress) {
		return nil
	}

	failureCategory := projectFailureCategoryExecution
	failureClass := projectFailureClassExecutionRuntime
	failureReason := summarizeFailure(cause)
	if providerClass, providerReason, ok := classifyProjectProviderFailure(cause); ok {
		failureCategory = projectFailureCategoryProvider
		failureClass = providerClass
		if strings.TrimSpace(providerReason) != "" {
			failureReason = providerReason
		}
	}
	record := buildProjectExecutionFailureRecord(progress, failureCategory, failureClass, failureReason, e.now().UTC())
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildProjectExecutionPauseMessage(record)); err != nil {
		return err
	}
	return e.applyProjectAutomaticFailure(ctx, *projectID, record)
}

func isRecoverableExecutionContinuationDepthError(err error) bool {
	return errors.Is(err, errContextCompressionContinuationDepthExceeded) ||
		errors.Is(err, errAgentTurnPromptGuardrailDepthExceeded)
}

func isRecoverableProjectExecutionFailure(err error) bool {
	return isTransientInfrastructureError(err) ||
		isTransientModelError(err) ||
		isRecoverableExecutionContinuationDepthError(err)
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
	if err := e.pauseProjectAfterExecutionFailure(ctx, rt, cause); err != nil {
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
	if draft, ok := e.latestSubstantiveAssistantDraftContent(ctx, rt, targetPath); ok {
		return draft, "", true
	}
	draft, ok := e.latestRecoveryAssistantDraftContent(ctx, rt)
	if ok {
		if reason := recoveryFileWriteDraftRejectReason(draft, targetPath); reason != "" {
			if priorDraft, priorOK := e.latestPriorSubstantiveAssistantDraftContent(ctx, rt, targetPath); priorOK {
				return priorDraft, "", true
			}
			if summaryDraft, summaryOK := e.latestContinuationSummaryDraftContent(ctx, rt, targetPath); summaryOK {
				return summaryDraft, "", true
			}
			if historicalDraft, historicalOK := e.latestTaskHistoricalSubstantiveDraftContent(ctx, rt, targetPath); historicalOK {
				return historicalDraft, "", true
			}
			if persistedDraft, persistedRejectReason, persistedOK := e.recoveryPersistedDraftContent(ctx, rt, targetPath); persistedOK && strings.TrimSpace(persistedRejectReason) == "" {
				return persistedDraft, "", true
			}
			return draft, reason, false
		}
		if looksLikeRecoveryFileDraft(draft) {
			return draft, "", true
		}
	}
	if draft, ok := e.latestPriorSubstantiveAssistantDraftContent(ctx, rt, targetPath); ok {
		return draft, "", true
	}
	if draft, ok := e.latestContinuationSummaryDraftContent(ctx, rt, targetPath); ok {
		return draft, "", true
	}
	if draft, ok := e.latestTaskHistoricalSubstantiveDraftContent(ctx, rt, targetPath); ok {
		return draft, "", true
	}
	return e.recoveryPersistedDraftContent(ctx, rt, targetPath)
}

func (e *TurnEngine) recoveryPersistedDraftContent(ctx context.Context, rt *turnRuntime, targetPath string) (string, string, bool) {
	if e == nil || rt == nil {
		return "", "", false
	}
	state, ok := e.loadRecoveryResumeState(ctx, rt)
	if !ok {
		return "", "", false
	}
	if checkpointTarget := strings.TrimSpace(state.targetPath); checkpointTarget != "" && checkpointTarget != strings.TrimSpace(targetPath) {
		return "", "", false
	}
	if draft := strings.TrimSpace(state.targetDraft); draft != "" && strings.TrimSpace(state.targetDraftRejectedReason) == "" {
		return draft, "", true
	}
	if draft := strings.TrimSpace(state.artifactDraft); draft != "" && strings.TrimSpace(state.artifactDraftRejectedReason) == "" {
		return draft, "", true
	}
	if draft := strings.TrimSpace(state.summaryDraft); draft != "" && strings.TrimSpace(state.summaryDraftRejectedReason) == "" {
		return draft, "", true
	}
	return "", "", false
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

func (e *TurnEngine) latestRecoveryArtifactDraftContent(ctx context.Context, rt *turnRuntime, targetPath string) (string, bool) {
	if e == nil || e.messages == nil || rt == nil || rt.session == nil || rt.turn == nil {
		return "", false
	}
	messages, err := e.messages.ListBySession(ctx, rt.session.ID)
	if err != nil {
		return "", false
	}
	return latestRecoveryArtifactDraftForTurn(messages, rt.turn.ID, targetPath)
}

func (e *TurnEngine) latestPriorSubstantiveAssistantDraftContent(ctx context.Context, rt *turnRuntime, targetPath string) (string, bool) {
	if e == nil || e.messages == nil || rt == nil || rt.session == nil || rt.turn == nil {
		return "", false
	}
	messages, err := e.messages.ListBySession(ctx, rt.session.ID)
	if err != nil {
		return "", false
	}
	if draft := latestPriorSubstantiveAssistantFinal(messages, rt.turn.ID, targetPath); draft != nil {
		return draft.Content, true
	}
	return "", false
}

func (e *TurnEngine) latestContinuationSummaryDraftContent(ctx context.Context, rt *turnRuntime, targetPath string) (string, bool) {
	if e == nil || e.messages == nil || rt == nil || rt.session == nil {
		return "", false
	}
	messages, err := e.messages.ListBySession(ctx, rt.session.ID)
	if err != nil {
		return "", false
	}
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if !strings.EqualFold(strings.TrimSpace(message.Role), "system") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(message.Status), "final") {
			continue
		}
		content := continuationSummaryDraftContent(message.Content)
		if content == "" {
			continue
		}
		if reason := recoveryFileWriteDraftRejectReason(content, targetPath); reason != "" {
			continue
		}
		if !looksLikeRecoveryFileDraft(content) {
			continue
		}
		return content, true
	}
	return "", false
}

func (e *TurnEngine) latestTaskHistoricalSubstantiveDraftContent(ctx context.Context, rt *turnRuntime, targetPath string) (string, bool) {
	if e == nil || e.pool == nil || rt == nil || rt.session == nil || rt.session.ScopeID == uuid.Nil {
		return "", false
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project_task") {
		return "", false
	}

	rows, err := e.pool.Query(ctx, `
		SELECT m.content
		FROM chat_session cs
		JOIN chat_message m ON m.session_id = cs.id
		WHERE cs.scope_type = 'project_task'
		  AND cs.scope_id = $1
		  AND cs.id <> $2
		  AND m.role = 'assistant'
		  AND m.status = 'final'
		ORDER BY m.created_at DESC
		LIMIT 100
	`, rt.session.ScopeID, rt.session.ID)
	if err != nil {
		return "", false
	}
	defer rows.Close()

	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return "", false
		}
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		if reason := recoveryFileWriteDraftRejectReason(content, targetPath); reason != "" {
			continue
		}
		if !looksLikeRecoveryFileDraft(content) {
			continue
		}
		return content, true
	}
	return "", false
}

func (e *TurnEngine) latestSubstantiveAssistantDraftContent(ctx context.Context, rt *turnRuntime, targetPath string) (string, bool) {
	if e == nil || e.messages == nil || rt == nil || rt.session == nil || rt.turn == nil {
		return "", false
	}
	messages, err := e.messages.ListBySession(ctx, rt.session.ID)
	if err != nil {
		return "", false
	}
	draft := latestSubstantiveAssistantFinalForTurn(messages, rt.turn.ID, targetPath)
	if draft == nil {
		return "", false
	}
	return draft.Content, true
}

func latestPriorSubstantiveAssistantFinal(messages []repo.ChatMessage, currentTurnID uuid.UUID, targetPath string) *repo.ChatMessage {
	var latest *repo.ChatMessage
	for i := range messages {
		message := messages[i]
		if message.TurnID == nil || *message.TurnID == currentTurnID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(message.Status), "final") {
			continue
		}
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		if reason := recoveryFileWriteDraftRejectReason(content, targetPath); reason != "" {
			continue
		}
		if !looksLikeRecoveryFileDraft(content) {
			continue
		}
		if latest == nil || message.SequenceNumber > latest.SequenceNumber {
			copyMessage := message
			latest = &copyMessage
		}
	}
	return latest
}

func latestRecoveryArtifactDraftForTurn(messages []repo.ChatMessage, turnID uuid.UUID, targetPath string) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.TurnID == nil || *message.TurnID != turnID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(message.Role), "tool_result") {
			continue
		}
		toolName, output, errText, ok := parseToolResultMessage(message.Content)
		if !ok || strings.TrimSpace(errText) != "" || !strings.EqualFold(strings.TrimSpace(toolName), "file.read") {
			continue
		}
		recoveredTargetPath, usedArtifactPath := recoveryTargetPathFromToolOutput(output)
		if !usedArtifactPath || strings.TrimSpace(recoveredTargetPath) == "" {
			continue
		}
		if strings.TrimSpace(targetPath) != "" && !sameWorkspaceRelativePath(recoveredTargetPath, targetPath) {
			continue
		}
		draft := strings.TrimSpace(recoveryArtifactDraftContent(anyString(output["content"])))
		if draft == "" {
			continue
		}
		return draft, true
	}
	return "", false
}

func continuationSummaryDraftContent(content string) string {
	trimmed := strings.TrimSpace(content)
	const prefix = "[Continuation summary]"
	if !strings.HasPrefix(trimmed, prefix) {
		return ""
	}
	summary := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
	if strings.EqualFold(summary, "Continuation summary unavailable.") {
		return ""
	}
	return summary
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

func latestSubstantiveAssistantFinalForTurn(messages []repo.ChatMessage, turnID uuid.UUID, targetPath string) *repo.ChatMessage {
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
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		if reason := recoveryFileWriteDraftRejectReason(content, targetPath); reason != "" {
			continue
		}
		if !looksLikeRecoveryFileDraft(content) {
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
	firstLine := lower
	if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
		firstLine = strings.TrimSpace(firstLine[:idx])
	}
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
	if containsAny(lower,
		"let me write the comprehensive",
		"let me write the full",
		"let me write the complete",
		"let me now write",
		"let me check the workspace structure",
		"i now have all four locked strategy artifacts",
		"the immediate directive is clear",
		"i see there are already stub files",
		"production-ready migration plan",
		"target file:",
	) && containsAny(lower,
		"migration plan",
		"plan",
		"document",
		"strategy artifacts",
		"stub files",
	) {
		return false
	}
	if len(trimmed) >= 180 && !hasStructuredRecoveryFileDraftMarkers(trimmed) && containsAny(firstLine,
		"let me ",
		"good.",
		"excellent.",
		"got it.",
		"perfect.",
		"i need to provide",
		"i need to write",
		"i need to create",
		"i need to complete",
		"i now have the strategy artifacts",
		"i now have the full strategy context",
		"i see the issue",
		"i apologize for the error",
		"i'll begin recovery",
		"i'm ready to work",
		"i'm ready to help",
	) {
		return false
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
	if looksLikeGenericTaskRecoveryReply(trimmed) || looksLikeRecoveryQuestionEcho(trimmed) {
		return fmt.Sprintf("assistant draft for %s repeated a generic recovery reply instead of the file body", path)
	}
	if len(trimmed) < 240 && containsAny(lower,
		"now write",
		"now let me write",
		"let me write",
		"let me create",
		"let me continue",
		"now i need to write",
		"now i need to create",
		"let me use",
		"let me try",
		"let me check",
	) && containsAny(lower,
		"migration plan",
		"infrastructure spec",
		"infrastructure planning task",
		"infrastructure specification",
		"specification",
		"spec file",
		"deployment checklist",
		"checklist",
		"document",
		"deliverable",
		"continuation summary draft",
		"concrete deliverables",
		"full content",
		"full file",
		"full draft",
		"file_write",
		"file.write",
		"directory",
		"approach",
	) {
		return fmt.Sprintf("assistant draft for %s described intent to write the deliverable instead of the file body", path)
	}
	if containsAny(lower,
		"current_flow_node_id",
		"execution doesn't exist yet",
		"task shows current_flow_node_id",
	) {
		return fmt.Sprintf("assistant draft for %s described runtime status analysis instead of the file body", path)
	}
	if containsAny(lower,
		"flow_node_execution_id",
		"can you provide the flow_node_execution_id",
		"if you'd like me to check the current task state",
		"determine the active flow node",
	) {
		return fmt.Sprintf("assistant draft for %s asked for runtime control-plane input instead of the file body", path)
	}
	if containsAny(lower,
		"what is the current state you need me to continue from",
		"do you want me to complete the deployment checklist",
		"or do you need me to start fresh on the full infrastructure specification",
		"should confirm whether that's the priority",
		"i now have the recovery context",
		"perfect! now i see the situation clearly",
		"now i see the situation clearly",
		"now let me replace it with the complete",
		"now i'll write the complete",
		"now i will write the complete",
		"let me try a different approach - delete and recreate",
		"let me try a different approach",
		"let me check the strategy artifacts to understand the locked decisions before proceeding",
		"let me first check the current state of the target file and recovery artifacts",
		"using the durable draft above",
		"using the substantive draft provided above",
	) {
		return fmt.Sprintf("assistant draft for %s asked the operator to choose the next step instead of the file body", path)
	}
	if (containsAny(lower,
		"i'll build:",
		"i will build:",
		"let me start:",
		"let me create a comprehensive",
		"let me create the comprehensive",
		"based on the task description and the planning artifacts",
		"i need to build `",
		"production-ready reporting script",
	) || strings.Contains(lower, "ready to integrate with upstream data pipelines")) &&
		(strings.Contains(trimmed, "1. **") || strings.Contains(trimmed, "2. **") || strings.Contains(trimmed, "\n1. ") || strings.Contains(trimmed, "\n2. ") || strings.Contains(trimmed, "\n- ")) {
		return fmt.Sprintf("assistant draft for %s described the implementation plan instead of the file body", path)
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
	if containsAny(lower,
		"`file_write`",
		"`file.write`",
		"file_write",
		"file.write",
	) && containsAny(lower,
		"content parameter",
		"path parameter",
		"requires a non-empty `content`",
		"requires a non-empty `path`",
		"requires a non-empty content",
		"requires a non-empty path",
		"passing it in the `content` field",
		"passing it in the content field",
		"cannot proceed without drafting the complete",
	) {
		return fmt.Sprintf("assistant draft for %s described tool-recovery troubleshooting instead of the file body", path)
	}
	if looksLikeRecoveryIntentNarrationPlaceholder(trimmed) {
		return fmt.Sprintf("assistant draft for %s described intent to write the deliverable instead of the file body", path)
	}
	if looksLikeStructuredRecoveryIntentPlaceholder(trimmed) {
		return fmt.Sprintf("assistant draft for %s described intent to write the deliverable instead of the file body", path)
	}
	if statusPath, ok := leadingToolStatusTargetPath(trimmed); ok {
		if strings.TrimSpace(targetPath) != "" && !sameWorkspaceRelativePath(statusPath, targetPath) {
			return fmt.Sprintf("assistant draft for %s appears to belong to a different deliverable than the target file body", path)
		}
		return fmt.Sprintf("assistant draft for %s repeated wrapped tool status instead of the file body", path)
	}
	if heading := leadingMarkdownHeading(trimmed); heading != "" && headingClearlyMismatchesTargetPath(heading, targetPath) {
		return fmt.Sprintf("assistant draft for %s appears to belong to a different deliverable than the target file body", path)
	}
	return ""
}

func leadingToolStatusTargetPath(content string) (string, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", false
	}
	firstLine := trimmed
	if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
		firstLine = strings.TrimSpace(firstLine[:idx])
	}
	lower := strings.ToLower(firstLine)
	for _, prefix := range []string{"file written:", "file edited:", "file updated:", "file created:"} {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		remaining := strings.TrimSpace(firstLine[len(prefix):])
		if strings.HasPrefix(remaining, "`") {
			remaining = strings.TrimPrefix(remaining, "`")
			if idx := strings.Index(remaining, "`"); idx >= 0 {
				remaining = remaining[:idx]
			}
		}
		remaining = strings.TrimSpace(remaining)
		if remaining == "" {
			return "", false
		}
		return remaining, true
	}
	return "", false
}

func leadingMarkdownHeading(content string) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			return strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		}
		break
	}
	return ""
}

func headingClearlyMismatchesTargetPath(heading, targetPath string) bool {
	targetTokens := deliverableMatchTokens(filepath.Base(strings.TrimSpace(targetPath)))
	headingTokens := deliverableMatchTokens(heading)
	if len(targetTokens) < 2 || len(headingTokens) < 2 {
		return false
	}
	for token := range headingTokens {
		if _, ok := targetTokens[token]; ok {
			return false
		}
	}
	return true
}

func deliverableMatchTokens(raw string) map[string]struct{} {
	cleaned := strings.ToLower(strings.TrimSpace(raw))
	cleaned = strings.TrimSuffix(cleaned, filepath.Ext(cleaned))
	replacer := strings.NewReplacer("-", " ", "_", " ", "/", " ", ".", " ", ":", " ")
	cleaned = replacer.Replace(cleaned)
	fields := strings.Fields(cleaned)
	tokens := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if strings.HasPrefix(field, "oc") && len(field) > 2 {
			if _, err := strconv.Atoi(strings.TrimLeft(field[2:], "0")); err == nil {
				continue
			}
		}
		switch field {
		case "md", "spec", "specification", "document", "draft", "deliverable", "complete", "final", "the", "and":
			continue
		}
		if len(field) < 3 {
			continue
		}
		tokens[field] = struct{}{}
	}
	return tokens
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
	if hasStructuredRecoveryFileDraftMarkers(trimmed) || strings.Count(trimmed, "\n") >= 3 || len(trimmed) > 900 {
		return false
	}

	lower := strings.ToLower(trimmed)
	wordCount := len(strings.Fields(trimmed))
	if containsAny(lower, "the single deliverable", "final deliverable") {
		return true
	}
	if containsAny(lower,
		"i have the context",
		"is in the work node",
		"needs execution",
		"now i see the situation clearly",
	) && containsAny(lower,
		"now write",
		"write the",
		"write a",
		"write this",
	) {
		return true
	}
	if containsAny(lower,
		"now i have the complete context",
		"strategy phase for",
		"resume execution by creating the actual",
	) && containsAny(lower,
		"migration plan",
		"document",
		"deliverable",
	) {
		return true
	}
	hasWriteIntent := containsAny(lower,
		"let me write",
		"let me draft",
		"let me create",
		"let me complete",
		"let me resume the task by completing",
		"let me resume by completing",
		"i need to create",
		"i need to complete",
		"i need to write a comprehensive",
		"i need to write the comprehensive",
		"need to create the",
		"need to complete the",
		"now i'll write",
		"now i will write",
		"now let me write",
		"now let me create",
		"let me now create",
		"let me now write",
		"i'll write",
		"i will write",
		"now write",
		"i'll draft",
		"i will draft",
		"i'll create",
		"i will create",
		"here is the draft",
		"below is the draft",
		"i'm going to write",
		"i am going to write",
		"time to write",
		"time to draft",
		"ready to write",
		"ready to draft",
		"write the full",
		"write the comprehensive",
		"then write",
		"then draft",
		"draft the full",
		"draft the comprehensive",
		"should write",
		"should draft",
	)
	hasSetupCue := containsAny(lower,
		"good. i have the context",
		"i have the context",
		"now i have the complete context",
		"i have the complete context",
		"now i have everything i need",
		"i now have everything i need",
		"i have everything i need",
		"now i have the full context",
		"i have the full context",
		"i understand the full context",
		"i have recovered the full context",
		"now i have all the context needed",
		"i have all the context needed",
		"i have enough context",
		"i have a complete picture",
		"i now have a complete picture",
		"i now have the complete picture",
		"i have what i need",
		"now that i have",
		"i now have the full",
		"i now have complete context",
		"i now have the full strategy context",
		"i now have the full recovery context",
		"i understand the task context",
		"i have the full task context",
		"i understand the full task context",
		"i need to resume",
		"the recovery state indicates",
		"the planning artifacts are complete",
		"the strategy artifacts are complete",
		"the strategy work is complete",
		"there's a skeleton file already",
		"the target file contains only a placeholder",
		"the target file currently just has a placeholder",
		"the migration plan file is just a stub",
		"the migration plan file is stubbed but incomplete",
		"the file exists but only contains a stub",
		"let me try a different approach",
		"delete and recreate",
		"i need to read the checkpoint artifacts",
		"i'll read the target file and recovery artifact",
		"let me check what's in the workspace",
		"let me check the acceptance criteria",
		"let me check the dependency log",
		"let me first examine the current state of the project and task",
		"let me examine the current state of the project and task",
		"let me read the strategy artifacts that are already locked",
		"let me read those to understand the locked decisions",
		"let me check the task flow and understand what step we're on",
		"before i proceed with drafting",
		"the decisions are locked and clear",
		"i need to confirm",
		"the draft document exists but is incomplete",
		"continue from where it was cut off",
		"resume execution by creating the actual",
	)
	hasDeliverableCue := containsAny(lower,
		"deliverable",
		"document",
		"strategy",
		"spec",
		"specification",
		"plan",
		"report",
		"unblocks",
		"foundation for",
		"strategic foundation",
	)
	if hasWriteIntent && (hasSetupCue || (wordCount <= 80 && hasDeliverableCue)) {
		return true
	}
	if containsAny(lower,
		"i have a complete picture",
		"i now have a complete picture",
		"the draft document exists but is incomplete",
		"continue from where it was cut off",
	) && containsAny(lower,
		"let me ",
		"i'll continue",
		"i will continue",
		"write the",
		"draft the",
	) && hasDeliverableCue {
		return true
	}
	if wordCount <= 80 && containsAny(lower,
		"critical deliverable",
		"deliverable that unblocks",
		"deliverable for",
		"unblocks",
		"strategic foundation",
		"foundation for",
	) && containsAny(lower,
		"deliverable",
		"document",
		"strategy",
		"write",
		"draft",
	) {
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

func looksLikeStructuredRecoveryIntentPlaceholder(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	wordCount := len(strings.Fields(trimmed))
	if wordCount < 20 || wordCount > 180 {
		return false
	}
	hasContextCue := containsAny(lower,
		"good. i have the context",
		"now i have enough context",
		"i have enough context",
		"i can see:",
		"i can see that:",
		"now i have the full context",
		"i have the full context",
		"i now have the full context",
		"i now have all the context needed",
		"i have all the context needed",
		"the strategy for ",
		"the strategy work is locked",
		"the strategy work is complete",
		"the planning artifacts are complete",
		"the planning artifacts are locked",
		"the strategy artifacts are complete",
		"the decisions are locked and clear",
		"the checkpoint indicates i should",
		"this is a durable recovery checkpoint",
		"success narrative partially drafted",
		"current flow node:",
		"current_flow_node_id:",
		"execution doesn't exist yet",
		"task shows current_flow_node_id",
		"is in the work node",
		"needs execution",
		"strategy artifacts:",
		"target file:",
	)
	hasIntentCue := containsAny(lower,
		"now i need to move forward to the",
		"now i'll write",
		"now i will write",
		"now let me write",
		"let me write",
		"let me draft",
		"i need to create",
		"i need to complete",
		"i'll write",
		"i will write",
		"this is the deliverable",
		"this is the deliverable that",
		"translates strategy into executability",
		"write the comprehensive",
		"draft the comprehensive",
		"let me read the oc-15 strategy artifacts",
		"let me read the strategy artifacts",
		"let me check the task flow and understand what step we're on",
		"let me check what flow template is assigned",
		"let me check the full file",
		"let me check the file",
	)
	hasStructuredLeadIn := strings.Count(trimmed, "\n") >= 2 && hasStructuredRecoveryFileDraftMarkers(trimmed)
	return hasStructuredLeadIn && hasContextCue && hasIntentCue
}

func looksLikeRecoveryQuestionEcho(content string) bool {
	normalized := strings.ToLower(normalizeInstructionText(content))
	if normalized == "" {
		return false
	}
	return containsAny(normalized,
		"quick clarification",
		"what would you like me to do",
		"what would you like me to help with",
		"please let me know the most useful next step",
		"continue drafting the success narrative",
		"move to the decision log or tradeoff matrix",
		"read existing planning artifacts to ground the",
	)
}

func (e *TurnEngine) strengthenRecoveryDraftRejectFailureReason(ctx context.Context, rt *turnRuntime, targetPath, currentReason string) string {
	current := strings.TrimSpace(currentReason)
	if current == "" || !taskcheckpoint.RecoveryFileWriteFailureRejectsDraft(current) {
		return current
	}
	priorReason := ""
	if checkpoint, ok := e.currentRecoveryFileWriteCheckpoint(ctx, rt); ok {
		priorReason = strings.TrimSpace(checkpoint.FailureReason)
	}
	if !taskcheckpoint.RecoveryFileWriteFailureRejectsDraft(priorReason) {
		return current
	}

	path := strings.TrimSpace(targetPath)
	if path == "" {
		path = "the requested workspace file"
	}
	if taskcheckpoint.RecoveryFileWriteFailureIsIntentOnly(priorReason) && taskcheckpoint.RecoveryFileWriteFailureIsIntentOnly(current) {
		return fmt.Sprintf("repeated intent-only recovery drafts for %s across explicit resume attempts; latest %s", path, current)
	}
	return fmt.Sprintf("repeated non-substantive recovery drafts for %s across explicit resume attempts; latest %s", path, current)
}

func (e *TurnEngine) recoveryCheckpointPriorFailureReasons(ctx context.Context, rt *turnRuntime, currentReason string) []string {
	if e == nil || rt == nil {
		return nil
	}
	checkpoint, ok := e.currentRecoveryFileWriteCheckpoint(ctx, rt)
	if !ok {
		return nil
	}
	reasons := append([]string(nil), taskcheckpoint.RecoveryFileWriteFailureHistory(checkpoint)...)
	current := strings.TrimSpace(currentReason)
	if len(reasons) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if strings.TrimSpace(reason) == "" || reason == current {
			continue
		}
		filtered = append(filtered, reason)
	}
	normalized := taskcheckpoint.NormalizeRecoveryFileWriteCheckpoint(taskcheckpoint.RecoveryFileWriteCheckpoint{
		PriorFailureReasons: filtered,
	})
	return append([]string(nil), normalized.PriorFailureReasons...)
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
	priorFailureReasons := e.recoveryCheckpointPriorFailureReasons(ctx, rt, failureReason)
	document := buildRecoveryFileWriteArtifactDocument(buildTaskLabel(taskRecord), targetRel, draft, failureReason, priorFailureReasons, e.now())
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
	targetPath = strings.TrimSpace(targetPath)
	artifactPath = strings.TrimSpace(artifactPath)
	if existing, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata); ok {
		existingTarget := strings.TrimSpace(existing.TargetPath)
		if existingTarget != "" && !sameWorkspaceRelativePath(existingTarget, targetPath) {
			if existingDraft, found := e.readRecoveryWorkspaceText(ctx, rt, existingTarget); found {
				if reason := recoveryFileWriteDraftRejectReason(existingDraft, existingTarget); reason == "" && looksLikeRecoveryFileDraft(existingDraft) {
					targetPath = existingTarget
					if existingArtifact := strings.TrimSpace(existing.ArtifactPath); existingArtifact != "" {
						if recoveredTarget, ok := recoveryTargetPathFromArtifact(existingArtifact); ok && sameWorkspaceRelativePath(recoveredTarget, existingTarget) {
							artifactPath = existingArtifact
						} else {
							artifactPath = ""
						}
					}
				}
			}
		}
	}
	if historicalTarget, _, ok := e.recoveryHistoricalSubstantiveOutputContext(ctx, rt); ok && historicalTarget != "" && !sameWorkspaceRelativePath(historicalTarget, targetPath) {
		if currentDraft, found := e.readRecoveryWorkspaceText(ctx, rt, targetPath); !found || recoveryFileWriteDraftRejectReason(currentDraft, targetPath) != "" || !looksLikeRecoveryFileDraft(currentDraft) {
			targetPath = historicalTarget
			if artifactPath != "" {
				if recoveredTarget, ok := recoveryTargetPathFromArtifact(artifactPath); ok && !sameWorkspaceRelativePath(recoveredTarget, historicalTarget) {
					artifactPath = ""
				}
			}
		}
	}
	priorFailureReasons := e.recoveryCheckpointPriorFailureReasons(ctx, rt, failureReason)
	checkpoint := taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:            targetPath,
		ArtifactPath:          artifactPath,
		FailureReason:         strings.TrimSpace(failureReason),
		PriorFailureReasons:   priorFailureReasons,
		HistoryStartMessageID: messageID.String(),
		HaltTurnID:            rt.turn.ID.String(),
		UpdatedAt:             e.now().UTC().Format(time.RFC3339Nano),
	}
	merged, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(taskRecord.Metadata, checkpoint)
	if err != nil {
		return err
	}
	taskRecord.Metadata = merged
	if _, err := updateTurnTaskMetadata(ctx, e.tasks, taskRecord); err != nil {
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
	if _, err := updateTurnTaskMetadata(ctx, e.tasks, taskRecord); err != nil {
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
		if rt.recoveryTurn {
			rt.recoveryWriteDone = true
		}
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

func buildRecoveryFileWriteArtifactDocument(taskLabel, targetPath, draft, failureReason string, priorFailureReasons []string, now time.Time) string {
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
	if len(priorFailureReasons) != 0 {
		lines = append(lines, "## Prior Failure History", "")
		for _, reason := range priorFailureReasons {
			lines = append(lines, "- "+strings.TrimSpace(reason))
		}
		lines = append(lines, "")
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
			if rt.recoveryTurn {
				rt.recoveryWriteDone = true
			}
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
		if !ok || current.Blocked || current.Count == 0 {
			return false, nil
		}
		if len(calls) == 0 {
			return false, nil
		}
		if current.InitialMessageID == rt.initialMessageID.String() && current.AttemptFingerprint != "" && !toolCallsContainAttemptFingerprint(calls, current.AttemptFingerprint) {
			return false, nil
		}
		cleared, clearErr := clearTaskValidationGuardMetadata(taskRecord.Metadata)
		if clearErr != nil {
			return false, clearErr
		}
		taskRecord.Metadata = cleared
		if _, err := updateTurnTaskMetadata(ctx, e.tasks, taskRecord); err != nil {
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
	updatedTask, err := updateTurnTaskMetadata(ctx, e.tasks, taskRecord)
	if err != nil {
		return false, err
	}

	if !blockedNow {
		return false, nil
	}

	if !strings.EqualFold(strings.TrimSpace(updatedTask.WorkStatus), "blocked") {
		if e.taskTransitions == nil {
			return false, fmt.Errorf(errMissingTaskTransitionServiceForValidationBlock)
		}
		blockReason := buildValidationLoopBlockReason(next)
		if _, err := e.taskTransitions.MarkBlocked(ctx, updatedTask.ID, blockReason, tasksvc.Actor{Type: "system"}); err != nil {
			return false, err
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

func messageRequestsFreshKickoff(session *chat.ChatSession, message repo.ChatMessage) bool {
	if isFreshKickoffRequest(session, message) {
		return true
	}
	metadata := messageMetadataMap(message.Metadata)
	fresh, _ := metadata["fresh_kickoff"].(bool)
	return fresh
}

func normalizeRoutedAgentForSession(session *chat.ChatSession, routedAgentID *uuid.UUID) *uuid.UUID {
	if session == nil || routedAgentID == nil || *routedAgentID == uuid.Nil {
		return routedAgentID
	}
	if strings.EqualFold(strings.TrimSpace(session.ScopeType), "project_task") && strings.EqualFold(strings.TrimSpace(session.Mode), "async") {
		return nil
	}
	return routedAgentID
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

func taskContinuationResumeMessageRootsHistory(message repo.ChatMessage) bool {
	if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
		return false
	}
	metadata := messageMetadataMap(message.Metadata)
	if strings.EqualFold(strings.TrimSpace(stringValue(metadata["source"])), taskContinuationResumeMessageSource) {
		return true
	}
	rooted, _ := metadata["continuation_root"].(bool)
	return rooted
}

func taskContinuationResumeMessageMetadata(session *chat.ChatSession, attempt int) json.RawMessage {
	if attempt < 1 {
		attempt = 1
	}
	payload := map[string]any{
		"source":                 taskContinuationResumeMessageSource,
		"continuation_root":      true,
		"continuation_attempt":   attempt,
		"synthetic_user_message": true,
	}
	if executionID := flowNodeExecutionIDFromSessionMetadata(session); executionID != nil && *executionID != uuid.Nil {
		payload["flow_node_execution_id"] = executionID.String()
	}
	return mustJSONRaw(payload)
}

func syntheticContinuationActionMessageMetadata(session *chat.ChatSession, source string) json.RawMessage {
	payload := map[string]any{
		"source":                 strings.TrimSpace(source),
		"synthetic_user_message": true,
	}
	if executionID := flowNodeExecutionIDFromSessionMetadata(session); executionID != nil && *executionID != uuid.Nil {
		payload["flow_node_execution_id"] = executionID.String()
	}
	return mustJSONRaw(payload)
}

func (e *TurnEngine) shouldAppendSyntheticUserPrompt(ctx context.Context, sessionID uuid.UUID, source string) (bool, error) {
	if e == nil || e.messages == nil || sessionID == uuid.Nil {
		return true, nil
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return true, nil
	}
	messages, err := e.messages.ListBySession(ctx, sessionID)
	if err != nil {
		return false, err
	}
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "user") || !strings.EqualFold(strings.TrimSpace(message.Status), "pending") {
			continue
		}
		metadata := messageMetadataMap(message.Metadata)
		synthetic, _ := metadata["synthetic_user_message"].(bool)
		if !synthetic {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(stringValue(metadata["source"])), source) {
			return false, nil
		}
	}
	return true, nil
}

func (e *TurnEngine) taskContinuationResumeAttempt(ctx context.Context, messageID uuid.UUID) int {
	if e == nil || e.messages == nil || messageID == uuid.Nil {
		return 0
	}
	message, err := e.messages.GetByID(ctx, messageID)
	if err != nil {
		return 0
	}
	metadata := messageMetadataMap(message.Metadata)
	if raw, ok := metadata["continuation_attempt"].(float64); ok {
		return int(raw)
	}
	if raw, ok := metadata["continuation_attempt"].(int); ok {
		return raw
	}
	return 0
}

func (e *TurnEngine) bootstrapInitialMessageRequestsFreshKickoff(ctx context.Context, sessionID uuid.UUID, initialMessageID string) (bool, error) {
	if e == nil || e.chat == nil || e.messages == nil {
		return false, nil
	}
	initialID, err := uuid.Parse(strings.TrimSpace(initialMessageID))
	if err != nil || initialID == uuid.Nil {
		return false, nil
	}
	session, err := e.chat.GetSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	message, err := e.messages.GetByID(ctx, initialID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if message.SessionID != sessionID {
		return false, nil
	}
	return messageRequestsFreshKickoff(session, message), nil
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
	if rt.session != nil &&
		rt.session.ScopeID != uuid.Nil &&
		strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") &&
		strings.EqualFold(strings.TrimSpace(rt.session.Mode), "async") {
		progress, err := e.loadProjectBootstrapProgress(ctx, rt.session.ScopeID)
		if err != nil {
			return true, err
		}
		if projectBootstrapSetupPersisted(progress) || progress.PlannedTaskCount > 0 || progress.AssignmentCount > 0 {
			return false, nil
		}
	}
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildFreshKickoffBlockerMessage(rt.projectIdentity, reason)); err != nil {
		return true, err
	}
	if err := e.completeTurn(ctx, rt); err != nil {
		return true, err
	}
	return true, nil
}

func (e *TurnEngine) handleProjectBootstrapGuardrailFailure(ctx context.Context, rt *turnRuntime, reason string) (bool, error) {
	if rt == nil || rt.turn == nil || rt.session == nil || rt.session.ScopeID == uuid.Nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") || !strings.EqualFold(strings.TrimSpace(rt.session.Mode), "async") {
		return false, nil
	}

	progress, err := e.loadProjectBootstrapProgress(ctx, rt.session.ScopeID)
	if err != nil {
		return true, err
	}
	state := projectBootstrapStateFromMetadata(rt.session.Metadata)
	if !e.projectBootstrapRuntimeManaged(ctx, rt.session, rt.initialMessageID) || progress.Materialized() {
		return false, nil
	}
	now := e.now().UTC()
	workflowMessageID := rt.initialMessageID
	if e.messages != nil && rt.initialMessageID != uuid.Nil {
		message, getErr := e.messages.GetByID(ctx, rt.initialMessageID)
		if getErr != nil && !errors.Is(getErr, repo.ErrNotFound) {
			return true, getErr
		}
		if getErr == nil {
			workflowMessageID = projectBootstrapWorkflowMessageID(&message)
		}
	}
	if strings.TrimSpace(state.InitialMessageID) == "" && workflowMessageID != uuid.Nil {
		state.InitialMessageID = workflowMessageID.String()
	}
	if state.StartedAt == nil {
		state.StartedAt = &now
	}
	record := buildProjectBootstrapAutomaticFailureRecord(
		progress,
		projectFailureCategoryBootstrap,
		projectBootstrapFailureGuardrail,
		buildProjectBootstrapGuardrailFailureReason(reason),
		now,
	)
	state.Status = projectBootstrapStatusFailed
	state.BootstrapTaskID = progress.BootstrapTaskID.String()
	state.BootstrapTaskOutstanding = progress.BootstrapTaskOutstanding
	state.LastTurnID = rt.turn.ID.String()
	if rt.agent.ID != uuid.Nil {
		state.LastResponderID = rt.agent.ID.String()
	}
	state.AutoTurnCount = 0
	applyProjectBootstrapProgressState(&state, progress)
	state.ValidationStatus = projectBootstrapValidationFailed
	state.ValidationFailureClass = projectBootstrapFailureGuardrail
	state.ValidationFailureReason = buildProjectBootstrapGuardrailFailureReason(reason)
	state.UpdatedAt = &now
	state.CompletedAt = nil
	state.FailedAt = &now
	state.FailureCategory = record.FailureCategory
	state.FailureClass = record.FailureClass
	state.FailurePhase = record.FailurePhase
	state.FailureReason = record.FailureReason
	state.ProviderFailureClass = ""
	state.ProviderFailureReason = ""

	if err := e.updateProjectBootstrapState(ctx, rt.session, state); err != nil {
		return true, err
	}
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildProjectBootstrapAutomaticFailureMessage(record)); err != nil {
		return true, err
	}
	if err := e.completeTurn(ctx, rt); err != nil {
		return true, err
	}
	if err := e.applyProjectAutomaticFailure(ctx, rt.session.ScopeID, record); err != nil {
		return true, err
	}
	return true, nil
}

func (e *TurnEngine) handleProjectBootstrapWatchdogTimeout(ctx context.Context, rt *turnRuntime, timeoutErr *projectBootstrapTimeoutError) (bool, error) {
	if rt == nil || rt.turn == nil || rt.session == nil || rt.session.ScopeID == uuid.Nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") || !strings.EqualFold(strings.TrimSpace(rt.session.Mode), "async") {
		return false, nil
	}

	progress, err := e.loadProjectBootstrapProgress(ctx, rt.session.ScopeID)
	if err != nil {
		return true, err
	}
	state := projectBootstrapStateFromMetadata(rt.session.Metadata)
	if !e.projectBootstrapRuntimeManaged(ctx, rt.session, rt.initialMessageID) || progress.Materialized() {
		return false, nil
	}
	recoverableValidation := projectBootstrapSetupPersisted(progress) &&
		progress.ValidationFailed() &&
		projectBootstrapRecoverableMaxToolCallFailure(progress)
	now := e.now().UTC()
	workflowMessageID := rt.initialMessageID
	if e.messages != nil && rt.initialMessageID != uuid.Nil {
		message, getErr := e.messages.GetByID(ctx, rt.initialMessageID)
		if getErr != nil && !errors.Is(getErr, repo.ErrNotFound) {
			return true, getErr
		}
		if getErr == nil {
			workflowMessageID = projectBootstrapWorkflowMessageID(&message)
		}
	}
	if strings.TrimSpace(state.InitialMessageID) == "" && workflowMessageID != uuid.Nil {
		state.InitialMessageID = workflowMessageID.String()
	}
	if state.StartedAt == nil {
		startedAt := rt.startedAt.UTC()
		state.StartedAt = &startedAt
	}
	if projectBootstrapSetupPersisted(progress) && (!progress.ValidationFailed() || recoverableValidation) {
		now = e.now().UTC()
		state.Status = projectBootstrapStatusActive
		state.LastTurnID = rt.turn.ID.String()
		if rt.agent.ID != uuid.Nil {
			state.LastResponderID = rt.agent.ID.String()
		}
		state.AutoTurnCount++
		applyProjectBootstrapProgressState(&state, progress)
		if recoverableValidation {
			state.ValidationStatus = ""
			state.ValidationFailureClass = ""
			state.ValidationFailureReason = ""
		}
		state.UpdatedAt = &now
		state.FailedAt = nil
		state.CompletedAt = nil
		state.FailureCategory = ""
		state.FailureClass = ""
		state.FailurePhase = ""
		state.FailureReason = ""
		state.ProviderFailureClass = ""
		state.ProviderFailureReason = ""
		rt.stopReason = stopReasonMaxDuration

		if err := e.recordStopReason(ctx, rt); err != nil {
			return true, err
		}
		if err := e.updateProjectBootstrapState(ctx, rt.session, state); err != nil {
			return true, err
		}
		if failErr := e.chat.FailTurn(ctx, rt.turn.ID, buildProjectBootstrapWatchdogFailureReason(timeoutErr)); failErr != nil && !errors.Is(failErr, chat.ErrInvalidStatusTransition) {
			return true, failErr
		}
		continuationAgentID := e.projectBootstrapContinuationAgent(ctx, rt.session, rt.agent.ID)
		var continuationMessage *chat.ChatMessage
		if recoverableValidation {
			continuationMessage, err = e.appendProjectBootstrapRecoveryContinuationMessage(ctx, rt.session.ID, continuationAgentID, state.InitialMessageID, state.AutoTurnCount, progress)
		} else {
			continuationMessage, err = e.appendProjectBootstrapContinuationMessage(ctx, rt.session.ID, continuationAgentID, state.InitialMessageID, state.AutoTurnCount)
		}
		if err != nil {
			return true, err
		}
		nextPayload := AgentTurnPayload{
			SessionID: rt.session.ID,
			MessageID: continuationMessage.ID,
		}
		if continuationAgentID != uuid.Nil {
			nextAgentID := continuationAgentID
			nextPayload.AgentID = &nextAgentID
		}
		runAfter := now.Add(defaultAutoContinueDelay)
		if _, err := e.enqueueAgentTurnIfActive(ctx, rt.session, nextPayload, &runAfter); err != nil {
			return true, err
		}
		return true, nil
	}
	record := buildProjectBootstrapAutomaticFailureRecord(
		progress,
		projectFailureCategoryBootstrap,
		projectBootstrapFailureStalled,
		buildProjectBootstrapWatchdogFailureReason(timeoutErr),
		now,
	)
	state.Status = projectBootstrapStatusFailed
	state.BootstrapTaskID = progress.BootstrapTaskID.String()
	state.BootstrapTaskOutstanding = progress.BootstrapTaskOutstanding
	state.LastTurnID = rt.turn.ID.String()
	if rt.agent.ID != uuid.Nil {
		state.LastResponderID = rt.agent.ID.String()
	}
	applyProjectBootstrapProgressState(&state, progress)
	state.ValidationStatus = projectBootstrapValidationFailed
	state.ValidationFailureClass = projectBootstrapFailureStalled
	state.ValidationFailureReason = buildProjectBootstrapWatchdogFailureReason(timeoutErr)
	state.UpdatedAt = &now
	state.CompletedAt = nil
	state.FailedAt = &now
	state.FailureCategory = record.FailureCategory
	state.FailureClass = record.FailureClass
	state.FailurePhase = record.FailurePhase
	state.FailureReason = record.FailureReason
	state.ProviderFailureClass = ""
	state.ProviderFailureReason = ""
	rt.stopReason = stopReasonMaxDuration

	if err := e.recordStopReason(ctx, rt); err != nil {
		return true, err
	}
	if err := e.updateProjectBootstrapState(ctx, rt.session, state); err != nil {
		return true, err
	}
	if failErr := e.chat.FailTurn(ctx, rt.turn.ID, state.FailureReason); failErr != nil && !errors.Is(failErr, chat.ErrInvalidStatusTransition) {
		return true, failErr
	}
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildProjectBootstrapAutomaticFailureMessage(record)); err != nil {
		return true, err
	}
	if err := e.applyProjectAutomaticFailure(ctx, rt.session.ScopeID, record); err != nil {
		return true, err
	}
	return true, nil
}

func (e *TurnEngine) handleProjectBootstrapTerminalTurnFailure(ctx context.Context, rt *turnRuntime, cause error) (bool, error) {
	if rt == nil || rt.turn == nil || rt.session == nil || rt.session.ScopeID == uuid.Nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") || !strings.EqualFold(strings.TrimSpace(rt.session.Mode), "async") {
		return false, nil
	}

	progress, err := e.loadProjectBootstrapProgress(ctx, rt.session.ScopeID)
	if err != nil {
		return true, err
	}
	state := projectBootstrapStateFromMetadata(rt.session.Metadata)
	if !e.projectBootstrapRuntimeManaged(ctx, rt.session, rt.initialMessageID) || progress.Materialized() {
		return false, nil
	}

	failureClass := projectBootstrapFailureRuntime
	switch {
	case errors.Is(cause, ErrAuthFailed):
		failureClass = projectBootstrapFailureProviderAuth
	case errors.Is(cause, ErrRateLimited):
		failureClass = projectBootstrapFailureProviderRateLimit
	case isTransientModelError(cause):
		failureClass = projectBootstrapFailureProviderTransient
	}

	now := e.now().UTC()
	workflowMessageID := rt.initialMessageID
	if e.messages != nil && rt.initialMessageID != uuid.Nil {
		message, getErr := e.messages.GetByID(ctx, rt.initialMessageID)
		if getErr != nil && !errors.Is(getErr, repo.ErrNotFound) {
			return true, getErr
		}
		if getErr == nil {
			workflowMessageID = projectBootstrapWorkflowMessageID(&message)
		}
	}
	if strings.TrimSpace(state.InitialMessageID) == "" && workflowMessageID != uuid.Nil {
		state.InitialMessageID = workflowMessageID.String()
	}
	if state.StartedAt == nil {
		startedAt := rt.startedAt.UTC()
		state.StartedAt = &startedAt
	}
	state.Status = projectBootstrapStatusFailed
	state.BootstrapTaskID = progress.BootstrapTaskID.String()
	state.BootstrapTaskOutstanding = progress.BootstrapTaskOutstanding
	state.LastTurnID = rt.turn.ID.String()
	if rt.agent.ID != uuid.Nil {
		state.LastResponderID = rt.agent.ID.String()
	}
	state.AssignmentCount = progress.AssignmentCount
	state.StaffingDraftCount = progress.StaffingDraftCount
	state.PlannedTaskCount = progress.PlannedTaskCount
	state.PlannedFlowTemplateCount = progress.PlannedFlowTemplateCount
	state.FirstWaveTaskCount = progress.FirstWaveTaskCount
	state.FirstWavePromotedCount = progress.FirstWavePromotedCount
	state.FirstWaveExecutionCount = progress.FirstWaveExecutionCount
	state.setFirstWaveJobCount(progress.FirstWaveJobCount)
	state.ValidationStatus = projectBootstrapValidationFailed
	state.ValidationFailureClass = failureClass
	state.ValidationFailureReason = buildProjectBootstrapTerminalFailureReason(projectBootstrapNextPendingCheckpoint(state), cause)
	state.UpdatedAt = &now
	state.CompletedAt = nil
	state.FailedAt = &now
	state.FailureClass = failureClass
	state.FailureReason = state.ValidationFailureReason

	if err := e.updateProjectBootstrapState(ctx, rt.session, state); err != nil {
		return true, err
	}
	if failErr := e.chat.FailTurn(ctx, rt.turn.ID, state.FailureReason); failErr != nil && !errors.Is(failErr, chat.ErrInvalidStatusTransition) {
		return true, failErr
	}
	if _, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildProjectBootstrapFailureMessage(state.FailureReason)); err != nil {
		return true, err
	}
	return true, nil
}

func (e *TurnEngine) projectBootstrapStreamWatchdog(ctx context.Context, rt *turnRuntime) (projectBootstrapWatchdog, bool, error) {
	if e == nil || rt == nil || rt.session == nil || rt.session.ScopeID == uuid.Nil || e.projectBootstrapTurnTimeout <= 0 {
		return projectBootstrapWatchdog{}, false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") || !strings.EqualFold(strings.TrimSpace(rt.session.Mode), "async") {
		return projectBootstrapWatchdog{}, false, nil
	}
	if e.messages == nil || rt.initialMessageID == uuid.Nil {
		return projectBootstrapWatchdog{}, false, nil
	}

	initialMessage, err := e.messages.GetByID(ctx, rt.initialMessageID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return projectBootstrapWatchdog{}, false, nil
		}
		return projectBootstrapWatchdog{}, false, err
	}
	messageSource := strings.TrimSpace(stringValue(messageMetadataMap(initialMessage.Metadata)["source"]))
	bootstrapState := projectBootstrapStateFromMetadata(rt.session.Metadata)
	if !strings.EqualFold(messageSource, projectBootstrapSource) && bootstrapState.Status != projectBootstrapStatusActive {
		return projectBootstrapWatchdog{}, false, nil
	}

	progress, err := e.loadProjectBootstrapProgress(ctx, rt.session.ScopeID)
	if err != nil {
		return projectBootstrapWatchdog{}, false, err
	}
	recoverableValidation := projectBootstrapSetupPersisted(progress) &&
		progress.ValidationFailed() &&
		projectBootstrapRecoverableMaxToolCallFailure(progress)
	if !e.projectBootstrapRuntimeManaged(ctx, rt.session, rt.initialMessageID) || progress.Materialized() || (progress.ValidationFailed() && !recoverableValidation) {
		return projectBootstrapWatchdog{}, false, nil
	}

	elapsed := e.now().UTC().Sub(rt.startedAt)
	remaining := e.projectBootstrapTurnTimeout - elapsed
	if remaining <= 0 {
		remaining = time.Millisecond
	}
	return projectBootstrapWatchdog{
		Timeout:   e.projectBootstrapTurnTimeout,
		Remaining: remaining,
	}, true, nil
}

func startProjectBootstrapWatchdogStream(parent context.Context, watchdog projectBootstrapWatchdog) projectBootstrapWatchdogStream {
	if parent == nil || watchdog.Timeout <= 0 || watchdog.Remaining <= 0 {
		return projectBootstrapWatchdogStream{ctx: parent}
	}
	ctx, cancel := context.WithCancelCause(parent)
	resetCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	timer := time.NewTimer(watchdog.Remaining)
	go func() {
		defer close(doneCh)
		defer timer.Stop()
		for {
			select {
			case <-stopCh:
				cancel(nil)
				return
			case <-parent.Done():
				cancel(context.Cause(parent))
				return
			case <-resetCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(watchdog.Timeout)
			case <-timer.C:
				cancel(errProjectBootstrapWatchdog)
				return
			}
		}
	}()
	return projectBootstrapWatchdogStream{
		ctx: ctx,
		reset: func() {
			select {
			case resetCh <- struct{}{}:
			default:
			}
		},
		stop: func() {
			close(stopCh)
			<-doneCh
		},
		cause: func() error {
			return context.Cause(ctx)
		},
		active: true,
	}
}

func (e *TurnEngine) asyncTurnStreamWatchdog(rt *turnRuntime) (time.Duration, bool) {
	if e == nil || rt == nil || rt.session == nil || e.asyncMaxDuration <= 0 {
		return 0, false
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.Mode), "async") {
		return 0, false
	}
	elapsed := e.now().UTC().Sub(rt.startedAt)
	remaining := e.asyncMaxDuration - elapsed
	if remaining <= 0 {
		remaining = time.Millisecond
	}
	return remaining, true
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

	checkpoint, _ := e.recoveryFileWriteCheckpointCandidate(ctx, rt, reason)
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

	message, err := e.appendSystemMessage(ctx, rt.turn.ID, rt.session.ID, buildRecoveryContinuationBlockedMessage(reason, checkpoint, queued))
	if err != nil {
		return true, err
	}
	if checkpoint != nil {
		failureReason := strings.TrimSpace(checkpoint.FailureReason)
		if failureReason == "" {
			failureReason = strings.TrimSpace(reason)
		}
		if checkpointErr := e.persistRecoveryFileWriteCheckpoint(ctx, rt, checkpoint.TargetPath, checkpoint.ArtifactPath, failureReason, message.ID); checkpointErr != nil {
			return true, checkpointErr
		}
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

func shouldBlockProjectKickoffFollowOnTool(rt *turnRuntime, toolName string) bool {
	if rt == nil || rt.projectIdentity == nil || rt.session == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "organization") {
		return false
	}
	if rt.agent.ID == uuid.Nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(rt.agent.DisplayName), "frank") && !strings.EqualFold(strings.TrimSpace(rt.agent.AgentType), "general") {
		return false
	}
	return strings.TrimSpace(toolName) != ""
}

func shouldBlockFreshKickoffPreCreateTool(rt *turnRuntime, toolName string) bool {
	if rt == nil || !rt.freshKickoff || rt.projectIdentity != nil || rt.session == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "organization") {
		return false
	}
	if rt.agent.ID == uuid.Nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(rt.agent.DisplayName), "frank") && !strings.EqualFold(strings.TrimSpace(rt.agent.AgentType), "general") {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(toolName), "project.create")
}

func shouldBlockFreshKickoffMemoryTool(rt *turnRuntime, toolName string) bool {
	if rt == nil || !rt.freshKickoff {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(toolName))
	if !strings.HasPrefix(name, "memory.") {
		return false
	}
	if rt.session == nil {
		return true
	}
	scopeType := strings.ToLower(strings.TrimSpace(rt.session.ScopeType))
	return scopeType == "organization" || scopeType == "project"
}

func shouldBlockFreshKickoffAgentBrowseTool(rt *turnRuntime, toolName string) bool {
	if rt == nil || !rt.freshKickoff || rt.session == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "agent.list", "agent.get":
		return true
	default:
		return false
	}
}

func shouldBlockProjectBootstrapRestaffingTool(rt *turnRuntime, toolName string) bool {
	if rt == nil || rt.session == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "staffing.browse_profiles", "staffing.get_profile", "agent.create_staff", "agent.create_temp":
	default:
		return false
	}
	state := projectBootstrapStateFromMetadata(rt.session.Metadata)
	if !projectBootstrapStateActive(state) || state.AssignmentCount == 0 {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(state.ValidationStatus), projectBootstrapValidationFailed) {
		switch strings.TrimSpace(state.ValidationFailureClass) {
		case projectBootstrapFailureMissingAssignments, projectBootstrapFailureMissingPM, projectBootstrapFailureMissingReviewer:
			return false
		}
	}
	if state.PlannedTaskCount > 0 || state.PlannedFlowTemplateCount > 0 || state.FirstWaveTaskCount > 0 || state.FirstWavePromotedCount > 0 || state.FirstWaveJobCount > 0 {
		return true
	}
	switch strings.TrimSpace(state.CurrentPhase) {
	case projectBootstrapCheckpointTaskTreePersisted,
		projectBootstrapCheckpointFlowTemplatesPersisted,
		projectBootstrapCheckpointFirstWaveSelected,
		projectBootstrapCheckpointFirstWaveExecutions,
		projectBootstrapCheckpointFirstWaveJobsClaimed:
		return true
	}
	switch strings.TrimSpace(state.LastSuccessfulCheckpoint) {
	case projectBootstrapCheckpointTaskTreePersisted,
		projectBootstrapCheckpointFlowTemplatesPersisted,
		projectBootstrapCheckpointFirstWaveSelected,
		projectBootstrapCheckpointFirstWaveExecutions,
		projectBootstrapCheckpointFirstWaveJobsClaimed:
		return true
	}
	return false
}

func shouldBlockProjectBootstrapExcessStaffingDiscoveryTool(rt *turnRuntime, toolName string) bool {
	if rt == nil || rt.session == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "staffing.browse_profiles", "staffing.get_profile":
	default:
		return false
	}
	state := projectBootstrapStateFromMetadata(rt.session.Metadata)
	if !projectBootstrapStateActive(state) || state.AssignmentCount > 0 {
		return false
	}
	if rt.toolCallsUsed < projectBootstrapStaffingDiscoveryBudget {
		return false
	}
	return true
}

func shouldBlockProjectBootstrapRecoveryRereadTool(rt *turnRuntime, toolName string, arguments map[string]any) bool {
	if rt == nil || rt.session == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") {
		return false
	}
	state := projectBootstrapStateFromMetadata(rt.session.Metadata)
	compactLateResume := projectBootstrapResumeUsesCompactRoster(state) && projectBootstrapResumeShouldStartWithPersist(state)
	if state.AutoTurnCount <= 0 && !compactLateResume {
		return false
	}
	if !projectBootstrapStateHasPersistedTaskTree(state) {
		return false
	}
	namedFailureTask := projectBootstrapFailureTaskNumber(state.ValidationFailureReason) > 0
	if !namedFailureTask {
		namedFailureTask = projectBootstrapFailureTaskNumber(rt.initialMessageText) > 0
	}
	namedFailureTaskHasDirectRepairID := namedFailureTask &&
		strings.Contains(rt.initialMessageText, "Use task.update directly on this task id instead of task.get")
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "project.list", "project.get":
		return true
	case "task.list":
		if compactLateResume {
			return true
		}
		if namedFailureTask {
			return true
		}
		return rt.toolCallsUsed > 0
	case "task.get":
		return namedFailureTaskHasDirectRepairID
	case "flow.list_templates":
		if compactLateResume {
			return true
		}
		if namedFailureTask {
			return true
		}
		return rt.toolCallsUsed > 0
	case "file.search":
		if compactLateResume {
			return true
		}
		if namedFailureTask {
			return true
		}
		return rt.toolCallsUsed > 0
	case "file.read":
		if compactLateResume && projectBootstrapRecoveryReadsPlanningPath(arguments) {
			return true
		}
		if namedFailureTask && projectBootstrapRecoveryReadsPlanningPath(arguments) {
			return true
		}
		return rt.toolCallsUsed > 0 && projectBootstrapRecoveryReadsPlanningPath(arguments)
	default:
		return false
	}
}

func shouldBlockTaskRecoveryStatusPathTool(rt *turnRuntime, toolName string, arguments map[string]any) bool {
	if rt == nil || rt.session == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project_task") || !strings.EqualFold(strings.TrimSpace(rt.session.Mode), "async") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "file.read", "file.write", "file.edit", "file.list", "file.search":
		return taskToolTouchesRecoveryStatusPath(arguments)
	default:
		return false
	}
}

func shouldBlockTaskStatusMessageTool(rt *turnRuntime, toolName string, arguments map[string]any) bool {
	if rt == nil || rt.session == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project_task") || !strings.EqualFold(strings.TrimSpace(rt.session.Mode), "async") {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(toolName), "message.send") {
		return false
	}
	targetSessionID := strings.TrimSpace(anyString(arguments["session_id"]))
	return targetSessionID == ""
}

func taskToolTouchesRecoveryStatusPath(arguments map[string]any) bool {
	for _, key := range []string{"path", "file_path", "target_path", "artifact_path", "file_pattern", "pattern"} {
		if taskToolArgumentReferencesRecoveryStatusPath(anyString(arguments[key])) {
			return true
		}
	}
	return false
}

func taskToolArgumentReferencesRecoveryStatusPath(raw string) bool {
	normalized := strings.ToLower(filepath.ToSlash(strings.TrimSpace(raw)))
	if normalized == "" {
		return false
	}
	return normalized == ".ottercamp/recovery" ||
		strings.HasPrefix(normalized, ".ottercamp/recovery/") ||
		normalized == "planning/recovery-state" ||
		strings.HasPrefix(normalized, "planning/recovery-state/") ||
		normalized == "planning/checkpoint" ||
		strings.HasPrefix(normalized, "planning/checkpoint/")
}

func projectBootstrapRecoveryReadsPlanningPath(arguments map[string]any) bool {
	path := strings.TrimSpace(stringValue(arguments["path"]))
	if path == "" {
		return false
	}
	normalized := strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	return normalized == "planning" ||
		strings.HasPrefix(normalized, "planning/") ||
		strings.Contains(normalized, "/planning/")
}

func projectBootstrapFailureNeedsDirectRepair(failureClass string) bool {
	switch strings.TrimSpace(failureClass) {
	case projectBootstrapFailureCompoundParent,
		projectBootstrapFailureSetupTaskScope,
		projectBootstrapFailureSetupTaskChildren,
		projectBootstrapFailureFirstWaveSize,
		projectBootstrapFailureFirstWaveFlow,
		projectBootstrapFailureFirstWaveExecution:
		return true
	default:
		return false
	}
}

func projectBootstrapStateHasPersistedTaskTree(state projectBootstrapState) bool {
	if state.PlannedTaskCount > 0 || state.PlannedFlowTemplateCount > 0 || state.FirstWaveTaskCount > 0 || state.FirstWavePromotedCount > 0 || state.FirstWaveJobCount > 0 {
		return true
	}
	switch strings.TrimSpace(state.CurrentPhase) {
	case projectBootstrapCheckpointTaskTreePersisted,
		projectBootstrapCheckpointFlowTemplatesPersisted,
		projectBootstrapCheckpointFirstWaveSelected,
		projectBootstrapCheckpointFirstWaveExecutions,
		projectBootstrapCheckpointFirstWaveJobsClaimed:
		return true
	}
	switch strings.TrimSpace(state.LastSuccessfulCheckpoint) {
	case projectBootstrapCheckpointTaskTreePersisted,
		projectBootstrapCheckpointFlowTemplatesPersisted,
		projectBootstrapCheckpointFirstWaveSelected,
		projectBootstrapCheckpointFirstWaveExecutions,
		projectBootstrapCheckpointFirstWaveJobsClaimed:
		return true
	}
	return false
}

func buildProjectKickoffFollowOnToolGuardError(identity *projectIdentity) string {
	if identity == nil {
		return "project kickoff is now handoff-only: provide Lori the handoff summary and end the turn"
	}
	return fmt.Sprintf("project kickoff is now handoff-only: project already created as slug=%s project_id=%s. Provide Lori the handoff summary and end the turn without additional tool use", strings.TrimSpace(identity.slug), identity.id)
}

func buildFreshKickoffPreCreateToolGuardError() string {
	return "fresh kickoff requires creating the new project first: skip project/memory/template browsing and call project.create before any other tool use"
}

func buildFreshKickoffMemoryToolGuardError() string {
	return "fresh kickoff should rely on the current request, current project description, and live tool results instead of archived memory; skip memory tools unless the user explicitly asks to reuse prior work"
}

func buildFreshKickoffAgentBrowseToolGuardError() string {
	return "fresh kickoff bootstrap should create dedicated project staff directly; skip browsing existing org agents unless the user explicitly asks to reuse prior staff"
}

func buildProjectBootstrapRestaffingToolGuardError(rt *turnRuntime) string {
	assignments := 0
	if rt != nil && rt.session != nil {
		assignments = projectBootstrapStateFromMetadata(rt.session.Metadata).AssignmentCount
	}
	if assignments <= 0 {
		return "bootstrap staffing is already persisted; reuse the existing project roster instead of browsing profiles or creating duplicate staff"
	}
	return fmt.Sprintf("bootstrap staffing is already persisted for %d active project assignments; reuse the existing project roster instead of browsing profiles or creating duplicate staff unless the validation failure explicitly says staffing is missing", assignments)
}

func buildProjectBootstrapExcessStaffingDiscoveryGuardError() string {
	return "bootstrap staffing discovery budget is exhausted for this turn. You already have enough project and profile context to act; stop browsing profiles and create/assign the concrete PM, workers, and reviewers now."
}

func buildProjectBootstrapRecoveryRereadToolGuardError(rt *turnRuntime, toolName string) string {
	state := projectBootstrapState{}
	initialMessage := ""
	if rt != nil && rt.session != nil {
		state = projectBootstrapStateFromMetadata(rt.session.Metadata)
	}
	if rt != nil {
		initialMessage = strings.ToLower(strings.TrimSpace(rt.initialMessageText))
	}
	scaffoldOnlyRecovery := (state.AssignmentCount > 0 && state.PlannedTaskCount == 0 &&
		(strings.TrimSpace(state.CurrentPhase) == projectBootstrapCheckpointTaskTreePersisted ||
			strings.TrimSpace(state.LastSuccessfulCheckpoint) == projectBootstrapCheckpointStaffingPersisted)) ||
		strings.Contains(initialMessage, "did not yet materialize any executable non-bootstrap project tasks") ||
		strings.Contains(initialMessage, "did not emit any executable non-bootstrap project tasks for the first wave") ||
		projectBootstrapRestartScaffoldFailureReason(state.ValidationFailureReason)
	if scaffoldOnlyRecovery {
		switch strings.ToLower(strings.TrimSpace(toolName)) {
		case "project.list", "project.get", "task.list", "flow.list_templates", "file.read", "file.search":
			return "bootstrap scaffold-only recovery already has the active project scope and persisted staffing roster. Do not reread project state first. Your next action must be direct task.create or subtask.create calls that materialize bounded non-bootstrap project work using the existing assignee roster from the bootstrap resume message."
		}
	}
	if projectBootstrapResumeUsesCompactRoster(state) && projectBootstrapResumeShouldStartWithPersist(state) {
		switch strings.ToLower(strings.TrimSpace(toolName)) {
		case "task.list", "flow.list_templates", "project.list", "project.get":
			return "late bootstrap resume already has persisted staffing, tasks, flows, and first-wave state. Do not reread broad project state first; call bootstrap.setup.persist now, or inspect only a single specifically blocked task or template if that tool names it."
		case "file.read", "file.search":
			return "late bootstrap resume should not reread scaffold planning artifacts before acting on the persisted first-wave state. Start with bootstrap.setup.persist, and only inspect a single specifically blocked task or template if that tool names it."
		}
	}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "task.list":
		return "bootstrap validation recovery already named the blocker and you already listed the persisted task tree on this turn; do not re-list it again. Repair the named task directly, or inspect only a single specific task if its details are still unclear."
	case "task.get":
		return "bootstrap validation recovery already includes the exact blocked task id and direct-repair instructions in the continuation message. Do not reread that task first; repair it directly with task.update and bounded child task creation."
	case "file.read", "file.search":
		return "bootstrap validation recovery should not fall back to scaffold planning file rereads once the blocker is named. Repair the persisted task tree directly, and inspect only the specific task you are fixing."
	default:
		return "bootstrap validation recovery already has the active project scope and persisted bootstrap state; skip broad project or template rereads and repair the named blocker directly."
	}
}

func buildTaskRecoveryStatusPathToolGuardError(toolName string) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "file.write", "file.edit":
		return "task execution should not write recovery-state or checkpoint files. Write the real deliverable artifact for the current task instead of mutating supervisor status files."
	default:
		return "task execution should not reread recovery-state or checkpoint files. Continue the current deliverable directly using the task artifacts already in this session."
	}
}

func buildTaskStatusMessageToolGuardError() string {
	return "task execution should not spend turns on status or notification messages without an explicit destination session. Continue the task deliverable directly in this turn, or provide a concrete target session_id for a real cross-session handoff."
}

func shouldStopAfterBlockedProjectBootstrapRecoveryReread(rt *turnRuntime, blocked bool) bool {
	if !blocked || rt == nil || rt.session == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project") {
		return false
	}
	state := projectBootstrapStateFromMetadata(rt.session.Metadata)
	initialMessage := strings.ToLower(strings.TrimSpace(rt.initialMessageText))
	scaffoldOnlyRecovery := (state.AssignmentCount > 0 && state.PlannedTaskCount == 0 &&
		(strings.TrimSpace(state.CurrentPhase) == projectBootstrapCheckpointTaskTreePersisted ||
			strings.TrimSpace(state.LastSuccessfulCheckpoint) == projectBootstrapCheckpointStaffingPersisted)) ||
		strings.Contains(initialMessage, "did not yet materialize any executable non-bootstrap project tasks") ||
		strings.Contains(initialMessage, "did not emit any executable non-bootstrap project tasks for the first wave") ||
		projectBootstrapRestartScaffoldFailureReason(state.ValidationFailureReason)
	if scaffoldOnlyRecovery {
		return false
	}
	return true
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
		streamCtx := ctx
		asyncWatchdogTimeout := time.Duration(0)
		watchdogStream := projectBootstrapWatchdogStream{ctx: ctx}
		watchdog, watchdogActive, err := e.projectBootstrapStreamWatchdog(ctx, rt)
		if err != nil {
			return ModelResponse{}, err
		}
		cancelWatchdog := func() {}
		if watchdogActive {
			watchdogStream = startProjectBootstrapWatchdogStream(ctx, watchdog)
			streamCtx = watchdogStream.ctx
			cancelWatchdog = watchdogStream.stop
		} else if remaining, ok := e.asyncTurnStreamWatchdog(rt); ok {
			asyncWatchdogTimeout = e.asyncMaxDuration
			streamCtx, cancelWatchdog = context.WithTimeoutCause(ctx, remaining, errAsyncTurnWatchdog)
		}

		type streamCallResult struct {
			response ModelResponse
			err      error
		}
		streamResultCh := make(chan streamCallResult, 1)
		go func() {
			response, callErr := e.models.StreamComplete(streamCtx, ModelRequest{
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
					if err := e.chat.UpdateMessageStatus(streamCtx, assistant.ID, "streaming", ""); err != nil {
						return err
					}
					streamingMarked = true
				}
				if watchdogActive && watchdogStream.reset != nil {
					watchdogStream.reset()
				}
				tokensSeen++
				builder.WriteString(token)
				if _, err := e.messages.UpdateContent(streamCtx, assistant.ID, builder.String()); err != nil {
					if e.immutableMessageWriteForTerminalTurn(streamCtx, rt, err) {
						return errTurnCancelled
					}
					return err
				}
				*chunkSeq++
				if err := e.publishEvent(streamCtx, rt.session.OrganizationID, "chat.message.chunk", "agent", &rt.agent.ID, map[string]any{
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
					_, _ = e.findSteerMessages(streamCtx, rt.session.ID, rt.startedAt)
				}
				return nil
			})
			select {
			case streamResultCh <- streamCallResult{response: response, err: callErr}:
			default:
			}
		}()

		var (
			response ModelResponse
			callErr  error
		)
		select {
		case result := <-streamResultCh:
			response = result.response
			callErr = result.err
		case <-streamCtx.Done():
			callErr = streamCtx.Err()
		}
		cancelWatchdog()

		if callErr != nil {
			if errors.Is(callErr, errTurnCancelled) {
				return ModelResponse{}, errTurnCancelled
			}
			if watchdogActive && errors.Is(context.Cause(streamCtx), errProjectBootstrapWatchdog) {
				timeoutErr := &projectBootstrapTimeoutError{
					InvocationID: invocation.ID,
					Timeout:      watchdog.Timeout,
				}
				if progress, progressErr := e.loadProjectBootstrapProgress(ctx, rt.session.ScopeID); progressErr == nil {
					timeoutErr.Progress = progress
				}
				errorCode := stringPtr("bootstrap_watchdog_timeout")
				errorText := stringPtr(timeoutErr.Error())
				_, _ = e.invocations.UpdateStatus(ctx, invocation.ID, "failed", errorCode, errorText)
				_ = e.chat.UpdateMessageStatus(ctx, assistant.ID, "failed", timeoutErr.Error())
				return ModelResponse{}, timeoutErr
			}
			if asyncWatchdogTimeout > 0 && errors.Is(callErr, context.DeadlineExceeded) && errors.Is(context.Cause(streamCtx), errAsyncTurnWatchdog) {
				timeoutErr := &asyncTurnTimeoutError{
					InvocationID: invocation.ID,
					Timeout:      asyncWatchdogTimeout,
				}
				rt.stopReason = stopReasonMaxDuration
				errorCode := stringPtr("turn_timeout")
				errorText := stringPtr(timeoutErr.Error())
				_, _ = e.invocations.UpdateStatus(ctx, invocation.ID, "failed", errorCode, errorText)
				_ = e.chat.UpdateMessageStatus(ctx, assistant.ID, "failed", timeoutErr.Error())
				return ModelResponse{}, timeoutErr
			}
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
			if e.immutableMessageWriteForTerminalTurn(ctx, rt, err) {
				return ModelResponse{}, errTurnCancelled
			}
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
		if err := e.invocations.UpdateCompletion(ctx, invocation.ID,
			usage.InputTokens,
			usage.OutputTokens,
			usage.CacheReadTokens,
			maxInt(0, int(e.now().Sub(started).Milliseconds())),
			maxInt(0, int(e.now().Sub(started).Milliseconds())),
			nil,
			nil,
		); err != nil {
			_ = e.chat.UpdateMessageStatus(ctx, assistant.ID, "failed", err.Error())
			return ModelResponse{}, fmt.Errorf("mark invocation complete: %w", err)
		}
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
	cleanupCtx := context.Background()
	if runID := rt.getActiveTier2Run(); runID != nil && e.runCanceler != nil {
		_ = e.runCanceler.RequestCancel(cleanupCtx, *runID, controlplane.CancelRequestActor{Type: "system"})
	}

	messages, _ := e.messages.ListBySession(cleanupCtx, rt.session.ID)
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.TurnID == nil || *m.TurnID != rt.turn.ID {
			continue
		}
		if strings.EqualFold(m.Role, "assistant") && strings.EqualFold(m.Status, "streaming") {
			_, _ = e.messages.UpdateStatus(cleanupCtx, m.ID, "failed", "cancelled")
			break
		}
	}
	systemMessage := "[Turn cancelled by user.]"
	cancelReason := "user_cancelled"
	sessionClosed := errors.Is(context.Cause(ctx), errTurnSessionClosed)
	if !sessionClosed {
		if closed, err := e.sessionClosedOrArchived(cleanupCtx, rt.session.ID); err == nil && closed {
			sessionClosed = true
		}
	}
	if sessionClosed {
		systemMessage = "[Turn cancelled because the session or project was closed.]"
		cancelReason = "session_closed"
	}
	_, _ = e.appendSystemMessage(cleanupCtx, rt.turn.ID, rt.session.ID, systemMessage)
	_ = e.chat.CancelTurn(cleanupCtx, rt.turn.ID, cancelReason)
	return errTurnCancelled
}

func (e *TurnEngine) sessionClosedOrArchived(ctx context.Context, sessionID uuid.UUID) (bool, error) {
	if e == nil || e.chat == nil || sessionID == uuid.Nil {
		return false, nil
	}
	session, err := e.chat.GetSession(ctx, sessionID)
	if errors.Is(err, repo.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	status := strings.TrimSpace(strings.ToLower(session.Status))
	return status == "closed" || status == "archived", nil
}

func (e *TurnEngine) watchTurnCancellation(ctx context.Context, rt *turnRuntime) (context.Context, func()) {
	cancelCtx, cancel := context.WithCancelCause(ctx)
	if rt != nil && rt.session != nil &&
		strings.EqualFold(strings.TrimSpace(rt.session.Mode), "async") &&
		strings.EqualFold(strings.TrimSpace(rt.session.ScopeType), "project_task") {
		return cancelCtx, func() { cancel(nil) }
	}
	consumer := e.cancelConsumerName
	projectID := resolveProjectID(ctx, rt.session, e.tasks)
	var (
		mu               sync.Mutex
		cancelledByEvent bool
	)
	sub := e.events.Subscribe(consumer, &rt.session.OrganizationID, func(_ context.Context, event eventbus.DomainEvent) error {
		payload := map[string]any{}
		if len(event.Payload) > 0 {
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return nil
			}
		}

		switch event.EventType {
		case "chat.turn.cancelled":
			turnID, ok := payloadUUID(payload, "turn_id")
			if !ok || turnID != rt.turn.ID {
				return nil
			}
		case "chat.session.closed":
			sessionID, ok := payloadUUID(payload, "session_id")
			if !ok || sessionID != rt.session.ID {
				return nil
			}
		case "project.archived":
			eventProjectID, ok := payloadUUID(payload, "project_id")
			if !ok || projectID == nil || *projectID == uuid.Nil || eventProjectID != *projectID {
				return nil
			}
		default:
			return nil
		}

		if runID := rt.getActiveTier2Run(); runID != nil && e.runCanceler != nil {
			_ = e.runCanceler.RequestCancel(context.Background(), *runID, controlplane.CancelRequestActor{Type: "system"})
		}
		mu.Lock()
		cancelledByEvent = true
		mu.Unlock()
		if event.EventType == "chat.turn.cancelled" {
			cancel(context.Canceled)
			return nil
		}
		cancel(errTurnSessionClosed)
		return nil
	})

	cleanup := func() {
		cancel(context.Canceled)
		mu.Lock()
		shouldDelay := cancelledByEvent
		mu.Unlock()
		if shouldDelay {
			time.AfterFunc(50*time.Millisecond, func() {
				e.events.Unsubscribe(sub)
			})
			return
		}
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
		agentID, err := e.resolveTaskScopeAgentForSession(ctx, session)
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

		// If the project already has the canonical bootstrap scaffold, treat the
		// session as active bootstrap work and route straight to Lori instead of
		// requiring a fresh Frank handoff turn.
		if loriID, loriErr := e.resolveLoriStarterID(ctx, session.OrganizationID); loriErr == nil && loriID != uuid.Nil {
			if e.shouldRouteScaffoldedProjectSessionToLori(ctx, session) {
				if err := e.ensureAgentParticipant(ctx, session.ID, loriID); err != nil {
					return uuid.Nil, err
				}
				return loriID, nil
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

func (e *TurnEngine) resolveTaskScopeAgentForSession(ctx context.Context, session *chat.ChatSession) (uuid.UUID, error) {
	if session == nil {
		return uuid.Nil, repo.ErrNotFound
	}
	if strings.EqualFold(strings.TrimSpace(session.Mode), "async") {
		return e.resolveTaskScopeAssignedAgent(ctx, session)
	}
	return e.resolveTaskScopeDiscussionAgent(ctx, session.OrganizationID, session.ScopeID)
}

func (e *TurnEngine) resolveTaskScopeAgent(ctx context.Context, organizationID, taskID uuid.UUID) (uuid.UUID, error) {
	return e.resolveTaskScopeAssignedAgent(ctx, &chat.ChatSession{
		OrganizationID: organizationID,
		ScopeID:        taskID,
	})
}

func (e *TurnEngine) resolveTaskScopeAssignedAgent(ctx context.Context, session *chat.ChatSession) (uuid.UUID, error) {
	if e.tasks == nil {
		return uuid.Nil, fmt.Errorf("internal invariant: task-scoped session is missing task repository")
	}
	if session == nil {
		return uuid.Nil, repo.ErrNotFound
	}
	organizationID := session.OrganizationID
	taskID := session.ScopeID
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
	if reviewAgentID, ok, reviewErr := e.resolveTaskScopeReviewAgent(ctx, taskRecord); reviewErr != nil {
		return uuid.Nil, reviewErr
	} else if ok {
		return reviewAgentID, nil
	}
	if taskRecord.AssignedAgentID == nil || *taskRecord.AssignedAgentID == uuid.Nil {
		recoveredAgentID, recovered, recoverErr := e.recoverMissingTaskAssigneeFromSession(ctx, session, taskRecord)
		if recoverErr != nil {
			return uuid.Nil, recoverErr
		}
		if recovered {
			return recoveredAgentID, nil
		}
		return uuid.Nil, fmt.Errorf("internal invariant: task-scoped session is missing assigned agent")
	}
	return *taskRecord.AssignedAgentID, nil
}

func (e *TurnEngine) resolveTaskScopeReviewAgent(ctx context.Context, taskRecord repo.ProjectTask) (uuid.UUID, bool, error) {
	if e == nil || e.assignments == nil || !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "review") || taskRecord.ProjectID == uuid.Nil {
		return uuid.Nil, false, nil
	}

	assignedID := uuid.Nil
	if taskRecord.AssignedAgentID != nil {
		assignedID = *taskRecord.AssignedAgentID
	}

	assignments, err := e.assignments.ListByProject(ctx, taskRecord.ProjectID)
	if err != nil {
		return uuid.Nil, false, err
	}

	preferred := uuid.Nil
	reviewer := uuid.Nil
	fallback := uuid.Nil
	for _, assignment := range assignments {
		if !assignment.IsActive || assignment.AgentID == uuid.Nil || assignment.AgentID == assignedID {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(assignment.Role), "reviewer") {
			reviewer = assignment.AgentID
			break
		}
		if strings.EqualFold(strings.TrimSpace(assignment.Role), "project_manager") {
			preferred = assignment.AgentID
			continue
		}
		if fallback == uuid.Nil {
			fallback = assignment.AgentID
		}
	}
	if reviewer != uuid.Nil {
		return reviewer, true, nil
	}
	if preferred != uuid.Nil {
		return preferred, true, nil
	}
	if fallback != uuid.Nil {
		return fallback, true, nil
	}
	return uuid.Nil, false, nil
}

func (e *TurnEngine) recoverMissingTaskAssigneeFromSession(ctx context.Context, session *chat.ChatSession, taskRecord repo.ProjectTask) (uuid.UUID, bool, error) {
	if e == nil || session == nil || e.chat == nil || e.assignments == nil || e.tasks == nil {
		return uuid.Nil, false, nil
	}
	if session.ID == uuid.Nil || taskRecord.ProjectID == uuid.Nil {
		return uuid.Nil, false, nil
	}

	participants, err := e.chat.ListParticipants(ctx, session.ID)
	if err != nil {
		return uuid.Nil, false, err
	}
	candidateIDs := make([]uuid.UUID, 0, 1)
	seen := make(map[uuid.UUID]struct{})
	for _, participant := range participants {
		if participant == nil || participant.ParticipantID == uuid.Nil || participant.RemovedAt != nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(participant.ParticipantType), "agent") {
			continue
		}
		if _, ok := seen[participant.ParticipantID]; ok {
			continue
		}
		seen[participant.ParticipantID] = struct{}{}
		candidateIDs = append(candidateIDs, participant.ParticipantID)
	}
	if len(candidateIDs) != 1 {
		return uuid.Nil, false, nil
	}

	assignments, err := e.assignments.ListByProject(ctx, taskRecord.ProjectID)
	if err != nil {
		return uuid.Nil, false, err
	}
	candidateID := candidateIDs[0]
	active := false
	for _, assignment := range assignments {
		if assignment.AgentID == candidateID && assignment.IsActive {
			active = true
			break
		}
	}
	if !active {
		return uuid.Nil, false, nil
	}

	taskRecord.AssignedAgentID = &candidateID
	if _, err := e.tasks.Update(ctx, taskRecord); err != nil {
		return uuid.Nil, false, err
	}
	if e.logger != nil {
		e.logger.Info("recovered missing task assignee from active session participant",
			"session_id", session.ID,
			"task_id", taskRecord.ID,
			"agent_id", candidateID,
		)
	}
	return candidateID, true, nil
}

func (e *TurnEngine) resolveTaskScopeDiscussionAgent(ctx context.Context, organizationID, taskID uuid.UUID) (uuid.UUID, error) {
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
	if e.assignments != nil && taskRecord.ProjectID != uuid.Nil {
		pm, pmErr := e.assignments.GetPM(ctx, taskRecord.ProjectID)
		if pmErr == nil && pm.IsActive && pm.AgentID != uuid.Nil {
			return pm.AgentID, nil
		}
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

func (e *TurnEngine) shouldRouteScaffoldedProjectSessionToLori(ctx context.Context, session *chat.ChatSession) bool {
	if e == nil || e.tasks == nil || session == nil || session.ScopeID == uuid.Nil {
		return false
	}
	tasks, err := e.tasks.ListByProject(ctx, session.ScopeID)
	if err != nil || len(tasks) == 0 {
		return false
	}

	hasBootstrapGate := false
	setupCount := 0
	for _, task := range tasks {
		metadata := messageMetadataMap(task.Metadata)
		if bootstrapGate, _ := metadata["bootstrap_gate"].(bool); bootstrapGate {
			hasBootstrapGate = true
		}
		if bootstrapSetupTask, _ := metadata["bootstrap_setup_task"].(bool); bootstrapSetupTask {
			setupCount++
		}
	}
	return hasBootstrapGate && setupCount >= 3
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

func (e *TurnEngine) countActiveAgentTurnJobsForSession(ctx context.Context, sessionID uuid.UUID) (int, error) {
	if e == nil || e.pool == nil || sessionID == uuid.Nil {
		return 0, nil
	}
	var count int
	err := e.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = $1
		  AND status IN ('pending', 'claimed')
		  AND payload->>'session_id' = $2
	`, AgentTurnJobType, sessionID.String()).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
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

func latestMessageSequenceForTurn(messages []repo.ChatMessage, turnID uuid.UUID) int64 {
	var latest int64
	for i := range messages {
		message := messages[i]
		if message.TurnID == nil || *message.TurnID != turnID {
			continue
		}
		if message.SequenceNumber > latest {
			latest = message.SequenceNumber
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

func shouldSuppressAutoContinuationForRecoveryHalt(messages []repo.ChatMessage, turnID uuid.UUID, metadata json.RawMessage) bool {
	if turnID == uuid.Nil {
		return false
	}
	if checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(metadata); ok {
		if strings.EqualFold(strings.TrimSpace(checkpoint.HaltTurnID), turnID.String()) {
			return true
		}
	}
	for _, message := range messages {
		if message.TurnID == nil || *message.TurnID != turnID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(message.Role), "system") {
			continue
		}
		content := strings.ToLower(strings.TrimSpace(message.Content))
		if strings.HasPrefix(content, "[recovery turn halted:") {
			return true
		}
	}
	return false
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

	if e.taskTransitions == nil {
		return fmt.Errorf(errMissingTaskTransitionServiceForRecoveryBlock)
	}
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

func looksLikeGenericTaskRecoveryReply(content string) bool {
	normalized := strings.ToLower(normalizeInstructionText(content))
	if normalized == "" {
		return false
	}
	patterns := []string{
		"i'll help",
		"i'm ready to help",
		"i am ready to help",
		"i'm ready to help you move forward",
		"i am ready to help you move forward",
		"i'm ready to work on",
		"i am ready to work on",
		"i'm ready to assist",
		"i am ready to assist",
		"i'm ready. i'm",
		"i am ready. i am",
		"what do you need",
		"what would you like me to focus on first",
		"what would you like me to help with",
		"what would you like me to help",
		"what would you like me to do",
		"what should i focus on first",
		"how can i help",
		"current status",
		"based on the context, i can see",
		"based on the context i can see",
		"i have access to the planning artifacts",
		"or is there a specific decision or constraint",
		"i'm currently working on",
		"my task is to create",
		"before i proceed with drafting",
		"let me first examine the current state of the project and task",
		"let me examine the current state of the project and task",
		"let me read the strategy artifacts that are already locked",
		"let me read those to understand the locked decisions",
		"let me check the task flow and understand what step we're on",
		"the decisions are locked and clear",
		"i need to confirm",
	}
	for _, pattern := range patterns {
		if strings.Contains(normalized, pattern) {
			return true
		}
	}
	return false
}

func buildGenericRecoveryReplyBlockedReason(reply string, attempts int) string {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return fmt.Sprintf("recovery halted after %d retries without a usable assistant response; re-queue only when the next attempt can continue the task directly", attempts)
	}
	return fmt.Sprintf("recovery halted after %d generic non-action replies; latest reply=%q", attempts, trimmed)
}

func completedWorkSignalFromMessages(taskRecord repo.ProjectTask, messages []repo.ChatMessage, turnID uuid.UUID) (workCompletionSignal, bool) {
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
			if !explicitExecutionDeliverableWriteCompleted(taskRecord, ToolResult{Name: toolName, Output: output}) {
				continue
			}
			latest = workCompletionSignal{filesCommitted: 1}
			found = true
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

func explicitDeliverablePath(taskRecord repo.ProjectTask) string {
	if taskRecord.Description == nil {
		return ""
	}
	matches := explicitDeliverablePathPattern.FindStringSubmatch(strings.TrimSpace(*taskRecord.Description))
	if len(matches) < 2 {
		return ""
	}
	return normalizeWorkspaceRelativePath(matches[1])
}

func explicitExecutionDeliverableWriteCompleted(taskRecord repo.ProjectTask, result ToolResult) bool {
	if !strings.EqualFold(strings.TrimSpace(result.Name), "file.write") || strings.TrimSpace(result.Error) != "" {
		return false
	}
	plan, hasPlan := taskplan.Parse(taskRecord.Metadata)
	if !hasPlan || !strings.EqualFold(strings.TrimSpace(plan.Mode), taskplan.ModeExecutionFirst) {
		return false
	}
	deliverablePath := explicitDeliverablePath(taskRecord)
	if deliverablePath == "" {
		return false
	}
	writtenPath := normalizeWorkspaceRelativePath(anyString(result.Output["path"]))
	if writtenPath == "" || !sameWorkspaceRelativePath(writtenPath, deliverablePath) {
		return false
	}
	return anyInt(result.Output["byte_size"]) > 0
}

func shouldStopAfterExecutionArtifactWrite(taskRecord repo.ProjectTask, result ToolResult) bool {
	if explicitExecutionDeliverableWriteCompleted(taskRecord, result) {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(result.Name), "file.write") || strings.TrimSpace(result.Error) != "" {
		return false
	}
	plan, hasPlan := taskplan.Parse(taskRecord.Metadata)
	if !hasPlan || !strings.EqualFold(strings.TrimSpace(plan.Mode), taskplan.ModeExecutionFirst) {
		return false
	}
	if explicitDeliverablePath(taskRecord) != "" {
		return false
	}
	writtenPath := normalizeWorkspaceRelativePath(anyString(result.Output["path"]))
	if writtenPath == "" || anyInt(result.Output["byte_size"]) <= 0 {
		return false
	}
	for _, artifact := range plan.Artifacts {
		artifactPath := normalizeWorkspaceRelativePath(artifact.RepoPath)
		if artifactPath == "" {
			continue
		}
		if sameWorkspaceRelativePath(writtenPath, artifactPath) {
			return true
		}
	}
	return false
}

func normalizeWorkspaceRelativePath(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(trimmed)))
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

func anyStrings(raw any) []string {
	switch typed := raw.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(item)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(anyString(item))
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
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

func jitteredRateLimitRetryDelay(delay time.Duration, sessionID, messageID uuid.UUID, retryCount int) time.Duration {
	if delay < rateLimitRetryJitterThreshold || maxRateLimitRetryJitter <= 0 {
		return delay
	}
	hasher := fnv.New64a()
	_, _ = hasher.Write(sessionID[:])
	_, _ = hasher.Write(messageID[:])
	_, _ = hasher.Write([]byte(strconv.Itoa(retryCount)))
	jitterRange := uint64(maxRateLimitRetryJitter / time.Second)
	if jitterRange == 0 {
		return delay
	}
	jitterSeconds := hasher.Sum64() % (jitterRange + 1)
	return delay + time.Duration(jitterSeconds)*time.Second
}

func transientInfrastructureRetryDelay(retryCount int) time.Duration {
	if retryCount < 0 {
		retryCount = 0
	}

	delay := defaultTransientInfraBackoff
	for i := 0; i < retryCount; i++ {
		if delay >= (maxTransientInfraBackoff / 2) {
			return maxTransientInfraBackoff
		}
		delay *= 2
	}
	if delay > maxTransientInfraBackoff {
		return maxTransientInfraBackoff
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
	var latest *repo.ChatTurn
	for _, turn := range turns {
		if turn.TriggerMessageID == nil || *turn.TriggerMessageID != messageID {
			continue
		}
		turnCopy := turn
		if latest == nil ||
			turnCopy.TurnNumber > latest.TurnNumber ||
			(turnCopy.TurnNumber == latest.TurnNumber && turnCopy.RetryCount > latest.RetryCount) {
			latest = &turnCopy
		}
	}
	if latest == nil {
		return false, nil
	}
	return strings.EqualFold(strings.TrimSpace(latest.Status), "cancelled"), nil
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
	if payload.FlowNodeExecutionID == nil && session != nil {
		payload.FlowNodeExecutionID = cloneUUIDPointer(flowNodeExecutionIDFromSessionMetadata(session))
	}
	if suppressed, err := e.suppressRecoveryRetryForCompletedTurn(ctx, session, payload.MessageID); err != nil {
		return false, err
	} else if suppressed {
		e.logger.Info("skipping enqueue after completed recovery halt for message",
			"session_id", payload.SessionID,
			"message_id", payload.MessageID,
		)
		return false, nil
	}
	if _, err := e.enqueuer.Enqueue(ctx, nil, AgentTurnJobType, e.jobPriority, payload, runAfter); err != nil {
		return false, err
	}
	return true, nil
}

func (e *TurnEngine) suppressRecoveryRetryForCompletedTurn(ctx context.Context, session *chat.ChatSession, messageID uuid.UUID) (bool, error) {
	if e == nil || e.turns == nil || session == nil || messageID == uuid.Nil {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project_task") {
		return false, nil
	}
	turns, err := e.turns.ListBySession(ctx, session.ID)
	if err != nil {
		return false, err
	}
	for i := range turns {
		turn := turns[i]
		if turn.TriggerMessageID == nil || *turn.TriggerMessageID != messageID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(turn.Status), "completed") {
			continue
		}
		if shouldSuppressAutoContinuationForStopReason(turn.StopReason) {
			return true, nil
		}
	}
	return false, nil
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

func flowNodeExecutionIDFromSessionMetadata(session *chat.ChatSession) *uuid.UUID {
	if session == nil || len(session.Metadata) == 0 || !json.Valid(session.Metadata) {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(session.Metadata, &payload); err != nil {
		return nil
	}
	executionID, ok := payloadUUID(payload, "flow_node_execution_id")
	if !ok || executionID == uuid.Nil {
		return nil
	}
	return &executionID
}

func (e *TurnEngine) syncBoundFlowExecutionTurnOwnership(ctx context.Context, session *chat.ChatSession, turnID *uuid.UUID) error {
	if e == nil || e.pool == nil || session == nil {
		return nil
	}
	executionID := flowNodeExecutionIDFromSessionMetadata(session)
	if executionID == nil || *executionID == uuid.Nil {
		return nil
	}
	execution, err := repo.NewFlowNodeExecutionRepo(e.pool).GetByID(ctx, *executionID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	updated := repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{
		RunID:  repo.FlowExecutionLiveOwnerFromMetadata(execution.Metadata).RunID,
		TurnID: turnID,
	})
	_, err = repo.NewFlowNodeExecutionRepo(e.pool).UpdateMetadata(ctx, execution.ID, updated)
	return err
}

func (e *TurnEngine) reconcileBoundFlowExecutionTurnOwnership(ctx context.Context, session *chat.ChatSession, turnID uuid.UUID) {
	if e == nil || e.pool == nil || e.chat == nil || session == nil || turnID == uuid.Nil {
		return
	}
	currentTurn, err := e.chat.GetTurn(ctx, turnID)
	if err != nil || currentTurn == nil {
		return
	}
	status := strings.ToLower(strings.TrimSpace(currentTurn.Status))
	if status != "completed" && status != "cancelled" && status != "failed" {
		return
	}
	executionID := flowNodeExecutionIDFromSessionMetadata(session)
	if executionID == nil || *executionID == uuid.Nil {
		return
	}
	executionRepo := repo.NewFlowNodeExecutionRepo(e.pool)
	execution, err := executionRepo.GetByID(ctx, *executionID)
	if err != nil {
		return
	}
	liveOwner := repo.FlowExecutionLiveOwnerFromMetadata(execution.Metadata)
	if liveOwner.TurnID == nil || *liveOwner.TurnID != turnID {
		return
	}
	_, _ = executionRepo.UpdateMetadata(ctx, execution.ID, repo.FlowExecutionMetadataWithLiveOwner(execution.Metadata, repo.FlowExecutionLiveOwner{
		RunID:  liveOwner.RunID,
		TurnID: nil,
	}))
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

func isTransientInfrastructureError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "sqlstate 53300") ||
		strings.Contains(text, "remaining connection slots are reserved") ||
		strings.Contains(text, "too many clients")
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
