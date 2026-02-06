package ws

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     isWebSocketOriginAllowed,
}

var orgIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var topicPattern = regexp.MustCompile(`^[a-zA-Z0-9:_-]{1,160}$`)

// Handler upgrades HTTP connections to websocket clients.
type Handler struct {
	Hub *Hub
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := NewClient(h.Hub, conn)
	h.Hub.Register(client)

	go client.WritePump()
	client.ReadPump()
}

type clientMessage struct {
	Type    string `json:"type"`
	OrgID   string `json:"org_id"`
	Topic   string `json:"topic"`
	Channel string `json:"channel"`
}

// ReadPump pumps messages from the websocket connection.
func (c *Client) ReadPump() {
	defer func() {
		c.Hub.Unregister(c)
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(maxMessageSize)
	_ = c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		return c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			break
		}

		var payload clientMessage
		if err := json.Unmarshal(message, &payload); err != nil {
			continue
		}

		switch payload.Type {
		case "subscribe":
			orgID := strings.TrimSpace(payload.OrgID)
			if isAllowedSubscriptionOrgID(orgID) {
				c.SetOrgID(orgID)
			}
			topic := strings.TrimSpace(payload.Topic)
			if topic == "" {
				topic = strings.TrimSpace(payload.Channel)
			}
			if isAllowedSubscriptionTopic(topic) {
				c.SubscribeTopic(topic)
			}
		case "unsubscribe":
			topic := strings.TrimSpace(payload.Topic)
			if topic == "" {
				topic = strings.TrimSpace(payload.Channel)
			}
			if isAllowedSubscriptionTopic(topic) {
				c.UnsubscribeTopic(topic)
			}
		}
	}
}

// WritePump pumps messages from the hub to the websocket connection.
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func isAllowedSubscriptionOrgID(orgID string) bool {
	if orgID == "" {
		return false
	}
	if orgID == "demo" || orgID == "default" {
		return true
	}
	return orgIDPattern.MatchString(orgID)
}

func isAllowedSubscriptionTopic(topic string) bool {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return false
	}
	return topicPattern.MatchString(topic)
}

func isWebSocketOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// Non-browser clients may omit Origin.
		return true
	}

	parsedOrigin, err := url.Parse(origin)
	if err != nil || parsedOrigin.Host == "" {
		return false
	}

	if allowed := parseAllowedOrigins(os.Getenv("WS_ALLOWED_ORIGINS")); len(allowed) > 0 {
		for _, candidate := range allowed {
			if strings.EqualFold(origin, candidate) {
				return true
			}
		}
		return false
	}

	originHost, originPort := splitHostPort(parsedOrigin.Host, parsedOrigin.Scheme)
	requestScheme := "http"
	if r.TLS != nil {
		requestScheme = "https"
	}
	requestHost, requestPort := splitHostPort(strings.TrimSpace(r.Host), requestScheme)
	if originHost == "" || requestHost == "" {
		return false
	}
	if !strings.EqualFold(originPort, requestPort) {
		return false
	}
	if strings.EqualFold(originHost, requestHost) {
		return true
	}

	// Treat loopback aliases as equivalent when port matches.
	return isLoopbackHost(originHost) && isLoopbackHost(requestHost)
}

func parseAllowedOrigins(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	out := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		candidate := strings.TrimSpace(strings.TrimRight(part, "/"))
		if candidate != "" {
			out = append(out, candidate)
		}
	}
	return out
}

func splitHostPort(hostport, scheme string) (string, string) {
	hostport = strings.TrimSpace(hostport)
	if hostport == "" {
		return "", ""
	}

	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
		switch strings.ToLower(scheme) {
		case "http", "ws":
			port = "80"
		case "https", "wss":
			port = "443"
		default:
			port = ""
		}
	}

	host = strings.Trim(host, "[]")
	return strings.ToLower(host), port
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(strings.Trim(host, "[]")) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
