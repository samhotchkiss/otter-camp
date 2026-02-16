package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeEllieTaxonomyClassifierStore struct {
	nodes          []store.EllieTaxonomyNode
	pending        []store.EllieTaxonomyPendingMemory
	upserts        []store.UpsertEllieMemoryTaxonomyInput
	marked         []fakeEllieTaxonomyMarkCall
	upsertErr      error
	markErr        error
	listNodesErr   error
	listPendingErr error
}

type fakeEllieTaxonomyMarkCall struct {
	OrgID    string
	MemoryID string
	At       time.Time
	Model    string
	TraceID  string
}

func (f *fakeEllieTaxonomyClassifierStore) ListAllNodes(
	_ context.Context,
	_ string,
) ([]store.EllieTaxonomyNode, error) {
	if f.listNodesErr != nil {
		return nil, f.listNodesErr
	}
	out := make([]store.EllieTaxonomyNode, len(f.nodes))
	copy(out, f.nodes)
	return out, nil
}

func (f *fakeEllieTaxonomyClassifierStore) ListPendingMemoriesForClassification(
	_ context.Context,
	_ string,
	_ int,
) ([]store.EllieTaxonomyPendingMemory, error) {
	if f.listPendingErr != nil {
		return nil, f.listPendingErr
	}
	out := make([]store.EllieTaxonomyPendingMemory, len(f.pending))
	copy(out, f.pending)
	return out, nil
}

func (f *fakeEllieTaxonomyClassifierStore) UpsertMemoryClassification(
	_ context.Context,
	input store.UpsertEllieMemoryTaxonomyInput,
) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserts = append(f.upserts, input)
	return nil
}

func (f *fakeEllieTaxonomyClassifierStore) MarkMemoryTaxonomyClassified(
	_ context.Context,
	orgID,
	memoryID string,
	classifiedAt time.Time,
	classifierModel,
	classifierTraceID string,
) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.marked = append(f.marked, fakeEllieTaxonomyMarkCall{
		OrgID:    orgID,
		MemoryID: memoryID,
		At:       classifiedAt,
		Model:    classifierModel,
		TraceID:  classifierTraceID,
	})
	return nil
}

type fakeEllieTaxonomyClassifierLLM struct {
	calls   int
	outputs []EllieTaxonomyLLMClassificationOutput
	errs    []error
}

func (f *fakeEllieTaxonomyClassifierLLM) ClassifyMemory(
	_ context.Context,
	_ EllieTaxonomyLLMClassificationInput,
) (EllieTaxonomyLLMClassificationOutput, error) {
	idx := f.calls
	f.calls += 1
	if idx < len(f.errs) && f.errs[idx] != nil {
		return EllieTaxonomyLLMClassificationOutput{}, f.errs[idx]
	}
	if idx < len(f.outputs) {
		return f.outputs[idx], nil
	}
	return EllieTaxonomyLLMClassificationOutput{}, errors.New("unexpected llm call")
}

func TestTaxonomyClassifierAssignsOneToThreeNodes(t *testing.T) {
	orgID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	rootID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	childID := "cccccccc-cccc-cccc-cccc-cccccccccccc"

	fakeStore := &fakeEllieTaxonomyClassifierStore{
		nodes: []store.EllieTaxonomyNode{
			{ID: rootID, OrgID: orgID, Slug: "projects", Depth: 0},
			{ID: childID, OrgID: orgID, ParentID: &rootID, Slug: "otter-camp", Depth: 1},
		},
		pending: []store.EllieTaxonomyPendingMemory{
			{MemoryID: "dddddddd-dddd-dddd-dddd-dddddddddddd", Title: "Taxonomy memory", Content: "Otter Camp retrieval pipeline"},
		},
	}
	fakeLLM := &fakeEllieTaxonomyClassifierLLM{outputs: []EllieTaxonomyLLMClassificationOutput{
		{
			Model:   "anthropic/claude-3-5-haiku-latest",
			TraceID: "trace-1",
			RawJSON: `{"classifications":[{"path":"projects","confidence":0.88},{"path":"projects/otter-camp","confidence":0.92}]}`,
		},
	}}

	worker := NewEllieTaxonomyClassifierWorker(fakeStore, EllieTaxonomyClassifierWorkerConfig{
		LLM:            fakeLLM,
		CandidateBatch: 10,
		MaxAssignments: 3,
	})

	result, err := worker.RunOnce(context.Background(), orgID)
	require.NoError(t, err)
	require.Equal(t, 1, result.PendingMemories)
	require.Equal(t, 1, result.ClassifiedMemories)
	require.Len(t, fakeStore.upserts, 2)
	require.Len(t, fakeStore.marked, 1)
}

func TestTaxonomyClassifierStoresConfidence(t *testing.T) {
	orgID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	rootID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	childID := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	memoryID := "dddddddd-dddd-dddd-dddd-dddddddddddd"

	fakeStore := &fakeEllieTaxonomyClassifierStore{
		nodes: []store.EllieTaxonomyNode{
			{ID: rootID, OrgID: orgID, Slug: "technical", Depth: 0},
			{ID: childID, OrgID: orgID, ParentID: &rootID, Slug: "embeddings", Depth: 1},
		},
		pending: []store.EllieTaxonomyPendingMemory{
			{MemoryID: memoryID, Title: "Embedding choice", Content: "Use text-embedding-3-small"},
		},
	}
	fakeLLM := &fakeEllieTaxonomyClassifierLLM{outputs: []EllieTaxonomyLLMClassificationOutput{
		{
			Model:   "anthropic/claude-3-5-haiku-latest",
			TraceID: "trace-2",
			RawJSON: `{"classifications":[{"path":"technical/embeddings","confidence":0.37}]}`,
		},
	}}

	worker := NewEllieTaxonomyClassifierWorker(fakeStore, EllieTaxonomyClassifierWorkerConfig{LLM: fakeLLM})

	result, err := worker.RunOnce(context.Background(), orgID)
	require.NoError(t, err)
	require.Equal(t, 1, result.ClassifiedMemories)
	require.Len(t, fakeStore.upserts, 1)
	require.Equal(t, memoryID, fakeStore.upserts[0].MemoryID)
	require.InDelta(t, 0.37, fakeStore.upserts[0].Confidence, 0.00001)
	require.Len(t, fakeStore.marked, 1)
	require.Equal(t, "anthropic/claude-3-5-haiku-latest", fakeStore.marked[0].Model)
	require.Equal(t, "trace-2", fakeStore.marked[0].TraceID)
}

func TestTaxonomyClassifierRejectsUnknownPaths(t *testing.T) {
	orgID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	rootID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	fakeStore := &fakeEllieTaxonomyClassifierStore{
		nodes: []store.EllieTaxonomyNode{
			{ID: rootID, OrgID: orgID, Slug: "projects", Depth: 0},
		},
		pending: []store.EllieTaxonomyPendingMemory{
			{MemoryID: "dddddddd-dddd-dddd-dddd-dddddddddddd", Title: "Unknown path", Content: "Project work"},
		},
	}
	fakeLLM := &fakeEllieTaxonomyClassifierLLM{outputs: []EllieTaxonomyLLMClassificationOutput{
		{
			Model:   "anthropic/claude-3-5-haiku-latest",
			TraceID: "trace-3",
			RawJSON: `{"classifications":[{"path":"projects/does-not-exist","confidence":0.91}]}`,
		},
	}}

	worker := NewEllieTaxonomyClassifierWorker(fakeStore, EllieTaxonomyClassifierWorkerConfig{LLM: fakeLLM})

	result, err := worker.RunOnce(context.Background(), orgID)
	require.NoError(t, err)
	require.Equal(t, 1, result.PendingMemories)
	require.Equal(t, 0, result.ClassifiedMemories)
	require.Equal(t, 1, result.InvalidOutputs)
	require.Empty(t, fakeStore.upserts)
	require.Empty(t, fakeStore.marked)
}
