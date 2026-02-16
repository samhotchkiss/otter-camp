package memory

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type integrationProjectDocsStore struct {
	mu sync.Mutex

	repoRoot    string
	docs        map[string]EllieProjectDocStoreUpsertInput
	active      map[string]bool
	roomResults []store.EllieRoomContextResult
	roomCalls   int
}

func newIntegrationProjectDocsStore(repoRoot string) *integrationProjectDocsStore {
	return &integrationProjectDocsStore{
		repoRoot: repoRoot,
		docs:     make(map[string]EllieProjectDocStoreUpsertInput),
		active:   make(map[string]bool),
	}
}

func (s *integrationProjectDocsStore) ListActiveProjectDocs(
	_ context.Context,
	_ string,
	_ string,
) ([]EllieProjectDocStoreRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows := make([]EllieProjectDocStoreRecord, 0, len(s.docs))
	for filePath, doc := range s.docs {
		if !s.active[filePath] {
			continue
		}
		rows = append(rows, EllieProjectDocStoreRecord{
			FilePath:    filePath,
			ContentHash: doc.ContentHash,
		})
	}
	return rows, nil
}

func (s *integrationProjectDocsStore) UpsertProjectDoc(
	_ context.Context,
	input EllieProjectDocStoreUpsertInput,
) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[input.FilePath] = input
	s.active[input.FilePath] = true
	return input.FilePath, nil
}

func (s *integrationProjectDocsStore) MarkProjectDocsInactiveExcept(
	_ context.Context,
	_ string,
	_ string,
	keepPaths []string,
) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	keep := make(map[string]struct{}, len(keepPaths))
	for _, filePath := range keepPaths {
		keep[filePath] = struct{}{}
	}

	count := 0
	for filePath, isActive := range s.active {
		if !isActive {
			continue
		}
		if _, ok := keep[filePath]; ok {
			continue
		}
		s.active[filePath] = false
		count += 1
	}
	return count, nil
}

func (s *integrationProjectDocsStore) SearchProjectDocsByEmbedding(
	_ context.Context,
	_ string,
	projectID string,
	_ string,
	_ []float64,
	limit int,
) ([]store.EllieProjectDocSearchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	paths := make([]string, 0, len(s.docs))
	for filePath := range s.docs {
		if s.active[filePath] {
			paths = append(paths, filePath)
		}
	}
	sort.Strings(paths)

	if limit > 0 && len(paths) > limit {
		paths = paths[:limit]
	}
	results := make([]store.EllieProjectDocSearchResult, 0, len(paths))
	for _, filePath := range paths {
		doc := s.docs[filePath]
		results = append(results, store.EllieProjectDocSearchResult{
			DocID:         filePath,
			ProjectID:     projectID,
			FilePath:      filePath,
			Title:         doc.Title,
			Summary:       doc.Summary,
			LocalRepoPath: s.repoRoot,
			Similarity:    1,
		})
	}
	return results, nil
}

func (s *integrationProjectDocsStore) SearchRoomContext(
	_ context.Context,
	_ string,
	_ string,
	_ string,
	_ int,
) ([]store.EllieRoomContextResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roomCalls += 1
	out := make([]store.EllieRoomContextResult, len(s.roomResults))
	copy(out, s.roomResults)
	return out, nil
}

func (s *integrationProjectDocsStore) SearchMemoriesByProject(
	_ context.Context,
	_ string,
	_ string,
	_ string,
	_ int,
) ([]store.EllieMemorySearchResult, error) {
	return []store.EllieMemorySearchResult{}, nil
}

func (s *integrationProjectDocsStore) SearchMemoriesOrgWide(
	_ context.Context,
	_ string,
	_ string,
	_ int,
) ([]store.EllieMemorySearchResult, error) {
	return []store.EllieMemorySearchResult{}, nil
}

func (s *integrationProjectDocsStore) SearchChatHistory(
	_ context.Context,
	_ string,
	_ string,
	_ int,
) ([]store.EllieChatHistoryResult, error) {
	return []store.EllieChatHistoryResult{}, nil
}

func (s *integrationProjectDocsStore) SearchMemoriesByProjectWithEmbedding(
	_ context.Context,
	_ string,
	_ string,
	_ string,
	_ []float64,
	_ int,
) ([]store.EllieMemorySearchResult, error) {
	return []store.EllieMemorySearchResult{}, nil
}

func (s *integrationProjectDocsStore) SearchMemoriesOrgWideWithEmbedding(
	_ context.Context,
	_ string,
	_ string,
	_ []float64,
	_ int,
) ([]store.EllieMemorySearchResult, error) {
	return []store.EllieMemorySearchResult{}, nil
}

func (s *integrationProjectDocsStore) SearchChatHistoryWithEmbedding(
	_ context.Context,
	_ string,
	_ string,
	_ []float64,
	_ int,
) ([]store.EllieChatHistoryResult, error) {
	return []store.EllieChatHistoryResult{}, nil
}

func TestProjectDocsIngestionIntegrationScanToRetrieve(t *testing.T) {
	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "docs", "guide.md"), []byte("# Guide\nCanonical project guidance"), 0o644))

	docsStore := newIntegrationProjectDocsStore(projectRoot)
	scanner := &EllieProjectDocsScanner{
		Summarizer:      &fakeProjectDocSummarizer{},
		EmbeddingClient: &fakeProjectDocEmbedder{},
		MaxSectionChars: 1024,
	}

	persistResult, err := scanner.ScanAndPersist(context.Background(), EllieProjectDocsScanAndPersistInput{
		OrgID:       "org-1",
		ProjectID:   "project-1",
		ProjectRoot: projectRoot,
		Store:       docsStore,
	})
	require.NoError(t, err)
	require.Equal(t, 1, persistResult.ProcessedDocs)
	require.Equal(t, 1, persistResult.UpdatedDocs)

	service := NewEllieRetrievalCascadeService(docsStore, nil)
	service.QueryEmbedder = &fakeEllieQueryEmbedder{vectors: [][]float64{{0.8, 0.2}}}

	response, err := service.Retrieve(context.Background(), EllieRetrievalRequest{
		OrgID:     "org-1",
		ProjectID: "project-1",
		Query:     "guide",
		Limit:     5,
	})
	require.NoError(t, err)
	require.False(t, response.NoInformation)
	require.Equal(t, 1, response.TierUsed)
	require.Len(t, response.Items, 1)
	require.Equal(t, "project_doc", response.Items[0].Source)
	require.Contains(t, response.Items[0].Snippet, "Canonical project guidance")
}

func TestProjectDocsRankingBeatsConversationMemories(t *testing.T) {
	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "docs", "architecture.md"), []byte("# Architecture\nAuthoritative architecture direction"), 0o644))

	docsStore := newIntegrationProjectDocsStore(projectRoot)
	docsStore.roomResults = []store.EllieRoomContextResult{
		{
			MessageID: "room-msg-1",
			RoomID:    "room-1",
			Body:      "Room-level memory snippet",
		},
	}

	scanner := &EllieProjectDocsScanner{
		Summarizer:      &fakeProjectDocSummarizer{},
		EmbeddingClient: &fakeProjectDocEmbedder{},
		MaxSectionChars: 1024,
	}
	_, err := scanner.ScanAndPersist(context.Background(), EllieProjectDocsScanAndPersistInput{
		OrgID:       "org-1",
		ProjectID:   "project-1",
		ProjectRoot: projectRoot,
		Store:       docsStore,
	})
	require.NoError(t, err)

	service := NewEllieRetrievalCascadeService(docsStore, nil)
	service.QueryEmbedder = &fakeEllieQueryEmbedder{vectors: [][]float64{{1, 0}}}

	response, err := service.Retrieve(context.Background(), EllieRetrievalRequest{
		OrgID:     "org-1",
		ProjectID: "project-1",
		RoomID:    "room-1",
		Query:     "architecture",
		Limit:     5,
	})
	require.NoError(t, err)
	require.False(t, response.NoInformation)
	require.Equal(t, 1, response.TierUsed)
	require.Equal(t, "project_doc", response.Items[0].Source)
	require.Contains(t, response.Items[0].Snippet, "Authoritative architecture direction")
	require.Equal(t, 0, docsStore.roomCalls)
}

func TestProjectDocsLargeDocumentSectioningIntegration(t *testing.T) {
	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "docs"), 0o755))
	largeContent := strings.Join([]string{
		"# Large",
		"",
		"Section one paragraph that should be split by scanner sectioning.",
		"",
		"Section two paragraph that should also be split by scanner sectioning.",
	}, "\n")
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "docs", "large.md"), []byte(largeContent), 0o644))

	docsStore := newIntegrationProjectDocsStore(projectRoot)
	scanner := &EllieProjectDocsScanner{
		Summarizer:      &fakeProjectDocSummarizer{},
		EmbeddingClient: &fakeProjectDocEmbedder{},
		MaxSectionChars: 40,
	}

	persistResult, err := scanner.ScanAndPersist(context.Background(), EllieProjectDocsScanAndPersistInput{
		OrgID:       "org-1",
		ProjectID:   "project-1",
		ProjectRoot: projectRoot,
		Store:       docsStore,
	})
	require.NoError(t, err)
	require.Equal(t, 1, persistResult.UpdatedDocs)

	largeDoc := docsStore.docs["docs/large.md"]
	require.Contains(t, largeDoc.Summary, "summary:docs/large.md:1")
	require.Contains(t, largeDoc.Summary, "summary:docs/large.md:2")
	require.Len(t, largeDoc.SummaryEmbedding, 1536)
}
