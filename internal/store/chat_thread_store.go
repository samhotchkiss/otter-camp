package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const (
	ChatThreadTypeDM      = "dm"
	ChatThreadTypeProject = "project"
	ChatThreadTypeIssue   = "issue"

	ChatThreadArchiveReasonIssueClosed     = "issue_closed"
	ChatThreadArchiveReasonProjectArchived = "project_archived"
)

type ChatThread struct {
	ID                 string     `json:"id"`
	OrgID              string     `json:"org_id"`
	UserID             string     `json:"user_id"`
	AgentID            *string    `json:"agent_id,omitempty"`
	ProjectID          *string    `json:"project_id,omitempty"`
	IssueID            *string    `json:"issue_id,omitempty"`
	ThreadKey          string     `json:"thread_key"`
	ThreadType         string     `json:"thread_type"`
	Title              string     `json:"title"`
	LastMessagePreview string     `json:"last_message_preview"`
	ArchivedAt         *time.Time `json:"archived_at,omitempty"`
	AutoArchivedReason *string    `json:"auto_archived_reason,omitempty"`
	LastMessageAt      time.Time  `json:"last_message_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type TouchChatThreadInput struct {
	UserID             string
	AgentID            *string
	ProjectID          *string
	IssueID            *string
	ThreadKey          string
	ThreadType         string
	Title              string
	LastMessagePreview string
	LastMessageAt      time.Time
}

type ListChatThreadsInput struct {
	Archived bool
	Query    string
	Limit    int
}

type ChatThreadStore struct {
	db *sql.DB
}

func NewChatThreadStore(db *sql.DB) *ChatThreadStore {
	return &ChatThreadStore{db: db}
}

const chatThreadColumns = `
	id,
	org_id,
	user_id,
	agent_id,
	project_id,
	issue_id,
	thread_key,
	thread_type,
	title,
	last_message_preview,
	archived_at,
	auto_archived_reason,
	last_message_at,
	created_at,
	updated_at
`

func (s *ChatThreadStore) TouchThread(ctx context.Context, input TouchChatThreadInput) (*ChatThread, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	userID := strings.TrimSpace(input.UserID)
	if !uuidRegex.MatchString(userID) {
		return nil, fmt.Errorf("invalid user_id")
	}
	threadKey := strings.TrimSpace(input.ThreadKey)
	if threadKey == "" {
		return nil, fmt.Errorf("thread_key is required")
	}
	threadType := normalizeChatThreadType(input.ThreadType)
	if !isValidChatThreadType(threadType) {
		return nil, fmt.Errorf("invalid thread_type")
	}

	agentID, err := normalizeOptionalChatThreadUUID(input.AgentID, "agent_id")
	if err != nil {
		return nil, err
	}
	projectID, err := normalizeOptionalChatThreadUUID(input.ProjectID, "project_id")
	if err != nil {
		return nil, err
	}
	issueID, err := normalizeOptionalChatThreadUUID(input.IssueID, "issue_id")
	if err != nil {
		return nil, err
	}

	title := strings.TrimSpace(input.Title)
	lastPreview := strings.TrimSpace(input.LastMessagePreview)
	lastMessageAt := input.LastMessageAt.UTC()
	if lastMessageAt.IsZero() {
		lastMessageAt = time.Now().UTC()
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	thread, err := scanChatThread(conn.QueryRowContext(
		ctx,
		`INSERT INTO chat_threads (
			org_id,
			user_id,
			agent_id,
			project_id,
			issue_id,
			thread_key,
			thread_type,
			title,
			last_message_preview,
			last_message_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (org_id, user_id, thread_key)
		DO UPDATE SET
			agent_id = COALESCE(EXCLUDED.agent_id, chat_threads.agent_id),
			project_id = COALESCE(EXCLUDED.project_id, chat_threads.project_id),
			issue_id = COALESCE(EXCLUDED.issue_id, chat_threads.issue_id),
			thread_type = EXCLUDED.thread_type,
			title = CASE
				WHEN EXCLUDED.title <> '' THEN EXCLUDED.title
				ELSE chat_threads.title
			END,
			last_message_preview = EXCLUDED.last_message_preview,
			last_message_at = GREATEST(chat_threads.last_message_at, EXCLUDED.last_message_at),
			archived_at = NULL,
			auto_archived_reason = NULL
		RETURNING `+chatThreadColumns,
		workspaceID,
		userID,
		nullableString(agentID),
		nullableString(projectID),
		nullableString(issueID),
		threadKey,
		threadType,
		title,
		lastPreview,
		lastMessageAt,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to touch chat thread: %w", err)
	}
	return &thread, nil
}

func (s *ChatThreadStore) ListByUser(ctx context.Context, userID string, input ListChatThreadsInput) ([]ChatThread, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	userID = strings.TrimSpace(userID)
	if !uuidRegex.MatchString(userID) {
		return nil, fmt.Errorf("invalid user_id")
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	where := []string{"org_id = $1", "user_id = $2"}
	args := []any{workspaceID, userID}
	if input.Archived {
		where = append(where, "archived_at IS NOT NULL")
	} else {
		where = append(where, "archived_at IS NULL")
	}
	if query := strings.TrimSpace(input.Query); query != "" {
		args = append(args, "%"+query+"%")
		idx := len(args)
		where = append(where, fmt.Sprintf("(title ILIKE $%d OR last_message_preview ILIKE $%d OR thread_key ILIKE $%d)", idx, idx, idx))
	}
	args = append(args, limit)

	rows, err := conn.QueryContext(
		ctx,
		`SELECT `+chatThreadColumns+`
		 FROM chat_threads
		 WHERE `+strings.Join(where, " AND ")+`
		 ORDER BY last_message_at DESC, id DESC
		 LIMIT $`+fmt.Sprint(len(args)),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list chat threads: %w", err)
	}
	defer rows.Close()

	threads := make([]ChatThread, 0, limit)
	for rows.Next() {
		thread, err := scanChatThread(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan chat thread: %w", err)
		}
		threads = append(threads, thread)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading chat threads: %w", err)
	}
	return threads, nil
}

func (s *ChatThreadStore) GetByIDForUser(ctx context.Context, userID, chatID string) (*ChatThread, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	userID = strings.TrimSpace(userID)
	if !uuidRegex.MatchString(userID) {
		return nil, fmt.Errorf("invalid user_id")
	}
	chatID = strings.TrimSpace(chatID)
	if !uuidRegex.MatchString(chatID) {
		return nil, fmt.Errorf("invalid chat_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	thread, err := scanChatThread(conn.QueryRowContext(
		ctx,
		`SELECT `+chatThreadColumns+`
		 FROM chat_threads
		 WHERE org_id = $1 AND user_id = $2 AND id = $3`,
		workspaceID,
		userID,
		chatID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load chat thread: %w", err)
	}
	return &thread, nil
}

func (s *ChatThreadStore) Archive(ctx context.Context, userID, chatID, reason string) (*ChatThread, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	userID = strings.TrimSpace(userID)
	if !uuidRegex.MatchString(userID) {
		return nil, fmt.Errorf("invalid user_id")
	}
	chatID = strings.TrimSpace(chatID)
	if !uuidRegex.MatchString(chatID) {
		return nil, fmt.Errorf("invalid chat_id")
	}
	reason = normalizeArchiveReason(reason)
	if reason != "" && !isValidArchiveReason(reason) {
		return nil, fmt.Errorf("invalid archive reason")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	thread, err := scanChatThread(conn.QueryRowContext(
		ctx,
		`UPDATE chat_threads
		 SET archived_at = NOW(),
		     auto_archived_reason = $4
		 WHERE org_id = $1
		   AND user_id = $2
		   AND id = $3
		 RETURNING `+chatThreadColumns,
		workspaceID,
		userID,
		chatID,
		nullableString(&reason),
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to archive chat thread: %w", err)
	}
	return &thread, nil
}

func (s *ChatThreadStore) Unarchive(ctx context.Context, userID, chatID string) (*ChatThread, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	userID = strings.TrimSpace(userID)
	if !uuidRegex.MatchString(userID) {
		return nil, fmt.Errorf("invalid user_id")
	}
	chatID = strings.TrimSpace(chatID)
	if !uuidRegex.MatchString(chatID) {
		return nil, fmt.Errorf("invalid chat_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	thread, err := scanChatThread(conn.QueryRowContext(
		ctx,
		`UPDATE chat_threads
		 SET archived_at = NULL,
		     auto_archived_reason = NULL
		 WHERE org_id = $1
		   AND user_id = $2
		   AND id = $3
		 RETURNING `+chatThreadColumns,
		workspaceID,
		userID,
		chatID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to unarchive chat thread: %w", err)
	}
	return &thread, nil
}

func (s *ChatThreadStore) AutoArchiveByIssue(ctx context.Context, issueID string) (int64, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return 0, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return 0, fmt.Errorf("invalid issue_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	result, err := conn.ExecContext(
		ctx,
		`UPDATE chat_threads
		 SET archived_at = NOW(),
		     auto_archived_reason = $3
		 WHERE org_id = $1
		   AND issue_id = $2
		   AND archived_at IS NULL`,
		workspaceID,
		issueID,
		ChatThreadArchiveReasonIssueClosed,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to auto-archive issue chat threads: %w", err)
	}
	return result.RowsAffected()
}

func (s *ChatThreadStore) AutoArchiveByProject(ctx context.Context, projectID string) (int64, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return 0, ErrNoWorkspace
	}

	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return 0, fmt.Errorf("invalid project_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	result, err := conn.ExecContext(
		ctx,
		`UPDATE chat_threads
		 SET archived_at = NOW(),
		     auto_archived_reason = $3
		 WHERE org_id = $1
		   AND project_id = $2
		   AND archived_at IS NULL`,
		workspaceID,
		projectID,
		ChatThreadArchiveReasonProjectArchived,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to auto-archive project chat threads: %w", err)
	}
	return result.RowsAffected()
}

func normalizeChatThreadType(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func isValidChatThreadType(threadType string) bool {
	switch threadType {
	case ChatThreadTypeDM, ChatThreadTypeProject, ChatThreadTypeIssue:
		return true
	default:
		return false
	}
}

func normalizeArchiveReason(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func isValidArchiveReason(reason string) bool {
	switch reason {
	case ChatThreadArchiveReasonIssueClosed, ChatThreadArchiveReasonProjectArchived:
		return true
	default:
		return false
	}
}

func normalizeOptionalChatThreadUUID(value *string, field string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	if !uuidRegex.MatchString(trimmed) {
		return nil, fmt.Errorf("invalid %s", field)
	}
	return &trimmed, nil
}

func scanChatThread(scanner interface{ Scan(dest ...any) error }) (ChatThread, error) {
	var (
		thread          ChatThread
		agentID         sql.NullString
		projectID       sql.NullString
		issueID         sql.NullString
		archivedAt      sql.NullTime
		autoReason      sql.NullString
		lastMessageText sql.NullString
		title           sql.NullString
	)

	if err := scanner.Scan(
		&thread.ID,
		&thread.OrgID,
		&thread.UserID,
		&agentID,
		&projectID,
		&issueID,
		&thread.ThreadKey,
		&thread.ThreadType,
		&title,
		&lastMessageText,
		&archivedAt,
		&autoReason,
		&thread.LastMessageAt,
		&thread.CreatedAt,
		&thread.UpdatedAt,
	); err != nil {
		return ChatThread{}, err
	}

	if agentID.Valid {
		trimmed := strings.TrimSpace(agentID.String)
		thread.AgentID = &trimmed
	}
	if projectID.Valid {
		trimmed := strings.TrimSpace(projectID.String)
		thread.ProjectID = &trimmed
	}
	if issueID.Valid {
		trimmed := strings.TrimSpace(issueID.String)
		thread.IssueID = &trimmed
	}
	if archivedAt.Valid {
		utc := archivedAt.Time.UTC()
		thread.ArchivedAt = &utc
	}
	if autoReason.Valid {
		trimmed := strings.TrimSpace(autoReason.String)
		thread.AutoArchivedReason = &trimmed
	}
	if title.Valid {
		thread.Title = strings.TrimSpace(title.String)
	}
	if lastMessageText.Valid {
		thread.LastMessagePreview = strings.TrimSpace(lastMessageText.String)
	}

	thread.ThreadType = normalizeChatThreadType(thread.ThreadType)
	thread.ThreadKey = strings.TrimSpace(thread.ThreadKey)
	thread.LastMessageAt = thread.LastMessageAt.UTC()
	thread.CreatedAt = thread.CreatedAt.UTC()
	thread.UpdatedAt = thread.UpdatedAt.UTC()

	return thread, nil
}
