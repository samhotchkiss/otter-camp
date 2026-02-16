package memory

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeEllieDedupStore struct {
	pairs        []EllieDedupPair
	memories     map[string]EllieDedupReviewMemory
	reviewed     map[string]bool
	recorded     []string
	deprecated   [][]string
	deprecatedBy []*string
	mergeID      string
	mergeCalls   int
	mergeInputs  []EllieDedupMergeDecision
	cursor       *EllieDedupCursorState
	cursorLog    []EllieDedupCursorState
}

func (f *fakeEllieDedupStore) ListCandidatePairs(_ context.Context, _ string, _ float64, _ int) ([]EllieDedupPair, error) {
	out := make([]EllieDedupPair, len(f.pairs))
	copy(out, f.pairs)
	return out, nil
}

func (f *fakeEllieDedupStore) ListMemoriesByIDs(_ context.Context, _ string, memoryIDs []string) ([]EllieDedupReviewMemory, error) {
	rows := make([]EllieDedupReviewMemory, 0, len(memoryIDs))
	for _, memoryID := range memoryIDs {
		if row, ok := f.memories[memoryID]; ok {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].MemoryID < rows[j].MemoryID
	})
	return rows, nil
}

func (f *fakeEllieDedupStore) IsPairReviewed(_ context.Context, _ string, memoryID1, memoryID2 string) (bool, error) {
	if f.reviewed == nil {
		return false, nil
	}
	key := fakeEllieDedupPairKey(memoryID1, memoryID2)
	return f.reviewed[key], nil
}

func (f *fakeEllieDedupStore) RecordReviewedPair(_ context.Context, _ string, memoryID1, memoryID2, _ string) error {
	if f.reviewed == nil {
		f.reviewed = make(map[string]bool)
	}
	key := fakeEllieDedupPairKey(memoryID1, memoryID2)
	f.reviewed[key] = true
	f.recorded = append(f.recorded, key)
	return nil
}

func (f *fakeEllieDedupStore) DeprecateMemories(_ context.Context, _ string, memoryIDs []string, supersededBy *string) error {
	ids := append([]string(nil), memoryIDs...)
	sort.Strings(ids)
	f.deprecated = append(f.deprecated, ids)
	f.deprecatedBy = append(f.deprecatedBy, supersededBy)
	return nil
}

func (f *fakeEllieDedupStore) CreateMergedMemory(_ context.Context, _ string, title, content string, _ []string) (string, error) {
	f.mergeCalls += 1
	f.mergeInputs = append(f.mergeInputs, EllieDedupMergeDecision{Title: title, Content: content})
	if strings.TrimSpace(f.mergeID) == "" {
		return "merged-memory-1", nil
	}
	return f.mergeID, nil
}

func (f *fakeEllieDedupStore) GetCursor(_ context.Context, _ string) (*EllieDedupCursorState, error) {
	if f.cursor == nil {
		return nil, nil
	}
	copyCursor := *f.cursor
	return &copyCursor, nil
}

func (f *fakeEllieDedupStore) UpsertCursor(_ context.Context, _ string, lastClusterKey *string, processedClusters, totalClusters int) error {
	var keyCopy *string
	if lastClusterKey != nil {
		trimmed := strings.TrimSpace(*lastClusterKey)
		if trimmed != "" {
			keyCopy = &trimmed
		}
	}
	next := EllieDedupCursorState{
		LastClusterKey:    keyCopy,
		ProcessedClusters: processedClusters,
		TotalClusters:     totalClusters,
	}
	f.cursor = &next
	f.cursorLog = append(f.cursorLog, next)
	return nil
}

type fakeEllieDedupReviewer struct {
	calls    int
	decision EllieDedupDecision
	dynamic  bool
}

func (f *fakeEllieDedupReviewer) Review(_ context.Context, input EllieDedupReviewInput) (EllieDedupDecision, error) {
	f.calls += 1
	if f.dynamic {
		next := f.decision
		if len(input.Cluster.MemoryIDs) > 0 {
			next.Keep = input.Cluster.MemoryIDs[0]
		}
		return next, nil
	}
	return f.decision, nil
}

func TestEllieDedupWorkerDeprecatesReviewedDuplicates(t *testing.T) {
	store := &fakeEllieDedupStore{
		pairs: []EllieDedupPair{{MemoryID1: "a", MemoryID2: "b", Similarity: 0.92}},
		memories: map[string]EllieDedupReviewMemory{
			"a": {MemoryID: "a", Title: "A", Content: "duplicate fact"},
			"b": {MemoryID: "b", Title: "B", Content: "duplicate fact"},
		},
	}
	reviewer := &fakeEllieDedupReviewer{decision: EllieDedupDecision{Keep: "a", Deprecate: []string{"b"}}}

	worker := NewEllieDedupWorker(store, EllieDedupWorkerConfig{Reviewer: reviewer})
	result, err := worker.RunOnce(context.Background(), "org-1")
	require.NoError(t, err)
	require.Equal(t, 1, result.ClustersReviewed)
	require.Equal(t, 1, result.MemoriesDeprecated)
	require.Equal(t, 1, reviewer.calls)
	require.Len(t, store.deprecated, 1)
	require.Equal(t, []string{"b"}, store.deprecated[0])
	require.Len(t, store.recorded, 1)
	require.Equal(t, "a|b", store.recorded[0])
}

func TestEllieDedupWorkerMergeCreatesReplacementAndDeprecatesOriginals(t *testing.T) {
	store := &fakeEllieDedupStore{
		mergeID: "merged-42",
		pairs:   []EllieDedupPair{{MemoryID1: "a", MemoryID2: "b", Similarity: 0.95}},
		memories: map[string]EllieDedupReviewMemory{
			"a": {MemoryID: "a", Title: "A", Content: "variant one"},
			"b": {MemoryID: "b", Title: "B", Content: "variant two"},
		},
	}
	reviewer := &fakeEllieDedupReviewer{decision: EllieDedupDecision{
		Deprecate: []string{"a", "b"},
		Merge:     &EllieDedupMergeDecision{Title: "Merged", Content: "combined memory"},
	}}

	worker := NewEllieDedupWorker(store, EllieDedupWorkerConfig{Reviewer: reviewer})
	result, err := worker.RunOnce(context.Background(), "org-1")
	require.NoError(t, err)
	require.Equal(t, 1, result.MergesCreated)
	require.Equal(t, 2, result.MemoriesDeprecated)
	require.Equal(t, 1, store.mergeCalls)
	require.Len(t, store.deprecated, 1)
	require.Equal(t, []string{"a", "b"}, store.deprecated[0])
	require.NotNil(t, store.deprecatedBy[0])
	require.Equal(t, "merged-42", *store.deprecatedBy[0])
}

func TestEllieDedupWorkerSkipsPreviouslyReviewedPairs(t *testing.T) {
	store := &fakeEllieDedupStore{
		reviewed: map[string]bool{"a|b": true},
		pairs:    []EllieDedupPair{{MemoryID1: "a", MemoryID2: "b", Similarity: 0.92}},
		memories: map[string]EllieDedupReviewMemory{
			"a": {MemoryID: "a", Title: "A", Content: "duplicate fact"},
			"b": {MemoryID: "b", Title: "B", Content: "duplicate fact"},
		},
	}
	reviewer := &fakeEllieDedupReviewer{decision: EllieDedupDecision{Keep: "a", Deprecate: []string{"b"}}}

	worker := NewEllieDedupWorker(store, EllieDedupWorkerConfig{Reviewer: reviewer})
	result, err := worker.RunOnce(context.Background(), "org-1")
	require.NoError(t, err)
	require.Equal(t, 0, result.ClustersReviewed)
	require.Equal(t, 0, reviewer.calls)
	require.Empty(t, store.deprecated)
	require.Empty(t, store.recorded)
}

func TestEllieDedupWorkerResumesFromCursorAfterInterruption(t *testing.T) {
	store := &fakeEllieDedupStore{
		pairs: []EllieDedupPair{
			{MemoryID1: "a", MemoryID2: "b", Similarity: 0.92},
			{MemoryID1: "c", MemoryID2: "d", Similarity: 0.93},
		},
		memories: map[string]EllieDedupReviewMemory{
			"a": {MemoryID: "a", Title: "A", Content: "dup"},
			"b": {MemoryID: "b", Title: "B", Content: "dup"},
			"c": {MemoryID: "c", Title: "C", Content: "dup"},
			"d": {MemoryID: "d", Title: "D", Content: "dup"},
		},
	}
	reviewer := &fakeEllieDedupReviewer{decision: EllieDedupDecision{Deprecate: []string{}}, dynamic: true}
	worker := NewEllieDedupWorker(store, EllieDedupWorkerConfig{
		Reviewer:          reviewer,
		MaxClustersPerRun: 1,
	})

	first, err := worker.RunOnce(context.Background(), "org-1")
	require.NoError(t, err)
	require.Equal(t, 1, first.ClustersReviewed)
	require.NotNil(t, store.cursor)
	require.NotNil(t, store.cursor.LastClusterKey)
	firstKey := *store.cursor.LastClusterKey
	require.NotEmpty(t, firstKey)

	second, err := worker.RunOnce(context.Background(), "org-1")
	require.NoError(t, err)
	require.Equal(t, 1, second.ClustersReviewed)
	require.Equal(t, 2, reviewer.calls)
	require.NotNil(t, store.cursor)
	require.Equal(t, 2, store.cursor.ProcessedClusters)
}

func TestEllieDedupWorkerProgressReportingIsMonotonic(t *testing.T) {
	store := &fakeEllieDedupStore{
		pairs: []EllieDedupPair{
			{MemoryID1: "a", MemoryID2: "b", Similarity: 0.92},
			{MemoryID1: "c", MemoryID2: "d", Similarity: 0.93},
		},
		memories: map[string]EllieDedupReviewMemory{
			"a": {MemoryID: "a", Title: "A", Content: "dup"},
			"b": {MemoryID: "b", Title: "B", Content: "dup"},
			"c": {MemoryID: "c", Title: "C", Content: "dup"},
			"d": {MemoryID: "d", Title: "D", Content: "dup"},
		},
	}
	reviewer := &fakeEllieDedupReviewer{decision: EllieDedupDecision{Deprecate: []string{}}, dynamic: true}
	worker := NewEllieDedupWorker(store, EllieDedupWorkerConfig{Reviewer: reviewer})

	_, err := worker.RunOnce(context.Background(), "org-1")
	require.NoError(t, err)
	require.NotEmpty(t, store.cursorLog)

	prev := -1
	for _, entry := range store.cursorLog {
		require.GreaterOrEqual(t, entry.ProcessedClusters, prev)
		prev = entry.ProcessedClusters
	}
	last := store.cursorLog[len(store.cursorLog)-1]
	require.Equal(t, 2, last.ProcessedClusters)
	require.Equal(t, 2, last.TotalClusters)
}

func fakeEllieDedupPairKey(memoryID1, memoryID2 string) string {
	id1, id2 := ellieDedupCanonicalPair(memoryID1, memoryID2)
	return id1 + "|" + id2
}
