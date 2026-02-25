//go:build integration

package observability

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/jobs"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestTraceSpanInsertAppendOnlyAndRetention(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{Slug: "trace-org-" + uuid.NewString()[:8], DisplayName: "Trace Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	service, err := NewTraceSpanService(pool)
	if err != nil {
		t.Fatalf("NewTraceSpanService: %v", err)
	}

	inserted, err := service.Create(ctx, repo.TraceSpan{
		TraceID:        uuid.New(),
		OrganizationID: &org.ID,
		SpanName:       "http.request",
		Kind:           "server",
		Status:         "ok",
		StartedAt:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create trace span: %v", err)
	}

	var partitionName string
	if err := pool.QueryRow(ctx, `SELECT tableoid::regclass::text FROM trace_span WHERE id = $1`, inserted.ID).Scan(&partitionName); err != nil {
		t.Fatalf("query trace span partition: %v", err)
	}
	if !strings.HasPrefix(partitionName, "trace_span_p_") {
		t.Fatalf("partition name = %q, want prefix trace_span_p_", partitionName)
	}

	if err := service.Update(ctx, inserted.ID, map[string]any{"status": "error"}); err != ErrTraceSpanAppendOnly {
		t.Fatalf("Update error = %v, want %v", err, ErrTraceSpanAppendOnly)
	}

	oldStart := time.Now().UTC().Add(-14 * 24 * time.Hour).Truncate(24 * time.Hour)
	oldEnd := oldStart.Add(7 * 24 * time.Hour)
	partitionDDL := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS trace_span_p_old PARTITION OF trace_span FOR VALUES FROM ('%s') TO ('%s')",
		oldStart.Format(time.RFC3339),
		oldEnd.Format(time.RFC3339),
	)
	if _, err := pool.Exec(ctx, partitionDDL); err != nil {
		t.Fatalf("create old trace partition: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trace_span (trace_id, organization_id, span_name, service, kind, status, attributes, events, started_at, created_at)
		VALUES ($1, $2, 'old.span', 'ottercamp', 'internal', 'ok', '{}'::jsonb, '[]'::jsonb, now() - interval '8 days', now() - interval '8 days')
	`, uuid.New(), org.ID); err != nil {
		t.Fatalf("insert old trace span: %v", err)
	}

	retentionJob, err := jobs.NewRetentionJob(jobs.RetentionJobOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewRetentionJob: %v", err)
	}
	if err := retentionJob.Run(ctx); err != nil {
		t.Fatalf("retention run: %v", err)
	}

	var oldCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM trace_span
		WHERE span_name = 'old.span'
	`).Scan(&oldCount); err != nil {
		t.Fatalf("count old trace spans: %v", err)
	}
	if oldCount != 0 {
		t.Fatalf("old trace spans = %d, want 0", oldCount)
	}
}
