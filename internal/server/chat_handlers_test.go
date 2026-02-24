package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestChatRoutesRegistered(t *testing.T) {
	registrar := NewChatRouteRegistrar(nil, nil)
	router := chi.NewRouter()
	registrar.RegisterRoutes(router)

	routes := make(map[string]struct{})
	if err := chi.Walk(router, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[strings.ToUpper(method)+" "+route] = struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("walk routes: %v", err)
	}

	required := []string{
		"GET /chat-sessions",
		"POST /chat-sessions",
		"GET /chat-sessions/{id}",
		"PATCH /chat-sessions/{id}",
		"DELETE /chat-sessions/{id}",
		"GET /chat-sessions/{id}/messages",
		"POST /chat-sessions/{id}/messages",
		"PATCH /chat-sessions/{id}/messages/{mid}",
		"POST /chat-sessions/{id}/cancel-turn",
		"POST /chat-sessions/{id}/messages/{mid}/steer",
		"GET /chat-sessions/{id}/messages/{mid}/reactions",
		"POST /chat-sessions/{id}/messages/{mid}/reactions",
		"DELETE /chat-sessions/{id}/messages/{mid}/reactions/{rid}",
		"GET /chat-sessions/{id}/participants",
		"POST /chat-sessions/{id}/participants",
		"DELETE /chat-sessions/{id}/participants/{pid}",
		"GET /chat-sessions/{id}/read-cursor",
		"PUT /chat-sessions/{id}/read-cursor",
		"GET /chat-sessions/{id}/artifacts",
	}

	for _, key := range required {
		if _, ok := routes[key]; !ok {
			t.Fatalf("missing route %q", key)
		}
	}
}

func TestCreateSessionMapsActiveSyncConflict(t *testing.T) {
	svc := &fakeChatService{
		createSessionFn: func(context.Context, chat.CreateSessionInput) (*chat.ChatSession, error) {
			return nil, chat.ErrActiveSyncSessionExists
		},
	}
	h := chatHandlers{service: svc}

	scopeID := uuid.New()
	req := newChatRequest(t, http.MethodPost, "/v1/chat-sessions", map[string]any{
		"scope_type": "project",
		"scope_id":   scopeID,
		"mode":       "sync",
	}, middleware.Principal{UserID: uuid.New(), OrganizationID: uuid.New(), Role: "admin"})
	rr := httptest.NewRecorder()

	h.createSession(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusConflict, rr.Body.String())
	}
	if got := errorCode(t, rr.Body.Bytes()); got != "active_sync_session_exists" {
		t.Fatalf("error.code = %q, want %q body=%s", got, "active_sync_session_exists", rr.Body.String())
	}
}

func TestEditQueuedMessageRejectsAgentAPIKey(t *testing.T) {
	svc := &fakeChatService{}
	h := chatHandlers{service: svc}

	sessionID := uuid.New()
	messageID := uuid.New()
	req := newChatRequest(t, http.MethodPatch, "/v1/chat-sessions/"+sessionID.String()+"/messages/"+messageID.String(), map[string]any{
		"content": "rewrite",
	}, middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: uuid.New(),
		Role:           "agent",
		AuthMethod:     middleware.AuthMethodAPIKey,
		APIKey:         &auth.APIKeyInfo{Scopes: []string{"agent:chat"}},
	})
	req = withRouteParams(req, map[string]string{"id": sessionID.String(), "mid": messageID.String()})
	rr := httptest.NewRecorder()

	h.editQueuedMessage(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
	}
	if svc.editQueuedCalls != 0 {
		t.Fatalf("EditQueuedMessage calls = %d, want 0", svc.editQueuedCalls)
	}
}

func TestGetSessionCrossOrgNotFound(t *testing.T) {
	svc := &fakeChatService{
		getSessionFn: func(context.Context, uuid.UUID) (*chat.ChatSession, error) {
			return nil, repo.ErrNotFound
		},
	}
	h := chatHandlers{service: svc}

	sessionID := uuid.New()
	req := newChatRequest(t, http.MethodGet, "/v1/chat-sessions/"+sessionID.String(), nil, middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: uuid.New(),
		Role:           "member",
	})
	req = withRouteParams(req, map[string]string{"id": sessionID.String()})
	rr := httptest.NewRecorder()

	h.getSession(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
}

func newChatRequest(t *testing.T, method, path string, payload any, principal middleware.Principal) *http.Request {
	t.Helper()

	var body []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = encoded
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(middleware.WithPrincipal(req.Context(), principal))
	return req
}

func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	errorObj, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("error object missing in body: %s", string(body))
	}
	value, _ := errorObj["code"].(string)
	return value
}

type fakeChatService struct {
	createSessionFn          func(context.Context, chat.CreateSessionInput) (*chat.ChatSession, error)
	getSessionFn             func(context.Context, uuid.UUID) (*chat.ChatSession, error)
	listSessionsFn           func(context.Context, chat.SessionFilter) ([]*chat.ChatSession, error)
	switchModeFn             func(context.Context, uuid.UUID, string) error
	closeSessionFn           func(context.Context, uuid.UUID) error
	getOrCreateNodeSessionFn func(context.Context, uuid.UUID, uuid.UUID) (*chat.ChatSession, error)
	addParticipantFn         func(context.Context, uuid.UUID, string, uuid.UUID, string) (*chat.ChatParticipant, error)
	removeParticipantFn      func(context.Context, uuid.UUID, uuid.UUID) error
	listParticipantsFn       func(context.Context, uuid.UUID) ([]*chat.ChatParticipant, error)
	updateNotificationPrefFn func(context.Context, uuid.UUID, uuid.UUID, string) error
	appendMessageFn          func(context.Context, chat.AppendMessageInput) (*chat.ChatMessage, error)
	updateMessageStatusFn    func(context.Context, uuid.UUID, string, string) error
	redactMessageFn          func(context.Context, uuid.UUID) error
	editQueuedMessageFn      func(context.Context, uuid.UUID, string) error
	getMessageFn             func(context.Context, uuid.UUID) (*chat.ChatMessage, error)
	listMessagesFn           func(context.Context, uuid.UUID, chat.MessageFilter) ([]*chat.ChatMessage, error)
	createTurnFn             func(context.Context, uuid.UUID, uuid.UUID) (*chat.ChatTurn, error)
	startTurnFn              func(context.Context, uuid.UUID) error
	completeTurnFn           func(context.Context, uuid.UUID) error
	cancelTurnFn             func(context.Context, uuid.UUID, string) error
	failTurnFn               func(context.Context, uuid.UUID, string) error
	getTurnFn                func(context.Context, uuid.UUID) (*chat.ChatTurn, error)
	listTurnsFn              func(context.Context, uuid.UUID) ([]*chat.ChatTurn, error)
	cancelCurrentTurnFn      func(context.Context, uuid.UUID) error
	cancelAndQueueNewFn      func(context.Context, uuid.UUID, string) error
	steerTurnFn              func(context.Context, uuid.UUID, uuid.UUID, string) error
	addReactionFn            func(context.Context, uuid.UUID, string, uuid.UUID, string) (*chat.ChatMessageReaction, error)
	removeReactionFn         func(context.Context, uuid.UUID) error
	listReactionsFn          func(context.Context, uuid.UUID) ([]*chat.ChatMessageReaction, error)
	editQueuedCalls          int
}

func (f *fakeChatService) CreateSession(ctx context.Context, input chat.CreateSessionInput) (*chat.ChatSession, error) {
	if f.createSessionFn != nil {
		return f.createSessionFn(ctx, input)
	}
	return nil, nil
}

func (f *fakeChatService) GetSession(ctx context.Context, id uuid.UUID) (*chat.ChatSession, error) {
	if f.getSessionFn != nil {
		return f.getSessionFn(ctx, id)
	}
	return nil, repo.ErrNotFound
}

func (f *fakeChatService) ListSessions(ctx context.Context, filter chat.SessionFilter) ([]*chat.ChatSession, error) {
	if f.listSessionsFn != nil {
		return f.listSessionsFn(ctx, filter)
	}
	return nil, nil
}

func (f *fakeChatService) SwitchMode(ctx context.Context, sessionID uuid.UUID, newMode string) error {
	if f.switchModeFn != nil {
		return f.switchModeFn(ctx, sessionID, newMode)
	}
	return nil
}

func (f *fakeChatService) CloseSession(ctx context.Context, sessionID uuid.UUID) error {
	if f.closeSessionFn != nil {
		return f.closeSessionFn(ctx, sessionID)
	}
	return nil
}

func (f *fakeChatService) GetOrCreateNodeSession(ctx context.Context, flowNodeExecutionID, agentID uuid.UUID) (*chat.ChatSession, error) {
	if f.getOrCreateNodeSessionFn != nil {
		return f.getOrCreateNodeSessionFn(ctx, flowNodeExecutionID, agentID)
	}
	return nil, nil
}

func (f *fakeChatService) AddParticipant(ctx context.Context, sessionID uuid.UUID, participantType string, participantID uuid.UUID, role string) (*chat.ChatParticipant, error) {
	if f.addParticipantFn != nil {
		return f.addParticipantFn(ctx, sessionID, participantType, participantID, role)
	}
	return nil, nil
}

func (f *fakeChatService) RemoveParticipant(ctx context.Context, sessionID, participantID uuid.UUID) error {
	if f.removeParticipantFn != nil {
		return f.removeParticipantFn(ctx, sessionID, participantID)
	}
	return nil
}

func (f *fakeChatService) ListParticipants(ctx context.Context, sessionID uuid.UUID) ([]*chat.ChatParticipant, error) {
	if f.listParticipantsFn != nil {
		return f.listParticipantsFn(ctx, sessionID)
	}
	return nil, nil
}

func (f *fakeChatService) UpdateNotificationPreference(ctx context.Context, sessionID, userID uuid.UUID, preference string) error {
	if f.updateNotificationPrefFn != nil {
		return f.updateNotificationPrefFn(ctx, sessionID, userID, preference)
	}
	return nil
}

func (f *fakeChatService) AppendMessage(ctx context.Context, input chat.AppendMessageInput) (*chat.ChatMessage, error) {
	if f.appendMessageFn != nil {
		return f.appendMessageFn(ctx, input)
	}
	return nil, nil
}

func (f *fakeChatService) UpdateMessageStatus(ctx context.Context, messageID uuid.UUID, newStatus, errorMsg string) error {
	if f.updateMessageStatusFn != nil {
		return f.updateMessageStatusFn(ctx, messageID, newStatus, errorMsg)
	}
	return nil
}

func (f *fakeChatService) RedactMessage(ctx context.Context, messageID uuid.UUID) error {
	if f.redactMessageFn != nil {
		return f.redactMessageFn(ctx, messageID)
	}
	return nil
}

func (f *fakeChatService) EditQueuedMessage(ctx context.Context, messageID uuid.UUID, newContent string) error {
	f.editQueuedCalls++
	if f.editQueuedMessageFn != nil {
		return f.editQueuedMessageFn(ctx, messageID, newContent)
	}
	return nil
}

func (f *fakeChatService) GetMessage(ctx context.Context, messageID uuid.UUID) (*chat.ChatMessage, error) {
	if f.getMessageFn != nil {
		return f.getMessageFn(ctx, messageID)
	}
	return nil, repo.ErrNotFound
}

func (f *fakeChatService) ListMessages(ctx context.Context, sessionID uuid.UUID, filter chat.MessageFilter) ([]*chat.ChatMessage, error) {
	if f.listMessagesFn != nil {
		return f.listMessagesFn(ctx, sessionID, filter)
	}
	return nil, nil
}

func (f *fakeChatService) CreateTurn(ctx context.Context, sessionID, agentID uuid.UUID) (*chat.ChatTurn, error) {
	if f.createTurnFn != nil {
		return f.createTurnFn(ctx, sessionID, agentID)
	}
	return nil, nil
}

func (f *fakeChatService) StartTurn(ctx context.Context, turnID uuid.UUID) error {
	if f.startTurnFn != nil {
		return f.startTurnFn(ctx, turnID)
	}
	return nil
}

func (f *fakeChatService) CompleteTurn(ctx context.Context, turnID uuid.UUID) error {
	if f.completeTurnFn != nil {
		return f.completeTurnFn(ctx, turnID)
	}
	return nil
}

func (f *fakeChatService) CancelTurn(ctx context.Context, turnID uuid.UUID, reason string) error {
	if f.cancelTurnFn != nil {
		return f.cancelTurnFn(ctx, turnID, reason)
	}
	return nil
}

func (f *fakeChatService) FailTurn(ctx context.Context, turnID uuid.UUID, errorMsg string) error {
	if f.failTurnFn != nil {
		return f.failTurnFn(ctx, turnID, errorMsg)
	}
	return nil
}

func (f *fakeChatService) GetTurn(ctx context.Context, turnID uuid.UUID) (*chat.ChatTurn, error) {
	if f.getTurnFn != nil {
		return f.getTurnFn(ctx, turnID)
	}
	return nil, repo.ErrNotFound
}

func (f *fakeChatService) ListTurns(ctx context.Context, sessionID uuid.UUID) ([]*chat.ChatTurn, error) {
	if f.listTurnsFn != nil {
		return f.listTurnsFn(ctx, sessionID)
	}
	return nil, nil
}

func (f *fakeChatService) CancelCurrentTurn(ctx context.Context, sessionID uuid.UUID) error {
	if f.cancelCurrentTurnFn != nil {
		return f.cancelCurrentTurnFn(ctx, sessionID)
	}
	return nil
}

func (f *fakeChatService) CancelAndQueueNew(ctx context.Context, sessionID uuid.UUID, newMessage string) error {
	if f.cancelAndQueueNewFn != nil {
		return f.cancelAndQueueNewFn(ctx, sessionID, newMessage)
	}
	return nil
}

func (f *fakeChatService) SteerTurn(ctx context.Context, sessionID, messageID uuid.UUID, steerContent string) error {
	if f.steerTurnFn != nil {
		return f.steerTurnFn(ctx, sessionID, messageID, steerContent)
	}
	return nil
}

func (f *fakeChatService) AddReaction(ctx context.Context, messageID uuid.UUID, reactorType string, reactorID uuid.UUID, emoji string) (*chat.ChatMessageReaction, error) {
	if f.addReactionFn != nil {
		return f.addReactionFn(ctx, messageID, reactorType, reactorID, emoji)
	}
	return nil, nil
}

func (f *fakeChatService) RemoveReaction(ctx context.Context, reactionID uuid.UUID) error {
	if f.removeReactionFn != nil {
		return f.removeReactionFn(ctx, reactionID)
	}
	return nil
}

func (f *fakeChatService) ListReactions(ctx context.Context, messageID uuid.UUID) ([]*chat.ChatMessageReaction, error) {
	if f.listReactionsFn != nil {
		return f.listReactionsFn(ctx, messageID)
	}
	return nil, nil
}
