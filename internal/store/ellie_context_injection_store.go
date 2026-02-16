package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type EllieContextInjectionPendingMessage struct {
	MessageID      string
	OrgID          string
	RoomID         string
	SenderID       string
	SenderType     string
	Body           string
	MessageType    string
	ConversationID *string
	CreatedAt      time.Time
	HasEmbedding   bool
}

type EllieContextInjectionMemoryCandidate struct {
	MemoryID             string
	Kind                 string
	Title                string
	Content              string
	Importance           int
	Confidence           float64
	OccurredAt           time.Time
	SourceConversationID *string
	SupersededBy         *string
	Similarity           float64
}

type CreateEllieContextInjectionMessageInput struct {
	OrgID          string
	RoomID         string
	SenderID       string
	Body           string
	MessageType    string
	ConversationID *string
	CreatedAt      time.Time
}

type EllieContextInjectionStore struct {
	db              *sql.DB
	targetDimension int
}

func NewEllieContextInjectionStore(db *sql.DB) *EllieContextInjectionStore {
	return NewEllieContextInjectionStoreWithDimension(db, legacyEmbeddingDimension)
}

func NewEllieContextInjectionStoreWithDimension(db *sql.DB, targetDimension int) *EllieContextInjectionStore {
	return &EllieContextInjectionStore{
		db:              db,
		targetDimension: normalizeEmbeddingDimension(targetDimension),
	}
}

func (s *EllieContextInjectionStore) embeddingColumn() string {
	if s == nil {
		return embeddingColumnForDimension(legacyEmbeddingDimension)
	}
	return embeddingColumnForDimension(s.targetDimension)
}

func (s *EllieContextInjectionStore) ListPendingMessagesSince(
	ctx context.Context,
	afterCreatedAt *time.Time,
	afterMessageID *string,
	limit int,
) ([]EllieContextInjectionPendingMessage, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie context injection store is not configured")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	args := []any{}
	where := []string{
		"cm.type NOT IN ('system', 'context_injection')",
		`(
			EXISTS (
				SELECT 1 FROM room_participants rp
				WHERE rp.org_id = cm.org_id
				  AND rp.room_id = cm.room_id
				  AND rp.participant_type = 'agent'
			)
			OR EXISTS (
				SELECT 1 FROM chat_messages am
				WHERE am.org_id = cm.org_id
				  AND am.room_id = cm.room_id
				  AND am.sender_type = 'agent'
			)
		)`,
	}

	if afterCreatedAt != nil && afterMessageID != nil {
		afterID := strings.TrimSpace(*afterMessageID)
		if afterID != "" {
			where = append(where, fmt.Sprintf("(cm.created_at, cm.id) > ($%d, $%d)", len(args)+1, len(args)+2))
			args = append(args, afterCreatedAt.UTC(), afterID)
		}
	}

	args = append(args, limit)
	limitArg := fmt.Sprintf("$%d", len(args))

	rows, err := s.db.QueryContext(
		ctx,
		fmt.Sprintf(`SELECT cm.id,
		        cm.org_id,
		        cm.room_id,
		        cm.sender_id::text,
		        cm.sender_type,
		        cm.body,
		        cm.type,
		        cm.conversation_id::text,
		        cm.created_at,
		        cm.%s IS NOT NULL
		 FROM chat_messages cm
		 WHERE `+strings.Join(where, " AND ")+`
		 ORDER BY cm.created_at ASC, cm.id ASC
		 LIMIT `+limitArg, s.embeddingColumn()),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending context injection messages: %w", err)
	}
	defer rows.Close()

	pending := make([]EllieContextInjectionPendingMessage, 0, limit)
	for rows.Next() {
		var (
			row            EllieContextInjectionPendingMessage
			conversationID sql.NullString
		)
		if err := rows.Scan(
			&row.MessageID,
			&row.OrgID,
			&row.RoomID,
			&row.SenderID,
			&row.SenderType,
			&row.Body,
			&row.MessageType,
			&conversationID,
			&row.CreatedAt,
			&row.HasEmbedding,
		); err != nil {
			return nil, fmt.Errorf("failed to scan pending context injection message row: %w", err)
		}
		if conversationID.Valid {
			value := strings.TrimSpace(conversationID.String)
			if value != "" {
				row.ConversationID = &value
			}
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading pending context injection messages: %w", err)
	}

	return pending, nil
}

func (s *EllieContextInjectionStore) UpdateMessageEmbedding(ctx context.Context, messageID string, embedding []float64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("ellie context injection store is not configured")
	}
	messageID = strings.TrimSpace(messageID)
	if !uuidRegex.MatchString(messageID) {
		return fmt.Errorf("invalid message_id")
	}
	vectorLiteral, err := formatVectorLiteral(embedding)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(
		ctx,
		fmt.Sprintf(`UPDATE chat_messages
		 SET %s = $2::vector
		 WHERE id = $1`, s.embeddingColumn()),
		messageID,
		vectorLiteral,
	)
	if err != nil {
		return fmt.Errorf("failed to update context injection message embedding: %w", err)
	}
	return nil
}

func (s *EllieContextInjectionStore) SearchMemoryCandidatesByEmbedding(
	ctx context.Context,
	orgID string,
	embedding []float64,
	limit int,
) ([]EllieContextInjectionMemoryCandidate, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie context injection store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 50 {
		limit = 50
	}

	vectorLiteral, err := formatVectorLiteral(embedding)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(
		ctx,
		fmt.Sprintf(`SELECT id,
		        kind,
		        title,
		        content,
		        importance,
		        confidence,
		        occurred_at,
		        source_conversation_id::text,
		        superseded_by::text,
		        1 - (%[1]s <=> $2::vector) AS similarity
		 FROM memories
		 WHERE org_id = $1
		   AND status = 'active'
		   AND %[1]s IS NOT NULL
		 ORDER BY %[1]s <=> $2::vector ASC, occurred_at DESC, id DESC
		 LIMIT $3`, s.embeddingColumn()),
		orgID,
		vectorLiteral,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to search context injection memory candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]EllieContextInjectionMemoryCandidate, 0, limit)
	for rows.Next() {
		var (
			candidate            EllieContextInjectionMemoryCandidate
			sourceConversationID sql.NullString
			supersededBy         sql.NullString
		)
		if err := rows.Scan(
			&candidate.MemoryID,
			&candidate.Kind,
			&candidate.Title,
			&candidate.Content,
			&candidate.Importance,
			&candidate.Confidence,
			&candidate.OccurredAt,
			&sourceConversationID,
			&supersededBy,
			&candidate.Similarity,
		); err != nil {
			return nil, fmt.Errorf("failed to scan context injection memory candidate: %w", err)
		}
		if sourceConversationID.Valid {
			value := strings.TrimSpace(sourceConversationID.String)
			if value != "" {
				candidate.SourceConversationID = &value
			}
		}
		if supersededBy.Valid {
			value := strings.TrimSpace(supersededBy.String)
			if value != "" {
				candidate.SupersededBy = &value
			}
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading context injection memory candidates: %w", err)
	}

	return candidates, nil
}

func (s *EllieContextInjectionStore) WasInjectedSinceCompaction(
	ctx context.Context,
	orgID,
	roomID,
	memoryID string,
) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("ellie context injection store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	roomID = strings.TrimSpace(roomID)
	memoryID = strings.TrimSpace(memoryID)
	if !uuidRegex.MatchString(orgID) {
		return false, fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(roomID) {
		return false, fmt.Errorf("invalid room_id")
	}
	if !uuidRegex.MatchString(memoryID) {
		return false, fmt.Errorf("invalid memory_id")
	}

	var exists bool
	err := s.db.QueryRowContext(
		ctx,
		`SELECT EXISTS (
			SELECT 1
			FROM context_injections ci
			JOIN rooms r
			  ON r.id = ci.room_id
			 AND r.org_id = ci.org_id
			WHERE ci.org_id = $1
			  AND ci.room_id = $2
			  AND ci.memory_id = $3
			  AND ci.injected_at > COALESCE(r.last_compacted_at, TIMESTAMPTZ '1970-01-01')
		)`,
		orgID,
		roomID,
		memoryID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check context injection ledger: %w", err)
	}
	return exists, nil
}

func (s *EllieContextInjectionStore) RecordInjection(
	ctx context.Context,
	orgID,
	roomID,
	memoryID string,
	injectedAt time.Time,
) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("ellie context injection store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	roomID = strings.TrimSpace(roomID)
	memoryID = strings.TrimSpace(memoryID)
	if !uuidRegex.MatchString(orgID) {
		return fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(roomID) {
		return fmt.Errorf("invalid room_id")
	}
	if !uuidRegex.MatchString(memoryID) {
		return fmt.Errorf("invalid memory_id")
	}
	if injectedAt.IsZero() {
		injectedAt = time.Now().UTC()
	} else {
		injectedAt = injectedAt.UTC()
	}

	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO context_injections (org_id, room_id, memory_id, injected_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (room_id, memory_id)
		 DO UPDATE SET injected_at = GREATEST(context_injections.injected_at, EXCLUDED.injected_at)`,
		orgID,
		roomID,
		memoryID,
		injectedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to record context injection ledger row: %w", err)
	}
	return nil
}

func (s *EllieContextInjectionStore) CreateInjectionMessage(
	ctx context.Context,
	input CreateEllieContextInjectionMessageInput,
) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("ellie context injection store is not configured")
	}
	orgID := strings.TrimSpace(input.OrgID)
	roomID := strings.TrimSpace(input.RoomID)
	senderID := strings.TrimSpace(input.SenderID)
	body := strings.TrimSpace(input.Body)
	messageType := strings.TrimSpace(strings.ToLower(input.MessageType))
	if !uuidRegex.MatchString(orgID) {
		return "", fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(roomID) {
		return "", fmt.Errorf("invalid room_id")
	}
	if !uuidRegex.MatchString(senderID) {
		return "", fmt.Errorf("invalid sender_id")
	}
	if body == "" {
		return "", fmt.Errorf("body is required")
	}
	if messageType == "" {
		messageType = "context_injection"
	}
	if messageType != "context_injection" {
		return "", fmt.Errorf("invalid message_type")
	}

	var conversationValue any
	if input.ConversationID != nil {
		conversationID := strings.TrimSpace(*input.ConversationID)
		if conversationID != "" {
			if !uuidRegex.MatchString(conversationID) {
				return "", fmt.Errorf("invalid conversation_id")
			}
			conversationValue = conversationID
		}
	}

	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	} else {
		createdAt = createdAt.UTC()
	}

	var messageID string
	err := s.db.QueryRowContext(
		ctx,
		`INSERT INTO chat_messages (
			org_id,
			room_id,
			sender_id,
			sender_type,
			body,
			type,
			conversation_id,
			attachments,
			created_at
		 ) VALUES (
			$1, $2, $3, 'agent', $4, $5, $6, '[]'::jsonb, $7
		 )
		 RETURNING id`,
		orgID,
		roomID,
		senderID,
		body,
		messageType,
		conversationValue,
		createdAt,
	).Scan(&messageID)
	if err != nil {
		return "", fmt.Errorf("failed to create context injection message: %w", err)
	}

	return messageID, nil
}

func (s *EllieContextInjectionStore) CountMessagesSinceLastContextInjection(
	ctx context.Context,
	orgID,
	roomID string,
) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("ellie context injection store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	roomID = strings.TrimSpace(roomID)
	if !uuidRegex.MatchString(orgID) {
		return 0, fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(roomID) {
		return 0, fmt.Errorf("invalid room_id")
	}

	var count int
	err := s.db.QueryRowContext(
		ctx,
		`WITH last_injection AS (
			SELECT created_at, id
			FROM chat_messages
			WHERE org_id = $1
			  AND room_id = $2
			  AND type = 'context_injection'
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		)
		SELECT CASE
			WHEN EXISTS (SELECT 1 FROM last_injection) THEN (
				SELECT COUNT(*)
				FROM chat_messages cm
				WHERE cm.org_id = $1
				  AND cm.room_id = $2
				  AND (cm.created_at, cm.id) > (
					SELECT created_at, id FROM last_injection
				  )
			)
			ELSE 2147483647
		END`,
		orgID,
		roomID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count messages since last context injection: %w", err)
	}

	return count, nil
}

func (s *EllieContextInjectionStore) CountRoomMessages(
	ctx context.Context,
	orgID,
	roomID string,
) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("ellie context injection store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	roomID = strings.TrimSpace(roomID)
	if !uuidRegex.MatchString(orgID) {
		return 0, fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(roomID) {
		return 0, fmt.Errorf("invalid room_id")
	}

	var count int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		 FROM chat_messages
		 WHERE org_id = $1
		   AND room_id = $2`,
		orgID,
		roomID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count room messages for context injection: %w", err)
	}
	return count, nil
}

func (s *EllieContextInjectionStore) CountPriorInjections(
	ctx context.Context,
	orgID,
	roomID string,
) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("ellie context injection store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	roomID = strings.TrimSpace(roomID)
	if !uuidRegex.MatchString(orgID) {
		return 0, fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(roomID) {
		return 0, fmt.Errorf("invalid room_id")
	}

	var count int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		 FROM context_injections
		 WHERE org_id = $1
		   AND room_id = $2`,
		orgID,
		roomID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count prior context injections: %w", err)
	}
	return count, nil
}
