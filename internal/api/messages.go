package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

const messageSelectColumns = "id, org_id, task_id, thread_id, agent_id, role, content, metadata, created_at, updated_at"

const (
	getMessageByIDSQL = "SELECT " + messageSelectColumns + " FROM messages WHERE id = $1"
	createMessageSQL  = `INSERT INTO messages (
		org_id,
		task_id,
		thread_id,
		agent_id,
		role,
		content,
		metadata
	) VALUES ($1, $2, $3, $4, $5, $6, $7)
	RETURNING ` + messageSelectColumns
	updateMessageSQL   = "UPDATE messages SET content = $1, metadata = $2 WHERE id = $3 RETURNING " + messageSelectColumns
	deleteMessageSQL   = "DELETE FROM messages WHERE id = $1"
	listByTaskIDSQL    = "SELECT " + messageSelectColumns + " FROM messages WHERE org_id = $1 AND task_id = $2 ORDER BY created_at ASC"
	listByThreadIDSQL  = "SELECT " + messageSelectColumns + " FROM messages WHERE org_id = $1 AND thread_id = $2 ORDER BY created_at ASC"
)

var (
	messagesDB     *sql.DB
	messagesDBErr  error
	messagesDBOnce sync.Once
)

var allowedMessageRoles = map[string]struct{}{
	"user":      {},
	"assistant": {},
	"system":    {},
}

// Message represents a message API payload.
type Message struct {
	ID        string          `json:"id"`
	OrgID     string          `json:"org_id"`
	TaskID    *string         `json:"task_id,omitempty"`
	ThreadID  *string         `json:"thread_id,omitempty"`
	AgentID   *string         `json:"agent_id,omitempty"`
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// MessageHandler manages message endpoints.
type MessageHandler struct {
	Hub *ws.Hub
}

type MessagesResponse struct {
	OrgID    string    `json:"org_id"`
	TaskID   *string   `json:"task_id,omitempty"`
	ThreadID *string   `json:"thread_id,omitempty"`
	Messages []Message `json:"messages"`
}

type CreateMessageRequest struct {
	OrgID    string          `json:"org_id"`
	TaskID   *string         `json:"task_id,omitempty"`
	ThreadID *string         `json:"thread_id,omitempty"`
	AgentID  *string         `json:"agent_id,omitempty"`
	Role     string          `json:"role,omitempty"`
	Content  string          `json:"content"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type UpdateMessageRequest struct {
	Content  *string         `json:"content,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// ListMessages handles GET /api/messages?task_id=X or ?thread_id=X
func (h *MessageHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
	orgID := strings.TrimSpace(r.URL.Query().Get("org_id"))
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing query parameter: org_id"})
		return
	}
	if !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
	threadID := strings.TrimSpace(r.URL.Query().Get("thread_id"))

	if taskID == "" && threadID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing query parameter: task_id or thread_id"})
		return
	}
	if taskID != "" && threadID != "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "provide either task_id or thread_id, not both"})
		return
	}

	var taskPtr, threadPtr *string
	var query string
	var args []interface{}

	if taskID != "" {
		if !uuidRegex.MatchString(taskID) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid task_id"})
			return
		}
		taskPtr = &taskID
		query = listByTaskIDSQL
		args = []interface{}{orgID, taskID}
	} else {
		if !uuidRegex.MatchString(threadID) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid thread_id"})
			return
		}
		threadPtr = &threadID
		query = listByThreadIDSQL
		args = []interface{}{orgID, threadID}
	}

	db, err := getMessagesDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	rows, err := db.QueryContext(r.Context(), query, args...)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list messages"})
		return
	}
	defer rows.Close()

	messages := make([]Message, 0)
	for rows.Next() {
		msg, err := scanMessage(rows)
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

	sendJSON(w, http.StatusOK, MessagesResponse{
		OrgID:    orgID,
		TaskID:   taskPtr,
		ThreadID: threadPtr,
		Messages: messages,
	})
}

// CreateMessage handles POST /api/messages
func (h *MessageHandler) CreateMessage(w http.ResponseWriter, r *http.Request) {
	var req CreateMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	orgID := strings.TrimSpace(req.OrgID)
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing org_id"})
		return
	}
	if !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	if req.TaskID == nil && req.ThreadID == nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing task_id or thread_id"})
		return
	}

	if err := validateOptionalUUID(req.TaskID, "task_id"); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if err := validateOptionalUUID(req.ThreadID, "thread_id"); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if err := validateOptionalUUID(req.AgentID, "agent_id"); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing content"})
		return
	}

	role := normalizeRole(req.Role)
	if role == "" {
		role = "user"
	}
	if !isValidRole(role) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid role"})
		return
	}

	metadataBytes := normalizeMetadata(req.Metadata)
	if !json.Valid(metadataBytes) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid metadata"})
		return
	}

	db, err := getMessagesDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	args := []interface{}{
		orgID,
		nullableString(req.TaskID),
		nullableString(req.ThreadID),
		nullableString(req.AgentID),
		role,
		content,
		metadataBytes,
	}

	msg, err := scanMessage(db.QueryRowContext(r.Context(), createMessageSQL, args...))
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create message"})
		return
	}

	broadcastMessageCreated(h.Hub, msg)
	sendJSON(w, http.StatusOK, msg)
}

// UpdateMessage handles PATCH /api/messages/:id
func (h *MessageHandler) UpdateMessage(w http.ResponseWriter, r *http.Request) {
	messageID := strings.TrimSpace(chi.URLParam(r, "id"))
	if messageID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing message id"})
		return
	}
	if !uuidRegex.MatchString(messageID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid message id"})
		return
	}

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	db, err := getMessagesDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	existing, err := scanMessage(db.QueryRowContext(r.Context(), getMessageByIDSQL, messageID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "message not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load message"})
		return
	}

	content, contentSet, err := parseOptionalStringField(raw, "content")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid content"})
		return
	}
	if contentSet {
		if content == nil || strings.TrimSpace(*content) == "" {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "content cannot be empty"})
			return
		}
	}

	metadata, metadataSet, err := parseOptionalRawField(raw, "metadata")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid metadata"})
		return
	}
	if metadataSet && len(metadata) > 0 && !json.Valid(metadata) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid metadata"})
		return
	}

	updated := existing
	if contentSet {
		updated.Content = strings.TrimSpace(*content)
	}
	if metadataSet {
		updated.Metadata = normalizeMetadata(metadata)
	}

	result, err := scanMessage(db.QueryRowContext(r.Context(), updateMessageSQL, updated.Content, updated.Metadata, messageID))
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update message"})
		return
	}

	broadcastMessageUpdated(h.Hub, result)
	sendJSON(w, http.StatusOK, result)
}

// DeleteMessage handles DELETE /api/messages/:id
func (h *MessageHandler) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	messageID := strings.TrimSpace(chi.URLParam(r, "id"))
	if messageID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing message id"})
		return
	}
	if !uuidRegex.MatchString(messageID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid message id"})
		return
	}

	db, err := getMessagesDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	// Check if message exists
	existing, err := scanMessage(db.QueryRowContext(r.Context(), getMessageByIDSQL, messageID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "message not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load message"})
		return
	}

	_, err = db.ExecContext(r.Context(), deleteMessageSQL, messageID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to delete message"})
		return
	}

	broadcastMessageDeleted(h.Hub, existing)
	sendJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": messageID})
}

func getMessagesDB() (*sql.DB, error) {
	messagesDBOnce.Do(func() {
		dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dbURL == "" {
			messagesDBErr = errors.New("DATABASE_URL is not set")
			return
		}

		db, err := sql.Open("postgres", dbURL)
		if err != nil {
			messagesDBErr = err
			return
		}

		if err := db.Ping(); err != nil {
			_ = db.Close()
			messagesDBErr = err
			return
		}

		messagesDB = db
	})

	return messagesDB, messagesDBErr
}

func scanMessage(scanner interface{ Scan(...any) error }) (Message, error) {
	var msg Message
	var taskID sql.NullString
	var threadID sql.NullString
	var agentID sql.NullString
	var metadataBytes []byte

	err := scanner.Scan(
		&msg.ID,
		&msg.OrgID,
		&taskID,
		&threadID,
		&agentID,
		&msg.Role,
		&msg.Content,
		&metadataBytes,
		&msg.CreatedAt,
		&msg.UpdatedAt,
	)
	if err != nil {
		return msg, err
	}

	if taskID.Valid {
		msg.TaskID = &taskID.String
	}
	if threadID.Valid {
		msg.ThreadID = &threadID.String
	}
	if agentID.Valid {
		msg.AgentID = &agentID.String
	}

	if len(metadataBytes) == 0 {
		msg.Metadata = json.RawMessage("{}")
	} else {
		msg.Metadata = json.RawMessage(metadataBytes)
	}

	return msg, nil
}

func normalizeRole(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isValidRole(role string) bool {
	_, ok := allowedMessageRoles[role]
	return ok
}

func normalizeMetadata(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("{}")
	}
	return raw
}

// Broadcast functions for WebSocket events
type messageEvent struct {
	Type    ws.MessageType `json:"type"`
	Message Message        `json:"message"`
}

func broadcastMessageCreated(hub *ws.Hub, msg Message) {
	broadcastMessageEvent(hub, ws.MessageType("message.created"), msg)
}

func broadcastMessageUpdated(hub *ws.Hub, msg Message) {
	broadcastMessageEvent(hub, ws.MessageType("message.updated"), msg)
}

func broadcastMessageDeleted(hub *ws.Hub, msg Message) {
	broadcastMessageEvent(hub, ws.MessageType("message.deleted"), msg)
}

func broadcastMessageEvent(hub *ws.Hub, messageType ws.MessageType, msg Message) {
	if hub == nil {
		return
	}

	payload, err := json.Marshal(messageEvent{
		Type:    messageType,
		Message: msg,
	})
	if err != nil {
		return
	}

	hub.Broadcast(msg.OrgID, payload)
}
