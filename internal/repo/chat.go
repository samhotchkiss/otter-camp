package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrMessageContentImmutable = errors.New("repo: message content immutable")

type chatExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type ChatSession struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	ScopeType      string
	ScopeID        uuid.UUID
	Mode           string
	Status         string
	Title          *string
	CreatedByType  string
	CreatedByID    uuid.UUID
	CurrentTurnID  *uuid.UUID
	LastMessageAt  *time.Time
	TurnCount      int
	MessageCount   int
	Metadata       json.RawMessage
	ClosedAt       *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ChatSessionRepo struct {
	db chatExecutor
}

func NewChatSessionRepo(pool *pgxpool.Pool) *ChatSessionRepo {
	return &ChatSessionRepo{db: pool}
}

func (r *ChatSessionRepo) Create(ctx context.Context, session ChatSession) (ChatSession, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO chat_session (
			organization_id,
			scope_type,
			scope_id,
			mode,
			status,
			title,
			created_by_type,
			created_by_id,
			current_turn_id,
			last_message_at,
			turn_count,
			message_count,
			metadata,
			closed_at
		)
		VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5, ''), 'active'), $6, $7, $8, $9, $10, $11, $12, $13::jsonb, $14)
		RETURNING id, organization_id, scope_type, scope_id, mode, status, title, created_by_type, created_by_id,
		          current_turn_id, last_message_at, turn_count, message_count, metadata, closed_at, created_at, updated_at
	`,
		session.OrganizationID,
		strings.TrimSpace(session.ScopeType),
		session.ScopeID,
		strings.TrimSpace(session.Mode),
		strings.TrimSpace(session.Status),
		trimStringPointer(session.Title),
		strings.TrimSpace(session.CreatedByType),
		session.CreatedByID,
		session.CurrentTurnID,
		session.LastMessageAt,
		session.TurnCount,
		session.MessageCount,
		normalizeChatJSON(session.Metadata, json.RawMessage(`{}`)),
		session.ClosedAt,
	)

	created, err := scanChatSession(row)
	if err != nil {
		return ChatSession{}, mapDBError(err)
	}
	return created, nil
}

func (r *ChatSessionRepo) GetByID(ctx context.Context, id uuid.UUID) (ChatSession, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, organization_id, scope_type, scope_id, mode, status, title, created_by_type, created_by_id,
		       current_turn_id, last_message_at, turn_count, message_count, metadata, closed_at, created_at, updated_at
		FROM chat_session
		WHERE id = $1
	`, id)

	item, err := scanChatSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatSession{}, ErrNotFound
	}
	if err != nil {
		return ChatSession{}, mapDBError(err)
	}
	return item, nil
}

func (r *ChatSessionRepo) GetByScopeAndMode(ctx context.Context, scopeType string, scopeID uuid.UUID, mode string) (*ChatSession, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, organization_id, scope_type, scope_id, mode, status, title, created_by_type, created_by_id,
		       current_turn_id, last_message_at, turn_count, message_count, metadata, closed_at, created_at, updated_at
		FROM chat_session
		WHERE scope_type = $1
		  AND scope_id = $2
		  AND mode = $3
		  AND status = 'active'
		ORDER BY created_at DESC
		LIMIT 1
	`, strings.TrimSpace(scopeType), scopeID, strings.TrimSpace(mode))

	item, err := scanChatSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapDBError(err)
	}
	return &item, nil
}

func (r *ChatSessionRepo) ListByOrg(ctx context.Context, organizationID uuid.UUID) ([]ChatSession, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, organization_id, scope_type, scope_id, mode, status, title, created_by_type, created_by_id,
		       current_turn_id, last_message_at, turn_count, message_count, metadata, closed_at, created_at, updated_at
		FROM chat_session
		WHERE organization_id = $1
		ORDER BY COALESCE(last_message_at, created_at) DESC, created_at DESC
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ChatSession, 0)
	for rows.Next() {
		item, scanErr := scanChatSession(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *ChatSessionRepo) UpdateMode(ctx context.Context, id uuid.UUID, mode string) (ChatSession, error) {
	return r.updateSessionColumn(ctx, id, `mode = $2`, strings.TrimSpace(mode))
}

func (r *ChatSessionRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string) (ChatSession, error) {
	return r.updateSessionColumn(ctx, id, `status = $2`, strings.TrimSpace(status))
}

func (r *ChatSessionRepo) UpdateCurrentTurn(ctx context.Context, id uuid.UUID, currentTurnID *uuid.UUID) (ChatSession, error) {
	return r.updateSessionColumn(ctx, id, `current_turn_id = $2`, currentTurnID)
}

func (r *ChatSessionRepo) IncrementCounts(ctx context.Context, id uuid.UUID, turnDelta, messageDelta int) (ChatSession, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE chat_session
		SET turn_count = turn_count + $2,
		    message_count = message_count + $3
		WHERE id = $1
		RETURNING id, organization_id, scope_type, scope_id, mode, status, title, created_by_type, created_by_id,
		          current_turn_id, last_message_at, turn_count, message_count, metadata, closed_at, created_at, updated_at
	`, id, turnDelta, messageDelta)

	item, err := scanChatSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatSession{}, ErrNotFound
	}
	if err != nil {
		return ChatSession{}, mapDBError(err)
	}
	return item, nil
}

func (r *ChatSessionRepo) UpdateLastMessageAt(ctx context.Context, id uuid.UUID, lastMessageAt time.Time) (ChatSession, error) {
	return r.updateSessionColumn(ctx, id, `last_message_at = $2`, lastMessageAt.UTC())
}

func (r *ChatSessionRepo) Close(ctx context.Context, id uuid.UUID) (ChatSession, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE chat_session
		SET status = 'closed',
		    closed_at = now()
		WHERE id = $1
		RETURNING id, organization_id, scope_type, scope_id, mode, status, title, created_by_type, created_by_id,
		          current_turn_id, last_message_at, turn_count, message_count, metadata, closed_at, created_at, updated_at
	`, id)

	item, err := scanChatSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatSession{}, ErrNotFound
	}
	if err != nil {
		return ChatSession{}, mapDBError(err)
	}
	return item, nil
}

func (r *ChatSessionRepo) updateSessionColumn(ctx context.Context, id uuid.UUID, assignment string, value any) (ChatSession, error) {
	query := fmt.Sprintf(`
		UPDATE chat_session
		SET %s
		WHERE id = $1
		RETURNING id, organization_id, scope_type, scope_id, mode, status, title, created_by_type, created_by_id,
		          current_turn_id, last_message_at, turn_count, message_count, metadata, closed_at, created_at, updated_at
	`, assignment)
	row := r.db.QueryRow(ctx, query, id, value)
	item, err := scanChatSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatSession{}, ErrNotFound
	}
	if err != nil {
		return ChatSession{}, mapDBError(err)
	}
	return item, nil
}

func scanChatSession(row pgx.Row) (ChatSession, error) {
	var item ChatSession
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.ScopeType,
		&item.ScopeID,
		&item.Mode,
		&item.Status,
		&item.Title,
		&item.CreatedByType,
		&item.CreatedByID,
		&item.CurrentTurnID,
		&item.LastMessageAt,
		&item.TurnCount,
		&item.MessageCount,
		&item.Metadata,
		&item.ClosedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return ChatSession{}, err
	}
	item.Metadata = normalizeChatJSON(item.Metadata, json.RawMessage(`{}`))
	return item, nil
}

type ChatParticipant struct {
	ID                     uuid.UUID
	SessionID              uuid.UUID
	ParticipantType        string
	ParticipantID          uuid.UUID
	Role                   string
	NotificationPreference string
	JoinedAt               time.Time
	RemovedAt              *time.Time
}

type ChatParticipantRepo struct {
	db chatExecutor
}

func NewChatParticipantRepo(pool *pgxpool.Pool) *ChatParticipantRepo {
	return &ChatParticipantRepo{db: pool}
}

func (r *ChatParticipantRepo) Create(ctx context.Context, participant ChatParticipant) (ChatParticipant, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO chat_participant (
			session_id,
			participant_type,
			participant_id,
			role,
			notification_preference,
			removed_at
		)
		VALUES ($1, $2, $3, COALESCE(NULLIF($4, ''), 'member'), COALESCE(NULLIF($5, ''), 'all'), $6)
		RETURNING id, session_id, participant_type, participant_id, role, notification_preference, joined_at, removed_at
	`, participant.SessionID, strings.TrimSpace(participant.ParticipantType), participant.ParticipantID, strings.TrimSpace(participant.Role), strings.TrimSpace(participant.NotificationPreference), participant.RemovedAt)

	item, err := scanChatParticipant(row)
	if err != nil {
		return ChatParticipant{}, mapDBError(err)
	}
	return item, nil
}

func (r *ChatParticipantRepo) GetBySessionAndActor(ctx context.Context, sessionID uuid.UUID, participantType string, participantID uuid.UUID) (ChatParticipant, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, session_id, participant_type, participant_id, role, notification_preference, joined_at, removed_at
		FROM chat_participant
		WHERE session_id = $1
		  AND participant_type = $2
		  AND participant_id = $3
		  AND removed_at IS NULL
	`, sessionID, strings.TrimSpace(participantType), participantID)

	item, err := scanChatParticipant(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatParticipant{}, ErrNotFound
	}
	if err != nil {
		return ChatParticipant{}, mapDBError(err)
	}
	return item, nil
}

func (r *ChatParticipantRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]ChatParticipant, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, session_id, participant_type, participant_id, role, notification_preference, joined_at, removed_at
		FROM chat_participant
		WHERE session_id = $1
		  AND removed_at IS NULL
		ORDER BY joined_at ASC
	`, sessionID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ChatParticipant, 0)
	for rows.Next() {
		item, scanErr := scanChatParticipant(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *ChatParticipantRepo) ListByActor(ctx context.Context, participantType string, participantID uuid.UUID) ([]ChatParticipant, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, session_id, participant_type, participant_id, role, notification_preference, joined_at, removed_at
		FROM chat_participant
		WHERE participant_type = $1
		  AND participant_id = $2
		  AND removed_at IS NULL
		ORDER BY joined_at DESC
	`, strings.TrimSpace(participantType), participantID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ChatParticipant, 0)
	for rows.Next() {
		item, scanErr := scanChatParticipant(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *ChatParticipantRepo) UpdateNotificationPreference(ctx context.Context, id uuid.UUID, preference string) (ChatParticipant, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE chat_participant
		SET notification_preference = $2
		WHERE id = $1
		RETURNING id, session_id, participant_type, participant_id, role, notification_preference, joined_at, removed_at
	`, id, strings.TrimSpace(preference))

	item, err := scanChatParticipant(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatParticipant{}, ErrNotFound
	}
	if err != nil {
		return ChatParticipant{}, mapDBError(err)
	}
	return item, nil
}

func (r *ChatParticipantRepo) Remove(ctx context.Context, id uuid.UUID) (ChatParticipant, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE chat_participant
		SET removed_at = now()
		WHERE id = $1
		  AND removed_at IS NULL
		RETURNING id, session_id, participant_type, participant_id, role, notification_preference, joined_at, removed_at
	`, id)

	item, err := scanChatParticipant(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatParticipant{}, ErrNotFound
	}
	if err != nil {
		return ChatParticipant{}, mapDBError(err)
	}
	return item, nil
}

func scanChatParticipant(row pgx.Row) (ChatParticipant, error) {
	var item ChatParticipant
	if err := row.Scan(
		&item.ID,
		&item.SessionID,
		&item.ParticipantType,
		&item.ParticipantID,
		&item.Role,
		&item.NotificationPreference,
		&item.JoinedAt,
		&item.RemovedAt,
	); err != nil {
		return ChatParticipant{}, err
	}
	return item, nil
}

type ChatTurn struct {
	ID                uuid.UUID
	SessionID         uuid.UUID
	TurnNumber        int
	CycleID           *uuid.UUID
	RespondingType    string
	RespondingID      uuid.UUID
	Status            string
	CancelRequestedAt *time.Time
	StartedAt         *time.Time
	CompletedAt       *time.Time
	DurationMS        *int
	ErrorMessage      *string
	CreatedAt         time.Time
}

type ChatTurnRepo struct {
	db chatExecutor
}

func NewChatTurnRepo(pool *pgxpool.Pool) *ChatTurnRepo {
	return &ChatTurnRepo{db: pool}
}

func (r *ChatTurnRepo) Create(ctx context.Context, turn ChatTurn) (ChatTurn, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO chat_turn (
			session_id,
			turn_number,
			cycle_id,
			responding_type,
			responding_id,
			status,
			cancel_requested_at,
			started_at,
			completed_at,
			duration_ms,
			error_message
		)
		VALUES ($1, $2, $3, COALESCE(NULLIF($4, ''), 'agent'), $5, COALESCE(NULLIF($6, ''), 'pending'), $7, $8, $9, $10, $11)
		RETURNING id, session_id, turn_number, cycle_id, responding_type, responding_id, status, cancel_requested_at,
		          started_at, completed_at, duration_ms, error_message, created_at
	`, turn.SessionID, turn.TurnNumber, turn.CycleID, strings.TrimSpace(turn.RespondingType), turn.RespondingID, strings.TrimSpace(turn.Status), turn.CancelRequestedAt, turn.StartedAt, turn.CompletedAt, turn.DurationMS, trimStringPointer(turn.ErrorMessage))

	item, err := scanChatTurn(row)
	if err != nil {
		return ChatTurn{}, mapDBError(err)
	}
	return item, nil
}

func (r *ChatTurnRepo) GetByID(ctx context.Context, id uuid.UUID) (ChatTurn, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, session_id, turn_number, cycle_id, responding_type, responding_id, status, cancel_requested_at,
		       started_at, completed_at, duration_ms, error_message, created_at
		FROM chat_turn
		WHERE id = $1
	`, id)

	item, err := scanChatTurn(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatTurn{}, ErrNotFound
	}
	if err != nil {
		return ChatTurn{}, mapDBError(err)
	}
	return item, nil
}

func (r *ChatTurnRepo) GetBySessionAndNumber(ctx context.Context, sessionID uuid.UUID, turnNumber int) (ChatTurn, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, session_id, turn_number, cycle_id, responding_type, responding_id, status, cancel_requested_at,
		       started_at, completed_at, duration_ms, error_message, created_at
		FROM chat_turn
		WHERE session_id = $1
		  AND turn_number = $2
	`, sessionID, turnNumber)

	item, err := scanChatTurn(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatTurn{}, ErrNotFound
	}
	if err != nil {
		return ChatTurn{}, mapDBError(err)
	}
	return item, nil
}

func (r *ChatTurnRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]ChatTurn, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, session_id, turn_number, cycle_id, responding_type, responding_id, status, cancel_requested_at,
		       started_at, completed_at, duration_ms, error_message, created_at
		FROM chat_turn
		WHERE session_id = $1
		ORDER BY turn_number ASC
	`, sessionID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ChatTurn, 0)
	for rows.Next() {
		item, scanErr := scanChatTurn(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *ChatTurnRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string) (ChatTurn, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE chat_turn
		SET status = $2
		WHERE id = $1
		RETURNING id, session_id, turn_number, cycle_id, responding_type, responding_id, status, cancel_requested_at,
		          started_at, completed_at, duration_ms, error_message, created_at
	`, id, strings.TrimSpace(status))
	return scanChatTurnWithNotFound(row)
}

func (r *ChatTurnRepo) SetStarted(ctx context.Context, id uuid.UUID, startedAt time.Time) (ChatTurn, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE chat_turn
		SET status = 'in_progress',
		    started_at = $2
		WHERE id = $1
		RETURNING id, session_id, turn_number, cycle_id, responding_type, responding_id, status, cancel_requested_at,
		          started_at, completed_at, duration_ms, error_message, created_at
	`, id, startedAt.UTC())
	return scanChatTurnWithNotFound(row)
}

func (r *ChatTurnRepo) SetCompleted(ctx context.Context, id uuid.UUID, completedAt time.Time, durationMS int) (ChatTurn, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE chat_turn
		SET status = 'completed',
		    completed_at = $2,
		    duration_ms = $3
		WHERE id = $1
		RETURNING id, session_id, turn_number, cycle_id, responding_type, responding_id, status, cancel_requested_at,
		          started_at, completed_at, duration_ms, error_message, created_at
	`, id, completedAt.UTC(), durationMS)
	return scanChatTurnWithNotFound(row)
}

func (r *ChatTurnRepo) SetCancelled(ctx context.Context, id uuid.UUID, cancelRequestedAt time.Time, completedAt time.Time) (ChatTurn, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE chat_turn
		SET status = 'cancelled',
		    cancel_requested_at = $2,
		    completed_at = $3
		WHERE id = $1
		RETURNING id, session_id, turn_number, cycle_id, responding_type, responding_id, status, cancel_requested_at,
		          started_at, completed_at, duration_ms, error_message, created_at
	`, id, cancelRequestedAt.UTC(), completedAt.UTC())
	return scanChatTurnWithNotFound(row)
}

func (r *ChatTurnRepo) SetFailed(ctx context.Context, id uuid.UUID, errorMessage string, completedAt time.Time) (ChatTurn, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE chat_turn
		SET status = 'failed',
		    error_message = $2,
		    completed_at = $3
		WHERE id = $1
		RETURNING id, session_id, turn_number, cycle_id, responding_type, responding_id, status, cancel_requested_at,
		          started_at, completed_at, duration_ms, error_message, created_at
	`, id, strings.TrimSpace(errorMessage), completedAt.UTC())
	return scanChatTurnWithNotFound(row)
}

func scanChatTurnWithNotFound(row pgx.Row) (ChatTurn, error) {
	item, err := scanChatTurn(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatTurn{}, ErrNotFound
	}
	if err != nil {
		return ChatTurn{}, mapDBError(err)
	}
	return item, nil
}

func scanChatTurn(row pgx.Row) (ChatTurn, error) {
	var item ChatTurn
	if err := row.Scan(
		&item.ID,
		&item.SessionID,
		&item.TurnNumber,
		&item.CycleID,
		&item.RespondingType,
		&item.RespondingID,
		&item.Status,
		&item.CancelRequestedAt,
		&item.StartedAt,
		&item.CompletedAt,
		&item.DurationMS,
		&item.ErrorMessage,
		&item.CreatedAt,
	); err != nil {
		return ChatTurn{}, err
	}
	return item, nil
}

type ChatMessage struct {
	ID             uuid.UUID
	SessionID      uuid.UUID
	TurnID         *uuid.UUID
	SequenceNumber int64
	AuthorType     *string
	AuthorID       *uuid.UUID
	Role           string
	Content        string
	ContentFormat  string
	Status         string
	IsRedacted     bool
	RedactedAt     *time.Time
	ToolCallID     *string
	ErrorMessage   *string
	Metadata       json.RawMessage
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ChatMessageRepo struct {
	pool *pgxpool.Pool
	db   chatExecutor
}

func NewChatMessageRepo(pool *pgxpool.Pool) *ChatMessageRepo {
	return &ChatMessageRepo{pool: pool, db: pool}
}

func (r *ChatMessageRepo) Create(ctx context.Context, message ChatMessage) (ChatMessage, error) {
	if r.pool == nil {
		return ChatMessage{}, fmt.Errorf("chat message repo requires pool for Create")
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ChatMessage{}, mapDBError(err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, message.SessionID.String()); err != nil {
		return ChatMessage{}, mapDBError(err)
	}

	sequenceNumber := message.SequenceNumber
	if sequenceNumber <= 0 {
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(MAX(sequence_number), 0) + 1
			FROM chat_message
			WHERE session_id = $1
		`, message.SessionID).Scan(&sequenceNumber); err != nil {
			return ChatMessage{}, mapDBError(err)
		}
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO chat_message (
			session_id,
			turn_id,
			sequence_number,
			author_type,
			author_id,
			role,
			content,
			content_format,
			status,
			is_redacted,
			redacted_at,
			tool_call_id,
			error_message,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE(NULLIF($8, ''), 'text'), COALESCE(NULLIF($9, ''), 'pending'), $10, $11, $12, $13, $14::jsonb)
		RETURNING id, session_id, turn_id, sequence_number, author_type, author_id, role, content, content_format, status,
		          is_redacted, redacted_at, tool_call_id, error_message, metadata, created_at, updated_at
	`,
		message.SessionID,
		message.TurnID,
		sequenceNumber,
		trimStringPointer(message.AuthorType),
		message.AuthorID,
		strings.TrimSpace(message.Role),
		message.Content,
		strings.TrimSpace(message.ContentFormat),
		strings.TrimSpace(message.Status),
		message.IsRedacted,
		message.RedactedAt,
		trimStringPointer(message.ToolCallID),
		trimStringPointer(message.ErrorMessage),
		normalizeChatJSON(message.Metadata, json.RawMessage(`{}`)),
	)

	created, err := scanChatMessage(row)
	if err != nil {
		return ChatMessage{}, mapDBError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ChatMessage{}, mapDBError(err)
	}
	return created, nil
}

func (r *ChatMessageRepo) GetByID(ctx context.Context, id uuid.UUID) (ChatMessage, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, session_id, turn_id, sequence_number, author_type, author_id, role, content, content_format, status,
		       is_redacted, redacted_at, tool_call_id, error_message, metadata, created_at, updated_at
		FROM chat_message
		WHERE id = $1
	`, id)
	return scanChatMessageWithNotFound(row)
}

func (r *ChatMessageRepo) GetBySequence(ctx context.Context, sessionID uuid.UUID, sequenceNumber int64) (ChatMessage, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, session_id, turn_id, sequence_number, author_type, author_id, role, content, content_format, status,
		       is_redacted, redacted_at, tool_call_id, error_message, metadata, created_at, updated_at
		FROM chat_message
		WHERE session_id = $1
		  AND sequence_number = $2
	`, sessionID, sequenceNumber)
	return scanChatMessageWithNotFound(row)
}

func (r *ChatMessageRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]ChatMessage, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, session_id, turn_id, sequence_number, author_type, author_id, role, content, content_format, status,
		       is_redacted, redacted_at, tool_call_id, error_message, metadata, created_at, updated_at
		FROM chat_message
		WHERE session_id = $1
		ORDER BY sequence_number ASC
	`, sessionID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ChatMessage, 0)
	for rows.Next() {
		item, scanErr := scanChatMessage(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *ChatMessageRepo) ListByTurn(ctx context.Context, sessionID, turnID uuid.UUID) ([]ChatMessage, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, session_id, turn_id, sequence_number, author_type, author_id, role, content, content_format, status,
		       is_redacted, redacted_at, tool_call_id, error_message, metadata, created_at, updated_at
		FROM chat_message
		WHERE session_id = $1
		  AND turn_id = $2
		ORDER BY sequence_number ASC
	`, sessionID, turnID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ChatMessage, 0)
	for rows.Next() {
		item, scanErr := scanChatMessage(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *ChatMessageRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string, errorMessage string) (ChatMessage, error) {
	normalizedStatus := strings.TrimSpace(status)
	row := r.db.QueryRow(ctx, `
		UPDATE chat_message
		SET status = $2,
		    error_message = CASE
		        WHEN $2 = 'failed' THEN NULLIF($3, '')
		        ELSE NULL
		    END
		WHERE id = $1
		RETURNING id, session_id, turn_id, sequence_number, author_type, author_id, role, content, content_format, status,
		          is_redacted, redacted_at, tool_call_id, error_message, metadata, created_at, updated_at
	`, id, normalizedStatus, strings.TrimSpace(errorMessage))
	return scanChatMessageWithNotFound(row)
}

func (r *ChatMessageRepo) UpdateContent(ctx context.Context, id uuid.UUID, content string) (ChatMessage, error) {
	var status string
	if err := r.db.QueryRow(ctx, `SELECT status FROM chat_message WHERE id = $1`, id).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ChatMessage{}, ErrNotFound
		}
		return ChatMessage{}, mapDBError(err)
	}
	if status == "final" || status == "redacted" {
		return ChatMessage{}, ErrMessageContentImmutable
	}

	row := r.db.QueryRow(ctx, `
		UPDATE chat_message
		SET content = $2
		WHERE id = $1
		RETURNING id, session_id, turn_id, sequence_number, author_type, author_id, role, content, content_format, status,
		          is_redacted, redacted_at, tool_call_id, error_message, metadata, created_at, updated_at
	`, id, content)
	return scanChatMessageWithNotFound(row)
}

func (r *ChatMessageRepo) Redact(ctx context.Context, id uuid.UUID) (ChatMessage, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE chat_message
		SET content = '',
		    is_redacted = true,
		    status = 'redacted',
		    redacted_at = now()
		WHERE id = $1
		RETURNING id, session_id, turn_id, sequence_number, author_type, author_id, role, content, content_format, status,
		          is_redacted, redacted_at, tool_call_id, error_message, metadata, created_at, updated_at
	`, id)
	return scanChatMessageWithNotFound(row)
}

func (r *ChatMessageRepo) GetPending(ctx context.Context, sessionID uuid.UUID) ([]ChatMessage, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, session_id, turn_id, sequence_number, author_type, author_id, role, content, content_format, status,
		       is_redacted, redacted_at, tool_call_id, error_message, metadata, created_at, updated_at
		FROM chat_message
		WHERE session_id = $1
		  AND status IN ('pending', 'streaming')
		ORDER BY sequence_number ASC
	`, sessionID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ChatMessage, 0)
	for rows.Next() {
		item, scanErr := scanChatMessage(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *ChatMessageRepo) GetByToolCallID(ctx context.Context, sessionID uuid.UUID, toolCallID string) ([]ChatMessage, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, session_id, turn_id, sequence_number, author_type, author_id, role, content, content_format, status,
		       is_redacted, redacted_at, tool_call_id, error_message, metadata, created_at, updated_at
		FROM chat_message
		WHERE session_id = $1
		  AND tool_call_id = $2
		ORDER BY sequence_number ASC
	`, sessionID, strings.TrimSpace(toolCallID))
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ChatMessage, 0)
	for rows.Next() {
		item, scanErr := scanChatMessage(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func scanChatMessageWithNotFound(row pgx.Row) (ChatMessage, error) {
	item, err := scanChatMessage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatMessage{}, ErrNotFound
	}
	if err != nil {
		return ChatMessage{}, mapDBError(err)
	}
	return item, nil
}

func scanChatMessage(row pgx.Row) (ChatMessage, error) {
	var item ChatMessage
	if err := row.Scan(
		&item.ID,
		&item.SessionID,
		&item.TurnID,
		&item.SequenceNumber,
		&item.AuthorType,
		&item.AuthorID,
		&item.Role,
		&item.Content,
		&item.ContentFormat,
		&item.Status,
		&item.IsRedacted,
		&item.RedactedAt,
		&item.ToolCallID,
		&item.ErrorMessage,
		&item.Metadata,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return ChatMessage{}, err
	}
	item.Metadata = normalizeChatJSON(item.Metadata, json.RawMessage(`{}`))
	return item, nil
}

type ChatArtifact struct {
	ID           uuid.UUID
	SessionID    uuid.UUID
	MessageID    uuid.UUID
	ArtifactType string
	Filename     *string
	ContentType  *string
	StorageKey   *string
	URL          *string
	ByteSize     *int64
	CreatedAt    time.Time
}

type ChatArtifactRepo struct {
	db chatExecutor
}

func NewChatArtifactRepo(pool *pgxpool.Pool) *ChatArtifactRepo {
	return &ChatArtifactRepo{db: pool}
}

func (r *ChatArtifactRepo) Create(ctx context.Context, artifact ChatArtifact) (ChatArtifact, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO chat_artifact (
			session_id,
			message_id,
			artifact_type,
			filename,
			content_type,
			storage_key,
			url,
			byte_size
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, session_id, message_id, artifact_type, filename, content_type, storage_key, url, byte_size, created_at
	`, artifact.SessionID, artifact.MessageID, strings.TrimSpace(artifact.ArtifactType), trimStringPointer(artifact.Filename), trimStringPointer(artifact.ContentType), trimStringPointer(artifact.StorageKey), trimStringPointer(artifact.URL), artifact.ByteSize)

	item, err := scanChatArtifact(row)
	if err != nil {
		return ChatArtifact{}, mapDBError(err)
	}
	return item, nil
}

func (r *ChatArtifactRepo) GetByID(ctx context.Context, id uuid.UUID) (ChatArtifact, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, session_id, message_id, artifact_type, filename, content_type, storage_key, url, byte_size, created_at
		FROM chat_artifact
		WHERE id = $1
	`, id)
	return scanChatArtifactWithNotFound(row)
}

func (r *ChatArtifactRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]ChatArtifact, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, session_id, message_id, artifact_type, filename, content_type, storage_key, url, byte_size, created_at
		FROM chat_artifact
		WHERE session_id = $1
		ORDER BY created_at ASC
	`, sessionID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ChatArtifact, 0)
	for rows.Next() {
		item, scanErr := scanChatArtifact(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *ChatArtifactRepo) ListByMessage(ctx context.Context, messageID uuid.UUID) ([]ChatArtifact, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, session_id, message_id, artifact_type, filename, content_type, storage_key, url, byte_size, created_at
		FROM chat_artifact
		WHERE message_id = $1
		ORDER BY created_at ASC
	`, messageID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ChatArtifact, 0)
	for rows.Next() {
		item, scanErr := scanChatArtifact(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func scanChatArtifactWithNotFound(row pgx.Row) (ChatArtifact, error) {
	item, err := scanChatArtifact(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatArtifact{}, ErrNotFound
	}
	if err != nil {
		return ChatArtifact{}, mapDBError(err)
	}
	return item, nil
}

func scanChatArtifact(row pgx.Row) (ChatArtifact, error) {
	var item ChatArtifact
	if err := row.Scan(
		&item.ID,
		&item.SessionID,
		&item.MessageID,
		&item.ArtifactType,
		&item.Filename,
		&item.ContentType,
		&item.StorageKey,
		&item.URL,
		&item.ByteSize,
		&item.CreatedAt,
	); err != nil {
		return ChatArtifact{}, err
	}
	return item, nil
}

type ChatSummary struct {
	ID                  uuid.UUID
	SessionID           uuid.UUID
	FromSequence        int64
	ToSequence          int64
	SummaryText         string
	SummarizedTurnCount int
	ModelInvocationID   *uuid.UUID
	CreatedAt           time.Time
}

type ChatSummaryRepo struct {
	db chatExecutor
}

func NewChatSummaryRepo(pool *pgxpool.Pool) *ChatSummaryRepo {
	return &ChatSummaryRepo{db: pool}
}

func (r *ChatSummaryRepo) Create(ctx context.Context, summary ChatSummary) (ChatSummary, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO chat_summary (
			session_id,
			from_sequence,
			to_sequence,
			summary_text,
			summarized_turn_count,
			model_invocation_id
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, session_id, from_sequence, to_sequence, summary_text, summarized_turn_count, model_invocation_id, created_at
	`, summary.SessionID, summary.FromSequence, summary.ToSequence, summary.SummaryText, summary.SummarizedTurnCount, summary.ModelInvocationID)

	item, err := scanChatSummary(row)
	if err != nil {
		return ChatSummary{}, mapDBError(err)
	}
	return item, nil
}

func (r *ChatSummaryRepo) GetLatestForSession(ctx context.Context, sessionID uuid.UUID) (ChatSummary, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, session_id, from_sequence, to_sequence, summary_text, summarized_turn_count, model_invocation_id, created_at
		FROM chat_summary
		WHERE session_id = $1
		ORDER BY to_sequence DESC, created_at DESC
		LIMIT 1
	`, sessionID)
	return scanChatSummaryWithNotFound(row)
}

func (r *ChatSummaryRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]ChatSummary, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, session_id, from_sequence, to_sequence, summary_text, summarized_turn_count, model_invocation_id, created_at
		FROM chat_summary
		WHERE session_id = $1
		ORDER BY from_sequence ASC, created_at ASC
	`, sessionID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ChatSummary, 0)
	for rows.Next() {
		item, scanErr := scanChatSummary(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func scanChatSummaryWithNotFound(row pgx.Row) (ChatSummary, error) {
	item, err := scanChatSummary(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatSummary{}, ErrNotFound
	}
	if err != nil {
		return ChatSummary{}, mapDBError(err)
	}
	return item, nil
}

func scanChatSummary(row pgx.Row) (ChatSummary, error) {
	var item ChatSummary
	if err := row.Scan(
		&item.ID,
		&item.SessionID,
		&item.FromSequence,
		&item.ToSequence,
		&item.SummaryText,
		&item.SummarizedTurnCount,
		&item.ModelInvocationID,
		&item.CreatedAt,
	); err != nil {
		return ChatSummary{}, err
	}
	return item, nil
}

type ChatReadCursor struct {
	ID               uuid.UUID
	SessionID        uuid.UUID
	UserID           uuid.UUID
	LastReadSequence int64
	UpdatedAt        time.Time
}

type ChatReadCursorRepo struct {
	db chatExecutor
}

func NewChatReadCursorRepo(pool *pgxpool.Pool) *ChatReadCursorRepo {
	return &ChatReadCursorRepo{db: pool}
}

func (r *ChatReadCursorRepo) Upsert(ctx context.Context, cursor ChatReadCursor) (ChatReadCursor, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO chat_read_cursor (
			session_id,
			user_id,
			last_read_sequence
		)
		VALUES ($1, $2, $3)
		ON CONFLICT (session_id, user_id)
		DO UPDATE SET
			last_read_sequence = EXCLUDED.last_read_sequence,
			updated_at = now()
		RETURNING id, session_id, user_id, last_read_sequence, updated_at
	`, cursor.SessionID, cursor.UserID, cursor.LastReadSequence)

	item, err := scanChatReadCursor(row)
	if err != nil {
		return ChatReadCursor{}, mapDBError(err)
	}
	return item, nil
}

func (r *ChatReadCursorRepo) GetBySessionAndUser(ctx context.Context, sessionID, userID uuid.UUID) (ChatReadCursor, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, session_id, user_id, last_read_sequence, updated_at
		FROM chat_read_cursor
		WHERE session_id = $1
		  AND user_id = $2
	`, sessionID, userID)
	return scanChatReadCursorWithNotFound(row)
}

func (r *ChatReadCursorRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]ChatReadCursor, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, session_id, user_id, last_read_sequence, updated_at
		FROM chat_read_cursor
		WHERE session_id = $1
		ORDER BY updated_at DESC
	`, sessionID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ChatReadCursor, 0)
	for rows.Next() {
		item, scanErr := scanChatReadCursor(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func scanChatReadCursorWithNotFound(row pgx.Row) (ChatReadCursor, error) {
	item, err := scanChatReadCursor(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatReadCursor{}, ErrNotFound
	}
	if err != nil {
		return ChatReadCursor{}, mapDBError(err)
	}
	return item, nil
}

func scanChatReadCursor(row pgx.Row) (ChatReadCursor, error) {
	var item ChatReadCursor
	if err := row.Scan(
		&item.ID,
		&item.SessionID,
		&item.UserID,
		&item.LastReadSequence,
		&item.UpdatedAt,
	); err != nil {
		return ChatReadCursor{}, err
	}
	return item, nil
}

type ChatMessageReaction struct {
	ID          uuid.UUID
	MessageID   uuid.UUID
	SessionID   uuid.UUID
	ReactorType string
	ReactorID   uuid.UUID
	Emoji       string
	CreatedAt   time.Time
}

type ChatMessageReactionRepo struct {
	db chatExecutor
}

func NewChatMessageReactionRepo(pool *pgxpool.Pool) *ChatMessageReactionRepo {
	return &ChatMessageReactionRepo{db: pool}
}

func (r *ChatMessageReactionRepo) Create(ctx context.Context, reaction ChatMessageReaction) (ChatMessageReaction, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO chat_message_reaction (
			message_id,
			session_id,
			reactor_type,
			reactor_id,
			emoji
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, message_id, session_id, reactor_type, reactor_id, emoji, created_at
	`, reaction.MessageID, reaction.SessionID, strings.TrimSpace(reaction.ReactorType), reaction.ReactorID, reaction.Emoji)

	item, err := scanChatMessageReaction(row)
	if err != nil {
		return ChatMessageReaction{}, mapDBError(err)
	}
	return item, nil
}

func (r *ChatMessageReactionRepo) GetByID(ctx context.Context, id uuid.UUID) (ChatMessageReaction, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, message_id, session_id, reactor_type, reactor_id, emoji, created_at
		FROM chat_message_reaction
		WHERE id = $1
	`, id)
	return scanChatMessageReactionWithNotFound(row)
}

func (r *ChatMessageReactionRepo) ListByMessage(ctx context.Context, messageID uuid.UUID) ([]ChatMessageReaction, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, message_id, session_id, reactor_type, reactor_id, emoji, created_at
		FROM chat_message_reaction
		WHERE message_id = $1
		ORDER BY created_at ASC
	`, messageID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ChatMessageReaction, 0)
	for rows.Next() {
		item, scanErr := scanChatMessageReaction(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *ChatMessageReactionRepo) DeleteByID(ctx context.Context, id uuid.UUID) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM chat_message_reaction WHERE id = $1`, id)
	if err != nil {
		return mapDBError(err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *ChatMessageReactionRepo) GetByActorAndEmoji(ctx context.Context, messageID uuid.UUID, reactorType string, reactorID uuid.UUID, emoji string) (ChatMessageReaction, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, message_id, session_id, reactor_type, reactor_id, emoji, created_at
		FROM chat_message_reaction
		WHERE message_id = $1
		  AND reactor_type = $2
		  AND reactor_id = $3
		  AND emoji = $4
	`, messageID, strings.TrimSpace(reactorType), reactorID, emoji)
	return scanChatMessageReactionWithNotFound(row)
}

func scanChatMessageReactionWithNotFound(row pgx.Row) (ChatMessageReaction, error) {
	item, err := scanChatMessageReaction(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatMessageReaction{}, ErrNotFound
	}
	if err != nil {
		return ChatMessageReaction{}, mapDBError(err)
	}
	return item, nil
}

func scanChatMessageReaction(row pgx.Row) (ChatMessageReaction, error) {
	var item ChatMessageReaction
	if err := row.Scan(
		&item.ID,
		&item.MessageID,
		&item.SessionID,
		&item.ReactorType,
		&item.ReactorID,
		&item.Emoji,
		&item.CreatedAt,
	); err != nil {
		return ChatMessageReaction{}, err
	}
	return item, nil
}

type MemorySource struct {
	ID         uuid.UUID
	MemoryID   uuid.UUID
	SourceType string
	SourceID   *uuid.UUID
	ImportID   *uuid.UUID
	SessionID  *uuid.UUID
	CreatedAt  time.Time
}

type MemorySourceRepo struct {
	db chatExecutor
}

func NewMemorySourceRepo(pool *pgxpool.Pool) *MemorySourceRepo {
	return &MemorySourceRepo{db: pool}
}

func (r *MemorySourceRepo) Create(ctx context.Context, source MemorySource) (MemorySource, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO memory_source (
			memory_id,
			source_type,
			source_id,
			import_id,
			session_id
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, memory_id, source_type, source_id, import_id, session_id, created_at
	`, source.MemoryID, strings.TrimSpace(source.SourceType), source.SourceID, source.ImportID, source.SessionID)

	item, err := scanMemorySource(row)
	if err != nil {
		return MemorySource{}, mapDBError(err)
	}
	return item, nil
}

func (r *MemorySourceRepo) GetByID(ctx context.Context, id uuid.UUID) (MemorySource, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, memory_id, source_type, source_id, import_id, session_id, created_at
		FROM memory_source
		WHERE id = $1
	`, id)
	return scanMemorySourceWithNotFound(row)
}

func (r *MemorySourceRepo) ListByMemory(ctx context.Context, memoryID uuid.UUID) ([]MemorySource, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, memory_id, source_type, source_id, import_id, session_id, created_at
		FROM memory_source
		WHERE memory_id = $1
		ORDER BY created_at ASC
	`, memoryID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]MemorySource, 0)
	for rows.Next() {
		item, scanErr := scanMemorySource(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *MemorySourceRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]MemorySource, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, memory_id, source_type, source_id, import_id, session_id, created_at
		FROM memory_source
		WHERE session_id = $1
		ORDER BY created_at ASC
	`, sessionID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]MemorySource, 0)
	for rows.Next() {
		item, scanErr := scanMemorySource(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *MemorySourceRepo) ListBySourceMessage(ctx context.Context, sourceMessageID uuid.UUID) ([]MemorySource, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, memory_id, source_type, source_id, import_id, session_id, created_at
		FROM memory_source
		WHERE source_type = 'chat_message'
		  AND source_id = $1
		ORDER BY created_at ASC
	`, sourceMessageID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]MemorySource, 0)
	for rows.Next() {
		item, scanErr := scanMemorySource(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func scanMemorySourceWithNotFound(row pgx.Row) (MemorySource, error) {
	item, err := scanMemorySource(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemorySource{}, ErrNotFound
	}
	if err != nil {
		return MemorySource{}, mapDBError(err)
	}
	return item, nil
}

func scanMemorySource(row pgx.Row) (MemorySource, error) {
	var item MemorySource
	if err := row.Scan(
		&item.ID,
		&item.MemoryID,
		&item.SourceType,
		&item.SourceID,
		&item.ImportID,
		&item.SessionID,
		&item.CreatedAt,
	); err != nil {
		return MemorySource{}, err
	}
	return item, nil
}

func normalizeChatJSON(raw, fallback json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		if len(fallback) == 0 {
			return json.RawMessage(`{}`)
		}
		return fallback
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		if len(fallback) == 0 {
			return json.RawMessage(`{}`)
		}
		return fallback
	}
	return json.RawMessage(trimmed)
}
