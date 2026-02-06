package ws

import (
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

// MessageType represents the type of a hub payload.
type MessageType string

const (
	MessageTaskCreated               MessageType = "TaskCreated"
	MessageTaskUpdated               MessageType = "TaskUpdated"
	MessageTaskStatusChanged         MessageType = "TaskStatusChanged"
	MessageCommentAdded              MessageType = "CommentAdded"
	MessageProjectChatMessageCreated MessageType = "ProjectChatMessageCreated"
)

// BroadcastMessage packages a payload for an org-scoped broadcast.
type BroadcastMessage struct {
	OrgID   string
	Topic   string
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
				if message.Topic != "" && !client.IsSubscribedToTopic(message.Topic) {
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
	h.BroadcastTopic(orgID, "", payload)
}

// BroadcastTopic sends a payload to all clients in an org subscribed to a topic.
// An empty topic broadcasts to every client in the org.
func (h *Hub) BroadcastTopic(orgID, topic string, payload []byte) {
	h.broadcast <- BroadcastMessage{
		OrgID:   strings.TrimSpace(orgID),
		Topic:   strings.TrimSpace(topic),
		Payload: payload,
	}
}

// Register adds a client to the hub.
func (h *Hub) Register(client *Client) {
	if client == nil {
		return
	}
	h.register <- client
}

// Unregister removes a client from the hub.
func (h *Hub) Unregister(client *Client) {
	if client == nil {
		return
	}
	h.unregister <- client
}

// Client represents a websocket connection.
type Client struct {
	Conn   *websocket.Conn
	Hub    *Hub
	Send   chan []byte
	mu     sync.RWMutex
	orgID  string
	topics map[string]struct{}
}

// NewClient returns a client ready for registration.
func NewClient(hub *Hub, conn *websocket.Conn) *Client {
	return &Client{
		Conn:   conn,
		Hub:    hub,
		Send:   make(chan []byte, 256),
		topics: make(map[string]struct{}),
	}
}

// OrgID returns the current org id.
func (c *Client) OrgID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.orgID
}

// SetOrgID updates the org id for the client.
func (c *Client) SetOrgID(orgID string) {
	c.mu.Lock()
	c.orgID = orgID
	c.mu.Unlock()
}

// SubscribeTopic subscribes the client to a topic.
func (c *Client) SubscribeTopic(topic string) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return
	}
	c.mu.Lock()
	c.topics[topic] = struct{}{}
	c.mu.Unlock()
}

// UnsubscribeTopic removes a topic subscription for the client.
func (c *Client) UnsubscribeTopic(topic string) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return
	}
	c.mu.Lock()
	delete(c.topics, topic)
	c.mu.Unlock()
}

// IsSubscribedToTopic reports whether the client is currently subscribed to topic.
func (c *Client) IsSubscribedToTopic(topic string) bool {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return true
	}
	c.mu.RLock()
	_, ok := c.topics[topic]
	c.mu.RUnlock()
	return ok
}
