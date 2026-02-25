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
