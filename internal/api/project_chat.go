package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

const (
	defaultProjectChatPageSize = 50
	maxProjectChatPageSize     = 200
	defaultProjectChatSearch   = 20
	maxProjectChatSearch       = 100
)

type ProjectChatHandler struct {
	ProjectStore *store.ProjectStore
	ChatStore    *store.ProjectChatStore
	IssueStore   *store.ProjectIssueStore
	DB           *sql.DB
	Hub          *ws.Hub
}

type createProjectChatMessageRequest struct {
	Author string `json:"author"`
	Body   string `json:"body"`
}

type projectChatMessagePayload struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type projectChatListResponse struct {
	Messages   []projectChatMessagePayload `json:"messages"`
	HasMore    bool                        `json:"has_more"`
	NextCursor string                      `json:"next_cursor,omitempty"`
}

type projectChatSearchItem struct {
	Message   projectChatMessagePayload `json:"message"`
	Relevance float64                   `json:"relevance"`
	Snippet   string                    `json:"snippet"`
}

type projectChatSearchResponse struct {
	Items []projectChatSearchItem `json:"items"`
	Total int                     `json:"total"`
}

type projectChatSaveToNotesResponse struct {
	Path  string `json:"path"`
	Saved bool   `json:"saved"`
}

type projectContentBootstrapResponse struct {
	Created []string `json:"created"`
}

type projectChatMessageCreatedEvent struct {
	Type    ws.MessageType            `json:"type"`
	Channel string                    `json:"channel"`
	Message projectChatMessagePayload `json:"message"`
}

func (h *ProjectChatHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.ProjectStore == nil || h.ChatStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}

	if err := h.requireProjectAccess(r.Context(), projectID); err != nil {
		handleProjectChatStoreError(w, err)
		return
	}

	limit, err := parseLimit(r.URL.Query().Get("limit"), defaultProjectChatPageSize, maxProjectChatPageSize)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
		return
	}

	cursor, err := parseCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid cursor"})
		return
	}

	var beforeCreatedAt *time.Time
	var beforeID *string
	if cursor != nil {
		beforeCreatedAt = &cursor.CreatedAt
		beforeID = &cursor.ID
	}

	messages, hasMore, err := h.ChatStore.List(r.Context(), projectID, limit, beforeCreatedAt, beforeID)
	if err != nil {
		handleProjectChatStoreError(w, err)
		return
	}

	payload := make([]projectChatMessagePayload, 0, len(messages))
	for _, message := range messages {
		payload = append(payload, toProjectChatPayload(message))
	}

	nextCursor := ""
	if hasMore && len(messages) > 0 {
		oldest := messages[len(messages)-1]
		nextCursor = encodeCursor(oldest.CreatedAt, oldest.ID)
	}

	sendJSON(w, http.StatusOK, projectChatListResponse{
		Messages:   payload,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
}

func (h *ProjectChatHandler) Create(w http.ResponseWriter, r *http.Request) {
	if h.ProjectStore == nil || h.ChatStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}

	if err := h.requireProjectAccess(r.Context(), projectID); err != nil {
		handleProjectChatStoreError(w, err)
		return
	}

	var req createProjectChatMessageRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	message, err := h.ChatStore.Create(r.Context(), store.CreateProjectChatMessageInput{
		ProjectID: projectID,
		Author:    req.Author,
		Body:      req.Body,
	})
	if err != nil {
		handleProjectChatStoreError(w, err)
		return
	}

	payload := toProjectChatPayload(*message)
	h.broadcastProjectChatCreated(r.Context(), payload)
	sendJSON(w, http.StatusCreated, map[string]projectChatMessagePayload{"message": payload})
}

func (h *ProjectChatHandler) Search(w http.ResponseWriter, r *http.Request) {
	if h.ProjectStore == nil || h.ChatStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}

	if err := h.requireProjectAccess(r.Context(), projectID); err != nil {
		handleProjectChatStoreError(w, err)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "q is required"})
		return
	}

	limit, err := parseLimit(r.URL.Query().Get("limit"), defaultProjectChatSearch, maxProjectChatSearch)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
		return
	}

	var author *string
	if raw := strings.TrimSpace(r.URL.Query().Get("author")); raw != "" {
		author = &raw
	}

	var from *time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
		value, err := parseDateTime(raw)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid from date"})
			return
		}
		from = &value
	}

	var to *time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
		value, err := parseDateTime(raw)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid to date"})
			return
		}
		to = &value
	}

	if from != nil && to != nil && from.After(*to) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "from must be before to"})
		return
	}

	results, err := h.ChatStore.Search(r.Context(), store.SearchProjectChatInput{
		ProjectID: projectID,
		Query:     query,
		Author:    author,
		From:      from,
		To:        to,
		Limit:     limit,
	})
	if err != nil {
		handleProjectChatStoreError(w, err)
		return
	}

	items := make([]projectChatSearchItem, 0, len(results))
	for _, result := range results {
		items = append(items, projectChatSearchItem{
			Message:   toProjectChatPayload(result.Message),
			Relevance: result.Relevance,
			Snippet:   result.Snippet,
		})
	}

	sendJSON(w, http.StatusOK, projectChatSearchResponse{Items: items, Total: len(items)})
}

func (h *ProjectChatHandler) SaveToNotes(w http.ResponseWriter, r *http.Request) {
	if h.ProjectStore == nil || h.ChatStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}
	messageID := strings.TrimSpace(chi.URLParam(r, "messageID"))
	if messageID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "message id is required"})
		return
	}

	if err := h.requireProjectAccess(r.Context(), projectID); err != nil {
		handleProjectChatStoreError(w, err)
		return
	}

	message, err := h.ChatStore.GetByID(r.Context(), projectID, messageID)
	if err != nil {
		handleProjectChatStoreError(w, err)
		return
	}

	relativePath, saved, err := saveProjectChatMessageToNotes(*message)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to save message to notes"})
		return
	}

	sendJSON(w, http.StatusOK, projectChatSaveToNotesResponse{
		Path:  relativePath,
		Saved: saved,
	})
}

func (h *ProjectChatHandler) BootstrapContent(w http.ResponseWriter, r *http.Request) {
	if h.ProjectStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}

	if err := h.requireProjectAccess(r.Context(), projectID); err != nil {
		handleProjectChatStoreError(w, err)
		return
	}

	result, err := bootstrapProjectContentLayout(projectID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to bootstrap content layout"})
		return
	}

	sendJSON(w, http.StatusOK, projectContentBootstrapResponse{Created: result.Created})
}

func (h *ProjectChatHandler) requireProjectAccess(ctx context.Context, projectID string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return store.ErrNoWorkspace
	}
	if h.ProjectStore == nil {
		return fmt.Errorf("project store unavailable")
	}
	_, err := h.ProjectStore.GetByID(ctx, projectID)
	return err
}

func (h *ProjectChatHandler) broadcastProjectChatCreated(ctx context.Context, message projectChatMessagePayload) {
	if h.Hub == nil {
		return
	}
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return
	}

	channel := projectChatChannel(message.ProjectID)
	payload, err := json.Marshal(projectChatMessageCreatedEvent{
		Type:    ws.MessageProjectChatMessageCreated,
		Channel: channel,
		Message: message,
	})
	if err != nil {
		return
	}

	h.Hub.BroadcastTopic(workspaceID, channel, payload)
}

func projectChatChannel(projectID string) string {
	return "project:" + strings.TrimSpace(projectID) + ":chat"
}

func toProjectChatPayload(message store.ProjectChatMessage) projectChatMessagePayload {
	return projectChatMessagePayload{
		ID:        message.ID,
		ProjectID: message.ProjectID,
		Author:    message.Author,
		Body:      message.Body,
		CreatedAt: message.CreatedAt,
		UpdatedAt: message.UpdatedAt,
	}
}

func saveProjectChatMessageToNotes(message store.ProjectChatMessage) (string, bool, error) {
	relativePath, absolutePath, err := resolveProjectContentWritePath(message.ProjectID, "/notes/project-chat.md")
	if err != nil {
		return "", false, err
	}

	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		return "", false, err
	}

	existingBytes, err := os.ReadFile(absolutePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	existing := string(existingBytes)

	metadataLine := projectChatSourceMarker(message)
	if strings.Contains(existing, metadataLine) {
		return relativePath, false, nil
	}

	var builder strings.Builder
	if strings.TrimSpace(existing) != "" {
		builder.WriteString(existing)
		if !strings.HasSuffix(existing, "\n") {
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}
	builder.WriteString(metadataLine)
	builder.WriteString("\n")
	builder.WriteString("### Chat capture (")
	builder.WriteString(message.Author)
	builder.WriteString(" · ")
	builder.WriteString(message.CreatedAt.UTC().Format(time.RFC3339))
	builder.WriteString(")\n\n")
	builder.WriteString(message.Body)
	builder.WriteString("\n")

	if err := os.WriteFile(absolutePath, []byte(builder.String()), 0o644); err != nil {
		return "", false, err
	}

	return relativePath, true, nil
}

func projectChatSourceMarker(message store.ProjectChatMessage) string {
	return fmt.Sprintf(
		"<!-- ottercamp_project_chat_source message_id=%s project_id=%s author=%s created_at=%s -->",
		message.ID,
		message.ProjectID,
		strings.ReplaceAll(strings.TrimSpace(message.Author), " ", "_"),
		message.CreatedAt.UTC().Format(time.RFC3339),
	)
}

func handleProjectChatStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
	default:
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
	}
}
