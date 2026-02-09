package ws

import (
	"context"
	"encoding/json"
	"log"
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

var subscriptionOrgPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var subscriptionTopicPattern = regexp.MustCompile(`^[A-Za-z0-9:_-]+$`)

type issueSubscriptionAuthorizer interface {
	CanSubscribeIssue(ctx context.Context, orgID, issueID string) (bool, error)
}

// Handler upgrades HTTP connections to websocket clients.
type Handler struct {
	Hub             *Hub
	IssueAuthorizer issueSubscriptionAuthorizer
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := NewClient(h.Hub, conn)
	h.Hub.register <- client

	go client.WritePump()
	client.ReadPump(r.Context(), h.IssueAuthorizer)
}

type clientMessage struct {
	Type    string `json:"type"`
	OrgID   string `json:"org_id"`
	Topic   string `json:"topic,omitempty"`
	Channel string `json:"channel,omitempty"`
}

// ReadPump pumps messages from the websocket connection.
func (c *Client) ReadPump(clientCtx context.Context, issueAuthorizer issueSubscriptionAuthorizer) {
	defer func() {
		c.Hub.unregister <- c
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
		processClientMessage(clientCtx, c, payload, issueAuthorizer)
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

func processClientMessage(
	ctx context.Context,
	client *Client,
	payload clientMessage,
	issueAuthorizer issueSubscriptionAuthorizer,
) {
	if client == nil {
		return
	}

	if orgID := strings.TrimSpace(payload.OrgID); orgID != "" && isAllowedSubscriptionOrgID(orgID) {
		client.SetOrgID(orgID)
	}

	topic := strings.TrimSpace(payload.Topic)
	if topic == "" {
		topic = strings.TrimSpace(payload.Channel)
	}
	if topic != "" && !isAllowedSubscriptionTopic(topic) {
		return
	}

	switch strings.ToLower(strings.TrimSpace(payload.Type)) {
	case "subscribe":
		if topic == "" {
			return
		}
		orgID := strings.TrimSpace(client.OrgID())
		if orgID == "" {
			return
		}
		if issueID, ok := issueIDFromTopic(topic); ok && issueAuthorizer != nil {
			authorizerCtx := ctx
			if authorizerCtx == nil {
				authorizerCtx = context.Background()
			}
			allowed, err := issueAuthorizer.CanSubscribeIssue(authorizerCtx, orgID, issueID)
			if err != nil {
				log.Printf("warning: issue subscription authorization error: org_id=%s issue_id=%s err=%v", orgID, issueID, err)
				return
			}
			if !allowed {
				return
			}
		}
		client.SubscribeTopic(topic)
	case "unsubscribe":
		if topic == "" {
			return
		}
		client.UnsubscribeTopic(topic)
	}
}

func issueIDFromTopic(topic string) (string, bool) {
	if !strings.HasPrefix(topic, "issue:") {
		return "", false
	}
	issueID := strings.TrimSpace(strings.TrimPrefix(topic, "issue:"))
	if issueID == "" {
		return "", false
	}
	return issueID, true
}

func isAllowedSubscriptionOrgID(orgID string) bool {
	orgID = strings.TrimSpace(strings.ToLower(orgID))
	if orgID == "demo" || orgID == "default" {
		return true
	}
	return subscriptionOrgPattern.MatchString(orgID)
}

func isAllowedSubscriptionTopic(topic string) bool {
	topic = strings.TrimSpace(topic)
	if topic == "" || len(topic) > 200 {
		return false
	}
	return subscriptionTopicPattern.MatchString(topic)
}

func isWebSocketOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}

	originURL, err := url.Parse(origin)
	if err != nil {
		return false
	}
	originHost := normalizeOriginHost(originURL.Host)
	if originHost == "" {
		return false
	}

	reqHost := normalizeOriginHost(r.Host)
	if reqHost == originHost || isLoopbackAliasPair(reqHost, originHost) {
		return true
	}

	allowList := strings.Split(strings.TrimSpace(os.Getenv("WS_ALLOWED_ORIGINS")), ",")
	for _, candidate := range allowList {
		if isAllowedOriginCandidate(originURL, candidate) {
			return true
		}
	}
	return false
}

func normalizeOriginHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "[") && strings.Contains(host, "]") {
		if parsedHost, _, err := net.SplitHostPort(host); err == nil {
			return strings.Trim(parsedHost, "[]")
		}
		return strings.Trim(host, "[]")
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		return parsedHost
	}
	return host
}

func isLoopbackAliasPair(a, b string) bool {
	loopback := map[string]bool{
		"localhost": true,
		"127.0.0.1": true,
		"::1":       true,
	}
	return loopback[a] && loopback[b]
}

func isAllowedOriginCandidate(originURL *url.URL, candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return false
	}
	if candidate == "*" {
		return true
	}

	parsedCandidate, err := url.Parse(candidate)
	if err != nil {
		return false
	}

	if parsedCandidate.Scheme != "" && parsedCandidate.Scheme != originURL.Scheme {
		return false
	}
	patternHost := normalizeOriginHost(parsedCandidate.Host)
	if patternHost == "" {
		return false
	}

	actualHost := normalizeOriginHost(originURL.Host)
	if strings.HasPrefix(patternHost, "*.") {
		suffix := strings.TrimPrefix(patternHost, "*.")
		if actualHost == suffix {
			return false
		}
		return strings.HasSuffix(actualHost, "."+suffix)
	}
	return actualHost == patternHost
}
