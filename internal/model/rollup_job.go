package model

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	DailyRollupJobType      = "model_usage_rollup_daily"
	dailyRollupDateLayout   = "2006-01-02"
	dailyRollupEnqueueDelay = 24 * time.Hour
	dailyRollupPriority     = 100
)

type rollupAggregationSource interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type modelUsageRollupWriter interface {
	Upsert(ctx context.Context, rollup repo.ModelUsageRollup) (repo.ModelUsageRollup, error)
}

type domainEventWriter interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
}

type DailyRollupJobOptions struct {
	Pool    *pgxpool.Pool
	Rollups modelUsageRollupWriter
	Events  domainEventWriter
	Logger  *slog.Logger
	Now     func() time.Time
}

type DailyRollupJob struct {
	source  rollupAggregationSource
	rollups modelUsageRollupWriter
	events  domainEventWriter
	logger  *slog.Logger
	now     func() time.Time
}

type dailyRollupPayload struct {
	Date string `json:"date"`
}

type dailyRollupEnqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error)
}

type DailyRollupTicker struct {
	enqueuer dailyRollupEnqueuer
	logger   *slog.Logger
	now      func() time.Time
}

func NewDailyRollupJob(opts DailyRollupJobOptions) (*DailyRollupJob, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("daily rollup job pool is required")
	}
	if opts.Rollups == nil {
		opts.Rollups = repo.NewModelUsageRollupRepo(opts.Pool)
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	return &DailyRollupJob{
		source:  opts.Pool,
		rollups: opts.Rollups,
		events:  opts.Events,
		logger:  opts.Logger,
		now:     opts.Now,
	}, nil
}

func (j *DailyRollupJob) RegisterJobs(registrar interface {
	Register(jobType string, handler jobqueue.JobHandler)
}) {
	if j == nil || registrar == nil {
		return
	}
	registrar.Register(DailyRollupJobType, j.Run)
}

func (j *DailyRollupJob) Run(ctx context.Context, job jobqueue.Job) error {
	if j == nil || j.source == nil || j.rollups == nil {
		return fmt.Errorf("daily rollup job is not configured")
	}

	targetDate := previousUTCDayStart(j.now())
	if len(job.Payload) > 0 {
		var payload dailyRollupPayload
		if err := json.Unmarshal(job.Payload, &payload); err == nil {
			if parsed, parseErr := time.Parse(dailyRollupDateLayout, strings.TrimSpace(payload.Date)); parseErr == nil {
				targetDate = parsed.UTC()
			}
		}
	}
	nextDate := targetDate.Add(24 * time.Hour)

	rowCountByOrg := map[uuid.UUID]int{}
	for _, dimension := range []struct {
		rollupType string
		column     string
		nonNull    bool
	}{
		{rollupType: "provider_connection", column: "provider_connection_id", nonNull: true},
		{rollupType: "model_provider", column: "model_provider_id", nonNull: true},
		{rollupType: "agent", column: "agent_id", nonNull: true},
		{rollupType: "project", column: "project_id", nonNull: true},
	} {
		count, err := j.rollupDimension(ctx, targetDate, nextDate, dimension.rollupType, dimension.column, dimension.nonNull)
		if err != nil {
			return err
		}
		for orgID, orgCount := range count {
			rowCountByOrg[orgID] += orgCount
		}
	}

	if j.events != nil {
		for orgID, rowCount := range rowCountByOrg {
			payload, _ := json.Marshal(map[string]any{
				"date":      targetDate.Format(dailyRollupDateLayout),
				"org_id":    orgID.String(),
				"row_count": rowCount,
			})
			if err := j.events.Publish(ctx, nil, eventbus.DomainEvent{
				OrganizationID: orgID,
				EventType:      "model.usage_rollup.completed",
				ActorType:      "system",
				Payload:        payload,
			}); err != nil {
				return err
			}
		}
	}

	return nil
}

func (j *DailyRollupJob) rollupDimension(ctx context.Context, periodStart, periodEnd time.Time, rollupType, column string, nonNull bool) (map[uuid.UUID]int, error) {
	nullClause := ""
	if nonNull {
		nullClause = "AND " + column + " IS NOT NULL"
	}

	query := fmt.Sprintf(`
		SELECT
			organization_id,
			%[1]s,
			COUNT(*)::integer,
			COALESCE(SUM(input_tokens), 0)::bigint,
			COALESCE(SUM(output_tokens), 0)::bigint
		FROM model_invocation
		WHERE status = 'completed'
		  AND created_at >= $1
		  AND created_at < $2
		  %[2]s
		GROUP BY organization_id, %[1]s
	`, column, nullClause)

	rows, err := j.source.Query(ctx, query, periodStart, periodEnd)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rowCountByOrg := map[uuid.UUID]int{}
	for rows.Next() {
		var (
			orgID       uuid.UUID
			rollupID    *uuid.UUID
			invocations int
			input       int64
			output      int64
		)
		if err := rows.Scan(&orgID, &rollupID, &invocations, &input, &output); err != nil {
			return nil, err
		}
		if _, err := j.rollups.Upsert(ctx, repo.ModelUsageRollup{
			OrganizationID:       orgID,
			RollupDate:           periodStart,
			RollupType:           rollupType,
			RollupID:             rollupID,
			ModelName:            nil,
			InvocationPurpose:    nil,
			TotalInvocations:     invocations,
			TotalInputTokens:     input,
			TotalOutputTokens:    output,
			TotalCacheReadTokens: 0,
			TotalLatencyMS:       0,
			TotalCostMicrocents:  0,
		}); err != nil {
			return nil, err
		}
		rowCountByOrg[orgID]++
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return rowCountByOrg, nil
}

func previousUTCDayStart(now time.Time) time.Time {
	utcNow := now.UTC()
	todayUTC := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day(), 0, 0, 0, 0, time.UTC)
	return todayUTC.Add(-24 * time.Hour)
}

func NewDailyRollupTicker(enqueuer dailyRollupEnqueuer, logger *slog.Logger, now func() time.Time) *DailyRollupTicker {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &DailyRollupTicker{enqueuer: enqueuer, logger: logger, now: now}
}

func (t *DailyRollupTicker) Start(ctx context.Context) {
	if t == nil || t.enqueuer == nil {
		return
	}

	enqueue := func() {
		payload := dailyRollupPayload{Date: previousUTCDayStart(t.now()).Format(dailyRollupDateLayout)}
		if _, err := t.enqueuer.Enqueue(ctx, nil, DailyRollupJobType, dailyRollupPriority, payload, nil); err != nil {
			t.logger.Warn("enqueue daily model usage rollup job failed", "error", err)
		}
	}

	enqueue()
	go func() {
		ticker := time.NewTicker(dailyRollupEnqueueDelay)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				enqueue()
			}
		}
	}()
}
