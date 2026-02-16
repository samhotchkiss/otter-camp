package memory

import (
	"context"
	"database/sql"
	"sort"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestTaxonomyRoundTripIntegration(t *testing.T) {
	db := setupEmbeddingWorkerTestDatabase(t)
	orgID := createTaxonomyIntegrationOrganization(t, db, "taxonomy-roundtrip-org")

	taxonomyStore := store.NewEllieTaxonomyStore(db)
	technicalRoot, err := taxonomyStore.CreateNode(context.Background(), store.CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		Slug:        "technical",
		DisplayName: "Technical",
	})
	require.NoError(t, err)
	_, err = taxonomyStore.CreateNode(context.Background(), store.CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		ParentID:    &technicalRoot.ID,
		Slug:        "embeddings",
		DisplayName: "Embeddings",
	})
	require.NoError(t, err)

	memoryID := createTaxonomyIntegrationMemory(t, db, orgID, "Embedding model", "Use text-embedding-3-small with 1536 dimensions")

	classifier := &fakeEllieTaxonomyClassifierLLM{outputs: []EllieTaxonomyLLMClassificationOutput{
		{
			Model:   "anthropic/claude-3-5-haiku-latest",
			TraceID: "trace-roundtrip",
			RawJSON: `{"classifications":[{"path":"technical/embeddings","confidence":0.93}]}`,
		},
	}}
	worker := NewEllieTaxonomyClassifierWorker(taxonomyStore, EllieTaxonomyClassifierWorkerConfig{LLM: classifier})

	runResult, err := worker.RunOnce(context.Background(), orgID)
	require.NoError(t, err)
	require.Equal(t, 1, runResult.PendingMemories)
	require.Equal(t, 1, runResult.ClassifiedMemories)

	service := NewEllieRetrievalCascadeService(&fakeEllieRetrievalStore{}, nil)
	service.TaxonomyStore = taxonomyStore
	service.TaxonomyQueryClassifier = &fakeEllieTaxonomyQueryClassifier{result: []EllieTaxonomyQueryClassification{
		{Path: "technical", Confidence: 0.8},
	}}

	retrieval, err := service.Retrieve(context.Background(), EllieRetrievalRequest{
		OrgID: orgID,
		Query: "embedding configuration",
		Limit: 10,
	})
	require.NoError(t, err)
	require.False(t, retrieval.NoInformation)
	require.Equal(t, 2, retrieval.TierUsed)
	require.Len(t, retrieval.Items, 1)
	require.Equal(t, memoryID, retrieval.Items[0].ID)
}

func TestTaxonomyClassifierMultiNodeIntegration(t *testing.T) {
	db := setupEmbeddingWorkerTestDatabase(t)
	orgID := createTaxonomyIntegrationOrganization(t, db, "taxonomy-multinode-org")

	taxonomyStore := store.NewEllieTaxonomyStore(db)
	projectsRoot, err := taxonomyStore.CreateNode(context.Background(), store.CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		Slug:        "projects",
		DisplayName: "Projects",
	})
	require.NoError(t, err)
	_, err = taxonomyStore.CreateNode(context.Background(), store.CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		ParentID:    &projectsRoot.ID,
		Slug:        "otter-camp",
		DisplayName: "Otter Camp",
	})
	require.NoError(t, err)

	technicalRoot, err := taxonomyStore.CreateNode(context.Background(), store.CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		Slug:        "technical",
		DisplayName: "Technical",
	})
	require.NoError(t, err)
	_, err = taxonomyStore.CreateNode(context.Background(), store.CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		ParentID:    &technicalRoot.ID,
		Slug:        "embeddings",
		DisplayName: "Embeddings",
	})
	require.NoError(t, err)

	memoryID := createTaxonomyIntegrationMemory(
		t,
		db,
		orgID,
		"Pearl embedding model",
		"Pearl runs for otter-camp and uses embedding vectors for semantic retrieval.",
	)

	classifier := &fakeEllieTaxonomyClassifierLLM{outputs: []EllieTaxonomyLLMClassificationOutput{
		{
			Model:   "anthropic/claude-3-5-haiku-latest",
			TraceID: "trace-multinode",
			RawJSON: `{"classifications":[{"path":"projects/otter-camp","confidence":0.88},{"path":"technical/embeddings","confidence":0.79}]}`,
		},
	}}
	worker := NewEllieTaxonomyClassifierWorker(taxonomyStore, EllieTaxonomyClassifierWorkerConfig{LLM: classifier})

	runResult, err := worker.RunOnce(context.Background(), orgID)
	require.NoError(t, err)
	require.Equal(t, 1, runResult.ClassifiedMemories)

	classifications, err := taxonomyStore.ListMemoryClassifications(context.Background(), orgID, memoryID)
	require.NoError(t, err)
	require.Len(t, classifications, 2)

	paths := []string{classifications[0].NodePath, classifications[1].NodePath}
	sort.Strings(paths)
	require.Equal(t, []string{"projects/otter-camp", "technical/embeddings"}, paths)
}

func TestTaxonomyRetrievalCrossOrgDenied(t *testing.T) {
	db := setupEmbeddingWorkerTestDatabase(t)
	orgA := createTaxonomyIntegrationOrganization(t, db, "taxonomy-cross-org-a")
	orgB := createTaxonomyIntegrationOrganization(t, db, "taxonomy-cross-org-b")

	taxonomyStore := store.NewEllieTaxonomyStore(db)

	orgARoot, err := taxonomyStore.CreateNode(context.Background(), store.CreateEllieTaxonomyNodeInput{
		OrgID:       orgA,
		Slug:        "technical",
		DisplayName: "Technical",
	})
	require.NoError(t, err)
	orgAChild, err := taxonomyStore.CreateNode(context.Background(), store.CreateEllieTaxonomyNodeInput{
		OrgID:       orgA,
		ParentID:    &orgARoot.ID,
		Slug:        "embeddings",
		DisplayName: "Embeddings",
	})
	require.NoError(t, err)

	orgBRoot, err := taxonomyStore.CreateNode(context.Background(), store.CreateEllieTaxonomyNodeInput{
		OrgID:       orgB,
		Slug:        "technical",
		DisplayName: "Technical",
	})
	require.NoError(t, err)
	_, err = taxonomyStore.CreateNode(context.Background(), store.CreateEllieTaxonomyNodeInput{
		OrgID:       orgB,
		ParentID:    &orgBRoot.ID,
		Slug:        "embeddings",
		DisplayName: "Embeddings",
	})
	require.NoError(t, err)

	memoryA := createTaxonomyIntegrationMemory(t, db, orgA, "Org A embedding", "Org A private memory")
	require.NoError(t, taxonomyStore.UpsertMemoryClassification(context.Background(), store.UpsertEllieMemoryTaxonomyInput{
		OrgID:      orgA,
		MemoryID:   memoryA,
		NodeID:     orgAChild.ID,
		Confidence: 0.95,
	}))
	require.NoError(t, taxonomyStore.MarkMemoryTaxonomyClassified(
		context.Background(),
		orgA,
		memoryA,
		time.Now().UTC(),
		"anthropic/claude-3-5-haiku-latest",
		"trace-cross-org",
	))

	service := NewEllieRetrievalCascadeService(&fakeEllieRetrievalStore{}, nil)
	service.TaxonomyStore = taxonomyStore
	service.TaxonomyQueryClassifier = &fakeEllieTaxonomyQueryClassifier{result: []EllieTaxonomyQueryClassification{{Path: "technical", Confidence: 0.7}}}

	retrieval, err := service.Retrieve(context.Background(), EllieRetrievalRequest{
		OrgID: orgB,
		Query: "embedding memory",
		Limit: 10,
	})
	require.NoError(t, err)
	require.True(t, retrieval.NoInformation)
	require.Empty(t, retrieval.Items)
}

func createTaxonomyIntegrationOrganization(t *testing.T, db *sql.DB, slug string) string {
	t.Helper()

	var orgID string
	err := db.QueryRow(
		`INSERT INTO organizations (name, slug, tier)
		 VALUES ($1, $2, 'free')
		 RETURNING id`,
		slug,
		slug,
	).Scan(&orgID)
	require.NoError(t, err)
	return orgID
}

func createTaxonomyIntegrationMemory(t *testing.T, db *sql.DB, orgID, title, content string) string {
	t.Helper()

	var memoryID string
	err := db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, metadata, status, occurred_at)
		 VALUES ($1, 'fact', $2, $3, '{}'::jsonb, 'active', $4)
		 RETURNING id`,
		orgID,
		title,
		content,
		time.Now().UTC(),
	).Scan(&memoryID)
	require.NoError(t, err)
	return memoryID
}
