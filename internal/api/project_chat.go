package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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

	projectChatSessionResetAuthor = "__otter_session__"
	projectChatSessionResetPrefix = "project_chat_session_reset:"
)

type ProjectChatHandler struct {
	ProjectStore       *store.ProjectStore
	ChatStore          *store.ProjectChatStore
	ChatThreadStore    *store.ChatThreadStore
	QuestionnaireStore *store.QuestionnaireStore
	IssueStore         *store.ProjectIssueStore
	DB                 *sql.DB
	Hub                *ws.Hub
	OpenClawDispatcher openClawMessageDispatcher
}

type createProjectChatMessageRequest struct {
	Author        string   `json:"author"`
	Body          string   `json:"body"`
	SenderType    string   `json:"sender_type,omitempty"`
	AttachmentIDs []string `json:"attachment_ids,omitempty"`
}

type projectChatMessagePayload struct {
	ID          string               `json:"id"`
	ProjectID   string               `json:"project_id"`
	Author      string               `json:"author"`
	Body        string               `json:"body"`
	Attachments []AttachmentMetadata `json:"attachments"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

type projectChatListResponse struct {
	Messages       []projectChatMessagePayload `json:"messages"`
	Questionnaires []questionnairePayload      `json:"questionnaires"`
	HasMore        bool                        `json:"has_more"`
	NextCursor     string                      `json:"next_cursor,omitempty"`
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

type openClawProjectChatDispatchEvent struct {
	Type      string                          `json:"type"`
	Timestamp time.Time                       `json:"timestamp"`
	OrgID     string                          `json:"org_id"`
	Data      openClawProjectChatDispatchData `json:"data"`
}

type openClawProjectChatDispatchData struct {
	MessageID   string `json:"message_id"`
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name,omitempty"`
	AgentID     string `json:"agent_id"`
	AgentName   string `json:"agent_name,omitempty"`
	SessionKey  string `json:"session_key"`
	Content     string `json:"content"`
	Author      string `json:"author,omitempty"`
}

type projectChatDispatchTarget struct {
	AgentID    string
	AgentName  string
	SessionKey string
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
	questionnaires := make([]questionnairePayload, 0)
	if h.QuestionnaireStore != nil {
		records, listErr := h.QuestionnaireStore.ListByContext(
			r.Context(),
			store.QuestionnaireContextProjectChat,
			projectID,
		)
		if listErr != nil {
			handleQuestionnaireStoreError(w, listErr)
			return
		}
		payloads, mapErr := mapQuestionnairePayloads(records)
		if mapErr != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load questionnaires"})
			return
		}
		questionnaires = payloads
	}

	nextCursor := ""
	if hasMore && len(messages) > 0 {
		oldest := messages[len(messages)-1]
		nextCursor = encodeCursor(oldest.CreatedAt, oldest.ID)
	}

	sendJSON(w, http.StatusOK, projectChatListResponse{
		Messages:       payload,
		Questionnaires: questionnaires,
		HasMore:        hasMore,
		NextCursor:     nextCursor,
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
	req.Author = strings.TrimSpace(req.Author)
	req.Body = strings.TrimSpace(req.Body)
	req.SenderType = strings.TrimSpace(strings.ToLower(req.SenderType))
	attachmentIDs, err := normalizeProjectChatAttachmentIDs(req.AttachmentIDs)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid attachment_ids"})
		return
	}
	req.AttachmentIDs = attachmentIDs

	if req.Author == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "author is required"})
		return
	}
	if req.Author == projectChatSessionResetAuthor {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "author is reserved"})
		return
	}
	if req.Body == "" && len(req.AttachmentIDs) == 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "body or attachment_ids is required"})
		return
	}
	if req.SenderType != "" && req.SenderType != "user" && req.SenderType != "agent" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid sender_type"})
		return
	}
	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace context"})
		return
	}
	if len(req.AttachmentIDs) > 0 {
		if h.DB == nil {
			sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
			return
		}
		for _, attachmentID := range req.AttachmentIDs {
			var exists bool
			if err := h.DB.QueryRowContext(
				r.Context(),
				`SELECT EXISTS(SELECT 1 FROM attachments WHERE id = $1 AND org_id = $2)`,
				attachmentID,
				workspaceID,
			).Scan(&exists); err != nil {
				sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to validate attachment_ids"})
				return
			}
			if !exists {
				sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid attachment_ids"})
				return
			}
		}
	}

	target, shouldDispatch, dispatchWarning, dispatchErr := h.resolveProjectChatDispatchTarget(r.Context(), projectID, req.SenderType)
	if dispatchErr != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to resolve project chat target"})
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
	if len(req.AttachmentIDs) > 0 {
		for _, attachmentID := range req.AttachmentIDs {
			if err := LinkAttachmentToChatMessage(h.DB, workspaceID, attachmentID, message.ID); err != nil {
				if errors.Is(err, ErrAttachmentNotFound) {
					sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid attachment_ids"})
					return
				}
				sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to link attachments"})
				return
			}
		}
		refreshed, err := h.ChatStore.GetByID(r.Context(), projectID, message.ID)
		if err != nil {
			handleProjectChatStoreError(w, err)
			return
		}
		message = refreshed
	}

	payload := toProjectChatPayload(*message)
	h.touchProjectChatThreadBestEffort(r.Context(), r, projectID, workspaceID, payload)
	delivery := dmDeliveryStatus{
		Attempted: shouldDispatch,
		Delivered: false,
	}
	if shouldDispatch {
		event, err := h.buildProjectChatDispatchEvent(r.Context(), payload, target)
		if err != nil {
			delivery.Error = "agent delivery unavailable; message was saved"
		} else {
			dedupeKey := fmt.Sprintf("project.chat.message:%s", payload.ID)
			queuedForRetry := false
			if queued, queueErr := enqueueOpenClawDispatchEvent(r.Context(), h.DB, event.OrgID, event.Type, dedupeKey, event); queueErr != nil {
				log.Printf("project chat dispatch enqueue failed for message %s: %v", payload.ID, queueErr)
			} else {
				queuedForRetry = queued
			}

			if err := h.dispatchProjectChatMessageToOpenClaw(event); err != nil {
				if queuedForRetry {
					delivery.Error = openClawDispatchQueuedWarning
				} else {
					delivery.Error = "agent delivery unavailable; message was saved"
				}
			} else {
				delivery.Delivered = true
				if queuedForRetry {
					if err := markOpenClawDispatchDeliveredByKey(r.Context(), h.DB, dedupeKey); err != nil {
						log.Printf("failed to mark project chat dispatch delivered for message %s: %v", payload.ID, err)
					}
				}
			}
		}
	} else if dispatchWarning != "" {
		delivery.Error = dispatchWarning
	}

	h.broadcastProjectChatCreated(r.Context(), payload)
	sendJSON(w, http.StatusCreated, map[string]interface{}{
		"message":  payload,
		"delivery": delivery,
	})
}

func (h *ProjectChatHandler) touchProjectChatThreadBestEffort(
	ctx context.Context,
	r *http.Request,
	projectID string,
	workspaceID string,
	message projectChatMessagePayload,
) {
	if h.ChatThreadStore == nil || h.DB == nil {
		return
	}

	identity, err := requireSessionIdentity(ctx, h.DB, r)
	if err != nil {
		return
	}
	if identity.OrgID != workspaceID {
		return
	}

	projectName := "Project chat"
	if h.ProjectStore != nil {
		workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
		if project, projectErr := h.ProjectStore.GetByID(workspaceCtx, projectID); projectErr == nil {
			if trimmedName := strings.TrimSpace(project.Name); trimmedName != "" {
				projectName = trimmedName
			}
		}
	}

	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
	if _, err := h.ChatThreadStore.TouchThread(workspaceCtx, store.TouchChatThreadInput{
		UserID:             identity.UserID,
		ProjectID:          &projectID,
		ThreadKey:          "project:" + projectID,
		ThreadType:         store.ChatThreadTypeProject,
		Title:              projectName,
		LastMessagePreview: strings.TrimSpace(message.Body),
		LastMessageAt:      message.CreatedAt,
	}); err != nil {
		log.Printf("project chat: failed to touch thread for project %s: %v", projectID, err)
	}
}

func (h *ProjectChatHandler) ResetSession(w http.ResponseWriter, r *http.Request) {
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

	sessionID := strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
	markerBody := buildProjectChatSessionResetBody(sessionID)

	message, err := h.ChatStore.Create(r.Context(), store.CreateProjectChatMessageInput{
		ProjectID: projectID,
		Author:    projectChatSessionResetAuthor,
		Body:      markerBody,
	})
	if err != nil {
		handleProjectChatStoreError(w, err)
		return
	}

	payload := toProjectChatPayload(*message)
	h.broadcastProjectChatCreated(r.Context(), payload)
	sendJSON(w, http.StatusCreated, map[string]any{
		"message":    payload,
		"session_id": sessionID,
	})
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

func projectChatSessionKey(agentID, projectID, sessionID string) string {
	base := fmt.Sprintf("agent:%s:project:%s", strings.TrimSpace(agentID), strings.TrimSpace(projectID))
	if strings.TrimSpace(sessionID) == "" {
		return base
	}
	return base + ":session:" + strings.TrimSpace(sessionID)
}

func buildProjectChatSessionResetBody(sessionID string) string {
	return projectChatSessionResetPrefix + strings.TrimSpace(sessionID)
}

func parseProjectChatSessionResetID(body string) (string, bool) {
	trimmed := strings.TrimSpace(body)
	if !strings.HasPrefix(trimmed, projectChatSessionResetPrefix) {
		return "", false
	}
	sessionID := strings.TrimSpace(strings.TrimPrefix(trimmed, projectChatSessionResetPrefix))
	if sessionID == "" {
		return "", false
	}
	return sessionID, true
}

func (h *ProjectChatHandler) buildProjectChatDispatchEvent(
	ctx context.Context,
	message projectChatMessagePayload,
	target projectChatDispatchTarget,
) (openClawProjectChatDispatchEvent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return openClawProjectChatDispatchEvent{}, fmt.Errorf("missing workspace")
	}

	projectName := ""
	if h.DB != nil {
		resolvedName, resolveErr := h.resolveProjectName(ctx, message.ProjectID)
		if resolveErr != nil {
			log.Printf("[project-chat] failed to resolve project name for %s: %v", message.ProjectID, resolveErr)
		} else {
			projectName = resolvedName
		}
	}

	event := openClawProjectChatDispatchEvent{
		Type:      "project.chat.message",
		Timestamp: time.Now().UTC(),
		OrgID:     workspaceID,
		Data: openClawProjectChatDispatchData{
			MessageID:   message.ID,
			ProjectID:   message.ProjectID,
			ProjectName: projectName,
			AgentID:     target.AgentID,
			AgentName:   target.AgentName,
			SessionKey:  target.SessionKey,
			Content:     message.Body,
			Author:      message.Author,
		},
	}

	return event, nil
}

func (h *ProjectChatHandler) resolveProjectName(ctx context.Context, projectID string) (string, error) {
	if h.DB == nil {
		return "", nil
	}
	var name string
	err := h.DB.QueryRowContext(
		ctx,
		`SELECT name FROM projects WHERE id = $1`,
		strings.TrimSpace(projectID),
	).Scan(&name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(name), nil
}

func (h *ProjectChatHandler) dispatchProjectChatMessageToOpenClaw(
	event openClawProjectChatDispatchEvent,
) error {
	if h.OpenClawDispatcher == nil {
		return ws.ErrOpenClawNotConnected
	}
	return h.OpenClawDispatcher.SendToOpenClaw(event)
}

func (h *ProjectChatHandler) resolveProjectChatDispatchTarget(
	ctx context.Context,
	projectID string,
	senderType string,
) (projectChatDispatchTarget, bool, string, error) {
	if senderType == "agent" {
		return projectChatDispatchTarget{}, false, "", nil
	}
	if h.DB == nil {
		return projectChatDispatchTarget{}, false, "project agent unavailable; message was saved but not delivered", nil
	}

	agentID, agentName, err := h.resolveProjectLeadAgent(ctx, projectID)
	if err != nil {
		return projectChatDispatchTarget{}, false, "", err
	}
	if agentID == "" {
		return projectChatDispatchTarget{}, false, "project agent unavailable; message was saved but not delivered", nil
	}

	sessionID, err := h.resolveActiveProjectChatSessionID(ctx, projectID)
	if err != nil {
		return projectChatDispatchTarget{}, false, "", err
	}

	return projectChatDispatchTarget{
		AgentID:    agentID,
		AgentName:  agentName,
		SessionKey: projectChatSessionKey(agentID, projectID, sessionID),
	}, true, "", nil
}

func (h *ProjectChatHandler) resolveActiveProjectChatSessionID(
	ctx context.Context,
	projectID string,
) (string, error) {
	if h.DB == nil {
		return "", nil
	}

	var markerBody string
	err := h.DB.QueryRowContext(
		ctx,
		`SELECT body
		 FROM project_chat_messages
		 WHERE project_id = $1
		   AND author = $2
		   AND body LIKE $3
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1`,
		strings.TrimSpace(projectID),
		projectChatSessionResetAuthor,
		projectChatSessionResetPrefix+"%",
	).Scan(&markerBody)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	sessionID, ok := parseProjectChatSessionResetID(markerBody)
	if !ok {
		return "", nil
	}
	return sessionID, nil
}

func (h *ProjectChatHandler) resolveProjectLeadAgent(
	ctx context.Context,
	projectID string,
) (string, string, error) {
	// 0) Prefer explicit project-level primary agent setting when available.
	if supportsProjectPrimaryAgentColumn(ctx, h.DB) {
		var primarySlug, primaryName string
		err := h.DB.QueryRowContext(ctx, `
			SELECT a.slug, a.display_name
			FROM projects p
			INNER JOIN agents a ON a.id = p.primary_agent_id
			WHERE p.id = $1
		`, projectID).Scan(&primarySlug, &primaryName)
		if err == nil {
			return strings.TrimSpace(primarySlug), strings.TrimSpace(primaryName), nil
		}
		if err != nil && err != sql.ErrNoRows {
			return "", "", err
		}
	}

	// 1) Fall back to explicit issue owners for this project.
	var err error
	var ownerSlug, ownerName string
	err = h.DB.QueryRowContext(ctx, `
		SELECT a.slug, a.display_name
		FROM project_issue_participants pip
		INNER JOIN project_issues pi ON pi.id = pip.issue_id
		INNER JOIN agents a ON a.id = pip.agent_id
		WHERE pi.project_id = $1
		  AND pip.removed_at IS NULL
		  AND pip.role = 'owner'
		GROUP BY a.slug, a.display_name
		ORDER BY COUNT(*) DESC, a.display_name ASC
		LIMIT 1
	`, projectID).Scan(&ownerSlug, &ownerName)
	if err == nil {
		return strings.TrimSpace(ownerSlug), strings.TrimSpace(ownerName), nil
	}
	if err != nil && err != sql.ErrNoRows {
		return "", "", err
	}

	// 2) Fall back to most frequently assigned agent on project tasks.
	var assigneeSlug, assigneeName string
	err = h.DB.QueryRowContext(ctx, `
		SELECT a.slug, a.display_name
		FROM tasks t
		INNER JOIN agents a ON a.id = t.assigned_agent_id
		WHERE t.project_id = $1
		GROUP BY a.slug, a.display_name
		ORDER BY COUNT(*) DESC, MAX(t.updated_at) DESC
		LIMIT 1
	`, projectID).Scan(&assigneeSlug, &assigneeName)
	if err == nil {
		return strings.TrimSpace(assigneeSlug), strings.TrimSpace(assigneeName), nil
	}
	if err != nil && err != sql.ErrNoRows {
		return "", "", err
	}

	// 3) Final fallback for new/empty projects: pick any active workspace agent.
	// Prefer known router agents first so project chat remains usable out of the box.
	var fallbackSlug, fallbackName string
	err = h.DB.QueryRowContext(ctx, `
		SELECT a.slug, a.display_name
		FROM projects p
		INNER JOIN agents a ON a.org_id = p.org_id
		WHERE p.id = $1
		  AND a.status = 'active'
		ORDER BY
			CASE LOWER(a.slug)
				WHEN 'chameleon' THEN 0
				WHEN 'marcus' THEN 1
				WHEN 'elephant' THEN 2
				ELSE 10
			END,
			a.updated_at DESC,
			a.display_name ASC
		LIMIT 1
	`, projectID).Scan(&fallbackSlug, &fallbackName)
	if err == nil {
		return strings.TrimSpace(fallbackSlug), strings.TrimSpace(fallbackName), nil
	}
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	return "", "", err
}

func toProjectChatPayload(message store.ProjectChatMessage) projectChatMessagePayload {
	attachments := make([]AttachmentMetadata, 0)
	if len(message.Attachments) > 0 && string(message.Attachments) != "null" {
		if err := json.Unmarshal(message.Attachments, &attachments); err != nil {
			attachments = make([]AttachmentMetadata, 0)
		}
	}
	return projectChatMessagePayload{
		ID:          message.ID,
		ProjectID:   message.ProjectID,
		Author:      message.Author,
		Body:        message.Body,
		Attachments: attachments,
		CreatedAt:   message.CreatedAt,
		UpdatedAt:   message.UpdatedAt,
	}
}

func normalizeProjectChatAttachmentIDs(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, id := range raw {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if !uuidRegex.MatchString(trimmed) {
			return nil, fmt.Errorf("invalid attachment id")
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out, nil
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
