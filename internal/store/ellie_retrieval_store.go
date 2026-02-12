package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type EllieRoomContextResult struct {
	MessageID      string
	RoomID         string
	Body           string
	ConversationID *string
	CreatedAt      time.Time
}

type EllieMemorySearchResult struct {
	MemoryID             string
	Kind                 string
	Title                string
	Content              string
	SourceConversationID *string
	SourceProjectID      *string
	OccurredAt           time.Time
}

type EllieChatHistoryResult struct {
	MessageID      string
	RoomID         string
	Body           string
	ConversationID *string
	CreatedAt      time.Time
}

type EllieRetrievalStore struct {
	db *sql.DB
}

func NewEllieRetrievalStore(db *sql.DB) *EllieRetrievalStore {
	return &EllieRetrievalStore{db: db}
}

func (s *EllieRetrievalStore) SearchRoomContext(ctx context.Context, orgID, roomID, query string, limit int) ([]EllieRoomContextResult, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie retrieval store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	roomID = strings.TrimSpace(roomID)
	query = strings.TrimSpace(query)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(roomID) {
		return nil, fmt.Errorf("invalid room_id")
	}
	if query == "" {
		return []EllieRoomContextResult{}, nil
	}
	if limit <= 0 {
		limit = 10
	}
	// Scaffold implementation: keyword-only matching while semantic/vector retrieval
	// follow-up is tracked in #850.

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, room_id, body, conversation_id::text, created_at
		 FROM chat_messages
		 WHERE org_id = $1
		   AND room_id = $2
		   AND body ILIKE $3
		 ORDER BY created_at DESC, id DESC
		 LIMIT $4`,
		orgID,
		roomID,
		"%"+query+"%",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to search room context: %w", err)
	}
	defer rows.Close()

	results := make([]EllieRoomContextResult, 0, limit)
	for rows.Next() {
		var (
			row            EllieRoomContextResult
			conversationID sql.NullString
		)
		if err := rows.Scan(&row.MessageID, &row.RoomID, &row.Body, &conversationID, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan room context result: %w", err)
		}
		if conversationID.Valid {
			value := strings.TrimSpace(conversationID.String)
			if value != "" {
				row.ConversationID = &value
			}
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading room context results: %w", err)
	}
	return results, nil
}

func (s *EllieRetrievalStore) SearchMemoriesByProject(ctx context.Context, orgID, projectID, query string, limit int) ([]EllieMemorySearchResult, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie retrieval store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	projectID = strings.TrimSpace(projectID)
	query = strings.TrimSpace(query)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}
	if query == "" {
		return []EllieMemorySearchResult{}, nil
	}
	if limit <= 0 {
		limit = 10
	}

	return s.queryMemories(ctx, orgID, query, limit, "source_project_id = $3", projectID)
}

func (s *EllieRetrievalStore) SearchMemoriesOrgWide(ctx context.Context, orgID, query string, limit int) ([]EllieMemorySearchResult, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie retrieval store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	query = strings.TrimSpace(query)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	if query == "" {
		return []EllieMemorySearchResult{}, nil
	}
	if limit <= 0 {
		limit = 10
	}

	return s.queryMemories(ctx, orgID, query, limit, "1=1")
}

func (s *EllieRetrievalStore) queryMemories(
	ctx context.Context,
	orgID,
	query string,
	limit int,
	extraPredicate string,
	extraArgs ...any,
) ([]EllieMemorySearchResult, error) {
	args := []any{orgID, "%" + query + "%"}
	args = append(args, extraArgs...)
	args = append(args, limit)
	limitArg := fmt.Sprintf("$%d", len(args))
	// Scaffold implementation: keyword-only matching while semantic/vector retrieval
	// follow-up is tracked in #850.

	querySQL := `SELECT id, kind, title, content, source_conversation_id::text, source_project_id::text, occurred_at
	 FROM memories
	 WHERE org_id = $1
	   AND status = 'active'
	   AND (` + extraPredicate + `)
	   AND ((title || ' ' || content) ILIKE $2)
	 ORDER BY occurred_at DESC, id DESC
	 LIMIT ` + limitArg

	rows, err := s.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search memories: %w", err)
	}
	defer rows.Close()

	results := make([]EllieMemorySearchResult, 0, limit)
	for rows.Next() {
		var (
			row                  EllieMemorySearchResult
			sourceConversationID sql.NullString
			sourceProjectID      sql.NullString
		)
		if err := rows.Scan(&row.MemoryID, &row.Kind, &row.Title, &row.Content, &sourceConversationID, &sourceProjectID, &row.OccurredAt); err != nil {
			return nil, fmt.Errorf("failed to scan memory search result: %w", err)
		}
		if sourceConversationID.Valid {
			value := strings.TrimSpace(sourceConversationID.String)
			if value != "" {
				row.SourceConversationID = &value
			}
		}
		if sourceProjectID.Valid {
			value := strings.TrimSpace(sourceProjectID.String)
			if value != "" {
				row.SourceProjectID = &value
			}
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading memory search results: %w", err)
	}
	return results, nil
}

func (s *EllieRetrievalStore) SearchChatHistory(ctx context.Context, orgID, query string, limit int) ([]EllieChatHistoryResult, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie retrieval store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	query = strings.TrimSpace(query)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	if query == "" {
		return []EllieChatHistoryResult{}, nil
	}
	if limit <= 0 {
		limit = 10
	}
	// Scaffold implementation: keyword-only matching while semantic/vector retrieval
	// follow-up is tracked in #850.

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, room_id, body, conversation_id::text, created_at
		 FROM chat_messages
		 WHERE org_id = $1
		   AND body ILIKE $2
		 ORDER BY created_at DESC, id DESC
		 LIMIT $3`,
		orgID,
		"%"+query+"%",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to search chat history: %w", err)
	}
	defer rows.Close()

	results := make([]EllieChatHistoryResult, 0, limit)
	for rows.Next() {
		var (
			row            EllieChatHistoryResult
			conversationID sql.NullString
		)
		if err := rows.Scan(&row.MessageID, &row.RoomID, &row.Body, &conversationID, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan chat history result: %w", err)
		}
		if conversationID.Valid {
			value := strings.TrimSpace(conversationID.String)
			if value != "" {
				row.ConversationID = &value
			}
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading chat history results: %w", err)
	}
	return results, nil
}
