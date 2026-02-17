package importer

import (
	"context"
	"fmt"
	"time"
)

const (
	defaultOpenClawEmbeddingPhaseTimeout      = 5 * time.Minute
	defaultOpenClawEmbeddingPhasePollInterval = 500 * time.Millisecond
)

type openClawEmbeddingRunOnceWorker interface {
	RunOnce(ctx context.Context) (int, error)
}

type openClawEmbeddingPendingCounter interface {
	CountPendingEmbeddings(ctx context.Context, orgID string) (int, error)
}

type OpenClawEmbeddingPhaseDrainRunner struct {
	Worker         openClawEmbeddingRunOnceWorker
	PendingCounter openClawEmbeddingPendingCounter
	Timeout        time.Duration
	PollInterval   time.Duration
	now            func() time.Time
	sleep          func(ctx context.Context, duration time.Duration) error
}

func NewOpenClawEmbeddingPhaseDrainRunner(
	worker openClawEmbeddingRunOnceWorker,
	counter openClawEmbeddingPendingCounter,
	timeout time.Duration,
) *OpenClawEmbeddingPhaseDrainRunner {
	return &OpenClawEmbeddingPhaseDrainRunner{
		Worker:         worker,
		PendingCounter: counter,
		Timeout:        timeout,
		PollInterval:   defaultOpenClawEmbeddingPhasePollInterval,
		now:            time.Now,
		sleep:          sleepOpenClawEmbeddingPhase,
	}
}

func (r *OpenClawEmbeddingPhaseDrainRunner) RunEmbeddingPhase(
	ctx context.Context,
	input OpenClawEmbeddingPhaseInput,
) (OpenClawEmbeddingPhaseResult, error) {
	if r == nil {
		return OpenClawEmbeddingPhaseResult{}, fmt.Errorf("embedding phase runner is nil")
	}
	if r.Worker == nil {
		return OpenClawEmbeddingPhaseResult{}, fmt.Errorf("embedding phase worker is required")
	}
	if r.PendingCounter == nil {
		return OpenClawEmbeddingPhaseResult{}, fmt.Errorf("embedding phase pending counter is required")
	}

	now := r.now
	if now == nil {
		now = time.Now
	}
	sleep := r.sleep
	if sleep == nil {
		sleep = sleepOpenClawEmbeddingPhase
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultOpenClawEmbeddingPhaseTimeout
	}
	pollInterval := r.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultOpenClawEmbeddingPhasePollInterval
	}

	startedAt := now()
	deadline := startedAt.Add(timeout)
	result := OpenClawEmbeddingPhaseResult{}

	remaining, err := r.PendingCounter.CountPendingEmbeddings(ctx, input.OrgID)
	if err != nil {
		return OpenClawEmbeddingPhaseResult{}, err
	}
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return OpenClawEmbeddingPhaseResult{}, err
		}
		if !now().Before(deadline) {
			result.TimedOut = true
			break
		}

		processed, runErr := r.Worker.RunOnce(ctx)
		if runErr != nil {
			return OpenClawEmbeddingPhaseResult{}, runErr
		}
		if processed < 0 {
			processed = 0
		}
		result.ProcessedEmbeddings += processed

		remaining, err = r.PendingCounter.CountPendingEmbeddings(ctx, input.OrgID)
		if err != nil {
			return OpenClawEmbeddingPhaseResult{}, err
		}
		if remaining <= 0 {
			remaining = 0
			break
		}
		if processed > 0 {
			continue
		}

		wait := pollInterval
		untilDeadline := deadline.Sub(now())
		if untilDeadline <= 0 {
			result.TimedOut = true
			break
		}
		if wait > untilDeadline {
			wait = untilDeadline
		}
		if wait > 0 {
			if sleepErr := sleep(ctx, wait); sleepErr != nil {
				return OpenClawEmbeddingPhaseResult{}, sleepErr
			}
		}
	}

	result.RemainingEmbeddings = remaining
	result.Duration = now().Sub(startedAt)
	if remaining > 0 && !now().Before(deadline) {
		result.TimedOut = true
	}
	return result, nil
}

func sleepOpenClawEmbeddingPhase(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
