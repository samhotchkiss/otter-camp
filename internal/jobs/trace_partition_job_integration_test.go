//go:build integration

package jobs

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestTraceSpanPartitionJobRunCreatesPartitionTable(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	now := time.Date(2030, 2, 25, 12, 0, 0, 0, time.UTC)
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
}
