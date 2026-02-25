package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
)

const TraceSpanPartitionCreateJobType = "trace_span_partition_create"

type TraceSpanPartitionJob struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

type TraceSpanPartitionJobOptions struct {
	Pool *pgxpool.Pool
	Now  func() time.Time
}

func NewTraceSpanPartitionJob(opts TraceSpanPartitionJobOptions) (*TraceSpanPartitionJob, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("trace span partition job requires database pool")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &TraceSpanPartitionJob{pool: opts.Pool, now: opts.Now}, nil
}

func (j *TraceSpanPartitionJob) RegisterJobs(registrar interface {
	Register(jobType string, handler jobqueue.JobHandler)
}) {
	if j == nil || registrar == nil {
		return
	}
	registrar.Register(TraceSpanPartitionCreateJobType, func(ctx context.Context, _ jobqueue.Job) error {
		return j.Run(ctx)
	})
}

func (j *TraceSpanPartitionJob) Run(ctx context.Context) error {
	start := j.now().UTC().Truncate(24 * time.Hour).Add(7 * 24 * time.Hour)
	end := start.Add(24 * time.Hour)
	partitionName := fmt.Sprintf("trace_span_p_%s", start.Format("20060102"))

	if _, err := j.pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s PARTITION OF trace_span
		FOR VALUES FROM ($1) TO ($2)
	`, partitionName), start, end); err != nil {
		return fmt.Errorf("create trace span partition: %w", err)
	}

	if _, err := j.pool.Exec(ctx, fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS %s_trace_id_idx ON %s (trace_id)
	`, partitionName, partitionName)); err != nil {
		return fmt.Errorf("create trace span trace_id index: %w", err)
	}
	if _, err := j.pool.Exec(ctx, fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS %s_org_created_idx ON %s (organization_id, created_at)
	`, partitionName, partitionName)); err != nil {
		return fmt.Errorf("create trace span organization/created index: %w", err)
	}

	return nil
}
