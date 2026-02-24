//go:build integration

package compaction

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestSleepReflectorRunAppliesDecayAndArchivesLowConfidence(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := seedOrg(t, ctx, pool)
	memoryRepo := repo.NewMemoryRepo(pool)
	runRepo := repo.NewMemoryCompactionRunRepo(pool)

	decayTarget, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		MemoryType:     "episodic",
		Scope:          "org",
		Content:        "decay target",
		ContentHash:    uuid.NewString(),
		Confidence:     0.6,
		UtilityScore:   0.5,
		Status:         "active",
		TrustTier:      0.8,
	})
	if err != nil {
		t.Fatalf("create decay target: %v", err)
	}
	archiveTarget, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		MemoryType:     "episodic",
		Scope:          "org",
		Content:        "archive target",
		ContentHash:    uuid.NewString(),
		Confidence:     0.15,
		UtilityScore:   0.5,
		Status:         "active",
		TrustTier:      0.8,
	})
	if err != nil {
		t.Fatalf("create archive target: %v", err)
	}

	old := time.Now().UTC().Add(-31 * 24 * time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE memory SET created_at = $2 WHERE id = ANY($1::uuid[])`, []uuid.UUID{decayTarget.ID, archiveTarget.ID}, old); err != nil {
		t.Fatalf("age memories: %v", err)
	}

	run, err := runRepo.Create(ctx, repo.MemoryCompactionRun{
		OrganizationID: org.ID,
		RunType:        "sleep_reflection",
		Status:         "pending",
		ScopeContext:   `{"trigger":"test"}`,
	})
	if err != nil {
		t.Fatalf("create compaction run: %v", err)
	}

	reflector, err := NewSleepReflector(SleepReflectorOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewSleepReflector: %v", err)
	}
	if err := reflector.Run(ctx, org.ID, run.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	updatedDecay, err := memoryRepo.GetByID(ctx, decayTarget.ID)
	if err != nil {
		t.Fatalf("get decay target: %v", err)
	}
	if updatedDecay.Confidence != 0.3 {
		t.Fatalf("decay target confidence = %.3f, want 0.300", updatedDecay.Confidence)
	}

	updatedArchive, err := memoryRepo.GetByID(ctx, archiveTarget.ID)
	if err != nil {
		t.Fatalf("get archive target: %v", err)
	}
	if updatedArchive.Status != "archived" {
		t.Fatalf("archive target status = %s, want archived", updatedArchive.Status)
	}
	if updatedArchive.ArchivedAt == nil {
		t.Fatal("archive target archived_at = nil, want non-nil")
	}
}

func seedOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool) repo.Organization {
	t.Helper()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{Slug: "compaction-org-" + uuid.NewString()[:8], DisplayName: "Compaction Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return org
}
