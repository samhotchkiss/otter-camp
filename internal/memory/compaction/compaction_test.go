package compaction

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCalculateDecayHalfLife(t *testing.T) {
	halfLife := 30 * 24 * time.Hour

	cases := []struct {
		name    string
		elapsed time.Duration
		want    float64
	}{
		{name: "31 days", elapsed: 31 * 24 * time.Hour, want: 0.3},
		{name: "62 days", elapsed: 62 * 24 * time.Hour, want: 0.15},
		{name: "93 days", elapsed: 93 * 24 * time.Hour, want: 0.075},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CalculateDecay(0.6, tc.elapsed, halfLife)
			if math.Abs(got-tc.want) > 0.000001 {
				t.Fatalf("CalculateDecay = %f, want %f", got, tc.want)
			}
		})
	}
}

func TestScopePromotionAction(t *testing.T) {
	cases := []struct {
		memoryType      string
		entityTaskCount int
		want            string
	}{
		{memoryType: "episodic", entityTaskCount: 0, want: ScopeActionArchive},
		{memoryType: "semantic", entityTaskCount: 0, want: ScopeActionPromoteProject},
		{memoryType: "procedural", entityTaskCount: 0, want: ScopeActionPromoteProject},
		{memoryType: "preference", entityTaskCount: 0, want: ScopeActionPromoteProject},
		{memoryType: "entity_profile", entityTaskCount: 2, want: ScopeActionArchive},
		{memoryType: "entity_profile", entityTaskCount: 3, want: ScopeActionPromoteProject},
		{memoryType: "unknown", entityTaskCount: 10, want: ScopeActionArchive},
	}

	for _, tc := range cases {
		t.Run(tc.memoryType, func(t *testing.T) {
			if got := ScopePromotionAction(tc.memoryType, tc.entityTaskCount); got != tc.want {
				t.Fatalf("ScopePromotionAction(%q, %d) = %q, want %q", tc.memoryType, tc.entityTaskCount, got, tc.want)
			}
		})
	}
}

func TestProcessCandidateBatchesCallsDeduplicatorAndArchivesDuplicate(t *testing.T) {
	orgID := uuid.New()
	strong := uuid.New()
	weak := uuid.New()
	unique := uuid.New()

	candidates := []CandidateMemory{
		{ID: strong, Content: "deploy pipeline retries fixed"},
		{ID: weak, Content: "deploy retries fixed in pipeline"},
		{ID: unique, Content: "billing alert threshold raised"},
	}

	dedup := &mockCandidateDeduplicator{
		reviews: []CandidateReview{
			{MemoryID: weak, Action: CandidateReviewArchive},
			{MemoryID: unique, Action: CandidateReviewKeep},
		},
	}

	statusByID := map[uuid.UUID]string{
		strong: "candidate",
		weak:   "candidate",
		unique: "candidate",
	}
	applier := func(context.Context, uuid.UUID, []CandidateMemory, []CandidateReview) (int, int, error) {
		updated := 0
		archived := 0
		for _, review := range dedup.reviews {
			if review.Action == CandidateReviewArchive || review.Action == CandidateReviewMerge {
				statusByID[review.MemoryID] = "archived"
				archived++
			}
		}
		return updated, archived, nil
	}

	examined, updated, archived, err := processCandidateBatches(context.Background(), orgID, candidates, dedup, applier)
	if err != nil {
		t.Fatalf("processCandidateBatches: %v", err)
	}
	if dedup.calls != 1 {
		t.Fatalf("deduplicator calls = %d, want 1", dedup.calls)
	}
	if len(dedup.batches) != 1 || len(dedup.batches[0]) != 3 {
		t.Fatalf("deduplicator batch size = %d, want 3", len(dedup.batches[0]))
	}
	if examined != 3 {
		t.Fatalf("examined = %d, want 3", examined)
	}
	if updated != 0 {
		t.Fatalf("updated = %d, want 0", updated)
	}
	if archived != 1 {
		t.Fatalf("archived = %d, want 1", archived)
	}
	if got := statusByID[weak]; got != "archived" {
		t.Fatalf("weaker duplicate status = %q, want archived", got)
	}
}

type mockCandidateDeduplicator struct {
	reviews []CandidateReview
	calls   int
	batches [][]CandidateMemory
}

func (m *mockCandidateDeduplicator) ReviewCandidateBatch(_ context.Context, _ uuid.UUID, batch []CandidateMemory) ([]CandidateReview, error) {
	m.calls++
	copied := append([]CandidateMemory(nil), batch...)
	m.batches = append(m.batches, copied)
	return append([]CandidateReview(nil), m.reviews...), nil
}
