package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

const (
	defaultChatMessagesLimit = 200
	maxChatMessagesLimit     = 500
)

type ChatsHandler struct {
	ChatThreadStore *store.ChatThreadStore
	DB              *sql.DB
}

type chatThreadPayload struct {
	ID                 string  `json:"id"`
	ThreadKey          string  `json:"thread_key"`
	ThreadType         string  `json:"thread_type"`
	Title              string  `json:"title"`
	LastMessagePreview string  `json:"last_message_preview"`
	LastMessageAt      string  `json:"last_message_at"`
	AgentID            *string `json:"agent_id,omitempty"`
	ProjectID          *string `json:"project_id,omitempty"`
	IssueID            *string `json:"issue_id,omitempty"`
	ArchivedAt         *string `json:"archived_at,omitempty"`
	AutoArchivedReason *string `json:"auto_archived_reason,omitempty"`
}

type chatsListResponse struct {
	Chats []chatThreadPayload `json:"chats"`
}

type chatMessagePayload struct {
	ID         string `json:"id"`
	Content    string `json:"content"`
	SenderName string `json:"sender_name,omitempty"`
	SenderType string `json:"sender_type,omitempty"`
	CreatedAt  string `json:"created_at"`
}

type chatDetailResponse struct {
	Chat     chatThreadPayload    `json:"chat"`
	Messages []chatMessagePayload `json:"messages"`
}

func (h *ChatsHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.ChatThreadStore == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	identity, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, identity.OrgID)

	archived := parseBooleanQuery(r.URL.Query().Get("archived"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	threads, err := h.ChatThreadStore.ListByUser(ctx, identity.UserID, store.ListChatThreadsInput{
		Archived: archived,
		Query:    query,
		Limit:    200,
	})
	if err != nil {
		handleChatThreadStoreError(w, err)
		return
	}

	payload := make([]chatThreadPayload, 0, len(threads))
	for _, thread := range threads {
		payload = append(payload, toChatThreadPayload(thread))
	}
	sendJSON(w, http.StatusOK, chatsListResponse{Chats: payload})
}

func (h *ChatsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.ChatThreadStore == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	identity, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}

	chatID := strings.TrimSpace(chi.URLParam(r, "id"))
	if chatID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "chat id is required"})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, identity.OrgID)
	thread, err := h.ChatThreadStore.GetByIDForUser(ctx, identity.UserID, chatID)
	if err != nil {
		handleChatThreadStoreError(w, err)
		return
	}

	limit := defaultChatMessagesLimit
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, parseErr := strconv.Atoi(rawLimit)
		if parseErr != nil || parsed <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
			return
		}
		if parsed > maxChatMessagesLimit {
			parsed = maxChatMessagesLimit
		}
		limit = parsed
	}

	messages, err := h.loadChatMessages(ctx, *thread, limit)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load chat messages"})
		return
	}

	sendJSON(w, http.StatusOK, chatDetailResponse{
		Chat:     toChatThreadPayload(*thread),
		Messages: messages,
	})
}

func (h *ChatsHandler) Archive(w http.ResponseWriter, r *http.Request) {
	if h.ChatThreadStore == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	identity, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	chatID := strings.TrimSpace(chi.URLParam(r, "id"))
	if chatID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "chat id is required"})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, identity.OrgID)
	thread, err := h.ChatThreadStore.Archive(ctx, identity.UserID, chatID, "")
	if err != nil {
		handleChatThreadStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, map[string]chatThreadPayload{"chat": toChatThreadPayload(*thread)})
}

func (h *ChatsHandler) Unarchive(w http.ResponseWriter, r *http.Request) {
	if h.ChatThreadStore == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	identity, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	chatID := strings.TrimSpace(chi.URLParam(r, "id"))
	if chatID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "chat id is required"})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, identity.OrgID)
	thread, err := h.ChatThreadStore.Unarchive(ctx, identity.UserID, chatID)
	if err != nil {
		handleChatThreadStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, map[string]chatThreadPayload{"chat": toChatThreadPayload(*thread)})
}

func (h *ChatsHandler) requireIdentity(w http.ResponseWriter, r *http.Request) (sessionIdentity, bool) {
	identity, err := requireSessionIdentity(r.Context(), h.DB, r)
	if err == nil {
		return identity, true
	}

	if errors.Is(err, errMissingAuthentication) || errors.Is(err, errInvalidSessionToken) {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return sessionIdentity{}, false
	}
	if errors.Is(err, errWorkspaceMismatch) {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "workspace mismatch"})
		return sessionIdentity{}, false
	}

	sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to authenticate session"})
	return sessionIdentity{}, false
}

func (h *ChatsHandler) loadChatMessages(ctx context.Context, thread store.ChatThread, limit int) ([]chatMessagePayload, error) {
	conn, err := store.WithWorkspace(ctx, h.DB)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	switch thread.ThreadType {
	case store.ChatThreadTypeDM:
		threadID := strings.TrimPrefix(thread.ThreadKey, "dm:")
		rows, err := conn.QueryContext(
			ctx,
			`SELECT id::text, content, COALESCE(sender_name, ''), COALESCE(sender_type, ''), created_at
			 FROM comments
			 WHERE org_id = $1 AND thread_id = $2
			 ORDER BY created_at ASC, id ASC
			 LIMIT $3`,
			thread.OrgID,
			threadID,
			limit,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanChatMessages(rows)
	case store.ChatThreadTypeProject:
		if thread.ProjectID == nil {
			return []chatMessagePayload{}, nil
		}
		rows, err := conn.QueryContext(
			ctx,
			`SELECT id::text, body, author, 'project', created_at
			 FROM project_chat_messages
			 WHERE org_id = $1 AND project_id = $2
			 ORDER BY created_at ASC, id ASC
			 LIMIT $3`,
			thread.OrgID,
			*thread.ProjectID,
			limit,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanChatMessages(rows)
	case store.ChatThreadTypeIssue:
		if thread.IssueID == nil {
			return []chatMessagePayload{}, nil
		}
		rows, err := conn.QueryContext(
			ctx,
			`SELECT c.id::text, c.body, COALESCE(a.display_name, ''), 'issue', c.created_at
			 FROM project_issue_comments c
			 LEFT JOIN agents a ON a.id = c.author_agent_id
			 WHERE c.org_id = $1 AND c.issue_id = $2
			 ORDER BY c.created_at ASC, c.id ASC
			 LIMIT $3`,
			thread.OrgID,
			*thread.IssueID,
			limit,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanChatMessages(rows)
	default:
		return []chatMessagePayload{}, nil
	}
}

func scanChatMessages(rows *sql.Rows) ([]chatMessagePayload, error) {
	messages := make([]chatMessagePayload, 0)
	for rows.Next() {
		var (
			message chatMessagePayload
			created time.Time
		)
		if err := rows.Scan(
			&message.ID,
			&message.Content,
			&message.SenderName,
			&message.SenderType,
			&created,
		); err != nil {
			return nil, err
		}
		message.ID = strings.TrimSpace(message.ID)
		message.Content = strings.TrimSpace(message.Content)
		message.SenderName = strings.TrimSpace(message.SenderName)
		message.SenderType = strings.TrimSpace(strings.ToLower(message.SenderType))
		message.CreatedAt = created.UTC().Format(time.RFC3339)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}

func toChatThreadPayload(thread store.ChatThread) chatThreadPayload {
	payload := chatThreadPayload{
		ID:                 thread.ID,
		ThreadKey:          thread.ThreadKey,
		ThreadType:         thread.ThreadType,
		Title:              thread.Title,
		LastMessagePreview: thread.LastMessagePreview,
		LastMessageAt:      thread.LastMessageAt.UTC().Format(time.RFC3339),
		AgentID:            thread.AgentID,
		ProjectID:          thread.ProjectID,
		IssueID:            thread.IssueID,
		AutoArchivedReason: thread.AutoArchivedReason,
	}
	if thread.ArchivedAt != nil {
		formatted := thread.ArchivedAt.UTC().Format(time.RFC3339)
		payload.ArchivedAt = &formatted
	}
	return payload
}

func parseBooleanQuery(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func handleChatThreadStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
	default:
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	}
}
