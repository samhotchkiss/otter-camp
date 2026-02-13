package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

const (
	defaultJobWorkerPollInterval  = 5 * time.Second
	defaultJobWorkerMaxPerPoll    = 50
	defaultJobWorkerRunTimeout    = 5 * time.Minute
	defaultJobWorkerMaxRunHistory = 100
	defaultJobRetryDelay          = 1 * time.Minute
)

type AgentJobWorkerConfig struct {
	PollInterval  time.Duration
	MaxPerPoll    int
	RunTimeout    time.Duration
	MaxRunHistory int
}

type AgentJobWorker struct {
	Store  *store.AgentJobStore
	Config AgentJobWorkerConfig
	Now    func() time.Time
	Logf   func(string, ...any)
}

func NewAgentJobWorker(jobStore *store.AgentJobStore, cfg AgentJobWorkerConfig) *AgentJobWorker {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultJobWorkerPollInterval
	}
	if cfg.MaxPerPoll <= 0 {
		cfg.MaxPerPoll = defaultJobWorkerMaxPerPoll
	}
	if cfg.RunTimeout <= 0 {
		cfg.RunTimeout = defaultJobWorkerRunTimeout
	}
	if cfg.MaxRunHistory <= 0 {
		cfg.MaxRunHistory = defaultJobWorkerMaxRunHistory
	}

	return &AgentJobWorker{
		Store:  jobStore,
		Config: cfg,
		Now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func (w *AgentJobWorker) Start(ctx context.Context) {
	for {
		if _, err := w.RunOnce(ctx); err != nil && w.Logf != nil {
			w.Logf("agent job worker run failed: %v", err)
		}
		if err := sleepWithContext(ctx, w.Config.PollInterval); err != nil {
			return
		}
	}
}

func (w *AgentJobWorker) RunOnce(ctx context.Context) (int, error) {
	if w == nil || w.Store == nil {
		return 0, fmt.Errorf("agent job worker is not configured")
	}

	now := w.now()
	if _, err := w.Store.CleanupStaleRuns(ctx, w.Config.RunTimeout, now); err != nil {
		return 0, err
	}

	dueJobs, err := w.Store.PickupDue(ctx, w.Config.MaxPerPoll, now)
	if err != nil {
		return 0, err
	}

	for _, job := range dueJobs {
		if execErr := w.executeJob(ctx, job); execErr != nil && w.Logf != nil {
			w.Logf("agent job execution failed for job %s: %v", job.ID, execErr)
		}
	}
	return len(dueJobs), nil
}

func (w *AgentJobWorker) executeJob(ctx context.Context, job store.AgentJob) error {
	now := w.now()
	runCtx, cancel := context.WithTimeout(ctx, w.Config.RunTimeout)
	defer cancel()

	roomID := ""
	if job.RoomID != nil {
		roomID = *job.RoomID
	}
	if roomID == "" {
		ensuredRoomID, err := w.Store.EnsureRoomForJob(runCtx, job.ID)
		if err != nil {
			return err
		}
		roomID = ensuredRoomID
	}

	run, err := w.Store.StartRun(runCtx, store.StartAgentJobRunInput{
		JobID:       job.ID,
		PayloadText: job.PayloadText,
		StartedAt:   now,
	})
	if err != nil {
		return err
	}

	scheduleSpec, scheduleErr := NormalizeScheduleSpec(
		job.ScheduleKind,
		job.CronExpr,
		job.IntervalMS,
		job.RunAt,
		job.Timezone,
	)
	if scheduleErr != nil {
		return w.completeFailure(runCtx, job, run, scheduleErr)
	}

	messageID, err := w.Store.CreateJobMessage(runCtx, store.CreateAgentJobMessageInput{
		JobID:       job.ID,
		OrgID:       job.OrgID,
		RoomID:      roomID,
		PayloadKind: job.PayloadKind,
		PayloadText: job.PayloadText,
		CreatedAt:   now,
	})
	if err != nil {
		return w.completeFailure(runCtx, job, run, err)
	}

	nextRunAt, err := ComputeNextRun(scheduleSpec, now, &now)
	if err != nil {
		return w.completeFailure(runCtx, job, run, err)
	}
	completeJob := job.ScheduleKind == store.AgentJobScheduleOnce && nextRunAt == nil
	_, err = w.Store.CompleteRun(runCtx, store.CompleteAgentJobRunInput{
		JobID:       job.ID,
		RunID:       run.ID,
		RunStatus:   store.AgentJobRunStatusSuccess,
		CompletedAt: now,
		MessageID:   &messageID,
		NextRunAt:   nextRunAt,
		CompleteJob: completeJob,
	})
	if err != nil {
		return err
	}
	_, _ = w.Store.PruneRunHistory(runCtx, job.ID, w.Config.MaxRunHistory)
	return nil
}

func (w *AgentJobWorker) completeFailure(
	ctx context.Context,
	job store.AgentJob,
	run *store.AgentJobRun,
	failure error,
) error {
	if run == nil {
		return failure
	}
	now := w.now()
	runStatus := store.AgentJobRunStatusError
	if errors.Is(failure, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		runStatus = store.AgentJobRunStatusTimeout
	}

	nextRunAt := computeFailureNextRun(job, now)
	runError := failure.Error()
	_, completeErr := w.Store.CompleteRun(ctx, store.CompleteAgentJobRunInput{
		JobID:       job.ID,
		RunID:       run.ID,
		RunStatus:   runStatus,
		CompletedAt: now,
		RunError:    &runError,
		NextRunAt:   nextRunAt,
	})
	if completeErr != nil {
		return errors.Join(failure, completeErr)
	}
	_, _ = w.Store.PruneRunHistory(ctx, job.ID, w.Config.MaxRunHistory)
	return failure
}

func computeFailureNextRun(job store.AgentJob, now time.Time) *time.Time {
	spec, err := NormalizeScheduleSpec(
		job.ScheduleKind,
		job.CronExpr,
		job.IntervalMS,
		job.RunAt,
		job.Timezone,
	)
	if err == nil {
		if nextRunAt, nextErr := ComputeNextRun(spec, now, &now); nextErr == nil {
			return nextRunAt
		}
	}
	if job.ScheduleKind == store.AgentJobScheduleOnce {
		return nil
	}
	retryAt := now.Add(defaultJobRetryDelay)
	return &retryAt
}

func (w *AgentJobWorker) now() time.Time {
	if w.Now == nil {
		return time.Now().UTC()
	}
	return w.Now().UTC()
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
