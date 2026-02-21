package memory

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeEllieRetrievalStore struct {
	mu sync.Mutex

	roomResults          []store.EllieRoomContextResult
	projectMem           []store.EllieMemorySearchResult
	orgMem               []store.EllieMemorySearchResult
	chatHistory          []store.EllieChatHistoryResult
	semanticProjectMem   []store.EllieMemorySearchResult
	semanticOrgMem       []store.EllieMemorySearchResult
	semanticChat         []store.EllieChatHistoryResult
	roomCalls            int
	projectMemCalls      int
	orgMemCalls          int
	chatCalls            int
	semanticProjectCalls int
	semanticOrgCalls     int
	semanticChatCalls    int
	lastSemanticVector   []float64
	callLog              *[]string
}

func (f *fakeEllieRetrievalStore) SearchRoomContext(_ context.Context, _ string, _ string, _ string, _ int) ([]store.EllieRoomContextResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.roomCalls += 1
	if f.callLog != nil {
		*f.callLog = append(*f.callLog, "room")
	}
	out := make([]store.EllieRoomContextResult, len(f.roomResults))
	copy(out, f.roomResults)
	return out, nil
}

func (f *fakeEllieRetrievalStore) SearchMemoriesByProject(_ context.Context, _ string, _ string, _ string, _ int) ([]store.EllieMemorySearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projectMemCalls += 1
	if f.callLog != nil {
		*f.callLog = append(*f.callLog, "vector_project")
	}
	out := make([]store.EllieMemorySearchResult, len(f.projectMem))
	copy(out, f.projectMem)
	return out, nil
}

func (f *fakeEllieRetrievalStore) SearchMemoriesOrgWide(_ context.Context, _ string, _ string, _ int) ([]store.EllieMemorySearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orgMemCalls += 1
	if f.callLog != nil {
		*f.callLog = append(*f.callLog, "vector_org")
	}
	out := make([]store.EllieMemorySearchResult, len(f.orgMem))
	copy(out, f.orgMem)
	return out, nil
}

func (f *fakeEllieRetrievalStore) SearchChatHistory(_ context.Context, _ string, _ string, _ int) ([]store.EllieChatHistoryResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chatCalls += 1
	if f.callLog != nil {
		*f.callLog = append(*f.callLog, "chat")
	}
	out := make([]store.EllieChatHistoryResult, len(f.chatHistory))
	copy(out, f.chatHistory)
	return out, nil
}

func (f *fakeEllieRetrievalStore) SearchMemoriesByProjectWithEmbedding(_ context.Context, _ string, _ string, _ string, queryEmbedding []float64, _ int) ([]store.EllieMemorySearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.semanticProjectCalls += 1
	if f.callLog != nil {
		*f.callLog = append(*f.callLog, "vector_project_semantic")
	}
	f.lastSemanticVector = append([]float64(nil), queryEmbedding...)
	source := f.semanticProjectMem
	if len(source) == 0 {
		source = f.projectMem
	}
	out := make([]store.EllieMemorySearchResult, len(source))
	copy(out, source)
	return out, nil
}

func (f *fakeEllieRetrievalStore) SearchMemoriesOrgWideWithEmbedding(_ context.Context, _ string, _ string, queryEmbedding []float64, _ int) ([]store.EllieMemorySearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.semanticOrgCalls += 1
	if f.callLog != nil {
		*f.callLog = append(*f.callLog, "vector_org_semantic")
	}
	f.lastSemanticVector = append([]float64(nil), queryEmbedding...)
	source := f.semanticOrgMem
	if len(source) == 0 {
		source = f.orgMem
	}
	out := make([]store.EllieMemorySearchResult, len(source))
	copy(out, source)
	return out, nil
}

func (f *fakeEllieRetrievalStore) SearchChatHistoryWithEmbedding(_ context.Context, _ string, _ string, queryEmbedding []float64, _ int) ([]store.EllieChatHistoryResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.semanticChatCalls += 1
	if f.callLog != nil {
		*f.callLog = append(*f.callLog, "chat_semantic")
	}
	f.lastSemanticVector = append([]float64(nil), queryEmbedding...)
	source := f.semanticChat
	if len(source) == 0 {
		source = f.chatHistory
	}
	out := make([]store.EllieChatHistoryResult, len(source))
	copy(out, source)
	return out, nil
}

type fakeEllieJSONLScanner struct {
	results []EllieRetrievedItem
	calls   int
}

func (f *fakeEllieJSONLScanner) Scan(_ context.Context, _ EllieJSONLScanInput) ([]EllieRetrievedItem, error) {
	f.calls += 1
	out := make([]EllieRetrievedItem, len(f.results))
	copy(out, f.results)
	return out, nil
}

type fakeEllieRetrievalQualitySink struct {
	calls  int
	events []EllieRetrievalQualitySignal
	err    error
}

func (f *fakeEllieRetrievalQualitySink) Record(_ context.Context, signal EllieRetrievalQualitySignal) error {
	f.calls += 1
	f.events = append(f.events, signal)
	return f.err
}

type fakeEllieQueryEmbedder struct {
	vectors [][]float64
	err     error
	calls   int
}

type fakeEllieTaxonomyQueryClassifier struct {
	calls   int
	result  []EllieTaxonomyQueryClassification
	err     error
	callLog *[]string
}

func (f *fakeEllieTaxonomyQueryClassifier) ClassifyQuery(
	_ context.Context,
	_ EllieTaxonomyQueryClassificationInput,
) ([]EllieTaxonomyQueryClassification, error) {
	f.calls += 1
	if f.callLog != nil {
		*f.callLog = append(*f.callLog, "taxonomy_classify")
	}
	if f.err != nil {
		return nil, f.err
	}
	out := make([]EllieTaxonomyQueryClassification, len(f.result))
	copy(out, f.result)
	return out, nil
}

type fakeEllieTaxonomyRetrievalStore struct {
	nodes            []store.EllieTaxonomyNode
	subtreeMemories  map[string][]store.EllieTaxonomySubtreeMemory
	listNodesCalls   int
	listSubtreeCalls int
	callLog          *[]string
}

func (f *fakeEllieTaxonomyRetrievalStore) ListAllNodes(_ context.Context, _ string) ([]store.EllieTaxonomyNode, error) {
	f.listNodesCalls += 1
	if f.callLog != nil {
		*f.callLog = append(*f.callLog, "taxonomy_nodes")
	}
	out := make([]store.EllieTaxonomyNode, len(f.nodes))
	copy(out, f.nodes)
	return out, nil
}

func (f *fakeEllieTaxonomyRetrievalStore) ListMemoriesBySubtree(
	_ context.Context,
	_ string,
	nodeID string,
	_ int,
) ([]store.EllieTaxonomySubtreeMemory, error) {
	f.listSubtreeCalls += 1
	if f.callLog != nil {
		*f.callLog = append(*f.callLog, "taxonomy_subtree")
	}
	source := f.subtreeMemories[nodeID]
	out := make([]store.EllieTaxonomySubtreeMemory, len(source))
	copy(out, source)
	return out, nil
}

func (f *fakeEllieQueryEmbedder) Embed(_ context.Context, _ []string) ([][]float64, error) {
	f.calls += 1
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float64, 0, len(f.vectors))
	for _, vector := range f.vectors {
		out = append(out, append([]float64(nil), vector...))
	}
	return out, nil
}

func TestEllieRetrievalCascadeUsesRoomThenMemoryThenChatThenJSONL(t *testing.T) {
	retrievalStore := &fakeEllieRetrievalStore{}
	jsonlScanner := &fakeEllieJSONLScanner{results: []EllieRetrievedItem{{Tier: 4, Source: "jsonl", ID: "line-1", Snippet: "from jsonl"}}}

	service := NewEllieRetrievalCascadeService(retrievalStore, jsonlScanner)

	response, err := service.Retrieve(context.Background(), EllieRetrievalRequest{
		OrgID:     "org-1",
		RoomID:    "room-1",
		ProjectID: "project-1",
		Query:     "database preference",
		Limit:     5,
	})
	require.NoError(t, err)
	require.False(t, response.NoInformation)
	require.Equal(t, 4, response.TierUsed)
	require.Len(t, response.Items, 1)
	require.Equal(t, "jsonl", response.Items[0].Source)
	require.Equal(t, 1, retrievalStore.roomCalls)
	require.Equal(t, 1, retrievalStore.projectMemCalls)
	require.Equal(t, 1, retrievalStore.orgMemCalls)
	require.Equal(t, 1, retrievalStore.chatCalls)
	require.Equal(t, 1, jsonlScanner.calls)
}

func TestEllieRetrievalCascadeReturnsNoInformationWhenAllTiersMiss(t *testing.T) {
	retrievalStore := &fakeEllieRetrievalStore{}
	jsonlScanner := &fakeEllieJSONLScanner{}

	service := NewEllieRetrievalCascadeService(retrievalStore, jsonlScanner)

	response, err := service.Retrieve(context.Background(), EllieRetrievalRequest{
		OrgID:  "org-1",
		RoomID: "room-1",
		Query:  "missing topic",
		Limit:  3,
	})
	require.NoError(t, err)
	require.True(t, response.NoInformation)
	require.Equal(t, 5, response.TierUsed)
	require.Empty(t, response.Items)
	require.Equal(t, 1, retrievalStore.roomCalls)
	require.Equal(t, 1, retrievalStore.orgMemCalls)
	require.Equal(t, 1, retrievalStore.chatCalls)
	require.Equal(t, 1, jsonlScanner.calls)
}

func TestEllieRetrievalServiceEmitsQualitySignals(t *testing.T) {
	retrievalStore := &fakeEllieRetrievalStore{
		orgMem: []store.EllieMemorySearchResult{
			{
				MemoryID: "mem-1",
				Title:    "Database Choice",
				Content:  "Use Postgres for deterministic migrations.",
			},
			{
				MemoryID: "mem-2",
				Title:    "Deploy Rule",
				Content:  "Always run smoke checks before cutover.",
			},
		},
	}
	qualitySink := &fakeEllieRetrievalQualitySink{}
	service := NewEllieRetrievalCascadeService(retrievalStore, nil)
	service.QualitySink = qualitySink

	response, err := service.Retrieve(context.Background(), EllieRetrievalRequest{
		OrgID:             "org-1",
		ProjectID:         "project-1",
		RoomID:            "room-1",
		Query:             "deployment checklist",
		Limit:             5,
		ReferencedItemIDs: []string{"mem-1"},
		MissedItemIDs:     []string{"mem-3"},
	})
	require.NoError(t, err)
	require.False(t, response.NoInformation)
	require.Equal(t, 2, response.TierUsed)

	require.Equal(t, 1, qualitySink.calls)
	require.Len(t, qualitySink.events, 1)
	require.Equal(t, "org-1", qualitySink.events[0].OrgID)
	require.Equal(t, "project-1", qualitySink.events[0].ProjectID)
	require.Equal(t, "room-1", qualitySink.events[0].RoomID)
	require.Equal(t, 2, qualitySink.events[0].TierUsed)
	require.Equal(t, 2, qualitySink.events[0].InjectedCount)
	require.Equal(t, 1, qualitySink.events[0].ReferencedCount)
	require.Equal(t, 1, qualitySink.events[0].MissedCount)
	require.False(t, qualitySink.events[0].NoInformation)
}

func TestEllieRetrievalServiceLogsQualitySinkErrors(t *testing.T) {
	retrievalStore := &fakeEllieRetrievalStore{
		orgMem: []store.EllieMemorySearchResult{
			{
				MemoryID: "mem-1",
				Title:    "Database Choice",
				Content:  "Use Postgres.",
			},
		},
	}
	qualitySink := &fakeEllieRetrievalQualitySink{err: errors.New("sink unavailable")}
	service := NewEllieRetrievalCascadeService(retrievalStore, nil)
	service.QualitySink = qualitySink

	var logBuffer bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&logBuffer)
	t.Cleanup(func() { log.SetOutput(originalWriter) })

	response, err := service.Retrieve(context.Background(), EllieRetrievalRequest{
		OrgID: "org-1",
		Query: "database choice",
		Limit: 1,
	})
	require.NoError(t, err)
	require.False(t, response.NoInformation)
	require.Equal(t, 2, response.TierUsed)
	require.Equal(t, 1, qualitySink.calls)

	logOutput := strings.ToLower(logBuffer.String())
	require.Contains(t, logOutput, "quality sink")
	require.Contains(t, logOutput, "sink unavailable")
}

func TestEllieRetrievalCascadeTierTwoNeverReturnsOverLimitAfterDedupe(t *testing.T) {
	retrievalStore := &fakeEllieRetrievalStore{
		projectMem: []store.EllieMemorySearchResult{
			{MemoryID: "mem-1", Title: "one", Content: "project"},
			{MemoryID: "mem-2", Title: "two", Content: "project"},
			{MemoryID: "mem-3", Title: "three", Content: "project"},
		},
		orgMem: []store.EllieMemorySearchResult{
			{MemoryID: "mem-2", Title: "two-org", Content: "org"},
			{MemoryID: "mem-4", Title: "four", Content: "org"},
			{MemoryID: "mem-5", Title: "five", Content: "org"},
			{MemoryID: "mem-6", Title: "six", Content: "org"},
		},
	}
	service := NewEllieRetrievalCascadeService(retrievalStore, nil)

	response, err := service.Retrieve(context.Background(), EllieRetrievalRequest{
		OrgID:     "org-1",
		ProjectID: "project-1",
		Query:     "database",
		Limit:     4,
	})
	require.NoError(t, err)
	require.Equal(t, 2, response.TierUsed)
	require.Len(t, response.Items, 4)
}

func TestEllieRetrievalCascadeUsesSemanticStoreResults(t *testing.T) {
	retrievalStore := &fakeEllieRetrievalStore{
		semanticOrgMem: []store.EllieMemorySearchResult{
			{
				MemoryID: "semantic-1",
				Title:    "Storage Choice",
				Content:  "The team chose Postgres.",
			},
		},
	}
	embedder := &fakeEllieQueryEmbedder{vectors: [][]float64{{0.8, 0.1, 0.2}}}
	service := NewEllieRetrievalCascadeService(retrievalStore, nil)
	service.QueryEmbedder = embedder

	response, err := service.Retrieve(context.Background(), EllieRetrievalRequest{
		OrgID: "org-1",
		Query: "database choice",
		Limit: 5,
	})
	require.NoError(t, err)
	require.False(t, response.NoInformation)
	require.Equal(t, 2, response.TierUsed)
	require.Len(t, response.Items, 1)
	require.Equal(t, "semantic-1", response.Items[0].ID)
	require.Equal(t, 1, embedder.calls)
	require.Equal(t, 1, retrievalStore.semanticOrgCalls)
	require.Equal(t, 0, retrievalStore.orgMemCalls)
	require.Equal(t, []float64{0.8, 0.1, 0.2}, retrievalStore.lastSemanticVector)
}

func TestEllieRetrievalCascadeFallsBackWhenQueryEmbeddingUnavailable(t *testing.T) {
	retrievalStore := &fakeEllieRetrievalStore{
		orgMem: []store.EllieMemorySearchResult{
			{
				MemoryID: "keyword-1",
				Title:    "Database Choice",
				Content:  "Use Postgres for production.",
			},
		},
	}
	embedder := &fakeEllieQueryEmbedder{err: errors.New("embedder unavailable")}
	service := NewEllieRetrievalCascadeService(retrievalStore, nil)
	service.QueryEmbedder = embedder

	response, err := service.Retrieve(context.Background(), EllieRetrievalRequest{
		OrgID: "org-1",
		Query: "database choice",
		Limit: 5,
	})
	require.NoError(t, err)
	require.False(t, response.NoInformation)
	require.Equal(t, 2, response.TierUsed)
	require.Len(t, response.Items, 1)
	require.Equal(t, "keyword-1", response.Items[0].ID)
	require.Equal(t, 1, embedder.calls)
	require.Equal(t, 1, retrievalStore.orgMemCalls)
	require.Equal(t, 0, retrievalStore.semanticOrgCalls)
}

func TestRetrievalCascadeIncludesTaxonomyTier(t *testing.T) {
	orgID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	rootID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	childID := "cccccccc-cccc-cccc-cccc-cccccccccccc"

	retrievalStore := &fakeEllieRetrievalStore{}
	taxonomyStore := &fakeEllieTaxonomyRetrievalStore{
		nodes: []store.EllieTaxonomyNode{
			{ID: rootID, OrgID: orgID, Slug: "projects"},
			{ID: childID, OrgID: orgID, ParentID: &rootID, Slug: "otter-camp"},
		},
		subtreeMemories: map[string][]store.EllieTaxonomySubtreeMemory{
			childID: {
				{MemoryID: "taxonomy-mem-1", Title: "Taxonomy memory", Content: "Found via taxonomy subtree", Kind: "fact"},
			},
		},
	}
	taxonomyClassifier := &fakeEllieTaxonomyQueryClassifier{result: []EllieTaxonomyQueryClassification{
		{Path: "projects/otter-camp", Confidence: 0.91},
	}}

	service := NewEllieRetrievalCascadeService(retrievalStore, nil)
	service.TaxonomyStore = taxonomyStore
	service.TaxonomyQueryClassifier = taxonomyClassifier

	response, err := service.Retrieve(context.Background(), EllieRetrievalRequest{
		OrgID: orgID,
		Query: "what happened in otter camp",
		Limit: 5,
	})
	require.NoError(t, err)
	require.False(t, response.NoInformation)
	require.Equal(t, 2, response.TierUsed)
	require.Len(t, response.Items, 1)
	require.Equal(t, "taxonomy-mem-1", response.Items[0].ID)
	require.Equal(t, 1, taxonomyClassifier.calls)
	require.Equal(t, 1, taxonomyStore.listNodesCalls)
	require.Equal(t, 1, taxonomyStore.listSubtreeCalls)
}

func TestRetrievalCascadeTaxonomyAfterVectorBeforeFallback(t *testing.T) {
	orgID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	rootID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	callLog := make([]string, 0, 8)

	retrievalStore := &fakeEllieRetrievalStore{
		chatHistory: []store.EllieChatHistoryResult{
			{MessageID: "chat-1", RoomID: "room-1", Body: "fallback"},
		},
		callLog: &callLog,
	}
	taxonomyStore := &fakeEllieTaxonomyRetrievalStore{
		nodes: []store.EllieTaxonomyNode{
			{ID: rootID, OrgID: orgID, Slug: "projects"},
		},
		subtreeMemories: map[string][]store.EllieTaxonomySubtreeMemory{},
		callLog:         &callLog,
	}
	taxonomyClassifier := &fakeEllieTaxonomyQueryClassifier{
		result:  []EllieTaxonomyQueryClassification{{Path: "projects", Confidence: 0.5}},
		callLog: &callLog,
	}

	service := NewEllieRetrievalCascadeService(retrievalStore, nil)
	service.TaxonomyStore = taxonomyStore
	service.TaxonomyQueryClassifier = taxonomyClassifier

	response, err := service.Retrieve(context.Background(), EllieRetrievalRequest{
		OrgID: orgID,
		Query: "missing memory",
		Limit: 5,
	})
	require.NoError(t, err)
	require.False(t, response.NoInformation)
	require.Equal(t, 3, response.TierUsed)
	require.Equal(t, []string{"vector_org", "taxonomy_nodes", "taxonomy_classify", "taxonomy_subtree", "chat"}, callLog)
}

func TestRetrievalCascadeDedupesVectorAndTaxonomyResults(t *testing.T) {
	orgID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	rootID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	retrievalStore := &fakeEllieRetrievalStore{
		orgMem: []store.EllieMemorySearchResult{
			{MemoryID: "mem-1", Title: "vector 1", Content: "vector"},
			{MemoryID: "mem-2", Title: "vector 2", Content: "vector"},
		},
	}
	taxonomyStore := &fakeEllieTaxonomyRetrievalStore{
		nodes: []store.EllieTaxonomyNode{{ID: rootID, OrgID: orgID, Slug: "projects"}},
		subtreeMemories: map[string][]store.EllieTaxonomySubtreeMemory{
			rootID: {
				{MemoryID: "mem-2", Title: "taxonomy duplicate", Content: "taxonomy", Kind: "fact"},
				{MemoryID: "mem-3", Title: "taxonomy unique", Content: "taxonomy", Kind: "fact"},
			},
		},
	}
	taxonomyClassifier := &fakeEllieTaxonomyQueryClassifier{result: []EllieTaxonomyQueryClassification{
		{Path: "projects", Confidence: 0.72},
	}}

	service := NewEllieRetrievalCascadeService(retrievalStore, nil)
	service.TaxonomyStore = taxonomyStore
	service.TaxonomyQueryClassifier = taxonomyClassifier

	response, err := service.Retrieve(context.Background(), EllieRetrievalRequest{
		OrgID: orgID,
		Query: "project context",
		Limit: 10,
	})
	require.NoError(t, err)
	require.Equal(t, 2, response.TierUsed)
	require.Len(t, response.Items, 3)
	require.Equal(t, "mem-1", response.Items[0].ID)
	require.Equal(t, "mem-2", response.Items[1].ID)
	require.Equal(t, "mem-3", response.Items[2].ID)
}
