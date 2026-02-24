package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/samhotchkiss/otter-camp/internal/budget"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/db"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/scheduling"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
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

	budgetService, err := budget.NewService(budget.Options{
		Pool:   pool.Raw(),
		Events: bus,
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("worker budget service setup: %w", err)
	}

	runService, err := controlplane.NewRunService(controlplane.RunServiceOptions{
		Pool:     pool.Raw(),
		EventBus: bus,
		Budget:   budgetService,
		Logger:   logger,
	})
	if err != nil {
		return fmt.Errorf("worker run service setup: %w", err)
	}
	supervisor, err := controlplane.NewSupervisor(controlplane.SupervisorOptions{
		Pool:       pool.Raw(),
		RunService: runService,
		EventBus:   bus,
		Logger:     logger,
	})
	if err != nil {
		return fmt.Errorf("worker supervisor setup: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	heartbeat := scheduling.NewSchedulerHeartbeat(pool.Raw(), jqWorker, logger, nil)
	heartbeat.Start(runCtx)
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
