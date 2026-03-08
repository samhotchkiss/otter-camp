package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"
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
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/push"
	"github.com/samhotchkiss/otter-camp/internal/push/adapters"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/scheduling"
	"github.com/samhotchkiss/otter-camp/internal/secret"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/tools"
	nativetools "github.com/samhotchkiss/otter-camp/internal/tools/native"
	"github.com/samhotchkiss/otter-camp/internal/turn"
)

const deterministicQueryEmbeddingDimensions = 1536

func Run(ctx context.Context, logger *slog.Logger, signalCh <-chan os.Signal) error {
	if logger == nil {
		logger = slog.Default()
	}

	pool, err := db.NewFromEnv(ctx)
	if err != nil {
		return fmt.Errorf("worker database setup: %w", err)
	}
	defer pool.Close()

	bus := eventbus.New(pool.Raw(), logger, eventbus.Config{})
	tasks, err := tasksvc.NewService(tasksvc.Options{
		Pool:     pool.Raw(),
		EventBus: bus,
	})
	if err != nil {
		return fmt.Errorf("worker task service setup: %w", err)
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

	jqWorker := jobqueue.New(pool.Raw(), logger, jobqueue.Config{})
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
		Pool:          pool.Raw(),
		DataDir:       strings.TrimSpace(os.Getenv("OTTERCAMP_DATA_DIR")),
		Chat:          chatService,
		ToolResolver:  toolResolver,
		Assembler:     promptAssembler,
		Summarization: summarizationChecker,
		ModelGateway:  modelGateway,
		Dispatcher:    toolDispatcher,
		RunCanceler:   runService,
		Events:        bus,
		Enqueuer:      jqWorker,
		Invocations:   modelInvocationRepo,
		ModelProfiles: repo.NewModelProfileRepo(pool.Raw()),
		Profiles:      profileResolver,
		Messages:      repo.NewChatMessageRepo(pool.Raw()),
		Turns:         repo.NewChatTurnRepo(pool.Raw()),
		Sessions:      repo.NewChatSessionRepo(pool.Raw()),
		Agents:        repo.NewAgentRepo(pool.Raw()),
		Tasks:         repo.NewProjectTaskRepo(pool.Raw()),
		MemorySources: repo.NewMemorySourceRepo(pool.Raw()),
		Memories:      repo.NewMemoryRepo(pool.Raw()),
		Logger:        logger,
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
