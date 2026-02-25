package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/budget"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/db"
	"github.com/samhotchkiss/otter-camp/internal/delivery"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/jobs"
	"github.com/samhotchkiss/otter-camp/internal/model"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/push"
	"github.com/samhotchkiss/otter-camp/internal/push/adapters"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/scheduling"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/tools"
	"github.com/samhotchkiss/otter-camp/internal/turn"
)

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
	toolResolver, err := tools.NewToolResolver(tools.ToolResolverOptions{
		Pool:   pool.Raw(),
		Events: bus,
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("worker tool resolver setup: %w", err)
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
	modelGateway := turn.ModelGateway(turn.UnavailableModelGateway{})
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OTTERCAMP_MODE")), "test") {
		modelGateway = deterministicTurnModelGateway{}
	}
	turnEngine, err := turn.NewEngine(turn.Options{
		Pool:          pool.Raw(),
		Chat:          chatService,
		ToolResolver:  toolResolver,
		Assembler:     promptAssembler,
		Summarization: summarizationChecker,
		ModelGateway:  modelGateway,
		Dispatcher:    turn.UnavailableToolDispatcher{},
		RunCanceler:   runService,
		Events:        bus,
		Enqueuer:      jqWorker,
		Invocations:   repo.NewModelInvocationRepo(pool.Raw()),
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

	supervisor, err := controlplane.NewSupervisor(controlplane.SupervisorOptions{
		Pool:       pool.Raw(),
		RunService: runService,
		EventBus:   bus,
		Logger:     logger,
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
	bus.Subscribe("push.delivery.consumer", nil, func(ctx context.Context, event eventbus.DomainEvent) error {
		return pushConsumer.Consume(ctx, event)
	})

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopDeliveryConsumer := deliveryConsumer.Start(runCtx)
	defer stopDeliveryConsumer()

	heartbeat := scheduling.NewSchedulerHeartbeat(pool.Raw(), jqWorker, logger, nil)
	heartbeat.Start(runCtx)
	model.NewDailyRollupTicker(jqWorker, logger, nil).Start(runCtx)
	jobs.NewDailyTicker(jqWorker, logger, jobs.RetentionEnforceJobType, 90, map[string]any{"source": "retention_ticker"}).Start(runCtx)
	jobs.NewDailyTicker(jqWorker, logger, jobs.TraceSpanPartitionCreateJobType, 90, map[string]any{"source": "trace_partition_ticker"}).Start(runCtx)
	supervisor.Start(runCtx)
	defer supervisor.Stop()

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
