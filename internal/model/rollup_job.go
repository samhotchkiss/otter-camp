package model

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
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
	rollupColumn := "mi." + column
	nullClause := ""
	if nonNull {
		nullClause = "AND " + rollupColumn + " IS NOT NULL"
	}

	query := fmt.Sprintf(`
		SELECT
			mi.organization_id,
			%[1]s,
			COALESCE(mi.model_name, ''),
			COALESCE(mp.metadata, '{}'::jsonb),
			COUNT(*)::integer,
			COALESCE(SUM(mi.input_tokens), 0)::bigint,
			COALESCE(SUM(mi.output_tokens), 0)::bigint
		FROM model_invocation mi
		LEFT JOIN model_provider mp ON mp.id = mi.model_provider_id
		WHERE mi.status = 'completed'
		  AND mi.created_at >= $1
		  AND mi.created_at < $2
		  %[2]s
		GROUP BY mi.organization_id, %[1]s, mi.model_name, mp.metadata
	`, rollupColumn, nullClause)

	rows, err := j.source.Query(ctx, query, periodStart, periodEnd)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type rollupAggregate struct {
		orgID       uuid.UUID
		rollupID    *uuid.UUID
		invocations int
		input       int64
		output      int64
		cost        int64
	}

	aggregates := make(map[string]rollupAggregate)
	rollupKey := func(orgID uuid.UUID, rollupID *uuid.UUID) string {
		if rollupID == nil {
			return orgID.String() + "|nil"
		}
		return orgID.String() + "|" + rollupID.String()
	}

	rowCountByOrg := map[uuid.UUID]int{}
	for rows.Next() {
		var (
			orgID       uuid.UUID
			rollupID    *uuid.UUID
			modelName   string
			metadataRaw json.RawMessage
			invocations int
			input       int64
			output      int64
		)
		if err := rows.Scan(&orgID, &rollupID, &modelName, &metadataRaw, &invocations, &input, &output); err != nil {
			return nil, err
		}
		inputCostPer1K, outputCostPer1K := resolveRollupModelCosts(modelName, metadataRaw)
		key := rollupKey(orgID, rollupID)
		aggregate := aggregates[key]
		aggregate.orgID = orgID
		aggregate.rollupID = cloneUUIDPointer(rollupID)
		aggregate.invocations += invocations
		aggregate.input += input
		aggregate.output += output
		aggregate.cost += estimateCostMicrocents(input, output, inputCostPer1K, outputCostPer1K)
		aggregates[key] = aggregate
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}

	for _, aggregate := range aggregates {
		if _, err := j.rollups.Upsert(ctx, repo.ModelUsageRollup{
			OrganizationID:       aggregate.orgID,
			RollupDate:           periodStart,
			RollupType:           rollupType,
			RollupID:             aggregate.rollupID,
			ModelName:            nil,
			InvocationPurpose:    nil,
			TotalInvocations:     aggregate.invocations,
			TotalInputTokens:     aggregate.input,
			TotalOutputTokens:    aggregate.output,
			TotalCacheReadTokens: 0,
			TotalLatencyMS:       0,
			TotalCostMicrocents:  aggregate.cost,
		}); err != nil {
			return nil, err
		}
		rowCountByOrg[aggregate.orgID]++
	}

	return rowCountByOrg, nil
}

func resolveRollupModelCosts(modelName string, providerMetadata json.RawMessage) (float64, float64) {
	input, output := parseProviderCosts(providerMetadata)
	fallbackInput, fallbackOutput := defaultModelCostsPer1K(modelName)
	if input == 0 {
		input = fallbackInput
	}
	if output == 0 {
		output = fallbackOutput
	}
	return input, output
}

func defaultModelCostsPer1K(modelName string) (float64, float64) {
	normalized := strings.ToLower(strings.TrimSpace(modelName))
	switch {
	case strings.Contains(normalized, "claude-opus"):
		return 1.5, 7.5
	case strings.Contains(normalized, "claude-sonnet"):
		return 0.3, 1.5
	case strings.Contains(normalized, "claude-haiku"):
		return 0.08, 0.4
	case strings.Contains(normalized, "gpt-4o"):
		return 0.5, 1.5
	case strings.Contains(normalized, "gpt-4.1"):
		return 0.2, 0.8
	default:
		return 0, 0
	}
}

func estimateCostMicrocents(inputTokens, outputTokens int64, inputCostPer1K, outputCostPer1K float64) int64 {
	if inputCostPer1K <= 0 && outputCostPer1K <= 0 {
		return 0
	}
	costCents := float64(inputTokens)*inputCostPer1K/1000.0 + float64(outputTokens)*outputCostPer1K/1000.0
	if costCents <= 0 {
		return 0
	}
	return int64(math.Round(costCents * 1_000_000))
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
