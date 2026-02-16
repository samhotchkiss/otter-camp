package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeEllieEntitySynthesisStore struct {
	candidates              []store.EllieEntitySynthesisCandidate
	sourceMemories          map[string][]store.EllieEntitySynthesisSourceMemory
	created                 []store.CreateEllieExtractedMemoryInput
	autoSuppressAfterCreate bool
}

func (f *fakeEllieEntitySynthesisStore) ListCandidates(
	_ context.Context,
	_ string,
	_ int,
	_ int,
) ([]store.EllieEntitySynthesisCandidate, error) {
	if f.autoSuppressAfterCreate && len(f.created) > 0 {
		return []store.EllieEntitySynthesisCandidate{}, nil
	}
	out := make([]store.EllieEntitySynthesisCandidate, len(f.candidates))
	copy(out, f.candidates)
	return out, nil
}

func (f *fakeEllieEntitySynthesisStore) ListSourceMemories(
	_ context.Context,
	_ string,
	entityKey string,
	_ int,
) ([]store.EllieEntitySynthesisSourceMemory, error) {
	rows := f.sourceMemories[entityKey]
	out := make([]store.EllieEntitySynthesisSourceMemory, len(rows))
	copy(out, rows)
	return out, nil
}

func (f *fakeEllieEntitySynthesisStore) CreateEllieExtractedMemory(
	_ context.Context,
	input store.CreateEllieExtractedMemoryInput,
) (string, error) {
	f.created = append(f.created, input)
	return fmt.Sprintf("synth-%d", len(f.created)), nil
}

type fakeEllieEntitySynthesizer struct {
	calls      int
	lastPrompt string
	result     EllieEntitySynthesisOutput
	err        error
}

func (f *fakeEllieEntitySynthesizer) Synthesize(
	_ context.Context,
	input EllieEntitySynthesisInput,
) (EllieEntitySynthesisOutput, error) {
	f.calls += 1
	f.lastPrompt = input.Prompt
	if f.err != nil {
		return EllieEntitySynthesisOutput{}, f.err
	}
	return f.result, nil
}

type fakeEllieEntityEmbedder struct {
	calls  int
	inputs []string
	result [][]float64
	err    error
}

func (f *fakeEllieEntityEmbedder) Embed(_ context.Context, inputs []string) ([][]float64, error) {
	f.calls += 1
	f.inputs = append(f.inputs, inputs...)
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func (f *fakeEllieEntityEmbedder) Dimension() int {
	if len(f.result) == 0 || len(f.result[0]) == 0 {
		return 0
	}
	return len(f.result[0])
}

type fakeEllieEntityEmbeddingStore struct {
	updates map[string][]float64
}

func (f *fakeEllieEntityEmbeddingStore) UpdateMemoryEmbedding(_ context.Context, memoryID string, embedding []float64) error {
	if f.updates == nil {
		f.updates = make(map[string][]float64)
	}
	copyEmbedding := make([]float64, len(embedding))
	copy(copyEmbedding, embedding)
	f.updates[memoryID] = copyEmbedding
	return nil
}

func TestEllieEntitySynthesisWorkerCreatesSynthesisMemoryWithSourceLinkage(t *testing.T) {
	projectID := "11111111-1111-1111-1111-111111111111"
	fakeStore := &fakeEllieEntitySynthesisStore{
		candidates: []store.EllieEntitySynthesisCandidate{
			{EntityKey: "itsalive", EntityName: "ItsAlive", MentionCount: 5},
		},
		sourceMemories: map[string][]store.EllieEntitySynthesisSourceMemory{
			"itsalive": {
				{
					MemoryID:        "mem-1",
					Title:           "ItsAlive embedder",
					Content:         "ItsAlive uses text-embedding-3-small at 1536 dimensions.",
					SourceProjectID: &projectID,
					OccurredAt:      time.Date(2026, 2, 16, 18, 45, 0, 0, time.UTC),
				},
				{
					MemoryID:   "mem-2",
					Title:      "ItsAlive docs",
					Content:    "ItsAlive routes project docs ingestion through docs/START-HERE.md.",
					OccurredAt: time.Date(2026, 2, 16, 18, 46, 0, 0, time.UTC),
				},
			},
		},
	}

	synthesizer := &fakeEllieEntitySynthesizer{result: EllieEntitySynthesisOutput{
		Title:   "ItsAlive: Definition",
		Content: "ItsAlive is Otter Camp's retrieval specialist. It manages memory synthesis and ranking context for project queries.",
		Model:   "anthropic/claude-sonnet-4-20250514",
	}}

	embedding := make([]float64, 1536)
	embedding[0] = 0.12
	embedder := &fakeEllieEntityEmbedder{result: [][]float64{embedding}}
	embeddingStore := &fakeEllieEntityEmbeddingStore{}

	worker := NewEllieEntitySynthesisWorker(fakeStore, embedder, embeddingStore, EllieEntitySynthesisWorkerConfig{
		MinMentions:       5,
		CandidateBatch:    25,
		SourceMemoryLimit: 200,
		Synthesizer:       synthesizer,
	})

	result, err := worker.RunOnce(context.Background(), "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	require.NoError(t, err)
	require.Equal(t, 1, result.CreatedCount)
	require.Equal(t, 1, synthesizer.calls)
	require.Equal(t, 1, embedder.calls)
	require.Len(t, fakeStore.created, 1)

	created := fakeStore.created[0]
	require.Equal(t, "fact", created.Kind)
	require.Equal(t, "ItsAlive: Definition", created.Title)
	require.Equal(t, 5, created.Importance)
	require.Equal(t, 0.95, created.Confidence)
	require.NotNil(t, created.SourceProjectID)
	require.Equal(t, projectID, *created.SourceProjectID)

	var metadata map[string]any
	require.NoError(t, json.Unmarshal(created.Metadata, &metadata))
	require.Equal(t, "synthesis", metadata["source_type"])
	require.Equal(t, "itsalive", metadata["entity_key"])
	require.Equal(t, float64(2), metadata["source_memory_count"])
	require.Equal(t, "anthropic/claude-sonnet-4-20250514", metadata["synthesis_model"])

	sourceIDs, ok := metadata["source_memory_ids"].([]any)
	require.True(t, ok)
	require.Len(t, sourceIDs, 2)
	require.Equal(t, "mem-1", sourceIDs[0])
	require.Equal(t, "mem-2", sourceIDs[1])

	require.Contains(t, embeddingStore.updates, "synth-1")
	require.Len(t, embeddingStore.updates["synth-1"], 1536)
}

func TestEllieEntitySynthesisWorkerIsIdempotentWhenInputsUnchanged(t *testing.T) {
	fakeStore := &fakeEllieEntitySynthesisStore{
		autoSuppressAfterCreate: true,
		candidates: []store.EllieEntitySynthesisCandidate{
			{EntityKey: "itsalive", EntityName: "ItsAlive", MentionCount: 5},
		},
		sourceMemories: map[string][]store.EllieEntitySynthesisSourceMemory{
			"itsalive": {
				{MemoryID: "mem-1", Title: "One", Content: "ItsAlive fact one.", OccurredAt: time.Date(2026, 2, 16, 18, 50, 0, 0, time.UTC)},
				{MemoryID: "mem-2", Title: "Two", Content: "ItsAlive fact two.", OccurredAt: time.Date(2026, 2, 16, 18, 51, 0, 0, time.UTC)},
			},
		},
	}

	embedding := make([]float64, 1536)
	worker := NewEllieEntitySynthesisWorker(
		fakeStore,
		&fakeEllieEntityEmbedder{result: [][]float64{embedding}},
		&fakeEllieEntityEmbeddingStore{},
		EllieEntitySynthesisWorkerConfig{
			Synthesizer: &fakeEllieEntitySynthesizer{result: EllieEntitySynthesisOutput{Title: "ItsAlive", Content: "Definition"}},
		},
	)

	first, err := worker.RunOnce(context.Background(), "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	require.NoError(t, err)
	require.Equal(t, 1, first.CreatedCount)

	second, err := worker.RunOnce(context.Background(), "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	require.NoError(t, err)
	require.Equal(t, 0, second.CreatedCount)
	require.Len(t, fakeStore.created, 1)
}

func TestEllieEntitySynthesisWorkerDoesNotModifySourceMemories(t *testing.T) {
	source := []store.EllieEntitySynthesisSourceMemory{
		{MemoryID: "mem-1", Title: "One", Content: "ItsAlive fact one.", OccurredAt: time.Date(2026, 2, 16, 18, 55, 0, 0, time.UTC)},
		{MemoryID: "mem-2", Title: "Two", Content: "ItsAlive fact two.", OccurredAt: time.Date(2026, 2, 16, 18, 56, 0, 0, time.UTC)},
	}
	before := make([]store.EllieEntitySynthesisSourceMemory, len(source))
	copy(before, source)

	fakeStore := &fakeEllieEntitySynthesisStore{
		candidates: []store.EllieEntitySynthesisCandidate{{EntityKey: "itsalive", EntityName: "ItsAlive", MentionCount: 5}},
		sourceMemories: map[string][]store.EllieEntitySynthesisSourceMemory{
			"itsalive": source,
		},
	}

	embedding := make([]float64, 1536)
	worker := NewEllieEntitySynthesisWorker(
		fakeStore,
		&fakeEllieEntityEmbedder{result: [][]float64{embedding}},
		&fakeEllieEntityEmbeddingStore{},
		EllieEntitySynthesisWorkerConfig{
			Synthesizer: &fakeEllieEntitySynthesizer{result: EllieEntitySynthesisOutput{Title: "ItsAlive", Content: "Definition"}},
		},
	)

	_, err := worker.RunOnce(context.Background(), "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	require.NoError(t, err)
	require.Equal(t, before, source)
}
