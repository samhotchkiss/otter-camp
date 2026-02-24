//go:build integration

package memory

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

var systemActorID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

func TestRetrieverFourStagePipelineRanksTopCosine(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, agent := seedOrgAndAgent(t, ctx, pool, []string{"org"})
	memoryRepo := repo.NewMemoryRepo(pool)

	created := make([]repo.Memory, 0, 20)
	for i := 1; i <= 20; i++ {
		item, err := memoryRepo.Create(ctx, repo.Memory{
			OrganizationID: org.ID,
			MemoryType:     "semantic",
			Scope:          "org",
			Content:        fmt.Sprintf("memory-%02d", i),
			ContentHash:    fmt.Sprintf("hash-%02d", i),
			Embedding:      embeddingWithSignal(float32(i)),
			Status:         "active",
			Confidence:     0.8,
			Sensitivity:    "normal",
		})
		if err != nil {
			t.Fatalf("create memory %d: %v", i, err)
		}
		created = append(created, item)
	}

	retriever, err := NewRetriever(RetrieverOptions{
		Pool:     pool,
		Embedder: staticEmbedder{vector: embeddingWithSignal(30)},
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	result, err := retriever.Query(ctx, RetrievalRequest{
		OrganizationID: org.ID,
		AgentID:        &agent.ID,
		Mode:           RetrievalModePassive,
		Query:          "release readiness",
		MaxResults:     5,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Memories) != 5 {
		t.Fatalf("result len = %d, want 5", len(result.Memories))
	}

	want := []string{"memory-16", "memory-17", "memory-18", "memory-19", "memory-20"}
	for idx, item := range result.Memories {
		if item.Memory.Content != want[idx] {
			t.Fatalf("result[%d] = %q, want %q", idx, item.Memory.Content, want[idx])
		}
	}
}

func TestRetrieverScopeAndSensitivityGates(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, agent := seedOrgAndAgent(t, ctx, pool, []string{"org"})
	projectRepo := repo.NewProjectRepo(pool)
	memoryRepo := repo.NewMemoryRepo(pool)

	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "scope-gate-project",
		DisplayName:    "Scope Gate Project",
		Description:    "scope gate",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    systemActorID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	orgMem, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "org-memory",
		ContentHash:    "scope-org-hash",
		Embedding:      embeddingWithSignal(1),
		Status:         "active",
		Sensitivity:    "normal",
		Confidence:     0.8,
	})
	if err != nil {
		t.Fatalf("create org memory: %v", err)
	}
	if _, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		ProjectID:      &project.ID,
		MemoryType:     "semantic",
		Scope:          "project",
		Content:        "project-memory",
		ContentHash:    "scope-project-hash",
		Embedding:      embeddingWithSignal(2),
		Status:         "active",
		Sensitivity:    "normal",
		Confidence:     0.8,
	}); err != nil {
		t.Fatalf("create project memory: %v", err)
	}
	if _, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		AgentID:        &agent.ID,
		MemoryType:     "semantic",
		Scope:          "agent",
		Content:        "agent-private-memory",
		ContentHash:    "scope-agent-hash",
		Embedding:      embeddingWithSignal(3),
		Status:         "active",
		Sensitivity:    "normal",
		Confidence:     0.8,
	}); err != nil {
		t.Fatalf("create agent private memory: %v", err)
	}
	if _, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "restricted-memory",
		ContentHash:    "scope-restricted-hash",
		Embedding:      embeddingWithSignal(4),
		Status:         "active",
		Sensitivity:    "restricted",
		Confidence:     0.8,
	}); err != nil {
		t.Fatalf("create restricted memory: %v", err)
	}

	retriever, err := NewRetriever(RetrieverOptions{
		Pool:     pool,
		Embedder: staticEmbedder{vector: embeddingWithSignal(5)},
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	passiveResult, err := retriever.Query(ctx, RetrievalRequest{
		OrganizationID:  org.ID,
		AgentID:         &agent.ID,
		Mode:            RetrievalModePassive,
		Query:           "scope check",
		SensitivityGate: true,
		MaxResults:      10,
	})
	if err != nil {
		t.Fatalf("passive Query: %v", err)
	}
	if len(passiveResult.Memories) != 1 {
		t.Fatalf("passive result len = %d, want 1", len(passiveResult.Memories))
	}
	if passiveResult.Memories[0].Memory.ID != orgMem.ID {
		t.Fatalf("passive memory id = %s, want %s", passiveResult.Memories[0].Memory.ID, orgMem.ID)
	}

	activeResult, err := retriever.Query(ctx, RetrievalRequest{
		OrganizationID:  org.ID,
		AgentID:         &agent.ID,
		Mode:            RetrievalModeAgentQuery,
		Query:           "scope check",
		SensitivityGate: false,
		MaxResults:      10,
	})
	if err != nil {
		t.Fatalf("active Query: %v", err)
	}
	foundRestricted := false
	for _, item := range activeResult.Memories {
		if item.Memory.Sensitivity == "restricted" {
			foundRestricted = true
			break
		}
	}
	if !foundRestricted {
		t.Fatal("restricted memory was not returned when sensitivity gate was disabled")
	}
}

func TestRetrieverTaxonomySubtreeFilter(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, agent := seedOrgAndAgent(t, ctx, pool, []string{"org"})
	memoryRepo := repo.NewMemoryRepo(pool)
	taxonomyRepo := repo.NewMemoryTaxonomyNodeRepo(pool)
	tagRepo := repo.NewMemoryTaxonomyTagRepo(pool)

	workflow, err := taxonomyRepo.Create(ctx, repo.MemoryTaxonomyNode{
		OrganizationID: org.ID,
		Slug:           "workflow",
		DisplayName:    "workflow",
	})
	if err != nil {
		t.Fatalf("create workflow root: %v", err)
	}
	workflowGit, err := taxonomyRepo.Create(ctx, repo.MemoryTaxonomyNode{
		OrganizationID: org.ID,
		ParentID:       &workflow.ID,
		Slug:           "workflow.git",
		DisplayName:    "workflow git",
	})
	if err != nil {
		t.Fatalf("create workflow child: %v", err)
	}
	preferences, err := taxonomyRepo.Create(ctx, repo.MemoryTaxonomyNode{
		OrganizationID: org.ID,
		Slug:           "preferences",
		DisplayName:    "preferences",
	})
	if err != nil {
		t.Fatalf("create preferences root: %v", err)
	}

	makeMemory := func(content string) repo.Memory {
		memory, createErr := memoryRepo.Create(ctx, repo.Memory{
			OrganizationID: org.ID,
			MemoryType:     "semantic",
			Scope:          "org",
			Content:        content,
			ContentHash:    hashContent(content),
			Embedding:      embeddingWithSignal(5),
			Status:         "active",
			Sensitivity:    "normal",
			Confidence:     0.8,
		})
		if createErr != nil {
			t.Fatalf("create memory %s: %v", content, createErr)
		}
		return memory
	}

	memoryA := makeMemory("workflow git standard")
	memoryB := makeMemory("user preference color")
	memoryC := makeMemory("workflow branch strategy")
	memoryD := makeMemory("workflow release checklist")

	for _, item := range []repo.Memory{memoryA, memoryC, memoryD} {
		if _, err := tagRepo.Create(ctx, repo.MemoryTaxonomyTag{
			MemoryID:       item.ID,
			TaxonomyNodeID: workflowGit.ID,
			AssignedBy:     "manual",
			Confidence:     1,
		}); err != nil {
			t.Fatalf("tag workflow memory: %v", err)
		}
	}
	if _, err := tagRepo.Create(ctx, repo.MemoryTaxonomyTag{
		MemoryID:       memoryB.ID,
		TaxonomyNodeID: preferences.ID,
		AssignedBy:     "manual",
		Confidence:     1,
	}); err != nil {
		t.Fatalf("tag preference memory: %v", err)
	}

	retriever, err := NewRetriever(RetrieverOptions{
		Pool:     pool,
		Embedder: staticEmbedder{vector: embeddingWithSignal(6)},
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	result, err := retriever.Query(ctx, RetrievalRequest{
		OrganizationID: org.ID,
		AgentID:        &agent.ID,
		Query:          "workflow",
		Mode:           RetrievalModePassive,
		MaxResults:     10,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if result.FallbackUsed {
		t.Fatal("fallback unexpectedly used for taxonomy subtree query")
	}
	if len(result.Memories) != 3 {
		t.Fatalf("result len = %d, want 3", len(result.Memories))
	}
	for _, item := range result.Memories {
		if item.Memory.ID == memoryB.ID {
			t.Fatalf("memory B (%s) should have been excluded by taxonomy filter", memoryB.ID)
		}
	}
}

func TestEntitySynthesizerTriggerFromMentions(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, _ := seedOrgAndAgent(t, ctx, pool, []string{"org"})
	entityRepo := repo.NewMemoryEntityRepo(pool)
	memoryRepo := repo.NewMemoryRepo(pool)
	mentionRepo := repo.NewMemoryEntityMentionRepo(pool)

	entity, err := entityRepo.Create(ctx, repo.MemoryEntity{
		OrganizationID: org.ID,
		CanonicalName:  "Acme Inc",
		EntityType:     "organization",
	})
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}

	for i := 0; i < 5; i++ {
		memory, createErr := memoryRepo.Create(ctx, repo.Memory{
			OrganizationID: org.ID,
			MemoryType:     "semantic",
			Scope:          "org",
			Content:        fmt.Sprintf("entity memory %d", i),
			ContentHash:    fmt.Sprintf("entity-hash-%d", i),
			Embedding:      embeddingWithSignal(float32(i + 1)),
			Status:         "active",
			Sensitivity:    "normal",
			Confidence:     0.9,
		})
		if createErr != nil {
			t.Fatalf("create mention memory %d: %v", i, createErr)
		}
		if _, mentionErr := mentionRepo.Create(ctx, repo.MemoryEntityMention{
			MemoryID:    memory.ID,
			EntityID:    entity.ID,
			MentionText: "Acme Inc",
			Confidence:  1,
		}); mentionErr != nil {
			t.Fatalf("create mention %d: %v", i, mentionErr)
		}
	}

	synthesizer, err := NewEntitySynthesizer(EntitySynthesizerOptions{
		Pool:  pool,
		Model: staticSynthesisModel{summary: "Acme is a high-priority customer with strict deployment windows."},
	})
	if err != nil {
		t.Fatalf("NewEntitySynthesizer: %v", err)
	}

	profile, err := synthesizer.SynthesizeProfile(ctx, entity.ID)
	if err != nil {
		t.Fatalf("SynthesizeProfile: %v", err)
	}
	if profile == nil {
		t.Fatal("profile is nil, want synthesized profile")
	}
	if profile.Memory.MemoryType != "entity_profile" {
		t.Fatalf("memory_type = %q, want entity_profile", profile.Memory.MemoryType)
	}

	storedEntity, err := entityRepo.GetByID(ctx, entity.ID)
	if err != nil {
		t.Fatalf("GetByID entity: %v", err)
	}
	if storedEntity.SynthesisMemoryID == nil {
		t.Fatal("synthesis_memory_id is nil")
	}
	if *storedEntity.SynthesisMemoryID != profile.Memory.ID {
		t.Fatalf("synthesis_memory_id = %s, want %s", *storedEntity.SynthesisMemoryID, profile.Memory.ID)
	}
}

func TestDeduperReviewClusterSupersedeA(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, _ := seedOrgAndAgent(t, ctx, pool, []string{"org"})
	memoryRepo := repo.NewMemoryRepo(pool)
	dedupRepo := repo.NewMemoryDedupReviewedRepo(pool)

	memoryA, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "deployment starts at 09:00",
		ContentHash:    "dedup-a",
		Embedding:      embeddingWithSignal(9),
		Status:         "active",
		Sensitivity:    "normal",
		Confidence:     0.8,
	})
	if err != nil {
		t.Fatalf("create memory A: %v", err)
	}
	memoryB, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "deploy starts at 9am",
		ContentHash:    "dedup-b",
		Embedding:      embeddingWithSignal(10),
		Status:         "active",
		Sensitivity:    "normal",
		Confidence:     0.8,
	})
	if err != nil {
		t.Fatalf("create memory B: %v", err)
	}

	deduper, err := NewDeduper(DeduperOptions{
		Pool: pool,
		Reviewer: staticDedupReviewer{
			decision: DedupDecisionSupersedeA,
		},
	})
	if err != nil {
		t.Fatalf("NewDeduper: %v", err)
	}

	if err := deduper.ReviewCluster(ctx, []DedupPair{{
		MemoryA:          memoryA,
		MemoryB:          memoryB,
		CosineSimilarity: 0.91,
	}}); err != nil {
		t.Fatalf("ReviewCluster: %v", err)
	}

	storedA, err := memoryRepo.GetByID(ctx, memoryA.ID)
	if err != nil {
		t.Fatalf("GetByID A: %v", err)
	}
	if storedA.Status != "superseded" {
		t.Fatalf("memory A status = %q, want superseded", storedA.Status)
	}
	if storedA.SupersededBy == nil || *storedA.SupersededBy != memoryB.ID {
		t.Fatalf("memory A superseded_by = %v, want %s", storedA.SupersededBy, memoryB.ID)
	}

	record, err := dedupRepo.GetByPair(ctx, memoryA.ID, memoryB.ID)
	if err != nil {
		t.Fatalf("GetByPair: %v", err)
	}
	if record.Decision != string(DedupDecisionSupersedeA) {
		t.Fatalf("dedup decision = %q, want %q", record.Decision, DedupDecisionSupersedeA)
	}
}

func TestContradictionDetectorArchivesWhenConfidenceFallsToZero(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, _ := seedOrgAndAgent(t, ctx, pool, []string{"org"})
	entityRepo := repo.NewMemoryEntityRepo(pool)
	memoryRepo := repo.NewMemoryRepo(pool)
	mentionRepo := repo.NewMemoryEntityMentionRepo(pool)

	entity, err := entityRepo.Create(ctx, repo.MemoryEntity{
		OrganizationID: org.ID,
		CanonicalName:  "Launch Window",
		EntityType:     "event",
	})
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}

	existing, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "Launch is at 09:00",
		ContentHash:    "contradict-existing",
		Embedding:      embeddingWithSignal(0),
		Status:         "active",
		Sensitivity:    "normal",
		Confidence:     0.05,
	})
	if err != nil {
		t.Fatalf("create existing memory: %v", err)
	}
	newMemory, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "Launch moved to 11:00",
		ContentHash:    "contradict-new",
		Embedding:      embeddingWithSignal(1),
		Status:         "candidate",
		Sensitivity:    "normal",
		Confidence:     0.90,
	})
	if err != nil {
		t.Fatalf("create new memory: %v", err)
	}

	for _, memoryID := range []uuid.UUID{existing.ID, newMemory.ID} {
		if _, err := mentionRepo.Create(ctx, repo.MemoryEntityMention{
			MemoryID:    memoryID,
			EntityID:    entity.ID,
			MentionText: "Launch Window",
			Confidence:  1,
		}); err != nil {
			t.Fatalf("create mention for %s: %v", memoryID, err)
		}
	}

	detector, err := NewContradictionDetector(ContradictionDetectorOptions{
		Pool: pool,
		Classifier: staticContradictionClassifier{
			label: ContradictionLabelContradictory,
		},
	})
	if err != nil {
		t.Fatalf("NewContradictionDetector: %v", err)
	}

	conflicts, err := detector.Check(ctx, newMemory)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].ID != existing.ID {
		t.Fatalf("conflicts = %v, want existing memory only", conflicts)
	}

	storedExisting, err := memoryRepo.GetByID(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetByID existing: %v", err)
	}
	if storedExisting.Status != "archived" {
		t.Fatalf("existing status = %q, want archived", storedExisting.Status)
	}

	storedNew, err := memoryRepo.GetByID(ctx, newMemory.ID)
	if err != nil {
		t.Fatalf("GetByID new: %v", err)
	}
	if storedNew.Confidence >= newMemory.Confidence {
		t.Fatalf("new confidence = %f, want less than %f", storedNew.Confidence, newMemory.Confidence)
	}
}

func seedOrgAndAgent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, scopes []string) (repo.Organization, repo.Agent) {
	t.Helper()

	orgRepo := repo.NewOrgRepo(pool)
	agentRepo := repo.NewAgentRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        fmt.Sprintf("memory-org-%s", uuid.NewString()[:8]),
		DisplayName: "Memory Test Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	agent, err := agentRepo.Create(ctx, repo.Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Memory Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "You are a memory agent.",
		OperatorInstructions: "",
		AgentType:            "general",
		IsStarterTrio:        false,
		PrivateMemory:        false,
		MemoryReadScopes:     scopes,
		CreatedByType:        "system",
		CreatedByID:          systemActorID,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	return org, agent
}

func embeddingWithSignal(signal float32) []float32 {
	vector := make([]float32, 1536)
	vector[0] = signal
	vector[1] = 1
	return vector
}

type staticEmbedder struct {
	vector []float32
}

func (s staticEmbedder) Embed(_ context.Context, _ uuid.UUID, _ string, inputs []string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i := range inputs {
		copyVector := make([]float32, len(s.vector))
		copy(copyVector, s.vector)
		out[i] = copyVector
	}
	return out, nil
}

type staticSynthesisModel struct {
	summary string
}

func (s staticSynthesisModel) SynthesizeEntityProfile(context.Context, uuid.UUID, repo.MemoryEntity, []repo.Memory) (string, error) {
	return s.summary, nil
}

type staticDedupReviewer struct {
	decision DedupDecision
}

func (s staticDedupReviewer) ReviewDedup(_ context.Context, _ uuid.UUID, pairs []DedupPair) ([]DedupReview, error) {
	out := make([]DedupReview, len(pairs))
	for index, pair := range pairs {
		out[index] = DedupReview{
			Pair:     pair,
			Decision: s.decision,
		}
	}
	return out, nil
}

type staticContradictionClassifier struct {
	label ContradictionLabel
}

func (s staticContradictionClassifier) ClassifyContradictions(_ context.Context, _ uuid.UUID, _ repo.Memory, candidates []repo.Memory) (map[uuid.UUID]ContradictionLabel, error) {
	out := make(map[uuid.UUID]ContradictionLabel, len(candidates))
	for _, candidate := range candidates {
		out[candidate.ID] = s.label
	}
	return out, nil
}
