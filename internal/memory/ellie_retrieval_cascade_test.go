package memory

import (
	"context"
	"sync"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeEllieRetrievalStore struct {
	mu sync.Mutex

	roomResults     []store.EllieRoomContextResult
	projectMem      []store.EllieMemorySearchResult
	orgMem          []store.EllieMemorySearchResult
	chatHistory     []store.EllieChatHistoryResult
	roomCalls       int
	projectMemCalls int
	orgMemCalls     int
	chatCalls       int
}

func (f *fakeEllieRetrievalStore) SearchRoomContext(_ context.Context, _ string, _ string, _ string, _ int) ([]store.EllieRoomContextResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.roomCalls += 1
	out := make([]store.EllieRoomContextResult, len(f.roomResults))
	copy(out, f.roomResults)
	return out, nil
}

func (f *fakeEllieRetrievalStore) SearchMemoriesByProject(_ context.Context, _ string, _ string, _ string, _ int) ([]store.EllieMemorySearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projectMemCalls += 1
	out := make([]store.EllieMemorySearchResult, len(f.projectMem))
	copy(out, f.projectMem)
	return out, nil
}

func (f *fakeEllieRetrievalStore) SearchMemoriesOrgWide(_ context.Context, _ string, _ string, _ int) ([]store.EllieMemorySearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orgMemCalls += 1
	out := make([]store.EllieMemorySearchResult, len(f.orgMem))
	copy(out, f.orgMem)
	return out, nil
}

func (f *fakeEllieRetrievalStore) SearchChatHistory(_ context.Context, _ string, _ string, _ int) ([]store.EllieChatHistoryResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chatCalls += 1
	out := make([]store.EllieChatHistoryResult, len(f.chatHistory))
	copy(out, f.chatHistory)
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
