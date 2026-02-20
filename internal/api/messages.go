package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

const (
	defaultThreadPageSize = 20
	maxThreadPageSize     = 100
)

var dmThreadAgentIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._ -]{0,127}$`)

// Message represents a task or DM message payload.
type Message struct {
	ID              string               `json:"id"`
	TaskID          *string              `json:"taskId,omitempty"`
	ThreadID        *string              `json:"threadId,omitempty"`
	SenderID        *string              `json:"senderId,omitempty"`
	SenderType      *string              `json:"senderType,omitempty"`
	SenderName      *string              `json:"senderName,omitempty"`
	SenderAvatarURL *string              `json:"senderAvatarUrl,omitempty"`
	Content         string               `json:"content"`
	Attachments     []AttachmentMetadata `json:"attachments,omitempty"`
	CreatedAt       time.Time            `json:"createdAt"`
	UpdatedAt       time.Time            `json:"updatedAt"`
}

type messageRow struct {
	ID              string
	OrgID           string
	TaskID          sql.NullString
	ThreadID        sql.NullString
	AuthorID        sql.NullString
	SenderID        sql.NullString
	SenderType      sql.NullString
	SenderName      sql.NullString
	SenderAvatarURL sql.NullString
	Content         string
	Attachments     []byte
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type MessageHandler struct {
	OpenClawDispatcher openClawMessageDispatcher
	Hub                *ws.Hub
	ChatThreadStore    *store.ChatThreadStore
}

type openClawMessageDispatcher interface {
	SendToOpenClaw(event interface{}) error
	IsConnected() bool
}

type dmDispatchTarget struct {
	AgentID    string
	SessionKey string
}

type openClawDMDispatchEvent struct {
	Type      string                 `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	OrgID     string                 `json:"org_id"`
	Data      openClawDMDispatchData `json:"data"`
}

type openClawDMDispatchAttachment struct {
	URL         string `json:"url"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

type openClawDMDispatchData struct {
	MessageID          string `json:"message_id"`
	ThreadID           string `json:"thread_id"`
	AgentID            string `json:"agent_id"`
	SessionKey         string `json:"session_key,omitempty"`
	Content            string `json:"content"`
	SenderID           string `json:"sender_id,omitempty"`
	SenderType         string `json:"sender_type,omitempty"`
	SenderName         string `json:"sender_name,omitempty"`
	InjectIdentity     bool   `json:"inject_identity,omitempty"`
	IncrementalContext string `json:"incremental_context,omitempty"`
	Attachments []openClawDMDispatchAttachment `json:"attachments,omitempty"`
}

type messageListResponse struct {
	Messages   []Message `json:"messages"`
	HasMore    bool      `json:"hasMore,omitempty"`
	NextCursor string    `json:"nextCursor,omitempty"`
	TotalCount int       `json:"totalCount,omitempty"`
}

type createMessageRequest struct {
	OrgID              *string
	TaskID             *string
	ThreadID           *string
	AuthorID           *string
	SenderID           *string
	SenderType         *string
	SenderName         *string
	SenderAvatarURL    *string
	IncrementalContext *string
	Content            string
	Attachments        []AttachmentMetadata
}

type updateMessageRequest struct {
	Content     *string
	Attachments *[]AttachmentMetadata
}

type cursorToken struct {
	CreatedAt time.Time
	ID        string
}

type dmDeliveryStatus struct {
	Attempted bool   `json:"attempted"`
	Delivered bool   `json:"delivered"`
	Error     string `json:"error,omitempty"`
}

// CreateMessage handles POST /api/messages.
func (h *MessageHandler) CreateMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	req, err := decodeCreateMessageRequest(r)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	if req.TaskID == nil && req.ThreadID == nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing task_id or thread_id"})
		return
	}
	if req.TaskID != nil && req.ThreadID != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "task_id and thread_id are mutually exclusive"})
		return
	}

	db, err := getTasksDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	orgID, err := resolveMessageOrgID(r.Context(), db, req)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	if strings.TrimSpace(req.Content) == "" && len(req.Attachments) == 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "content or attachments required"})
		return
	}

	if req.AuthorID != nil && !uuidRegex.MatchString(*req.AuthorID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid author_id"})
		return
	}

	if req.TaskID != nil && !uuidRegex.MatchString(*req.TaskID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid task_id"})
		return
	}

	if req.ThreadID != nil && strings.TrimSpace(*req.ThreadID) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid thread_id"})
		return
	}

	attachmentsJSON, err := json.Marshal(req.Attachments)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid attachments"})
		return
	}

	dispatchTarget, shouldDispatch, dispatchWarning, statusCode, dispatchErr := h.resolveDMDispatchTarget(r.Context(), db, orgID, req)
	if dispatchErr != nil {
		sendJSON(w, statusCode, errorResponse{Error: dispatchErr.Error()})
		return
	}

	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create message"})
		return
	}
	defer tx.Rollback()

	var messageID string
	err = tx.QueryRowContext(r.Context(), `
		INSERT INTO comments (
			org_id,
			task_id,
			author_id,
			thread_id,
			sender_id,
			sender_type,
			sender_name,
			sender_avatar_url,
			content,
			attachments
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id
	`,
		orgID,
		nullableString(req.TaskID),
		nullableString(req.AuthorID),
		nullableString(req.ThreadID),
		nullableString(req.SenderID),
		nullableString(req.SenderType),
		nullableString(req.SenderName),
		nullableString(req.SenderAvatarURL),
		req.Content,
		attachmentsJSON,
	).Scan(&messageID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create message"})
		return
	}

	if err := linkAttachmentsToMessage(r.Context(), tx, messageID, req.Attachments); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to link attachments"})
		return
	}

	if err := tx.Commit(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create message"})
		return
	}

	delivery := dmDeliveryStatus{
		Attempted: shouldDispatch,
		Delivered: false,
	}

	if shouldDispatch {
		event := h.buildDMDispatchEvent(r.Context(), db, orgID, messageID, req, dispatchTarget)
		dedupeKey := fmt.Sprintf("dm.message:%s", messageID)
		queuedForRetry := false
		if queued, err := enqueueOpenClawDispatchEvent(r.Context(), db, orgID, event.Type, dedupeKey, event); err != nil {
			log.Printf("dm dispatch enqueue failed for message %s: %v", messageID, err)
		} else {
			queuedForRetry = queued
		}

		if err := h.dispatchDMMessageToOpenClaw(event); err != nil {
			log.Printf("dm dispatch failed for message %s: %v", messageID, err)
			if queuedForRetry {
				delivery.Error = openClawDispatchQueuedWarning
			} else {
				delivery.Error = "agent delivery unavailable; message was saved"
			}
		} else {
			delivery.Delivered = true
			if queuedForRetry {
				if err := markOpenClawDispatchDeliveredByKey(r.Context(), db, dedupeKey); err != nil {
					log.Printf("failed to mark dm dispatch delivered for message %s: %v", messageID, err)
				}
			}
		}
	} else if dispatchWarning != "" {
		delivery.Error = dispatchWarning
	}

	message, err := loadMessageByID(r.Context(), db, messageID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load message"})
		return
	}
	h.touchDMChatThreadBestEffort(r.Context(), db, r, orgID, req, message)
	h.broadcastDMMessage(orgID, message)

	sendJSON(w, http.StatusOK, map[string]interface{}{
		"message":  message,
		"delivery": delivery,
	})
}

// GetMessage handles GET /api/messages/{id}.
func (h *MessageHandler) GetMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	messageID := strings.TrimSpace(chi.URLParam(r, "id"))
	if messageID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing message id"})
		return
	}
	if !uuidRegex.MatchString(messageID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid message id"})
		return
	}

	db, err := getTasksDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	message, err := loadMessageByID(r.Context(), db, messageID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "message not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load message"})
		return
	}

	sendJSON(w, http.StatusOK, map[string]Message{"message": message})
}

// UpdateMessage handles PUT /api/messages/{id}.
func (h *MessageHandler) UpdateMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	messageID := strings.TrimSpace(chi.URLParam(r, "id"))
	if messageID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing message id"})
		return
	}
	if !uuidRegex.MatchString(messageID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid message id"})
		return
	}

	updateReq, err := decodeUpdateMessageRequest(r)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if updateReq.Content == nil && updateReq.Attachments == nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "no fields to update"})
		return
	}

	db, err := getTasksDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	existing, err := loadMessageRowByID(r.Context(), db, messageID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "message not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load message"})
		return
	}

	content := existing.Content
	if updateReq.Content != nil {
		content = *updateReq.Content
	}

	attachments := existing.Attachments
	if updateReq.Attachments != nil {
		attachments, err = json.Marshal(*updateReq.Attachments)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid attachments"})
			return
		}
	}

	_, err = db.ExecContext(r.Context(), `
		UPDATE comments
		SET content = $1, attachments = $2
		WHERE id = $3
	`, content, attachments, messageID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update message"})
		return
	}

	if updateReq.Attachments != nil {
		if err := linkAttachmentsToMessage(r.Context(), db, messageID, *updateReq.Attachments); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to link attachments"})
			return
		}
	}

	message, err := loadMessageByID(r.Context(), db, messageID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load message"})
		return
	}

	sendJSON(w, http.StatusOK, map[string]Message{"message": message})
}

// DeleteMessage handles DELETE /api/messages/{id}.
func (h *MessageHandler) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	messageID := strings.TrimSpace(chi.URLParam(r, "id"))
	if messageID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing message id"})
		return
	}
	if !uuidRegex.MatchString(messageID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid message id"})
		return
	}

	db, err := getTasksDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	result, err := db.ExecContext(r.Context(), `DELETE FROM comments WHERE id = $1`, messageID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to delete message"})
		return
	}

	rows, err := result.RowsAffected()
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to delete message"})
		return
	}
	if rows == 0 {
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "message not found"})
		return
	}

	sendJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// ListMessages handles GET /api/messages?task_id=<uuid> or ?thread_id=<id>.
func (h *MessageHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	taskID := firstNonEmpty(
		strings.TrimSpace(r.URL.Query().Get("task_id")),
		strings.TrimSpace(r.URL.Query().Get("taskId")),
	)
	threadID := firstNonEmpty(
		strings.TrimSpace(r.URL.Query().Get("thread_id")),
		strings.TrimSpace(r.URL.Query().Get("threadId")),
	)

	if taskID == "" && threadID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing task_id or thread_id"})
		return
	}
	if taskID != "" && threadID != "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "task_id and thread_id are mutually exclusive"})
		return
	}

	if taskID != "" {
		h.listTaskMessages(w, r, taskID)
		return
	}
	h.listThreadMessages(w, r, threadID)
}

// ListThreadMessages handles GET /api/threads/{id}/messages.
func (h *MessageHandler) ListThreadMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	threadID := strings.TrimSpace(chi.URLParam(r, "id"))
	if threadID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing thread id"})
		return
	}

	threadType := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("type")))
	if threadType == "task" || (threadType == "" && uuidRegex.MatchString(threadID)) {
		h.listTaskMessages(w, r, threadID)
		return
	}

	h.listThreadMessages(w, r, threadID)
}

func (h *MessageHandler) listTaskMessages(w http.ResponseWriter, r *http.Request, taskID string) {
	if !uuidRegex.MatchString(taskID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid task_id"})
		return
	}

	db, err := getTasksDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	rows, err := db.QueryContext(r.Context(), `
		SELECT
			c.id,
			c.org_id,
			c.task_id,
			c.thread_id,
			c.author_id,
			COALESCE(c.sender_id, c.author_id::text),
			COALESCE(c.sender_type, CASE WHEN c.author_id IS NOT NULL THEN 'agent' ELSE NULL END),
			COALESCE(c.sender_name, a.display_name),
			COALESCE(c.sender_avatar_url, a.avatar_url),
			c.content,
			c.attachments,
			c.created_at,
			c.updated_at
		FROM comments c
		LEFT JOIN agents a ON c.author_id = a.id
		WHERE c.task_id = $1
		ORDER BY c.created_at ASC
	`, taskID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list messages"})
		return
	}
	defer rows.Close()

	messages := make([]Message, 0)
	for rows.Next() {
		row, err := scanMessageRow(rows)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read messages"})
			return
		}
		msg, err := buildMessageFromRow(row)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read messages"})
			return
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read messages"})
		return
	}

	sendJSON(w, http.StatusOK, messageListResponse{Messages: messages})
}

func (h *MessageHandler) listThreadMessages(w http.ResponseWriter, r *http.Request, threadID string) {
	db, err := getTasksDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	limit, err := parseLimit(r.URL.Query().Get("limit"), defaultThreadPageSize, maxThreadPageSize)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
		return
	}

	cursor, err := parseCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid cursor"})
		return
	}

	orgID := strings.TrimSpace(r.URL.Query().Get("org_id"))
	if orgID != "" && !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	totalCount, err := countThreadMessages(r.Context(), db, threadID, orgID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to count messages"})
		return
	}

	query, args := buildThreadMessagesQuery(threadID, orgID, cursor, limit+1)
	rows, err := db.QueryContext(r.Context(), query, args...)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list messages"})
		return
	}
	defer rows.Close()

	messages := make([]Message, 0)
	for rows.Next() {
		row, err := scanMessageRow(rows)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read messages"})
			return
		}
		msg, err := buildMessageFromRow(row)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read messages"})
			return
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read messages"})
		return
	}

	hasMore := false
	if len(messages) > limit {
		hasMore = true
		messages = messages[:limit]
	}

	// currently messages are newest-first; reverse to ascending
	reverseMessages(messages)

	nextCursor := ""
	if hasMore && len(messages) > 0 {
		oldest := messages[0]
		nextCursor = encodeCursor(oldest.CreatedAt, oldest.ID)
	}

	sendJSON(w, http.StatusOK, messageListResponse{
		Messages:   messages,
		HasMore:    hasMore,
		NextCursor: nextCursor,
		TotalCount: totalCount,
	})
}

func decodeCreateMessageRequest(r *http.Request) (createMessageRequest, error) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return createMessageRequest{}, errors.New("invalid request body")
	}

	taskID, _, err := parseOptionalStringFieldAny(raw, "task_id", "taskId")
	if err != nil {
		return createMessageRequest{}, errors.New("invalid task_id")
	}

	threadID, _, err := parseOptionalStringFieldAny(raw, "thread_id", "threadId")
	if err != nil {
		return createMessageRequest{}, errors.New("invalid thread_id")
	}

	orgID, _, err := parseOptionalStringFieldAny(raw, "org_id", "orgId")
	if err != nil {
		return createMessageRequest{}, errors.New("invalid org_id")
	}

	authorID, _, err := parseOptionalStringFieldAny(raw, "author_id", "authorId")
	if err != nil {
		return createMessageRequest{}, errors.New("invalid author_id")
	}

	senderID, _, err := parseOptionalStringFieldAny(raw, "sender_id", "senderId")
	if err != nil {
		return createMessageRequest{}, errors.New("invalid sender_id")
	}

	senderType, _, err := parseOptionalStringFieldAny(raw, "sender_type", "senderType")
	if err != nil {
		return createMessageRequest{}, errors.New("invalid sender_type")
	}

	senderName, _, err := parseOptionalStringFieldAny(raw, "sender_name", "senderName")
	if err != nil {
		return createMessageRequest{}, errors.New("invalid sender_name")
	}

	senderAvatarURL, _, err := parseOptionalStringFieldAny(raw, "sender_avatar_url", "senderAvatarUrl")
	if err != nil {
		return createMessageRequest{}, errors.New("invalid sender_avatar_url")
	}

	incrementalContext, _, err := parseOptionalStringFieldAny(raw, "incremental_context", "incrementalContext")
	if err != nil {
		return createMessageRequest{}, errors.New("invalid incremental_context")
	}

	contentRaw, ok := raw["content"]
	content := ""
	if ok && len(contentRaw) > 0 && string(contentRaw) != "null" {
		if err := json.Unmarshal(contentRaw, &content); err != nil {
			return createMessageRequest{}, errors.New("invalid content")
		}
	}

	attachments := []AttachmentMetadata{}
	if attachmentsRaw, ok := raw["attachments"]; ok && len(attachmentsRaw) > 0 && string(attachmentsRaw) != "null" {
		if err := json.Unmarshal(attachmentsRaw, &attachments); err != nil {
			return createMessageRequest{}, errors.New("invalid attachments")
		}
	}

	return createMessageRequest{
		OrgID:              trimPtr(orgID),
		TaskID:             trimPtr(taskID),
		ThreadID:           trimPtr(threadID),
		AuthorID:           trimPtr(authorID),
		SenderID:           trimPtr(senderID),
		SenderType:         trimPtr(senderType),
		SenderName:         trimPtr(senderName),
		SenderAvatarURL:    trimPtr(senderAvatarURL),
		IncrementalContext: trimPtr(incrementalContext),
		Content:            strings.TrimSpace(content),
		Attachments:        attachments,
	}, nil
}

func decodeUpdateMessageRequest(r *http.Request) (updateMessageRequest, error) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return updateMessageRequest{}, errors.New("invalid request body")
	}

	var content *string
	if rawContent, ok := raw["content"]; ok {
		if len(rawContent) == 0 || string(rawContent) == "null" {
			empty := ""
			content = &empty
		} else {
			var parsed string
			if err := json.Unmarshal(rawContent, &parsed); err != nil {
				return updateMessageRequest{}, errors.New("invalid content")
			}
			content = &parsed
		}
	}

	var attachments *[]AttachmentMetadata
	if rawAttachments, ok := raw["attachments"]; ok {
		if len(rawAttachments) == 0 || string(rawAttachments) == "null" {
			empty := []AttachmentMetadata{}
			attachments = &empty
		} else {
			var parsed []AttachmentMetadata
			if err := json.Unmarshal(rawAttachments, &parsed); err != nil {
				return updateMessageRequest{}, errors.New("invalid attachments")
			}
			attachments = &parsed
		}
	}

	return updateMessageRequest{
		Content:     content,
		Attachments: attachments,
	}, nil
}

func resolveMessageOrgID(ctx context.Context, db *sql.DB, req createMessageRequest) (string, error) {
	if req.TaskID != nil {
		var orgID string
		err := db.QueryRowContext(ctx, `SELECT org_id FROM tasks WHERE id = $1`, *req.TaskID).Scan(&orgID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", errors.New("task not found")
			}
			return "", errors.New("failed to load task")
		}
		return orgID, nil
	}

	if req.OrgID != nil && strings.TrimSpace(*req.OrgID) != "" {
		if !uuidRegex.MatchString(*req.OrgID) {
			return "", errors.New("invalid org_id")
		}
		return *req.OrgID, nil
	}

	if req.ThreadID != nil {
		threadID := strings.TrimSpace(*req.ThreadID)
		if threadID != "" {
			// Existing threads carry org scoping via stored messages.
			var orgID string
			err := db.QueryRowContext(
				ctx,
				`SELECT org_id FROM comments WHERE thread_id = $1 ORDER BY created_at DESC LIMIT 1`,
				threadID,
			).Scan(&orgID)
			if err == nil {
				return orgID, nil
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return "", errors.New("failed to load thread")
			}
		}

		if agentID := parseDMThreadAgentID(*req.ThreadID); agentID != "" {
			if !uuidRegex.MatchString(agentID) {
				return "", errors.New("missing org_id")
			}
			var orgID string
			err := db.QueryRowContext(ctx, `SELECT org_id FROM agents WHERE id = $1`, agentID).Scan(&orgID)
			if err == nil {
				return orgID, nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return "", errors.New("failed to load agent")
			}
		}
	}

	return "", errors.New("missing org_id")
}

func parseDMThreadAgentID(threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if !strings.HasPrefix(threadID, "dm_") {
		return ""
	}
	agentID := strings.TrimPrefix(threadID, "dm_")
	if !dmThreadAgentIDPattern.MatchString(agentID) {
		return ""
	}
	return agentID
}

func (h *MessageHandler) resolveDMDispatchTarget(
	ctx context.Context,
	db *sql.DB,
	orgID string,
	req createMessageRequest,
) (dmDispatchTarget, bool, string, int, error) {
	if req.ThreadID == nil {
		return dmDispatchTarget{}, false, "", 0, nil
	}

	if isAgentAuthoredMessage(req) {
		return dmDispatchTarget{}, false, "", 0, nil
	}

	agentID := parseDMThreadAgentID(*req.ThreadID)
	if agentID == "" && uuidRegex.MatchString(*req.ThreadID) {
		// thread_id is a chat record UUID — look up the thread_key and extract
		// the agent ID. Use a direct query without RLS dependency.
		var threadKey string
		_ = db.QueryRowContext(ctx,
			`SELECT COALESCE(thread_key, '') FROM chat_threads WHERE id = $1`,
			*req.ThreadID,
		).Scan(&threadKey)
		if threadKey != "" && strings.HasPrefix(threadKey, "dm:") {
			agentID = parseDMThreadAgentID(strings.TrimPrefix(threadKey, "dm:"))
		}
	}
	if agentID == "" {
		return dmDispatchTarget{}, false, "", 0, nil
	}

	var target dmDispatchTarget
	err := db.QueryRowContext(
		ctx,
		`SELECT id, COALESCE(session_key, '') FROM agent_sync_state WHERE org_id = $1 AND id = $2`,
		orgID,
		agentID,
	).Scan(&target.AgentID, &target.SessionKey)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return dmDispatchTarget{}, false, "", http.StatusInternalServerError, errors.New("failed to resolve agent thread")
		}
		// UUID not found in sync state — try resolving the agent's slug and
		// looking up sync state by slug. DM threads use UUIDs but sync state
		// stores agents by slug.
		if uuidRegex.MatchString(agentID) {
			var slug string
			if slugErr := db.QueryRowContext(ctx,
				`SELECT slug FROM agents WHERE org_id = $1 AND id = $2`,
				orgID, agentID,
			).Scan(&slug); slugErr == nil && slug != "" {
				err = db.QueryRowContext(ctx,
					`SELECT id, COALESCE(session_key, '') FROM agent_sync_state WHERE org_id = $1 AND id = $2`,
					orgID, slug,
				).Scan(&target.AgentID, &target.SessionKey)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return dmDispatchTarget{}, false, "", http.StatusInternalServerError, errors.New("failed to resolve agent thread")
				}
			}
		}
		// Not a UUID — try resolving by agent name, role, or legacy
		// agentRoles display name (handles DM thread keys like "Chief of Staff").
		if err != nil || target.AgentID == "" {
			// First, check the hardcoded agentRoles reverse map (legacy sync names).
			resolvedSlug := ""
			for slug, roleName := range agentRoles {
				if strings.EqualFold(roleName, agentID) || strings.EqualFold(slug, agentID) {
					resolvedSlug = slug
					break
				}
			}
			// If not found in agentRoles, try the agents table by name or role.
			if resolvedSlug == "" {
				_ = db.QueryRowContext(ctx,
					`SELECT COALESCE(slug, role, name) FROM agents WHERE org_id = $1 AND (name = $2 OR role = $2) LIMIT 1`,
					orgID, agentID,
				).Scan(&resolvedSlug)
			}
			if resolvedSlug != "" {
				err = db.QueryRowContext(ctx,
					`SELECT id, COALESCE(session_key, '') FROM agent_sync_state WHERE org_id = $1 AND id = $2`,
					orgID, resolvedSlug,
				).Scan(&target.AgentID, &target.SessionKey)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return dmDispatchTarget{}, false, "", http.StatusInternalServerError, errors.New("failed to resolve agent thread")
				}
			}
		}
	}
	if err == nil && strings.TrimSpace(target.SessionKey) != "" {
		originalSessionKey := strings.TrimSpace(target.SessionKey)
		normalizedSessionKey, normalizeErr := normalizeDMDispatchSessionKey(ctx, db, orgID, target.AgentID, target.SessionKey)
		if normalizeErr != nil {
			return dmDispatchTarget{}, false, "", http.StatusInternalServerError, errors.New("failed to resolve agent thread")
		}
		if strings.TrimSpace(normalizedSessionKey) != originalSessionKey {
			if _, updateErr := db.ExecContext(
				ctx,
				`UPDATE agent_sync_state SET session_key = $3, updated_at = NOW() WHERE org_id = $1 AND id = $2`,
				orgID,
				target.AgentID,
				normalizedSessionKey,
			); updateErr != nil {
				return dmDispatchTarget{}, false, "", http.StatusInternalServerError, errors.New("failed to resolve agent thread")
			}
		}
		target.SessionKey = normalizedSessionKey
		return target, true, "", 0, nil
	}

	// DM fallback: when sync state is empty, derive a safe session key from the
	// workspace agent identity (Elephant routes as its own OpenClaw agent slot).
	fallbackAgentID, fallbackErr := resolveDMThreadWorkspaceAgentID(ctx, db, orgID, agentID)
	if fallbackErr != nil {
		return dmDispatchTarget{}, false, "", http.StatusInternalServerError, errors.New("failed to resolve agent thread")
	}
	if fallbackAgentID != "" {
		fallbackSessionKey, fallbackSessionErr := fallbackDMDispatchSessionKey(ctx, db, orgID, fallbackAgentID)
		if fallbackSessionErr != nil {
			return dmDispatchTarget{}, false, "", http.StatusInternalServerError, errors.New("failed to resolve agent thread")
		}
		return dmDispatchTarget{
			AgentID:    fallbackAgentID,
			SessionKey: fallbackSessionKey,
		}, true, "", 0, nil
	}

	return dmDispatchTarget{}, false, "agent session unavailable; message was saved but not delivered", 0, nil
}

func resolveDMThreadWorkspaceAgentID(
	ctx context.Context,
	db *sql.DB,
	orgID string,
	threadAgentID string,
) (string, error) {
	candidate := strings.TrimSpace(threadAgentID)
	if candidate == "" {
		return "", nil
	}

	var resolved string
	if uuidRegex.MatchString(candidate) {
		err := db.QueryRowContext(
			ctx,
			`SELECT id FROM agents WHERE org_id = $1 AND id = $2`,
			orgID,
			candidate,
		).Scan(&resolved)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return resolveDefaultDMWorkspaceAgentID(ctx, db, orgID)
			}
			return "", err
		}
		return strings.TrimSpace(resolved), nil
	}

	err := db.QueryRowContext(
		ctx,
		`SELECT id FROM agents WHERE org_id = $1 AND LOWER(slug) = LOWER($2)`,
		orgID,
		candidate,
	).Scan(&resolved)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resolveDefaultDMWorkspaceAgentID(ctx, db, orgID)
		}
		return "", err
	}
	return strings.TrimSpace(resolved), nil
}

func resolveDefaultDMWorkspaceAgentID(
	ctx context.Context,
	db *sql.DB,
	orgID string,
) (string, error) {
	if strings.TrimSpace(orgID) == "" {
		return "", nil
	}

	var resolved string
	err := db.QueryRowContext(
		ctx,
		`SELECT a.id
		 FROM agents a
		 LEFT JOIN agent_sync_state ass
		   ON ass.org_id = a.org_id
		  AND (
			LOWER(ass.id) = LOWER(a.slug)
			OR LOWER(ass.id) = LOWER(a.id::text)
		  )
		 WHERE a.org_id = $1
		   AND a.status = 'active'
		 ORDER BY
		   CASE LOWER(a.slug)
		     WHEN 'main' THEN 0
		     WHEN 'frank' THEN 1
		     WHEN 'chameleon' THEN 2
		     WHEN 'elephant' THEN 3
		     ELSE 10
		   END,
		   CASE
		     WHEN LOWER(a.display_name) LIKE '%frank%' THEN 0
		     ELSE 1
		   END,
		   CASE
		     WHEN LOWER(COALESCE(ass.status, '')) = 'online' THEN 0
		     ELSE 1
		   END,
		   COALESCE(ass.updated_at, a.updated_at) DESC,
		   a.updated_at DESC,
		   a.display_name ASC
		 LIMIT 1`,
		orgID,
	).Scan(&resolved)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(resolved), nil
}

func canonicalChameleonSessionKey(agentID string) string {
	return "agent:chameleon:oc:" + strings.ToLower(strings.TrimSpace(agentID))
}

const dmRoutingExemptAgentMain = "main"
const dmRoutingExemptAgentLori = "lori"

var dmRoutingExemptAgentSlugs = map[string]struct{}{
	dmRoutingExemptAgentMain:    {},
	openClawSystemAgentElephant: {},
	dmRoutingExemptAgentLori:    {},
}

func isDMRoutingExemptAgentSlug(agentSlug string) bool {
	normalizedSlug := strings.ToLower(strings.TrimSpace(agentSlug))
	if normalizedSlug == "" {
		return false
	}
	_, ok := dmRoutingExemptAgentSlugs[normalizedSlug]
	return ok
}

func canonicalAgentMainSessionKey(agentSlug string) string {
	return "agent:" + strings.ToLower(strings.TrimSpace(agentSlug)) + ":main"
}

func dmFallbackSessionKeyForAgentSlug(agentSlug string, agentID string) string {
	if isDMRoutingExemptAgentSlug(agentSlug) {
		return canonicalAgentMainSessionKey(agentSlug)
	}
	return canonicalChameleonSessionKey(agentID)
}

func normalizeDMDispatchSessionKeyForAgentSlug(agentSlug string, agentID string, sessionKey string) string {
	normalizedSessionKey := strings.TrimSpace(sessionKey)
	normalizedSlug := strings.TrimSpace(agentSlug)
	if normalizedSessionKey == "" {
		return normalizedSessionKey
	}

	// Exempt agents (main, elephant, lori) always route to their canonical
	// main session, regardless of what sync state contains. The sync state
	// may point to a cron or project session which is wrong for DMs.
	if isDMRoutingExemptAgentSlug(normalizedSlug) {
		return canonicalAgentMainSessionKey(normalizedSlug)
	}
	if normalizedSlug != "" && strings.EqualFold(normalizedSessionKey, canonicalAgentMainSessionKey(normalizedSlug)) {
		return canonicalChameleonSessionKey(agentID)
	}
	return normalizedSessionKey
}

func fallbackDMDispatchSessionKey(
	ctx context.Context,
	db *sql.DB,
	orgID string,
	agentID string,
) (string, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", nil
	}

	agentSlug, err := resolveWorkspaceAgentSlugByID(ctx, db, orgID, agentID)
	if err != nil {
		return "", err
	}
	return dmFallbackSessionKeyForAgentSlug(agentSlug, agentID), nil
}

func normalizeDMDispatchSessionKey(
	ctx context.Context,
	db *sql.DB,
	orgID string,
	agentID string,
	sessionKey string,
) (string, error) {
	normalizedSessionKey := strings.TrimSpace(sessionKey)
	if normalizedSessionKey == "" {
		return fallbackDMDispatchSessionKey(ctx, db, orgID, agentID)
	}

	agentSlug, err := resolveWorkspaceAgentSlugByID(ctx, db, orgID, agentID)
	if err != nil {
		return "", err
	}
	return normalizeDMDispatchSessionKeyForAgentSlug(agentSlug, agentID, normalizedSessionKey), nil
}

func resolveWorkspaceAgentSlugByID(
	ctx context.Context,
	db *sql.DB,
	orgID string,
	agentID string,
) (string, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", nil
	}

	var slug string
	err := db.QueryRowContext(
		ctx,
		`SELECT COALESCE(slug, '') FROM agents WHERE org_id = $1 AND id = $2`,
		orgID,
		agentID,
	).Scan(&slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(slug), nil
}

type dmInjectionState struct {
	ThreadID           string
	SessionKey         string
	AgentID            string
	InjectedAt         sql.NullTime
	InjectionHash      string
	CompactionDetected bool
}

func loadDMInjectionState(
	ctx context.Context,
	db *sql.DB,
	orgID string,
	threadID string,
) (*dmInjectionState, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, nil
	}

	var state dmInjectionState
	err := db.QueryRowContext(
		ctx,
		`SELECT
			thread_id,
			COALESCE(session_key, ''),
			agent_id,
			injected_at,
			COALESCE(injection_hash, ''),
			compaction_detected
		 FROM dm_injection_state
		 WHERE org_id = $1 AND thread_id = $2`,
		orgID,
		threadID,
	).Scan(
		&state.ThreadID,
		&state.SessionKey,
		&state.AgentID,
		&state.InjectedAt,
		&state.InjectionHash,
		&state.CompactionDetected,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &state, nil
}

func computeDMAgentIdentityHash(
	ctx context.Context,
	db *sql.DB,
	orgID string,
	agentID string,
) (string, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", nil
	}

	var soulMD string
	var identityMD string
	var instructionsMD string
	err := db.QueryRowContext(
		ctx,
		`SELECT
			COALESCE(soul_md, ''),
			COALESCE(identity_md, ''),
			COALESCE(instructions_md, '')
		 FROM agents
		 WHERE org_id = $1 AND id = $2`,
		orgID,
		agentID,
	).Scan(&soulMD, &identityMD, &instructionsMD)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}

	hashInput := soulMD + "\x1f" + identityMD + "\x1f" + instructionsMD
	sum := sha256.Sum256([]byte(hashInput))
	return hex.EncodeToString(sum[:]), nil
}

func shouldInjectDMIdentity(state *dmInjectionState, currentHash string) bool {
	if state == nil {
		return true
	}
	if !state.InjectedAt.Valid {
		return true
	}
	if state.CompactionDetected {
		return true
	}

	normalizedCurrentHash := strings.TrimSpace(currentHash)
	normalizedStoredHash := strings.TrimSpace(state.InjectionHash)
	return normalizedCurrentHash != normalizedStoredHash
}

func persistDMInjectionState(
	ctx context.Context,
	db *sql.DB,
	orgID string,
	threadID string,
	sessionKey string,
	agentID string,
	currentHash string,
	injectIdentity bool,
) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}

	sessionKey = strings.TrimSpace(sessionKey)
	agentID = strings.TrimSpace(agentID)
	currentHash = strings.TrimSpace(currentHash)

	if injectIdentity {
		_, err := db.ExecContext(
			ctx,
			`INSERT INTO dm_injection_state (
				org_id,
				thread_id,
				session_key,
				agent_id,
				injected_at,
				injection_hash,
				compaction_detected,
				updated_at
			) VALUES ($1, $2, $3, $4, NOW(), NULLIF($5, ''), FALSE, NOW())
			ON CONFLICT (org_id, thread_id) DO UPDATE SET
				session_key = EXCLUDED.session_key,
				agent_id = EXCLUDED.agent_id,
				injected_at = EXCLUDED.injected_at,
				injection_hash = EXCLUDED.injection_hash,
				compaction_detected = FALSE,
				updated_at = NOW()`,
			orgID,
			threadID,
			sessionKey,
			agentID,
			currentHash,
		)
		return err
	}

	_, err := db.ExecContext(
		ctx,
		`UPDATE dm_injection_state
		 SET session_key = $3,
		     agent_id = $4,
		     updated_at = NOW()
		 WHERE org_id = $1 AND thread_id = $2`,
		orgID,
		threadID,
		sessionKey,
		agentID,
	)
	return err
}

func (h *MessageHandler) buildDMDispatchEvent(
	ctx context.Context,
	db *sql.DB,
	orgID string,
	messageID string,
	req createMessageRequest,
	target dmDispatchTarget,
) openClawDMDispatchEvent {
	threadID := ""
	if req.ThreadID != nil {
		threadID = strings.TrimSpace(*req.ThreadID)
	}

	event := openClawDMDispatchEvent{
		Type:      "dm.message",
		Timestamp: time.Now().UTC(),
		OrgID:     orgID,
		Data: openClawDMDispatchData{
			MessageID:  messageID,
			ThreadID:   threadID,
			AgentID:    target.AgentID,
			SessionKey: strings.TrimSpace(target.SessionKey),
			Content:    req.Content,
		},
	}
	if req.SenderID != nil {
		event.Data.SenderID = strings.TrimSpace(*req.SenderID)
	}
	if req.SenderType != nil {
		event.Data.SenderType = strings.TrimSpace(*req.SenderType)
	}
	if req.SenderName != nil {
		event.Data.SenderName = strings.TrimSpace(*req.SenderName)
	}
	if attachments := buildDMDispatchAttachments(req.Attachments); len(attachments) > 0 {
		event.Data.Attachments = attachments
	}
	if req.IncrementalContext != nil {
		event.Data.IncrementalContext = strings.TrimSpace(*req.IncrementalContext)
	}

	threadID = strings.TrimSpace(threadID)
	if threadID != "" {
		injectIdentity := true
		currentHash := ""

		if db != nil {
			identityHash, err := computeDMAgentIdentityHash(ctx, db, orgID, target.AgentID)
			if err != nil {
				log.Printf("messages: failed to compute DM identity hash for thread %s: %v", threadID, err)
			} else {
				currentHash = identityHash
				state, loadErr := loadDMInjectionState(ctx, db, orgID, threadID)
				if loadErr != nil {
					log.Printf("messages: failed to load DM injection state for thread %s: %v", threadID, loadErr)
				} else {
					injectIdentity = shouldInjectDMIdentity(state, currentHash)
					if persistErr := persistDMInjectionState(
						ctx,
						db,
						orgID,
						threadID,
						target.SessionKey,
						target.AgentID,
						currentHash,
						injectIdentity,
					); persistErr != nil {
						log.Printf("messages: failed to persist DM injection state for thread %s: %v", threadID, persistErr)
						injectIdentity = true
					}
				}
			}
		}

		event.Data.InjectIdentity = injectIdentity
	}

	return event
}

func buildDMDispatchAttachments(attachments []AttachmentMetadata) []openClawDMDispatchAttachment {
	if len(attachments) == 0 {
		return nil
	}

	result := make([]openClawDMDispatchAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		url := strings.TrimSpace(attachment.URL)
		if id := strings.TrimSpace(attachment.ID); id != "" {
			url = "/api/attachments/" + id
		}
		if url == "" {
			continue
		}
		filename := strings.TrimSpace(attachment.Filename)
		if filename == "" {
			filename = "attachment"
		}
		contentType := strings.TrimSpace(attachment.MimeType)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		sizeBytes := attachment.SizeBytes
		if sizeBytes < 0 {
			sizeBytes = 0
		}
		result = append(result, openClawDMDispatchAttachment{
			URL:         url,
			Filename:    filename,
			ContentType: contentType,
			SizeBytes:   sizeBytes,
		})
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (h *MessageHandler) dispatchDMMessageToOpenClaw(event openClawDMDispatchEvent) error {
	if h.OpenClawDispatcher == nil {
		return ws.ErrOpenClawNotConnected
	}
	return h.OpenClawDispatcher.SendToOpenClaw(event)
}

func isAgentAuthoredMessage(req createMessageRequest) bool {
	if req.SenderType == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(*req.SenderType), "agent")
}

func (h *MessageHandler) broadcastDMMessage(orgID string, message Message) {
	if h.Hub == nil {
		return
	}

	event, ok := buildDMMessageBroadcastEvent(message)
	if !ok {
		return
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return
	}

	h.Hub.Broadcast(orgID, payload)
}

func buildDMMessageBroadcastEvent(message Message) (map[string]interface{}, bool) {
	if message.ThreadID == nil {
		return nil, false
	}

	threadID := strings.TrimSpace(*message.ThreadID)
	if threadID == "" {
		return nil, false
	}

	data := map[string]interface{}{
		"threadId":  threadID,
		"thread_id": threadID,
		"message":   message,
	}

	if preview := strings.TrimSpace(message.Content); preview != "" {
		data["preview"] = preview
	}
	if message.SenderName != nil {
		if from := strings.TrimSpace(*message.SenderName); from != "" {
			data["from"] = from
		}
	}

	return map[string]interface{}{
		"type": "DMMessageReceived",
		"data": data,
	}, true
}

func (h *MessageHandler) touchDMChatThreadBestEffort(
	ctx context.Context,
	db *sql.DB,
	r *http.Request,
	orgID string,
	req createMessageRequest,
	message Message,
) {
	if h.ChatThreadStore == nil || db == nil || req.ThreadID == nil {
		return
	}

	threadID := strings.TrimSpace(*req.ThreadID)
	if threadID == "" || !strings.HasPrefix(threadID, "dm_") {
		return
	}

	identity, err := requireSessionIdentity(ctx, db, r)
	if err != nil {
		return
	}
	if identity.OrgID != orgID {
		return
	}

	var agentID *string
	if parsed := parseDMThreadAgentID(threadID); uuidRegex.MatchString(parsed) {
		agentID = &parsed
	}

	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
	title := resolveDMChatThreadTitle(ctx, db, identity.OrgID, agentID, req.SenderName)

	if _, err := h.ChatThreadStore.TouchThread(workspaceCtx, store.TouchChatThreadInput{
		UserID:             identity.UserID,
		AgentID:            agentID,
		ThreadKey:          "dm:" + threadID,
		ThreadType:         store.ChatThreadTypeDM,
		Title:              title,
		LastMessagePreview: strings.TrimSpace(message.Content),
		LastMessageAt:      message.CreatedAt,
	}); err != nil {
		log.Printf("messages: failed to touch DM chat thread for %s: %v", threadID, err)
	}
}

func resolveDMChatThreadTitle(
	ctx context.Context,
	db *sql.DB,
	orgID string,
	agentID *string,
	senderName *string,
) string {
	title := "Direct message"
	if senderName != nil {
		if trimmed := strings.TrimSpace(*senderName); trimmed != "" {
			title = trimmed
		}
	}
	if db == nil || agentID == nil {
		return title
	}

	var displayName string
	if err := db.QueryRowContext(
		ctx,
		`SELECT display_name FROM agents WHERE org_id = $1 AND id = $2`,
		orgID,
		*agentID,
	).Scan(&displayName); err != nil {
		return title
	}
	if trimmed := strings.TrimSpace(displayName); trimmed != "" {
		return trimmed
	}
	return title
}

func loadMessageByID(ctx context.Context, db *sql.DB, messageID string) (Message, error) {
	row, err := loadMessageRowByID(ctx, db, messageID)
	if err != nil {
		return Message{}, err
	}
	return buildMessageFromRow(row)
}

func loadMessageRowByID(ctx context.Context, db *sql.DB, messageID string) (messageRow, error) {
	row := db.QueryRowContext(ctx, `
		SELECT
			c.id,
			c.org_id,
			c.task_id,
			c.thread_id,
			c.author_id,
			COALESCE(c.sender_id, c.author_id::text),
			COALESCE(c.sender_type, CASE WHEN c.author_id IS NOT NULL THEN 'agent' ELSE NULL END),
			COALESCE(c.sender_name, a.display_name),
			COALESCE(c.sender_avatar_url, a.avatar_url),
			c.content,
			c.attachments,
			c.created_at,
			c.updated_at
		FROM comments c
		LEFT JOIN agents a ON c.author_id = a.id
		WHERE c.id = $1
	`, messageID)
	return scanMessageRow(row)
}

func scanMessageRow(scanner interface{ Scan(...any) error }) (messageRow, error) {
	var row messageRow
	err := scanner.Scan(
		&row.ID,
		&row.OrgID,
		&row.TaskID,
		&row.ThreadID,
		&row.AuthorID,
		&row.SenderID,
		&row.SenderType,
		&row.SenderName,
		&row.SenderAvatarURL,
		&row.Content,
		&row.Attachments,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	return row, err
}

func buildMessageFromRow(row messageRow) (Message, error) {
	msg := Message{
		ID:        row.ID,
		Content:   row.Content,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}

	if row.TaskID.Valid {
		msg.TaskID = &row.TaskID.String
	}
	if row.ThreadID.Valid {
		msg.ThreadID = &row.ThreadID.String
	}
	if row.SenderID.Valid {
		msg.SenderID = &row.SenderID.String
	}
	if row.SenderType.Valid {
		msg.SenderType = &row.SenderType.String
	}
	if row.SenderName.Valid {
		msg.SenderName = &row.SenderName.String
	}
	if row.SenderAvatarURL.Valid {
		msg.SenderAvatarURL = &row.SenderAvatarURL.String
	}

	if len(row.Attachments) > 0 {
		var attachments []AttachmentMetadata
		if err := json.Unmarshal(row.Attachments, &attachments); err != nil {
			return msg, err
		}
		if len(attachments) > 0 {
			msg.Attachments = attachments
		}
	}

	return msg, nil
}

type messageAttachmentLinker interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func linkAttachmentsToMessage(ctx context.Context, db messageAttachmentLinker, messageID string, attachments []AttachmentMetadata) error {
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.ID) == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, `UPDATE attachments SET comment_id = $1 WHERE id = $2`, messageID, attachment.ID); err != nil {
			return err
		}
	}
	return nil
}

func parseOptionalStringFieldAny(raw map[string]json.RawMessage, keys ...string) (*string, bool, error) {
	for _, key := range keys {
		if _, ok := raw[key]; ok {
			return parseOptionalStringField(raw, key)
		}
	}
	return nil, false, nil
}

func trimPtr(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func parseLimit(value string, defaultValue, maxValue int) (int, error) {
	if strings.TrimSpace(value) == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 || parsed > maxValue {
		return 0, fmt.Errorf("invalid limit")
	}
	return parsed, nil
}

func parseCursor(value string) (*cursorToken, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parts := strings.SplitN(value, "|", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid cursor")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, err
	}
	return &cursorToken{CreatedAt: createdAt, ID: parts[1]}, nil
}

func encodeCursor(createdAt time.Time, id string) string {
	return fmt.Sprintf("%s|%s", createdAt.UTC().Format(time.RFC3339Nano), id)
}

func countThreadMessages(ctx context.Context, db *sql.DB, threadID, orgID string) (int, error) {
	query := "SELECT COUNT(*) FROM comments WHERE thread_id = $1"
	args := []interface{}{threadID}
	if orgID != "" {
		query += " AND org_id = $2"
		args = append(args, orgID)
	}
	var total int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func buildThreadMessagesQuery(threadID, orgID string, cursor *cursorToken, limit int) (string, []interface{}) {
	conditions := []string{"c.thread_id = $1"}
	args := []interface{}{threadID}
	if orgID != "" {
		args = append(args, orgID)
		conditions = append(conditions, fmt.Sprintf("c.org_id = $%d", len(args)))
	}
	if cursor != nil {
		args = append(args, cursor.CreatedAt, cursor.ID)
		conditions = append(conditions, fmt.Sprintf("(c.created_at, c.id) < ($%d, $%d)", len(args)-1, len(args)))
	}

	query := `
		SELECT
			c.id,
			c.org_id,
			c.task_id,
			c.thread_id,
			c.author_id,
			COALESCE(c.sender_id, c.author_id::text),
			COALESCE(c.sender_type, CASE WHEN c.author_id IS NOT NULL THEN 'agent' ELSE NULL END),
			COALESCE(c.sender_name, a.display_name),
			COALESCE(c.sender_avatar_url, a.avatar_url),
			c.content,
			c.attachments,
			c.created_at,
			c.updated_at
		FROM comments c
		LEFT JOIN agents a ON c.author_id = a.id
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY c.created_at DESC, c.id DESC
		LIMIT ` + strconv.Itoa(limit)

	return query, args
}

func reverseMessages(messages []Message) {
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
}
