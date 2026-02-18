package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

type EllieRoomIngestionCandidate struct {
	OrgID  string
	RoomID string
}

type EllieRoomCursor struct {
	OrgID                string
	RoomID               string
	LastMessageID        string
	LastMessageCreatedAt time.Time
}

type EllieIngestionMessage struct {
	ID             string
	OrgID          string
	RoomID         string
	SenderType     string
	Body           string
	CreatedAt      time.Time
	TokenCount     int
	ConversationID *string
}

type UpsertEllieRoomCursorInput struct {
	OrgID                string
	RoomID               string
	LastMessageID        string
	LastMessageCreatedAt time.Time
}

type CreateEllieIngestionWindowRunInput struct {
	OrgID            string
	RoomID           string
	WindowStartAt    time.Time
	WindowEndAt      time.Time
	FirstMessageID   *string
	LastMessageID    *string
	MessageCount     int
	TokenCount       int
	LLMUsed          bool
	LLMModel         string
	LLMTraceID       string
	LLMAttempts      int
	OK               bool
	Error            string
	DurationMS       int
	InsertedTotal    int
	InsertedMemories int
	InsertedProjects int
	InsertedIssues   int
}

type EllieIngestionCoverageDay struct {
	Day               time.Time  `json:"day"`
	TotalMessages     int        `json:"totalMessages"`
	ProcessedMessages int        `json:"processedMessages"`
	Windows           int        `json:"windows"`
	WindowsOK         int        `json:"windowsOK"`
	WindowsFailed     int        `json:"windowsFailed"`
	Retries           int        `json:"retries"`
	InsertedTotal     int        `json:"insertedTotal"`
	InsertedMemories  int        `json:"insertedMemories"`
	InsertedProjects  int        `json:"insertedProjects"`
	InsertedIssues    int        `json:"insertedIssues"`
	LastOKAt          *time.Time `json:"lastOKAt,omitempty"`
}

type EllieIngestionCoverageSummary struct {
	ExtractedUpTo *time.Time `json:"extractedUpTo,omitempty"`
}

var validEllieMemoryKinds = map[string]struct{}{
	"technical_decision": {},
	"process_decision":   {},
	"preference":         {},
	"fact":               {},
	"lesson":             {},
	"pattern":            {},
	"anti_pattern":       {},
	"correction":         {},
	"process_outcome":    {},
	"context":            {},
}

var validEllieMemoryStatuses = map[string]struct{}{
	"active":     {},
	"deprecated": {},
	"archived":   {},
}

type CreateEllieExtractedMemoryInput struct {
	OrgID                string
	Kind                 string
	Title                string
	Content              string
	Metadata             json.RawMessage
	Importance           int
	Confidence           float64
	Status               string
	Sensitivity          string
	OccurredAt           time.Time
	SourceConversationID *string
	SourceProjectID      *string
}

type EllieIngestionStore struct {
	db *sql.DB
}

func NewEllieIngestionStore(db *sql.DB) *EllieIngestionStore {
	return &EllieIngestionStore{db: db}
}

func (s *EllieIngestionStore) ListRoomsForIngestion(ctx context.Context, limit int) ([]EllieRoomIngestionCandidate, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie ingestion store is not configured")
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}

	rows, err := s.db.QueryContext(
		ctx,
		`WITH latest_room_messages AS (
		     SELECT DISTINCT ON (org_id, room_id)
		       org_id,
		       room_id,
		       id AS latest_message_id,
		       created_at AS latest_message_created_at
		     FROM chat_messages
		     ORDER BY org_id, room_id, created_at DESC, id DESC
		 )
		 SELECT latest.org_id, latest.room_id
		 FROM latest_room_messages latest
		 JOIN rooms room
		   ON room.org_id = latest.org_id
		  AND room.id = latest.room_id
		 LEFT JOIN ellie_ingestion_cursors cursor
		   ON cursor.org_id = latest.org_id
		  AND cursor.source_type = 'room'
		  AND cursor.source_id = latest.room_id::text
		 WHERE room.exclude_from_ingestion = FALSE
		   AND (latest.latest_message_created_at, latest.latest_message_id) >
		       (
		         COALESCE(cursor.last_message_created_at, TIMESTAMPTZ '1970-01-01'),
		         COALESCE(cursor.last_message_id, '00000000-0000-0000-0000-000000000000'::uuid)
		       )
		 ORDER BY latest.latest_message_created_at ASC, latest.room_id ASC
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list ellie ingestion rooms: %w", err)
	}
	defer rows.Close()

	candidates := make([]EllieRoomIngestionCandidate, 0, limit)
	for rows.Next() {
		var row EllieRoomIngestionCandidate
		if err := rows.Scan(&row.OrgID, &row.RoomID); err != nil {
			return nil, fmt.Errorf("failed to scan ellie ingestion room: %w", err)
		}
		candidates = append(candidates, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading ellie ingestion rooms: %w", err)
	}
	return candidates, nil
}

func (s *EllieIngestionStore) ListRoomsForIngestionByOrg(ctx context.Context, orgID string, limit int) ([]EllieRoomIngestionCandidate, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie ingestion store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}

	rows, err := s.db.QueryContext(
		ctx,
		`WITH latest_room_messages AS (
		     SELECT DISTINCT ON (org_id, room_id)
		       org_id,
		       room_id,
		       id AS latest_message_id,
		       created_at AS latest_message_created_at
		     FROM chat_messages
		     WHERE org_id = $1
		     ORDER BY org_id, room_id, created_at DESC, id DESC
		 )
		 SELECT latest.org_id, latest.room_id
		 FROM latest_room_messages latest
		 JOIN rooms room
		   ON room.org_id = latest.org_id
		  AND room.id = latest.room_id
		 LEFT JOIN ellie_ingestion_cursors cursor
		   ON cursor.org_id = latest.org_id
		  AND cursor.source_type = 'room'
		  AND cursor.source_id = latest.room_id::text
		 WHERE room.exclude_from_ingestion = FALSE
		   AND (latest.latest_message_created_at, latest.latest_message_id) >
		       (
		         COALESCE(cursor.last_message_created_at, TIMESTAMPTZ '1970-01-01'),
		         COALESCE(cursor.last_message_id, '00000000-0000-0000-0000-000000000000'::uuid)
		       )
		 ORDER BY latest.latest_message_created_at ASC, latest.room_id ASC
		 LIMIT $2`,
		orgID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list ellie ingestion rooms: %w", err)
	}
	defer rows.Close()

	candidates := make([]EllieRoomIngestionCandidate, 0, limit)
	for rows.Next() {
		var row EllieRoomIngestionCandidate
		if err := rows.Scan(&row.OrgID, &row.RoomID); err != nil {
			return nil, fmt.Errorf("failed to scan ellie ingestion room: %w", err)
		}
		candidates = append(candidates, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading ellie ingestion rooms: %w", err)
	}
	return candidates, nil
}

func (s *EllieIngestionStore) CountRoomsForIngestion(ctx context.Context, orgID string) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("ellie ingestion store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return 0, fmt.Errorf("invalid org_id")
	}

	var count int
	err := s.db.QueryRowContext(
		ctx,
		`WITH latest_room_messages AS (
		     SELECT DISTINCT ON (org_id, room_id)
		       org_id,
		       room_id,
		       id AS latest_message_id,
		       created_at AS latest_message_created_at
		     FROM chat_messages
		     WHERE org_id = $1
		     ORDER BY org_id, room_id, created_at DESC, id DESC
		 )
		 SELECT COUNT(*)
		 FROM latest_room_messages latest
		 JOIN rooms room
		   ON room.org_id = latest.org_id
		  AND room.id = latest.room_id
		 LEFT JOIN ellie_ingestion_cursors cursor
		   ON cursor.org_id = latest.org_id
		  AND cursor.source_type = 'room'
		  AND cursor.source_id = latest.room_id::text
		 WHERE room.exclude_from_ingestion = FALSE
		   AND (latest.latest_message_created_at, latest.latest_message_id) >
		       (
		         COALESCE(cursor.last_message_created_at, TIMESTAMPTZ '1970-01-01'),
		         COALESCE(cursor.last_message_id, '00000000-0000-0000-0000-000000000000'::uuid)
		       )`,
		orgID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count ellie ingestion rooms: %w", err)
	}
	if count < 0 {
		count = 0
	}
	return count, nil
}

func (s *EllieIngestionStore) GetRoomCursor(ctx context.Context, orgID, roomID string) (*EllieRoomCursor, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie ingestion store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	roomID = strings.TrimSpace(roomID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(roomID) {
		return nil, fmt.Errorf("invalid room_id")
	}

	var (
		row           EllieRoomCursor
		lastMessageID sql.NullString
	)
	err := s.db.QueryRowContext(
		ctx,
		`SELECT org_id, source_id, last_message_id::text, COALESCE(last_message_created_at, TIMESTAMPTZ '1970-01-01')
		 FROM ellie_ingestion_cursors
		 WHERE org_id = $1
		   AND source_type = 'room'
		   AND source_id = $2`,
		orgID,
		roomID,
	).Scan(&row.OrgID, &row.RoomID, &lastMessageID, &row.LastMessageCreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to load ellie room cursor: %w", err)
	}
	if lastMessageID.Valid {
		row.LastMessageID = lastMessageID.String
	}
	return &row, nil
}

func (s *EllieIngestionStore) ListRoomMessagesSince(
	ctx context.Context,
	orgID,
	roomID string,
	afterCreatedAt *time.Time,
	afterMessageID *string,
	limit int,
) ([]EllieIngestionMessage, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie ingestion store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	roomID = strings.TrimSpace(roomID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(roomID) {
		return nil, fmt.Errorf("invalid room_id")
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}

	args := []any{orgID, roomID}
	where := `WHERE org_id = $1 AND room_id = $2`
	if afterCreatedAt != nil && afterMessageID != nil && strings.TrimSpace(*afterMessageID) != "" {
		where += ` AND (created_at, id) > ($3, $4)`
		args = append(args, afterCreatedAt.UTC(), strings.TrimSpace(*afterMessageID))
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id,
		        org_id,
		        room_id,
		        sender_type,
		        body,
		        created_at,
		        COALESCE(token_count, 0)::INT,
		        conversation_id::text
		 FROM chat_messages
		 `+where+`
		 ORDER BY created_at ASC, id ASC
		 LIMIT $`+fmt.Sprint(len(args)),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list ellie room messages: %w", err)
	}
	defer rows.Close()

	messages := make([]EllieIngestionMessage, 0, limit)
	for rows.Next() {
		var (
			row            EllieIngestionMessage
			conversationID sql.NullString
		)
		if err := rows.Scan(
			&row.ID,
			&row.OrgID,
			&row.RoomID,
			&row.SenderType,
			&row.Body,
			&row.CreatedAt,
			&row.TokenCount,
			&conversationID,
		); err != nil {
			return nil, fmt.Errorf("failed to scan ellie room message: %w", err)
		}
		if conversationID.Valid {
			value := strings.TrimSpace(conversationID.String)
			if value != "" {
				row.ConversationID = &value
			}
		}
		messages = append(messages, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading ellie room messages: %w", err)
	}
	return messages, nil
}

func (s *EllieIngestionStore) InsertExtractedMemory(ctx context.Context, input CreateEllieExtractedMemoryInput) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("ellie ingestion store is not configured")
	}

	orgID := strings.TrimSpace(input.OrgID)
	if !uuidRegex.MatchString(orgID) {
		return false, fmt.Errorf("invalid org_id")
	}

	kind := strings.TrimSpace(strings.ToLower(input.Kind))
	if kind == "" {
		return false, fmt.Errorf("kind is required")
	}
	if _, ok := validEllieMemoryKinds[kind]; !ok {
		return false, fmt.Errorf("invalid kind")
	}

	title := strings.TrimSpace(input.Title)
	content := strings.TrimSpace(input.Content)
	if title == "" || content == "" {
		return false, fmt.Errorf("title and content are required")
	}

	importance := input.Importance
	if importance <= 0 {
		importance = 3
	}
	if importance > 5 {
		importance = 5
	}

	confidence := input.Confidence
	if math.IsNaN(confidence) || confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	status := strings.TrimSpace(strings.ToLower(input.Status))
	if status == "" {
		status = "active"
	}
	if _, ok := validEllieMemoryStatuses[status]; !ok {
		status = "active"
	}

	sensitivity, err := normalizeEllieSensitivity(input.Sensitivity)
	if err != nil {
		return false, err
	}

	occurredAt := input.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	sourceConversationID, err := normalizeOptionalEllieUUID(input.SourceConversationID)
	if err != nil {
		return false, fmt.Errorf("source_conversation_id: %w", err)
	}
	sourceProjectID, err := normalizeOptionalEllieUUID(input.SourceProjectID)
	if err != nil {
		return false, fmt.Errorf("source_project_id: %w", err)
	}

	metadata := normalizeJSONMap(input.Metadata)

	result, err := s.db.ExecContext(
		ctx,
		`INSERT INTO memories (
			org_id,
			kind,
			title,
			content,
			metadata,
			importance,
			confidence,
			status,
			source_conversation_id,
			source_project_id,
			occurred_at,
			sensitivity
		) VALUES (
			$1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9, $10, $11, $12
		)
		ON CONFLICT (org_id, content_hash) WHERE status = 'active' DO NOTHING`,
		orgID,
		kind,
		title,
		content,
		metadata,
		importance,
		confidence,
		status,
		sourceConversationID,
		sourceProjectID,
		occurredAt,
		sensitivity,
	)
	if err != nil {
		return false, fmt.Errorf("failed to insert ellie extracted memory: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to read ellie extracted memory insert count: %w", err)
	}
	return rowsAffected > 0, nil
}

func (s *EllieIngestionStore) CreateEllieExtractedMemory(ctx context.Context, input CreateEllieExtractedMemoryInput) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("ellie ingestion store is not configured")
	}

	orgID := strings.TrimSpace(input.OrgID)
	if !uuidRegex.MatchString(orgID) {
		return "", fmt.Errorf("invalid org_id")
	}

	kind := strings.TrimSpace(strings.ToLower(input.Kind))
	if _, ok := validEllieMemoryKinds[kind]; !ok {
		return "", fmt.Errorf("invalid kind")
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		return "", fmt.Errorf("title is required")
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return "", fmt.Errorf("content is required")
	}

	importance := input.Importance
	if importance == 0 {
		importance = 3
	}
	if importance < 1 || importance > 5 {
		return "", fmt.Errorf("invalid importance")
	}

	confidence := input.Confidence
	if confidence == 0 {
		confidence = 0.5
	}
	if math.IsNaN(confidence) || confidence < 0 || confidence > 1 {
		return "", fmt.Errorf("invalid confidence")
	}

	status := strings.TrimSpace(strings.ToLower(input.Status))
	if status == "" {
		status = "active"
	}
	if _, ok := validEllieMemoryStatuses[status]; !ok {
		return "", fmt.Errorf("invalid status")
	}

	sensitivity, err := normalizeEllieSensitivity(input.Sensitivity)
	if err != nil {
		return "", err
	}

	occurredAt := input.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	sourceConversationID, err := normalizeOptionalEllieUUID(input.SourceConversationID)
	if err != nil {
		return "", fmt.Errorf("source_conversation_id: %w", err)
	}
	sourceProjectID, err := normalizeOptionalEllieUUID(input.SourceProjectID)
	if err != nil {
		return "", fmt.Errorf("source_project_id: %w", err)
	}

	metadata := normalizeJSONMap(input.Metadata)

	var memoryID string
	err = s.db.QueryRowContext(
		ctx,
		`INSERT INTO memories (
			org_id, kind, title, content, metadata, importance, confidence,
			status, source_conversation_id, source_project_id, occurred_at, sensitivity
		) VALUES (
			$1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9, $10, $11, $12
		)
		RETURNING id`,
		orgID,
		kind,
		title,
		content,
		metadata,
		importance,
		confidence,
		status,
		sourceConversationID,
		sourceProjectID,
		occurredAt,
		sensitivity,
	).Scan(&memoryID)
	if err != nil {
		return "", fmt.Errorf("failed to create ellie extracted memory: %w", err)
	}

	return memoryID, nil
}

func (s *EllieIngestionStore) UpsertRoomCursor(ctx context.Context, input UpsertEllieRoomCursorInput) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("ellie ingestion store is not configured")
	}
	orgID := strings.TrimSpace(input.OrgID)
	roomID := strings.TrimSpace(input.RoomID)
	if !uuidRegex.MatchString(orgID) {
		return fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(roomID) {
		return fmt.Errorf("invalid room_id")
	}
	occurredAt := input.LastMessageCreatedAt.UTC()
	if occurredAt.IsZero() {
		return fmt.Errorf("last_message_created_at is required")
	}

	var lastMessageID any
	trimmedMessageID := strings.TrimSpace(input.LastMessageID)
	if trimmedMessageID != "" {
		if !uuidRegex.MatchString(trimmedMessageID) {
			return fmt.Errorf("invalid last_message_id")
		}
		lastMessageID = trimmedMessageID
	}

	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO ellie_ingestion_cursors (
			org_id,
			source_type,
			source_id,
			last_message_id,
			last_message_created_at
		) VALUES (
			$1,
			'room',
			$2,
			$3,
			$4
		)
		ON CONFLICT (org_id, source_type, source_id) DO UPDATE
		SET
			last_message_id = EXCLUDED.last_message_id,
			last_message_created_at = EXCLUDED.last_message_created_at,
			updated_at = NOW()`,
		orgID,
		roomID,
		lastMessageID,
		occurredAt,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert ellie room cursor: %w", err)
	}
	return nil
}

func (s *EllieIngestionStore) CreateWindowRun(ctx context.Context, input CreateEllieIngestionWindowRunInput) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("ellie ingestion store is not configured")
	}

	orgID := strings.TrimSpace(input.OrgID)
	roomID := strings.TrimSpace(input.RoomID)
	if !uuidRegex.MatchString(orgID) {
		return fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(roomID) {
		return fmt.Errorf("invalid room_id")
	}

	startAt := input.WindowStartAt.UTC()
	endAt := input.WindowEndAt.UTC()
	if startAt.IsZero() || endAt.IsZero() {
		return fmt.Errorf("window_start_at and window_end_at are required")
	}

	messageCount := input.MessageCount
	if messageCount < 0 {
		messageCount = 0
	}
	tokenCount := input.TokenCount
	if tokenCount < 0 {
		tokenCount = 0
	}

	var firstMessageID any
	if input.FirstMessageID != nil && strings.TrimSpace(*input.FirstMessageID) != "" {
		trimmed := strings.TrimSpace(*input.FirstMessageID)
		if uuidRegex.MatchString(trimmed) {
			firstMessageID = trimmed
		}
	}
	var lastMessageID any
	if input.LastMessageID != nil && strings.TrimSpace(*input.LastMessageID) != "" {
		trimmed := strings.TrimSpace(*input.LastMessageID)
		if uuidRegex.MatchString(trimmed) {
			lastMessageID = trimmed
		}
	}

	llmModel := strings.TrimSpace(input.LLMModel)
	if llmModel == "" {
		llmModel = ""
	}
	llmTraceID := strings.TrimSpace(input.LLMTraceID)
	if llmTraceID == "" {
		llmTraceID = ""
	}

	llmAttempts := input.LLMAttempts
	if llmAttempts < 0 {
		llmAttempts = 0
	}

	durationMS := input.DurationMS
	if durationMS < 0 {
		durationMS = 0
	}

	insertedTotal := input.InsertedTotal
	if insertedTotal < 0 {
		insertedTotal = 0
	}
	insertedMemories := input.InsertedMemories
	if insertedMemories < 0 {
		insertedMemories = 0
	}
	insertedProjects := input.InsertedProjects
	if insertedProjects < 0 {
		insertedProjects = 0
	}
	insertedIssues := input.InsertedIssues
	if insertedIssues < 0 {
		insertedIssues = 0
	}

	errText := strings.TrimSpace(input.Error)
	if len(errText) > 2000 {
		errText = errText[:2000]
	}

	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO ellie_ingestion_window_runs (
			org_id,
			room_id,
			window_start_at,
			window_end_at,
			first_message_id,
			last_message_id,
			message_count,
			token_count,
			llm_used,
			llm_model,
			llm_trace_id,
			llm_attempts,
			ok,
			error,
			duration_ms,
			inserted_total,
			inserted_memories,
			inserted_projects,
			inserted_issues
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, NULLIF($10, ''), NULLIF($11, ''), $12,
			$13, NULLIF($14, ''), $15,
			$16, $17, $18, $19
		)`,
		orgID,
		roomID,
		startAt,
		endAt,
		firstMessageID,
		lastMessageID,
		messageCount,
		tokenCount,
		input.LLMUsed,
		llmModel,
		llmTraceID,
		llmAttempts,
		input.OK,
		errText,
		durationMS,
		insertedTotal,
		insertedMemories,
		insertedProjects,
		insertedIssues,
	)
	if err != nil {
		return fmt.Errorf("failed to insert ellie ingestion window run: %w", err)
	}
	return nil
}

func (s *EllieIngestionStore) ListCoverageByDay(ctx context.Context, orgID string, days int) ([]EllieIngestionCoverageDay, EllieIngestionCoverageSummary, error) {
	if s == nil || s.db == nil {
		return nil, EllieIngestionCoverageSummary{}, fmt.Errorf("ellie ingestion store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, EllieIngestionCoverageSummary{}, fmt.Errorf("invalid org_id")
	}
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}
	startDay := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")

	rows, err := s.db.QueryContext(
		ctx,
		`WITH msg_days AS (
		     SELECT (created_at AT TIME ZONE 'UTC')::date AS day,
		            COUNT(*)::int AS total_messages
		     FROM chat_messages
		     WHERE org_id = $1
		       AND created_at >= ($2::date::timestamp AT TIME ZONE 'UTC')
		     GROUP BY day
		 ),
		 run_days AS (
		     SELECT (window_end_at AT TIME ZONE 'UTC')::date AS day,
		            SUM(message_count)::int AS processed_messages,
		            COUNT(*)::int AS windows,
		            SUM(CASE WHEN ok THEN 1 ELSE 0 END)::int AS windows_ok,
		            SUM(CASE WHEN ok THEN 0 ELSE 1 END)::int AS windows_failed,
		            SUM(GREATEST(llm_attempts - 1, 0))::int AS retries,
		            SUM(inserted_total)::int AS inserted_total,
		            SUM(inserted_memories)::int AS inserted_memories,
		            SUM(inserted_projects)::int AS inserted_projects,
		            SUM(inserted_issues)::int AS inserted_issues,
		            MAX(window_end_at) FILTER (WHERE ok) AS last_ok_at
		     FROM ellie_ingestion_window_runs
		     WHERE org_id = $1
		       AND window_end_at >= ($2::date::timestamp AT TIME ZONE 'UTC')
		     GROUP BY day
		 ),
		 days AS (
		     SELECT day FROM msg_days
		     UNION
		     SELECT day FROM run_days
		 )
		 SELECT d.day,
		        COALESCE(m.total_messages, 0)::int,
		        COALESCE(r.processed_messages, 0)::int,
		        COALESCE(r.windows, 0)::int,
		        COALESCE(r.windows_ok, 0)::int,
		        COALESCE(r.windows_failed, 0)::int,
		        COALESCE(r.retries, 0)::int,
		        COALESCE(r.inserted_total, 0)::int,
		        COALESCE(r.inserted_memories, 0)::int,
		        COALESCE(r.inserted_projects, 0)::int,
		        COALESCE(r.inserted_issues, 0)::int,
		        r.last_ok_at
		 FROM days d
		 LEFT JOIN msg_days m ON m.day = d.day
		 LEFT JOIN run_days r ON r.day = d.day
		 ORDER BY d.day ASC`,
		orgID,
		startDay,
	)
	if err != nil {
		return nil, EllieIngestionCoverageSummary{}, fmt.Errorf("failed to list ellie ingestion coverage: %w", err)
	}
	defer rows.Close()

	out := make([]EllieIngestionCoverageDay, 0, days)
	for rows.Next() {
		var (
			day               time.Time
			totalMessages     int
			processedMessages int
			windows           int
			windowsOK         int
			windowsFailed     int
			retries           int
			insertedTotal     int
			insertedMemories  int
			insertedProjects  int
			insertedIssues    int
			lastOKAt          sql.NullTime
		)
		if err := rows.Scan(
			&day,
			&totalMessages,
			&processedMessages,
			&windows,
			&windowsOK,
			&windowsFailed,
			&retries,
			&insertedTotal,
			&insertedMemories,
			&insertedProjects,
			&insertedIssues,
			&lastOKAt,
		); err != nil {
			return nil, EllieIngestionCoverageSummary{}, fmt.Errorf("failed to scan ellie ingestion coverage: %w", err)
		}
		row := EllieIngestionCoverageDay{
			Day:               day,
			TotalMessages:     totalMessages,
			ProcessedMessages: processedMessages,
			Windows:           windows,
			WindowsOK:         windowsOK,
			WindowsFailed:     windowsFailed,
			Retries:           retries,
			InsertedTotal:     insertedTotal,
			InsertedMemories:  insertedMemories,
			InsertedProjects:  insertedProjects,
			InsertedIssues:    insertedIssues,
		}
		if lastOKAt.Valid {
			v := lastOKAt.Time.UTC()
			row.LastOKAt = &v
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, EllieIngestionCoverageSummary{}, fmt.Errorf("failed reading ellie ingestion coverage: %w", err)
	}

	var extractedUpTo sql.NullTime
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT MAX(window_end_at) FILTER (WHERE ok)
		 FROM ellie_ingestion_window_runs
		 WHERE org_id = $1`,
		orgID,
	).Scan(&extractedUpTo); err != nil {
		return out, EllieIngestionCoverageSummary{}, fmt.Errorf("failed to read ellie ingestion extracted_up_to: %w", err)
	}
	var summary EllieIngestionCoverageSummary
	if extractedUpTo.Valid {
		v := extractedUpTo.Time.UTC()
		summary.ExtractedUpTo = &v
	}

	return out, summary, nil
}

func (s *EllieIngestionStore) HasComplianceFingerprint(ctx context.Context, orgID, fingerprint string) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("ellie ingestion store is not configured")
	}

	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return false, fmt.Errorf("invalid org_id")
	}

	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return false, fmt.Errorf("compliance fingerprint is required")
	}

	var exists bool
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT EXISTS(
			SELECT 1
			  FROM memories
			 WHERE org_id = $1
			   AND metadata->>'compliance_fingerprint' = $2
		)`,
		orgID,
		fingerprint,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("failed to check compliance fingerprint memory: %w", err)
	}

	return exists, nil
}

func normalizeOptionalEllieUUID(value *string) (interface{}, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	if !uuidRegex.MatchString(trimmed) {
		return nil, fmt.Errorf("invalid uuid")
	}
	return trimmed, nil
}
