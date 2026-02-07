package ws

import (
	"sync"

	"github.com/gorilla/websocket"
)

// MessageType represents the type of a hub payload.
type MessageType string

const (
	MessageTaskCreated       MessageType = "TaskCreated"
	MessageTaskUpdated       MessageType = "TaskUpdated"
	MessageTaskStatusChanged MessageType = "TaskStatusChanged"
	MessageCommentAdded      MessageType = "CommentAdded"
	MessageGitPush                    MessageType = "GitPush"
	MessageIssueReviewAddressed       MessageType = "IssueReviewAddressed"
	MessageIssueReviewSaved           MessageType = "IssueReviewSaved"
	MessageIssueCommentCreated        MessageType = "IssueCommentCreated"
	MessageProjectChatMessageCreated  MessageType = "ProjectChatMessageCreated"
)

// BroadcastMessage packages a payload for an org-scoped broadcast.
type BroadcastMessage struct {
	OrgID   string
	Payload []byte
}

// Hub manages active clients and org-scoped broadcasts.
type Hub struct {
	clients    map[*Client]bool
	register   chan *Client
	unregister chan *Client
	broadcast  chan BroadcastMessage
}

// NewHub builds a new Hub.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan BroadcastMessage),
	}
}

// Run starts the hub loop.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.Send)
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				if client.OrgID() != message.OrgID {
					continue
				}
				select {
				case client.Send <- message.Payload:
				default:
					delete(h.clients, client)
					close(client.Send)
				}
			}
		}
	}
}

// Broadcast sends a payload to all clients in an org.
func (h *Hub) Broadcast(orgID string, payload []byte) {
	h.broadcast <- BroadcastMessage{OrgID: orgID, Payload: payload}
}

// BroadcastTopic sends a typed message to all clients in an org.
func (h *Hub) BroadcastTopic(orgID string, topic string, payload []byte) {
	h.broadcast <- BroadcastMessage{OrgID: orgID, Payload: payload}
}

// Register adds a client to the hub.
func (h *Hub) Register(c *Client) {
	h.register <- c
}

// Unregister removes a client from the hub.
func (h *Hub) Unregister(c *Client) {
	h.unregister <- c
}

// Client represents a websocket connection.
type Client struct {
	Conn  *websocket.Conn
	Hub   *Hub
	Send  chan []byte
	mu    sync.RWMutex
	orgID string
}

// NewClient returns a client ready for registration.
func NewClient(hub *Hub, conn *websocket.Conn) *Client {
	return &Client{
		Conn: conn,
		Hub:  hub,
		Send: make(chan []byte, 256),
	}
}

// OrgID returns the current org id.
func (c *Client) OrgID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.orgID
}

// SubscribeTopic is a no-op placeholder for topic-based subscriptions.
func (c *Client) SubscribeTopic(topic string) {}

// SetOrgID updates the org id for the client.
func (c *Client) SetOrgID(orgID string) {
	c.mu.Lock()
	c.orgID = orgID
	c.mu.Unlock()
}
