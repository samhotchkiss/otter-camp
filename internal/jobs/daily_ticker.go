package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type dailyJobEnqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error)
}

type DailyTicker struct {
	enqueuer dailyJobEnqueuer
	logger   *slog.Logger
	jobType  string
	priority int
	payload  any
	interval time.Duration
}

func NewDailyTicker(enqueuer dailyJobEnqueuer, logger *slog.Logger, jobType string, priority int, payload any) *DailyTicker {
	if logger == nil {
		logger = slog.Default()
	}
	return &DailyTicker{
		enqueuer: enqueuer,
		logger:   logger,
		jobType:  jobType,
		priority: priority,
		payload:  payload,
		interval: 24 * time.Hour,
	}
}

func (t *DailyTicker) Start(ctx context.Context) {
	if t == nil || t.enqueuer == nil || t.jobType == "" {
		return
	}
	go func() {
		t.enqueue(ctx)
		ticker := time.NewTicker(t.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.enqueue(ctx)
			}
		}
	}()
}

func (t *DailyTicker) enqueue(ctx context.Context) {
	if _, err := t.enqueuer.Enqueue(ctx, nil, t.jobType, t.priority, t.payload, nil); err != nil && ctx.Err() == nil {
		t.logger.Error("daily job enqueue failed", "job_type", t.jobType, "error", err)
	}
}
