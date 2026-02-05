package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// OpenClawHandler manages WebSocket connections from OpenClaw bridge.
// Unlike browser clients, OpenClaw is the source of truth for agent data.
type OpenClawHandler struct {
	Hub        *Hub
	conn       *websocket.Conn
	mu         sync.RWMutex
	authSecret string
}

// NewOpenClawHandler creates a handler for OpenClaw connections.
func NewOpenClawHandler(hub *Hub) *OpenClawHandler {
	secret := os.Getenv("OPENCLAW_WS_SECRET")
	return &OpenClawHandler{
		Hub:        hub,
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

	if token != h.authSecret {
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
		conn.WriteMessage(websocket.TextMessage, data)
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
		conn.Close()
		log.Printf("[openclaw-ws] OpenClaw disconnected")
	}()

	conn.SetReadLimit(maxMessageSize * 10) // Allow larger messages from OpenClaw
	conn.SetReadDeadline(time.Now().Add(pongWait * 2))
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
		return nil // No connection, silently ignore
	}

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	return conn.WriteMessage(websocket.TextMessage, data)
}

// IsConnected returns true if OpenClaw is currently connected.
func (h *OpenClawHandler) IsConnected() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.conn != nil
}
