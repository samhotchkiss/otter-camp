package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/bootstrap"
	"github.com/samhotchkiss/otter-camp/internal/browser"
	"github.com/samhotchkiss/otter-camp/internal/budget"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/cli"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/db"
	"github.com/samhotchkiss/otter-camp/internal/delivery"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/gateway"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/jobs"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/memory/compaction"
	"github.com/samhotchkiss/otter-camp/internal/memory/importer"
	"github.com/samhotchkiss/otter-camp/internal/model"
	"github.com/samhotchkiss/otter-camp/internal/observability"
	"github.com/samhotchkiss/otter-camp/internal/policy"
	"github.com/samhotchkiss/otter-camp/internal/profiles"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/projectpause"
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/push"
	"github.com/samhotchkiss/otter-camp/internal/push/adapters"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/scheduling"
	"github.com/samhotchkiss/otter-camp/internal/secret"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/taskorchestration"
	"github.com/samhotchkiss/otter-camp/internal/tools"
	nativetools "github.com/samhotchkiss/otter-camp/internal/tools/native"
	"github.com/samhotchkiss/otter-camp/internal/turn"
)

const deterministicQueryEmbeddingDimensions = 1536
const defaultWorkerMaxConns int32 = 32

func workerConcurrency() (int, error) {
	if raw := strings.TrimSpace(os.Getenv("OTTERCAMP_WORKER_CONCURRENCY")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return 0, fmt.Errorf("invalid OTTERCAMP_WORKER_CONCURRENCY %q", raw)
		}
		return v, nil
	}
	return 0, nil
}

func workerDBMaxConns() (int32, error) {
	if raw := strings.TrimSpace(os.Getenv("OTTERCAMP_WORKER_DB_MAX_CONNS")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return 0, fmt.Errorf("invalid OTTERCAMP_WORKER_DB_MAX_CONNS %q", raw)
		}
		return int32(v), nil
	}
	if raw := strings.TrimSpace(os.Getenv("OTTERCAMP_DB_MAX_CONNS")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return 0, fmt.Errorf("invalid OTTERCAMP_DB_MAX_CONNS %q", raw)
		}
		return int32(v), nil
	}
	if db.DefaultMaxConns > defaultWorkerMaxConns {
		return db.DefaultMaxConns, nil
	}
	return defaultWorkerMaxConns, nil
}

func Run(ctx context.Context, logger *slog.Logger, signalCh <-chan os.Signal) error {
	if logger == nil {
		logger = slog.Default()
	}

	databaseURL := strings.TrimSpace(os.Getenv("OTTERCAMP_DATABASE_URL"))
	if databaseURL == "" {
		return fmt.Errorf("worker database setup: OTTERCAMP_DATABASE_URL is required")
	}
	maxConns, err := workerDBMaxConns()
	if err != nil {
		return fmt.Errorf("worker database setup: %w", err)
	}
	pool, err := db.New(ctx, databaseURL, maxConns)
	if err != nil {
		return fmt.Errorf("worker database setup: %w", err)
	}
	defer pool.Close()

	bootstrapper := bootstrap.NewBootstrapper(bootstrap.Options{
		Pool:   pool.Raw(),
		Logger: logger,
	})
	bootstrap.RegisterStarterTrioStep(bootstrapper, repo.NewAgentRepo(pool.Raw()))
	bootstrap.RegisterCapabilityPolicyStep(bootstrapper, repo.NewCapabilityPolicyRepo(pool.Raw()))
	if err := bootstrapper.Run(ctx); err != nil {
		return fmt.Errorf("worker bootstrap reconcile: %w", err)
	}

	bus := eventbus.New(pool.Raw(), logger, eventbus.Config{})
	tasks, err := tasksvc.NewService(tasksvc.Options{
		Pool:     pool.Raw(),
		EventBus: bus,
	})
	if err != nil {
		return fmt.Errorf("worker task service setup: %w", err)
	}
	if parentRepaired, draftSettled, gatesCancelled, pausesCleared, err := startupProjectCleanup(ctx, pool.Raw(), tasks); err != nil {
		return fmt.Errorf("worker startup project cleanup: %w", err)
	} else {
		if parentRepaired > 0 {
			logger.Info("worker startup: auto-completed dormant orchestration parents", "count", parentRepaired)
		}
		if draftSettled > 0 {
			logger.Info("worker startup: auto-completed satisfied draft tasks", "count", draftSettled)
		}
		if gatesCancelled > 0 {
			logger.Info("worker startup: cancelled impossible draft project gates", "count", gatesCancelled)
		}
		if pausesCleared > 0 {
			logger.Info("worker startup: cleared stale bootstrap pauses", "count", pausesCleared)
		}
	}

	scheduleEngine, err := scheduling.NewScheduleEngine(scheduling.ScheduleEngineOptions{
		Pool:   pool.Raw(),
		Tasks:  tasks,
		Events: bus,
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("worker schedule engine setup: %w", err)
	}

	workerBatchSize, err := workerConcurrency()
	if err != nil {
		return fmt.Errorf("worker queue setup: %w", err)
	}
	jqWorker := jobqueue.New(pool.Raw(), logger, jobqueue.Config{
		BatchSize: workerBatchSize,
	})
	tickWorker := scheduling.NewScheduleTickWorker(pool.Raw(), scheduleEngine, logger)
	jqWorker.Register(scheduling.ScheduleTickJobType, tickWorker.Execute)

	retentionJob, err := jobs.NewRetentionJob(jobs.RetentionJobOptions{
		Pool:   pool.Raw(),
		Events: bus,
		Store:  nil,
	})
	if err != nil {
		return fmt.Errorf("worker retention job setup: %w", err)
	}
	retentionJob.RegisterJobs(jqWorker)

	tracePartitionJob, err := jobs.NewTraceSpanPartitionJob(jobs.TraceSpanPartitionJobOptions{
		Pool: pool.Raw(),
	})
	if err != nil {
		return fmt.Errorf("worker trace partition job setup: %w", err)
	}
	tracePartitionJob.RegisterJobs(jqWorker)
	mergeWorker, err := delivery.NewMergeWorker(delivery.MergeWorkerOptions{
		Pool:   pool.Raw(),
		Git:    delivery.UnavailableGitService{},
		Events: bus,
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("worker merge worker setup: %w", err)
	}
	pushWorker, err := delivery.NewPushWorker(delivery.PushWorkerOptions{
		Pool:   pool.Raw(),
		Git:    delivery.UnavailableGitService{},
		Events: bus,
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("worker push worker setup: %w", err)
	}
	deployWorker, err := delivery.NewDeployWorker(delivery.DeployWorkerOptions{
		Pool:   pool.Raw(),
		Events: bus,
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("worker deploy worker setup: %w", err)
	}
	envUpdater, err := delivery.NewEnvUpdater(delivery.EnvUpdaterOptions{
		Pool:   pool.Raw(),
		Events: bus,
	})
	if err != nil {
		return fmt.Errorf("worker deploy env updater setup: %w", err)
	}
	deliveryConsumer := delivery.NewEventConsumer(delivery.EventConsumerOptions{
		Bus:     bus,
		Updater: envUpdater,
		Logger:  logger,
	})
	jqWorker.Register(delivery.MergeExecuteJobType, mergeWorker.Execute)
	jqWorker.Register("push_execute", pushWorker.Execute)
	jqWorker.Register(delivery.DeployTaskCreateJobType, deployWorker.Execute)
	dailyRollupJob, err := model.NewDailyRollupJob(model.DailyRollupJobOptions{
		Pool:   pool.Raw(),
		Events: bus,
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("worker model usage rollup setup: %w", err)
	}
	dailyRollupJob.RegisterJobs(jqWorker)

	budgetService, err := budget.NewService(budget.Options{
		Pool:   pool.Raw(),
		Events: bus,
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("worker budget service setup: %w", err)
	}
	chatService, err := chat.NewService(chat.Options{
		Pool:   pool.Raw(),
		Events: bus,
	})
	if err != nil {
		return fmt.Errorf("worker chat service setup: %w", err)
	}
	flowSessionBridge, err := projectsvc.NewFlowSessionBridge(projectsvc.FlowSessionBridgeOptions{
		Pool:  pool.Raw(),
		Chats: chatService,
	})
	if err != nil {
		return fmt.Errorf("worker flow session bridge setup: %w", err)
	}
	flowService, err := flowsvc.NewService(flowsvc.Options{
		Pool:          pool.Raw(),
		Events:        bus,
		TasksService:  tasks,
		SessionBridge: flowSessionBridge,
	})
	if err != nil {
		return fmt.Errorf("worker flow service setup: %w", err)
	}

	runService, err := controlplane.NewRunService(controlplane.RunServiceOptions{
		Pool:          pool.Raw(),
		EventBus:      bus,
		Budget:        budgetService,
		SessionBridge: flowSessionBridge,
		Logger:        logger,
	})
	if err != nil {
		return fmt.Errorf("worker run service setup: %w", err)
	}
	queueRuns, ok := runService.(interface {
		CreateRun(ctx context.Context, input controlplane.CreateRunInput) (controlplane.Run, error)
		CreateExecutionWakeup(ctx context.Context, input controlplane.ExecutionWakeupInput) (controlplane.ExecutionWakeupResult, error)
		StartRun(ctx context.Context, runID uuid.UUID) error
		CompleteRun(ctx context.Context, runID uuid.UUID, output json.RawMessage) error
		FailRun(ctx context.Context, runID uuid.UUID, reason, failureClass string) error
		ConfirmCancelled(ctx context.Context, runID uuid.UUID) error
		GetRun(ctx context.Context, runID uuid.UUID) (controlplane.Run, error)
		ListRunsByTask(ctx context.Context, organizationID, taskID uuid.UUID, status, triggerType string) ([]controlplane.Run, error)
		ReleaseExecutionOwner(ctx context.Context, taskID, sessionID uuid.UUID, reason string) (controlplane.ExecutionWakeupResult, error)
		ReleaseExecutionOwnerForRun(ctx context.Context, taskID, sessionID, runID uuid.UUID, reason string) (controlplane.ExecutionWakeupResult, error)
		RetireRuntimeStateForTask(ctx context.Context, taskID uuid.UUID, reason string) error
		RetireRuntimeStateForProject(ctx context.Context, projectID uuid.UUID, reason string) error
	})
	if !ok {
		return fmt.Errorf("worker run service does not support execution wakeups")
	}
	queueProcessor, err := controlplane.NewTaskQueueProcessor(controlplane.TaskQueueProcessorOptions{
		Events:         bus,
		Tasks:          repo.NewProjectTaskRepo(pool.Raw()),
		Projects:       repo.NewProjectRepo(pool.Raw()),
		TaskService:    tasks,
		Flow:           flowService,
		FlowExecutions: repo.NewFlowNodeExecutionRepo(pool.Raw()),
		FlowNodes:      repo.NewFlowNodeRepo(pool.Raw()),
		Assignments:    repo.NewAgentProjectAssignmentRepo(pool.Raw()),
		Runs:           queueRuns,
		Chats:          chatService,
		Sessions:       repo.NewChatSessionRepo(pool.Raw()),
	})
	if err != nil {
		return fmt.Errorf("worker task queue processor setup: %w", err)
	}
	taskQueuedSub := queueProcessor.SubscribeTaskQueued(nil)
	defer bus.Unsubscribe(taskQueuedSub)
	taskCompletedSub := queueProcessor.SubscribeTaskCompleted(nil)
	defer bus.Unsubscribe(taskCompletedSub)
	runCancellationSub := queueProcessor.SubscribeRunCancellationRequested(nil)
	defer bus.Unsubscribe(runCancellationSub)
	flowAdvancedSub := queueProcessor.SubscribeFlowAdvanced(nil)
	defer bus.Unsubscribe(flowAdvancedSub)
	projectResumedSub := queueProcessor.SubscribeProjectResumed(nil)
	defer bus.Unsubscribe(projectResumedSub)
	projectArchivedSub := queueProcessor.SubscribeProjectArchived(nil)
	defer bus.Unsubscribe(projectArchivedSub)
	turnCompletedSub := queueProcessor.SubscribeTurnCompletedWakeups(nil)
	defer bus.Unsubscribe(turnCompletedSub)
	turnCancelledSub := queueProcessor.SubscribeTurnCancelledWakeups(nil)
	defer bus.Unsubscribe(turnCancelledSub)
	if repaired, err := repairExecutionWakeupsMissingKickoff(ctx, pool.Raw(), queueProcessor); err != nil {
		return fmt.Errorf("worker startup execution wakeup repair: %w", err)
	} else if repaired > 0 {
		logger.Info("worker startup: repaired active execution wakeups missing kickoff", "count", repaired)
	}
	toolResolver, err := tools.NewToolResolver(tools.ToolResolverOptions{
		Pool:   pool.Raw(),
		Events: bus,
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("worker tool resolver setup: %w", err)
	}
	secretService := secret.NewService(repo.NewSecretRepo(pool.Raw()))

	storageBackend, err := storage.New(storage.ConfigFromEnv(os.LookupEnv))
	if err != nil {
		return fmt.Errorf("worker object storage setup: %w", err)
	}

	policyEvaluator, err := policy.NewPolicyEvaluator(policy.EvaluatorOptions{
		Policies: repo.NewCapabilityPolicyRepo(pool.Raw()),
		Budgets:  budgetService,
	})
	if err != nil {
		return fmt.Errorf("worker policy evaluator setup: %w", err)
	}
	if err := policyEvaluator.LoadInstancePolicies(ctx); err != nil {
		return fmt.Errorf("worker policy evaluator load: %w", err)
	}
	capabilityPolicy := &capabilityPolicyEvaluator{evaluator: policyEvaluator}

	cliExecutor := cli.NewExecutor(cli.ExecutorOptions{
		Pool:          pool.Raw(),
		SecretService: secretService,
		Store:         storageBackend,
	})
	memoryRetriever, err := memory.NewRetriever(memory.RetrieverOptions{
		Pool:     pool.Raw(),
		Embedder: deterministicQueryEmbedder{},
	})
	if err != nil {
		return fmt.Errorf("worker memory retriever setup: %w", err)
	}
	var profileCatalog *profiles.Catalog
	profileDir := strings.TrimSpace(os.Getenv("OTTERCAMP_PROFILES_DIR"))
	if profileDir == "" {
		profileDir = "./agent-profiles"
	}
	if cat, catErr := profiles.LoadCatalog(profileDir); catErr != nil {
		logger.Warn("agent profiles not loaded", "error", catErr)
	} else {
		profileCatalog = cat
	}
	nativeExecutor := nativetools.NewExecutor(nativetools.ExecutorOptions{
		Pool:     pool.Raw(),
		DataDir:  strings.TrimSpace(os.Getenv("OTTERCAMP_DATA_DIR")),
		Memory:   memoryRetriever,
		CLI:      cliExecutor,
		Events:   bus,
		Secrets:  secretService,
		Profiles: profileCatalog,
	})
	browserExecutor, err := browser.NewExecutor(browser.ExecutorOptions{
		Pool:      pool.Raw(),
		Runs:      runService,
		Artifacts: controlplane.NewRunArtifactRepository(pool.Raw()),
		Store:     storageBackend,
	})
	if err != nil {
		return fmt.Errorf("worker browser executor setup: %w", err)
	}

	mcpConnectionRepo := repo.NewMCPConnectionRepo(pool.Raw())
	mcpCatalogRepo := repo.NewMCPToolCatalogRepo(pool.Raw())
	mcpService, err := mcp.NewService(mcp.ServiceOptions{
		Connections:      mcpConnectionRepo,
		Catalog:          mcpCatalogRepo,
		Bindings:         repo.NewMCPSecretBindingRepo(pool.Raw()),
		Resolver:         secretService,
		EventBus:         bus,
		TransportFactory: mcp.NewDefaultTransportFactory(nil),
	})
	if err != nil {
		return fmt.Errorf("worker mcp service setup: %w", err)
	}
	mcpExecutor, err := mcp.NewExecutor(mcp.ExecutorOptions{
		Connections:      mcpConnectionRepo,
		ConnectionStatus: mcpConnectionRepo,
		Catalog:          mcpCatalogRepo,
		Assignments:      repo.NewAgentProjectAssignmentRepo(pool.Raw()),
		Caller:           mcpService,
		Logs:             repo.NewMCPExecutionLogRepo(pool.Raw()),
	})
	if err != nil {
		return fmt.Errorf("worker mcp executor setup: %w", err)
	}

	toolBroker, err := controlplane.NewToolBroker(controlplane.ToolBrokerOptions{
		Pool:     pool.Raw(),
		EventBus: bus,
		Policy:   capabilityPolicy,
		Native:   nativeExecutor,
		CLI:      cliExecutor,
		Browser:  browserExecutor,
		MCP:      mcpExecutor,
	})
	if err != nil {
		return fmt.Errorf("worker tool broker setup: %w", err)
	}
	toolDispatcher, err := newLiveToolDispatcher(toolBroker, runService, repo.NewAgentRepo(pool.Raw()))
	if err != nil {
		return fmt.Errorf("worker tool dispatcher setup: %w", err)
	}

	promptAssembler, err := prompt.NewPromptAssembler(prompt.AssemblerOptions{
		Pool:   pool.Raw(),
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("worker prompt assembler setup: %w", err)
	}
	summarizationChecker, err := chat.NewSummarizationChecker(chat.SummarizationCheckerOptions{
		Pool: pool.Raw(),
	})
	if err != nil {
		return fmt.Errorf("worker summarization checker setup: %w", err)
	}
	profileResolver := model.NewProfileResolver(
		repo.NewModelProfileAssignmentRepo(pool.Raw()),
		repo.NewModelProfileRepo(pool.Raw()),
	)

	modelInvocationRepo := repo.NewModelInvocationRepo(pool.Raw())
	healthChecker := gateway.NewHealthChecker()
	traceSpanService, err := observability.NewTraceSpanService(pool.Raw())
	if err != nil {
		return fmt.Errorf("worker trace span service setup: %w", err)
	}
	liveModelGateway, err := gateway.NewLiveModelGateway(gateway.LiveModelGatewayOptions{
		Router:      gateway.NewRouter(repo.NewModelProfileRepo(pool.Raw()), repo.NewProviderConnectionRepo(pool.Raw()), healthChecker),
		Providers:   repo.NewModelProviderRepo(pool.Raw()),
		Secrets:     secret.NewService(repo.NewSecretRepo(pool.Raw())),
		Invocations: modelInvocationRepo,
		HealthStore: repo.NewProviderConnectionRepo(pool.Raw()),
		Enqueuer:    jqWorker,
		Health:      healthChecker,
		Spans:       traceSpanService,
		Logger:      logger,
	})
	if err != nil {
		return fmt.Errorf("worker live model gateway setup: %w", err)
	}
	rollupWorker := gateway.NewRollupWorker(pool.Raw(), nil, logger)
	rollupWorker.RegisterJobs(jqWorker)

	summarizer, err := chat.NewSummarizer(chat.SummarizerOptions{
		Pool:     pool.Raw(),
		Resolver: profileResolver,
		Model:    &gatewaySummarizationModel{gateway: liveModelGateway},
	})
	if err != nil {
		return fmt.Errorf("worker summarizer setup: %w", err)
	}
	summarizer.RegisterJobs(jqWorker)

	sessionCleaner, err := chat.NewSessionCleaner(chat.SessionCleanerOptions{
		Pool:     pool.Raw(),
		Resolver: profileResolver,
		Model:    &gatewaySummarizationModel{gateway: liveModelGateway},
	})
	if err != nil {
		return fmt.Errorf("worker session cleaner setup: %w", err)
	}
	sessionCleaner.RegisterJobs(jqWorker)

	modelGateway := turn.ModelGateway(liveModelGateway)
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OTTERCAMP_MODE")), "test") {
		modelGateway = deterministicTurnModelGateway{}
	}
	turnEngine, err := turn.NewEngine(turn.Options{
		Pool:            pool.Raw(),
		DataDir:         strings.TrimSpace(os.Getenv("OTTERCAMP_DATA_DIR")),
		Chat:            chatService,
		ToolResolver:    toolResolver,
		Assembler:       promptAssembler,
		Summarization:   summarizationChecker,
		ModelGateway:    modelGateway,
		Dispatcher:      toolDispatcher,
		RunCanceler:     runService,
		Events:          bus,
		Enqueuer:        jqWorker,
		Invocations:     modelInvocationRepo,
		ModelProfiles:   repo.NewModelProfileRepo(pool.Raw()),
		Profiles:        profileResolver,
		Messages:        repo.NewChatMessageRepo(pool.Raw()),
		Turns:           repo.NewChatTurnRepo(pool.Raw()),
		Sessions:        repo.NewChatSessionRepo(pool.Raw()),
		Agents:          repo.NewAgentRepo(pool.Raw()),
		Tasks:           repo.NewProjectTaskRepo(pool.Raw()),
		Projects:        repo.NewProjectRepo(pool.Raw()),
		Organizations:   repo.NewOrgRepo(pool.Raw()),
		FlowNodes:       repo.NewFlowNodeRepo(pool.Raw()),
		FlowAdvancer:    flowService,
		Assignments:     repo.NewAgentProjectAssignmentRepo(pool.Raw()),
		Environments:    repo.NewProjectEnvironmentRepo(pool.Raw()),
		TaskTransitions: tasks,
		MemorySources:   repo.NewMemorySourceRepo(pool.Raw()),
		Memories:        repo.NewMemoryRepo(pool.Raw()),
		Logger:          logger,
	})
	if err != nil {
		return fmt.Errorf("worker turn engine setup: %w", err)
	}
	turnEngine.RegisterJobHandler(jqWorker)
	turnUserSub := turnEngine.SubscribeUserMessageEnqueue(nil)
	defer bus.Unsubscribe(turnUserSub)
	turnReactionSub := turnEngine.SubscribeReactionFeedback(nil)
	defer bus.Unsubscribe(turnReactionSub)
	turnAutoContinueSub := turnEngine.SubscribeTurnCompletedAutoContinuation(nil)
	defer bus.Unsubscribe(turnAutoContinueSub)
	turnBootstrapCancelledSub := turnEngine.SubscribeTurnCancelledBootstrapRecovery(nil)
	defer bus.Unsubscribe(turnBootstrapCancelledSub)
	turnTaskStatusSub := turnEngine.SubscribeTaskStatusBootstrap(nil)
	defer bus.Unsubscribe(turnTaskStatusSub)
	turnProjectResumedSub := turnEngine.SubscribeProjectResumedPendingTurns(nil)
	defer bus.Unsubscribe(turnProjectResumedSub)
	if cleaned, cleanErr := turnEngine.CleanupLegacyCancelConsumerCursors(context.Background()); cleanErr != nil {
		logger.Warn("failed to clean legacy turn cancel consumer cursors", "error", cleanErr)
	} else if cleaned > 0 {
		logger.Info("cleaned legacy turn cancel consumer cursors", "count", cleaned)
	}
	if recovered, recoverErr := turnEngine.RecoverCancelledBootstrapSessions(context.Background()); recoverErr != nil {
		logger.Warn("failed to recover cancelled bootstrap sessions", "error", recoverErr)
	} else if recovered > 0 {
		logger.Info("recovered cancelled bootstrap sessions", "count", recovered)
	}

	memoryConsumer, err := memory.NewEventConsumer(memory.EventConsumerOptions{
		Pool:     pool.Raw(),
		Events:   bus,
		Enqueuer: jqWorker,
	})
	if err != nil {
		return fmt.Errorf("worker memory event consumer setup: %w", err)
	}
	memoryConsumer.RegisterJobs(jqWorker)
	memoryTurnCompletedSub := memoryConsumer.SubscribeTurnCompleted(nil)
	defer bus.Unsubscribe(memoryTurnCompletedSub)

	sleepReflector, err := compaction.NewSleepReflector(compaction.SleepReflectorOptions{
		Pool:         pool.Raw(),
		Deduplicator: compaction.DefaultThresholdDeduplicator(),
	})
	if err != nil {
		return fmt.Errorf("worker sleep reflector setup: %w", err)
	}
	sleepReflector.RegisterJobs(jqWorker)
	// TaskConsolidator requires a TaskSummaryModel (LLM) — not yet wired (issue 126)

	memImporter, err := importer.NewImporter(importer.ImporterOptions{
		Pool:  pool.Raw(),
		Store: storageBackend,
	})
	if err != nil {
		return fmt.Errorf("worker memory importer setup: %w", err)
	}
	memImporter.RegisterJobs(jqWorker)

	memHardener, err := memory.NewHardener(memory.HardenerOptions{
		Pool:   pool.Raw(),
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("worker memory hardener setup: %w", err)
	}
	memHardener.RegisterJobs(jqWorker)

	retentionSweeper, err := chat.NewRetentionSweeper(chat.RetentionSweeperOptions{
		Pool: pool.Raw(),
	})
	if err != nil {
		return fmt.Errorf("worker retention sweeper setup: %w", err)
	}
	retentionSweeper.RegisterJobs(jqWorker)

	budgetService.RegisterJobs(jqWorker)

	supervisor, err := controlplane.NewSupervisor(controlplane.SupervisorOptions{
		Pool:        pool.Raw(),
		RunService:  runService,
		ChatService: chatService,
		EventBus:    bus,
		Logger:      logger,
	})
	if err != nil {
		return fmt.Errorf("worker supervisor setup: %w", err)
	}

	pushRepo := push.NewPreferenceRepository(pool.Raw())
	pushService, err := push.NewPreferenceService(push.PreferenceServiceOptions{
		Repository: pushRepo,
	})
	if err != nil {
		return fmt.Errorf("worker push preference service setup: %w", err)
	}
	pushAdapter := adapters.NewMultiAdapter(
		adapters.NewAPNSAdapter(logger),
		adapters.NewFCMAdapter(logger),
	)
	pushConsumer, err := push.NewDeliveryConsumer(push.DeliveryConsumerOptions{
		Pool:        pool.Raw(),
		Logger:      logger,
		Preferences: pushService,
		Tokens:      pushRepo,
		Adapter:     pushAdapter,
	})
	if err != nil {
		return fmt.Errorf("worker push delivery consumer setup: %w", err)
	}
	pushConsumerSub := bus.Subscribe("push.delivery.consumer", nil, func(ctx context.Context, event eventbus.DomainEvent) error {
		return pushConsumer.Consume(ctx, event)
	})
	defer bus.Unsubscribe(pushConsumerSub)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopDeliveryConsumer := deliveryConsumer.Start(runCtx)
	defer stopDeliveryConsumer()
	mcpService.StartHealthScheduler(runCtx)

	heartbeat := scheduling.NewSchedulerHeartbeat(pool.Raw(), jqWorker, logger, nil)
	heartbeat.Start(runCtx)
	model.NewDailyRollupTicker(jqWorker, logger, nil).Start(runCtx)
	jobs.NewDailyTicker(jqWorker, logger, jobs.RetentionEnforceJobType, 90, map[string]any{"source": "retention_ticker"}).Start(runCtx)
	jobs.NewDailyTicker(jqWorker, logger, jobs.TraceSpanPartitionCreateJobType, 90, map[string]any{"source": "trace_partition_ticker"}).Start(runCtx)
	supervisor.Start(runCtx)
	defer supervisor.Stop()

	// Purge stale agent_turn jobs for closed/archived sessions before
	// starting the job worker. This prevents wasting LLM calls on old turns
	// after a restart.
	if purged, purgeErr := jqWorker.PurgeStaleAgentTurnJobs(runCtx); purgeErr != nil {
		logger.Warn("failed to purge stale agent_turn jobs", "error", purgeErr)
	} else if purged > 0 {
		logger.Info("purged stale agent_turn jobs", "count", purged)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- jqWorker.Start(runCtx)
	}()

	logger.Info("worker started")
	defer logger.Info("worker stopped")

	select {
	case <-runCtx.Done():
	case <-signalCh:
		cancel()
	case err := <-errCh:
		if err != nil {
			return err
		}
		cancel()
	}

	_ = jqWorker.Stop()
	if err := <-errCh; err != nil {
		return err
	}

	return nil
}

type deterministicQueryEmbedder struct{}

func (deterministicQueryEmbedder) Embed(_ context.Context, _ uuid.UUID, _ string, inputs []string) ([][]float32, error) {
	vectors := make([][]float32, 0, len(inputs))
	for _, input := range inputs {
		sum := sha256.Sum256([]byte(strings.TrimSpace(input)))
		vector := make([]float32, deterministicQueryEmbeddingDimensions)
		vector[0] = float32(sum[0]) / 255.0
		vector[1] = 1
		for i := 0; i < len(sum) && i+2 < len(vector); i++ {
			vector[i+2] = float32(sum[i]) / 255.0
		}
		vectors = append(vectors, vector)
	}
	return vectors, nil
}

func startupProjectCleanup(ctx context.Context, pool *pgxpool.Pool, tasks tasksvc.TaskService) (int, int, int, int, error) {
	if pool == nil || tasks == nil {
		return 0, 0, 0, 0, nil
	}
	orgRepo := repo.NewOrgRepo(pool)
	projectRepo := repo.NewProjectRepo(pool)
	taskRepo := repo.NewProjectTaskRepo(pool)

	orgs, err := orgRepo.List(ctx)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	parentRepaired := 0
	draftSettled := 0
	gatesCancelled := 0
	pausesCleared := 0
	for _, org := range orgs {
		projects, err := projectRepo.List(ctx, org.ID)
		if err != nil {
			return parentRepaired, draftSettled, gatesCancelled, pausesCleared, err
		}
		for _, project := range projects {
			cleared, err := clearStaleBootstrapPauseForQueuedFirstWave(ctx, pool, projectRepo, taskRepo, project)
			if err != nil {
				return parentRepaired, draftSettled, gatesCancelled, pausesCleared, err
			}
			if cleared {
				pausesCleared++
			}
			repaired, settled, cancelled, err := startupCleanupProjectDrafts(ctx, taskRepo, tasks, project.ID)
			if err != nil {
				return parentRepaired, draftSettled, gatesCancelled, pausesCleared, err
			}
			parentRepaired += repaired
			draftSettled += settled
			gatesCancelled += cancelled
		}
	}
	return parentRepaired, draftSettled, gatesCancelled, pausesCleared, nil
}

func clearStaleBootstrapPauseForQueuedFirstWave(ctx context.Context, pool *pgxpool.Pool, projectRepo *repo.ProjectRepo, taskRepo *repo.ProjectTaskRepo, project repo.Project) (bool, error) {
	if project.ID == uuid.Nil || pool == nil || projectRepo == nil || taskRepo == nil {
		return false, nil
	}
	pauseState := projectpause.Parse(project.Settings)
	if !pauseState.IsPaused {
		return false, nil
	}
	pauseMetadata := parseJSONMap(pauseState.Metadata)
	if !strings.EqualFold(strings.TrimSpace(fmt.Sprintf("%v", pauseMetadata["source"])), "project_bootstrap") {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(fmt.Sprintf("%v", pauseMetadata["failure_class"])), "first_wave_execution_missing") {
		return false, nil
	}
	tasks, err := taskRepo.ListByProject(ctx, project.ID)
	if err != nil {
		return false, err
	}
	selectedCount := 0
	promotedCount := 0
	selectedTaskIDs := make([]uuid.UUID, 0)
	for _, task := range tasks {
		metadata := parseJSONMap(task.Metadata)
		if !boolValue(metadata["bootstrap_first_wave_selected"]) {
			continue
		}
		selectedCount++
		selectedTaskIDs = append(selectedTaskIDs, task.ID)
		switch strings.ToLower(strings.TrimSpace(task.WorkStatus)) {
		case "queued", "in_progress", "review", "done":
			promotedCount++
		}
	}
	if selectedCount == 0 {
		return false, nil
	}
	executionCount, err := countActiveFirstWaveExecutions(ctx, pool, selectedTaskIDs)
	if err != nil {
		return false, err
	}
	if promotedCount == 0 && executionCount == 0 {
		return false, nil
	}
	updated := project
	updated.Settings, err = projectpause.ClearPause(updated.Settings)
	if err != nil {
		return false, err
	}
	if _, err := projectRepo.Update(ctx, updated); err != nil {
		return false, err
	}
	return true, nil
}

func countActiveFirstWaveExecutions(ctx context.Context, pool *pgxpool.Pool, taskIDs []uuid.UUID) (int, error) {
	if pool == nil || len(taskIDs) == 0 {
		return 0, nil
	}
	var count int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM flow_node_execution
		WHERE task_id = ANY($1)
		  AND status = 'active'
	`, taskIDs).Scan(&count)
	return count, err
}

func parseJSONMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || !json.Valid(raw) {
		return map[string]any{}
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil || payload == nil {
		return map[string]any{}
	}
	return payload
}

func boolValue(raw any) bool {
	switch value := raw.(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func startupCleanupProjectDrafts(ctx context.Context, taskRepo *repo.ProjectTaskRepo, tasks tasksvc.TaskService, projectID uuid.UUID) (int, int, int, error) {
	parentRepaired := 0
	draftSettled := 0
	gatesCancelled := 0
	for pass := 0; pass < 4; pass++ {
		drafts, err := taskRepo.ListByProject(ctx, projectID, "draft")
		if err != nil {
			return parentRepaired, draftSettled, gatesCancelled, err
		}
		progressed := false
		for _, task := range drafts {
			switch {
			case len(taskdecomp.ParseChildTaskIDs(task.Metadata)) > 0:
				_, err := tasks.TransitionStatus(ctx, task.ID, "done", tasksvc.Actor{
					Type:                           "system",
					AllowOrchestrationAutoComplete: true,
				})
				if err == nil {
					parentRepaired++
					progressed = true
					continue
				}
				if errors.Is(err, repo.ErrConflict) || errors.Is(err, taskorchestration.ErrParentCompletionRequirements) {
					continue
				}
				var invalidTransition tasksvc.ErrInvalidStatusTransition
				if errors.As(err, &invalidTransition) {
					continue
				}
				return parentRepaired, draftSettled, gatesCancelled, err
			case draftTaskAutoCompletes(task):
				_, err := tasks.TransitionStatus(ctx, task.ID, "done", tasksvc.Actor{
					Type:                            "system",
					AllowDoneBypass:                 true,
					AllowSatisfiedDraftAutoComplete: true,
				})
				if err == nil {
					draftSettled++
					progressed = true
					continue
				}
				if errors.Is(err, repo.ErrConflict) {
					continue
				}
				var invalidTransition tasksvc.ErrInvalidStatusTransition
				if errors.As(err, &invalidTransition) {
					continue
				}
				return parentRepaired, draftSettled, gatesCancelled, err
			case strings.EqualFold(strings.TrimSpace(task.BlocksScope), "all") && tasksvc.ValidateProjectGateTask(task) != nil:
				_, err := tasks.TransitionStatus(ctx, task.ID, "cancelled", tasksvc.Actor{Type: "system"})
				if err == nil {
					gatesCancelled++
					progressed = true
					continue
				}
				if errors.Is(err, repo.ErrConflict) {
					continue
				}
				var invalidTransition tasksvc.ErrInvalidStatusTransition
				if errors.As(err, &invalidTransition) {
					continue
				}
				return parentRepaired, draftSettled, gatesCancelled, err
			}
		}
		if !progressed {
			break
		}
	}
	return parentRepaired, draftSettled, gatesCancelled, nil
}

func draftTaskAutoCompletes(task repo.ProjectTask) bool {
	if !strings.EqualFold(strings.TrimSpace(task.WorkStatus), "draft") {
		return false
	}
	return tasksvc.SatisfiedDraftAutoCompletable(task)
}

func repairExecutionWakeupsMissingKickoff(ctx context.Context, pool *pgxpool.Pool, queueProcessor *controlplane.TaskQueueProcessor) (int64, error) {
	if pool == nil || queueProcessor == nil {
		return 0, nil
	}
	rows, err := pool.Query(ctx, `
		SELECT e.task_id, e.id
		FROM flow_node_execution e
		JOIN chat_session cs ON cs.id = e.session_id
		WHERE e.status = 'active'
		  AND COALESCE(e.runtime_substate, 'waiting_for_turn') = 'waiting_for_turn'
		  AND cs.scope_type = 'project_task'
		  AND cs.mode = 'async'
		  AND cs.status = 'active'
		  AND cs.current_turn_id IS NULL
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_message cm
		    WHERE cm.session_id = cs.id
		      AND cm.role = 'user'
		      AND COALESCE(cm.metadata->>'source', '') = 'task_queue_processor'
		      AND COALESCE(cm.metadata->>'flow_node_execution_id', '') = e.id::text
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM job_queue jq
		    WHERE jq.job_type = 'agent_turn'
		      AND jq.status IN ('pending', 'claimed')
		      AND (
		        COALESCE(jq.payload->>'flow_node_execution_id', '') = e.id::text
		        OR (jq.payload->>'session_id')::uuid = cs.id
		      )
		  )
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var repaired int64
	for rows.Next() {
		var taskID uuid.UUID
		var executionID uuid.UUID
		if err := rows.Scan(&taskID, &executionID); err != nil {
			return repaired, err
		}
		if err := queueProcessor.RepairExecutionWakeupKickoff(ctx, taskID, executionID); err != nil {
			return repaired, err
		}
		repaired++
	}
	if err := rows.Err(); err != nil {
		return repaired, err
	}
	return repaired, nil
}
