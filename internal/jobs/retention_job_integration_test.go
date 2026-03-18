//go:build integration

package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestRetentionJobRunDeletesOldDomainEvents(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{Slug: "retention-org-" + uuid.NewString()[:8], DisplayName: "Retention Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO domain_event (organization_id, event_type, actor_type, actor_id, payload, created_at)
		VALUES ($1, 'old.event', 'system', NULL, '{}'::jsonb, now() - interval '91 days')
	`, org.ID); err != nil {
		t.Fatalf("insert old domain_event: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO domain_event (organization_id, event_type, actor_type, actor_id, payload, created_at)
		VALUES ($1, 'new.event', 'system', NULL, '{}'::jsonb, now() - interval '1 day')
	`, org.ID); err != nil {
		t.Fatalf("insert new domain_event: %v", err)
	}

	job, err := NewRetentionJob(RetentionJobOptions{Pool: pool, Now: time.Now})
	if err != nil {
		t.Fatalf("NewRetentionJob: %v", err)
	}
	if err := job.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var oldCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'old.event'
	`, org.ID).Scan(&oldCount); err != nil {
		t.Fatalf("count old domain_event rows: %v", err)
	}
	if oldCount != 0 {
		t.Fatalf("old domain_event rows = %d, want 0", oldCount)
	}

	var newCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'new.event'
	`, org.ID).Scan(&newCount); err != nil {
		t.Fatalf("count new domain_event rows: %v", err)
	}
	if newCount != 1 {
		t.Fatalf("new domain_event rows = %d, want 1", newCount)
	}
}

func TestRetentionJobRunDeletesOldTerminalJobQueueRows(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "retention-job-queue-" + uuid.NewString()[:8],
		DisplayName: "Retention Job Queue Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	_ = org

	if _, err := pool.Exec(ctx, `
		INSERT INTO job_queue (job_type, priority, payload, status, created_at, updated_at)
		VALUES
			('old.done', 10, '{}'::jsonb, 'done', now() - interval '91 days', now() - interval '91 days'),
			('old.dead', 10, '{}'::jsonb, 'dead_letter', now() - interval '91 days', now() - interval '91 days'),
			('new.done', 10, '{}'::jsonb, 'done', now() - interval '1 day', now() - interval '1 day'),
			('new.dead', 10, '{}'::jsonb, 'dead_letter', now() - interval '1 day', now() - interval '1 day'),
			('old.pending', 10, '{}'::jsonb, 'pending', now() - interval '91 days', now() - interval '91 days')
	`); err != nil {
		t.Fatalf("insert job_queue rows: %v", err)
	}

	job, err := NewRetentionJob(RetentionJobOptions{Pool: pool, Now: time.Now})
	if err != nil {
		t.Fatalf("NewRetentionJob: %v", err)
	}
	if err := job.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	assertCount := func(jobType string, want int) {
		t.Helper()
		var got int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM job_queue
			WHERE job_type = $1
		`, jobType).Scan(&got); err != nil {
			t.Fatalf("count job_queue rows for %s: %v", jobType, err)
		}
		if got != want {
			t.Fatalf("%s rows = %d, want %d", jobType, got, want)
		}
	}

	assertCount("old.done", 0)
	assertCount("old.dead", 0)
	assertCount("new.done", 1)
	assertCount("new.dead", 1)
	assertCount("old.pending", 1)
}
