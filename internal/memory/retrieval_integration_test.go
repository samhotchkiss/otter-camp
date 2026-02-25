//go:build integration

package memory

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestRetrieval_ScopeFilter(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, agent := seedOrgAndAgent(t, ctx, pool, []string{"project"})
	projectRepo := repo.NewProjectRepo(pool)
	memoryRepo := repo.NewMemoryRepo(pool)

	projectOne, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "scope-p1-" + uuid.NewString()[:8],
		DisplayName:    "Scope P1",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project one: %v", err)
	}
	projectTwo, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "scope-p2-" + uuid.NewString()[:8],
		DisplayName:    "Scope P2",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project two: %v", err)
	}

	if _, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		ProjectID:      &projectOne.ID,
		MemoryType:     "semantic",
		Scope:          "project",
		Content:        "project one memory",
		ContentHash:    "scope-p1",
		Embedding:      embeddingWithSignal(10),
		Status:         "active",
		Confidence:     0.8,
		Sensitivity:    "normal",
	}); err != nil {
		t.Fatalf("create project one memory: %v", err)
	}
	if _, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		ProjectID:      &projectTwo.ID,
		MemoryType:     "semantic",
		Scope:          "project",
		Content:        "project two memory",
		ContentHash:    "scope-p2",
		Embedding:      embeddingWithSignal(11),
		Status:         "active",
		Confidence:     0.8,
		Sensitivity:    "normal",
	}); err != nil {
		t.Fatalf("create project two memory: %v", err)
	}

	retriever, err := NewRetriever(RetrieverOptions{
		Pool:     pool,
		Embedder: staticEmbedder{vector: embeddingWithSignal(12)},
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	result, err := retriever.Query(ctx, RetrievalRequest{
		OrganizationID: org.ID,
		AgentID:        &agent.ID,
		ProjectID:      &projectOne.ID,
		Query:          "project memory",
		Mode:           RetrievalModePassive,
		MaxResults:     10,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Memories) == 0 {
		t.Fatal("expected at least one scoped memory")
	}
	for _, item := range result.Memories {
		if item.Memory.ProjectID == nil || *item.Memory.ProjectID != projectOne.ID {
			t.Fatalf("retrieved memory project_id = %v, want %s", item.Memory.ProjectID, projectOne.ID)
		}
	}
}

func TestRetrieval_VectorSimilarity(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := createMemoryTestOrg(t, ctx, pool, "retrieval-vector")

	var extension string
	if err := pool.QueryRow(ctx, `SELECT extname FROM pg_extension WHERE extname = 'vector'`).Scan(&extension); err != nil {
		t.Fatalf("pgvector extension missing: %v", err)
	}

	memoryRepo := repo.NewMemoryRepo(pool)
	insert := func(content string, vector []float32) {
		if _, err := memoryRepo.Create(ctx, repo.Memory{
			OrganizationID: org.ID,
			MemoryType:     "semantic",
			Scope:          "org",
			Content:        content,
			ContentHash:    uuid.NewString(),
			Embedding:      vector,
			Status:         "active",
			Confidence:     0.8,
			Sensitivity:    "normal",
		}); err != nil {
			t.Fatalf("create memory %q: %v", content, err)
		}
	}

	insert("memory-1", vector1536(0.9, 0.1, 0))
	insert("memory-2", vector1536(1.0, 0.9, 0))
	insert("memory-3", vector1536(-0.9, 0, 0))
	insert("memory-4", vector1536(0.95, 0.8, 0))
	insert("memory-5", vector1536(0, 0, 1))

	query := vector1536(1, 0.88, 0)
	rows, err := pool.Query(ctx, `
		SELECT content
		FROM memory
		WHERE organization_id = $1
		ORDER BY embedding <=> $2::vector ASC
		LIMIT 2
	`, org.ID, vectorLiteral(query))
	if err != nil {
		t.Fatalf("vector similarity query: %v", err)
	}
	defer rows.Close()

	got := make([]string, 0, 2)
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			t.Fatalf("scan content: %v", err)
		}
		got = append(got, content)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ranked result len = %d, want 2", len(got))
	}
	want := map[string]bool{"memory-2": true, "memory-4": true}
	if !want[got[0]] || !want[got[1]] {
		t.Fatalf("top results = %v, want memory-2 and memory-4", got)
	}
}

func TestRetrieval_FallbackToFullCorpus(t *testing.T) {
	TestRetrieverTaxonomySubtreeFilterFallbackWhenFewResults(t)
}

func TestRetrieval_SensitivityGating(t *testing.T) {
	TestRetrieverScopeAndSensitivityGates(t)
}

func TestRetrieval_InjectionOrdering(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, agent := seedOrgAndAgent(t, ctx, pool, []string{"org"})

	memoryRepo := repo.NewMemoryRepo(pool)
	signals := []float32{3, 12, 40}
	for _, signal := range signals {
		if _, err := memoryRepo.Create(ctx, repo.Memory{
			OrganizationID: org.ID,
			MemoryType:     "semantic",
			Scope:          "org",
			Content:        fmt.Sprintf("injection-%0.0f", signal),
			ContentHash:    uuid.NewString(),
			Embedding:      embeddingWithSignal(signal),
			Status:         "active",
			Confidence:     0.8,
			Sensitivity:    "normal",
		}); err != nil {
			t.Fatalf("create memory signal %f: %v", signal, err)
		}
	}

	retriever, err := NewRetriever(RetrieverOptions{
		Pool:     pool,
		Embedder: staticEmbedder{vector: embeddingWithSignal(40)},
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	result, err := retriever.Query(ctx, RetrievalRequest{
		OrganizationID: org.ID,
		AgentID:        &agent.ID,
		Query:          "release check",
		Mode:           RetrievalModePassive,
		MaxResults:     3,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Memories) != 3 {
		t.Fatalf("result len = %d, want 3", len(result.Memories))
	}
	scores := []float64{result.Memories[0].Score, result.Memories[1].Score, result.Memories[2].Score}
	if !sort.Float64sAreSorted(scores) {
		t.Fatalf("scores are not least-relevant-first: %v", scores)
	}
}

func TestRetrieval_EntitySynthesis(t *testing.T) {
	TestEntitySynthesizerTriggerFromMentions(t)
}

func TestDedup_CosinePrescreenAndLLM(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, _ := seedOrgAndAgent(t, ctx, pool, []string{"org"})

	memoryRepo := repo.NewMemoryRepo(pool)
	dedupRepo := repo.NewMemoryDedupReviewedRepo(pool)

	m1, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "Project Alpha launches in March with weekly stakeholder updates",
		ContentHash:    "dedup-m1",
		Embedding:      vector1536(1, 0.95, 0),
		Status:         "active",
		Confidence:     0.8,
		Sensitivity:    "normal",
	})
	if err != nil {
		t.Fatalf("create m1: %v", err)
	}
	m2, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "Project Alpha ships in March and keeps weekly stakeholder updates",
		ContentHash:    "dedup-m2",
		Embedding:      vector1536(1, 0.9, 0.05),
		Status:         "active",
		Confidence:     0.7,
		Sensitivity:    "normal",
	})
	if err != nil {
		t.Fatalf("create m2: %v", err)
	}

	cos := cosineSimilarity(m1.Embedding, m2.Embedding)
	if cos < 0.88 {
		t.Fatalf("cosine similarity = %.4f, want >= 0.88", cos)
	}

	deduper, err := NewDeduper(DeduperOptions{
		Pool:     pool,
		Reviewer: staticDedupReviewer{decision: DedupDecisionSupersedeB},
		Merger:   staticDedupMerger{},
	})
	if err != nil {
		t.Fatalf("NewDeduper: %v", err)
	}
	if err := deduper.ReviewCluster(ctx, []DedupPair{{MemoryA: m1, MemoryB: m2, CosineSimilarity: cos}}); err != nil {
		t.Fatalf("ReviewCluster: %v", err)
	}

	// Acceptance criterion: superseded memory still exists (no hard delete).
	if _, err := memoryRepo.GetByID(ctx, m1.ID); err != nil {
		t.Fatalf("m1 unexpectedly missing: %v", err)
	}
	record, err := dedupRepo.GetByPair(ctx, m1.ID, m2.ID)
	if err != nil {
		t.Fatalf("GetByPair: %v", err)
	}
	if record.Decision != string(DedupDecisionSupersedeB) {
		t.Fatalf("dedup decision = %q, want %q", record.Decision, DedupDecisionSupersedeB)
	}
}

func TestDedup_CosineBelowThreshold(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := createMemoryTestOrg(t, ctx, pool, "dedup-low-cosine")

	memoryRepo := repo.NewMemoryRepo(pool)
	model := &integrationMockExtractor{fixtures: []ExtractedMemory{{
		Content:         usefulMemoryContent("completely unrelated preference around color themes and documentation style"),
		MemoryType:      "semantic",
		Confidence:      0.8,
		UtilityEstimate: 0.8,
	}}}
	extractor, err := NewExtractor(ExtractorOptions{
		Pool:                       pool,
		Model:                      model,
		Embedder:                   &fakeEmbedder{vectors: [][]float32{vector1536(0, 1, 0)}},
		BehavioralOverridePatterns: []string{"ignore\\s+all\\s+previous\\s+instructions"},
		Prompt:                     "extract",
	})
	if err != nil {
		t.Fatalf("NewExtractor: %v", err)
	}

	if _, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        usefulMemoryContent("project alpha launch timeline and stakeholder updates"),
		ContentHash:    "dedup-low-m1",
		Embedding:      vector1536(1, 0, 0),
		Status:         "active",
		Confidence:     0.8,
		Sensitivity:    "normal",
	}); err != nil {
		t.Fatalf("create baseline memory: %v", err)
	}

	if err := extractor.ExtractFromMessages(ctx, org.ID, []ChatMessage{{
		Role:       "user",
		AuthorType: "human_user",
		Content:    usefulMessage(),
	}}, ExtractionSourceContext{TrustTierCap: 0.8}); err != nil {
		t.Fatalf("ExtractFromMessages: %v", err)
	}

	var dedupCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM memory_dedup_reviewed WHERE decision = 'deferred'`).Scan(&dedupCount); err != nil {
		t.Fatalf("count dedup reviews: %v", err)
	}
	if dedupCount != 0 {
		t.Fatalf("dedup review count = %d, want 0", dedupCount)
	}
	var memoryCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM memory WHERE organization_id = $1`, org.ID).Scan(&memoryCount); err != nil {
		t.Fatalf("count memories: %v", err)
	}
	if memoryCount != 2 {
		t.Fatalf("memory count = %d, want 2", memoryCount)
	}
}

func TestContradiction_Detection(t *testing.T) {
	TestContradictionDetectorDemotesActiveMemoryToCandidateWhenConfidenceDropsLow(t)
}

func vector1536(x, y, z float32) []float32 {
	v := make([]float32, 1536)
	v[0] = x
	v[1] = y
	v[2] = z
	var sum float64
	for _, value := range v[:3] {
		sum += float64(value * value)
	}
	if sum == 0 {
		v[0] = 1
		return v
	}
	norm := float32(math.Sqrt(sum))
	v[0] = v[0] / norm
	v[1] = v[1] / norm
	v[2] = v[2] / norm
	return v
}

func vectorLiteral(values []float32) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprintf("%f", value)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

type staticDedupMerger struct{}

func (staticDedupMerger) Merge(_ context.Context, _ pgx.Tx, _ uuid.UUID, memoryA, _ repo.Memory) (repo.Memory, error) {
	return memoryA, nil
}
