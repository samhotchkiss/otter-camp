package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	realtimeEventBufferSize   = 1000
	realtimeTypingTTL         = 15 * time.Second
	realtimeHeartbeatInterval = 15 * time.Second
	realtimeReplayBatchSize   = 500
)

type RealtimeRouteOptions struct {
	Pool   *pgxpool.Pool
	Events eventbus.EventBus
	Logger *slog.Logger
}

type RealtimeRouteRegistrar struct {
	handlers *realtimeHandlers
}

func NewRealtimeRouteRegistrar(opts RealtimeRouteOptions) *RealtimeRouteRegistrar {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	h := &realtimeHandlers{logger: logger, pool: opts.Pool}
	if opts.Pool != nil {
		h.chatSessions = repo.NewChatSessionRepo(opts.Pool)
		h.projects = repo.NewProjectRepo(opts.Pool)
		h.tasks = repo.NewProjectTaskRepo(opts.Pool)
		h.participants = repo.NewChatParticipantRepo(opts.Pool)
		h.users = repo.NewHumanUserRepo(opts.Pool)
		h.messages = repo.NewChatMessageRepo(opts.Pool)
	}
	if opts.Events != nil && opts.Pool != nil {
		h.hub = newRealtimeHub(opts.Pool, opts.Events, logger, realtimeHubOptions{
			HeartbeatInterval: realtimeHeartbeatInterval,
		})
	}

	return &RealtimeRouteRegistrar{handlers: h}
}

func (r *RealtimeRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.Get("/events/stream", r.handlers.streamEvents)
	router.Get("/ws/negotiate", r.handlers.negotiateTransport)
	router.Get("/ws", r.handlers.websocket)
}

func (r *RealtimeRouteRegistrar) Close() error {
	if r == nil || r.handlers == nil || r.handlers.hub == nil {
		return nil
	}
	r.handlers.hub.close()
	return nil
}

type realtimeHandlers struct {
	logger *slog.Logger
	pool   *pgxpool.Pool
	hub    *realtimeHub

	chatSessions *repo.ChatSessionRepo
	projects     *repo.ProjectRepo
	tasks        *repo.ProjectTaskRepo
	participants *repo.ChatParticipantRepo
	users        *repo.HumanUserRepo
	messages     *repo.ChatMessageRepo
}

type realtimeScope struct {
	Kind string
	ID   uuid.UUID
}

type realtimeFrameType string

const (
	realtimeFrameEvent          realtimeFrameType = "event"
	realtimeFrameTyping         realtimeFrameType = "typing"
	realtimeFramePong           realtimeFrameType = "pong"
	realtimeFrameBufferOverflow realtimeFrameType = "buffer_overflow"
	realtimeFrameError          realtimeFrameType = "error"
)

type realtimeFrame struct {
	Type      realtimeFrameType
	Event     *eventbus.DomainEvent
	SessionID uuid.UUID
	UserID    uuid.UUID
	ExpiresAt time.Time
	Message   string
}

type realtimeConnection struct {
	id          uuid.UUID
	principal   middleware.Principal
	events      chan realtimeFrame
	closed      chan struct{}
	transport   string
	scopes      map[realtimeScope]struct{}
	closeMu     sync.Mutex
	closeReason string
	closedFlag  bool
}

func newRealtimeConnection(principal middleware.Principal, scopes []realtimeScope, transport string) *realtimeConnection {
	scopeMap := make(map[realtimeScope]struct{}, len(scopes))
	for _, scope := range scopes {
		scopeMap[scope] = struct{}{}
	}
	return &realtimeConnection{
		id:        uuid.New(),
		principal: principal,
		events:    make(chan realtimeFrame, realtimeEventBufferSize),
		closed:    make(chan struct{}),
		transport: transport,
		scopes:    scopeMap,
	}
}

func (c *realtimeConnection) close(reason string) {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closedFlag {
		return
	}
	c.closedFlag = true
	c.closeReason = strings.TrimSpace(reason)
	close(c.closed)
}

func (c *realtimeConnection) reason() string {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	return c.closeReason
}

func (c *realtimeConnection) setScopes(scopes []realtimeScope) {
	scopeMap := make(map[realtimeScope]struct{}, len(scopes))
	for _, scope := range scopes {
		scopeMap[scope] = struct{}{}
	}
	c.closeMu.Lock()
	c.scopes = scopeMap
	c.closeMu.Unlock()
}

func (c *realtimeConnection) addScopes(scopes []realtimeScope) {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	for _, scope := range scopes {
		c.scopes[scope] = struct{}{}
	}
}

func (c *realtimeConnection) removeScopes(scopes []realtimeScope) {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	for _, scope := range scopes {
		delete(c.scopes, scope)
	}
}

func (c *realtimeConnection) snapshotScopes() map[realtimeScope]struct{} {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	out := make(map[realtimeScope]struct{}, len(c.scopes))
	for scope := range c.scopes {
		out[scope] = struct{}{}
	}
	return out
}

type realtimeHubOptions struct {
	HeartbeatInterval time.Duration
}

type realtimeHub struct {
	pool      *pgxpool.Pool
	events    eventbus.EventBus
	logger    *slog.Logger
	heartbeat time.Duration

	participants *repo.ChatParticipantRepo
	users        *repo.HumanUserRepo
	messages     *repo.ChatMessageRepo

	mu          sync.RWMutex
	connections map[uuid.UUID]*realtimeConnection
	sub         eventbus.Subscription
	hasSub      bool
	closed      bool
}

func newRealtimeHub(pool *pgxpool.Pool, events eventbus.EventBus, logger *slog.Logger, opts realtimeHubOptions) *realtimeHub {
	if logger == nil {
		logger = slog.Default()
	}
	heartbeat := opts.HeartbeatInterval
	if heartbeat <= 0 {
		heartbeat = realtimeHeartbeatInterval
	}
	hub := &realtimeHub{
		pool:         pool,
		events:       events,
		logger:       logger,
		heartbeat:    heartbeat,
		participants: repo.NewChatParticipantRepo(pool),
		users:        repo.NewHumanUserRepo(pool),
		messages:     repo.NewChatMessageRepo(pool),
		connections:  make(map[uuid.UUID]*realtimeConnection),
	}

	if events != nil {
		consumerName := "realtime.fanout." + uuid.NewString()
		hub.sub = events.Subscribe(consumerName, nil, func(ctx context.Context, event eventbus.DomainEvent) error {
			hub.dispatchDomainEvent(ctx, event)
			return nil
		})
		hub.hasSub = true
	}

	return hub
}

func (h *realtimeHub) heartbeatInterval() time.Duration {
	if h == nil || h.heartbeat <= 0 {
		return realtimeHeartbeatInterval
	}
	return h.heartbeat
}

func (h *realtimeHub) addConnection(conn *realtimeConnection) {
	if h == nil || conn == nil {
		return
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		conn.close("")
		return
	}
	h.connections[conn.id] = conn
	h.mu.Unlock()
}

func (h *realtimeHub) removeConnection(conn *realtimeConnection) {
	if h == nil || conn == nil {
		return
	}
	h.mu.Lock()
	delete(h.connections, conn.id)
	h.mu.Unlock()
	conn.close("")
}

func (h *realtimeHub) close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	connections := make([]*realtimeConnection, 0, len(h.connections))
	for _, conn := range h.connections {
		connections = append(connections, conn)
	}
	h.connections = make(map[uuid.UUID]*realtimeConnection)
	sub := h.sub
	hasSub := h.hasSub
	events := h.events
	h.mu.Unlock()

	for _, conn := range connections {
		conn.close("")
	}
	if hasSub && events != nil {
		events.Unsubscribe(sub)
	}
}

func (h *realtimeHub) enqueue(conn *realtimeConnection, frame realtimeFrame) {
	if conn == nil {
		return
	}
	select {
	case <-conn.closed:
		return
	default:
	}

	select {
	case conn.events <- frame:
	default:
		conn.close(string(realtimeFrameBufferOverflow))
	}
}

func (h *realtimeHub) dispatchDomainEvent(ctx context.Context, event eventbus.DomainEvent) {
	if h == nil {
		return
	}
	payload := decodeRealtimePayload(event.Payload)

	h.mu.RLock()
	connections := make([]*realtimeConnection, 0, len(h.connections))
	for _, conn := range h.connections {
		connections = append(connections, conn)
	}
	h.mu.RUnlock()

	for _, conn := range connections {
		if conn.principal.OrganizationID != event.OrganizationID {
			continue
		}
		scopes := conn.snapshotScopes()
		if !routeEventToScopes(event.EventType, payload, scopes) {
			continue
		}
		if !h.allowedByNotificationPreference(ctx, conn.principal, event, payload) {
			continue
		}
		h.enqueue(conn, realtimeFrame{Type: realtimeFrameEvent, Event: &event})
	}
}

func (h *realtimeHub) broadcastTyping(sender *realtimeConnection, sessionID uuid.UUID, userID uuid.UUID) {
	if h == nil || sender == nil {
		return
	}
	expiresAt := time.Now().UTC().Add(realtimeTypingTTL)

	h.mu.RLock()
	connections := make([]*realtimeConnection, 0, len(h.connections))
	for _, conn := range h.connections {
		connections = append(connections, conn)
	}
	h.mu.RUnlock()

	for _, conn := range connections {
		if conn.id == sender.id {
			continue
		}
		if conn.principal.OrganizationID != sender.principal.OrganizationID {
			continue
		}
		if !hasSessionScope(conn.snapshotScopes(), sessionID) {
			continue
		}
		h.enqueue(conn, realtimeFrame{
			Type:      realtimeFrameTyping,
			SessionID: sessionID,
			UserID:    userID,
			ExpiresAt: expiresAt,
		})
	}
}

func (h *realtimeHub) loadReplayEvents(ctx context.Context, orgID uuid.UUID, lastEventID int64) ([]eventbus.DomainEvent, error) {
	if h == nil || h.pool == nil {
		return nil, nil
	}
	events := make([]eventbus.DomainEvent, 0)
	cursor := lastEventID
	for {
		rows, err := h.pool.Query(ctx, `
			SELECT id, organization_id, seq, event_type, actor_type, actor_id, payload, created_at
			FROM domain_event
			WHERE organization_id = $1
			  AND seq > $2
			ORDER BY seq ASC
			LIMIT $3
		`, orgID, cursor, realtimeReplayBatchSize)
		if err != nil {
			return nil, fmt.Errorf("query replay events: %w", err)
		}

		batchCount := 0
		for rows.Next() {
			var event eventbus.DomainEvent
			if err := rows.Scan(
				&event.ID,
				&event.OrganizationID,
				&event.Seq,
				&event.EventType,
				&event.ActorType,
				&event.ActorID,
				&event.Payload,
				&event.CreatedAt,
			); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan replay event: %w", err)
			}
			events = append(events, event)
			cursor = event.Seq
			batchCount++
		}
		if rows.Err() != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate replay events: %w", rows.Err())
		}
		rows.Close()
		if batchCount < realtimeReplayBatchSize {
			break
		}
	}

	return events, nil
}

func (h *realtimeHub) bounds(ctx context.Context, orgID uuid.UUID) (minSeq int64, maxSeq int64, err error) {
	if h == nil || h.pool == nil {
		return 0, 0, nil
	}
	err = h.pool.QueryRow(ctx, `
		SELECT COALESCE(MIN(seq), 0), COALESCE(MAX(seq), 0)
		FROM domain_event
		WHERE organization_id = $1
	`, orgID).Scan(&minSeq, &maxSeq)
	if err != nil {
		return 0, 0, fmt.Errorf("query event bounds: %w", err)
	}
	return minSeq, maxSeq, nil
}

func (h *realtimeHub) allowedByNotificationPreference(ctx context.Context, principal middleware.Principal, event eventbus.DomainEvent, payload map[string]any) bool {
	if !strings.HasPrefix(event.EventType, "chat.") {
		return true
	}
	if principal.UserID == uuid.Nil || isAgentAPIKey(principal) {
		return true
	}

	sessionID, ok := payloadUUID(payload, "session_id")
	if !ok || sessionID == uuid.Nil {
		return true
	}

	participant, err := h.participants.GetBySessionAndActor(ctx, sessionID, "human_user", principal.UserID)
	if errors.Is(err, repo.ErrNotFound) {
		return true
	}
	if err != nil {
		h.logger.Debug("notification preference lookup failed", "error", err)
		return true
	}

	preference := strings.ToLower(strings.TrimSpace(participant.NotificationPreference))
	switch preference {
	case "", "all":
		return true
	case "none":
		return false
	case "mentions":
		content, authorID, resolved := h.resolveMessageContext(ctx, event, payload)
		if !resolved {
			return false
		}
		return mentionPreferencePasses(content, authorID, principal.UserID, mentionTokensForPrincipal(principal))
	default:
		return true
	}
}

func (h *realtimeHub) resolveMessageContext(ctx context.Context, event eventbus.DomainEvent, payload map[string]any) (content string, authorID *uuid.UUID, ok bool) {
	if strings.HasPrefix(event.EventType, "chat.message.") {
		if value, exists := payloadString(payload, "content"); exists {
			content = value
		}
		if id, exists := payloadUUID(payload, "author_id"); exists {
			authorID = &id
		}
		if content != "" || authorID != nil {
			return content, authorID, true
		}
	}

	messageID, exists := payloadUUID(payload, "message_id")
	if !exists || messageID == uuid.Nil {
		return "", nil, false
	}

	message, err := h.messages.GetByID(ctx, messageID)
	if err != nil {
		return "", nil, false
	}
	content = message.Content
	if message.AuthorID != nil {
		copyID := *message.AuthorID
		authorID = &copyID
	}
	return content, authorID, true
}

func (h *realtimeHandlers) negotiateTransport(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if _, ok := middleware.PrincipalFromContext(r.Context()); !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	preferred := "sse"
	fallback := "websocket"
	if isMobileUserAgent(r.UserAgent()) {
		preferred = "websocket"
		fallback = "sse"
	}

	baseURL := requestBaseURL(r)
	websocketURL := strings.Replace(baseURL, "https://", "wss://", 1)
	websocketURL = strings.Replace(websocketURL, "http://", "ws://", 1)

	responder.JSON(w, http.StatusOK, map[string]any{
		"preferred":     preferred,
		"fallback":      fallback,
		"websocket_url": websocketURL + "/v1/ws",
		"sse_url":       baseURL + "/v1/events/stream",
	})
}

func (h *realtimeHandlers) streamEvents(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.hub == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "realtime service unavailable")
		return
	}

	scopes, status, err := h.parseAndValidateScopes(r.Context(), principal, r.URL.Query().Get("scopes"))
	if err != nil {
		if status == http.StatusForbidden {
			responder.Error(w, status, api.ErrCodeForbidden, err.Error())
			return
		}
		responder.Error(w, status, api.ErrCodeBadRequest, err.Error())
		return
	}

	lastEventID, err := parseLastEventID(r, true)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid last-event-id")
		return
	}

	minSeq, highWatermark, err := h.hub.bounds(r.Context(), principal.OrganizationID)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to query event cursor")
		return
	}
	replay, err := h.hub.loadReplayEvents(r.Context(), principal.OrganizationID, lastEventID)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to replay events")
		return
	}
	gap := detectEventGap(lastEventID, minSeq) || detectReplayGap(lastEventID, replay)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if gap {
		w.Header().Set("X-Events-Gap", "true")
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "streaming unsupported")
		return
	}

	conn := newRealtimeConnection(principal, scopes, "sse")
	h.hub.addConnection(conn)
	defer h.hub.removeConnection(conn)

	connectedPayload := map[string]any{"high_watermark": highWatermark}
	if gap {
		connectedPayload["gap"] = true
	}
	if err := writeSSEEvent(w, "", "connected", connectedPayload); err != nil {
		h.logClientDisconnect("sse", err)
		return
	}
	flusher.Flush()
	for _, event := range replay {
		payload := decodeRealtimePayload(event.Payload)
		if !routeEventToScopes(event.EventType, payload, conn.snapshotScopes()) {
			continue
		}
		if !h.hub.allowedByNotificationPreference(r.Context(), principal, event, payload) {
			continue
		}
		if err := writeSSEDomainEvent(w, event); err != nil {
			h.logClientDisconnect("sse", err)
			return
		}
		flusher.Flush()
	}

	heartbeatTicker := time.NewTicker(h.hub.heartbeatInterval())
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			h.logger.Debug("sse client disconnected")
			conn.close("")
			return
		case <-conn.closed:
			if conn.reason() == string(realtimeFrameBufferOverflow) {
				_ = writeSSEEvent(w, "", string(realtimeFrameBufferOverflow), map[string]any{"reason": "buffer_overflow"})
				flusher.Flush()
			}
			return
		case <-heartbeatTicker.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				h.logClientDisconnect("sse", err)
				return
			}
			flusher.Flush()
		case frame := <-conn.events:
			switch frame.Type {
			case realtimeFrameEvent:
				if frame.Event == nil {
					continue
				}
				if err := writeSSEDomainEvent(w, *frame.Event); err != nil {
					h.logClientDisconnect("sse", err)
					return
				}
			case realtimeFrameTyping:
				payload := map[string]any{
					"type":       "typing",
					"session_id": frame.SessionID,
					"user_id":    frame.UserID,
					"expires_at": frame.ExpiresAt.UTC().Format(time.RFC3339Nano),
				}
				if err := writeSSEEvent(w, "", "typing", payload); err != nil {
					h.logClientDisconnect("sse", err)
					return
				}
			case realtimeFrameBufferOverflow:
				_ = writeSSEEvent(w, "", string(realtimeFrameBufferOverflow), map[string]any{"reason": "buffer_overflow"})
				flusher.Flush()
				return
			}
			flusher.Flush()
		}
	}
}

func (h *realtimeHandlers) websocket(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.hub == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "realtime service unavailable")
		return
	}

	scopes, status, err := h.parseAndValidateScopes(r.Context(), principal, r.URL.Query().Get("scopes"))
	if err != nil {
		if status == http.StatusForbidden {
			responder.Error(w, status, api.ErrCodeForbidden, err.Error())
			return
		}
		responder.Error(w, status, api.ErrCodeBadRequest, err.Error())
		return
	}

	lastEventID, err := parseLastEventID(r, false)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid last-event-id")
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool {
			return true
		},
	}

	wsConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer wsConn.Close()

	minSeq, highWatermark, err := h.hub.bounds(r.Context(), principal.OrganizationID)
	if err != nil {
		_ = wsConn.WriteJSON(map[string]any{"type": "error", "error": "failed to query event cursor"})
		return
	}
	replay, err := h.hub.loadReplayEvents(r.Context(), principal.OrganizationID, lastEventID)
	if err != nil {
		_ = wsConn.WriteJSON(map[string]any{"type": "error", "error": "failed to replay events"})
		return
	}
	gap := detectEventGap(lastEventID, minSeq) || detectReplayGap(lastEventID, replay)

	conn := newRealtimeConnection(principal, scopes, "ws")
	h.hub.addConnection(conn)
	defer h.hub.removeConnection(conn)

	if err := wsConn.WriteJSON(map[string]any{
		"type":           "connected",
		"high_watermark": highWatermark,
		"gap":            gap,
	}); err != nil {
		return
	}

	for _, event := range replay {
		payload := decodeRealtimePayload(event.Payload)
		if !routeEventToScopes(event.EventType, payload, conn.snapshotScopes()) {
			continue
		}
		if !h.hub.allowedByNotificationPreference(r.Context(), principal, event, payload) {
			continue
		}
		if err := wsConn.WriteJSON(domainEventFrame(event)); err != nil {
			h.logClientDisconnect("websocket", err)
			return
		}
	}

	writeErrCh := make(chan error, 1)
	go func() {
		for {
			select {
			case <-conn.closed:
				if conn.reason() == string(realtimeFrameBufferOverflow) {
					_ = wsConn.WriteJSON(map[string]any{"type": "buffer_overflow"})
				}
				writeErrCh <- nil
				return
			case frame := <-conn.events:
				switch frame.Type {
				case realtimeFrameEvent:
					if frame.Event == nil {
						continue
					}
					if err := wsConn.WriteJSON(domainEventFrame(*frame.Event)); err != nil {
						writeErrCh <- err
						return
					}
				case realtimeFrameTyping:
					if err := wsConn.WriteJSON(map[string]any{
						"type":       "typing",
						"session_id": frame.SessionID,
						"user_id":    frame.UserID,
						"expires_at": frame.ExpiresAt.UTC().Format(time.RFC3339Nano),
					}); err != nil {
						writeErrCh <- err
						return
					}
				case realtimeFramePong:
					if err := wsConn.WriteJSON(map[string]any{"type": "pong"}); err != nil {
						writeErrCh <- err
						return
					}
				case realtimeFrameError:
					if err := wsConn.WriteJSON(map[string]any{"type": "error", "error": frame.Message}); err != nil {
						writeErrCh <- err
						return
					}
				case realtimeFrameBufferOverflow:
					if err := wsConn.WriteJSON(map[string]any{"type": "buffer_overflow"}); err != nil {
						writeErrCh <- err
						return
					}
					writeErrCh <- nil
					return
				}
			}
		}
	}()

	for {
		select {
		case err := <-writeErrCh:
			if err != nil {
				h.logClientDisconnect("websocket", err)
			}
			conn.close("")
			return
		default:
		}

		_, payload, err := wsConn.ReadMessage()
		if err != nil {
			h.logClientDisconnect("websocket", err)
			conn.close("")
			return
		}

		var msg websocketClientMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			h.hub.enqueue(conn, realtimeFrame{Type: realtimeFrameError, Message: "invalid message"})
			continue
		}
		switch strings.ToLower(strings.TrimSpace(msg.Type)) {
		case "subscribe":
			next, parseErr := parseScopeSpecs(strings.Join(msg.Scopes, ","))
			if parseErr != nil {
				h.hub.enqueue(conn, realtimeFrame{Type: realtimeFrameError, Message: "invalid scope"})
				continue
			}
			if err := h.validateScopes(r.Context(), principal, next); err != nil {
				h.hub.enqueue(conn, realtimeFrame{Type: realtimeFrameError, Message: err.Error()})
				continue
			}
			conn.addScopes(next)
		case "unsubscribe":
			next, parseErr := parseScopeSpecs(strings.Join(msg.Scopes, ","))
			if parseErr != nil {
				h.hub.enqueue(conn, realtimeFrame{Type: realtimeFrameError, Message: "invalid scope"})
				continue
			}
			conn.removeScopes(next)
		case "typing":
			sessionID, parseErr := uuid.Parse(strings.TrimSpace(msg.SessionID))
			if parseErr != nil || sessionID == uuid.Nil {
				h.hub.enqueue(conn, realtimeFrame{Type: realtimeFrameError, Message: "invalid session_id"})
				continue
			}
			if !hasSessionScope(conn.snapshotScopes(), sessionID) {
				h.hub.enqueue(conn, realtimeFrame{Type: realtimeFrameError, Message: "session scope required for typing"})
				continue
			}
			h.hub.broadcastTyping(conn, sessionID, principal.UserID)
		case "ping":
			h.hub.enqueue(conn, realtimeFrame{Type: realtimeFramePong})
		default:
			h.hub.enqueue(conn, realtimeFrame{Type: realtimeFrameError, Message: "unsupported message type"})
		}
	}
}

type websocketClientMessage struct {
	Type      string   `json:"type"`
	Scopes    []string `json:"scopes"`
	SessionID string   `json:"session_id"`
}

func (h *realtimeHandlers) parseAndValidateScopes(ctx context.Context, principal middleware.Principal, raw string) ([]realtimeScope, int, error) {
	scopes, err := parseScopeSpecs(raw)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if err := h.validateScopes(ctx, principal, scopes); err != nil {
		if errors.Is(err, errScopeForbidden) {
			return nil, http.StatusForbidden, err
		}
		return nil, http.StatusBadRequest, err
	}
	return scopes, http.StatusOK, nil
}

var errScopeForbidden = errors.New("scope forbidden")

func (h *realtimeHandlers) validateScopes(ctx context.Context, principal middleware.Principal, scopes []realtimeScope) error {
	for _, scope := range scopes {
		switch scope.Kind {
		case "org":
			if isAgentAPIKey(principal) || !isOrgAdminRole(principal.Role) {
				return fmt.Errorf("%w: org scope requires org-admin", errScopeForbidden)
			}
		case "session":
			session, err := h.chatSessions.GetByID(ctx, scope.ID)
			if err != nil || session.OrganizationID != principal.OrganizationID {
				return fmt.Errorf("invalid scope")
			}
		case "project":
			project, err := h.projects.GetByID(ctx, scope.ID)
			if err != nil || project.OrganizationID != principal.OrganizationID {
				return fmt.Errorf("invalid scope")
			}
		case "task":
			taskRecord, err := h.tasks.GetByID(ctx, scope.ID)
			if err != nil || taskRecord.OrganizationID != principal.OrganizationID {
				return fmt.Errorf("invalid scope")
			}
		default:
			return fmt.Errorf("invalid scope")
		}
	}
	return nil
}

func parseScopeSpecs(raw string) ([]realtimeScope, error) {
	parts := strings.Split(strings.TrimSpace(raw), ",")
	if len(parts) == 0 {
		return nil, fmt.Errorf("scopes are required")
	}
	seen := make(map[realtimeScope]struct{})
	scopes := make([]realtimeScope, 0, len(parts))
	for _, part := range parts {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}
		if strings.EqualFold(token, "org") {
			scope := realtimeScope{Kind: "org"}
			if _, exists := seen[scope]; !exists {
				scopes = append(scopes, scope)
				seen[scope] = struct{}{}
			}
			continue
		}

		segments := strings.SplitN(token, ":", 2)
		if len(segments) != 2 {
			return nil, fmt.Errorf("invalid scope %q", token)
		}
		kind := strings.ToLower(strings.TrimSpace(segments[0]))
		id, err := uuid.Parse(strings.TrimSpace(segments[1]))
		if err != nil {
			return nil, fmt.Errorf("invalid scope %q", token)
		}
		scope := realtimeScope{Kind: kind, ID: id}
		if _, exists := seen[scope]; exists {
			continue
		}
		scopes = append(scopes, scope)
		seen[scope] = struct{}{}
	}
	if len(scopes) == 0 {
		return nil, fmt.Errorf("scopes are required")
	}
	return scopes, nil
}

func parseLastEventID(r *http.Request, allowHeader bool) (int64, error) {
	candidate := strings.TrimSpace(r.URL.Query().Get("last-event-id"))
	if candidate == "" && allowHeader {
		candidate = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	if candidate == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(candidate, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid last-event-id")
	}
	return value, nil
}

func detectEventGap(lastEventID int64, minSeq int64) bool {
	if lastEventID <= 0 || minSeq <= 0 {
		return false
	}
	return lastEventID+1 < minSeq
}

func detectReplayGap(lastEventID int64, events []eventbus.DomainEvent) bool {
	if lastEventID <= 0 || len(events) == 0 {
		return false
	}
	previous := lastEventID
	for _, event := range events {
		if event.Seq-previous > 1 {
			return true
		}
		previous = event.Seq
	}
	return false
}

func (h *realtimeHandlers) logClientDisconnect(transport string, err error) {
	if h == nil || h.logger == nil {
		return
	}
	if err == nil {
		h.logger.Debug(transport + " client disconnected")
		return
	}
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		h.logger.Debug(transport+" client disconnected", "error", err)
		return
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(message, "broken pipe") || strings.Contains(message, "connection reset by peer") || strings.Contains(message, "close sent") {
		h.logger.Debug(transport+" client disconnected", "error", err)
		return
	}
	h.logger.Debug(transport+" stream closed", "error", err)
}

func routeEventToScopes(eventType string, payload map[string]any, scopes map[realtimeScope]struct{}) bool {
	if len(scopes) == 0 {
		return false
	}
	if _, ok := scopes[realtimeScope{Kind: "org"}]; ok {
		return true
	}

	sessionID, hasSession := payloadUUID(payload, "session_id")
	projectID, hasProject := payloadUUID(payload, "project_id")
	taskID, hasTask := payloadUUID(payload, "task_id")

	for scope := range scopes {
		switch scope.Kind {
		case "session":
			if strings.HasPrefix(eventType, "chat.") && hasSession && scope.ID == sessionID {
				return true
			}
		case "project":
			if (strings.HasPrefix(eventType, "project.") || strings.HasPrefix(eventType, "task.")) && hasProject && scope.ID == projectID {
				return true
			}
		case "task":
			if strings.HasPrefix(eventType, "task.") && hasTask && scope.ID == taskID {
				return true
			}
		}
	}
	return false
}

func hasSessionScope(scopes map[realtimeScope]struct{}, sessionID uuid.UUID) bool {
	if _, ok := scopes[realtimeScope{Kind: "org"}]; ok {
		return true
	}
	_, ok := scopes[realtimeScope{Kind: "session", ID: sessionID}]
	return ok
}

func payloadUUID(payload map[string]any, key string) (uuid.UUID, bool) {
	value, ok := payload[key]
	if !ok {
		return uuid.Nil, false
	}
	switch typed := value.(type) {
	case string:
		id, err := uuid.Parse(strings.TrimSpace(typed))
		if err != nil {
			return uuid.Nil, false
		}
		return id, true
	case uuid.UUID:
		if typed == uuid.Nil {
			return uuid.Nil, false
		}
		return typed, true
	default:
		return uuid.Nil, false
	}
}

func payloadString(payload map[string]any, key string) (string, bool) {
	value, ok := payload[key]
	if !ok {
		return "", false
	}
	str, ok := value.(string)
	if !ok {
		return "", false
	}
	return str, true
}

func decodeRealtimePayload(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	decoded := make(map[string]any)
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return map[string]any{}
	}
	return decoded
}

func mentionPreferencePasses(content string, authorID *uuid.UUID, userID uuid.UUID, tokens []string) bool {
	if authorID != nil && *authorID == userID {
		return true
	}
	lower := strings.ToLower(content)
	for _, token := range tokens {
		t := strings.TrimSpace(strings.ToLower(token))
		if t == "" {
			continue
		}
		if strings.Contains(lower, "@"+t) {
			return true
		}
	}
	return false
}

func mentionTokensForPrincipal(principal middleware.Principal) []string {
	set := make(map[string]struct{})
	add := func(value string) {
		trimmed := strings.TrimSpace(strings.ToLower(value))
		if trimmed == "" {
			return
		}
		set[trimmed] = struct{}{}
	}

	if principal.DisplayName != "" {
		add(principal.DisplayName)
		add(strings.ReplaceAll(principal.DisplayName, " ", "_"))
		add(strings.ReplaceAll(principal.DisplayName, " ", ""))
	}
	if principal.Email != "" {
		parts := strings.SplitN(strings.ToLower(principal.Email), "@", 2)
		if len(parts) > 0 {
			add(parts[0])
		}
	}

	out := make([]string, 0, len(set))
	for token := range set {
		out = append(out, token)
	}
	return out
}

func isOrgAdminRole(role string) bool {
	normalized := strings.ToLower(strings.TrimSpace(role))
	return normalized == "owner" || normalized == "admin"
}

func writeSSEEvent(w io.Writer, id, event string, data any) error {
	if strings.TrimSpace(id) != "" {
		if _, err := io.WriteString(w, "id: "+id+"\n"); err != nil {
			return err
		}
	}
	if strings.TrimSpace(event) != "" {
		if _, err := io.WriteString(w, "event: "+event+"\n"); err != nil {
			return err
		}
	}
	if data != nil {
		encoded, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, "data: "+string(encoded)+"\n"); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func writeSSEDomainEvent(w io.Writer, event eventbus.DomainEvent) error {
	return writeSSEEvent(w, strconv.FormatInt(event.Seq, 10), event.EventType, map[string]any{
		"seq":         event.Seq,
		"event_id":    event.ID.String(),
		"org_id":      event.OrganizationID.String(),
		"event_type":  event.EventType,
		"payload":     json.RawMessage(event.Payload),
		"occurred_at": event.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
}

func domainEventFrame(event eventbus.DomainEvent) map[string]any {
	return map[string]any{
		"id":          event.Seq,
		"event_type":  event.EventType,
		"payload":     json.RawMessage(event.Payload),
		"occurred_at": event.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func readSSEFrame(reader *bufio.Reader) (eventName string, id string, data string, err error) {
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			if errors.Is(readErr, io.EOF) && line == "" {
				return "", "", "", io.EOF
			}
			if line == "" {
				return "", "", "", readErr
			}
		}

		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, ":") {
			if readErr != nil {
				return "", "", "", readErr
			}
			continue
		}
		if line == "" {
			if eventName != "" || id != "" || data != "" {
				return eventName, id, data, nil
			}
			if readErr != nil {
				return "", "", "", readErr
			}
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			if readErr != nil {
				return "", "", "", readErr
			}
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		switch key {
		case "event":
			eventName = value
		case "id":
			id = value
		case "data":
			data = value
		}

		if readErr != nil {
			return eventName, id, data, readErr
		}
	}
}
