//go:build integration

package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

	old := time.Now().UTC().Add(-31 * 24 * time.Hour)
	decayTarget := insertMemoryWithTimes(t, ctx, pool, memorySeed{
		OrganizationID: org.ID,
		MemoryType:     "episodic",
		Scope:          "org",
		Status:         "active",
		Content:        "decay target",
		Confidence:     0.6,
		CreatedAt:      old,
		UpdatedAt:      old,
	})
	archiveTarget := insertMemoryWithTimes(t, ctx, pool, memorySeed{
		OrganizationID: org.ID,
		MemoryType:     "episodic",
		Scope:          "org",
		Status:         "active",
		Content:        "archive target",
		Confidence:     0.15,
		CreatedAt:      old,
		UpdatedAt:      old,
	})

	run, err := runRepo.Create(ctx, repo.MemoryCompactionRun{
		OrganizationID: org.ID,
		RunType:        "sleep_reflection",
		Status:         "pending",
		ScopeContext:   `{"trigger":"test"}`,
	})
	if err != nil {
		t.Fatalf("create compaction run: %v", err)
	}

	reflector, err := NewSleepReflector(SleepReflectorOptions{
		Pool:         pool,
		Deduplicator: &staticCandidateDeduplicator{},
	})
	if err != nil {
		t.Fatalf("NewSleepReflector: %v", err)
	}
	if err := reflector.Run(ctx, org.ID, run.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	updatedDecay, err := memoryRepo.GetByID(ctx, decayTarget)
	if err != nil {
		t.Fatalf("get decay target: %v", err)
	}
	if updatedDecay.Confidence != 0.3 {
		t.Fatalf("decay target confidence = %.3f, want 0.300", updatedDecay.Confidence)
	}

	updatedArchive, err := memoryRepo.GetByID(ctx, archiveTarget)
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

func TestSleepReflectorRunDecayIsIdempotentWithinHalfLife(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedOrg(t, ctx, pool)
	memoryRepo := repo.NewMemoryRepo(pool)
	runRepo := repo.NewMemoryCompactionRunRepo(pool)

	old := time.Now().UTC().Add(-31 * 24 * time.Hour)
	memoryID := insertMemoryWithTimes(t, ctx, pool, memorySeed{
		OrganizationID: org.ID,
		MemoryType:     "episodic",
		Scope:          "org",
		Status:         "active",
		Content:        "idempotent decay target",
		Confidence:     0.6,
		CreatedAt:      old,
		UpdatedAt:      old,
	})

	reflector, err := NewSleepReflector(SleepReflectorOptions{
		Pool:         pool,
		Deduplicator: &staticCandidateDeduplicator{},
	})
	if err != nil {
		t.Fatalf("NewSleepReflector: %v", err)
	}

	firstRun, err := runRepo.Create(ctx, repo.MemoryCompactionRun{
		OrganizationID: org.ID,
		RunType:        "sleep_reflection",
		Status:         "pending",
		ScopeContext:   `{"trigger":"test-idempotency-1"}`,
	})
	if err != nil {
		t.Fatalf("create first run: %v", err)
	}
	if err := reflector.Run(ctx, org.ID, firstRun.ID); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	afterFirst, err := memoryRepo.GetByID(ctx, memoryID)
	if err != nil {
		t.Fatalf("get memory after first run: %v", err)
	}
	if afterFirst.Confidence != 0.3 {
		t.Fatalf("confidence after first run = %.3f, want 0.300", afterFirst.Confidence)
	}

	secondRun, err := runRepo.Create(ctx, repo.MemoryCompactionRun{
		OrganizationID: org.ID,
		RunType:        "sleep_reflection",
		Status:         "pending",
		ScopeContext:   `{"trigger":"test-idempotency-2"}`,
	})
	if err != nil {
		t.Fatalf("create second run: %v", err)
	}
	if err := reflector.Run(ctx, org.ID, secondRun.ID); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	afterSecond, err := memoryRepo.GetByID(ctx, memoryID)
	if err != nil {
		t.Fatalf("get memory after second run: %v", err)
	}
	if afterSecond.Confidence != 0.3 {
		t.Fatalf("confidence after second run = %.3f, want 0.300", afterSecond.Confidence)
	}
}

func TestSleepReflectorRunWithoutMemoriesCompletes(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedOrg(t, ctx, pool)
	runRepo := repo.NewMemoryCompactionRunRepo(pool)

	run, err := runRepo.Create(ctx, repo.MemoryCompactionRun{
		OrganizationID: org.ID,
		RunType:        "sleep_reflection",
		Status:         "pending",
		ScopeContext:   `{"trigger":"no-memories"}`,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	reflector, err := NewSleepReflector(SleepReflectorOptions{
		Pool:         pool,
		Deduplicator: &staticCandidateDeduplicator{},
	})
	if err != nil {
		t.Fatalf("NewSleepReflector: %v", err)
	}
	if err := reflector.Run(ctx, org.ID, run.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	finalRun, err := runRepo.GetByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if finalRun.Status != "completed" {
		t.Fatalf("status = %q, want completed", finalRun.Status)
	}
	if finalRun.ErrorMessage != nil {
		t.Fatalf("error_message = %v, want nil", finalRun.ErrorMessage)
	}
}

func TestSleepReflectorRunFailureSetsRunErrorMessage(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedOrg(t, ctx, pool)
	runRepo := repo.NewMemoryCompactionRunRepo(pool)

	old := time.Now().UTC().Add(-2 * 24 * time.Hour)
	insertMemoryWithTimes(t, ctx, pool, memorySeed{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Status:         "candidate",
		Content:        "candidate for failing dedup",
		Confidence:     0.7,
		CreatedAt:      old,
		UpdatedAt:      old,
	})

	run, err := runRepo.Create(ctx, repo.MemoryCompactionRun{
		OrganizationID: org.ID,
		RunType:        "sleep_reflection",
		Status:         "pending",
		ScopeContext:   `{"trigger":"failure-path"}`,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	reflector, err := NewSleepReflector(SleepReflectorOptions{
		Pool:         pool,
		Deduplicator: failingCandidateDeduplicator{err: errors.New("deduplicator exploded")},
	})
	if err != nil {
		t.Fatalf("NewSleepReflector: %v", err)
	}
	if err := reflector.Run(ctx, org.ID, run.ID); err == nil {
		t.Fatal("Run error = nil, want failure")
	}

	finalRun, err := runRepo.GetByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if finalRun.Status != "failed" {
		t.Fatalf("status = %q, want failed", finalRun.Status)
	}
	if finalRun.ErrorMessage == nil || strings.TrimSpace(*finalRun.ErrorMessage) == "" {
		t.Fatalf("error_message = %v, want non-empty", finalRun.ErrorMessage)
	}
}

func TestSleepReflectorRunCandidateDedupProcessesBatchAndUpdatesRunCounters(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedOrg(t, ctx, pool)
	memoryRepo := repo.NewMemoryRepo(pool)
	runRepo := repo.NewMemoryCompactionRunRepo(pool)

	strong, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "strong duplicate source",
		ContentHash:    uuid.NewString(),
		Confidence:     0.6,
		UtilityScore:   0.7,
		Status:         "candidate",
		TrustTier:      0.8,
	})
	if err != nil {
		t.Fatalf("create strong candidate: %v", err)
	}
	weak, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "weak duplicate source",
		ContentHash:    uuid.NewString(),
		Confidence:     0.6,
		UtilityScore:   0.6,
		Status:         "candidate",
		TrustTier:      0.8,
	})
	if err != nil {
		t.Fatalf("create weak candidate: %v", err)
	}
	unique, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "unique candidate source",
		ContentHash:    uuid.NewString(),
		Confidence:     0.6,
		UtilityScore:   0.6,
		Status:         "candidate",
		TrustTier:      0.8,
	})
	if err != nil {
		t.Fatalf("create unique candidate: %v", err)
	}

	old := time.Now().UTC().Add(-2 * 24 * time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE memory SET created_at = $2 WHERE id = ANY($1::uuid[])`, []uuid.UUID{strong.ID, weak.ID, unique.ID}, old); err != nil {
		t.Fatalf("age candidate memories: %v", err)
	}

	boostedScore := 72
	deduplicator := &staticCandidateDeduplicator{
		reviews: []CandidateReview{
			{MemoryID: weak.ID, Action: CandidateReviewArchive},
			{MemoryID: unique.ID, Action: CandidateReviewKeep, ExtractionScore: &boostedScore},
		},
	}
	reflector, err := NewSleepReflector(SleepReflectorOptions{
		Pool:         pool,
		Deduplicator: deduplicator,
	})
	if err != nil {
		t.Fatalf("NewSleepReflector: %v", err)
	}

	run, err := runRepo.Create(ctx, repo.MemoryCompactionRun{
		OrganizationID: org.ID,
		RunType:        "sleep_reflection",
		Status:         "pending",
		ScopeContext:   `{"trigger":"candidate-test"}`,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := reflector.Run(ctx, org.ID, run.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if deduplicator.calls != 1 {
		t.Fatalf("deduplicator calls = %d, want 1", deduplicator.calls)
	}
	if len(deduplicator.batches) != 1 || len(deduplicator.batches[0]) != 3 {
		t.Fatalf("deduplicator batch size = %d, want 3", len(deduplicator.batches[0]))
	}

	weakFinal, err := memoryRepo.GetByID(ctx, weak.ID)
	if err != nil {
		t.Fatalf("get weak candidate: %v", err)
	}
	if weakFinal.Status != "archived" {
		t.Fatalf("weak candidate status = %s, want archived", weakFinal.Status)
	}

	uniqueFinal, err := memoryRepo.GetByID(ctx, unique.ID)
	if err != nil {
		t.Fatalf("get unique candidate: %v", err)
	}
	if uniqueFinal.ExtractionScore == nil || *uniqueFinal.ExtractionScore != boostedScore {
		t.Fatalf("unique candidate extraction_score = %v, want %d", uniqueFinal.ExtractionScore, boostedScore)
	}

	runFinal, err := runRepo.GetByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("get compaction run: %v", err)
	}
	if runFinal.MemoriesExamined < 3 {
		t.Fatalf("memories_examined = %d, want at least 3", runFinal.MemoriesExamined)
	}
	if runFinal.MemoriesArchived < 1 {
		t.Fatalf("memories_archived = %d, want at least 1", runFinal.MemoriesArchived)
	}
	if runFinal.MemoriesUpdated < 1 {
		t.Fatalf("memories_updated = %d, want at least 1", runFinal.MemoriesUpdated)
	}
}

type staticCandidateDeduplicator struct {
	reviews []CandidateReview
	calls   int
	batches [][]CandidateMemory
}

func (d *staticCandidateDeduplicator) ReviewCandidateBatch(_ context.Context, _ uuid.UUID, batch []CandidateMemory) ([]CandidateReview, error) {
	d.calls++
	copied := append([]CandidateMemory(nil), batch...)
	d.batches = append(d.batches, copied)
	return append([]CandidateReview(nil), d.reviews...), nil
}

type failingCandidateDeduplicator struct {
	err error
}

func (d failingCandidateDeduplicator) ReviewCandidateBatch(context.Context, uuid.UUID, []CandidateMemory) ([]CandidateReview, error) {
	if d.err == nil {
		return nil, errors.New("failed review")
	}
	return nil, d.err
}

type memorySeed struct {
	OrganizationID uuid.UUID
	MemoryType     string
	Scope          string
	Status         string
	Content        string
	Confidence     float64
	Extraction     *int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func insertMemoryWithTimes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed memorySeed) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO memory (
			organization_id,
			memory_type,
			scope,
			content,
			content_hash,
			confidence,
			utility_score,
			extraction_score,
			status,
			trust_tier,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 0.5, $7, $8, 0.8, $9, $10)
		RETURNING id
	`,
		seed.OrganizationID,
		seed.MemoryType,
		seed.Scope,
		seed.Content,
		fmt.Sprintf("%s-%s", seed.Content, uuid.NewString()),
		seed.Confidence,
		seed.Extraction,
		seed.Status,
		seed.CreatedAt,
		seed.UpdatedAt,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert memory seed: %v", err)
	}
	return id
}

func seedOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool) repo.Organization {
	t.Helper()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{Slug: "compaction-org-" + uuid.NewString()[:8], DisplayName: "Compaction Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return org
}
