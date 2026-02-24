//go:build integration

package repo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestMemoryRepoGetByContentHashAndListCandidates(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	memoryRepo := NewMemoryRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "memory-hash-org", DisplayName: "Memory Hash Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	memA, err := memoryRepo.Create(ctx, Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "alpha fact",
		ContentHash:    "hash-alpha",
		Status:         "candidate",
	})
	if err != nil {
		t.Fatalf("create memory A: %v", err)
	}
	memB, err := memoryRepo.Create(ctx, Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "beta fact",
		ContentHash:    "hash-beta",
		Status:         "candidate",
	})
	if err != nil {
		t.Fatalf("create memory B: %v", err)
	}

	oldTime := time.Now().UTC().AddDate(0, 0, -8)
	if _, err := pool.Exec(ctx, `UPDATE memory SET created_at = $2 WHERE id = $1`, memA.ID, oldTime); err != nil {
		t.Fatalf("backdate memory A: %v", err)
	}

	byHashA, err := memoryRepo.GetByContentHash(ctx, org.ID, "hash-alpha")
	if err != nil {
		t.Fatalf("GetByContentHash alpha: %v", err)
	}
	if byHashA.ID != memA.ID {
		t.Fatalf("hash alpha returned id %s, want %s", byHashA.ID, memA.ID)
	}

	_, err = memoryRepo.GetByContentHash(ctx, org.ID, "unknown-hash")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByContentHash unknown error = %v, want ErrNotFound", err)
	}

	candidates, err := memoryRepo.ListCandidates(ctx, org.ID, time.Now().UTC().AddDate(0, 0, -7))
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("ListCandidates count = %d, want 1", len(candidates))
	}
	if candidates[0].ID != memA.ID {
		t.Fatalf("candidate id = %s, want %s", candidates[0].ID, memA.ID)
	}
	if candidates[0].ID == memB.ID {
		t.Fatalf("unexpected candidate memory %s returned", memB.ID)
	}
}

func TestMemoryRepoListForRetrievalFiltersSensitivity(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	memoryRepo := NewMemoryRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "memory-sensitivity-org", DisplayName: "Memory Sensitivity Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	if _, err := memoryRepo.Create(ctx, Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "normal memory",
		ContentHash:    "normal-hash",
		Status:         "active",
		Sensitivity:    "normal",
	}); err != nil {
		t.Fatalf("create normal memory: %v", err)
	}
	if _, err := memoryRepo.Create(ctx, Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "restricted memory",
		ContentHash:    "restricted-hash",
		Status:         "active",
		Sensitivity:    "restricted",
	}); err != nil {
		t.Fatalf("create restricted memory: %v", err)
	}

	sensitivity := "normal"
	items, err := memoryRepo.ListForRetrieval(ctx, RetrievalFilter{
		OrganizationID: org.ID,
		Statuses:       []string{"active"},
		Sensitivity:    &sensitivity,
	})
	if err != nil {
		t.Fatalf("ListForRetrieval: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("ListForRetrieval count = %d, want 1", len(items))
	}
	if items[0].Sensitivity != "normal" {
		t.Fatalf("retrieved sensitivity = %q, want normal", items[0].Sensitivity)
	}
}

func TestMemoryRepoVectorRoundTripAndHNSWExplain(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	memoryRepo := NewMemoryRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "memory-vector-org", DisplayName: "Memory Vector Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	expected := makeEmbedding(1536, 0.01)
	created, err := memoryRepo.Create(ctx, Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "vector memory",
		ContentHash:    "vector-hash",
		Status:         "active",
		Embedding:      expected,
	})
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}

	stored, err := memoryRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(stored.Embedding) != len(expected) {
		t.Fatalf("embedding length = %d, want %d", len(stored.Embedding), len(expected))
	}
	for _, index := range []int{0, 1, 2, 128, 1024, 1535} {
		if math.Abs(float64(stored.Embedding[index]-expected[index])) > 0.0001 {
			t.Fatalf("embedding[%d] = %f, want %f", index, stored.Embedding[index], expected[index])
		}
	}

	rng := rand.New(rand.NewSource(42))
	for index := 0; index < 100; index++ {
		vector := make([]float32, 1536)
		for idx := range vector {
			vector[idx] = rng.Float32()
		}
		if _, err := memoryRepo.Create(ctx, Memory{
			OrganizationID: org.ID,
			MemoryType:     "semantic",
			Scope:          "org",
			Content:        fmt.Sprintf("vector-%03d", index),
			ContentHash:    fmt.Sprintf("vector-hash-%03d", index),
			Status:         "active",
			Embedding:      vector,
		}); err != nil {
			t.Fatalf("seed vector row %d: %v", index, err)
		}
	}

	if _, err := pool.Exec(ctx, `SET enable_seqscan = off`); err != nil {
		t.Fatalf("disable seqscan: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `SET enable_seqscan = on`)
	})

	explainRows, err := pool.Query(ctx, `
		EXPLAIN SELECT id
		FROM memory
		WHERE embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT 10
	`, embeddingToLiteral(makeEmbedding(1536, 0.02)))
	if err != nil {
		t.Fatalf("EXPLAIN query: %v", err)
	}
	defer explainRows.Close()

	lines := make([]string, 0)
	for explainRows.Next() {
		var line string
		if scanErr := explainRows.Scan(&line); scanErr != nil {
			t.Fatalf("scan explain line: %v", scanErr)
		}
		lines = append(lines, line)
	}
	if explainRows.Err() != nil {
		t.Fatalf("iterate explain rows: %v", explainRows.Err())
	}

	plan := strings.Join(lines, "\n")
	if !strings.Contains(plan, "memory_embedding_hnsw_idx") {
		t.Fatalf("expected HNSW index in plan, got:\n%s", plan)
	}
}

func TestMemoryRepoSupersedeAndDedupPairUniqueness(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	memoryRepo := NewMemoryRepo(pool)
	dedupRepo := NewMemoryDedupReviewedRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "memory-dedup-org", DisplayName: "Memory Dedup Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	a, err := memoryRepo.Create(ctx, Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "memory A",
		ContentHash:    "memory-a-hash",
		Status:         "active",
	})
	if err != nil {
		t.Fatalf("create memory A: %v", err)
	}
	b, err := memoryRepo.Create(ctx, Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "memory B",
		ContentHash:    "memory-b-hash",
		Status:         "active",
	})
	if err != nil {
		t.Fatalf("create memory B: %v", err)
	}

	superseded, err := memoryRepo.Supersede(ctx, a.ID, b.ID)
	if err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	if superseded.SupersededBy == nil || *superseded.SupersededBy != b.ID {
		t.Fatalf("superseded_by = %v, want %s", superseded.SupersededBy, b.ID)
	}
	if superseded.SupersededAt == nil {
		t.Fatal("superseded_at is nil")
	}

	if _, err := dedupRepo.Create(ctx, MemoryDedupReviewed{
		MemoryIDA:        a.ID,
		MemoryIDB:        b.ID,
		CosineSimilarity: 0.9123,
		Decision:         "deferred",
		ReviewedBy:       "llm",
	}); err != nil {
		t.Fatalf("create dedup (A,B): %v", err)
	}

	_, err = dedupRepo.Create(ctx, MemoryDedupReviewed{
		MemoryIDA:        b.ID,
		MemoryIDB:        a.ID,
		CosineSimilarity: 0.9123,
		Decision:         "deferred",
		ReviewedBy:       "llm",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("create dedup (B,A) error = %v, want ErrConflict", err)
	}
}

func TestMemoryImportRequestedBySetNullOnDelete(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	importRepo := NewMemoryImportRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "memory-import-org", DisplayName: "Memory Import Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO human_user (organization_id, email, display_name, role)
		VALUES ($1, $2, $3, 'admin')
		RETURNING id
	`, org.ID, "memory-import-user@example.com", "Memory Import User").Scan(&userID); err != nil {
		t.Fatalf("create human_user: %v", err)
	}

	created, err := importRepo.Create(ctx, MemoryImport{
		OrganizationID: org.ID,
		RequestedBy:    &userID,
		FileKey:        "imports/memory.jsonl.zip",
	})
	if err != nil {
		t.Fatalf("create memory import: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM human_user WHERE id = $1`, userID); err != nil {
		t.Fatalf("delete human_user: %v", err)
	}

	stored, err := importRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID memory import: %v", err)
	}
	if stored.RequestedBy != nil {
		t.Fatalf("requested_by = %v, want nil", stored.RequestedBy)
	}
}

func makeEmbedding(size int, scale float32) []float32 {
	items := make([]float32, size)
	for index := range items {
		items[index] = scale * float32(index+1)
	}
	return items
}
