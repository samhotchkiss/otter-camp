//go:build integration

package jobs

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestTraceSpanDefaultPartitionDroppedByMigration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_class
			WHERE relname = 'trace_span_p_default'
		)
	`).Scan(&exists); err != nil {
		t.Fatalf("default partition existence query: %v", err)
	}
	if exists {
		t.Fatal("trace_span_p_default should be dropped by migration")
	}
}

func TestTraceSpanPartitionJobRunCreatesPartitionTable(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	now := time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)
	job, err := NewTraceSpanPartitionJob(TraceSpanPartitionJobOptions{
		Pool: pool,
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewTraceSpanPartitionJob: %v", err)
	}

	if err := job.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	start := now.UTC().Truncate(24 * time.Hour).Add(7 * 24 * time.Hour)
	partitionName := fmt.Sprintf("trace_span_p_%s", start.Format("20060102"))

	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_inherits i
			JOIN pg_class child ON child.oid = i.inhrelid
			JOIN pg_class parent ON parent.oid = i.inhparent
			WHERE child.relname = $1
			  AND parent.relname = 'trace_span'
		)
	`, partitionName).Scan(&exists); err != nil {
		t.Fatalf("partition existence query: %v", err)
	}
	if !exists {
		t.Fatalf("partition %q was not created", partitionName)
	}

	createdAt := start.Add(2 * time.Hour)
	traceID := uuid.New()
	var spanID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO trace_span (
			trace_id,
			organization_id,
			span_name,
			service,
			kind,
			status,
			attributes,
			events,
			started_at,
			created_at
		)
		VALUES ($1, NULL, 'partition.validation', 'ottercamp', 'internal', 'ok', '{}'::jsonb, '[]'::jsonb, $2, $2)
		RETURNING id
	`, traceID, createdAt).Scan(&spanID); err != nil {
		t.Fatalf("insert trace_span row: %v", err)
	}

	var actualPartition string
	if err := pool.QueryRow(ctx, `
		SELECT tableoid::regclass::text
		FROM trace_span
		WHERE id = $1
		  AND created_at = $2
	`, spanID, createdAt).Scan(&actualPartition); err != nil {
		t.Fatalf("query inserted span partition: %v", err)
	}
	if actualPartition != partitionName {
		t.Fatalf("inserted span partition = %q, want %q", actualPartition, partitionName)
	}
}
