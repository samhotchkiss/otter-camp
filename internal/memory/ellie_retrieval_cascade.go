package memory

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

type EllieRetrievalStore interface {
	SearchRoomContext(ctx context.Context, orgID, roomID, query string, limit int) ([]store.EllieRoomContextResult, error)
	SearchMemoriesByProject(ctx context.Context, orgID, projectID, query string, limit int) ([]store.EllieMemorySearchResult, error)
	SearchMemoriesOrgWide(ctx context.Context, orgID, query string, limit int) ([]store.EllieMemorySearchResult, error)
	SearchChatHistory(ctx context.Context, orgID, query string, limit int) ([]store.EllieChatHistoryResult, error)
}

type EllieSemanticRetrievalStore interface {
	SearchMemoriesByProjectWithEmbedding(ctx context.Context, orgID, projectID, query string, queryEmbedding []float64, limit int) ([]store.EllieMemorySearchResult, error)
	SearchMemoriesOrgWideWithEmbedding(ctx context.Context, orgID, query string, queryEmbedding []float64, limit int) ([]store.EllieMemorySearchResult, error)
	SearchChatHistoryWithEmbedding(ctx context.Context, orgID, query string, queryEmbedding []float64, limit int) ([]store.EllieChatHistoryResult, error)
}

type EllieProjectDocSemanticRetrievalStore interface {
	SearchProjectDocsByEmbedding(ctx context.Context, orgID, projectID, query string, queryEmbedding []float64, limit int) ([]store.EllieProjectDocSearchResult, error)
}

type EllieQueryEmbedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float64, error)
}

type EllieJSONLScanInput struct {
	OrgID string
	Query string
	Limit int
}

type EllieJSONLScanner interface {
	Scan(ctx context.Context, input EllieJSONLScanInput) ([]EllieRetrievedItem, error)
}

type EllieRetrievalCascadeService struct {
	Store         EllieRetrievalStore
	JSONLScanner  EllieJSONLScanner
	QualitySink   EllieRetrievalQualitySink
	QueryEmbedder EllieQueryEmbedder
}

type EllieRetrievalRequest struct {
	OrgID             string
	RoomID            string
	ProjectID         string
	Query             string
	Limit             int
	ReferencedItemIDs []string
	MissedItemIDs     []string
}

type EllieRetrievedItem struct {
	Tier           int
	Source         string
	ID             string
	Snippet        string
	RoomID         string
	MemoryID       string
	ConversationID string
	ProjectID      string
}

type EllieRetrievalResponse struct {
	Items         []EllieRetrievedItem
	TierUsed      int
	NoInformation bool
}

type EllieRetrievalQualitySignal struct {
	OrgID             string
	ProjectID         string
	RoomID            string
	Query             string
	TierUsed          int
	InjectedCount     int
	ReferencedCount   int
	MissedCount       int
	NoInformation     bool
	InjectedItemIDs   []string
	ReferencedItemIDs []string
	MissedItemIDs     []string
}

type EllieRetrievalQualitySink interface {
	Record(ctx context.Context, signal EllieRetrievalQualitySignal) error
}

func NewEllieRetrievalCascadeService(store EllieRetrievalStore, scanner EllieJSONLScanner) *EllieRetrievalCascadeService {
	return &EllieRetrievalCascadeService{Store: store, JSONLScanner: scanner}
}

func (s *EllieRetrievalCascadeService) Retrieve(ctx context.Context, request EllieRetrievalRequest) (EllieRetrievalResponse, error) {
	if s == nil || s.Store == nil {
		return EllieRetrievalResponse{}, fmt.Errorf("ellie retrieval store is required")
	}
	orgID := strings.TrimSpace(request.OrgID)
	roomID := strings.TrimSpace(request.RoomID)
	projectID := strings.TrimSpace(request.ProjectID)
	query := strings.TrimSpace(request.Query)
	if orgID == "" {
		return EllieRetrievalResponse{}, fmt.Errorf("org_id is required")
	}
	if query == "" {
		response := EllieRetrievalResponse{TierUsed: 5, NoInformation: true}
		s.emitQualitySignal(ctx, request, response)
		return response, nil
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 5
	}
	queryEmbedding, hasQueryEmbedding := s.getQueryEmbedding(ctx, query)
	semanticStore, semanticStoreOK := s.Store.(EllieSemanticRetrievalStore)
	projectDocStore, projectDocStoreOK := s.Store.(EllieProjectDocSemanticRetrievalStore)

	if projectID != "" && projectDocStoreOK && hasQueryEmbedding {
		projectDocResults, err := projectDocStore.SearchProjectDocsByEmbedding(ctx, orgID, projectID, query, queryEmbedding, limit)
		if err != nil {
			return EllieRetrievalResponse{}, fmt.Errorf("project docs lookup failed: %w", err)
		}
		if len(projectDocResults) > 0 {
			response := EllieRetrievalResponse{
				Items:         mapProjectDocResultsToRetrievedItems(projectDocResults, limit),
				TierUsed:      1,
				NoInformation: false,
			}
			s.emitQualitySignal(ctx, request, response)
			return response, nil
		}
	}

	if roomID != "" {
		roomResults, err := s.Store.SearchRoomContext(ctx, orgID, roomID, query, limit)
		if err != nil {
			return EllieRetrievalResponse{}, fmt.Errorf("tier 1 room context lookup failed: %w", err)
		}
		if len(roomResults) > 0 {
			response := EllieRetrievalResponse{
				Items:         mapRoomResultsToRetrievedItems(roomResults),
				TierUsed:      1,
				NoInformation: false,
			}
			s.emitQualitySignal(ctx, request, response)
			return response, nil
		}
	}

	memoryResults := make([]store.EllieMemorySearchResult, 0, limit)
	if projectID != "" {
		var (
			projectResults []store.EllieMemorySearchResult
			err            error
		)
		if semanticStoreOK && hasQueryEmbedding {
			projectResults, err = semanticStore.SearchMemoriesByProjectWithEmbedding(ctx, orgID, projectID, query, queryEmbedding, limit)
		} else {
			projectResults, err = s.Store.SearchMemoriesByProject(ctx, orgID, projectID, query, limit)
		}
		if err != nil {
			return EllieRetrievalResponse{}, fmt.Errorf("tier 2 project memory lookup failed: %w", err)
		}
		memoryResults = append(memoryResults, projectResults...)
	}

	var (
		orgResults []store.EllieMemorySearchResult
		err        error
	)
	if semanticStoreOK && hasQueryEmbedding {
		orgResults, err = semanticStore.SearchMemoriesOrgWideWithEmbedding(ctx, orgID, query, queryEmbedding, limit)
	} else {
		orgResults, err = s.Store.SearchMemoriesOrgWide(ctx, orgID, query, limit)
	}
	if err != nil {
		return EllieRetrievalResponse{}, fmt.Errorf("tier 2 org memory lookup failed: %w", err)
	}
	memoryResults = append(memoryResults, orgResults...)
	memoryResults = dedupeMemoryResults(memoryResults)
	if limit > 0 && len(memoryResults) > limit {
		memoryResults = memoryResults[:limit]
	}
	if len(memoryResults) > 0 {
		response := EllieRetrievalResponse{
			Items:         mapMemoryResultsToRetrievedItems(memoryResults, limit),
			TierUsed:      2,
			NoInformation: false,
		}
		s.emitQualitySignal(ctx, request, response)
		return response, nil
	}

	var chatResults []store.EllieChatHistoryResult
	if semanticStoreOK && hasQueryEmbedding {
		chatResults, err = semanticStore.SearchChatHistoryWithEmbedding(ctx, orgID, query, queryEmbedding, limit)
	} else {
		chatResults, err = s.Store.SearchChatHistory(ctx, orgID, query, limit)
	}
	if err != nil {
		return EllieRetrievalResponse{}, fmt.Errorf("tier 3 chat history lookup failed: %w", err)
	}
	if len(chatResults) > 0 {
		response := EllieRetrievalResponse{
			Items:         mapChatHistoryResultsToRetrievedItems(chatResults),
			TierUsed:      3,
			NoInformation: false,
		}
		s.emitQualitySignal(ctx, request, response)
		return response, nil
	}

	if s.JSONLScanner != nil {
		jsonlResults, err := s.JSONLScanner.Scan(ctx, EllieJSONLScanInput{OrgID: orgID, Query: query, Limit: limit})
		if err != nil {
			return EllieRetrievalResponse{}, fmt.Errorf("tier 4 jsonl lookup failed: %w", err)
		}
		if len(jsonlResults) > 0 {
			for i := range jsonlResults {
				jsonlResults[i].Tier = 4
				if strings.TrimSpace(jsonlResults[i].Source) == "" {
					jsonlResults[i].Source = "jsonl"
				}
			}
			response := EllieRetrievalResponse{
				Items:         jsonlResults,
				TierUsed:      4,
				NoInformation: false,
			}
			s.emitQualitySignal(ctx, request, response)
			return response, nil
		}
	}

	response := EllieRetrievalResponse{TierUsed: 5, NoInformation: true}
	s.emitQualitySignal(ctx, request, response)
	return response, nil
}

func (s *EllieRetrievalCascadeService) getQueryEmbedding(ctx context.Context, query string) ([]float64, bool) {
	if s == nil || s.QueryEmbedder == nil {
		return nil, false
	}
	embeddings, err := s.QueryEmbedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, false
	}
	if len(embeddings) != 1 || len(embeddings[0]) == 0 {
		return nil, false
	}
	return embeddings[0], true
}

func mapRoomResultsToRetrievedItems(results []store.EllieRoomContextResult) []EllieRetrievedItem {
	items := make([]EllieRetrievedItem, 0, len(results))
	for _, row := range results {
		conversationID := ""
		if row.ConversationID != nil {
			conversationID = strings.TrimSpace(*row.ConversationID)
		}
		items = append(items, EllieRetrievedItem{
			Tier:           1,
			Source:         "room",
			ID:             row.MessageID,
			Snippet:        row.Body,
			RoomID:         row.RoomID,
			ConversationID: conversationID,
		})
	}
	return items
}

func mapMemoryResultsToRetrievedItems(results []store.EllieMemorySearchResult, limit int) []EllieRetrievedItem {
	items := make([]EllieRetrievedItem, 0, len(results))
	for _, row := range results {
		conversationID := ""
		if row.SourceConversationID != nil {
			conversationID = strings.TrimSpace(*row.SourceConversationID)
		}
		projectID := ""
		if row.SourceProjectID != nil {
			projectID = strings.TrimSpace(*row.SourceProjectID)
		}
		items = append(items, EllieRetrievedItem{
			Tier:           2,
			Source:         "memory",
			ID:             row.MemoryID,
			MemoryID:       row.MemoryID,
			Snippet:        strings.TrimSpace(row.Title + ": " + row.Content),
			ConversationID: conversationID,
			ProjectID:      projectID,
		})
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	return items
}

func mapProjectDocResultsToRetrievedItems(results []store.EllieProjectDocSearchResult, limit int) []EllieRetrievedItem {
	items := make([]EllieRetrievedItem, 0, len(results))
	for _, row := range results {
		snippet := strings.TrimSpace(loadProjectDocContent(row.LocalRepoPath, row.FilePath))
		if snippet == "" {
			snippet = strings.TrimSpace(row.Summary)
		}
		if snippet == "" {
			snippet = strings.TrimSpace(row.Title)
		}
		items = append(items, EllieRetrievedItem{
			Tier:      1,
			Source:    "project_doc",
			ID:        row.DocID,
			Snippet:   snippet,
			ProjectID: row.ProjectID,
		})
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	return items
}

func mapChatHistoryResultsToRetrievedItems(results []store.EllieChatHistoryResult) []EllieRetrievedItem {
	items := make([]EllieRetrievedItem, 0, len(results))
	for _, row := range results {
		conversationID := ""
		if row.ConversationID != nil {
			conversationID = strings.TrimSpace(*row.ConversationID)
		}
		items = append(items, EllieRetrievedItem{
			Tier:           3,
			Source:         "chat_history",
			ID:             row.MessageID,
			Snippet:        row.Body,
			RoomID:         row.RoomID,
			ConversationID: conversationID,
		})
	}
	return items
}

func dedupeMemoryResults(results []store.EllieMemorySearchResult) []store.EllieMemorySearchResult {
	seen := make(map[string]struct{}, len(results))
	out := make([]store.EllieMemorySearchResult, 0, len(results))
	for _, row := range results {
		id := strings.TrimSpace(row.MemoryID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, row)
	}
	return out
}

func (s *EllieRetrievalCascadeService) emitQualitySignal(
	ctx context.Context,
	request EllieRetrievalRequest,
	response EllieRetrievalResponse,
) {
	if s == nil || s.QualitySink == nil {
		return
	}

	injectedIDs := make([]string, 0, len(response.Items))
	injectedSet := make(map[string]struct{}, len(response.Items))
	for _, item := range response.Items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, exists := injectedSet[id]; exists {
			continue
		}
		injectedSet[id] = struct{}{}
		injectedIDs = append(injectedIDs, id)
	}

	referencedIDs := dedupeTrimmedIDs(request.ReferencedItemIDs)
	referencedCount := 0
	for _, id := range referencedIDs {
		if _, ok := injectedSet[id]; ok {
			referencedCount += 1
		}
	}
	missedIDs := dedupeTrimmedIDs(request.MissedItemIDs)

	if err := s.QualitySink.Record(ctx, EllieRetrievalQualitySignal{
		OrgID:             strings.TrimSpace(request.OrgID),
		ProjectID:         strings.TrimSpace(request.ProjectID),
		RoomID:            strings.TrimSpace(request.RoomID),
		Query:             strings.TrimSpace(request.Query),
		TierUsed:          response.TierUsed,
		InjectedCount:     len(injectedIDs),
		ReferencedCount:   referencedCount,
		MissedCount:       len(missedIDs),
		NoInformation:     response.NoInformation,
		InjectedItemIDs:   injectedIDs,
		ReferencedItemIDs: referencedIDs,
		MissedItemIDs:     missedIDs,
	}); err != nil {
		log.Printf("warning: ellie retrieval quality sink record failed: %v", err)
	}
}

func dedupeTrimmedIDs(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func loadProjectDocContent(repoRoot, relativePath string) string {
	root := strings.TrimSpace(repoRoot)
	rel := strings.TrimSpace(relativePath)
	if root == "" || rel == "" {
		return ""
	}

	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return ""
	}
	candidate := filepath.Clean(filepath.Join(absRoot, filepath.FromSlash(rel)))
	relCheck, err := filepath.Rel(absRoot, candidate)
	if err != nil {
		return ""
	}
	if relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(os.PathSeparator)) {
		return ""
	}

	content, err := os.ReadFile(candidate)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}
