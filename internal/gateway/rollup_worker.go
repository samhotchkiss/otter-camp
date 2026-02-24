package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type RollupWorker struct {
	pool    *pgxpool.Pool
	rollups *repo.ModelUsageRollupRepo
	logger  *slog.Logger
}

func NewRollupWorker(pool *pgxpool.Pool, rollups *repo.ModelUsageRollupRepo, logger *slog.Logger) *RollupWorker {
	if rollups == nil && pool != nil {
		rollups = repo.NewModelUsageRollupRepo(pool)
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &RollupWorker{
		pool:    pool,
		rollups: rollups,
		logger:  logger,
	}
}

func (w *RollupWorker) RegisterJobs(registrar interface {
	Register(jobType string, handler jobqueue.JobHandler)
}) {
	if w == nil || registrar == nil {
		return
	}

	registrar.Register(rollupUpdateJobType, func(ctx context.Context, job jobqueue.Job) error {
		var payload RollupUpdatePayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("decode rollup_update payload: %w", err)
		}
		rollupDate, err := time.Parse(defaultRollupQueueDate, payload.RollupDate)
		if err != nil {
			return fmt.Errorf("parse rollup_date: %w", err)
		}
		return w.RunRollupForDate(ctx, payload.OrgID, rollupDate)
	})
}

func (w *RollupWorker) RunRollupForDate(ctx context.Context, orgID uuid.UUID, date time.Time) error {
	if w == nil || w.pool == nil || w.rollups == nil {
		return fmt.Errorf("rollup worker is not configured")
	}
	if orgID == uuid.Nil {
		return fmt.Errorf("organization id is required")
	}

	rollupDate := time.Date(date.UTC().Year(), date.UTC().Month(), date.UTC().Day(), 0, 0, 0, 0, time.UTC)
	nextDate := rollupDate.Add(24 * time.Hour)

	dimensions := []struct {
		rollupType string
		column     string
		nonNull    bool
	}{
		{rollupType: "provider_connection", column: "provider_connection_id", nonNull: true},
		{rollupType: "model_provider", column: "model_provider_id", nonNull: false},
		{rollupType: "agent", column: "agent_id", nonNull: true},
		{rollupType: "project", column: "project_id", nonNull: true},
	}

	for _, dim := range dimensions {
		if err := w.rollupDimension(ctx, orgID, rollupDate, nextDate, dim.rollupType, dim.column, dim.nonNull); err != nil {
			return err
		}
	}
	if err := w.rollupOrganizationTotal(ctx, orgID, rollupDate, nextDate); err != nil {
		return err
	}
	return nil
}

func (w *RollupWorker) rollupDimension(
	ctx context.Context,
	orgID uuid.UUID,
	rollupDate time.Time,
	nextDate time.Time,
	rollupType string,
	column string,
	nonNull bool,
) error {
	nullClause := ""
	if nonNull {
		nullClause = fmt.Sprintf("AND %s IS NOT NULL", column)
	}

	query := fmt.Sprintf(`
		SELECT
			%[1]s,
			model_name,
			invocation_purpose,
			COUNT(*)::integer,
			COALESCE(SUM(input_tokens), 0)::bigint,
			COALESCE(SUM(output_tokens), 0)::bigint,
			COALESCE(SUM(cache_read_tokens), 0)::bigint,
			COALESCE(SUM(latency_ms), 0)::bigint
		FROM model_invocation
		WHERE organization_id = $1
		  AND status = 'completed'
		  AND created_at >= $2
		  AND created_at < $3
		  %[2]s
		GROUP BY %[1]s, model_name, invocation_purpose
	`, column, nullClause)

	rows, err := w.pool.Query(ctx, query, orgID, rollupDate, nextDate)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			rollupID       *uuid.UUID
			modelName      string
			purpose        string
			invocations    int
			inputTokens    int64
			outputTokens   int64
			cacheRead      int64
			latencyTotalMS int64
		)
		if err := rows.Scan(
			&rollupID,
			&modelName,
			&purpose,
			&invocations,
			&inputTokens,
			&outputTokens,
			&cacheRead,
			&latencyTotalMS,
		); err != nil {
			return err
		}

		modelName = stringsTrim(modelName)
		purpose = stringsTrim(purpose)
		if _, err := w.rollups.Upsert(ctx, repo.ModelUsageRollup{
			OrganizationID:       orgID,
			RollupDate:           rollupDate,
			RollupType:           rollupType,
			RollupID:             rollupID,
			ModelName:            ptrString(modelName),
			InvocationPurpose:    ptrString(purpose),
			TotalInvocations:     invocations,
			TotalInputTokens:     inputTokens,
			TotalOutputTokens:    outputTokens,
			TotalCacheReadTokens: cacheRead,
			TotalLatencyMS:       latencyTotalMS,
			// Pricing config is optional and not required for budget enforcement.
			TotalCostMicrocents: 0,
		}); err != nil {
			return err
		}
	}

	if rows.Err() != nil {
		return rows.Err()
	}
	return nil
}

func (w *RollupWorker) rollupOrganizationTotal(ctx context.Context, orgID uuid.UUID, rollupDate, nextDate time.Time) error {
	var (
		invocations    int
		inputTokens    int64
		outputTokens   int64
		cacheRead      int64
		latencyTotalMS int64
	)

	if err := w.pool.QueryRow(ctx, `
		SELECT
			COUNT(*)::integer,
			COALESCE(SUM(input_tokens), 0)::bigint,
			COALESCE(SUM(output_tokens), 0)::bigint,
			COALESCE(SUM(cache_read_tokens), 0)::bigint,
			COALESCE(SUM(latency_ms), 0)::bigint
		FROM model_invocation
		WHERE organization_id = $1
		  AND status = 'completed'
		  AND created_at >= $2
		  AND created_at < $3
	`, orgID, rollupDate, nextDate).Scan(
		&invocations,
		&inputTokens,
		&outputTokens,
		&cacheRead,
		&latencyTotalMS,
	); err != nil {
		return err
	}

	if _, err := w.rollups.Upsert(ctx, repo.ModelUsageRollup{
		OrganizationID:       orgID,
		RollupDate:           rollupDate,
		RollupType:           "model_provider",
		RollupID:             nil,
		ModelName:            nil,
		InvocationPurpose:    nil,
		TotalInvocations:     invocations,
		TotalInputTokens:     inputTokens,
		TotalOutputTokens:    outputTokens,
		TotalCacheReadTokens: cacheRead,
		TotalLatencyMS:       latencyTotalMS,
		TotalCostMicrocents:  0,
	}); err != nil {
		return err
	}
	return nil
}

func ptrString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringsTrim(value string) string {
	return strings.TrimSpace(value)
}
