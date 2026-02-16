package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestEllieEntitySynthesisRoundTripRanksDefinitionFirst(t *testing.T) {
	db := setupEmbeddingWorkerTestDatabase(t)

	var orgID string
	err := db.QueryRow(
		`INSERT INTO organizations (name, slug, tier) VALUES ('Entity Synthesis Org', 'entity-synthesis-org', 'free') RETURNING id`,
	).Scan(&orgID)
	require.NoError(t, err)

	var projectID string
	err = db.QueryRow(
		`INSERT INTO projects (org_id, name, status) VALUES ($1, 'Entity Synthesis Project', 'active') RETURNING id`,
		orgID,
	).Scan(&projectID)
	require.NoError(t, err)

	base := time.Date(2026, 2, 16, 19, 30, 0, 0, time.UTC)
	sourceMemoryIDs := make([]string, 0, 5)
	for i := 0; i < 5; i += 1 {
		var memoryID string
		err = db.QueryRow(
			`INSERT INTO memories (org_id, kind, title, content, metadata, status, source_project_id, occurred_at)
			 VALUES ($1, 'fact', $2, $3, '{}'::jsonb, 'active', $4, $5)
			 RETURNING id`,
			orgID,
			fmt.Sprintf("ItsAlive source fact %d", i+1),
			fmt.Sprintf("ItsAlive fact %d: service component behavior detail.", i+1),
			projectID,
			base.Add(time.Duration(i)*time.Minute),
		).Scan(&memoryID)
		require.NoError(t, err)
		sourceMemoryIDs = append(sourceMemoryIDs, memoryID)
	}

	embeddingStore := store.NewConversationEmbeddingStoreWithDimension(db, 1536)
	sourceVector := make([]float64, 1536)
	sourceVector[1] = 1.0
	for _, memoryID := range sourceMemoryIDs {
		err = embeddingStore.UpdateMemoryEmbedding(context.Background(), memoryID, sourceVector)
		require.NoError(t, err)
	}

	synthVector := make([]float64, 1536)
	synthVector[0] = 1.0
	embedder := &fakeEllieEntityEmbedder{result: [][]float64{synthVector}}
	synthesizer := &fakeEllieEntitySynthesizer{result: EllieEntitySynthesisOutput{
		Title:   "ItsAlive definition",
		Content: "ItsAlive is Otter Camp's memory retrieval service. It synthesizes scattered facts into a single, high-fidelity definition.",
		Model:   "anthropic/claude-sonnet-4-20250514",
	}}

	worker := NewEllieEntitySynthesisWorker(
		store.NewEllieEntitySynthesisStore(db),
		embedder,
		embeddingStore,
		EllieEntitySynthesisWorkerConfig{Synthesizer: synthesizer},
	)

	runResult, err := worker.RunOnce(context.Background(), orgID)
	require.NoError(t, err)
	require.Equal(t, 1, runResult.CreatedCount)

	var synthesizedMemoryID string
	err = db.QueryRow(
		`SELECT id
		 FROM memories
		 WHERE org_id = $1
		   AND metadata->>'source_type' = 'synthesis'
		   AND metadata->>'entity_key' = 'itsalive'
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1`,
		orgID,
	).Scan(&synthesizedMemoryID)
	require.NoError(t, err)

	retrievalStore := store.NewEllieRetrievalStore(db)
	results, err := retrievalStore.SearchMemoriesOrgWideWithEmbedding(
		context.Background(),
		orgID,
		"What is ItsAlive?",
		synthVector,
		10,
	)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	require.Equal(t, synthesizedMemoryID, results[0].MemoryID)
}
