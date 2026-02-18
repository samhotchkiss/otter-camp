package ws

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// OpenClawHandler manages WebSocket connections from OpenClaw bridge.
// Unlike browser clients, OpenClaw is the source of truth for agent data.
type OpenClawHandler struct {
	Hub        *Hub
	conn       *websocket.Conn
	mu         sync.RWMutex
	writeMu    sync.Mutex
	requestMu  sync.Mutex
	requestSeq uint64
	requests   map[string]chan openClawRequestResult
	authSecret string
}

var ErrOpenClawNotConnected = errors.New("openclaw bridge not connected")

const openClawConnectionWait = 2 * time.Second

type openClawRequestResult struct {
	data json.RawMessage
	err  error
}

// NewOpenClawHandler creates a handler for OpenClaw connections.
func NewOpenClawHandler(hub *Hub) *OpenClawHandler {
	secret := strings.TrimSpace(os.Getenv("OPENCLAW_WS_SECRET"))
	return &OpenClawHandler{
		Hub:        hub,
		requests:   make(map[string]chan openClawRequestResult),
		authSecret: secret,
	}
}

// OpenClawEvent represents an event from OpenClaw.
type OpenClawEvent struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	OrgID     string          `json:"org_id"`
	Data      json.RawMessage `json:"data"`
}

// AgentStatusEvent is sent when an agent's status changes.
type AgentStatusEvent struct {
	AgentID     string    `json:"agent_id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"` // online, busy, offline
	CurrentTask string    `json:"current_task,omitempty"`
	LastActive  time.Time `json:"last_active"`
	Model       string    `json:"model,omitempty"`
	Tokens      int       `json:"tokens,omitempty"`
}

// FeedItemEvent is sent when a new feed item arrives.
type FeedItemEvent struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"` // message, approval, alert, etc.
	Agent     string    `json:"agent"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	Priority  string    `json:"priority,omitempty"` // normal, urgent
}

// ServeHTTP handles WebSocket upgrade for OpenClaw connections.
func (h *OpenClawHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.authSecret == "" {
		log.Printf("[openclaw-ws] OPENCLAW_WS_SECRET is not configured")
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	// Validate auth token
	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.Header.Get("X-OpenClaw-Token")
	}

	token = strings.TrimSpace(token)
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(h.authSecret)) != 1 {
		log.Printf("[openclaw-ws] Auth failed: invalid token")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Upgrade connection
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[openclaw-ws] Upgrade failed: %v", err)
		return
	}

	h.mu.Lock()
	h.conn = conn
	h.mu.Unlock()

	log.Printf("[openclaw-ws] OpenClaw connected from %s", r.RemoteAddr)

	// Send welcome message
	welcome := map[string]interface{}{
		"type":      "connected",
		"timestamp": time.Now(),
		"message":   "OtterCamp API connected",
	}
	if data, err := json.Marshal(welcome); err == nil {
		h.writeMu.Lock()
		_ = conn.WriteMessage(websocket.TextMessage, data)
		h.writeMu.Unlock()
	}

	// Start read pump
	go h.readPump(conn)
}

// readPump handles incoming messages from OpenClaw.
func (h *OpenClawHandler) readPump(conn *websocket.Conn) {
	defer func() {
		h.mu.Lock()
		if h.conn == conn {
			h.conn = nil
		}
		h.mu.Unlock()
		h.failPendingRequests(ErrOpenClawNotConnected)
		conn.Close()
		log.Printf("[openclaw-ws] OpenClaw disconnected")
	}()

	conn.SetReadLimit(maxMessageSize * 10) // Allow larger messages from OpenClaw
	conn.SetReadDeadline(time.Now().Add(pongWait * 2))
	defaultPingHandler := conn.PingHandler()
	conn.SetPingHandler(func(appData string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongWait * 2))
		if defaultPingHandler != nil {
			return defaultPingHandler(appData)
		}
		return nil
	})
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait * 2))
		return nil
	})

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[openclaw-ws] Read error: %v", err)
			}
			break
		}

		// Parse event
		var event OpenClawEvent
		if err := json.Unmarshal(message, &event); err != nil {
			log.Printf("[openclaw-ws] Parse error: %v", err)
			continue
		}

		// Handle event types
		h.handleEvent(event)
	}
}

// handleEvent processes an event from OpenClaw.
func (h *OpenClawHandler) handleEvent(event OpenClawEvent) {
	if h.tryResolveRequest(event) {
		return
	}

	log.Printf("[openclaw-ws] Event: %s (org: %s)", event.Type, event.OrgID)

	// Re-broadcast to browser clients
	payload, err := json.Marshal(map[string]interface{}{
		"type":      event.Type,
		"timestamp": event.Timestamp,
		"data":      event.Data,
	})
	if err != nil {
		log.Printf("[openclaw-ws] Marshal error: %v", err)
		return
	}

	// If org_id is specified, broadcast to that org only
	// Otherwise broadcast to all (for demo mode)
	orgID := event.OrgID
	if orgID == "" {
		orgID = "demo" // Default org for demo mode
	}

	h.Hub.Broadcast(orgID, payload)
}

// SendToOpenClaw sends a message back to OpenClaw (for approvals, commands, etc.)
func (h *OpenClawHandler) SendToOpenClaw(event interface{}) error {
	h.mu.RLock()
	conn := h.conn
	h.mu.RUnlock()

	if conn == nil {
		return ErrOpenClawNotConnected
	}

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	return conn.WriteMessage(websocket.TextMessage, data)
}

// IsConnected returns true if OpenClaw is currently connected.
func (h *OpenClawHandler) IsConnected() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.conn != nil
}

func (h *OpenClawHandler) Request(
	ctx context.Context,
	eventType, orgID string,
	data map[string]any,
) (json.RawMessage, error) {
	if h == nil {
		return nil, errors.New("openclaw handler is nil")
	}
	requestType := strings.TrimSpace(eventType)
	if requestType == "" {
		return nil, errors.New("event type is required")
	}

	requestID := h.nextRequestID()
	payloadMap := make(map[string]any, len(data)+1)
	for key, value := range data {
		payloadMap[key] = value
	}
	payloadMap["request_id"] = requestID

	payload, err := json.Marshal(payloadMap)
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := h.waitForConnection(ctx); err != nil {
			return nil, err
		}

		resultCh := make(chan openClawRequestResult, 1)
		h.requestMu.Lock()
		h.requests[requestID] = resultCh
		h.requestMu.Unlock()

		sendErr := h.SendToOpenClaw(OpenClawEvent{
			Type:      requestType,
			Timestamp: time.Now().UTC(),
			OrgID:     strings.TrimSpace(orgID),
			Data:      payload,
		})
		if sendErr != nil {
			h.removeRequest(requestID)
			if errors.Is(sendErr, ErrOpenClawNotConnected) && attempt == 0 {
				continue
			}
			return nil, sendErr
		}

		select {
		case <-ctx.Done():
			h.removeRequest(requestID)
			return nil, ctx.Err()
		case result := <-resultCh:
			h.removeRequest(requestID)
			if result.err != nil {
				if errors.Is(result.err, ErrOpenClawNotConnected) && attempt == 0 {
					continue
				}
				return nil, result.err
			}
			return append(json.RawMessage(nil), result.data...), nil
		}
	}
	return nil, ErrOpenClawNotConnected
}

func (h *OpenClawHandler) waitForConnection(ctx context.Context) error {
	if h == nil {
		return errors.New("openclaw handler is nil")
	}
	if h.IsConnected() {
		return nil
	}

	timeout := time.NewTimer(openClawConnectionWait)
	defer timeout.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return ErrOpenClawNotConnected
		case <-ticker.C:
			if h.IsConnected() {
				return nil
			}
		}
	}
}

func (h *OpenClawHandler) nextRequestID() string {
	seq := atomic.AddUint64(&h.requestSeq, 1)
	return "bridge-req-" + strconv.FormatUint(seq, 10)
}

func (h *OpenClawHandler) removeRequest(requestID string) {
	h.requestMu.Lock()
	delete(h.requests, requestID)
	h.requestMu.Unlock()
}

func (h *OpenClawHandler) tryResolveRequest(event OpenClawEvent) bool {
	var envelope struct {
		RequestID string          `json:"request_id"`
		OK        *bool           `json:"ok"`
		Error     string          `json:"error"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(event.Data, &envelope); err != nil {
		return false
	}
	requestID := strings.TrimSpace(envelope.RequestID)
	if requestID == "" {
		return false
	}

	h.requestMu.Lock()
	resultCh, ok := h.requests[requestID]
	if ok {
		delete(h.requests, requestID)
	}
	h.requestMu.Unlock()
	if !ok {
		return false
	}

	result := openClawRequestResult{
		data: append(json.RawMessage(nil), envelope.Data...),
	}
	if len(result.data) == 0 {
		result.data = append(json.RawMessage(nil), event.Data...)
	}
	if envelope.OK != nil && !*envelope.OK {
		message := strings.TrimSpace(envelope.Error)
		if message == "" {
			message = "openclaw bridge request failed"
		}
		result.err = errors.New(message)
	}

	select {
	case resultCh <- result:
	default:
	}
	return true
}

func (h *OpenClawHandler) failPendingRequests(err error) {
	if err == nil {
		err = ErrOpenClawNotConnected
	}

	h.requestMu.Lock()
	requests := h.requests
	h.requests = make(map[string]chan openClawRequestResult)
	h.requestMu.Unlock()

	for _, resultCh := range requests {
		select {
		case resultCh <- openClawRequestResult{err: err}:
		default:
		}
	}
}
