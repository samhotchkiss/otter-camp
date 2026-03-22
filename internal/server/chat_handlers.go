package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	chatDefaultListLimit = 50
	chatMaxListLimit     = 200
	chatRedactedContent  = "[redacted]"
)

type chatSessionRepository interface {
	UpdateTitle(ctx context.Context, id uuid.UUID, title *string) (repo.ChatSession, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) (repo.ChatSession, error)
}

type chatReadCursorRepository interface {
	Upsert(ctx context.Context, cursor repo.ChatReadCursor) (repo.ChatReadCursor, error)
	GetBySessionAndUser(ctx context.Context, sessionID, userID uuid.UUID) (repo.ChatReadCursor, error)
}

type chatArtifactRepository interface {
	ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatArtifact, error)
	ListByMessage(ctx context.Context, messageID uuid.UUID) ([]repo.ChatArtifact, error)
}

type chatMessageRepository interface {
	ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatMessage, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, errorMessage string) (repo.ChatMessage, error)
	Redact(ctx context.Context, id uuid.UUID) (repo.ChatMessage, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.ChatMessage, error)
}

type ChatRouteRegistrar struct {
	handlers chatHandlers
}

func NewChatRouteRegistrar(service chat.ChatService, pool *pgxpool.Pool) *ChatRouteRegistrar {
	h := chatHandlers{
		service:  service,
		testMode: strings.EqualFold(strings.TrimSpace(os.Getenv("OTTERCAMP_MODE")), "test"),
	}
	if pool != nil {
		h.sessions = repo.NewChatSessionRepo(pool)
		h.readCursors = repo.NewChatReadCursorRepo(pool)
		h.artifacts = repo.NewChatArtifactRepo(pool)
		h.messages = repo.NewChatMessageRepo(pool)
		h.agents = repo.NewAgentRepo(pool)
		h.tasks = repo.NewProjectTaskRepo(pool)
		h.assignments = repo.NewAgentProjectAssignmentRepo(pool)
	}
	return &ChatRouteRegistrar{handlers: h}
}

func (r *ChatRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.With(middleware.RequireAnyScope(requireReadScope("chat")...)).Get("/chat-sessions", r.handlers.listSessions)
	router.With(middleware.RequireAnyScope(requireWriteScope("chat")...)).Post("/chat-sessions", r.handlers.createSession)
	router.With(middleware.RequireAnyScope(requireReadScope("chat")...)).Get("/chat-sessions/{id}", r.handlers.getSession)
	router.With(middleware.RequireAnyScope(requireWriteScope("chat")...)).Patch("/chat-sessions/{id}", r.handlers.patchSession)
	router.With(middleware.RequireAnyScope(requireWriteScope("chat")...)).Delete("/chat-sessions/{id}", r.handlers.deleteSession)

	router.With(middleware.RequireAnyScope(requireReadScope("chat")...)).Get("/chat-sessions/{id}/messages", r.handlers.listMessages)
	router.With(middleware.RequireAnyScope(requireReadScope("chat")...)).Get("/chat-sessions/{id}/messages/{mid}", r.handlers.getMessage)
	router.With(middleware.RequireAnyScope(requireWriteScope("chat")...)).Post("/chat-sessions/{id}/messages", r.handlers.appendMessage)
	router.With(middleware.RequireAnyScope(requireWriteScope("chat")...)).Patch("/chat-sessions/{id}/messages/{mid}", r.handlers.editQueuedMessage)
	router.With(middleware.RequireAnyScope(requireWriteScope("chat")...)).Delete("/chat-sessions/{id}/messages/{mid}", r.handlers.redactMessage)
	router.With(middleware.RequireAnyScope(requireReadScope("chat")...)).Get("/chat-sessions/{id}/turns", r.handlers.listTurns)
	router.With(middleware.RequireAnyScope(requireReadScope("chat")...)).Get("/chat-sessions/{id}/export", r.handlers.exportSession)

	router.With(middleware.RequireAnyScope(requireWriteScope("chat")...)).Post("/chat-sessions/{id}/cancel-turn", r.handlers.cancelTurn)
	router.With(middleware.RequireAnyScope(requireWriteScope("chat")...)).Post("/chat-sessions/{id}/cancel", r.handlers.cancelTurn)
	router.With(middleware.RequireAnyScope(requireWriteScope("chat")...)).Post("/chat-sessions/{id}/messages/{mid}/steer", r.handlers.steerTurn)

	router.With(middleware.RequireAnyScope(requireReadScope("chat")...)).Get("/chat-sessions/{id}/messages/{mid}/reactions", r.handlers.listReactions)
	router.With(middleware.RequireAnyScope(requireWriteScope("chat")...)).Post("/chat-sessions/{id}/messages/{mid}/reactions", r.handlers.addReaction)
	router.With(middleware.RequireAnyScope(requireWriteScope("chat")...)).Delete("/chat-sessions/{id}/messages/{mid}/reactions/{rid}", r.handlers.removeReaction)

	router.With(middleware.RequireAnyScope(requireReadScope("chat")...)).Get("/chat-sessions/{id}/participants", r.handlers.listParticipants)
	router.With(middleware.RequireAnyScope(requireWriteScope("chat")...)).Post("/chat-sessions/{id}/participants", r.handlers.addParticipant)
	router.With(middleware.RequireAnyScope(requireWriteScope("chat")...)).Delete("/chat-sessions/{id}/participants/{pid}", r.handlers.removeParticipant)

	router.With(middleware.RequireAnyScope(requireReadScope("chat")...)).Get("/chat-sessions/{id}/read-cursor", r.handlers.getReadCursor)
	router.With(middleware.RequireAnyScope(requireWriteScope("chat")...)).Put("/chat-sessions/{id}/read-cursor", r.handlers.putReadCursor)

	router.With(middleware.RequireAnyScope(requireReadScope("chat")...)).Get("/chat-sessions/{id}/artifacts", r.handlers.listArtifacts)
}

type orgAgentLister interface {
	GetStarterTrio(ctx context.Context, organizationID uuid.UUID) ([]repo.Agent, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

type projectTaskReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
}

type projectPMAssignmentReader interface {
	GetPM(ctx context.Context, projectID uuid.UUID) (repo.AgentProjectAssignment, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.AgentProjectAssignment, error)
}

type chatHandlers struct {
	service chat.ChatService

	sessions    chatSessionRepository
	readCursors chatReadCursorRepository
	artifacts   chatArtifactRepository
	messages    chatMessageRepository
	agents      orgAgentLister
	tasks       projectTaskReader
	assignments projectPMAssignmentReader
	testMode    bool
}

type createChatSessionRequest struct {
	ScopeType string    `json:"scope_type"`
	ScopeID   uuid.UUID `json:"scope_id"`
	Mode      string    `json:"mode"`
	Title     *string   `json:"title"`
}

type patchChatSessionRequest struct {
	Title  *string `json:"title"`
	Mode   *string `json:"mode"`
	Status *string `json:"status"`
}

type appendChatMessageRequest struct {
	Content       string `json:"content"`
	ContentFormat string `json:"content_format"`
	Role          string `json:"role,omitempty"`
}

type editChatMessageRequest struct {
	Content string `json:"content"`
}

type cancelTurnRequest struct {
	Reason *string    `json:"reason"`
	TurnID *uuid.UUID `json:"turn_id"`
}

type steerTurnRequest struct {
	Content string `json:"content"`
}

type addReactionRequest struct {
	Emoji    string `json:"emoji,omitempty"`
	Reaction string `json:"reaction,omitempty"`
}

type addParticipantRequest struct {
	ParticipantType string    `json:"participant_type"`
	ParticipantID   uuid.UUID `json:"participant_id"`
	Role            string    `json:"role"`
}

type putReadCursorRequest struct {
	LastReadSequence int64 `json:"last_read_sequence"`
}

type chatSessionResponse struct {
	ID             uuid.UUID               `json:"id"`
	OrganizationID uuid.UUID               `json:"organization_id"`
	ScopeType      string                  `json:"scope_type"`
	ScopeID        uuid.UUID               `json:"scope_id"`
	Mode           string                  `json:"mode"`
	Status         string                  `json:"status"`
	Title          *string                 `json:"title"`
	CreatedByType  string                  `json:"created_by_type"`
	CreatedByID    uuid.UUID               `json:"created_by_id"`
	LastMessageAt  *time.Time              `json:"last_message_at"`
	TurnCount      int                     `json:"turn_count"`
	MessageCount   int                     `json:"message_count"`
	Metadata       json.RawMessage         `json:"metadata,omitempty"`
	CreatedAt      time.Time               `json:"created_at"`
	UpdatedAt      time.Time               `json:"updated_at"`
	ClosedAt       *time.Time              `json:"closed_at,omitempty"`
	Participants   []chatParticipantRecord `json:"participants,omitempty"`
}

type chatMessageRecord struct {
	ID             uuid.UUID       `json:"id"`
	SessionID      uuid.UUID       `json:"session_id"`
	TurnID         *uuid.UUID      `json:"turn_id"`
	SequenceNumber int64           `json:"sequence_number"`
	AuthorType     *string         `json:"author_type"`
	AuthorID       *uuid.UUID      `json:"author_id"`
	Role           string          `json:"role"`
	Content        string          `json:"content"`
	ContentFormat  string          `json:"content_format"`
	Status         string          `json:"status"`
	IsRedacted     bool            `json:"is_redacted"`
	ToolCallID     *string         `json:"tool_call_id"`
	ErrorMessage   *string         `json:"error_message,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type chatParticipantRecord struct {
	ID                     uuid.UUID  `json:"id"`
	SessionID              uuid.UUID  `json:"session_id"`
	ParticipantType        string     `json:"participant_type"`
	ParticipantID          uuid.UUID  `json:"participant_id"`
	Role                   string     `json:"role"`
	NotificationPreference string     `json:"notification_preference"`
	JoinedAt               time.Time  `json:"joined_at"`
	RemovedAt              *time.Time `json:"removed_at,omitempty"`
}

type chatReactionRecord struct {
	ID          uuid.UUID `json:"id"`
	MessageID   uuid.UUID `json:"message_id"`
	SessionID   uuid.UUID `json:"session_id"`
	ReactorType string    `json:"reactor_type"`
	ReactorID   uuid.UUID `json:"reactor_id"`
	Reaction    string    `json:"reaction,omitempty"`
	Emoji       string    `json:"emoji"`
	CreatedAt   time.Time `json:"created_at"`
}

type chatMessageDetailRecord struct {
	chatMessageRecord
	Reactions []chatReactionRecord `json:"reactions"`
}

type chatTurnRecord struct {
	ID                uuid.UUID  `json:"id"`
	SessionID         uuid.UUID  `json:"session_id"`
	TurnNumber        int        `json:"turn_number"`
	RespondingType    string     `json:"responding_type"`
	RespondingID      uuid.UUID  `json:"responding_id"`
	Status            string     `json:"status"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	DurationMS        *int       `json:"duration_ms,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

type chatReadCursorRecord struct {
	UserID           uuid.UUID `json:"user_id"`
	SessionID        uuid.UUID `json:"session_id"`
	LastReadSequence int64     `json:"last_read_sequence"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type chatArtifactRecord struct {
	ID           uuid.UUID `json:"id"`
	SessionID    uuid.UUID `json:"session_id"`
	MessageID    uuid.UUID `json:"message_id"`
	ArtifactType string    `json:"artifact_type"`
	Filename     *string   `json:"filename"`
	ContentType  *string   `json:"content_type"`
	StorageKey   *string   `json:"storage_key"`
	URL          *string   `json:"url"`
	ByteSize     *int64    `json:"byte_size"`
	CreatedAt    time.Time `json:"created_at"`
}

func (h chatHandlers) createSession(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	var req createChatSessionRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	scopeType := strings.TrimSpace(req.ScopeType)
	scopeID := req.ScopeID
	if strings.EqualFold(scopeType, "organization") && scopeID == uuid.Nil {
		scopeID = principal.OrganizationID
	}
	if scopeType == "" || scopeID == uuid.Nil || strings.TrimSpace(req.Mode) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "scope_type, scope_id, and mode are required")
		return
	}

	session, err := h.service.CreateSession(r.Context(), chat.CreateSessionInput{
		OrganizationID: principal.OrganizationID,
		ScopeType:      scopeType,
		ScopeID:        scopeID,
		Mode:           req.Mode,
		Title:          trimOptionalString(req.Title),
	})
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	participantType := "human_user"
	if isAgentAPIKey(principal) {
		participantType = "agent"
	}
	_, _ = h.service.AddParticipant(r.Context(), session.ID, participantType, principal.UserID, "owner")

	taskResponderID := uuid.Nil
	if strings.EqualFold(scopeType, "project_task") {
		if responderID, ok := h.resolveTaskResponderID(r.Context(), principal.OrganizationID, scopeID, req.Mode); ok {
			taskResponderID = responderID
			_, _ = h.service.AddParticipant(r.Context(), session.ID, "agent", responderID, "responder")
		}
	}
	h.autoAddAssignedProjectParticipants(r.Context(), session.ID, scopeType, scopeID, taskResponderID)

	// For org-scope sessions, automatically add starter-trio staff agents as
	// participants so they are immediately available for conversations.
	if strings.EqualFold(scopeType, "organization") && h.agents != nil {
		if starterAgents, err := h.agents.GetStarterTrio(r.Context(), principal.OrganizationID); err == nil {
			for _, agent := range starterAgents {
				_, _ = h.service.AddParticipant(r.Context(), session.ID, "agent", agent.ID, "member")
			}
		}
	}

	responder.JSON(w, http.StatusCreated, toChatSessionResponse(session, nil))
}

func (h chatHandlers) resolveTaskResponderID(ctx context.Context, organizationID, taskID uuid.UUID, mode string) (uuid.UUID, bool) {
	if h.tasks != nil && taskID != uuid.Nil {
		if taskRecord, err := h.tasks.GetByID(ctx, taskID); err == nil {
			if taskRecord.OrganizationID == organizationID || taskRecord.OrganizationID == uuid.Nil {
				if strings.EqualFold(strings.TrimSpace(mode), "sync") {
					if h.assignments != nil {
						if pm, pmErr := h.assignments.GetPM(ctx, taskRecord.ProjectID); pmErr == nil && pm.IsActive && pm.AgentID != uuid.Nil {
							return pm.AgentID, true
						}
					}
					if taskRecord.AssignedAgentID != nil && *taskRecord.AssignedAgentID != uuid.Nil {
						return *taskRecord.AssignedAgentID, true
					}
				} else {
					if taskRecord.AssignedAgentID != nil && *taskRecord.AssignedAgentID != uuid.Nil {
						return *taskRecord.AssignedAgentID, true
					}
					if h.assignments != nil {
						if pm, pmErr := h.assignments.GetPM(ctx, taskRecord.ProjectID); pmErr == nil && pm.IsActive && pm.AgentID != uuid.Nil {
							return pm.AgentID, true
						}
					}
				}
			}
		}
	}
	return h.resolveFrankStarterID(ctx, organizationID)
}

func (h chatHandlers) autoAddAssignedProjectParticipants(ctx context.Context, sessionID uuid.UUID, scopeType string, scopeID uuid.UUID, skipAgentID uuid.UUID) {
	if h.assignments == nil || sessionID == uuid.Nil || scopeID == uuid.Nil {
		return
	}

	projectID := scopeID
	if strings.EqualFold(scopeType, "project_task") {
		if h.tasks == nil {
			return
		}
		taskRecord, err := h.tasks.GetByID(ctx, scopeID)
		if err != nil {
			return
		}
		projectID = taskRecord.ProjectID
	}
	if !strings.EqualFold(scopeType, "project") && !strings.EqualFold(scopeType, "project_task") {
		return
	}

	assignments, err := h.assignments.ListByProject(ctx, projectID)
	if err != nil || len(assignments) == 0 {
		return
	}

	for _, assignment := range assignments {
		if !assignment.IsActive || assignment.AgentID == uuid.Nil {
			continue
		}
		if assignment.AgentID == skipAgentID {
			continue
		}
		_, _ = h.service.AddParticipant(ctx, sessionID, "agent", assignment.AgentID, "member")
	}
}

func (h chatHandlers) resolveFrankStarterID(ctx context.Context, organizationID uuid.UUID) (uuid.UUID, bool) {
	if h.agents == nil || organizationID == uuid.Nil {
		return uuid.Nil, false
	}
	starterAgents, err := h.agents.GetStarterTrio(ctx, organizationID)
	if err != nil {
		return uuid.Nil, false
	}
	for _, agent := range starterAgents {
		if strings.EqualFold(strings.TrimSpace(agent.DisplayName), "frank") && agent.ID != uuid.Nil {
			return agent.ID, true
		}
	}
	for _, agent := range starterAgents {
		if strings.EqualFold(strings.TrimSpace(agent.AgentType), "general") && agent.ID != uuid.Nil {
			return agent.ID, true
		}
	}
	for _, agent := range starterAgents {
		if agent.ID != uuid.Nil {
			return agent.ID, true
		}
	}
	return uuid.Nil, false
}

func (h chatHandlers) listSessions(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	query := r.URL.Query()
	limit := clampListLimit(query.Get("limit"), chatDefaultListLimit, chatMaxListLimit)

	var scopeID *uuid.UUID
	if rawScopeID := strings.TrimSpace(query.Get("scope_id")); rawScopeID != "" {
		parsed, err := uuid.Parse(rawScopeID)
		if err != nil {
			responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid scope_id")
			return
		}
		scopeID = &parsed
	}

	var cursorSessionID *uuid.UUID
	if rawCursor := strings.TrimSpace(query.Get("cursor")); rawCursor != "" {
		decoded, err := decodeUUIDCursor(rawCursor)
		if err != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
			return
		}
		cursorSessionID = &decoded
	}

	status := strings.TrimSpace(query.Get("status"))
	if status == "" {
		status = "active"
	}

	items, err := h.service.ListSessions(r.Context(), chat.SessionFilter{
		OrganizationID:  principal.OrganizationID,
		ScopeType:       strings.TrimSpace(query.Get("scope_type")),
		ScopeID:         scopeID,
		Status:          status,
		Mode:            strings.TrimSpace(query.Get("mode")),
		Limit:           limit + 1,
		CursorSessionID: cursorSessionID,
	})
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}

	payload := make([]chatSessionResponse, 0, len(items))
	for _, item := range items {
		payload = append(payload, toChatSessionResponse(item, nil))
	}

	var nextCursor *string
	if hasMore && len(items) > 0 {
		encoded := encodeUUIDCursor(items[len(items)-1].ID)
		nextCursor = &encoded
	}

	responder.JSONList(w, http.StatusOK, payload, api.PaginationMeta{
		NextCursor: nextCursor,
		HasMore:    hasMore,
		Limit:      limit,
	})
}

func (h chatHandlers) getSession(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	_, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}

	session, err := h.service.GetSession(r.Context(), sessionID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	participants, err := h.service.ListParticipants(r.Context(), sessionID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}
	participantPayload := toChatParticipantResponses(participants)

	responder.JSON(w, http.StatusOK, toChatSessionResponse(session, participantPayload))
}

func (h chatHandlers) patchSession(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	_, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}

	var req patchChatSessionRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.Title == nil && req.Mode == nil && req.Status == nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "at least one patch field is required")
		return
	}

	if req.Mode != nil {
		if err := h.service.SwitchMode(r.Context(), sessionID, strings.TrimSpace(*req.Mode)); err != nil {
			h.respondChatError(responder, w, err)
			return
		}
	}

	if req.Title != nil {
		if h.sessions == nil {
			responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat session repository unavailable")
			return
		}
		if _, err := h.sessions.UpdateTitle(r.Context(), sessionID, trimOptionalString(req.Title)); err != nil {
			h.respondChatError(responder, w, err)
			return
		}
	}

	if req.Status != nil {
		if h.sessions == nil {
			responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat session repository unavailable")
			return
		}
		targetStatus := strings.TrimSpace(strings.ToLower(*req.Status))
		if targetStatus != "archived" {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "status must be archived")
			return
		}
		current, err := h.service.GetSession(r.Context(), sessionID)
		if err != nil {
			h.respondChatError(responder, w, err)
			return
		}
		if current.Status != "active" && current.Status != "archived" {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid status transition")
			return
		}
		if current.Status == "active" {
			if _, err := h.sessions.UpdateStatus(r.Context(), sessionID, "archived"); err != nil {
				h.respondChatError(responder, w, err)
				return
			}
		}
	}

	session, err := h.service.GetSession(r.Context(), sessionID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	responder.JSON(w, http.StatusOK, toChatSessionResponse(session, nil))
}

func (h chatHandlers) deleteSession(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	_, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}

	if err := h.service.CloseSession(r.Context(), sessionID); err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	session, err := h.service.GetSession(r.Context(), sessionID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	responder.JSON(w, http.StatusOK, map[string]any{
		"status":    "closed",
		"closed_at": session.ClosedAt,
	})
}

func (h chatHandlers) listMessages(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	_, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}

	session, err := h.service.GetSession(r.Context(), sessionID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	query := r.URL.Query()
	limit := clampListLimit(query.Get("limit"), chatDefaultListLimit, chatMaxListLimit)

	var cursor *int64
	if rawCursor := strings.TrimSpace(query.Get("cursor")); rawCursor != "" {
		decoded, err := decodeInt64Cursor(rawCursor)
		if err != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
			return
		}
		cursor = &decoded
	}

	before, err := parseOptionalInt64(query.Get("before_sequence"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid before_sequence")
		return
	}
	after, err := parseOptionalInt64(query.Get("after_sequence"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid after_sequence")
		return
	}

	items, err := h.service.ListMessages(r.Context(), sessionID, chat.MessageFilter{
		Status:         strings.TrimSpace(query.Get("status")),
		Limit:          limit + 1,
		CursorSequence: cursor,
		BeforeSequence: before,
		AfterSequence:  after,
	})
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}

	payload := make([]chatMessageRecord, 0, len(items))
	for _, item := range items {
		payload = append(payload, toChatMessageResponse(item))
	}

	var nextCursor *string
	if hasMore && len(items) > 0 {
		encoded := encodeInt64Cursor(items[len(items)-1].SequenceNumber)
		nextCursor = &encoded
	}
	total := session.MessageCount

	responder.JSONList(w, http.StatusOK, payload, api.PaginationMeta{
		NextCursor: nextCursor,
		HasMore:    hasMore,
		Limit:      limit,
		Total:      &total,
	})
}

func (h chatHandlers) getMessage(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	_, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}
	messageID, ok := parseChatPathUUID(responder, w, r, "mid", "invalid message id")
	if !ok {
		return
	}

	message, err := h.service.GetMessage(r.Context(), messageID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}
	if message.SessionID != sessionID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	reactions, err := h.service.ListReactions(r.Context(), messageID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	responder.JSON(w, http.StatusOK, chatMessageDetailRecord{
		chatMessageRecord: toChatMessageResponse(message),
		Reactions:         toChatReactionResponses(reactions),
	})
}

func (h chatHandlers) listTurns(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	_, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}
	if _, err := h.service.GetSession(r.Context(), sessionID); err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	turns, err := h.service.ListTurns(r.Context(), sessionID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	payload := make([]chatTurnRecord, 0, len(turns))
	for _, turn := range turns {
		payload = append(payload, toChatTurnResponse(turn))
	}
	responder.JSON(w, http.StatusOK, payload)
}

func (h chatHandlers) exportSession(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	_, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}
	if _, err := h.service.GetSession(r.Context(), sessionID); err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	format := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("format")))
	if format == "" {
		format = "jsonl"
	}
	if format != "jsonl" && format != "json" {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid format")
		return
	}

	// Export all messages, paging through the service filter by sequence cursor.
	items := make([]chatMessageRecord, 0)
	var cursor *int64
	for {
		page, err := h.service.ListMessages(r.Context(), sessionID, chat.MessageFilter{
			Limit:          chatMaxListLimit,
			CursorSequence: cursor,
		})
		if err != nil {
			h.respondChatError(responder, w, err)
			return
		}
		if len(page) == 0 {
			break
		}
		for _, message := range page {
			items = append(items, toChatMessageResponse(message))
		}
		last := page[len(page)-1].SequenceNumber
		cursor = &last
		if len(page) < chatMaxListLimit {
			break
		}
	}

	if format == "json" {
		responder.JSON(w, http.StatusOK, items)
		return
	}

	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	for _, item := range items {
		if err := encoder.Encode(item); err != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
			return
		}
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}

func (h chatHandlers) appendMessage(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}

	var req appendChatMessageRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "content is required")
		return
	}

	authorType := "human_user"
	if isAgentAPIKey(principal) {
		authorType = "agent"
	}
	role := normalizeAppendRole(req.Role)
	message, err := h.service.AppendMessage(r.Context(), chat.AppendMessageInput{
		SessionID:     sessionID,
		AuthorType:    &authorType,
		AuthorID:      &principal.UserID,
		Role:          role,
		Content:       content,
		ContentFormat: strings.TrimSpace(req.ContentFormat),
	})
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	if role == "user" {
		h.kickoffDeterministicTurn(sessionID, content)
	}

	responder.JSON(w, http.StatusAccepted, toChatMessageResponse(message))
}

func (h chatHandlers) editQueuedMessage(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if isAgentAPIKey(principal) {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}
	messageID, ok := parseChatPathUUID(responder, w, r, "mid", "invalid message id")
	if !ok {
		return
	}

	message, err := h.service.GetMessage(r.Context(), messageID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}
	if message.SessionID != sessionID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	var req editChatMessageRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "content is required")
		return
	}

	if err := h.service.EditQueuedMessage(r.Context(), messageID, strings.TrimSpace(req.Content)); err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	updated, err := h.service.GetMessage(r.Context(), messageID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	responder.JSON(w, http.StatusOK, toChatMessageResponse(updated))
}

func (h chatHandlers) redactMessage(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if _, ok := h.requirePrincipal(w, r); !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}
	messageID, ok := parseChatPathUUID(responder, w, r, "mid", "invalid message id")
	if !ok {
		return
	}

	message, err := h.service.GetMessage(r.Context(), messageID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}
	if message.SessionID != sessionID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	if err := h.service.RedactMessage(r.Context(), messageID); err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	updated, err := h.service.GetMessage(r.Context(), messageID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	responder.JSON(w, http.StatusOK, toChatMessageResponse(updated))
}

func (h chatHandlers) cancelTurn(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	_, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}

	var (
		cancelReason *string
		requestTurn  *uuid.UUID
	)
	if r.ContentLength > 0 {
		var req cancelTurnRequest
		if err := decodeJSON(r, &req); err != nil {
			responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
			return
		}
		cancelReason = trimOptionalString(req.Reason)
		requestTurn = req.TurnID
	}
	reason := ""
	if cancelReason != nil {
		reason = *cancelReason
	}

	turnID := uuid.Nil
	if requestTurn != nil && *requestTurn != uuid.Nil {
		turn, err := h.service.GetTurn(r.Context(), *requestTurn)
		if err != nil {
			h.respondChatError(responder, w, err)
			return
		}
		if turn.SessionID != sessionID {
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
			return
		}
		if err := h.service.CancelTurn(r.Context(), turn.ID, reason); err != nil {
			h.respondChatError(responder, w, err)
			return
		}
		turnID = turn.ID
	} else {
		turnID = h.currentInProgressTurnID(r.Context(), sessionID)
		if err := h.service.CancelCurrentTurn(r.Context(), sessionID); err != nil {
			h.respondChatError(responder, w, err)
			return
		}
		if turnID == uuid.Nil {
			turnID = h.latestTurnIDByStatus(r.Context(), sessionID, "cancelled")
		}
	}

	responder.JSON(w, http.StatusOK, map[string]any{
		"turn_id": turnID,
		"status":  "cancelled",
	})
}

func (h chatHandlers) steerTurn(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	_, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}
	messageID, ok := parseChatPathUUID(responder, w, r, "mid", "invalid message id")
	if !ok {
		return
	}

	targetMessage, err := h.service.GetMessage(r.Context(), messageID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}
	if targetMessage.SessionID != sessionID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	var req steerTurnRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "content is required")
		return
	}

	if err := h.service.SteerTurn(r.Context(), sessionID, messageID, strings.TrimSpace(req.Content)); err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	message, err := h.findLatestSteerMessage(r.Context(), sessionID, messageID, targetMessage.SequenceNumber)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}

	responder.JSON(w, http.StatusOK, toChatMessageResponse(message))
}

func (h chatHandlers) listReactions(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	_, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}
	messageID, ok := parseChatPathUUID(responder, w, r, "mid", "invalid message id")
	if !ok {
		return
	}

	if !h.messageBelongsToSession(r.Context(), sessionID, messageID, responder, w) {
		return
	}

	reactions, err := h.service.ListReactions(r.Context(), messageID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusOK, toChatReactionResponses(reactions))
}

func (h chatHandlers) addReaction(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}
	messageID, ok := parseChatPathUUID(responder, w, r, "mid", "invalid message id")
	if !ok {
		return
	}

	if !h.messageBelongsToSession(r.Context(), sessionID, messageID, responder, w) {
		return
	}

	var req addReactionRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	emoji := strings.TrimSpace(req.Emoji)
	if emoji == "" {
		emoji = strings.TrimSpace(req.Reaction)
	}
	if emoji == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "emoji or reaction is required")
		return
	}

	reactorType := "human_user"
	if isAgentAPIKey(principal) {
		reactorType = "agent"
	}
	reaction, err := h.service.AddReaction(r.Context(), messageID, reactorType, principal.UserID, emoji)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusCreated, toChatReactionResponse(reaction))
}

func (h chatHandlers) removeReaction(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}
	messageID, ok := parseChatPathUUID(responder, w, r, "mid", "invalid message id")
	if !ok {
		return
	}
	reactionID, ok := parseChatPathUUID(responder, w, r, "rid", "invalid reaction id")
	if !ok {
		return
	}

	if !h.messageBelongsToSession(r.Context(), sessionID, messageID, responder, w) {
		return
	}

	reactions, err := h.service.ListReactions(r.Context(), messageID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}
	found := false
	var reaction *chat.ChatMessageReaction
	for _, item := range reactions {
		if item.ID == reactionID {
			found = true
			reaction = item
			break
		}
	}
	if !found {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}
	if reaction != nil && reaction.ReactorID != principal.UserID && !isAdminRole(principal.Role) {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	if err := h.service.RemoveReaction(r.Context(), reactionID); err != nil {
		h.respondChatError(responder, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h chatHandlers) listParticipants(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	_, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}

	participants, err := h.service.ListParticipants(r.Context(), sessionID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusOK, toChatParticipantResponses(participants))
}

func (h chatHandlers) addParticipant(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	_, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}

	var req addParticipantRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.ParticipantType) == "" || req.ParticipantID == uuid.Nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "participant_type and participant_id are required")
		return
	}

	participant, err := h.service.AddParticipant(r.Context(), sessionID, req.ParticipantType, req.ParticipantID, req.Role)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusCreated, toChatParticipantResponse(participant))
}

func (h chatHandlers) removeParticipant(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat service unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}
	participantID, ok := parseChatPathUUID(responder, w, r, "pid", "invalid participant id")
	if !ok {
		return
	}

	session, err := h.service.GetSession(r.Context(), sessionID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}
	isAdmin := isAdminRole(principal.Role)
	isOwner := strings.EqualFold(session.CreatedByType, "human_user") && session.CreatedByID == principal.UserID
	if !isAdmin && !isOwner && principal.UserID != participantID {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	if err := h.service.RemoveParticipant(r.Context(), sessionID, participantID); err != nil {
		h.respondChatError(responder, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h chatHandlers) getReadCursor(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil || h.readCursors == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat cursor repository unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}

	if _, err := h.service.GetSession(r.Context(), sessionID); err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	cursor, err := h.readCursors.GetBySessionAndUser(r.Context(), sessionID, principal.UserID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusOK, toChatReadCursorResponse(cursor))
}

func (h chatHandlers) putReadCursor(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if isAgentAPIKey(principal) {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}
	if h.service == nil || h.readCursors == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat cursor repository unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}

	if _, err := h.service.GetSession(r.Context(), sessionID); err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	var req putReadCursorRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.LastReadSequence < 0 {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "last_read_sequence must be >= 0")
		return
	}

	cursor, err := h.readCursors.Upsert(r.Context(), repo.ChatReadCursor{
		SessionID:        sessionID,
		UserID:           principal.UserID,
		LastReadSequence: req.LastReadSequence,
	})
	if err != nil {
		h.respondChatError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusOK, map[string]any{
		"last_read_sequence": cursor.LastReadSequence,
		"updated_at":         cursor.UpdatedAt,
	})
}

func (h chatHandlers) listArtifacts(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	_, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.service == nil || h.artifacts == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "chat artifact repository unavailable")
		return
	}

	sessionID, ok := parseChatPathUUID(responder, w, r, "id", "invalid session id")
	if !ok {
		return
	}

	if _, err := h.service.GetSession(r.Context(), sessionID); err != nil {
		h.respondChatError(responder, w, err)
		return
	}

	query := r.URL.Query()
	limit := clampListLimit(query.Get("limit"), chatDefaultListLimit, chatMaxListLimit)
	artifactType := strings.TrimSpace(query.Get("artifact_type"))

	var items []repo.ChatArtifact
	if rawMessageID := strings.TrimSpace(query.Get("message_id")); rawMessageID != "" {
		messageID, err := uuid.Parse(rawMessageID)
		if err != nil {
			responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid message_id")
			return
		}
		if !h.messageBelongsToSession(r.Context(), sessionID, messageID, responder, w) {
			return
		}
		items, err = h.artifacts.ListByMessage(r.Context(), messageID)
		if err != nil {
			h.respondChatError(responder, w, err)
			return
		}
	} else {
		var err error
		items, err = h.artifacts.ListBySession(r.Context(), sessionID)
		if err != nil {
			h.respondChatError(responder, w, err)
			return
		}
	}

	if artifactType != "" {
		filtered := make([]repo.ChatArtifact, 0, len(items))
		for _, item := range items {
			if strings.EqualFold(strings.TrimSpace(item.ArtifactType), artifactType) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	if rawCursor := strings.TrimSpace(query.Get("cursor")); rawCursor != "" {
		cursorID, err := decodeUUIDCursor(rawCursor)
		if err != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
			return
		}
		cropped := make([]repo.ChatArtifact, 0, len(items))
		seen := false
		for _, item := range items {
			if seen {
				cropped = append(cropped, item)
				continue
			}
			if item.ID == cursorID {
				seen = true
			}
		}
		items = cropped
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}

	payload := make([]chatArtifactRecord, 0, len(items))
	for _, item := range items {
		payload = append(payload, toChatArtifactResponse(item))
	}

	var nextCursor *string
	if hasMore && len(items) > 0 {
		encoded := encodeUUIDCursor(items[len(items)-1].ID)
		nextCursor = &encoded
	}

	responder.JSONList(w, http.StatusOK, payload, api.PaginationMeta{
		NextCursor: nextCursor,
		HasMore:    hasMore,
		Limit:      limit,
	})
}

func (h chatHandlers) messageBelongsToSession(ctx context.Context, sessionID, messageID uuid.UUID, responder api.Responder, w http.ResponseWriter) bool {
	message, err := h.service.GetMessage(ctx, messageID)
	if err != nil {
		h.respondChatError(responder, w, err)
		return false
	}
	if message.SessionID != sessionID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return false
	}
	return true
}

func (h chatHandlers) findLatestSteerMessage(ctx context.Context, sessionID, steerMessageID uuid.UUID, afterSequence int64) (*chat.ChatMessage, error) {
	if h.messages == nil {
		items, err := h.service.ListMessages(ctx, sessionID, chat.MessageFilter{AfterSequence: &afterSequence, Limit: chatMaxListLimit})
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			return nil, repo.ErrNotFound
		}
		return items[len(items)-1], nil
	}

	items, err := h.messages.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if item.SequenceNumber <= afterSequence {
			break
		}
		if item.Role != "user" {
			continue
		}
		if !metadataSteerMatches(item.Metadata, steerMessageID) {
			continue
		}
		copyItem := item
		return (*chat.ChatMessage)(&copyItem), nil
	}
	return nil, repo.ErrNotFound
}

func metadataSteerMatches(metadata json.RawMessage, steerMessageID uuid.UUID) bool {
	if len(metadata) == 0 {
		return false
	}
	var parsed map[string]any
	if err := json.Unmarshal(metadata, &parsed); err != nil {
		return false
	}
	value, ok := parsed["steer_message_id"]
	if !ok {
		return false
	}
	text, ok := value.(string)
	if !ok {
		return false
	}
	return strings.TrimSpace(text) == steerMessageID.String()
}

func (h chatHandlers) currentInProgressTurnID(ctx context.Context, sessionID uuid.UUID) uuid.UUID {
	turns, err := h.service.ListTurns(ctx, sessionID)
	if err != nil {
		return uuid.Nil
	}
	selected := uuid.Nil
	maxTurn := -1
	for _, turn := range turns {
		if !strings.EqualFold(strings.TrimSpace(turn.Status), "in_progress") {
			continue
		}
		if turn.TurnNumber > maxTurn {
			maxTurn = turn.TurnNumber
			selected = turn.ID
		}
	}
	return selected
}

func (h chatHandlers) latestTurnIDByStatus(ctx context.Context, sessionID uuid.UUID, status string) uuid.UUID {
	turns, err := h.service.ListTurns(ctx, sessionID)
	if err != nil {
		return uuid.Nil
	}
	selected := uuid.Nil
	maxTurn := -1
	for _, turn := range turns {
		if !strings.EqualFold(strings.TrimSpace(turn.Status), strings.TrimSpace(status)) {
			continue
		}
		if turn.TurnNumber > maxTurn {
			maxTurn = turn.TurnNumber
			selected = turn.ID
		}
	}
	return selected
}

func (h chatHandlers) kickoffDeterministicTurn(sessionID uuid.UUID, content string) {
	if !h.testMode || h.service == nil {
		return
	}
	if h.currentInProgressTurnID(context.Background(), sessionID) != uuid.Nil {
		return
	}
	agentID, ok := h.firstActiveAgentParticipant(context.Background(), sessionID)
	if !ok {
		return
	}

	turn, err := h.service.CreateTurn(context.Background(), sessionID, agentID)
	if err != nil || turn == nil {
		return
	}
	if err := h.service.StartTurn(context.Background(), turn.ID); err != nil {
		return
	}

	go h.finishDeterministicTurn(sessionID, turn.ID, agentID, content)
}

func (h chatHandlers) finishDeterministicTurn(sessionID, turnID, agentID uuid.UUID, userContent string) {
	delay := deterministicTurnDelay(userContent)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	<-timer.C

	ctx := context.Background()
	turn, err := h.service.GetTurn(ctx, turnID)
	if err != nil || turn == nil {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(turn.Status), "in_progress") {
		return
	}

	authorType := "agent"
	role := "assistant"
	reply := deterministicTurnResponse(userContent)
	message, err := h.service.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  sessionID,
		TurnID:     &turnID,
		AuthorType: &authorType,
		AuthorID:   &agentID,
		Role:       role,
		Content:    reply,
	})
	if err != nil {
		return
	}

	h.markMessageFinal(ctx, message.ID)
	_ = h.service.CompleteTurn(ctx, turnID)
}

func (h chatHandlers) firstActiveAgentParticipant(ctx context.Context, sessionID uuid.UUID) (uuid.UUID, bool) {
	participants, err := h.service.ListParticipants(ctx, sessionID)
	if err != nil {
		return uuid.Nil, false
	}
	activeAgentIDs := make([]uuid.UUID, 0, len(participants))
	for _, participant := range participants {
		if participant == nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(participant.ParticipantType), "agent") {
			continue
		}
		if participant.ParticipantID == uuid.Nil || participant.RemovedAt != nil {
			continue
		}
		activeAgentIDs = append(activeAgentIDs, participant.ParticipantID)
	}
	if len(activeAgentIDs) == 0 {
		return uuid.Nil, false
	}

	session, err := h.service.GetSession(ctx, sessionID)
	if err == nil && session != nil {
		if preferredID, ok := h.preferredProjectAgentParticipant(ctx, session, activeAgentIDs); ok {
			return preferredID, true
		}
	}
	return activeAgentIDs[0], true
}

func (h chatHandlers) preferredProjectAgentParticipant(ctx context.Context, session *chat.ChatSession, activeAgentIDs []uuid.UUID) (uuid.UUID, bool) {
	if session == nil {
		return uuid.Nil, false
	}
	scopeType := strings.TrimSpace(session.ScopeType)
	if scopeType != "project" && scopeType != "project_task" {
		return uuid.Nil, false
	}

	activeSet := make(map[uuid.UUID]struct{}, len(activeAgentIDs))
	for _, agentID := range activeAgentIDs {
		if agentID != uuid.Nil {
			activeSet[agentID] = struct{}{}
		}
	}

	projectID := session.ScopeID
	if scopeType == "project_task" {
		if h.tasks == nil {
			return uuid.Nil, false
		}
		taskRecord, err := h.tasks.GetByID(ctx, session.ScopeID)
		if err != nil {
			return uuid.Nil, false
		}
		projectID = taskRecord.ProjectID
	}

	if h.assignments != nil && projectID != uuid.Nil {
		if assignment, err := h.assignments.GetPM(ctx, projectID); err == nil {
			if _, ok := activeSet[assignment.AgentID]; ok {
				return assignment.AgentID, true
			}
		}
	}

	if h.agents == nil {
		return uuid.Nil, false
	}
	for _, agentID := range activeAgentIDs {
		agent, err := h.agents.GetByID(ctx, agentID)
		if err != nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(agent.LifecycleStatus), "active") {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(agent.AgentType), "pm") {
			return agentID, true
		}
	}
	return uuid.Nil, false
}

func (h chatHandlers) markMessageFinal(ctx context.Context, messageID uuid.UUID) {
	if err := h.service.UpdateMessageStatus(ctx, messageID, "final", ""); err == nil {
		return
	}
	if h.messages == nil {
		return
	}
	_, _ = h.messages.UpdateStatus(ctx, messageID, "final", "")
}

func deterministicTurnDelay(content string) time.Duration {
	trimmed := strings.ToLower(strings.TrimSpace(content))
	if strings.HasPrefix(trimmed, "[slow-response]") {
		return 2 * time.Second
	}
	return 150 * time.Millisecond
}

func deterministicTurnResponse(content string) string {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(strings.ToLower(trimmed), "[slow-response]") {
		trimmed = strings.TrimSpace(trimmed[len("[slow-response]"):])
	}
	if trimmed == "" {
		return "I am here and ready to help."
	}
	return "Deterministic reply: " + trimmed
}

func (h chatHandlers) requirePrincipal(w http.ResponseWriter, r *http.Request) (middleware.Principal, bool) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return middleware.Principal{}, false
	}
	return principal, true
}

func (h chatHandlers) respondChatError(responder api.Responder, w http.ResponseWriter, err error) {
	status, code, message := mapChatError(err)
	responder.Error(w, status, code, message)
}

func mapChatError(err error) (int, string, string) {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return http.StatusNotFound, api.ErrCodeNotFound, "resource not found"
	case errors.Is(err, repo.ErrConflict):
		return http.StatusConflict, api.ErrCodeConflict, "conflict"
	case errors.Is(err, chat.ErrActiveSyncSessionExists):
		return http.StatusConflict, "active_sync_session_exists", "active sync session already exists"
	case errors.Is(err, chat.ErrTurnInProgress):
		return http.StatusConflict, "turn_in_progress", "turn is in progress"
	case errors.Is(err, chat.ErrNoActiveTurn):
		return http.StatusConflict, "no_active_turn", "no active turn"
	case errors.Is(err, chat.ErrDuplicateReaction):
		return http.StatusConflict, "duplicate_reaction", "duplicate reaction"
	case errors.Is(err, chat.ErrAlreadyParticipant):
		return http.StatusConflict, "already_participant", "participant already active in session"
	case errors.Is(err, chat.ErrMessageNotEditable):
		return http.StatusUnprocessableEntity, "message_not_editable", "message is not editable"
	case errors.Is(err, chat.ErrSessionClosed):
		return http.StatusConflict, "session_closed", "session is closed"
	case errors.Is(err, chat.ErrProjectArchived):
		return http.StatusConflict, "project_archived", "project is archived"
	case errors.Is(err, chat.ErrForbidden):
		return http.StatusForbidden, api.ErrCodeForbidden, "forbidden"
	default:
		if isValidationError(err) {
			return http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error()
		}
		return http.StatusInternalServerError, api.ErrCodeInternal, "request failed"
	}
}

func isValidationError(err error) bool {
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "invalid") || strings.Contains(text, "required")
}

func parseChatPathUUID(responder api.Responder, w http.ResponseWriter, r *http.Request, key, message string) (uuid.UUID, bool) {
	value := strings.TrimSpace(chi.URLParam(r, key))
	parsed, err := uuid.Parse(value)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, message)
		return uuid.Nil, false
	}
	return parsed, true
}

func parseOptionalInt64(raw string) (*int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func clampListLimit(raw string, fallback int, max int) int {
	parsed := fallback
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		if value, err := strconv.Atoi(trimmed); err == nil {
			parsed = value
		}
	}
	if parsed <= 0 {
		return fallback
	}
	if parsed > max {
		return max
	}
	return parsed
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func encodeUUIDCursor(id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id.String()))
}

func decodeUUIDCursor(raw string) (uuid.UUID, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, err
	}
	return uuid.Parse(strings.TrimSpace(string(decoded)))
}

func encodeInt64Cursor(value int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(value, 10)))
}

func decodeInt64Cursor(raw string) (int64, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(decoded)), 10, 64)
}

func toChatSessionResponse(model *chat.ChatSession, participants []chatParticipantRecord) chatSessionResponse {
	if model == nil {
		return chatSessionResponse{}
	}
	response := chatSessionResponse{
		ID:             model.ID,
		OrganizationID: model.OrganizationID,
		ScopeType:      model.ScopeType,
		ScopeID:        model.ScopeID,
		Mode:           model.Mode,
		Status:         model.Status,
		Title:          model.Title,
		CreatedByType:  model.CreatedByType,
		CreatedByID:    model.CreatedByID,
		LastMessageAt:  model.LastMessageAt,
		TurnCount:      model.TurnCount,
		MessageCount:   model.MessageCount,
		Metadata:       model.Metadata,
		CreatedAt:      model.CreatedAt,
		UpdatedAt:      model.UpdatedAt,
		ClosedAt:       model.ClosedAt,
	}
	if len(participants) > 0 {
		response.Participants = participants
	}
	return response
}

func toChatMessageResponse(model *chat.ChatMessage) chatMessageRecord {
	if model == nil {
		return chatMessageRecord{}
	}
	content := model.Content
	if model.IsRedacted && strings.TrimSpace(content) == "" {
		content = chatRedactedContent
	}
	return chatMessageRecord{
		ID:             model.ID,
		SessionID:      model.SessionID,
		TurnID:         model.TurnID,
		SequenceNumber: model.SequenceNumber,
		AuthorType:     model.AuthorType,
		AuthorID:       model.AuthorID,
		Role:           model.Role,
		Content:        content,
		ContentFormat:  model.ContentFormat,
		Status:         model.Status,
		IsRedacted:     model.IsRedacted,
		ToolCallID:     model.ToolCallID,
		ErrorMessage:   model.ErrorMessage,
		Metadata:       model.Metadata,
		CreatedAt:      model.CreatedAt,
		UpdatedAt:      model.UpdatedAt,
	}
}

func toChatParticipantResponse(model *chat.ChatParticipant) chatParticipantRecord {
	if model == nil {
		return chatParticipantRecord{}
	}
	return chatParticipantRecord{
		ID:                     model.ID,
		SessionID:              model.SessionID,
		ParticipantType:        model.ParticipantType,
		ParticipantID:          model.ParticipantID,
		Role:                   model.Role,
		NotificationPreference: model.NotificationPreference,
		JoinedAt:               model.JoinedAt,
		RemovedAt:              model.RemovedAt,
	}
}

func toChatParticipantResponses(items []*chat.ChatParticipant) []chatParticipantRecord {
	result := make([]chatParticipantRecord, 0, len(items))
	for _, item := range items {
		result = append(result, toChatParticipantResponse(item))
	}
	return result
}

func toChatReactionResponse(model *chat.ChatMessageReaction) chatReactionRecord {
	if model == nil {
		return chatReactionRecord{}
	}
	return chatReactionRecord{
		ID:          model.ID,
		MessageID:   model.MessageID,
		SessionID:   model.SessionID,
		ReactorType: model.ReactorType,
		ReactorID:   model.ReactorID,
		Reaction:    model.Emoji,
		Emoji:       model.Emoji,
		CreatedAt:   model.CreatedAt,
	}
}

func toChatReactionResponses(items []*chat.ChatMessageReaction) []chatReactionRecord {
	result := make([]chatReactionRecord, 0, len(items))
	for _, item := range items {
		result = append(result, toChatReactionResponse(item))
	}
	return result
}

func toChatTurnResponse(model *chat.ChatTurn) chatTurnRecord {
	if model == nil {
		return chatTurnRecord{}
	}
	return chatTurnRecord{
		ID:                model.ID,
		SessionID:         model.SessionID,
		TurnNumber:        model.TurnNumber,
		RespondingType:    model.RespondingType,
		RespondingID:      model.RespondingID,
		Status:            normalizeTurnResponseStatus(model.Status),
		CancelRequestedAt: model.CancelRequestedAt,
		StartedAt:         model.StartedAt,
		CompletedAt:       model.CompletedAt,
		DurationMS:        model.DurationMS,
		CreatedAt:         model.CreatedAt,
	}
}

func normalizeTurnResponseStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "in_progress":
		return "active"
	default:
		return strings.TrimSpace(strings.ToLower(value))
	}
}

func normalizeAppendRole(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", "user", "human":
		return "user"
	case "assistant":
		return "assistant"
	case "tool_call":
		return "tool_call"
	case "tool_result":
		return "tool_result"
	case "system":
		return "system"
	default:
		return strings.TrimSpace(strings.ToLower(value))
	}
}

func toChatReadCursorResponse(model repo.ChatReadCursor) chatReadCursorRecord {
	return chatReadCursorRecord{
		UserID:           model.UserID,
		SessionID:        model.SessionID,
		LastReadSequence: model.LastReadSequence,
		UpdatedAt:        model.UpdatedAt,
	}
}

func toChatArtifactResponse(model repo.ChatArtifact) chatArtifactRecord {
	return chatArtifactRecord{
		ID:           model.ID,
		SessionID:    model.SessionID,
		MessageID:    model.MessageID,
		ArtifactType: model.ArtifactType,
		Filename:     model.Filename,
		ContentType:  model.ContentType,
		StorageKey:   model.StorageKey,
		URL:          model.URL,
		ByteSize:     model.ByteSize,
		CreatedAt:    model.CreatedAt,
	}
}

func isAgentAPIKey(principal middleware.Principal) bool {
	if principal.APIKey == nil || !strings.EqualFold(principal.AuthMethod, middleware.AuthMethodAPIKey) {
		return false
	}
	if strings.EqualFold(principal.Role, "agent") {
		return true
	}
	for _, scope := range principal.APIKey.Scopes {
		normalized := strings.ToLower(strings.TrimSpace(scope))
		if normalized == "agent" || strings.HasPrefix(normalized, "agent:") {
			return true
		}
	}
	return false
}
