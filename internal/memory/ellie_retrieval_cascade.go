package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

type EllieRetrievalStore interface {
	SearchRoomContext(ctx context.Context, orgID, roomID, query string, limit int) ([]store.EllieRoomContextResult, error)
	SearchMemoriesByProject(ctx context.Context, orgID, projectID, query string, limit int) ([]store.EllieMemorySearchResult, error)
	SearchMemoriesOrgWide(ctx context.Context, orgID, query string, limit int) ([]store.EllieMemorySearchResult, error)
	SearchChatHistory(ctx context.Context, orgID, query string, limit int) ([]store.EllieChatHistoryResult, error)
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
	Store        EllieRetrievalStore
	JSONLScanner EllieJSONLScanner
}

type EllieRetrievalRequest struct {
	OrgID     string
	RoomID    string
	ProjectID string
	Query     string
	Limit     int
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
		return EllieRetrievalResponse{TierUsed: 5, NoInformation: true}, nil
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 5
	}

	if roomID != "" {
		roomResults, err := s.Store.SearchRoomContext(ctx, orgID, roomID, query, limit)
		if err != nil {
			return EllieRetrievalResponse{}, fmt.Errorf("tier 1 room context lookup failed: %w", err)
		}
		if len(roomResults) > 0 {
			return EllieRetrievalResponse{
				Items:         mapRoomResultsToRetrievedItems(roomResults),
				TierUsed:      1,
				NoInformation: false,
			}, nil
		}
	}

	memoryResults := make([]store.EllieMemorySearchResult, 0, limit)
	if projectID != "" {
		projectResults, err := s.Store.SearchMemoriesByProject(ctx, orgID, projectID, query, limit)
		if err != nil {
			return EllieRetrievalResponse{}, fmt.Errorf("tier 2 project memory lookup failed: %w", err)
		}
		memoryResults = append(memoryResults, projectResults...)
	}

	orgResults, err := s.Store.SearchMemoriesOrgWide(ctx, orgID, query, limit)
	if err != nil {
		return EllieRetrievalResponse{}, fmt.Errorf("tier 2 org memory lookup failed: %w", err)
	}
	memoryResults = append(memoryResults, orgResults...)
	memoryResults = dedupeMemoryResults(memoryResults)
	if len(memoryResults) > 0 {
		return EllieRetrievalResponse{
			Items:         mapMemoryResultsToRetrievedItems(memoryResults, limit),
			TierUsed:      2,
			NoInformation: false,
		}, nil
	}

	chatResults, err := s.Store.SearchChatHistory(ctx, orgID, query, limit)
	if err != nil {
		return EllieRetrievalResponse{}, fmt.Errorf("tier 3 chat history lookup failed: %w", err)
	}
	if len(chatResults) > 0 {
		return EllieRetrievalResponse{
			Items:         mapChatHistoryResultsToRetrievedItems(chatResults),
			TierUsed:      3,
			NoInformation: false,
		}, nil
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
			return EllieRetrievalResponse{
				Items:         jsonlResults,
				TierUsed:      4,
				NoInformation: false,
			}, nil
		}
	}

	return EllieRetrievalResponse{TierUsed: 5, NoInformation: true}, nil
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
