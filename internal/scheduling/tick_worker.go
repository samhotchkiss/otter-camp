package scheduling

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	ScheduleTickJobType   = "schedule_tick"
	scheduleTickPriority  = 10
	scheduleTickKeyPrefix = "schedule_tick"
)

type scheduleEngine interface {
	MaybeFire(ctx context.Context, schedule repo.TaskSchedule) error
}

type ScheduleTickWorker struct {
	pool   *pgxpool.Pool
	engine scheduleEngine
	logger *slog.Logger
}

func NewScheduleTickWorker(pool *pgxpool.Pool, engine scheduleEngine, logger *slog.Logger) *ScheduleTickWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &ScheduleTickWorker{
		pool:   pool,
		engine: engine,
		logger: logger,
	}
}

func (w *ScheduleTickWorker) Execute(ctx context.Context, _ jobqueue.Job) error {
	if w == nil || w.pool == nil || w.engine == nil {
		return fmt.Errorf("schedule tick worker is not configured")
	}

	schedules, err := w.loadEnabledSchedules(ctx)
	if err != nil {
		return err
	}

	for _, schedule := range schedules {
		if err := w.engine.MaybeFire(ctx, schedule); err != nil {
			w.logger.Error("schedule fire failed", "schedule_id", schedule.ID, "error", err)
		}
	}

	return nil
}

func (w *ScheduleTickWorker) loadEnabledSchedules(ctx context.Context) ([]repo.TaskSchedule, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT
			id,
			organization_id,
			project_id,
			flow_template_id,
			display_name,
			cron_expression,
			overlap_policy,
			max_duration_ms,
			is_enabled,
			last_fired_at,
			next_fire_at,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM task_schedule
		WHERE is_enabled = true
		  AND project_id IS NOT NULL
		ORDER BY created_at ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	schedules := make([]repo.TaskSchedule, 0)
	for rows.Next() {
		var schedule repo.TaskSchedule
		if err := rows.Scan(
			&schedule.ID,
			&schedule.OrganizationID,
			&schedule.ProjectID,
			&schedule.FlowTemplateID,
			&schedule.DisplayName,
			&schedule.CronExpression,
			&schedule.OverlapPolicy,
			&schedule.MaxDurationMS,
			&schedule.IsEnabled,
			&schedule.LastFiredAt,
			&schedule.NextFireAt,
			&schedule.CreatedByType,
			&schedule.CreatedByID,
			&schedule.CreatedAt,
			&schedule.UpdatedAt,
		); err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}

	return schedules, nil
}

type scheduleTickEnqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error)
}

type SchedulerHeartbeat struct {
	pool     *pgxpool.Pool
	enqueuer scheduleTickEnqueuer
	clock    clock.Clock
	logger   *slog.Logger
}

func NewSchedulerHeartbeat(pool *pgxpool.Pool, enqueuer scheduleTickEnqueuer, logger *slog.Logger, clk clock.Clock) *SchedulerHeartbeat {
	if clk == nil {
		clk = clock.Real{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &SchedulerHeartbeat{
		pool:     pool,
		enqueuer: enqueuer,
		clock:    clk,
		logger:   logger,
	}
}

func (h *SchedulerHeartbeat) Start(ctx context.Context) {
	if h == nil || h.pool == nil || h.enqueuer == nil {
		return
	}

	go h.run(ctx)
}

func (h *SchedulerHeartbeat) run(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	h.enqueueForCurrentMinute(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.enqueueForCurrentMinute(ctx)
		}
	}
}

func (h *SchedulerHeartbeat) enqueueForCurrentMinute(ctx context.Context) {
	now := h.clock.Now().UTC().Truncate(time.Minute)
	key := fmt.Sprintf("%s_%s", scheduleTickKeyPrefix, now.Format("20060102_1504"))

	if err := h.enqueueIfMissing(ctx, now, key); err != nil && ctx.Err() == nil {
		h.logger.Error("schedule tick enqueue failed", "idempotency_key", key, "error", err)
	}
}

func (h *SchedulerHeartbeat) enqueueIfMissing(ctx context.Context, minute time.Time, key string) error {
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var hasLock bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtext($1))`, "schedule_tick_global").Scan(&hasLock); err != nil {
		return err
	}
	if !hasLock {
		return nil
	}

	var pendingExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM job_queue
			WHERE job_type = $1
			  AND status IN ('pending', 'claimed')
			LIMIT 1
		)
	`, ScheduleTickJobType).Scan(&pendingExists); err != nil {
		return err
	}
	if pendingExists {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return nil
	}

	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM job_queue
			WHERE job_type = $1
			  AND payload->>'idempotency_key' = $2
			  AND created_at >= $3
			LIMIT 1
		)
	`, ScheduleTickJobType, key, minute).Scan(&exists); err != nil {
		return err
	}
	if exists {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return nil
	}

	if _, err := h.enqueuer.Enqueue(ctx, tx, ScheduleTickJobType, scheduleTickPriority, map[string]any{
		"idempotency_key": key,
		"source":          "scheduler_heartbeat",
	}, nil); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}
