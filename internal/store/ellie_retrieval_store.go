package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type EllieRetrievedMemory struct {
	ID             string
	OrgID          string
	Kind           string
	Title          string
	Content        string
	Sensitivity    string
	SourceProject  *string
	ConversationID *string
	OccurredAt     time.Time
	CreatedAt      time.Time
}

type EllieRetrievedConversation struct {
	ID          string
	OrgID       string
	RoomID      string
	Topic       string
	Sensitivity string
	StartedAt   time.Time
	EndedAt     *time.Time
	CreatedAt   time.Time
}

type EllieRoomConversationHistoryItem struct {
	MessageID               string
	ConversationID          *string
	Body                    string
	CreatedAt               time.Time
	ConversationSensitivity *string
}

type EllieRetrievalStore struct {
	db *sql.DB
}

func NewEllieRetrievalStore(db *sql.DB) *EllieRetrievalStore {
	return &EllieRetrievalStore{db: db}
}

func (s *EllieRetrievalStore) ListMemoriesForOrg(ctx context.Context, orgID string, limit int) ([]EllieRetrievedMemory, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie retrieval store is not configured")
	}
	normalizedOrgID, queryLimit, err := normalizeEllieScopedQuery(orgID, limit)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			id, org_id, kind, title, content, sensitivity, source_project_id, source_conversation_id, occurred_at, created_at
		 FROM memories
		 WHERE org_id = $1
		 ORDER BY occurred_at DESC, created_at DESC, id DESC
		 LIMIT $2`,
		normalizedOrgID,
		queryLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list ellie memories for org: %w", err)
	}
	defer rows.Close()

	memories := make([]EllieRetrievedMemory, 0, queryLimit)
	for rows.Next() {
		entry, scanErr := scanEllieRetrievedMemory(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan ellie memory: %w", scanErr)
		}
		memories = append(memories, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading ellie memory rows: %w", err)
	}
	return memories, nil
}

func (s *EllieRetrievalStore) ListProjectMemories(ctx context.Context, orgID, projectID string, limit int) ([]EllieRetrievedMemory, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie retrieval store is not configured")
	}
	normalizedOrgID, queryLimit, err := normalizeEllieScopedQuery(orgID, limit)
	if err != nil {
		return nil, err
	}
	normalizedProjectID := strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(normalizedProjectID) {
		return nil, fmt.Errorf("invalid project_id")
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			id, org_id, kind, title, content, sensitivity, source_project_id, source_conversation_id, occurred_at, created_at
		 FROM memories
		 WHERE org_id = $1
		   AND source_project_id = $2
		 ORDER BY occurred_at DESC, created_at DESC, id DESC
		 LIMIT $3`,
		normalizedOrgID,
		normalizedProjectID,
		queryLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list ellie project memories: %w", err)
	}
	defer rows.Close()

	memories := make([]EllieRetrievedMemory, 0, queryLimit)
	for rows.Next() {
		entry, scanErr := scanEllieRetrievedMemory(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan ellie project memory: %w", scanErr)
		}
		memories = append(memories, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading ellie project memory rows: %w", err)
	}
	return memories, nil
}

func (s *EllieRetrievalStore) ListRoomConversations(ctx context.Context, orgID, roomID string, limit int) ([]EllieRetrievedConversation, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie retrieval store is not configured")
	}
	normalizedOrgID, queryLimit, err := normalizeEllieScopedQuery(orgID, limit)
	if err != nil {
		return nil, err
	}
	normalizedRoomID := strings.TrimSpace(roomID)
	if !uuidRegex.MatchString(normalizedRoomID) {
		return nil, fmt.Errorf("invalid room_id")
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			id, org_id, room_id, topic, sensitivity, started_at, ended_at, created_at
		 FROM conversations
		 WHERE org_id = $1
		   AND room_id = $2
		 ORDER BY started_at DESC, id DESC
		 LIMIT $3`,
		normalizedOrgID,
		normalizedRoomID,
		queryLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list ellie room conversations: %w", err)
	}
	defer rows.Close()

	conversations := make([]EllieRetrievedConversation, 0, queryLimit)
	for rows.Next() {
		entry, scanErr := scanEllieRetrievedConversation(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan ellie room conversation: %w", scanErr)
		}
		conversations = append(conversations, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading ellie room conversation rows: %w", err)
	}
	return conversations, nil
}

func (s *EllieRetrievalStore) ListProjectConversations(ctx context.Context, orgID, projectID string, limit int) ([]EllieRetrievedConversation, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie retrieval store is not configured")
	}
	normalizedOrgID, queryLimit, err := normalizeEllieScopedQuery(orgID, limit)
	if err != nil {
		return nil, err
	}
	normalizedProjectID := strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(normalizedProjectID) {
		return nil, fmt.Errorf("invalid project_id")
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			c.id, c.org_id, c.room_id, c.topic, c.sensitivity, c.started_at, c.ended_at, c.created_at
		 FROM conversations c
		 JOIN rooms r
		   ON r.id = c.room_id
		  AND r.org_id = c.org_id
		 WHERE c.org_id = $1
		   AND r.type = 'project'
		   AND r.context_id = $2
		 ORDER BY c.started_at DESC, c.id DESC
		 LIMIT $3`,
		normalizedOrgID,
		normalizedProjectID,
		queryLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list ellie project conversations: %w", err)
	}
	defer rows.Close()

	conversations := make([]EllieRetrievedConversation, 0, queryLimit)
	for rows.Next() {
		entry, scanErr := scanEllieRetrievedConversation(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan ellie project conversation: %w", scanErr)
		}
		conversations = append(conversations, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading ellie project conversation rows: %w", err)
	}
	return conversations, nil
}

func (s *EllieRetrievalStore) ListRoomConversationHistory(ctx context.Context, orgID, roomID string, limit int) ([]EllieRoomConversationHistoryItem, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie retrieval store is not configured")
	}
	normalizedOrgID, queryLimit, err := normalizeEllieScopedQuery(orgID, limit)
	if err != nil {
		return nil, err
	}
	normalizedRoomID := strings.TrimSpace(roomID)
	if !uuidRegex.MatchString(normalizedRoomID) {
		return nil, fmt.Errorf("invalid room_id")
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			m.id,
			m.conversation_id,
			m.body,
			m.created_at,
			c.sensitivity
		 FROM chat_messages m
		 LEFT JOIN conversations c
		   ON c.id = m.conversation_id
		  AND c.org_id = m.org_id
		 WHERE m.org_id = $1
		   AND m.room_id = $2
		 ORDER BY m.created_at DESC, m.id DESC
		 LIMIT $3`,
		normalizedOrgID,
		normalizedRoomID,
		queryLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list ellie room conversation history: %w", err)
	}
	defer rows.Close()

	items := make([]EllieRoomConversationHistoryItem, 0, queryLimit)
	for rows.Next() {
		item, scanErr := scanEllieRoomConversationHistoryItem(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan ellie room conversation history item: %w", scanErr)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading ellie room conversation history rows: %w", err)
	}
	return items, nil
}

func normalizeEllieScopedQuery(orgID string, limit int) (string, int, error) {
	normalizedOrgID := strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(normalizedOrgID) {
		return "", 0, fmt.Errorf("invalid org_id")
	}
	queryLimit := limit
	if queryLimit <= 0 {
		queryLimit = 20
	}
	if queryLimit > 200 {
		queryLimit = 200
	}
	return normalizedOrgID, queryLimit, nil
}

func scanEllieRetrievedMemory(scanner interface{ Scan(...any) error }) (EllieRetrievedMemory, error) {
	var row EllieRetrievedMemory
	var sourceProject sql.NullString
	var conversationID sql.NullString
	err := scanner.Scan(
		&row.ID,
		&row.OrgID,
		&row.Kind,
		&row.Title,
		&row.Content,
		&row.Sensitivity,
		&sourceProject,
		&conversationID,
		&row.OccurredAt,
		&row.CreatedAt,
	)
	if err != nil {
		return EllieRetrievedMemory{}, err
	}
	row.SourceProject = nullStringPointer(sourceProject)
	row.ConversationID = nullStringPointer(conversationID)
	return row, nil
}

func scanEllieRetrievedConversation(scanner interface{ Scan(...any) error }) (EllieRetrievedConversation, error) {
	var row EllieRetrievedConversation
	var topic sql.NullString
	var endedAt sql.NullTime
	err := scanner.Scan(
		&row.ID,
		&row.OrgID,
		&row.RoomID,
		&topic,
		&row.Sensitivity,
		&row.StartedAt,
		&endedAt,
		&row.CreatedAt,
	)
	if err != nil {
		return EllieRetrievedConversation{}, err
	}
	row.Topic = topic.String
	if endedAt.Valid {
		timestamp := endedAt.Time
		row.EndedAt = &timestamp
	}
	return row, nil
}

func scanEllieRoomConversationHistoryItem(scanner interface{ Scan(...any) error }) (EllieRoomConversationHistoryItem, error) {
	var row EllieRoomConversationHistoryItem
	var conversationID sql.NullString
	var sensitivity sql.NullString
	err := scanner.Scan(
		&row.MessageID,
		&conversationID,
		&row.Body,
		&row.CreatedAt,
		&sensitivity,
	)
	if err != nil {
		return EllieRoomConversationHistoryItem{}, err
	}
	row.ConversationID = nullStringPointer(conversationID)
	row.ConversationSensitivity = nullStringPointer(sensitivity)
	return row, nil
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	trimmed := strings.TrimSpace(value.String)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
